package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aethernet/aethernet/panel/internal/handlers"
)

var (
	listenAddr = flag.String("listen", "0.0.0.0:8080", "panel listen address")
	daemonAddr = flag.String("daemon", "http://127.0.0.1:8080", "local daemon REST endpoint")
	staticDir  = flag.String("static", "./web", "directory containing the panel's static assets")
)

func main() {
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	mux := http.NewServeMux()
	handlers.Register(mux, handlers.Options{
		DaemonBaseURL: *daemonAddr,
		StaticDir:     *staticDir,
		Logger:        logger,
	})

	srv := &http.Server{
		Addr:              *listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		logger.Info("aether-panel listening", "addr", *listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	logger.Info("shutting down")
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	_ = srv.Shutdown(sctx)
}
