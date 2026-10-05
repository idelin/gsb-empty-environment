#include "sparsecopy/posix_adapter.hpp"

#include <cerrno>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>

namespace sparsecopy {

int RealPosixAdapter::open(const char* path, int flags, int mode) {
  return ::open(path, flags, static_cast<mode_t>(mode));
}

int RealPosixAdapter::close(int fd) { return ::close(fd); }

ssize_t RealPosixAdapter::pread(int fd, void* buf, size_t count, int64_t offset) {
  return ::pread(fd, buf, count, static_cast<off_t>(offset));
}

ssize_t RealPosixAdapter::pwrite(int fd, const void* buf, size_t count, int64_t offset) {
  return ::pwrite(fd, buf, count, static_cast<off_t>(offset));
}

int RealPosixAdapter::fstat(int fd, FileStat& out) {
  struct stat st;
  if (::fstat(fd, &st) != 0) return -1;
  out.dev = static_cast<uint64_t>(st.st_dev);
  out.ino = static_cast<uint64_t>(st.st_ino);
  out.size = static_cast<int64_t>(st.st_size);
#if defined(__APPLE__)
  out.mtime_ns = st.st_mtimespec.tv_sec * 1000000000LL + st.st_mtimespec.tv_nsec;
  out.ctime_ns = st.st_ctimespec.tv_sec * 1000000000LL + st.st_ctimespec.tv_nsec;
#else
  out.mtime_ns = st.st_mtim.tv_sec * 1000000000LL + st.st_mtim.tv_nsec;
  out.ctime_ns = st.st_ctim.tv_sec * 1000000000LL + st.st_ctim.tv_nsec;
#endif
  return 0;
}

// macOS has no SEEK_DATA/SEEK_HOLE; fall back to treating the file as one
// data extent. Logical content stays correct, holes are just not detected.
int64_t RealPosixAdapter::seek_data(int fd, int64_t offset) {
#if defined(SEEK_DATA)
  return static_cast<int64_t>(::lseek(fd, static_cast<off_t>(offset), SEEK_DATA));
#else
  struct stat st;
  if (::fstat(fd, &st) != 0) return -1;
  if (offset >= static_cast<int64_t>(st.st_size)) {
    errno = ENXIO;
    return -1;
  }
  return offset;
#endif
}

int64_t RealPosixAdapter::seek_hole(int fd, int64_t offset) {
#if defined(SEEK_HOLE)
  return static_cast<int64_t>(::lseek(fd, static_cast<off_t>(offset), SEEK_HOLE));
#else
  struct stat st;
  if (::fstat(fd, &st) != 0) return -1;
  if (offset >= static_cast<int64_t>(st.st_size)) {
    errno = EINVAL;
    return -1;
  }
  return static_cast<int64_t>(st.st_size);
#endif
}

int RealPosixAdapter::ftruncate(int fd, int64_t length) {
  return ::ftruncate(fd, static_cast<off_t>(length));
}

int RealPosixAdapter::fsync(int fd) { return ::fsync(fd); }

}  // namespace sparsecopy
