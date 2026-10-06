#pragma once

// Virtual mapping backend for tests: serves byte ranges from an in-memory
// buffer, with hooks for latency, blocking-until-cancel, and fault injection.
// Tracks map calls and live mappings so tests can assert that failures and
// cancellations release every resource.

#include <atomic>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <mutex>
#include <thread>
#include <utility>
#include <vector>

#include "swr/mapping_backend.hpp"

namespace swr::testing {

class VirtualMappingBackend : public IMappingBackend {
 public:
  explicit VirtualMappingBackend(std::vector<std::byte> data) : data_(std::move(data)) {}

  // The next n map() calls fail with Error::kMapFailed.
  void fail_next_maps(int n) {
    std::lock_guard lock(mutex_);
    fail_next_ = n;
  }

  // While enabled, map() blocks until the cancel token fires, then returns
  // Error::kCancelled.
  void set_block_until_cancelled(bool on) {
    std::lock_guard lock(mutex_);
    block_until_cancelled_ = on;
  }

  void set_map_delay(std::chrono::milliseconds d) {
    std::lock_guard lock(mutex_);
    delay_ = d;
  }

  std::size_t map_calls() const {
    std::lock_guard lock(mutex_);
    return map_calls_;
  }

  // Number of mappings currently handed out and not yet released.
  std::size_t active_mappings() const {
    std::lock_guard lock(mutex_);
    return active_mappings_;
  }

  Result<Mapping> map(std::uint64_t offset, std::size_t length,
                      const CancelToken& token) override {
    std::chrono::milliseconds delay;
    bool block;
    {
      std::lock_guard lock(mutex_);
      ++map_calls_;
      if (fail_next_ > 0) {
        --fail_next_;
        return std::unexpected(Error::kMapFailed);
      }
      delay = delay_;
      block = block_until_cancelled_;
    }

    if (block) {
      while (!token->cancelled()) token->wait_for(std::chrono::milliseconds(1));
      return std::unexpected(Error::kCancelled);
    }
    // Interruptible sleep: observe cancellation roughly every millisecond.
    for (auto waited = std::chrono::milliseconds(0); waited < delay;
         waited += std::chrono::milliseconds(1)) {
      if (token->cancelled()) return std::unexpected(Error::kCancelled);
      token->wait_for(std::chrono::milliseconds(1));
    }
    if (token->cancelled()) return std::unexpected(Error::kCancelled);
    if (offset > data_.size() || length > data_.size() - offset) {
      return std::unexpected(Error::kOutOfRange);
    }

    {
      std::lock_guard lock(mutex_);
      ++active_mappings_;
    }
    const std::byte* base = data_.data() + offset;
    return Mapping(base, length, [this] {
      std::lock_guard lock(mutex_);
      --active_mappings_;
    });
  }

  std::uint64_t size() const override { return data_.size(); }

 private:
  std::vector<std::byte> data_;
  mutable std::mutex mutex_;
  std::size_t map_calls_ = 0;
  std::size_t active_mappings_ = 0;
  int fail_next_ = 0;
  bool block_until_cancelled_ = false;
  std::chrono::milliseconds delay_{0};
};

}  // namespace swr::testing
