package features_test

import (
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

// steps_rules_events_test.go: assertions on the published events, field by
// field. Each step reads the NEWEST event of its type from the outbox.

// diff compares one named field of an event.
func diff(name string, got, want any) error {
	if fmt.Sprint(got) != fmt.Sprint(want) {
		return fmt.Errorf("%s = %v, want %v", name, got, want)
	}
	return nil
}

// atHour compares an instant with the scenario's hour.
func atHour(name string, got time.Time, h int) error {
	if !got.Equal(hour(h)) {
		return fmt.Errorf("%s = %s, want %s", name, got.Format(timeFormat), hour(h).Format(timeFormat))
	}
	return nil
}

// firstErr returns the first non-nil error.
func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *rules) registerEvents(sc *godog.ScenarioContext) {
	sc.Step(`^the PackageScanned events show the states "([^"]*)"$`, r.scannedStates)
	sc.Step(`^the last PackageScanned event is scan "([^"]*)" of type "([A-Z_]+)" at node "([^"]*)" with state "([A-Z_]+)"$`, r.lastScanned)
	sc.Step(`^the PackageHandedOver event carries purchase order "([^"]*)", node "([^"]*)" and instant hour (\d+)$`, r.handedOverEvent)
	sc.Step(`^the PackageDelivered event carries order "([^"]*)", assignment "([^"]*)" and instant hour (\d+)$`, r.deliveredEvent)
	sc.Step(`^the PackageReturned event carries order "([^"]*)" and instant hour (\d+)$`, r.returnedEvent)
	sc.Step(`^the PackageExceptionRaised event carries reason "([A-Z_]+)", order "([^"]*)", assignment "([^"]*)" and instant hour (\d+)$`, r.exceptionEvent)
	sc.Step(`^the PackageLost event carries order "([^"]*)" and instant hour (\d+)$`, r.lostEvent)
	sc.Step(`^the LabelIssued event for package (\d+) carries order "([^"]*)", assignment "([^"]*)", tier "([^"]*)" and carrier "([^"]*)"$`, r.labelIssuedEvent)
	sc.Step(`^the LabelIssued event carries postal prefix "([^"]*)"$`, r.postalPrefixEvent)
	sc.Step(`^the LabelVoided event carries reason "([^"]*)" for the tracking number of the label$`, r.labelVoidedEvent)
}

func (r *rules) scannedStates(want string) error {
	var got []string
	for _, e := range allEvents[journey.PackageScanned](r.events()) {
		got = append(got, string(e.State))
	}
	return diff("PackageScanned states", strings.Join(got, ","), want)
}

func (r *rules) lastScanned(id, typ, node, state string) error {
	e, err := lastEvent[journey.PackageScanned](r.events())
	if err != nil {
		return err
	}
	return firstErr(diff("scanId", e.ScanID, id), diff("scanType", e.ScanType, typ), diff("nodeId", e.NodeID, node), diff("state", e.State, state))
}

func (r *rules) handedOverEvent(po, node string, h int) error {
	e, err := lastEvent[journey.PackageHandedOver](r.events())
	if err != nil {
		return err
	}
	return firstErr(diff("poNumber", e.PONumber, po), diff("nodeId", e.NodeID, node), atHour("handedOverAt", e.HandedOverAt, h))
}

func (r *rules) deliveredEvent(order, asg string, h int) error {
	e, err := lastEvent[journey.PackageDelivered](r.events())
	if err != nil {
		return err
	}
	return firstErr(diff("orderId", e.OrderID, order), diff("assignmentId", e.AssignmentID, asg), atHour("deliveredAt", e.DeliveredAt, h))
}

func (r *rules) returnedEvent(order string, h int) error {
	e, err := lastEvent[journey.PackageReturned](r.events())
	if err != nil {
		return err
	}
	return firstErr(diff("orderId", e.OrderID, order), atHour("returnedAt", e.ReturnedAt, h))
}

func (r *rules) exceptionEvent(reason, order, asg string, h int) error {
	e, err := lastEvent[journey.PackageExceptionRaised](r.events())
	if err != nil {
		return err
	}
	return firstErr(diff("reason", e.Reason, reason), diff("orderId", e.OrderID, order), diff("assignmentId", e.AssignmentID, asg), atHour("at", e.RaisedAt, h))
}

func (r *rules) lostEvent(order string, h int) error {
	e, err := lastEvent[journey.PackageLost](r.events())
	if err != nil {
		return err
	}
	return firstErr(diff("orderId", e.OrderID, order), atHour("declaredAt", e.DeclaredAt, h))
}

func (r *rules) labelIssuedEvent(pkg int, order, asg, tier, carrier string) error {
	for _, e := range allEvents[label.LabelIssued](r.events()) {
		if e.PackageNo != pkg {
			continue
		}
		return firstErr(diff("orderId", e.OrderID, order), diff("assignmentId", e.AssignmentID, asg), diff("tier", e.Tier, tier),
			diff("carrier", e.Carrier, carrier), diff("trackingNumber", e.TrackingNumber != "", true), diff("labelRef", e.LabelRef != "", true))
	}
	return fmt.Errorf("no LabelIssued for package %d", pkg)
}

func (r *rules) postalPrefixEvent(prefix string) error {
	e, err := lastEvent[label.LabelIssued](r.events())
	if err != nil {
		return err
	}
	return diff("destinationPostalPrefix", e.DestinationPostalPrefix, prefix)
}

func (r *rules) labelVoidedEvent(reason string) error {
	e, err := lastEvent[label.LabelVoided](r.events())
	if err != nil {
		return err
	}
	return firstErr(diff("reason", e.Reason, reason), diff("trackingNumber", e.TrackingNumber, r.w.tracking()), diff("labelRef", e.LabelRef, r.w.labels[0]))
}
