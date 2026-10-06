// Test suite for the sliding-window reader, driven by a virtual mapping
// backend (plus one mmap round-trip test against a real temp file).

#include <chrono>
#include <cstdio>
#include <cstring>
#include <fstream>
#include <future>
#include <thread>
#include <vector>

#include "swr/mmap_backend.hpp"
#include "swr/sliding_window_reader.hpp"
#include "virtual_mapping_backend.hpp"

namespace {

int g_failures = 0;

#define CHECK(cond)                                                         \
  do {                                                                      \
    if (!(cond)) {                                                          \
      ++g_failures;                                                         \
      std::fprintf(stderr, "FAIL %s:%d: CHECK(%s)\n", __FILE__, __LINE__,  \
                   #cond);                                                  \
    }                                                                       \
  } while (0)

std::vector<std::byte> make_data(std::size_t n) {
  std::vector<std::byte> data(n);
  for (std::size_t i = 0; i < n; ++i) data[i] = static_cast<std::byte>(i % 251);
  return data;
}

bool bytes_match(const swr::ReadView& view, const std::vector<std::byte>& data,
                 std::size_t offset) {
  if (view.size() > data.size() - offset) return false;
  std::vector<std::byte> got = view.to_vector();
  return std::memcmp(got.data(), data.data() + offset, got.size()) == 0;
}

using swr::Error;
using swr::ReadView;
using swr::SlidingWindowReader;
using swr::make_cancel_token;
using swr::testing::VirtualMappingBackend;

void read_within_window() {
  auto data = make_data(200);
  auto backend = std::make_shared<VirtualMappingBackend>(data);
  SlidingWindowReader reader(backend, /*window_size=*/64, /*max_resident=*/4);
  auto view = reader.read(10, 20);
  CHECK(view.has_value());
  CHECK(view->size() == 20);
  CHECK(view->chunk_count() == 1);
  CHECK(bytes_match(*view, data, 10));
  CHECK(reader.resident_windows() == 1);
}

void cross_boundary_read() {
  auto data = make_data(200);
  auto backend = std::make_shared<VirtualMappingBackend>(data);
  SlidingWindowReader reader(backend, /*window_size=*/64, /*max_resident=*/4);

  // Two windows: [60, 68).
  auto view = reader.read(60, 8);
  CHECK(view.has_value());
  CHECK(view->size() == 8);
  CHECK(view->chunk_count() == 2);
  CHECK(bytes_match(*view, data, 60));
  CHECK(reader.resident_windows() == 2);

  // Three windows: [62, 130).
  auto wide = reader.read(62, 68);
  CHECK(wide.has_value());
  CHECK(wide->size() == 68);
  CHECK(wide->chunk_count() == 3);
  CHECK(bytes_match(*wide, data, 62));

  // Partial copy from the middle of a cross-boundary view.
  std::byte buf[4];
  CHECK(wide->copy_to(buf, 4, /*view_offset=*/2) == 4);
  CHECK(std::memcmp(buf, data.data() + 64, 4) == 0);
}

void view_outlives_cache_and_reader() {
  auto data = make_data(200);
  auto backend = std::make_shared<VirtualMappingBackend>(data);
  ReadView pinned;
  {
    SlidingWindowReader reader(backend, /*window_size=*/64, /*max_resident=*/4);
    auto view = reader.read(60, 8);  // borrows windows 0 and 1
    CHECK(view.has_value());
    pinned = std::move(*view);
    // Touch other windows so the cache slides while `pinned` is alive.
    CHECK(reader.read(128, 8).has_value());
  }  // reader (and its whole cache) is destroyed here
  CHECK(pinned.size() == 8);
  CHECK(bytes_match(pinned, data, 60));  // borrowed data must not dangle
}

void zero_length_read() {
  auto data = make_data(200);
  auto backend = std::make_shared<VirtualMappingBackend>(data);
  SlidingWindowReader reader(backend, /*window_size=*/64, /*max_resident=*/4);

  auto empty = reader.read(0, 0);
  CHECK(empty.has_value());
  CHECK(empty->empty());
  CHECK(empty->chunk_count() == 0);
  CHECK(backend->map_calls() == 0);  // zero-length reads map nothing

  // Zero-length at exactly EOF is valid; past EOF is not.
  CHECK(reader.read(data.size(), 0).has_value());
  CHECK(reader.read(data.size() + 1, 0).error() == Error::kOutOfRange);
  CHECK(backend->map_calls() == 0);

  // A zero-length read succeeds even when the backend is armed to fail.
  backend->fail_next_maps(1);
  CHECK(reader.read(10, 0).has_value());
  CHECK(backend->map_calls() == 0);
}

void backpressure_when_all_windows_pinned() {
  auto data = make_data(256);
  auto backend = std::make_shared<VirtualMappingBackend>(data);
  SlidingWindowReader reader(backend, /*window_size=*/16, /*max_resident=*/2);

  auto a = reader.read(0, 4);    // pins window 0
  auto b = reader.read(16, 4);   // pins window 1
  CHECK(a.has_value() && b.has_value());
  CHECK(reader.resident_windows() == 2);
  CHECK(reader.pinned_windows() == 2);

  // Every slot is pinned by a live view: the reader must apply backpressure
  // rather than evict borrowed data or grow past the limit.
  auto c = reader.read(32, 4);
  CHECK(!c.has_value());
  CHECK(c.error() == Error::kBackpressure);
  CHECK(reader.resident_windows() == 2);

  // Releasing a view unpins its window, so the cache can slide again.
  a = ReadView{};
  auto d = reader.read(32, 4);
  CHECK(d.has_value());
  CHECK(bytes_match(*d, data, 32));
  CHECK(reader.resident_windows() == 2);  // limit still respected
}

void cancellation_releases_slot() {
  auto data = make_data(200);
  auto backend = std::make_shared<VirtualMappingBackend>(data);
  SlidingWindowReader reader(backend, /*window_size=*/64, /*max_resident=*/2);
  backend->set_block_until_cancelled(true);

  auto token = make_cancel_token();
  auto future = reader.read_async(0, 8, token);
  while (backend->map_calls() == 0) std::this_thread::yield();
  token->cancel();

  auto result = future.get();
  CHECK(!result.has_value());
  CHECK(result.error() == Error::kCancelled);
  // The cancelled load must release its slot and every backend resource.
  CHECK(reader.resident_windows() == 0);
  CHECK(backend->active_mappings() == 0);

  // The reader is still usable afterwards.
  backend->set_block_until_cancelled(false);
  auto view = reader.read(0, 8);
  CHECK(view.has_value());
  CHECK(bytes_match(*view, data, 0));
}

void map_failure_releases_slot() {
  auto data = make_data(200);
  auto backend = std::make_shared<VirtualMappingBackend>(data);
  SlidingWindowReader reader(backend, /*window_size=*/64, /*max_resident=*/2);

  backend->fail_next_maps(1);
  auto failed = reader.read(0, 8);
  CHECK(!failed.has_value());
  CHECK(failed.error() == Error::kMapFailed);
  // Failure must not consume a resident slot or leak a mapping.
  CHECK(reader.resident_windows() == 0);
  CHECK(backend->active_mappings() == 0);

  // Retry succeeds once the backend recovers.
  auto view = reader.read(0, 8);
  CHECK(view.has_value());
  CHECK(bytes_match(*view, data, 0));
  CHECK(reader.resident_windows() == 1);
  CHECK(backend->map_calls() == 2);
}

void eof_tail_window_and_out_of_range() {
  auto data = make_data(200);  // windows of 64: 64, 64, 64, 8
  auto backend = std::make_shared<VirtualMappingBackend>(data);
  SlidingWindowReader reader(backend, /*window_size=*/64, /*max_resident=*/4);

  auto tail = reader.read(192, 8);
  CHECK(tail.has_value());
  CHECK(tail->size() == 8);
  CHECK(bytes_match(*tail, data, 192));

  auto past_end = reader.read(192, 9);
  CHECK(!past_end.has_value());
  CHECK(past_end.error() == Error::kOutOfRange);
  CHECK(reader.read(200, 1).error() == Error::kOutOfRange);

  // Cross-boundary read into the short tail window: [120, 200) spans
  // windows 1, 2 and the 8-byte tail window 3.
  auto into_tail = reader.read(120, 80);
  CHECK(into_tail.has_value());
  CHECK(into_tail->chunk_count() == 3);
  CHECK(bytes_match(*into_tail, data, 120));
}

void concurrent_readers_share_one_load() {
  auto data = make_data(200);
  auto backend = std::make_shared<VirtualMappingBackend>(data);
  backend->set_map_delay(std::chrono::milliseconds(30));
  SlidingWindowReader reader(backend, /*window_size=*/64, /*max_resident=*/4);

  auto f1 = reader.read_async(0, 16);
  auto f2 = reader.read_async(8, 16);  // same window while the first load runs
  auto r1 = f1.get();
  auto r2 = f2.get();
  CHECK(r1.has_value() && r2.has_value());
  CHECK(bytes_match(*r1, data, 0));
  CHECK(bytes_match(*r2, data, 8));
  CHECK(backend->map_calls() == 1);  // one load, two waiters
}

void mmap_backend_roundtrip() {
  auto data = make_data(1000);
  const std::string path = "/tmp/swr_mmap_test.bin";
  {
    std::ofstream out(path, std::ios::binary);
    out.write(reinterpret_cast<const char*>(data.data()),
              static_cast<std::streamsize>(data.size()));
  }
  auto backend = std::make_shared<swr::MmapBackend>(path);
  CHECK(backend->size() == data.size());
  // Window size deliberately not a page multiple to exercise alignment.
  SlidingWindowReader reader(backend, /*window_size=*/100, /*max_resident=*/4);
  auto view = reader.read(90, 220);  // [90, 310) crosses windows 0..3
  CHECK(view.has_value());
  CHECK(view->chunk_count() == 4);
  CHECK(bytes_match(*view, data, 90));
  std::remove(path.c_str());
}

}  // namespace

int main() {
  struct {
    const char* name;
    void (*fn)();
  } tests[] = {
      {"read_within_window", read_within_window},
      {"cross_boundary_read", cross_boundary_read},
      {"view_outlives_cache_and_reader", view_outlives_cache_and_reader},
      {"zero_length_read", zero_length_read},
      {"backpressure_when_all_windows_pinned", backpressure_when_all_windows_pinned},
      {"cancellation_releases_slot", cancellation_releases_slot},
      {"map_failure_releases_slot", map_failure_releases_slot},
      {"eof_tail_window_and_out_of_range", eof_tail_window_and_out_of_range},
      {"concurrent_readers_share_one_load", concurrent_readers_share_one_load},
      {"mmap_backend_roundtrip", mmap_backend_roundtrip},
  };
  for (const auto& t : tests) {
    int before = g_failures;
    t.fn();
    std::fprintf(stderr, "%s %s\n", g_failures == before ? "PASS" : "FAIL", t.name);
  }
  if (g_failures == 0) {
    std::fprintf(stderr, "all tests passed\n");
    return 0;
  }
  std::fprintf(stderr, "%d check(s) failed\n", g_failures);
  return 1;
}
