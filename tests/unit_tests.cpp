// Unit tests for LockCore semantics and the blocking LockManager wrapper.
#include <chrono>
#include <thread>

#include "filelock/lock_manager.hpp"
#include "invariant_checker.hpp"
#include "test_framework.hpp"

using namespace filelock;
using filelock::testing::CheckSnapshotInvariants;

namespace {

bool Holds(const LockCore& core, HandleId h, const char* res, LockMode m) {
  for (const auto& s : core.Snapshot()) {
    if (s.id != res) continue;
    for (const auto& holder : s.holders) {
      if (holder.first == h) return holder.second == m;
    }
  }
  return false;
}

size_t QueueLen(const LockCore& core, const char* res) {
  for (const auto& s : core.Snapshot()) {
    if (s.id == res) return s.queue.size();
  }
  return 0;
}

}  // namespace

TEST(unit, canonicalize) {
  CHECK(CanonicalizeResourceId("a//b/./c").value() == "a/b/c");
  CHECK(CanonicalizeResourceId("a/./b").value() == "a/b");
  CHECK(CanonicalizeResourceId("a/b/../c").value() == "a/c");
  CHECK(CanonicalizeResourceId("a/b/").value() == "a/b");
  CHECK(CanonicalizeResourceId("/a//b/").value() == "a/b");
  CHECK(!CanonicalizeResourceId("").has_value());
  CHECK(!CanonicalizeResourceId("/").has_value());
  CHECK(!CanonicalizeResourceId("..").has_value());
  CHECK(!CanonicalizeResourceId("a/../../b").has_value());
  CHECK(!CanonicalizeResourceId(".").has_value());
}

TEST(unit, shared_shared_compatible) {
  LockCore core;
  HandleId a = core.OpenHandle();
  HandleId b = core.OpenHandle();
  CHECK(core.AcquireShared(a, "res", nullptr) == Status::kOk);
  CHECK(core.AcquireShared(b, "res", nullptr) == Status::kOk);
  CheckSnapshotInvariants(core.Snapshot());
  CHECK(core.Release(a, "res") == Status::kOk);
  CHECK(core.Release(b, "res") == Status::kOk);
  CHECK(core.Snapshot().empty());
}

TEST(unit, exclusive_excludes_everything) {
  LockCore core;
  HandleId a = core.OpenHandle();
  HandleId b = core.OpenHandle();
  HandleId c = core.OpenHandle();
  CHECK(core.AcquireExclusive(a, "res", nullptr) == Status::kOk);
  SeqNo seq_b = 0, seq_c = 0;
  CHECK(core.AcquireShared(b, "res", &seq_b) == Status::kQueued);
  CHECK(core.AcquireExclusive(c, "res", &seq_c) == Status::kQueued);
  CheckSnapshotInvariants(core.Snapshot());
  CHECK(core.Release(a, "res") == Status::kOk);
  // FIFO: b's shared request is ahead of c's exclusive request.
  CHECK(Holds(core, b, "res", LockMode::kShared));
  CHECK(!Holds(core, c, "res", LockMode::kExclusive));
  CHECK(core.Release(b, "res") == Status::kOk);
  CHECK(Holds(core, c, "res", LockMode::kExclusive));
  CHECK(core.Release(c, "res") == Status::kOk);
}

TEST(unit, fifo_readers_cannot_barge_queued_writer) {
  LockCore core;
  HandleId a = core.OpenHandle();
  HandleId w = core.OpenHandle();
  HandleId late = core.OpenHandle();
  CHECK(core.AcquireShared(a, "res", nullptr) == Status::kOk);
  SeqNo seq_w = 0, seq_late = 0;
  CHECK(core.AcquireExclusive(w, "res", &seq_w) == Status::kQueued);
  // A new reader arriving behind a queued writer must queue (no barging),
  // which is what keeps the writer from starving.
  CHECK(core.AcquireShared(late, "res", &seq_late) == Status::kQueued);
  CHECK(core.Release(a, "res") == Status::kOk);
  CHECK(Holds(core, w, "res", LockMode::kExclusive));
  CHECK(!Holds(core, late, "res", LockMode::kShared));
  CHECK(core.Release(w, "res") == Status::kOk);
  CHECK(Holds(core, late, "res", LockMode::kShared));
}

TEST(unit, upgrade_basic_and_convert) {
  LockCore core;
  HandleId a = core.OpenHandle();
  CHECK(core.AcquireShared(a, "res", nullptr) == Status::kOk);
  CHECK(core.UpgradeToExclusive(a, "res", nullptr) == Status::kOk);
  CHECK(Holds(core, a, "res", LockMode::kExclusive));
  CHECK(core.Release(a, "res") == Status::kOk);
}

