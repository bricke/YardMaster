// Command yardmaster runs YardMaster: the web UI, its API, the /v1 gateway, and the
// switchyard-server it supervises. See docs/architecture.md.
//
// Subcommands:
//
//	serve           run YardMaster (the default)
//	reset-password  set a new admin password (run with docker exec)
//	healthcheck     exit 0 if YardMaster answers on its HTTP port (for Docker)
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"yardmaster/internal/audit"
	"yardmaster/internal/auth"
	"yardmaster/internal/deploy"
	"yardmaster/internal/httpapi"
	"yardmaster/internal/metrics"
	"yardmaster/internal/secrets"
	"yardmaster/internal/settings"
	"yardmaster/internal/store"
	"yardmaster/internal/supervisor"
	"yardmaster/internal/tlsca"
	"yardmaster/internal/usage"
)

var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "reset-password":
		err = resetPassword()
	case "healthcheck":
		err = healthcheck()
	case "version", "--version":
		fmt.Println("yardmaster", version)
	default:
		err = fmt.Errorf("unknown command %q (want serve, reset-password or healthcheck)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "yardmaster:", err)
		os.Exit(1)
	}
}

func serve() error {
	// Everything YardMaster writes (database, keys, certificates, configs) is for the
	// container user only.
	syscall.Umask(0o077)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	cfg, err := settings.Load()
	if err != nil {
		return err
	}
	for _, dir := range []string{cfg.DataDir, cfg.SwitchyardDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w (is /data writable by UID %d?)", dir, err, os.Getuid())
		}
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	keys, err := secrets.Open(cfg.SecretsDir(), cfg.SecretKey)
	if err != nil {
		return err
	}
	if !keys.Encrypted() {
		slog.Warn("provider keys set in the UI are stored unencrypted (file mode 0600); set YARDMASTER_SECRET_KEY to encrypt them")
	}
	authSvc := auth.New(db)
	auditLog := audit.New(db)
	certs, err := tlsca.Open(cfg.TLSDir())
	if err != nil {
		return err
	}

	// The supervisor needs the ingester (to rotate the routing log while Switchyard is
	// stopped), which needs the ledger, which needs the applied config: wire them in order.
	var applier *deploy.Applier
	prices, err := usage.NewPrices(ctx, db)
	if err != nil {
		return err
	}
	ledger := usage.NewLedger(db, prices, func() *deploy.Parsed { return applier.Parsed() })
	ingester := usage.NewIngester(cfg.RoutingLogPath(), db, ledger)
	sup := supervisor.New(supervisor.Options{
		Bin:             cfg.SwitchyardBin,
		ConfigPath:      cfg.ConfigPath(),
		RoutingLogPath:  cfg.RoutingLogPath(),
		Port:            cfg.SwitchyardPort,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Env:             switchyardEnv(keys),
		BeforeStart:     func() { ingester.RotateIfLarge(context.WithoutCancel(ctx)) },
	})
	applier, err = deploy.NewApplier(db, sup, keys, cfg.ConfigPath(), cfg.HistoryDir())
	if err != nil {
		return err
	}
	scraper := metrics.NewScraper(sup.URL())

	srv := httpapi.New(httpapi.Deps{
		Settings: cfg, Version: version, DB: db, Auth: authSvc, Audit: auditLog, Keys: keys,
		Sup: sup, Applier: applier, Ledger: ledger, Prices: prices, Metrics: scraper, TLS: certs,
	})
	if err := firstAdmin(ctx, cfg, authSvc, auditLog, srv); err != nil {
		return err
	}
	srv.SeedHTTPSName(ctx)
	if fp := certs.Fingerprint(); fp != "" {
		slog.Info("TLS certificate", "mode", certs.Mode(), "sha256", fp)
	}

	var wg sync.WaitGroup
	run := func(f func(context.Context)) {
		wg.Add(1)
		go func() { defer wg.Done(); f(ctx) }()
	}
	run(ledger.Run)
	run(ingester.Run)
	run(scraper.Run)
	run(func(ctx context.Context) { housekeeping(ctx, cfg, db, authSvc, auditLog, ledger, certs) })

	if current, _ := applier.Current(); current != "" {
		if err := sup.Start(); err != nil {
			slog.Error("starting switchyard-server", "err", err)
		}
	} else {
		slog.Info("no Switchyard config yet: open the web UI and run the setup wizard")
	}
	run(sup.Run)

	handler := srv.Handler()
	plain := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.HTTPPort),
		Handler:           srv.PlainHandler(handler),
		ReadHeaderTimeout: 10 * time.Second,
	}
	secure := &https{
		srv: &http.Server{
			Addr:              ":" + strconv.Itoa(cfg.HTTPSPort),
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			TLSConfig:         &tls.Config{GetCertificate: certs.GetCertificate, MinVersion: tls.VersionTLS12},
		},
	}
	go func() {
		slog.Info("YardMaster listening", "http", plain.Addr, "version", version)
		if err := plain.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server stopped", "err", err)
			stop()
		}
	}()
	// Start serving HTTPS once it's active; checking every few seconds means turning it on
	// in Settings needs no restart.
	run(func(ctx context.Context) {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			if srv.HTTPSActive(ctx) {
				secure.start()
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout+10*time.Second)
	defer cancel()
	plain.Shutdown(shutdownCtx)
	secure.shutdown(shutdownCtx)
	wg.Wait()
	return nil
}

// https runs the HTTPS listener once, when HTTPS becomes active.
type https struct {
	srv  *http.Server
	once sync.Once
	on   bool
}

func (h *https) start() {
	h.once.Do(func() {
		h.on = true
		go func() {
			slog.Info("HTTPS listening", "addr", h.srv.Addr)
			if err := h.srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("HTTPS server stopped", "err", err)
			}
		}()
	})
}

