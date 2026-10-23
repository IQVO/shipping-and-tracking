package http

import (
	"fmt"
	"net/http"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// Request bodies. Field bounds repeat apis/openapi.yaml; a violation is a
// 422 validation-failed (the JSON is well-formed, the value is not).
type issueLabelRequest struct {
	PONumber     *string `json:"poNumber"`
	PackageNo    *int    `json:"packageNo"`
	AssignmentID *string `json:"assignmentId"`
	OrderID      *string `json:"orderId"`
	Tier         *string `json:"tier"`
}

type voidLabelRequest struct {
	Reason *string `json:"reason"`
}

type recordScanRequest struct {
	ScanID   *string    `json:"scanId"`
	NodeID   *string    `json:"nodeId"`
	ScanType *string    `json:"scanType"`
	At       *time.Time `json:"at"`
}

const (
	maxFieldLen  = 64
	maxReasonLen = 500
	maxInt32     = 2147483647
)

// validationError wraps a message as shared.ErrValidation.
func validationError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", shared.ErrValidation, fmt.Sprintf(format, args...))
}

// text checks one required string field against 1..max characters.
func text(name string, v *string, max int) (string, error) {
	switch {
	case v == nil:
		return "", validationError("%s is required", name)
	case *v == "":
		return "", validationError("%s must not be empty", name)
	case len(*v) > max:
		return "", validationError("%s is longer than %d characters", name, max)
	}
	return *v, nil
}

func (s *Server) handleIssueLabel(w http.ResponseWriter, r *http.Request) {
	var req issueLabelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	cmd, err := req.command()
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.IssueLabel.Execute(r.Context(), cmd)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (q issueLabelRequest) command() (usecases.IssueLabelCmd, error) {
	var cmd usecases.IssueLabelCmd
	var err error
	if cmd.PONumber, err = text("poNumber", q.PONumber, maxFieldLen); err != nil {
		return cmd, err
	}
	if cmd.AssignmentID, err = text("assignmentId", q.AssignmentID, maxFieldLen); err != nil {
		return cmd, err
	}
	if cmd.OrderID, err = text("orderId", q.OrderID, maxFieldLen); err != nil {
		return cmd, err
	}
	tier, err := text("tier", q.Tier, maxFieldLen)
	if err != nil {
		return cmd, err
	}
	cmd.Tier = shared.Tier(tier)
	if !cmd.Tier.Valid() {
		return cmd, validationError("tier %q is not one of SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD", tier)
	}
	if q.PackageNo == nil || *q.PackageNo < 1 || *q.PackageNo > maxInt32 {
		return cmd, validationError("packageNo must be between 1 and %d", maxInt32)
	}
	cmd.PackageNo = *q.PackageNo
	return cmd, nil
}

func (s *Server) handleVoidLabel(w http.ResponseWriter, r *http.Request) {
	var req voidLabelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	reason, err := text("reason", req.Reason, maxReasonLen)
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.VoidLabel.Execute(r.Context(), label.Ref(pathParam(r, "labelRef")), reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleGetJourney(w http.ResponseWriter, r *http.Request) {
	v, err := s.GetJourney.Execute(r.Context(), shared.TrackingNumber(pathParam(r, "trackingNumber")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleRecordScan(w http.ResponseWriter, r *http.Request) {
	var req recordScanRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	cmd, err := req.command(shared.TrackingNumber(pathParam(r, "trackingNumber")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.RecordScan.Execute(r.Context(), cmd)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (q recordScanRequest) command(tn shared.TrackingNumber) (usecases.RecordScanCmd, error) {
	cmd := usecases.RecordScanCmd{TrackingNumber: tn}
	var err error
	if cmd.ScanID, err = text("scanId", q.ScanID, shared.MaxScanID); err != nil {
		return cmd, err
	}
	if cmd.NodeID, err = text("nodeId", q.NodeID, maxFieldLen); err != nil {
		return cmd, err
	}
	typ, err := text("scanType", q.ScanType, maxFieldLen)
	if err != nil {
		return cmd, err
	}
	cmd.Type = journey.ScanType(typ)
	if !cmd.Type.Valid() {
		return cmd, validationError("scanType %q is not a known scan type", typ)
	}
	if q.At == nil || q.At.IsZero() {
		return cmd, validationError("at is required")
	}
	cmd.At = q.At.UTC()
	return cmd, nil
}
