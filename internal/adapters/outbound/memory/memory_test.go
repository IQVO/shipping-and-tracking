package memory_test

import (
	"context"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/repocontract"
)

// TestMemoryDoublesPassTheRepositoryContract runs the same suite the Postgres
// adapters pass in the integration tests.
func TestMemoryDoublesPassTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Fixture {
		s := memory.NewStore()
		return repocontract.Fixture{
			Labels: s.Labels(), Snapshots: s.Snapshots(), Journeys: s.Journeys(), Catalog: s.Catalog(), Inbox: s.Inbox(),
			Outbox: s.Outbox(), UoW: s.UnitOfWork(),
			OutboxCount: func(context.Context) (int, error) { return len(s.Outbox().Events()), nil },
		}
	})
}
