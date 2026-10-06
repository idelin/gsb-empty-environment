#pragma once

#include <cstddef>
#include <cstdint>
#include <string>

#include "lfr/cancellation.hpp"
#include "lfr/expected.hpp"

namespace lfr {

enum class MapErrorCode {
  kInvalidRange,
  kOutOfMemory,
  kIoFault,
  kCancelled,
};

struct MapError {
  MapErrorCode code;
  std::string message;
};

// A mapped region of the file. Ownership is transferred: whoever receives a
// Region from map() must eventually hand it back via unmap().
struct Region {
  std::byte* data = nullptr;
  uint64_t offset = 0;  // file offset of data[0]
  size_t length = 0;
};

// Pluggable mapping backend. Real implementations use mmap / MapViewOfFile /
// io_uring; tests use a virtual in-memory backend.
class IMappingBackend {
public:
  virtual ~IMappingBackend() = default;

  // Maps [offset, offset + length) of the file. Must observe the cancellation
  // token and return MapErrorCode::kCancelled promptly when it is set.
  virtual Expected<Region, MapError> map(uint64_t offset, size_t length,
                                         CancellationToken token) = 0;

  // Releases a region previously returned by map(). Must not throw.
  virtual void unmap(Region region) noexcept = 0;
};

}  // namespace lfr
