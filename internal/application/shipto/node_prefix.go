package shipto

import (
	"errors"
	"strings"
)

// ErrDestinationPrefixUnavailable is the return-label refusal when the return
// destination is named by its node and this context holds no usable prefix
// for it: the node's registration has not been consumed yet, or its
// registered postal code leaves no letter or digit from A-Z 0-9
// (409 destination-prefix-unavailable, ADR 0018). It is about this context's
// state, not about the request: the same request may be served later.
var ErrDestinationPrefixUnavailable = errors.New("destination prefix unavailable")

// NodePrefix is the destination prefix a label carries for a node, derived
// from the postal code node-topology registered for it (ADR 0018). It is the
// prefix rule of ADR 0002 A10 restricted to the characters a label prefix may
// hold: the code is upper-cased, everything outside A-Z and 0-9 is dropped
// (punctuation, whitespace, and letters or digits of other scripts, which the
// label's own rule would reject), and the first PrefixLen characters of what
// is left are the prefix, the whole run when it is shorter. A code that
// leaves nothing answers "": the node is known, its prefix is not usable.
func NodePrefix(postalCode string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(postalCode) {
		if b.Len() == PrefixLen {
			break
		}
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
