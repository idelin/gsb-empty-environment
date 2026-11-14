#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstdlib>
#include <cstring>
#include <iterator>
#include <limits>
#include <random>
#include <sstream>
#include <utility>

#if defined(__linux__)
#include <sys/random.h>
#endif

namespace netsec::radius {
namespace {

constexpr std::size_t kHeaderSize = 20;
constexpr std::size_t kMaxPacketSize = 4096;
constexpr std::size_t kMaxAttributeValue = 253;
constexpr std::size_t kMaxProxyStateSize = 253;

constexpr std::array<std::uint8_t, 4> kProxyStateMagic{0x4e, 0x53, 0x50, 0x58};
constexpr std::uint8_t kProxyStateVersion = 1;

constexpr std::size_t kStateOffsetSigningGeneration = 6;
constexpr std::size_t kStateOffsetOriginGeneration = 14;
constexpr std::size_t kStateOffsetNonce = 22;
constexpr std::size_t kStateOffsetRequestDigest = 38;
constexpr std::size_t kStateOffsetUpstreamDigest = 54;
constexpr std::size_t kStateOffsetRouteId = 86;
constexpr std::size_t kStateOffsetNasHash = 102;
constexpr std::size_t kStateOffsetPeerHash = 118;
constexpr std::size_t kStateOffsetHmac = 134;
constexpr std::size_t kProxyStateWireSize = 166;

[[nodiscard]] std::uint16_t read_be16(const std::uint8_t* data) {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(data[0]) << 8U) |
        static_cast<std::uint16_t>(data[1]));
}

[[nodiscard]] std::uint32_t read_be32(const std::uint8_t* data) {
    return (static_cast<std::uint32_t>(data[0]) << 24U) |
           (static_cast<std::uint32_t>(data[1]) << 16U) |
           (static_cast<std::uint32_t>(data[2]) << 8U) |
           static_cast<std::uint32_t>(data[3]);
}

[[nodiscard]] std::uint32_t read_le32(const std::uint8_t* data) {
    return static_cast<std::uint32_t>(data[0]) |
           (static_cast<std::uint32_t>(data[1]) << 8U) |
           (static_cast<std::uint32_t>(data[2]) << 16U) |
           (static_cast<std::uint32_t>(data[3]) << 24U);
}

[[nodiscard]] std::uint64_t read_be64(const std::uint8_t* data) {
    std::uint64_t value = 0;
    for (std::size_t i = 0; i < 8; ++i) {
        value = (value << 8U) | static_cast<std::uint64_t>(data[i]);
    }
    return value;
}

void write_be16(Octets& out, std::uint16_t value) {
    out.push_back(static_cast<std::uint8_t>((value >> 8U) & 0xffU));
    out.push_back(static_cast<std::uint8_t>(value & 0xffU));
}

void write_be32(Octets& out, std::uint32_t value) {
    out.push_back(static_cast<std::uint8_t>((value >> 24U) & 0xffU));
    out.push_back(static_cast<std::uint8_t>((value >> 16U) & 0xffU));
    out.push_back(static_cast<std::uint8_t>((value >> 8U) & 0xffU));
    out.push_back(static_cast<std::uint8_t>(value & 0xffU));
}

void write_be64(Octets& out, std::uint64_t value) {
    for (std::size_t i = 0; i < 8; ++i) {
        const std::size_t shift = (7U - i) * 8U;
        out.push_back(static_cast<std::uint8_t>((value >> shift) & 0xffU));
    }
}

[[nodiscard]] bool constant_time_equal(const std::uint8_t* lhs,
                                       const std::uint8_t* rhs,
                                       std::size_t size) noexcept {
    std::uint8_t difference = 0;
    for (std::size_t i = 0; i < size; ++i) {
        difference = static_cast<std::uint8_t>(difference | (lhs[i] ^ rhs[i]));
    }
    return difference == 0;
}

[[nodiscard]] bool constant_time_equal(const Octets& lhs,
                                       const Octets& rhs) noexcept {
    if (lhs.size() != rhs.size()) {
        return false;
    }
    return constant_time_equal(lhs.data(), rhs.data(), lhs.size());
}

template <typename Container>
void append_bytes(Octets& out, const Container& container) {
    out.insert(out.end(), container.begin(), container.end());
}

void append_length_prefixed(Octets& out, std::string_view value) {
    if (value.size() > std::numeric_limits<std::uint16_t>::max()) {
        throw RadiusError("length-prefixed value is too large");
    }
    write_be16(out, static_cast<std::uint16_t>(value.size()));
    out.insert(out.end(), value.begin(), value.end());
}

// -------------------------------------------------------------------------
// SHA-256 (FIPS 180-4)
// -------------------------------------------------------------------------

class Sha256 {
public:
    Sha256() : state_{0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
                      0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U} {}

    void update(const std::uint8_t* data, std::size_t size) {
        while (size > 0) {
            const std::size_t take = std::min<std::size_t>(64 - buffer_size_, size);
            std::memcpy(buffer_.data() + buffer_size_, data, take);
            buffer_size_ += take;
            data += take;
            size -= take;

            if (buffer_size_ == 64) {
                transform(buffer_.data());
                bit_length_ += 512;
                buffer_size_ = 0;
            }
        }
    }

    void update(std::string_view value) {
        update(reinterpret_cast<const std::uint8_t*>(value.data()), value.size());
    }

    [[nodiscard]] std::array<std::uint8_t, 32> finish() {
        const std::uint64_t total_bits =
            bit_length_ + static_cast<std::uint64_t>(buffer_size_) * 8U;

        buffer_[buffer_size_++] = 0x80;
        if (buffer_size_ > 56) {
            while (buffer_size_ < 64) {
                buffer_[buffer_size_++] = 0;
            }
            transform(buffer_.data());
            buffer_size_ = 0;
        }
        while (buffer_size_ < 56) {
            buffer_[buffer_size_++] = 0;
        }
        for (std::size_t i = 0; i < 8; ++i) {
            const std::size_t shift = (7U - i) * 8U;
            buffer_[56 + i] =
                static_cast<std::uint8_t>((total_bits >> shift) & 0xffU);
        }
        transform(buffer_.data());

        std::array<std::uint8_t, 32> digest{};
        for (std::size_t i = 0; i < 8; ++i) {
            digest[i * 4] = static_cast<std::uint8_t>((state_[i] >> 24U) & 0xffU);
            digest[i * 4 + 1] =
                static_cast<std::uint8_t>((state_[i] >> 16U) & 0xffU);
            digest[i * 4 + 2] =
                static_cast<std::uint8_t>((state_[i] >> 8U) & 0xffU);
            digest[i * 4 + 3] =
                static_cast<std::uint8_t>(state_[i] & 0xffU);
        }
        return digest;
    }

private:
    static std::uint32_t rotate_right(std::uint32_t value, std::uint32_t bits) {
        return (value >> bits) | (value << (32U - bits));
    }

    static std::uint32_t choose(std::uint32_t x, std::uint32_t y,
                                std::uint32_t z) {
        return (x & y) ^ (~x & z);
    }

    static std::uint32_t majority(std::uint32_t x, std::uint32_t y,
                                  std::uint32_t z) {
        return (x & y) ^ (x & z) ^ (y & z);
    }

    static std::uint32_t sigma0(std::uint32_t x) {
        return rotate_right(x, 2) ^ rotate_right(x, 13) ^ rotate_right(x, 22);
    }

    static std::uint32_t sigma1(std::uint32_t x) {
        return rotate_right(x, 6) ^ rotate_right(x, 11) ^ rotate_right(x, 25);
    }

    static std::uint32_t gamma0(std::uint32_t x) {
        return rotate_right(x, 7) ^ rotate_right(x, 18) ^ (x >> 3U);
    }

    static std::uint32_t gamma1(std::uint32_t x) {
        return rotate_right(x, 17) ^ rotate_right(x, 19) ^ (x >> 10U);
    }

