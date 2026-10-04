#pragma once

#include <cstdint>
#include <vector>

#include "cblk/chunked_reader.hpp"

namespace cblk {

struct ChunkSpec {
  std::vector<std::uint8_t> data;
  Codec codec = Codec::kStore;
};

// Serializes chunks into a single image: directory first, then physical
// blocks laid out back to back. Logical offsets are assigned sequentially.
std::vector<std::uint8_t> BuildImage(const std::vector<ChunkSpec>& chunks);

}  // namespace cblk
