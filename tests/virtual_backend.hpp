#pragma once

// Virtual in-memory mapping backend used to verify SlidingWindowReader
// behavior: deterministic content, injectable faults, artificial latency for
// cancellation tests, and exact accounting of live mappings.

#include <atomic>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <mutex>
#include <optional>
#include <thread>

#include "lfr/backend.hpp"

class VirtualMappingBackend final : public lfr::IMappingBackend {
public:
  explicit VirtualMappingBackend(uint64_t size) : size_(size) {}

  // Deterministic content: byte at file offset i.
  static std::byte pattern(uint64_t i) {
    return static_cast<std::byte>((i * 131u + 7u) & 0xFFu);
  }

  uint64_t size() const { return size_; }

  int active_mappings() const { return active_.load(); }
  int total_maps() const { return total_maps_.load(); }
  int total_unmaps() const { return total_unmaps_.load(); }

  // Inject a fault: any map() whose offset equals `offset` fails with `code`.
  void fail_maps_at(uint64_t offset, lfr::MapErrorCode code) {
    std::lock_guard<std::mutex> lock(mu_);
    fail_offset_ = offset;
    fail_code_ = code;
  }

  void clear_fault() {
    std::lock_guard<std::mutex> lock(mu_);
    fail_offset_.reset();
  }

  // Artificial mapping latency (ms) so cancellation can land mid-map.
  void set_map_delay_ms(int ms) { map_delay_ms_.store(ms); }

  lfr::Expected<lfr::Region, lfr::MapError> map(uint64_t offset, size_t length,
                                                lfr::CancellationToken token) override {
    if (offset > size_ || length > size_ - offset) {
      return lfr::unexpected(
          lfr::MapError{lfr::MapErrorCode::kInvalidRange, "map range outside virtual file"});
    }
    {
      std::lock_guard<std::mutex> lock(mu_);
      if (fail_offset_ && *fail_offset_ == offset) {
        return lfr::unexpected(lfr::MapError{fail_code_, "injected mapping fault"});
      }
    }
    const int delay = map_delay_ms_.load();
    for (int i = 0; i < delay; ++i) {
      if (token.cancelled()) {
        return lfr::unexpected(
            lfr::MapError{lfr::MapErrorCode::kCancelled, "map cancelled during mapping"});
      }
      std::this_thread::sleep_for(std::chrono::milliseconds(1));
    }
    if (token.cancelled()) {
      return lfr::unexpected(lfr::MapError{lfr::MapErrorCode::kCancelled, "map cancelled"});
    }

    auto buffer = std::make_unique<std::byte[]>(length);
    for (size_t i = 0; i < length; ++i) {
      buffer[i] = pattern(offset + i);
    }
    active_.fetch_add(1);
    total_maps_.fetch_add(1);
    return lfr::Region{buffer.release(), offset, length};
  }

  void unmap(lfr::Region region) noexcept override {
    delete[] region.data;
    active_.fetch_sub(1);
    total_unmaps_.fetch_add(1);
  }

private:
  uint64_t size_;
  std::atomic<int> active_{0};
  std::atomic<int> total_maps_{0};
  std::atomic<int> total_unmaps_{0};
  std::atomic<int> map_delay_ms_{0};
  std::mutex mu_;
  std::optional<uint64_t> fail_offset_;
  lfr::MapErrorCode fail_code_ = lfr::MapErrorCode::kIoFault;
};
