package usecases_test

import (
	"context"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// returnsNode is the node a return goes to; its prefix differs from the
// ship-to prefix of ord-1 (01310) and from anything a request carries.
const (
	returnsNode       = "node-rc-01"
	returnsNodePrefix = "07750"
)

// putNode stores a node in the local NodeDestinationView (ADR 0018).
func putNode(t *testing.T, r *rig, id, prefix string) {
	t.Helper()
	noErr(t, r.store.NodeDestinations().Upsert(context.Background(), repository.NodeDestination{NodeID: id, PostalPrefix: prefix}))
}

// nodeReturnCmd names the return destination by its node, the form of ADR 0018.
func nodeReturnCmd(auth string, pkg int, node string) usecases.IssueReturnLabelCmd {
	return usecases.IssueReturnLabelCmd{ReturnAuthorizationID: auth, OrderID: "ord-1", PackageNo: pkg, DestinationNodeID: node}
}

func TestIssueReturnLabelDerivesThePrefixFromTheDestinationNode(t *testing.T) {
	r := newRig(t)
	putNode(t, r, returnsNode, returnsNodePrefix)
	v, err := usecases.NewIssueReturnLabel(r.deps).Execute(context.Background(), nodeReturnCmd("ret-1", 1, returnsNode))
	noErr(t, err)
	stored, err := r.store.Labels().Get(context.Background(), label.Ref(v.LabelRef))
	noErr(t, err)
	if stored.DestinationPostalPrefix() != returnsNodePrefix {
		t.Fatalf("label prefix = %q, want the node's own %q", stored.DestinationPostalPrefix(), returnsNodePrefix)
	}
	evs := r.store.Outbox().Events()
	if len(evs) != 1 {
		t.Fatalf("outbox has %d events, want 1", len(evs))
	}
	issued, ok := evs[0].(label.LabelIssued)
	if !ok || issued.DestinationPostalPrefix != returnsNodePrefix || issued.Purpose != label.PurposeReturn {
		t.Fatalf("LabelIssued = %+v, want the node's prefix on a RETURN label", evs[0])
	}
}

func TestIssueReturnLabelRefusesADestinationNodeWithoutAPrefix(t *testing.T) {
	cases := []struct {
		name string
		seed func(t *testing.T, r *rig)
	}{
		{"a node this context does not know", func(*testing.T, *rig) {}},
		{"a node whose registered postal code left no prefix", func(t *testing.T, r *rig) { putNode(t, r, returnsNode, "") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			c.seed(t, r)
			m := &recordingMetrics{}
			r.deps.Metrics = m
			before := r.store.Counts()
			_, err := usecases.NewIssueReturnLabel(r.deps).Execute(context.Background(), nodeReturnCmd("ret-1", 1, returnsNode))
			wantErr(t, err, shipto.ErrDestinationPrefixUnavailable)
			if after := r.store.Counts(); after != before {
				t.Fatalf("a refusal stores nothing: %+v -> %+v", before, after)
			}
			if len(m.rejected) != 1 || m.rejected[0] != "destination-prefix-unavailable" {
				t.Fatalf("rejected = %v, want the problem slug once", m.rejected)
			}
		})
	}
}

// The refusal is about this context's state, not about the request: the very
// same command is served once the node fact has been consumed.
func TestIssueReturnLabelIsServedOnceTheNodeFactArrived(t *testing.T) {
	r := newRig(t)
	issue := usecases.NewIssueReturnLabel(r.deps)
	cmd := nodeReturnCmd("ret-1", 1, returnsNode)
	_, err := issue.Execute(context.Background(), cmd)
	wantErr(t, err, shipto.ErrDestinationPrefixUnavailable)
	putNode(t, r, returnsNode, returnsNodePrefix)
	v, err := issue.Execute(context.Background(), cmd)
	noErr(t, err)
	if v.ReturnAuthorizationID != "ret-1" || v.Purpose != "RETURN" {
		t.Fatalf("view = %+v", v)
	}
}

func TestIssueReturnLabelNeedsExactlyOneDestination(t *testing.T) {
	both := nodeReturnCmd("ret-1", 1, returnsNode)
	both.DestinationPostalPrefix = "01310"
	cases := map[string]usecases.IssueReturnLabelCmd{
		"both the node and the prefix":    both,
		"neither the node nor the prefix": nodeReturnCmd("ret-1", 1, ""),
	}
	for name, cmd := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			putNode(t, r, returnsNode, returnsNodePrefix)
			before := r.store.Counts()
			_, err := usecases.NewIssueReturnLabel(r.deps).Execute(context.Background(), cmd)
			wantErr(t, err, shared.ErrValidation)
			if after := r.store.Counts(); after != before {
				t.Fatalf("a refusal stores nothing: %+v -> %+v", before, after)
			}
		})
	}
}

// The deprecated form keeps working exactly as before ADR 0018: the prefix is
// taken as sent and the node view is never consulted (a Deps without one
// serves it).
func TestIssueReturnLabelDeprecatedPrefixFormIsUnchanged(t *testing.T) {
	r := newRig(t)
	putNode(t, r, returnsNode, returnsNodePrefix)
	r.deps.NodeDestinations = nil
	v, err := usecases.NewIssueReturnLabel(r.deps).Execute(context.Background(), returnCmd("ret-1", 1))
	noErr(t, err)
	stored, err := r.store.Labels().Get(context.Background(), label.Ref(v.LabelRef))
	noErr(t, err)
	if stored.DestinationPostalPrefix() != "01310" {
		t.Fatalf("label prefix = %q, want the prefix the caller sent", stored.DestinationPostalPrefix())
	}
	bad := returnCmd("ret-2", 1)
	bad.DestinationPostalPrefix = "013 1"
	_, err = usecases.NewIssueReturnLabel(r.deps).Execute(context.Background(), bad)
	wantErr(t, err, shared.ErrValidation)
}
