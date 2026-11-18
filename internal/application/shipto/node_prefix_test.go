package shipto

import (
	"regexp"
	"testing"
)

// requestPrefixPattern is the pattern apis/openapi.yaml gives
// IssueReturnLabelRequest.destinationPostalPrefix: a node prefix must be a
// value the deprecated request form could have carried.
var requestPrefixPattern = regexp.MustCompile(`^[A-Z0-9]+$`)

func TestNodePrefixIsThePostalPrefixRuleOverAZ09(t *testing.T) {
	cases := []struct {
		name, postalCode, want string
	}{
		{"digits and a dash", "07750-000", "07750"},
		{"an unpunctuated code", "07095000", "07095"},
		{"exactly the prefix length", "12345", "12345"},
		{"one character more than the prefix length", "123456", "12345"},
		{"shorter than the prefix length is kept whole", "A1", "A1"},
		{"a single usable character", "7", "7"},
		{"lower case is upper-cased", "sw1a 1aa", "SW1A1"},
		{"spaces and punctuation are dropped before counting", " 0.7-7 5/0 0", "07750"},
		{"a letter outside A-Z is dropped, not counted", "\u00c91310-100", "13101"},
		{"a lower-case letter outside a-z is dropped too", "\u00e91310", "1310"},
		{"digits outside 0-9 are dropped, not counted", "\u0660\u0661\u066234567", "34567"},
		{"the characters next to the accepted ranges are dropped", "/:@[`{", ""},
		{"the edges of the accepted ranges are kept", "09azAZ", "09AZA"},
		{"only punctuation leaves nothing", "-- /", ""},
		{"only letters outside A-Z leave nothing", "\u00c9\u00c0\u00dc", ""},
		{"an empty code leaves nothing", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NodePrefix(c.postalCode)
			if got != c.want {
				t.Fatalf("NodePrefix(%q) = %q, want %q", c.postalCode, got, c.want)
			}
			if got != "" && !requestPrefixPattern.MatchString(got) {
				t.Fatalf("NodePrefix(%q) = %q is not a value the request pattern admits", c.postalCode, got)
			}
			if len(got) > PrefixLen {
				t.Fatalf("NodePrefix(%q) = %q is longer than PrefixLen", c.postalCode, got)
			}
		})
	}
}

// For a code written in A-Z 0-9 and punctuation the node prefix IS the
// ship-to prefix of ADR 0002 A10: one rule, two uses.
func TestNodePrefixEqualsTheShipToPrefixOfAnASCIICode(t *testing.T) {
	for _, code := range []string{"07750-000", "01310-100", "sw1a 1aa", "A1", "90210", "K1A 0B1", "1"} {
		if got, want := NodePrefix(code), (Address{PostalCode: code}).PostalPrefix(); got != want {
			t.Errorf("NodePrefix(%q) = %q, the ship-to rule gives %q", code, got, want)
		}
	}
}