    void transform(const std::uint8_t* chunk) {
        static constexpr std::array<std::uint32_t, 64> k{
            0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U,
            0x3956c25bU, 0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U,
            0xd807aa98U, 0x12835b01U, 0x243185beU, 0x550c7dc3U,
            0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U, 0xc19bf174U,
            0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
            0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU,
            0x983e5152U, 0xa831c66dU, 0xb00327c8U, 0xbf597fc7U,
            0xc6e00bf3U, 0xd5a79147U, 0x06ca6351U, 0x14292967U,
            0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU, 0x53380d13U,
            0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
            0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U,
            0xd192e819U, 0xd6990624U, 0xf40e3585U, 0x106aa070U,
            0x19a4c116U, 0x1e376c08U, 0x2748774cU, 0x34b0bcb5U,
            0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU, 0x682e6ff3U,
            0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
            0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U};

        std::array<std::uint32_t, 64> words{};
        for (std::size_t i = 0; i < 16; ++i) {
            words[i] = read_be32(chunk + i * 4);
        }
        for (std::size_t i = 16; i < 64; ++i) {
            words[i] = gamma1(words[i - 2]) + words[i - 7] +
                       gamma0(words[i - 15]) + words[i - 16];
        }

        std::uint32_t a = state_[0];
        std::uint32_t b = state_[1];
        std::uint32_t c = state_[2];
        std::uint32_t d = state_[3];
        std::uint32_t e = state_[4];
        std::uint32_t f = state_[5];
        std::uint32_t g = state_[6];
        std::uint32_t h = state_[7];

        for (std::size_t i = 0; i < 64; ++i) {
            const std::uint32_t t1 =
                h + sigma1(e) + choose(e, f, g) + k[i] + words[i];
            const std::uint32_t t2 = sigma0(a) + majority(a, b, c);
            h = g;
            g = f;
            f = e;
            e = d + t1;
            d = c;
            c = b;
            b = a;
            a = t1 + t2;
        }

        state_[0] += a;
        state_[1] += b;
        state_[2] += c;
        state_[3] += d;
        state_[4] += e;
        state_[5] += f;
        state_[6] += g;
        state_[7] += h;
    }

    std::array<std::uint32_t, 8> state_;
    std::array<std::uint8_t, 64> buffer_{};
    std::size_t buffer_size_{0};
    std::uint64_t bit_length_{0};
};

[[nodiscard]] std::array<std::uint8_t, 32> digest_with_domain(
    std::string_view domain, const Octets& payload) {
    Sha256 hash;
    hash.update(domain);
    if (!payload.empty()) {
        hash.update(payload.data(), payload.size());
    }
    return hash.finish();
}

// -------------------------------------------------------------------------
// MD5 (RFC 1321), used only for the RADIUS wire authenticators.
// -------------------------------------------------------------------------

class Md5 {
public:
    Md5() : state_{0x67452301U, 0xefcdab89U, 0x98badcfeU, 0x10325476U} {}

    void update(const std::uint8_t* data, std::size_t size) {
        while (size > 0) {
            const std::size_t take = std::min<std::size_t>(64 - buffer_size_, size);
            std::memcpy(buffer_.data() + buffer_size_, data, take);
            buffer_size_ += take;
            data += take;
            size -= take;
            if (buffer_size_ == 64) {
                transform(buffer_.data());
                bit_length_ += 512;
                buffer_size_ = 0;
            }
        }
    }

    [[nodiscard]] std::array<std::uint8_t, 16> finish() {
        const std::uint64_t total_bits =
            bit_length_ + static_cast<std::uint64_t>(buffer_size_) * 8U;

        buffer_[buffer_size_++] = 0x80;
        if (buffer_size_ > 56) {
            while (buffer_size_ < 64) {
                buffer_[buffer_size_++] = 0;
            }
            transform(buffer_.data());
            buffer_size_ = 0;
        }
        while (buffer_size_ < 56) {
            buffer_[buffer_size_++] = 0;
        }
        for (std::size_t i = 0; i < 8; ++i) {
            buffer_[56 + i] =
                static_cast<std::uint8_t>((total_bits >> (i * 8U)) & 0xffU);
        }
        transform(buffer_.data());

        std::array<std::uint8_t, 16> digest{};
        for (std::size_t i = 0; i < 4; ++i) {
            digest[i * 4] = static_cast<std::uint8_t>(state_[i] & 0xffU);
            digest[i * 4 + 1] =
                static_cast<std::uint8_t>((state_[i] >> 8U) & 0xffU);
            digest[i * 4 + 2] =
                static_cast<std::uint8_t>((state_[i] >> 16U) & 0xffU);
            digest[i * 4 + 3] =
                static_cast<std::uint8_t>((state_[i] >> 24U) & 0xffU);
        }
        return digest;
    }

private:
    static std::uint32_t shift(std::uint32_t value, std::uint32_t amount) {
        return (value << amount) | (value >> (32U - amount));
    }

    void transform(const std::uint8_t* chunk) {
        static constexpr std::array<std::uint32_t, 64> k{
            0xd76aa478U, 0xe8c7b756U, 0x242070dbU, 0xc1bdceeeU,
            0xf57c0fafU, 0x4787c62aU, 0xa8304613U, 0xfd469501U,
            0x698098d8U, 0x8b44f7afU, 0xffff5bb1U, 0x895cd7beU,
            0x6b901122U, 0xfd987193U, 0xa679438eU, 0x49b40821U,
            0xf61e2562U, 0xc040b340U, 0x265e5a51U, 0xe9b6c7aaU,
            0xd62f105dU, 0x02441453U, 0xd8a1e681U, 0xe7d3fbc8U,
            0x21e1cde6U, 0xc33707d6U, 0xf4d50d87U, 0x455a14edU,
            0xa9e3e905U, 0xfcefa3f8U, 0x676f02d9U, 0x8d2a4c8aU,
            0xfffa3942U, 0x8771f681U, 0x6d9d6122U, 0xfde5380cU,
            0xa4beea44U, 0x4bdecfa9U, 0xf6bb4b60U, 0xbebfbc70U,
            0x289b7ec6U, 0xeaa127faU, 0xd4ef3085U, 0x04881d05U,
            0xd9d4d039U, 0xe6db99e5U, 0x1fa27cf8U, 0xc4ac5665U,
            0xf4292244U, 0x432aff97U, 0xab9423a7U, 0xfc93a039U,
            0x655b59c3U, 0x8f0ccc92U, 0xffeff47dU, 0x85845dd1U,
            0x6fa87e4fU, 0xfe2ce6e0U, 0xa3014314U, 0x4e0811a1U,
            0xf7537e82U, 0xbd3af235U, 0x2ad7d2bbU, 0xeb86d391U};
        static constexpr std::array<std::uint32_t, 64> s{
            7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22,
            5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20,
            4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23,
            6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21};

        std::array<std::uint32_t, 16> words{};
        for (std::size_t i = 0; i < 16; ++i) {
            words[i] = read_le32(chunk + i * 4);
        }

        std::uint32_t a = state_[0];
        std::uint32_t b = state_[1];
        std::uint32_t c = state_[2];
        std::uint32_t d = state_[3];

        for (std::size_t i = 0; i < 64; ++i) {
            std::uint32_t function = 0;
            std::size_t word_index = 0;
            if (i < 16) {
                function = (b & c) | (~b & d);
                word_index = i;
            } else if (i < 32) {
                function = (d & b) | (~d & c);
                word_index = (5 * i + 1) % 16;
            } else if (i < 48) {
                function = b ^ c ^ d;
                word_index = (3 * i + 5) % 16;
            } else {
                function = c ^ (b | ~d);
                word_index = (7 * i) % 16;
            }
            const std::uint32_t next =
                b + shift(a + function + words[word_index] + k[i], s[i]);
            a = d;
            d = c;
            c = b;
            b = next;
        }

        state_[0] += a;
        state_[1] += b;
        state_[2] += c;
        state_[3] += d;
    }

    std::array<std::uint32_t, 4> state_;
    std::array<std::uint8_t, 64> buffer_{};
    std::size_t buffer_size_{0};
    std::uint64_t bit_length_{0};
};

