// Package txload is the transaction load driver (spec §9/§10/§14 paths
// under load). It drives simulated transfers through the real pipeline:
// the EVM adapter's endpoint pool and transactor, the shared transaction
// state machine, the idempotency-keyed flow, and the recovery worker.
//
// Configuration covers the endpoint pool, target rate with an optional
// linear ramp, send concurrency, transaction size (calldata padding),
// per-transaction timeout, and submit retry policy. The driver counts
// outcomes per state-machine phase, records submit/confirm/end-to-end
// latency samples, probes every endpoint for the health matrix, and
// publishes everything it measures into the framework's memory
// collector so the embedded dashboard and any Prometheus scraper see
// one consistent /metrics view.
package txload

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/cuihairu/chame/adapters/evm"
	"github.com/cuihairu/chame/application/transaction"
	"github.com/cuihairu/chame/capability/signer"
	txmodel "github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/capability"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/core/metrics"
	"github.com/cuihairu/chame/core/module"
	memmetrics "github.com/cuihairu/chame/infrastructure/observability/memory"
	"github.com/cuihairu/chame/infrastructure/observability/prometheus"
	memstore "github.com/cuihairu/chame/infrastructure/persistence/memory"
	"github.com/cuihairu/chame/tools/txload/devchain"
)

// Metric names the driver adds on top of the framework's declared set
// (core/metrics). Everything the driver emits is bounded by
// configuration: one series per state, per endpoint, per network.
const (
	MetricTxDuration     = "txload_transaction_duration_seconds"
	MetricSubmitDuration = "txload_submit_duration_seconds"
	MetricInflight       = "txload_inflight"
	MetricEndpointFo     = "endpoint_failovers_total"
)

// paceTick is the pacer's control period; target-rate changes within a
// ramp resolve at this granularity.
const paceTick = 10 * time.Millisecond

// Endpoint is one RPC endpoint in the pool. Node is set for endpoints
// the driver itself hosts (the mock devchain), enabling live fault
// injection; it is nil for external endpoints.
type Endpoint struct {
	URL  string
	Node *devchain.Node
}

// Config is the load run's shape.
type Config struct {
	// NetworkName and ChainID pin the logical network the pool serves.
	NetworkName string
	ChainID     uint64
	// Endpoints is the RPC endpoint pool (http(s) URLs).
	Endpoints []string
	// KeyHex is the dev signer's plaintext key. Development only; the
	// signer refuses production environments itself (spec §12.4).
	KeyHex string
	// TPS is the target submission rate; Ramp spreads it linearly from
	// zero over this duration (zero means constant rate from the start).
	TPS  float64
	Ramp time.Duration
	// Duration bounds the send phase; Total, when positive, bounds the
	// number of transactions instead. Both zero means send until the
	// context ends.
	Duration time.Duration
	Total    int64
	// Concurrency bounds in-flight send pipelines (flow.Begin/Drive).
	Concurrency int
	// TxSize pads every transaction's calldata to this many bytes.
	TxSize int
	// TxTimeout bounds one transaction's submit phase.
	TxTimeout time.Duration
	// Retries is how many times a transiently failing submit (an
	// endpoint error the pool has already failed over on) is re-driven
	// before the record is left for the recovery worker.
	Retries      int
	RetryBackoff time.Duration
	// RequestTimeout bounds one RPC request inside the adapter.
	RequestTimeout time.Duration
	// Worker tunes the recovery loop. The defaults assume production
	// pacing (30s scans); a load run wants sub-second values.
	Worker transaction.Config
	// ProbeInterval paces the endpoint health matrix refresh.
	ProbeInterval time.Duration
	// Logger receives diagnostics. Default: slog.Default.
	Logger *slog.Logger
}

// latencySamples accumulates one kind of latency observation.
type latencySamples struct {
	mu sync.Mutex
	v  []float64
}

func (s *latencySamples) add(seconds float64) {
	s.mu.Lock()
	s.v = append(s.v, seconds)
	s.mu.Unlock()
}

func (s *latencySamples) snapshot() []float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]float64(nil), s.v...)
}