TEST(unit, two_readers_upgrade_no_mutual_wait) {
  LockCore core;
  HandleId a = core.OpenHandle();
  HandleId b = core.OpenHandle();
  CHECK(core.AcquireShared(a, "res", nullptr) == Status::kOk);
  CHECK(core.AcquireShared(b, "res", nullptr) == Status::kOk);
  SeqNo seq_a = 0;
  // First upgrader queues and waits for the other reader to drain.
  CHECK(core.UpgradeToExclusive(a, "res", &seq_a) == Status::kQueued);
  // Second upgrader fails immediately instead of blocking: no deadlock.
  CHECK(core.UpgradeToExclusive(b, "res", nullptr) == Status::kUpgradeConflict);
  CheckSnapshotInvariants(core.Snapshot());
  // b still holds its shared lock and can make progress.
  CHECK(core.Release(b, "res") == Status::kOk);
  CHECK(Holds(core, a, "res", LockMode::kExclusive));
  CHECK(core.Release(a, "res") == Status::kOk);
  CHECK(core.Snapshot().empty());
}

TEST(unit, upgrade_jumps_queued_writer_when_sole_holder) {
  LockCore core;
  HandleId a = core.OpenHandle();
  HandleId w = core.OpenHandle();
  CHECK(core.AcquireShared(a, "res", nullptr) == Status::kOk);
  SeqNo seq_w = 0;
  CHECK(core.AcquireExclusive(w, "res", &seq_w) == Status::kQueued);
  // a is the sole holder: upgrade converts immediately, ahead of the queued
  // writer (bounded priority jump, at most one per resource).
  CHECK(core.UpgradeToExclusive(a, "res", nullptr) == Status::kOk);
  CHECK(Holds(core, a, "res", LockMode::kExclusive));
  CHECK(QueueLen(core, "res") == 1);
  CHECK(core.Release(a, "res") == Status::kOk);
  CHECK(Holds(core, w, "res", LockMode::kExclusive));
}

TEST(unit, cancel_upgrade_retains_shared_lock) {
  LockCore core;
  HandleId a = core.OpenHandle();
  HandleId b = core.OpenHandle();
  CHECK(core.AcquireShared(a, "res", nullptr) == Status::kOk);
  CHECK(core.AcquireShared(b, "res", nullptr) == Status::kOk);
  SeqNo seq_a = 0;
  CHECK(core.UpgradeToExclusive(a, "res", &seq_a) == Status::kQueued);
  CHECK(core.CancelRequest(seq_a) == Status::kOk);
  // Fixed semantics: the original shared lock is retained after cancel.
  CHECK(Holds(core, a, "res", LockMode::kShared));
  CheckSnapshotInvariants(core.Snapshot());
  // b may now become the pending upgrader.
  SeqNo seq_b = 0;
  CHECK(core.UpgradeToExclusive(b, "res", &seq_b) == Status::kQueued);
  CHECK(core.Release(a, "res") == Status::kOk);
  CHECK(Holds(core, b, "res", LockMode::kExclusive));
}

TEST(unit, cancel_queued_acquire_holds_nothing) {
  LockCore core;
  HandleId a = core.OpenHandle();
  HandleId b = core.OpenHandle();
  CHECK(core.AcquireExclusive(a, "res", nullptr) == Status::kOk);
  SeqNo seq_b = 0;
  CHECK(core.AcquireShared(b, "res", &seq_b) == Status::kQueued);
  CHECK(core.CancelRequest(seq_b) == Status::kOk);
  CHECK(!Holds(core, b, "res", LockMode::kShared));
  CHECK(core.CancelRequest(seq_b) == Status::kNotHeld);  // double cancel
  CHECK(core.Release(a, "res") == Status::kOk);
  CHECK(core.Snapshot().empty());
}

TEST(unit, double_release_is_rejected_noop) {
  LockCore core;
  HandleId a = core.OpenHandle();
  HandleId b = core.OpenHandle();
  CHECK(core.AcquireShared(a, "res", nullptr) == Status::kOk);
  CHECK(core.AcquireShared(b, "res", nullptr) == Status::kOk);
  CHECK(core.Release(a, "res") == Status::kOk);
  auto before = core.DebugString();
  CHECK(core.Release(a, "res") == Status::kNotHeld);  // double release
  CHECK(core.DebugString() == before);                // state unchanged
  CHECK(core.Release(a, "never-held") == Status::kNotHeld);
  CHECK(Holds(core, b, "res", LockMode::kShared));
}

