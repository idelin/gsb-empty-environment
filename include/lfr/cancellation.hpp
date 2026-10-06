#pragma once

#include <atomic>
#include <memory>

namespace lfr {

class CancellationToken {
public:
  CancellationToken() = default;

  bool cancelled() const { return state_ && state_->load(std::memory_order_acquire); }

private:
  friend class CancellationSource;
  explicit CancellationToken(std::shared_ptr<std::atomic<bool>> state)
      : state_(std::move(state)) {}

  std::shared_ptr<std::atomic<bool>> state_;
};

class CancellationSource {
public:
  CancellationSource() : state_(std::make_shared<std::atomic<bool>>(false)) {}

  CancellationToken token() const { return CancellationToken(state_); }

  void cancel() { state_->store(true, std::memory_order_release); }

private:
  std::shared_ptr<std::atomic<bool>> state_;
};

}  // namespace lfr
