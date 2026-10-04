// Tests for chunkio. Expected values are raw byte-string references.
#include "chunkio.h"

#include <cstdio>
#include <cstring>
#include <string>
#include <vector>

using chunkio::ChunkEntry;
using chunkio::Status;

static int g_failures = 0;
static int g_checks = 0;

#define CHECK(cond)                                                        \
  do {                                                                     \
    ++g_checks;                                                            \
    if (!(cond)) {                                                         \
      ++g_failures;                                                        \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
    }                                                                      \
  } while (0)

#define CHECK_STATUS_EQ(actual, expected)                                  \
  do {                                                                     \
    ++g_checks;                                                            \
    const Status a_ = (actual);                                            \
    if (a_ != (expected)) {                                                \
      ++g_failures;                                                        \
      std::fprintf(stderr, "FAIL %s:%d: got %s, want %s\n", __FILE__,      \
                   __LINE__, chunkio::status_name(a_),                     \
                   chunkio::status_name(expected));                        \
    }                                                                      \
  } while (0)

namespace {

std::vector<uint8_t> bytes(const std::string& s) {
  return std::vector<uint8_t>(s.begin(), s.end());
}

// Builds a chunked file in memory block by block.
struct Builder {
  std::vector<uint8_t> file;
  std::vector<ChunkEntry> dir;

  uint64_t logical_end() const {
    return dir.empty() ? 0
                       : dir.back().logical_offset + dir.back().logical_size;
  }

  void add_block(const std::vector<uint8_t>& physical, uint64_t logical_size,
                 uint32_t codec, uint32_t crc) {
    ChunkEntry e;
    e.logical_offset = logical_end();
    e.logical_size = logical_size;
    e.physical_offset = file.size();
    e.physical_size = physical.size();
    e.codec = codec;
    e.crc32 = crc;
    file.insert(file.end(), physical.begin(), physical.end());
    dir.push_back(e);
  }

  void add_store(const std::string& data) {
    add_block(bytes(data), data.size(), chunkio::kCodecStore,
              chunkio::crc32(bytes(data).data(), data.size()));
  }

