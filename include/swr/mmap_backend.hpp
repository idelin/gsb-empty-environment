#pragma once

#include <memory>
#include <string>

#include "swr/mapping_backend.hpp"

namespace swr {

// Production backend: maps file ranges with POSIX mmap(PROT_READ, MAP_PRIVATE).
// Opens the file read-only; construction throws std::system_error on failure.
class MmapBackend : public IMappingBackend {
 public:
  explicit MmapBackend(const std::string& path);
  ~MmapBackend() override;

  MmapBackend(const MmapBackend&) = delete;
  MmapBackend& operator=(const MmapBackend&) = delete;

  Result<Mapping> map(std::uint64_t offset, std::size_t length,
                      const CancelToken& token) override;
  std::uint64_t size() const override;

 private:
  int fd_ = -1;
  std::uint64_t size_ = 0;
};

}  // namespace swr
