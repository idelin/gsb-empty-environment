#include "netsec.hpp"

#include <algorithm>
#include <chrono>
#include <random>
#include <utility>

namespace netsec {
namespace {

using Clock = std::chrono::system_clock;

constexpr std::string_view kCertificateDomain = "netsec/eid/certificate/v1";
constexpr std::string_view kDownloadDomain = "netsec/eid/download/v1";
constexpr std::size_t kMaximumCertificateChain = 16;

void append_u64(Bytes& out, std::uint64_t value) {
    for (int shift = 56; shift >= 0; shift -= 8) {
        out.push_back(static_cast<Byte>((value >> shift) & 0xffU));
    }
}

void append_i64(Bytes& out, std::int64_t value) {
    append_u64(out, static_cast<std::uint64_t>(value));
}

void append_bytes(Bytes& out, std::span<const Byte> value) {
    append_u64(out, static_cast<std::uint64_t>(value.size()));
    out.insert(out.end(), value.begin(), value.end());
}

void append_string(Bytes& out, std::string_view value) {
    append_bytes(out, std::span<const Byte>(
                          reinterpret_cast<const Byte*>(value.data()), value.size()));
}

std::int64_t current_time_seconds() {
    return std::chrono::duration_cast<std::chrono::seconds>(
               Clock::now().time_since_epoch())
        .count();
}

void fill_random(std::span<Byte> output) {
    std::random_device random;
    for (Byte& value : output) {
        value = static_cast<Byte>(random() & 0xffU);
    }
}

std::string random_hex(std::size_t byte_count) {
    Bytes bytes(byte_count);
    fill_random(bytes);
    return to_hex(bytes);
}

bool constant_time_equal(std::span<const Byte> left,
                         std::span<const Byte> right) {
    if (left.size() != right.size()) {
        return false;
    }
    unsigned int difference = 0;
    for (std::size_t index = 0; index < left.size(); ++index) {
        difference |= static_cast<unsigned int>(left[index] ^ right[index]);
    }
    return difference == 0;
}

constexpr std::array<std::uint32_t, 64> kSha256Constants = {
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU,
    0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U, 0xd807aa98U, 0x12835b01U,
    0x243185beU, 0x550c7dc3U, 0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U,
    0xc19bf174U, 0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
    0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU, 0x983e5152U,
    0xa831c66dU, 0xb00327c8U, 0xbf597fc7U, 0xc6e00bf3U, 0xd5a79147U,
    0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU,
    0x53380d13U, 0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
    0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U, 0xd192e819U,
    0xd6990624U, 0xf40e3585U, 0x106aa070U, 0x19a4c116U, 0x1e376c08U,
    0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU,
    0x682e6ff3U, 0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U,
};

constexpr std::uint32_t rotate_right32(std::uint32_t value,
                                       std::uint32_t count) {
    return (value >> count) | (value << (32U - count));
}

std::array<Byte, 32> sha256_bytes(std::span<const Byte> input) {
    Bytes padded(input.begin(), input.end());
    const std::uint64_t bit_length =
        static_cast<std::uint64_t>(input.size()) * 8U;
    padded.push_back(0x80U);
    while ((padded.size() % 64U) != 56U) {
        padded.push_back(0U);
    }
    for (int shift = 56; shift >= 0; shift -= 8) {
        padded.push_back(static_cast<Byte>((bit_length >> shift) & 0xffU));
    }

    std::array<std::uint32_t, 8> state = {
        0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
        0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U,
    };

    for (std::size_t offset = 0; offset < padded.size(); offset += 64U) {
        std::array<std::uint32_t, 64> words{};
        for (std::size_t index = 0; index < 16U; ++index) {
            const std::size_t position = offset + index * 4U;
            words[index] =
                (static_cast<std::uint32_t>(padded[position]) << 24U) |
                (static_cast<std::uint32_t>(padded[position + 1U]) << 16U) |
                (static_cast<std::uint32_t>(padded[position + 2U]) << 8U) |
                static_cast<std::uint32_t>(padded[position + 3U]);
        }
        for (std::size_t index = 16U; index < 64U; ++index) {
            const std::uint32_t s0 =
                rotate_right32(words[index - 15U], 7U) ^
                rotate_right32(words[index - 15U], 18U) ^
                (words[index - 15U] >> 3U);
            const std::uint32_t s1 =
                rotate_right32(words[index - 2U], 17U) ^
                rotate_right32(words[index - 2U], 19U) ^
                (words[index - 2U] >> 10U);
            words[index] =
                words[index - 16U] + s0 + words[index - 7U] + s1;
        }

        std::uint32_t a = state[0];
        std::uint32_t b = state[1];
        std::uint32_t c = state[2];
        std::uint32_t d = state[3];
        std::uint32_t e = state[4];
        std::uint32_t f = state[5];
        std::uint32_t g = state[6];
        std::uint32_t h = state[7];

        for (std::size_t index = 0; index < 64U; ++index) {
            const std::uint32_t sum1 =
                rotate_right32(e, 6U) ^ rotate_right32(e, 11U) ^
                rotate_right32(e, 25U);
            const std::uint32_t choice = (e & f) ^ ((~e) & g);
            const std::uint32_t temporary1 =
                h + sum1 + choice + kSha256Constants[index] + words[index];
            const std::uint32_t sum0 =
                rotate_right32(a, 2U) ^ rotate_right32(a, 13U) ^
                rotate_right32(a, 22U);
            const std::uint32_t majority =
                (a & b) ^ (a & c) ^ (b & c);
            const std::uint32_t temporary2 = sum0 + majority;

            h = g;
            g = f;
            f = e;
            e = d + temporary1;
            d = c;
            c = b;
            b = a;
            a = temporary1 + temporary2;
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

    std::array<Byte, 32> digest{};
    for (std::size_t index = 0; index < state.size(); ++index) {
        digest[index * 4U] =
            static_cast<Byte>((state[index] >> 24U) & 0xffU);
        digest[index * 4U + 1U] =
            static_cast<Byte>((state[index] >> 16U) & 0xffU);
        digest[index * 4U + 2U] =
            static_cast<Byte>((state[index] >> 8U) & 0xffU);
        digest[index * 4U + 3U] =
            static_cast<Byte>(state[index] & 0xffU);
    }
    return digest;
}

constexpr std::array<std::uint64_t, 80> kSha512Constants = {
    0x428a2f98d728ae22ULL, 0x7137449123ef65cdULL,
    0xb5c0fbcfec4d3b2fULL, 0xe9b5dba58189dbbcULL,
    0x3956c25bf348b538ULL, 0x59f111f1b605d019ULL,
    0x923f82a4af194f9bULL, 0xab1c5ed5da6d8118ULL,
    0xd807aa98a3030242ULL, 0x12835b0145706fbeULL,
    0x243185be4ee4b28cULL, 0x550c7dc3d5ffb4e2ULL,
    0x72be5d74f27b896fULL, 0x80deb1fe3b1696b1ULL,
    0x9bdc06a725c71235ULL, 0xc19bf174cf692694ULL,
    0xe49b69c19ef14ad2ULL, 0xefbe4786384f25e3ULL,
    0x0fc19dc68b8cd5b5ULL, 0x240ca1cc77ac9c65ULL,
    0x2de92c6f592b0275ULL, 0x4a7484aa6ea6e483ULL,
    0x5cb0a9dcbd41fbd4ULL, 0x76f988da831153b5ULL,
    0x983e5152ee66dfabULL, 0xa831c66d2db43210ULL,
    0xb00327c898fb213fULL, 0xbf597fc7beef0ee4ULL,
    0xc6e00bf33da88fc2ULL, 0xd5a79147930aa725ULL,
    0x06ca6351e003826fULL, 0x142929670a0e6e70ULL,
    0x27b70a8546d22ffcULL, 0x2e1b21385c26c926ULL,
    0x4d2c6dfc5ac42aedULL, 0x53380d139d95b3dfULL,
    0x650a73548baf63deULL, 0x766a0abb3c77b2a8ULL,
    0x81c2c92e47edaee6ULL, 0x92722c851482353bULL,
    0xa2bfe8a14cf10364ULL, 0xa81a664bbc423001ULL,
    0xc24b8b70d0f89791ULL, 0xc76c51a30654be30ULL,
    0xd192e819d6ef5218ULL, 0xd69906245565a910ULL,
    0xf40e35855771202aULL, 0x106aa07032bbd1b8ULL,
    0x19a4c116b8d2d0c8ULL, 0x1e376c085141ab53ULL,
    0x2748774cdf8eeb99ULL, 0x34b0bcb5e19b48a8ULL,
    0x391c0cb3c5c95a63ULL, 0x4ed8aa4ae3418acbULL,
    0x5b9cca4f7763e373ULL, 0x682e6ff3d6b2b8a3ULL,
    0x748f82ee5defb2fcULL, 0x78a5636f43172f60ULL,
    0x84c87814a1f0ab72ULL, 0x8cc702081a6439ecULL,
    0x90befffa23631e28ULL, 0xa4506cebde82bde9ULL,
    0xbef9a3f7b2c67915ULL, 0xc67178f2e372532bULL,
    0xca273eceea26619cULL, 0xd186b8c721c0c207ULL,
    0xeada7dd6cde0eb1eULL, 0xf57d4f7fee6ed178ULL,
    0x06f067aa72176fbaULL, 0x0a637dc5a2c898a6ULL,
    0x113f9804bef90daeULL, 0x1b710b35131c471bULL,
    0x28db77f523047d84ULL, 0x32caab7b40c72493ULL,
    0x3c9ebe0a15c9bebcULL, 0x431d67c49c100d4cULL,
    0x4cc5d4becb3e42b6ULL, 0x597f299cfc657e2aULL,
    0x5fcb6fab3ad6faecULL, 0x6c44198c4a475817ULL,
};

constexpr std::uint64_t rotate_right64(std::uint64_t value,
                                       std::uint64_t count) {
    return (value >> count) | (value << (64U - count));
}

std::array<Byte, 64> sha512_bytes(std::span<const Byte> input) {
    Bytes padded(input.begin(), input.end());
    const std::uint64_t bit_length =
        static_cast<std::uint64_t>(input.size()) * 8U;
    padded.push_back(0x80U);
    while ((padded.size() % 128U) != 112U) {
        padded.push_back(0U);
    }
    for (int index = 0; index < 8; ++index) {
        padded.push_back(0U);
    }
    for (int shift = 56; shift >= 0; shift -= 8) {
        padded.push_back(static_cast<Byte>((bit_length >> shift) & 0xffU));
    }

    std::array<std::uint64_t, 8> state = {
        0x6a09e667f3bcc908ULL, 0xbb67ae8584caa73bULL,
        0x3c6ef372fe94f82bULL, 0xa54ff53a5f1d36f1ULL,
        0x510e527fade682d1ULL, 0x9b05688c2b3e6c1fULL,
        0x1f83d9abfb41bd6bULL, 0x5be0cd19137e2179ULL,
    };

    for (std::size_t offset = 0; offset < padded.size(); offset += 128U) {
        std::array<std::uint64_t, 80> words{};
        for (std::size_t index = 0; index < 16U; ++index) {
            const std::size_t position = offset + index * 8U;
            std::uint64_t word = 0;
            for (std::size_t byte_index = 0; byte_index < 8U; ++byte_index) {
                word = (word << 8U) |
                       static_cast<std::uint64_t>(padded[position + byte_index]);
            }
            words[index] = word;
        }
        for (std::size_t index = 16U; index < 80U; ++index) {
            const std::uint64_t s0 =
                rotate_right64(words[index - 15U], 1U) ^
                rotate_right64(words[index - 15U], 8U) ^
                (words[index - 15U] >> 7U);
            const std::uint64_t s1 =
                rotate_right64(words[index - 2U], 19U) ^
                rotate_right64(words[index - 2U], 61U) ^
                (words[index - 2U] >> 6U);
            words[index] =
                words[index - 16U] + s0 + words[index - 7U] + s1;
        }

        std::uint64_t a = state[0];
        std::uint64_t b = state[1];
        std::uint64_t c = state[2];
        std::uint64_t d = state[3];
        std::uint64_t e = state[4];
        std::uint64_t f = state[5];
        std::uint64_t g = state[6];
        std::uint64_t h = state[7];

        for (std::size_t index = 0; index < 80U; ++index) {
            const std::uint64_t sum1 =
                rotate_right64(e, 14U) ^ rotate_right64(e, 18U) ^
                rotate_right64(e, 41U);
            const std::uint64_t choice = (e & f) ^ ((~e) & g);
            const std::uint64_t temporary1 =
                h + sum1 + choice + kSha512Constants[index] + words[index];
            const std::uint64_t sum0 =
                rotate_right64(a, 28U) ^ rotate_right64(a, 34U) ^
                rotate_right64(a, 39U);
            const std::uint64_t majority =
                (a & b) ^ (a & c) ^ (b & c);
            const std::uint64_t temporary2 = sum0 + majority;

            h = g;
            g = f;
            f = e;
            e = d + temporary1;
            d = c;
            c = b;
            b = a;
            a = temporary1 + temporary2;
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

    std::array<Byte, 64> digest{};
    for (std::size_t index = 0; index < state.size(); ++index) {
        for (std::size_t byte_index = 0; byte_index < 8U; ++byte_index) {
            digest[index * 8U + byte_index] = static_cast<Byte>(
                (state[index] >> (56U - byte_index * 8U)) & 0xffU);
        }
    }
    return digest;
}

// Ed25519 field arithmetic. Values are radix-2^16 limbs.
using EdInteger = std::int64_t;
using EdField = EdInteger[16];

constexpr EdField kFieldZero = {};
constexpr EdField kFieldOne = {1};
constexpr EdField kCurveD = {
    0x78a3, 0x1359, 0x4dca, 0x75eb, 0xd8ab, 0x4141, 0x0a4d, 0x0070,
    0xe898, 0x7779, 0x4079, 0x8cc7, 0xfe73, 0x2b6f, 0x6cee, 0x5203,
};
constexpr EdField kCurveD2 = {
    0xf159, 0x26b2, 0x9b94, 0xebd6, 0xb156, 0x8283, 0x149a, 0x00e0,
    0xd130, 0xeef3, 0x80f2, 0x198e, 0xfce7, 0x56df, 0xd9dc, 0x2406,
};
constexpr EdField kCurveX = {
    0xd51a, 0x8f25, 0x2d60, 0xc956, 0xa7b2, 0x9525, 0xc760, 0x692c,
    0xdc5c, 0xfdd6, 0xe231, 0xc0a4, 0x53fe, 0xcd6e, 0x36d3, 0x2169,
};
constexpr EdField kCurveY = {
    0x6658, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666,
    0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666,
};
constexpr EdField kCurveI = {
    0xa0b0, 0x4a0e, 0x1b27, 0xc4ee, 0xe478, 0xad2f, 0x1806, 0x2f43,
    0xd7a7, 0x3dfb, 0x0099, 0x2b4d, 0xdf0b, 0x4fc1, 0x2480, 0x2b83,
};

void ed_set(EdField destination, const EdField source) {
    for (std::size_t index = 0; index < 16U; ++index) {
        destination[index] = source[index];
    }
}

void ed_carry(EdField value) {
    for (std::size_t index = 0; index < 16U; ++index) {
        value[index] += (1LL << 16);
        const EdInteger carry = value[index] >> 16;
        value[(index + 1U) * (index < 15U)] +=
            carry - 1 + 37 * (carry - 1) * (index == 15U);
        value[index] -= carry * 65536;
    }
}

void ed_select(EdField left, EdField right, int swap) {
    const EdInteger mask = ~(static_cast<EdInteger>(swap) - 1);
    for (std::size_t index = 0; index < 16U; ++index) {
        const EdInteger difference = mask & (left[index] ^ right[index]);
        left[index] ^= difference;
        right[index] ^= difference;
    }
}

void ed_pack(Byte* output, const EdField value) {
    EdField temporary;
    EdField reduced;
    ed_set(temporary, value);
    ed_carry(temporary);
    ed_carry(temporary);
    ed_carry(temporary);

    for (int round = 0; round < 2; ++round) {
        reduced[0] = temporary[0] - 0xffed;
        for (std::size_t index = 1; index < 15U; ++index) {
            reduced[index] = temporary[index] - 0xffff -
                             ((reduced[index - 1U] >> 16) & 1);
            reduced[index - 1U] &= 0xffff;
        }
        reduced[15] = temporary[15] - 0x7fff -
                      ((reduced[14] >> 16) & 1);
        const int borrow = static_cast<int>((reduced[15] >> 16) & 1);
        reduced[14] &= 0xffff;
        ed_select(temporary, reduced, 1 - borrow);
    }

    for (std::size_t index = 0; index < 16U; ++index) {
        output[index * 2U] = static_cast<Byte>(temporary[index] & 0xff);
        output[index * 2U + 1U] =
            static_cast<Byte>((temporary[index] >> 8) & 0xff);
    }
}

void ed_unpack(EdField output, const Byte* input) {
    for (std::size_t index = 0; index < 16U; ++index) {
        output[index] = input[index * 2U] +
                        (static_cast<EdInteger>(input[index * 2U + 1U]) << 8);
    }
    output[15] &= 0x7fff;
}

void ed_add(EdField output, const EdField left, const EdField right) {
    for (std::size_t index = 0; index < 16U; ++index) {
        output[index] = left[index] + right[index];
    }
}

void ed_sub(EdField output, const EdField left, const EdField right) {
    for (std::size_t index = 0; index < 16U; ++index) {
        output[index] = left[index] - right[index];
    }
}

void ed_mul(EdField output, const EdField left, const EdField right) {
    EdInteger products[31] = {};
    for (std::size_t left_index = 0; left_index < 16U; ++left_index) {
        for (std::size_t right_index = 0; right_index < 16U; ++right_index) {
            products[left_index + right_index] +=
                left[left_index] * right[right_index];
        }
    }
    for (std::size_t index = 0; index < 15U; ++index) {
        products[index] += 38 * products[index + 16U];
    }
    for (std::size_t index = 0; index < 16U; ++index) {
        output[index] = products[index];
    }
    ed_carry(output);
    ed_carry(output);
}

void ed_square(EdField output, const EdField value) {
    ed_mul(output, value, value);
}

void ed_invert(EdField output, const EdField value) {
    EdField result;
    ed_set(result, value);
    for (int exponent = 253; exponent >= 0; --exponent) {
        ed_square(result, result);
        if (exponent != 2 && exponent != 4) {
            ed_mul(result, result, value);
        }
    }
    ed_set(output, result);
}

void ed_pow2523(EdField output, const EdField value) {
    EdField result;
    ed_set(result, value);
    for (int exponent = 250; exponent >= 0; --exponent) {
        ed_square(result, result);
        if (exponent != 1) {
            ed_mul(result, result, value);
        }
    }
    ed_set(output, result);
}

bool ed_not_equal(const EdField left, const EdField right) {
    Byte left_bytes[32];
    Byte right_bytes[32];
    ed_pack(left_bytes, left);
    ed_pack(right_bytes, right);
    return !constant_time_equal(
        std::span<const Byte>(left_bytes, 32),
        std::span<const Byte>(right_bytes, 32));
}

Byte ed_parity(const EdField value) {
    Byte packed[32];
    ed_pack(packed, value);
    return packed[0] & 1U;
}

void ed_point_add(EdField point[4], EdField other[4]) {
    EdField a;
    EdField b;
    EdField c;
    EdField d;
    EdField temporary;
    EdField e;
    EdField f;
    EdField g;
    EdField h;

    ed_sub(a, point[1], point[0]);
    ed_sub(temporary, other[1], other[0]);
    ed_mul(a, a, temporary);
    ed_add(b, point[0], point[1]);
    ed_add(temporary, other[0], other[1]);
    ed_mul(b, b, temporary);
    ed_mul(c, point[3], other[3]);
    ed_mul(c, c, kCurveD2);
    ed_mul(d, point[2], other[2]);
    ed_add(d, d, d);
    ed_sub(e, b, a);
    ed_sub(f, d, c);
    ed_add(g, d, c);
    ed_add(h, b, a);
    ed_mul(point[0], e, f);
    ed_mul(point[1], h, g);
    ed_mul(point[2], g, f);
    ed_mul(point[3], e, h);
}

void ed_point_swap(EdField point[4], EdField other[4], Byte swap) {
    for (std::size_t index = 0; index < 4U; ++index) {
        ed_select(point[index], other[index], swap);
    }
}

void ed_point_pack(Byte* output, EdField point[4]) {
    EdField inverse;
    EdField x;
    EdField y;
    ed_invert(inverse, point[2]);
    ed_mul(x, point[0], inverse);
    ed_mul(y, point[1], inverse);
    ed_pack(output, y);
    output[31] ^= static_cast<Byte>(ed_parity(x) << 7);
}

void ed_scalar_multiply(EdField point[4], EdField base[4],
                        const Byte* scalar) {
    ed_set(point[0], kFieldZero);
    ed_set(point[1], kFieldOne);
    ed_set(point[2], kFieldOne);
    ed_set(point[3], kFieldZero);
    for (int bit = 255; bit >= 0; --bit) {
        const Byte selected =
            static_cast<Byte>((scalar[bit / 8] >> (bit & 7)) & 1U);
        ed_point_swap(point, base, selected);
        ed_point_add(base, point);
        ed_point_add(point, point);
        ed_point_swap(point, base, selected);
    }
}

void ed_scalar_base(EdField point[4], const Byte* scalar) {
    EdField base[4];
    ed_set(base[0], kCurveX);
    ed_set(base[1], kCurveY);
    ed_set(base[2], kFieldOne);
    ed_mul(base[3], kCurveX, kCurveY);
    ed_scalar_multiply(point, base, scalar);
}

int ed_unpack_negative(EdField point[4], const Byte* packed) {
    EdField temporary;
    EdField check;
    EdField numerator;
    EdField denominator;
    EdField denominator2;
    EdField denominator4;
    EdField denominator6;

    ed_set(point[2], kFieldOne);
    ed_unpack(point[1], packed);
    ed_square(numerator, point[1]);
    ed_mul(denominator, numerator, kCurveD);
    ed_sub(numerator, numerator, point[2]);
    ed_add(denominator, point[2], denominator);

    ed_square(denominator2, denominator);
    ed_square(denominator4, denominator2);
    ed_mul(denominator6, denominator4, denominator2);
    ed_mul(temporary, denominator6, numerator);
    ed_mul(temporary, temporary, denominator);

    ed_pow2523(temporary, temporary);
    ed_mul(temporary, temporary, numerator);
    ed_mul(temporary, temporary, denominator);
    ed_mul(temporary, temporary, denominator);
    ed_mul(point[0], temporary, denominator);

    ed_square(check, point[0]);
    ed_mul(check, check, denominator);
    if (ed_not_equal(check, numerator)) {
        ed_mul(point[0], point[0], kCurveI);
    }

    ed_square(check, point[0]);
    ed_mul(check, check, denominator);
    if (ed_not_equal(check, numerator)) {
        return -1;
    }

    if (ed_parity(point[0]) == (packed[31] >> 7)) {
        ed_sub(point[0], kFieldZero, point[0]);
    }

    ed_mul(point[3], point[0], point[1]);
    return 0;
}

void ed_mod_l(Byte* output, EdInteger value[64]) {
    static constexpr std::array<Byte, 32> order = {
        0xed, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58,
        0xd6, 0x9c, 0xf7, 0xa2, 0xde, 0xf9, 0xde, 0x14,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10,
    };

    EdInteger carry = 0;
    for (int index = 63; index >= 32; --index) {
        carry = 0;
        int position = index - 32;
        for (; position < index - 12; ++position) {
            value[position] +=
                carry -
                16 * value[index] *
                    order[static_cast<std::size_t>(position - (index - 32))];
            carry = (value[position] + 128) >> 8;
            value[position] -= carry * 256;
        }
        value[position] += carry;
        value[index] = 0;
    }

    carry = 0;
    for (int index = 0; index < 32; ++index) {
        value[index] +=
            carry -
            (value[31] >> 4) * order[static_cast<std::size_t>(index)];
        carry = value[index] >> 8;
        value[index] &= 255;
    }
    for (int index = 0; index < 32; ++index) {
        value[index] -=
            carry * order[static_cast<std::size_t>(index)];
    }
    for (int index = 0; index < 32; ++index) {
        value[index + 1] += value[index] >> 8;
        output[index] = static_cast<Byte>(value[index] & 255);
    }
}

void ed_reduce(Byte* scalar) {
    EdInteger value[64];
    for (std::size_t index = 0; index < 64U; ++index) {
        value[index] = scalar[index];
    }
    for (std::size_t index = 0; index < 64U; ++index) {
        scalar[index] = 0;
    }
    ed_mod_l(scalar, value);
}

void ed_expand_seed(const std::array<Byte, 32>& seed,
                    std::array<Byte, 64>& expanded) {
    const std::array<Byte, 64> digest = sha512_bytes(seed);
    expanded = digest;
    expanded[0] &= 248U;
    expanded[31] &= 127U;
    expanded[31] |= 64U;
}

PublicKey ed_public_key_from_expanded(const std::array<Byte, 64>& expanded) {
    EdField point[4];
    PublicKey public_key{};
    ed_scalar_base(point, expanded.data());
    ed_point_pack(public_key.data(), point);
    return public_key;
}

Signature ed_sign(const std::array<Byte, 32>& seed,
                  std::span<const Byte> message) {
    std::array<Byte, 64> expanded{};
    ed_expand_seed(seed, expanded);
    const PublicKey public_key = ed_public_key_from_expanded(expanded);

    Bytes first_hash_input;
    first_hash_input.insert(first_hash_input.end(), expanded.begin() + 32,
                            expanded.end());
    first_hash_input.insert(first_hash_input.end(), message.begin(),
                            message.end());
    const std::array<Byte, 64> first_hash =
        sha512_bytes(first_hash_input);
    std::array<Byte, 64> nonce = first_hash;
    ed_reduce(nonce.data());

    Signature signature{};
    EdField point[4];
    ed_scalar_base(point, nonce.data());
    ed_point_pack(signature.data(), point);
    std::copy(public_key.begin(), public_key.end(), signature.begin() + 32);

    Bytes second_hash_input;
    second_hash_input.insert(second_hash_input.end(), signature.begin(),
                             signature.begin() + 64);
    second_hash_input.insert(second_hash_input.end(), message.begin(),
                             message.end());
    std::array<Byte, 64> challenge = sha512_bytes(second_hash_input);
    ed_reduce(challenge.data());

    EdInteger product[64] = {};
    for (std::size_t index = 0; index < 32U; ++index) {
        product[index] = nonce[index];
    }
    for (std::size_t left_index = 0; left_index < 32U; ++left_index) {
        for (std::size_t right_index = 0; right_index < 32U; ++right_index) {
            product[left_index + right_index] +=
                static_cast<EdInteger>(challenge[left_index]) *
                static_cast<EdInteger>(expanded[right_index]);
        }
    }
    ed_mod_l(signature.data() + 32, product);
    return signature;
}

bool ed_verify(const PublicKey& public_key, std::span<const Byte> message,
               const Signature& signature) {
    EdField point[4];
    if (ed_unpack_negative(point, public_key.data()) != 0) {
        return false;
    }

    Bytes challenge_input;
    challenge_input.insert(challenge_input.end(), signature.begin(),
                           signature.begin() + 32);
    challenge_input.insert(challenge_input.end(), public_key.begin(),
                           public_key.end());
    challenge_input.insert(challenge_input.end(), message.begin(),
                           message.end());
    std::array<Byte, 64> challenge = sha512_bytes(challenge_input);
    ed_reduce(challenge.data());

    EdField doubled[4];
    EdField base_point[4];
    ed_scalar_multiply(doubled, point, challenge.data());
    ed_scalar_base(base_point, signature.data() + 32);
    ed_point_add(doubled, base_point);

    Byte encoded[32];
    ed_point_pack(encoded, doubled);
    return constant_time_equal(
        std::span<const Byte>(encoded, 32),
        std::span<const Byte>(signature.data(), 32));
}

void validate_private_key_length(const Bytes& seed) {
    if (seed.size() != 32U) {
        throw std::invalid_argument("Ed25519 seed must contain 32 bytes");
    }
}

}  // namespace

std::string to_hex(std::span<const Byte> bytes) {
    static constexpr char digits[] = "0123456789abcdef";
    std::string output;
    output.reserve(bytes.size() * 2U);
    for (const Byte value : bytes) {
        output.push_back(digits[value >> 4U]);
        output.push_back(digits[value & 0x0fU]);
    }
    return output;
}

Bytes from_hex(std::string_view text) {
    if ((text.size() % 2U) != 0U) {
        throw std::invalid_argument("hex input must have an even length");
    }
    auto decode = [](char value) -> int {
        if (value >= '0' && value <= '9') {
            return value - '0';
        }
        if (value >= 'a' && value <= 'f') {
            return value - 'a' + 10;
        }
        if (value >= 'A' && value <= 'F') {
            return value - 'A' + 10;
        }
        return -1;
    };

    Bytes output;
    output.reserve(text.size() / 2U);
    for (std::size_t index = 0; index < text.size(); index += 2U) {
        const int high = decode(text[index]);
        const int low = decode(text[index + 1U]);
        if (high < 0 || low < 0) {
            throw std::invalid_argument("hex input contains a non-hex digit");
        }
        output.push_back(static_cast<Byte>((high << 4) | low));
    }
    return output;
}

Digest sha256(std::span<const Byte> data) {
    return sha256_bytes(data);
}

Digest sha256(std::string_view text) {
    return sha256_bytes(std::span<const Byte>(
        reinterpret_cast<const Byte*>(text.data()), text.size()));
}

PrivateKey::PrivateKey() : PrivateKey(generate()) {}

PrivateKey::PrivateKey(const std::array<Byte, 32>& seed) : seed_(seed) {}

PrivateKey PrivateKey::generate() {
    std::array<Byte, 32> seed{};
    fill_random(seed);
    return PrivateKey(seed);
}

PrivateKey PrivateKey::from_hex(std::string_view seed_hex) {
    const Bytes seed = netsec::from_hex(seed_hex);
    validate_private_key_length(seed);
    std::array<Byte, 32> bytes{};
    std::copy(seed.begin(), seed.end(), bytes.begin());
    return PrivateKey(bytes);
}

PublicKey PrivateKey::public_key() const {
    std::array<Byte, 64> expanded{};
    ed_expand_seed(seed_, expanded);
    return ed_public_key_from_expanded(expanded);
}

Signature PrivateKey::sign(std::span<const Byte> message) const {
    return ed_sign(seed_, message);
}

Signature PrivateKey::sign(std::string_view message) const {
    return sign(std::span<const Byte>(
        reinterpret_cast<const Byte*>(message.data()), message.size()));
}

const std::array<Byte, 32>& PrivateKey::seed() const noexcept {
    return seed_;
}

Bytes Certificate::signing_payload() const {
    return certificate_signing_payload(*this);
}

Bytes certificate_signing_payload(const Certificate& certificate) {
    Bytes output;
    append_string(output, kCertificateDomain);
    append_string(output, certificate.subject);
    append_string(output, certificate.issuer);
    append_bytes(output, certificate.public_key);
    append_u64(output, certificate.serial);
    append_i64(output, certificate.not_before);
    append_i64(output, certificate.not_after);
    output.push_back(certificate.is_ca ? 1U : 0U);
    return output;
}

Certificate make_root_certificate(std::string subject, const PrivateKey& key,
                                  std::uint64_t serial,
                                  std::int64_t not_before,
                                  std::int64_t not_after) {
    if (subject.empty()) {
        throw std::invalid_argument("root certificate subject must not be empty");
    }
    if (not_after < not_before) {
        throw std::invalid_argument("certificate validity interval is invalid");
    }
    Certificate certificate;
    certificate.subject = std::move(subject);
    certificate.issuer = certificate.subject;
    certificate.public_key = key.public_key();
    certificate.serial = serial;
    certificate.not_before = not_before;
    certificate.not_after = not_after;
    certificate.is_ca = true;
    certificate.signature = key.sign(certificate.signing_payload());
    return certificate;
}

Certificate issue_certificate(std::string subject, const PublicKey& subject_key,
                              std::string issuer, const PrivateKey& issuer_key,
                              std::uint64_t serial, std::int64_t not_before,
                              std::int64_t not_after, bool is_ca) {
    if (subject.empty() || issuer.empty()) {
        throw std::invalid_argument("certificate names must not be empty");
    }
    if (not_after < not_before) {
        throw std::invalid_argument("certificate validity interval is invalid");
    }
    Certificate certificate;
    certificate.subject = std::move(subject);
    certificate.issuer = std::move(issuer);
    certificate.public_key = subject_key;
    certificate.serial = serial;
    certificate.not_before = not_before;
    certificate.not_after = not_after;
    certificate.is_ca = is_ca;
    certificate.signature = issuer_key.sign(certificate.signing_payload());
    return certificate;
}

Certificate issue_certificate(std::string subject, const PublicKey& subject_key,
                              const Certificate& issuer_certificate,
                              const PrivateKey& issuer_key, std::uint64_t serial,
                              std::int64_t not_before, std::int64_t not_after,
                              bool is_ca) {
    if (!issuer_certificate.is_ca) {
        throw std::invalid_argument("issuer certificate is not a CA");
    }
    return issue_certificate(std::move(subject), subject_key,
                             issuer_certificate.subject, issuer_key, serial,
                             not_before, not_after, is_ca);
}

Bytes download_signing_payload(const DownloadRequest& request) {
    Bytes output;
    append_string(output, kDownloadDomain);
    append_string(output, request.transaction_id);
    append_string(output, request.eid);
    append_string(output, request.server_identity);
    append_string(output, request.nonce);
    append_string(output, request.profile.id);
    append_u64(output, request.profile.version);
    append_bytes(output, request.profile.payload);
    append_i64(output, request.validation_time);
    return output;
}

Signature sign_download_request(const PrivateKey& server_key,
                                const DownloadRequest& request) {
    return server_key.sign(download_signing_payload(request));
}

bool verify_download_signature(const PublicKey& server_key,
                               const DownloadRequest& request) {
    return ed_verify(server_key, download_signing_payload(request),
                     request.signature);
}

const char* error_code_name(ErrorCode code) noexcept {
    switch (code) {
        case ErrorCode::None:
            return "none";
        case ErrorCode::InvalidArgument:
            return "invalid_argument";
        case ErrorCode::EidMismatch:
            return "eid_mismatch";
        case ErrorCode::TransactionAlreadyUsed:
            return "transaction_already_used";
        case ErrorCode::InvalidNonce:
            return "invalid_nonce";
        case ErrorCode::NonceReplayed:
            return "nonce_replayed";
        case ErrorCode::InvalidCertificateChain:
            return "invalid_certificate_chain";
        case ErrorCode::UntrustedCertificate:
            return "untrusted_certificate";
        case ErrorCode::CertificateNotYetValid:
            return "certificate_not_yet_valid";
        case ErrorCode::CertificateExpired:
            return "certificate_expired";
        case ErrorCode::ServerIdentityMismatch:
            return "server_identity_mismatch";
        case ErrorCode::InvalidSignature:
            return "invalid_signature";
        case ErrorCode::VersionRollback:
            return "version_rollback";
        case ErrorCode::UnknownSession:
            return "unknown_session";
        case ErrorCode::StageOutOfOrder:
            return "stage_out_of_order";
        case ErrorCode::StageAlreadyConfirmed:
            return "stage_already_confirmed";
        case ErrorCode::TransactionAlreadyInstalled:
            return "transaction_already_installed";
        case ErrorCode::StateMismatch:
            return "state_mismatch";
    }
    return "unknown";
}

TransactionError::TransactionError(ErrorCode code, std::string message)
    : std::runtime_error(std::move(message)), code_(code) {}

ErrorCode TransactionError::code() const noexcept {
    return code_;
}

DownloadTransactionManager::DownloadTransactionManager(std::string device_eid)
    : device_eid_(std::move(device_eid)) {}

DownloadTransactionManager::DownloadTransactionManager(
    std::string device_eid, const Certificate& root_certificate)
    : DownloadTransactionManager(std::move(device_eid)) {
    add_trust_anchor(root_certificate);
}

DownloadTransactionManager::DownloadTransactionManager(std::string device_eid,
                                                       TrustAnchor anchor)
    : DownloadTransactionManager(std::move(device_eid)) {
    add_trust_anchor(std::move(anchor));
}

const std::string& DownloadTransactionManager::eid() const noexcept {
    return device_eid_;
}

void DownloadTransactionManager::add_trust_anchor(
    const Certificate& root_certificate) {
    if (root_certificate.subject.empty() || !root_certificate.is_ca) {
        throw std::invalid_argument(
            "a trust anchor must be a named CA certificate");
    }
    add_trust_anchor(root_certificate.subject, root_certificate.public_key);
}

void DownloadTransactionManager::add_trust_anchor(TrustAnchor anchor) {
    add_trust_anchor(std::move(anchor.subject), anchor.public_key);
}

void DownloadTransactionManager::add_trust_anchor(
    std::string subject, const PublicKey& public_key) {
    if (subject.empty()) {
        throw std::invalid_argument("trust anchor subject must not be empty");
    }
    auto& keys = trust_anchors_[std::move(subject)];
    if (std::find(keys.begin(), keys.end(), public_key) == keys.end()) {
        keys.push_back(public_key);
    }
}

std::size_t DownloadTransactionManager::trust_anchor_count() const noexcept {
    std::size_t count = 0;
    for (const auto& entry : trust_anchors_) {
        count += entry.second.size();
    }
    return count;
}

std::string DownloadTransactionManager::issue_nonce() {
    require_issued_nonce_ = true;
    for (;;) {
        std::string nonce = random_hex(32);
        if (outstanding_nonces_.insert(nonce).second) {
            return nonce;
        }
    }
}

void DownloadTransactionManager::require_issued_nonce(bool required) noexcept {
    require_issued_nonce_ = required;
}

bool DownloadTransactionManager::requires_issued_nonce() const noexcept {
    return require_issued_nonce_;
}

ErrorCode DownloadTransactionManager::validate_certificate_chain(
    const std::vector<Certificate>& chain,
    std::string_view expected_server_identity,
    std::int64_t validation_time) const {
    if (chain.empty() || chain.size() > kMaximumCertificateChain) {
        return ErrorCode::InvalidCertificateChain;
    }

    const Certificate& leaf = chain.front();
    if (leaf.subject.empty() || leaf.issuer.empty() ||
        leaf.subject != expected_server_identity) {
        return ErrorCode::ServerIdentityMismatch;
    }
    if (leaf.is_ca) {
        return ErrorCode::InvalidCertificateChain;
    }

    for (std::size_t index = 0; index < chain.size(); ++index) {
        const Certificate& certificate = chain[index];
        if (certificate.subject.empty() || certificate.issuer.empty()) {
            return ErrorCode::InvalidCertificateChain;
        }
        if (validation_time < certificate.not_before) {
            return ErrorCode::CertificateNotYetValid;
        }
        if (validation_time > certificate.not_after) {
            return ErrorCode::CertificateExpired;
        }

        const auto anchors = trust_anchors_.find(certificate.subject);
        if (anchors != trust_anchors_.end() &&
            std::find(anchors->second.begin(), anchors->second.end(),
                      certificate.public_key) != anchors->second.end()) {
            return certificate.is_ca ? ErrorCode::None
                                     : ErrorCode::InvalidCertificateChain;
        }

        const Bytes signed_payload = certificate.signing_payload();
        if (index + 1U < chain.size()) {
            const Certificate& parent = chain[index + 1U];
            if (!parent.is_ca || certificate.issuer != parent.subject ||
                !ed_verify(parent.public_key, signed_payload,
                           certificate.signature)) {
                return ErrorCode::InvalidCertificateChain;
            }
            continue;
        }

        const auto issuer_anchors = trust_anchors_.find(certificate.issuer);
        if (issuer_anchors == trust_anchors_.end()) {
            return ErrorCode::UntrustedCertificate;
        }
        for (const PublicKey& anchor_key : issuer_anchors->second) {
            if (ed_verify(anchor_key, signed_payload, certificate.signature)) {
                return ErrorCode::None;
            }
        }
        return ErrorCode::InvalidCertificateChain;
    }
    return ErrorCode::UntrustedCertificate;
}

DownloadReceipt DownloadTransactionManager::download_impl(
    const DownloadRequest& request) {
    if (device_eid_.empty()) {
        throw TransactionError(ErrorCode::InvalidArgument,
                               "the target device has no EID");
    }
    if (request.eid.empty() || request.eid != device_eid_) {
        throw TransactionError(ErrorCode::EidMismatch,
                               "transaction EID does not match this device");
    }
    if (request.transaction_id.empty() || request.server_identity.empty() ||
        request.nonce.empty() || request.profile.id.empty()) {
        throw TransactionError(ErrorCode::InvalidArgument,
                               "transaction fields must not be empty");
    }
    if (used_transactions_.find(request.transaction_id) !=
        used_transactions_.end()) {
        throw TransactionError(ErrorCode::TransactionAlreadyUsed,
                               "transaction identifier has already been used");
    }
    if (used_nonces_.find(request.nonce) != used_nonces_.end()) {
        throw TransactionError(ErrorCode::NonceReplayed,
                               "nonce has already been consumed");
    }
    if (require_issued_nonce_ &&
        outstanding_nonces_.find(request.nonce) == outstanding_nonces_.end()) {
        throw TransactionError(ErrorCode::InvalidNonce,
                               "nonce was not issued by this device");
    }
    if (request.certificate_chain.empty()) {
        throw TransactionError(ErrorCode::InvalidCertificateChain,
                               "certificate chain is missing");
    }

    const std::int64_t validation_time =
        request.validation_time == 0 ? current_time_seconds()
                                     : request.validation_time;
    const ErrorCode certificate_error = validate_certificate_chain(
        request.certificate_chain, request.server_identity, validation_time);
    if (certificate_error != ErrorCode::None) {
        throw TransactionError(certificate_error,
                               "certificate chain validation failed");
    }

    const Certificate& leaf = request.certificate_chain.front();
    if (!verify_download_signature(leaf.public_key, request)) {
        throw TransactionError(ErrorCode::InvalidSignature,
                               "download transaction signature is invalid");
    }

    const auto installed = installed_versions_.find(request.profile.id);
    if (installed != installed_versions_.end() &&
        request.profile.version <= installed->second) {
        throw TransactionError(
            ErrorCode::VersionRollback,
            "profile version is not greater than the installed version");
    }

    PendingTransaction transaction;
    transaction.transaction_id = request.transaction_id;
    transaction.server_identity = request.server_identity;
    transaction.nonce = request.nonce;
    transaction.profile_id = request.profile.id;
    transaction.profile_version = request.profile.version;
    transaction.profile_data = request.profile.payload;
    transaction.profile_digest = sha256(transaction.profile_data);
    do {
        transaction.session_id = random_hex(24);
    } while (sessions_.find(transaction.session_id) != sessions_.end());

    sessions_.emplace(transaction.session_id, transaction);
    used_transactions_.insert(transaction.transaction_id);
    used_nonces_.insert(transaction.nonce);
    outstanding_nonces_.erase(transaction.nonce);

    DownloadReceipt receipt;
    receipt.transaction_id = transaction.transaction_id;
    receipt.session_id = transaction.session_id;
    receipt.eid = device_eid_;
    receipt.server_identity = transaction.server_identity;
    receipt.nonce = transaction.nonce;
    receipt.profile_id = transaction.profile_id;
    receipt.profile_version = transaction.profile_version;
    receipt.profile_digest = transaction.profile_digest;
    receipt.profile_data = transaction.profile_data;
    return receipt;
}

DownloadReceipt DownloadTransactionManager::download(
    const DownloadRequest& request) {
    return download_impl(request);
}

DownloadReceipt DownloadTransactionManager::begin_download(
    const DownloadRequest& request) {
    return download_impl(request);
}

Result<DownloadReceipt> DownloadTransactionManager::try_download(
    const DownloadRequest& request) noexcept {
    try {
        return Result<DownloadReceipt>{download_impl(request), ErrorCode::None, {}};
    } catch (const TransactionError& error) {
        return Result<DownloadReceipt>{std::nullopt, error.code(), error.what()};
    } catch (const std::exception& error) {
        return Result<DownloadReceipt>{
            std::nullopt, ErrorCode::InvalidArgument, error.what()};
    } catch (...) {
        return Result<DownloadReceipt>{
            std::nullopt, ErrorCode::InvalidArgument, "unknown error"};
    }
}

Result<DownloadReceipt> DownloadTransactionManager::try_begin_download(
    const DownloadRequest& request) noexcept {
    return try_download(request);
}

void DownloadTransactionManager::confirm_download_impl(
    std::string_view session_id) {
    const auto transaction = sessions_.find(std::string(session_id));
    if (transaction == sessions_.end()) {
        throw TransactionError(ErrorCode::UnknownSession,
                               "download session does not exist");
    }
    switch (transaction->second.stage) {
        case DownloadStage::Downloaded:
            transaction->second.stage = DownloadStage::DownloadConfirmed;
            return;
        case DownloadStage::DownloadConfirmed:
            throw TransactionError(ErrorCode::StageAlreadyConfirmed,
                                   "download stage was already confirmed");
        case DownloadStage::Installed:
            throw TransactionError(ErrorCode::TransactionAlreadyInstalled,
                                   "transaction was already installed");
    }
}

void DownloadTransactionManager::confirm_download(std::string_view session_id) {
    confirm_download_impl(session_id);
}

void DownloadTransactionManager::confirm_download(
    const DownloadReceipt& receipt) {
    if (receipt.eid != device_eid_) {
        throw TransactionError(ErrorCode::EidMismatch,
                               "receipt belongs to another EID");
    }
    confirm_download_impl(receipt.session_id);
}

StatusResult DownloadTransactionManager::try_confirm_download(
    std::string_view session_id) noexcept {
    try {
        confirm_download_impl(session_id);
        return StatusResult{ErrorCode::None, {}};
    } catch (const TransactionError& error) {
        return StatusResult{error.code(), error.what()};
    } catch (const std::exception& error) {
        return StatusResult{ErrorCode::InvalidArgument, error.what()};
    } catch (...) {
        return StatusResult{ErrorCode::InvalidArgument, "unknown error"};
    }
}

StatusResult DownloadTransactionManager::try_confirm_download(
    const DownloadReceipt& receipt) noexcept {
    if (receipt.eid != device_eid_) {
        return StatusResult{ErrorCode::EidMismatch,
                            "receipt belongs to another EID"};
    }
    return try_confirm_download(receipt.session_id);
}

InstallReceipt DownloadTransactionManager::install_impl(
    std::string_view session_id) {
    const auto transaction = sessions_.find(std::string(session_id));
    if (transaction == sessions_.end()) {
        throw TransactionError(ErrorCode::UnknownSession,
                               "download session does not exist");
    }
    PendingTransaction& pending = transaction->second;
    if (pending.stage == DownloadStage::Downloaded) {
        throw TransactionError(ErrorCode::StageOutOfOrder,
                               "download must be confirmed before installation");
    }
    if (pending.stage == DownloadStage::Installed) {
        throw TransactionError(ErrorCode::TransactionAlreadyInstalled,
                               "transaction was already installed");
    }

    const auto installed = installed_versions_.find(pending.profile_id);
    if (installed != installed_versions_.end() &&
        pending.profile_version <= installed->second) {
        throw TransactionError(
            ErrorCode::VersionRollback,
            "profile version is no longer greater than the installed version");
    }

    installed_versions_[pending.profile_id] = pending.profile_version;
    installed_profiles_[pending.profile_id] = pending.profile_data;
    pending.stage = DownloadStage::Installed;

    InstallReceipt receipt;
    receipt.transaction_id = pending.transaction_id;
    receipt.session_id = pending.session_id;
    receipt.eid = device_eid_;
    receipt.profile_id = pending.profile_id;
    receipt.profile_version = pending.profile_version;
    receipt.profile_digest = pending.profile_digest;
    return receipt;
}

InstallReceipt DownloadTransactionManager::install(std::string_view session_id) {
    return install_impl(session_id);
}

InstallReceipt DownloadTransactionManager::install(
    const DownloadReceipt& receipt) {
    if (receipt.eid != device_eid_) {
        throw TransactionError(ErrorCode::EidMismatch,
                               "receipt belongs to another EID");
    }
    return install_impl(receipt.session_id);
}

InstallReceipt DownloadTransactionManager::confirm_install(
    std::string_view session_id) {
    return install_impl(session_id);
}

InstallReceipt DownloadTransactionManager::confirm_install(
    const DownloadReceipt& receipt) {
    return install(receipt);
}

Result<InstallReceipt> DownloadTransactionManager::try_install(
    std::string_view session_id) noexcept {
    try {
        return Result<InstallReceipt>{install_impl(session_id), ErrorCode::None, {}};
    } catch (const TransactionError& error) {
        return Result<InstallReceipt>{std::nullopt, error.code(), error.what()};
    } catch (const std::exception& error) {
        return Result<InstallReceipt>{
            std::nullopt, ErrorCode::InvalidArgument, error.what()};
    } catch (...) {
        return Result<InstallReceipt>{
            std::nullopt, ErrorCode::InvalidArgument, "unknown error"};
    }
}

Result<InstallReceipt> DownloadTransactionManager::try_install(
    const DownloadReceipt& receipt) noexcept {
    if (receipt.eid != device_eid_) {
        return Result<InstallReceipt>{std::nullopt, ErrorCode::EidMismatch,
                                      "receipt belongs to another EID"};
    }
    return try_install(receipt.session_id);
}

Result<InstallReceipt> DownloadTransactionManager::try_confirm_install(
    std::string_view session_id) noexcept {
    return try_install(session_id);
}

Result<InstallReceipt> DownloadTransactionManager::try_confirm_install(
    const DownloadReceipt& receipt) noexcept {
    return try_install(receipt);
}

DownloadStage DownloadTransactionManager::stage(
    std::string_view session_id) const {
    const auto transaction = sessions_.find(std::string(session_id));
    if (transaction == sessions_.end()) {
        throw TransactionError(ErrorCode::UnknownSession,
                               "download session does not exist");
    }
    return transaction->second.stage;
}

bool DownloadTransactionManager::transaction_seen(
    std::string_view transaction_id) const noexcept {
    return used_transactions_.find(std::string(transaction_id)) !=
           used_transactions_.end();
}

std::optional<Bytes> DownloadTransactionManager::downloaded_profile(
    std::string_view session_id) const {
    const auto transaction = sessions_.find(std::string(session_id));
    if (transaction == sessions_.end()) {
        return std::nullopt;
    }
    return transaction->second.profile_data;
}

std::uint64_t DownloadTransactionManager::installed_version(
    std::string_view profile_id) const noexcept {
    const auto version = installed_versions_.find(std::string(profile_id));
    return version == installed_versions_.end() ? 0U : version->second;
}

std::optional<Bytes> DownloadTransactionManager::installed_profile(
    std::string_view profile_id) const {
    const auto profile = installed_profiles_.find(std::string(profile_id));
    if (profile == installed_profiles_.end()) {
        return std::nullopt;
    }
    return profile->second;
}

ManagerState DownloadTransactionManager::snapshot() const {
    ManagerState state;
    state.eid = device_eid_;
    state.installed_versions = installed_versions_;
    state.installed_profiles = installed_profiles_;
    state.used_transactions = used_transactions_;
    state.used_nonces = used_nonces_;
    return state;
}

void DownloadTransactionManager::restore_state(const ManagerState& state) {
    if (state.eid != device_eid_) {
        throw TransactionError(ErrorCode::StateMismatch,
                               "state belongs to another EID");
    }
    installed_versions_ = state.installed_versions;
    installed_profiles_ = state.installed_profiles;
    used_transactions_ = state.used_transactions;
    used_nonces_ = state.used_nonces;
    sessions_.clear();
    outstanding_nonces_.clear();
}

}  // namespace netsec
