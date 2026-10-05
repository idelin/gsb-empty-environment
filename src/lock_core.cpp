#include "filelock/lock_core.hpp"

#include <algorithm>
#include <sstream>

namespace filelock {

const char* ToString(Status s) {
  switch (s) {
    case Status::kOk: return "kOk";
    case Status::kQueued: return "kQueued";
    case Status::kUpgradeConflict: return "kUpgradeConflict";
    case Status::kNotHeld: return "kNotHeld";
    case Status::kAlreadyHeld: return "kAlreadyHeld";
    case Status::kClosed: return "kClosed";
    case Status::kCanceled: return "kCanceled";
    case Status::kInvalidResource: return "kInvalidResource";
  }
  return "?";
}

const char* ToString(LockMode m) {
  return m == LockMode::kShared ? "shared" : "exclusive";
}

std::optional<std::string> CanonicalizeResourceId(std::string_view id) {
  std::vector<std::string> parts;
  size_t i = 0;
  while (i <= id.size()) {
    size_t j = id.find('/', i);
    if (j == std::string_view::npos) j = id.size();
    std::string_view seg = id.substr(i, j - i);
    if (seg.empty() || seg == ".") {
      // skip
    } else if (seg == "..") {
      if (parts.empty()) return std::nullopt;  // escapes the root
      parts.pop_back();
    } else {
      parts.emplace_back(seg);
    }
    i = j + 1;
  }
  if (parts.empty()) return std::nullopt;
  std::string out;
  for (size_t k = 0; k < parts.size(); ++k) {
    if (k) out.push_back('/');
    out += parts[k];
  }
  return out;
}

HandleId LockCore::OpenHandle() {
  HandleId h = next_handle_++;
  open_handles_.insert(h);
  return h;
}

bool LockCore::IsOpen(HandleId h) const { return open_handles_.count(h) != 0; }

bool LockCore::CloseHandle(HandleId h) {
  if (!IsOpen(h)) return false;
  std::vector<std::string> affected;
  auto held_it = held_by_handle_.find(h);
  if (held_it != held_by_handle_.end()) {
    for (const auto& res_id : held_it->second) {
      auto rit = resources_.find(res_id);
      if (rit != resources_.end()) {
        rit->second.holders.erase(h);
        affected.push_back(res_id);
      }
    }
    held_by_handle_.erase(held_it);
  }
  auto pend_it = pending_by_handle_.find(h);
  if (pend_it != pending_by_handle_.end()) {
    for (SeqNo seq : pend_it->second) {
      auto sit = resource_of_seq_.find(seq);
      if (sit == resource_of_seq_.end()) continue;
      const std::string res_id = sit->second;
      resource_of_seq_.erase(sit);
      auto rit = resources_.find(res_id);
      if (rit == resources_.end()) continue;
      Resource& r = rit->second;
      for (auto q = r.queue.begin(); q != r.queue.end(); ++q) {
        if (q->seq == seq) {
          if (q->kind == ReqKind::kUpgrade && r.pending_upgrader == h) {
            r.pending_upgrader = 0;
          }
          r.queue.erase(q);
          break;
        }
      }
      affected.push_back(res_id);
    }
    pending_by_handle_.erase(pend_it);
  }
  open_handles_.erase(h);
  for (const auto& res_id : affected) {
    auto rit = resources_.find(res_id);
    if (rit != resources_.end()) ProcessQueue(res_id, rit->second);
  }
  return true;
}

Status LockCore::Prepare(HandleId h, std::string_view res,
                         std::string* id_out) const {
  if (!IsOpen(h)) return Status::kClosed;
  auto id = CanonicalizeResourceId(res);
  if (!id) return Status::kInvalidResource;
  *id_out = std::move(*id);
  return Status::kOk;
}

void LockCore::IndexRequest(const std::string& res_id, const Request& req) {
  resource_of_seq_[req.seq] = res_id;
  pending_by_handle_[req.handle].insert(req.seq);
}

void LockCore::UnindexRequest(const Request& req) {
  resource_of_seq_.erase(req.seq);
  auto it = pending_by_handle_.find(req.handle);
  if (it != pending_by_handle_.end()) {
    it->second.erase(req.seq);
    if (it->second.empty()) pending_by_handle_.erase(it);
  }
}

void LockCore::ProcessQueue(const std::string& res_id, Resource& r) {
  for (;;) {
    // Upgrade priority: once the pending upgrader is the sole remaining
    // holder, it converts immediately, jumping ahead of queued *new* lock
    // requests. This is what makes reader-upgrade deadlock-free; the jump is
    // bounded (at most one upgrader per resource) so queued writers cannot
    // starve.
    if (r.pending_upgrader != 0 && r.holders.size() == 1) {
      auto sole = r.holders.begin();
      if (sole->first == r.pending_upgrader &&
          sole->second == LockMode::kShared) {
        for (auto it = r.queue.begin(); it != r.queue.end(); ++it) {
          if (it->kind == ReqKind::kUpgrade &&
              it->handle == r.pending_upgrader) {
            UnindexRequest(*it);
            r.queue.erase(it);
            break;
          }
        }
        sole->second = LockMode::kExclusive;
        r.pending_upgrader = 0;
        continue;
      }
    }
    if (r.queue.empty()) return;
    const Request& head = r.queue.front();
    if (head.kind == ReqKind::kShared) {
      bool any_exclusive = false;
      for (const auto& kv : r.holders) {
        if (kv.second == LockMode::kExclusive) {
          any_exclusive = true;
          break;
        }
      }
      if (any_exclusive) return;
      r.holders[head.handle] = LockMode::kShared;
      held_by_handle_[head.handle].insert(res_id);
      UnindexRequest(head);
      r.queue.pop_front();
      continue;
    }
    if (head.kind == ReqKind::kExclusive) {
      if (!r.holders.empty()) return;
      r.holders[head.handle] = LockMode::kExclusive;
      held_by_handle_[head.handle].insert(res_id);
      UnindexRequest(head);
      r.queue.pop_front();
      continue;
    }
    // Head is an upgrade that is not (yet) sole holder: nothing to do.
    return;
  }
}

Status LockCore::AcquireShared(HandleId h, std::string_view res,
                               SeqNo* seq_out) {
  std::string id;
  Status prep = Prepare(h, res, &id);
  if (prep != Status::kOk) return prep;
  Resource& r = resources_[id];
  if (r.holders.count(h)) return Status::kAlreadyHeld;
  bool any_exclusive = false;
  for (const auto& kv : r.holders) {
    if (kv.second == LockMode::kExclusive) any_exclusive = true;
  }
  // FIFO fairness: while the queue is non-empty, new readers may not barge
  // ahead of queued writers/upgraders (writer starvation freedom).
  if (r.queue.empty() && !any_exclusive) {
    r.holders[h] = LockMode::kShared;
    held_by_handle_[h].insert(id);
    return Status::kOk;
  }
  Request req{next_seq_++, h, ReqKind::kShared};
  r.queue.push_back(req);
  IndexRequest(id, req);
  if (seq_out) *seq_out = req.seq;
  return Status::kQueued;
}

Status LockCore::AcquireExclusive(HandleId h, std::string_view res,
                                  SeqNo* seq_out) {
  std::string id;
  Status prep = Prepare(h, res, &id);
  if (prep != Status::kOk) return prep;
  Resource& r = resources_[id];
  if (r.holders.count(h)) return Status::kAlreadyHeld;
  if (r.queue.empty() && r.holders.empty()) {
    r.holders[h] = LockMode::kExclusive;
    held_by_handle_[h].insert(id);
    return Status::kOk;
  }
  Request req{next_seq_++, h, ReqKind::kExclusive};
  r.queue.push_back(req);
  IndexRequest(id, req);
  if (seq_out) *seq_out = req.seq;
  return Status::kQueued;
}

Status LockCore::UpgradeToExclusive(HandleId h, std::string_view res,
                                    SeqNo* seq_out) {
  std::string id;
  Status prep = Prepare(h, res, &id);
  if (prep != Status::kOk) return prep;
  Resource& r = resources_[id];
  auto it = r.holders.find(h);
  if (it == r.holders.end()) return Status::kNotHeld;
  if (it->second == LockMode::kExclusive) return Status::kAlreadyHeld;
  // Fixed rule: at most one pending upgrade per resource. A second upgrader
  // fails immediately instead of waiting, so two readers that both upgrade
  // can never wait on each other forever.
  if (r.pending_upgrader != 0) return Status::kUpgradeConflict;
  if (r.holders.size() == 1) {
    // Sole holder: convert in place, even if new-lock requests are queued
    // (bounded priority jump, see ProcessQueue).
    it->second = LockMode::kExclusive;
    return Status::kOk;
  }
  Request req{next_seq_++, h, ReqKind::kUpgrade};
  r.queue.push_back(req);
  r.pending_upgrader = h;
  IndexRequest(id, req);
  if (seq_out) *seq_out = req.seq;
  return Status::kQueued;
}

Status LockCore::Release(HandleId h, std::string_view res) {
  std::string id;
  Status prep = Prepare(h, res, &id);
  if (prep != Status::kOk) return prep;
  auto rit = resources_.find(id);
  if (rit == resources_.end()) return Status::kNotHeld;
  Resource& r = rit->second;
  auto it = r.holders.find(h);
  if (it == r.holders.end()) return Status::kNotHeld;  // double release: no-op
  r.holders.erase(it);
  auto hb = held_by_handle_.find(h);
  if (hb != held_by_handle_.end()) {
    hb->second.erase(id);
    if (hb->second.empty()) held_by_handle_.erase(hb);
  }
  ProcessQueue(id, r);
  return Status::kOk;
}

Status LockCore::CancelRequest(SeqNo seq) {
  auto sit = resource_of_seq_.find(seq);
  if (sit == resource_of_seq_.end()) return Status::kNotHeld;
  const std::string res_id = sit->second;
  auto rit = resources_.find(res_id);
  if (rit == resources_.end()) return Status::kNotHeld;
  Resource& r = rit->second;
  for (auto it = r.queue.begin(); it != r.queue.end(); ++it) {
    if (it->seq == seq) {
      Request req = *it;
      r.queue.erase(it);
      UnindexRequest(req);
      if (req.kind == ReqKind::kUpgrade && r.pending_upgrader == req.handle) {
        r.pending_upgrader = 0;
      }
      // For upgrades the caller's shared lock stays in holders_ untouched:
      // canceling an upgrade retains the original shared lock.
      ProcessQueue(res_id, r);
      return Status::kOk;
    }
  }
  return Status::kNotHeld;
}

bool LockCore::IsQueued(SeqNo seq) const {
  return resource_of_seq_.count(seq) != 0;
}

std::vector<LockCore::ResourceSnapshot> LockCore::Snapshot() const {
  std::vector<ResourceSnapshot> out;
  for (const auto& kv : resources_) {
    const Resource& r = kv.second;
    if (r.holders.empty() && r.queue.empty()) continue;
    ResourceSnapshot snap;
    snap.id = kv.first;
    for (const auto& h : r.holders) snap.holders.push_back(h);
    std::sort(snap.holders.begin(), snap.holders.end());
    for (const auto& q : r.queue) {
      snap.queue.push_back(RequestInfo{q.seq, q.handle, q.kind});
    }
    snap.pending_upgrader = r.pending_upgrader;
    out.push_back(std::move(snap));
  }
  std::sort(out.begin(), out.end(),
            [](const ResourceSnapshot& a, const ResourceSnapshot& b) {
              return a.id < b.id;
            });
  return out;
}

std::string LockCore::DebugString() const {
  std::ostringstream os;
  for (const auto& snap : Snapshot()) {
    os << snap.id << " holders=";
    for (const auto& h : snap.holders) {
      os << h.first << (h.second == LockMode::kShared ? "S" : "X") << ",";
    }
    os << " queue=";
    for (const auto& q : snap.queue) {
      os << q.seq << ":" << q.handle << ":" << static_cast<int>(q.kind) << ",";
    }
    os << " up=" << snap.pending_upgrader << ";";
  }
  os << " open=";
  std::vector<HandleId> hs(open_handles_.begin(), open_handles_.end());
  std::sort(hs.begin(), hs.end());
  for (HandleId h : hs) os << h << ",";
  return os.str();
}

}  // namespace filelock
