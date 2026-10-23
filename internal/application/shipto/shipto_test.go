package shipto

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func good() Address {
	return Address{
		Name: "Ana Example", Line1: "10 Sample Street", Line2: "Apt 4", City: "Sampletown", Region: "SP",
		PostalCode: "01310-100", CountryCode: "BR", Phone: "+55 11 5550 0100",
	}
}

func TestValidateAcceptsAWellFormedAddress(t *testing.T) {
	if err := good().Validate(); err != nil {
		t.Fatal(err)
	}
	a := good()
	a.Line2, a.Phone = "", ""
	if err := a.Validate(); err != nil {
		t.Fatalf("line2 and phone are optional: %v", err)
	}
}

func TestValidateRefusesWithoutLeakingTheValue(t *testing.T) {
	cases := map[string]func(*Address){
		"name":       func(a *Address) { a.Name = " " },
		"line1":      func(a *Address) { a.Line1 = "" },
		"city":       func(a *Address) { a.City = "" },
		"region":     func(a *Address) { a.Region = "" },
		"postalCode": func(a *Address) { a.PostalCode = "" },
		"country":    func(a *Address) { a.CountryCode = "BRA" },
		"country1":   func(a *Address) { a.CountryCode = "B" },
		"too long":   func(a *Address) { a.Line1 = strings.Repeat("x", MaxField+1) },
		"phone long": func(a *Address) { a.Phone = strings.Repeat("1", MaxField+1) },
		"no alnum":   func(a *Address) { a.PostalCode = "---" },
	}
	for name, mut := range cases {
		a := good()
		mut(&a)
		err := a.Validate()
		if !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
			continue
		}
		for _, secret := range []string{"Sample", "Ana", "01310"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: error leaks %q: %v", name, secret, err)
			}
		}
	}
	a := good()
	a.Line1 = strings.Repeat("x", MaxField)
	if err := a.Validate(); err != nil {
		t.Fatalf("exactly MaxField characters is accepted: %v", err)
	}
}

func TestNormalizedIgnoresCaseSpacingPunctuationNameAndPhone(t *testing.T) {
	a := good()
	b := Address{
		Name: "Someone Else", Line1: "10  SAMPLE street.", Line2: "apt-4", City: "sampletown", Region: "sp",
		PostalCode: "01310100", CountryCode: "br",
	}
	if a.Normalized() != b.Normalized() {
		t.Fatalf("%q != %q", a.Normalized(), b.Normalized())
	}
	if a.Normalized() != "10SAMPLESTREET|APT4|SAMPLETOWN|SP|01310100|BR" {
		t.Fatalf("normalized = %q", a.Normalized())
	}
	c := good()
	c.Line1 = "11 Sample Street"
	if a.Normalized() == c.Normalized() {
		t.Fatal("another street is another stop")
	}
}

func TestPostalPrefixIsTheFirstFiveLettersOrDigits(t *testing.T) {
	for in, want := range map[string]string{
		"01310-100": "01310", "0131": "0131", "01310": "01310", "sw1a 1aa": "SW1A1", "ab": "AB",
	} {
		a := good()
		a.PostalCode = in
		if got := a.PostalPrefix(); got != want {
			t.Errorf("PostalPrefix(%q) = %q, want %q", in, got, want)
		}
	}
	if PrefixLen != 5 {
		t.Fatal("prefix length is 5 (ADR 0002 A10)")
	}
}

func TestAnAddressPrintsRedacted(t *testing.T) {
	a := good()
	for _, s := range []string{fmt.Sprintf("%v", a), fmt.Sprintf("%+v", a), fmt.Sprintf("%#v", a), a.String()} {
		if strings.Contains(s, "Sample") || strings.Contains(s, "Ana") || !strings.Contains(s, "redacted") {
			t.Errorf("not redacted: %q", s)
		}
	}
}

func TestPlaintextRoundTrip(t *testing.T) {
	b, err := good().Plaintext()
	if err != nil {
		t.Fatal(err)
	}
	got, err := FromPlaintext(b)
	if err != nil || got != good() {
		t.Fatalf("round trip: %v %v", got == good(), err)
	}
	if _, err := FromPlaintext([]byte("{")); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("garbage must be a validation error, got %v", err)
	}
}
