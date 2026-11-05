// Package shipto holds the ship-to address as the application layer meets it:
// a value read from customer-ordering for the duration of one IssueLabel call,
// from which the label's derived values (the opaque stopKey input and the
// postal prefix) and the encrypted snapshot are made. The address is synthetic
// PII (ADR RN-0006): it is never logged, published or returned. Address
// redacts itself when formatted, so an accidental %v or %+v prints nothing.
package shipto

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// PrefixLen is the length of the destination postal prefix (ADR 0002 A10).
const PrefixLen = 5

// MaxField is the longest accepted address field, in characters.
const MaxField = 200

// Address is the ship-to of an order, as GET /orders/{orderId}/ship-to returns it.
type Address struct {
	Name        string `json:"name"`
	Line1       string `json:"line1"`
	Line2       string `json:"line2,omitempty"`
	City        string `json:"city"`
	Region      string `json:"region"`
	PostalCode  string `json:"postalCode"`
	CountryCode string `json:"countryCode"`
	Phone       string `json:"phone,omitempty"`
}

// String redacts the address.
func (Address) String() string { return "[ship-to redacted]" }

// GoString redacts the address for %#v.
func (Address) GoString() string { return "[ship-to redacted]" }

// Validate checks the required fields and the length bounds. The error names
// the field, never its value.
func (a Address) Validate() error {
	required := [][2]string{
		{"name", a.Name}, {"line1", a.Line1}, {"city", a.City}, {"region", a.Region}, {"postalCode", a.PostalCode},
	}
	for _, f := range required {
		if strings.TrimSpace(f[1]) == "" {
			return shared.Invalid("ship-to %s is required", f[0])
		}
	}
	if len(a.CountryCode) != 2 {
		return shared.Invalid("ship-to countryCode must have 2 characters")
	}
	for _, f := range [][2]string{{"name", a.Name}, {"line1", a.Line1}, {"line2", a.Line2}, {"city", a.City},
		{"region", a.Region}, {"postalCode", a.PostalCode}, {"phone", a.Phone}} {
		if len([]rune(f[1])) > MaxField {
			return shared.Invalid("ship-to %s is longer than %d characters", f[0], MaxField)
		}
	}
	if len(Alnum(a.PostalCode)) == 0 {
		return shared.Invalid("ship-to postalCode has no letter or digit")
	}
	return nil
}

// Verdict is the outcome of the deterministic deliverability evaluation.
type Verdict string

// The two verdicts.
const (
	VerdictClean   Verdict = "CLEAN"
	VerdictSuspect Verdict = "SUSPECT"
)

// AddressQuality is the result of evaluating a ship-to for deliverability.
// Reasons are fixed vocabulary tokens: they are safe to log and publish
// because they never carry address values (the redaction rule of ADR RN-0006).
type AddressQuality struct {
	Verdict Verdict
	Reasons []string
}

// Suspect reports whether the address failed the evaluation.
func (q AddressQuality) Suspect() bool { return q.Verdict == VerdictSuspect }

// ErrAddressSuspect is the label-issuance refusal for a suspect address
// (422 address-suspect). The wrapped detail names reason tokens only.
var ErrAddressSuspect = errors.New("ship-to address suspect")

// placeholderValues carry no deliverable information. An address whose
// recipient, line or city reduces (Alnum) to one of them passes Validate
// (the field is non-empty) but names nobody at nowhere.
var placeholderValues = map[string]struct{}{
	"TEST": {}, "TESTTEST": {}, "TESTADDRESS": {}, "TESTUSER": {},
	"FOO": {}, "FOOBAR": {}, "ASDF": {}, "QWERTY": {}, "XXX": {},
	"NA": {}, "NAA": {}, "TBD": {}, "UNKNOWN": {}, "NONE": {},
}

// Quality evaluates the address with conservative deterministic rules:
// only objectively broken addresses are SUSPECT (a false refusal blocks
// fulfilment), so the rules are pattern defects Validate cannot see, not
// risk scores. Rules: RECIPIENT_PLACEHOLDER (the recipient line is a
// placeholder token), STREET_MISSING_NUMBER (the street line has no digit,
// so there is no house number to deliver to), STREET_PLACEHOLDER,
// CITY_PLACEHOLDER and POSTAL_CODE_TOO_SHORT (fewer than three alphanumerics
// cannot carry a routable postal prefix). Address errors are the leading
// cause of failed first attempts, and the check runs at the one place the
// plaintext address is open: label issuance (ADR 0011).
func (a Address) Quality() AddressQuality {
	q := AddressQuality{Verdict: VerdictClean}
	if _, hit := placeholderValues[Alnum(a.Name)]; hit {
		q.Reasons = append(q.Reasons, "RECIPIENT_PLACEHOLDER")
	}
	street := Alnum(a.Line1)
	if _, hit := placeholderValues[street]; hit {
		q.Reasons = append(q.Reasons, "STREET_PLACEHOLDER")
	} else if !strings.ContainsFunc(street, unicode.IsDigit) {
		q.Reasons = append(q.Reasons, "STREET_MISSING_NUMBER")
	}
	if _, hit := placeholderValues[Alnum(a.City)]; hit {
		q.Reasons = append(q.Reasons, "CITY_PLACEHOLDER")
	}
	if len(Alnum(a.PostalCode)) < 3 {
		q.Reasons = append(q.Reasons, "POSTAL_CODE_TOO_SHORT")
	}
	if len(q.Reasons) > 0 {
		q.Verdict = VerdictSuspect
	}
	return q
}

// Alnum upper-cases s and keeps only its letters and digits.
func Alnum(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Normalized is the canonical text of the address that the stopKey hashes:
// line1, line2, city, region, postal code and country, each reduced to upper
// case letters and digits and joined by "|". The recipient name and the phone
// are not part of it, so neighbours at one address share a stop (ADR 0002 A10).
func (a Address) Normalized() string {
	return strings.Join([]string{
		Alnum(a.Line1), Alnum(a.Line2), Alnum(a.City), Alnum(a.Region), Alnum(a.PostalCode), Alnum(a.CountryCode),
	}, "|")
}

// PostalPrefix is the first PrefixLen letters or digits of the postal code.
func (a Address) PostalPrefix() string {
	p := []rune(Alnum(a.PostalCode))
	if len(p) > PrefixLen {
		p = p[:PrefixLen]
	}
	return string(p)
}

// Plaintext is the JSON that is encrypted into the snapshot.
func (a Address) Plaintext() ([]byte, error) { return json.Marshal(a) }

// FromPlaintext is the inverse of Plaintext (used by the label printing path
// and the tests; the service itself never returns an address).
func FromPlaintext(b []byte) (Address, error) {
	var a Address
	if err := json.Unmarshal(b, &a); err != nil {
		return Address{}, shared.Invalid("ship-to snapshot is not valid JSON")
	}
	return a, nil
}