// Percentiles reports p50/p90/p99 in seconds over one sample set.
func (s *latencySamples) Percentiles() (p50, p90, p99 float64) {
	v := s.snapshot()
	if len(v) == 0 {
		return 0, 0, 0
	}
	sort.Float64s(v)
	pick := func(q float64) float64 {
		idx := int(q * float64(len(v)-1))
		return v[idx]
	}
	return pick(0.50), pick(0.90), pick(0.99)
}

// Result is the run's summary. Counts are outcomes of the tracked
// transactions; the sample sets back the percentile report and the
// driver's histograms.
type Result struct {
	StartedAt time.Time
	EndedAt   time.Time

	Sent        int64
	Submitted   int64
	SubmitFails int64
	TimedOut    int64
	Succeeded   int64 // reached included (or beyond): the load's confirmation policy
	Reverted    int64
	Failed      int64
	InflightEnd int64

	// PhaseCounts is the final distribution of tracked records across
	// state-machine statuses.
	PhaseCounts map[string]int64
	// Transitions counts observed status moves ("from->to"), including
	// the recovery worker's.
	Transitions map[string]int64
	// EndpointHealth carries the last probe result per endpoint URL.
	EndpointHealth map[string]bool

	// Submit, confirm, and end-to-end latency sample sets.
	Submit  *latencySamples
	Confirm *latencySamples
	E2E     *latencySamples
}

// Driver runs one load campaign against one endpoint pool.
type Driver struct {
	cfg    Config
	logger *slog.Logger
	nid    identifiers.NetworkID
	key    *ecdsa.PrivateKey
	sender identifiers.Address

	signer  *evm.LocalDevSigner
	evmMod  *evm.Module
	adapter *evm.Adapter
	net     *evm.Network
	repo    *memstore.Store
	flow    *transaction.Flow
	store   *module.Store

	collector *memmetrics.Collector
	recorder  *prometheus.Recorder
	endpoints []Endpoint

	// run state
	sent        atomic.Int64
	submitted   atomic.Int64
	submitFails atomic.Int64
	timedOut    atomic.Int64
	succeeded   atomic.Int64
	reverted    atomic.Int64
	failed      atomic.Int64
	nonce       atomic.Uint64

	trackedMu    sync.Mutex
	trackedIDs   []txmodel.TransactionID
	trackedMap   map[txmodel.TransactionID]*trackEntry
	transitions  map[string]*atomic.Int64
	lastHealth   map[string]bool
	lastHealthMu sync.Mutex
	nodes        map[string]*devchain.Node
	Submit       latencySamples
	Confirm      latencySamples
	E2E          latencySamples

	startedAt    time.Time
	lastFailover uint64
}

type trackEntry struct {
	createdAt time.Time
	done      atomic.Bool
}

// NewDriver validates the config and builds the driver without touching
// the network. Run dials the pool.
func NewDriver(cfg Config) (*Driver, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("txload: at least one endpoint is required")
	}
	if cfg.TPS <= 0 {
		return nil, fmt.Errorf("txload: tps must be positive")
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 4
	}
	if cfg.TxTimeout <= 0 {
		cfg.TxTimeout = 15 * time.Second
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 100 * time.Millisecond
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 5 * time.Second
	}
	if cfg.ProbeInterval <= 0 {
		cfg.ProbeInterval = 500 * time.Millisecond
	}
	if cfg.NetworkName == "" {
		cfg.NetworkName = "txload"
	}
	if cfg.ChainID == 0 {
		cfg.ChainID = 1337
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	key, err := evm.DevKeyFromHex(cfg.KeyHex)
	if err != nil {
		return nil, fmt.Errorf("txload: bad key: %w", err)
	}
	sender, err := evm.DevKeyAddress(key)
	if err != nil {
		return nil, fmt.Errorf("txload: sender address: %w", err)
	}
	nid := identifiers.NetworkID{
		Family:  identifiers.FamilyEVM,
		Name:    cfg.NetworkName,
		ChainID: fmt.Sprintf("%d", cfg.ChainID),
	}
	return &Driver{
		cfg:         cfg,
		logger:      logger,
		nid:         nid,
		key:         key,
		sender:      sender,
		trackedMap:  map[txmodel.TransactionID]*trackEntry{},
		transitions: map[string]*atomic.Int64{},
		lastHealth:  map[string]bool{},
		nodes:       map[string]*devchain.Node{},
	}, nil
}