func (h *https) shutdown(ctx context.Context) {
	if h.on {
		h.srv.Shutdown(ctx)
	}
}

// switchyardEnv builds switchyard-server's environment: a minimal base plus the provider
// keys its config names. YardMaster's own secrets (admin password, master key, proxy
// secret) are never passed on.
func switchyardEnv(keys *secrets.Store) func(string) ([]string, error) {
	return func(configPath string) ([]string, error) {
		env := []string{"PATH=" + os.Getenv("PATH"), "HOME=/tmp"}
		for _, name := range []string{"RUST_LOG", "TZ", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "SSL_CERT_FILE"} {
			if v, ok := os.LookupEnv(name); ok {
				env = append(env, name+"="+v)
			}
		}
		text, err := os.ReadFile(configPath)
		if err != nil {
			return nil, err
		}
		parsed, err := deploy.Parse(string(text))
		if err != nil {
			// Let switchyard-server report the TOML error itself.
			return env, nil
		}
		provider, err := keys.Env(parsed.KeyEnvNames())
		if err != nil {
			return nil, err
		}
		return append(env, provider...), nil
	}
}

// firstAdmin creates the admin from YARDMASTER_ADMIN_PASSWORD, or arms the first-run
// screen with a one-time code printed to the log.
func firstAdmin(ctx context.Context, cfg settings.Settings, a *auth.Service, log *audit.Log, srv *httpapi.Server) error {
	if cfg.Auth != settings.AuthBuiltin {
		return nil
	}
	has, err := a.HasAdmin(ctx)
	if err != nil || has {
		return err
	}
	if cfg.AdminPassword != "" {
		if _, err := a.CreateAdmin(ctx, "admin", cfg.AdminPassword); err != nil {
			return fmt.Errorf("YARDMASTER_ADMIN_PASSWORD: %w", err)
		}
		log.Record(ctx, "admin", "", audit.AdminCreated, "from YARDMASTER_ADMIN_PASSWORD")
		slog.Info("admin account created from YARDMASTER_ADMIN_PASSWORD", "username", "admin")
		return nil
	}
	code := strings.ToUpper(auth.TempPassword()[:12])
	code = code[:4] + "-" + code[4:8] + "-" + code[8:]
	srv.SetSetupCode(code)
	fmt.Printf("\n  YardMaster first-run setup code: %s\n  Open the web UI and enter it to create the admin account.\n\n", code)
	return nil
}

// housekeeping prunes old data and renews certificates, once at start and then hourly.
func housekeeping(ctx context.Context, cfg settings.Settings, db *store.DB, a *auth.Service, log *audit.Log, ledger *usage.Ledger, certs *tlsca.Manager) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		a.PruneSessions(ctx)
		if err := ledger.Prune(ctx, cfg.UsageRetentionDays); err != nil {
			slog.Error("pruning usage", "err", err)
		}
		if _, err := log.Prune(ctx, cfg.AuditRetentionDays); err != nil {
			slog.Error("pruning audit log", "err", err)
		}
		if err := certs.RenewIfNeeded(); err != nil {
			slog.Error("renewing the TLS certificate", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// resetPassword sets a new admin password from a terminal (docker exec -it … yardmaster
// reset-password), or from stdin when piped.
func resetPassword() error {
	cfg, err := settings.Load()
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	var pw string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print("New admin password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		fmt.Print("Again: ")
		b2, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		if string(b) != string(b2) {
			return errors.New("the passwords don't match")
		}
		pw = string(b)
	} else {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		pw = strings.TrimRight(line, "\r\n")
	}
	u, err := auth.New(db).SetAdminPassword(context.Background(), pw)
	if err != nil {
		return err
	}
	audit.New(db).Record(context.Background(), u.Username, "", audit.AdminPasswordReset, "via yardmaster reset-password")
	fmt.Printf("Password for %s changed. Their sessions have been signed out.\n", u.Username)
	return nil
}

// healthcheck is for Docker's HEALTHCHECK: YardMaster itself answers.
func healthcheck() error {
	port := os.Getenv("YARDMASTER_HTTP_PORT")
	if port == "" {
		port = "8080"
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/health")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
