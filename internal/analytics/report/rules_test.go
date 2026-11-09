package report_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/analytics/report"
)

func TestTouchScanTypesAreThePhysicalHandlings(t *testing.T) {
	want := []string{"PICKUP_DEPARTURE", "DEPARTURE", "TRANSIT_ARRIVAL", "STATION_ARRIVAL", "OUT_FOR_DELIVERY", "DELIVERY_FAILED"}
	if got := report.TouchScanTypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TouchScanTypes = %v, want %v", got, want)
	}
	for _, s := range want {
		if !report.IsTouch(s) {
			t.Errorf("IsTouch(%s) = false, want true", s)
		}
	}
	for _, s := range []string{"SHIPMENT_CONFIRMED", "DELIVERED", "", "pickup_departure"} {
		if report.IsTouch(s) {
			t.Errorf("IsTouch(%q) = true, want false", s)
		}
	}
}

func TestFailedAttemptsAreTheDeliveryFailedScans(t *testing.T) {
	if got := report.FailedAttemptScanTypes(); !reflect.DeepEqual(got, []string{"DELIVERY_FAILED"}) {
		t.Fatalf("FailedAttemptScanTypes = %v", got)
	}
	if !report.IsFailedAttempt("DELIVERY_FAILED") {
		t.Error("DELIVERY_FAILED must be a failed attempt")
	}
	for _, s := range []string{"DELIVERED", "OUT_FOR_DELIVERY", ""} {
		if report.IsFailedAttempt(s) {
			t.Errorf("IsFailedAttempt(%q) = true, want false", s)
		}
	}
}

func TestPercentileRanksAndUnknownTier(t *testing.T) {
	if report.TouchesP50() != 0.5 || report.TouchesP90() != 0.9 {
		t.Fatalf("ranks = %v, %v; want 0.5, 0.9", report.TouchesP50(), report.TouchesP90())
	}
	if report.UnknownTier() != "UNKNOWN" {
		t.Fatalf("UnknownTier = %q", report.UnknownTier())
	}
}

func TestOutcomeOf(t *testing.T) {
	cases := []struct {
		kind report.Kind
		want string
		ok   bool
	}{
		{report.KindPackageDelivered, report.OutcomeDelivered, true},
		{report.KindPackageReturned, report.OutcomeReturned, true},
		{report.KindPackageLost, report.OutcomeLost, true},
		{report.KindPackageScanned, "", false},
		{report.KindLabelIssued, "", false},
	}
	for _, c := range cases {
		got, ok := report.OutcomeOf(c.kind)
		if got != c.want || ok != c.ok {
			t.Errorf("OutcomeOf(%s) = %q, %v; want %q, %v", c.kind, got, ok, c.want, c.ok)
		}
	}
}

var at = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

func validScan() report.Event {
	return report.Event{Kind: report.KindPackageScanned, EventID: "e1", At: at, TrackingNumber: "STT1", OccurredAt: at,
		ScanID: "s1", ScanType: "DEPARTURE"}
}

func validLabel() report.Event {
	return report.Event{Kind: report.KindLabelIssued, EventID: "e2", At: at, TrackingNumber: "STT1", OccurredAt: at,
		LabelRef: "lbl-1", Tier: "NEXT_DAY"}
}

func TestValidateAcceptsCompleteFacts(t *testing.T) {
	outcome := report.Event{Kind: report.KindPackageLost, EventID: "e3", At: at, TrackingNumber: "STT1", OccurredAt: at}
	for _, e := range []report.Event{validScan(), validLabel(), outcome} {
		if err := e.Validate(); err != nil {
			t.Errorf("%s: %v", e.Kind, err)
		}
	}
}

func TestValidateNamesTheFirstMissingField(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*report.Event)
		base   func() report.Event
	}{
		{"id", func(e *report.Event) { e.EventID = "" }, validScan},
		{"time", func(e *report.Event) { e.At = time.Time{} }, validScan},
		{"trackingNumber", func(e *report.Event) { e.TrackingNumber = "" }, validScan},
		{"the business instant", func(e *report.Event) { e.OccurredAt = time.Time{} }, validScan},
		{"scanId", func(e *report.Event) { e.ScanID = "" }, validScan},
		{"scanType", func(e *report.Event) { e.ScanType = "" }, validScan},
		{"labelRef", func(e *report.Event) { e.LabelRef = "" }, validLabel},
		{"tier", func(e *report.Event) { e.Tier = "" }, validLabel},
		{"a known kind", func(e *report.Event) { e.Kind = "LabelVoided" }, validScan},
	}
	for _, c := range cases {
		e := c.base()
		c.mutate(&e)
		err := e.Validate()
		if !errors.Is(err, report.ErrInvalidEvent) {
			t.Errorf("%s: err = %v, want ErrInvalidEvent", c.name, err)
			continue
		}
		if !strings.HasSuffix(err.Error(), ": "+c.name+" is required") {
			t.Errorf("%s: err = %q does not name the field", c.name, err)
		}
	}
}
