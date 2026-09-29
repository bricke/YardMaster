package supervisor

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary doubles as a fake switchyard-server: when FAKE_SWITCHYARD is set in
// its environment, it behaves as the config file tells it to instead of running tests.
//
//	healthy       serve /health, exit on SIGTERM
//	unhealthy     serve /health with 503
//	crash         exit 1 at once
//	stubborn      serve /health and ignore SIGTERM
//	invalid       rejected by --dry-run
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_SWITCHYARD") != "" {
		fakeSwitchyard()
		return
	}
	os.Exit(m.Run())
}

func fakeSwitchyard() {
	fs := flag.NewFlagSet("switchyard-server", flag.ExitOnError)
	config := fs.String("config", "", "")
	port := fs.Int("port", 0, "")
	dryRun := fs.Bool("dry-run", false, "")
	fs.String("host", "", "")
	fs.String("routing-log-file", "", "")
	fs.String("shutdown-timeout", "", "")
	fs.Parse(os.Args[1:])

	b, err := os.ReadFile(*config)
	if err != nil {
		fmt.Println("can't read config:", err)
		os.Exit(2)
	}
	mode := strings.TrimSpace(string(b))
	if *dryRun {
		if mode == "invalid" {
			fmt.Println("error: unknown route type `teleport`")
			os.Exit(1)
		}
		os.Exit(0)
	}
	fmt.Println("fake switchyard-server starting:", mode)
	if mode == "crash" {
		os.Exit(1)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	status := http.StatusOK
	if mode == "unhealthy" {
		status = http.StatusServiceUnavailable
	}
	go http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", *port), http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	for range signals {
		if mode != "stubborn" {
			fmt.Println("draining and stopping")
			os.Exit(0)
		}
	}
}

type fixture struct {
	sup        *Supervisor
	configPath string
	envFor     chan string // config paths Env was called with
	started    chan struct{}
}

// newFixture returns a supervisor running the fake with the given mode written to its
// config. An empty mode writes no config.
func newFixture(t *testing.T, mode string, shutdown time.Duration) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{configPath: filepath.Join(dir, "config.toml"), envFor: make(chan string, 16), started: make(chan struct{}, 16)}
	if mode != "" {
		f.write(t, mode)
	}
	f.sup = New(Options{
		Bin:             os.Args[0],
		ConfigPath:      f.configPath,
		RoutingLogPath:  filepath.Join(dir, "routing.jsonl"),
		Port:            freePort(t),
		ShutdownTimeout: shutdown,
		Env: func(path string) ([]string, error) {
			f.envFor <- path
			return []string{"FAKE_SWITCHYARD=1"}, nil
		},
		BeforeStart: func() { f.started <- struct{}{} },
	})
	t.Cleanup(f.sup.Stop)
	return f
}

