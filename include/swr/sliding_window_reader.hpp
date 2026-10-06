#pragma once

#include <cstddef>
#include <cstdint>
#include <future>
#include <memory>

#include "swr/cancel_token.hpp"
#include "swr/error.hpp"
#include "swr/mapping_backend.hpp"
#include "swr/read_view.hpp"

namespace swr {

// Reads a large file through a fixed-size sliding-window cache.
//
//  - The file is divided into windows of `window_size` bytes (the tail
//    window may be smaller).
//  - At most `max_resident_windows` windows are resident at once; a read
//    that would exceed the limit while every resident window is pinned by
//    live views fails with Error::kBackpressure instead of blocking or
//    growing unboundedly.
//  - Reads may span window boundaries; the returned ReadView shares
//    ownership of every window it touches, so sliding/evicting the cache
//    never dangles borrowed data.
//  - Window loads observe a CancelToken; a failed or cancelled load
//    releases its slot so later reads can retry.
class SlidingWindowReader {
 public:
  SlidingWindowReader(std::shared_ptr<IMappingBackend> backend, std::size_t window_size,
                      std::size_t max_resident_windows);
  ~SlidingWindowReader();

  SlidingWindowReader(const SlidingWindowReader&) = delete;
  SlidingWindowReader& operator=(const SlidingWindowReader&) = delete;

  // Synchronous read of [offset, offset+length).
  // A zero-length read always succeeds with an empty view and maps nothing.
  Result<ReadView> read(std::uint64_t offset, std::size_t length,
                        const CancelToken& token = no_cancel()) const;

  // Asynchronous read; the mapping work runs on a worker thread so a slow
  // backend can be cancelled via `token` while blocked.
  std::future<Result<ReadView>> read_async(std::uint64_t offset, std::size_t length,
                                           CancelToken token = make_cancel_token()) const;

  std::size_t window_size() const;
  std::size_t max_resident_windows() const;
  std::uint64_t file_size() const;

  // Number of windows currently held by the cache (pinned or not).
  std::size_t resident_windows() const;

  // Number of resident windows still referenced by live views.
  std::size_t pinned_windows() const;

 private:
  struct State;
  std::shared_ptr<State> state_;
};

}  // namespace swr
