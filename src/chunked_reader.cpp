#include "cblk/chunked_reader.hpp"

#include <algorithm>
#include <cstring>
#include <limits>

namespace cblk {
namespace {

constexpr std::uint8_t kMagic[kMagicSize] = {'C', 'B', 'L', 'K',
                                             'D', 'I', 'R', '1'};

bool CheckedAdd(std::uint64_t a, std::uint64_t b, std::uint64_t* out) {
  if (a > std::numeric_limits<std::uint64_t>::max() - b) return false;
  *out = a + b;
  return true;
}

std::uint32_t LoadU32(const std::uint8_t* p) {
  return static_cast<std::uint32_t>(p[0]) |
         (static_cast<std::uint32_t>(p[1]) << 8) |
         (static_cast<std::uint32_t>(p[2]) << 16) |
         (static_cast<std::uint32_t>(p[3]) << 24);
}

std::uint64_t LoadU64(const std::uint8_t* p) {
  return static_cast<std::uint64_t>(LoadU32(p)) |
         (static_cast<std::uint64_t>(LoadU32(p + 4)) << 32);
}

Error MakeError(ErrorCode code, std::string message) {
  return Error{code, std::move(message)};
}

// Decodes one RLE block, refusing to emit more than expected_size bytes and
// requiring the input to be consumed exactly.
bool RleDecode(const std::uint8_t* in, std::size_t in_size,
               std::uint64_t expected_size, std::vector<std::uint8_t>* out) {
  if (expected_size > std::numeric_limits<std::size_t>::max()) return false;
  out->clear();
  out->reserve(static_cast<std::size_t>(expected_size));
  std::size_t pos = 0;
  while (pos < in_size) {
    const std::uint8_t tag = in[pos++];
    if (tag < 128) {  // literal run of tag + 1 bytes
      const std::size_t count = static_cast<std::size_t>(tag) + 1;
      if (count > in_size - pos) return false;
      if (count > expected_size - out->size()) return false;
      out->insert(out->end(), in + pos, in + pos + count);
      pos += count;
    } else {  // repeat next byte (tag - 127) times
      const std::size_t count = static_cast<std::size_t>(tag) - 127;
      if (pos >= in_size) return false;
      if (count > expected_size - out->size()) return false;
      out->insert(out->end(), count, in[pos++]);
    }
  }
  return pos == in_size && out->size() == expected_size;
}

}  // namespace

const char* ErrorCodeName(ErrorCode code) {
  switch (code) {
    case ErrorCode::kOk: return "kOk";
    case ErrorCode::kTruncated: return "kTruncated";
    case ErrorCode::kBadMagic: return "kBadMagic";
    case ErrorCode::kDirectoryOverflow: return "kDirectoryOverflow";
    case ErrorCode::kOverlappingDirectory: return "kOverlappingDirectory";
    case ErrorCode::kUnknownCodec: return "kUnknownCodec";
    case ErrorCode::kCorruptBlock: return "kCorruptBlock";
    case ErrorCode::kOutOfRange: return "kOutOfRange";
    case ErrorCode::kInvalidArgument: return "kInvalidArgument";
  }
  return "kUnknown";
}

std::uint32_t Crc32(const std::uint8_t* data, std::size_t size) {
  static std::uint32_t table[256];
  static const bool initialized = [] {
    for (std::uint32_t i = 0; i < 256; ++i) {
      std::uint32_t c = i;
      for (int k = 0; k < 8; ++k)
        c = (c & 1) ? (0xEDB88320u ^ (c >> 1)) : (c >> 1);
      table[i] = c;
    }
    return true;
  }();
  (void)initialized;
  std::uint32_t crc = 0xFFFFFFFFu;
  for (std::size_t i = 0; i < size; ++i)
    crc = table[(crc ^ data[i]) & 0xFF] ^ (crc >> 8);
  return crc ^ 0xFFFFFFFFu;
}

Reader Reader::Open(const std::uint8_t* data, std::size_t size, Error& err) {
  Reader reader;
  err = {};
  if (data == nullptr && size != 0) {
    err = MakeError(ErrorCode::kInvalidArgument, "null image");
    return reader;
  }
  if (size < kHeaderSize) {
    err = MakeError(ErrorCode::kTruncated, "image smaller than header");
    return reader;
  }
  if (std::memcmp(data, kMagic, kMagicSize) != 0) {
    err = MakeError(ErrorCode::kBadMagic, "bad magic");
    return reader;
  }
  const std::uint64_t count = LoadU64(data + kMagicSize);
  if (count > (size - kHeaderSize) / kEntrySize) {
    err = MakeError(ErrorCode::kTruncated, "directory extends past image");
    return reader;
  }

  std::vector<Entry> entries;
  entries.reserve(static_cast<std::size_t>(count));
  for (std::uint64_t i = 0; i < count; ++i) {
    const std::uint8_t* p = data + kHeaderSize + i * kEntrySize;
    Entry e;
    e.logical_offset = LoadU64(p);
    e.logical_length = LoadU64(p + 8);
    e.physical_offset = LoadU64(p + 16);
    e.physical_length = LoadU64(p + 24);
    const std::uint32_t codec = LoadU32(p + 32);
    e.crc32 = LoadU32(p + 36);

    std::uint64_t end = 0;
    if (!CheckedAdd(e.logical_offset, e.logical_length, &end)) {
      err = MakeError(ErrorCode::kDirectoryOverflow,
                      "logical range wraps around");
      return Reader{};
    }
    if (!CheckedAdd(e.physical_offset, e.physical_length, &end) ||
        end > size) {
      err = MakeError(ErrorCode::kDirectoryOverflow,
                      "physical range escapes image");
      return Reader{};
    }
    switch (codec) {
      case static_cast<std::uint32_t>(Codec::kStore):
        if (e.physical_length != e.logical_length) {
          err = MakeError(ErrorCode::kCorruptBlock,
                          "store block length mismatch");
          return Reader{};
        }
        e.codec = Codec::kStore;
        break;
      case static_cast<std::uint32_t>(Codec::kRle):
        e.codec = Codec::kRle;
        break;
      default:
        err = MakeError(ErrorCode::kUnknownCodec, "unsupported codec");
        return Reader{};
    }
    entries.push_back(e);
  }

  std::sort(entries.begin(), entries.end(),
            [](const Entry& a, const Entry& b) {
              return a.logical_offset < b.logical_offset;
            });
  for (std::size_t i = 1; i < entries.size(); ++i) {
    if (entries[i].logical_offset <
        entries[i - 1].logical_offset + entries[i - 1].logical_length) {
      err = MakeError(ErrorCode::kOverlappingDirectory,
                      "logical ranges overlap");
      return Reader{};
    }
  }

  reader.data_ = data;
  reader.size_ = size;
  reader.entries_ = std::move(entries);
  reader.logical_size_ =
      reader.entries_.empty()
          ? 0
          : reader.entries_.back().logical_offset +
                reader.entries_.back().logical_length;
  return reader;
}

void Reader::Cancel() { cache_.clear(); }

const Reader::Entry* Reader::FindEntry(std::uint64_t logical_offset,
                                       std::size_t* index) const {
  auto it = std::upper_bound(
      entries_.begin(), entries_.end(), logical_offset,
      [](std::uint64_t value, const Entry& e) {
        return value < e.logical_offset;
      });
  if (it == entries_.begin()) return nullptr;
  --it;
  if (logical_offset >= it->logical_offset + it->logical_length)
    return nullptr;
  *index = static_cast<std::size_t>(it - entries_.begin());
  return &*it;
}

const std::vector<std::uint8_t>* Reader::DecodeBlock(std::size_t index,
                                                     Error& err) {
  auto cached = cache_.find(index);
  if (cached != cache_.end()) return &cached->second;

  const Entry& e = entries_[index];
  const std::uint8_t* physical = data_ + e.physical_offset;
  const std::size_t physical_size = static_cast<std::size_t>(e.physical_length);

  std::vector<std::uint8_t> decoded;
  bool ok = false;
  switch (e.codec) {
    case Codec::kStore:
      decoded.assign(physical, physical + physical_size);
      ok = true;
      break;
    case Codec::kRle:
      ok = RleDecode(physical, physical_size, e.logical_length, &decoded);
      break;
  }
  // Never publish a block whose size or checksum does not match the
  // directory: callers must not see unverified data.
  if (!ok || decoded.size() != e.logical_length ||
      Crc32(decoded.data(), decoded.size()) != e.crc32) {
    err = MakeError(ErrorCode::kCorruptBlock, "block failed verification");
    return nullptr;
  }
  return &cache_.emplace(index, std::move(decoded)).first->second;
}

std::size_t Reader::ReadSome(Cursor& cursor, std::uint8_t* buffer,
                             std::size_t buffer_size, Error& err) {
  err = {};
  if (buffer == nullptr && buffer_size != 0) {
    err = MakeError(ErrorCode::kInvalidArgument, "null buffer");
    return 0;
  }
  if (cursor.offset >= logical_size_ || cursor.remaining == 0) return 0;

  Cursor work = cursor;  // commit only on success
  std::size_t written = 0;
  while (written < buffer_size && work.remaining > 0 &&
         work.offset < logical_size_) {
    std::size_t index = 0;
    const Entry* entry = FindEntry(work.offset, &index);
    if (entry == nullptr) {
      err = MakeError(ErrorCode::kOutOfRange, "offset not covered");
      return 0;
    }
    const std::vector<std::uint8_t>* block = DecodeBlock(index, err);
    if (block == nullptr) return 0;

    const std::uint64_t within = work.offset - entry->logical_offset;
    const std::uint64_t left_in_block = entry->logical_length - within;
    const std::size_t take = static_cast<std::size_t>(std::min<std::uint64_t>(
        std::min<std::uint64_t>(left_in_block, work.remaining),
        buffer_size - written));
    std::memcpy(buffer + written, block->data() + within, take);
    written += take;
    work.offset += take;
    work.remaining -= take;
  }
  cursor = work;
  return written;
}

Error Reader::Read(std::uint64_t offset, std::uint64_t length,
                   std::vector<std::uint8_t>& out) {
  out.clear();
  if (offset >= logical_size_) return {};
  const std::uint64_t available = logical_size_ - offset;
  const std::uint64_t clamped = std::min(length, available);
  out.resize(static_cast<std::size_t>(clamped));

  Cursor cursor{offset, clamped};
  Error err;
  std::size_t total = 0;
  while (total < clamped) {
    const std::size_t n =
        ReadSome(cursor, out.data() + total,
                 static_cast<std::size_t>(clamped) - total, err);
    if (err) {
      out.clear();  // a failed read must not hand back partial data
      return err;
    }
    if (n == 0) break;
    total += n;
  }
  out.resize(total);
  return {};
}

}  // namespace cblk
