#include "chunkio.h"

#include <algorithm>
#include <cstring>

namespace chunkio {

namespace {

constexpr char kMagic[8] = {'C', 'H', 'N', 'K', 'D', 'I', 'R', '1'};
constexpr size_t kHeaderSize = 12;
constexpr size_t kEntrySize = 44;

uint32_t load_le32(const uint8_t* p) {
  return static_cast<uint32_t>(p[0]) | (static_cast<uint32_t>(p[1]) << 8) |
         (static_cast<uint32_t>(p[2]) << 16) | (static_cast<uint32_t>(p[3]) << 24);
}

uint64_t load_le64(const uint8_t* p) {
  return static_cast<uint64_t>(load_le32(p)) |
         (static_cast<uint64_t>(load_le32(p + 4)) << 32);
}

void store_le32(uint8_t* p, uint32_t v) {
  p[0] = static_cast<uint8_t>(v);
  p[1] = static_cast<uint8_t>(v >> 8);
  p[2] = static_cast<uint8_t>(v >> 16);
  p[3] = static_cast<uint8_t>(v >> 24);
}

void store_le64(uint8_t* p, uint64_t v) {
  store_le32(p, static_cast<uint32_t>(v));
  store_le32(p + 4, static_cast<uint32_t>(v >> 32));
}

void set_err(std::string* err, const char* msg) {
  if (err) *err = msg;
}

} // namespace

const char* status_name(Status s) {
  switch (s) {
    case Status::kOk: return "ok";
    case Status::kDone: return "done";
    case Status::kNeedMoreSpace: return "need_more_space";
    case Status::kBadDirectory: return "bad_directory";
    case Status::kOverlappingDirectory: return "overlapping_directory";
    case Status::kDirectoryOverflow: return "directory_overflow";
    case Status::kPhysicalOutOfRange: return "physical_out_of_range";
    case Status::kBadBlock: return "bad_block";
    case Status::kShortRead: return "short_read";
    case Status::kUnknownCodec: return "unknown_codec";
    case Status::kInvalidArgument: return "invalid_argument";
    case Status::kCancelled: return "cancelled";
  }
  return "unknown";
}

uint32_t crc32(const uint8_t* data, size_t size) {
  static const uint32_t* const table = [] {
    auto* t = new uint32_t[256];
    for (uint32_t i = 0; i < 256; ++i) {
      uint32_t c = i;
      for (int k = 0; k < 8; ++k)
        c = (c & 1) ? (0xEDB88320u ^ (c >> 1)) : (c >> 1);
      t[i] = c;
    }
    return t;
  }();
  uint32_t crc = 0xFFFFFFFFu;
  for (size_t i = 0; i < size; ++i)
    crc = table[(crc ^ data[i]) & 0xFF] ^ (crc >> 8);
  return crc ^ 0xFFFFFFFFu;
}

MemorySource::MemorySource(const uint8_t* data, size_t size)
    : data_(data), size_(size) {}

MemorySource::MemorySource(std::vector<uint8_t> data)
    : owned_(std::move(data)), data_(owned_.data()), size_(owned_.size()) {}

size_t MemorySource::read_at(uint64_t offset, uint8_t* out, size_t size) const {
  if (offset >= size_) return 0;
  const size_t n = std::min<size_t>(size, static_cast<size_t>(size_ - offset));
  std::memcpy(out, data_ + offset, n);
  return n;
}

Status parse_directory(const uint8_t* data, size_t size,
                       std::vector<ChunkEntry>* out, std::string* err) {
  if (!data || !out) return Status::kInvalidArgument;
  if (size < kHeaderSize) {
    set_err(err, "directory truncated before header");
    return Status::kBadDirectory;
  }
  if (std::memcmp(data, kMagic, sizeof(kMagic)) != 0) {
    set_err(err, "bad directory magic");
    return Status::kBadDirectory;
  }
  const uint32_t count = load_le32(data + 8);
  // Division-based check avoids any overflow in count * kEntrySize.
  if (count > (size - kHeaderSize) / kEntrySize) {
    set_err(err, "directory truncated in entry table");
    return Status::kBadDirectory;
  }
  std::vector<ChunkEntry> entries;
  entries.reserve(count);
  uint64_t prev_end = 0;
  for (uint32_t i = 0; i < count; ++i) {
    const uint8_t* p = data + kHeaderSize + static_cast<size_t>(i) * kEntrySize;
    ChunkEntry e;
    e.logical_offset = load_le64(p);
    e.logical_size = load_le64(p + 8);
    e.physical_offset = load_le64(p + 16);
    e.physical_size = load_le64(p + 24);
    e.codec = load_le32(p + 32);
    e.crc32 = load_le32(p + 36);
    if (e.logical_size > UINT64_MAX - e.logical_offset) {
      set_err(err, "logical offset + size overflows");
      return Status::kDirectoryOverflow;
    }
    if (e.physical_size > UINT64_MAX - e.physical_offset) {
      set_err(err, "physical offset + size overflows");
      return Status::kDirectoryOverflow;
    }
    if (!entries.empty() && e.logical_offset < prev_end) {
      set_err(err, "directory entries overlap or are unsorted");
      return Status::kOverlappingDirectory;
    }
    prev_end = e.logical_offset + e.logical_size;
    entries.push_back(e);
  }
  *out = std::move(entries);
  return Status::kOk;
}

std::vector<uint8_t> serialize_directory(const std::vector<ChunkEntry>& entries) {
  std::vector<uint8_t> out(kHeaderSize + entries.size() * kEntrySize);
  std::memcpy(out.data(), kMagic, sizeof(kMagic));
  store_le32(out.data() + 8, static_cast<uint32_t>(entries.size()));
  for (size_t i = 0; i < entries.size(); ++i) {
    uint8_t* p = out.data() + kHeaderSize + i * kEntrySize;
    store_le64(p, entries[i].logical_offset);
    store_le64(p + 8, entries[i].logical_size);
    store_le64(p + 16, entries[i].physical_offset);
    store_le64(p + 24, entries[i].physical_size);
    store_le32(p + 32, entries[i].codec);
    store_le32(p + 36, entries[i].crc32);
  }
  return out;
}

Reader::Reader(const ISource* src, std::vector<ChunkEntry> dir, uint64_t logical_size)
    : src_(src), dir_(std::move(dir)), logical_size_(logical_size) {}

Status Reader::open(const ISource* src, std::vector<ChunkEntry> dir,
                    std::unique_ptr<Reader>* out, std::string* err) {
  if (!src || !out) return Status::kInvalidArgument;
  uint64_t prev_end = 0;
  for (size_t i = 0; i < dir.size(); ++i) {
    const ChunkEntry& e = dir[i];
    if (e.logical_size > UINT64_MAX - e.logical_offset) {
      set_err(err, "logical offset + size overflows");
      return Status::kDirectoryOverflow;
    }
    if (e.physical_size > UINT64_MAX - e.physical_offset) {
      set_err(err, "physical offset + size overflows");
      return Status::kDirectoryOverflow;
    }
    if (i > 0 && e.logical_offset < prev_end) {
      set_err(err, "directory entries overlap or are unsorted");
      return Status::kOverlappingDirectory;
    }
    prev_end = e.logical_offset + e.logical_size;
    if (e.physical_offset + e.physical_size > src->size()) {
      set_err(err, "physical range extends past end of source");
      return Status::kPhysicalOutOfRange;
    }
  }
  const uint64_t logical_size = dir.empty() ? 0 : prev_end;
  out->reset(new Reader(src, std::move(dir), logical_size));
  return Status::kOk;
}

Status Reader::open_range(uint64_t offset, uint64_t length,
                          std::unique_ptr<Session>* out) const {
  if (!out) return Status::kInvalidArgument;
  if (offset > logical_size_) return Status::kInvalidArgument;
  // Clamp the end to the logical size; this also makes offset+length
  // overflow impossible because we never add past logical_size_.
  uint64_t end = logical_size_;
  if (length < logical_size_ - offset) end = offset + length;
  out->reset(new Session(this, offset, end));
  return Status::kOk;
}

Reader::Session::Session(const Reader* reader, uint64_t begin, uint64_t end)
    : reader_(reader), pos_(begin), end_(end), done_(begin >= end) {}

Status Reader::Session::ensure_block(size_t idx) {
  if (cache_valid_ && cached_idx_ == idx) return Status::kOk;
  cache_valid_ = false;
  cache_.clear();
  const ChunkEntry& e = reader_->dir_[idx];
  if (e.physical_size > SIZE_MAX || e.logical_size > SIZE_MAX)
    return Status::kBadBlock;
  std::vector<uint8_t> raw(static_cast<size_t>(e.physical_size));
  const size_t got =
      reader_->src_->read_at(e.physical_offset, raw.data(), raw.size());
  if (got != raw.size()) return Status::kShortRead;

  std::vector<uint8_t> decoded;
  switch (e.codec) {
    case kCodecStore:
      if (e.physical_size != e.logical_size) return Status::kBadBlock;
      decoded = std::move(raw);
      break;
    case kCodecRle: {
      decoded.reserve(static_cast<size_t>(e.logical_size));
      size_t i = 0;
      while (i < raw.size()) {
        if (raw.size() - i < 2) return Status::kBadBlock;
        const uint8_t count = raw[i];
        const uint8_t value = raw[i + 1];
        i += 2;
        if (count == 0) return Status::kBadBlock;
        // Bounded by logical_size, so decoded can never grow past it.
        if (count > e.logical_size - decoded.size()) return Status::kBadBlock;
        decoded.insert(decoded.end(), count, value);
      }
      if (decoded.size() != static_cast<size_t>(e.logical_size))
        return Status::kBadBlock;
      break;
    }
    default:
      return Status::kUnknownCodec;
  }
  // Verify before publishing: no unverified byte ever reaches the cache.
  if (crc32(decoded.data(), decoded.size()) != e.crc32)
    return Status::kBadBlock;
  cache_ = std::move(decoded);
  cached_idx_ = idx;
  cache_valid_ = true;
  return Status::kOk;
}

size_t Reader::Session::read(uint8_t* out, size_t out_size, Status* st) {
  if (cancelled_) {
    *st = Status::kCancelled;
    return 0;
  }
  if (done_) {
    *st = Status::kDone;
    return 0;
  }
  if (!out && out_size > 0) {
    *st = Status::kInvalidArgument;
    return 0;
  }
  const std::vector<ChunkEntry>& dir = reader_->dir_;
  size_t written = 0;
  Status result = Status::kOk;
  while (written < out_size && pos_ < end_) {
    // Advance to the first block whose logical range ends after pos_.
    while (next_idx_ < dir.size() &&
           dir[next_idx_].logical_offset + dir[next_idx_].logical_size <= pos_)
      ++next_idx_;
    if (next_idx_ == dir.size() || dir[next_idx_].logical_offset > pos_) {
      // Gap between blocks (or past the last block): zero-filled region.
      uint64_t gap_end = end_;
      if (next_idx_ < dir.size() && dir[next_idx_].logical_offset < gap_end)
        gap_end = dir[next_idx_].logical_offset;
      const size_t n = static_cast<size_t>(
          std::min<uint64_t>(gap_end - pos_, out_size - written));
      std::memset(out + written, 0, n);
      written += n;
      pos_ += n;
      continue;
    }
    const Status s = ensure_block(next_idx_);
    if (s != Status::kOk) {
      result = s; // bytes from this (unverified) block are not returned
      break;
    }
    const ChunkEntry& e = dir[next_idx_];
    const uint64_t in_block = pos_ - e.logical_offset;
    const uint64_t avail = e.logical_size - in_block;
    const size_t n = static_cast<size_t>(std::min<uint64_t>(
        std::min<uint64_t>(avail, end_ - pos_), out_size - written));
    std::memcpy(out + written, cache_.data() + in_block, n);
    written += n;
    pos_ += n;
  }
  if (pos_ >= end_) {
    done_ = true;
    result = Status::kDone;
  } else if (result == Status::kOk) {
    result = Status::kNeedMoreSpace;
  }
  *st = result;
  return written;
}

void Reader::Session::cancel() {
  cancelled_ = true;
  cache_valid_ = false;
  std::vector<uint8_t>().swap(cache_); // release capacity, not just size
}

} // namespace chunkio