[[nodiscard]] std::array<std::uint8_t, 32> hmac_sha256(const Octets& key,
                                                       std::string_view message) {
    Octets normalized = key;
    if (normalized.size() > 64) {
        Octets input = key;
        Sha256 hash;
        hash.update(input.data(), input.size());
        const auto digest = hash.finish();
        normalized.assign(digest.begin(), digest.end());
    }
    normalized.resize(64, 0);

    Octets inner(64);
    Octets outer(64);
    for (std::size_t i = 0; i < 64; ++i) {
        inner[i] = static_cast<std::uint8_t>(normalized[i] ^ 0x36U);
        outer[i] = static_cast<std::uint8_t>(normalized[i] ^ 0x5cU);
    }
    inner.insert(inner.end(), message.begin(), message.end());
    Sha256 inner_hash;
    inner_hash.update(inner.data(), inner.size());
    const auto inner_digest = inner_hash.finish();
    outer.insert(outer.end(), inner_digest.begin(), inner_digest.end());
    Sha256 outer_hash;
    outer_hash.update(outer.data(), outer.size());
    return outer_hash.finish();
}

[[nodiscard]] std::array<std::uint8_t, 16> md5_of(const Octets& input) {
    Md5 hash;
    if (!input.empty()) {
        hash.update(input.data(), input.size());
    }
    return hash.finish();
}

[[nodiscard]] std::array<std::uint8_t, 16> hmac_md5(const Octets& key,
                                                     const Octets& message) {
    Octets normalized = key;
    if (normalized.size() > 64) {
        const auto digest = md5_of(key);
        normalized.assign(digest.begin(), digest.end());
    }
    normalized.resize(64, 0);

    Octets inner(64);
    Octets outer(64);
    for (std::size_t i = 0; i < 64; ++i) {
        inner[i] = static_cast<std::uint8_t>(normalized[i] ^ 0x36U);
        outer[i] = static_cast<std::uint8_t>(normalized[i] ^ 0x5cU);
    }
    inner.insert(inner.end(), message.begin(), message.end());
    const auto inner_digest = md5_of(inner);
    outer.insert(outer.end(), inner_digest.begin(), inner_digest.end());
    return md5_of(outer);
}

// -------------------------------------------------------------------------
// Wire helpers
// -------------------------------------------------------------------------

[[nodiscard]] Octets serialize_with_authenticator(const Message& message,
                                                  const Octets& authenticator,
                                                  const AttributeRules& rules) {
    validate_message(message, rules);
    if (!authenticator.empty() && authenticator.size() != 16) {
        throw ParseError("RADIUS authenticator must be 16 octets");
    }

    Octets wire;
    wire.reserve(kHeaderSize + message.attributes.size() * 2);
    wire.push_back(message.code);
    wire.push_back(message.identifier);

    std::size_t total = kHeaderSize;
    for (const auto& attribute : message.attributes) {
        total += 2 + attribute.value.size();
    }
    if (total > kMaxPacketSize || total > std::numeric_limits<std::uint16_t>::max()) {
        throw ParseError("RADIUS packet exceeds the 4096-octet limit");
    }
    write_be16(wire, static_cast<std::uint16_t>(total));

    if (authenticator.empty()) {
        wire.insert(wire.end(), 16, 0);
    } else {
        wire.insert(wire.end(), authenticator.begin(), authenticator.end());
    }

    for (const auto& attribute : message.attributes) {
        wire.push_back(attribute.type);
        wire.push_back(static_cast<std::uint8_t>(attribute.value.size() + 2));
        wire.insert(wire.end(), attribute.value.begin(), attribute.value.end());
    }
    return wire;
}

[[nodiscard]] Octets state_hmac_key(const Octets& secret) {
    const auto tag = hmac_sha256(secret, "netsec/radius/proxy-state-key/v1");
    return Octets(tag.begin(), tag.end());
}

}  // namespace

AttributeRules AttributeRules::secure_defaults() {
    return AttributeRules{};
}

Octets secure_random(std::size_t size) {
    Octets output(size);
    if (size == 0) {
        return output;
    }
#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || \
    defined(__NetBSD__)
    arc4random_buf(output.data(), output.size());
#elif defined(__linux__)
    std::size_t offset = 0;
    while (offset < output.size()) {
        const auto result = ::getrandom(output.data() + offset,
                                       output.size() - offset, 0);
        if (result < 0) {
            throw RadiusError("operating system random source failed");
        }
        offset += static_cast<std::size_t>(result);
    }
#else
    std::random_device device;
    for (auto& byte : output) {
        byte = static_cast<std::uint8_t>(device() & 0xffU);
    }
#endif
    return output;
}

std::optional<VendorAttribute> decode_vendor_specific(
    const Attribute& attribute) {
    if (attribute.type != kVendorSpecificAttribute) {
        return std::nullopt;
    }
    if (attribute.value.size() < 6 || attribute.value.size() > kMaxAttributeValue) {
        return std::nullopt;
    }
    const std::uint32_t vendor_id = read_be32(attribute.value.data());
    const std::uint8_t vendor_type = attribute.value[4];
    const std::uint8_t vendor_length = attribute.value[5];
    if (vendor_length < 2 ||
        static_cast<std::size_t>(vendor_length) != attribute.value.size() - 4U) {
        return std::nullopt;
    }
    Octets value(attribute.value.begin() + 6, attribute.value.end());
    return VendorAttribute{vendor_id, vendor_type, std::move(value)};
}

Attribute encode_vendor_specific(const VendorAttribute& attribute) {
    if (attribute.value.size() > 247) {
        throw ParseError("vendor-specific value is too long for one attribute");
    }
    Attribute result;
    result.type = kVendorSpecificAttribute;
    write_be32(result.value, attribute.vendor_id);
    result.value.push_back(attribute.type);
    result.value.push_back(static_cast<std::uint8_t>(attribute.value.size() + 2));
    result.value.insert(result.value.end(), attribute.value.begin(),
                        attribute.value.end());
    return result;
}

namespace {

void check_value_size(std::size_t size, std::size_t minimum,
                      std::size_t maximum, std::uint8_t type) {
    if (size < minimum || size > maximum) {
        std::ostringstream message;
        message << "attribute type " << static_cast<unsigned>(type)
                << " has invalid value length " << size;
        throw ParseError(message.str());
    }
}

void check_exact_size(std::size_t size, std::size_t expected,
                      std::uint8_t type) {
    if (size != expected) {
        std::ostringstream message;
        message << "attribute type " << static_cast<unsigned>(type)
                << " requires exactly " << expected << " octets, got " << size;
        throw ParseError(message.str());
    }
}

}  // namespace

void validate_attribute(const Attribute& attribute,
                        const AttributeRules& rules) {
    if (attribute.type == 0) {
        throw ParseError("RADIUS attribute type 0 is reserved");
    }
    if (attribute.value.size() > kMaxAttributeValue) {
        throw ParseError("RADIUS attribute value exceeds 253 octets");
    }

    if (attribute.type == kVendorSpecificAttribute) {
        if (!decode_vendor_specific(attribute).has_value()) {
            throw ParseError("malformed vendor-specific attribute");
        }
        return;
    }

    if (!rules.strict_standard_lengths) {
        return;
    }

    switch (attribute.type) {
        case 1:  // User-Name
        case 11: // Filter-ID
        case 18: // Reply-Message
        case 19: // Callback-Number
        case 20: // Callback-ID
        case 24: // State
        case 25: // Class
        case 30: // Called-Station-Id
        case 31: // Calling-Station-Id
        case 32: // NAS-Identifier
        case 33: // Proxy-State
        case 34: // Login-LAT-Service
        case 35: // Login-LAT-Node
        case 36: // Login-LAT-Group
        case 39: // Framed-AppleTalk-Zone
        case 44: // Acct-Session-Id
        case 50: // Acct-Multi-Session-Id
        case 60: // CHAP-Challenge
        case 63: // Login-LAT-Port
        case 79: // EAP-Message
        case 126: // Operator-Name
            check_value_size(attribute.value.size(), 1, kMaxAttributeValue,
                            attribute.type);
            break;
        case 2:  // User-Password
            check_exact_size(attribute.value.size(), 16, attribute.type);
            break;
        case 3:  // CHAP-Password
            check_exact_size(attribute.value.size(), 17, attribute.type);
            break;
        case 4:  // NAS-IP-Address
        case 5:  // NAS-Port
        case 6:  // Service-Type
        case 7:  // Framed-Protocol
        case 8:  // Framed-IP-Address
        case 9:  // Framed-IP-Netmask
        case 10: // Framed-Routing
        case 13: // Framed-Compression
        case 14: // Login-IP-Host
        case 15: // Login-Service
        case 16: // Login-TCP-Port
        case 27: // Session-Timeout
        case 28: // Idle-Timeout
        case 29: // Termination-Action
        case 37: // Framed-AppleTalk-Link
        case 38: // Framed-AppleTalk-Network
        case 40: // Acct-Status-Type
        case 41: // Acct-Delay-Time
        case 42: // Acct-Input-Octets
        case 43: // Acct-Output-Octets
        case 45: // Acct-Authentic
        case 46: // Acct-Session-Time
        case 47: // Acct-Input-Packets
        case 48: // Acct-Output-Packets
        case 49: // Acct-Terminate-Cause
        case 51: // Acct-Link-Count
        case 52: // Acct-Input-Gigawords
        case 53: // Acct-Output-Gigawords
        case 55: // Event-Timestamp
        case 61: // NAS-Port-Type
        case 62: // Port-Limit
            check_exact_size(attribute.value.size(), 4, attribute.type);
            break;
        case 12: // Framed-MTU
            check_exact_size(attribute.value.size(), 2, attribute.type);
            break;
        case 80: // Message-Authenticator
            check_exact_size(attribute.value.size(), 16, attribute.type);
            break;
        default:
            check_value_size(attribute.value.size(), 0, kMaxAttributeValue,
                            attribute.type);
            break;
    }
}

