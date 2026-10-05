#include "posix_adapter.h"

#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>

namespace spc {

int RealPosixAdapter::open_read(const std::string& path) {
  int fd = ::open(path.c_str(), O_RDONLY);
  if (fd < 0) errno_ = errno;
  return fd;
}

int RealPosixAdapter::open_write(const std::string& path) {
  int fd = ::open(path.c_str(), O_RDWR | O_CREAT, 0644);
  if (fd < 0) errno_ = errno;
  return fd;
}

int RealPosixAdapter::close(int fd) {
  int rc = ::close(fd);
  if (rc < 0) errno_ = errno;
  return rc;
}

ssize_t RealPosixAdapter::pread(int fd, void* buf, size_t count,
                                uint64_t offset) {
  ssize_t n = ::pread(fd, buf, count, static_cast<off_t>(offset));
  if (n < 0) errno_ = errno;
  return n;
}

ssize_t RealPosixAdapter::pwrite(int fd, const void* buf, size_t count,
                                 uint64_t offset) {
  ssize_t n = ::pwrite(fd, buf, count, static_cast<off_t>(offset));
  if (n < 0) errno_ = errno;
  return n;
}

bool RealPosixAdapter::fstat(int fd, FileStat& out) {
  struct stat st;
  if (::fstat(fd, &st) != 0) {
    errno_ = errno;
    return false;
  }
  out.dev = static_cast<uint64_t>(st.st_dev);
  out.ino = static_cast<uint64_t>(st.st_ino);
  out.size = static_cast<uint64_t>(st.st_size);
#if defined(__APPLE__)
  out.mtime_ns = static_cast<uint64_t>(st.st_mtimespec.tv_sec) * 1000000000ull +
                 static_cast<uint64_t>(st.st_mtimespec.tv_nsec);
  out.ctime_ns = static_cast<uint64_t>(st.st_ctimespec.tv_sec) * 1000000000ull +
                 static_cast<uint64_t>(st.st_ctimespec.tv_nsec);
#else
  out.mtime_ns = static_cast<uint64_t>(st.st_mtim.tv_sec) * 1000000000ull +
                 static_cast<uint64_t>(st.st_mtim.tv_nsec);
  out.ctime_ns = static_cast<uint64_t>(st.st_ctim.tv_sec) * 1000000000ull +
                 static_cast<uint64_t>(st.st_ctim.tv_nsec);
#endif
  return true;
}

bool RealPosixAdapter::ftruncate(int fd, uint64_t length) {
  if (::ftruncate(fd, static_cast<off_t>(length)) != 0) {
    errno_ = errno;
    return false;
  }
  return true;
}

bool RealPosixAdapter::fsync(int fd) {
  if (::fsync(fd) != 0) {
    errno_ = errno;
    return false;
  }
  return true;
}

int64_t RealPosixAdapter::seek_data(int fd, uint64_t offset, bool& supported) {
#if defined(SEEK_DATA) && defined(SEEK_HOLE)
  off_t r = ::lseek(fd, static_cast<off_t>(offset), SEEK_DATA);
  if (r < 0) {
    if (errno == EINVAL || errno == ENOTSUP || errno == ENODEV) {
      supported = false;
      return -1;
    }
    errno_ = errno;  // ENXIO: no data beyond offset
    return -1;
  }
  return static_cast<int64_t>(r);
#else
  (void)fd;
  (void)offset;
  supported = false;
  return -1;
#endif
}

int64_t RealPosixAdapter::seek_hole(int fd, uint64_t offset, bool& supported) {
#if defined(SEEK_DATA) && defined(SEEK_HOLE)
  off_t r = ::lseek(fd, static_cast<off_t>(offset), SEEK_HOLE);
  if (r < 0) {
    if (errno == EINVAL || errno == ENOTSUP || errno == ENODEV) {
      supported = false;
      return -1;
    }
    errno_ = errno;
    return -1;
  }
  return static_cast<int64_t>(r);
#else
  (void)fd;
  (void)offset;
  supported = false;
  return -1;
#endif
}

}  // namespace spc
