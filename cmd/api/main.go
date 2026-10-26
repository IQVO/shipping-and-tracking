// Command api is shipping-and-tracking's composition root: it wires env config
// into adapters, adapters into use cases, and use cases into the HTTP router,
// the four Kafka consumers, the outbox relay and the periodic jobs, all on real
// Postgres and real Kafka: there is no in-memory mode, no log sink and no
// consumer that is off.
//
// Environment (REQUIRED unless a default is shown):
//
//	HTTP_ADDR                   listen address (default :8080)
//	DATABASE_URL                Postgres DSN (REQUIRED)
//	MIGRATIONS_DATABASE_URL     direct DSN for the boot migrations (default DATABASE_URL)
//	KAFKA_BROKERS               comma-separated brokers (REQUIRED: relay, consumers, /readyz)
//	EVENT_PUBLISHER             kafka (default kafka); log is a boot error
//	CUSTOMER_ORDERING_URL       base URL of customer-ordering (REQUIRED: GetShipTo)
//	CUSTOMER_ORDERING_TIMEOUT   one GetShipTo call (default 3s)
//	SHIP_TO_ENCRYPTION_KEY      base64 of the 32-byte AES-256-GCM key (REQUIRED)
//	STOP_KEY_SECRET             HMAC secret of the stopKey, at least 16 bytes (REQUIRED)
//	VENDOR_CONSUMER_GROUP, LINEHAUL_CONSUMER_GROUP, LASTMILE_CONSUMER_GROUP, CATALOG_CONSUMER_GROUP
//	                            stable group ids (ALL REQUIRED, boot error naming the variable)
//	OUTBOX_RELAY_INTERVAL       relay poll interval (Go duration or seconds, default 1s)
//	OUTBOX_MAX_ATTEMPTS         failures before the relay excludes a row (default 20, 0 = unlimited)
//	SILENCE_SWEEP_INTERVAL      exception-on-silence sweep period (default 5m)
//	HOUSEKEEPING_INTERVAL       ship-to purge and technical-table purge period (default 1h)
//	SHUTDOWN_DRAIN_DELAY        wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL                   debug|info|warn|error (default info)
//	OTEL_SERVICE_NAME           service.name of spans and metrics (default shipping-and-tracking)
//	OTEL_EXPORTER_OTLP_ENDPOINT Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
//	SERVICE_VERSION             service.version (default dev)
//	ENVIRONMENT                 deployment.environment.name (default local)
//
// GET /readyz is 200 only while Postgres answers a ping AND a Kafka broker
// answers a metadata request (verdict cached 3s, bounded 2s); /healthz is
// liveness only. The process exits non-zero when the relay or a consumer
// returns on its own, so the orchestrator restarts it. No log line, event or
// response carries a ship-to address (ADR 0002 A9).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/clock"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/ids"
	outboundkafka "github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/kafka"
	outboxrelay "github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/outbox"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/telemetry"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/bootretry"
	"github.com/claudioed/shipping-and-tracking/internal/scheduler"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// workerDrainTimeout bounds how long shutdown waits for the consumers, the
// relay and the jobs to return after their context is cancelled.
const workerDrainTimeout = 10 * time.Second

func main() {
	if err := run(context.Background(), os.Getenv); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the service and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the integration test stops the real entry point that way). env is
// os.Getenv in production.
func run(ctx context.Context, env func(string) string) error {
	// The handler is wrapped so a record written with a span-carrying context
	// (the *Context slog variants) also carries trace_id and span_id.
	logger := slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: newLevel(env("LOG_LEVEL"))})))
	slog.SetDefault(logger)

	// Misconfiguration is refused before anything is opened or bound.
	cfg, err := loadConfig(env, logger)
	if err != nil {
		return err
	}
	// Telemetry comes up before the router is built (otelchimetric binds the
	// global MeterProvider at construction) and is flushed LAST: deferred before
	// the pool, so the final spans of the drain still leave the process.
	tel := loadTelemetryEnv(env)
	flushTelemetry, err := startTelemetry(logger, tel)
	if err != nil {
		return err
	}
	defer flushTelemetry()

	pool, err := openPool(ctx, cfg, logger)
	if err != nil {
		return err
	}
	// pool.Close is deferred FIRST so it runs LAST, after the HTTP drain, the
	// consumers, the jobs and the relay have all stopped.
	defer pool.Close()

	deps := buildDeps(pool, cfg)
	readiness := inboundhttp.NewReadiness(inboundhttp.DefaultReadinessTTL, inboundhttp.DefaultReadinessTimeout,
		inboundhttp.Dependency{Name: "postgres", Check: pool.Ping},
		inboundhttp.Dependency{Name: "kafka", Check: outboundkafka.NewBrokerCheck(cfg.brokers).Check},
	)
	httpServer := &http.Server{
		Addr:              cfg.httpAddr,
		Handler:           inboundhttp.NewRouter(buildServer(deps, pool, readiness), logger, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}

	workers := startWorkers(deps, pool, cfg, logger)
	return serveUntilSignal(ctx, logger, httpServer, readiness, cfg.drainDelay, workers)
}

