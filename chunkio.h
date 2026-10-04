// chunkio: random-access range reads over chunked, compressed byte streams.
//
// A chunked file is a sequence of independently compressed physical blocks.
// A directory maps logical (uncompressed) offsets to physical (compressed)
// blocks. Directory entries carry a CRC32 of the uncompressed block payload;
// decoded blocks are verified before any byte is handed to the caller.
#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <string>
#include <vector>

namespace chunkio {

enum class Status {
  kOk = 0,
  kDone,                 // range fully produced
  kNeedMoreSpace,        // output buffer full; call read() again to continue
  kBadDirectory,         // malformed magic/truncated/unsorted directory
  kOverlappingDirectory, // logical ranges of entries overlap
  kDirectoryOverflow,    // offset + size wraps around
  kPhysicalOutOfRange,   // physical range extends past end of source
  kBadBlock,             // block failed decode/size/CRC verification
  kShortRead,            // physical source returned fewer bytes than requested
  kUnknownCodec,         // unsupported codec id
  kInvalidArgument,
  kCancelled,            // session was cancelled
};

const char* status_name(Status s);

constexpr uint32_t kCodecStore = 0; // uncompressed, physical_size == logical_size
constexpr uint32_t kCodecRle = 1;   // (count:u8, value:u8) run pairs, count >= 1

struct ChunkEntry {
  uint64_t logical_offset = 0;
  uint64_t logical_size = 0;
  uint64_t physical_offset = 0;
  uint64_t physical_size = 0;
  uint32_t codec = kCodecStore;
  uint32_t crc32 = 0; // CRC32 (IEEE) of the uncompressed block payload
};

uint32_t crc32(const uint8_t* data, size_t size);

// Random-access byte source backing the physical file.
class ISource {
 public:
  virtual ~ISource() = default;
  // Reads up to `size` bytes at `offset`; returns the number of bytes read
  // (short only at end of source).
  virtual size_t read_at(uint64_t offset, uint8_t* out, size_t size) const = 0;
  virtual uint64_t size() const = 0;
};

class MemorySource final : public ISource {
 public:
  MemorySource(const uint8_t* data, size_t size);
  explicit MemorySource(std::vector<uint8_t> data);
  size_t read_at(uint64_t offset, uint8_t* out, size_t size) const override;
  uint64_t size() const override { return size_; }

 private:
  std::vector<uint8_t> owned_;
  const uint8_t* data_;
  size_t size_;
};

// Serialized directory layout (all integers little-endian):
//   offset 0:  8-byte magic "CHNKDIR1"
//   offset 8:  u32 entry count
//   offset 12: count * 44-byte records:
//              u64 logical_offset, u64 logical_size,
//              u64 physical_offset, u64 physical_size,
//              u32 codec, u32 crc32
// Parsing validates: magic, truncation, per-entry offset+size overflow,
// and that logical ranges are sorted and non-overlapping.
Status parse_directory(const uint8_t* data, size_t size,
                       std::vector<ChunkEntry>* out, std::string* err = nullptr);

std::vector<uint8_t> serialize_directory(const std::vector<ChunkEntry>& entries);

class Reader {
 public:
  // Validates the directory against the source (overflow, overlap, physical
  // bounds) and creates a Reader on success.
  static Status open(const ISource* src, std::vector<ChunkEntry> dir,
                     std::unique_ptr<Reader>* out, std::string* err = nullptr);

  uint64_t logical_size() const { return logical_size_; }
  const std::vector<ChunkEntry>& directory() const { return dir_; }

  // A Session streams one logical range. It decodes at most the blocks that
  // intersect the range, keeps at most one decoded block cached, and can be
  // resumed with a fresh buffer after kNeedMoreSpace.
  class Session {
   public:
    // Writes up to `out_size` bytes of the range into `out`.
    // Returns the number of bytes written and sets `st`:
    //   kDone          range fully produced (bytes written may be < out_size)
    //   kNeedMoreSpace buffer filled before the range ended; call read() again
    //   kBadBlock/...  failure; bytes already returned were verified, bytes
    //                  from the failing block are never returned
    // `out` may be nullptr only when out_size == 0.
    size_t read(uint8_t* out, size_t out_size, Status* st);

    // Aborts the session and releases the decoded-block cache.
    void cancel();

    bool done() const { return done_; }
    bool cancelled() const { return cancelled_; }
    size_t cached_bytes() const { return cache_.size(); }
    uint64_t position() const { return pos_; }

   private:
    friend class Reader;
    Session(const Reader* reader, uint64_t begin, uint64_t end);
    Status ensure_block(size_t idx);

    const Reader* reader_;
    uint64_t pos_;
    uint64_t end_;
    size_t next_idx_ = 0;
    bool done_ = false;
    bool cancelled_ = false;
    std::vector<uint8_t> cache_;
    size_t cached_idx_ = 0;
    bool cache_valid_ = false;
  };

  // Opens a session for logical range [offset, offset + length), clamped to
  // the logical end of the stream. An empty or end-of-stream range yields a
  // session that is immediately done.
  Status open_range(uint64_t offset, uint64_t length,
                    std::unique_ptr<Session>* out) const;

 private:
  Reader(const ISource* src, std::vector<ChunkEntry> dir, uint64_t logical_size);
  const ISource* src_;
  std::vector<ChunkEntry> dir_;
  uint64_t logical_size_;
};

} // namespace chunkio
