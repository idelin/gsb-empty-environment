#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <iostream>
#include <string>
#include <sys/stat.h>
#include <unistd.h>

#include "fake_adapter.h"
#include "sparse_copy.h"

namespace {

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                              \
  do {                                                           \
    ++g_checks;                                                  \
    if (!(cond)) {                                               \
      ++g_failures;                                              \
      std::cerr << "FAIL " << __FILE__ << ":" << __LINE__ << " " \
                << #cond << "\n";                                \
    }                                                            \
  } while (0)

template <typename Ex>
bool throws_of_type(void (*fn)(void*), void* ctx) {
  try {
    fn(ctx);
  } catch (const Ex&) {
    return true;
  } catch (const std::exception& e) {
    std::cerr << "unexpected exception: " << e.what() << "\n";
    return false;
  }
  return false;
}

std::string g_tmpdir;

std::string tmp_path(const std::string& name) { return g_tmpdir + "/" + name; }

// Data-hole-data-hole-data with a trailing hole; sized to force chunk
// splits across hole boundaries with chunk_size = 7.
std::string make_boundary_pattern() {
  std::string s;
  s += "AAAA";                 // [0,4)   data
  s += std::string(13, '\0');  // [4,17)  hole
  s += "BB";                   // [17,19) data
  s += std::string(3, '\0');   // [19,22) hole
  s += "CCCCCC";               // [22,28) data
  s += std::string(9, '\0');   // [28,37) trailing hole
  return s;                    // size 37
}

void test_hole_boundaries() {
  spc::test::FakeAdapter ad;
  const std::string content = make_boundary_pattern();
  ad.create_sparse("src", content.size(), {});
  ad.truncate_file("src", 0);
  ad.create_file("src", content);

  spc::SparseCopier copier(ad);
  spc::CopyOptions opt;
  opt.chunk_size = 7;
  opt.cursor_path = tmp_path("boundary.cursor");
  spc::CopyStats st = copier.copy("src", "dst", opt);

  CHECK(st.logical_bytes == content.size());
  CHECK(st.data_bytes == 4 + 2 + 6);
  CHECK(st.hole_bytes == 13 + 3 + 9);
  CHECK(st.extents == 3);
  CHECK(ad.read_file("dst") == content);
  std::string why;
  CHECK(spc::verify_copy(ad, "src", "dst", &why));
  if (!why.empty()) std::cerr << "verify: " << why << "\n";
}

void test_all_hole_file() {
  spc::test::FakeAdapter ad;
  ad.create_sparse("src", 8192, {});

  spc::SparseCopier copier(ad);
  spc::CopyOptions opt;
  opt.chunk_size = 1024;
  opt.cursor_path = tmp_path("allhole.cursor");
  spc::CopyStats st = copier.copy("src", "dst", opt);

  CHECK(st.data_bytes == 0);
  CHECK(st.hole_bytes == 8192);
  CHECK(st.extents == 0);
  CHECK(ad.file_size("dst") == 8192);
  CHECK(ad.read_file("dst") == std::string(8192, '\0'));
  CHECK(spc::verify_copy(ad, "src", "dst"));
}

void test_source_truncated_mid_copy() {
  spc::test::FakeAdapter ad;
  ad.create_file("src", std::string(100, 'x'));

  bool truncated = false;
  ad.faults.on_pread = [&](spc::test::FakeAdapter& a) {
    if (!truncated) {
      truncated = true;
      a.truncate_file("src", 10);
    }
  };

  spc::SparseCopier copier(ad);
  spc::CopyOptions opt;
  opt.chunk_size = 64;
  opt.cursor_path = tmp_path("trunc.cursor");
  struct Ctx {
    spc::SparseCopier* c;
    spc::CopyOptions* o;
  } ctx{&copier, &opt};
  auto fn = [](void* p) {
    Ctx* c = static_cast<Ctx*>(p);
    c->c->copy("src", "dst", *c->o);
  };
  CHECK(throws_of_type<spc::SourceChangedError>(fn, &ctx));
}

void test_source_rewritten_mid_copy() {
  spc::test::FakeAdapter ad;
  ad.create_file("src", std::string(64, 'y'));

  bool mutated = false;
  ad.faults.on_pread = [&](spc::test::FakeAdapter& a) {
    if (!mutated) {
      mutated = true;
      a.mutate_byte("src", 63, 'Z');  // same length, content changes
    }
  };

  spc::SparseCopier copier(ad);
  spc::CopyOptions opt;
  opt.chunk_size = 16;
  opt.cursor_path = tmp_path("rewrite.cursor");
  struct Ctx {
    spc::SparseCopier* c;
    spc::CopyOptions* o;
  } ctx{&copier, &opt};
  auto fn = [](void* p) {
    Ctx* c = static_cast<Ctx*>(p);
    c->c->copy("src", "dst", *c->o);
  };
  CHECK(throws_of_type<spc::SourceChangedError>(fn, &ctx));
}

void test_dirty_tail_removed() {
  spc::test::FakeAdapter ad;
  ad.create_file("src", "hello sparse world");       // 18 bytes
  ad.create_file("dst", std::string(100, 'X'));      // pre-existing dirty tail

  spc::SparseCopier copier(ad);
  spc::CopyOptions opt;
  opt.cursor_path = tmp_path("tail.cursor");
  copier.copy("src", "dst", opt);

  CHECK(ad.file_size("dst") == 18);
  CHECK(ad.read_file("dst") == "hello sparse world");
  CHECK(spc::verify_copy(ad, "src", "dst"));
}

void test_short_reads_and_writes() {
  spc::test::FakeAdapter ad;
  const std::string content = make_boundary_pattern();
  ad.create_file("src", content);
  ad.faults.max_read = 3;
  ad.faults.max_write = 2;

  spc::SparseCopier copier(ad);
  spc::CopyOptions opt;
  opt.chunk_size = 16;
  opt.cursor_path = tmp_path("short.cursor");
  spc::CopyStats st = copier.copy("src", "dst", opt);

  CHECK(st.data_bytes == 12);
  CHECK(ad.read_file("dst") == content);
  CHECK(spc::verify_copy(ad, "src", "dst"));
}

void test_resume_after_failure() {
  spc::test::FakeAdapter ad;
  const std::string content = make_boundary_pattern();
  ad.create_file("src", content);

  spc::SparseCopier copier(ad);
  spc::CopyOptions opt;
  opt.chunk_size = 8;
  opt.cursor_path = tmp_path("resume.cursor");

  ad.faults.fail_write_after = 5;  // die inside the second chunk
  struct Ctx {
    spc::SparseCopier* c;
    spc::CopyOptions* o;
  } ctx{&copier, &opt};
  auto fn = [](void* p) {
    Ctx* c = static_cast<Ctx*>(p);
    c->c->copy("src", "dst", *c->o);
  };
  CHECK(throws_of_type<spc::IoError>(fn, &ctx));
  CHECK(access(opt.cursor_path.c_str(), F_OK) == 0);  // cursor survives

  ad.faults.fail_write_after = UINT64_MAX;  // heal
  spc::CopyStats st = copier.copy("src", "dst", opt);
  CHECK(st.resumed);
  CHECK(ad.read_file("dst") == content);
  CHECK(spc::verify_copy(ad, "src", "dst"));
  CHECK(access(opt.cursor_path.c_str(), F_OK) != 0);  // cursor cleaned up
}

void test_cursor_binds_source_identity() {
  spc::test::FakeAdapter ad;
  ad.create_file("srcA", std::string(64, 'a'));

  spc::SparseCopier copier(ad);
  spc::CopyOptions opt;
  opt.chunk_size = 8;
  opt.cursor_path = tmp_path("bind.cursor");

  ad.faults.fail_write_after = 3;
  struct Ctx {
    spc::SparseCopier* c;
    spc::CopyOptions* o;
    const char* src;
  } ctx{&copier, &opt, "srcA"};
  auto fn = [](void* p) {
    Ctx* c = static_cast<Ctx*>(p);
    c->c->copy(c->src, "dst", *c->o);
  };
  CHECK(throws_of_type<spc::IoError>(fn, &ctx));
  ad.faults.fail_write_after = UINT64_MAX;

  // Different inode: cursor must be rejected.
  ad.create_file("srcB", std::string(64, 'a'));
  ctx.src = "srcB";
  CHECK(throws_of_type<spc::CursorMismatchError>(fn, &ctx));

  // Same file, different length: cursor must be rejected.
  ad.truncate_file("srcA", 32);
  ctx.src = "srcA";
  CHECK(throws_of_type<spc::CursorMismatchError>(fn, &ctx));
}

void test_fallback_zero_scan() {
  spc::test::FakeAdapter ad;
  ad.seek_supported = false;  // simulate a backend without SEEK_DATA/HOLE
  const std::string content = make_boundary_pattern();
  ad.create_file("src", content);

  spc::SparseCopier copier(ad);
  spc::CopyOptions opt;
  opt.chunk_size = 4;  // scan block = chunk size
  opt.cursor_path = tmp_path("scan.cursor");
  spc::CopyStats st = copier.copy("src", "dst", opt);

  CHECK(ad.read_file("dst") == content);
  // Holes here are not block-aligned, so some zero bytes are written as
  // data; logical content must still match exactly.
  CHECK(st.data_bytes >= 12);
  CHECK(spc::verify_copy(ad, "src", "dst"));
}

void test_real_filesystem() {
  spc::RealPosixAdapter ad;
  const std::string src = tmp_path("real.src");
  const std::string dst = tmp_path("real.dst");

  const off_t far = 1 << 20;
  int fd = ::open(src.c_str(), O_RDWR | O_CREAT | O_TRUNC, 0644);
  CHECK(fd >= 0);
  CHECK(::write(fd, "HEAD", 4) == 4);
  CHECK(::pwrite(fd, "TAIL", 4, far) == 4);
  CHECK(::ftruncate(fd, far + 4 + 4096) == 0);  // trailing hole
  ::close(fd);

  // Pre-existing destination with a dirty tail longer than the source.
  int dfd = ::open(dst.c_str(), O_RDWR | O_CREAT | O_TRUNC, 0644);
  CHECK(dfd >= 0);
  std::string junk(3 << 20, 'Q');
  CHECK(::write(dfd, junk.data(), junk.size()) == (ssize_t)junk.size());
  ::close(dfd);

  spc::SparseCopier copier(ad);
  spc::CopyOptions opt;
  opt.chunk_size = 4096;
  opt.cursor_path = tmp_path("real.cursor");
  spc::CopyStats st = copier.copy(src, dst, opt);

  CHECK(st.logical_bytes == (uint64_t)(far + 4 + 4096));
  // Whether holes are skipped here depends on the filesystem's SEEK_DATA
  // support (APFS reports one big data extent); hole-skipping is covered
  // by the fake-adapter tests. Logical fidelity is asserted below.
  CHECK(st.data_bytes <= st.logical_bytes);

  std::string why;
  CHECK(spc::verify_copy(ad, src, dst, &why));
  if (!why.empty()) std::cerr << "verify: " << why << "\n";

  // Explicit logical comparison (not physical occupancy).
  int a = ::open(src.c_str(), O_RDONLY);
  int b = ::open(dst.c_str(), O_RDONLY);
  CHECK(a >= 0 && b >= 0);
  struct stat sa, sb;
  CHECK(::fstat(a, &sa) == 0 && ::fstat(b, &sb) == 0);
  CHECK(sa.st_size == sb.st_size);
  std::string ba(sa.st_size, '\0'), bb(sb.st_size, '\0');
  CHECK(::read(a, &ba[0], ba.size()) == (ssize_t)ba.size());
  CHECK(::read(b, &bb[0], bb.size()) == (ssize_t)bb.size());
  CHECK(ba == bb);
  ::close(a);
  ::close(b);
}

}  // namespace

int main() {
  char tmpl[] = "/tmp/spc_test_XXXXXX";
  const char* dir = mkdtemp(tmpl);
  if (!dir) {
    std::cerr << "mkdtemp failed\n";
    return 1;
  }
  g_tmpdir = dir;

  test_hole_boundaries();
  test_all_hole_file();
  test_source_truncated_mid_copy();
  test_source_rewritten_mid_copy();
  test_dirty_tail_removed();
  test_short_reads_and_writes();
  test_resume_after_failure();
  test_cursor_binds_source_identity();
  test_fallback_zero_scan();
  test_real_filesystem();

  std::cout << (g_checks - g_failures) << "/" << g_checks
            << " checks passed\n";
  return g_failures == 0 ? 0 : 1;
}
