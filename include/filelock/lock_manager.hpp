// Blocking, thread-safe file-lock manager.
//
// Semantics (fixed by this library, see README.md):
//  * Locks are keyed by canonical resource ids (see CanonicalizeResourceId).
//  * Modes: shared, exclusive, and shared->exclusive upgrade.
//  * Fair FIFO queue per resource; new readers may not barge ahead of queued
//    writers/upgraders (no writer starvation).
//  * At most one pending upgrade per resource. A second concurrent upgrade
//    fails immediately with kUpgradeConflict instead of blocking, so two
//    readers upgrading at the same time never wait on each other forever.
//  * A pending upgrader converts as soon as it is the sole remaining holder,
//    with bounded priority over queued new-lock requests.
//  * Canceling a pending upgrade RETAINS the caller's original shared lock.
//  * Canceling a pending shared/exclusive acquire leaves nothing held.
//  * Closing a handle releases every lock it holds and cancels every queued
//    request; a second release of the same lock returns kNotHeld and is a
//    no-op (no double-release).
#pragma once

#include <atomic>
#include <condition_variable>
#include <memory>
#include <mutex>
#include <string_view>

#include "filelock/lock_core.hpp"

namespace filelock {

// Copyable cancellation handle; call Cancel() from any thread.
class CancelToken {
 public:
  CancelToken() : flag_(std::make_shared<std::atomic<bool>>(false)) {}
  void Cancel() const { flag_->store(true, std::memory_order_release); }
  bool IsCanceled() const { return flag_->load(std::memory_order_acquire); }

 private:
  std::shared_ptr<std::atomic<bool>> flag_;
};

class LockManager {
 public:
  LockManager() = default;
  LockManager(const LockManager&) = delete;
  LockManager& operator=(const LockManager&) = delete;

  // Move-only RAII handle. The destructor closes the handle, releasing all
  // locks it holds. A Handle may be used from one thread at a time, except
  // that Close() is thread-safe.
  class Handle {
   public:
    Handle() = default;
    Handle(Handle&& other) noexcept
        : mgr_(other.mgr_), id_(other.id_) {
      other.mgr_ = nullptr;
      other.id_ = 0;
    }
    Handle& operator=(Handle&& other) noexcept {
      if (this != &other) {
        Close();
        mgr_ = other.mgr_;
        id_ = other.id_;
        other.mgr_ = nullptr;
        other.id_ = 0;
      }
      return *this;
    }
    Handle(const Handle&) = delete;
    Handle& operator=(const Handle&) = delete;
    ~Handle() { Close(); }

    // Blocking acquires. If tok is canceled while waiting, the pending
    // request is canceled and kCanceled is returned.
    Status AcquireShared(std::string_view res, const CancelToken* tok = nullptr);
    Status AcquireExclusive(std::string_view res, const CancelToken* tok = nullptr);
    // Requires holding the resource in shared mode. On kCanceled the shared
    // lock is retained. On kUpgradeConflict nothing changes (still shared).
    Status UpgradeToExclusive(std::string_view res, const CancelToken* tok = nullptr);
    Status Release(std::string_view res);
    void Close();
    bool IsOpen() const { return mgr_ != nullptr && id_ != 0; }
    HandleId Id() const { return id_; }

   private:
    friend class LockManager;
    Handle(LockManager* mgr, HandleId id) : mgr_(mgr), id_(id) {}
    LockManager* mgr_ = nullptr;
    HandleId id_ = 0;
  };

  Handle Open();

  // Test/diagnostic hook: deterministic snapshot of the lock table.
  std::vector<LockCore::ResourceSnapshot> Snapshot();

 private:
  template <typename CoreOp>
  Status WaitOp(HandleId id, CoreOp&& op, const CancelToken* tok);

  Status DoAcquireShared(HandleId id, std::string_view res, const CancelToken* tok);
  Status DoAcquireExclusive(HandleId id, std::string_view res, const CancelToken* tok);
  Status DoUpgrade(HandleId id, std::string_view res, const CancelToken* tok);
  Status DoRelease(HandleId id, std::string_view res);
  void DoClose(HandleId id);

  std::mutex mu_;
  std::condition_variable cv_;
  LockCore core_;
};

}  // namespace filelock