void validate_message(const Message& message, const AttributeRules& rules) {
    if (message.code == 0) {
        throw ParseError("RADIUS code 0 is invalid");
    }
    if (!message.authenticator.empty() && message.authenticator.size() != 16) {
        throw ParseError("RADIUS authenticator must be 16 octets");
    }

    std::size_t total = kHeaderSize;
    std::set<std::uint8_t> standard_seen;
    std::set<std::pair<std::uint32_t, std::uint8_t>> vendor_seen;

    for (const auto& attribute : message.attributes) {
        validate_attribute(attribute, rules);
        total += 2 + attribute.value.size();

        if (rules.reject_duplicate_single_value) {
            if (attribute.type != kVendorSpecificAttribute &&
                rules.repeatable_standard_types.count(attribute.type) == 0) {
                if (!standard_seen.insert(attribute.type).second) {
                    std::ostringstream text;
                    text << "duplicate single-value attribute type "
                         << static_cast<unsigned>(attribute.type);
                    throw ParseError(text.str());
                }
            }
            if (attribute.type == kVendorSpecificAttribute) {
                const auto vendor = decode_vendor_specific(attribute);
                if (vendor.has_value() &&
                    rules.single_value_vendor_attributes.count(
                        {vendor->vendor_id, vendor->type}) != 0) {
                    if (!vendor_seen
                             .insert({vendor->vendor_id, vendor->type})
                             .second) {
                        throw ParseError("duplicate single-value vendor attribute");
                    }
                }
            }
        }
    }

    if (total > kMaxPacketSize) {
        throw ParseError("RADIUS packet exceeds the 4096-octet limit");
    }
}

Message Message::parse(const Octets& wire, const ParseOptions& options) {
    if (wire.size() < kHeaderSize) {
        throw ParseError("RADIUS packet is shorter than the 20-octet header");
    }
    const std::uint16_t declared_length = read_be16(wire.data() + 2);
    if (declared_length < kHeaderSize ||
        declared_length > kMaxPacketSize) {
        throw ParseError("RADIUS packet has an invalid length");
    }
    if (options.require_exact_packet_length &&
        static_cast<std::size_t>(declared_length) != wire.size()) {
        throw ParseError("RADIUS declared length does not match the packet");
    }
    if (static_cast<std::size_t>(declared_length) > wire.size()) {
        throw ParseError("RADIUS packet is shorter than its declared length");
    }

    Message message;
    message.code = wire[0];
    message.identifier = wire[1];
    message.authenticator.assign(wire.begin() + 4, wire.begin() + 20);

    std::size_t position = kHeaderSize;
    while (position < static_cast<std::size_t>(declared_length)) {
        if (position + 2 > static_cast<std::size_t>(declared_length)) {
            throw ParseError("truncated RADIUS attribute header");
        }
        const std::uint8_t type = wire[position];
        const std::uint8_t length = wire[position + 1];
        if (length < 2) {
            throw ParseError("RADIUS attribute length is shorter than 2");
        }
        const std::size_t end = position + length;
        if (end > static_cast<std::size_t>(declared_length)) {
            throw ParseError("RADIUS attribute overruns the packet");
        }
        Attribute attribute;
        attribute.type = type;
        attribute.value.assign(wire.begin() +
                                   static_cast<std::ptrdiff_t>(position + 2),
                               wire.begin() + static_cast<std::ptrdiff_t>(end));
        validate_attribute(attribute, options.rules);
        message.attributes.push_back(std::move(attribute));
        position = end;
    }
    if (position != static_cast<std::size_t>(declared_length)) {
        throw ParseError("RADIUS attributes do not end at the packet boundary");
    }

    validate_message(message, options.rules);
    return message;
}

Octets Message::serialize() const {
    return serialize(AttributeRules::secure_defaults());
}

Octets Message::serialize(const AttributeRules& rules) const {
    return serialize_with_authenticator(*this, authenticator, rules);
}

std::vector<Attribute> Message::attributes_of_type(std::uint8_t type) const {
    std::vector<Attribute> result;
    for (const auto& attribute : attributes) {
        if (attribute.type == type) {
            result.push_back(attribute);
        }
    }
    return result;
}

std::size_t Message::count_of_type(std::uint8_t type) const {
    return static_cast<std::size_t>(
        std::count_if(attributes.begin(), attributes.end(),
                      [type](const Attribute& attribute) {
                          return attribute.type == type;
                      }));
}

bool is_operator_attribute(const Attribute& attribute) {
    if (attribute.type == kOperatorNameAttribute) {
        return true;
    }
    if (attribute.type != kVendorSpecificAttribute) {
        return false;
    }
    const auto vendor = decode_vendor_specific(attribute);
    if (!vendor.has_value()) {
        return false;
    }
    // 3GPP, Wi-Fi Alliance, and Aruba operator/roaming dictionaries.
    return vendor->vendor_id == 10415 || vendor->vendor_id == 14122 ||
           vendor->vendor_id == 14823;
}

std::vector<Attribute> operator_attributes(
    const std::vector<Attribute>& attributes) {
    std::vector<Attribute> result;
    for (const auto& attribute : attributes) {
        if (is_operator_attribute(attribute)) {
            result.push_back(attribute);
        }
    }
    return result;
}

void KeyRing::install(std::string peer, std::uint64_t generation,
                      Octets secret, bool active) {
    if (peer.empty()) {
        throw TrustError("shared key peer identity must not be empty");
    }
    if (generation == 0) {
        throw TrustError("shared key generation 0 is reserved");
    }
    if (secret.empty()) {
        throw TrustError("shared key must not be empty");
    }
    SharedKey key{std::move(peer), generation, std::move(secret), active};
    auto& peer_keys = keys_[key.peer];
    peer_keys[generation] = std::move(key);
}

void KeyRing::activate(std::string_view peer, std::uint64_t generation) {
    auto peer_it = keys_.find(std::string(peer));
    if (peer_it == keys_.end()) {
        throw TrustError("unknown shared key peer");
    }
    auto key_it = peer_it->second.find(generation);
    if (key_it == peer_it->second.end()) {
        throw TrustError("unknown shared key generation");
    }
    key_it->second.active = true;
}

void KeyRing::retire(std::string_view peer, std::uint64_t generation) {
    auto peer_it = keys_.find(std::string(peer));
    if (peer_it == keys_.end()) {
        throw TrustError("unknown shared key peer");
    }
    auto key_it = peer_it->second.find(generation);
    if (key_it == peer_it->second.end()) {
        throw TrustError("unknown shared key generation");
    }
    key_it->second.active = false;
}

const SharedKey* KeyRing::find(std::string_view peer,
                               std::uint64_t generation) const {
    const auto peer_it = keys_.find(std::string(peer));
    if (peer_it == keys_.end()) {
        return nullptr;
    }
    const auto key_it = peer_it->second.find(generation);
    if (key_it == peer_it->second.end()) {
        return nullptr;
    }
    return &key_it->second;
}

