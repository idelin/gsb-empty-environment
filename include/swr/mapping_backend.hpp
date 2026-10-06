#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <utility>

#include "swr/cancel_token.hpp"
#include "swr/error.hpp"

namespace swr {

// Abstraction over a source of mapped byte ranges (mmap, VirtualAlloc,
// in-memory test doubles, ...). A successful map() returns a Mapping whose
// destruction releases the underlying mapping.
class IMappingBackend {
 public:
  // RAII handle for a mapped region. Move-only.
  class Mapping {
   public:
    Mapping() = default;
    Mapping(const std::byte* data, std::size_t size, std::function<void()> unmap)
        : data_(data), size_(size), unmap_(std::move(unmap)) {}
    Mapping(Mapping&& other) noexcept { *this = std::move(other); }
    Mapping& operator=(Mapping&& other) noexcept {
      if (this != &other) {
        reset();
        data_ = std::exchange(other.data_, nullptr);
        size_ = std::exchange(other.size_, 0);
        unmap_ = std::move(other.unmap_);
      }
      return *this;
    }
    Mapping(const Mapping&) = delete;
    Mapping& operator=(const Mapping&) = delete;
    ~Mapping() { reset(); }

    const std::byte* data() const { return data_; }
    std::size_t size() const { return size_; }
    explicit operator bool() const { return data_ != nullptr; }

   private:
    void reset() {
      if (data_ && unmap_) unmap_();
      data_ = nullptr;
      size_ = 0;
    }

    const std::byte* data_ = nullptr;
    std::size_t size_ = 0;
    std::function<void()> unmap_;
  };

  virtual ~IMappingBackend() = default;

  // Maps [offset, offset+length) of the file. May block; must observe the
  // cancel token and return Error::kCancelled promptly when it fires.
  virtual Result<Mapping> map(std::uint64_t offset, std::size_t length,
                              const CancelToken& token) = 0;

  // Total size of the backing file in bytes.
  virtual std::uint64_t size() const = 0;
};

}  // namespace swr
