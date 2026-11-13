// Package netsec implements atomic, generation-pinned publication of JIT code
// and its metadata for a runtime host.
//
// The core invariant is simple: code, function/instruction boundaries, stack
// maps, exception tables and call-target tables are published as one immutable
// Manifest. A new call resolves the current manifest once and pins that
// manifest for the whole activation. Every metadata lookup then goes through
// the activation's pinned UnitVersion, never through the "current" entry table.
// Consequently an old frame cannot observe new code, and a new frame cannot
// observe old metadata.
//
// Publication is staged deliberately:
//
//  1. Begin registers a complete Manifest as Pending and stages its content.
//  2. Barrier asks every registered thread for its safepoint PC and return
//     address. A thread inside a replaced range must present a lease on the old
//     manifest.
//  3. Commit atomically swaps the entry-table pointer. Until this point new
//     calls still resolve the old manifest; afterwards they resolve the new
//     one. Existing frames keep their pinned manifest until exit.
//
// A retired manifest can only be reclaimed after all frame, callback and
// sampler leases drain. Failed publications abort their staged content, and
// recovery accepts only a manifest whose units and root verify, reclaiming
// everything else as an orphan.
//
// The host is responsible for registering every executing thread with
// Publisher.RegisterThread; FrameThread adapts a Frame to that interface. The
// publisher cannot discover host threads by itself.
//
// StagedPublication plus Explorer model the unsafe component-at-a-time
// publication order. They perform a deterministic breadth-first search over
// thread interleavings and return the shortest witness in which new code runs
// with an old stack map, old exception table and old call-target table while
// still executing "successfully". Replay reproduces a witness exactly.
package netsec
