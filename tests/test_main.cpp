// Tests verify logical content AND hole layout (via SEEK_DATA/SEEK_HOLE
// maps), never just physical occupancy.
#include <cinttypes>
#include <cstdio>
#include <cstring>
#include <fcntl.h>
#include <string>
#include <vector>

#include "fake_posix.hpp"
#include "sparsecopy/sparse_copy.hpp"
#include "sparsecopy/posix_adapter.hpp"

using namespace sparsecopy;
using sparsecopy::test::FakePosixAdapter;

static int failures = 0;

#define CHECK(cond)                                                     \
  do {                                                                  \
    if (!(cond)) {                                                      \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
      ++failures;                                                       \
    }                                                                   \
  } while (0)

static void fill_pattern(std::vector<char>& v, uint8_t seed) {
    for (size_t i = 0; i < v.size(); ++i) v[i] = static_cast<char>(seed + i * 31);
}

// Logical content must match byte-for-byte, including hole zeros.
static void expect_same_logical(const FakePosixAdapter& os, int a, int b) {
  const auto& fa = os.file(a);
  const auto& fb = os.file(b);
  CHECK(fa.st.size == fb.st.size);
  if (fa.st.size == fb.st.size) {
    CHECK(fa.bytes == fb.bytes);
  }
}

static void expect_same_holes(const FakePosixAdapter& os, int a, int b) {
  CHECK(os.data_extents(a) == os.data_extents(b));
}

// Extents straddle the 4 KiB copy buffer on both sides; every I/O is
// forced short (7 bytes) to exercise the read/write loops.
static void test_cross_hole_boundary() {
  FakePosixAdapter os;
  const int64_t size = 1 << 20;
  int src = os.create_file(size);
  const std::pair<int64_t, int64_t> extents[] = {
      {0, 1000}, {4093, 5000}, {100000, 100001}, {size - 10, size}};
  for (auto [b, e] : extents) {
    std::vector<char> chunk(static_cast<size_t>(e - b));
    fill_pattern(chunk, static_cast<uint8_t>(b));
    os.plant_data(src, b, chunk.data(), chunk.size());
  }
  int dst = os.create_file(0);

  os.max_read = 7;
  os.max_write = 5;
  SparseCopier copier(os, CopyOptions{.buffer_size = 4096});
  copier.copy(src, dst);

  expect_same_logical(os, src, dst);
  expect_same_holes(os, src, dst);
}

static void test_all_hole_file() {
  FakePosixAdapter os;
  const int64_t size = 1 << 20;
  int src = os.create_file(size);  // no data extents at all
  int dst = os.create_file(0);

  SparseCopier copier(os, CopyOptions{.buffer_size = 4096});
  copier.copy(src, dst);

  const auto& fd = os.file(dst);
  CHECK(fd.st.size == size);
  for (char c : fd.bytes) CHECK(c == 0);
  CHECK(os.data_extents(dst).empty());  // stays sparse, nothing materialized
}

// The hook truncates the source after a few reads; the copy must fail
// rather than silently produce a shorter destination.
static void test_source_truncated_mid_copy() {
  FakePosixAdapter os;
  const int64_t size = 1 << 20;
  int src = os.create_file(size);
  std::vector<char> chunk(4096);
  fill_pattern(chunk, 7);
  os.plant_data(src, 0, chunk.data(), chunk.size());
  os.plant_data(src, size - 4096, chunk.data(), chunk.size());
  int dst = os.create_file(0);

  os.max_read = 1000;  // force several reads per extent so the truncation
                       // lands mid-extent
  int reads = 0;
  os.hook = [&](const char* op, int fd) {
    if (std::strcmp(op, "pread") == 0 && fd == src && ++reads == 3) {
      os.truncate_file(src, 1024);  // yank the rug mid-copy
    }
  };
  SparseCopier copier(os, CopyOptions{.buffer_size = 4096});
  bool threw = false;
  try {
    copier.copy(src, dst);
  } catch (const SourceChangedError&) {
    threw = true;
  }
  CHECK(threw);
}

// The source is modified (not truncated) between the first and last fstat;
// the final snapshot comparison must catch it.
static void test_source_modified_mid_copy() {
  FakePosixAdapter os;
  int src = os.create_file(1 << 16);
  std::vector<char> chunk(128, 'x');
  os.plant_data(src, 0, chunk.data(), chunk.size());
  int dst = os.create_file(0);

  os.hook = [&](const char* op, int fd) {
    if (std::strcmp(op, "fsync") == 0 || std::strcmp(op, "ftruncate") == 0) {
      if (fd == dst) os.plant_data(src, 8192, chunk.data(), chunk.size());
    }
  };
  SparseCopier copier(os);
  bool threw = false;
  try {
    copier.copy(src, dst);
  } catch (const SourceChangedError&) {
    threw = true;
  }
  CHECK(threw);
}

// Destination already exists, is longer than the source, and holds garbage
// both inside source hole ranges and beyond EOF. Copy must zero the holes
// and cut the dirty tail.
static void test_dirty_destination_tail() {
  FakePosixAdapter os;
  const int64_t size = 1 << 16;
  int src = os.create_file(size);
  std::vector<char> chunk(1000, 'A');
  os.plant_data(src, 4096, chunk.data(), chunk.size());
  int dst = os.create_file(size * 2);  // dirty tail beyond source EOF
  std::vector<char> garbage(500, 'G');
  os.plant_data(dst, 0, garbage.data(), garbage.size());        // in a src hole
  os.plant_data(dst, size, garbage.data(), garbage.size());     // past src EOF
  os.plant_data(dst, size * 2 - 100, garbage.data(), 100);

  SparseCopier copier(os, CopyOptions{.buffer_size = 512});
  copier.copy(src, dst);

  expect_same_logical(os, src, dst);
  expect_same_holes(os, src, dst);
}

