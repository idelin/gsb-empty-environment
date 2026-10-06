#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <mutex>
#include <string>
#include <unordered_map>
#include <vector>

#include "lfr/backend.hpp"
#include "lfr/cancellation.hpp"
#include "lfr/expected.hpp"

namespace lfr {

enum class ReadErrorCode {
  kOutOfRange,    // [offset, offset+length) lies outside the file
  kBackpressure,  // too many resident windows; retry after releasing views
  kCancelled,     // the cancellation token fired
  kMapFailed,     // the backend failed to map a window
};

struct ReadError {
  ReadErrorCode code;
  std::string message;
};

namespace detail {

// One fixed-size window of the file, backed by a mapped Region. Windows are
// reference-counted: a window is unmapped only when the cache AND every
// outstanding view have released it, so sliding the window set forward can
// never dangle data that is still borrowed.
struct Window {
  uint64_t index = 0;
  Region region;
  std::shared_ptr<IMappingBackend> backend;

  ~Window() {
    if (region.data != nullptr) {
      backend->unmap(region);
    }
  }

  Window() = default;
  Window(const Window&) = delete;
  Window& operator=(const Window&) = delete;
};

}  // namespace detail

// A view over a byte range of the file. Owns shared references to every
// window it spans, keeping the underlying mappings alive for as long as the
// view exists — including across window slides and cache evictions.
class BufferView {
public:
  BufferView() = default;

  size_t size() const { return length_; }
  bool empty() const { return length_ == 0; }

  // Copies the view's bytes into `out`, which must hold at least size() bytes.
  void copy_to(std::byte* out) const;

  // Invokes fn(const std::byte* data, size_t length) once per spanned window,
  // in file order. The pointer is valid only during the callback.
  template <typename F>
  void for_each_chunk(F&& fn) const {
    size_t remaining = length_;
    size_t skip = first_offset_;
    for (const auto& window : windows_) {
      if (remaining == 0) break;
      size_t available = window->region.length - skip;
      size_t n = available < remaining ? available : remaining;
      fn(window->region.data + skip, n);
      remaining -= n;
      skip = 0;
    }
  }

private:
  friend class SlidingWindowReader;

  BufferView(std::vector<std::shared_ptr<const detail::Window>> windows,
             size_t first_offset, size_t length)
      : windows_(std::move(windows)), first_offset_(first_offset), length_(length) {}

  std::vector<std::shared_ptr<const detail::Window>> windows_;
  size_t first_offset_ = 0;  // offset of the view inside the first window
  size_t length_ = 0;
};

// Reads a large file through a bounded set of fixed-size sliding windows.
//
// - At most max_resident_windows windows are alive at any time; windows still
//   borrowed by live BufferViews count against the limit. Requests that would
//   exceed the limit fail with ReadErrorCode::kBackpressure instead of
//   blocking or evicting borrowed data.
// - A read may span multiple adjacent windows; the returned BufferView owns
//   them all.
class SlidingWindowReader {
public:
  SlidingWindowReader(std::shared_ptr<IMappingBackend> backend, uint64_t file_size,
                      size_t window_size, size_t max_resident_windows);

  SlidingWindowReader(const SlidingWindowReader&) = delete;
  SlidingWindowReader& operator=(const SlidingWindowReader&) = delete;

  uint64_t file_size() const { return file_size_; }
  size_t window_size() const { return window_size_; }
  size_t max_resident_windows() const { return max_resident_windows_; }

  // Number of windows currently alive (cached or borrowed by views).
  size_t resident_windows() const;

  Expected<BufferView, ReadError> read(uint64_t offset, size_t length,
                                       CancellationToken token = {});

private:
  Expected<std::shared_ptr<const detail::Window>, ReadError> acquire(
      uint64_t window_index, const CancellationToken& token);

  // Drops expired weak entries; returns the number of live windows.
  // Caller must hold mu_.
  size_t purge_locked();

  std::shared_ptr<IMappingBackend> backend_;
  uint64_t file_size_;
  size_t window_size_;
  size_t max_resident_windows_;

  mutable std::mutex mu_;
  std::unordered_map<uint64_t, std::weak_ptr<detail::Window>> resident_;
  size_t live_ = 0;     // live windows (valid while holding mu_ after purge)
  size_t pending_ = 0;  // windows currently being mapped outside the lock
};

}  // namespace lfr
