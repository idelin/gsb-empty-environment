#ifndef MEDIASAFE_READER_H
#define MEDIASAFE_READER_H

#include <cstddef>
#include <cstdint>
#include <cstring>

namespace mediasafe::detail {

// Bounds-checked, endian-aware cursor over a fixed byte buffer.
// All accessors return false instead of reading out of range, and all
// multi-byte loads are assembled bytewise so there is no alignment UB.
class ByteReader {
public:
    ByteReader(const uint8_t* data, std::size_t size, bool little_endian)
        : data_(data), size_(size), le_(little_endian) {}

    std::size_t size() const { return size_; }
    bool little_endian() const { return le_; }

    bool in_range(std::size_t offset, std::size_t length) const {
        if (offset > size_ || length > (size_ - offset)) {
            return false;
        }
        return true;
    }

    bool u8(std::size_t offset, uint8_t& out) const {
        if (!in_range(offset, 1)) return false;
        out = data_[offset];
        return true;
    }

    bool u16(std::size_t offset, uint16_t& out) const {
        if (!in_range(offset, 2)) return false;
        if (le_) {
            out = static_cast<uint16_t>(
                static_cast<uint16_t>(data_[offset]) |
                (static_cast<uint16_t>(data_[offset + 1]) << 8));
        } else {
            out = static_cast<uint16_t>(
                (static_cast<uint16_t>(data_[offset]) << 8) |
                static_cast<uint16_t>(data_[offset + 1]));
        }
        return true;
    }

    bool u32(std::size_t offset, uint32_t& out) const {
        if (!in_range(offset, 4)) return false;
        if (le_) {
            out = static_cast<uint32_t>(data_[offset]) |
                  (static_cast<uint32_t>(data_[offset + 1]) << 8) |
                  (static_cast<uint32_t>(data_[offset + 2]) << 16) |
                  (static_cast<uint32_t>(data_[offset + 3]) << 24);
        } else {
            out = (static_cast<uint32_t>(data_[offset]) << 24) |
                  (static_cast<uint32_t>(data_[offset + 1]) << 16) |
                  (static_cast<uint32_t>(data_[offset + 2]) << 8) |
                  static_cast<uint32_t>(data_[offset + 3]);
        }
        return true;
    }

    bool u64(std::size_t offset, uint64_t& out) const {
        uint32_t lo = 0;
        uint32_t hi = 0;
        if (le_) {
            if (!u32(offset, lo) || !u32(offset + 4, hi)) return false;
            out = static_cast<uint64_t>(lo) |
                   (static_cast<uint64_t>(hi) << 32);
        } else {
            if (!u32(offset, hi) || !u32(offset + 4, lo)) return false;
            out = (static_cast<uint64_t>(hi) << 32) |
                   static_cast<uint64_t>(lo);
        }
        return true;
    }

    bool bytes(std::size_t offset, std::size_t length,
               const uint8_t*& out) const {
        if (!in_range(offset, length)) return false;
        out = data_ + offset;
        return true;
    }

private:
    const uint8_t* data_;
    std::size_t size_;
    bool le_;
};

} // namespace mediasafe::detail

#endif // MEDIASAFE_READER_H
