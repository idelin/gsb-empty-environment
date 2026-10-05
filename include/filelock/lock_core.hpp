// Deterministic, single-threaded lock-table state machine.
//
// LockCore contains *all* lock semantics (grant / queue / upgrade / cancel /
// close). It never blocks and owns no threads; the blocking LockManager wraps
// it with a mutex + condition variable. Because it is deterministic and
// copyable, tests can exhaustively explore interleavings directly on it.
#pragma once

#include <cstdint>
#include <deque>
#include <optional>
#include <string>
#include <string_view>
#include <unordered_map>
#include <unordered_set>
#include <vector>

namespace filelock {

using HandleId = std::uint64_t;
using SeqNo = std::uint64_t;

enum class LockMode { kShared, kExclusive };

enum class Status {
  kOk,               // granted / completed
  kQueued,           // request enqueued; wait on the returned SeqNo
  kUpgradeConflict,  // another upgrade is already pending on the resource
  kNotHeld,          // release/upgrade/cancel without holding / unknown seq
  kAlreadyHeld,      // acquire while already holding the resource
  kClosed,           // handle is closed
  kCanceled,         // request was canceled before being granted
  kInvalidResource,  // resource id cannot be canonicalized
};

const char* ToString(Status s);
const char* ToString(LockMode m);

// Canonicalizes a resource id: collapses duplicate '/', resolves '.' and '..'
// lexically, strips trailing '/'. Returns nullopt for empty ids or ids that
// would escape the root (e.g. "..", "a/../../b").
std::optional<std::string> CanonicalizeResourceId(std::string_view id);

class LockCore {
 public:
  enum class ReqKind { kShared = 0, kExclusive = 1, kUpgrade = 2 };

  struct RequestInfo {
    SeqNo seq = 0;
    HandleId handle = 0;
    ReqKind kind = ReqKind::kShared;
  };

  // Deterministic (sorted) view of one resource, used by tests/checkers.
  struct ResourceSnapshot {
    std::string id;
    std::vector<std::pair<HandleId, LockMode>> holders;  // sorted by handle
    std::vector<RequestInfo> queue;                      // FIFO order
    HandleId pending_upgrader = 0;                       // 0 = none
  };

  HandleId OpenHandle();
  bool IsOpen(HandleId h) const;

  // Releases every lock held by the handle and cancels every queued request
  // of the handle. Returns false if the handle was not open.
  bool CloseHandle(HandleId h);

  // All acquire/upgrade calls either complete synchronously (kOk / error) or
  // return kQueued and set *seq_out to the queued request id.
  Status AcquireShared(HandleId h, std::string_view res, SeqNo* seq_out);
  Status AcquireExclusive(HandleId h, std::string_view res, SeqNo* seq_out);
  // Requires holding the resource in shared mode. At most one upgrade may be
  // pending per resource; a second one fails immediately with
  // kUpgradeConflict (it never blocks, so two upgrading readers cannot wait
  // on each other forever).
  Status UpgradeToExclusive(HandleId h, std::string_view res, SeqNo* seq_out);
  Status Release(HandleId h, std::string_view res);

  // Cancels a queued request. For an upgrade request the caller's original
  // shared lock is RETAINED (fixed semantics). Returns kOk if the request was
  // queued and is now canceled, kNotHeld if the seq is unknown.
  Status CancelRequest(SeqNo seq);

  bool IsQueued(SeqNo seq) const;

  // Sorted, deterministic snapshot of all non-empty resources.
  std::vector<ResourceSnapshot> Snapshot() const;
  // Canonical textual form of the whole state (for BFS dedup in tests).
  std::string DebugString() const;

 private:
  struct Request {
    SeqNo seq;
    HandleId handle;
    ReqKind kind;
  };
  struct Resource {
    std::unordered_map<HandleId, LockMode> holders;
    std::deque<Request> queue;
    HandleId pending_upgrader = 0;
  };

  Status Prepare(HandleId h, std::string_view res, std::string* id_out) const;
  void IndexRequest(const std::string& res_id, const Request& req);
  void UnindexRequest(const Request& req);
  // Grants whatever is grantable on the resource after a state change.
  void ProcessQueue(const std::string& res_id, Resource& r);

  std::unordered_map<std::string, Resource> resources_;
  std::unordered_set<HandleId> open_handles_;
  std::unordered_map<HandleId, std::unordered_set<std::string>> held_by_handle_;
  std::unordered_map<HandleId, std::unordered_set<SeqNo>> pending_by_handle_;
  std::unordered_map<SeqNo, std::string> resource_of_seq_;
  HandleId next_handle_ = 1;
  SeqNo next_seq_ = 1;
};

}  // namespace filelock