// openPool runs the embedded migrations, opens the pool and pings it, retrying
// the very first dials with backoff.
func openPool(ctx context.Context, cfg config, logger *slog.Logger) (*pgxpool.Pool, error) {
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error { return postgres.RunMigrations(cfg.migrationsURL) }); err != nil {
		return nil, err
	}
	pool, err := postgres.NewPool(ctx, cfg.databaseURL)
	if err != nil {
		return nil, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return nil, err
	}
	logger.Info("postgres adapters configured")
	return pool, nil
}

// buildDeps wires the ports every use case needs.
func buildDeps(pool *pgxpool.Pool, cfg config) usecases.Deps {
	return usecases.Deps{
		Labels: postgres.NewLabelRepo(pool), Snapshots: postgres.NewSnapshotRepo(pool), Journeys: postgres.NewJourneyRepo(pool),
		Catalog: postgres.NewCatalogRepo(pool), Inbox: postgres.NewProcessedEventRepo(pool),
		Outbox: postgres.NewOutbox(postgres.NewOutboxRepo(pool), outboundkafka.NewEncoder()), UoW: postgres.NewUnitOfWork(pool),
		Clock: clock.System{}, IDs: ids.Random{}, ShipTo: cfg.shipTo, Sealer: cfg.sealer, StopKeys: cfg.stopKeys,
	}
}

func buildServer(d usecases.Deps, pool *pgxpool.Pool, readiness *inboundhttp.Readiness) *inboundhttp.Server {
	return &inboundhttp.Server{
		IssueLabel: usecases.NewIssueLabel(d), VoidLabel: usecases.NewVoidLabel(d),
		RecordScan: usecases.NewRecordScan(d), GetJourney: usecases.NewGetJourney(d),
		Idempotency: func(route string) func(http.Handler) http.Handler {
			return inboundhttp.RequireIdempotencyKey(pool, pgtx.With, route)
		},
		Readiness: readiness,
	}
}

// worker is a started background loop and the channel closed once it returned.
type worker struct {
	name string
	done chan struct{}
}

// workers are the background loops and the function that stops them.
type workers struct {
	consumers []*worker
	jobs      []*worker
	relay     *worker
	stopConsumers,
	stopJobs,
	stopRelay func()
}

// startWorkers starts every consumer, the periodic jobs and the outbox relay.
// The readers dial lazily inside Run, so a broker outage never blocks boot.
func startWorkers(d usecases.Deps, pool *pgxpool.Pool, cfg config, logger *slog.Logger) workers {
	var w workers
	w.consumers, w.stopConsumers = startConsumers(d, cfg, logger)
	w.jobs, w.stopJobs = startJobs(d, pool, cfg, logger)
	w.relay, w.stopRelay = startRelay(pool, cfg, logger)
	return w
}

func startConsumers(d usecases.Deps, cfg config, logger *slog.Logger) ([]*worker, func()) {
	applyScans := usecases.NewApplyScans(d)
	consumers := []*inboundkafka.Consumer{
		inboundkafka.NewVendorConsumer(cfg.brokers, cfg.groups[envVendorGroup], applyScans, logger),
		inboundkafka.NewLinehaulConsumer(cfg.brokers, cfg.groups[envLinehaulGroup], applyScans, logger),
		inboundkafka.NewLastMileConsumer(cfg.brokers, cfg.groups[envLastMileGroup], applyScans, usecases.NewReturnToSender(d), logger),
		inboundkafka.NewCatalogConsumer(cfg.brokers, cfg.groups[envCatalogGroup], usecases.NewRecordCatalogItem(d), logger),
	}
	ctx, stop := context.WithCancel(context.Background())
	var ws []*worker
	for _, c := range consumers {
		w := &worker{name: c.Name, done: make(chan struct{})}
		ws = append(ws, w)
		go func() {
			defer close(w.done)
			logger.Info("consumer running", "consumer", c.Name, "topic", c.Topic, "brokers", cfg.brokers)
			if err := c.Run(ctx); !errors.Is(err, context.Canceled) {
				logger.Error("consumer stopped", "consumer", c.Name, "error", err)
			}
		}()
	}
	return ws, func() {
		stop()
		for _, c := range consumers {
			_ = c.Close()
		}
	}
}

