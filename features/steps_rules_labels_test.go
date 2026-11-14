package features_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

// steps_rules_labels_test.go: the label, ship-to and catalog vocabulary of
// label_rules.feature and retention_rules.feature.

// addressParts are values of the synthetic ship-to that must never leave the
// encrypted snapshot (the same list the issue scenario of issue_label.feature uses).
var addressParts = []string{"Sample Street", "Sampletown", "Ana Example", "01310-100", "5550", "Apt 4"}

// spaces turns the table token (blank) into a value of spaces only, which a
// Gherkin cell cannot hold.
func spaces(s string) string {
	if s == "(blank)" {
		return "   "
	}
	return s
}

func (r *rules) registerLabels(sc *godog.ScenarioContext) {
	sc.Step(`^customer order "([^"]*)" is addressed to recipient "([^"]*)", line1 "([^"]*)", city "([^"]*)", postal code "([^"]*)"$`, r.addressed)
	sc.Step(`^customer order "([^"]*)" ships to an address without a region$`, r.noRegion)
	sc.Step(`^customer-ordering is available again$`, func() error { r.w.gateway.Fail(false); return nil })
	sc.Step(`^customer-ordering was asked (\d+) times?$`, r.askedTimes)
	sc.Step(`^the refusal names only the reason "([A-Z_]+)"$`, r.refusalNamesOnly)
	sc.Step(`^the label is still "([A-Z]+)"$`, r.labelIsStill)
	sc.Step(`^the label has void reason "([^"]*)"$`, r.labelVoidReason)
	sc.Step(`^I void the label with a reason of (\d+) characters$`, func(n int) error { return r.w.iVoidTheLabel(strings.Repeat("r", n)) })
	sc.Step(`^the two LabelIssued events carry different stop keys$`, r.differentStopKeys)
	sc.Step(`^no published event or timeline contains any part of the address$`, r.noAddressInEventsOrTimeline)
	sc.Step(`^the request is refused$`, r.refusedAnyhow)
	r.registerCatalog(sc)
}

func (r *rules) registerCatalog(sc *godog.ScenarioContext) {
	sc.Step(`^catalog item "([^"]*)" weighing (\d+) g is recorded by event "([^"]*)"$`, r.recordCatalogItem)
	sc.Step(`^the catalog event was skipped as already processed$`, func() error {
		if !r.relay.skipped {
			return fmt.Errorf("the catalog event was recorded, want it skipped")
		}
		return nil
	})
	sc.Step(`^the catalog view is empty$`, func() error { return diff("catalog items", r.w.store.Counts().Catalog, 0) })
	sc.Step(`^the catalog view holds (\d+) items? and item "([^"]*)" weighs (\d+) g$`, r.catalogHolds)
}

func (r *rules) addressed(order, recipient, line1, city, postal string) error {
	r.w.gateway.Put(order, shipto.Address{
		Name: spaces(recipient), Line1: spaces(line1), City: spaces(city), Region: "SP", PostalCode: spaces(postal), CountryCode: "BR",
	})
	return nil
}

func (r *rules) noRegion(order string) error {
	r.w.gateway.Put(order, shipto.Address{Name: "Ana Example", Line1: "10 Sample Street", City: "Sampletown", PostalCode: "01310-100", CountryCode: "BR"})
	return nil
}

func (r *rules) askedTimes(n int) error {
	return diff("customer-ordering calls", len(r.w.gateway.Calls()), n)
}

// refusalNamesOnly pins the refusal detail to the reason tokens: no address value.
func (r *rules) refusalNamesOnly(token string) error {
	want := shipto.ErrAddressSuspect.Error() + ": " + token
	if r.w.lastErr == nil {
		return fmt.Errorf("want the refusal %q, got success", want)
	}
	return diff("refusal", r.w.lastErr.Error(), want)
}

func (r *rules) labelIsStill(status string) error {
	l, err := r.w.store.Labels().Get(context.Background(), label.Ref(r.w.labels[0]))
	if err != nil {
		return err
	}
	return diff("label status", l.Status(), status)
}

func (r *rules) labelVoidReason(reason string) error {
	return firstErr(diff("voidReason", r.w.lastLabel.VoidReason, reason), diff("voidedAt present", r.w.lastLabel.VoidedAt != nil, true))
}

func (r *rules) differentStopKeys() error {
	var keys []string
	for _, e := range allEvents[label.LabelIssued](r.events()) {
		keys = append(keys, e.StopKey)
	}
	if len(keys) != 2 || keys[0] == keys[1] {
		return fmt.Errorf("stop keys %v, want two different keys", keys)
	}
	return nil
}

func (r *rules) noAddressInEventsOrTimeline() error {
	haystack := fmt.Sprintf("%+v %#v", r.w.lastJourney, r.events())
	for _, part := range addressParts {
		if strings.Contains(haystack, part) {
			return fmt.Errorf("%q leaked into a published event or the timeline", part)
		}
	}
	return nil
}

func (r *rules) refusedAnyhow() error {
	if r.w.lastErr == nil {
		return fmt.Errorf("want the request refused, got success")
	}
	return nil
}

func (r *rules) recordCatalogItem(id string, grams int, ev string) error {
	recorded, err := usecases.NewRecordCatalogItem(r.w.deps).Execute(context.Background(), "offer-availability", ev, repository.CatalogDimensions{
		CatalogItemID: id, Title: "Mug", LengthMm: 120, WidthMm: 90, HeightMm: 100, WeightG: int64(grams), Fragile: true,
	})
	r.relay.skipped, r.w.lastErr = err == nil && !recorded, err
	return nil
}

func (r *rules) catalogHolds(n int, id string, grams int) error {
	if err := diff("catalog items", r.w.store.Counts().Catalog, n); err != nil {
		return err
	}
	d, err := r.w.store.Catalog().Get(context.Background(), id)
	if err != nil {
		return err
	}
	return diff("weightG of "+id, d.WeightG, grams)
}
