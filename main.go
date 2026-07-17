// Command kdy-hrajeme je malý server na hlasování o termínech.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/RichieEXEC/gaming_session_voter/internal/app"
	"github.com/RichieEXEC/gaming_session_voter/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dbPath := env("DB_PATH", "/data/kdyhrajeme.db")
	addr := ":" + env("PORT", "8080")

	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Error("create data dir", "dir", dir, "err", err)
			os.Exit(1)
		}
		// SQLite hlásí nezapisovatelný adresář jako "out of memory (14)",
		// což pošle člověka hledat úplně jinam. Radši to zjistíme dřív a
		// řekneme narovinu, co je špatně.
		if err := checkWritable(dir); err != nil {
			log.Error("data dir is not writable",
				"dir", dir,
				"uid", os.Getuid(),
				"err", err,
				"hint", "mount a volume writable by uid 10001, or let the container start as root so the entrypoint can fix it",
			)
			os.Exit(1)
		}
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Error("open store", "path", dbPath, "err", err)
		os.Exit(1)
	}
	defer st.Close()

	handler, err := app.New(st, log)
	if err != nil {
		log.Error("build app", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Info("shutting down")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Error("shutdown", "err", err)
		}
		close(idle)
	}()

	log.Info("listening", "addr", addr, "db", dbPath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("listen", "err", err)
		os.Exit(1)
	}
	<-idle
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// checkWritable ověří zápis skutečným souborem. Koukat na bity práv
// nestačí: rozhoduje uid, gid i read-only mount.
func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".writetest-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}
