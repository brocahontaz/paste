// Command paste is a lightweight anonymous paste service. It serves the web
// UI, a JSON-free HTTP API, and provides a `healthcheck` subcommand used by
// the Docker HEALTHCHECK (exec form, no shell).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"paste/internal/config"
	"paste/internal/store"
	"paste/internal/web"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			runHealthcheck()
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown subcommand %q (expected: healthcheck)\n", os.Args[1])
			os.Exit(2)
		}
	}
	run()
}

func run() {
	cfg := config.Load()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	srv, err := web.New(&cfg, st)
	if err != nil {
		log.Fatalf("init web server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Background purge loop: periodically delete expired pastes.
	go func() {
		ticker := time.NewTicker(cfg.CleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := st.DeleteExpired(time.Now().Unix())
				if err != nil {
					log.Printf("purge expired pastes: %v", err)
					continue
				}
				if n > 0 {
					log.Printf("purged %d expired paste(s)", n)
				}
			}
		}
	}()

	httpSrv := &http.Server{
		Addr:              cfg.BindAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.ListenAndServe()
	}()
	log.Printf("paste service listening on %s", cfg.BindAddr)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// runHealthcheck probes http://127.0.0.1:{port from PASTE_BIND_ADDR}/healthz
// and exits 0 on HTTP 200, 1 otherwise.
func runHealthcheck() {
	cfg := config.Load()
	client := &http.Client{Timeout: 3 * time.Second}
	url := "http://127.0.0.1:" + healthcheckPort(cfg.BindAddr) + "/healthz"
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: unexpected status %d\n", resp.StatusCode)
		os.Exit(1)
	}
}

// healthcheckPort extracts the port from a bind address such as ":8080",
// "127.0.0.1:8080", or a bare "8080".
func healthcheckPort(bindAddr string) string {
	if _, port, err := net.SplitHostPort(bindAddr); err == nil && port != "" {
		return port
	}
	return bindAddr
}
