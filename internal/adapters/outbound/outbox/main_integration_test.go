//go:build integration

package outbox_test

import (
	"os"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/testing/kafkatest"
	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}