TEST(unit, close_releases_everything) {
  LockCore core;
  HandleId a = core.OpenHandle();
  HandleId b = core.OpenHandle();
  CHECK(core.AcquireShared(a, "r1", nullptr) == Status::kOk);
  CHECK(core.AcquireExclusive(a, "r2", nullptr) == Status::kOk);
  CHECK(core.AcquireShared(b, "r3", nullptr) == Status::kOk);
  SeqNo seq_a = 0;
  CHECK(core.AcquireExclusive(a, "r3", &seq_a) == Status::kQueued);
  SeqNo seq_b = 0;
  CHECK(core.AcquireShared(b, "r2", &seq_b) == Status::kQueued);
  CHECK(core.CloseHandle(a));
  // a's held locks are gone and its queued request was canceled.
  CHECK(!Holds(core, a, "r1", LockMode::kShared));
  CHECK(!Holds(core, a, "r2", LockMode::kExclusive));
  CHECK(QueueLen(core, "r3") == 0);
  // b's queued request on r2 got granted by the close.
  CHECK(Holds(core, b, "r2", LockMode::kShared));
  CHECK(!core.IsOpen(a));
  CHECK(!core.CloseHandle(a));  // double close
  CheckSnapshotInvariants(core.Snapshot());
}

TEST(unit, invalid_resource_and_closed_handle) {
  LockCore core;
  HandleId a = core.OpenHandle();
  CHECK(core.AcquireShared(a, "../escape", nullptr) == Status::kInvalidResource);
  CHECK(core.AcquireShared(a, "", nullptr) == Status::kInvalidResource);
  CHECK(core.CloseHandle(a));
  CHECK(core.AcquireShared(a, "res", nullptr) == Status::kClosed);
  CHECK(core.Release(a, "res") == Status::kClosed);
}

TEST(unit, already_held_rejected) {
  LockCore core;
  HandleId a = core.OpenHandle();
  CHECK(core.AcquireShared(a, "res", nullptr) == Status::kOk);
  CHECK(core.AcquireShared(a, "res", nullptr) == Status::kAlreadyHeld);
  CHECK(core.AcquireExclusive(a, "res", nullptr) == Status::kAlreadyHeld);
  CHECK(core.UpgradeToExclusive(a, "res", nullptr) == Status::kOk);
  CHECK(core.UpgradeToExclusive(a, "res", nullptr) == Status::kAlreadyHeld);
}

TEST(unit, canonical_ids_share_one_lock) {
  LockCore core;
  HandleId a = core.OpenHandle();
  HandleId b = core.OpenHandle();
  CHECK(core.AcquireExclusive(a, "dir//sub/./file", nullptr) == Status::kOk);
  SeqNo seq_b = 0;
  // Different spelling of the same canonical id sees the same lock.
  CHECK(core.AcquireShared(b, "dir/sub/file", &seq_b) == Status::kQueued);
  CHECK(core.Release(a, "dir/./sub/file") == Status::kOk);
  CHECK(Holds(core, b, "dir/sub/file", LockMode::kShared));
}

TEST(manager, blocking_acquire_and_release_unblocks) {
  LockManager mgr;
  auto a = mgr.Open();
  CHECK(a.AcquireExclusive("res") == Status::kOk);
  auto b = mgr.Open();
  std::atomic<Status> result{Status::kQueued};
  std::thread t([&] { result = b.AcquireShared("res"); });
  std::this_thread::sleep_for(std::chrono::milliseconds(20));
  CHECK(result.load() == Status::kQueued);  // still blocked
  CHECK(a.Release("res") == Status::kOk);
  t.join();
  CHECK(result.load() == Status::kOk);
  CHECK(b.Release("res") == Status::kOk);
}

TEST(manager, cancel_blocked_acquire) {
  LockManager mgr;
  auto a = mgr.Open();
  CHECK(a.AcquireExclusive("res") == Status::kOk);
  auto b = mgr.Open();
  CancelToken tok;
  std::atomic<Status> result{Status::kQueued};
  std::thread t([&] { result = b.AcquireShared("res", &tok); });
  std::this_thread::sleep_for(std::chrono::milliseconds(20));
  tok.Cancel();
  t.join();
  CHECK(result.load() == Status::kCanceled);
  // Canceled acquire holds nothing; double release is rejected.
  CHECK(b.Release("res") == Status::kNotHeld);
  CHECK(a.Release("res") == Status::kOk);
}

TEST(manager, close_under_blocked_waiter) {
  LockManager mgr;
  auto a = mgr.Open();
  CHECK(a.AcquireExclusive("res") == Status::kOk);
  auto b = mgr.Open();
  std::atomic<Status> result{Status::kQueued};
  std::thread t([&] { result = b.AcquireShared("res"); });
  std::this_thread::sleep_for(std::chrono::milliseconds(20));
  b.Close();
  t.join();
  CHECK(result.load() == Status::kClosed);
  CHECK(a.Release("res") == Status::kOk);
}

