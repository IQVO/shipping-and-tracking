package journey

import (
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func TestEveryEventIdentifiesItsAggregateAndName(t *testing.T) {
	h := shared.Header{Version: 3, At: at(4)}
	cases := []struct {
		ev   shared.Event
		name string
	}{
		{PackageScanned{Header: h, TrackingNumber: "T1"}, "PackageScanned"},
		{PackageHandedOver{Header: h, TrackingNumber: "T1"}, "PackageHandedOver"},
		{PackageDelivered{Header: h, TrackingNumber: "T1"}, "PackageDelivered"},
		{PackageExceptionRaised{Header: h, TrackingNumber: "T1"}, "PackageExceptionRaised"},
		{PackageReturned{Header: h, TrackingNumber: "T1"}, "PackageReturned"},
		{PackageLost{Header: h, TrackingNumber: "T1"}, "PackageLost"},
	}
	for _, c := range cases {
		if c.ev.EventName() != c.name || c.ev.Entity() != "journey" || c.ev.SubjectID() != "T1" ||
			c.ev.AggregateVersion() != 3 || !c.ev.OccurredAt().Equal(at(4)) {
			t.Errorf("%s: %s/%s/%s/%d/%v", c.name, c.ev.EventName(), c.ev.Entity(), c.ev.SubjectID(), c.ev.AggregateVersion(), c.ev.OccurredAt())
		}
	}
}
