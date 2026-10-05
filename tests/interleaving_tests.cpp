// Interleaving tests:
//  1. Gated exhaustive interleavings: two real threads run a 4-step program
//     against the real blocking LockManager; a conductor forces every
//     possible step interleaving (C(8,4) = 70 schedules) and checks
//     invariants after every step. Per-thread logs are replayed against an
//     independent lock-holding model.
//  2. Barrier stress: two readers lined up on a barrier both upgrade;
//     verifies they never wait on each other forever.
#include <array>
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <functional>
#include <mutex>
#include <thread>
#include <vector>

#include "filelock/lock_manager.hpp"
#include "invariant_checker.hpp"
#include "test_framework.hpp"

using namespace filelock;
using filelock::testing::CheckSnapshotInvariants;

namespace {

class Barrier {
 public:
  explicit Barrier(int n) : n_(n) {}
  void ArriveAndWait() {
    std::unique_lock<std::mutex> lk(mu_);
    int gen = gen_;
    if (++count_ == n_) {
      ++gen_;
      count_ = 0;
      cv_.notify_all();
    } else {
      cv_.wait(lk, [&] { return gen_ != gen; });
    }
  }

 private:
  std::mutex mu_;
  std::condition_variable cv_;
  int n_;
  int count_ = 0;
  int gen_ = 0;
};

// One-thread-at-a-time turn gate controlled by the conductor. Turns are
// counted (not boolean) so consecutive turns given to a blocked thread can
// never collapse into one.
class TurnGate {
 public:
  void WaitTurn() {
    std::unique_lock<std::mutex> lk(mu_);
    cv_.wait(lk, [&] { return turns_ > 0; });
    --turns_;
  }
  void SignalDone() {
    std::lock_guard<std::mutex> lk(mu_);
    ++done_;
    cv_.notify_all();
  }
  void GiveTurn() {
    {
      std::lock_guard<std::mutex> lk(mu_);
      ++turns_;
    }
    cv_.notify_all();
  }
  int DoneCount() {
    std::lock_guard<std::mutex> lk(mu_);
    return done_;
  }
  bool WaitDone(int expected, std::chrono::milliseconds timeout) {
    std::unique_lock<std::mutex> lk(mu_);
    return cv_.wait_for(lk, timeout, [&] { return done_ >= expected; });
  }

