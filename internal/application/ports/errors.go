package ports

import "errors"

var (
	// ErrNotFound is what every repository, and the ship-to gateway, returns
	// for a lookup that matches nothing (problem type not-found, 404). An
	// adapter wraps its own miss (a missing row, an HTTP 404) into this
	// sentinel, so callers test errors.Is(err, ports.ErrNotFound) and nothing else.
	ErrNotFound = errors.New("not found")
	// ErrConcurrentUpdate is what Save returns when the stored version is not
	// the one the aggregate was loaded at (problem type concurrent-update,
	// 409): the expectation is `stored version == aggregate.Version()-1`, and
	// a create (Version() 1) expects that the id is not stored yet.
	ErrConcurrentUpdate = errors.New("concurrent update")
	// ErrShipToUnavailable is what the ship-to gateway returns when
	// customer-ordering did not answer in time or answered with a failure
	// (problem type ship-to-unavailable, 503).
	ErrShipToUnavailable = errors.New("ship-to unavailable")
)
