package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aethernet/aethernet/daemon/internal/api"
	"github.com/aethernet/aethernet/daemon/internal/cluster"
	"github.com/aethernet/aethernet/daemon/internal/config"
	"github.com/aethernet/aethernet/daemon/internal/docker"
	"github.com/aethernet/aethernet/daemon/internal/raftfsm"
	"github.com/aethernet/aethernet/daemon/internal/scheduler"
	"github.com/aethernet/aethernet/daemon/internal/sftp"
)

var (
	configPath = flag.String("config", "/etc/aethernet/daemon.yaml", "path to daemon config")
	bootstrap  = flag.Bool("bootstrap", false, "initialize a new single-node cluster")
	joinAddr   = flag.String("join", "", "address of an existing cluster member to join")
	joinToken  = flag.String("join-token", "", "one-time join token (required when --join is set)")
	logLevel   = flag.String("log-level", "info", "debug|info|warn|error")
)

func main() {
	flag.Parse()
	logger := newLogger(*logLevel)
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("daemon exited", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load(*configPath)
	if err != nil {
		// Allow first-run with no config when bootstrapping.
		if !errors.Is(err, os.ErrNotExist) || !*bootstrap {
			return fmt.Errorf("load config: %w", err)
		}
		cfg = config.Default()
		if err := os.MkdirAll(filepath.Dir(*configPath), 0o755); err == nil {
			_ = cfg.Save(*configPath)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Raft FSM + storage
	fsm, err := raftfsm.NewFSM(filepath.Join(cfg.DataDir, "fsm"))
	if err != nil {
		return fmt.Errorf("init fsm: %w", err)
	}

	// 2. Cluster (Raft) supervisor
	clu, err := cluster.New(cluster.Options{
		NodeID:        cfg.NodeID,
		BindAddr:      cfg.RaftBindAddr,
		AdvertiseAddr: cfg.RaftAdvertiseAddr,
		DataDir:       filepath.Join(cfg.DataDir, "raft"),
		Bootstrap:     *bootstrap,
		Logger:        logger.With("component", "cluster"),
		FSM:           fsm,
	})
	if err != nil {
		return fmt.Errorf("init cluster: %w", err)
	}
	defer clu.Close()

	if *joinAddr != "" {
		if err := clu.JoinExisting(ctx, *joinAddr, *joinToken); err != nil {
			return fmt.Errorf("join cluster: %w", err)
		}
	}

	// 3. Docker controller
	dc, err := docker.New(logger.With("component", "docker"))
	if err != nil {
		logger.Warn("docker controller unavailable; server lifecycle disabled", "err", err)
	}

	// 4. Scheduler — placement decisions, failover
	sched := scheduler.New(scheduler.Options{
		Cluster: clu,
		FSM:     fsm,
		Docker:  dc,
		Logger:  logger.With("component", "scheduler"),
	})
	go sched.Run(ctx)

	// 5. SFTP server
	sftpSrv, err := sftp.New(sftp.Options{
		Listen:  cfg.SFTPListen,
		HostKey: filepath.Join(cfg.DataDir, "sftp", "host_key"),
		FSM:     fsm,
		Docker:  dc,
		Logger:  logger.With("component", "sftp"),
	})
	if err != nil {
		return fmt.Errorf("init sftp: %w", err)
	}
	go func() {
		if err := sftpSrv.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("sftp server exited", "err", err)
		}
	}()

	// 6. gRPC + REST API
	apiSrv := api.New(api.Options{
		GRPCListen: cfg.GRPCListen,
		HTTPListen: cfg.HTTPListen,
		Cluster:    clu,
		FSM:        fsm,
		Scheduler:  sched,
		Docker:     dc,
		Logger:     logger.With("component", "api"),
	})
	go func() {
		if err := apiSrv.Serve(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("api server exited", "err", err)
		}
	}()

	logger.Info("aether-daemon started",
		"node_id", cfg.NodeID,
		"raft", cfg.RaftBindAddr,
		"grpc", cfg.GRPCListen,
		"http", cfg.HTTPListen,
		"sftp", cfg.SFTPListen,
	)

	// Wait for shutdown signal.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	logger.Info("shutdown signal received")
	cancel()
	shutdownCtx, scancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer scancel()
	apiSrv.Shutdown(shutdownCtx)
	return nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
