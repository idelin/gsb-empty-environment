#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <string_view>
#include <vector>

namespace netsec::wire {

// Big-endian SSH wire-format primitives shared by the certificate parser
// (production) and the in-memory certificate builder (test harness).

class Reader {
public:
    Reader() = default;
    explicit Reader(std::string_view blob) : blob_(blob) {}

    bool empty() const noexcept { return pos_ >= blob_.size(); }
    std::size_t remaining() const noexcept { return blob_.size() - pos_; }

    bool readByte(std::uint8_t& out);
    bool readBool(bool& out);
    bool readUint32(std::uint32_t& out);
    bool readUint64(std::uint64_t& out);
    bool readString(std::string_view& out);
    bool readString(std::string& out);
    bool readBytes(std::string_view& out, std::size_t length);

private:
    std::string_view blob_;
    std::size_t pos_ = 0;
};

class Writer {
public:
    void writeByte(std::uint8_t value);
    void writeBool(bool value);
    void writeUint32(std::uint32_t value);
    void writeUint64(std::uint64_t value);
    void writeString(std::string_view value);
    void writeRaw(std::string_view value);

    std::string take() { return std::move(buffer_); }
    const std::string& buffer() const noexcept { return buffer_; }

private:
    std::string buffer_;
};

}  // namespace netsec::wire