func (f *fixture) write(t *testing.T, mode string) {
	if err := os.WriteFile(f.configPath, []byte(mode), 0o600); err != nil {
		t.Fatal(err)
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// eventually polls cond until it holds or the timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStartAndStop(t *testing.T) {
	f := newFixture(t, "healthy", 5*time.Second)
	if err := f.sup.Start(); err != nil {
		t.Fatal(err)
	}
	if !f.sup.WaitHealthy(t.Context(), 10*time.Second) {
		t.Fatalf("never became healthy; logs: %q", f.sup.Logs())
	}
	st := f.sup.Status()
	if st.State != StateUp || st.PID == 0 || st.LastHealthy.IsZero() {
		t.Errorf("status after start: %+v", st)
	}
	if got := <-f.envFor; got != f.configPath {
		t.Errorf("Env called for %q, want %q", got, f.configPath)
	}
	if len(f.started) != 1 {
		t.Error("BeforeStart didn't run")
	}
	if err := f.sup.Start(); err == nil {
		t.Error("a second Start while running was accepted")
	}

	f.sup.Stop()
	st = f.sup.Status()
	if st.State != StateStopped || st.PID != 0 || st.LastExit != "exited normally" {
		t.Errorf("status after stop: %+v", st)
	}
	eventually(t, 2*time.Second, "the child's output in the logs", func() bool {
		return slices.Contains(f.sup.Logs(), "draining and stopping")
	})
}

func TestStartWithoutConfig(t *testing.T) {
	f := newFixture(t, "", time.Second)
	if err := f.sup.Start(); err == nil {
		t.Fatal("started without a config")
	}
	if st := f.sup.Status(); st.State != StateUnconfigured {
		t.Errorf("state %q, want %q", st.State, StateUnconfigured)
	}
}

func TestWaitHealthyGivesUpWhenTheProcessExits(t *testing.T) {
	f := newFixture(t, "crash", time.Second)
	if err := f.sup.Start(); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	if f.sup.WaitHealthy(t.Context(), 20*time.Second) {
		t.Fatal("a crashed process reported healthy")
	}
	if waited := time.Since(begin); waited > 10*time.Second {
		t.Errorf("waited %v for a process that had already exited", waited)
	}
	eventually(t, 2*time.Second, "the exit to be recorded", func() bool {
		st := f.sup.Status()
		return st.State == StateDown && strings.Contains(st.LastExit, "exit status 1")
	})
}

func TestUnhealthyIsNotUp(t *testing.T) {
	f := newFixture(t, "unhealthy", time.Second)
	if err := f.sup.Start(); err != nil {
		t.Fatal(err)
	}
	if f.sup.WaitHealthy(t.Context(), 2*time.Second) {
		t.Fatal("a 503 on /health counted as healthy")
	}
	if st := f.sup.Status(); st.State != StateStarting {
		t.Errorf("state %q, want %q", st.State, StateStarting)
	}
}

func TestRunRestartsAfterACrash(t *testing.T) {
	f := newFixture(t, "healthy", 5*time.Second)
	if err := f.sup.Start(); err != nil {
		t.Fatal(err)
	}
	if !f.sup.WaitHealthy(t.Context(), 10*time.Second) {
		t.Fatal("never became healthy")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { f.sup.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	first := f.sup.Status().PID
	syscall.Kill(first, syscall.SIGKILL)
	eventually(t, 20*time.Second, "a restart", func() bool {
		st := f.sup.Status()
		return st.Restarts == 1 && st.State == StateUp && st.PID != 0 && st.PID != first
	})
}

func TestStopDoesNotRestart(t *testing.T) {
	f := newFixture(t, "healthy", 5*time.Second)
	if err := f.sup.Start(); err != nil {
		t.Fatal(err)
	}
	f.sup.WaitHealthy(t.Context(), 10*time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { f.sup.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	f.sup.Stop()
	time.Sleep(4 * time.Second) // longer than one Run tick
	if st := f.sup.Status(); st.State != StateStopped || st.Restarts != 0 || st.PID != 0 {
		t.Errorf("after Stop: %+v", st)
	}
}

func TestStopKillsAChildThatWontExit(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the shutdown timeout plus its margin")
	}
	f := newFixture(t, "stubborn", time.Second)
	if err := f.sup.Start(); err != nil {
		t.Fatal(err)
	}
	if !f.sup.WaitHealthy(t.Context(), 10*time.Second) {
		t.Fatal("never became healthy")
	}
	begin := time.Now()
	f.sup.Stop()
	if waited := time.Since(begin); waited < time.Second {
		t.Errorf("killed after %v, before the drain window ended", waited)
	}
	st := f.sup.Status()
	if st.PID != 0 || !strings.Contains(st.LastExit, "killed") {
		t.Errorf("after Stop: %+v", st)
	}
}

func TestDryRun(t *testing.T) {
	f := newFixture(t, "", time.Second)
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.toml"), filepath.Join(dir, "bad.toml")
	os.WriteFile(good, []byte("healthy"), 0o600)
	os.WriteFile(bad, []byte("invalid"), 0o600)
	if err := f.sup.DryRun(t.Context(), good); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
	err := f.sup.DryRun(t.Context(), bad)
	if err == nil || !strings.Contains(err.Error(), "unknown route type") {
		t.Errorf("error %v, want switchyard-server's own message", err)
	}
	if got := <-f.envFor; got != good {
		t.Errorf("Env called for %q, want the candidate %q", got, good)
	}
	if st := f.sup.Status(); st.PID != 0 {
		t.Errorf("a dry run left a process behind: %+v", st)
	}
}

func TestRingKeepsTheLastLines(t *testing.T) {
	r := newRing(3)
	if got := r.lines(); len(got) != 0 {
		t.Errorf("empty ring: %q", got)
	}
	for _, l := range []string{"a", "b"} {
		r.add(l)
	}
	if got := strings.Join(r.lines(), ""); got != "ab" {
		t.Errorf("partly full: %q", got)
	}
	for _, l := range []string{"c", "d", "e"} {
		r.add(l)
	}
	if got := strings.Join(r.lines(), ""); got != "cde" {
		t.Errorf("wrapped: %q", got)
	}
}
