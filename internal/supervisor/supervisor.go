// Package supervisor runs switchyard-server as a child process: start, graceful stop,
// health checks, and restarts with backoff after a crash.
package supervisor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// States shown on the Health page and in the users' status badge.
const (
	StateUnconfigured = "unconfigured" // no config applied yet
	StateStarting     = "starting"
	StateUp           = "up"
	StateRestarting   = "restarting" // a config apply or manual restart is in progress
	StateDown         = "down"       // crashed or failing health checks
	StateStopped      = "stopped"
)

// Options configure the child process.
type Options struct {
	Bin             string
	ConfigPath      string
	RoutingLogPath  string
	Port            int
	ShutdownTimeout time.Duration
	// Env returns the environment for a child running the config at path: PATH, HOME and
	// the provider keys that config names. Nothing else from YardMaster's own environment
	// is passed on.
	Env func(configPath string) ([]string, error)
	// BeforeStart runs while the child is stopped, e.g. to rotate the routing log safely.
	BeforeStart func()
}

// Status is a snapshot for the UI.
type Status struct {
	State       string    `json:"state"`
	PID         int       `json:"pid,omitempty"`
	StartedAt   time.Time `json:"started_at,omitzero"`
	Restarts    int       `json:"restarts"`
	LastExit    string    `json:"last_exit,omitempty"`
	LastHealthy time.Time `json:"last_healthy,omitzero"`
}

type Supervisor struct {
	opts   Options
	client *http.Client

	mu        sync.Mutex
	state     string
	cmd       *exec.Cmd
	exited    chan struct{} // closed when the current child exits
	wanted    bool          // whether the child should be running
	startedAt time.Time
	restarts  int
	lastExit  string
	healthyAt time.Time
	logs      *ring
}

func New(opts Options) *Supervisor {
	return &Supervisor{
		opts:   opts,
		client: &http.Client{Timeout: 2 * time.Second},
		state:  StateUnconfigured,
		logs:   newRing(300),
	}
}

// URL is where switchyard-server listens: loopback only, so nothing outside the
// container can reach it.
func (s *Supervisor) URL() string { return "http://127.0.0.1:" + strconv.Itoa(s.opts.Port) }

// Status returns a snapshot.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{State: s.state, StartedAt: s.startedAt, Restarts: s.restarts, LastExit: s.lastExit, LastHealthy: s.healthyAt}
	if s.cmd != nil && s.cmd.Process != nil {
		st.PID = s.cmd.Process.Pid
	}
	return st
}

// Logs returns the last lines switchyard-server wrote.
func (s *Supervisor) Logs() []string { return s.logs.lines() }

// SetState lets the apply flow show "restarting" for the whole apply.
func (s *Supervisor) SetState(state string) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

// Run keeps the child alive until ctx ends: it restarts it after crashes with backoff and
// checks /health every few seconds.
func (s *Supervisor) Run(ctx context.Context) {
	backoff := time.Second
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Stop()
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		wanted, running, state := s.wanted, s.cmd != nil, s.state
		s.mu.Unlock()
		if !wanted || state == StateRestarting {
			continue
		}
		if !running {
			slog.Warn("switchyard-server is not running, restarting", "in", backoff)
			time.Sleep(backoff)
			if err := s.Start(); err != nil {
				slog.Error("restarting switchyard-server failed", "err", err)
				backoff = min(backoff*2, 30*time.Second)
			} else {
				s.mu.Lock()
				s.restarts++
				s.mu.Unlock()
			}
			continue
		}
		if s.Healthy(ctx) {
			backoff = time.Second
			s.mu.Lock()
			if s.state != StateRestarting {
				s.state = StateUp
			}
			s.healthyAt = time.Now()
			s.mu.Unlock()
		} else {
			s.mu.Lock()
			if s.state == StateUp {
				s.state = StateDown
			}
			s.mu.Unlock()
		}
	}
}

// Start launches switchyard-server with the current config. It returns once the process
// has started; use WaitHealthy to know it is serving.
func (s *Supervisor) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return errors.New("switchyard-server is already running")
	}
	if _, err := os.Stat(s.opts.ConfigPath); err != nil {
		s.state = StateUnconfigured
		return fmt.Errorf("no config applied yet")
	}
	if s.opts.BeforeStart != nil {
		s.opts.BeforeStart()
	}
	env, err := s.opts.Env(s.opts.ConfigPath)
	if err != nil {
		return err
	}
	cmd := exec.Command(s.opts.Bin,
		"--config", s.opts.ConfigPath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(s.opts.Port),
		"--routing-log-file", s.opts.RoutingLogPath,
		"--shutdown-timeout", s.opts.ShutdownTimeout.String(),
	)
	cmd.Env = env
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		s.state = StateDown
		return fmt.Errorf("starting switchyard-server: %w", err)
	}
	s.cmd = cmd
	s.wanted = true
	s.startedAt = time.Now()
	if s.state != StateRestarting {
		s.state = StateStarting
	}
	exited := make(chan struct{})
	s.exited = exited
	go s.copyLogs(out)
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		if s.cmd == cmd {
			s.cmd = nil
			s.lastExit = exitText(err)
			if s.wanted && s.state != StateRestarting {
				s.state = StateDown
			}
		}
		s.mu.Unlock()
		close(exited)
		slog.Info("switchyard-server exited", "status", exitText(err))
	}()
	slog.Info("switchyard-server started", "pid", cmd.Process.Pid)
	return nil
}

// Stop asks switchyard-server to shut down gracefully (SIGTERM drains in-flight requests
// for --shutdown-timeout), and kills it if it takes longer than that plus a margin.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	s.wanted = false
	cmd, exited := s.cmd, s.exited
	s.mu.Unlock()
	if cmd == nil {
		return
	}
	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(s.opts.ShutdownTimeout + 5*time.Second):
		slog.Warn("switchyard-server didn't stop in time, killing it")
		cmd.Process.Kill()
		<-exited
	}
	s.mu.Lock()
	if s.state != StateRestarting {
		s.state = StateStopped
	}
	s.mu.Unlock()
}

// Healthy reports whether /health answers OK.
func (s *Supervisor) Healthy(ctx context.Context) bool {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.URL()+"/health", nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// WaitHealthy waits up to timeout for /health to answer OK. It gives up early if the
// process exits.
func (s *Supervisor) WaitHealthy(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		running := s.cmd != nil
		s.mu.Unlock()
		if !running {
			return false
		}
		if s.Healthy(ctx) {
			s.mu.Lock()
			s.state = StateUp
			s.healthyAt = time.Now()
			s.mu.Unlock()
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// DryRun validates a config file with switchyard-server --dry-run. It returns
// switchyard-server's own message when the config is rejected.
func (s *Supervisor) DryRun(ctx context.Context, path string) error {
	env, err := s.opts.Env(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.opts.Bin, "--config", path, "--dry-run")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

func (s *Supervisor) copyLogs(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		s.logs.add(line)
		// Pass Switchyard's output through to the container log as well.
		fmt.Fprintln(os.Stdout, "[switchyard] "+line)
	}
}

func exitText(err error) string {
	if err == nil {
		return "exited normally"
	}
	return err.Error()
}

// ring keeps the last n log lines.
type ring struct {
	mu   sync.Mutex
	buf  []string
	next int
	full bool
}

func newRing(n int) *ring { return &ring{buf: make([]string, n)} }

func (r *ring) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = line
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

func (r *ring) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]string{}, r.buf[:r.next]...)
	}
	return append(append([]string{}, r.buf[r.next:]...), r.buf[:r.next]...)
}
