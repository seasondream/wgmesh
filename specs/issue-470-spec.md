# Specification: Issue #470

## Classification
feature

## Problem Analysis
The wgmesh daemon has no observability endpoint. During dogfooding there is no way to monitor mesh health, peer connectivity, or NAT traversal behavior without reading log output. A Prometheus-compatible metrics endpoint would allow scraping by Coroot (already deployed at table.beerpub.dev) or any standard monitoring stack.

The daemon already has:
- An HTTP server for pprof (`main.go:364`, flag `--pprof`)
- An RPC server via Unix socket (`pkg/rpc/`)
- Peer state in `PeerStore` (`pkg/daemon/peerstore.go`)
- Relay routes in `Daemon.relayRoutes` (`pkg/daemon/daemon.go`)
- NAT type detection in `pkg/discovery/stun.go`
- Discovery layer activity across `pkg/discovery/dht.go`, `lan.go`, `gossip.go`, `registry.go`

## Deliverables
code

## Affected Files
- `main.go` — add `--metrics` flag and HTTP handler
- `pkg/daemon/metrics.go` — new file: metrics collector
- `pkg/daemon/metrics_test.go` — new file: tests
- `go.mod` / `go.sum` — add `github.com/prometheus/client_golang`

## Proposed Approach

### Task 1: Add prometheus dependency
In go.mod, add `github.com/prometheus/client_golang`. Run `go mod tidy`.

### Task 2: Create `pkg/daemon/metrics.go`
Define and register Prometheus metrics:

```go
var (
    activePeers = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "wgmesh_active_peers",
        Help: "Number of currently active peers in the mesh",
    })
    relayedPeers = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "wgmesh_relayed_peers",
        Help: "Number of peers routed via relay (not direct)",
    })
    natType = prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "wgmesh_nat_type",
        Help: "Local NAT type (1=cone, 2=symmetric, 0=unknown)",
    }, []string{"type"})
    discoveryEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "wgmesh_discovery_events_total",
        Help: "Discovery events by layer",
    }, []string{"layer"})  // labels: "dht", "lan", "gossip", "registry"
    reconcileDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
        Name:    "wgmesh_reconcile_duration_seconds",
        Help:    "Time spent in reconcile loop",
        Buckets: prometheus.DefBuckets,
    })
)
```

Create a `MetricsCollector` struct with a `func (m *MetricsCollector) Update(d *Daemon)` method that reads peer store, relay routes, and NAT type, then sets gauges. This method will be called from the reconcile loop.

Register all metrics in an `init()` function or a `RegisterMetrics()` function called from main.

### Task 3: Wire metrics update into reconcile loop
In `pkg/daemon/daemon.go`, at the end of `reconcile()` (after `applyDesiredPeerConfigs`), call the metrics update:

```go
updateMetrics(len(peers), len(relayRoutes))
```

For `reconcileDuration`, wrap the reconcile body with a timer:
```go
start := time.Now()
// ... existing reconcile logic ...
reconcileDuration.Observe(time.Since(start).Seconds())
```

### Task 4: Add `--metrics` flag and HTTP handler in `main.go`
Add a `--metrics` string flag (default `""`, meaning disabled). When set (e.g., `--metrics :9090`):

```go
if *metricsAddr != "" {
    metricsMux := http.NewServeMux()
    metricsMux.Handle("/metrics", promhttp.Handler())
    go func() {
        log.Printf("metrics server listening on %s", *metricsAddr)
        if err := http.ListenAndServe(*metricsAddr, metricsMux); err != nil {
            log.Printf("metrics server error: %v", err)
        }
    }()
}
```

### Task 5: Write tests in `pkg/daemon/metrics_test.go`
- `TestMetricsRegistered` — verify all metrics can be collected without panic
- `TestActivePeersGauge` — call update with known peer count, verify gauge value
- `TestRelayedPeersGauge` — verify relay count reflects relay routes map
- `TestReconcileDuration` — verify histogram records a positive value after reconcile

### Task 6: Verify
Run `go build ./...`, `go test ./...`, `go vet ./...`. All must pass.
