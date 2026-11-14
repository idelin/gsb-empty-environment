#include "netsec_crypto.hpp"

#include <algorithm>
#include <array>
#include <cstring>
#include <fstream>
#include <limits>
#include <stdexcept>

#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__)
#include <cstdlib>
#endif

namespace netsec::detail {
namespace {

constexpr std::array<std::uint32_t, 64> kSha256Constants = {
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU, 0x59f111f1U,
    0x923f82a4U, 0xab1c5ed5U, 0xd807aa98U, 0x12835b01U, 0x243185beU, 0x550c7dc3U,
    0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U, 0xc19bf174U, 0xe49b69c1U, 0xefbe4786U,
    0x0fc19dc6U, 0x240ca1ccU, 0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU,
    0x983e5152U, 0xa831c66dU, 0xb00327c8U, 0xbf597fc7U, 0xc6e00bf3U, 0xd5a79147U,
    0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU, 0x53380d13U,
    0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U, 0xa2bfe8a1U, 0xa81a664bU,
    0xc24b8b70U, 0xc76c51a3U, 0xd192e819U, 0xd6990624U, 0xf40e3585U, 0x106aa070U,
    0x19a4c116U, 0x1e376c08U, 0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU,
    0x5b9cca4fU, 0x682e6ff3U, 0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U};

constexpr std::uint32_t rotr32(std::uint32_t value, unsigned int count) noexcept {
    return (value >> count) | (value << (32U - count));
}

constexpr std::uint32_t rotl32(std::uint32_t value, unsigned int count) noexcept {
    return (value << count) | (value >> (32U - count));
}

std::uint32_t load_be32(const std::uint8_t* input) noexcept {
    return (static_cast<std::uint32_t>(input[0]) << 24U) |
           (static_cast<std::uint32_t>(input[1]) << 16U) |
           (static_cast<std::uint32_t>(input[2]) << 8U) |
           static_cast<std::uint32_t>(input[3]);
}

std::uint32_t load_le32(const std::uint8_t* input) noexcept {
    return static_cast<std::uint32_t>(input[0]) |
           (static_cast<std::uint32_t>(input[1]) << 8U) |
           (static_cast<std::uint32_t>(input[2]) << 16U) |
           (static_cast<std::uint32_t>(input[3]) << 24U);
}

void store_be32(std::uint8_t* output, std::uint32_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

void store_le32(std::uint8_t* output, std::uint32_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value);
    output[1] = static_cast<std::uint8_t>(value >> 8U);
    output[2] = static_cast<std::uint8_t>(value >> 16U);
    output[3] = static_cast<std::uint8_t>(value >> 24U);
}

void store_le64(std::uint8_t* output, std::uint64_t value) noexcept {
    for (unsigned int index = 0; index < 8; ++index) {
        output[index] = static_cast<std::uint8_t>(value >> (8U * index));
    }
}

void sha256_compress(std::array<std::uint32_t, 8>& state, const std::uint8_t* block) noexcept {
    std::array<std::uint32_t, 64> schedule{};
    for (std::size_t index = 0; index < 16; ++index) {
        schedule[index] = load_be32(block + index * 4);
    }
    for (std::size_t index = 16; index < 64; ++index) {
        const std::uint32_t s0 = rotr32(schedule[index - 15], 7) ^
                                 rotr32(schedule[index - 15], 18) ^
                                 (schedule[index - 15] >> 3U);
        const std::uint32_t s1 = rotr32(schedule[index - 2], 17) ^
                                 rotr32(schedule[index - 2], 19) ^
                                 (schedule[index - 2] >> 10U);
        schedule[index] = schedule[index - 16] + s0 + schedule[index - 7] + s1;
    }

    std::uint32_t a = state[0];
    std::uint32_t b = state[1];
    std::uint32_t c = state[2];
    std::uint32_t d = state[3];
    std::uint32_t e = state[4];
    std::uint32_t f = state[5];
    std::uint32_t g = state[6];
    std::uint32_t h = state[7];

    for (std::size_t index = 0; index < 64; ++index) {
        const std::uint32_t sum1 = rotr32(e, 6) ^ rotr32(e, 11) ^ rotr32(e, 25);
        const std::uint32_t choice = (e & f) ^ ((~e) & g);
        const std::uint32_t temp1 = h + sum1 + choice + kSha256Constants[index] + schedule[index];
        const std::uint32_t sum0 = rotr32(a, 2) ^ rotr32(a, 13) ^ rotr32(a, 22);
        const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
        const std::uint32_t temp2 = sum0 + majority;

        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
    state[5] += f;
    state[6] += g;
    state[7] += h;
}

struct Poly1305State {
    std::uint32_t r[5]{};
    std::uint32_t h[5]{};
    std::uint32_t pad[4]{};
};

void poly1305_blocks(Poly1305State& state, const std::uint8_t* message, std::size_t length,
                     std::uint32_t high_bit) noexcept {
    constexpr std::uint32_t kMask = 0x3ffffffU;
    const std::uint32_t r0 = state.r[0];
    const std::uint32_t r1 = state.r[1];
    const std::uint32_t r2 = state.r[2];
    const std::uint32_t r3 = state.r[3];
    const std::uint32_t r4 = state.r[4];
    const std::uint64_t s1 = static_cast<std::uint64_t>(r1) * 5U;
    const std::uint64_t s2 = static_cast<std::uint64_t>(r2) * 5U;
    const std::uint64_t s3 = static_cast<std::uint64_t>(r3) * 5U;
    const std::uint64_t s4 = static_cast<std::uint64_t>(r4) * 5U;

    std::uint32_t h0 = state.h[0];
    std::uint32_t h1 = state.h[1];
    std::uint32_t h2 = state.h[2];
    std::uint32_t h3 = state.h[3];
    std::uint32_t h4 = state.h[4];

    while (length >= 16) {
        h0 += load_le32(message + 0) & kMask;
        h1 += (load_le32(message + 3) >> 2U) & kMask;
        h2 += (load_le32(message + 6) >> 4U) & kMask;
        h3 += (load_le32(message + 9) >> 6U) & kMask;
        h4 += (load_le32(message + 12) >> 8U) | high_bit;

        std::uint64_t d0 = static_cast<std::uint64_t>(h0) * r0 +
                           static_cast<std::uint64_t>(h1) * s4 +
                           static_cast<std::uint64_t>(h2) * s3 +
                           static_cast<std::uint64_t>(h3) * s2 +
                           static_cast<std::uint64_t>(h4) * s1;
        std::uint64_t d1 = static_cast<std::uint64_t>(h0) * r1 +
                           static_cast<std::uint64_t>(h1) * r0 +
                           static_cast<std::uint64_t>(h2) * s4 +
                           static_cast<std::uint64_t>(h3) * s3 +
                           static_cast<std::uint64_t>(h4) * s2;
        std::uint64_t d2 = static_cast<std::uint64_t>(h0) * r2 +
                           static_cast<std::uint64_t>(h1) * r1 +
                           static_cast<std::uint64_t>(h2) * r0 +
                           static_cast<std::uint64_t>(h3) * s4 +
                           static_cast<std::uint64_t>(h4) * s3;
        std::uint64_t d3 = static_cast<std::uint64_t>(h0) * r3 +
                           static_cast<std::uint64_t>(h1) * r2 +
                           static_cast<std::uint64_t>(h2) * r1 +
                           static_cast<std::uint64_t>(h3) * r0 +
                           static_cast<std::uint64_t>(h4) * s4;
        std::uint64_t d4 = static_cast<std::uint64_t>(h0) * r4 +
                           static_cast<std::uint64_t>(h1) * r3 +
                           static_cast<std::uint64_t>(h2) * r2 +
                           static_cast<std::uint64_t>(h3) * r1 +
                           static_cast<std::uint64_t>(h4) * r0;

        std::uint64_t carry = d0 >> 26U;
        h0 = static_cast<std::uint32_t>(d0) & kMask;
        d1 += carry;
        carry = d1 >> 26U;
        h1 = static_cast<std::uint32_t>(d1) & kMask;
        d2 += carry;
        carry = d2 >> 26U;
        h2 = static_cast<std::uint32_t>(d2) & kMask;
        d3 += carry;
        carry = d3 >> 26U;
        h3 = static_cast<std::uint32_t>(d3) & kMask;
        d4 += carry;
        carry = d4 >> 26U;
        h4 = static_cast<std::uint32_t>(d4) & kMask;
        h0 += static_cast<std::uint32_t>(carry * 5U);
        carry = h0 >> 26U;
        h0 &= kMask;
        h1 += static_cast<std::uint32_t>(carry);

        message += 16;
        length -= 16;
    }

    state.h[0] = h0;
    state.h[1] = h1;
    state.h[2] = h2;
    state.h[3] = h3;
    state.h[4] = h4;
}

void chacha20_block(std::span<const std::uint8_t, 32> key,
                    std::span<const std::uint8_t, 12> nonce,
                    std::uint32_t counter,
                    std::array<std::uint8_t, 64>& output) noexcept {
    std::array<std::uint32_t, 16> state{};
    state[0] = 0x61707865U;
    state[1] = 0x3320646eU;
    state[2] = 0x79622d32U;
    state[3] = 0x6b206574U;
    for (std::size_t index = 0; index < 8; ++index) {
        state[4 + index] = load_le32(key.data() + index * 4);
    }
    state[12] = counter;
    state[13] = load_le32(nonce.data() + 0);
    state[14] = load_le32(nonce.data() + 4);
    state[15] = load_le32(nonce.data() + 8);

    std::array<std::uint32_t, 16> working = state;

    const auto quarter_round = [](std::uint32_t& a, std::uint32_t& b, std::uint32_t& c,
                                  std::uint32_t& d) {
        a += b;
        d ^= a;
        d = rotl32(d, 16);
        c += d;
        b ^= c;
        b = rotl32(b, 12);
        a += b;
        d ^= a;
        d = rotl32(d, 8);
        c += d;
        b ^= c;
        b = rotl32(b, 7);
    };

    for (unsigned int round = 0; round < 10; ++round) {
        quarter_round(working[0], working[4], working[8], working[12]);
        quarter_round(working[1], working[5], working[9], working[13]);
        quarter_round(working[2], working[6], working[10], working[14]);
        quarter_round(working[3], working[7], working[11], working[15]);

        quarter_round(working[0], working[5], working[10], working[15]);
        quarter_round(working[1], working[6], working[11], working[12]);
        quarter_round(working[2], working[7], working[8], working[13]);
        quarter_round(working[3], working[4], working[9], working[14]);
    }

    for (std::size_t index = 0; index < 16; ++index) {
        store_le32(output.data() + index * 4, working[index] + state[index]);
    }
}

std::array<std::uint8_t, 16> aead_tag(std::span<const std::uint8_t, 32> poly_key,
                                      std::span<const std::uint8_t> associated_data,
                                      std::span<const std::uint8_t> ciphertext) {
    Bytes mac_input;
    mac_input.reserve(associated_data.size() + ciphertext.size() + 32);
    mac_input.insert(mac_input.end(), associated_data.begin(), associated_data.end());
    while ((mac_input.size() % 16U) != 0U) {
        mac_input.push_back(0);
    }
    mac_input.insert(mac_input.end(), ciphertext.begin(), ciphertext.end());
    while ((mac_input.size() % 16U) != 0U) {
        mac_input.push_back(0);
    }

    std::array<std::uint8_t, 16> lengths{};
    store_le64(lengths.data(), static_cast<std::uint64_t>(associated_data.size()));
    store_le64(lengths.data() + 8, static_cast<std::uint64_t>(ciphertext.size()));
    mac_input.insert(mac_input.end(), lengths.begin(), lengths.end());
    return poly1305_mac(poly_key, mac_input);
}

}  // namespace

std::array<std::uint8_t, 32> sha256(std::span<const std::uint8_t> message) {
    std::array<std::uint32_t, 8> state = {0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
                                          0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U};

    Bytes padded(message.begin(), message.end());
    const std::uint64_t bit_length = static_cast<std::uint64_t>(message.size()) * 8U;
    padded.push_back(0x80U);
    while ((padded.size() % 64U) != 56U) {
        padded.push_back(0);
    }
    std::array<std::uint8_t, 8> encoded_length{};
    for (unsigned int index = 0; index < 8; ++index) {
        encoded_length[index] = static_cast<std::uint8_t>(bit_length >> (56U - 8U * index));
    }
    padded.insert(padded.end(), encoded_length.begin(), encoded_length.end());

    for (std::size_t offset = 0; offset < padded.size(); offset += 64) {
        sha256_compress(state, padded.data() + offset);
    }

    std::array<std::uint8_t, 32> digest{};
    for (std::size_t index = 0; index < state.size(); ++index) {
        store_be32(digest.data() + index * 4, state[index]);
    }
    return digest;
}

std::array<std::uint8_t, 32> hmac_sha256(std::span<const std::uint8_t> key,
                                         std::span<const std::uint8_t> message) {
    constexpr std::size_t kBlockSize = 64;
    std::array<std::uint8_t, kBlockSize> normalized_key{};

    if (key.size() > kBlockSize) {
        const auto hashed = sha256(key);
        std::copy(hashed.begin(), hashed.end(), normalized_key.begin());
    } else {
        std::copy(key.begin(), key.end(), normalized_key.begin());
    }

    Bytes inner;
    inner.reserve(kBlockSize + message.size());
    for (std::uint8_t byte : normalized_key) {
        inner.push_back(static_cast<std::uint8_t>(byte ^ 0x36U));
    }
    inner.insert(inner.end(), message.begin(), message.end());
    const auto inner_digest = sha256(inner);

    Bytes outer;
    outer.reserve(kBlockSize + inner_digest.size());
    for (std::uint8_t byte : normalized_key) {
        outer.push_back(static_cast<std::uint8_t>(byte ^ 0x5cU));
    }
    outer.insert(outer.end(), inner_digest.begin(), inner_digest.end());
    return sha256(outer);
}

std::array<std::uint8_t, 32> hkdf_sha256(std::span<const std::uint8_t> input_key_material,
                                         std::span<const std::uint8_t> salt,
                                         std::span<const std::uint8_t> info) {
    std::array<std::uint8_t, 32> zero_salt{};
    const auto pseudo_random_key =
        hmac_sha256(salt.empty() ? std::span<const std::uint8_t>(zero_salt)
                                 : salt,
                    input_key_material);

    Bytes expand_input(info.begin(), info.end());
    expand_input.push_back(0x01U);
    return hmac_sha256(pseudo_random_key, expand_input);
}

void chacha20_xor(std::span<const std::uint8_t, 32> key,
                  std::span<const std::uint8_t, 12> nonce,
                  std::uint32_t initial_counter,
                  std::span<const std::uint8_t> input,
                  std::span<std::uint8_t> output) {
    if (output.size() < input.size()) {
        throw std::invalid_argument("chacha20 output buffer is too small");
    }

    std::array<std::uint8_t, 64> key_stream{};
    std::uint32_t counter = initial_counter;
    std::size_t offset = 0;
    while (offset < input.size()) {
        chacha20_block(key, nonce, counter, key_stream);
        const std::size_t block_size = std::min<std::size_t>(64, input.size() - offset);
        for (std::size_t index = 0; index < block_size; ++index) {
            output[offset + index] =
                static_cast<std::uint8_t>(input[offset + index] ^ key_stream[index]);
        }
        offset += block_size;
        ++counter;
    }
}

std::array<std::uint8_t, 16> poly1305_mac(std::span<const std::uint8_t, 32> key,
                                          std::span<const std::uint8_t> message) {
    constexpr std::uint32_t kMask = 0x3ffffffU;
    constexpr std::uint32_t kHighBit = 1U << 24U;

    Poly1305State state{};
    state.r[0] = (load_le32(key.data() + 0) >> 0U) & 0x3ffffffU;
    state.r[1] = (load_le32(key.data() + 3) >> 2U) & 0x3ffff03U;
    state.r[2] = (load_le32(key.data() + 6) >> 4U) & 0x3ffc0ffU;
    state.r[3] = (load_le32(key.data() + 9) >> 6U) & 0x3f03fffU;
    state.r[4] = (load_le32(key.data() + 12) >> 8U) & 0x00fffffU;
    for (std::size_t index = 0; index < 4; ++index) {
        state.pad[index] = load_le32(key.data() + 16 + index * 4);
    }

    const std::size_t full_blocks = message.size() / 16U;
    poly1305_blocks(state, message.data(), full_blocks * 16U, kHighBit);

    const std::size_t remainder = message.size() % 16U;
    if (remainder != 0U) {
        std::array<std::uint8_t, 16> block{};
        std::copy_n(message.data() + full_blocks * 16U, remainder, block.begin());
        block[remainder] = 0x01U;
        poly1305_blocks(state, block.data(), 16U, 0U);
    }

    std::uint32_t h0 = state.h[0];
    std::uint32_t h1 = state.h[1];
    std::uint32_t h2 = state.h[2];
    std::uint32_t h3 = state.h[3];
    std::uint32_t h4 = state.h[4];

    std::uint32_t carry = h1 >> 26U;
    h1 &= kMask;
    h2 += carry;
    carry = h2 >> 26U;
    h2 &= kMask;
    h3 += carry;
    carry = h3 >> 26U;
    h3 &= kMask;
    h4 += carry;
    carry = h4 >> 26U;
    h4 &= kMask;
    h0 += carry * 5U;
    carry = h0 >> 26U;
    h0 &= kMask;
    h1 += carry;

    std::uint32_t g0 = h0 + 5U;
    carry = g0 >> 26U;
    g0 &= kMask;
    std::uint32_t g1 = h1 + carry;
    carry = g1 >> 26U;
    g1 &= kMask;
    std::uint32_t g2 = h2 + carry;
    carry = g2 >> 26U;
    g2 &= kMask;
    std::uint32_t g3 = h3 + carry;
    carry = g3 >> 26U;
    g3 &= kMask;
    std::uint32_t g4 = h4 + carry - (1U << 26U);

    const std::uint32_t select = (g4 >> 31U) - 1U;
    g0 &= select;
    g1 &= select;
    g2 &= select;
    g3 &= select;
    g4 &= select;
    const std::uint32_t keep = ~select;
    h0 = (h0 & keep) | g0;
    h1 = (h1 & keep) | g1;
    h2 = (h2 & keep) | g2;
    h3 = (h3 & keep) | g3;
    h4 = (h4 & keep) | g4;

    h0 = (h0 | (h1 << 26U)) & 0xffffffffU;
    h1 = ((h1 >> 6U) | (h2 << 20U)) & 0xffffffffU;
    h2 = ((h2 >> 12U) | (h3 << 14U)) & 0xffffffffU;
    h3 = ((h3 >> 18U) | (h4 << 8U)) & 0xffffffffU;

    std::uint64_t accumulator = static_cast<std::uint64_t>(h0) + state.pad[0];
    std::array<std::uint8_t, 16> tag{};
    store_le32(tag.data() + 0, static_cast<std::uint32_t>(accumulator));
    accumulator = (accumulator >> 32U) + h1 + state.pad[1];
    store_le32(tag.data() + 4, static_cast<std::uint32_t>(accumulator));
    accumulator = (accumulator >> 32U) + h2 + state.pad[2];
    store_le32(tag.data() + 8, static_cast<std::uint32_t>(accumulator));
    accumulator = (accumulator >> 32U) + h3 + state.pad[3];
    store_le32(tag.data() + 12, static_cast<std::uint32_t>(accumulator));
    return tag;
}

AeadResult chacha20_poly1305_seal(std::span<const std::uint8_t, 32> key,
                                  std::span<const std::uint8_t, 12> nonce,
                                  std::span<const std::uint8_t> associated_data,
                                  std::span<const std::uint8_t> plaintext) {
    std::array<std::uint8_t, 64> first_block{};
    chacha20_block(key, nonce, 0U, first_block);
    std::array<std::uint8_t, 32> poly_key{};
    std::copy_n(first_block.begin(), poly_key.size(), poly_key.begin());

    AeadResult result;
    result.ciphertext.resize(plaintext.size());
    if (!plaintext.empty()) {
        chacha20_xor(key, nonce, 1U, plaintext, result.ciphertext);
    }
    result.tag = aead_tag(poly_key, associated_data, result.ciphertext);
    return result;
}

bool chacha20_poly1305_open(std::span<const std::uint8_t, 32> key,
                            std::span<const std::uint8_t, 12> nonce,
                            std::span<const std::uint8_t> associated_data,
                            std::span<const std::uint8_t> ciphertext,
                            std::span<const std::uint8_t, 16> tag,
                            Bytes& plaintext) {
    std::array<std::uint8_t, 64> first_block{};
    chacha20_block(key, nonce, 0U, first_block);
    std::array<std::uint8_t, 32> poly_key{};
    std::copy_n(first_block.begin(), poly_key.size(), poly_key.begin());

    const auto expected_tag = aead_tag(poly_key, associated_data, ciphertext);
    if (!constant_time_equal(expected_tag, tag)) {
        plaintext.clear();
        return false;
    }

    plaintext.resize(ciphertext.size());
    if (!ciphertext.empty()) {
        chacha20_xor(key, nonce, 1U, ciphertext, plaintext);
    }
    return true;
}

Bytes random_bytes(std::size_t count) {
    Bytes output(count);
    if (count == 0) {
        return output;
    }

#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__)
    arc4random_buf(output.data(), output.size());
#else
    std::ifstream random_source("/dev/urandom", std::ios::binary);
    if (!random_source ||
        !random_source.read(reinterpret_cast<char*>(output.data()),
                            static_cast<std::streamsize>(output.size()))) {
        throw std::runtime_error("unable to obtain operating-system randomness");
    }
#endif
    return output;
}

bool constant_time_equal(std::span<const std::uint8_t> lhs,
                         std::span<const std::uint8_t> rhs) noexcept {
    if (lhs.size() != rhs.size()) {
        return false;
    }
    std::uint8_t difference = 0;
    for (std::size_t index = 0; index < lhs.size(); ++index) {
        difference |= static_cast<std::uint8_t>(lhs[index] ^ rhs[index]);
    }
    return difference == 0;
}

}  // namespace netsec::detail
