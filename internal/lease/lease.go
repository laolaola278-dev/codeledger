// Package lease defines the identity and duration primitives for task leases
// and the project mutation lock. Lease IDs are unique per acquisition and can
// be injected for deterministic tests.
package lease

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// DefaultDuration is the lease duration used when a caller does not specify
// one (project mutation lock default).
const DefaultDuration = 2 * time.Minute

// IDGen generates a new lease ID. It is injectable so tests can produce
// deterministic IDs; the production default is RandomID.
type IDGen func() string

// RandomID returns a cryptographically random lease ID of the form
// "lease-<32 lowercase hex chars>" (128 bits of entropy).
func RandomID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand never fails on supported platforms; fall back to a
		// zero-padded ID rather than panic (panic must not express business
		// failure at the process boundary).
		return "lease-" + hex.EncodeToString(buf[:])
	}
	return "lease-" + hex.EncodeToString(buf[:])
}

// StaticID returns an IDGen that always returns the given ID. It is intended
// for tests only.
func StaticID(id string) IDGen {
	return func() string { return id }
}

// Auth is the transport-neutral lease authorization a mutating command
// supplies to the service layer. It carries the fencing credentials (agent +
// lease-id) plus the explicit local-administrator override (force + reason).
// The service layer interprets it identically regardless of which command
// (or future embedded application) supplied it; the CLI never performs its
// own ownership checks outside the project mutation lock.
type Auth struct {
	// Agent is the identity the caller declares for itself.
	Agent string
	// LeaseID is the per-acquisition fencing token. It is REQUIRED (together
	// with Agent) whenever a lock record exists for the target task; it is
	// only omitted on the no-record compatibility path.
	LeaseID string
	// Force requests an explicit local-administrator override of an existing
	// active/expired/legacy record. It requires a non-empty Reason and Agent.
	Force bool
	// Reason documents why a forced override is being performed (audited).
	Reason string
}
