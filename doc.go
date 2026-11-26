// Package archiveproof builds fixed continuity certificates for an archive
// host whose verifier is allowed to read only approved records.
//
// A signed timestamp may commit to several evidence nodes.  Visible renewals
// bind the exact old identity and content digest; hidden evidence may only be
// represented by a licensed shared commitment.  Clock offsets are signed
// interval bounds, and every record referencing one permanent clock source
// shares the same offset regardless of signing-key rotation.
//
// BuildCertificate returns a fixed certificate that contains a valid
// acceptance-to-genesis chain for every compatible clocking.  Verify may choose
// a different chain for the actual clocking, but it never gains access to
// evidence outside the certificate.  Small candidate graphs are minimized
// exactly; large graphs use a deterministic shortest-path union followed by
// redundancy elimination.
package archiveproof
