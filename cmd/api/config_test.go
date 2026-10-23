package main

import (
	"encoding/base64"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func goodEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://u:p@h/db", "KAFKA_BROKERS": "b1:9092, b2:9092",
		"CUSTOMER_ORDERING_URL": "http://customer-ordering:8080", "SHIP_TO_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"STOP_KEY_SECRET": "0123456789abcdef", "VENDOR_CONSUMER_GROUP": "v", "LINEHAUL_CONSUMER_GROUP": "l",
		"LASTMILE_CONSUMER_GROUP": "m", "CATALOG_CONSUMER_GROUP": "c",
	}
}

func load(env map[string]string) (config, error) {
	return loadConfig(func(k string) string { return env[k] }, quiet)
}

func TestAGoodEnvironmentLoadsWithItsDefaults(t *testing.T) {
	c, err := load(goodEnv())
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"http address", c.httpAddr == ":8080"},
		{"migrations dsn defaults to the database dsn", c.migrationsURL == c.databaseURL},
		{"brokers are split and trimmed", len(c.brokers) == 2 && c.brokers[1] == "b2:9092"},
		{"relay attempts", c.relayMaxAttempt == 20},
		{"relay interval", c.relayInterval == time.Second},
		{"sweep interval", c.sweepInterval == 5*time.Minute},
		{"housekeeping interval", c.housekeeping == time.Hour},
		{"drain delay", c.drainDelay == 5*time.Second},
		{"sealer", c.sealer != nil},
		{"keyer", c.stopKeys != nil},
		{"gateway", c.shipTo != nil},
		{"groups", c.groups[envVendorGroup] == "v" && c.groups[envCatalogGroup] == "c"},
	}
	for _, ck := range checks {
		if !ck.ok {
			t.Errorf("%s is wrong in %+v", ck.name, c)
		}
	}
}

func TestEveryMissingOrBadVariableIsABootErrorNamingIt(t *testing.T) {
	cases := []struct {
		name, key, value, want string
	}{
		{"database", "DATABASE_URL", "", "DATABASE_URL"},
		{"brokers", "KAFKA_BROKERS", " , ", "KAFKA_BROKERS"},
		{"publisher log", "EVENT_PUBLISHER", "log", "EVENT_PUBLISHER=log"},
		{"publisher unknown", "EVENT_PUBLISHER", "pulsar", "EVENT_PUBLISHER"},
		{"vendor group", "VENDOR_CONSUMER_GROUP", "", "VENDOR_CONSUMER_GROUP"},
		{"linehaul group", "LINEHAUL_CONSUMER_GROUP", "", "LINEHAUL_CONSUMER_GROUP"},
		{"last-mile group", "LASTMILE_CONSUMER_GROUP", "", "LASTMILE_CONSUMER_GROUP"},
		{"catalog group", "CATALOG_CONSUMER_GROUP", "", "CATALOG_CONSUMER_GROUP"},
		{"no encryption key", "SHIP_TO_ENCRYPTION_KEY", "", "SHIP_TO_ENCRYPTION_KEY"},
		{"short encryption key", "SHIP_TO_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)), "SHIP_TO_ENCRYPTION_KEY"},
		{"no stop secret", "STOP_KEY_SECRET", "", "STOP_KEY_SECRET"},
		{"short stop secret", "STOP_KEY_SECRET", "short", "STOP_KEY_SECRET"},
		{"no customer-ordering", "CUSTOMER_ORDERING_URL", "", "CUSTOMER_ORDERING_URL"},
		{"relative customer-ordering", "CUSTOMER_ORDERING_URL", "customer-ordering", "CUSTOMER_ORDERING_URL"},
		{"bad timeout", "CUSTOMER_ORDERING_TIMEOUT", "soon", "CUSTOMER_ORDERING_TIMEOUT"},
		{"negative timeout", "CUSTOMER_ORDERING_TIMEOUT", "-1s", "CUSTOMER_ORDERING_TIMEOUT"},
	}
	for _, c := range cases {
		env := goodEnv()
		env[c.key] = c.value
		_, err := load(env)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one naming %s", c.name, err, c.want)
		}
	}
}

func TestASecretIsNeverPartOfABootError(t *testing.T) {
	env := goodEnv()
	env["STOP_KEY_SECRET"] = "tooshort-secret"[:5]
	env["SHIP_TO_ENCRYPTION_KEY"] = "not-base64-%%%"
	for _, key := range []string{"STOP_KEY_SECRET", "SHIP_TO_ENCRYPTION_KEY"} {
		e := goodEnv()
		e[key] = env[key]
		if _, err := load(e); err == nil || strings.Contains(err.Error(), env[key]) {
			t.Errorf("%s: error %v must not echo the value", key, err)
		}
	}
}

func TestOptionalSettingsAndTheirFallbacks(t *testing.T) {
	env := goodEnv()
	env["HTTP_ADDR"] = ":9090"
	env["MIGRATIONS_DATABASE_URL"] = "postgres://direct"
	env["OUTBOX_RELAY_INTERVAL"] = "250ms"
	env["OUTBOX_MAX_ATTEMPTS"] = "0"
	env["SILENCE_SWEEP_INTERVAL"] = "30"
	env["HOUSEKEEPING_INTERVAL"] = "2h"
	env["SHUTDOWN_DRAIN_DELAY"] = "0"
	env["CUSTOMER_ORDERING_TIMEOUT"] = "1.5"
	c, err := load(env)
	if err != nil {
		t.Fatal(err)
	}
	if c.httpAddr != ":9090" || c.migrationsURL != "postgres://direct" || c.relayInterval != 250*time.Millisecond || c.relayMaxAttempt != 0 ||
		c.sweepInterval != 30*time.Second || c.housekeeping != 2*time.Hour || c.drainDelay != 0 {
		t.Fatalf("config = %+v", c)
	}

	bad := goodEnv()
	bad["OUTBOX_RELAY_INTERVAL"] = "fast"
	bad["OUTBOX_MAX_ATTEMPTS"] = "-3"
	bad["SILENCE_SWEEP_INTERVAL"] = "0"
	bad["HOUSEKEEPING_INTERVAL"] = "-1h"
	bad["SHUTDOWN_DRAIN_DELAY"] = "-5s"
	c, err = load(bad)
	if err != nil {
		t.Fatal(err)
	}
	if c.relayInterval != time.Second || c.relayMaxAttempt != 20 || c.sweepInterval != 5*time.Minute || c.housekeeping != time.Hour || c.drainDelay != 5*time.Second {
		t.Fatalf("a bad optional value must fall back to its default, got %+v", c)
	}
}

func TestLogLevels(t *testing.T) {
	for in, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "x": slog.LevelInfo} {
		if got := newLevel(in); got != want {
			t.Errorf("%q = %v, want %v", in, got, want)
		}
	}
}
