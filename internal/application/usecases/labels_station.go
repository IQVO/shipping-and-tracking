package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
)

// destinationStation is the delivery station the local area view holds for
// the destination postal prefix, or "" when the view has no row for the prefix
// or its row names no station (ADR 0019): the label is issued all the same and
// LabelIssued omits destinationStationNodeId. Only an infrastructure failure of
// the view is an error. It runs before the transaction opens, like the
// ship-to read, and reads this context's own copy: no sibling is called.
func (w writer) destinationStation(ctx context.Context, prefix string) (string, error) {
	if w.AreaModes == nil {
		return "", nil
	}
	area, err := w.AreaModes.Get(ctx, prefix)
	if errors.Is(err, ports.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return area.DeliveryStationNodeID, nil
}
