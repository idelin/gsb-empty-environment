// In-memory PosixAdapter with sparse extent tracking and injectable
// short reads/writes plus an operation hook (used to mutate the source
// mid-copy). Byte i is a "hole" when present[i] == 0; holes read as 0.
#pragma once

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <functional>
#include <map>
#include <memory>
#include <string>
#include <vector>

#include "sparsecopy/posix_adapter.hpp"

namespace sparsecopy {
namespace test {

class FakePosixAdapter : public PosixAdapter {
public:
  struct File {
    std::vector<char> bytes;    // logical content (holes are 0)
    std::vector<char> present;  // 1 = data extent, 0 = hole
    FileStat st;
  };

  size_t max_read = static_cast<size_t>(-1);   // short-read injection
  size_t max_write = static_cast<size_t>(-1);  // short-write injection
  // Called before every operation with (op name, fd); tests may mutate
  // files here, e.g. truncate the source mid-copy.
  std::function<void(const char* op, int fd)> hook;

  int create_file(int64_t size, uint64_t dev = 1, uint64_t ino = 0) {
    auto f = std::make_shared<File>();
    f->bytes.assign(static_cast<size_t>(size), 0);
    f->present.assign(static_cast<size_t>(size), 0);
    f->st.dev = dev;
    f->st.ino = ino ? ino : next_ino_++;
    f->st.size = size;
    f->st.mtime_ns = 1000;
    f->st.ctime_ns = 1000;
    const int fd = next_fd_++;
    files_[fd] = std::move(f);
    return fd;
  }

  // Directly write data, marking the range as a data extent.
  void plant_data(int fd, int64_t offset, const void* buf, size_t n) {
    File& f = at(fd);
    std::memcpy(f.bytes.data() + offset, buf, n);
    std::fill_n(f.present.begin() + offset, n, 1);
    touch(f);
  }

  void truncate_file(int fd, int64_t size) {
    File& f = at(fd);
    f.bytes.resize(static_cast<size_t>(size), 0);
    f.present.resize(static_cast<size_t>(size), 0);
    f.st.size = size;
    touch(f);
  }

  const File& file(int fd) const { return *files_.at(fd); }

  // List of [begin, end) data extents, used to compare hole layouts.
  std::vector<std::pair<int64_t, int64_t>> data_extents(int fd) const {
    const File& f = at(fd);
    std::vector<std::pair<int64_t, int64_t>> out;
    int64_t i = 0;
    while (i < f.st.size) {
      if (f.present[static_cast<size_t>(i)]) {
        int64_t j = i;
        while (j < f.st.size && f.present[static_cast<size_t>(j)]) ++j;
        out.emplace_back(i, j);
        i = j;
      } else {
        ++i;
      }
    }
    return out;
  }

  int open(const char*, int, int) override {
    errno = ENOSYS;
    return -1;
  }

  int close(int) override { return 0; }

  ssize_t pread(int fd, void* buf, size_t count, int64_t offset) override {
    if (hook) hook("pread", fd);
    File& f = at(fd);
    if (offset >= f.st.size) return 0;
    size_t n = std::min(count, static_cast<size_t>(f.st.size - offset));
    n = std::min(n, max_read);
    std::memcpy(buf, f.bytes.data() + offset, n);
    return static_cast<ssize_t>(n);
  }

  ssize_t pwrite(int fd, const void* buf, size_t count, int64_t offset) override {
    if (hook) hook("pwrite", fd);
    File& f = at(fd);
    size_t n = std::min(count, max_write);
    const int64_t end = offset + static_cast<int64_t>(n);
    if (end > f.st.size) {
      f.bytes.resize(static_cast<size_t>(end), 0);
      f.present.resize(static_cast<size_t>(end), 0);
      f.st.size = end;
    }
    std::memcpy(f.bytes.data() + offset, buf, n);
    std::fill_n(f.present.begin() + offset, n, 1);
    touch(f);
    return static_cast<ssize_t>(n);
  }

  int fstat(int fd, FileStat& out) override {
    if (hook) hook("fstat", fd);
    out = at(fd).st;
    return 0;
  }

  int64_t seek_data(int fd, int64_t offset) override {
    if (hook) hook("seek_data", fd);
    File& f = at(fd);
    if (offset < 0 || offset >= f.st.size) {
      errno = ENXIO;
      return -1;
    }
    for (int64_t i = offset; i < f.st.size; ++i) {
      if (f.present[static_cast<size_t>(i)]) return i;
    }
    errno = ENXIO;
    return -1;
  }

  int64_t seek_hole(int fd, int64_t offset) override {
    if (hook) hook("seek_hole", fd);
    File& f = at(fd);
    if (offset < 0 || offset >= f.st.size) {
      errno = EINVAL;
      return -1;
    }
    for (int64_t i = offset; i < f.st.size; ++i) {
      if (!f.present[static_cast<size_t>(i)]) return i;
    }
    return f.st.size;
  }

  int ftruncate(int fd, int64_t length) override {
    if (hook) hook("ftruncate", fd);
    truncate_file(fd, length);
    return 0;
  }

  int fsync(int) override { return 0; }

private:
  File& at(int fd) { return *files_.at(fd); }
  const File& at(int fd) const { return *files_.at(fd); }
  static void touch(File& f) {
    f.st.mtime_ns += 1;
    f.st.ctime_ns += 1;
  }

  std::map<int, std::shared_ptr<File>> files_;
  int next_fd_ = 3;
  uint64_t next_ino_ = 100;
};

}  // namespace test
}  // namespace sparsecopy
