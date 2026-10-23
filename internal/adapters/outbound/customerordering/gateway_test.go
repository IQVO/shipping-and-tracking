package customerordering_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/customerordering"
	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
)

const body = `{"name":"Ana Example","line1":"10 Sample Street","line2":"Apt 4","city":"Sampletown","region":"SP","postalCode":"01310-100","countryCode":"BR","phone":"+55 11 5550 0100","unknown":"x"}`

func serve(t *testing.T, h http.HandlerFunc) *customerordering.Gateway {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	g, err := customerordering.New(srv.URL+"/", 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGetShipToReadsTheDocumentAndEscapesTheOrderId(t *testing.T) {
	var path string
	g := serve(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	a, err := g.GetShipTo(context.Background(), "ord/1 x")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/orders/ord%2F1%20x/ship-to" {
		t.Errorf("path = %s", path)
	}
	if a.Name != "Ana Example" || a.Line1 != "10 Sample Street" || a.Line2 != "Apt 4" || a.City != "Sampletown" || a.Region != "SP" ||
		a.PostalCode != "01310-100" || a.CountryCode != "BR" || a.Phone != "+55 11 5550 0100" {
		t.Errorf("address decoded wrongly")
	}
	if fmt.Sprintf("%v %+v %#v", a, a, a) != "[ship-to redacted] [ship-to redacted] [ship-to redacted]" {
		t.Error("the address must redact itself")
	}
}

func TestFailuresAreTheTwoPortSentinelsAndNeverEchoTheAddress(t *testing.T) {
	cases := []struct {
		name string
		h    http.HandlerFunc
		want error
	}{
		{"404", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }, ports.ErrNotFound},
		{"500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }, ports.ErrShipToUnavailable},
		{"403", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }, ports.ErrShipToUnavailable},
		{"not json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("Sample Street <html>")) }, ports.ErrShipToUnavailable},
		{"slow", func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		}, ports.ErrShipToUnavailable},
	}
	for _, c := range cases {
		_, err := serve(t, c.h).GetShipTo(context.Background(), "ord-1")
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "Sample") {
			t.Errorf("%s: the error leaks the body: %v", c.name, err)
		}
	}
}

func TestAnUnreachableServerIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	g, _ := customerordering.New(url, 0)
	if _, err := g.GetShipTo(context.Background(), "ord-1"); !errors.Is(err, ports.ErrShipToUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRefusesABaseThatIsNotAnAbsoluteHTTPURL(t *testing.T) {
	for _, bad := range []string{"", "customer-ordering", "ftp://x", "http://", "://x"} {
		if _, err := customerordering.New(bad, 0); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
