#include "netsec_crypto.hpp"

#include <algorithm>
#include <array>
#include <cstring>

namespace netsec::detail {
namespace {

// ---------------------------------------------------------------------------
// Keccak-256 (the pre-standard Ethereum variant, padding byte 0x01)
// ---------------------------------------------------------------------------

constexpr std::uint64_t kKeccakRoundConstants[24] = {
    0x0000000000000001ULL, 0x0000000000008082ULL, 0x800000000000808aULL,
    0x8000000080008000ULL, 0x000000000000808bULL, 0x0000000080000001ULL,
    0x8000000080008081ULL, 0x8000000000008009ULL, 0x000000000000008aULL,
    0x0000000000000088ULL, 0x0000000080008009ULL, 0x000000008000000aULL,
    0x000000008000808bULL, 0x800000000000008bULL, 0x8000000000008089ULL,
    0x8000000000008003ULL, 0x8000000000008002ULL, 0x8000000000000080ULL,
    0x000000000000800aULL, 0x800000008000000aULL, 0x8000000080008081ULL,
    0x8000000000008080ULL, 0x0000000080000001ULL, 0x8000000080008008ULL,
};

constexpr unsigned kKeccakRotation[24] = {
    1, 3, 6, 10, 15, 21, 28, 36, 45, 55, 2, 14,
    27, 41, 56, 8, 25, 43, 62, 18, 39, 61, 20, 44,
};

constexpr unsigned kKeccakPi[24] = {
    10, 7, 11, 17, 18, 3, 5, 16, 8, 21, 24, 4,
    15, 23, 19, 13, 12, 2, 20, 14, 22, 9, 6, 1,
};

constexpr std::uint64_t rotl64(std::uint64_t value, unsigned amount) {
    return (value << amount) | (value >> (64U - amount));
}

void keccak_f1600(std::uint64_t state[25]) {
    std::uint64_t bc[5];
    for (unsigned round = 0; round < 24; ++round) {
        for (unsigned x = 0; x < 5; ++x) {
            bc[x] = state[x] ^ state[x + 5] ^ state[x + 10] ^ state[x + 15] ^
                    state[x + 20];
        }
        for (unsigned x = 0; x < 5; ++x) {
            const std::uint64_t delta =
                bc[(x + 4) % 5] ^ rotl64(bc[(x + 1) % 5], 1);
            for (unsigned y = 0; y < 25; y += 5) {
                state[y + x] ^= delta;
            }
        }

        std::uint64_t current = state[1];
        for (unsigned i = 0; i < 24; ++i) {
            const unsigned destination = kKeccakPi[i];
            bc[0] = state[destination];
            state[destination] = rotl64(current, kKeccakRotation[i]);
            current = bc[0];
        }

        for (unsigned y = 0; y < 25; y += 5) {
            for (unsigned x = 0; x < 5; ++x) {
                bc[x] = state[y + x];
            }
            for (unsigned x = 0; x < 5; ++x) {
                state[y + x] =
                    bc[x] ^ ((~bc[(x + 1) % 5]) & bc[(x + 2) % 5]);
            }
        }
        state[0] ^= kKeccakRoundConstants[round];
    }
}

void keccak_absorb_block(std::uint64_t state[25], const std::uint8_t* block) {
    for (unsigned lane = 0; lane < 17; ++lane) {
        std::uint64_t value = 0;
        for (unsigned byte = 0; byte < 8; ++byte) {
            value |= static_cast<std::uint64_t>(block[lane * 8 + byte])
                     << (8U * byte);
        }
        state[lane] ^= value;
    }
    keccak_f1600(state);
}

// ---------------------------------------------------------------------------
// Minimal 256-bit arithmetic for public-key recovery. The data is public, so
// variable-time arithmetic is acceptable here; no secret scalar is processed.
// ---------------------------------------------------------------------------

struct U256 {
    std::uint64_t word[4]{};  // little endian
};

constexpr U256 kFieldPrime{{
    0xfffffffefffffc2fULL,
    0xffffffffffffffffULL,
    0xffffffffffffffffULL,
    0xffffffffffffffffULL,
}};

constexpr U256 kCurveOrder{{
    0xbfd25e8cd0364141ULL,
    0xbaaedce6af48a03bULL,
    0xfffffffffffffffeULL,
    0xffffffffffffffffULL,
}};

constexpr U256 kHalfCurveOrder{{
    0xdfe92f46681b20a0ULL,
    0x5d576e7357a4501dULL,
    0xffffffffffffffffULL,
    0x7fffffffffffffffULL,
}};

constexpr U256 kGeneratorX{{
    0x59f2815b16f81798ULL,
    0x029bfcdb2dce28d9ULL,
    0x55a06295ce870b07ULL,
    0x79be667ef9dcbbacULL,
}};

constexpr U256 kGeneratorY{{
    0x9c47d08ffb10d4b8ULL,
    0xfd17b448a6855419ULL,
    0x5da4fbfc0e1108a8ULL,
    0x483ada7726a3c465ULL,
}};

constexpr U256 kSqrtExponent{{
    0xffffffffbfffff0cULL,
    0xffffffffffffffffULL,
    0xffffffffffffffffULL,
    0x3fffffffffffffffULL,
}};

int compare(const U256& left, const U256& right) {
    for (int index = 3; index >= 0; --index) {
        if (left.word[index] < right.word[index]) {
            return -1;
        }
        if (left.word[index] > right.word[index]) {
            return 1;
        }
    }
    return 0;
}

// Portable 64x64 -> 128 multiplication. This avoids __int128 so the component
// can be compiled for 32-bit firmware targets where that extension is absent.
void multiply_64x64(std::uint64_t left,
                    std::uint64_t right,
                    std::uint64_t& high,
                    std::uint64_t& low) {
    const std::uint64_t left_low = left & 0xffffffffULL;
    const std::uint64_t left_high = left >> 32U;
    const std::uint64_t right_low = right & 0xffffffffULL;
    const std::uint64_t right_high = right >> 32U;

    const std::uint64_t p0 = left_low * right_low;
    const std::uint64_t p1 = left_low * right_high;
    const std::uint64_t p2 = left_high * right_low;
    const std::uint64_t p3 = left_high * right_high;
    const std::uint64_t middle =
        (p0 >> 32U) + (p1 & 0xffffffffULL) + (p2 & 0xffffffffULL);
    low = (middle << 32U) | (p0 & 0xffffffffULL);
    high = p3 + (p1 >> 32U) + (p2 >> 32U) + (middle >> 32U);
}

std::uint64_t add_limbs(std::uint64_t left,
                        std::uint64_t right,
                        std::uint64_t carry,
                        std::uint64_t& result) {
    result = left + right;
    std::uint64_t next = result < left ? 1U : 0U;
    const std::uint64_t with_carry = result + carry;
    if (with_carry < result) {
        ++next;
    }
    result = with_carry;
    return next;
}

std::uint64_t subtract_limbs(std::uint64_t left,
                             std::uint64_t right,
                             std::uint64_t borrow,
                             std::uint64_t& result) {
    const std::uint64_t subtrahend = right + borrow;
    const bool subtrahend_wrapped = subtrahend < right;
    result = left - subtrahend;
    return (subtrahend_wrapped || left < subtrahend) ? 1U : 0U;
}

bool is_zero(const U256& value) {
    return (value.word[0] | value.word[1] | value.word[2] | value.word[3]) == 0;
}

bool equal(const U256& left, const U256& right) {
    return compare(left, right) == 0;
}

U256 from_be(const std::uint8_t* bytes) {
    U256 value;
    for (unsigned word = 0; word < 4; ++word) {
        std::uint64_t lane = 0;
        for (unsigned byte = 0; byte < 8; ++byte) {
            lane = (lane << 8) | bytes[word * 8 + byte];
        }
        value.word[3 - word] = lane;
    }
    return value;
}

U256 from_be(const std::array<std::uint8_t, 32>& bytes) {
    return from_be(bytes.data());
}

std::array<std::uint8_t, 32> to_be(const U256& value) {
    std::array<std::uint8_t, 32> bytes{};
    for (unsigned word = 0; word < 4; ++word) {
        std::uint64_t lane = value.word[3 - word];
        for (unsigned byte = 0; byte < 8; ++byte) {
            bytes[word * 8 + byte] =
                static_cast<std::uint8_t>(lane >> (56U - 8U * byte));
        }
    }
    return bytes;
}

U256 add_mod(const U256& left, const U256& right, const U256& modulus) {
    U256 sum;
    std::uint64_t carry = 0;
    for (unsigned index = 0; index < 4; ++index) {
        carry = add_limbs(left.word[index], right.word[index], carry,
                          sum.word[index]);
    }

    if (carry != 0 || compare(sum, modulus) >= 0) {
        std::uint64_t borrow = 0;
        for (unsigned index = 0; index < 4; ++index) {
            borrow = subtract_limbs(sum.word[index], modulus.word[index],
                                    borrow, sum.word[index]);
        }
    }
    return sum;
}

U256 sub_mod(const U256& left, const U256& right, const U256& modulus) {
    U256 difference;
    std::uint64_t borrow = 0;
    for (unsigned index = 0; index < 4; ++index) {
        borrow = subtract_limbs(left.word[index], right.word[index], borrow,
                                difference.word[index]);
    }
    if (borrow != 0) {
        std::uint64_t carry = 0;
        for (unsigned index = 0; index < 4; ++index) {
            carry = add_limbs(difference.word[index], modulus.word[index],
                              carry, difference.word[index]);
        }
    }
    return difference;
}

U256 negate_mod(const U256& value, const U256& modulus) {
    return is_zero(value) ? U256{} : sub_mod(modulus, value, modulus);
}

U256 multiply_small(const U256& value, unsigned factor, const U256& modulus) {
    U256 result;
    for (unsigned index = 0; index < factor; ++index) {
        result = add_mod(result, value, modulus);
    }
    return result;
}

struct U512 {
    std::uint64_t word[8]{};
};

U512 multiply_full(const U256& left, const U256& right) {
    U512 product;
    for (unsigned i = 0; i < 4; ++i) {
        std::uint64_t carry = 0;
        for (unsigned j = 0; j < 4; ++j) {
            std::uint64_t high = 0;
            std::uint64_t low = 0;
            multiply_64x64(left.word[i], right.word[j], high, low);

            std::uint64_t next = 0;
            const std::uint64_t carry_out =
                add_limbs(low, product.word[i + j], carry, next);
            product.word[i + j] = next;
            carry = high + carry_out;
        }
        product.word[i + 4] += carry;
    }
    return product;
}

void subtract_modulus_5(std::uint64_t result[5], const U256& modulus) {
    std::uint64_t borrow = 0;
    for (unsigned index = 0; index < 4; ++index) {
        borrow = subtract_limbs(result[index], modulus.word[index], borrow,
                                result[index]);
    }
    result[4] -= borrow;
}

U256 reduce_512(const U512& input, const U256& modulus) {
    U256 remainder;
    for (int bit = 511; bit >= 0; --bit) {
        const std::uint64_t high = remainder.word[3] >> 63U;
        remainder.word[3] =
            (remainder.word[3] << 1U) | (remainder.word[2] >> 63U);
        remainder.word[2] =
            (remainder.word[2] << 1U) | (remainder.word[1] >> 63U);
        remainder.word[1] =
            (remainder.word[1] << 1U) | (remainder.word[0] >> 63U);
        remainder.word[0] =
            (remainder.word[0] << 1U) |
            ((input.word[static_cast<unsigned>(bit) / 64U] >>
              (static_cast<unsigned>(bit) % 64U)) &
             1U);

        if (high != 0 || compare(remainder, modulus) >= 0) {
            std::uint64_t extended[5] = {
                remainder.word[0], remainder.word[1], remainder.word[2],
                remainder.word[3], high};
            subtract_modulus_5(extended, modulus);
            remainder.word[0] = extended[0];
            remainder.word[1] = extended[1];
            remainder.word[2] = extended[2];
            remainder.word[3] = extended[3];
        }
    }
    return remainder;
}

U256 multiply_mod(const U256& left, const U256& right, const U256& modulus) {
    return reduce_512(multiply_full(left, right), modulus);
}

U256 power_mod(U256 base, U256 exponent, const U256& modulus) {
    U256 result = U256{{1, 0, 0, 0}};
    for (unsigned bit = 0; bit < 256; ++bit) {
        if (((exponent.word[bit / 64U] >> (bit % 64U)) & 1U) != 0) {
            result = multiply_mod(result, base, modulus);
        }
        base = multiply_mod(base, base, modulus);
    }
    return result;
}

U256 inverse_mod(const U256& value, const U256& prime) {
    return power_mod(value, sub_mod(prime, U256{{2, 0, 0, 0}}, prime), prime);
}

U256 reduce_modulus(const U256& value, const U256& modulus) {
    if (compare(value, modulus) < 0) {
        return value;
    }
    U512 extended;
    for (unsigned index = 0; index < 4; ++index) {
        extended.word[index] = value.word[index];
    }
    return reduce_512(extended, modulus);
}

struct AffinePoint {
    U256 x;
    U256 y;
    bool infinity = true;
};

struct JacobianPoint {
    U256 x;
    U256 y;
    U256 z;
    bool infinity = true;
};

JacobianPoint double_point(const JacobianPoint& point) {
    if (point.infinity || is_zero(point.y)) {
        return {};
    }

    const U256 y_squared = multiply_mod(point.y, point.y, kFieldPrime);
    const U256 s = multiply_small(
        multiply_mod(point.x, y_squared, kFieldPrime), 4, kFieldPrime);
    const U256 m = multiply_small(
        multiply_mod(point.x, point.x, kFieldPrime), 3, kFieldPrime);
    const U256 m_squared = multiply_mod(m, m, kFieldPrime);

    JacobianPoint result;
    result.infinity = false;
    result.x =
        sub_mod(m_squared, multiply_small(s, 2, kFieldPrime), kFieldPrime);
    const U256 y_fourth = multiply_mod(y_squared, y_squared, kFieldPrime);
    result.y = sub_mod(
        multiply_mod(m, sub_mod(s, result.x, kFieldPrime), kFieldPrime),
        multiply_small(y_fourth, 8, kFieldPrime), kFieldPrime);
    result.z = multiply_small(
        multiply_mod(point.y, point.z, kFieldPrime), 2, kFieldPrime);
    return result;
}

JacobianPoint add_points(const JacobianPoint& left, const JacobianPoint& right) {
    if (left.infinity) {
        return right;
    }
    if (right.infinity) {
        return left;
    }

    const U256 z1_squared = multiply_mod(left.z, left.z, kFieldPrime);
    const U256 z2_squared = multiply_mod(right.z, right.z, kFieldPrime);
    const U256 u1 = multiply_mod(left.x, z2_squared, kFieldPrime);
    const U256 u2 = multiply_mod(right.x, z1_squared, kFieldPrime);
    const U256 s1 = multiply_mod(
        multiply_mod(left.y, z2_squared, kFieldPrime), right.z, kFieldPrime);
    const U256 s2 = multiply_mod(
        multiply_mod(right.y, z1_squared, kFieldPrime), left.z, kFieldPrime);
    const U256 h = sub_mod(u2, u1, kFieldPrime);
    const U256 r = sub_mod(s2, s1, kFieldPrime);

    if (is_zero(h)) {
        return is_zero(r) ? double_point(left) : JacobianPoint{};
    }

    const U256 h_squared = multiply_mod(h, h, kFieldPrime);
    const U256 h_cubed = multiply_mod(h, h_squared, kFieldPrime);
    const U256 v = multiply_mod(u1, h_squared, kFieldPrime);

    JacobianPoint result;
    result.infinity = false;
    result.x = sub_mod(
        sub_mod(multiply_mod(r, r, kFieldPrime), h_cubed, kFieldPrime),
        multiply_small(v, 2, kFieldPrime), kFieldPrime);
    result.y = sub_mod(
        multiply_mod(r, sub_mod(v, result.x, kFieldPrime), kFieldPrime),
        multiply_mod(s1, h_cubed, kFieldPrime), kFieldPrime);
    result.z = multiply_mod(multiply_mod(left.z, right.z, kFieldPrime), h,
                            kFieldPrime);
    return result;
}

JacobianPoint add_mixed(const JacobianPoint& left, const AffinePoint& right) {
    if (right.infinity) {
        return left;
    }
    if (left.infinity) {
        return JacobianPoint{right.x, right.y, U256{{1, 0, 0, 0}}, false};
    }

    const U256 z1_squared = multiply_mod(left.z, left.z, kFieldPrime);
    const U256 u2 = multiply_mod(right.x, z1_squared, kFieldPrime);
    const U256 s2 = multiply_mod(
        multiply_mod(right.y, z1_squared, kFieldPrime), left.z, kFieldPrime);
    const U256 h = sub_mod(u2, left.x, kFieldPrime);
    const U256 r = sub_mod(s2, left.y, kFieldPrime);

    if (is_zero(h)) {
        return is_zero(r) ? double_point(left) : JacobianPoint{};
    }

    const U256 h_squared = multiply_mod(h, h, kFieldPrime);
    const U256 h_cubed = multiply_mod(h, h_squared, kFieldPrime);
    const U256 v = multiply_mod(left.x, h_squared, kFieldPrime);

    JacobianPoint result;
    result.infinity = false;
    result.x = sub_mod(
        sub_mod(multiply_mod(r, r, kFieldPrime), h_cubed, kFieldPrime),
        multiply_small(v, 2, kFieldPrime), kFieldPrime);
    result.y = sub_mod(
        multiply_mod(r, sub_mod(v, result.x, kFieldPrime), kFieldPrime),
        multiply_mod(left.y, h_cubed, kFieldPrime), kFieldPrime);
    result.z = multiply_mod(left.z, h, kFieldPrime);
    return result;
}

AffinePoint to_affine(const JacobianPoint& point) {
    if (point.infinity || is_zero(point.z)) {
        return {};
    }
    const U256 z_inverse = inverse_mod(point.z, kFieldPrime);
    const U256 z_inverse_squared =
        multiply_mod(z_inverse, z_inverse, kFieldPrime);
    AffinePoint result;
    result.infinity = false;
    result.x = multiply_mod(point.x, z_inverse_squared, kFieldPrime);
    result.y = multiply_mod(
        multiply_mod(point.y, z_inverse_squared, kFieldPrime), z_inverse,
        kFieldPrime);
    return result;
}

AffinePoint scalar_multiply(const AffinePoint& point, const U256& scalar) {
    JacobianPoint result;
    for (int bit = 255; bit >= 0; --bit) {
        result = double_point(result);
        if (((scalar.word[static_cast<unsigned>(bit) / 64U] >>
              (static_cast<unsigned>(bit) % 64U)) &
             1U) != 0) {
            result = add_mixed(result, point);
        }
    }
    return to_affine(result);
}

std::optional<AffinePoint> decompress_point(const U256& x, unsigned parity) {
    if (compare(x, kFieldPrime) >= 0) {
        return std::nullopt;
    }

    const U256 x_squared = multiply_mod(x, x, kFieldPrime);
    const U256 x_cubed = multiply_mod(x_squared, x, kFieldPrime);
    const U256 right_hand_side =
        add_mod(x_cubed, U256{{7, 0, 0, 0}}, kFieldPrime);
    U256 y = power_mod(right_hand_side, kSqrtExponent, kFieldPrime);
    if (!equal(multiply_mod(y, y, kFieldPrime), right_hand_side)) {
        return std::nullopt;
    }
    if ((y.word[0] & 1U) != (parity & 1U)) {
        y = sub_mod(kFieldPrime, y, kFieldPrime);
    }
    return AffinePoint{x, y, false};
}

AffinePoint generator() {
    return AffinePoint{kGeneratorX, kGeneratorY, false};
}

std::optional<Address> recover_address_impl(const Hash256& digest,
                                            const CompactSignature& signature,
                                            bool require_low_s) {
    if (signature.recovery_id > 1) {
        return std::nullopt;
    }

    const U256 r = from_be(signature.bytes.data());
    const U256 s = from_be(signature.bytes.data() + 32);
    if (is_zero(r) || is_zero(s)) {
        return std::nullopt;
    }
    if (compare(r, kCurveOrder) >= 0 || compare(s, kCurveOrder) >= 0) {
        return std::nullopt;
    }
    if (require_low_s && compare(s, kHalfCurveOrder) > 0) {
        return std::nullopt;
    }

    const std::optional<AffinePoint> r_point =
        decompress_point(r, signature.recovery_id & 1U);
    if (!r_point.has_value()) {
        return std::nullopt;
    }

    const U256 z = reduce_modulus(from_be(digest.bytes), kCurveOrder);
    const U256 r_inverse = inverse_mod(r, kCurveOrder);
    const U256 u1 = negate_mod(
        multiply_mod(z, r_inverse, kCurveOrder), kCurveOrder);
    const U256 u2 = multiply_mod(s, r_inverse, kCurveOrder);

    const AffinePoint g_product = scalar_multiply(generator(), u1);
    const AffinePoint r_product = scalar_multiply(*r_point, u2);
    const AffinePoint public_key = to_affine(add_points(
        JacobianPoint{g_product.x, g_product.y, U256{{1, 0, 0, 0}},
                      g_product.infinity},
        JacobianPoint{r_product.x, r_product.y, U256{{1, 0, 0, 0}},
                      r_product.infinity}));
    if (public_key.infinity) {
        return std::nullopt;
    }

    std::array<std::uint8_t, 64> encoded{};
    const auto x_bytes = to_be(public_key.x);
    const auto y_bytes = to_be(public_key.y);
    std::copy(x_bytes.begin(), x_bytes.end(), encoded.begin());
    std::copy(y_bytes.begin(), y_bytes.end(), encoded.begin() + 32);

    const Hash256 hash = keccak256(encoded);
    Address address;
    std::copy(hash.bytes.begin() + 12, hash.bytes.end(), address.bytes.begin());
    return address;
}

}  // namespace

Hash256 keccak256(ByteView data) {
    std::uint64_t state[25] = {};
    constexpr std::size_t rate = 136;
    std::size_t offset = 0;

    while (data.size() - offset >= rate) {
        keccak_absorb_block(state, data.data() + offset);
        offset += rate;
    }

    std::array<std::uint8_t, rate> final_block{};
    const std::size_t remaining = data.size() - offset;
    if (remaining != 0) {
        std::copy_n(data.data() + offset, remaining, final_block.begin());
    }
    final_block[remaining] = 0x01U;
    final_block[rate - 1] |= 0x80U;
    keccak_absorb_block(state, final_block.data());

    Hash256 output;
    for (unsigned byte = 0; byte < 32; ++byte) {
        output.bytes[byte] = static_cast<std::uint8_t>(
            state[byte / 8] >> (8U * (byte % 8U)));
    }
    return output;
}

Hash256 keccak256(const Hash256& prefix, ByteView suffix) {
    Bytes buffer;
    buffer.reserve(prefix.bytes.size() + suffix.size());
    buffer.insert(buffer.end(), prefix.bytes.begin(), prefix.bytes.end());
    buffer.insert(buffer.end(), suffix.begin(), suffix.end());
    return keccak256(buffer);
}

bool scalar_is_zero(const std::array<std::uint8_t, 32>& scalar) {
    return std::all_of(scalar.begin(), scalar.end(),
                       [](std::uint8_t byte) { return byte == 0; });
}

bool scalar_is_valid(const std::array<std::uint8_t, 32>& scalar) {
    const U256 value = from_be(scalar);
    return !is_zero(value) && compare(value, kCurveOrder) < 0;
}

bool scalar_is_low_s(const std::array<std::uint8_t, 32>& scalar) {
    return compare(from_be(scalar), kHalfCurveOrder) <= 0;
}

std::optional<Address> recover_address(const Hash256& signing_digest,
                                       const CompactSignature& signature,
                                       bool require_low_s) {
    return recover_address_impl(signing_digest, signature, require_low_s);
}

}  // namespace netsec::detail
