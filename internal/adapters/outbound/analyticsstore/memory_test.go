package analyticsstore_test

import (
	"errors"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/shipping-and-tracking/internal/analytics/report"
)

func TestMemoryStoreContract(t *testing.T) {
	runContract(t, func(*testing.T) store { return analyticsstore.NewMemoryStore() })
}

func TestMemoryStoreFailIsReturnedOnce(t *testing.T) {
	m := analyticsstore.NewMemoryStore()
	boom := errors.New("database down")
	m.Fail = boom
	e := report.Event{Kind: report.KindPackageLost, EventID: "e", TrackingNumber: "A", At: nov3, OccurredAt: nov3}
	if _, err := m.Apply(ctx, e); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the injected failure", err)
	}
	if applied, err := m.Apply(ctx, e); err != nil || !applied {
		t.Fatalf("the retry must apply: %v %v", applied, err)
	}
	if err := m.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}
