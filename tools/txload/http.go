package txload

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cuihairu/chame/infrastructure/observability/prometheus"
	"github.com/cuihairu/chame/tools/txload/devchain"
)

//go:embed dashboard.html
var dashboardHTML []byte

// Handler builds the tool's HTTP surface: the self-contained dashboard
// at /, the Prometheus exposition the dashboard reads at /metrics, the
// live counters snapshot at /api/state, the fault-injection control at
// /api/faults, and a plain /healthz over the endpoint pool.
// NOTE: dashboard and /api/faults endpoints have no auth by design;
// bind -http to loopback only when exposing fault-injection controls.

func (d *Driver) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(dashboardHTML)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		d.runMu.Lock()
		collector := d.collector
		d.runMu.Unlock()
		if collector == nil {
			// Run has not built the collector yet.
			http.Error(w, "no run in progress", http.StatusServiceUnavailable)
			return
		}
		prometheus.Handler(collector).ServeHTTP(w, nil)
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, d.Snapshot())
	})
	mux.HandleFunc("/api/faults", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Endpoint   string   `json:"endpoint"`
			Down       *bool    `json:"down"`
			ErrorRate  *float64 `json:"error_rate"`
			DropRate   *float64 `json:"drop_rate"`
			RevertRate *float64 `json:"revert_rate"`
			LatencyMS  *int     `json:"latency_ms"`
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || json.Unmarshal(body, &req) != nil || req.Endpoint == "" {
			http.Error(w, "bad request: endpoint and fields required", http.StatusBadRequest)
			return
		}
		node := d.faultNode(req.Endpoint)
		if node == nil {
			http.Error(w, "endpoint is external; fault injection only applies to self-hosted mock endpoints", http.StatusBadRequest)
			return
		}
		f := node.Faults()
		if req.Down != nil {
			f.Down = *req.Down
		}
		if req.ErrorRate != nil {
			f.ErrorRate = *req.ErrorRate
		}
		if req.DropRate != nil {
			f.DropRate = *req.DropRate
		}
		if req.RevertRate != nil {
			f.RevertRate = *req.RevertRate
		}
		if req.LatencyMS != nil {
			f.LatencyMS = *req.LatencyMS
		}
		if err := node.SetFaults(f); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, node.Faults())
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d.cfg.RequestTimeout)
		defer cancel()
		net, ok := d.adapter.Network(d.nid)
		healthy := ok && net.Healthy(ctx) == nil
		status := http.StatusOK
		if !healthy {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(map[bool]string{true: "ok\n", false: "unhealthy\n"}[healthy]))
	})
	return mux
}

// Snapshot is the live counters' read for the dashboard's state panel
// and fault controls. Series data itself always comes from /metrics;
// this only carries config and the per-endpoint fault profiles.
type Snapshot struct {
	Network       string           `json:"network"`
	ChainID       uint64           `json:"chain_id"`
	Sent          int64            `json:"sent"`
	Submitted     int64            `json:"submitted"`
	SubmitFails   int64            `json:"submit_fails"`
	TimedOut      int64            `json:"timed_out"`
	Succeeded     int64            `json:"succeeded"`
	Reverted      int64            `json:"reverted"`
	Failed        int64            `json:"failed"`
	Inflight      int64            `json:"inflight"`
	TPSTarget     float64          `json:"tps_target"`
	RampSeconds   float64          `json:"ramp_seconds"`
	DurationSec   float64          `json:"duration_seconds"`
	UptimeSeconds float64          `json:"uptime_seconds"`
	FaultEditable bool             `json:"fault_editable"`
	Endpoints     []EndpointView   `json:"endpoints"`
	Transitions   map[string]int64 `json:"transitions"`
}

// EndpointView is one endpoint row for the state view.
type EndpointView struct {
	URL    string           `json:"url"`
	Health bool             `json:"health"`
	Faults *devchain.Faults `json:"faults,omitempty"`
}

// Snapshot renders the current state.
func (d *Driver) Snapshot() Snapshot {
	d.runMu.Lock()
	startedAt := d.startedAt
	endpoints := d.endpoints
	d.runMu.Unlock()
	s := Snapshot{
		Network:       d.nid.String(),
		ChainID:       d.cfg.ChainID,
		Sent:          d.sent.Load(),
		Submitted:     d.submitted.Load(),
		SubmitFails:   d.submitFails.Load(),
		TimedOut:      d.timedOut.Load(),
		Succeeded:     d.succeeded.Load(),
		Reverted:      d.reverted.Load(),
		Failed:        d.failed.Load(),
		Inflight:      d.sent.Load() - d.succeeded.Load() - d.reverted.Load() - d.failed.Load() - d.timedOut.Load() - d.submitFails.Load(),
		TPSTarget:     d.cfg.TPS,
		RampSeconds:   d.cfg.Ramp.Seconds(),
		DurationSec:   d.cfg.Duration.Seconds(),
		UptimeSeconds: time.Since(startedAt).Seconds(),
		Transitions:   map[string]int64{},
	}
	d.trackedMu.Lock()
	for k, ctr := range d.transitions {
		s.Transitions[k] = ctr.Load()
	}
	d.trackedMu.Unlock()
	s.Endpoints = make([]EndpointView, 0, len(endpoints))
	for _, ep := range endpoints {
		view := EndpointView{URL: ep.URL}
		d.lastHealthMu.Lock()
		view.Health = d.lastHealth[ep.URL]
		d.lastHealthMu.Unlock()
		if ep.Node != nil {
			s.FaultEditable = true
			f := ep.Node.Faults()
			view.Faults = &f
		}
		s.Endpoints = append(s.Endpoints, view)
	}
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// probeChainID sends one minimal JSON-RPC chain-id read straight to an
// endpoint URL — independent of the adapter's pool, so the health
// matrix judges each endpoint on its own.
func probeChainID(ctx context.Context, url string) error {
	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("endpoint answered %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return err
	}
	var parsed struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return err
	}
	if parsed.Error != nil {
		return fmt.Errorf("rpc error: %s", parsed.Error.Message)
	}
	if parsed.Result == "" {
		return fmt.Errorf("no chain id in response")
	}
	return nil
}