 private:
  std::mutex mu_;
  std::condition_variable cv_;
  int turns_ = 0;
  int done_ = 0;
};

enum class StepKind { kAcqShared, kUpgrade, kRelease, kClose };

struct StepResult {
  int step;
  Status status;
};

// Independent per-handle lock-holding model: replays the observed log and
// validates every transition, including "cancel/conflict keeps the shared
// lock" and "double release is rejected".
void CheckHandleLog(const std::vector<StepResult>& log) {
  enum class M { kNone, kShared, kExclusive, kClosed };
  M st = M::kNone;
  static const StepKind kinds[] = {StepKind::kAcqShared, StepKind::kUpgrade,
                                   StepKind::kRelease, StepKind::kClose};
  for (const auto& e : log) {
    switch (kinds[e.step]) {
      case StepKind::kAcqShared:
        if (e.status == Status::kOk) {
          CHECK(st == M::kNone);
          st = M::kShared;
        } else {
          CHECK(e.status == Status::kCanceled);
          CHECK(st == M::kNone);
        }
        break;
      case StepKind::kUpgrade:
        if (e.status == Status::kOk) {
          CHECK(st == M::kShared);
          st = M::kExclusive;
        } else {
          CHECK(e.status == Status::kUpgradeConflict ||
                e.status == Status::kCanceled);
          CHECK(st == M::kShared);  // original shared lock retained
        }
        break;
      case StepKind::kRelease:
        if (e.status == Status::kOk) {
          CHECK(st == M::kShared || st == M::kExclusive);
          st = M::kNone;
        } else {
          CHECK(e.status == Status::kNotHeld);  // double release: rejected
          CHECK(st == M::kNone);
        }
        break;
      case StepKind::kClose:
        CHECK(e.status == Status::kOk);
        st = M::kClosed;
        break;
    }
  }
  CHECK(st == M::kClosed);
}

void RunOneSchedule(const std::vector<int>& schedule,
                    std::atomic<int>* conflict_count,
                    std::atomic<int>* upgrade_ok_count) {
  LockManager mgr;
  std::array<CancelToken, 2> tokens;
  std::array<TurnGate, 2> gates;
  std::array<std::vector<StepResult>, 2> logs;
  std::mutex log_mu;

  auto worker = [&](int tid) {
    auto h = mgr.Open();
    std::vector<std::function<Status()>> steps = {
        [&] { return h.AcquireShared("res", &tokens[tid]); },
        [&] { return h.UpgradeToExclusive("res", &tokens[tid]); },
        [&] { return h.Release("res"); },
        [&] {
          h.Close();
          return Status::kOk;
        },
    };
    for (int i = 0; i < static_cast<int>(steps.size()); ++i) {
      gates[tid].WaitTurn();
      Status s = steps[i]();
      {
        std::lock_guard<std::mutex> lk(log_mu);
        logs[tid].push_back({i, s});
      }
      gates[tid].SignalDone();
    }
  };

  std::array<std::thread, 2> threads = {std::thread(worker, 0),
                                        std::thread(worker, 1)};
  std::array<int, 2> turns_given = {0, 0};
  for (int t : schedule) {
    gates[t].GiveTurn();
    ++turns_given[t];
    // A step may legitimately block (queued behind the other thread); the
    // conductor then moves on and the step completes later.
    gates[t].WaitDone(turns_given[t], std::chrono::milliseconds(30));
    CheckSnapshotInvariants(mgr.Snapshot());
  }
  // Drain: cancel anything still pending and run all remaining steps.
  tokens[0].Cancel();
  tokens[1].Cancel();
  for (int t = 0; t < 2; ++t) {
    for (;;) {
      int done = gates[t].DoneCount();
      if (done >= 4) break;
      gates[t].GiveTurn();
      if (!gates[t].WaitDone(done + 1, std::chrono::milliseconds(500))) {
        CHECK(false);  // a step never completed: the library deadlocked
        break;
      }
    }
  }
  threads[0].join();
  threads[1].join();

  for (int t = 0; t < 2; ++t) {
    CHECK(logs[t].size() == 4);
    CheckHandleLog(logs[t]);
    for (const auto& e : logs[t]) {
      if (e.step == 1 && e.status == Status::kUpgradeConflict) {
        ++(*conflict_count);
      }
      if (e.step == 1 && e.status == Status::kOk) ++(*upgrade_ok_count);
    }
  }
  // After both handles are closed the table must be completely empty:
  // closing a handle releases everything it held.
  CHECK(mgr.Snapshot().empty());
}

}  // namespace

TEST(interleaving, exhaustive_two_reader_upgrade_schedules) {
  std::atomic<int> conflict_count{0};
  std::atomic<int> upgrade_ok_count{0};
  int schedules = 0;
  // All interleavings of four steps of thread 0 and four steps of thread 1.
  auto popcount = [](unsigned v) {
    int n = 0;
    while (v) {
      n += static_cast<int>(v & 1u);
      v >>= 1u;
    }
    return n;
  };
  for (int mask = 0; mask < (1 << 8); ++mask) {
    if (popcount(static_cast<unsigned>(mask)) != 4) continue;
    std::vector<int> schedule;
    for (int bit = 0; bit < 8; ++bit) {
      schedule.push_back((mask >> bit) & 1);
    }
    RunOneSchedule(schedule, &conflict_count, &upgrade_ok_count);
    ++schedules;
  }
  CHECK(schedules == 70);
  // The interesting paths must actually have been exercised.
  CHECK(conflict_count.load() > 0);
  CHECK(upgrade_ok_count.load() > 0);
}

