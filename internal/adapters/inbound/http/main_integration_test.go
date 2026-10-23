//go:build integration

package http_test

import (
	"os"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}
