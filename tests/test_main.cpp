#include <cstdint>
#include <cstdio>
#include <cstring>
#include <string>
#include <vector>

#include "cblk/chunked_reader.hpp"
#include "cblk/chunked_writer.hpp"

namespace {

int g_failures = 0;

#define CHECK(cond)                                                     \
  do {                                                                  \
    if (!(cond)) {                                                      \
      std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);       \
      ++g_failures;                                                     \
    }                                                                   \
  } while (0)

std::vector<std::uint8_t> Bytes(const std::string& s) {
  return std::vector<std::uint8_t>(s.begin(), s.end());
}

void StoreU64(std::uint8_t* p, std::uint64_t v) {
  for (int i = 0; i < 8; ++i) p[i] = static_cast<std::uint8_t>(v >> (8 * i));
}

std::vector<std::uint8_t> ReadAll(cblk::Reader& reader, std::uint64_t offset,
                                  std::uint64_t length,
                                  std::size_t buffer_size) {
  cblk::Reader::Cursor cursor{offset, length};
  std::vector<std::uint8_t> result;
  std::vector<std::uint8_t> buffer(buffer_size);
  for (;;) {
    cblk::Error err;
    const std::size_t n =
        reader.ReadSome(cursor, buffer.data(), buffer.size(), err);
    CHECK(!err);
    if (err || n == 0) break;
    result.insert(result.end(), buffer.begin(), buffer.begin() + n);
  }
  return result;
}

// Reads spanning a block boundary must match the raw reference bytes, both
// in one shot and through a too-small caller buffer.
void TestCrossBlock() {
  const std::string a = "alpha-block-";
  const std::string b = "BETA-block-2";
  const std::string c = "gamma3";
  const std::string reference = a + b + c;
  auto image = cblk::BuildImage({{Bytes(a)}, {Bytes(b)}, {Bytes(c)}});

  cblk::Error err;
  cblk::Reader reader = cblk::Reader::Open(image, err);
  CHECK(!err);
  CHECK(reader.logical_size() == reference.size());
  CHECK(reader.block_count() == 3);

  const std::size_t offset = a.size() - 4;
  const std::size_t length = 4 + b.size() + 2;  // crosses a->b and b->c
  std::vector<std::uint8_t> out;
  err = reader.Read(offset, length, out);
  CHECK(!err);
  CHECK(out == Bytes(reference.substr(offset, length)));

  const std::vector<std::uint8_t> streamed =
      ReadAll(reader, offset, length, /*buffer_size=*/3);
  CHECK(streamed == Bytes(reference.substr(offset, length)));
}

// A read at (or past) the logical end yields zero bytes and no error.
void TestEndEmptyRead() {
  const std::string reference = "some-bytes";
  auto image = cblk::BuildImage({{Bytes(reference)}});
  cblk::Error err;
  cblk::Reader reader = cblk::Reader::Open(image, err);
  CHECK(!err);

  std::vector<std::uint8_t> out;
  err = reader.Read(reference.size(), 0, out);
  CHECK(!err && out.empty());
  err = reader.Read(reference.size(), 100, out);
  CHECK(!err && out.empty());
  err = reader.Read(reference.size() + 1000, 5, out);
  CHECK(!err && out.empty());

  cblk::Reader::Cursor cursor{reference.size(), 8};
  std::uint8_t byte = 0xAB;
  const std::size_t n = reader.ReadSome(cursor, &byte, 1, err);
  CHECK(!err);
  CHECK(n == 0);
  CHECK(byte == 0xAB);  // untouched

  // A range that starts in bounds but runs past the end is clamped.
  err = reader.Read(reference.size() - 3, 100, out);
  CHECK(!err);
  CHECK(out == Bytes(reference.substr(reference.size() - 3)));
}

// Two entries claiming the same logical range must be rejected at open.
void TestOverlappingDirectory() {
  auto image = cblk::BuildImage({{Bytes("0123456789")}, {Bytes("abcde")}});
  // Second entry: logical_offset 4, length 5 -> overlaps [0,10).
  StoreU64(image.data() + cblk::kHeaderSize + cblk::kEntrySize, 4);
  StoreU64(image.data() + cblk::kHeaderSize + cblk::kEntrySize + 8, 5);

  cblk::Error err;
  cblk::Reader reader = cblk::Reader::Open(image, err);
  CHECK(err.code == cblk::ErrorCode::kOverlappingDirectory);
}

// Cancel drops every cached decoded block; reading still works afterwards.
void TestCancelReleasesCache() {
  const std::string a = "first-block-first-block";
  const std::string b = "second";
  const std::string reference = a + b;
  auto image = cblk::BuildImage({{Bytes(a)}, {Bytes(b)}});
  cblk::Error err;
  cblk::Reader reader = cblk::Reader::Open(image, err);
  CHECK(!err);

  cblk::Reader::Cursor cursor{0, reference.size()};
  std::vector<std::uint8_t> buffer(5);
  std::size_t n = reader.ReadSome(cursor, buffer.data(), buffer.size(), err);
  CHECK(!err && n == buffer.size());
  CHECK(reader.cached_block_count() == 1);

  reader.Cancel();
  CHECK(reader.cached_block_count() == 0);

  // The cursor survives the cancel; the remaining bytes still decode.
  std::vector<std::uint8_t> rest = Bytes(reference.substr(0, n));
  while (cursor.remaining > 0) {
    n = reader.ReadSome(cursor, buffer.data(), buffer.size(), err);
    CHECK(!err);
    if (err || n == 0) break;
    rest.insert(rest.end(), buffer.begin(), buffer.begin() + n);
  }
  CHECK(rest == Bytes(reference));
}