TEST(interleaving, barrier_two_readers_upgrade_never_deadlock) {
  for (int iter = 0; iter < 300; ++iter) {
    LockManager mgr;
    Barrier bar(2);
    std::array<std::atomic<int>, 2> up{{-1, -1}};
    std::array<std::atomic<int>, 2> rel{{-1, -1}};
    auto worker = [&](int idx) {
      auto h = mgr.Open();
      if (h.AcquireShared("res") != Status::kOk) {
        up[idx] = 100;
        return;
      }
      bar.ArriveAndWait();
      up[idx] = static_cast<int>(h.UpgradeToExclusive("res"));
      rel[idx] = static_cast<int>(h.Release("res"));
    };
    std::thread t0(worker, 0);
    std::thread t1(worker, 1);
    t0.join();
    t1.join();  // joining at all proves there was no mutual permanent wait
    int oks = (up[0] == static_cast<int>(Status::kOk)) +
              (up[1] == static_cast<int>(Status::kOk));
    int conflicts = (up[0] == static_cast<int>(Status::kUpgradeConflict)) +
                    (up[1] == static_cast<int>(Status::kUpgradeConflict));
    CHECK(oks == 1);
    CHECK(conflicts == 1);
    CHECK(rel[0] == static_cast<int>(Status::kOk));
    CHECK(rel[1] == static_cast<int>(Status::kOk));
    CHECK(mgr.Snapshot().empty());
  }
}

TEST(interleaving, barrier_upgrade_cancel_race) {
  std::atomic<int> cancels{0};
  std::atomic<int> conflicts{0};
  for (int iter = 0; iter < 300; ++iter) {
    LockManager mgr;
    Barrier bar(3);
    auto ha = mgr.Open();
    auto hb = mgr.Open();
    CHECK(ha.AcquireShared("res") == Status::kOk);
    CHECK(hb.AcquireShared("res") == Status::kOk);
    CancelToken tok_a;
    std::array<std::atomic<int>, 2> up{{-1, -1}};
    std::array<std::atomic<int>, 2> rel{{-1, -1}};
    auto worker = [&](int idx, LockManager::Handle& h, const CancelToken* tok) {
      bar.ArriveAndWait();
      up[idx] = static_cast<int>(h.UpgradeToExclusive("res", tok));
      // Hold the lock briefly so a racing cancel/grant window stays open.
      std::this_thread::sleep_for(std::chrono::milliseconds(5));
      rel[idx] = static_cast<int>(h.Release("res"));
    };
    std::thread ta(worker, 0, std::ref(ha), &tok_a);
    std::thread tb(worker, 1, std::ref(hb), nullptr);
    bar.ArriveAndWait();
    tok_a.Cancel();  // races with A's upgrade; no-op if A is not pending
    ta.join();
    tb.join();
    // Both finished: no deadlock. Whatever happened, each release succeeded
    // exactly once (cancel/conflict retains the shared lock).
    CHECK(rel[0] == static_cast<int>(Status::kOk));
    CHECK(rel[1] == static_cast<int>(Status::kOk));
    int ok = static_cast<int>(Status::kOk);
    int conflict = static_cast<int>(Status::kUpgradeConflict);
    int canceled = static_cast<int>(Status::kCanceled);
    CHECK(up[0] == ok || up[0] == conflict || up[0] == canceled);
    CHECK(up[1] == ok || up[1] == conflict || up[1] == canceled);
    CHECK(up[0] != ok || up[1] != ok);  // never both exclusive
    if (up[0] == canceled) ++cancels;
    if (up[0] == conflict || up[1] == conflict) ++conflicts;
    ha.Close();
    hb.Close();
    CHECK(mgr.Snapshot().empty());
  }
  CHECK(cancels.load() > 0);
  CHECK(conflicts.load() > 0);
}