const SharedKey& KeyRing::require_active(std::string_view peer,
                                         std::uint64_t generation) const {
    const SharedKey* key = find(peer, generation);
    if (key == nullptr) {
        throw TrustError("unknown shared key generation");
    }
    if (!key->active) {
        throw TrustError("shared key generation is retired");
    }
    return *key;
}

bool KeyRing::contains(std::string_view peer,
                       std::uint64_t generation) const {
    return find(peer, generation) != nullptr;
}

std::array<std::uint8_t, 16> identity_hash(std::string_view identity) {
    Octets payload;
    write_be16(payload, static_cast<std::uint16_t>(identity.size()));
    payload.insert(payload.end(), identity.begin(), identity.end());
    const auto digest =
        digest_with_domain("netsec/radius/identity/v1", payload);
    std::array<std::uint8_t, 16> truncated{};
    std::copy_n(digest.begin(), truncated.size(), truncated.begin());
    return truncated;
}

std::array<std::uint8_t, 16> route_id_hash(const PolicyRoute& route) {
    Octets payload;
    append_length_prefixed(payload, route.source.nas_identifier);
    write_be64(payload, route.source.key_generation);
    append_length_prefixed(payload, route.source.roaming_peer);
    append_length_prefixed(payload, route.home_server);
    append_length_prefixed(payload, route.billing_group);
    write_be16(payload,
               static_cast<std::uint16_t>(route.operator_attributes.size()));
    for (const auto& attribute : route.operator_attributes) {
        validate_attribute(attribute);
        payload.push_back(attribute.type);
        write_be16(payload,
                   static_cast<std::uint16_t>(attribute.value.size()));
        payload.insert(payload.end(), attribute.value.begin(),
                       attribute.value.end());
    }
    const auto digest = digest_with_domain("netsec/radius/route/v1", payload);
    std::array<std::uint8_t, 16> truncated{};
    std::copy_n(digest.begin(), truncated.size(), truncated.begin());
    return truncated;
}

std::uint64_t proxy_state_signing_generation(const Octets& value) {
    if (value.size() != kProxyStateWireSize ||
        !constant_time_equal(value.data(), kProxyStateMagic.data(),
                             kProxyStateMagic.size()) ||
        value[4] != kProxyStateVersion) {
        throw ChainError("unrecognized Proxy-State record");
    }
    return read_be64(value.data() + kStateOffsetSigningGeneration);
}

Octets encode_proxy_state(const ProxyStateRecord& record,
                          const Octets& secret) {
    if (secret.empty()) {
        throw TrustError("cannot sign Proxy-State with an empty key");
    }
    if (record.signing_generation == 0 || record.origin_key_generation == 0) {
        throw TrustError("Proxy-State key generations must be non-zero");
    }
    Octets value;
    value.reserve(kProxyStateWireSize);
    value.insert(value.end(), kProxyStateMagic.begin(), kProxyStateMagic.end());
    value.push_back(kProxyStateVersion);
    value.push_back(0);  // flags
    write_be64(value, record.signing_generation);
    write_be64(value, record.origin_key_generation);
    append_bytes(value, record.nonce);
    append_bytes(value, record.request_digest);
    append_bytes(value, record.upstream_chain_digest);
    append_bytes(value, record.route_id);
    append_bytes(value, record.nas_identifier_hash);
    append_bytes(value, record.roaming_peer_hash);

    const Octets signing_key = state_hmac_key(secret);
    const auto tag = hmac_sha256(
        signing_key,
        std::string_view(reinterpret_cast<const char*>(value.data()),
                         value.size()));
    append_bytes(value, tag);
    if (value.size() != kProxyStateWireSize ||
        value.size() > kMaxProxyStateSize) {
        throw ChainError("Proxy-State record has an invalid size");
    }
    return value;
}

ProxyStateRecord decode_proxy_state(const Octets& value,
                                    const Octets& secret) {
    if (value.size() != kProxyStateWireSize ||
        !constant_time_equal(value.data(), kProxyStateMagic.data(),
                             kProxyStateMagic.size()) ||
        value[4] != kProxyStateVersion || value[5] != 0) {
        throw ChainError("unrecognized Proxy-State record");
    }
    if (secret.empty()) {
        throw TrustError("cannot verify Proxy-State with an empty key");
    }

    const Octets signing_key = state_hmac_key(secret);
    const auto expected = hmac_sha256(
        signing_key,
        std::string_view(reinterpret_cast<const char*>(value.data()),
                         kStateOffsetHmac));
    if (!constant_time_equal(expected.data(),
                             value.data() + kStateOffsetHmac,
                             expected.size())) {
        throw ChainError("Proxy-State authentication failed");
    }

    ProxyStateRecord record;
    record.signing_generation =
        read_be64(value.data() + kStateOffsetSigningGeneration);
    record.origin_key_generation =
        read_be64(value.data() + kStateOffsetOriginGeneration);
    std::copy_n(value.begin() +
                    static_cast<std::ptrdiff_t>(kStateOffsetNonce),
                record.nonce.size(), record.nonce.begin());
    std::copy_n(value.begin() +
                    static_cast<std::ptrdiff_t>(kStateOffsetRequestDigest),
                record.request_digest.size(), record.request_digest.begin());
    std::copy_n(value.begin() +
                    static_cast<std::ptrdiff_t>(kStateOffsetUpstreamDigest),
                record.upstream_chain_digest.size(),
                record.upstream_chain_digest.begin());
    std::copy_n(value.begin() +
                    static_cast<std::ptrdiff_t>(kStateOffsetRouteId),
                record.route_id.size(), record.route_id.begin());
    std::copy_n(value.begin() +
                    static_cast<std::ptrdiff_t>(kStateOffsetNasHash),
                record.nas_identifier_hash.size(),
                record.nas_identifier_hash.begin());
    std::copy_n(value.begin() +
                    static_cast<std::ptrdiff_t>(kStateOffsetPeerHash),
                record.roaming_peer_hash.size(),
                record.roaming_peer_hash.begin());
    if (record.signing_generation == 0 || record.origin_key_generation == 0) {
        throw ChainError("Proxy-State key generations must be non-zero");
    }
    return record;
}

std::array<std::uint8_t, 32> chain_digest(const std::vector<Octets>& values) {
    Octets payload;
    write_be32(payload, static_cast<std::uint32_t>(values.size()));
    for (const auto& value : values) {
        write_be16(payload, static_cast<std::uint16_t>(value.size()));
        payload.insert(payload.end(), value.begin(), value.end());
    }
    return digest_with_domain("netsec/radius/chain/v1", payload);
}

std::array<std::uint8_t, 16> request_digest(const Message& message,
                                            const AttributeRules& rules) {
    validate_message(message, rules);
    Octets payload;
    payload.push_back(message.code);
    payload.push_back(message.identifier);
    std::size_t count = 0;
    for (const auto& attribute : message.attributes) {
        if (attribute.type != kProxyStateAttribute &&
            attribute.type != kMessageAuthenticatorAttribute) {
            ++count;
        }
    }
    if (count > std::numeric_limits<std::uint32_t>::max()) {
        throw ParseError("too many attributes");
    }
    write_be32(payload, static_cast<std::uint32_t>(count));
    for (const auto& attribute : message.attributes) {
        if (attribute.type == kProxyStateAttribute ||
            attribute.type == kMessageAuthenticatorAttribute) {
            continue;
        }
        payload.push_back(attribute.type);
        write_be16(payload,
                   static_cast<std::uint16_t>(attribute.value.size()));
        payload.insert(payload.end(), attribute.value.begin(),
                       attribute.value.end());
    }
    const auto digest =
        digest_with_domain("netsec/radius/request/v1", payload);
    std::array<std::uint8_t, 16> truncated{};
    std::copy_n(digest.begin(), truncated.size(), truncated.begin());
    return truncated;
}

void compute_accounting_request_authenticator(
    Message& request, const Octets& secret, const AttributeRules& rules) {
    if (request.code != kAccountingRequest) {
        throw RadiusError("accounting authenticator requires Accounting-Request");
    }
    if (secret.empty()) {
        throw AuthenticationError("shared secret must not be empty");
    }
    const Octets wire = serialize_with_authenticator(request, Octets{}, rules);
    Octets material = wire;
    material.insert(material.end(), secret.begin(), secret.end());
    const auto digest = md5_of(material);
    request.authenticator.assign(digest.begin(), digest.end());
}

