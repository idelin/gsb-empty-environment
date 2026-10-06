#pragma once

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <memory>
#include <mutex>

namespace swr {

// Shared cancellation state. cancellable operations observe this token;
// cancelling is thread-safe and wakes any blocked waiters.
class CancelState {
 public:
  void cancel() {
    {
      std::lock_guard lock(mutex_);
      cancelled_.store(true, std::memory_order_release);
    }
    cv_.notify_all();
  }

  bool cancelled() const { return cancelled_.load(std::memory_order_acquire); }

  // Blocks until cancelled or the timeout elapses. Returns true if cancelled.
  template <class Rep, class Period>
  bool wait_for(std::chrono::duration<Rep, Period> d) const {
    std::unique_lock lock(mutex_);
    return cv_.wait_for(lock, d, [&] { return cancelled_.load(std::memory_order_acquire); });
  }

 private:
  mutable std::mutex mutex_;
  mutable std::condition_variable cv_;
  std::atomic<bool> cancelled_{false};
};

using CancelToken = std::shared_ptr<CancelState>;

inline CancelToken make_cancel_token() { return std::make_shared<CancelState>(); }

// A token that is never cancelled, for callers that do not need cancellation.
inline const CancelToken& no_cancel() {
  static const CancelToken token = make_cancel_token();
  return token;
}

}  // namespace swr
