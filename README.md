# lfr — sliding-window large-file reader

Reads large files through a bounded set of fixed-size sliding windows with
safe, owning views and explicit backpressure.

## Design

- **Fixed-size windows**: the file is mapped in `window_size` chunks (the last
  window may be short). Windows are reference-counted `shared_ptr`s; the cache
  only holds `weak_ptr`s.
- **Owning views**: `SlidingWindowReader::read(offset, length)` returns a
  `BufferView` that holds shared references to every window it spans, so a
  cross-window read stays valid no matter how far the window set slides —
  moving windows can never dangle borrowed data.
- **Backpressure**: at most `max_resident_windows` windows may be alive
  (cached or borrowed). Requests that would exceed the limit fail immediately
  with `ReadErrorCode::kBackpressure`; releasing views frees budget.
- **Cancellation**: every read accepts a `lfr::CancellationToken`; it is
  checked before, during, and between window mappings.
- **Failure safety**: a failed `map()` releases its reserved slot and any
  windows already acquired by the same read — no leaks, no half-state.

## Layout

- `include/lfr/backend.hpp` — `IMappingBackend` (mmap/MapViewOfFile/io_uring
  plug in here), `Region`, `MapError`
- `include/lfr/reader.hpp` + `src/reader.cpp` — `SlidingWindowReader`,
  `BufferView`, `ReadError`
- `include/lfr/cancellation.hpp` — `CancellationSource` / `CancellationToken`
- `include/lfr/expected.hpp` — minimal C++17 `Expected`/`Unexpected`
- `tests/virtual_backend.hpp` — deterministic in-memory backend with
  injectable faults, artificial latency, and live-mapping accounting
- `tests/test_main.cpp` — covers cross-boundary reads, zero-length reads,
  EOF partial windows, backpressure and release, borrowed views surviving
  window slides, cancellation (before and during mapping), and mapping-failure
  release

## Build & test

```sh
cmake -S . -B build && cmake --build build
./build/lfr_tests        # or: ctest --test-dir build
```

## Usage

```cpp
auto backend = std::make_shared<MyMmapBackend>(fd, file_size);
lfr::SlidingWindowReader reader(backend, file_size,
                                /*window_size=*/1 << 20,
                                /*max_resident_windows=*/8);

lfr::CancellationSource cancel;
auto view = reader.read(offset, length, cancel.token());
if (!view) {
  switch (view.error().code) {
    case lfr::ReadErrorCode::kBackpressure: /* retry later */ break;
    // kOutOfRange / kCancelled / kMapFailed ...
  }
} else {
  view->for_each_chunk([](const std::byte* data, size_t n) { /* ... */ });
}
```
