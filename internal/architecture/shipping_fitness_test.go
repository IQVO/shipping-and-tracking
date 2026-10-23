package architecture

// Shipping-and-tracking fitness tests (this repository's own, not managed by
// the template).
//
//   - TestContractsCarryNoShipToField: the ship-to address is synthetic PII
//     confined to customer-ordering and this service's encrypted snapshot
//     (ADR RN-0006, ADR 0002 A9). No OpenAPI or AsyncAPI schema may declare an
//     address field.
//   - TestConsumedTypesAreDocumented: every CloudEvents type the Kafka
//     consumers handle is written out in ADR 0003 and in the AsyncAPI document,
//     so a consumer cannot start reading a type the documentation does not know.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// shipToKeyRx matches a YAML key that names a ship-to field. "name" is not in
// the list on purpose: it is also the key of every OpenAPI parameter. A bare
// "address" key counts only when it opens a nested schema (nothing after the
// colon): an AsyncAPI channel's `address: <topic>` is the topic name.
var shipToKeyRx = regexp.MustCompile(`(?m)^\s*(line1|line2|addressLine\w*|postalCode|zipCode|countryCode|phone|phoneNumber|shipTo|shipToAddress|shippingAddress|deliveryAddress|recipient\w*):|^\s*(address):\s*$`)

// shipToKeys returns the ship-to keys a contract declares.
func shipToKeys(contract string) []string {
	var keys []string
	for _, m := range shipToKeyRx.FindAllStringSubmatch(contract, -1) {
		keys = append(keys, m[1]+m[2])
	}
	return keys
}

func TestContractsCarryNoShipToField(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{"openapi.yaml", "asyncapi.yaml"} {
		b, err := os.ReadFile(filepath.Join(root, "apis", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if keys := shipToKeys(string(b)); len(keys) > 0 {
			t.Errorf("apis/%s declares ship-to fields %v: the address never leaves the encrypted snapshot", name, keys)
		}
	}
}

func TestShipToSensorBites(t *testing.T) {
	bad := "components:\n  schemas:\n    Label:\n      properties:\n        postalCode:\n          type: string\n"
	if got := shipToKeys(bad); len(got) != 1 || got[0] != "postalCode" {
		t.Fatalf("the sensor must see a postalCode property, got %v", got)
	}
	// The postal PREFIX, the opaque stop key and a channel's topic address are allowed.
	ok := "channels:\n  c:\n    address: retailnetwork.x.events\nproperties:\n  destinationPostalPrefix:\n    type: string\n  stopKey:\n    type: string\n  name: x\n"
	if got := shipToKeys("properties:\n  address:\n    type: object\n"); len(got) != 1 {
		t.Fatalf("a nested address schema must be reported, got %v", got)
	}
	if got := shipToKeys(ok); len(got) != 0 {
		t.Fatalf("the sensor must accept the prefix and the stop key, got %v", got)
	}
}

var consumedTypeConstRx = regexp.MustCompile(`"(com\.retailnetwork\.[a-z]+\.[a-z-]+\.[a-z]+\.[A-Z][A-Za-z0-9]*)"`)

// undocumented returns the consumed types that are missing from any document.
func undocumented(source string, docs map[string]string) []string {
	var missing []string
	seen := map[string]bool{}
	for _, m := range consumedTypeConstRx.FindAllStringSubmatch(source, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		for name, text := range docs {
			if !strings.Contains(text, m[1]) {
				missing = append(missing, m[1]+" (not in "+name+")")
			}
		}
	}
	sort.Strings(missing)
	return missing
}

func TestConsumedTypesAreDocumented(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(parts ...string) string {
		b, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	source := read("internal", "adapters", "inbound", "kafka", "handlers.go")
	docs := map[string]string{
		"docs/adr/0003-consumed-contracts-and-local-read-models.md": read("docs", "adr", "0003-consumed-contracts-and-local-read-models.md"),
	}
	if m := undocumented(source, docs); len(m) > 0 {
		t.Errorf("consumed types missing from the documentation: %v", m)
	}
	if len(consumedTypeConstRx.FindAllString(source, -1)) < 7 {
		t.Error("the sensor found fewer than the 7 consumed types: the handlers' constants moved")
	}
}

func TestConsumedTypeSensorBites(t *testing.T) {
	src := `const T = "com.retailnetwork.logistics.linehaul-transportation.load.LoadPlanned"`
	if got := undocumented(src, map[string]string{"adr": "nothing about it"}); len(got) != 1 {
		t.Fatalf("an undocumented type must be reported, got %v", got)
	}
	if got := undocumented(src, map[string]string{"adr": "com.retailnetwork.logistics.linehaul-transportation.load.LoadPlanned"}); len(got) != 0 {
		t.Fatalf("a documented type is fine, got %v", got)
	}
}
