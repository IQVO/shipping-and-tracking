// Package customerordering is the HTTP adapter of ports.ShipToGateway: the
// model's one synchronous call, GET {base}/orders/{orderId}/ship-to on
// customer-ordering. The address is decoded into a shipto.Address, which
// redacts itself when formatted; nothing here logs the body, and every error
// message names the order and the status, never a field of the address.
package customerordering

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
)

// DefaultTimeout bounds one GetShipTo call (the call precedes the transaction,
// so it never holds a lock).
const DefaultTimeout = 3 * time.Second

// maxBody bounds the response read; a ship-to is a few hundred bytes.
const maxBody = 64 << 10

// Gateway calls customer-ordering.
type Gateway struct {
	base   string
	client *http.Client
}

var _ ports.ShipToGateway = (*Gateway)(nil)

// New builds a Gateway over baseURL (CUSTOMER_ORDERING_URL) with the given
// timeout (DefaultTimeout when not positive). It refuses a base that is not an
// absolute http(s) URL.
func New(baseURL string, timeout time.Duration) (*Gateway, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("CUSTOMER_ORDERING_URL %q is not an absolute http(s) URL", baseURL)
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Gateway{base: u.String(), client: &http.Client{Timeout: timeout}}, nil
}

// GetShipTo implements ports.ShipToGateway. 404 is ports.ErrNotFound; a
// timeout, a transport failure, any other status and an unreadable or invalid
// body are ports.ErrShipToUnavailable.
func (g *Gateway) GetShipTo(ctx context.Context, orderID string) (shipto.Address, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.base+"/orders/"+url.PathEscape(orderID)+"/ship-to", http.NoBody)
	if err != nil {
		return shipto.Address{}, unavailable("build request", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return shipto.Address{}, unavailable("request", errors.Unwrap(err))
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return shipto.Address{}, fmt.Errorf("%w: order %s", ports.ErrNotFound, orderID)
	case resp.StatusCode != http.StatusOK:
		return shipto.Address{}, fmt.Errorf("%w: customer-ordering answered %d", ports.ErrShipToUnavailable, resp.StatusCode)
	}
	var a shipto.Address
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxBody))
	if err := dec.Decode(&a); err != nil {
		return shipto.Address{}, fmt.Errorf("%w: the response is not a ship-to document", ports.ErrShipToUnavailable)
	}
	return a, nil
}

// unavailable wraps a transport failure without echoing the URL (which
// contains the order id only, but the cause may be anything).
func unavailable(what string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s failed", ports.ErrShipToUnavailable, what)
	}
	return fmt.Errorf("%w: %s failed: %v", ports.ErrShipToUnavailable, what, cause)
}
