#include "cblk/chunked_writer.hpp"

namespace cblk {
namespace {

void AppendU32(std::vector<std::uint8_t>* out, std::uint32_t v) {
  out->push_back(static_cast<std::uint8_t>(v));
  out->push_back(static_cast<std::uint8_t>(v >> 8));
  out->push_back(static_cast<std::uint8_t>(v >> 16));
  out->push_back(static_cast<std::uint8_t>(v >> 24));
}

void AppendU64(std::vector<std::uint8_t>* out, std::uint64_t v) {
  AppendU32(out, static_cast<std::uint32_t>(v));
  AppendU32(out, static_cast<std::uint32_t>(v >> 32));
}

std::vector<std::uint8_t> RleEncode(const std::vector<std::uint8_t>& in) {
  std::vector<std::uint8_t> out;
  std::size_t i = 0;
  while (i < in.size()) {
    std::size_t run = 1;
    while (i + run < in.size() && in[i + run] == in[i] && run < 128) ++run;
    if (run >= 2) {
      out.push_back(static_cast<std::uint8_t>(127 + run));
      out.push_back(in[i]);
      i += run;
      continue;
    }
    const std::size_t literal_start = i;
    std::size_t literal_len = 0;
    while (i < in.size() && literal_len < 128) {
      run = 1;
      while (i + run < in.size() && in[i + run] == in[i] && run < 128) ++run;
      if (run >= 2) break;
      ++i;
      ++literal_len;
    }
    out.push_back(static_cast<std::uint8_t>(literal_len - 1));
    out.insert(out.end(), in.begin() + literal_start,
               in.begin() + literal_start + literal_len);
  }
  return out;
}

}  // namespace

std::vector<std::uint8_t> BuildImage(const std::vector<ChunkSpec>& chunks) {
  std::vector<std::uint8_t> image;
  const std::uint64_t count = chunks.size();
  image.insert(image.end(), {'C', 'B', 'L', 'K', 'D', 'I', 'R', '1'});
  AppendU64(&image, count);

  const std::uint64_t directory_end = kHeaderSize + count * kEntrySize;
  std::uint64_t logical_offset = 0;
  std::uint64_t physical_offset = directory_end;
  std::vector<std::vector<std::uint8_t>> blocks;
  blocks.reserve(chunks.size());

  for (const ChunkSpec& chunk : chunks) {
    std::vector<std::uint8_t> physical =
        chunk.codec == Codec::kRle ? RleEncode(chunk.data) : chunk.data;
    AppendU64(&image, logical_offset);
    AppendU64(&image, chunk.data.size());
    AppendU64(&image, physical_offset);
    AppendU64(&image, physical.size());
    AppendU32(&image, static_cast<std::uint32_t>(chunk.codec));
    AppendU32(&image, Crc32(chunk.data.data(), chunk.data.size()));
    logical_offset += chunk.data.size();
    physical_offset += physical.size();
    blocks.push_back(std::move(physical));
  }
  for (const auto& block : blocks)
    image.insert(image.end(), block.begin(), block.end());
  return image;
}

}  // namespace cblk
