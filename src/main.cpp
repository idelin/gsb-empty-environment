#include <cerrno>
#include <cstdio>
#include <cstring>
#include <fcntl.h>
#include <string>

#include "sparsecopy/posix_adapter.hpp"
#include "sparsecopy/sparse_copy.hpp"

namespace {

int copy_file(sparsecopy::PosixAdapter& os, const char* src, const char* dst,
              sparsecopy::ResumeCursor* cursor) {
  int sfd = os.open(src, O_RDONLY, 0);
  if (sfd < 0) {
    std::fprintf(stderr, "open %s: %s\n", src, std::strerror(errno));
    return 1;
  }
  int dfd = os.open(dst, O_WRONLY | O_CREAT, 0644);
  if (dfd < 0) {
    std::fprintf(stderr, "open %s: %s\n", dst, std::strerror(errno));
    os.close(sfd);
    return 1;
  }
  sparsecopy::SparseCopier copier(os);
  int rc = 0;
  try {
    copier.copy(sfd, dfd, cursor);
  } catch (const sparsecopy::SourceChangedError& e) {
    std::fprintf(stderr, "sparse-copy: %s; restart the copy\n", e.what());
    rc = 2;
  } catch (const sparsecopy::CopyError& e) {
    std::fprintf(stderr, "sparse-copy: %s\n", e.what());
    rc = 1;
  }
  os.close(dfd);
  os.close(sfd);
  return rc;
}

}  // namespace

int main(int argc, char** argv) {
  if (argc != 3 && argc != 4) {
    std::fprintf(stderr, "usage: sparse-copy SRC DST [CURSOR_FILE]\n");
    return 64;
  }
  sparsecopy::RealPosixAdapter os;
  sparsecopy::ResumeCursor cursor;
  sparsecopy::ResumeCursor* cursor_ptr = nullptr;
  std::string cursor_path;
  if (argc == 4) {
    cursor_path = argv[3];
    if (FILE* f = std::fopen(cursor_path.c_str(), "r")) {
      char buf[256] = {0};
      if (std::fgets(buf, sizeof(buf), f) &&
          sparsecopy::ResumeCursor::parse(buf, cursor)) {
        cursor_ptr = &cursor;
      }
      std::fclose(f);
    }
    if (!cursor_ptr) {
      cursor_ptr = &cursor;  // engine initializes it on a fresh copy
    }
  }
  int rc = copy_file(os, argv[1], argv[2], cursor_ptr);
  if (rc == 0 && !cursor_path.empty()) {
    if (FILE* f = std::fopen(cursor_path.c_str(), "w")) {
      const std::string line = cursor.serialize();
      std::fwrite(line.data(), 1, line.size(), f);
      std::fwrite("\n", 1, 1, f);
      std::fclose(f);
    }
  }
  return rc;
}