// startJobs starts the exception-on-silence sweep, the ship-to purge and the
// housekeeping of the technical tables.
func startJobs(d usecases.Deps, pool *pgxpool.Pool, cfg config, logger *slog.Logger) ([]*worker, func()) {
	sweep, purge, house := usecases.NewSweepSilence(d), usecases.NewPurgeShipTo(d), postgres.NewHousekeeping(pool)
	jobs := []scheduler.Job{
		{Name: "silence-sweep", Interval: cfg.sweepInterval, RunOnStart: true, Run: func(ctx context.Context) (string, error) {
			r, err := sweep.Execute(ctx)
			return fmt.Sprintf("raised=%d lost=%d conflicts=%d", r.Raised, r.Lost, r.Conflicts), err
		}},
		{Name: "ship-to-purge", Interval: cfg.housekeeping, RunOnStart: true, Run: func(ctx context.Context) (string, error) {
			n, err := purge.Execute(ctx)
			return fmt.Sprintf("snapshots_deleted=%d", n), err
		}},
		{Name: "housekeeping", Interval: cfg.housekeeping, RunOnStart: true, Run: func(ctx context.Context) (string, error) {
			return housekeep(ctx, house, d.Clock.Now())
		}},
	}
	ctx, stop := context.WithCancel(context.Background())
	var ws []*worker
	for _, j := range jobs {
		w := &worker{name: j.Name, done: make(chan struct{})}
		ws = append(ws, w)
		go func() {
			defer close(w.done)
			scheduler.Run(ctx, logger, j)
		}()
	}
	return ws, stop
}

// housekeep purges the technical tables and summarizes the counts.
func housekeep(ctx context.Context, h *postgres.Housekeeping, now time.Time) (string, error) {
	keys, err := h.PurgeIdempotencyKeys(ctx, now.Add(-idempotencyRetention))
	if err != nil {
		return "", err
	}
	rows, err := h.PurgeRelayedOutbox(ctx, now.Add(-outboxRetention))
	if err != nil {
		return "", err
	}
	claims, err := h.PurgeProcessedEvents(ctx, now.Add(-inboxRetention))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("idempotency_keys=%d outbox_events=%d processed_events=%d", keys, rows, claims), nil
}

// startRelay starts the always-on relay goroutine over the Kafka sink. Kafka is
// dialled lazily on the first send, never here.
func startRelay(pool *pgxpool.Pool, cfg config, logger *slog.Logger) (*worker, func()) {
	sink := outboundkafka.NewRelaySink(cfg.brokers)
	relay := outboxrelay.NewRelay(postgres.NewOutboxRepo(pool), sink, logger,
		outboxrelay.WithInterval(cfg.relayInterval), outboxrelay.WithMaxAttempts(cfg.relayMaxAttempt))
	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: "outbox-relay", done: make(chan struct{})}
	go func() {
		defer close(w.done)
		defer func() {
			if err := sink.Close(); err != nil {
				logger.Error("kafka relay sink close failed", "error", err)
			}
		}()
		logger.Info("outbox relay running", "publisher", "kafka", "interval", cfg.relayInterval, "max_attempts", cfg.relayMaxAttempt, "topic", outboundkafka.Topic)
		if err := relay.Run(ctx); !errors.Is(err, context.Canceled) {
			logger.Error("outbox relay stopped", "error", err)
		}
	}()
	return w, stop
}

// serveUntilSignal runs httpServer until SIGINT/SIGTERM (or a listen error),
// then shuts down in the fleet order: (1) /readyz flips to 503, (2) wait the
// drain delay, (3) drain HTTP, (4) stop and await the consumers and the jobs,
// (5) stop and await the relay LAST so nothing committed above is stranded; the
// caller's deferred pool.Close runs after all of it.
func serveUntilSignal(ctx context.Context, logger *slog.Logger, httpServer *http.Server, readiness *inboundhttp.Readiness, drain time.Duration, w workers) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	required := append(append([]*worker{w.relay}, w.consumers...), w.jobs...)
	died := watchWorkers(required...)
	select {
	case err := <-errCh:
		stopAll(w)
		return err
	case name := <-died:
		// A consumer, a job loop or the relay returned on its own: the service
		// cannot run without it and must not sit "alive" with a dead worker, so
		// exit non-zero and let the orchestrator restart it.
		stopAll(w)
		return fmt.Errorf("%s stopped unexpectedly: exiting so the orchestrator restarts the service", name)
	case <-sigCtx.Done():
	}

	readiness.SetNotReady()
	if drain > 0 {
		logger.Info("shutdown: readiness flipped to not-ready; waiting for traffic to drain", "drain_delay", drain.String())
		time.Sleep(drain)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := httpServer.Shutdown(shutdownCtx)

	w.stopConsumers()
	w.stopJobs()
	for _, x := range append(append([]*worker{}, w.consumers...), w.jobs...) {
		await(logger, x)
	}
	w.stopRelay()
	await(logger, w.relay)
	return err
}

func stopAll(w workers) {
	w.stopConsumers()
	w.stopJobs()
	w.stopRelay()
}

// watchWorkers returns a channel that receives the name of every worker that
// returns. Workers only return when stopped or on an unrecoverable failure, so
// a value read while the service is serving means a worker died.
func watchWorkers(ws ...*worker) <-chan string {
	died := make(chan string, len(ws))
	for _, w := range ws {
		go func() {
			<-w.done
			died <- w.name
		}()
	}
	return died
}

// await waits (bounded) for w to return.
func await(logger *slog.Logger, w *worker) {
	select {
	case <-w.done:
	case <-time.After(workerDrainTimeout):
		logger.Warn("worker did not stop before the shutdown drain deadline", "worker", w.name)
	}
}