bool verify_accounting_request_authenticator(
    const Message& request, const Octets& secret, const AttributeRules& rules) {
    if (request.code != kAccountingRequest || request.authenticator.size() != 16) {
        return false;
    }
    if (secret.empty()) {
        return false;
    }
    const Octets wire = serialize_with_authenticator(request, Octets{}, rules);
    Octets material = wire;
    material.insert(material.end(), secret.begin(), secret.end());
    const auto digest = md5_of(material);
    return constant_time_equal(digest.data(), request.authenticator.data(),
                               digest.size());
}

void apply_message_authenticator(Message& message, const Octets& secret,
                                 const AttributeRules& rules) {
    if (secret.empty()) {
        throw AuthenticationError("shared secret must not be empty");
    }
    std::size_t index = message.attributes.size();
    std::size_t count = 0;
    for (std::size_t i = 0; i < message.attributes.size(); ++i) {
        if (message.attributes[i].type == kMessageAuthenticatorAttribute) {
            index = i;
            ++count;
        }
    }
    if (count > 1) {
        throw ParseError("duplicate Message-Authenticator");
    }
    if (count == 0) {
        Attribute attribute;
        attribute.type = kMessageAuthenticatorAttribute;
        attribute.value.assign(16, 0);
        message.attributes.push_back(std::move(attribute));
        index = message.attributes.size() - 1;
    } else if (message.attributes[index].value.size() != 16) {
        throw ParseError("Message-Authenticator must be 16 octets");
    }
    message.attributes[index].value.assign(16, 0);
    if (message.authenticator.empty()) {
        message.authenticator = secure_random(16);
    }
    if (message.authenticator.size() != 16) {
        throw ParseError("RADIUS authenticator must be 16 octets");
    }
    const Octets wire =
        serialize_with_authenticator(message, message.authenticator, rules);
    const auto digest = hmac_md5(secret, wire);
    message.attributes[index].value.assign(digest.begin(), digest.end());
}

bool verify_message_authenticator(const Message& message,
                                  const Octets& secret,
                                  const AttributeRules& rules) {
    if (secret.empty() || message.authenticator.size() != 16) {
        return false;
    }
    std::size_t index = message.attributes.size();
    std::size_t count = 0;
    for (std::size_t i = 0; i < message.attributes.size(); ++i) {
        if (message.attributes[i].type == kMessageAuthenticatorAttribute) {
            index = i;
            ++count;
        }
    }
    if (count != 1 || message.attributes[index].value.size() != 16) {
        return false;
    }
    Message working = message;
    working.attributes[index].value.assign(16, 0);
    const Octets wire =
        serialize_with_authenticator(working, working.authenticator, rules);
    const auto digest = hmac_md5(secret, wire);
    return constant_time_equal(digest.data(),
                               message.attributes[index].value.data(),
                               digest.size());
}

void seal_response_authenticator(Message& response,
                                 const Octets& request_authenticator,
                                 const Octets& secret,
                                 const AttributeRules& rules) {
    if (request_authenticator.size() != 16) {
        throw AuthenticationError("request authenticator must be 16 octets");
    }
    if (secret.empty()) {
        throw AuthenticationError("shared secret must not be empty");
    }
    const Octets wire =
        serialize_with_authenticator(response, request_authenticator, rules);
    Octets material = wire;
    material.insert(material.end(), secret.begin(), secret.end());
    const auto digest = md5_of(material);
    response.authenticator.assign(digest.begin(), digest.end());
}

bool verify_response_authenticator(const Message& response,
                                   const Octets& request_authenticator,
                                   const Octets& secret,
                                   const AttributeRules& rules) {
    if (request_authenticator.size() != 16 ||
        response.authenticator.size() != 16 || secret.empty()) {
        return false;
    }
    const Octets wire =
        serialize_with_authenticator(response, request_authenticator, rules);
    Octets material = wire;
    material.insert(material.end(), secret.begin(), secret.end());
    const auto digest = md5_of(material);
    return constant_time_equal(digest.data(), response.authenticator.data(),
                               digest.size());
}

namespace {

[[nodiscard]] std::vector<Octets> extract_proxy_state_chain(
    const Message& message) {
    std::vector<Octets> chain;
    chain.reserve(message.count_of_type(kProxyStateAttribute));
    for (const auto& attribute : message.attributes) {
        if (attribute.type == kProxyStateAttribute) {
            chain.push_back(attribute.value);
        }
    }
    return chain;
}

[[nodiscard]] Octets require_nas_identifier(const Message& message) {
    if (message.count_of_type(kNasIdentifierAttribute) != 1) {
        throw TrustError("request must contain exactly one NAS-Identifier");
    }
    for (const auto& attribute : message.attributes) {
        if (attribute.type == kNasIdentifierAttribute) {
            return attribute.value;
        }
    }
    throw TrustError("request must contain exactly one NAS-Identifier");
}

[[nodiscard]] std::array<std::uint8_t, 16> hash_attribute_value(
    const Octets& value) {
    return identity_hash(
        std::string_view(reinterpret_cast<const char*>(value.data()),
                         value.size()));
}

[[nodiscard]] bool hash_equal(const std::array<std::uint8_t, 16>& lhs,
                              const std::array<std::uint8_t, 16>& rhs) {
    return constant_time_equal(lhs.data(), rhs.data(), lhs.size());
}

[[nodiscard]] bool array_equal(const std::array<std::uint8_t, 32>& lhs,
                               const std::array<std::uint8_t, 32>& rhs) {
    return constant_time_equal(lhs.data(), rhs.data(), lhs.size());
}

[[nodiscard]] bool chain_equal(const std::vector<Octets>& lhs,
                               const std::vector<Octets>& rhs) {
    if (lhs.size() != rhs.size()) {
        return false;
    }
    for (std::size_t i = 0; i < lhs.size(); ++i) {
        if (!constant_time_equal(lhs[i], rhs[i])) {
            return false;
        }
    }
    return true;
}

[[nodiscard]] bool has_message_authenticator(const Message& message) {
    return message.count_of_type(kMessageAuthenticatorAttribute) != 0;
}

void verify_source_authenticator(const Message& message,
                                 const Octets& secret,
                                 bool transport_authenticated,
                                 const AttributeRules& rules) {
    if (message.code == kAccountingRequest) {
        if (!verify_accounting_request_authenticator(message, secret, rules)) {
            throw AuthenticationError("invalid Accounting-Request authenticator");
        }
        return;
    }
    if (has_message_authenticator(message)) {
        if (!verify_message_authenticator(message, secret, rules)) {
            throw AuthenticationError("invalid Message-Authenticator");
        }
        return;
    }
    if (!transport_authenticated) {
        throw AuthenticationError(
            "Access-Request without Message-Authenticator needs an "
            "authenticated transport binding");
    }
}

}  // namespace

ProxyEngine::ProxyEngine(std::string local_peer, KeyRing keys,
                         std::vector<SourceBinding> sources,
                         std::vector<PolicyRoute> routes,
                         AttributeRules rules)
    : local_peer_(std::move(local_peer)),
      keys_(std::move(keys)),
      sources_(std::move(sources)),
      routes_(std::move(routes)),
      rules_(std::move(rules)) {}

const SourceBinding* ProxyEngine::find_source(
    std::string_view transport_peer, IngressKind kind,
    std::string_view nas_identifier) const {
    const SourceBinding* result = nullptr;
    for (const auto& source : sources_) {
        if (source.transport_peer != transport_peer || source.kind != kind) {
            continue;
        }
        if (kind == IngressKind::Nas &&
            source.nas_identifier != nas_identifier) {
            continue;
        }
        if (result != nullptr) {
            throw TrustError("ambiguous source binding");
        }
        result = &source;
    }
    return result;
}

const PolicyRoute* ProxyEngine::find_route(
    const SourceIdentity& identity) const {
    const PolicyRoute* result = nullptr;
    for (const auto& route : routes_) {
        if (route.source == identity) {
            if (result != nullptr) {
                throw TrustError("ambiguous policy route for source identity");
            }
            result = &route;
        }
    }
    return result;
}

