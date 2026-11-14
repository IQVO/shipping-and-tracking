package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

// labelPrefix returns the label's destination postal prefix of a journey.
func (w writer) labelPrefix(ctx context.Context, j *journey.Journey) (string, error) {
	l, err := w.Labels.Get(ctx, label.Ref(j.LabelRef()))
	if err != nil {
		return "", err
	}
	return l.DestinationPostalPrefix(), nil
}

// isCarrierArea reports whether the area read model knows the prefix as
// CARRIER_HANDOFF. An unknown prefix is the pre-C4 world: NETWORK_ROUTE.
func (w writer) isCarrierArea(ctx context.Context, prefix string) (bool, error) {
	if w.AreaModes == nil {
		return false, nil
	}
	area, err := w.AreaModes.Get(ctx, prefix)
	if errors.Is(err, ports.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return area.DeliveryMode == repository.DeliveryModeCarrierHandoff, nil
}

// noteCarrierChoice computes the deterministic carrier choice over the CURRENT
// area view for a journey that just arrived at a station of a CARRIER_HANDOFF
// area, and stores it on the journey (ADR 0015). Every other journey is
// untouched. It runs inside the caller's unit of work.
func (w writer) noteCarrierChoice(ctx context.Context, j *journey.Journey) error {
	if w.AreaModes == nil || j.State() != journey.StateAtStation {
		return nil
	}
	prefix, err := w.labelPrefix(ctx, j)
	if err != nil {
		return err
	}
	area, err := w.AreaModes.Get(ctx, prefix)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if area.DeliveryMode != repository.DeliveryModeCarrierHandoff {
		return nil
	}
	choice, ok := journey.ChooseCarrier(area.Carriers)
	if !ok {
		return nil
	}
	return j.NoteCarrierChoice(choice.CarrierID, choice.HandoffNodeID, area.Version)
}
