#pragma once

#include <cstddef>
#include <cstdint>
#include <sys/types.h>

namespace sparsecopy {

// Snapshot-relevant identity/timestamps of a file.
struct FileStat {
  uint64_t dev = 0;
  uint64_t ino = 0;
  int64_t size = 0;
  int64_t mtime_ns = 0;
  int64_t ctime_ns = 0;
};

// Injectable POSIX facade. All methods return -1 and set errno on failure,
// mirroring the real syscalls so the copy engine has one error model.
// pread/pwrite may legally complete short; callers must loop.
class PosixAdapter {
public:
  virtual ~PosixAdapter() = default;

  virtual int open(const char* path, int flags, int mode) = 0;
  virtual int close(int fd) = 0;
  virtual ssize_t pread(int fd, void* buf, size_t count, int64_t offset) = 0;
  virtual ssize_t pwrite(int fd, const void* buf, size_t count, int64_t offset) = 0;
  virtual int fstat(int fd, FileStat& out) = 0;
  // Next data extent >= offset; -1/ENXIO if none before EOF.
  virtual int64_t seek_data(int fd, int64_t offset) = 0;
  // Next hole >= offset; the region after the last data extent up to EOF
  // counts as a hole.
  virtual int64_t seek_hole(int fd, int64_t offset) = 0;
  virtual int ftruncate(int fd, int64_t length) = 0;
  virtual int fsync(int fd) = 0;
};

class RealPosixAdapter : public PosixAdapter {
public:
  int open(const char* path, int flags, int mode) override;
  int close(int fd) override;
  ssize_t pread(int fd, void* buf, size_t count, int64_t offset) override;
  ssize_t pwrite(int fd, const void* buf, size_t count, int64_t offset) override;
  int fstat(int fd, FileStat& out) override;
  int64_t seek_data(int fd, int64_t offset) override;
  int64_t seek_hole(int fd, int64_t offset) override;
  int ftruncate(int fd, int64_t length) override;
  int fsync(int fd) override;
};

}  // namespace sparsecopy
