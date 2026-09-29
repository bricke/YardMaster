package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"yardmaster/internal/fsutil"
	"yardmaster/internal/secrets"
	"yardmaster/internal/store"
	"yardmaster/internal/supervisor"
)

const (
	historyKeep   = 10
	healthTimeout = 15 * time.Second

	settingModel  = "deployment.model"
	settingSource = "deployment.source"

	SourceWizard = "wizard"
	SourceTOML   = "toml"
)

// Applier owns the running config: it previews, applies and rolls back. Only one
// apply runs at a time.
type Applier struct {
	mu         sync.Mutex
	db         *store.DB
	sup        *supervisor.Supervisor
	keys       *secrets.Store
	configPath string
	historyDir string

	parsedMu sync.RWMutex
	parsed   *Parsed
}

func NewApplier(db *store.DB, sup *supervisor.Supervisor, keys *secrets.Store, configPath, historyDir string) (*Applier, error) {
	if err := os.MkdirAll(historyDir, 0o700); err != nil {
		return nil, err
	}
	a := &Applier{db: db, sup: sup, keys: keys, configPath: configPath, historyDir: historyDir}
	if text, err := a.Current(); err == nil && text != "" {
		p, err := Parse(text)
		if err == nil {
			a.parsed = p
		}
	}
	return a, nil
}

