#ifndef SPC_POSIX_ADAPTER_H
#define SPC_POSIX_ADAPTER_H

#include <cstddef>
#include <cstdint>
#include <string>
#include <sys/types.h>

namespace spc {

struct FileStat {
  uint64_t dev = 0;
  uint64_t ino = 0;
  uint64_t size = 0;
  uint64_t mtime_ns = 0;
  uint64_t ctime_ns = 0;
};

// Abstraction over the POSIX file syscalls the copier needs.
// Methods report failure C-style (negative return / false) and expose the
// error code through last_errno(). Short reads/writes are NOT errors; the
// engine loops around them. Tests inject faults through a fake.
class PosixAdapter {
public:
  virtual ~PosixAdapter() = default;

  virtual int open_read(const std::string& path) = 0;
  // Opens for read/write, creating if needed. Must NOT truncate.
  virtual int open_write(const std::string& path) = 0;
  virtual int close(int fd) = 0;

  virtual ssize_t pread(int fd, void* buf, size_t count, uint64_t offset) = 0;
  virtual ssize_t pwrite(int fd, const void* buf, size_t count,
                         uint64_t offset) = 0;

  virtual bool fstat(int fd, FileStat& out) = 0;
  virtual bool ftruncate(int fd, uint64_t length) = 0;
  virtual bool fsync(int fd) = 0;

  // Returns the offset of the next data extent >= offset, or -1 when no data
  // remains (ENXIO semantics). Sets supported=false when the backend cannot
  // scan for holes (e.g. SEEK_DATA missing); the engine then falls back to
  // zero-block scanning.
  virtual int64_t seek_data(int fd, uint64_t offset, bool& supported) = 0;
  // Returns the offset of the next hole >= offset.
  virtual int64_t seek_hole(int fd, uint64_t offset, bool& supported) = 0;

  virtual int last_errno() const = 0;
};

// Real adapter backed by actual syscalls.
class RealPosixAdapter : public PosixAdapter {
public:
  int open_read(const std::string& path) override;
  int open_write(const std::string& path) override;
  int close(int fd) override;
  ssize_t pread(int fd, void* buf, size_t count, uint64_t offset) override;
  ssize_t pwrite(int fd, const void* buf, size_t count,
                 uint64_t offset) override;
  bool fstat(int fd, FileStat& out) override;
  bool ftruncate(int fd, uint64_t length) override;
  bool fsync(int fd) override;
  int64_t seek_data(int fd, uint64_t offset, bool& supported) override;
  int64_t seek_hole(int fd, uint64_t offset, bool& supported) override;
  int last_errno() const override { return errno_; }

private:
  int errno_ = 0;
};

}  // namespace spc

#endif
