package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/crypto"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/customerordering"
	outboxrelay "github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/outbox"
)

// Defaults of the optional variables.
const (
	defaultHTTPAddr           = ":8080"
	defaultSweepInterval      = 5 * time.Minute
	defaultHousekeepingPeriod = time.Hour
	defaultDrainDelay         = 5 * time.Second
)

// Retention of the technical tables (ADR 0005).
const (
	idempotencyRetention = 24 * time.Hour
	outboxRetention      = 7 * 24 * time.Hour
	inboxRetention       = 30 * 24 * time.Hour
)

// Consumer-group variables: every one is mandatory, a missing one is a boot
// error naming it.
const (
	envVendorGroup   = "VENDOR_CONSUMER_GROUP"
	envLinehaulGroup = "LINEHAUL_CONSUMER_GROUP"
	envLastMileGroup = "LASTMILE_CONSUMER_GROUP"
	envCatalogGroup  = "CATALOG_CONSUMER_GROUP"
)

var consumerGroupVars = []string{envVendorGroup, envLinehaulGroup, envLastMileGroup, envCatalogGroup}

// Boot errors for the variables the context cannot run without. Each message
// names the variable.
var (
	errMissingDatabaseURL = errors.New("DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode")
	errMissingBrokers     = errors.New("KAFKA_BROKERS is required: this context runs on real Kafka (outbox relay, consumers, /readyz)")
)

// config is the validated environment.
type config struct {
	httpAddr        string
	databaseURL     string
	migrationsURL   string
	brokers         []string
	groups          map[string]string
	sealer          *crypto.Sealer
	stopKeys        *crypto.StopKeyer
	shipTo          *customerordering.Gateway
	relayInterval   time.Duration
	relayMaxAttempt int
	sweepInterval   time.Duration
	housekeeping    time.Duration
	drainDelay      time.Duration
}

// loadConfig validates the environment before anything is opened or bound.
// Every error names the variable; a secret value is never part of a message.
func loadConfig(env func(string) string, logger *slog.Logger) (config, error) {
	c := config{
		httpAddr:    orDefault(env("HTTP_ADDR"), defaultHTTPAddr),
		databaseURL: env("DATABASE_URL"),
		groups:      map[string]string{},
	}
	if c.databaseURL == "" {
		return config{}, errMissingDatabaseURL
	}
	c.migrationsURL = orDefault(env("MIGRATIONS_DATABASE_URL"), c.databaseURL)
	if c.brokers = splitList(env("KAFKA_BROKERS")); len(c.brokers) == 0 {
		return config{}, errMissingBrokers
	}
	if err := parsePublisherMode(env("EVENT_PUBLISHER")); err != nil {
		return config{}, err
	}
	for _, name := range consumerGroupVars {
		if c.groups[name] = env(name); c.groups[name] == "" {
			return config{}, fmt.Errorf("%s is required: its consumer always runs on real Kafka", name)
		}
	}
	if err := c.loadSecrets(env); err != nil {
		return config{}, err
	}
	if err := c.loadGateway(env); err != nil {
		return config{}, err
	}
	c.relayInterval = parseDuration("OUTBOX_RELAY_INTERVAL", env("OUTBOX_RELAY_INTERVAL"), outboxrelay.DefaultInterval, logger)
	c.relayMaxAttempt = parseMaxAttempts(env("OUTBOX_MAX_ATTEMPTS"), logger)
	c.sweepInterval = parseDuration("SILENCE_SWEEP_INTERVAL", env("SILENCE_SWEEP_INTERVAL"), defaultSweepInterval, logger)
	c.housekeeping = parseDuration("HOUSEKEEPING_INTERVAL", env("HOUSEKEEPING_INTERVAL"), defaultHousekeepingPeriod, logger)
	c.drainDelay = parseDrainDelay(env("SHUTDOWN_DRAIN_DELAY"), logger)
	return c, nil
}