// Current returns the applied config, or "" when none has been applied.
func (a *Applier) Current() (string, error) {
	b, err := os.ReadFile(a.configPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// Parsed returns the applied config's parsed form, or nil.
func (a *Applier) Parsed() *Parsed {
	a.parsedMu.RLock()
	defer a.parsedMu.RUnlock()
	return a.parsed
}

// Model returns the wizard's saved model and where the applied config came from.
func (a *Applier) Model(ctx context.Context) (*Deployment, string, error) {
	var d Deployment
	if _, err := a.db.GetSetting(ctx, settingModel, &d); err != nil {
		return nil, "", err
	}
	var source string
	if _, err := a.db.GetSetting(ctx, settingSource, &source); err != nil {
		return nil, "", err
	}
	return &d, source, nil
}

// SaveModel stores the wizard's model without applying it (a draft).
func (a *Applier) SaveModel(ctx context.Context, d *Deployment) error {
	return a.db.SetSetting(ctx, settingModel, d)
}

// Preview is what the admin sees before confirming an apply.
type Preview struct {
	TOML    string     `json:"toml"`
	Diff    []DiffLine `json:"diff"`
	Changed bool       `json:"changed"`
	// Errors from YardMaster's checks or switchyard-server --dry-run. Apply is refused
	// while there are any.
	Errors string `json:"errors,omitempty"`
	// MissingKeys lists key variables with no value yet.
	MissingKeys []string `json:"missing_keys,omitempty"`
}

// Preview validates candidate with switchyard-server --dry-run and diffs it against the
// running config.
func (a *Applier) Preview(ctx context.Context, candidate string) (*Preview, error) {
	current, err := a.Current()
	if err != nil {
		return nil, err
	}
	p := &Preview{TOML: candidate, Diff: Diff(current, candidate)}
	p.Changed = Changed(p.Diff)
	parsed, err := Parse(candidate)
	if err != nil {
		p.Errors = "The TOML can't be read: " + err.Error()
		return p, nil
	}
	for _, name := range parsed.KeyEnvNames() {
		status, err := a.keys.Status(name)
		if err != nil {
			return nil, err
		}
		if status == secrets.Missing {
			p.MissingKeys = append(p.MissingKeys, name)
		}
	}
	if len(p.MissingKeys) > 0 {
		p.Errors = "Set these API keys before applying: " + strings.Join(p.MissingKeys, ", ")
		return p, nil
	}
	if err := a.dryRun(ctx, candidate); err != nil {
		p.Errors = err.Error()
	}
	return p, nil
}

func (a *Applier) dryRun(ctx context.Context, candidate string) error {
	tmp, err := os.CreateTemp(filepath.Dir(a.configPath), "candidate-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(candidate); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	return a.sup.DryRun(ctx, tmp.Name())
}

// ApplyError explains a failed apply. RolledBack reports whether the previous config is
// running again.
type ApplyError struct {
	Reason     string   `json:"reason"`
	RolledBack bool     `json:"rolled_back"`
	Logs       []string `json:"logs,omitempty"`
}

func (e *ApplyError) Error() string { return e.Reason }

// Apply validates candidate, stops switchyard-server gracefully (in-flight requests drain),
// starts it on the new config and waits for /health. If it doesn't come up healthy, the
// previous config is restored and started again.
func (a *Applier) Apply(ctx context.Context, candidate, source string, model *Deployment) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	preview, err := a.Preview(ctx, candidate)
	if err != nil {
		return err
	}
	if preview.Errors != "" {
		return &ApplyError{Reason: preview.Errors}
	}
	previous, err := a.Current()
	if err != nil {
		return err
	}
	parsed, _ := Parse(candidate)

	a.sup.SetState(supervisor.StateRestarting)
	a.sup.Stop()
	if err := writeFile(a.configPath, candidate); err != nil {
		a.restart(ctx, previous)
		return err
	}
	if err := a.sup.Start(); err == nil && a.sup.WaitHealthy(ctx, healthTimeout) {
		a.setParsed(parsed)
		a.saveHistory(candidate)
		a.db.SetSetting(ctx, settingSource, source)
		if model != nil {
			a.db.SetSetting(ctx, settingModel, model)
		}
		return nil
	}

	// The new config didn't come up: put the old one back.
	logs := tail(a.sup.Logs(), 20)
	a.sup.Stop()
	fail := &ApplyError{Reason: "switchyard-server didn't become healthy with the new config", Logs: logs}
	if previous == "" {
		os.Remove(a.configPath)
		a.sup.SetState(supervisor.StateUnconfigured)
		return fail
	}
	if err := writeFile(a.configPath, previous); err != nil {
		fail.Reason += "; restoring the previous config also failed: " + err.Error()
		return fail
	}
	fail.RolledBack = a.restart(ctx, previous)
	if !fail.RolledBack {
		fail.Reason += "; the previous config didn't come back up either"
	}
	return fail
}

// Restart stops and starts switchyard-server on the current config, e.g. after a key
// changed (keys reach Switchyard only at start).
func (a *Applier) Restart(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := a.Current()
	if err != nil {
		return err
	}
	if current == "" {
		return errors.New("no config applied yet")
	}
	if !a.restart(ctx, current) {
		return &ApplyError{Reason: "switchyard-server didn't become healthy after the restart", Logs: tail(a.sup.Logs(), 20)}
	}
	return nil
}

func (a *Applier) restart(ctx context.Context, config string) bool {
	if config == "" {
		return false
	}
	a.sup.SetState(supervisor.StateRestarting)
	a.sup.Stop()
	if err := a.sup.Start(); err != nil {
		a.sup.SetState(supervisor.StateDown)
		return false
	}
	if !a.sup.WaitHealthy(ctx, healthTimeout) {
		a.sup.SetState(supervisor.StateDown)
		return false
	}
	return true
}

func (a *Applier) setParsed(p *Parsed) {
	a.parsedMu.Lock()
	a.parsed = p
	a.parsedMu.Unlock()
}

// HistoryEntry is one previously applied config.
type HistoryEntry struct {
	ID        string `json:"id"`
	AppliedAt int64  `json:"applied_at"`
}

// History lists applied configs, newest first.
func (a *Applier) History() ([]HistoryEntry, error) {
	names, err := filepath.Glob(filepath.Join(a.historyDir, "*.toml"))
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	out := []HistoryEntry{}
	for _, n := range names {
		id := strings.TrimSuffix(filepath.Base(n), ".toml")
		ts, _ := strconv.ParseInt(strings.SplitN(id, "-", 2)[0], 10, 64)
		out = append(out, HistoryEntry{ID: id, AppliedAt: ts})
	}
	return out, nil
}

// HistoryConfig returns one history entry's TOML.
func (a *Applier) HistoryConfig(id string) (string, error) {
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r == '-') {
			return "", errors.New("unknown history entry")
		}
	}
	b, err := os.ReadFile(filepath.Join(a.historyDir, id+".toml"))
	if err != nil {
		return "", errors.New("unknown history entry")
	}
	return string(b), nil
}

func (a *Applier) saveHistory(config string) {
	id := fmt.Sprintf("%d-%d", time.Now().Unix(), time.Now().Nanosecond()%1000)
	writeFile(filepath.Join(a.historyDir, id+".toml"), config)
	names, _ := filepath.Glob(filepath.Join(a.historyDir, "*.toml"))
	sort.Strings(names)
	for len(names) > historyKeep {
		os.Remove(names[0])
		names = names[1:]
	}
}

func writeFile(path, content string) error {
	return fsutil.WriteFileAtomic(path, []byte(content), 0o600)
}

func tail(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}
