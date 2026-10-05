#include "filelock/lock_manager.hpp"

#include <chrono>

namespace filelock {

LockManager::Handle LockManager::Open() {
  std::lock_guard<std::mutex> lk(mu_);
  return Handle(this, core_.OpenHandle());
}

template <typename CoreOp>
Status LockManager::WaitOp(HandleId id, CoreOp&& op, const CancelToken* tok) {
  std::unique_lock<std::mutex> lk(mu_);
  SeqNo seq = 0;
  Status s = op(core_, id, &seq);
  if (s != Status::kQueued) {
    cv_.notify_all();
    return s;
  }
  for (;;) {
    if (!core_.IsQueued(seq)) {
      // Granted, or the handle was closed under us (which cancels requests).
      return core_.IsOpen(id) ? Status::kOk : Status::kClosed;
    }
    if (tok && tok->IsCanceled()) {
      core_.CancelRequest(seq);
      cv_.notify_all();
      return Status::kCanceled;
    }
    // Timed wait so a token canceled without touching the manager is still
    // observed promptly.
    cv_.wait_for(lk, std::chrono::milliseconds(2));
  }
}

Status LockManager::DoAcquireShared(HandleId id, std::string_view res,
                                    const CancelToken* tok) {
  return WaitOp(id,
                [&](LockCore& c, HandleId h, SeqNo* s) {
                  return c.AcquireShared(h, res, s);
                },
                tok);
}

Status LockManager::DoAcquireExclusive(HandleId id, std::string_view res,
                                       const CancelToken* tok) {
  return WaitOp(id,
                [&](LockCore& c, HandleId h, SeqNo* s) {
                  return c.AcquireExclusive(h, res, s);
                },
                tok);
}

Status LockManager::DoUpgrade(HandleId id, std::string_view res,
                              const CancelToken* tok) {
  return WaitOp(id,
                [&](LockCore& c, HandleId h, SeqNo* s) {
                  return c.UpgradeToExclusive(h, res, s);
                },
                tok);
}

Status LockManager::DoRelease(HandleId id, std::string_view res) {
  std::lock_guard<std::mutex> lk(mu_);
  Status s = core_.Release(id, res);
  cv_.notify_all();
  return s;
}

void LockManager::DoClose(HandleId id) {
  std::lock_guard<std::mutex> lk(mu_);
  core_.CloseHandle(id);
  cv_.notify_all();
}

Status LockManager::Handle::AcquireShared(std::string_view res,
                                          const CancelToken* tok) {
  if (!IsOpen()) return Status::kClosed;
  return mgr_->DoAcquireShared(id_, res, tok);
}

Status LockManager::Handle::AcquireExclusive(std::string_view res,
                                             const CancelToken* tok) {
  if (!IsOpen()) return Status::kClosed;
  return mgr_->DoAcquireExclusive(id_, res, tok);
}

Status LockManager::Handle::UpgradeToExclusive(std::string_view res,
                                               const CancelToken* tok) {
  if (!IsOpen()) return Status::kClosed;
  return mgr_->DoUpgrade(id_, res, tok);
}

Status LockManager::Handle::Release(std::string_view res) {
  if (!IsOpen()) return Status::kClosed;
  return mgr_->DoRelease(id_, res);
}

void LockManager::Handle::Close() {
  if (mgr_ != nullptr && id_ != 0) {
    mgr_->DoClose(id_);
    id_ = 0;
  }
}

std::vector<LockCore::ResourceSnapshot> LockManager::Snapshot() {
  std::lock_guard<std::mutex> lk(mu_);
  return core_.Snapshot();
}

}  // namespace filelock