const PolicyRoute* ProxyEngine::find_route_by_hashes(
    const ProxyStateRecord& state) const {
    const PolicyRoute* result = nullptr;
    for (const auto& route : routes_) {
        if (route.source.key_generation != state.origin_key_generation) {
            continue;
        }
        if (!hash_equal(identity_hash(route.source.nas_identifier),
                        state.nas_identifier_hash) ||
            !hash_equal(identity_hash(route.source.roaming_peer),
                        state.roaming_peer_hash)) {
            continue;
        }
        if (result != nullptr) {
            throw TrustError("ambiguous policy route for Proxy-State identity");
        }
        result = &route;
    }
    return result;
}

const KeyRing& ProxyEngine::keys() const noexcept {
    return keys_;
}

const std::vector<PolicyRoute>& ProxyEngine::routes() const noexcept {
    return routes_;
}

ForwardedRequest ProxyEngine::forward(const Octets& request_wire,
                                      const IngressInfo& ingress) const {
    Message request = Message::parse(
        request_wire, ParseOptions{rules_, true});
    if (request.code != kAccessRequest &&
        request.code != kAccountingRequest) {
        throw RadiusError("only Access-Request and Accounting-Request can be forwarded");
    }

    const Octets nas_identifier = require_nas_identifier(request);
    const SourceBinding* proxy_binding =
        find_source(ingress.transport_peer, IngressKind::Proxy);
    const SourceBinding* nas_binding = find_source(
        ingress.transport_peer, IngressKind::Nas,
        std::string_view(reinterpret_cast<const char*>(nas_identifier.data()),
                         nas_identifier.size()));
    if (proxy_binding != nullptr && nas_binding != nullptr) {
        throw TrustError("source is configured as both NAS and proxy");
    }

    const std::vector<Attribute> original_attributes = request.attributes;
    const std::vector<Octets> existing_chain =
        extract_proxy_state_chain(request);

    SourceIdentity origin;
    const PolicyRoute* route = nullptr;
    std::string upstream_peer;
    std::uint64_t upstream_generation = 0;
    Octets upstream_authenticator = request.authenticator;
    std::size_t untrusted_states = 0;
    bool source_was_nas = false;

    if (proxy_binding != nullptr) {
        if (existing_chain.empty()) {
            throw TrustError("proxy source did not provide a Proxy-State chain");
        }
        const std::uint64_t signing_generation =
            proxy_state_signing_generation(existing_chain.back());
        if (signing_generation != proxy_binding->key_generation) {
            throw TrustError("upstream Proxy-State key generation mismatch");
        }
        const SharedKey& upstream_key =
            keys_.require_active(proxy_binding->transport_peer,
                                 signing_generation);
        const ProxyStateRecord upstream_state =
            decode_proxy_state(existing_chain.back(), upstream_key.secret);
        std::vector<Octets> prefix(
            existing_chain.begin(), existing_chain.end() - 1);
        if (!array_equal(chain_digest(prefix),
                         upstream_state.upstream_chain_digest)) {
            throw ChainError("upstream Proxy-State chain digest mismatch");
        }
        if (!hash_equal(hash_attribute_value(nas_identifier),
                        upstream_state.nas_identifier_hash)) {
            throw TrustError("NAS-Identifier does not match the trusted chain");
        }
        if (!hash_equal(request_digest(request, rules_),
                        upstream_state.request_digest)) {
            throw ChainError("request changed after Proxy-State was added");
        }
        // The signed Proxy-State already authenticates the forwarding hop.
        verify_source_authenticator(request, upstream_key.secret, true, rules_);
        route = find_route_by_hashes(upstream_state);
        if (route == nullptr) {
            throw TrustError("no policy route for the trusted roaming source");
        }
        origin = route->source;
        upstream_peer = proxy_binding->transport_peer;
        upstream_generation = signing_generation;
        untrusted_states = 0;
    } else if (nas_binding != nullptr) {
        const SharedKey& upstream_key = keys_.require_active(
            nas_binding->transport_peer, nas_binding->key_generation);
        verify_source_authenticator(request, upstream_key.secret,
                                    ingress.transport_authenticated, rules_);
        origin = SourceIdentity{nas_binding->nas_identifier,
                                nas_binding->key_generation,
                                nas_binding->roaming_peer};
        route = find_route(origin);
        if (route == nullptr) {
            throw TrustError("no policy route for the NAS source");
        }
        upstream_peer = nas_binding->transport_peer;
        upstream_generation = nas_binding->key_generation;
        source_was_nas = true;
        untrusted_states = existing_chain.size();
    } else {
        throw TrustError("unrecognized forwarding source");
    }

    if (route->signing_generation == 0) {
        throw TrustError("route has no downstream signing key generation");
    }
    const SharedKey& downstream_key =
        keys_.require_active(route->next_hop, route->signing_generation);

    ProxyStateRecord state;
    state.signing_generation = route->signing_generation;
    state.origin_key_generation = origin.key_generation;
    const Octets nonce = secure_random(16);
    std::copy(nonce.begin(), nonce.end(), state.nonce.begin());
    state.request_digest = request_digest(request, rules_);
    state.upstream_chain_digest = chain_digest(existing_chain);
    state.route_id = route_id_hash(*route);
    state.nas_identifier_hash = identity_hash(origin.nas_identifier);
    state.roaming_peer_hash = identity_hash(origin.roaming_peer);

    const Octets generated =
        encode_proxy_state(state, downstream_key.secret);
    request.attributes.push_back(
        Attribute{kProxyStateAttribute, generated});

    if (request.code == kAccountingRequest) {
        compute_accounting_request_authenticator(
            request, downstream_key.secret, rules_);
    } else if (has_message_authenticator(request)) {
        apply_message_authenticator(request, downstream_key.secret, rules_);
    }

    ForwardedRequest forwarded;
    forwarded.forwarded = request;
    forwarded.wire =
        serialize_with_authenticator(request, request.authenticator, rules_);
    forwarded.user_writable_attributes.clear();
    for (const auto& attribute : original_attributes) {
        if (attribute.type != kProxyStateAttribute) {
            forwarded.user_writable_attributes.push_back(attribute);
        }
    }
    forwarded.proxy_generated_attributes.push_back(
        Attribute{kProxyStateAttribute, generated});
    forwarded.proxy_state_chain = existing_chain;
    forwarded.proxy_state_chain.push_back(generated);
    forwarded.generated_proxy_state = generated;
    forwarded.state = state;
    forwarded.origin = origin;
    forwarded.route = *route;
    forwarded.upstream_peer = upstream_peer;
    forwarded.upstream_generation = upstream_generation;
    forwarded.upstream_request_authenticator = upstream_authenticator;
    forwarded.forwarded_request_authenticator = request.authenticator;
    forwarded.source_was_nas = source_was_nas;
    forwarded.untrusted_proxy_state_count = untrusted_states;
    return forwarded;
}