TEST(manager, handle_destructor_releases) {
  LockManager mgr;
  {
    auto a = mgr.Open();
    CHECK(a.AcquireExclusive("res") == Status::kOk);
  }  // destructor closes the handle
  CHECK(mgr.Snapshot().empty());
  auto b = mgr.Open();
  CHECK(b.AcquireExclusive("res") == Status::kOk);
}

TEST(manager, cancel_upgrade_retains_shared) {
  LockManager mgr;
  auto a = mgr.Open();
  auto b = mgr.Open();
  CHECK(a.AcquireShared("res") == Status::kOk);
  CHECK(b.AcquireShared("res") == Status::kOk);
  CancelToken tok;
  std::atomic<Status> up{Status::kQueued};
  std::thread t([&] { up = a.UpgradeToExclusive("res", &tok); });
  // Wait until A is the designated pending upgrader.
  CHECK(tf::WaitFor([&] {
    auto snap = mgr.Snapshot();
    return !snap.empty() && snap[0].pending_upgrader == a.Id();
  }));
  tok.Cancel();
  t.join();
  CHECK(up.load() == Status::kCanceled);
  // Fixed semantics: the original shared lock is retained after cancel.
  auto snap = mgr.Snapshot();
  ASSERT(snap.size() == 1);
  CHECK(snap[0].holders.size() == 2);
  CHECK(snap[0].pending_upgrader == 0);
  CHECK(a.Release("res") == Status::kOk);  // proves A still held shared
  CHECK(b.Release("res") == Status::kOk);
  a.Close();
  b.Close();
  CHECK(mgr.Snapshot().empty());
}

TEST(manager, double_release_rejected_and_noop) {
  LockManager mgr;
  auto a = mgr.Open();
  auto b = mgr.Open();
  CHECK(a.AcquireShared("res") == Status::kOk);
  CHECK(b.AcquireShared("res") == Status::kOk);
  auto before = mgr.Snapshot();
  CHECK(a.Release("res") == Status::kOk);
  CHECK(a.Release("res") == Status::kNotHeld);  // double release
  auto after = mgr.Snapshot();
  // b's shared lock must be untouched by the rejected second release.
  ASSERT(after.size() == 1);
  CHECK(after[0].holders.size() == 1);
  CHECK(after[0].holders[0].first == b.Id());
  CHECK(before.size() == 1);
  b.Close();
  a.Close();
}

TEST(manager, close_releases_all_held_and_pending) {
  LockManager mgr;
  auto a = mgr.Open();
  auto b = mgr.Open();
  CHECK(a.AcquireShared("r1") == Status::kOk);
  CHECK(a.AcquireExclusive("r2") == Status::kOk);
  CHECK(b.AcquireExclusive("r3") == Status::kOk);
  CancelToken tok;
  std::atomic<Status> pending{Status::kQueued};
  std::thread t([&] { pending = a.AcquireShared("r3", &tok); });
  // Wait until A's request is queued on r3.
  CHECK(tf::WaitFor([&] {
    for (const auto& s : mgr.Snapshot()) {
      if (s.id == "r3" && !s.queue.empty()) return true;
    }
    return false;
  }));
  a.Close();  // must release r1, r2 and cancel the pending r3 request
  t.join();
  CHECK(pending.load() == Status::kClosed);
  auto snap = mgr.Snapshot();
  for (const auto& s : snap) {
    CHECK(s.id != "r1");
    CHECK(s.id != "r2");
    if (s.id == "r3") CHECK(s.queue.empty());
  }
  b.Close();
  CHECK(mgr.Snapshot().empty());
}

TEST(manager, writer_not_starved_by_new_readers) {
  LockManager mgr;
  auto a = mgr.Open();
  CHECK(a.AcquireShared("res") == Status::kOk);
  auto w = mgr.Open();
  std::atomic<Status> writer{Status::kQueued};
  std::thread t([&] { writer = w.AcquireExclusive("res"); });
  // Wait until the writer is queued.
  CHECK(tf::WaitFor([&] {
    auto snap = mgr.Snapshot();
    return !snap.empty() && !snap[0].queue.empty();
  }));
  // A new reader must not barge ahead of the queued writer.
  auto late = mgr.Open();
  CancelToken tok;
  std::atomic<Status> late_res{Status::kQueued};
  std::thread t2([&] { late_res = late.AcquireShared("res", &tok); });
  std::this_thread::sleep_for(std::chrono::milliseconds(20));
  CHECK(late_res.load() == Status::kQueued);  // still waiting: no barging
  tok.Cancel();
  t2.join();
  CHECK(late_res.load() == Status::kCanceled);
  CHECK(a.Release("res") == Status::kOk);
  t.join();
  CHECK(writer.load() == Status::kOk);
  CHECK(w.Release("res") == Status::kOk);
  // Now the canceled reader can acquire immediately.
  CHECK(late.AcquireShared("res") == Status::kOk);
  late.Close();
  w.Close();
  a.Close();
}
