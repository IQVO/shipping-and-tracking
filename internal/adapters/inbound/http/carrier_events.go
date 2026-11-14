package http

import (
	"net/http"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// carrierEventRequest is the body of POST /carrier-events (ADR 0015). It is
// deliberately unauthenticated, exactly like the scans route.
type carrierEventRequest struct {
	CarrierID      *string    `json:"carrierId"`
	CarrierEventID *string    `json:"carrierEventId"`
	TrackingNumber *string    `json:"trackingNumber"`
	Status         *string    `json:"status"`
	At             *time.Time `json:"at"`
	ProofCode      *string    `json:"proofCode"`
	LocationCode   *string    `json:"locationCode"`
}

// maxProofLen bounds the optional proof and location codes.
const maxProofLen = 128

func (q carrierEventRequest) command() (usecases.CarrierEventCmd, error) {
	cmd := usecases.CarrierEventCmd{}
	var err error
	if cmd.CarrierID, err = text("carrierId", q.CarrierID, maxFieldLen); err != nil {
		return cmd, err
	}
	if cmd.CarrierEventID, err = text("carrierEventId", q.CarrierEventID, shared.MaxScanID/2); err != nil {
		return cmd, err
	}
	tn, err := text("trackingNumber", q.TrackingNumber, maxFieldLen)
	if err != nil {
		return cmd, err
	}
	cmd.TrackingNumber = shared.TrackingNumber(tn)
	status, err := text("status", q.Status, maxFieldLen)
	if err != nil {
		return cmd, err
	}
	cmd.Status = status
	if q.At == nil || q.At.IsZero() {
		return cmd, validationError("at is required")
	}
	cmd.At = q.At.UTC()
	if q.ProofCode != nil && *q.ProofCode != "" {
		if cmd.ProofCode, err = text("proofCode", q.ProofCode, maxProofLen); err != nil {
			return cmd, err
		}
	}
	if q.LocationCode != nil && *q.LocationCode != "" {
		if cmd.LocationCode, err = text("locationCode", q.LocationCode, maxProofLen); err != nil {
			return cmd, err
		}
	}
	return cmd, nil
}

func (s *Server) handleCarrierEvent(w http.ResponseWriter, r *http.Request) {
	var req carrierEventRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	cmd, err := req.command()
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.ReportCarrierEvent.Execute(r.Context(), cmd)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
