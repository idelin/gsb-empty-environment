// Independent lock-holding model / invariant checker.
//
// Given a snapshot of the lock table, verifies the safety invariants from
// first principles (it does not reuse any LockCore logic):
//   I1 mutual exclusion: an exclusive holder is the only holder.
//   I2 at most one pending upgrade per resource, and the pending upgrader
//      currently holds the resource in shared mode.
//   I3 queue well-formedness: a queued shared request at the head implies an
//      exclusive holder exists (otherwise it would have been granted); every
//      queued upgrade belongs to the pending upgrader.
//   I4 no holder appears twice on the same resource.
#pragma once

#include <set>

#include "filelock/lock_core.hpp"
#include "test_framework.hpp"

namespace filelock {
namespace testing {

inline void CheckSnapshotInvariants(
    const std::vector<LockCore::ResourceSnapshot>& snaps) {
  for (const auto& s : snaps) {
    // I4
    std::set<HandleId> seen;
    for (const auto& h : s.holders) {
      CHECK(seen.insert(h.first).second);
    }
    // I1
    int exclusive_count = 0;
    for (const auto& h : s.holders) {
      if (h.second == LockMode::kExclusive) ++exclusive_count;
    }
    if (exclusive_count > 0) {
      CHECK(exclusive_count == 1);
      CHECK(s.holders.size() == 1);
    }
    // I2
    int queued_upgrades = 0;
    HandleId upgrade_handle = 0;
    for (const auto& q : s.queue) {
      if (q.kind == LockCore::ReqKind::kUpgrade) {
        ++queued_upgrades;
        upgrade_handle = q.handle;
      }
    }
    CHECK(queued_upgrades <= 1);
    if (s.pending_upgrader != 0) {
      CHECK(queued_upgrades == 1);
      CHECK(s.pending_upgrader == upgrade_handle);
      bool holds_shared = false;
      for (const auto& h : s.holders) {
        if (h.first == s.pending_upgrader &&
            h.second == LockMode::kShared) {
          holds_shared = true;
        }
      }
      CHECK(holds_shared);
    } else {
      CHECK(queued_upgrades == 0);
    }
    // I3
    if (!s.queue.empty() &&
        s.queue.front().kind == LockCore::ReqKind::kShared) {
      CHECK(exclusive_count == 1);
    }
  }
}

}  // namespace testing
}  // namespace filelock