// Metrics exposes the collector the driver publishes into; the HTTP
// layer serves it at /metrics.
func (d *Driver) Metrics() *memmetrics.Collector { return d.collector }

// Endpoints returns the endpoint pool with their fault injectors.
func (d *Driver) Endpoints() []Endpoint { return d.endpoints }

// AttachNode registers the fault injector for one self-hosted endpoint
// URL, so /api/faults can flip it live.
func (d *Driver) AttachNode(url string, node *devchain.Node) {
	d.nodes[url] = node
}

// faultNode returns the injector behind one URL, if any.
func (d *Driver) faultNode(url string) *devchain.Node { return d.nodes[url] }

// Network returns the logical network identity.
func (d *Driver) Network() identifiers.NetworkID { return d.nid }

// Config returns the effective configuration.
func (d *Driver) Config() Config { return d.cfg }

// Adapter exposes the started EVM adapter (health wiring in the HTTP
// layer).
func (d *Driver) Adapter() *evm.Adapter { return d.adapter }

// Run dials the endpoint pool, assembles the pipeline (adapter,
// in-memory repository, flow, recovery worker), drives the load until
// the duration or total is reached, then waits a grace period for the
// recovery worker to settle stragglers and returns the summary.
func (d *Driver) Run(ctx context.Context) (*Result, error) {
	d.collector = memmetrics.New()
	d.recorder = prometheus.NewRecorder(d.collector)

	ref, err := signer.NewAccountRef(d.nid, d.sender)
	if err != nil {
		return nil, fmt.Errorf("txload: %w", err)
	}
	d.signer, err = evm.NewLocalDevSigner("development", d.key, ref)
	if err != nil {
		return nil, fmt.Errorf("txload: %w", err)
	}
	defer func() { _ = d.signer.Close() }()

	d.evmMod = evm.New(evm.Config{Networks: []evm.NetworkConfig{{
		Name:           d.cfg.NetworkName,
		ChainID:        d.cfg.ChainID,
		Endpoints:      d.cfg.Endpoints,
		RequestTimeout: d.cfg.RequestTimeout,
	}}}, evm.WithLogger(slog.New(slog.DiscardHandler)))
	d.store = module.NewStore()
	reg := module.NewRegistry(d.store, "txload")
	if err := d.evmMod.Register(reg); err != nil {
		return nil, fmt.Errorf("txload: evm adapter config: %w", err)
	}
	if err := d.evmMod.Start(ctx); err != nil {
		return nil, fmt.Errorf("txload: endpoint pool: %w", err)
	}
	adapterAny, ok := d.store.Capability(capability.Network)
	if !ok {
		return nil, fmt.Errorf("txload: evm adapter published no network capability")
	}
	d.adapter = adapterAny.(*evm.Adapter)
	d.net, ok = d.adapter.Network(d.nid)
	if !ok {
		return nil, fmt.Errorf("txload: evm adapter has no network %s", d.nid)
	}

	d.repo = memstore.New()
	workerCfg := mergeWorker(d.cfg.Worker)
	txMod, err := transaction.NewModule(d.repo, d.net,
		transaction.WithTransactor(d.net, d.signer),
		transaction.WithWorkerConfig(workerCfg),
		transaction.WithWorkerMetrics(d.workerMetrics()),
		transaction.WithModuleLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		return nil, fmt.Errorf("txload: transaction pipeline: %w", err)
	}
	if err := txMod.Register(reg); err != nil {
		return nil, fmt.Errorf("txload: transaction pipeline: %w", err)
	}
	d.flow = txMod.Flow()
	if d.flow == nil {
		return nil, fmt.Errorf("txload: transaction pipeline published no flow")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, t := range d.store.Tasks() {
		go t.Task(runCtx)
	}

	// The pool keeps one client per configured endpoint; the driver's
	// fault injectors line up by URL.
	d.endpoints = make([]Endpoint, len(d.cfg.Endpoints))
	for i, url := range d.cfg.Endpoints {
		d.endpoints[i] = Endpoint{URL: url, Node: d.faultNode(url)}
	}

	d.startedAt = time.Now()
	res := &Result{
		StartedAt:      d.startedAt,
		PhaseCounts:    map[string]int64{},
		Transitions:    map[string]int64{},
		EndpointHealth: map[string]bool{},
		Submit:         &d.Submit,
		Confirm:        &d.Confirm,
		E2E:            &d.E2E,
	}

	// Nonce seed: an external endpoint may already hold account state; a
	// local mock starts at zero.
	if seed, err := d.net.PendingNonceAt(ctx, d.sender); err == nil {
		d.nonce.Store(seed)
	}

	var wg sync.WaitGroup
	tokens := make(chan struct{}, d.cfg.Concurrency)
	stopPacer := make(chan struct{})

	// Pacer: linear ramp from zero to TPS over cfg.Ramp, constant target
	// afterwards; emissions are integrated over paceTick so a ramp
	// changes the instantaneous rate smoothly.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(tokens)
		emissions := 0.0
		var scheduled int64
		for {
			select {
			case <-stopPacer:
				return
			case <-runCtx.Done():
				return
			case <-time.After(paceTick):
			}
			elapsed := time.Since(d.startedAt)
			target := d.cfg.TPS
			if d.cfg.Ramp > 0 && elapsed < d.cfg.Ramp {
				target = d.cfg.TPS * float64(elapsed) / float64(d.cfg.Ramp)
			}
			emissions += target * paceTick.Seconds()
			for emissions >= 1.0 {
				emissions -= 1.0
				if d.cfg.Total > 0 && scheduled >= d.cfg.Total {
					return
				}
				scheduled++
				select {
				case tokens <- struct{}{}:
				case <-stopPacer:
					return
				case <-runCtx.Done():
					return
				}
			}
		}
	}()

	// Senders: each token drives one simulated transfer through the
	// state machine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range tokens {
			if runCtx.Err() != nil {
				return
			}
			d.sendOne(runCtx)
		}
	}()

	// Watcher: polls tracked records for outcomes (inclusion, terminal
	// statuses, timeouts) and records latency samples.
	watchStop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchStop:
				return
			case <-ticker.C:
				d.watch()
			}
		}
	}()

	// Gauge refresh: state distribution and in-flight depth.
	gaugeStop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-gaugeStop:
				return
			case <-ticker.C:
				d.publishGauges()
			}
		}
	}()

	// Probe: endpoint health matrix over the pool.
	probeStop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(d.cfg.ProbeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-probeStop:
				return
			case <-ticker.C:
				d.probe()
			}
		}
	}()

	// Send-phase bound.
	if d.cfg.Duration > 0 || d.cfg.Total > 0 {
		var timer <-chan time.Time
		if d.cfg.Duration > 0 {
			t := time.NewTimer(d.cfg.Duration)
			defer t.Stop()
			timer = t.C
		}
		select {
		case <-timer:
		case <-ctx.Done():
		}
	} else {
		<-ctx.Done()
	}
	close(stopPacer)
	// The pacer closes the token channel on its way out; buffered sends
	// (at most the concurrency bound) drain into the senders.
	// Grace: let the recovery worker settle stragglers before counting.
	grace := 3 * workerCfg.MinStaleAge
	if grace < 2*time.Second {
		grace = 2 * time.Second
	}
	select {
	case <-time.After(grace):
	case <-ctx.Done():
	}
	close(watchStop)
	close(gaugeStop)
	close(probeStop)
	wg.Wait()
	d.watch() // one final pass for records resolved during teardown

	res.EndedAt = time.Now()
	res.Sent = d.sent.Load()
	res.Submitted = d.submitted.Load()
	res.SubmitFails = d.submitFails.Load()
	res.TimedOut = d.timedOut.Load()
	res.Succeeded = d.succeeded.Load()
	res.Reverted = d.reverted.Load()
	res.Failed = d.failed.Load()
	res.InflightEnd = res.Sent - res.SubmitFails - res.TimedOut - res.Succeeded - res.Reverted - res.Failed

	d.trackedMu.Lock()
	for _, id := range d.trackedIDs {
		tx, err := d.repo.Get(context.Background(), id)
		if err != nil {
			continue
		}
		res.PhaseCounts[string(tx.Status)]++
	}
	for key, ctr := range d.transitions {
		res.Transitions[key] = ctr.Load()
	}
	d.trackedMu.Unlock()
	for _, ep := range d.endpoints {
		d.lastHealthMu.Lock()
		res.EndpointHealth[ep.URL] = d.lastHealth[ep.URL]
		d.lastHealthMu.Unlock()
	}
	return res, nil
}

