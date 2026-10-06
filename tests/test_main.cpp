#include <cstdio>
#include <memory>
#include <thread>
#include <vector>

#include "lfr/reader.hpp"
#include "virtual_backend.hpp"

namespace {

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                        \
  do {                                                                     \
    ++g_checks;                                                            \
    if (!(cond)) {                                                         \
      ++g_failures;                                                        \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
    }                                                                      \
  } while (0)

bool view_matches_pattern(const lfr::BufferView& view, uint64_t file_offset) {
  std::vector<std::byte> bytes(view.size());
  view.copy_to(bytes.data());
  for (size_t i = 0; i < bytes.size(); ++i) {
    if (bytes[i] != VirtualMappingBackend::pattern(file_offset + i)) {
      return false;
    }
  }
  return true;
}

std::shared_ptr<VirtualMappingBackend> make_backend(uint64_t size) {
  return std::make_shared<VirtualMappingBackend>(size);
}

void test_single_window_read() {
  auto backend = make_backend(64 * 10);
  lfr::SlidingWindowReader reader(backend, backend->size(), 64, 4);

  auto view = reader.read(5, 20);
  CHECK(view.has_value());
  CHECK(view->size() == 20);
  CHECK(view_matches_pattern(*view, 5));
  CHECK(backend->active_mappings() == 1);
}

void test_cross_boundary_read() {
  auto backend = make_backend(64 * 10);
  lfr::SlidingWindowReader reader(backend, backend->size(), 64, 8);

  // Spans two windows: bytes 60..67.
  auto two = reader.read(60, 8);
  CHECK(two.has_value());
  CHECK(two->size() == 8);
  CHECK(view_matches_pattern(*two, 60));
  CHECK(backend->active_mappings() == 2);

  // Spans three windows: bytes 50..149.
  auto three = reader.read(50, 100);
  CHECK(three.has_value());
  CHECK(view_matches_pattern(*three, 50));

  // Chunk boundaries line up with window boundaries.
  std::vector<size_t> chunk_sizes;
  three->for_each_chunk([&](const std::byte*, size_t n) { chunk_sizes.push_back(n); });
  CHECK(chunk_sizes.size() == 3);
  CHECK(chunk_sizes[0] == 14);  // 64 - 50
  CHECK(chunk_sizes[1] == 64);
  CHECK(chunk_sizes[2] == 22);  // 100 - 14 - 64
}

void test_zero_length_read() {
  auto backend = make_backend(64 * 10);
  lfr::SlidingWindowReader reader(backend, backend->size(), 64, 4);

  const int maps_before = backend->total_maps();
  auto empty = reader.read(10, 0);
  CHECK(empty.has_value());
  CHECK(empty->size() == 0);
  CHECK(empty->empty());
  CHECK(backend->total_maps() == maps_before);  // backend untouched

  // Zero-length at EOF is valid; beyond EOF is not.
  CHECK(reader.read(backend->size(), 0).has_value());
  auto past_end = reader.read(backend->size() + 1, 0);
  CHECK(!past_end.has_value());
  CHECK(past_end.error().code == lfr::ReadErrorCode::kOutOfRange);
}

void test_eof_partial_window() {
  const uint64_t size = 64 * 4 + 30;
  auto backend = make_backend(size);
  lfr::SlidingWindowReader reader(backend, size, 64, 8);

  auto tail = reader.read(64 * 4 + 10, 20);
  CHECK(tail.has_value());
  CHECK(view_matches_pattern(*tail, 64 * 4 + 10));

  auto over = reader.read(64 * 4 + 10, 21);  // one byte past EOF
  CHECK(!over.has_value());
  CHECK(over.error().code == lfr::ReadErrorCode::kOutOfRange);
}

void test_backpressure_and_release() {
  auto backend = make_backend(64 * 10);
  lfr::SlidingWindowReader reader(backend, backend->size(), 64, 2);

  auto first = reader.read(0, 10);    // window 0
  auto second = reader.read(64, 10);  // window 1
  CHECK(first.has_value());
  CHECK(second.has_value());
  CHECK(reader.resident_windows() == 2);

  // Third distinct window exceeds the budget -> backpressure.
  auto blocked = reader.read(128, 10);
  CHECK(!blocked.has_value());
  CHECK(blocked.error().code == lfr::ReadErrorCode::kBackpressure);

  // Re-referencing an already resident window is still allowed.
  auto again = reader.read(4, 4);
  CHECK(again.has_value());

  // Releasing a view frees budget; the retry succeeds.
  first = lfr::BufferView{};
  again = lfr::BufferView{};
  auto third = reader.read(128, 10);
  CHECK(third.has_value());
  CHECK(view_matches_pattern(*third, 128));

  // A single read spanning more windows than the limit also backpressures,
  // and must not leak the windows it already acquired.
  lfr::SlidingWindowReader tight(backend, backend->size(), 64, 1);
  auto span = tight.read(60, 8);  // needs windows 0 and 1
  CHECK(!span.has_value());
  CHECK(span.error().code == lfr::ReadErrorCode::kBackpressure);
  CHECK(tight.resident_windows() == 0);
}

void test_borrowed_view_survives_window_slide() {
  auto backend = make_backend(64 * 10);
  lfr::SlidingWindowReader reader(backend, backend->size(), 64, 2);

  auto borrowed = reader.read(0, 16);
  CHECK(borrowed.has_value());

  // Slide the window set far forward; the borrowed window is evicted from the
  // cache but must stay mapped because `borrowed` still owns it.
  for (uint64_t w = 1; w <= 5; ++w) {
    auto temp = reader.read(w * 64, 8);
    CHECK(temp.has_value());
  }
  CHECK(backend->active_mappings() == 1);  // only the borrowed window remains
  CHECK(view_matches_pattern(*borrowed, 0));  // data not dangling

  borrowed = lfr::BufferView{};
  CHECK(backend->active_mappings() == 0);
}

void test_cancellation_before_and_during_map() {
  auto backend = make_backend(64 * 10);
  lfr::SlidingWindowReader reader(backend, backend->size(), 64, 4);

  // Pre-cancelled token: no mapping happens at all.
  lfr::CancellationSource source;
  source.cancel();
  const int maps_before = backend->total_maps();
  auto early = reader.read(0, 10, source.token());
  CHECK(!early.has_value());
  CHECK(early.error().code == lfr::ReadErrorCode::kCancelled);
  CHECK(backend->total_maps() == maps_before);

  // Cancellation lands while the backend is mapping.
  backend->set_map_delay_ms(300);
  lfr::CancellationSource mid_source;
  lfr::Expected<lfr::BufferView, lfr::ReadError> result =
      lfr::unexpected(lfr::ReadError{lfr::ReadErrorCode::kOutOfRange, "unset"});
  std::thread worker([&] { result = reader.read(0, 10, mid_source.token()); });
  std::this_thread::sleep_for(std::chrono::milliseconds(50));
  mid_source.cancel();
  worker.join();
  CHECK(!result.has_value());
  CHECK(result.error().code == lfr::ReadErrorCode::kCancelled);
  CHECK(backend->active_mappings() == 0);  // nothing leaked
  CHECK(reader.resident_windows() == 0);

  // The reader recovers after cancellation.
  backend->set_map_delay_ms(0);
  auto after = reader.read(0, 10);
  CHECK(after.has_value());
  CHECK(view_matches_pattern(*after, 0));
}

void test_map_failure_releases_everything() {
  auto backend = make_backend(64 * 10);
  lfr::SlidingWindowReader reader(backend, backend->size(), 64, 4);

  // Window 1 (offset 64) fails; window 0 was already mapped by this read.
  backend->fail_maps_at(64, lfr::MapErrorCode::kIoFault);
  auto failed = reader.read(0, 100);  // spans windows 0 and 1
  CHECK(!failed.has_value());
  CHECK(failed.error().code == lfr::ReadErrorCode::kMapFailed);
  CHECK(backend->active_mappings() == 0);  // window 0 released
  CHECK(reader.resident_windows() == 0);
  CHECK(backend->total_maps() == backend->total_unmaps());

  // Fault cleared: the same read succeeds and returns correct data.
  backend->clear_fault();
  auto ok = reader.read(0, 100);
  CHECK(ok.has_value());
  CHECK(view_matches_pattern(*ok, 0));
}

}  // namespace

int main() {
  test_single_window_read();
  test_cross_boundary_read();
  test_zero_length_read();
  test_eof_partial_window();
  test_backpressure_and_release();
  test_borrowed_view_survives_window_slide();
  test_cancellation_before_and_during_map();
  test_map_failure_releases_everything();

  std::printf("%d checks, %d failures\n", g_checks, g_failures);
  return g_failures == 0 ? 0 : 1;
}
