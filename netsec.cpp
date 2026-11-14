#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <bit>
#include <limits>
#include <set>
#include <stdexcept>
#include <tuple>
#include <utility>

namespace netsec {
namespace {

constexpr std::uint32_t rotl32(std::uint32_t value, unsigned int count) {
    return (value << count) | (value >> (32U - count));
}

constexpr std::uint32_t rotr32(std::uint32_t value, unsigned int count) {
    return (value >> count) | (value << (32U - count));
}

constexpr std::uint64_t rotr64(std::uint64_t value, unsigned int count) {
    return (value >> count) | (value << (64U - count));
}

void append_u32_be(Bytes& output, std::uint32_t value) {
    output.push_back(static_cast<std::uint8_t>(value >> 24U));
    output.push_back(static_cast<std::uint8_t>(value >> 16U));
    output.push_back(static_cast<std::uint8_t>(value >> 8U));
    output.push_back(static_cast<std::uint8_t>(value));
}

std::uint32_t read_u32_be(const std::uint8_t* data) {
    return (static_cast<std::uint32_t>(data[0]) << 24U) |
           (static_cast<std::uint32_t>(data[1]) << 16U) |
           (static_cast<std::uint32_t>(data[2]) << 8U) |
           static_cast<std::uint32_t>(data[3]);
}

Bytes pad(std::span<const std::uint8_t> data,
          std::size_t block_size,
          std::size_t length_size) {
    Bytes result(data.begin(), data.end());
    result.push_back(0x80U);
    const std::size_t target = block_size - length_size;
    while (result.size() % block_size != target) {
        result.push_back(0U);
    }
    const std::uint64_t bit_length = static_cast<std::uint64_t>(data.size()) * 8U;
    for (std::size_t index = 0; index < length_size - 8U; ++index) {
        result.push_back(0U);
    }
    for (int shift = 56; shift >= 0; shift -= 8) {
        result.push_back(static_cast<std::uint8_t>(
            bit_length >> static_cast<unsigned int>(shift)));
    }
    return result;
}

}  // namespace

std::size_t digest_size(HashAlgorithm algorithm) {
    switch (algorithm) {
    case HashAlgorithm::Sha1:
        return 20;
    case HashAlgorithm::Sha256:
        return 32;
    case HashAlgorithm::Sha384:
        return 48;
    case HashAlgorithm::Sha512:
        return 64;
    }
    return 0;
}

std::string_view algorithm_name(HashAlgorithm algorithm) {
    switch (algorithm) {
    case HashAlgorithm::Sha1:
        return "sha1";
    case HashAlgorithm::Sha256:
        return "sha256";
    case HashAlgorithm::Sha384:
        return "sha384";
    case HashAlgorithm::Sha512:
        return "sha512";
    }
    return "unknown";
}

Bytes sha1(std::span<const std::uint8_t> data) {
    static constexpr std::array<std::uint32_t, 5> initial{
        0x67452301U, 0xefcdab89U, 0x98badcfeU, 0x10325476U, 0xc3d2e1f0U};
    std::array<std::uint32_t, 5> state = initial;
    const Bytes message = pad(data, 64, 8);
    std::array<std::uint32_t, 80> words{};

    for (std::size_t offset = 0; offset < message.size(); offset += 64) {
        for (std::size_t index = 0; index < 16; ++index) {
            words[index] = read_u32_be(message.data() + offset + index * 4);
        }
        for (std::size_t index = 16; index < 80; ++index) {
            words[index] = rotl32(words[index - 3] ^ words[index - 8] ^
                                      words[index - 14] ^ words[index - 16],
                                  1);
        }

        std::uint32_t a = state[0];
        std::uint32_t b = state[1];
        std::uint32_t c = state[2];
        std::uint32_t d = state[3];
        std::uint32_t e = state[4];

        for (std::size_t index = 0; index < 80; ++index) {
            std::uint32_t function = 0;
            std::uint32_t constant = 0;
            if (index < 20) {
                function = (b & c) | ((~b) & d);
                constant = 0x5a827999U;
            } else if (index < 40) {
                function = b ^ c ^ d;
                constant = 0x6ed9eba1U;
            } else if (index < 60) {
                function = (b & c) | (b & d) | (c & d);
                constant = 0x8f1bbcdcU;
            } else {
                function = b ^ c ^ d;
                constant = 0xca62c1d6U;
            }
            const std::uint32_t temporary =
                rotl32(a, 5) + function + e + constant + words[index];
            e = d;
            d = c;
            c = rotl32(b, 30);
            b = a;
            a = temporary;
        }

        state[0] += a;
        state[1] += b;
        state[2] += c;
        state[3] += d;
        state[4] += e;
    }

    Bytes output;
    output.reserve(20);
    for (std::uint32_t word : state) {
        append_u32_be(output, word);
    }
    return output;
}

Bytes sha256(std::span<const std::uint8_t> data) {
    static constexpr std::array<std::uint32_t, 64> constants{
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
    static constexpr std::array<std::uint32_t, 8> initial{
        0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
        0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U};
    std::array<std::uint32_t, 8> state = initial;
    const Bytes message = pad(data, 64, 8);
    std::array<std::uint32_t, 64> words{};

    for (std::size_t offset = 0; offset < message.size(); offset += 64) {
        for (std::size_t index = 0; index < 16; ++index) {
            words[index] = read_u32_be(message.data() + offset + index * 4);
        }
        for (std::size_t index = 16; index < 64; ++index) {
            const std::uint32_t s0 = rotr32(words[index - 15], 7) ^
                                     rotr32(words[index - 15], 18) ^
                                     (words[index - 15] >> 3U);
            const std::uint32_t s1 = rotr32(words[index - 2], 17) ^
                                     rotr32(words[index - 2], 19) ^
                                     (words[index - 2] >> 10U);
            words[index] = words[index - 16] + s0 + words[index - 7] + s1;
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
            const std::uint32_t s1 = rotr32(e, 6) ^ rotr32(e, 11) ^ rotr32(e, 25);
            const std::uint32_t choose = (e & f) ^ ((~e) & g);
            const std::uint32_t temporary1 =
                h + s1 + choose + constants[index] + words[index];
            const std::uint32_t s0 = rotr32(a, 2) ^ rotr32(a, 13) ^ rotr32(a, 22);
            const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
            const std::uint32_t temporary2 = s0 + majority;
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

    Bytes output;
    output.reserve(32);
    for (std::uint32_t word : state) {
        append_u32_be(output, word);
    }
    return output;
}

namespace {

Bytes sha512_family(std::span<const std::uint8_t> data, bool truncated) {
    static constexpr std::array<std::uint64_t, 80> constants{
        0x428a2f98d728ae22ULL, 0x7137449123ef65cdULL, 0xb5c0fbcfec4d3b2fULL,
        0xe9b5dba58189dbbcULL, 0x3956c25bf348b538ULL, 0x59f111f1b605d019ULL,
        0x923f82a4af194f9bULL, 0xab1c5ed5da6d8118ULL, 0xd807aa98a3030242ULL,
        0x12835b0145706fbeULL, 0x243185be4ee4b28cULL, 0x550c7dc3d5ffb4e2ULL,
        0x72be5d74f27b896fULL, 0x80deb1fe3b1696b1ULL, 0x9bdc06a725c71235ULL,
        0xc19bf174cf692694ULL, 0xe49b69c19ef14ad2ULL, 0xefbe4786384f25e3ULL,
        0x0fc19dc68b8cd5b5ULL, 0x240ca1cc77ac9c65ULL, 0x2de92c6f592b0275ULL,
        0x4a7484aa6ea6e483ULL, 0x5cb0a9dcbd41fbd4ULL, 0x76f988da831153b5ULL,
        0x983e5152ee66dfabULL, 0xa831c66d2db43210ULL, 0xb00327c898fb213fULL,
        0xbf597fc7beef0ee4ULL, 0xc6e00bf33da88fc2ULL, 0xd5a79147930aa725ULL,
        0x06ca6351e003826fULL, 0x142929670a0e6e70ULL, 0x27b70a8546d22ffcULL,
        0x2e1b21385c26c926ULL, 0x4d2c6dfc5ac42aedULL, 0x53380d139d95b3dfULL,
        0x650a73548baf63deULL, 0x766a0abb3c77b2a8ULL, 0x81c2c92e47edaee6ULL,
        0x92722c851482353bULL, 0xa2bfe8a14cf10364ULL, 0xa81a664bbc423001ULL,
        0xc24b8b70d0f89791ULL, 0xc76c51a30654be30ULL, 0xd192e819d6ef5218ULL,
        0xd69906245565a910ULL, 0xf40e35855771202aULL, 0x106aa07032bbd1b8ULL,
        0x19a4c116b8d2d0c8ULL, 0x1e376c085141ab53ULL, 0x2748774cdf8eeb99ULL,
        0x34b0bcb5e19b48a8ULL, 0x391c0cb3c5c95a63ULL, 0x4ed8aa4ae3418acbULL,
        0x5b9cca4f7763e373ULL, 0x682e6ff3d6b2b8a3ULL, 0x748f82ee5defb2fcULL,
        0x78a5636f43172f60ULL, 0x84c87814a1f0ab72ULL, 0x8cc702081a6439ecULL,
        0x90befffa23631e28ULL, 0xa4506cebde82bde9ULL, 0xbef9a3f7b2c67915ULL,
        0xc67178f2e372532bULL, 0xca273eceea26619cULL, 0xd186b8c721c0c207ULL,
        0xeada7dd6cde0eb1eULL, 0xf57d4f7fee6ed178ULL, 0x06f067aa72176fbaULL,
        0x0a637dc5a2c898a6ULL, 0x113f9804bef90daeULL, 0x1b710b35131c471bULL,
        0x28db77f523047d84ULL, 0x32caab7b40c72493ULL, 0x3c9ebe0a15c9bebcULL,
        0x431d67c49c100d4cULL, 0x4cc5d4becb3e42b6ULL, 0x597f299cfc657e2aULL,
        0x5fcb6fab3ad6faecULL, 0x6c44198c4a475817ULL};
    static constexpr std::array<std::uint64_t, 8> sha512_initial{
        0x6a09e667f3bcc908ULL, 0xbb67ae8584caa73bULL, 0x3c6ef372fe94f82bULL,
        0xa54ff53a5f1d36f1ULL, 0x510e527fade682d1ULL, 0x9b05688c2b3e6c1fULL,
        0x1f83d9abfb41bd6bULL, 0x5be0cd19137e2179ULL};
    static constexpr std::array<std::uint64_t, 8> sha384_initial{
        0xcbbb9d5dc1059ed8ULL, 0x629a292a367cd507ULL, 0x9159015a3070dd17ULL,
        0x152fecd8f70e5939ULL, 0x67332667ffc00b31ULL, 0x8eb44a8768581511ULL,
        0xdb0c2e0d64f98fa7ULL, 0x47b5481dbefa4fa4ULL};

    std::array<std::uint64_t, 8> state = truncated ? sha384_initial : sha512_initial;
    const Bytes message = pad(data, 128, 16);
    std::array<std::uint64_t, 80> words{};

    for (std::size_t offset = 0; offset < message.size(); offset += 128) {
        for (std::size_t index = 0; index < 16; ++index) {
            std::uint64_t word = 0;
            for (std::size_t byte = 0; byte < 8; ++byte) {
                word = (word << 8U) | message[offset + index * 8 + byte];
            }
            words[index] = word;
        }
        for (std::size_t index = 16; index < 80; ++index) {
            const std::uint64_t s0 = rotr64(words[index - 15], 1) ^
                                     rotr64(words[index - 15], 8) ^
                                     (words[index - 15] >> 7U);
            const std::uint64_t s1 = rotr64(words[index - 2], 19) ^
                                     rotr64(words[index - 2], 61) ^
                                     (words[index - 2] >> 6U);
            words[index] = words[index - 16] + s0 + words[index - 7] + s1;
        }

        std::uint64_t a = state[0];
        std::uint64_t b = state[1];
        std::uint64_t c = state[2];
        std::uint64_t d = state[3];
        std::uint64_t e = state[4];
        std::uint64_t f = state[5];
        std::uint64_t g = state[6];
        std::uint64_t h = state[7];

        for (std::size_t index = 0; index < 80; ++index) {
            const std::uint64_t s1 = rotr64(e, 14) ^ rotr64(e, 18) ^ rotr64(e, 41);
            const std::uint64_t choose = (e & f) ^ ((~e) & g);
            const std::uint64_t temporary1 =
                h + s1 + choose + constants[index] + words[index];
            const std::uint64_t s0 = rotr64(a, 28) ^ rotr64(a, 34) ^ rotr64(a, 39);
            const std::uint64_t majority = (a & b) ^ (a & c) ^ (b & c);
            const std::uint64_t temporary2 = s0 + majority;
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

    Bytes output;
    const std::size_t words_to_emit = truncated ? 6 : 8;
    output.reserve(words_to_emit * 8);
    for (std::size_t index = 0; index < words_to_emit; ++index) {
        for (int shift = 56; shift >= 0; shift -= 8) {
            output.push_back(static_cast<std::uint8_t>(
                state[index] >> static_cast<unsigned int>(shift)));
        }
    }
    return output;
}

struct BigUInt {
    std::vector<std::uint32_t> limbs;

    [[nodiscard]] bool is_zero() const noexcept {
        return limbs.empty();
    }

    void trim() {
        while (!limbs.empty() && limbs.back() == 0U) {
            limbs.pop_back();
        }
    }

    static BigUInt from_be(std::span<const std::uint8_t> bytes) {
        BigUInt value;
        const std::size_t count = (bytes.size() + 3U) / 4U;
        value.limbs.resize(count, 0U);
        for (std::size_t index = 0; index < bytes.size(); ++index) {
            const std::size_t from_end = bytes.size() - 1U - index;
            value.limbs[index / 4U] |=
                static_cast<std::uint32_t>(bytes[from_end]) << ((index % 4U) * 8U);
        }
        value.trim();
        return value;
    }

    [[nodiscard]] Bytes to_be(std::size_t width) const {
        Bytes output(width, 0U);
        for (std::size_t index = 0; index < limbs.size(); ++index) {
            for (std::size_t byte = 0; byte < 4; ++byte) {
                const std::size_t position = index * 4U + byte;
                if (position >= width) {
                    throw std::invalid_argument("integer does not fit requested width");
                }
                output[width - 1U - position] =
                    static_cast<std::uint8_t>(limbs[index] >> (byte * 8U));
            }
        }
        return output;
    }

    [[nodiscard]] int compare(const BigUInt& other) const {
        if (limbs.size() != other.limbs.size()) {
            return limbs.size() < other.limbs.size() ? -1 : 1;
        }
        for (std::size_t index = limbs.size(); index > 0; --index) {
            if (limbs[index - 1U] != other.limbs[index - 1U]) {
                return limbs[index - 1U] < other.limbs[index - 1U] ? -1 : 1;
            }
        }
        return 0;
    }

    static BigUInt add(const BigUInt& left, const BigUInt& right) {
        BigUInt result;
        const std::size_t count = std::max(left.limbs.size(), right.limbs.size());
        result.limbs.resize(count, 0U);
        std::uint64_t carry = 0;
        for (std::size_t index = 0; index < count; ++index) {
            const std::uint64_t sum =
                carry +
                (index < left.limbs.size() ? left.limbs[index] : 0U) +
                (index < right.limbs.size() ? right.limbs[index] : 0U);
            result.limbs[index] = static_cast<std::uint32_t>(sum);
            carry = sum >> 32U;
        }
        if (carry != 0) {
            result.limbs.push_back(static_cast<std::uint32_t>(carry));
        }
        result.trim();
        return result;
    }

    static BigUInt subtract(const BigUInt& left, const BigUInt& right) {
        BigUInt result;
        result.limbs.resize(left.limbs.size(), 0U);
        std::uint64_t borrow = 0;
        for (std::size_t index = 0; index < left.limbs.size(); ++index) {
            const std::uint64_t subtrahend =
                (index < right.limbs.size() ? right.limbs[index] : 0U) + borrow;
            const std::uint64_t minuend = left.limbs[index];
            if (minuend < subtrahend) {
                result.limbs[index] =
                    static_cast<std::uint32_t>(
                        (minuend + (std::uint64_t{1} << 32U) - subtrahend) &
                        0xffffffffULL);
                borrow = 1;
            } else {
                result.limbs[index] =
                    static_cast<std::uint32_t>(minuend - subtrahend);
                borrow = 0;
            }
        }
        if (borrow != 0) {
            throw std::invalid_argument("unsigned subtraction underflow");
        }
        result.trim();
        return result;
    }

    static BigUInt multiply(const BigUInt& left, const BigUInt& right) {
        if (left.is_zero() || right.is_zero()) {
            return {};
        }
        BigUInt result;
        result.limbs.assign(left.limbs.size() + right.limbs.size(), 0U);
        for (std::size_t i = 0; i < left.limbs.size(); ++i) {
            std::uint64_t carry = 0;
            for (std::size_t j = 0; j < right.limbs.size(); ++j) {
                const std::uint64_t partial =
                    static_cast<std::uint64_t>(left.limbs[i]) * right.limbs[j] +
                    result.limbs[i + j] + carry;
                result.limbs[i + j] = static_cast<std::uint32_t>(partial);
                carry = partial >> 32U;
            }
            result.limbs[i + right.limbs.size()] =
                static_cast<std::uint32_t>(carry);
        }
        result.trim();
        return result;
    }

    void shift_left_one() {
        std::uint32_t carry = 0;
        for (std::uint32_t& limb : limbs) {
            const std::uint32_t next_carry = limb >> 31U;
            limb = (limb << 1U) | carry;
            carry = next_carry;
        }
        if (carry != 0U) {
            limbs.push_back(carry);
        }
    }

    void set_bit(std::size_t bit) {
        const std::size_t index = bit / 32U;
        if (index >= limbs.size()) {
            limbs.resize(index + 1U, 0U);
        }
        limbs[index] |= static_cast<std::uint32_t>(1U << (bit % 32U));
    }

    [[nodiscard]] bool bit(std::size_t index) const {
        return index / 32U < limbs.size() &&
               ((limbs[index / 32U] >> (index % 32U)) & 1U) != 0U;
    }

    [[nodiscard]] std::size_t bit_length() const {
        if (limbs.empty()) {
            return 0;
        }
        std::uint32_t top = limbs.back();
        unsigned int bits = 0;
        while (top != 0U) {
            ++bits;
            top >>= 1U;
        }
        return (limbs.size() - 1U) * 32U + bits;
    }
};

std::pair<BigUInt, BigUInt> divmod(const BigUInt& numerator,
                                   const BigUInt& denominator) {
    if (denominator.is_zero()) {
        throw std::invalid_argument("division by zero");
    }
    if (numerator.compare(denominator) < 0) {
        return {BigUInt{}, numerator};
    }

    BigUInt quotient;
    BigUInt remainder;
    const std::size_t bits = numerator.bit_length();
    for (std::size_t index = bits; index > 0; --index) {
        const std::size_t bit_index = index - 1U;
        remainder.shift_left_one();
        if (numerator.bit(bit_index)) {
            remainder.set_bit(0U);
        }
        if (remainder.compare(denominator) >= 0) {
            remainder = BigUInt::subtract(remainder, denominator);
            quotient.set_bit(bit_index);
        }
    }
    quotient.trim();
    remainder.trim();
    return {quotient, remainder};
}

BigUInt mod_mul(const BigUInt& left,
                const BigUInt& right,
                const BigUInt& modulus) {
    return divmod(BigUInt::multiply(left, right), modulus).second;
}

BigUInt mod_pow(const BigUInt& base,
                std::uint32_t exponent,
                const BigUInt& modulus) {
    if (modulus.is_zero()) {
        throw std::invalid_argument("zero modulus");
    }
    const BigUInt reduced_base = divmod(base, modulus).second;
    BigUInt result;
    result.limbs = {1U};
    if (exponent == 0U) {
        return divmod(result, modulus).second;
    }
    const unsigned int top_bit =
        31U - static_cast<unsigned int>(std::countl_zero(exponent));
    for (int bit = static_cast<int>(top_bit); bit >= 0; --bit) {
        result = mod_mul(result, result, modulus);
        if (((exponent >> static_cast<unsigned int>(bit)) & 1U) != 0U) {
            result = mod_mul(result, reduced_base, modulus);
        }
    }
    return result;
}

bool valid_rsa_key(const RsaPublicKey& key, std::string& detail) {
    if (key.modulus.size() < 256U) {
        detail = "RSA AK modulus must be at least 2048 bits";
        return false;
    }
    if (key.modulus.front() == 0U || (key.modulus.back() & 1U) == 0U) {
        detail = "invalid RSA AK modulus";
        return false;
    }
    if (key.exponent < 3U || (key.exponent & 1U) == 0U) {
        detail = "invalid RSA AK exponent";
        return false;
    }
    return true;
}

Bytes digest_info_prefix(HashAlgorithm algorithm) {
    switch (algorithm) {
    case HashAlgorithm::Sha1:
        return {0x30U, 0x21U, 0x30U, 0x09U, 0x06U, 0x05U, 0x2bU, 0x0eU,
                0x03U, 0x02U, 0x1aU, 0x05U, 0x00U, 0x04U, 0x14U};
    case HashAlgorithm::Sha256:
        return {0x30U, 0x31U, 0x30U, 0x0dU, 0x06U, 0x09U, 0x60U, 0x86U, 0x48U,
                0x01U, 0x65U, 0x03U, 0x04U, 0x02U, 0x01U, 0x05U, 0x00U, 0x04U,
                0x20U};
    case HashAlgorithm::Sha384:
        return {0x30U, 0x41U, 0x30U, 0x0dU, 0x06U, 0x09U, 0x60U, 0x86U, 0x48U,
                0x01U, 0x65U, 0x03U, 0x04U, 0x02U, 0x02U, 0x05U, 0x00U, 0x04U,
                0x30U};
    case HashAlgorithm::Sha512:
        return {0x30U, 0x51U, 0x30U, 0x0dU, 0x06U, 0x09U, 0x60U, 0x86U, 0x48U,
                0x01U, 0x65U, 0x03U, 0x04U, 0x02U, 0x03U, 0x05U, 0x00U, 0x04U,
                0x40U};
    }
    return {};
}

bool verify_rsa_pkcs1(const RsaPublicKey& key,
                      HashAlgorithm hash_algorithm,
                      std::span<const std::uint8_t> message,
                      std::span<const std::uint8_t> signature) {
    if (signature.size() != key.modulus.size()) {
        return false;
    }
    const std::size_t hash_length = digest_size(hash_algorithm);
    if (hash_length == 0U) {
        return false;
    }

    const BigUInt modulus = BigUInt::from_be(key.modulus);
    const BigUInt signature_value = BigUInt::from_be(signature);
    if (signature_value.compare(modulus) >= 0) {
        return false;
    }

    const BigUInt encoded = mod_pow(signature_value, key.exponent, modulus);
    const Bytes actual = encoded.to_be(key.modulus.size());
    const Bytes prefix = digest_info_prefix(hash_algorithm);
    const Bytes digest = compute_digest(hash_algorithm, message);
    const std::size_t digest_info_length = prefix.size() + digest.size();
    if (key.modulus.size() < digest_info_length + 11U) {
        return false;
    }

    Bytes expected(key.modulus.size(), 0xffU);
    expected[0] = 0x00U;
    expected[1] = 0x01U;
    expected[key.modulus.size() - digest_info_length - 1U] = 0x00U;
    std::copy(prefix.begin(), prefix.end(),
              expected.begin() + static_cast<std::ptrdiff_t>(
                                     key.modulus.size() - digest_info_length));
    std::copy(digest.begin(), digest.end(),
              expected.begin() + static_cast<std::ptrdiff_t>(
                                     key.modulus.size() - digest.size()));
    return actual == expected;
}

VerificationResult failure(ValidationError error,
                           std::string detail,
                           std::size_t event_index = kNoEvent) {
    VerificationResult result;
    result.error = error;
    result.detail = std::move(detail);
    result.event_index = event_index;
    return result;
}

bool supported_algorithm(HashAlgorithm algorithm) {
    return digest_size(algorithm) != 0U;
}

VerificationResult validate_policy(const MeasurementPolicy& policy,
                                   std::vector<HashAlgorithm>& banks) {
    if (policy.id.empty()) {
        return failure(ValidationError::InvalidPolicy, "measurement policy id is empty");
    }
    if (policy.expected_events.empty()) {
        return failure(ValidationError::InvalidPolicy, "measurement policy has no events");
    }

    const bool explicit_banks = !policy.pcr_banks.empty();
    std::set<HashAlgorithm> bank_set;
    for (HashAlgorithm bank : policy.pcr_banks) {
        if (!supported_algorithm(bank)) {
            return failure(ValidationError::UnsupportedAlgorithm, "unsupported PCR bank");
        }
        if (!bank_set.insert(bank).second) {
            return failure(ValidationError::InvalidPolicy, "duplicate PCR bank");
        }
    }

    std::set<HashAlgorithm> observed_banks;
    for (const MeasurementEvent& event : policy.expected_events) {
        if (event.type.empty()) {
            return failure(ValidationError::InvalidPolicy, "expected event type is empty");
        }
        if (event.digests.empty()) {
            return failure(ValidationError::InvalidPolicy,
                           "expected event has no digests");
        }
        for (const EventDigest& digest : event.digests) {
            if (!supported_algorithm(digest.algorithm)) {
                return failure(ValidationError::UnsupportedAlgorithm,
                               "unsupported expected digest algorithm");
            }
            observed_banks.insert(digest.algorithm);
        }
    }

    if (!explicit_banks) {
        bank_set = observed_banks;
    }
    banks.assign(bank_set.begin(), bank_set.end());
    if (banks.empty()) {
        return failure(ValidationError::InvalidPolicy, "measurement policy has no PCR banks");
    }
    if (!std::includes(bank_set.begin(), bank_set.end(),
                       observed_banks.begin(), observed_banks.end())) {
        return failure(ValidationError::InvalidPolicy,
                       "expected event uses a PCR bank outside the policy");
    }

    for (const MeasurementEvent& event : policy.expected_events) {
        if (event.digests.size() != banks.size()) {
            return failure(ValidationError::InvalidPolicy,
                           "expected event does not cover every PCR bank");
        }
        std::set<HashAlgorithm> event_algorithms;
        for (const EventDigest& digest : event.digests) {
            if (!event_algorithms.insert(digest.algorithm).second) {
                return failure(ValidationError::InvalidPolicy,
                               "expected event has duplicate digest algorithm");
            }
            if (digest.value.size() != digest_size(digest.algorithm)) {
                return failure(ValidationError::InvalidPolicy,
                               "expected event digest has an invalid length");
            }
        }
        for (HashAlgorithm bank : banks) {
            if (!event_algorithms.contains(bank)) {
                return failure(ValidationError::InvalidPolicy,
                               "expected event is missing a required PCR bank");
            }
        }
    }
    return {};
}

const EventDigest* find_digest(const MeasurementEvent& event,
                               HashAlgorithm algorithm) {
    const auto iterator = std::find_if(
        event.digests.begin(), event.digests.end(),
        [algorithm](const EventDigest& digest) {
            return digest.algorithm == algorithm;
        });
    return iterator == event.digests.end() ? nullptr : &*iterator;
}

std::vector<PcrValue> replay_validated(const MeasurementLog& log,
                                       const std::vector<HashAlgorithm>& banks) {
    using PcrKey = std::pair<HashAlgorithm, std::uint32_t>;
    std::map<PcrKey, Bytes> state;

    for (const MeasurementEvent& event : log.events) {
        for (HashAlgorithm bank : banks) {
            state.try_emplace(
                PcrKey{bank, event.pcr_index},
                Bytes(digest_size(bank), 0U));
        }
    }

    for (const MeasurementEvent& event : log.events) {
        for (HashAlgorithm bank : banks) {
            const EventDigest* digest = find_digest(event, bank);
            if (digest == nullptr) {
                throw std::invalid_argument("event is missing a PCR bank digest");
            }
            Bytes& current = state.at(PcrKey{bank, event.pcr_index});
            Bytes input;
            input.reserve(current.size() + digest->value.size());
            input.insert(input.end(), current.begin(), current.end());
            input.insert(input.end(), digest->value.begin(), digest->value.end());
            current = compute_digest(bank, input);
        }
    }

    std::vector<PcrValue> output;
    output.reserve(state.size());
    for (const auto& [key, value] : state) {
        output.push_back(PcrValue{key.first, key.second, value});
    }
    return output;
}

VerificationResult compare_pcrs(std::span<const PcrValue> claimed,
                                const std::vector<PcrValue>& computed) {
    if (claimed.size() != computed.size()) {
        return failure(ValidationError::PcrSelectionMismatch,
                       "PCR set does not match the replayed measurement chain");
    }

    using PcrKey = std::pair<HashAlgorithm, std::uint32_t>;
    std::map<PcrKey, const PcrValue*> claimed_map;
    for (const PcrValue& value : claimed) {
        const PcrKey key{value.algorithm, value.pcr_index};
        if (!claimed_map.emplace(key, &value).second) {
            return failure(ValidationError::PcrSelectionMismatch,
                           "duplicate PCR value in quote");
        }
    }

    for (const PcrValue& computed_value : computed) {
        const PcrKey key{computed_value.algorithm, computed_value.pcr_index};
        const auto iterator = claimed_map.find(key);
        if (iterator == claimed_map.end()) {
            return failure(ValidationError::PcrSelectionMismatch,
                           "quote is missing a replayed PCR value");
        }
        if (iterator->second->value != computed_value.value) {
            return failure(ValidationError::PcrValueMismatch,
                           "PCR " + std::to_string(key.second) + " " +
                               std::string(algorithm_name(key.first)) +
                               " does not match the replayed chain");
        }
    }
    return {};
}

void append_size_prefixed(Bytes& output, std::span<const std::uint8_t> value) {
    if (value.size() > std::numeric_limits<std::uint32_t>::max()) {
        throw std::length_error("quote field is too large");
    }
    append_u32_be(output, static_cast<std::uint32_t>(value.size()));
    output.insert(output.end(), value.begin(), value.end());
}

void append_size_prefixed(Bytes& output, std::string_view value) {
    append_size_prefixed(
        output,
        std::span<const std::uint8_t>(
            reinterpret_cast<const std::uint8_t*>(value.data()), value.size()));
}

VerificationResult validate_quote_policy(const QuotePolicy& policy) {
    if (policy.expected_nonce.empty()) {
        return failure(ValidationError::InvalidPolicy, "expected quote nonce is empty");
    }
    if (policy.policy_id.empty()) {
        return failure(ValidationError::InvalidPolicy, "quote policy id is empty");
    }
    if (policy.trusted_aks.empty()) {
        return failure(ValidationError::InvalidPolicy, "no trusted AKs configured");
    }
    for (const auto& [id, key] : policy.trusted_aks) {
        std::string detail;
        if (id.empty() || !valid_rsa_key(key, detail)) {
            return failure(ValidationError::InvalidPolicy,
                           "trusted AK " + id + ": " + detail);
        }
    }
    std::set<std::pair<HashAlgorithm, std::uint32_t>> selections;
    for (const PcrSelection& selection : policy.pcr_selection) {
        if (!supported_algorithm(selection.algorithm)) {
            return failure(ValidationError::UnsupportedAlgorithm,
                           "unsupported PCR selection algorithm");
        }
        if (!selections.emplace(selection.algorithm, selection.pcr_index).second) {
            return failure(ValidationError::InvalidPolicy,
                           "duplicate PCR selection");
        }
    }
    return {};
}

VerificationResult compare_selection(const std::vector<PcrValue>& pcrs,
                                     const std::vector<PcrSelection>& selection) {
    if (pcrs.size() != selection.size()) {
        return failure(ValidationError::PcrSelectionMismatch,
                       "quote PCR selection does not match policy");
    }
    std::set<std::pair<HashAlgorithm, std::uint32_t>> actual;
    for (const PcrValue& value : pcrs) {
        if (!actual.emplace(value.algorithm, value.pcr_index).second) {
            return failure(ValidationError::PcrSelectionMismatch,
                           "duplicate PCR value in quote");
        }
    }
    for (const PcrSelection& expected : selection) {
        if (!actual.contains({expected.algorithm, expected.pcr_index})) {
            return failure(ValidationError::PcrSelectionMismatch,
                           "quote does not contain the required PCR selection");
        }
    }
    return {};
}

VerificationResult validate_quote_structure(const Quote& quote) {
    std::set<std::pair<HashAlgorithm, std::uint32_t>> keys;
    for (const PcrValue& value : quote.pcrs) {
        if (!supported_algorithm(value.algorithm)) {
            return failure(ValidationError::UnsupportedAlgorithm,
                           "quote contains an unsupported PCR algorithm");
        }
        if (!keys.emplace(value.algorithm, value.pcr_index).second) {
            return failure(ValidationError::PcrSelectionMismatch,
                           "quote contains a duplicate PCR value");
        }
        if (value.value.size() != digest_size(value.algorithm)) {
            return failure(ValidationError::PcrValueMismatch,
                           "quote contains a PCR value with an invalid length");
        }
    }
    return {};
}

}  // namespace

Bytes sha384(std::span<const std::uint8_t> data) {
    return sha512_family(data, true);
}

Bytes sha512(std::span<const std::uint8_t> data) {
    return sha512_family(data, false);
}

Bytes compute_digest(HashAlgorithm algorithm, std::span<const std::uint8_t> data) {
    switch (algorithm) {
    case HashAlgorithm::Sha1:
        return sha1(data);
    case HashAlgorithm::Sha256:
        return sha256(data);
    case HashAlgorithm::Sha384:
        return sha384(data);
    case HashAlgorithm::Sha512:
        return sha512(data);
    }
    throw std::invalid_argument("unsupported hash algorithm");
}

Bytes quote_signing_payload(const Quote& quote) {
    static constexpr std::string_view magic = "NETSEC-QUOTE-V1";
    Bytes payload(magic.begin(), magic.end());
    payload.push_back(static_cast<std::uint8_t>(quote.signature_hash));
    append_size_prefixed(payload, quote.nonce);
    append_size_prefixed(payload, quote.ak_id);
    append_size_prefixed(payload, quote.policy_id);
    if (quote.pcrs.size() > std::numeric_limits<std::uint32_t>::max()) {
        throw std::length_error("too many PCR values");
    }
    append_u32_be(payload, static_cast<std::uint32_t>(quote.pcrs.size()));
    for (const PcrValue& value : quote.pcrs) {
        payload.push_back(static_cast<std::uint8_t>(value.algorithm));
        append_u32_be(payload, value.pcr_index);
        append_size_prefixed(payload, value.value);
    }
    return payload;
}

std::vector<PcrValue> replay_measurement_log(const MeasurementLog& log,
                                             const MeasurementPolicy& policy) {
    const VerificationResult validation = verify_measurement_log(log, policy);
    if (!validation.ok()) {
        throw std::invalid_argument(validation.detail);
    }
    return validation.computed_pcrs;
}

VerificationResult verify_measurement_log(const MeasurementLog& log,
                                          const MeasurementPolicy& policy,
                                          std::span<const PcrValue> claimed_pcrs) {
    std::vector<HashAlgorithm> banks;
    VerificationResult result = validate_policy(policy, banks);
    if (!result.ok()) {
        return result;
    }

    if (log.events.empty()) {
        return failure(ValidationError::EmptyLog, "measurement log is empty");
    }

    std::set<std::string> known_types;
    for (const MeasurementEvent& event : policy.expected_events) {
        known_types.insert(event.type);
    }
    for (std::size_t index = 0; index < log.events.size(); ++index) {
        const MeasurementEvent& event = log.events[index];
        if (!known_types.contains(event.type)) {
            return failure(ValidationError::UnknownEventType,
                           "unknown measurement event type: " + event.type,
                           index);
        }
        for (const EventDigest& digest : event.digests) {
            if (!supported_algorithm(digest.algorithm)) {
                return failure(ValidationError::UnsupportedAlgorithm,
                               "unsupported event digest algorithm",
                               index);
            }
        }
    }

    if (log.events.size() < policy.expected_events.size()) {
        return failure(ValidationError::MissingEvent,
                       "measurement log is missing expected events",
                       log.events.size());
    }
    if (log.events.size() > policy.expected_events.size()) {
        return failure(ValidationError::ExtraEvent,
                       "measurement log contains unexpected events",
                       policy.expected_events.size());
    }

    for (std::size_t index = 0; index < log.events.size(); ++index) {
        const MeasurementEvent& actual = log.events[index];
        const MeasurementEvent& expected = policy.expected_events[index];

        if (actual.type != expected.type) {
            return failure(ValidationError::EventOrderMismatch,
                           "event " + std::to_string(index) + " is " + actual.type +
                               ", expected " + expected.type,
                           index);
        }
        if (actual.pcr_index != expected.pcr_index) {
            return failure(ValidationError::PcrIndexMismatch,
                           "event " + std::to_string(index) +
                               " targets the wrong PCR",
                           index);
        }

        std::set<HashAlgorithm> actual_algorithms;
        for (const EventDigest& digest : actual.digests) {
            if (!actual_algorithms.insert(digest.algorithm).second) {
                return failure(ValidationError::DuplicateDigest,
                               "event " + std::to_string(index) +
                                   " has duplicate digest algorithms",
                               index);
            }
        }

        if (actual.digests.size() < expected.digests.size()) {
            return failure(ValidationError::MissingDigest,
                           "event " + std::to_string(index) +
                               " is missing a bank digest",
                           index);
        }
        if (actual.digests.size() > expected.digests.size()) {
            return failure(ValidationError::UnexpectedDigest,
                           "event " + std::to_string(index) +
                               " has an unexpected bank digest",
                           index);
        }

        for (std::size_t digest_index = 0;
             digest_index < expected.digests.size();
             ++digest_index) {
            const EventDigest& actual_digest = actual.digests[digest_index];
            const EventDigest& expected_digest = expected.digests[digest_index];
            if (actual_digest.algorithm != expected_digest.algorithm) {
                return failure(ValidationError::DigestAlgorithmMismatch,
                               "event " + std::to_string(index) +
                                   " has the wrong digest algorithm",
                               index);
            }
            if (actual_digest.value.size() != digest_size(actual_digest.algorithm)) {
                return failure(ValidationError::DigestLengthMismatch,
                               "event " + std::to_string(index) +
                                   " has an invalid digest length",
                               index);
            }
            if (actual_digest.value != expected_digest.value) {
                return failure(ValidationError::DigestMismatch,
                               "event " + std::to_string(index) + " (" +
                                   actual.type + ") digest does not match policy",
                               index);
            }
        }
    }

    result.computed_pcrs = replay_validated(log, banks);
    if (!claimed_pcrs.empty()) {
        const VerificationResult comparison =
            compare_pcrs(claimed_pcrs, result.computed_pcrs);
        if (!comparison.ok()) {
            VerificationResult failed = comparison;
            failed.computed_pcrs = result.computed_pcrs;
            return failed;
        }
    }
    return result;
}

VerificationResult verify_measurement_chain(const MeasurementLog& log,
                                            const MeasurementPolicy& policy,
                                            std::span<const PcrValue> claimed_pcrs) {
    return verify_measurement_log(log, policy, claimed_pcrs);
}

VerificationResult verify_quote(const Quote& quote,
                                const QuotePolicy& policy,
                                std::span<const PcrValue> expected_pcrs) {
    VerificationResult result = validate_quote_policy(policy);
    if (!result.ok()) {
        return result;
    }
    if (!supported_algorithm(quote.signature_hash)) {
        return failure(ValidationError::UnsupportedAlgorithm,
                       "unsupported quote signature hash");
    }
    const VerificationResult structure = validate_quote_structure(quote);
    if (!structure.ok()) {
        return structure;
    }
    if (quote.nonce != policy.expected_nonce) {
        return failure(ValidationError::QuoteNonceMismatch,
                       "quote nonce does not match policy");
    }
    if (quote.policy_id != policy.policy_id) {
        return failure(ValidationError::QuotePolicyMismatch,
                       "quote policy id does not match policy");
    }
    if (!policy.expected_ak_id.empty() && quote.ak_id != policy.expected_ak_id) {
        return failure(ValidationError::UntrustedAk,
                       "quote was signed by an unexpected AK");
    }

    const auto ak = policy.trusted_aks.find(quote.ak_id);
    if (ak == policy.trusted_aks.end()) {
        return failure(ValidationError::UntrustedAk,
                       "quote AK is not trusted");
    }
    std::string key_detail;
    if (!valid_rsa_key(ak->second, key_detail)) {
        return failure(ValidationError::InvalidAk, key_detail);
    }

    if (!policy.pcr_selection.empty()) {
        const VerificationResult selection =
            compare_selection(quote.pcrs, policy.pcr_selection);
        if (!selection.ok()) {
            return selection;
        }
    }
    if (!expected_pcrs.empty()) {
        const std::vector<PcrValue> expected(expected_pcrs.begin(), expected_pcrs.end());
        const VerificationResult comparison = compare_pcrs(quote.pcrs, expected);
        if (!comparison.ok()) {
            return comparison;
        }
    }

    const Bytes payload = quote_signing_payload(quote);
    if (!verify_rsa_pkcs1(ak->second, quote.signature_hash, payload, quote.signature)) {
        return failure(ValidationError::QuoteSignatureInvalid,
                       "quote signature verification failed");
    }
    return {};
}

VerificationResult verify_attestation(const MeasurementLog& log,
                                      const MeasurementPolicy& measurement_policy,
                                      const Quote& quote,
                                      const QuotePolicy& quote_policy) {
    if (measurement_policy.id != quote_policy.policy_id) {
        return failure(ValidationError::QuotePolicyMismatch,
                       "measurement and quote policies disagree");
    }

    VerificationResult chain = verify_measurement_log(log, measurement_policy);
    if (!chain.ok()) {
        return chain;
    }

    std::vector<PcrValue> expected;
    if (quote_policy.pcr_selection.empty()) {
        expected = chain.computed_pcrs;
    } else {
        std::set<std::pair<HashAlgorithm, std::uint32_t>> seen;
        for (const PcrSelection& selection : quote_policy.pcr_selection) {
            if (!seen.emplace(selection.algorithm, selection.pcr_index).second) {
                VerificationResult failed =
                    failure(ValidationError::InvalidPolicy, "duplicate PCR selection");
                failed.computed_pcrs = chain.computed_pcrs;
                return failed;
            }
            const auto iterator = std::find_if(
                chain.computed_pcrs.begin(), chain.computed_pcrs.end(),
                [&selection](const PcrValue& value) {
                    return value.algorithm == selection.algorithm &&
                           value.pcr_index == selection.pcr_index;
                });
            if (iterator == chain.computed_pcrs.end()) {
                VerificationResult failed = failure(
                    ValidationError::PcrSelectionMismatch,
                    "policy selects a PCR that the measurement chain does not extend");
                failed.computed_pcrs = chain.computed_pcrs;
                return failed;
            }
            expected.push_back(*iterator);
        }
    }

    VerificationResult quote_result = verify_quote(quote, quote_policy, expected);
    quote_result.computed_pcrs = std::move(chain.computed_pcrs);
    return quote_result;
}

MeasurementLogVerifier::MeasurementLogVerifier(MeasurementPolicy policy)
    : policy_(std::move(policy)) {}

const MeasurementPolicy& MeasurementLogVerifier::policy() const noexcept {
    return policy_;
}

VerificationResult MeasurementLogVerifier::verify(
    const MeasurementLog& log,
    std::span<const PcrValue> claimed_pcrs) const {
    return verify_measurement_log(log, policy_, claimed_pcrs);
}

AttestationVerifier::AttestationVerifier(MeasurementPolicy measurement_policy,
                                         QuotePolicy quote_policy)
    : measurement_policy_(std::move(measurement_policy)),
      quote_policy_(std::move(quote_policy)) {}

VerificationResult AttestationVerifier::verify(const MeasurementLog& log,
                                               const Quote& quote) const {
    return verify_attestation(log, measurement_policy_, quote, quote_policy_);
}

}  // namespace netsec
