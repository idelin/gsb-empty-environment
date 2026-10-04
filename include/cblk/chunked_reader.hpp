#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <unordered_map>
#include <vector>

namespace cblk {

// On-disk format (all integers little-endian):
//   header:  8-byte magic "CBLKDIR1", u64 entry_count
//   entry:   u64 logical_offset, u64 logical_length,
//            u64 physical_offset, u64 physical_length,
//            u32 codec, u32 crc32 (of the decoded block)
//   payload: physical blocks referenced by the entries
inline constexpr std::size_t kMagicSize = 8;
inline constexpr std::size_t kHeaderSize = 16;
inline constexpr std::size_t kEntrySize = 40;

enum class Codec : std::uint32_t {
  kStore = 0,  // physical bytes are the logical bytes
  kRle = 1,    // PackBits-style run-length encoding
};

enum class ErrorCode {
  kOk = 0,
  kTruncated,             // image ends before the directory or a block
  kBadMagic,              // not a chunked image
  kDirectoryOverflow,     // offset + length wraps around or escapes the image
  kOverlappingDirectory,  // two entries claim the same logical range
  kUnknownCodec,          // entry names an unsupported codec
  kCorruptBlock,          // decoded block failed size or CRC verification
  kOutOfRange,            // logical offset is not covered by the directory
  kInvalidArgument,
};

struct Error {
  ErrorCode code = ErrorCode::kOk;
  std::string message;
  explicit operator bool() const { return code != ErrorCode::kOk; }
};

const char* ErrorCodeName(ErrorCode code);

std::uint32_t Crc32(const std::uint8_t* data, std::size_t size);

// Random-access reader over a chunked compressed image.
// The caller owns the image buffer and must keep it alive.
class Reader {
 public:
  Reader() = default;

  static Reader Open(const std::uint8_t* data, std::size_t size, Error& err);
  static Reader Open(const std::vector<std::uint8_t>& image, Error& err) {
    return Open(image.data(), image.size(), err);
  }

  std::uint64_t logical_size() const { return logical_size_; }
  std::size_t block_count() const { return entries_.size(); }
  std::size_t cached_block_count() const { return cache_.size(); }

  // Releases every decoded block held in the cache. In-flight cursors stay
  // valid; the next ReadSome simply decodes again.
  void Cancel();

  // Continuation state for range reads. When the caller's buffer is too
  // small, call ReadSome again with the same cursor until remaining == 0.
  struct Cursor {
    std::uint64_t offset = 0;
    std::uint64_t remaining = 0;
  };

  // Decodes only blocks intersecting the requested range and copies at most
  // buffer_size bytes into buffer, advancing the cursor. Returns the number
  // of bytes written (0 at end of range / end of logical data). On error
  // returns 0, sets err, and leaves the cursor unchanged.
  std::size_t ReadSome(Cursor& cursor, std::uint8_t* buffer,
                       std::size_t buffer_size, Error& err);

  // One-shot range read. The range is clamped to the logical size, so a
  // read at or past the end yields an empty result without an error.
  Error Read(std::uint64_t offset, std::uint64_t length,
             std::vector<std::uint8_t>& out);

 private:
  struct Entry {
    std::uint64_t logical_offset;
    std::uint64_t logical_length;
    std::uint64_t physical_offset;
    std::uint64_t physical_length;
    Codec codec;
    std::uint32_t crc32;
  };

  const Entry* FindEntry(std::uint64_t logical_offset,
                         std::size_t* index) const;
  const std::vector<std::uint8_t>* DecodeBlock(std::size_t index, Error& err);

  const std::uint8_t* data_ = nullptr;
  std::size_t size_ = 0;
  std::vector<Entry> entries_;  // sorted by logical_offset
  std::uint64_t logical_size_ = 0;
  std::unordered_map<std::size_t, std::vector<std::uint8_t>> cache_;
};

}  // namespace cblk