// loadSecrets builds the sealer (SHIP_TO_ENCRYPTION_KEY, base64 of 32 bytes)
// and the stopKey keyer (STOP_KEY_SECRET, at least 16 bytes).
func (c *config) loadSecrets(env func(string) string) error {
	rawKey := env("SHIP_TO_ENCRYPTION_KEY")
	if rawKey == "" {
		return errors.New("SHIP_TO_ENCRYPTION_KEY is required: the ship-to snapshot is always encrypted (base64 of 32 bytes)")
	}
	key, err := crypto.DecodeKey(rawKey)
	if err != nil {
		return fmt.Errorf("SHIP_TO_ENCRYPTION_KEY: %w", err)
	}
	if c.sealer, err = crypto.NewSealer(key); err != nil {
		return fmt.Errorf("SHIP_TO_ENCRYPTION_KEY: %w", err)
	}
	secret := env("STOP_KEY_SECRET")
	if secret == "" {
		return errors.New("STOP_KEY_SECRET is required: the stopKey is a keyed hash (at least 16 bytes)")
	}
	if c.stopKeys, err = crypto.NewStopKeyer([]byte(secret)); err != nil {
		return fmt.Errorf("STOP_KEY_SECRET: %w", err)
	}
	return nil
}

// loadGateway builds the GetShipTo gateway (CUSTOMER_ORDERING_URL).
func (c *config) loadGateway(env func(string) string) error {
	base := env("CUSTOMER_ORDERING_URL")
	if base == "" {
		return errors.New("CUSTOMER_ORDERING_URL is required: labels need the ship-to of customer-ordering")
	}
	timeout, err := parseOptionalDuration("CUSTOMER_ORDERING_TIMEOUT", env("CUSTOMER_ORDERING_TIMEOUT"))
	if err != nil {
		return err
	}
	if c.shipTo, err = customerordering.New(base, timeout); err != nil {
		return err
	}
	return nil
}

func orDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func splitList(raw string) []string {
	var out []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

// parsePublisherMode validates EVENT_PUBLISHER: unset or "kafka". There is no
// log-only sink; the variable stays because the deployment sets it.
func parsePublisherMode(raw string) error {
	switch raw {
	case "", "kafka":
		return nil
	case "log":
		return errors.New("EVENT_PUBLISHER=log is not supported: this context publishes to real Kafka")
	default:
		return fmt.Errorf("EVENT_PUBLISHER: unknown value %q (want kafka)", raw)
	}
}

// parseOptionalDuration reads a Go duration or plain seconds; empty is zero
// (the caller's default) and a malformed value is a boot error.
func parseOptionalDuration(name, raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	d, ok := parseDurationValue(raw)
	if !ok || d <= 0 {
		return 0, fmt.Errorf("%s: %q is not a positive duration (Go duration or seconds)", name, raw)
	}
	return d, nil
}

func parseDurationValue(raw string) (time.Duration, bool) {
	if d, err := time.ParseDuration(raw); err == nil {
		return d, true
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil {
		return time.Duration(secs * float64(time.Second)), true
	}
	return 0, false
}

// parseDuration reads a positive duration, falling back (with a warning) when
// unset, malformed or non-positive.
func parseDuration(name, raw string, fallback time.Duration, logger *slog.Logger) time.Duration {
	if raw == "" {
		return fallback
	}
	if d, ok := parseDurationValue(raw); ok && d > 0 {
		return d
	}
	logger.Warn("invalid duration; using the default", "variable", name, "value", raw, "default", fallback.String())
	return fallback
}

// parseMaxAttempts reads OUTBOX_MAX_ATTEMPTS: unset = the fleet default, 0 =
// unlimited; negative or unparsable values use the default so a typo never
// silently removes the cap.
func parseMaxAttempts(raw string, logger *slog.Logger) int {
	if raw == "" {
		return outboxrelay.DefaultMaxAttempts
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		logger.Warn("invalid OUTBOX_MAX_ATTEMPTS; using the default", "value", raw, "default", outboxrelay.DefaultMaxAttempts)
		return outboxrelay.DefaultMaxAttempts
	}
	return n
}

// parseDrainDelay reads SHUTDOWN_DRAIN_DELAY ("0" disables; unset, negative or
// unparsable = the default).
func parseDrainDelay(raw string, logger *slog.Logger) time.Duration {
	if raw == "" {
		return defaultDrainDelay
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		logger.Warn("ignoring invalid SHUTDOWN_DRAIN_DELAY", "value", raw, "default", defaultDrainDelay.String())
		return defaultDrainDelay
	}
	return d
}

// newLevel maps LOG_LEVEL debug|info|warn|warning|error.
func newLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
