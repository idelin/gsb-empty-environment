#include "netsec/wire.hpp"

#include <cstring>

namespace netsec::wire {

bool Reader::readByte(std::uint8_t& out) {
    if (remaining() < 1) {
        return false;
    }
    out = static_cast<std::uint8_t>(blob_[pos_++]);
    return true;
}

bool Reader::readBool(bool& out) {
    std::uint8_t value = 0;
    if (!readByte(value)) {
        return false;
    }
    out = value != 0;
    return true;
}

bool Reader::readUint32(std::uint32_t& out) {
    if (remaining() < 4) {
        return false;
    }
    std::uint32_t value = 0;
    for (int i = 0; i < 4; ++i) {
        value = (value << 8) |
                static_cast<std::uint8_t>(blob_[pos_ + static_cast<std::size_t>(i)]);
    }
    pos_ += 4;
    out = value;
    return true;
}

bool Reader::readUint64(std::uint64_t& out) {
    if (remaining() < 8) {
        return false;
    }
    std::uint64_t value = 0;
    for (int i = 0; i < 8; ++i) {
        value = (value << 8) |
                static_cast<std::uint8_t>(blob_[pos_ + static_cast<std::size_t>(i)]);
    }
    pos_ += 8;
    out = value;
    return true;
}

bool Reader::readString(std::string_view& out) {
    std::uint32_t length = 0;
    if (!readUint32(length)) {
        return false;
    }
    if (length > remaining()) {
        return false;
    }
    out = blob_.substr(pos_, length);
    pos_ += length;
    return true;
}

bool Reader::readString(std::string& out) {
    std::string_view view;
    if (!readString(view)) {
        return false;
    }
    out.assign(view.data(), view.size());
    return true;
}

bool Reader::readBytes(std::string_view& out, std::size_t length) {
    if (length > remaining()) {
        return false;
    }
    out = blob_.substr(pos_, length);
    pos_ += length;
    return true;
}

void Writer::writeByte(std::uint8_t value) {
    buffer_.push_back(static_cast<char>(value));
}

void Writer::writeBool(bool value) {
    writeByte(value ? 1 : 0);
}

void Writer::writeUint32(std::uint32_t value) {
    for (int shift = 24; shift >= 0; shift -= 8) {
        writeByte(static_cast<std::uint8_t>((value >> shift) & 0xFFu));
    }
}

void Writer::writeUint64(std::uint64_t value) {
    for (int shift = 56; shift >= 0; shift -= 8) {
        writeByte(static_cast<std::uint8_t>((value >> shift) & 0xFFu));
    }
}

void Writer::writeString(std::string_view value) {
    writeUint32(static_cast<std::uint32_t>(value.size()));
    buffer_.append(value.data(), value.size());
}

void Writer::writeRaw(std::string_view value) {
    buffer_.append(value.data(), value.size());
}

}  // namespace netsec::wire
