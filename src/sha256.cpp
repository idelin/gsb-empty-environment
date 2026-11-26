#include "machosec/machosec.h"

#include <cstring>

namespace machosec {
namespace {

constexpr std::array<uint32_t, 64> kRoundConstants = {
    0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u, 0x3956c25bu,
    0x59f111f1u, 0x923f82a4u, 0xab1c5ed5u, 0xd807aa98u, 0x12835b01u,
    0x243185beu, 0x550c7dc3u, 0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u,
    0xc19bf174u, 0xe49b69c1u, 0xefbe4786u, 0x0fc19dc6u, 0x240ca1ccu,
    0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau, 0x983e5152u,
    0xa831c66du, 0xb00327c8u, 0xbf597fc7u, 0xc6e00bf3u, 0xd5a79147u,
    0x06ca6351u, 0x14292967u, 0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu,
    0x53380d13u, 0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u,
    0xa2bfe8a1u, 0xa81a664bu, 0xc24b8b70u, 0xc76c51a3u, 0xd192e819u,
    0xd6990624u, 0xf40e3585u, 0x106aa070u, 0x19a4c116u, 0x1e376c08u,
    0x2748774cu, 0x34b0bcb5u, 0x391c0cb3u, 0x4ed8aa4au, 0x5b9cca4fu,
    0x682e6ff3u, 0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u,
    0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u};

uint32_t rotate_right(uint32_t value, uint32_t bits) {
    return (value >> bits) | (value << (32u - bits));
}

void compress_block(std::array<uint32_t, 8>& state,
                    const std::array<uint8_t, 64>& block) {
    std::array<uint32_t, 64> words{};
    for (size_t j = 0; j < 16; ++j) {
        const size_t p = j * 4;
        words[j] = (uint32_t(block[p]) << 24u) |
                   (uint32_t(block[p + 1]) << 16u) |
                   (uint32_t(block[p + 2]) << 8u) |
                   uint32_t(block[p + 3]);
    }
    for (size_t j = 16; j < 64; ++j) {
        const uint32_t w15 = words[j - 15];
        const uint32_t w2 = words[j - 2];
        const uint32_t s0 = rotate_right(w15, 7) ^
                            rotate_right(w15, 18) ^ (w15 >> 3u);
        const uint32_t s1 = rotate_right(w2, 17) ^
                            rotate_right(w2, 19) ^ (w2 >> 10u);
        words[j] = words[j - 16] + s0 + words[j - 7] + s1;
    }

    std::array<uint32_t, 8> working = state;
    for (size_t j = 0; j < 64; ++j) {
        const uint32_t s1 = rotate_right(working[4], 6) ^
                            rotate_right(working[4], 11) ^
                            rotate_right(working[4], 25);
        const uint32_t choice =
            (working[4] & working[5]) ^ (~working[4] & working[6]);
        const uint32_t temp1 =
            working[7] + s1 + choice + kRoundConstants[j] + words[j];
        const uint32_t s0 = rotate_right(working[0], 2) ^
                            rotate_right(working[0], 13) ^
                            rotate_right(working[0], 22);
        const uint32_t majority = (working[0] & working[1]) ^
                                  (working[0] & working[2]) ^
                                  (working[1] & working[2]);
        const uint32_t temp2 = s0 + majority;
        working[7] = working[6];
        working[6] = working[5];
        working[5] = working[4];
        working[4] = working[3] + temp1;
        working[3] = working[2];
        working[2] = working[1];
        working[1] = working[0];
        working[0] = temp1 + temp2;
    }

    for (size_t j = 0; j < 8; ++j) {
        state[j] += working[j];
    }
}

}  // namespace

Sha256::Sha256()
    : state_{0x6a09e667u, 0xbb67ae85u, 0x3c6ef372u, 0xa54ff53au,
             0x510e527fu, 0x9b05688cu, 0x1f83d9abu, 0x5be0cd19u} {}

void Sha256::update(ByteView data) {
    for (size_t i = 0; i < data.size; ++i) {
        buffer_[buffer_size_++] = data.data[i];
        bit_length_ += 8u;
        if (buffer_size_ == buffer_.size()) {
            compress_block(state_, buffer_);
            buffer_size_ = 0;
        }
    }
}

void Sha256::finish(Sha256Digest& digest) {
    const uint64_t length_bits = bit_length_;
    buffer_[buffer_size_++] = 0x80u;

    if (buffer_size_ > 56) {
        while (buffer_size_ < buffer_.size()) {
            buffer_[buffer_size_++] = 0;
        }
        compress_block(state_, buffer_);
        buffer_size_ = 0;
    }

    while (buffer_size_ < 56) {
        buffer_[buffer_size_++] = 0;
    }

    for (int shift = 56; shift >= 0; shift -= 8) {
        buffer_[buffer_size_++] =
            static_cast<uint8_t>((length_bits >> shift) & 0xffu);
    }
    compress_block(state_, buffer_);
    buffer_size_ = 0;

    for (size_t i = 0; i < 8; ++i) {
        digest[i * 4] = static_cast<uint8_t>(state_[i] >> 24u);
        digest[i * 4 + 1] = static_cast<uint8_t>(state_[i] >> 16u);
        digest[i * 4 + 2] = static_cast<uint8_t>(state_[i] >> 8u);
        digest[i * 4 + 3] = static_cast<uint8_t>(state_[i]);
    }
}

Sha256Digest Sha256::hash(ByteView data) {
    Sha256 hasher;
    hasher.update(data);
    Sha256Digest digest{};
    hasher.finish(digest);
    return digest;
}

std::string to_hex(const Sha256Digest& digest) {
    static constexpr char kHex[] = "0123456789abcdef";
    std::string result(digest.size() * 2, '0');
    for (size_t i = 0; i < digest.size(); ++i) {
        result[i * 2] = kHex[digest[i] >> 4u];
        result[i * 2 + 1] = kHex[digest[i] & 0x0fu];
    }
    return result;
}

}  // namespace machosec