  void add_rle(const std::string& raw) {
    std::vector<uint8_t> enc;
    for (size_t i = 0; i < raw.size();) {
      size_t run = 1;
      while (i + run < raw.size() && raw[i + run] == raw[i] && run < 255) ++run;
      enc.push_back(static_cast<uint8_t>(run));
      enc.push_back(static_cast<uint8_t>(raw[i]));
      i += run;
    }
    add_block(enc, raw.size(), chunkio::kCodecRle,
              chunkio::crc32(bytes(raw).data(), raw.size()));
  }
};

std::unique_ptr<chunkio::Reader> make_reader(chunkio::ISource* src,
                                             std::vector<ChunkEntry> dir) {
  std::unique_ptr<chunkio::Reader> reader;
  Status st = chunkio::Reader::open(src, std::move(dir), &reader);
  CHECK_STATUS_EQ(st, Status::kOk);
  return reader;
}

// Reads a whole range, resuming with `chunk` sized buffers, and returns the
// concatenation of everything produced.
std::string read_all(chunkio::Reader& reader, uint64_t offset, uint64_t length,
                     size_t chunk, Status* final_status) {
  std::unique_ptr<chunkio::Reader::Session> session;
  Status st = reader.open_range(offset, length, &session);
  CHECK_STATUS_EQ(st, Status::kOk);
  std::string out;
  std::vector<uint8_t> buf(chunk);
  for (;;) {
    const size_t n = session->read(buf.data(), buf.size(), &st);
    out.append(reinterpret_cast<const char*>(buf.data()), n);
    if (st != Status::kNeedMoreSpace) break;
  }
  *final_status = st;
  return out;
}

void test_cross_block_read() {
  Builder b;
  b.add_store("hello ");
  b.add_store("world!");
  chunkio::MemorySource src(b.file);
  auto reader = make_reader(&src, b.dir);
  CHECK(reader->logical_size() == 12);

  Status st;
  // Exact-size buffer spanning the block boundary.
  CHECK(read_all(*reader, 0, 12, 12, &st) == "hello world!");
  CHECK_STATUS_EQ(st, Status::kDone);
  // Slice crossing the boundary: logical [4, 10).
  CHECK(read_all(*reader, 4, 6, 6, &st) == "o worl");
  CHECK_STATUS_EQ(st, Status::kDone);
  // Slice fully inside the second block.
  CHECK(read_all(*reader, 6, 5, 5, &st) == "world");
  // Length past the end is clamped to the logical end.
  CHECK(read_all(*reader, 8, 1000, 64, &st) == "rld!");
  CHECK_STATUS_EQ(st, Status::kDone);
}

void test_resume_with_small_buffer() {
  Builder b;
  b.add_store("hello ");
  b.add_store("world!");
  b.add_rle("aaaabbbcc");
  chunkio::MemorySource src(b.file);
  auto reader = make_reader(&src, b.dir);
  const std::string reference = "hello world!aaaabbbcc";
  CHECK(reader->logical_size() == reference.size());

  for (size_t chunk : {1, 2, 3, 7, 64}) {
    Status st;
    const std::string got = read_all(*reader, 0, reference.size(), chunk, &st);
    CHECK(got == reference);
    CHECK_STATUS_EQ(st, Status::kDone);
  }
  // Resume starting mid-block with a 1-byte buffer.
  Status st;
  CHECK(read_all(*reader, 5, 14, 1, &st) == reference.substr(5, 14));
  CHECK_STATUS_EQ(st, Status::kDone);
}

void test_end_empty_read() {
  Builder b;
  b.add_store("hello ");
  b.add_store("world!");
  chunkio::MemorySource src(b.file);
  auto reader = make_reader(&src, b.dir);

  std::unique_ptr<chunkio::Reader::Session> session;
  // Zero-length read exactly at the logical end.
  CHECK_STATUS_EQ(reader->open_range(12, 0, &session), Status::kOk);
  uint8_t buf[4];
  Status st;
  CHECK(session->read(buf, sizeof(buf), &st) == 0);
  CHECK_STATUS_EQ(st, Status::kDone);
  CHECK(session->done());
  // Non-zero length at the end clamps to empty.
  CHECK_STATUS_EQ(reader->open_range(12, 5, &session), Status::kOk);
  CHECK(session->read(buf, sizeof(buf), &st) == 0);
  CHECK_STATUS_EQ(st, Status::kDone);
  // Past the end is rejected.
  CHECK_STATUS_EQ(reader->open_range(13, 1, &session), Status::kInvalidArgument);
}

void test_overlapping_directory() {
  Builder b;
  b.add_store("hello ");
  b.add_store("world!");
  std::vector<ChunkEntry> dir = b.dir;
  dir[1].logical_offset = 3; // overlaps [0,6)

  std::vector<ChunkEntry> parsed;
  const std::vector<uint8_t> raw = chunkio::serialize_directory(dir);
  CHECK_STATUS_EQ(chunkio::parse_directory(raw.data(), raw.size(), &parsed),
                  Status::kOverlappingDirectory);

  chunkio::MemorySource src(b.file);
  std::unique_ptr<chunkio::Reader> reader;
  CHECK_STATUS_EQ(chunkio::Reader::open(&src, dir, &reader),
                  Status::kOverlappingDirectory);
  CHECK(reader == nullptr);
}

void test_cancel_releases_cache() {
  Builder b;
  b.add_store(std::string(4096, 'x'));
  b.add_store("tail");
  chunkio::MemorySource src(b.file);
  auto reader = make_reader(&src, b.dir);

  std::unique_ptr<chunkio::Reader::Session> session;
  CHECK_STATUS_EQ(reader->open_range(0, 4100, &session), Status::kOk);
  uint8_t buf[8];
  Status st;
  CHECK(session->read(buf, sizeof(buf), &st) == 8);
  CHECK_STATUS_EQ(st, Status::kNeedMoreSpace);
  CHECK(session->cached_bytes() == 4096); // first block decoded and cached
  session->cancel();
  CHECK(session->cancelled());
  CHECK(session->cached_bytes() == 0); // cache released
  CHECK(session->read(buf, sizeof(buf), &st) == 0);
  CHECK_STATUS_EQ(st, Status::kCancelled);
}

void test_bad_block_returns_nothing_unverified() {
  Builder b;
  b.add_store("hello ");
  b.add_store("world!");
  // Corrupt one payload byte of the second block in the physical file.
  b.file[b.dir[1].physical_offset + 2] ^= 0xFF;
  chunkio::MemorySource src(b.file);
  auto reader = make_reader(&src, b.dir);

  // Range fully inside the corrupted block: no bytes may come back.
  std::unique_ptr<chunkio::Reader::Session> session;
  CHECK_STATUS_EQ(reader->open_range(6, 6, &session), Status::kOk);
  uint8_t buf[16];
  Status st;
  CHECK(session->read(buf, sizeof(buf), &st) == 0);
  CHECK_STATUS_EQ(st, Status::kBadBlock);

  // Range spanning a good block into the bad one: verified prefix only.
  CHECK_STATUS_EQ(reader->open_range(0, 12, &session), Status::kOk);
  const size_t n = session->read(buf, sizeof(buf), &st);
  CHECK_STATUS_EQ(st, Status::kBadBlock);
  CHECK(n == 6);
  CHECK(std::memcmp(buf, "hello ", 6) == 0);
}

void test_directory_overflow() {
  Builder b;
  b.add_store("hello ");
  std::vector<ChunkEntry> dir = b.dir;

  // logical_offset + logical_size wraps around.
  dir[0].logical_offset = UINT64_MAX - 2;
  dir[0].logical_size = 8;
  std::vector<uint8_t> raw = chunkio::serialize_directory(dir);
  std::vector<ChunkEntry> parsed;
  CHECK_STATUS_EQ(chunkio::parse_directory(raw.data(), raw.size(), &parsed),
                  Status::kDirectoryOverflow);

  // physical_offset + physical_size wraps around.
  dir = b.dir;
  dir[0].physical_offset = UINT64_MAX - 1;
  dir[0].physical_size = 4;
  raw = chunkio::serialize_directory(dir);
  CHECK_STATUS_EQ(chunkio::parse_directory(raw.data(), raw.size(), &parsed),
                  Status::kDirectoryOverflow);

  // Truncated directory and bad magic.
  raw = chunkio::serialize_directory(b.dir);
  CHECK_STATUS_EQ(chunkio::parse_directory(raw.data(), raw.size() - 1, &parsed),
                  Status::kBadDirectory);
  raw[0] = 'X';
  CHECK_STATUS_EQ(chunkio::parse_directory(raw.data(), raw.size(), &parsed),
                  Status::kBadDirectory);
}

void test_physical_out_of_range() {
  Builder b;
  b.add_store("hello ");
  std::vector<ChunkEntry> dir = b.dir;
  dir[0].physical_offset = b.file.size() - 2; // 6 bytes no longer fit
  chunkio::MemorySource src(b.file);
  std::unique_ptr<chunkio::Reader> reader;
  CHECK_STATUS_EQ(chunkio::Reader::open(&src, dir, &reader),
                  Status::kPhysicalOutOfRange);
}

void test_rle_round_trip() {
  Builder b;
  b.add_rle("aaabbbbbbccccccccccddddddddddddddddddxyz");
  chunkio::MemorySource src(b.file);
  auto reader = make_reader(&src, b.dir);
  Status st;
  CHECK(read_all(*reader, 0, reader->logical_size(), 5, &st) ==
        "aaabbbbbbccccccccccddddddddddddddddddxyz");
  CHECK_STATUS_EQ(st, Status::kDone);
}

void test_gap_is_zero_filled() {
  Builder b;
  b.add_store("ab");
  b.add_store("cd");
  // Introduce a 3-byte gap between the two blocks.
  b.dir[1].logical_offset = 5;
  chunkio::MemorySource src(b.file);
  auto reader = make_reader(&src, b.dir);
  CHECK(reader->logical_size() == 7);
  Status st;
  const std::string got = read_all(*reader, 0, 7, 4, &st);
  CHECK_STATUS_EQ(st, Status::kDone);
  CHECK(got == std::string("ab\0\0\0cd", 7));
}

// Counts physical reads to prove only intersecting blocks are fetched.
struct CountingSource : chunkio::ISource {
  explicit CountingSource(const std::vector<uint8_t>& d) : data(d) {}
  size_t read_at(uint64_t offset, uint8_t* out, size_t size) const override {
    ++reads;
    if (offset >= data.size()) return 0;
    const size_t n = std::min<size_t>(size, data.size() - offset);
    std::memcpy(out, data.data() + offset, n);
    return n;
  }
  uint64_t size() const override { return data.size(); }
  std::vector<uint8_t> data;
  mutable size_t reads = 0;
};

void test_decodes_only_intersecting_blocks() {
  Builder b;
  b.add_store("aaaa");
  b.add_store("bbbb");
  b.add_store("cccc");
  b.add_store("dddd");
  CountingSource src(b.file);
  auto reader = make_reader(&src, b.dir);

  // Range [5, 9) intersects only blocks 1 and 2.
  Status st;
  CHECK(read_all(*reader, 5, 4, 16, &st) == "bbbc");
  CHECK_STATUS_EQ(st, Status::kDone);
  CHECK(src.reads == 2);
}

} // namespace

int main() {
  test_cross_block_read();
  test_resume_with_small_buffer();
  test_end_empty_read();
  test_overlapping_directory();
  test_cancel_releases_cache();
  test_bad_block_returns_nothing_unverified();
  test_directory_overflow();
  test_physical_out_of_range();
  test_rle_round_trip();
  test_gap_is_zero_filled();
  test_decodes_only_intersecting_blocks();

  if (g_failures == 0) {
    std::printf("PASS: all %d checks succeeded\n", g_checks);
    return 0;
  }
  std::fprintf(stderr, "FAILED: %d of %d checks failed\n", g_failures, g_checks);
  return 1;
}