static void test_resume_cursor_binding() {
  // Round-trip.
  ResumeCursor c;
  c.source = SourceIdentity{42, 777, 1 << 20};
  c.next_offset = 4096;
  ResumeCursor parsed;
  CHECK(ResumeCursor::parse(c.serialize(), parsed));
  CHECK(parsed.source == c.source && parsed.next_offset == c.next_offset);
  CHECK(parsed.matches(c.source));
  CHECK(!parsed.matches(SourceIdentity{42, 777, (1 << 20) + 1}));  // length
  CHECK(!parsed.matches(SourceIdentity{42, 778, 1 << 20}));        // inode
  CHECK(!parsed.matches(SourceIdentity{43, 777, 1 << 20}));        // device
  ResumeCursor junk;
  CHECK(!ResumeCursor::parse("not a cursor", junk));
  CHECK(!ResumeCursor::parse("sparsecopy/1 1 2 3 99", junk));  // off > size
}

// A copy that dies mid-way resumes from the cursor instead of restarting,
// and still ends with identical content and hole layout.
static void test_resume_completes_copy() {
  FakePosixAdapter os;
  const int64_t size = 1 << 18;
  int src = os.create_file(size);
  std::vector<char> chunk(3000);
  fill_pattern(chunk, 3);
  os.plant_data(src, 0, chunk.data(), chunk.size());
  os.plant_data(src, 100000, chunk.data(), chunk.size());
  os.plant_data(src, size - 3000, chunk.data(), chunk.size());
  int dst = os.create_file(0);

  SparseCopier copier(os, CopyOptions{.buffer_size = 1024});
  ResumeCursor cursor;

  // First attempt: blow up on the second extent's first write (the first
  // extent takes 3 pwrites of 1024/1024/952 bytes).
  int writes = 0;
  os.hook = [&](const char* op, int fd) {
    if (std::strcmp(op, "pwrite") == 0 && fd == dst && ++writes == 4) {
      throw CopyError("simulated crash");
    }
  };
  bool crashed = false;
  try {
    copier.copy(src, dst, &cursor);
  } catch (const CopyError&) {
    crashed = true;
  }
  CHECK(crashed);
  CHECK(cursor.matches(SourceIdentity{os.file(src).st.dev, os.file(src).st.ino, size}));
  const int64_t resume_from = cursor.next_offset;
  CHECK(resume_from > 0);

  // Second attempt: no crash; must resume, not restart (dst keeps its
  // partial first extent because ftruncate(0) is skipped on resume).
  os.hook = nullptr;
  copier.copy(src, dst, &cursor);

  expect_same_logical(os, src, dst);
  expect_same_holes(os, src, dst);
  CHECK(cursor.next_offset == size);
}

// A cursor bound to a different source (same path, new inode/length) must
// be rejected: the destination is rebuilt from scratch.
static void test_stale_cursor_restarts() {
  FakePosixAdapter os;
  int src = os.create_file(8192, /*dev=*/1, /*ino=*/555);
  std::vector<char> chunk(8192, 'N');
  os.plant_data(src, 0, chunk.data(), chunk.size());
  int dst = os.create_file(0);

  ResumeCursor cursor;
  cursor.source = SourceIdentity{1, 444, 16384};  // stale identity
  cursor.next_offset = 4096;

  SparseCopier copier(os);
  copier.copy(src, dst, &cursor);  // must ignore the stale cursor

  expect_same_logical(os, src, dst);
  expect_same_holes(os, src, dst);
  CHECK(cursor.source.ino == 555);
  CHECK(cursor.next_offset == 8192);
}

// End-to-end on the real filesystem: create a sparse source, copy it, and
// compare logical content byte-for-byte (not st_blocks).
static void test_real_filesystem() {
  RealPosixAdapter os;
  const char* src_path = "sparse_test_src.bin";
  const char* dst_path = "sparse_test_dst.bin";
  const int64_t size = 1 << 20;

  int sfd = os.open(src_path, O_RDWR | O_CREAT | O_TRUNC, 0644);
  CHECK(sfd >= 0);
  CHECK(os.ftruncate(sfd, size) == 0);
  std::vector<char> chunk(5000);
  fill_pattern(chunk, 11);
  CHECK(os.pwrite(sfd, chunk.data(), chunk.size(), 4093) == 5000);
  CHECK(os.pwrite(sfd, chunk.data(), chunk.size(), size - 5000) == 5000);

  int dfd = os.open(dst_path, O_RDWR | O_CREAT | O_TRUNC, 0644);
  CHECK(dfd >= 0);

  SparseCopier copier(os, CopyOptions{.buffer_size = 4096});
  copier.copy(sfd, dfd);

  FileStat st;
  CHECK(os.fstat(dfd, st) == 0 && st.size == size);
  std::vector<char> a(4096), b(4096);
  for (int64_t off = 0; off < size; off += 4096) {
    CHECK(os.pread(sfd, a.data(), a.size(), off) == 4096);
    CHECK(os.pread(dfd, b.data(), b.size(), off) == 4096);
    CHECK(a == b);
  }
  os.close(sfd);
  os.close(dfd);
  std::remove(src_path);
  std::remove(dst_path);
}

int main() {
  test_cross_hole_boundary();
  test_all_hole_file();
  test_source_truncated_mid_copy();
  test_source_modified_mid_copy();
  test_dirty_destination_tail();
  test_resume_cursor_binding();
  test_resume_completes_copy();
  test_stale_cursor_restarts();
  test_real_filesystem();
  if (failures == 0) {
    std::printf("all tests passed\n");
    return 0;
  }
  std::fprintf(stderr, "%d check(s) failed\n", failures);
  return 1;
}
