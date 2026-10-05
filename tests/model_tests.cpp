// Model exploration: BFS over the deterministic LockCore, enumerating every
// interleaving (including cancel branches) of three short handle programs on
// one resource. An independent invariant checker validates every reachable
// state; terminal states must be completely clean and no reachable state may
// be a dead end (which is exactly "two upgrading readers cannot wait on each
// other forever", plus starvation freedom for these programs).
#include <deque>
#include <unordered_set>
#include <vector>

#include "filelock/lock_core.hpp"
#include "invariant_checker.hpp"
#include "test_framework.hpp"

using namespace filelock;
using filelock::testing::CheckSnapshotInvariants;

namespace {

enum class OpType { kAcqShared, kAcqExclusive, kUpgrade, kRelease, kClose };

struct Op {
  OpType type;
  bool cancellable = false;
};

struct HandleState {
  size_t pc = 0;
  SeqNo pending = 0;
  bool done = false;
};

struct Node {
  LockCore core;
  std::vector<HandleState> hs;
};

std::string KeyOf(const Node& n) {
  std::string k = n.core.DebugString();
  for (const auto& h : n.hs) {
    k += "|" + std::to_string(h.pc) + ":" + std::to_string(h.pending) + ":" +
         (h.done ? "1" : "0");
  }
  return k;
}

bool Finished(const HandleState& h, size_t prog_len) {
  return h.done || (h.pc >= prog_len && h.pending == 0);
}

}  // namespace

TEST(model, exhaustive_bfs_two_upgraders_and_writer) {
  const std::vector<std::vector<Op>> programs = {
      {{OpType::kAcqShared, false},
       {OpType::kUpgrade, true},
       {OpType::kRelease, false},
       {OpType::kClose, false}},
      {{OpType::kAcqShared, false},
       {OpType::kUpgrade, true},
       {OpType::kRelease, false},
       {OpType::kClose, false}},
      {{OpType::kAcqExclusive, false},
       {OpType::kRelease, false},
       {OpType::kClose, false}},
  };
  const int kNumHandles = static_cast<int>(programs.size());

  Node root;
  std::vector<HandleId> ids;
  for (int i = 0; i < kNumHandles; ++i) ids.push_back(root.core.OpenHandle());
  root.hs.resize(kNumHandles);

  std::deque<Node> work;
  std::unordered_set<std::string> visited;
  work.push_back(root);
  visited.insert(KeyOf(root));

  int states = 0;
  int terminals = 0;
  bool saw_upgrade_conflict = false;
  bool saw_upgrade_cancel = false;
  bool saw_upgrade_success = false;
  bool saw_queued = false;

  while (!work.empty()) {
    Node node = std::move(work.front());
    work.pop_front();
    ++states;
    ASSERT_LT(states, 4000000);
    CheckSnapshotInvariants(node.core.Snapshot());

    bool all_finished = true;
    for (int h = 0; h < kNumHandles; ++h) {
      if (!Finished(node.hs[h], programs[h].size())) all_finished = false;
    }
    if (all_finished) {
      ++terminals;
      // Every handle closed => the table must be completely empty.
      CHECK(node.core.Snapshot().empty());
      continue;
    }

    int successors = 0;
    for (int h = 0; h < kNumHandles; ++h) {
      const HandleState& hs = node.hs[h];
      if (hs.done) continue;
      if (hs.pending != 0) {
        if (!node.core.IsQueued(hs.pending)) {
          // The request was granted by someone else's release/close/cancel.
          Node n = node;
          n.hs[h].pending = 0;
          std::string key = KeyOf(n);
          if (visited.insert(key).second) work.push_back(std::move(n));
          ++successors;
        } else if (programs[h][hs.pc - 1].cancellable) {
          Node n = node;
          CHECK(n.core.CancelRequest(n.hs[h].pending) == Status::kOk);
          n.hs[h].pending = 0;
          saw_upgrade_cancel = true;
          std::string key = KeyOf(n);
          if (visited.insert(key).second) work.push_back(std::move(n));
          ++successors;
        }
        // Otherwise the handle must wait; other handles make progress.
        continue;
      }
      if (hs.pc >= programs[h].size()) continue;
      const Op& op = programs[h][hs.pc];
      Node n = node;
      SeqNo seq = 0;
      Status st = Status::kOk;
      switch (op.type) {
        case OpType::kAcqShared:
          st = n.core.AcquireShared(ids[h], "r", &seq);
          CHECK(st == Status::kOk || st == Status::kQueued);
          break;
        case OpType::kAcqExclusive:
          st = n.core.AcquireExclusive(ids[h], "r", &seq);
          CHECK(st == Status::kOk || st == Status::kQueued);
          break;
        case OpType::kUpgrade:
          st = n.core.UpgradeToExclusive(ids[h], "r", &seq);
          CHECK(st == Status::kOk || st == Status::kQueued ||
                st == Status::kUpgradeConflict);
          if (st == Status::kUpgradeConflict) saw_upgrade_conflict = true;
          if (st == Status::kOk) saw_upgrade_success = true;
          break;
        case OpType::kRelease:
          st = n.core.Release(ids[h], "r");
          CHECK(st == Status::kOk);
          break;
        case OpType::kClose:
          CHECK(n.core.CloseHandle(ids[h]));
          n.hs[h].done = true;
          break;
      }
      if (st == Status::kQueued) {
        n.hs[h].pending = seq;
        saw_queued = true;
      }
      ++n.hs[h].pc;
      std::string key = KeyOf(n);
      if (visited.insert(key).second) work.push_back(std::move(n));
      ++successors;
    }
    // No dead ends: from every non-terminal state at least one handle can
    // move. In particular the two upgrading readers never block each other
    // permanently (the second upgrade fails immediately with
    // kUpgradeConflict, and a queued upgrade is always cancellable).
    CHECK(successors > 0);
  }

  CHECK(terminals > 0);
  CHECK(saw_queued);
  CHECK(saw_upgrade_conflict);
  CHECK(saw_upgrade_cancel);
  CHECK(saw_upgrade_success);
  std::fprintf(stderr, "  [model] explored %d states, %d terminals\n", states,
               terminals);
}
