package repocontract

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// T0 is the instant every builder starts from (whole seconds: Postgres keeps
// microseconds).
var T0 = time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)

// Hour returns T0 plus n hours.
func Hour(n int) time.Time { return T0.Add(time.Duration(n) * time.Hour) }

// Pair is a label and the journey of the same package.
type Pair struct {
	Label   *label.ShippingLabel
	Journey *journey.Journey
}

// NewPair builds (without storing) the label and journey number n: label
// lbl-n, tracking RNn..., purchase order PO-n, package 1.
func NewPair(t testing.TB, n int) Pair {
	t.Helper()
	tn := shared.TrackingNumber(fmt.Sprintf("RN%015d", n))
	l, _, err := label.Issue(label.NewLabel{
		Ref: label.Ref(fmt.Sprintf("lbl-%d", n)), TrackingNumber: tn, PONumber: fmt.Sprintf("PO-%d", n), PackageNo: 1,
		AssignmentID: fmt.Sprintf("asg-%d", n), OrderID: fmt.Sprintf("ord-%d", n), Tier: shared.TierNextDay,
		StopKey: strings.Repeat("ab", label.StopKeyLen/2), DestinationPostalPrefix: "01310",
	}, T0)
	noErr(t, err)
	j, err := journey.Start(journey.NewJourney{
		TrackingNumber: tn, LabelRef: string(l.Ref()), PONumber: l.PONumber(), AssignmentID: l.AssignmentID(),
		OrderID: l.OrderID(), Tier: l.Tier(),
	}, T0)
	noErr(t, err)
	return Pair{Label: l, Journey: j}
}

// Seed stores the pair and a snapshot for its label through f.
func Seed(t testing.TB, f Fixture, n int) Pair {
	t.Helper()
	p := NewPair(t, n)
	ctx := context.Background()
	noErr(t, f.Labels.Save(ctx, p.Label))
	noErr(t, f.Snapshots.Put(ctx, p.Label.Ref(), repository.SealedShipTo{KeyID: "k1", Nonce: []byte{1, 2, 3}, Ciphertext: []byte("sealed")}))
	noErr(t, f.Journeys.Save(ctx, p.Journey))
	return p
}

// Scan applies one scan at hour h to j, failing the test on any error.
func Scan(t testing.TB, j *journey.Journey, id string, typ journey.ScanType, h int) {
	t.Helper()
	_, err := j.RecordScan(journey.ScanInput{ID: id, NodeID: "node-" + id, Type: typ, At: Hour(h)}, Hour(h))
	noErr(t, err)
}
