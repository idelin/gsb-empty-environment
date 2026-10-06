#include "swr/sliding_window_reader.hpp"

#include <algorithm>
#include <condition_variable>
#include <mutex>
#include <stdexcept>
#include <unordered_map>
#include <utility>
#include <vector>

namespace swr {

namespace {

// One resident window: an owned mapping plus its coordinates.
struct Window {
  std::uint64_t index;
  IMappingBackend::Mapping mapping;
};

// Coordinates a single in-flight window load so concurrent readers of the
// same window share one backend map() call.
struct InFlight {
  std::mutex mutex;
  std::condition_variable cv;
  bool done = false;
  Result<std::shared_ptr<Window>> result{std::unexpected(Error::kCancelled)};
};

}  // namespace

struct SlidingWindowReader::State {
  State(std::shared_ptr<IMappingBackend> b, std::size_t ws, std::size_t maxw)
      : backend(std::move(b)), window_size(ws), max_resident(maxw), file_size(backend->size()) {}

  std::shared_ptr<IMappingBackend> backend;
  const std::size_t window_size;
  const std::size_t max_resident;
  const std::uint64_t file_size;

  mutable std::mutex mutex;
  std::unordered_map<std::uint64_t, std::shared_ptr<Window>> cache;
  std::unordered_map<std::uint64_t, std::shared_ptr<InFlight>> inflight;

  // Slots are consumed by resident windows plus in-flight loads.
  std::size_t slots_used_locked() const { return cache.size() + inflight.size(); }

  // Drops cache entries no view is borrowing. Returns windows freed.
  std::size_t evict_unpinned_locked() {
    std::size_t freed = 0;
    for (auto it = cache.begin(); it != cache.end();) {
      if (it->second.use_count() == 1) {  // only the cache references it
        it = cache.erase(it);
        ++freed;
      } else {
        ++it;
      }
    }
    return freed;
  }

  std::size_t window_length(std::uint64_t index) const {
    std::uint64_t offset = index * window_size;
    std::uint64_t remaining = file_size - offset;
    return static_cast<std::size_t>(std::min<std::uint64_t>(window_size, remaining));
  }

  Result<std::shared_ptr<Window>> get_window(std::uint64_t index, const CancelToken& token) {
    std::shared_ptr<InFlight> load;
    bool is_loader = false;
    {
      std::lock_guard lock(mutex);
      if (auto it = cache.find(index); it != cache.end()) return it->second;
      if (auto it = inflight.find(index); it != inflight.end()) {
        load = it->second;
      } else {
        if (slots_used_locked() >= max_resident) {
          evict_unpinned_locked();
          if (slots_used_locked() >= max_resident) {
            return std::unexpected(Error::kBackpressure);
          }
        }
        load = std::make_shared<InFlight>();
        inflight.emplace(index, load);
        is_loader = true;
      }
    }

    if (!is_loader) {
      // Wait for whichever thread is loading this window.
      std::unique_lock lock(load->mutex);
      load->cv.wait(lock, [&] { return load->done; });
      return load->result;
    }

    // Loader path: map outside the cache lock so slow backends (and
    // cancellation) do not block unrelated readers.
    Result<IMappingBackend::Mapping> mapped =
        token->cancelled()
            ? Result<IMappingBackend::Mapping>(std::unexpected(Error::kCancelled))
            : backend->map(index * window_size, window_length(index), token);

    Result<std::shared_ptr<Window>> result =
        mapped ? Result<std::shared_ptr<Window>>(
                     std::make_shared<Window>(Window{index, std::move(*mapped)}))
               : Result<std::shared_ptr<Window>>(std::unexpected(mapped.error()));

    {
      std::lock_guard lock(mutex);
      if (result) cache.emplace(index, *result);
      inflight.erase(index);  // slot released on both success and failure
      std::lock_guard load_lock(load->mutex);
      load->result = result;
      load->done = true;
    }
    load->cv.notify_all();
    return result;
  }

  Result<ReadView> do_read(std::uint64_t offset, std::size_t length,
                           const CancelToken& token) {
  if (length == 0) {
    if (offset > file_size) return std::unexpected(Error::kOutOfRange);
    return ReadView{};  // zero-length reads never touch the backend
  }
  if (offset > file_size || length > file_size - offset) {
    return std::unexpected(Error::kOutOfRange);
  }

  const std::uint64_t first = offset / window_size;
  const std::uint64_t last = (offset + length - 1) / window_size;

  // Resolve every window first; a failure partway must not leak a partial view.
  std::vector<std::shared_ptr<Window>> windows;
  windows.reserve(static_cast<std::size_t>(last - first + 1));
  for (std::uint64_t index = first; index <= last; ++index) {
    Result<std::shared_ptr<Window>> w = get_window(index, token);
    if (!w) return std::unexpected(w.error());
    windows.push_back(std::move(*w));
  }

  ReadView view;
  for (std::size_t i = 0; i < windows.size(); ++i) {
    const Window& w = *windows[i];
    std::size_t begin = (i == 0) ? static_cast<std::size_t>(offset % window_size) : 0;
    std::size_t end = w.mapping.size();
    if (i == windows.size() - 1) {
      end = static_cast<std::size_t>((offset + length - 1) % window_size) + 1;
    }
    view.add_slice(windows[i], w.mapping.data() + begin, end - begin);
  }
  return view;
}
};

SlidingWindowReader::SlidingWindowReader(std::shared_ptr<IMappingBackend> backend,
                                         std::size_t window_size,
                                         std::size_t max_resident_windows)
    : state_(std::make_shared<State>(std::move(backend), window_size, max_resident_windows)) {
  if (window_size == 0 || max_resident_windows == 0) {
    throw std::invalid_argument("window_size and max_resident_windows must be non-zero");
  }
}

SlidingWindowReader::~SlidingWindowReader() = default;

Result<ReadView> SlidingWindowReader::read(std::uint64_t offset, std::size_t length,
                                           const CancelToken& token) const {
  return state_->do_read(offset, length, token);
}

std::future<Result<ReadView>> SlidingWindowReader::read_async(std::uint64_t offset,
                                                              std::size_t length,
                                                              CancelToken token) const {
  std::shared_ptr<State> state = state_;
  return std::async(std::launch::async, [state, offset, length, token = std::move(token)] {
    return state->do_read(offset, length, token);
  });
}

std::size_t SlidingWindowReader::window_size() const { return state_->window_size; }
std::size_t SlidingWindowReader::max_resident_windows() const { return state_->max_resident; }
std::uint64_t SlidingWindowReader::file_size() const { return state_->file_size; }

std::size_t SlidingWindowReader::resident_windows() const {
  std::lock_guard lock(state_->mutex);
  return state_->cache.size();
}

std::size_t SlidingWindowReader::pinned_windows() const {
  std::lock_guard lock(state_->mutex);
  std::size_t pinned = 0;
  for (const auto& [_, w] : state_->cache) {
    if (w.use_count() > 1) ++pinned;
  }
  return pinned;
}

}  // namespace swr
