#pragma once

#include <cstddef>
#include <cstring>
#include <memory>
#include <utility>
#include <vector>

namespace swr {

// A borrowed view over one or more sliding windows. Each slice keeps its
// backing window alive via shared ownership, so evicting or sliding the
// window cache can never dangle data referenced by a live ReadView.
//
// Cross-window ranges are exposed as a sequence of contiguous chunks
// (data is not guaranteed contiguous across a window boundary).
class ReadView {
 public:
  struct Chunk {
    const std::byte* data;
    std::size_t size;
  };

  ReadView() = default;

  std::size_t size() const { return total_; }
  bool empty() const { return total_ == 0; }
  std::size_t chunk_count() const { return slices_.size(); }

  // Visits each contiguous chunk in order. Stops early if fn returns false.
  template <class Fn>
  void for_each_chunk(Fn&& fn) const {
    for (const Slice& s : slices_) {
      if (!fn(Chunk{s.data, s.size})) return;
    }
  }

  // Copies up to `capacity` bytes starting at `view_offset` within the view.
  // Returns the number of bytes copied.
  std::size_t copy_to(std::byte* out, std::size_t capacity,
                      std::size_t view_offset = 0) const {
    std::size_t copied = 0;
    std::size_t skipped = 0;
    for (const Slice& s : slices_) {
      if (copied >= capacity) break;
      std::size_t chunk_off = 0;
      if (skipped < view_offset) {
        std::size_t skip = std::min(view_offset - skipped, s.size);
        skipped += skip;
        chunk_off = skip;
        if (chunk_off == s.size) continue;
      }
      std::size_t n = std::min(s.size - chunk_off, capacity - copied);
      std::memcpy(out + copied, s.data + chunk_off, n);
      copied += n;
    }
    return copied;
  }

  std::vector<std::byte> to_vector() const {
    std::vector<std::byte> out(total_);
    copy_to(out.data(), out.size());
    return out;
  }

  bool operator==(const ReadView& other) const { return to_vector() == other.to_vector(); }

  // --- internals, used by SlidingWindowReader ---
  struct Slice {
    std::shared_ptr<const void> keepalive;  // pins the backing window
    const std::byte* data;
    std::size_t size;
  };

  void add_slice(std::shared_ptr<const void> keepalive, const std::byte* data,
                 std::size_t size) {
    if (size == 0) return;
    slices_.push_back(Slice{std::move(keepalive), data, size});
    total_ += size;
  }

 private:
  std::vector<Slice> slices_;
  std::size_t total_ = 0;
};

}  // namespace swr
