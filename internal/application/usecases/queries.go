package usecases

import (
	"context"

	"github.com/claudioed/shipping-and-tracking/internal/application/views"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// GetJourney is the Open Host query GetJourney: the customer-visible timeline
// of one package. It reads the aggregate through the repository.
type GetJourney struct{ Deps }

// NewGetJourney builds the query.
func NewGetJourney(d Deps) *GetJourney { return &GetJourney{d} }

// Execute returns the journey, or ports.ErrNotFound.
func (q *GetJourney) Execute(ctx context.Context, tn shared.TrackingNumber) (views.Journey, error) {
	j, err := q.Journeys.Get(ctx, tn)
	if err != nil {
		return views.Journey{}, err
	}
	return views.JourneyFrom(j), nil
}
