#ifndef SPC_TEST_FAKE_ADAPTER_H
#define SPC_TEST_FAKE_ADAPTER_H

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <functional>
#include <map>
#include <string>
#include <vector>

#include "posix_adapter.h"

namespace spc {
namespace test {

// In-memory PosixAdapter with fault injection: short reads, short writes,
// I/O errors after N written bytes, and mutation hooks to simulate a source
// that changes mid-copy.
class FakeAdapter : public PosixAdapter {
public:
  struct Faults {
    size_t max_read = SIZE_MAX;    // cap per pread (short reads)
    size_t max_write = SIZE_MAX;   // cap per pwrite (short writes)
    uint64_t fail_write_after = UINT64_MAX;  // EIO once this many bytes written
    std::function<void(FakeAdapter&)> on_pread;  // mutation hook
  };

  Faults faults;
  bool seek_supported = true;

  void create_file(const std::string& path, const std::string& content) {
    File f;
    f.ino = next_ino_++;
    f.bytes.assign(content.begin(), content.end());
    touch(f);
    files_[path] = f;
  }

  void create_sparse(const std::string& path, uint64_t size,
                     const std::map<uint64_t, std::string>& chunks) {
    File f;
    f.ino = next_ino_++;
    f.bytes.assign(size, 0);
    for (const auto& kv : chunks)
      std::memcpy(f.bytes.data() + kv.first, kv.second.data(),
                  kv.second.size());
    touch(f);
    files_[path] = f;
  }

  void truncate_file(const std::string& path, uint64_t size) {
    File& f = files_.at(path);
    f.bytes.resize(size, 0);
    touch(f);
  }

  void mutate_byte(const std::string& path, uint64_t off, char v) {
    File& f = files_.at(path);
    f.bytes.at(off) = v;
    touch(f);
  }

  std::string read_file(const std::string& path) {
    const File& f = files_.at(path);
    return std::string(f.bytes.begin(), f.bytes.end());
  }

  uint64_t file_size(const std::string& path) {
    return files_.at(path).bytes.size();
  }

  int open_read(const std::string& path) override {
    auto it = files_.find(path);
    if (it == files_.end()) {
      errno_ = ENOENT;
      return -1;
    }
    return alloc_fd(path);
  }

  int open_write(const std::string& path) override {
    if (files_.find(path) == files_.end()) create_file(path, "");
    return alloc_fd(path);
  }

  int close(int fd) override {
    fds_.erase(fd);
    return 0;
  }

  ssize_t pread(int fd, void* buf, size_t count, uint64_t offset) override {
    if (faults.on_pread) faults.on_pread(*this);
    File& f = files_.at(fds_.at(fd));
    if (offset >= f.bytes.size()) return 0;
    size_t n = std::min<size_t>(count, f.bytes.size() - offset);
    n = std::min(n, faults.max_read);
    std::memcpy(buf, f.bytes.data() + offset, n);
    return static_cast<ssize_t>(n);
  }

  ssize_t pwrite(int fd, const void* buf, size_t count,
                 uint64_t offset) override {
    if (written_ >= faults.fail_write_after) {
      errno_ = EIO;
      return -1;
    }
    File& f = files_.at(fds_.at(fd));
    size_t n = std::min(count, faults.max_write);
    if (offset + n > f.bytes.size()) f.bytes.resize(offset + n, 0);
    std::memcpy(f.bytes.data() + offset, buf, n);
    written_ += n;
    touch(f);
    return static_cast<ssize_t>(n);
  }

  bool fstat(int fd, FileStat& out) override {
    const File& f = files_.at(fds_.at(fd));
    out.dev = 1;
    out.ino = f.ino;
    out.size = f.bytes.size();
    out.mtime_ns = f.mtime;
    out.ctime_ns = f.ctime;
    return true;
  }

  bool ftruncate(int fd, uint64_t length) override {
    File& f = files_.at(fds_.at(fd));
    f.bytes.resize(length, 0);
    touch(f);
    return true;
  }

  bool fsync(int) override { return true; }

  int64_t seek_data(int fd, uint64_t offset, bool& supported) override {
    if (!seek_supported) {
      supported = false;
      return -1;
    }
    const File& f = files_.at(fds_.at(fd));
    for (uint64_t i = offset; i < f.bytes.size(); ++i)
      if (f.bytes[i] != 0) return static_cast<int64_t>(i);
    errno_ = ENXIO;
    return -1;
  }

  int64_t seek_hole(int fd, uint64_t offset, bool& supported) override {
    if (!seek_supported) {
      supported = false;
      return -1;
    }
    const File& f = files_.at(fds_.at(fd));
    for (uint64_t i = offset; i < f.bytes.size(); ++i)
      if (f.bytes[i] == 0) return static_cast<int64_t>(i);
    return static_cast<int64_t>(f.bytes.size());
  }

  int last_errno() const override { return errno_; }

private:
  struct File {
    std::vector<char> bytes;
    uint64_t ino = 0;
    uint64_t mtime = 0;
    uint64_t ctime = 0;
  };

  void touch(File& f) {
    f.mtime = clock_++;
    f.ctime = clock_++;
  }

  int alloc_fd(const std::string& path) {
    int fd = next_fd_++;
    fds_[fd] = path;
    return fd;
  }

  std::map<std::string, File> files_;
  std::map<int, std::string> fds_;
  uint64_t clock_ = 1;
  uint64_t next_ino_ = 100;
  uint64_t written_ = 0;
  int next_fd_ = 3;
  int errno_ = 0;
};

}  // namespace test
}  // namespace spc

#endif
