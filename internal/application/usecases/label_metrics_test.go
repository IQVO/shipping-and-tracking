package usecases_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

// recordingMetrics counts the ports.LabelMetrics calls of a use case.
type recordingMetrics struct {
	mu       sync.Mutex
	issued   int
	rejected []string
}

var _ ports.LabelMetrics = (*recordingMetrics)(nil)

func (m *recordingMetrics) LabelIssued(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.issued++
}

func (m *recordingMetrics) LabelRejected(_ context.Context, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected = append(m.rejected, reason)
}

func TestIssueLabelCountsEachAttemptOnceByOutcome(t *testing.T) {
	r := newRig(t)
	m := &recordingMetrics{}
	r.deps.Metrics = m
	issue := usecases.NewIssueLabel(r.deps)

	_, err := issue.Execute(context.Background(), issueCmd("PO-1", 1))
	noErr(t, err)
	if m.issued != 1 || len(m.rejected) != 0 {
		t.Fatalf("after an issue: issued=%d rejected=%v, want 1 / none", m.issued, m.rejected)
	}

	_, err = issue.Execute(context.Background(), issueCmd("PO-1", 1))
	wantErr(t, err, label.ErrLabelAlreadyIssued)
	if m.issued != 1 || len(m.rejected) != 1 || m.rejected[0] != "label-already-issued" {
		t.Fatalf("after a refusal: issued=%d rejected=%v, want 1 / [label-already-issued]", m.issued, m.rejected)
	}
}

func TestIssueLabelRejectionReasonIsTheProblemSlug(t *testing.T) {
	tests := []struct {
		name string
		cmd  usecases.IssueLabelCmd
		fail bool
		want string
	}{
		{"an invalid command", issueCmd("PO-1", 0), false, "validation-failed"},
		{"an unknown order", func() usecases.IssueLabelCmd { c := issueCmd("PO-1", 1); c.OrderID = "ord-9"; return c }(), false, "validation-failed"},
		{"customer-ordering down", issueCmd("PO-1", 1), true, "ship-to-unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			m := &recordingMetrics{}
			r.deps.Metrics = m
			r.gateway.Fail(tt.fail)
			_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), tt.cmd)
			if err == nil {
				t.Fatal("the attempt must be refused")
			}
			if len(m.rejected) != 1 || m.rejected[0] != tt.want || m.issued != 0 {
				t.Fatalf("issued=%d rejected=%v, want one rejection %q", m.issued, m.rejected, tt.want)
			}
		})
	}
}

func TestIssueLabelAnUnexpectedFailureIsAnInternalError(t *testing.T) {
	r := newRig(t)
	m := &recordingMetrics{}
	r.deps.Metrics = m
	boom := errors.New("disk on fire")
	r.deps.UoW = failingUoW{err: boom}
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	wantErr(t, err, boom)
	if len(m.rejected) != 1 || m.rejected[0] != "internal-error" {
		t.Fatalf("rejected=%v, want [internal-error]", m.rejected)
	}
}

// failingUoW fails every unit of work with err.
type failingUoW struct{ err error }

func (u failingUoW) Do(context.Context, func(context.Context) error) error { return u.err }
