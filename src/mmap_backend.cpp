#include "swr/mmap_backend.hpp"

#include <fcntl.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

#include <cerrno>
#include <cstring>
#include <stdexcept>
#include <system_error>

namespace swr {

MmapBackend::MmapBackend(const std::string& path) {
  fd_ = ::open(path.c_str(), O_RDONLY);
  if (fd_ < 0) {
    throw std::system_error(errno, std::generic_category(), "open: " + path);
  }
  struct stat st {};
  if (::fstat(fd_, &st) != 0) {
    int e = errno;
    ::close(fd_);
    fd_ = -1;
    throw std::system_error(e, std::generic_category(), "fstat: " + path);
  }
  size_ = static_cast<std::uint64_t>(st.st_size);
}

MmapBackend::~MmapBackend() {
  if (fd_ >= 0) ::close(fd_);
}

Result<IMappingBackend::Mapping> MmapBackend::map(std::uint64_t offset, std::size_t length,
                                                  const CancelToken& token) {
  if (token->cancelled()) return std::unexpected(Error::kCancelled);
  if (offset > size_ || length > size_ - offset) {
    return std::unexpected(Error::kOutOfRange);
  }
  if (length == 0) return std::unexpected(Error::kInvalidArgument);

  // mmap requires page-aligned offsets; map a slightly larger region and
  // hand back an interior pointer.
  const long page = ::sysconf(_SC_PAGESIZE);
  const std::uint64_t aligned = offset - (offset % static_cast<std::uint64_t>(page));
  const std::size_t extra = static_cast<std::size_t>(offset - aligned);
  const std::size_t map_len = extra + length;

  void* p = ::mmap(nullptr, map_len, PROT_READ, MAP_PRIVATE, fd_, static_cast<off_t>(aligned));
  if (p == MAP_FAILED) return std::unexpected(Error::kMapFailed);
  if (token->cancelled()) {
    ::munmap(p, map_len);
    return std::unexpected(Error::kCancelled);
  }

  const std::byte* data = static_cast<const std::byte*>(p) + extra;
  return Mapping(data, length, [p, map_len] { ::munmap(p, map_len); });
}

std::uint64_t MmapBackend::size() const { return size_; }

}  // namespace swr
