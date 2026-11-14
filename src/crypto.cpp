#include "netsec/crypto.hpp"

#include <cstring>

namespace netsec {

namespace {

constexpr std::uint32_t rotr(std::uint32_t value, std::uint32_t shift) noexcept {
    return (value >> shift) | (value << (32 - shift));
}

constexpr std::uint32_t kRoundConstants[64] = {
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4,
    0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe,
    0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f,
    0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
    0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc,
    0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
    0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116,
    0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7,
    0xc67178f2};

}  // namespace

Sha256::Sha256() noexcept {
    reset();
}

void Sha256::reset() noexcept {
    state_[0] = 0x6a09e667;
    state_[1] = 0xbb67ae85;
    state_[2] = 0x3c6ef372;
    state_[3] = 0xa54ff53a;
    state_[4] = 0x510e527f;
    state_[5] = 0x9b05688c;
    state_[6] = 0x1f83d9ab;
    state_[7] = 0x5be0cd19;
    bit_length_ = 0;
    buffer_length_ = 0;
    finished_ = false;
}

void Sha256::transform(const std::uint8_t block[kSha256BlockSize]) noexcept {
    std::uint32_t words[64];
    for (std::size_t i = 0; i < 16; ++i) {
        const std::size_t offset = i * 4;
        words[i] = (std::uint32_t{block[offset]} << 24) |
                   (std::uint32_t{block[offset + 1]} << 16) |
                   (std::uint32_t{block[offset + 2]} << 8) |
                   std::uint32_t{block[offset + 3]};
    }
    for (std::size_t i = 16; i < 64; ++i) {
        const std::uint32_t s0 = rotr(words[i - 15], 7) ^ rotr(words[i - 15], 18) ^
                                 (words[i - 15] >> 3);
        const std::uint32_t s1 = rotr(words[i - 2], 17) ^ rotr(words[i - 2], 19) ^
                                 (words[i - 2] >> 10);
        words[i] = words[i - 16] + s0 + words[i - 7] + s1;
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
        const std::uint32_t sigma1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
        const std::uint32_t choice = (e & f) ^ (~e & g);
        const std::uint32_t temp1 = h + sigma1 + choice + kRoundConstants[i] + words[i];
        const std::uint32_t sigma0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
        const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
        const std::uint32_t temp2 = sigma0 + majority;

        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
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

void Sha256::update(std::span<const std::uint8_t> data) noexcept {
    if (data.empty()) {
        return;
    }
    if (finished_) {
        reset();
    }
    bit_length_ += static_cast<std::uint64_t>(data.size()) * 8;

    std::size_t offset = 0;
    if (buffer_length_ > 0) {
        while (offset < data.size() && buffer_length_ < kSha256BlockSize) {
            buffer_[buffer_length_++] = data[offset++];
        }
        if (buffer_length_ == kSha256BlockSize) {
            transform(buffer_);
            buffer_length_ = 0;
        }
    }

    while (offset + kSha256BlockSize <= data.size()) {
        transform(data.data() + offset);
        offset += kSha256BlockSize;
    }

    while (offset < data.size()) {
        buffer_[buffer_length_++] = data[offset++];
    }
}

void Sha256::update(std::string_view data) noexcept {
    update(std::span<const std::uint8_t>(
        reinterpret_cast<const std::uint8_t*>(data.data()), data.size()));
}

Sha256Digest Sha256::finish() noexcept {
    if (!finished_) {
        const std::uint64_t length = bit_length_;

        buffer_[buffer_length_++] = 0x80;
        if (buffer_length_ > 56) {
            while (buffer_length_ < kSha256BlockSize) {
                buffer_[buffer_length_++] = 0;
            }
            transform(buffer_);
            buffer_length_ = 0;
        }
        while (buffer_length_ < 56) {
            buffer_[buffer_length_++] = 0;
        }
        for (int i = 7; i >= 0; --i) {
            buffer_[buffer_length_++] =
                static_cast<std::uint8_t>((length >> (static_cast<std::uint64_t>(i) * 8)) & 0xFF);
        }
        transform(buffer_);
        finished_ = true;
    }

    Sha256Digest digest{};
    for (std::size_t i = 0; i < 8; ++i) {
        digest[i * 4] = static_cast<std::uint8_t>(state_[i] >> 24);
        digest[i * 4 + 1] = static_cast<std::uint8_t>(state_[i] >> 16);
        digest[i * 4 + 2] = static_cast<std::uint8_t>(state_[i] >> 8);
        digest[i * 4 + 3] = static_cast<std::uint8_t>(state_[i]);
    }
    return digest;
}

Sha256Digest sha256(std::span<const std::uint8_t> data) noexcept {
    Sha256 hasher;
    hasher.update(data);
    return hasher.finish();
}

Sha256Digest sha256(std::string_view data) noexcept {
    Sha256 hasher;
    hasher.update(data);
    return hasher.finish();
}

Sha256Digest hmac_sha256(std::span<const std::uint8_t> key,
                         std::span<const std::uint8_t> message) noexcept {
    std::array<std::uint8_t, kSha256BlockSize> normalized_key{};
    if (key.size() > kSha256BlockSize) {
        const Sha256Digest key_digest = sha256(key);
        std::memcpy(normalized_key.data(), key_digest.data(), key_digest.size());
    } else {
        std::memcpy(normalized_key.data(), key.data(), key.size());
    }

    std::array<std::uint8_t, kSha256BlockSize> inner_pad{};
    std::array<std::uint8_t, kSha256BlockSize> outer_pad{};
    for (std::size_t i = 0; i < kSha256BlockSize; ++i) {
        inner_pad[i] = normalized_key[i] ^ 0x36;
        outer_pad[i] = normalized_key[i] ^ 0x5C;
    }

    Sha256 inner;
    inner.update(inner_pad);
    inner.update(message);
    const Sha256Digest inner_digest = inner.finish();

    Sha256 outer;
    outer.update(outer_pad);
    outer.update(std::span<const std::uint8_t>(inner_digest.data(), inner_digest.size()));
    return outer.finish();
}

bool constant_time_equal(std::span<const std::uint8_t> lhs,
                         std::span<const std::uint8_t> rhs) noexcept {
    if (lhs.size() != rhs.size()) {
        return false;
    }
    std::uint8_t difference = 0;
    for (std::size_t i = 0; i < lhs.size(); ++i) {
        difference |= static_cast<std::uint8_t>(lhs[i] ^ rhs[i]);
    }
    return difference == 0;
}

}  // namespace netsec