VerifiedResponse ProxyEngine::accept_response(
    const Octets& response_wire, const ForwardedRequest& request) const {
    Message response = Message::parse(
        response_wire, ParseOptions{rules_, true});

    const bool access_exchange = request.forwarded.code == kAccessRequest;
    if (access_exchange) {
        if (response.code != kAccessAccept &&
            response.code != kAccessReject &&
            response.code != kAccessChallenge) {
            throw ChainError("response code does not match the forwarded request");
        }
    } else if (response.code != kAccountingResponse) {
        throw ChainError("response code does not match the forwarded request");
    }

    const std::vector<Octets> chain = extract_proxy_state_chain(response);
    if (!chain_equal(chain, request.proxy_state_chain)) {
        throw ChainError("response Proxy-State chain does not match the request");
    }
    if (chain.empty() ||
        !constant_time_equal(chain.back(), request.generated_proxy_state)) {
        throw ChainError("response did not echo this proxy's Proxy-State");
    }

    const SharedKey& downstream_key = keys_.require_active(
        request.route.next_hop, request.route.signing_generation);
    const ProxyStateRecord state =
        decode_proxy_state(chain.back(), downstream_key.secret);
    if (!hash_equal(state.nonce, request.state.nonce) ||
        !hash_equal(state.route_id, request.state.route_id) ||
        state.signing_generation != request.state.signing_generation ||
        state.origin_key_generation != request.state.origin_key_generation) {
        throw ChainError("response Proxy-State record changed");
    }
    if (!verify_response_authenticator(
            response, request.forwarded_request_authenticator,
            downstream_key.secret, rules_)) {
        throw AuthenticationError("response authenticator is invalid");
    }

    Message upstream = response;
    if (request.source_was_nas) {
        upstream.attributes.erase(
            std::remove_if(upstream.attributes.begin(), upstream.attributes.end(),
                           [](const Attribute& attribute) {
                               return attribute.type == kProxyStateAttribute;
                           }),
            upstream.attributes.end());
    } else {
        auto it = std::find_if(
            upstream.attributes.rbegin(), upstream.attributes.rend(),
            [](const Attribute& attribute) {
                return attribute.type == kProxyStateAttribute;
            });
        if (it == upstream.attributes.rend()) {
            throw ChainError("response did not contain this proxy's Proxy-State");
        }
        upstream.attributes.erase(std::next(it).base());
    }

    const SharedKey& upstream_key = keys_.require_active(
        request.upstream_peer, request.upstream_generation);
    seal_response_authenticator(upstream,
                                request.upstream_request_authenticator,
                                upstream_key.secret, rules_);
    Octets upstream_wire =
        serialize_with_authenticator(upstream, upstream.authenticator, rules_);

    VerifiedResponse verified;
    verified.message = std::move(response);
    verified.upstream_wire = std::move(upstream_wire);
    verified.state = state;
    return verified;
}

HomeServer::HomeServer(std::string home_server, KeyRing keys,
                       std::vector<PolicyRoute> policies,
                       AttributeRules rules)
    : home_server_(std::move(home_server)),
      keys_(std::move(keys)),
      policies_(std::move(policies)),
      rules_(std::move(rules)) {}

const std::string& HomeServer::home_server() const noexcept {
    return home_server_;
}

const PolicyRoute* HomeServer::find_route(
    const ProxyStateRecord& state) const {
    const PolicyRoute* result = nullptr;
    for (const auto& route : policies_) {
        if (route.source.key_generation != state.origin_key_generation) {
            continue;
        }
        if (!hash_equal(identity_hash(route.source.nas_identifier),
                        state.nas_identifier_hash) ||
            !hash_equal(identity_hash(route.source.roaming_peer),
                        state.roaming_peer_hash)) {
            continue;
        }
        if (route.signing_generation != state.signing_generation ||
            (route_id_hash(route) != state.route_id) ||
            (!home_server_.empty() && route.home_server != home_server_)) {
            continue;
        }
        if (result != nullptr) {
            throw TrustError("ambiguous home policy route");
        }
        result = &route;
    }
    return result;
}

VerifiedRequest HomeServer::verify_request(
    const Octets& request_wire, const IngressInfo& ingress) const {
    const Message request = Message::parse(
        request_wire, ParseOptions{rules_, true});
    if (request.code != kAccessRequest &&
        request.code != kAccountingRequest) {
        throw RadiusError("home server only accepts authorization and accounting requests");
    }

    const std::vector<Octets> chain = extract_proxy_state_chain(request);
    if (chain.empty()) {
        throw TrustError("request has no trusted Proxy-State chain");
    }
    const std::uint64_t signing_generation =
        proxy_state_signing_generation(chain.back());
    const SharedKey& key = keys_.require_active(
        ingress.transport_peer, signing_generation);
    const ProxyStateRecord state =
        decode_proxy_state(chain.back(), key.secret);
    const std::vector<Octets> prefix(chain.begin(), chain.end() - 1);
    if (!array_equal(chain_digest(prefix), state.upstream_chain_digest)) {
        throw ChainError("Proxy-State chain digest mismatch");
    }
    if (!hash_equal(request_digest(request, rules_), state.request_digest)) {
        throw ChainError("request fields changed after Proxy-State was added");
    }
    const Octets nas_identifier = require_nas_identifier(request);
    if (!hash_equal(hash_attribute_value(nas_identifier),
                    state.nas_identifier_hash)) {
        throw TrustError("NAS-Identifier does not match the trusted chain");
    }

    // The Proxy-State HMAC already authenticates the forwarding hop.  When
    // present, the native RADIUS authenticators are checked as well.
    verify_source_authenticator(request, key.secret, true, rules_);

    const PolicyRoute* route = find_route(state);
    if (route == nullptr) {
        throw TrustError(
            "no home policy matches NAS-Identifier, key generation, and "
            "roaming peer from the trusted chain");
    }

    VerifiedRequest verified;
    verified.message = request;
    verified.state = state;
    verified.route = *route;
    return verified;
}

PolicyDecision HomeServer::decide(const VerifiedRequest& verified) const {
    PolicyDecision decision;
    decision.accepted = true;
    decision.reason = "trusted Proxy-State chain";
    decision.home_server = verified.route.home_server;
    decision.billing_group = verified.route.billing_group;
    decision.nas_identifier = verified.route.source.nas_identifier;
    decision.roaming_peer = verified.route.source.roaming_peer;
    decision.origin_key_generation =
        verified.route.source.key_generation;
    decision.signing_generation = verified.state.signing_generation;
    decision.trusted_operator_attributes =
        verified.route.operator_attributes;
    return decision;
}

PolicyDecision HomeServer::evaluate(const Octets& request,
                                    const IngressInfo& ingress) const {
    try {
        return decide(verify_request(request, ingress));
    } catch (const RadiusError& error) {
        PolicyDecision rejected;
        rejected.accepted = false;
        rejected.reason = error.what();
        return rejected;
    }
}

PolicyDecision HomeServer::authorize(const Octets& request,
                                     const IngressInfo& ingress) const {
    try {
        const Message message = Message::parse(
            request, ParseOptions{rules_, true});
        if (message.code != kAccessRequest) {
            PolicyDecision rejected;
            rejected.accepted = false;
            rejected.reason = "authorize() requires Access-Request";
            return rejected;
        }
    } catch (const RadiusError& error) {
        PolicyDecision rejected;
        rejected.accepted = false;
        rejected.reason = error.what();
        return rejected;
    }
    return evaluate(request, ingress);
}

PolicyDecision HomeServer::account(const Octets& request,
                                   const IngressInfo& ingress) const {
    try {
        const Message message = Message::parse(
            request, ParseOptions{rules_, true});
        if (message.code != kAccountingRequest) {
            PolicyDecision rejected;
            rejected.accepted = false;
            rejected.reason = "account() requires Accounting-Request";
            return rejected;
        }
    } catch (const RadiusError& error) {
        PolicyDecision rejected;
        rejected.accepted = false;
        rejected.reason = error.what();
        return rejected;
    }
    return evaluate(request, ingress);
}

Octets HomeServer::respond(const Octets& request_wire,
                           const IngressInfo& ingress,
                           std::uint8_t response_code,
                           const std::vector<Attribute>& attributes) const {
    const VerifiedRequest request = verify_request(request_wire, ingress);
    const bool access_exchange = request.message.code == kAccessRequest;
    if (access_exchange) {
        if (response_code != kAccessAccept &&
            response_code != kAccessReject &&
            response_code != kAccessChallenge) {
            throw RadiusError("invalid response code for Access-Request");
        }
    } else if (response_code != kAccountingResponse) {
        throw RadiusError("invalid response code for Accounting-Request");
    }

    for (const auto& attribute : attributes) {
        validate_attribute(attribute, rules_);
        if (attribute.type == kProxyStateAttribute ||
            attribute.type == kMessageAuthenticatorAttribute) {
            throw ParseError(
                "response attributes may not contain Proxy-State or "
                "Message-Authenticator");
        }
    }

    Message response;
    response.code = response_code;
    response.identifier = request.message.identifier;
    response.authenticator.assign(16, 0);
    response.attributes = attributes;
    for (const auto& attribute : request.message.attributes) {
        if (attribute.type == kProxyStateAttribute) {
            response.attributes.push_back(attribute);
        }
    }

    const SharedKey& key = keys_.require_active(
        ingress.transport_peer, request.state.signing_generation);
    seal_response_authenticator(response, request.message.authenticator,
                                key.secret, rules_);
    return serialize_with_authenticator(
        response, response.authenticator, rules_);
}

}  // namespace netsec::radius