// A tampered block must surface as an error, never as unverified bytes.
void TestCorruptBlock() {
  const std::string reference = "integrity-matters";
  auto image = cblk::BuildImage({{Bytes(reference)}});
  image.back() ^= 0xFF;  // flip a payload byte

  cblk::Error err;
  cblk::Reader reader = cblk::Reader::Open(image, err);
  CHECK(!err);

  std::vector<std::uint8_t> out;
  err = reader.Read(0, reference.size(), out);
  CHECK(err.code == cblk::ErrorCode::kCorruptBlock);
  CHECK(out.empty());
  CHECK(reader.cached_block_count() == 0);  // bad block is not cached
}

// Directory arithmetic must be overflow-checked.
void TestDirectoryOverflow() {
  // Logical offset + length wraps around u64.
  {
    auto image = cblk::BuildImage({{Bytes("xy")}});
    StoreU64(image.data() + cblk::kHeaderSize,
             std::uint64_t{0} - 3);  // logical_offset = 2^64 - 3
    StoreU64(image.data() + cblk::kHeaderSize + 8, 10);
    cblk::Error err;
    cblk::Reader reader = cblk::Reader::Open(image, err);
    CHECK(err.code == cblk::ErrorCode::kDirectoryOverflow);
  }
  // Physical range escapes the image.
  {
    auto image = cblk::BuildImage({{Bytes("xy")}});
    StoreU64(image.data() + cblk::kHeaderSize + 16, image.size() - 1);
    StoreU64(image.data() + cblk::kHeaderSize + 24, 4);
    cblk::Error err;
    cblk::Reader reader = cblk::Reader::Open(image, err);
    CHECK(err.code == cblk::ErrorCode::kDirectoryOverflow);
  }
  // Entry count larger than the image can hold.
  {
    auto image = cblk::BuildImage({{Bytes("xy")}});
    StoreU64(image.data() + cblk::kMagicSize, std::uint64_t{1} << 40);
    cblk::Error err;
    cblk::Reader reader = cblk::Reader::Open(image, err);
    CHECK(err.code == cblk::ErrorCode::kTruncated);
  }
}

// A range read decodes only the blocks it actually intersects.
void TestDecodesOnlyIntersectingBlocks() {
  auto image = cblk::BuildImage(
      {{Bytes("aaaa")}, {Bytes("bbbb")}, {Bytes("cccc")}, {Bytes("dddd")}});
  cblk::Error err;
  cblk::Reader reader = cblk::Reader::Open(image, err);
  CHECK(!err);

  std::vector<std::uint8_t> out;
  err = reader.Read(5, 2, out);  // entirely inside block 1
  CHECK(!err);
  CHECK(out == Bytes("bb"));
  CHECK(reader.cached_block_count() == 1);

  err = reader.Read(6, 5, out);  // blocks 1 and 2
  CHECK(!err);
  CHECK(out == Bytes("bbccc"));
  CHECK(reader.cached_block_count() == 2);
}

// RLE blocks round-trip and decode only on demand.
void TestRleCodec() {
  const std::string runs = "aaaaaaaaaaaaaaaaaaaabbbcccccccccccccccccccc";
  const std::string mixed = "xyz";
  const std::string reference = runs + mixed;
  auto image = cblk::BuildImage(
      {{Bytes(runs), cblk::Codec::kRle}, {Bytes(mixed), cblk::Codec::kRle}});
  CHECK(image.size() < cblk::kHeaderSize + 2 * cblk::kEntrySize +
                           reference.size());  // actually compressed

  cblk::Error err;
  cblk::Reader reader = cblk::Reader::Open(image, err);
  CHECK(!err);

  std::vector<std::uint8_t> out;
  err = reader.Read(0, reference.size(), out);
  CHECK(!err);
  CHECK(out == Bytes(reference));

  err = reader.Read(runs.size() - 2, 4, out);  // crosses into block 2
  CHECK(!err);
  CHECK(out == Bytes(reference.substr(runs.size() - 2, 4)));

  // Corrupt a run-length encoded block: verification must still fail.
  image[image.size() - 4] ^= 0x01;
  cblk::Reader broken = cblk::Reader::Open(image, err);
  CHECK(!err);
  err = broken.Read(runs.size(), mixed.size(), out);
  CHECK(err.code == cblk::ErrorCode::kCorruptBlock);
  CHECK(out.empty());
}

}  // namespace

int main() {
  TestCrossBlock();
  TestEndEmptyRead();
  TestOverlappingDirectory();
  TestCancelReleasesCache();
  TestCorruptBlock();
  TestDirectoryOverflow();
  TestDecodesOnlyIntersectingBlocks();
  TestRleCodec();

  if (g_failures == 0) {
    std::printf("all tests passed\n");
    return 0;
  }
  std::printf("%d check(s) failed\n", g_failures);
  return 1;
}
