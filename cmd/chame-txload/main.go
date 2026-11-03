// Command chame-txload drives simulated transaction load through the
// real pipeline — the EVM adapter's endpoint pool, the shared
// transaction state machine, the idempotency-keyed flow, and the
// recovery worker — and serves a self-contained dashboard over the
// metrics it publishes.
//
// With no -endpoints the command hosts a local mock dev chain (two
// endpoints behind one chain state, the second carrying the injected
// fault profile), so the whole loop runs locally with zero external
// dependencies:
//
//	chame-txload -tps 20 -ramp 5s -duration 30s -drop-rate 0.1 -error-rate 0.2 -http 127.0.0.1:8090
//
// Point -endpoints at real or remote JSON-RPC endpoints to load-test
// them instead (fault injection then does not apply).
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cuihairu/chame/tools/txload"
	"github.com/cuihairu/chame/tools/txload/devchain"
)

// defaultDevKey is a published development-only key (anvil #0). The
// signer refuses production environments on its own (spec §12.4).
const defaultDevKey = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

func main() {
	var (
		endpoints   = flag.String("endpoints", "", "comma-separated RPC endpoint pool (empty: host a local mock dev chain)")
		network     = flag.String("network", "txload", "logical network name")
		chainID     = flag.Uint64("chain-id", 1337, "EIP-155 chain id")
		key         = flag.String("key", defaultDevKey, "development signer key (hex)")
		tps         = flag.Float64("tps", 10, "target transactions per second")
		ramp        = flag.Duration("ramp", 0, "linear ramp to -tps over this duration (0: constant rate)")
		duration    = flag.Duration("duration", 30*time.Second, "send-phase duration (0 with -count: run until interrupt)")
		count       = flag.Int64("count", 0, "total transactions (0: bound by -duration)")
		concurrency = flag.Int("concurrency", 8, "in-flight submit pipelines")
		txSize      = flag.Int("tx-size", 0, "calldata padding per transaction, bytes")
		timeout     = flag.Duration("timeout", 15*time.Second, "per-transaction submit timeout")
		retries     = flag.Int("retries", 2, "submit retry attempts on transient endpoint errors")
		backoff     = flag.Duration("backoff", 100*time.Millisecond, "submit retry backoff")
		httpAddr    = flag.String("http", "127.0.0.1:8090", "dashboard+metrics listen address (empty: disable)")
		blockTime   = flag.Duration("block-time", 200*time.Millisecond, "mock chain block interval (self-hosted)")
		errorRate   = flag.Float64("error-rate", 0, "mock endpoint JSON-RPC error probability (self-hosted)")
		dropRate    = flag.Float64("drop-rate", 0, "mock endpoint accepted-but-never-mined probability (self-hosted)")
		revertRate  = flag.Float64("revert-rate", 0, "mock chain mined-as-failed probability (self-hosted)")
		latencyMS   = flag.Int("latency-ms", 0, "mock endpoint added latency, ms (self-hosted)")
	)
	flag.Parse()

	cfg := txload.Config{
		NetworkName:    *network,
		ChainID:        *chainID,
		KeyHex:         *key,
		TPS:            *tps,
		Ramp:           *ramp,
		Duration:       *duration,
		Total:          *count,
		Concurrency:    *concurrency,
		TxSize:         *txSize,
		TxTimeout:      *timeout,
		Retries:        *retries,
		RetryBackoff:   *backoff,
		RequestTimeout: 5 * time.Second,
	}

	var chain *devchain.Chain
	faultNodes := map[string]*devchain.Node{}
	if *endpoints == "" {
		// Self-host: one chain state, two endpoints, both carrying the
		// fault profile (the pool's sticky cursor means a single faulty
		// endpoint goes quiet after the first failover). The down switch
		// stays per endpoint, so an outage still fails over cleanly.
		chain = devchain.NewChain(*chainID, *blockTime)
		defer chain.Stop()
		faults := devchain.Faults{
			ErrorRate:  *errorRate,
			DropRate:   *dropRate,
			RevertRate: *revertRate,
			LatencyMS:  *latencyMS,
		}
		nodes := []*devchain.Node{
			chain.NewNode(faults),
			chain.NewNode(faults),
		}
		for _, n := range nodes {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				failf("mock endpoint listen: %v", err)
			}
			url := "http://" + ln.Addr().String()
			srv := &http.Server{Handler: n}
			go func() { _ = srv.Serve(ln) }()
			cfg.Endpoints = append(cfg.Endpoints, url)
			faultNodes[url] = n
		}
		fmt.Printf("mock dev chain: chain %d, block interval %s\n", *chainID, *blockTime)
		fmt.Printf("endpoints: %s\n", cfg.Endpoints)
	} else {
		cfg.Endpoints = strings.Split(*endpoints, ",")
		for i := range cfg.Endpoints {
			cfg.Endpoints[i] = strings.TrimSpace(cfg.Endpoints[i])
		}
		fmt.Printf("endpoints: %s\n", cfg.Endpoints)
	}

	driver, err := txload.NewDriver(cfg)
	if err != nil {
		failf("%v", err)
	}
	if chain != nil {
		for url, node := range faultNodes {
			driver.AttachNode(url, node)
		}
	}

	// Dashboard + metrics live for the whole run.
	var srv *http.Server
	if *httpAddr != "" {
		srv = &http.Server{Handler: driver.Handler()}
		ln, err := net.Listen("tcp", *httpAddr)
		if err != nil {
			failf("dashboard listen: %v", err)
		}
		go func() { _ = srv.Serve(ln) }()
		fmt.Printf("dashboard: http://%s/  metrics: http://%s/metrics\n", ln.Addr(), ln.Addr())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("driving load: tps=%.1f ramp=%s duration=%s count=%d concurrency=%d tx-size=%d retries=%d\n",
		*tps, *ramp, *duration, *count, *concurrency, *txSize, *retries)
	res, err := driver.Run(ctx)
	if err != nil {
		failf("run: %v", err)
	}
	driver.Stop()
	if srv != nil {
		_ = srv.Close()
	}

	fmt.Println()
	fmt.Println("run summary")
	fmt.Printf("  window: %s .. %s (%.1fs)\n",
		res.StartedAt.Format(time.RFC3339), res.EndedAt.Format(time.RFC3339),
		res.EndedAt.Sub(res.StartedAt).Seconds())
	fmt.Printf("  %s\n", res.SummaryLine())
	fmt.Printf("  endpoint health: %s\n", healthLine(res))
	fmt.Println(res.DetailBlock())
}

func healthLine(res *txload.Result) string {
	out := ""
	for url, up := range res.EndpointHealth {
		mark := "down"
		if up {
			mark = "up"
		}
		if out != "" {
			out += "  "
		}
		out += fmt.Sprintf("%s=%s", url, mark)
	}
	return out
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "chame-txload: "+format+"\n", args...)
	os.Exit(1)
}

// NOTE: -http dashboard has no auth by design; bind to loopback only.
