#include "netsec/encoding.hpp"

#include <array>
#include <stdexcept>

namespace netsec {

namespace {

constexpr char kHexDigits[] = "0123456789abcdef";

constexpr std::array<char, 64> kBase64Table = {
    'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M', 'N', 'O', 'P',
    'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z', 'a', 'b', 'c', 'd', 'e', 'f',
    'g', 'h', 'i', 'j', 'k', 'l', 'm', 'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v',
    'w', 'x', 'y', 'z', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '+', '/'};

int base64_value(char ch) noexcept {
    if (ch >= 'A' && ch <= 'Z') return ch - 'A';
    if (ch >= 'a' && ch <= 'z') return ch - 'a' + 26;
    if (ch >= '0' && ch <= '9') return ch - '0' + 52;
    if (ch == '+') return 62;
    if (ch == '/') return 63;
    return -1;
}

int hex_value(char ch) noexcept {
    if (ch >= '0' && ch <= '9') return ch - '0';
    if (ch >= 'a' && ch <= 'f') return ch - 'a' + 10;
    if (ch >= 'A' && ch <= 'F') return ch - 'A' + 10;
    return -1;
}

}  // namespace

std::string to_hex(std::span<const std::uint8_t> bytes) {
    std::string result;
    result.reserve(bytes.size() * 2);
    for (std::uint8_t byte : bytes) {
        result.push_back(kHexDigits[byte >> 4]);
        result.push_back(kHexDigits[byte & 0x0F]);
    }
    return result;
}

std::vector<std::uint8_t> from_hex(std::string_view hex) {
    if (hex.size() % 2 != 0) {
        throw std::invalid_argument("hex string has odd length");
    }
    std::vector<std::uint8_t> result;
    result.reserve(hex.size() / 2);
    for (std::size_t i = 0; i < hex.size(); i += 2) {
        const int high = hex_value(hex[i]);
        const int low = hex_value(hex[i + 1]);
        if (high < 0 || low < 0) {
            throw std::invalid_argument("invalid hex character");
        }
        result.push_back(static_cast<std::uint8_t>((high << 4) | low));
    }
    return result;
}

std::string to_base64(std::span<const std::uint8_t> bytes) {
    std::string result;
    result.reserve(((bytes.size() + 2) / 3) * 4);

    std::size_t i = 0;
    for (; i + 3 <= bytes.size(); i += 3) {
        const std::uint32_t triple =
            (std::uint32_t{bytes[i]} << 16) | (std::uint32_t{bytes[i + 1]} << 8) |
            std::uint32_t{bytes[i + 2]};
        result.push_back(kBase64Table[(triple >> 18) & 0x3F]);
        result.push_back(kBase64Table[(triple >> 12) & 0x3F]);
        result.push_back(kBase64Table[(triple >> 6) & 0x3F]);
        result.push_back(kBase64Table[triple & 0x3F]);
    }

    const std::size_t remaining = bytes.size() - i;
    if (remaining == 1) {
        const std::uint32_t triple = std::uint32_t{bytes[i]} << 16;
        result.push_back(kBase64Table[(triple >> 18) & 0x3F]);
        result.push_back(kBase64Table[(triple >> 12) & 0x3F]);
        result.push_back('=');
        result.push_back('=');
    } else if (remaining == 2) {
        const std::uint32_t triple =
            (std::uint32_t{bytes[i]} << 16) | (std::uint32_t{bytes[i + 1]} << 8);
        result.push_back(kBase64Table[(triple >> 18) & 0x3F]);
        result.push_back(kBase64Table[(triple >> 12) & 0x3F]);
        result.push_back(kBase64Table[(triple >> 6) & 0x3F]);
        result.push_back('=');
    }
    return result;
}

std::vector<std::uint8_t> from_base64(std::string_view base64) {
    if (base64.size() % 4 != 0) {
        throw std::invalid_argument("base64 input length must be a multiple of 4");
    }

    std::vector<std::uint8_t> result;
    result.reserve((base64.size() / 4) * 3);

    for (std::size_t i = 0; i < base64.size(); i += 4) {
        const bool pad1 = base64[i + 2] == '=';
        const bool pad2 = base64[i + 3] == '=';
        if (pad1 && !pad2) {
            throw std::invalid_argument("malformed base64 padding");
        }

        const int v0 = base64_value(base64[i]);
        const int v1 = base64_value(base64[i + 1]);
        const int v2 = pad1 ? 0 : base64_value(base64[i + 2]);
        const int v3 = pad2 ? 0 : base64_value(base64[i + 3]);
        if (v0 < 0 || v1 < 0 || v2 < 0 || v3 < 0) {
            throw std::invalid_argument("invalid base64 character");
        }

        const std::uint32_t triple =
            (static_cast<std::uint32_t>(v0) << 18) |
            (static_cast<std::uint32_t>(v1) << 12) |
            (static_cast<std::uint32_t>(v2) << 6) |
            static_cast<std::uint32_t>(v3);
        result.push_back(static_cast<std::uint8_t>((triple >> 16) & 0xFF));
        if (!pad1) {
            result.push_back(static_cast<std::uint8_t>((triple >> 8) & 0xFF));
        }
        if (!pad2) {
            result.push_back(static_cast<std::uint8_t>(triple & 0xFF));
        }
    }
    return result;
}

}  // namespace netsec
