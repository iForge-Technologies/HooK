// Command hub is the Hook server: it runs checks, stores results, sends alerts
// and serves the dashboard.
//
//	hub                        start the server
//	hub reset-2fa <username>   turn off two-factor auth for a locked-out user
//	hub set-password <user>    set a new password (read from stdin)
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"hook/internal/alert"
	"hook/internal/api"
	"hook/internal/auth"
	"hook/internal/config"
	"hook/internal/events"
	"hook/internal/fleet"
	"hook/internal/health"
	"hook/internal/maint"
	"hook/internal/monitor"
	"hook/internal/platform"
	"hook/internal/pwa"
	"hook/internal/secret"
	"hook/internal/store"
	"hook/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load()
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	box, err := secret.Load(cfg.Secret, cfg.DataDir)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DataDir, box)
	if err != nil {
		return err
	}
	defer st.Close()

	if len(os.Args) > 1 {
		if os.Args[1] == "backup" {
			return backup(st, cfg, os.Args[2:])
		}
		return command(st, os.Args[1:])
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Optional overrides, for an egress proxy or local testing.
	if u := os.Getenv("HOOK_VERCEL_API"); u != "" {
		platform.VercelAPI = strings.TrimRight(u, "/")
	}
	if u := os.Getenv("HOOK_RENDER_API"); u != "" {
		platform.RenderAPI = strings.TrimRight(u, "/")
	}

	broker := events.NewBroker()
	alerts := alert.NewDispatcher(st, cfg.PublicURL, log)
	cal := maint.New(st, log)
	alerts.Muted = cal.Muted
	pusher, err := pwa.New(st, pushContact(cfg), log)
	if err != nil {
		return fmt.Errorf("web push keys: %w", err)
	}
	alerts.Push = func(ctx context.Context, title, body, path, tag string) error {
		_, err := pusher.Send(ctx, pwa.Notification{Title: title, Body: body, URL: path, Tag: tag})
		return err
	}
	fl := fleet.New(st, alerts, broker, log)
	if err := fl.Start(); err != nil {
		return err
	}
	go fl.Run(ctx)
	go fl.RunForecasts(ctx)
	sched := monitor.New(st, alerts, broker, fl, cal, log)
	if err := sched.Start(ctx); err != nil {
		return err
	}
	go monitor.Maintain(ctx, st, cfg.RawRetentionDays, log)
	hm := health.New(st, health.Options{DataDir: cfg.DataDir, URL: cfg.DeadmanURL, FailURL: cfg.DeadmanFailURL,
		Every: time.Duration(cfg.DeadmanEvery) * time.Second, LastBeat: sched.LastBeat, Monitors: sched.Active}, log)
	go hm.Run(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(cfg, st, sched, fl, cal, hm, pusher, alerts, broker, web.Dist(), log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// no WriteTimeout: the SSE stream is long-lived
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	log.Info("hook hub listening", "addr", cfg.Addr, "data", cfg.DataDir, "version", api.Version)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}

// pushContact is the VAPID "subscriber" push services may use to reach the
// hub's operator: HOOK_PUSH_CONTACT (an email), or the public https URL.
func pushContact(cfg config.Config) string {
	if c := os.Getenv("HOOK_PUSH_CONTACT"); c != "" {
		return strings.TrimPrefix(c, "mailto:") // the library adds it back
	}
	if strings.HasPrefix(cfg.PublicURL, "https://") {
		return cfg.PublicURL
	}
	return "hook@localhost"
}

// backup writes a consistent copy of the database (safe while the hub runs)
// plus the encryption key, without which stored secrets can't be decrypted.
//
//	hub backup /var/backups/hook
func backup(st *store.Store, cfg config.Config, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: hub backup <directory>")
	}
	stamp := time.Now().Format("20060102-150405")
	dbPath := filepath.Join(args[0], "hook-"+stamp+".db")
	if err := st.Backup(dbPath); err != nil {
		return err
	}
	fmt.Println("Database:", dbPath)
	if cfg.Secret != "" {
		fmt.Println("Key:      set by HOOK_SECRET. Keep a copy of that value with this backup:")
		fmt.Println("          without it, channel tokens, connection strings and 2FA secrets can't be decrypted.")
		return nil
	}
	key, err := os.ReadFile(filepath.Join(cfg.DataDir, "secret.key"))
	if err != nil {
		return fmt.Errorf("read secret.key: %w", err)
	}
	keyPath := filepath.Join(args[0], "secret-"+stamp+".key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return err
	}
	fmt.Println("Key:     ", keyPath)
	fmt.Println("Store the key apart from the database copy (anyone with both can read your stored secrets).")
	return nil
}

func command(st *store.Store, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: hub reset-2fa <username> | hub set-password <username>")
	}
	u, err := st.UserByName(args[1])
	if err != nil {
		return fmt.Errorf("user %q: %w", args[1], err)
	}
	switch args[0] {
	case "reset-2fa":
		if err := st.SetTOTP(u.ID, nil, false); err != nil {
			return err
		}
		fmt.Println("Two-factor authentication turned off for", u.Username)
	case "set-password":
		fmt.Fprint(os.Stderr, "New password: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		pw := strings.TrimRight(line, "\r\n")
		if err := auth.ValidatePassword(pw); err != nil {
			return err
		}
		hash, err := auth.HashPassword(pw)
		if err != nil {
			return err
		}
		if err := st.SetPassword(u.ID, hash); err != nil {
			return err
		}
		if err := st.DeleteUserSessions(u.ID); err != nil {
			return err
		}
		fmt.Println("Password updated; all sessions signed out.")
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}
