package deploy

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
		if !secrets.ValidName(name) {
			p.Errors = fmt.Sprintf("api_key_env %q can't hold a provider key: use a variable name like OPENROUTER_API_KEY, outside YARDMASTER_*", name)
			return p, nil
		}
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

	// switchyard-server reads its config only at start, so it keeps running on the
	// previous one until the restart; a failed write leaves it untouched.
	if err := writeFile(a.configPath, candidate); err != nil {
		return err
	}
	if a.startHealthy(ctx) {
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
	fail.RolledBack = a.startHealthy(ctx)
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
	if !a.startHealthy(ctx) {
		return &ApplyError{Reason: "switchyard-server didn't become healthy after the restart", Logs: tail(a.sup.Logs(), 20)}
	}
	return nil
}

// startHealthy restarts switchyard-server on the config file, showing "restarting" until
// it answers /health. It reports whether it did; if not, the state is down.
func (a *Applier) startHealthy(ctx context.Context) bool {
	a.sup.SetState(supervisor.StateRestarting)
	a.sup.Stop()
	if err := a.sup.Start(); err == nil && a.sup.WaitHealthy(ctx, healthTimeout) {
		return true
	}
	a.sup.SetState(supervisor.StateDown)
	return false
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
	names, err := a.historyFiles()
	if err != nil {
		return nil, err
	}
	out := []HistoryEntry{}
	for _, n := range slices.Backward(names) {
		id := strings.TrimSuffix(filepath.Base(n), ".toml")
		ts, _ := historyTime(id)
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
	now := time.Now()
	id := fmt.Sprintf("%d-%09d", now.Unix(), now.Nanosecond())
	writeFile(filepath.Join(a.historyDir, id+".toml"), config)
	names, _ := a.historyFiles()
	for len(names) > historyKeep {
		os.Remove(names[0])
		names = names[1:]
	}
}

// historyFiles lists the history files, oldest first.
func (a *Applier) historyFiles() ([]string, error) {
	names, err := filepath.Glob(filepath.Join(a.historyDir, "*.toml"))
	if err != nil {
		return nil, err
	}
	// Numerically, not as strings: names written before the fraction was zero-padded
	// ("<seconds>-<ns % 1000>") don't sort as text.
	key := func(n string) (int64, int64) { return historyTime(strings.TrimSuffix(filepath.Base(n), ".toml")) }
	slices.SortFunc(names, func(x, y string) int {
		xs, xf := key(x)
		ys, yf := key(y)
		return cmp.Or(cmp.Compare(xs, ys), cmp.Compare(xf, yf))
	})
	return names, nil
}

// historyTime splits a history ID, "<unix seconds>-<fraction>", into its two numbers.
func historyTime(id string) (sec, frac int64) {
	s, f, _ := strings.Cut(id, "-")
	sec, _ = strconv.ParseInt(s, 10, 64)
	frac, _ = strconv.ParseInt(f, 10, 64)
	return sec, frac
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