// Stop shuts the adapter's endpoints down.
func (d *Driver) Stop() {
	if d.evmMod != nil {
		_ = d.evmMod.Stop(context.Background())
	}
}

// mergeWorker fills zero worker fields with load-run pacing (the
// application package's defaults assume production 30s scans).
func mergeWorker(c transaction.Config) transaction.Config {
	if c.ScanInterval <= 0 {
		c.ScanInterval = 150 * time.Millisecond
	}
	if c.LeaseTTL <= 0 {
		c.LeaseTTL = time.Minute
	}
	if c.TaskTimeout <= 0 {
		c.TaskTimeout = 5 * time.Second
	}
	if c.MaxConcurrent < 1 {
		c.MaxConcurrent = 16
	}
	if c.BatchSize < 1 {
		c.BatchSize = 500
	}
	if c.MinStaleAge <= 0 {
		c.MinStaleAge = 100 * time.Millisecond
	}
	if len(c.StuckStatuses) == 0 {
		c.StuckStatuses = []txmodel.TransactionStatus{
			txmodel.TxPrepared,
			txmodel.TxSigned,
			txmodel.TxSubmitted,
			txmodel.TxUnknown,
			txmodel.TxReorged,
		}
	}
	return c
}

// sendOne opens one idempotency-keyed operation and drives it to
// submitted, retrying transient endpoint errors per the config. The
// record it leaves behind is the recovery worker's business.
func (d *Driver) sendOne(ctx context.Context) {
	id := txmodel.TransactionID(fmt.Sprintf("txload-%d", d.sent.Add(1)))
	entry := &trackEntry{createdAt: time.Now()}
	d.trackedMu.Lock()
	d.trackedIDs = append(d.trackedIDs, id)
	d.trackedMap[id] = entry
	d.trackedMu.Unlock()

	txCtx, cancel := context.WithTimeout(ctx, d.cfg.TxTimeout)
	defer cancel()

	nonce := d.nonce.Add(1) - 1
	recipient, err := identifiers.NewEVMAddress(common.BytesToAddress(d.recipient(id)).Hex())
	if err != nil {
		d.submitFails.Add(1)
		return
	}
	padding := d.padding()
	intent := &evm.BuildIntent{
		Sender:   d.sender,
		To:       recipient,
		Value:    "1",
		Data:     padding,
		GasLimit: 21_000 + uint64(len(padding))*16,
		Nonce:    &nonce,
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", id, recipient.String(), d.cfg.TxSize)))
	now := entry.createdAt
	seed := txmodel.Transaction{
		ID:          id,
		OperationID: txmodel.OperationID("op-" + string(id)),
		Network:     d.nid,
		Status:      txmodel.TxCreated,
		Sender:      d.sender,
		Recipient:   recipient,
		RequestHash: hex.EncodeToString(hash[:]),
		ChainData:   &evm.ChainRecord{Intent: intent},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	scope := txmodel.IdempotencyScope{Application: "txload", OperationType: "transfer"}
	started := time.Now()
	tx, err := d.flow.Begin(txCtx, scope, string(id), seed)
	if err != nil {
		// Below submission and never advanced: the entry is closed here,
		// so the watcher cannot count it a second time.
		entry.done.Store(true)
		if txCtx.Err() != nil {
			d.timedOut.Add(1)
			_ = d.recorder.TransactionTimeout(d.nid.String())
		} else {
			d.submitFails.Add(1)
		}
		return
	}
	for attempt := 0; attempt <= d.cfg.Retries; attempt++ {
		tx, err = d.flow.Drive(txCtx, tx.ID)
		if err == nil || txCtx.Err() != nil {
			break
		}
		select {
		case <-txCtx.Done():
		case <-time.After(d.cfg.RetryBackoff):
		}
	}
	// What counts is where the record honestly is, not what the last
	// Drive returned: the pool's failover or the recovery worker may
	// have advanced it already.
	stored, getErr := d.repo.Get(ctx, id)
	if getErr != nil {
		d.submitFails.Add(1)
		entry.done.Store(true)
		return
	}
	if !reachedSubmission(stored.Status) {
		// The send phase ended below submission. A decisive rejection
		// (signed→failed) is an outcome, not a submit failure; a send
		// timeout is counted here once, and the record — stuck below
		// submission, where the recovery worker never takes it — is
		// closed. Everything else is a submit failure.
		entry.done.Store(true)
		switch stored.Status {
		case txmodel.TxFailed, txmodel.TxReverted:
			d.failed.Add(1)
			_ = d.recorder.TransactionFailed(d.nid.String(), "failed")
			d.E2E.add(time.Since(entry.createdAt).Seconds())
		default:
			if txCtx.Err() != nil {
				d.timedOut.Add(1)
				_ = d.recorder.TransactionTimeout(d.nid.String())
			} else {
				d.submitFails.Add(1)
			}
		}
		return
	}
	d.submitted.Add(1)
	_ = d.recorder.TransactionSubmitted(d.nid.String())
	d.Submit.add(time.Since(started).Seconds())
	_ = d.collector.Histogram(MetricSubmitDuration).Observe(metrics.Labels{"network": d.nid.String()}, time.Since(started).Seconds())
}

func reachedSubmission(s txmodel.TransactionStatus) bool {
	switch s {
	case txmodel.TxSubmitted, txmodel.TxPending, txmodel.TxIncluded,
		txmodel.TxConfirmed, txmodel.TxFinalized, txmodel.TxReverted, txmodel.TxUnknown:
		return true
	}
	return false
}

// watch resolves tracked records against the store. The load's
// confirmation policy is inclusion — the adapter's receipt observation
// marks included; confirmed/finalized need an application confirmation
// policy this tool deliberately does not invent. Reverted and failed
// are terminal.
func (d *Driver) watch() {
	now := time.Now()
	d.trackedMu.Lock()
	defer d.trackedMu.Unlock()
	for _, id := range d.trackedIDs {
		entry := d.trackedMap[id]
		if entry.done.Load() {
			continue
		}
		tx, err := d.repo.Get(context.Background(), id)
		if err != nil {
			continue
		}
		switch tx.Status {
		case txmodel.TxIncluded, txmodel.TxConfirmed, txmodel.TxFinalized:
			entry.done.Store(true)
			d.succeeded.Add(1)
			e2e := now.Sub(entry.createdAt).Seconds()
			_ = d.recorder.TransactionConfirmed(d.nid.String(), e2e)
			d.Confirm.add(e2e)
			d.E2E.add(e2e)
			_ = d.collector.Histogram(MetricTxDuration).Observe(metrics.Labels{"network": d.nid.String()}, e2e)
		case txmodel.TxReverted:
			entry.done.Store(true)
			d.reverted.Add(1)
			_ = d.recorder.TransactionFailed(d.nid.String(), "reverted")
			e2e := now.Sub(entry.createdAt).Seconds()
			d.E2E.add(e2e)
			_ = d.collector.Histogram(MetricTxDuration).Observe(metrics.Labels{"network": d.nid.String()}, e2e)
		case txmodel.TxFailed:
			entry.done.Store(true)
			d.failed.Add(1)
			_ = d.recorder.TransactionFailed(d.nid.String(), "failed")
			e2e := now.Sub(entry.createdAt).Seconds()
			d.E2E.add(e2e)
			_ = d.collector.Histogram(MetricTxDuration).Observe(metrics.Labels{"network": d.nid.String()}, e2e)
		}
		if !entry.done.Load() && now.Sub(entry.createdAt) > d.cfg.TxTimeout*2 {
			entry.done.Store(true)
			d.timedOut.Add(1)
			_ = d.recorder.TransactionTimeout(d.nid.String())
		}
	}
}

// publishGauges refreshes the state distribution and in-flight gauges.
func (d *Driver) publishGauges() {
	d.trackedMu.Lock()
	counts := map[txmodel.TransactionStatus]int64{}
	for _, id := range d.trackedIDs {
		tx, err := d.repo.Get(context.Background(), id)
		if err != nil {
			continue
		}
		counts[tx.Status]++
	}
	d.trackedMu.Unlock()
	inflight := d.sent.Load() - d.succeeded.Load() - d.reverted.Load() - d.failed.Load() -
		d.timedOut.Load() - d.submitFails.Load()
	_ = d.collector.Gauge(MetricInflight).Set(nil, float64(inflight))
	for status, n := range counts {
		_ = d.collector.Gauge(metrics.MetricTransactionState).Set(metrics.Labels{"state": string(status)}, float64(n))
	}
}

// probe refreshes the endpoint health matrix with one chain-id read per
// endpoint, plus the adapter's failover counter.
func (d *Driver) probe() {
	fo := d.net.Stats().Failovers
	if delta := fo - d.lastFailover; delta > 0 {
		_ = d.collector.Counter(MetricEndpointFo).Add(metrics.Labels{"network": d.nid.String()}, float64(delta))
	}
	d.lastFailover = fo
	for _, ep := range d.endpoints {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), d.cfg.RequestTimeout)
		err := probeChainID(ctx, ep.URL)
		cancel()
		elapsed := time.Since(started).Seconds()
		_ = d.collector.Counter(metrics.MetricRPCRequests).Add(metrics.Labels{"endpoint": ep.URL, "method": "eth_chain_id"}, 1)
		_ = d.collector.Histogram(metrics.MetricRPCDuration).Observe(metrics.Labels{"endpoint": ep.URL}, elapsed)
		healthy := err == nil
		_ = d.collector.Gauge(metrics.MetricEndpointHealth).Set(metrics.Labels{"endpoint": ep.URL}, boolGauge(healthy))
		d.lastHealthMu.Lock()
		d.lastHealth[ep.URL] = healthy
		d.lastHealthMu.Unlock()
		if !healthy {
			_ = d.recorder.RPCError(d.nid.String(), "unavailable")
		}
	}
}

