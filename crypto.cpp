#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstring>
#include <stdexcept>

namespace netsec {

namespace {

constexpr char kHexDigits[] = "0123456789abcdef";

int hex_value(char c) {
    if (c >= '0' && c <= '9') {
        return c - '0';
    }
    if (c >= 'a' && c <= 'f') {
        return c - 'a' + 10;
    }
    if (c >= 'A' && c <= 'F') {
        return c - 'A' + 10;
    }
    return -1;
}

std::uint32_t rotr32(std::uint32_t value, unsigned count) {
    return (value >> count) | (value << (32U - count));
}

std::uint64_t rotr64(std::uint64_t value, unsigned count) {
    return (value >> count) | (value << (64U - count));
}

void append_u64_be(Bytes& out, std::uint64_t value) {
    for (int shift = 56; shift >= 0; shift -= 8) {
        out.push_back(static_cast<Byte>((value >> shift) & 0xffU));
    }
}

std::uint32_t load_u32_be(const Byte* p) {
    return (static_cast<std::uint32_t>(p[0]) << 24) |
           (static_cast<std::uint32_t>(p[1]) << 16) |
           (static_cast<std::uint32_t>(p[2]) << 8) |
           static_cast<std::uint32_t>(p[3]);
}

std::uint64_t load_u64_be(const Byte* p) {
    std::uint64_t value = 0;
    for (int i = 0; i < 8; ++i) {
        value = (value << 8) | p[i];
    }
    return value;
}

constexpr std::array<std::uint32_t, 64> kSha256K = {
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
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U,
};

constexpr std::array<std::uint64_t, 80> kSha512K = {
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

std::array<std::uint32_t, 8> sha256_compress(
    std::array<std::uint32_t, 8> state, const Byte* block) {
    std::array<std::uint32_t, 64> w{};
    for (std::size_t i = 0; i < 16; ++i) {
        w[i] = load_u32_be(block + i * 4);
    }
    for (std::size_t i = 16; i < 64; ++i) {
        const std::uint32_t s0 = rotr32(w[i - 15], 7) ^
                                 rotr32(w[i - 15], 18) ^ (w[i - 15] >> 3);
        const std::uint32_t s1 = rotr32(w[i - 2], 17) ^
                                 rotr32(w[i - 2], 19) ^ (w[i - 2] >> 10);
        w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }

    std::uint32_t a = state[0];
    std::uint32_t b = state[1];
    std::uint32_t c = state[2];
    std::uint32_t d = state[3];
    std::uint32_t e = state[4];
    std::uint32_t f = state[5];
    std::uint32_t g = state[6];
    std::uint32_t h = state[7];

    for (std::size_t i = 0; i < 64; ++i) {
        const std::uint32_t s1 = rotr32(e, 6) ^ rotr32(e, 11) ^ rotr32(e, 25);
        const std::uint32_t ch = (e & f) ^ (~e & g);
        const std::uint32_t temp1 = h + s1 + ch + kSha256K[i] + w[i];
        const std::uint32_t s0 = rotr32(a, 2) ^ rotr32(a, 13) ^ rotr32(a, 22);
        const std::uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
        const std::uint32_t temp2 = s0 + maj;
        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    return {state[0] + a, state[1] + b, state[2] + c, state[3] + d,
            state[4] + e, state[5] + f, state[6] + g, state[7] + h};
}

std::array<std::uint64_t, 8> sha512_compress(
    std::array<std::uint64_t, 8> state, const Byte* block) {
    std::array<std::uint64_t, 80> w{};
    for (std::size_t i = 0; i < 16; ++i) {
        w[i] = load_u64_be(block + i * 8);
    }
    for (std::size_t i = 16; i < 80; ++i) {
        const std::uint64_t s0 = rotr64(w[i - 15], 1) ^
                                 rotr64(w[i - 15], 8) ^ (w[i - 15] >> 7);
        const std::uint64_t s1 = rotr64(w[i - 2], 19) ^
                                 rotr64(w[i - 2], 61) ^ (w[i - 2] >> 6);
        w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }

    std::uint64_t a = state[0];
    std::uint64_t b = state[1];
    std::uint64_t c = state[2];
    std::uint64_t d = state[3];
    std::uint64_t e = state[4];
    std::uint64_t f = state[5];
    std::uint64_t g = state[6];
    std::uint64_t h = state[7];

    for (std::size_t i = 0; i < 80; ++i) {
        const std::uint64_t s1 = rotr64(e, 14) ^ rotr64(e, 18) ^ rotr64(e, 41);
        const std::uint64_t ch = (e & f) ^ (~e & g);
        const std::uint64_t temp1 = h + s1 + ch + kSha512K[i] + w[i];
        const std::uint64_t s0 = rotr64(a, 28) ^ rotr64(a, 34) ^ rotr64(a, 39);
        const std::uint64_t maj = (a & b) ^ (a & c) ^ (b & c);
        const std::uint64_t temp2 = s0 + maj;
        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    return {state[0] + a, state[1] + b, state[2] + c, state[3] + d,
            state[4] + e, state[5] + f, state[6] + g, state[7] + h};
}

Digest sha256_impl(std::span<const Byte> data) {
    Bytes message(data.begin(), data.end());
    const std::uint64_t bit_length =
        static_cast<std::uint64_t>(data.size()) * 8U;
    message.push_back(0x80U);
    while ((message.size() % 64U) != 56U) {
        message.push_back(0U);
    }
    append_u64_be(message, bit_length);

    std::array<std::uint32_t, 8> state = {
        0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
        0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U,
    };
    for (std::size_t offset = 0; offset < message.size(); offset += 64) {
        state = sha256_compress(state, message.data() + offset);
    }

    Digest out{};
    for (std::size_t i = 0; i < state.size(); ++i) {
        out[i * 4] = static_cast<Byte>(state[i] >> 24);
        out[i * 4 + 1] = static_cast<Byte>(state[i] >> 16);
        out[i * 4 + 2] = static_cast<Byte>(state[i] >> 8);
        out[i * 4 + 3] = static_cast<Byte>(state[i]);
    }
    return out;
}

Sha512Digest sha512_impl(std::span<const Byte> data) {
    Bytes message(data.begin(), data.end());
    const std::uint64_t bit_length =
        static_cast<std::uint64_t>(data.size()) * 8U;
    message.push_back(0x80U);
    while ((message.size() % 128U) != 112U) {
        message.push_back(0U);
    }
    append_u64_be(message, 0U);
    append_u64_be(message, bit_length);

    std::array<std::uint64_t, 8> state = {
        0x6a09e667f3bcc908ULL, 0xbb67ae8584caa73bULL,
        0x3c6ef372fe94f82bULL, 0xa54ff53a5f1d36f1ULL,
        0x510e527fade682d1ULL, 0x9b05688c2b3e6c1fULL,
        0x1f83d9abfb41bd6bULL, 0x5be0cd19137e2179ULL,
    };
    for (std::size_t offset = 0; offset < message.size(); offset += 128) {
        state = sha512_compress(state, message.data() + offset);
    }

    Sha512Digest out{};
    for (std::size_t i = 0; i < state.size(); ++i) {
        for (int j = 0; j < 8; ++j) {
            out[i * 8 + static_cast<std::size_t>(j)] =
                static_cast<Byte>(state[i] >> (56 - j * 8));
        }
    }
    return out;
}

// Ed25519 group arithmetic follows the compact 16-limb representation used by
// TweetNaCl (public domain), with a standard RFC 8032 signing nonce.
using Fe = std::array<std::int64_t, 16>;
using Point = std::array<Fe, 4>;

constexpr Fe kFe0{};
constexpr Fe kFe1{1};
constexpr Fe kD = {
    0x78a3, 0x1359, 0x4dca, 0x75eb, 0xd8ab, 0x4141, 0x0a4d, 0x0070,
    0xe898, 0x7779, 0x4079, 0x8cc7, 0xfe73, 0x2b6f, 0x6cee, 0x5203,
};
constexpr Fe kD2 = {
    0xf159, 0x26b2, 0x9b94, 0xebd6, 0xb156, 0x8283, 0x149a, 0x00e0,
    0xd130, 0xeef3, 0x80f2, 0x198e, 0xfce7, 0x56df, 0xd9dc, 0x2406,
};
constexpr Fe kX = {
    0xd51a, 0x8f25, 0x2d60, 0xc956, 0xa7b2, 0x9525, 0xc760, 0x692c,
    0xdc5c, 0xfdd6, 0xe231, 0xc0a4, 0x53fe, 0xcd6e, 0x36d3, 0x2169,
};
constexpr Fe kY = {
    0x6658, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666,
    0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666,
};
constexpr Fe kI = {
    0xa0b0, 0x4a0e, 0x1b27, 0xc4ee, 0xe478, 0xad2f, 0x1806, 0x2f43,
    0xd7a7, 0x3dfb, 0x0099, 0x2b4d, 0xdf0b, 0x4fc1, 0x2480, 0x2b83,
};
constexpr std::array<Byte, 32> kOrderL = {
    0xed, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58,
    0xd6, 0x9c, 0xf7, 0xa2, 0xde, 0xf9, 0xde, 0x14,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10,
};
constexpr std::array<Byte, 32> kFieldPrime = {
    0xed, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f,
};

void fe_carry(Fe& value) {
    for (int i = 0; i < 16; ++i) {
        value[i] += 1LL << 16;
        const std::int64_t carry = value[i] >> 16;
        if (i < 15) {
            value[i + 1] += carry - 1;
        } else {
            value[0] += (carry - 1) * 38;
        }
        value[i] -= carry << 16;
    }
}

void fe_select(Fe& p, Fe& q, int select) {
    const std::int64_t mask = ~static_cast<std::int64_t>(select) + 1;
    for (std::size_t i = 0; i < 16; ++i) {
        const std::int64_t t = mask & (p[i] ^ q[i]);
        p[i] ^= t;
        q[i] ^= t;
    }
}

void fe_pack(Byte* out, const Fe& input) {
    Fe t = input;
    fe_carry(t);
    fe_carry(t);
    fe_carry(t);
    for (int round = 0; round < 2; ++round) {
        Fe m{};
        m[0] = t[0] - 0xffed;
        for (int i = 1; i < 15; ++i) {
            m[i] = t[i] - 0xffff - ((m[i - 1] >> 16) & 1);
            m[i - 1] &= 0xffff;
        }
        m[15] = t[15] - 0x7fff - ((m[14] >> 16) & 1);
        const int borrow = static_cast<int>((m[15] >> 16) & 1);
        m[14] &= 0xffff;
        fe_select(t, m, 1 - borrow);
    }
    for (int i = 0; i < 16; ++i) {
        out[2 * i] = static_cast<Byte>(t[i] & 0xff);
        out[2 * i + 1] = static_cast<Byte>(t[i] >> 8);
    }
}

bool fe_not_equal(const Fe& a, const Fe& b) {
    Byte packed_a[32]{};
    Byte packed_b[32]{};
    fe_pack(packed_a, a);
    fe_pack(packed_b, b);
    Byte difference = 0;
    for (std::size_t i = 0; i < 32; ++i) {
        difference |= static_cast<Byte>(packed_a[i] ^ packed_b[i]);
    }
    return difference != 0;
}

Byte fe_parity(const Fe& value) {
    Byte packed[32]{};
    fe_pack(packed, value);
    return packed[0] & 1;
}

void fe_unpack(Fe& out, const Byte* input) {
    for (int i = 0; i < 16; ++i) {
        out[i] = static_cast<std::int64_t>(input[2 * i]) +
                 (static_cast<std::int64_t>(input[2 * i + 1]) << 8);
    }
    out[15] &= 0x7fff;
}

void fe_add(Fe& out, const Fe& a, const Fe& b) {
    for (std::size_t i = 0; i < 16; ++i) {
        out[i] = a[i] + b[i];
    }
}

void fe_sub(Fe& out, const Fe& a, const Fe& b) {
    for (std::size_t i = 0; i < 16; ++i) {
        out[i] = a[i] - b[i];
    }
}

void fe_mul(Fe& out, const Fe& a, const Fe& b) {
    std::array<std::int64_t, 31> t{};
    for (std::size_t i = 0; i < 16; ++i) {
        for (std::size_t j = 0; j < 16; ++j) {
            t[i + j] += a[i] * b[j];
        }
    }
    for (std::size_t i = 0; i < 15; ++i) {
        t[i] += 38 * t[i + 16];
    }
    for (std::size_t i = 0; i < 16; ++i) {
        out[i] = t[i];
    }
    fe_carry(out);
    fe_carry(out);
}

void fe_square(Fe& out, const Fe& a) {
    fe_mul(out, a, a);
}

void fe_invert(Fe& out, const Fe& input) {
    Fe c = input;
    for (int a = 253; a >= 0; --a) {
        fe_square(c, c);
        if (a != 2 && a != 4) {
            fe_mul(c, c, input);
        }
    }
    out = c;
}

void fe_pow2523(Fe& out, const Fe& input) {
    Fe c = input;
    for (int a = 250; a >= 0; --a) {
        fe_square(c, c);
        if (a != 1) {
            fe_mul(c, c, input);
        }
    }
    out = c;
}

void point_add(Point& p, const Point& q) {
    Fe a{};
    Fe b{};
    Fe c{};
    Fe d{};
    Fe t{};
    Fe e{};
    Fe f{};
    Fe g{};
    Fe h{};

    fe_sub(a, p[1], p[0]);
    fe_sub(t, q[1], q[0]);
    fe_mul(a, a, t);
    fe_add(b, p[0], p[1]);
    fe_add(t, q[0], q[1]);
    fe_mul(b, b, t);
    fe_mul(c, p[3], q[3]);
    fe_mul(c, c, kD2);
    fe_mul(d, p[2], q[2]);
    fe_add(d, d, d);
    fe_sub(e, b, a);
    fe_sub(f, d, c);
    fe_add(g, d, c);
    fe_add(h, b, a);

    fe_mul(p[0], e, f);
    fe_mul(p[1], h, g);
    fe_mul(p[2], g, f);
    fe_mul(p[3], e, h);
}

void point_cswap(Point& p, Point& q, Byte select) {
    for (std::size_t i = 0; i < 4; ++i) {
        fe_select(p[i], q[i], select);
    }
}

void point_pack(Byte* out, const Point& p) {
    Fe tx{};
    Fe ty{};
    Fe zi{};
    fe_invert(zi, p[2]);
    fe_mul(tx, p[0], zi);
    fe_mul(ty, p[1], zi);
    fe_pack(out, ty);
    out[31] ^= static_cast<Byte>(fe_parity(tx) << 7);
}

void point_scalar_mult(Point& p, Point q, const Byte* scalar) {
    p[0] = kFe0;
    p[1] = kFe1;
    p[2] = kFe1;
    p[3] = kFe0;
    for (int i = 255; i >= 0; --i) {
        const Byte bit = (scalar[i / 8] >> (i & 7)) & 1U;
        point_cswap(p, q, bit);
        point_add(q, p);
        point_add(p, p);
        point_cswap(p, q, bit);
    }
}

void point_scalar_base(Point& p, const Byte* scalar) {
    Point q{};
    q[0] = kX;
    q[1] = kY;
    q[2] = kFe1;
    fe_mul(q[3], kX, kY);
    point_scalar_mult(p, q, scalar);
}

bool point_unpack_negative(Point& r, const Byte* packed) {
    Fe t{};
    Fe chk{};
    Fe num{};
    Fe den{};
    Fe den2{};
    Fe den4{};
    Fe den6{};

    Byte y_bytes[32]{};
    std::memcpy(y_bytes, packed, 32);
    y_bytes[31] &= 0x7fU;
    bool canonical_y = false;
    for (int i = 31; i >= 0; --i) {
        if (y_bytes[i] < kFieldPrime[static_cast<std::size_t>(i)]) {
            canonical_y = true;
            break;
        }
        if (y_bytes[i] > kFieldPrime[static_cast<std::size_t>(i)]) {
            break;
        }
    }
    if (!canonical_y) {
        return false;
    }

    r[2] = kFe1;
    fe_unpack(r[1], packed);
    fe_square(num, r[1]);
    fe_mul(den, num, kD);
    fe_sub(num, num, r[2]);
    fe_add(den, r[2], den);

    fe_square(den2, den);
    fe_square(den4, den2);
    fe_mul(den6, den4, den2);
    fe_mul(t, den6, num);
    fe_mul(t, t, den);

    fe_pow2523(t, t);
    fe_mul(t, t, num);
    fe_mul(t, t, den);
    fe_mul(t, t, den);
    fe_mul(r[0], t, den);

    fe_square(chk, r[0]);
    fe_mul(chk, chk, den);
    if (fe_not_equal(chk, num)) {
        fe_mul(r[0], r[0], kI);
    }

    fe_square(chk, r[0]);
    fe_mul(chk, chk, den);
    if (fe_not_equal(chk, num)) {
        return false;
    }

    if (fe_parity(r[0]) == (packed[31] >> 7)) {
        fe_sub(r[0], kFe0, r[0]);
    }

    fe_mul(r[3], r[0], r[1]);
    return true;
}

void mod_order(Byte* out, std::array<std::int64_t, 64>& x) {
    std::int64_t carry = 0;
    for (int i = 63; i >= 32; --i) {
        carry = 0;
        int j = i - 32;
        for (; j < i - 12; ++j) {
            x[static_cast<std::size_t>(j)] +=
                carry - 16 * x[static_cast<std::size_t>(i)] *
                            kOrderL[static_cast<std::size_t>(j - (i - 32))];
            carry = (x[static_cast<std::size_t>(j)] + 128) >> 8;
            x[static_cast<std::size_t>(j)] -= carry << 8;
        }
        x[static_cast<std::size_t>(j)] += carry;
        x[static_cast<std::size_t>(i)] = 0;
    }

    carry = 0;
    for (int j = 0; j < 32; ++j) {
        x[static_cast<std::size_t>(j)] +=
            carry - (x[31] >> 4) * kOrderL[static_cast<std::size_t>(j)];
        carry = x[static_cast<std::size_t>(j)] >> 8;
        x[static_cast<std::size_t>(j)] &= 255;
    }
    for (int j = 0; j < 32; ++j) {
        x[static_cast<std::size_t>(j)] -=
            carry * kOrderL[static_cast<std::size_t>(j)];
    }
    for (int i = 0; i < 32; ++i) {
        x[static_cast<std::size_t>(i + 1)] +=
            x[static_cast<std::size_t>(i)] >> 8;
        out[i] = static_cast<Byte>(
            x[static_cast<std::size_t>(i)] & 255);
    }
}

void reduce_scalar(Byte* scalar_64) {
    std::array<std::int64_t, 64> x{};
    for (std::size_t i = 0; i < 64; ++i) {
        x[i] = scalar_64[i];
    }
    std::fill(scalar_64, scalar_64 + 64, 0);
    mod_order(scalar_64, x);
}

void clamp_scalar(Byte* scalar) {
    scalar[0] &= 248U;
    scalar[31] &= 127U;
    scalar[31] |= 64U;
}

bool constant_time_equal(const Byte* a, const Byte* b, std::size_t size) {
    Byte difference = 0;
    for (std::size_t i = 0; i < size; ++i) {
        difference |= static_cast<Byte>(a[i] ^ b[i]);
    }
    return difference == 0;
}

bool scalar_is_reduced(const Byte* scalar) {
    for (int i = 31; i >= 0; --i) {
        if (scalar[i] < kOrderL[static_cast<std::size_t>(i)]) {
            return true;
        }
        if (scalar[i] > kOrderL[static_cast<std::size_t>(i)]) {
            return false;
        }
    }
    return false;
}

}  // namespace

std::string to_hex(std::span<const Byte> bytes) {
    std::string out;
    out.reserve(bytes.size() * 2);
    for (const Byte byte : bytes) {
        out.push_back(kHexDigits[byte >> 4]);
        out.push_back(kHexDigits[byte & 0x0fU]);
    }
    return out;
}

Bytes bytes_from_hex(std::string_view hex) {
    if ((hex.size() % 2U) != 0U) {
        throw std::invalid_argument("hex input must have an even length");
    }
    Bytes out;
    out.reserve(hex.size() / 2);
    for (std::size_t i = 0; i < hex.size(); i += 2) {
        const int high = hex_value(hex[i]);
        const int low = hex_value(hex[i + 1]);
        if (high < 0 || low < 0) {
            throw std::invalid_argument("invalid hexadecimal digit");
        }
        out.push_back(static_cast<Byte>((high << 4) | low));
    }
    return out;
}

namespace crypto {

Digest sha256(std::span<const Byte> data) {
    return sha256_impl(data);
}

Sha512Digest sha512(std::span<const Byte> data) {
    return sha512_impl(data);
}

PublicKey ed25519_public_key(const Seed& seed) {
    Sha512Digest expanded = sha512(seed);
    clamp_scalar(expanded.data());
    Point point{};
    point_scalar_base(point, expanded.data());
    PublicKey public_key{};
    point_pack(public_key.data(), point);
    return public_key;
}

bool ed25519_is_valid_public_key(const PublicKey& public_key) {
    Point point{};
    return point_unpack_negative(point, public_key.data());
}

Signature ed25519_sign(std::span<const Byte> message, const Seed& seed) {
    Sha512Digest expanded = sha512(seed);
    clamp_scalar(expanded.data());

    const PublicKey public_key = ed25519_public_key(seed);

    Bytes nonce_input;
    nonce_input.reserve(32 + message.size());
    nonce_input.insert(nonce_input.end(), expanded.begin() + 32, expanded.end());
    nonce_input.insert(nonce_input.end(), message.begin(), message.end());
    std::array<Byte, 64> nonce = sha512(nonce_input);
    reduce_scalar(nonce.data());

    Signature signature{};
    Point r_point{};
    point_scalar_base(r_point, nonce.data());
    point_pack(signature.data(), r_point);

    Bytes challenge_input;
    challenge_input.reserve(64 + message.size());
    challenge_input.insert(challenge_input.end(), signature.begin(),
                           signature.begin() + 32);
    challenge_input.insert(challenge_input.end(), public_key.begin(),
                           public_key.end());
    challenge_input.insert(challenge_input.end(), message.begin(), message.end());
    std::array<Byte, 64> challenge = sha512(challenge_input);
    reduce_scalar(challenge.data());

    std::array<std::int64_t, 64> product{};
    for (std::size_t i = 0; i < 32; ++i) {
        product[i] = nonce[i];
    }
    for (std::size_t i = 0; i < 32; ++i) {
        for (std::size_t j = 0; j < 32; ++j) {
            product[i + j] += static_cast<std::int64_t>(challenge[i]) *
                              static_cast<std::int64_t>(expanded[j]);
        }
    }
    mod_order(signature.data() + 32, product);
    return signature;
}

bool ed25519_verify(std::span<const Byte> message, const Signature& signature,
                    const PublicKey& public_key) {
    if (!scalar_is_reduced(signature.data() + 32)) {
        return false;
    }
    Point q{};
    if (!point_unpack_negative(q, public_key.data())) {
        return false;
    }

    Bytes challenge_input;
    challenge_input.reserve(64 + message.size());
    challenge_input.insert(challenge_input.end(), signature.begin(),
                           signature.begin() + 32);
    challenge_input.insert(challenge_input.end(), public_key.begin(),
                           public_key.end());
    challenge_input.insert(challenge_input.end(), message.begin(), message.end());
    std::array<Byte, 64> challenge = sha512(challenge_input);
    reduce_scalar(challenge.data());

    Point p{};
    point_scalar_mult(p, q, challenge.data());
    Point base{};
    point_scalar_base(base, signature.data() + 32);
    point_add(p, base);

    Byte packed[32]{};
    point_pack(packed, p);
    return constant_time_equal(packed, signature.data(), 32);
}

}  // namespace crypto

SigningKey::SigningKey(Seed seed) noexcept : seed_(seed) {}

SigningKey SigningKey::from_hex(std::string_view hex) {
    const Bytes bytes = bytes_from_hex(hex);
    if (bytes.size() != kSeedSize) {
        throw std::invalid_argument("Ed25519 seed must be 32 bytes");
    }
    Seed seed{};
    std::copy(bytes.begin(), bytes.end(), seed.begin());
    return SigningKey(seed);
}

PublicKey SigningKey::public_key() const {
    return crypto::ed25519_public_key(seed_);
}

Signature SigningKey::sign(std::span<const Byte> message) const {
    return crypto::ed25519_sign(message, seed_);
}

}  // namespace netsec
