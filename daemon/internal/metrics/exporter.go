// Package metrics exposes Prometheus metrics for the AetherNet daemon.
package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/aethernet/aethernet/daemon/internal/cluster"
	"github.com/aethernet/aethernet/daemon/internal/raftfsm"
	"github.com/aethernet/aethernet/pkg/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Options struct {
	Listen  string // e.g. ":9100"
	NodeID  string
	Cluster *cluster.Cluster
	FSM     *raftfsm.FSM
	Logger  *slog.Logger
}

type Exporter struct {
	opts Options
	reg  *prometheus.Registry

	// Gauges
	nodeMemTotalMB  *prometheus.GaugeVec
	nodeMemUsedMB   *prometheus.GaugeVec
	nodeCPULoad     *prometheus.GaugeVec
	nodeState       *prometheus.GaugeVec
	containerCount  prometheus.Gauge
	raftIsLeader    prometheus.Gauge
	raftCommitIndex prometheus.Gauge
	serversByState  *prometheus.GaugeVec
}

func New(o Options) *Exporter {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	reg := prometheus.NewRegistry()
	e := &Exporter{
		opts: o,
		reg:  reg,
	}

	e.nodeMemTotalMB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "aethernet",
		Name:      "node_memory_total_mb",
		Help:      "Total memory of each cluster node in MiB",
	}, []string{"node_id"})

	e.nodeMemUsedMB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "aethernet",
		Name:      "node_memory_used_mb",
		Help:      "Used memory of each cluster node in MiB",
	}, []string{"node_id"})

	e.nodeCPULoad = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "aethernet",
		Name:      "node_cpu_load_percent",
		Help:      "1-minute CPU load average as a percentage per node",
	}, []string{"node_id"})

	e.nodeState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "aethernet",
		Name:      "node_state",
		Help:      "State of each cluster node (1=up, 2=suspect, 3=down, 4=draining)",
	}, []string{"node_id"})

	e.containerCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "aethernet",
		Name:      "container_count",
		Help:      "Number of running Minecraft OCI containers on this node",
	})

	e.raftIsLeader = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "aethernet",
		Name:      "raft_is_leader",
		Help:      "1 if this node is the current Raft leader, 0 otherwise",
	})

	e.raftCommitIndex = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "aethernet",
		Name:      "raft_commit_index",
		Help:      "Current Raft commit index",
	})

	e.serversByState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "aethernet",
		Name:      "servers_by_state",
		Help:      "Number of servers in each state",
	}, []string{"state"})

	reg.MustRegister(
		e.nodeMemTotalMB,
		e.nodeMemUsedMB,
		e.nodeCPULoad,
		e.nodeState,
		e.containerCount,
		e.raftIsLeader,
		e.raftCommitIndex,
		e.serversByState,
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
	return e
}

// Serve starts the Prometheus HTTP metrics server on opts.Listen.
func (e *Exporter) Serve(ctx context.Context) error {
	// Start collector loop
	go e.collect(ctx)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(e.reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })

	srv := &http.Server{
		Addr:              e.opts.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	e.opts.Logger.Info("prometheus exporter listening", "addr", e.opts.Listen)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		_ = srv.Close()
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (e *Exporter) collect(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.scrape()
		}
	}
}

func (e *Exporter) scrape() {
	// Cluster nodes
	nodes := e.opts.FSM.Nodes()
	for _, n := range nodes {
		labels := prometheus.Labels{"node_id": n.ID}
		e.nodeMemTotalMB.With(labels).Set(float64(n.Resources.MemoryTotalMB))
		e.nodeMemUsedMB.With(labels).Set(float64(n.Resources.MemoryUsedMB))
		e.nodeCPULoad.With(labels).Set(n.Resources.CPULoad)
		e.nodeState.With(labels).Set(float64(n.State))
	}

	// Raft state
	if e.opts.Cluster.IsLeader() {
		e.raftIsLeader.Set(1)
	} else {
		e.raftIsLeader.Set(0)
	}
	e.raftCommitIndex.Set(float64(e.opts.Cluster.Raft().AppliedIndex()))

	// Servers by state
	stateCounts := map[string]float64{
		types.ServerStopped.String():  0,
		types.ServerStarting.String(): 0,
		types.ServerReady.String():    0,
		types.ServerStopping.String(): 0,
		types.ServerCrashed.String():  0,
		types.ServerOrphaned.String(): 0,
	}
	for _, srv := range e.opts.FSM.Servers() {
		stateCounts[srv.State.String()]++
	}
	for state, count := range stateCounts {
		e.serversByState.With(prometheus.Labels{"state": state}).Set(count)
	}
}