// workerMetrics maps the recovery loop's hooks into transition counters
// the run report and dashboard show.
func (d *Driver) workerMetrics() transaction.Metrics {
	return transaction.Metrics{
		Reconciled: func(_ txmodel.TransactionID, from, to txmodel.TransactionStatus) {
			d.countTransition(from, to)
		},
		Failed: func(_ txmodel.TransactionID, err error) {
			// Transient reconciliation errors are the worker's own retry
			// business (it re-scans the record on the next pass); the
			// fault-injected runs produce many by design.
			_ = err
		},
	}
}

func (d *Driver) countTransition(from, to txmodel.TransactionStatus) {
	key := string(from) + "->" + string(to)
	d.trackedMu.Lock()
	ctr := d.transitions[key]
	if ctr == nil {
		ctr = &atomic.Int64{}
		d.transitions[key] = ctr
	}
	d.trackedMu.Unlock()
	ctr.Add(1)
}

func (d *Driver) recipient(id txmodel.TransactionID) []byte {
	sum := sha256.Sum256([]byte("recipient:" + string(id)))
	return sum[:20]
}

func (d *Driver) padding() []byte {
	if d.cfg.TxSize <= 0 {
		return nil
	}
	out := make([]byte, d.cfg.TxSize)
	for i := range out {
		out[i] = byte(rand.IntN(256))
	}
	return out
}

