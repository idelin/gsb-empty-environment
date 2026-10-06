#include "lfr/reader.hpp"

#include <algorithm>
#include <cstring>
#include <stdexcept>

namespace lfr {

void BufferView::copy_to(std::byte* out) const {
  for_each_chunk([out](const std::byte* data, size_t length) mutable {
    std::memcpy(out, data, length);
    out += length;
  });
}

SlidingWindowReader::SlidingWindowReader(std::shared_ptr<IMappingBackend> backend,
                                         uint64_t file_size, size_t window_size,
                                         size_t max_resident_windows)
    : backend_(std::move(backend)),
      file_size_(file_size),
      window_size_(window_size),
      max_resident_windows_(max_resident_windows) {
  if (backend_ == nullptr) {
    throw std::invalid_argument("SlidingWindowReader: backend must not be null");
  }
  if (window_size_ == 0) {
    throw std::invalid_argument("SlidingWindowReader: window_size must be > 0");
  }
  if (max_resident_windows_ == 0) {
    throw std::invalid_argument("SlidingWindowReader: max_resident_windows must be > 0");
  }
}

size_t SlidingWindowReader::purge_locked() {
  size_t live = 0;
  for (auto it = resident_.begin(); it != resident_.end();) {
    if (it->second.expired()) {
      it = resident_.erase(it);
    } else {
      ++live;
      ++it;
    }
  }
  live_ = live;
  return live;
}

size_t SlidingWindowReader::resident_windows() const {
  std::lock_guard<std::mutex> lock(mu_);
  return const_cast<SlidingWindowReader*>(this)->purge_locked() + pending_;
}

Expected<BufferView, ReadError> SlidingWindowReader::read(uint64_t offset, size_t length,
                                                          CancellationToken token) {
  if (offset > file_size_ || length > file_size_ - offset) {
    return unexpected(ReadError{ReadErrorCode::kOutOfRange,
                                "read range [" + std::to_string(offset) + ", " +
                                    std::to_string(offset + length) + ") exceeds file size " +
                                    std::to_string(file_size_)});
  }
  if (length == 0) {
    // Zero-length reads never touch the backend or the window budget.
    return BufferView{};
  }
  if (token.cancelled()) {
    return unexpected(ReadError{ReadErrorCode::kCancelled, "read cancelled before mapping"});
  }

  const uint64_t first_window = offset / window_size_;
  const uint64_t last_window = (offset + length - 1) / window_size_;

  std::vector<std::shared_ptr<const detail::Window>> windows;
  windows.reserve(static_cast<size_t>(last_window - first_window + 1));
  for (uint64_t index = first_window; index <= last_window; ++index) {
    if (token.cancelled()) {
      // Dropping `windows` releases every window acquired so far.
      return unexpected(ReadError{ReadErrorCode::kCancelled, "read cancelled mid-span"});
    }
    auto window = acquire(index, token);
    if (!window) {
      return unexpected(window.error());
    }
    windows.push_back(std::move(*window));
  }

  const size_t first_offset = static_cast<size_t>(offset - first_window * window_size_);
  return BufferView{std::move(windows), first_offset, length};
}

Expected<std::shared_ptr<const detail::Window>, ReadError> SlidingWindowReader::acquire(
    uint64_t window_index, const CancellationToken& token) {
  {
    std::lock_guard<std::mutex> lock(mu_);
    purge_locked();
    auto it = resident_.find(window_index);
    if (it != resident_.end()) {
      if (auto existing = it->second.lock()) {
        return existing;
      }
    }
    if (live_ + pending_ >= max_resident_windows_) {
      return unexpected(ReadError{
          ReadErrorCode::kBackpressure,
          "resident window limit " + std::to_string(max_resident_windows_) +
              " reached; release outstanding views and retry"});
    }
    ++pending_;  // reserve a slot while mapping outside the lock
  }

  struct PendingGuard {
    SlidingWindowReader* self;
    ~PendingGuard() {
      std::lock_guard<std::mutex> lock(self->mu_);
      --self->pending_;
    }
  } guard{this};

  const uint64_t window_offset = window_index * window_size_;
  const size_t window_length =
      static_cast<size_t>(std::min<uint64_t>(window_size_, file_size_ - window_offset));

  auto mapped = backend_->map(window_offset, window_length, token);
  if (!mapped) {
    if (mapped.error().code == MapErrorCode::kCancelled) {
      return unexpected(ReadError{ReadErrorCode::kCancelled, mapped.error().message});
    }
    return unexpected(ReadError{ReadErrorCode::kMapFailed,
                                "failed to map window " + std::to_string(window_index) +
                                    ": " + mapped.error().message});
  }

  auto window = std::make_shared<detail::Window>();
  window->index = window_index;
  window->region = *mapped;
  window->backend = backend_;

  {
    std::lock_guard<std::mutex> lock(mu_);
    auto it = resident_.find(window_index);
    if (it != resident_.end()) {
      if (auto existing = it->second.lock()) {
        // Another thread won the race; ours unmaps on destruction.
        return existing;
      }
    }
    resident_[window_index] = window;
    ++live_;
  }
  return window;
}

}  // namespace lfr
