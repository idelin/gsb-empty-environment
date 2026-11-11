// Package netsec composes multi-layer user-namespace ID mappings for a
// security-sensitive host process.
//
// Each Layer is built from Entry triples (container start, parent start,
// length). NewLayer rejects zero-length entries, intervals that would extend
// past the uint32 ID space, and any overlap between entry intervals in either
// container or parent coordinates. Layers are immutable once validated.
//
// A Chain publishes complete layer chains as immutable Versions atomically;
// queries always run against the pinned Version the caller holds, so a
// publication can never expose a half-updated chain. Version.MapInterval
// returns the exact partition of a queried container interval into host Shards
// and unmapped Holes. Version.MapID returns an explicit boolean for the
// mapped case: unmapped holes are never translated, so a hole cannot be
// mistaken for host ID 0 even when a real mapping legitimately targets 0.
package netsec