func boolGauge(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// SummaryLine renders the one-line verdict main prints.
func (r *Result) SummaryLine() string {
	s50, s90, s99 := r.Submit.Percentiles()
	e50, e90, e99 := r.E2E.Percentiles()
	return fmt.Sprintf(
		"sent=%d submitted=%d succeeded=%d reverted=%d failed=%d submit-fails=%d timeouts=%d inflight=%d | submit p50/p90/p99=%.3fs/%.3fs/%.3fs | e2e p50/p90/p99=%.3fs/%.3fs/%.3fs",
		r.Sent, r.Submitted, r.Succeeded, r.Reverted, r.Failed, r.SubmitFails, r.TimedOut, r.InflightEnd,
		s50, s90, s99, e50, e90, e99)
}

// DetailBlock renders the phase and transition tables main prints.
func (r *Result) DetailBlock() string {
	var b strings.Builder
	b.WriteString("phase distribution:\n")
	statuses := make([]string, 0, len(r.PhaseCounts))
	for s := range r.PhaseCounts {
		statuses = append(statuses, s)
	}
	sort.Strings(statuses)
	for _, s := range statuses {
		fmt.Fprintf(&b, "  %-12s %d\n", s, r.PhaseCounts[s])
	}
	if len(r.Transitions) > 0 {
		b.WriteString("observed transitions:\n")
		keys := make([]string, 0, len(r.Transitions))
		for k := range r.Transitions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  %-24s %d\n", k, r.Transitions[k])
		}
	}
	return b.String()
}
