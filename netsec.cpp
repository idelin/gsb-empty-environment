#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <cerrno>
#include <climits>
#include <cstring>
#include <fstream>
#include <limits>
#include <map>
#include <set>
#include <sstream>
#include <stdexcept>
#include <utility>

namespace netsec {
namespace detail {

// ===========================================================================
// Small utilities
// ===========================================================================

using u8 = std::uint8_t;
using u32 = std::uint32_t;
using u64 = std::uint64_t;

static std::string trim(std::string_view s) {
    std::size_t b = 0;
    std::size_t e = s.size();
    while (b < e && std::isspace(static_cast<unsigned char>(s[b]))) ++b;
    while (e > b && std::isspace(static_cast<unsigned char>(s[e - 1]))) --e;
    return std::string(s.substr(b, e - b));
}

static std::string lower_ascii(std::string_view s) {
    std::string out(s);
    for (char& c : out) {
        c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
    }
    return out;
}

static std::string collapse_ws(std::string_view s) {
    std::string out;
    bool space = false;
    for (unsigned char c : s) {
        if (std::isspace(c)) {
            space = true;
        } else {
            if (space && !out.empty()) out.push_back(' ');
            out.push_back(static_cast<char>(c));
            space = false;
        }
    }
    return lower_ascii(trim(out));
}

static bool read_file(const std::string& path, Bytes& out, std::string& error) {
    std::ifstream in(path, std::ios::binary);
    if (!in) {
        error = "cannot open " + path;
        return false;
    }
    in.seekg(0, std::ios::end);
    const auto end = in.tellg();
    if (end < 0) {
        error = "cannot determine size of " + path;
        return false;
    }
    in.seekg(0, std::ios::beg);
    out.resize(static_cast<std::size_t>(end));
    if (!out.empty()) {
        in.read(reinterpret_cast<char*>(out.data()),
                static_cast<std::streamsize>(out.size()));
        if (!in) {
            error = "cannot read " + path;
            return false;
        }
    }
    return true;
}

static int base64_value(unsigned char c) {
    if (c >= 'A' && c <= 'Z') return c - 'A';
    if (c >= 'a' && c <= 'z') return c - 'a' + 26;
    if (c >= '0' && c <= '9') return c - '0' + 52;
    if (c == '+') return 62;
    if (c == '/') return 63;
    return -1;
}

std::string base64_encode(ByteView data) {
    static constexpr char alphabet[] =
        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    std::string out;
    out.reserve(((data.size() + 2) / 3) * 4);
    for (std::size_t i = 0; i < data.size(); i += 3) {
        const std::size_t n = std::min<std::size_t>(3, data.size() - i);
        std::uint32_t v = static_cast<std::uint32_t>(data[i]) << 16;
        if (n > 1) v |= static_cast<std::uint32_t>(data[i + 1]) << 8;
        if (n > 2) v |= static_cast<std::uint32_t>(data[i + 2]);
        out.push_back(alphabet[(v >> 18) & 0x3f]);
        out.push_back(alphabet[(v >> 12) & 0x3f]);
        out.push_back(n > 1 ? alphabet[(v >> 6) & 0x3f] : '=');
        out.push_back(n > 2 ? alphabet[v & 0x3f] : '=');
    }
    return out;
}

std::optional<Bytes> base64_decode(std::string_view text) {
    Bytes out;
    std::uint32_t acc = 0;
    int bits = 0;
    bool padding = false;
    for (unsigned char c : text) {
        if (std::isspace(c)) continue;
        if (c == '=') {
            padding = true;
            continue;
        }
        if (padding) return std::nullopt;
        const int v = base64_value(c);
        if (v < 0) return std::nullopt;
        acc = (acc << 6) | static_cast<std::uint32_t>(v);
        bits += 6;
        if (bits >= 8) {
            bits -= 8;
            out.push_back(static_cast<u8>((acc >> bits) & 0xff));
        }
    }
    if (bits >= 6) return std::nullopt;
    if (bits > 0) {
        const std::uint32_t mask = (1u << bits) - 1u;
        if ((acc & mask) != 0) return std::nullopt;
    }
    return out;
}

std::string hex_encode(ByteView data, bool upper = false) {
    static constexpr char lo[] = "0123456789abcdef";
    static constexpr char hi[] = "0123456789ABCDEF";
    const char* digits = upper ? hi : lo;
    std::string out;
    out.reserve(data.size() * 2);
    for (u8 b : data) {
        out.push_back(digits[b >> 4]);
        out.push_back(digits[b & 0x0f]);
    }
    return out;
}

std::optional<Bytes> hex_decode(std::string_view text) {
    std::string compact;
    compact.reserve(text.size());
    for (char c : text) {
        if (std::isspace(static_cast<unsigned char>(c)) || c == ':') continue;
        compact.push_back(c);
    }
    if (compact.size() % 2 != 0) return std::nullopt;
    auto nibble = [](char c) -> int {
        if (c >= '0' && c <= '9') return c - '0';
        if (c >= 'a' && c <= 'f') return c - 'a' + 10;
        if (c >= 'A' && c <= 'F') return c - 'A' + 10;
        return -1;
    };
    Bytes out;
    out.reserve(compact.size() / 2);
    for (std::size_t i = 0; i < compact.size(); i += 2) {
        const int a = nibble(compact[i]);
        const int b = nibble(compact[i + 1]);
        if (a < 0 || b < 0) return std::nullopt;
        out.push_back(static_cast<u8>((a << 4) | b));
    }
    return out;
}

std::vector<Bytes> parse_pem_blocks(std::string_view pem, std::string_view label) {
    std::vector<Bytes> out;
    const std::string begin = "-----BEGIN " + std::string(label) + "-----";
    const std::string end = "-----END " + std::string(label) + "-----";
    std::size_t pos = 0;
    while (true) {
        const std::size_t b = pem.find(begin, pos);
        if (b == std::string_view::npos) break;
        const std::size_t data_begin = b + begin.size();
        const std::size_t e = pem.find(end, data_begin);
        if (e == std::string_view::npos) break;
        auto decoded = base64_decode(pem.substr(data_begin, e - data_begin));
        if (decoded) out.push_back(std::move(*decoded));
        pos = e + end.size();
    }
    return out;
}

std::optional<Bytes> pem_to_der(std::string_view pem, std::string_view label) {
    auto blocks = parse_pem_blocks(pem, label);
    if (blocks.empty()) return std::nullopt;
    return std::move(blocks.front());
}

static bool looks_like_text(ByteView data) {
    bool saw_text = false;
    for (u8 c : data) {
        if (c == 0) return false;
        if (c >= 0x20 && c < 0x7f) {
            saw_text = true;
        } else if (c != '\r' && c != '\n' && c != '\t') {
            return false;
        }
    }
    return saw_text;
}

// ===========================================================================
// Time handling
// ===========================================================================

// Howard Hinnant's civil calendar algorithms, with 1970-01-01 as day zero.
static std::int64_t days_from_civil(int y, unsigned m, unsigned d) {
    y -= m <= 2;
    const std::int64_t era =
        (y >= 0 ? y : y - 399) / 400;
    const unsigned yoe = static_cast<unsigned>(y - era * 400);
    const unsigned doy =
        (153u * (m + (m > 2 ? -3u : 9u)) + 2u) / 5u + d - 1u;
    const unsigned doe = yoe * 365u + yoe / 4u - yoe / 100u + doy;
    return era * 146097 + static_cast<std::int64_t>(doe) - 719468;
}

static bool parse_digits(std::string_view s, std::size_t pos, std::size_t n,
                         int& value) {
    if (pos + n > s.size()) return false;
    int v = 0;
    for (std::size_t i = 0; i < n; ++i) {
        const char c = s[pos + i];
        if (c < '0' || c > '9') return false;
        v = v * 10 + (c - '0');
    }
    value = v;
    return true;
}

static bool parse_time_string(std::string_view text, TimePoint& out,
                              std::string* normalized, std::string& error) {
    std::string s = trim(text);
    int year = 0;
    std::size_t p = 0;
    bool generalized = false;
    if (s.size() >= 15 && s[14] == 'Z') {
        generalized = true;
    } else if (s.size() >= 13 && s[12] == 'Z') {
        generalized = false;
    } else {
        error = "time is not a supported UTC DER time";
        return false;
    }

    int month = 0, day = 0, hour = 0, minute = 0, second = 0;
    if (generalized) {
        if (!parse_digits(s, p, 4, year)) {
            error = "bad GeneralizedTime year";
            return false;
        }
        p += 4;
    } else {
        int yy = 0;
        if (!parse_digits(s, p, 2, yy)) {
            error = "bad UTCTime year";
            return false;
        }
        year = yy >= 50 ? 1900 + yy : 2000 + yy;
        p += 2;
    }
    if (!parse_digits(s, p, 2, month) || !parse_digits(s, p + 2, 2, day) ||
        !parse_digits(s, p + 4, 2, hour) || !parse_digits(s, p + 6, 2, minute) ||
        !parse_digits(s, p + 8, 2, second)) {
        error = "bad time digits";
        return false;
    }
    p += 10;
    if (month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 ||
        minute > 59 || second > 60) {
        error = "time field out of range";
        return false;
    }

    std::int64_t nanos = 0;
    if (p < s.size() && s[p] == '.') {
        ++p;
        std::int64_t scale = 100000000;
        bool any = false;
        while (p < s.size() && s[p] >= '0' && s[p] <= '9') {
            if (scale > 0) {
                nanos += static_cast<std::int64_t>(s[p] - '0') * scale;
                scale /= 10;
            }
            any = true;
            ++p;
        }
        if (!any) {
            error = "empty fractional seconds";
            return false;
        }
    }
    if (p >= s.size() || s[p] != 'Z') {
        error = "time must use UTC (Z)";
        return false;
    }
    ++p;
    if (p != s.size()) {
        error = "trailing data in time";
        return false;
    }

    const std::int64_t days = days_from_civil(year, static_cast<unsigned>(month),
                                              static_cast<unsigned>(day));
    const std::int64_t secs =
        days * 86400 + hour * 3600 + minute * 60 + second;
    const auto duration = std::chrono::seconds(secs) +
                          std::chrono::nanoseconds(nanos);
    out = TimePoint(std::chrono::duration_cast<TimePoint::duration>(duration));
    if (normalized) {
        char buf[32];
        std::snprintf(buf, sizeof(buf), "%04d%02d%02d%02d%02d%02dZ", year,
                      month, day, hour, minute, second);
        *normalized = buf;
    }
    return true;
}

// ===========================================================================
// Digest algorithms (MD5, SHA-1, SHA-2)
// ===========================================================================

static u32 rotr32(u32 x, unsigned n) {
    return (x >> n) | (x << (32 - n));
}

static u32 rotl32(u32 x, unsigned n) {
    return (x << n) | (x >> (32 - n));
}

static u64 rotr64(u64 x, unsigned n) {
    return (x >> n) | (x << (64 - n));
}

static u32 load_be32(const u8* p) {
    return (static_cast<u32>(p[0]) << 24) |
           (static_cast<u32>(p[1]) << 16) |
           (static_cast<u32>(p[2]) << 8) | static_cast<u32>(p[3]);
}

static u64 load_be64(const u8* p) {
    return (static_cast<u64>(load_be32(p)) << 32) | load_be32(p + 4);
}

static void store_be32(u8* p, u32 v) {
    p[0] = static_cast<u8>(v >> 24);
    p[1] = static_cast<u8>(v >> 16);
    p[2] = static_cast<u8>(v >> 8);
    p[3] = static_cast<u8>(v);
}

static void store_be64(u8* p, u64 v) {
    store_be32(p, static_cast<u32>(v >> 32));
    store_be32(p + 4, static_cast<u32>(v));
}

static void store_le32(u8* p, u32 v) {
    p[0] = static_cast<u8>(v);
    p[1] = static_cast<u8>(v >> 8);
    p[2] = static_cast<u8>(v >> 16);
    p[3] = static_cast<u8>(v >> 24);
}

static Bytes md5_digest(ByteView data) {
    static constexpr u32 K[64] = {
        0xd76aa478, 0xe8c7b756, 0x242070db, 0xc1bdceee, 0xf57c0faf,
        0x4787c62a, 0xa8304613, 0xfd469501, 0x698098d8, 0x8b44f7af,
        0xffff5bb1, 0x895cd7be, 0x6b901122, 0xfd987193, 0xa679438e,
        0x49b40821, 0xf61e2562, 0xc040b340, 0x265e5a51, 0xe9b6c7aa,
        0xd62f105d, 0x02441453, 0xd8a1e681, 0xe7d3fbc8, 0x21e1cde6,
        0xc33707d6, 0xf4d50d87, 0x455a14ed, 0xa9e3e905, 0xfcefa3f8,
        0x676f02d9, 0x8d2a4c8a, 0xfffa3942, 0x8771f681, 0x6d9d6122,
        0xfde5380c, 0xa4beea44, 0x4bdecfa9, 0xf6bb4b60, 0xbebfbc70,
        0x289b7ec6, 0xeaa127fa, 0xd4ef3085, 0x04881d05, 0xd9d4d039,
        0xe6db99e5, 0x1fa27cf8, 0xc4ac5665, 0xf4292244, 0x432aff97,
        0xab9423a7, 0xfc93a039, 0x655b59c3, 0x8f0ccc92, 0xffeff47d,
        0x85845dd1, 0x6fa87e4f, 0xfe2ce6e0, 0xa3014314, 0x4e0811a1,
        0xf7537e82, 0xbd3af235, 0x2ad7d2bb, 0xeb86d391};
    static constexpr unsigned S[64] = {
        7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22,
        5, 9,  14, 20, 5, 9,  14, 20, 5, 9,  14, 20, 5, 9,  14, 20,
        4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23,
        6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21};

    u32 h0 = 0x67452301, h1 = 0xefcdab89, h2 = 0x98badcfe, h3 = 0x10325476;
    Bytes msg(data.begin(), data.end());
    const u64 bit_len = static_cast<u64>(msg.size()) * 8;
    msg.push_back(0x80);
    while (msg.size() % 64 != 56) msg.push_back(0);
    for (int i = 0; i < 8; ++i) {
        msg.push_back(static_cast<u8>(bit_len >> (8 * i)));
    }
    for (std::size_t off = 0; off < msg.size(); off += 64) {
        u32 M[16];
        for (int i = 0; i < 16; ++i) {
            const u8* p = msg.data() + off + i * 4;
            M[i] = static_cast<u32>(p[0]) | (static_cast<u32>(p[1]) << 8) |
                   (static_cast<u32>(p[2]) << 16) |
                   (static_cast<u32>(p[3]) << 24);
        }
        u32 a = h0, b = h1, c = h2, d = h3;
        for (unsigned i = 0; i < 64; ++i) {
            u32 f = 0;
            unsigned g = 0;
            if (i < 16) {
                f = (b & c) | (~b & d);
                g = i;
            } else if (i < 32) {
                f = (d & b) | (~d & c);
                g = (5 * i + 1) % 16;
            } else if (i < 48) {
                f = b ^ c ^ d;
                g = (3 * i + 5) % 16;
            } else {
                f = c ^ (b | ~d);
                g = (7 * i) % 16;
            }
            const u32 tmp = d;
            d = c;
            c = b;
            b = b + ((a + f + K[i] + M[g]) << S[i] |
                     (a + f + K[i] + M[g]) >> (32 - S[i]));
            a = tmp;
        }
        h0 += a;
        h1 += b;
        h2 += c;
        h3 += d;
    }
    Bytes out(16);
    store_le32(out.data(), h0);
    store_le32(out.data() + 4, h1);
    store_le32(out.data() + 8, h2);
    store_le32(out.data() + 12, h3);
    return out;
}

static Bytes sha1_digest(ByteView data) {
    Bytes msg(data.begin(), data.end());
    const u64 bit_len = static_cast<u64>(msg.size()) * 8;
    msg.push_back(0x80);
    while (msg.size() % 64 != 56) msg.push_back(0);
    for (int i = 7; i >= 0; --i) {
        msg.push_back(static_cast<u8>(bit_len >> (8 * i)));
    }
    u32 h[5] = {0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476,
                0xc3d2e1f0};
    for (std::size_t off = 0; off < msg.size(); off += 64) {
        u32 w[80];
        for (int i = 0; i < 16; ++i) w[i] = load_be32(msg.data() + off + i * 4);
        for (int i = 16; i < 80; ++i) {
            const u32 x = w[i - 3] ^ w[i - 8] ^ w[i - 14] ^ w[i - 16];
            w[i] = (x << 1) | (x >> 31);
        }
        u32 a = h[0], b = h[1], c = h[2], d = h[3], e = h[4];
        for (int i = 0; i < 80; ++i) {
            u32 f = 0, k = 0;
            if (i < 20) {
                f = (b & c) | (~b & d);
                k = 0x5a827999;
            } else if (i < 40) {
                f = b ^ c ^ d;
                k = 0x6ed9eba1;
            } else if (i < 60) {
                f = (b & c) | (b & d) | (c & d);
                k = 0x8f1bbcdc;
            } else {
                f = b ^ c ^ d;
                k = 0xca62c1d6;
            }
            const u32 tmp = rotl32(a, 5) + f + e + k + w[i];
            e = d;
            d = c;
            c = rotl32(b, 30);
            b = a;
            a = tmp;
        }
        h[0] += a;
        h[1] += b;
        h[2] += c;
        h[3] += d;
        h[4] += e;
    }
    Bytes out(20);
    for (int i = 0; i < 5; ++i) store_be32(out.data() + i * 4, h[i]);
    return out;
}

static Bytes sha256_digest(ByteView data, bool sha224) {
    static constexpr u32 K[64] = {
        0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b,
        0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01,
        0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7,
        0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
        0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152,
        0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
        0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc,
        0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
        0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819,
        0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08,
        0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f,
        0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
        0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};
    u32 h[8];
    if (sha224) {
        const u32 iv[8] = {0xc1059ed8, 0x367cd507, 0x3070dd17, 0xf70e5939,
                           0xffc00b31, 0x68581511, 0x64f98fa7, 0xbefa4fa4};
        std::copy(std::begin(iv), std::end(iv), h);
    } else {
        const u32 iv[8] = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
                           0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};
        std::copy(std::begin(iv), std::end(iv), h);
    }
    Bytes msg(data.begin(), data.end());
    const u64 bit_len = static_cast<u64>(msg.size()) * 8;
    msg.push_back(0x80);
    while (msg.size() % 64 != 56) msg.push_back(0);
    for (int i = 7; i >= 0; --i) {
        msg.push_back(static_cast<u8>(bit_len >> (8 * i)));
    }
    for (std::size_t off = 0; off < msg.size(); off += 64) {
        u32 w[64];
        for (int i = 0; i < 16; ++i) w[i] = load_be32(msg.data() + off + i * 4);
        for (int i = 16; i < 64; ++i) {
            const u32 s0 = rotr32(w[i - 15], 7) ^ rotr32(w[i - 15], 18) ^
                           (w[i - 15] >> 3);
            const u32 s1 = rotr32(w[i - 2], 17) ^ rotr32(w[i - 2], 19) ^
                           (w[i - 2] >> 10);
            w[i] = w[i - 16] + s0 + w[i - 7] + s1;
        }
        u32 a = h[0], b = h[1], c = h[2], d = h[3];
        u32 e = h[4], f = h[5], g = h[6], hh = h[7];
        for (int i = 0; i < 64; ++i) {
            const u32 S1 = rotr32(e, 6) ^ rotr32(e, 11) ^ rotr32(e, 25);
            const u32 ch = (e & f) ^ (~e & g);
            const u32 t1 = hh + S1 + ch + K[i] + w[i];
            const u32 S0 = rotr32(a, 2) ^ rotr32(a, 13) ^ rotr32(a, 22);
            const u32 maj = (a & b) ^ (a & c) ^ (b & c);
            const u32 t2 = S0 + maj;
            hh = g;
            g = f;
            f = e;
            e = d + t1;
            d = c;
            c = b;
            b = a;
            a = t1 + t2;
        }
        h[0] += a;
        h[1] += b;
        h[2] += c;
        h[3] += d;
        h[4] += e;
        h[5] += f;
        h[6] += g;
        h[7] += hh;
    }
    const std::size_t words = sha224 ? 7 : 8;
    Bytes out(words * 4);
    for (std::size_t i = 0; i < words; ++i) {
        store_be32(out.data() + i * 4, h[i]);
    }
    return out;
}

static Bytes sha512_digest(ByteView data, bool sha384) {
    static constexpr u64 K[80] = {
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
    u64 h[8];
    if (sha384) {
        const u64 iv[8] = {
            0xcbbb9d5dc1059ed8ULL, 0x629a292a367cd507ULL,
            0x9159015a3070dd17ULL, 0x152fecd8f70e5939ULL,
            0x67332667ffc00b31ULL, 0x8eb44a8768581511ULL,
            0xdb0c2e0d64f98fa7ULL, 0x47b5481dbefa4fa4ULL};
        std::copy(std::begin(iv), std::end(iv), h);
    } else {
        const u64 iv[8] = {
            0x6a09e667f3bcc908ULL, 0xbb67ae8584caa73bULL,
            0x3c6ef372fe94f82bULL, 0xa54ff53a5f1d36f1ULL,
            0x510e527fade682d1ULL, 0x9b05688c2b3e6c1fULL,
            0x1f83d9abfb41bd6bULL, 0x5be0cd19137e2179ULL};
        std::copy(std::begin(iv), std::end(iv), h);
    }
    Bytes msg(data.begin(), data.end());
    const u64 bit_len = static_cast<u64>(msg.size()) * 8;
    msg.push_back(0x80);
    while (msg.size() % 128 != 112) msg.push_back(0);
    // 128-bit length; the high 64 bits are zero for practical inputs.
    for (int i = 0; i < 8; ++i) msg.push_back(0);
    for (int i = 7; i >= 0; --i) {
        msg.push_back(static_cast<u8>(bit_len >> (8 * i)));
    }
    for (std::size_t off = 0; off < msg.size(); off += 128) {
        u64 w[80];
        for (int i = 0; i < 16; ++i) w[i] = load_be64(msg.data() + off + i * 8);
        for (int i = 16; i < 80; ++i) {
            const u64 s0 = rotr64(w[i - 15], 1) ^ rotr64(w[i - 15], 8) ^
                           (w[i - 15] >> 7);
            const u64 s1 = rotr64(w[i - 2], 19) ^ rotr64(w[i - 2], 61) ^
                           (w[i - 2] >> 6);
            w[i] = w[i - 16] + s0 + w[i - 7] + s1;
        }
        u64 a = h[0], b = h[1], c = h[2], d = h[3];
        u64 e = h[4], f = h[5], g = h[6], hh = h[7];
        for (int i = 0; i < 80; ++i) {
            const u64 S1 = rotr64(e, 14) ^ rotr64(e, 18) ^ rotr64(e, 41);
            const u64 ch = (e & f) ^ (~e & g);
            const u64 t1 = hh + S1 + ch + K[i] + w[i];
            const u64 S0 = rotr64(a, 28) ^ rotr64(a, 34) ^ rotr64(a, 39);
            const u64 maj = (a & b) ^ (a & c) ^ (b & c);
            const u64 t2 = S0 + maj;
            hh = g;
            g = f;
            f = e;
            e = d + t1;
            d = c;
            c = b;
            b = a;
            a = t1 + t2;
        }
        h[0] += a;
        h[1] += b;
        h[2] += c;
        h[3] += d;
        h[4] += e;
        h[5] += f;
        h[6] += g;
        h[7] += hh;
    }
    const std::size_t words = sha384 ? 6 : 8;
    Bytes out(words * 8);
    for (std::size_t i = 0; i < words; ++i) {
        store_be64(out.data() + i * 8, h[i]);
    }
    return out;
}

static Bytes digest_bytes(HashAlgorithm algorithm, ByteView data) {
    switch (algorithm) {
        case HashAlgorithm::Md5:
            return md5_digest(data);
        case HashAlgorithm::Sha1:
            return sha1_digest(data);
        case HashAlgorithm::Sha224:
            return sha256_digest(data, true);
        case HashAlgorithm::Sha256:
            return sha256_digest(data, false);
        case HashAlgorithm::Sha384:
            return sha512_digest(data, true);
        case HashAlgorithm::Sha512:
            return sha512_digest(data, false);
        default:
            return {};
    }
}

// ===========================================================================
// Unsigned big integers
// ===========================================================================

struct BigUInt {
    std::vector<u32> limbs;  // little-endian base 2^32

    BigUInt() = default;
    explicit BigUInt(u64 v) {
        if (v) {
            limbs.push_back(static_cast<u32>(v));
            if (v >> 32) limbs.push_back(static_cast<u32>(v >> 32));
        }
    }

    static BigUInt from_be(ByteView b) {
        BigUInt r;
        std::size_t i = b.size();
        while (i >= 4) {
            i -= 4;
            r.limbs.push_back((static_cast<u32>(b[i]) << 24) |
                              (static_cast<u32>(b[i + 1]) << 16) |
                              (static_cast<u32>(b[i + 2]) << 8) |
                              static_cast<u32>(b[i + 3]));
        }
        if (i > 0) {
            u32 v = 0;
            for (std::size_t j = 0; j < i; ++j) {
                v = (v << 8) | b[j];
            }
            r.limbs.push_back(v);
        }
        r.normalize();
        return r;
    }

    static BigUInt from_hex(std::string_view hex) {
        std::optional<Bytes> b = hex_decode(hex);
        return b ? from_be(*b) : BigUInt{};
    }

    bool zero() const noexcept { return limbs.empty(); }
    bool one() const noexcept { return limbs.size() == 1 && limbs[0] == 1; }

    void normalize() {
        while (!limbs.empty() && limbs.back() == 0) limbs.pop_back();
    }

    std::size_t bits() const {
        if (limbs.empty()) return 0;
        u32 top = limbs.back();
        unsigned n = 0;
        while (top) {
            ++n;
            top >>= 1;
        }
        return (limbs.size() - 1) * 32 + n;
    }

    bool bit(std::size_t i) const {
        const std::size_t w = i / 32;
        if (w >= limbs.size()) return false;
        return ((limbs[w] >> (i % 32)) & 1u) != 0;
    }

    void set_bit(std::size_t i) {
        const std::size_t w = i / 32;
        if (w >= limbs.size()) limbs.resize(w + 1, 0);
        limbs[w] |= (1u << (i % 32));
    }

    bool even() const { return limbs.empty() || (limbs[0] & 1u) == 0; }

    void shr1() {
        u32 carry = 0;
        for (std::size_t i = limbs.size(); i-- > 0;) {
            const u32 next = limbs[i] & 1u;
            limbs[i] = (limbs[i] >> 1) | (carry << 31);
            carry = next;
        }
        normalize();
    }

    void shl1() {
        u32 carry = 0;
        for (u32& limb : limbs) {
            const u32 next = limb >> 31;
            limb = (limb << 1) | carry;
            carry = next;
        }
        if (carry) limbs.push_back(carry);
    }

    int cmp(const BigUInt& other) const {
        if (limbs.size() != other.limbs.size()) {
            return limbs.size() < other.limbs.size() ? -1 : 1;
        }
        for (std::size_t i = limbs.size(); i-- > 0;) {
            if (limbs[i] != other.limbs[i]) {
                return limbs[i] < other.limbs[i] ? -1 : 1;
            }
        }
        return 0;
    }

    friend bool operator==(const BigUInt& a, const BigUInt& b) {
        return a.cmp(b) == 0;
    }
    friend bool operator!=(const BigUInt& a, const BigUInt& b) {
        return a.cmp(b) != 0;
    }
    friend bool operator<(const BigUInt& a, const BigUInt& b) {
        return a.cmp(b) < 0;
    }
    friend bool operator>=(const BigUInt& a, const BigUInt& b) {
        return a.cmp(b) >= 0;
    }

    static BigUInt add(const BigUInt& a, const BigUInt& b) {
        BigUInt r;
        const std::size_t n = std::max(a.limbs.size(), b.limbs.size());
        r.limbs.assign(n, 0);
        u64 carry = 0;
        for (std::size_t i = 0; i < n; ++i) {
            const u64 av = i < a.limbs.size() ? a.limbs[i] : 0;
            const u64 bv = i < b.limbs.size() ? b.limbs[i] : 0;
            const u64 sum = av + bv + carry;
            r.limbs[i] = static_cast<u32>(sum);
            carry = sum >> 32;
        }
        if (carry) r.limbs.push_back(static_cast<u32>(carry));
        return r;
    }

    static BigUInt sub(const BigUInt& a, const BigUInt& b) {
        BigUInt r = a;
        u64 borrow = 0;
        for (std::size_t i = 0; i < r.limbs.size(); ++i) {
            const u64 av = r.limbs[i];
            const u64 bv = (i < b.limbs.size() ? b.limbs[i] : 0) + borrow;
            if (av >= bv) {
                r.limbs[i] = static_cast<u32>(av - bv);
                borrow = 0;
            } else {
                r.limbs[i] = static_cast<u32>((1ULL << 32) + av - bv);
                borrow = 1;
            }
        }
        r.normalize();
        return r;
    }

    void sub_assign(const BigUInt& b) { *this = sub(*this, b); }

    static BigUInt mul(const BigUInt& a, const BigUInt& b) {
        if (a.zero() || b.zero()) return BigUInt{};
        BigUInt r;
        r.limbs.assign(a.limbs.size() + b.limbs.size(), 0);
        for (std::size_t i = 0; i < a.limbs.size(); ++i) {
            u64 carry = 0;
            for (std::size_t j = 0; j < b.limbs.size(); ++j) {
                const u64 cur = static_cast<u64>(a.limbs[i]) * b.limbs[j] +
                                r.limbs[i + j] + carry;
                r.limbs[i + j] = static_cast<u32>(cur);
                carry = cur >> 32;
            }
            std::size_t k = i + b.limbs.size();
            while (carry) {
                const u64 cur = static_cast<u64>(r.limbs[k]) + carry;
                r.limbs[k] = static_cast<u32>(cur);
                carry = cur >> 32;
                ++k;
            }
        }
        r.normalize();
        return r;
    }

    static void divmod(const BigUInt& a, const BigUInt& b, BigUInt& q,
                       BigUInt& r) {
        if (b.zero()) throw std::domain_error("division by zero");
        q = BigUInt{};
        r = BigUInt{};
        const std::size_t n = a.bits();
        q.limbs.assign((n + 31) / 32, 0);
        for (std::size_t i = n; i-- > 0;) {
            r.shl1();
            if (a.bit(i)) {
                if (r.limbs.empty()) r.limbs.push_back(0);
                r.limbs[0] |= 1u;
            }
            if (r.cmp(b) >= 0) {
                r.sub_assign(b);
                q.set_bit(i);
            }
        }
        q.normalize();
    }

    static BigUInt mod(const BigUInt& a, const BigUInt& m) {
        BigUInt q, r;
        divmod(a, m, q, r);
        return r;
    }

    static BigUInt add_mod(const BigUInt& a, const BigUInt& b,
                           const BigUInt& m) {
        BigUInt s = add(a, b);
        if (s.cmp(m) >= 0) s.sub_assign(m);
        return s;
    }

    static BigUInt sub_mod(const BigUInt& a, const BigUInt& b,
                           const BigUInt& m) {
        if (a.cmp(b) >= 0) return sub(a, b);
        return sub(add(a, m), b);
    }

    static BigUInt mul_mod(const BigUInt& a, const BigUInt& b,
                           const BigUInt& m) {
        return mod(mul(a, b), m);
    }

    static BigUInt pow_mod(BigUInt base, BigUInt exp, const BigUInt& m) {
        if (m.one()) return BigUInt{};
        BigUInt result(1);
        base = mod(base, m);
        while (!exp.zero()) {
            if (!exp.even()) result = mul_mod(result, base, m);
            exp.shr1();
            if (!exp.zero()) base = mul_mod(base, base, m);
        }
        return result;
    }

    // Binary extended GCD; m must be odd (true for all supported curve
    // orders and prime fields).
    static BigUInt inv_mod(BigUInt a, const BigUInt& m) {
        a = mod(a, m);
        if (a.zero()) return BigUInt{};
        BigUInt u = a, v = m;
        BigUInt x1(1), x2(0);
        while (!u.one() && !v.one()) {
            while (u.even()) {
                u.shr1();
                if (x1.even()) {
                    x1.shr1();
                } else {
                    x1 = add(x1, m);
                    x1.shr1();
                }
            }
            while (v.even()) {
                v.shr1();
                if (x2.even()) {
                    x2.shr1();
                } else {
                    x2 = add(x2, m);
                    x2.shr1();
                }
            }
            if (u.cmp(v) >= 0) {
                u.sub_assign(v);
                x1 = sub_mod(x1, x2, m);
            } else {
                v.sub_assign(u);
                x2 = sub_mod(x2, x1, m);
            }
        }
        return u.one() ? mod(x1, m) : mod(x2, m);
    }

    Bytes to_be(std::size_t min_bytes = 0) const {
        std::size_t n = (bits() + 7) / 8;
        if (n < min_bytes) n = min_bytes;
        Bytes out(n, 0);
        for (std::size_t i = 0; i < n; ++i) {
            const std::size_t limb = i / 4;
            const unsigned shift = static_cast<unsigned>((i % 4) * 8);
            const u32 v = limb < limbs.size() ? limbs[limb] : 0;
            out[n - 1 - i] = static_cast<u8>(v >> shift);
        }
        return out;
    }

    std::string to_decimal() const {
        if (zero()) return "0";
        static constexpr u32 base = 1000000000u;
        std::vector<u32> digits{0};
        for (std::size_t i = limbs.size(); i-- > 0;) {
            u64 carry = limbs[i];
            for (u32& d : digits) {
                const u64 cur = static_cast<u64>(d) * 4294967296ULL + carry;
                d = static_cast<u32>(cur % base);
                carry = cur / base;
            }
            while (carry) {
                digits.push_back(static_cast<u32>(carry % base));
                carry /= base;
            }
        }
        std::ostringstream os;
        os << digits.back();
        char buf[16];
        for (std::size_t i = digits.size() - 1; i-- > 0;) {
            std::snprintf(buf, sizeof(buf), "%09u", digits[i]);
            os << buf;
        }
        return os.str();
    }

    std::optional<u64> to_u64() const {
        if (limbs.size() > 2) return std::nullopt;
        u64 v = 0;
        if (!limbs.empty()) v = limbs[0];
        if (limbs.size() > 1) v |= static_cast<u64>(limbs[1]) << 32;
        return v;
    }

    std::string to_hex() const {
        return hex_encode(to_be());
    }
};

static bool parse_decimal_u64(std::string_view text, u64& out) {
    std::string s = trim(text);
    if (s.empty()) return false;
    u64 v = 0;
    int base = 10;
    std::size_t i = 0;
    if (s.size() > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')) {
        base = 16;
        i = 2;
    }
    for (; i < s.size(); ++i) {
        int d = 0;
        const char c = s[i];
        if (c >= '0' && c <= '9') {
            d = c - '0';
        } else if (base == 16 && c >= 'a' && c <= 'f') {
            d = c - 'a' + 10;
        } else if (base == 16 && c >= 'A' && c <= 'F') {
            d = c - 'A' + 10;
        } else {
            return false;
        }
        if (v > (std::numeric_limits<u64>::max() - static_cast<u64>(d)) /
                    static_cast<u64>(base)) {
            return false;
        }
        v = v * static_cast<u64>(base) + static_cast<u64>(d);
    }
    out = v;
    return true;
}

static std::string canonical_integer_text(std::string_view text,
                                          bool* valid = nullptr) {
    std::string s = trim(text);
    if (s.empty()) {
        if (valid) *valid = false;
        return {};
    }
    // Accept decimal, 0x-prefixed hex, and colon/space separated hex.
    bool hexish = false;
    if (s.size() > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')) {
        hexish = true;
        s = s.substr(2);
    } else if (s.find_first_not_of("0123456789") != std::string::npos) {
        hexish = true;
    }
    BigUInt value;
    if (hexish) {
        std::string compact;
        for (char c : s) {
            if (std::isspace(static_cast<unsigned char>(c)) || c == ':') continue;
            compact.push_back(c);
        }
        if (compact.size() % 2) compact.insert(compact.begin(), '0');
        auto b = hex_decode(compact);
        if (!b) {
            if (valid) *valid = false;
            return {};
        }
        value = BigUInt::from_be(*b);
    } else {
        u64 v = 0;
        if (!parse_decimal_u64(s, v)) {
            // Fall back to arbitrary precision decimal.
            BigUInt acc;
            for (char c : s) {
                if (c < '0' || c > '9') {
                    if (valid) *valid = false;
                    return {};
                }
                acc = BigUInt::add(BigUInt::mul(acc, BigUInt(10)),
                                   BigUInt(static_cast<u64>(c - '0')));
            }
            value = acc;
        } else {
            value = BigUInt(v);
        }
    }
    if (valid) *valid = true;
    return value.to_decimal();
}

// ===========================================================================
// DER
// ===========================================================================

struct Der {
    u8 cls = 0;          // 0 universal, 1 application, 2 context, 3 private
    bool constructed = false;
    u32 tag = 0;
    ByteView full;
    ByteView content;
    std::vector<Der> children;

    bool is(u8 c, u32 t) const { return cls == c && tag == t; }
    bool is_universal(u32 t) const { return cls == 0 && tag == t; }
    bool is_context(u32 t) const { return cls == 2 && tag == t; }
};

static bool parse_der_one(ByteView data, std::size_t& pos, Der& out,
                          std::string& error, int depth = 0) {
    if (depth > 64) {
        error = "DER nesting too deep";
        return false;
    }
    if (pos >= data.size()) {
        error = "truncated DER tag";
        return false;
    }
    const std::size_t start = pos;
    const u8 first = data[pos++];
    out.cls = static_cast<u8>(first >> 6);
    out.constructed = (first & 0x20) != 0;
    out.tag = first & 0x1f;
    if (out.tag == 0x1f) {
        u32 tag = 0;
        int count = 0;
        while (true) {
            if (pos >= data.size()) {
                error = "truncated high-tag-number";
                return false;
            }
            const u8 b = data[pos++];
            if (++count > 5) {
                error = "tag number too large";
                return false;
            }
            tag = (tag << 7) | (b & 0x7f);
            if ((b & 0x80) == 0) break;
        }
        out.tag = tag;
    }
    if (pos >= data.size()) {
        error = "truncated DER length";
        return false;
    }
    const u8 lb = data[pos++];
    std::size_t len = 0;
    if ((lb & 0x80) == 0) {
        len = lb;
    } else {
        const unsigned n = lb & 0x7f;
        if (n == 0) {
            error = "indefinite length is not DER";
            return false;
        }
        if (n > sizeof(std::size_t) || pos + n > data.size()) {
            error = "invalid DER length";
            return false;
        }
        for (unsigned i = 0; i < n; ++i) {
            if (len > (std::numeric_limits<std::size_t>::max() >> 8)) {
                error = "DER length overflow";
                return false;
            }
            len = (len << 8) | data[pos++];
        }
    }
    if (len > data.size() - pos) {
        error = "DER value exceeds input";
        return false;
    }
    out.full = data.subspan(start, pos - start + len);
    out.content = data.subspan(pos, len);
    pos += len;
    return true;
}

static bool parse_der_children(Der& node, std::string& error, int depth = 0) {
    if (depth > 128) {
        error = "DER nesting too deep";
        return false;
    }
    node.children.clear();
    if (!node.constructed) return true;
    std::size_t pos = 0;
    while (pos < node.content.size()) {
        Der child;
        if (!parse_der_one(node.content, pos, child, error, depth + 1)) {
            return false;
        }
        node.children.push_back(std::move(child));
    }
    for (Der& child : node.children) {
        if (!parse_der_children(child, error, depth + 1)) return false;
    }
    return true;
}

static bool parse_der_full(ByteView data, Der& out, std::string& error) {
    std::size_t pos = 0;
    if (!parse_der_one(data, pos, out, error)) return false;
    if (pos != data.size()) {
        error = "trailing bytes after DER value";
        return false;
    }
    return parse_der_children(out, error);
}

static bool decode_oid(const Der& node, std::string& out) {
    if (!node.is_universal(6) || node.content.empty()) return false;
    std::ostringstream os;
    const u8* p = node.content.data();
    const std::size_t n = node.content.size();
    std::size_t i = 0;
    const unsigned first = p[i++];
    unsigned arc1 = first / 40;
    unsigned arc2 = first % 40;
    if (arc1 > 2) {
        arc1 = 2;
        arc2 = first - 80;
    }
    os << arc1 << '.' << arc2;
    while (i < n) {
        u64 value = 0;
        while (i < n) {
            const u8 b = p[i++];
            if (value > (std::numeric_limits<u64>::max() >> 7)) return false;
            value = (value << 7) | (b & 0x7f);
            if ((b & 0x80) == 0) break;
        }
        os << '.' << value;
    }
    out = os.str();
    return true;
}

static bool decode_positive_integer(const Der& node, BigUInt& out,
                                    std::string& error) {
    if (!node.is_universal(2) || node.content.empty()) {
        error = "expected non-empty INTEGER";
        return false;
    }
    if (node.content[0] & 0x80) {
        error = "negative INTEGER is not allowed";
        return false;
    }
    std::size_t i = 0;
    while (i + 1 < node.content.size() && node.content[i] == 0) ++i;
    out = BigUInt::from_be(node.content.subspan(i));
    return true;
}

static bool decode_int32(const Der& node, std::int32_t& out,
                         std::string& error) {
    BigUInt v;
    if (!decode_positive_integer(node, v, error)) return false;
    auto u = v.to_u64();
    if (!u || *u > static_cast<u64>(std::numeric_limits<std::int32_t>::max())) {
        error = "INTEGER does not fit int32";
        return false;
    }
    out = static_cast<std::int32_t>(*u);
    return true;
}

static bool decode_integer_u32(const Der& node, u32& out, std::string& error) {
    BigUInt v;
    if (!decode_positive_integer(node, v, error)) return false;
    auto u = v.to_u64();
    if (!u || *u > std::numeric_limits<u32>::max()) {
        error = "INTEGER does not fit uint32";
        return false;
    }
    out = static_cast<u32>(*u);
    return true;
}

static bool expect_sequence(const Der& node, std::string_view what,
                            std::string& error) {
    if (!node.is_universal(16) || !node.constructed) {
        error = std::string("expected SEQUENCE for ") + std::string(what);
        return false;
    }
    return true;
}

static bool expect_set(const Der& node, std::string_view what,
                       std::string& error) {
    if (!node.is_universal(17) || !node.constructed) {
        error = std::string("expected SET for ") + std::string(what);
        return false;
    }
    return true;
}

static bool expect_oid(const Der& node, std::string& oid, std::string_view what,
                       std::string& error) {
    if (!decode_oid(node, oid)) {
        error = std::string("expected OBJECT IDENTIFIER for ") +
                std::string(what);
        return false;
    }
    return true;
}

static bool expect_octet_string(const Der& node, Bytes& out,
                                std::string_view what, std::string& error) {
    if (!node.is_universal(4)) {
        error = std::string("expected OCTET STRING for ") + std::string(what);
        return false;
    }
    out.assign(node.content.begin(), node.content.end());
    return true;
}

static bool parse_algorithm_identifier(const Der& node, std::string& oid,
                                       Der* params, std::string& error) {
    if (!expect_sequence(node, "AlgorithmIdentifier", error)) return false;
    if (node.children.empty()) {
        error = "empty AlgorithmIdentifier";
        return false;
    }
    if (!expect_oid(node.children[0], oid, "algorithm", error)) return false;
    if (params) {
        if (node.children.size() > 1) {
            *params = node.children[1];
        } else {
            *params = Der{};
        }
    }
    return true;
}

// ===========================================================================
// X.509 certificate parsing
// ===========================================================================

static std::string decode_directory_value(const Der& value) {
    if (value.is_universal(30)) {  // BMPString, UTF-16BE
        std::string out;
        for (std::size_t i = 0; i + 1 < value.content.size(); i += 2) {
            const u32 c = (static_cast<u32>(value.content[i]) << 8) |
                          value.content[i + 1];
            if (c < 0x80) {
                out.push_back(static_cast<char>(c));
            } else if (c < 0x800) {
                out.push_back(static_cast<char>(0xc0 | (c >> 6)));
                out.push_back(static_cast<char>(0x80 | (c & 0x3f)));
            } else {
                out.push_back(static_cast<char>(0xe0 | (c >> 12)));
                out.push_back(static_cast<char>(0x80 | ((c >> 6) & 0x3f)));
                out.push_back(static_cast<char>(0x80 | (c & 0x3f)));
            }
        }
        return out;
    }
    if (value.is_universal(12) || value.is_universal(19) ||
        value.is_universal(20) || value.is_universal(22) ||
        value.is_universal(26) || value.is_universal(28)) {
        return std::string(value.content.begin(), value.content.end());
    }
    return hex_encode(value.content);
}

static std::string directory_attribute_name(std::string_view oid) {
    static const std::map<std::string, std::string> names = {
        {"2.5.4.3", "CN"},       {"2.5.4.6", "C"},
        {"2.5.4.7", "L"},        {"2.5.4.8", "ST"},
        {"2.5.4.9", "STREET"},   {"2.5.4.10", "O"},
        {"2.5.4.11", "OU"},      {"2.5.4.5", "SERIALNUMBER"},
        {"2.5.4.17", "postalCode"},
        {"2.5.4.4", "SN"},       {"2.5.4.42", "GN"},
        {"2.5.4.12", "title"},
        {"1.2.840.113549.1.9.1", "emailAddress"},
        {"0.9.2342.19200300.100.1.25", "DC"},
        {"0.9.2342.19200300.100.1.1", "UID"}};
    const auto it = names.find(std::string(oid));
    return it == names.end() ? std::string(oid) : it->second;
}

static bool parse_name(const Der& name, std::string& out,
                       std::string& error) {
    if (!name.is_universal(16) || !name.constructed) {
        error = "expected X.500 Name SEQUENCE";
        return false;
    }
    std::ostringstream os;
    bool first_rdn = true;
    for (const Der& rdn : name.children) {
        if (!rdn.is_universal(17) || !rdn.constructed) {
            error = "expected RelativeDistinguishedName SET";
            return false;
        }
        if (!first_rdn) os << ',';
        first_rdn = false;
        bool first_ava = true;
        for (const Der& ava : rdn.children) {
            if (!ava.is_universal(16) || ava.children.size() != 2) {
                error = "bad AttributeTypeAndValue";
                return false;
            }
            std::string oid;
            if (!decode_oid(ava.children[0], oid)) {
                error = "bad attribute OID";
                return false;
            }
            if (!first_ava) os << '+';
            first_ava = false;
            os << directory_attribute_name(oid) << '='
               << decode_directory_value(ava.children[1]);
        }
    }
    out = os.str();
    return true;
}

static bool parse_der_time(const Der& node, TimePoint& out,
                           std::string& normalized, std::string& error) {
    if (!node.is_universal(23) && !node.is_universal(24)) {
        error = "expected UTCTime or GeneralizedTime";
        return false;
    }
    return parse_time_string(
        std::string_view(reinterpret_cast<const char*>(node.content.data()),
                         node.content.size()),
        out, &normalized, error);
}

enum class KeyKind {
    Unknown,
    Rsa,
    Ec,
};

struct EcCurve {
    std::string oid;
    BigUInt p;
    BigUInt a;
    BigUInt b;
    BigUInt gx;
    BigUInt gy;
    BigUInt n;
};

static BigUInt min_three(const BigUInt& p) {
    return BigUInt::sub(p, BigUInt(3));
}

static const EcCurve* ec_curve_for_oid(std::string_view oid) {
    static const EcCurve p256 = [] {
        EcCurve c;
        c.oid = "1.2.840.10045.3.1.7";
        c.p = BigUInt::from_hex(
            "ffffffff00000001000000000000000000000000ffffffffffffffffffffffff");
        c.a = min_three(c.p);
        c.b = BigUInt::from_hex(
            "5ac635d8aa3a93e7b3ebbd55769886bc651d06b0cc53b0f63bce3c3e27d2604b");
        c.gx = BigUInt::from_hex(
            "6b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296");
        c.gy = BigUInt::from_hex(
            "4fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f5");
        c.n = BigUInt::from_hex(
            "ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551");
        return c;
    }();
    static const EcCurve p384 = [] {
        EcCurve c;
        c.oid = "1.3.132.0.34";
        c.p = BigUInt::from_hex(
            "fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe"
            "ffffffff0000000000000000ffffffff");
        c.a = min_three(c.p);
        c.b = BigUInt::from_hex(
            "b3312fa7e23ee7e4988e056be3f82d19181d9c6efe8141120314088f5013875a"
            "c656398d8a2ed19d2a85c8edd3ec2aef");
        c.gx = BigUInt::from_hex(
            "aa87ca22be8b05378eb1c71ef320ad746e1d3b628ba79b9859f741e082542a38"
            "5502f25dbf55296c3a545e3872760ab7");
        c.gy = BigUInt::from_hex(
            "3617de4a96262c6f5d9e98bf9292dc29f8f41dbd289a147ce9da3113b5f0b8c0"
            "0a60b1ce1d7e819d7a431d7c90ea0e5f");
        c.n = BigUInt::from_hex(
            "ffffffffffffffffffffffffffffffffffffffffffffffffc7634d81f4372ddf"
            "581a0db248b0a77aecec196accc52973");
        return c;
    }();
    static const EcCurve p521 = [] {
        EcCurve c;
        c.oid = "1.3.132.0.35";
        c.p = BigUInt::from_hex(
            "01ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
            "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
            "ffff");
        c.a = min_three(c.p);
        c.b = BigUInt::from_hex(
            "0051953eb9618e1c9a1f929a21a0b68540eea2da725b99b315f3b8b489918ef1"
            "09e156193951ec7e937b1652c0bd3bb1bf073573df883d2c34f1ef451fd46b50"
            "3f00");
        c.gx = BigUInt::from_hex(
            "00c6858e06b70404e9cd9e3ecb662395b4429c648139053fb521f828af606b4d"
            "3dbaa14b5e77efe75928fe1dc127a2ffa8de3348b3c1856a429bf97e7e31c2e5"
            "bd66");
        c.gy = BigUInt::from_hex(
            "011839296a789a3bc0045c8a5fb42c7d1bd998f54449579b446817afbd17273e"
            "662c97ee72995ef42640c550b9013fad0761353c7086a272c24088be94769fd1"
            "6650");
        c.n = BigUInt::from_hex(
            "01ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
            "fffa51868783bf2f966b7fcc0148f709a5d03bb5c9b8899c47aebb6fb71e9"
            "1386409");
        return c;
    }();
    if (oid == p256.oid) return &p256;
    if (oid == p384.oid) return &p384;
    if (oid == p521.oid) return &p521;
    return nullptr;
}

struct ParsedCert {
    CertificateInfo info;
    Bytes tbs;
    Bytes signature;
    std::string signature_algorithm_oid;
    Bytes signature_params_der;
    KeyKind key_kind = KeyKind::Unknown;
    BigUInt rsa_n;
    BigUInt rsa_e;
    const EcCurve* curve = nullptr;
    BigUInt ec_x;
    BigUInt ec_y;
    Bytes key_bits;
};

static std::optional<ParsedCert> parse_certificate_internal(
    ByteView der, std::string* error_out = nullptr) {
    auto fail = [&](std::string msg) -> std::optional<ParsedCert> {
        if (error_out) *error_out = std::move(msg);
        return std::nullopt;
    };

    ParsedCert cert;
    cert.info.der.assign(der.begin(), der.end());
    std::string error;
    Der root;
    if (!parse_der_full(ByteView(cert.info.der), root, error)) {
        return fail("certificate: " + error);
    }
    if (!expect_sequence(root, "Certificate", error) ||
        root.children.size() != 3) {
        return fail("certificate: expected Certificate SEQUENCE");
    }
    const Der& tbs = root.children[0];
    if (!expect_sequence(tbs, "TBSCertificate", error)) {
        return fail("certificate: bad TBSCertificate");
    }
    cert.tbs.assign(tbs.full.begin(), tbs.full.end());

    std::string sig_oid;
    Der sig_params;
    if (!parse_algorithm_identifier(root.children[1], sig_oid, &sig_params,
                                    error)) {
        return fail("certificate: " + error);
    }
    cert.signature_algorithm_oid = sig_oid;
    if (!sig_params.full.empty()) {
        cert.signature_params_der.assign(sig_params.full.begin(),
                                         sig_params.full.end());
    }
    const Der& sig_bit = root.children[2];
    if (!sig_bit.is_universal(3) || sig_bit.content.empty() ||
        sig_bit.content[0] != 0) {
        return fail("certificate: bad signature BIT STRING");
    }
    cert.signature.assign(sig_bit.content.begin() + 1, sig_bit.content.end());

    if (tbs.children.empty()) return fail("certificate: empty TBSCertificate");
    std::int32_t version = 1;
    std::size_t i = 0;
    if (tbs.children[i].is_context(0)) {
        if (tbs.children[i].children.empty()) {
            return fail("certificate: empty version");
        }
        std::string e;
        if (!decode_int32(tbs.children[i].children[0], version, e)) {
            return fail("certificate: " + e);
        }
        ++i;
    }
    if (i + 6 > tbs.children.size()) {
        return fail("certificate: truncated TBSCertificate fields");
    }
    const Der& serial_node = tbs.children[i++];
    ++i;  // signature AlgorithmIdentifier (same as outer signature)
    const Der& issuer_node = tbs.children[i++];
    const Der& validity_node = tbs.children[i++];
    const Der& subject_node = tbs.children[i++];
    const Der& spki_node = tbs.children[i++];

    BigUInt serial;
    if (!decode_positive_integer(serial_node, serial, error)) {
        return fail("certificate: " + error);
    }
    cert.info.serial_number = serial.to_decimal();
    if (auto v = serial.to_u64()) {
        cert.info.serial_fits_uint64 = true;
        cert.info.serial_uint64 = *v;
    }

    if (!expect_sequence(validity_node, "Validity", error) ||
        validity_node.children.size() != 2) {
        return fail("certificate: bad validity");
    }
    if (!parse_der_time(validity_node.children[0], cert.info.not_before,
                        cert.info.not_before_utc, error) ||
        !parse_der_time(validity_node.children[1], cert.info.not_after,
                        cert.info.not_after_utc, error)) {
        return fail("certificate: " + error);
    }
    if (!parse_name(issuer_node, cert.info.issuer, error)) {
        return fail("certificate: " + error);
    }
    if (!parse_name(subject_node, cert.info.subject, error)) {
        return fail("certificate: " + error);
    }
    cert.info.issuer_der.assign(issuer_node.full.begin(), issuer_node.full.end());
    cert.info.subject_der.assign(subject_node.full.begin(),
                                 subject_node.full.end());
    cert.info.self_signed = cert.info.issuer_der == cert.info.subject_der;
    cert.info.spki_der.assign(spki_node.full.begin(), spki_node.full.end());

    // SubjectPublicKeyInfo
    if (!expect_sequence(spki_node, "SubjectPublicKeyInfo", error) ||
        spki_node.children.size() < 2) {
        return fail("certificate: bad SubjectPublicKeyInfo");
    }
    std::string key_oid;
    Der key_params;
    if (!parse_algorithm_identifier(spki_node.children[0], key_oid, &key_params,
                                    error)) {
        return fail("certificate: " + error);
    }
    const Der& pub_bit = spki_node.children[1];
    if (!pub_bit.is_universal(3) || pub_bit.content.empty() ||
        pub_bit.content[0] != 0) {
        return fail("certificate: bad public key BIT STRING");
    }
    cert.key_bits.assign(pub_bit.content.begin() + 1, pub_bit.content.end());
    if (key_oid == "1.2.840.113549.1.1.1") {
        cert.key_kind = KeyKind::Rsa;
        Der rsa_pub;
        std::string rsa_error;
        if (!parse_der_full(ByteView(cert.key_bits), rsa_pub, rsa_error) ||
            !expect_sequence(rsa_pub, "RSAPublicKey", rsa_error) ||
            rsa_pub.children.size() < 2) {
            return fail("certificate: bad RSA public key");
        }
        std::string e;
        if (!decode_positive_integer(rsa_pub.children[0], cert.rsa_n, e) ||
            !decode_positive_integer(rsa_pub.children[1], cert.rsa_e, e)) {
            return fail("certificate: " + e);
        }
    } else if (key_oid == "1.2.840.10045.2.1") {
        cert.key_kind = KeyKind::Ec;
        std::string curve_oid;
        if (!decode_oid(key_params, curve_oid)) {
            return fail("certificate: missing EC curve OID");
        }
        cert.curve = ec_curve_for_oid(curve_oid);
        if (!cert.curve) {
            return fail("certificate: unsupported EC curve " + curve_oid);
        }
        const Bytes& b = cert.key_bits;
        const std::size_t coord_len = (cert.curve->p.bits() + 7) / 8;
        if (b.size() == 1 + 2 * coord_len && b[0] == 4) {
            cert.ec_x = BigUInt::from_be(
                ByteView(b).subspan(1, coord_len));
            cert.ec_y = BigUInt::from_be(
                ByteView(b).subspan(1 + coord_len, coord_len));
        } else if (b.size() == 1 + coord_len &&
                   (b[0] == 2 || b[0] == 3)) {
            cert.ec_x = BigUInt::from_be(ByteView(b).subspan(1, coord_len));
            // y^2 = x^3 + ax + b.  All supported fields have p == 3 mod 4.
            BigUInt rhs = BigUInt::add(
                BigUInt::add(BigUInt::mul_mod(
                                 BigUInt::mul_mod(cert.ec_x, cert.ec_x,
                                                  cert.curve->p),
                                 cert.ec_x, cert.curve->p),
                             BigUInt::mul_mod(cert.curve->a, cert.ec_x,
                                              cert.curve->p)),
                cert.curve->b);
            rhs = BigUInt::mod(rhs, cert.curve->p);
            // Use (p+1)/4.
            BigUInt quarter = BigUInt::add(cert.curve->p, BigUInt(1));
            quarter.shr1();
            quarter.shr1();
            BigUInt root = BigUInt::pow_mod(rhs, quarter, cert.curve->p);
            if (!root.zero() && root.even() != (b[0] == 2)) {
                root = BigUInt::sub(cert.curve->p, root);
            }
            cert.ec_y = root;
        } else {
            return fail("certificate: unsupported EC point encoding");
        }
    }

    // Extensions
    for (; i < tbs.children.size(); ++i) {
        const Der& optional = tbs.children[i];
        if (!optional.is_context(3) || optional.children.empty()) continue;
        const Der& extensions = optional.children[0];
        if (!expect_sequence(extensions, "Extensions", error)) {
            return fail("certificate: bad extensions");
        }
        for (const Der& ext : extensions.children) {
            if (!ext.is_universal(16) || ext.children.size() < 2) {
                return fail("certificate: bad extension");
            }
            std::string ext_oid;
            if (!decode_oid(ext.children[0], ext_oid)) {
                return fail("certificate: bad extension OID");
            }
            bool critical = false;
            std::size_t value_index = 1;
            if (ext.children[value_index].is_universal(1)) {
                if (!ext.children[value_index].content.empty()) {
                    critical = ext.children[value_index].content[0] != 0;
                }
                ++value_index;
            }
            if (value_index >= ext.children.size() ||
                !ext.children[value_index].is_universal(4)) {
                return fail("certificate: bad extension value");
            }
            Bytes value(ext.children[value_index].content.begin(),
                        ext.children[value_index].content.end());
            Der parsed;
            std::string ext_error;
            if (!parse_der_full(ByteView(value), parsed, ext_error)) {
                return fail("certificate: extension " + ext_oid + ": " +
                            ext_error);
            }
            if (ext_oid == "2.5.29.19") {  // basicConstraints
                cert.info.has_basic_constraints = true;
                cert.info.basic_constraints_critical = critical;
                if (parsed.is_universal(16)) {
                    for (const Der& f : parsed.children) {
                        if (f.is_universal(1)) {
                            cert.info.is_ca = !f.content.empty() &&
                                              f.content[0] != 0;
                        }
                    }
                }
            } else if (ext_oid == "2.5.29.15") {  // keyUsage
                cert.info.has_key_usage = true;
                cert.info.key_usage_critical = critical;
                if (parsed.is_universal(3) && !parsed.content.empty()) {
                    const unsigned unused = parsed.content[0];
                    const Bytes bits(parsed.content.begin() + 1,
                                     parsed.content.end());
                    auto bit = [&](unsigned n) {
                        const std::size_t byte = n / 8;
                        if (byte >= bits.size()) return false;
                        return ((bits[byte] >> (7 - (n % 8))) & 1u) != 0;
                    };
                    if (unused <= 7) {
                        cert.info.key_usage_digital_signature = bit(0);
                        cert.info.key_usage_non_repudiation = bit(1);
                        cert.info.key_usage_key_cert_sign = bit(5);
                    }
                }
            } else if (ext_oid == "2.5.29.37") {  // extKeyUsage
                cert.info.has_extended_key_usage = true;
                cert.info.extended_key_usage_critical = critical;
                if (parsed.is_universal(16)) {
                    for (const Der& c : parsed.children) {
                        std::string purpose;
                        if (decode_oid(c, purpose) &&
                            purpose == "1.3.6.1.5.5.7.3.8") {
                            cert.info.has_time_stamping_eku = true;
                        }
                    }
                }
            } else if (ext_oid == "2.5.29.14") {  // subjectKeyIdentifier
                if (parsed.is_universal(4)) {
                    cert.info.subject_key_id = hex_encode(parsed.content);
                }
            } else if (ext_oid == "2.5.29.35") {  // authorityKeyIdentifier
                if (parsed.is_universal(16)) {
                    for (const Der& f : parsed.children) {
                        if (f.is_context(0) && !f.content.empty()) {
                            cert.info.authority_key_id = hex_encode(f.content);
                        }
                    }
                }
            }
        }
    }
    return cert;
}

std::optional<CertificateInfo> parse_certificate(ByteView der,
                                                 std::string* error) {
    auto cert = parse_certificate_internal(der, error);
    if (!cert) return std::nullopt;
    return cert->info;
}

// ===========================================================================
// Signature verification (RSA PKCS#1 v1.5, RSA-PSS, ECDSA)
// ===========================================================================

enum class SignatureFamily {
    Unknown,
    RsaPkcs1,
    RsaPss,
    Ecdsa,
};

static std::optional<HashAlgorithm> hash_from_signature_oid(std::string_view oid) {
    static const std::map<std::string, HashAlgorithm> oids = {
        {"1.2.840.113549.1.1.4", HashAlgorithm::Md5},
        {"1.2.840.113549.1.1.5", HashAlgorithm::Sha1},
        {"1.2.840.113549.1.1.14", HashAlgorithm::Sha224},
        {"1.2.840.113549.1.1.11", HashAlgorithm::Sha256},
        {"1.2.840.113549.1.1.12", HashAlgorithm::Sha384},
        {"1.2.840.113549.1.1.13", HashAlgorithm::Sha512},
        {"1.2.840.10045.4.1", HashAlgorithm::Sha1},
        {"1.2.840.10045.4.3.1", HashAlgorithm::Sha224},
        {"1.2.840.10045.4.3.2", HashAlgorithm::Sha256},
        {"1.2.840.10045.4.3.3", HashAlgorithm::Sha384},
        {"1.2.840.10045.4.3.4", HashAlgorithm::Sha512}};
    const auto it = oids.find(std::string(oid));
    if (it == oids.end()) return std::nullopt;
    return it->second;
}

static SignatureFamily signature_family_from_oid(std::string_view oid) {
    if (oid == "1.2.840.113549.1.1.10") return SignatureFamily::RsaPss;
    if (oid == "1.2.840.113549.1.1.1" || oid == "1.2.840.113549.1.1.4" ||
        oid == "1.2.840.113549.1.1.5" || oid == "1.2.840.113549.1.1.11" ||
        oid == "1.2.840.113549.1.1.12" || oid == "1.2.840.113549.1.1.13" ||
        oid == "1.2.840.113549.1.1.14") {
        return SignatureFamily::RsaPkcs1;
    }
    if (oid == "1.2.840.10045.4.1" || oid == "1.2.840.10045.4.3.1" ||
        oid == "1.2.840.10045.4.3.2" || oid == "1.2.840.10045.4.3.3" ||
        oid == "1.2.840.10045.4.3.4") {
        return SignatureFamily::Ecdsa;
    }
    return SignatureFamily::Unknown;
}

static Bytes mgf1(std::string_view hash_oid, ByteView seed, std::size_t length) {
    auto alg = hash_algorithm_from_oid(hash_oid);
    const Bytes h = digest_bytes(alg.value_or(HashAlgorithm::Sha1), seed);
    Bytes out;
    out.reserve(length);
    for (u32 counter = 0; out.size() < length; ++counter) {
        Bytes block = h;
        block.push_back(static_cast<u8>(counter >> 24));
        block.push_back(static_cast<u8>(counter >> 16));
        block.push_back(static_cast<u8>(counter >> 8));
        block.push_back(static_cast<u8>(counter));
        const Bytes part = digest_bytes(alg.value_or(HashAlgorithm::Sha1),
                                        ByteView(block));
        const std::size_t take = std::min(part.size(), length - out.size());
        out.insert(out.end(), part.begin(), part.begin() + take);
    }
    return out;
}

static bool parse_rsa_pss_params(const Der& params, std::string& hash_oid,
                                 std::string& mgf_hash_oid, u32& salt_len,
                                 u32& trailer, std::string& error) {
    hash_oid = "1.3.14.3.2.26";       // SHA-1
    mgf_hash_oid = hash_oid;
    salt_len = 20;
    trailer = 1;
    if (params.full.empty()) return true;
    if (!params.is_universal(16)) {
        error = "RSASSA-PSS parameters must be a SEQUENCE";
        return false;
    }
    for (const Der& f : params.children) {
        if (f.is_context(0) && !f.children.empty()) {
            if (!parse_algorithm_identifier(f.children[0], hash_oid, nullptr,
                                            error)) {
                return false;
            }
        } else if (f.is_context(1) && !f.children.empty()) {
            const Der& mgf = f.children[0];
            if (!mgf.is_universal(16) || mgf.children.size() < 2) {
                error = "bad PSS maskGenAlgorithm";
                return false;
            }
            std::string mgf_oid;
            if (!decode_oid(mgf.children[0], mgf_oid) ||
                mgf_oid != "1.2.840.113549.1.1.8") {
                error = "unsupported PSS mask generation function";
                return false;
            }
            if (!parse_algorithm_identifier(mgf.children[1], mgf_hash_oid,
                                            nullptr, error)) {
                return false;
            }
        } else if (f.is_context(2) && !f.children.empty()) {
            if (!decode_integer_u32(f.children[0], salt_len, error)) return false;
        } else if (f.is_context(3) && !f.children.empty()) {
            if (!decode_integer_u32(f.children[0], trailer, error)) return false;
        }
    }
    return true;
}

static bool verify_rsa_pkcs1(const BigUInt& n, const BigUInt& e,
                             HashAlgorithm hash, ByteView message,
                             ByteView signature, std::string& error) {
    if (n.zero() || n.bits() < 16) {
        error = "RSA modulus is invalid";
        return false;
    }
    const std::size_t k = (n.bits() + 7) / 8;
    if (signature.size() > k) {
        error = "RSA signature longer than modulus";
        return false;
    }
    Bytes sig(k, 0);
    std::copy(signature.begin(), signature.end(),
              sig.begin() + (k - signature.size()));
    const BigUInt s = BigUInt::from_be(ByteView(sig));
    if (s.cmp(n) >= 0) {
        error = "RSA signature representative out of range";
        return false;
    }
    const BigUInt m = BigUInt::pow_mod(s, e, n);
    const Bytes em = m.to_be(k);
    if (em.size() != k || em[0] != 0 || em[1] != 1) {
        error = "RSA PKCS#1 padding error";
        return false;
    }
    std::size_t pos = 2;
    while (pos < em.size() && em[pos] == 0xff) ++pos;
    if (pos < 2 + 8 || pos >= em.size() || em[pos] != 0) {
        error = "RSA PKCS#1 padding too short";
        return false;
    }
    Bytes digest_info(em.begin() + pos + 1, em.end());
    Der di;
    std::string der_error;
    if (!parse_der_full(ByteView(digest_info), di, der_error) ||
        !di.is_universal(16) || di.children.size() < 2) {
        error = "RSA DigestInfo is malformed";
        return false;
    }
    std::string alg_oid;
    if (!parse_algorithm_identifier(di.children[0], alg_oid, nullptr, der_error)) {
        error = "RSA DigestInfo algorithm is malformed";
        return false;
    }
    auto di_alg = hash_algorithm_from_oid(alg_oid);
    if (!di_alg || *di_alg != hash) {
        error = "RSA DigestInfo algorithm does not match signature algorithm";
        return false;
    }
    Bytes di_digest;
    if (!expect_octet_string(di.children[1], di_digest, "digest", der_error)) {
        error = "RSA DigestInfo digest is malformed";
        return false;
    }
    const Bytes expected = digest_bytes(hash, message);
    if (di_digest != expected) {
        error = "RSA signature digest does not match signed data";
        return false;
    }
    return true;
}

static bool verify_rsa_pss(const BigUInt& n, const BigUInt& e,
                           const Der& params, ByteView message,
                           ByteView signature, std::string& error) {
    if (n.zero() || n.bits() < 16) {
        error = "RSA modulus is invalid";
        return false;
    }
    std::string hash_oid, mgf_hash_oid;
    u32 salt_len = 0;
    u32 trailer = 1;
    if (!parse_rsa_pss_params(params, hash_oid, mgf_hash_oid, salt_len, trailer,
                              error)) {
        return false;
    }
    if (trailer != 1) {
        error = "unsupported RSA-PSS trailer field";
        return false;
    }
    auto hash = hash_algorithm_from_oid(hash_oid);
    if (!hash) {
        error = "unsupported RSA-PSS hash algorithm";
        return false;
    }
    const std::size_t k = (n.bits() + 7) / 8;
    const std::size_t em_bits = n.bits() - 1;
    const std::size_t em_len = (em_bits + 7) / 8;
    if (signature.size() > k) {
        error = "RSA-PSS signature longer than modulus";
        return false;
    }
    Bytes sig(k, 0);
    std::copy(signature.begin(), signature.end(),
              sig.begin() + (k - signature.size()));
    const BigUInt s = BigUInt::from_be(ByteView(sig));
    if (s.cmp(n) >= 0) {
        error = "RSA-PSS signature representative out of range";
        return false;
    }
    const BigUInt m = BigUInt::pow_mod(s, e, n);
    Bytes em = m.to_be(k);
    if (em.size() > em_len) {
        em.erase(em.begin(), em.begin() + (em.size() - em_len));
    } else if (em.size() < em_len) {
        Bytes padded(em_len, 0);
        std::copy(em.begin(), em.end(),
                  padded.begin() + (em_len - em.size()));
        em = std::move(padded);
    }
    const size_t h_len = digest_bytes(*hash, ByteView{}).size();
    if (em_len < h_len + 2 || salt_len > em_len - h_len - 2) {
        error = "RSA-PSS parameters exceed modulus";
        return false;
    }
    const std::size_t db_len = em_len - h_len - 1;
    if (em[em_len - 1] != 0xbc) {
        error = "RSA-PSS trailer byte is wrong";
        return false;
    }
    const unsigned top_bits = static_cast<unsigned>(8 * em_len - em_bits);
    const u8 top_mask = static_cast<u8>(0xff >> top_bits);
    if ((em[0] & ~top_mask) != 0) {
        error = "RSA-PSS leading bits are not zero";
        return false;
    }
    const Bytes h(em.begin() + db_len, em.begin() + db_len + h_len);
    Bytes masked_db(em.begin(), em.begin() + db_len);
    const Bytes db_mask = mgf1(mgf_hash_oid, ByteView(h), db_len);
    Bytes db(db_len, 0);
    for (std::size_t i = 0; i < db_len; ++i) db[i] = masked_db[i] ^ db_mask[i];
    db[0] &= top_mask;
    const std::size_t ps_len = em_len - h_len - salt_len - 2;
    for (std::size_t i = 0; i < ps_len; ++i) {
        if (db[i] != 0) {
            error = "RSA-PSS padding bytes are not zero";
            return false;
        }
    }
    if (db[ps_len] != 0x01) {
        error = "RSA-PSS separator byte is wrong";
        return false;
    }
    const Bytes salt(db.begin() + ps_len + 1, db.end());
    Bytes m_prime(8, 0);
    const Bytes m_hash = digest_bytes(*hash, message);
    m_prime.insert(m_prime.end(), m_hash.begin(), m_hash.end());
    m_prime.insert(m_prime.end(), salt.begin(), salt.end());
    const Bytes h_prime = digest_bytes(*hash, ByteView(m_prime));
    if (h_prime != h) {
        error = "RSA-PSS hash does not match signed data";
        return false;
    }
    return true;
}

struct EcPoint {
    BigUInt x;
    BigUInt y;
    bool infinity = true;
};

static bool ec_is_on_curve(const EcCurve& c, const BigUInt& x,
                           const BigUInt& y) {
    const BigUInt lhs = BigUInt::mod(BigUInt::mul(y, y), c.p);
    BigUInt rhs = BigUInt::add(
        BigUInt::add(BigUInt::mul_mod(BigUInt::mul_mod(x, x, c.p), x, c.p),
                     BigUInt::mul_mod(c.a, x, c.p)),
        c.b);
    rhs = BigUInt::mod(rhs, c.p);
    return lhs == rhs;
}

static EcPoint ec_double(const EcCurve& c, const EcPoint& p) {
    if (p.infinity || p.y.zero()) return EcPoint{};
    const BigUInt inv = BigUInt::inv_mod(
        BigUInt::mul_mod(BigUInt(2), p.y, c.p), c.p);
    BigUInt num = BigUInt::mul_mod(BigUInt(3), BigUInt::mul_mod(p.x, p.x, c.p),
                                   c.p);
    // lambda = (3x^2 + a) / 2y.  For the NIST curves a = p-3.
    num = BigUInt::add_mod(num, BigUInt::mod(c.a, c.p), c.p);
    const BigUInt lambda = BigUInt::mul_mod(num, inv, c.p);
    const BigUInt x3 = BigUInt::sub_mod(
        BigUInt::mul_mod(lambda, lambda, c.p),
        BigUInt::add_mod(p.x, p.x, c.p), c.p);
    const BigUInt y3 = BigUInt::sub_mod(
        BigUInt::mul_mod(lambda, BigUInt::sub_mod(p.x, x3, c.p), c.p),
        p.y, c.p);
    EcPoint r;
    r.infinity = false;
    r.x = x3;
    r.y = y3;
    return r;
}

static EcPoint ec_add(const EcCurve& c, const EcPoint& p, const EcPoint& q) {
    if (p.infinity) return q;
    if (q.infinity) return p;
    if (p.x == q.x) {
        if (BigUInt::mod(BigUInt::add(p.y, q.y), c.p).zero()) return EcPoint{};
        return ec_double(c, p);
    }
    const BigUInt dx = BigUInt::sub_mod(q.x, p.x, c.p);
    const BigUInt dy = BigUInt::sub_mod(q.y, p.y, c.p);
    const BigUInt inv = BigUInt::inv_mod(dx, c.p);
    const BigUInt lambda = BigUInt::mul_mod(dy, inv, c.p);
    const BigUInt x3 = BigUInt::sub_mod(
        BigUInt::sub_mod(BigUInt::mul_mod(lambda, lambda, c.p), p.x, c.p),
        q.x, c.p);
    const BigUInt y3 = BigUInt::sub_mod(
        BigUInt::mul_mod(lambda, BigUInt::sub_mod(p.x, x3, c.p), c.p),
        p.y, c.p);
    EcPoint r;
    r.infinity = false;
    r.x = x3;
    r.y = y3;
    return r;
}

static EcPoint ec_mul(const EcCurve& c, const BigUInt& k, const EcPoint& p) {
    EcPoint result;
    EcPoint base = p;
    BigUInt scalar = k;
    while (!scalar.zero()) {
        if (!scalar.even()) result = ec_add(c, result, base);
        scalar.shr1();
        if (!scalar.zero()) base = ec_double(c, base);
    }
    return result;
}

static bool parse_ecdsa_signature(ByteView sig, BigUInt& r, BigUInt& s,
                                  std::string& error) {
    Der root;
    if (!parse_der_full(sig, root, error)) return false;
    if (!root.is_universal(16) || root.children.size() != 2) {
        error = "ECDSA signature must be SEQUENCE { r, s }";
        return false;
    }
    std::string e;
    if (!decode_positive_integer(root.children[0], r, e) ||
        !decode_positive_integer(root.children[1], s, e)) {
        error = "ECDSA signature: " + e;
        return false;
    }
    return true;
}

static bool verify_ecdsa(const ParsedCert& key, HashAlgorithm hash,
                         ByteView message, ByteView signature,
                         std::string& error) {
    if (key.key_kind != KeyKind::Ec || !key.curve) {
        error = "issuer key is not an EC key";
        return false;
    }
    BigUInt r, s;
    if (!parse_ecdsa_signature(signature, r, s, error)) return false;
    const EcCurve& c = *key.curve;
    if (r.zero() || s.zero() || r.cmp(c.n) >= 0 || s.cmp(c.n) >= 0) {
        error = "ECDSA signature values out of range";
        return false;
    }
    EcPoint pub;
    pub.infinity = false;
    pub.x = key.ec_x;
    pub.y = key.ec_y;
    if (!ec_is_on_curve(c, pub.x, pub.y)) {
        error = "EC public key is not on the curve";
        return false;
    }
    const Bytes digest = digest_bytes(hash, message);
    BigUInt z = BigUInt::from_be(ByteView(digest));
    const std::size_t digest_bits = digest.size() * 8;
    if (digest_bits > c.n.bits()) {
        for (std::size_t i = 0; i < digest_bits - c.n.bits(); ++i) z.shr1();
    }
    z = BigUInt::mod(z, c.n);
    BigUInt w = BigUInt::inv_mod(s, c.n);
    const BigUInt u1 = BigUInt::mod(BigUInt::mul(z, w), c.n);
    const BigUInt u2 = BigUInt::mod(BigUInt::mul(r, w), c.n);
    EcPoint g;
    g.infinity = false;
    g.x = c.gx;
    g.y = c.gy;
    const EcPoint p1 = ec_mul(c, u1, g);
    const EcPoint p2 = ec_mul(c, u2, pub);
    const EcPoint sum = ec_add(c, p1, p2);
    if (sum.infinity) {
        error = "ECDSA verification point is at infinity";
        return false;
    }
    const BigUInt v = BigUInt::mod(sum.x, c.n);
    if (v != r) {
        error = "ECDSA signature does not match signed data";
        return false;
    }
    return true;
}

static bool verify_signature_with_key(const ParsedCert& key,
                                      std::string_view sig_oid,
                                      const Der& sig_params,
                                      std::optional<HashAlgorithm> explicit_hash,
                                      ByteView message, ByteView signature,
                                      std::string& error) {
    const SignatureFamily family = signature_family_from_oid(sig_oid);
    if (family == SignatureFamily::Unknown) {
        error = "unsupported signature algorithm " + std::string(sig_oid);
        return false;
    }
    std::optional<HashAlgorithm> hash = hash_from_signature_oid(sig_oid);
    if (!hash && explicit_hash) hash = explicit_hash;
    if (!hash) {
        error = "signature algorithm carries no supported hash";
        return false;
    }
    if (family == SignatureFamily::RsaPkcs1 ||
        family == SignatureFamily::RsaPss) {
        if (key.key_kind != KeyKind::Rsa) {
            error = "issuer key is not an RSA key";
            return false;
        }
        if (family == SignatureFamily::RsaPss) {
            return verify_rsa_pss(key.rsa_n, key.rsa_e, sig_params, message,
                                  signature, error);
        }
        return verify_rsa_pkcs1(key.rsa_n, key.rsa_e, *hash, message,
                                signature, error);
    }
    return verify_ecdsa(key, *hash, message, signature, error);
}

static bool verify_certificate_signature(const ParsedCert& cert,
                                         const ParsedCert& issuer,
                                         std::string& error) {
    Der params;
    if (!cert.signature_params_der.empty()) {
        std::string der_error;
        if (!parse_der_full(ByteView(cert.signature_params_der), params,
                            der_error)) {
            error = "certificate signature parameters: " + der_error;
            return false;
        }
    }
    return verify_signature_with_key(issuer, cert.signature_algorithm_oid,
                                     params, std::nullopt,
                                     ByteView(cert.tbs),
                                     ByteView(cert.signature), error);
}

// ===========================================================================
// RFC 3161 / CMS parsing
// ===========================================================================

static Bytes der_length(std::size_t length) {
    Bytes out;
    if (length < 0x80) {
        out.push_back(static_cast<u8>(length));
        return out;
    }
    u8 tmp[sizeof(std::size_t)];
    std::size_t n = 0;
    while (length) {
        tmp[n++] = static_cast<u8>(length);
        length >>= 8;
    }
    out.push_back(static_cast<u8>(0x80 | n));
    for (std::size_t i = n; i-- > 0;) out.push_back(tmp[i]);
    return out;
}

static Bytes der_wrap(u8 tag, ByteView content) {
    Bytes out;
    out.push_back(tag);
    const Bytes len = der_length(content.size());
    out.insert(out.end(), len.begin(), len.end());
    out.insert(out.end(), content.begin(), content.end());
    return out;
}

static bool collect_octet_string(const Der& node, Bytes& out,
                                 std::string& error) {
    if (!node.is_universal(4)) {
        error = "expected OCTET STRING";
        return false;
    }
    if (!node.constructed) {
        out.insert(out.end(), node.content.begin(), node.content.end());
        return true;
    }
    for (const Der& child : node.children) {
        if (!collect_octet_string(child, out, error)) return false;
    }
    return true;
}

static bool parse_accuracy(const Der& node, Accuracy& out,
                           std::string& error) {
    if (!node.is_universal(16)) {
        error = "Accuracy must be a SEQUENCE";
        return false;
    }
    unsigned untagged_index = 0;
    for (const Der& f : node.children) {
        u32 v = 0;
        if (f.is_universal(2)) {
            if (!decode_integer_u32(f, v, error)) return false;
            if (untagged_index == 0) out.seconds = v;
            else if (untagged_index == 1) out.millis = v;
            else if (untagged_index == 2) out.micros = v;
            ++untagged_index;
            continue;
        }
        if (f.cls != 2 || f.tag > 2) {
            error = "unexpected field in Accuracy";
            return false;
        }
        if (f.constructed && !f.children.empty()) {
            if (!decode_integer_u32(f.children[0], v, error)) return false;
        } else {
            Der integer;
            integer.cls = 0;
            integer.constructed = false;
            integer.tag = 2;
            integer.content = f.content;
            if (!decode_integer_u32(integer, v, error)) return false;
        }
        if (f.tag == 0) out.seconds = v;
        else if (f.tag == 1) out.millis = v;
        else if (f.tag == 2) out.micros = v;
    }
    return true;
}

static bool parse_general_name(const Der& name, std::string& text,
                               std::string& error) {
    if (name.cls != 2) {
        error = "GeneralName must be context specific";
        return false;
    }
    switch (name.tag) {
        case 1:  // rfc822Name
        case 2:  // dNSName
        case 6:  // URI
        case 7:  // iPAddress
            text.assign(reinterpret_cast<const char*>(name.content.data()),
                        name.content.size());
            return true;
        case 4: {  // directoryName
            if (name.children.empty()) {
                error = "empty directoryName";
                return false;
            }
            return parse_name(name.children[0], text, error);
        }
        default:
            text = hex_encode(name.content);
            return true;
    }
}

static bool parse_message_imprint(const Der& node, MessageImprint& out,
                                  std::string& error) {
    if (!expect_sequence(node, "MessageImprint", error) ||
        node.children.size() < 2) {
        error = "bad MessageImprint";
        return false;
    }
    std::string oid;
    if (!parse_algorithm_identifier(node.children[0], oid, nullptr, error)) {
        return false;
    }
    out.algorithm_oid = oid;
    out.algorithm = hash_algorithm_from_oid(oid).value_or(HashAlgorithm::Unknown);
    if (!expect_octet_string(node.children[1], out.digest, "hashedMessage",
                             error)) {
        return false;
    }
    return true;
}

static bool parse_tst_info(ByteView der, TimestampInfo& out,
                           std::string& error) {
    out.raw_der.assign(der.begin(), der.end());
    Der root;
    if (!parse_der_full(der, root, error)) return false;
    if (!expect_sequence(root, "TSTInfo", error) || root.children.size() < 5) {
        error = "TSTInfo is too short";
        return false;
    }
    std::size_t i = 0;
    if (!decode_int32(root.children[i++], out.version, error)) return false;
    if (out.version != 1) {
        error = "unsupported TSTInfo version";
        return false;
    }
    if (!expect_oid(root.children[i++], out.policy_oid, "policy", error)) {
        return false;
    }
    if (!parse_message_imprint(root.children[i++], out.message_imprint,
                               error)) {
        return false;
    }
    if (out.message_imprint.algorithm != HashAlgorithm::Unknown &&
        out.message_imprint.digest.size() !=
            hash_output_size(out.message_imprint.algorithm)) {
        error = "messageImprint digest length does not match its algorithm";
        return false;
    }
    BigUInt serial;
    if (!decode_positive_integer(root.children[i++], serial, error)) {
        return false;
    }
    out.serial_number = serial.to_decimal();
    if (auto v = serial.to_u64()) {
        out.serial_fits_uint64 = true;
        out.serial_uint64 = *v;
    }
    if (!parse_der_time(root.children[i++], out.gen_time, out.gen_time_utc,
                        error)) {
        return false;
    }
    for (; i < root.children.size(); ++i) {
        const Der& f = root.children[i];
        if (f.is_universal(16)) {
            if (!parse_accuracy(f, out.accuracy, error)) return false;
        } else if (f.is_universal(1)) {
            out.ordering = !f.content.empty() && f.content[0] != 0;
        } else if (f.is_universal(2)) {
            BigUInt nonce;
            if (!decode_positive_integer(f, nonce, error)) return false;
            out.nonce = nonce.to_decimal();
            if (auto v = nonce.to_u64()) {
                out.nonce_fits_uint64 = true;
                out.nonce_uint64 = *v;
            }
        } else if (f.is_context(0)) {
            if (f.children.empty()) {
                error = "empty TSA GeneralName";
                return false;
            }
            out.tsa_name_der.assign(f.full.begin(), f.full.end());
            std::string name;
            std::string name_error;
            if (parse_general_name(f.children[0], name, name_error)) {
                out.tsa_name = name;
            } else {
                out.tsa_name = hex_encode(f.children[0].full);
            }
        }
        // [1] extensions are intentionally preserved only in raw_der.
    }
    return true;
}

struct ParsedSignerInfo {
    SignerIdentifierType sid_type = SignerIdentifierType::Unknown;
    Bytes sid_issuer;
    std::string sid_serial;
    Bytes sid_ski;
    std::string digest_oid;
    std::string signature_oid;
    Bytes signature_params_der;
    Bytes signature;
    bool has_signed_attrs = false;
    Bytes signed_attrs_der;
    bool has_content_type = false;
    std::string content_type;
    bool has_message_digest = false;
    Bytes message_digest;
    bool has_signing_certificate = false;
    std::string signing_certificate_oid;
    std::string signing_certificate_hash_oid;
    Bytes signing_certificate_hash;
    std::string signing_certificate_serial;
    Bytes signing_certificate_issuer;
    std::optional<std::size_t> signer_cert_index;
};

struct ParsedTimestampToken {
    Bytes der;
    TimestampInfo info;
    std::vector<ParsedCert> certificates;
    std::vector<Bytes> embedded_crls;
    std::vector<std::string> cms_digest_algorithms;
    Bytes econtent;
    bool econtent_type_is_tst_info = false;
    ParsedSignerInfo signer;
};

static bool parse_ess_issuer_serial(const Der& node, Bytes& issuer,
                                    std::string& serial, std::string& error) {
    if (!node.is_universal(16) || node.children.empty()) return true;
    const Der& names = node.children[0];
    if (names.is_universal(16) && !names.children.empty()) {
        issuer.assign(names.children[0].full.begin(), names.children[0].full.end());
    }
    if (node.children.size() > 1) {
        BigUInt value;
        if (!decode_positive_integer(node.children[1], value, error)) {
            return false;
        }
        serial = value.to_decimal();
    }
    return true;
}

static bool parse_signing_certificate_attribute(const Der& value,
                                                ParsedSignerInfo& signer,
                                                std::string& error) {
    if (!value.is_universal(16) || value.children.empty()) {
        error = "signingCertificate attribute must be a SEQUENCE";
        return false;
    }
    const Der& certs = value.children[0];
    if (!certs.is_universal(16) || certs.children.empty()) {
        error = "signingCertificate has no certs sequence";
        return false;
    }
    const Der& entry = certs.children[0];
    if (!entry.is_universal(16) || entry.children.empty()) {
        error = "signingCertificate ESSCertID is malformed";
        return false;
    }
    std::size_t i = 0;
    signer.signing_certificate_hash_oid =
        signer.signing_certificate_oid == "1.2.840.113549.1.9.16.2.12"
            ? hash_algorithm_oid(HashAlgorithm::Sha1)
            : hash_algorithm_oid(HashAlgorithm::Sha256);
    if (entry.children[i].is_universal(16)) {
        std::string oid;
        if (!parse_algorithm_identifier(entry.children[i], oid, nullptr,
                                        error)) {
            return false;
        }
        signer.signing_certificate_hash_oid = oid;
        ++i;
    }
    if (i >= entry.children.size() || !entry.children[i].is_universal(4)) {
        error = "signingCertificate is missing certHash";
        return false;
    }
    signer.signing_certificate_hash.assign(entry.children[i].content.begin(),
                                           entry.children[i].content.end());
    ++i;
    if (i < entry.children.size()) {
        if (!parse_ess_issuer_serial(entry.children[i],
                                     signer.signing_certificate_issuer,
                                     signer.signing_certificate_serial, error)) {
            return false;
        }
    }
    return true;
}

static bool parse_signer_info(const Der& node, ParsedSignerInfo& out,
                              std::string& error) {
    if (!node.is_universal(16) || node.children.size() < 5) {
        error = "SignerInfo is too short";
        return false;
    }
    std::size_t i = 0;
    std::int32_t version = 0;
    if (!decode_int32(node.children[i++], version, error)) return false;
    const Der& sid = node.children[i++];
    if (sid.is_universal(16) && sid.children.size() == 2) {
        out.sid_type = SignerIdentifierType::IssuerAndSerialNumber;
        out.sid_issuer.assign(sid.children[0].full.begin(),
                              sid.children[0].full.end());
        BigUInt serial;
        if (!decode_positive_integer(sid.children[1], serial, error)) {
            return false;
        }
        out.sid_serial = serial.to_decimal();
    } else if (sid.is_context(0)) {
        out.sid_type = SignerIdentifierType::SubjectKeyIdentifier;
        out.sid_ski.assign(sid.content.begin(), sid.content.end());
    } else {
        error = "unsupported SignerIdentifier";
        return false;
    }
    if (!parse_algorithm_identifier(node.children[i++], out.digest_oid, nullptr,
                                    error)) {
        return false;
    }
    if (i < node.children.size() && node.children[i].is_context(0)) {
        out.has_signed_attrs = true;
        const Der& attrs = node.children[i++];
        out.signed_attrs_der = der_wrap(0x31, attrs.content);
        for (const Der& attr : attrs.children) {
            if (!attr.is_universal(16) || attr.children.size() < 2) {
                error = "malformed signed attribute";
                return false;
            }
            std::string oid;
            if (!decode_oid(attr.children[0], oid)) {
                error = "malformed signed attribute OID";
                return false;
            }
            const Der& values = attr.children[1];
            if (!values.is_universal(17) || values.children.empty()) {
                error = "signed attribute has no value";
                return false;
            }
            const Der& value = values.children[0];
            if (oid == "1.2.840.113549.1.9.3") {  // content-type
                if (out.has_content_type) {
                    error = "duplicate content-type attribute";
                    return false;
                }
                out.has_content_type = true;
                if (!decode_oid(value, out.content_type)) {
                    error = "bad content-type attribute";
                    return false;
                }
            } else if (oid == "1.2.840.113549.1.9.4") {  // message-digest
                if (out.has_message_digest) {
                    error = "duplicate message-digest attribute";
                    return false;
                }
                out.has_message_digest = true;
                if (!value.is_universal(4)) {
                    error = "bad message-digest attribute";
                    return false;
                }
                out.message_digest.assign(value.content.begin(),
                                          value.content.end());
            } else if (oid == "1.2.840.113549.1.9.16.2.12" ||
                       oid == "1.2.840.113549.1.9.16.2.47") {
                if (out.has_signing_certificate) {
                    error = "duplicate signing-certificate attribute";
                    return false;
                }
                out.has_signing_certificate = true;
                out.signing_certificate_oid = oid;
                if (!parse_signing_certificate_attribute(value, out, error)) {
                    return false;
                }
            }
        }
    }
    if (i >= node.children.size()) {
        error = "SignerInfo is missing signatureAlgorithm";
        return false;
    }
    Der params;
    if (!parse_algorithm_identifier(node.children[i++], out.signature_oid,
                                    &params, error)) {
        return false;
    }
    if (!params.full.empty()) {
        out.signature_params_der.assign(params.full.begin(), params.full.end());
    }
    if (i >= node.children.size() ||
        !node.children[i].is_universal(4)) {
        error = "SignerInfo is missing signature";
        return false;
    }
    out.signature.assign(node.children[i].content.begin(),
                         node.children[i].content.end());
    return true;
}

static std::optional<std::size_t> match_signer_certificate(
    const ParsedSignerInfo& signer, const std::vector<ParsedCert>& certs) {
    for (std::size_t i = 0; i < certs.size(); ++i) {
        const ParsedCert& cert = certs[i];
        if (signer.sid_type == SignerIdentifierType::IssuerAndSerialNumber) {
            if (cert.info.serial_number != signer.sid_serial) continue;
            if (cert.info.issuer_der == signer.sid_issuer) return i;
        } else if (signer.sid_type ==
                   SignerIdentifierType::SubjectKeyIdentifier) {
            if (!signer.sid_ski.empty() &&
                lower_ascii(cert.info.subject_key_id) ==
                    lower_ascii(hex_encode(signer.sid_ski))) {
                return i;
            }
        }
    }
    // Some CAs re-encode an equivalent DN.  Fall back to serial plus a
    // normalized distinguished-name comparison, never just serial.
    for (std::size_t i = 0; i < certs.size(); ++i) {
        const ParsedCert& cert = certs[i];
        if (signer.sid_type != SignerIdentifierType::IssuerAndSerialNumber) {
            continue;
        }
        if (cert.info.serial_number != signer.sid_serial) continue;
        std::string sid_name;
        std::string err;
        Der name;
        if (parse_der_full(ByteView(signer.sid_issuer), name, err) &&
            parse_name(name, sid_name, err) &&
            collapse_ws(sid_name) == collapse_ws(cert.info.issuer)) {
            return i;
        }
    }
    return std::nullopt;
}

static ImportResult parse_token_der(ByteView der) {
    ImportResult result;
    auto parsed = std::make_shared<ParsedTimestampToken>();
    parsed->der.assign(der.begin(), der.end());

    std::string error;
    Der root;
    if (!parse_der_full(ByteView(parsed->der), root, error)) {
        result.code = ErrorCode::InvalidDer;
        result.message = "timestamp token: " + error;
        return result;
    }
    if (!expect_sequence(root, "ContentInfo", error) ||
        root.children.size() < 2) {
        result.code = ErrorCode::InvalidTokenStructure;
        result.message = "timestamp token: ContentInfo is malformed";
        return result;
    }
    std::string content_type;
    if (!expect_oid(root.children[0], content_type, "contentType", error)) {
        result.code = ErrorCode::InvalidTokenStructure;
        result.message = "timestamp token: " + error;
        return result;
    }
    if (content_type != "1.2.840.113549.1.7.2") {
        result.code = ErrorCode::InvalidContentType;
        result.message = "timestamp token is not CMS SignedData";
        return result;
    }
    if (!root.children[1].is_context(0) ||
        root.children[1].children.empty()) {
        result.code = ErrorCode::InvalidTokenStructure;
        result.message = "timestamp token: missing SignedData content";
        return result;
    }
    const Der& signed_data = root.children[1].children[0];
    if (!expect_sequence(signed_data, "SignedData", error) ||
        signed_data.children.size() < 4) {
        result.code = ErrorCode::InvalidTokenStructure;
        result.message = "timestamp token: bad SignedData";
        return result;
    }

    std::size_t i = 0;
    std::int32_t version = 0;
    if (!decode_int32(signed_data.children[i++], version, error)) {
        result.code = ErrorCode::InvalidTokenStructure;
        result.message = "timestamp token: " + error;
        return result;
    }
    const Der& digest_set = signed_data.children[i++];
    if (!expect_set(digest_set, "digestAlgorithms", error)) {
        result.code = ErrorCode::InvalidTokenStructure;
        result.message = "timestamp token: " + error;
        return result;
    }
    for (const Der& alg : digest_set.children) {
        std::string oid;
        if (!parse_algorithm_identifier(alg, oid, nullptr, error)) {
            result.code = ErrorCode::InvalidTokenStructure;
            result.message = "timestamp token: " + error;
            return result;
        }
        parsed->cms_digest_algorithms.push_back(oid);
    }

    const Der& encap = signed_data.children[i++];
    if (!expect_sequence(encap, "EncapsulatedContentInfo", error) ||
        encap.children.empty()) {
        result.code = ErrorCode::InvalidTokenStructure;
        result.message = "timestamp token: bad EncapsulatedContentInfo";
        return result;
    }
    std::string econtent_type;
    if (!decode_oid(encap.children[0], econtent_type)) {
        result.code = ErrorCode::InvalidTokenStructure;
        result.message = "timestamp token: bad eContentType";
        return result;
    }
    parsed->econtent_type_is_tst_info =
        econtent_type == "1.2.840.113549.1.9.16.1.4";
    if (!parsed->econtent_type_is_tst_info) {
        result.code = ErrorCode::InvalidContentType;
        result.message = "timestamp token eContentType is not id-ct-TSTInfo";
        return result;
    }
    if (encap.children.size() > 1 && encap.children[1].is_context(0) &&
        !encap.children[1].children.empty()) {
        if (!collect_octet_string(encap.children[1].children[0],
                                  parsed->econtent, error)) {
            result.code = ErrorCode::InvalidTokenStructure;
            result.message = "timestamp token: " + error;
            return result;
        }
    } else {
        result.code = ErrorCode::InvalidTokenStructure;
        result.message = "timestamp token has no TSTInfo content";
        return result;
    }

    for (; i < signed_data.children.size(); ++i) {
        const Der& field = signed_data.children[i];
        if (field.is_context(0)) {
            for (const Der& choice : field.children) {
                if (!choice.is_universal(16)) continue;  // other CertificateChoices
                std::string cert_error;
                auto cert = parse_certificate_internal(choice.full, &cert_error);
                if (!cert) {
                    result.code = ErrorCode::CertificateInvalid;
                    result.message = "timestamp token: " + cert_error;
                    return result;
                }
                parsed->certificates.push_back(std::move(*cert));
            }
        } else if (field.is_context(1)) {
            for (const Der& crl : field.children) {
                if (crl.is_universal(16)) {
                    parsed->embedded_crls.emplace_back(crl.full.begin(),
                                                       crl.full.end());
                }
            }
        } else if (field.is_universal(17)) {  // signerInfos
            if (field.children.empty()) {
                result.code = ErrorCode::InvalidTokenStructure;
                result.message = "timestamp token has no SignerInfo";
                return result;
            }
            if (field.children.size() != 1) {
                result.code = ErrorCode::UnsupportedStructure;
                result.message =
                    "timestamp token has multiple SignerInfo values";
                return result;
            }
            if (!parse_signer_info(field.children[0], parsed->signer, error)) {
                result.code = ErrorCode::InvalidTokenStructure;
                result.message = "timestamp token: " + error;
                return result;
            }
        }
    }

    if (!parse_tst_info(ByteView(parsed->econtent), parsed->info, error)) {
        result.code = ErrorCode::InvalidTokenStructure;
        result.message = "timestamp token TSTInfo: " + error;
        return result;
    }
    if (parsed->signer.signature.empty()) {
        result.code = ErrorCode::InvalidTokenStructure;
        result.message = "timestamp token has no signature";
        return result;
    }
    parsed->signer.signer_cert_index =
        match_signer_certificate(parsed->signer, parsed->certificates);

    result.code = ErrorCode::Ok;
    result.token.raw_der = parsed->der;
    result.token.tst_info = parsed->info;
    for (const ParsedCert& cert : parsed->certificates) {
        result.token.certificates.push_back(cert.info);
    }
    if (parsed->signer.signer_cert_index) {
        result.token.signer_certificate =
            parsed->certificates[*parsed->signer.signer_cert_index].info;
    }
    result.token.signer_issuer = [&] {
        std::string name;
        std::string err;
        Der n;
        if (!parsed->signer.sid_issuer.empty() &&
            parse_der_full(ByteView(parsed->signer.sid_issuer), n, err)) {
            parse_name(n, name, err);
        }
        return name;
    }();
    result.token.signer_serial = parsed->signer.sid_serial;
    result.token.signer_identifier_type = parsed->signer.sid_type;
    result.token.cms_digest_algorithms = parsed->cms_digest_algorithms;
    result.token.signer_digest_algorithm_oid = parsed->signer.digest_oid;
    result.token.signature_algorithm_oid = parsed->signer.signature_oid;
    result.token.parsed = std::move(parsed);
    return result;
}

// ===========================================================================
// CRL and OCSP parsing
// ===========================================================================

struct ParsedCrl {
    Bytes der;
    Bytes tbs;
    Bytes signature;
    std::string signature_algorithm_oid;
    Bytes issuer_der;
    std::string issuer_name;
    TimePoint this_update{};
    TimePoint next_update{};
    bool has_next_update = false;
    std::set<std::string> revoked_serials;
};

static std::optional<ParsedCrl> parse_crl_internal(ByteView der,
                                                   std::string& error) {
    ParsedCrl crl;
    crl.der.assign(der.begin(), der.end());
    Der root;
    if (!parse_der_full(der, root, error) ||
        !root.is_universal(16) || root.children.size() != 3) {
        if (error.empty()) error = "CRL must be a SEQUENCE of three elements";
        return std::nullopt;
    }
    const Der& tbs = root.children[0];
    if (!tbs.is_universal(16)) {
        error = "CRL tbsCertList is not a SEQUENCE";
        return std::nullopt;
    }
    crl.tbs.assign(tbs.full.begin(), tbs.full.end());
    std::string sig_oid;
    if (!parse_algorithm_identifier(root.children[1], sig_oid, nullptr, error)) {
        return std::nullopt;
    }
    crl.signature_algorithm_oid = sig_oid;
    const Der& sig_bit = root.children[2];
    if (!sig_bit.is_universal(3) || sig_bit.content.empty() ||
        sig_bit.content[0] != 0) {
        error = "CRL signature is not a BIT STRING";
        return std::nullopt;
    }
    crl.signature.assign(sig_bit.content.begin() + 1, sig_bit.content.end());

    std::size_t i = 0;
    if (i < tbs.children.size() && tbs.children[i].is_universal(2)) {
        ++i;  // version
    }
    if (i + 2 >= tbs.children.size()) {
        error = "CRL tbsCertList is too short";
        return std::nullopt;
    }
    ++i;  // signature AlgorithmIdentifier
    const Der& issuer = tbs.children[i++];
    if (!parse_name(issuer, crl.issuer_name, error)) return std::nullopt;
    crl.issuer_der.assign(issuer.full.begin(), issuer.full.end());
    std::string normalized_time;
    if (!parse_der_time(tbs.children[i++], crl.this_update, normalized_time,
                        error)) {
        return std::nullopt;
    }
    if (i < tbs.children.size() &&
        (tbs.children[i].is_universal(23) ||
         tbs.children[i].is_universal(24))) {
        if (!parse_der_time(tbs.children[i++], crl.next_update,
                            normalized_time, error)) {
            return std::nullopt;
        }
        crl.has_next_update = true;
    }
    if (i < tbs.children.size() && tbs.children[i].is_universal(16)) {
        const Der& revoked = tbs.children[i++];
        for (const Der& entry : revoked.children) {
            if (!entry.is_universal(16) || entry.children.empty()) {
                error = "malformed CRL revoked entry";
                return std::nullopt;
            }
            BigUInt serial;
            if (!decode_positive_integer(entry.children[0], serial, error)) {
                return std::nullopt;
            }
            crl.revoked_serials.insert(serial.to_decimal());
        }
    }
    return crl;
}

std::optional<Bytes> normalize_crl(ByteView der, std::string* error) {
    std::string local;
    auto crl = parse_crl_internal(der, local);
    if (!crl) {
        if (error) *error = local;
        return std::nullopt;
    }
    return crl->der;
}

static Bytes hash_of_bytes(HashAlgorithm alg, ByteView data) {
    return digest_bytes(alg, data);
}

struct OcspCheck {
    bool matched = false;
    RevocationStatus status = RevocationStatus::Unknown;
    bool time_valid = true;
};

static bool parse_ocsp_response(
    ByteView der, const ParsedCert& cert, const ParsedCert& issuer,
    TimePoint evaluation, std::chrono::seconds skew, OcspCheck& result,
    std::string& error) {
    Der root;
    if (!parse_der_full(der, root, error) ||
        !root.is_universal(16) || root.children.empty()) {
        if (error.empty()) error = "OCSPResponse is malformed";
        return false;
    }
    if (!root.children[0].is_universal(10) ||
        root.children[0].content.empty()) {
        error = "OCSPResponse has no responseStatus";
        return false;
    }
    if (root.children[0].content[0] != 0) {
        error = "OCSP responder returned a non-successful status";
        return false;
    }
    if (root.children.size() < 2 || !root.children[1].is_context(0) ||
        root.children[1].children.empty()) {
        error = "OCSPResponse has no responseBytes";
        return false;
    }
    const Der& response_bytes = root.children[1].children[0];
    if (!response_bytes.is_universal(16) || response_bytes.children.size() < 2) {
        error = "OCSP ResponseBytes is malformed";
        return false;
    }
    std::string response_type;
    if (!decode_oid(response_bytes.children[0], response_type) ||
        response_type != "1.3.6.1.5.5.7.48.1.1") {
        error = "OCSP response is not id-pkix-ocsp-basic";
        return false;
    }
    Bytes basic_bytes;
    if (!expect_octet_string(response_bytes.children[1], basic_bytes,
                             "OCSP response", error)) {
        return false;
    }
    Der basic;
    if (!parse_der_full(ByteView(basic_bytes), basic, error) ||
        !basic.is_universal(16) || basic.children.size() < 3) {
        if (error.empty()) error = "BasicOCSPResponse is malformed";
        return false;
    }
    const Der& tbs = basic.children[0];
    std::string sig_oid;
    if (!parse_algorithm_identifier(basic.children[1], sig_oid, nullptr,
                                    error)) {
        return false;
    }
    const Der& sig_bit = basic.children[2];
    if (!sig_bit.is_universal(3) || sig_bit.content.empty() ||
        sig_bit.content[0] != 0) {
        error = "OCSP signature is malformed";
        return false;
    }
    Bytes signature(sig_bit.content.begin() + 1, sig_bit.content.end());

    std::vector<ParsedCert> responder_certs;
    if (basic.children.size() > 3 && basic.children[3].is_context(0)) {
        for (const Der& choice : basic.children[3].children) {
            if (!choice.is_universal(16)) continue;
            std::string cert_error;
            auto c = parse_certificate_internal(choice.full, &cert_error);
            if (c) responder_certs.push_back(std::move(*c));
        }
    }

    if (!tbs.is_universal(16)) {
        error = "OCSP ResponseData is malformed";
        return false;
    }
    std::size_t i = 0;
    if (i < tbs.children.size() && tbs.children[i].is_context(0)) ++i;  // version
    if (i >= tbs.children.size()) {
        error = "OCSP ResponseData has no responderID";
        return false;
    }
    const Der& responder_id = tbs.children[i++];
    if (i >= tbs.children.size()) {
        error = "OCSP ResponseData has no producedAt";
        return false;
    }
    TimePoint produced_at{};
    std::string normalized, time_error;
    if (!parse_der_time(tbs.children[i++], produced_at, normalized,
                        time_error)) {
        return false;
    }
    if (i >= tbs.children.size() || !tbs.children[i].is_universal(16)) {
        error = "OCSP ResponseData has no SingleResponse list";
        return false;
    }
    const Der& responses = tbs.children[i];

    ParsedCert responder;
    bool have_responder = false;
    auto id_matches = [&](const ParsedCert& candidate) {
        if (responder_id.is_context(1)) {
            std::string name;
            std::string e;
            const Der* name_node = nullptr;
            if (!responder_id.children.empty()) name_node = &responder_id.children[0];
            if (name_node && parse_name(*name_node, name, e)) {
                if (collapse_ws(name) == collapse_ws(candidate.info.subject)) {
                    return true;
                }
            }
            return std::equal(responder_id.content.begin(),
                              responder_id.content.end(),
                              candidate.info.subject_der.begin(),
                              candidate.info.subject_der.end());
        }
        if (responder_id.is_context(2)) {
            Bytes key_hash;
            if (!responder_id.children.empty()) {
                key_hash.assign(responder_id.children[0].content.begin(),
                                responder_id.children[0].content.end());
            } else {
                key_hash.assign(responder_id.content.begin(),
                                responder_id.content.end());
            }
            const Bytes candidate_hash = hash_of_bytes(
                HashAlgorithm::Sha1, ByteView(candidate.key_bits));
            return candidate_hash == key_hash;
        }
        return false;
    };
    for (const ParsedCert& candidate : responder_certs) {
        if (id_matches(candidate)) {
            responder = candidate;
            have_responder = true;
            break;
        }
    }
    if (!have_responder && id_matches(issuer)) {
        responder = issuer;
        have_responder = true;
    }
    if (!have_responder) {
        error = "OCSP responder certificate is not available";
        return false;
    }
    std::string sig_error;
    if (!verify_signature_with_key(responder, sig_oid, Der{}, std::nullopt,
                                   ByteView(tbs.full), ByteView(signature),
                                   sig_error)) {
        error = "OCSP signature: " + sig_error;
        return false;
    }

    for (const Der& single : responses.children) {
        if (!single.is_universal(16) || single.children.size() < 4) continue;
        const Der& cert_id = single.children[0];
        if (!cert_id.is_universal(16) || cert_id.children.size() < 4) continue;
        std::string hash_oid;
        if (!parse_algorithm_identifier(cert_id.children[0], hash_oid, nullptr,
                                        error)) {
            return false;
        }
        auto cert_id_hash = hash_algorithm_from_oid(hash_oid);
        if (!cert_id_hash) continue;
        Bytes issuer_name_hash, issuer_key_hash;
        if (cert_id.children[1].is_universal(4)) {
            issuer_name_hash.assign(cert_id.children[1].content.begin(),
                                    cert_id.children[1].content.end());
        }
        if (cert_id.children[2].is_universal(4)) {
            issuer_key_hash.assign(cert_id.children[2].content.begin(),
                                   cert_id.children[2].content.end());
        }
        BigUInt serial;
        if (!decode_positive_integer(cert_id.children[3], serial, error)) {
            return false;
        }
        if (serial.to_decimal() != cert.info.serial_number) continue;
        const Bytes expected_name_hash =
            hash_of_bytes(*cert_id_hash, ByteView(issuer.info.subject_der));
        const Bytes expected_key_hash =
            hash_of_bytes(*cert_id_hash, ByteView(issuer.key_bits));
        if (expected_name_hash != issuer_name_hash ||
            expected_key_hash != issuer_key_hash) {
            continue;
        }

        const Der& status = single.children[1];
        OcspCheck item;
        item.matched = true;
        if (status.is_context(1)) {
            item.status = RevocationStatus::Revoked;
        } else if (status.is_context(2)) {
            item.status = RevocationStatus::Unknown;
        } else if (status.is_context(0)) {
            item.status = RevocationStatus::Good;
        } else {
            continue;
        }
        TimePoint this_update{};
        std::string norm;
        if (!parse_der_time(single.children[2], this_update, norm, error)) {
            continue;
        }
        item.time_valid =
            this_update <= evaluation + skew;
        if (single.children.size() > 3 && single.children[3].is_context(0) &&
            !single.children[3].children.empty()) {
            TimePoint next{};
            std::string n2;
            if (parse_der_time(single.children[3].children[0], next, n2,
                               error)) {
                item.time_valid =
                    item.time_valid && next >= evaluation - skew;
            }
        }
        result = item;
        return true;
    }
    return false;
}

// ===========================================================================
// Certificate path building and purpose checks
// ===========================================================================

static bool same_der(const Bytes& a, const Bytes& b) {
    return a.size() == b.size() &&
           std::equal(a.begin(), a.end(), b.begin());
}

static bool same_name(const std::string& a, const std::string& b) {
    return collapse_ws(a) == collapse_ws(b);
}

static Bytes normalize_certificate_input(const Bytes& raw) {
    if (!looks_like_text(ByteView(raw))) return raw;
    const std::string_view text(
        reinterpret_cast<const char*>(raw.data()), raw.size());
    auto blocks = parse_pem_blocks(text, "CERTIFICATE");
    if (blocks.empty()) {
        blocks = parse_pem_blocks(text, "TRUSTED CERTIFICATE");
    }
    if (blocks.empty()) return raw;
    return std::move(blocks.front());
}

static bool cert_is_anchor(const ParsedCert& cert,
                           const std::vector<Bytes>& anchors) {
    for (const Bytes& anchor : anchors) {
        if (same_der(cert.info.der, anchor)) return true;
    }
    return false;
}

static bool issuer_matches(const ParsedCert& leaf, const ParsedCert& issuer) {
    if (same_der(leaf.info.issuer_der, issuer.info.subject_der)) return true;
    if (!leaf.info.authority_key_id.empty() &&
        !issuer.info.subject_key_id.empty() &&
        lower_ascii(leaf.info.authority_key_id) ==
            lower_ascii(issuer.info.subject_key_id)) {
        return true;
    }
    return same_name(leaf.info.issuer, issuer.info.subject);
}

static bool cert_time_valid(const ParsedCert& cert, TimePoint at,
                            std::chrono::seconds skew) {
    return at + skew >= cert.info.not_before &&
           at - skew <= cert.info.not_after;
}

static bool verify_anchor_signature(const ParsedCert& anchor,
                                    std::string& error) {
    if (!anchor.info.self_signed) return true;
    return verify_certificate_signature(anchor, anchor, error);
}

static bool build_certificate_path(
    const ParsedCert& leaf, const std::vector<ParsedCert>& pool,
    const std::vector<Bytes>& anchors, std::vector<ParsedCert>& path,
    std::string& error) {
    path.clear();
    std::vector<ParsedCert> working = pool;
    bool leaf_is_anchor = cert_is_anchor(leaf, anchors);
    path.push_back(leaf);
    if (leaf_is_anchor) {
        std::string anchor_error;
        if (!verify_anchor_signature(leaf, anchor_error)) {
            error = "trust anchor self-signature: " + anchor_error;
            return false;
        }
        return true;
    }

    std::function<bool(const ParsedCert&)> search =
        [&](const ParsedCert& current) -> bool {
        bool found_issuer = false;
        for (const ParsedCert& candidate : working) {
            if (same_der(candidate.info.der, current.info.der)) continue;
            if (!issuer_matches(current, candidate)) continue;
            found_issuer = true;
            bool already = false;
            for (const ParsedCert& p : path) {
                if (same_der(p.info.der, candidate.info.der)) {
                    already = true;
                    break;
                }
            }
            if (already) continue;
            std::string sig_error;
            if (!verify_certificate_signature(current, candidate, sig_error)) {
                continue;
            }
            if (cert_is_anchor(candidate, anchors)) {
                std::string anchor_error;
                if (!verify_anchor_signature(candidate, anchor_error)) {
                    continue;
                }
                path.push_back(candidate);
                return true;
            }
            if (!candidate.info.is_ca) continue;
            path.push_back(candidate);
            if (search(candidate)) return true;
            path.pop_back();
        }
        (void)found_issuer;
        return false;
    };
    if (!search(leaf)) {
        error = "no path from the signer certificate to a trust anchor";
        return false;
    }
    return true;
}

static bool certificate_can_issue(const ParsedCert& cert,
                                  std::string& error) {
    if (!cert.info.is_ca) {
        error = "intermediate certificate is not a CA";
        return false;
    }
    if (cert.info.has_key_usage && !cert.info.key_usage_key_cert_sign) {
        error = "CA certificate lacks keyCertSign usage";
        return false;
    }
    return true;
}

// ===========================================================================
// Revocation checking
// ===========================================================================

struct RevocationOutcome {
    bool ok = true;
    ErrorCode code = ErrorCode::Ok;
    std::string message;
    bool checked = false;
};

static RevocationOutcome check_path_revocation(
    const std::vector<ParsedCert>& path,
    const VerificationPolicy& policy,
    const RevocationEvidence& evidence,
    const RevocationChecker& callback,
    TimePoint evaluation) {
    RevocationOutcome out;
    if (policy.revocation == RevocationMode::Disabled || path.size() < 2) {
        return out;
    }

    std::vector<ParsedCrl> crls;
    for (const Bytes& raw : evidence.crls) {
        std::string error;
        Bytes der = raw;
        if (looks_like_text(ByteView(raw))) {
            const std::string_view text(
                reinterpret_cast<const char*>(raw.data()), raw.size());
            auto blocks = parse_pem_blocks(text, "X509 CRL");
            if (blocks.empty()) blocks = parse_pem_blocks(text, "CRL");
            if (!blocks.empty()) der = std::move(blocks.front());
        }
        auto crl = parse_crl_internal(ByteView(der), error);
        if (crl) crls.push_back(std::move(*crl));
    }

    bool all_good = true;
    bool any_checked = false;
    std::string last_ocsp_error;
    auto serial_in = [](const std::vector<std::string>& list,
                        const std::string& serial) {
        for (const std::string& entry : list) {
            bool valid = false;
            if (canonical_integer_text(entry, &valid) == serial && valid) {
                return true;
            }
        }
        return false;
    };
    const bool serial_lists_authoritative =
        !evidence.revoked_serials.empty() || !evidence.good_serials.empty();
    const bool evidence_supplied =
        !evidence.crls.empty() || !evidence.ocsp_responses.empty() ||
        serial_lists_authoritative || static_cast<bool>(callback);
    for (std::size_t i = 0; i + 1 < path.size(); ++i) {
        const ParsedCert& cert = path[i];
        const ParsedCert& issuer = path[i + 1];
        bool evidence_for_cert = false;
        RevocationStatus status = RevocationStatus::Unknown;

        if (callback) {
            const RevocationStatus cb =
                callback(cert.info, issuer.info);
            if (cb != RevocationStatus::NotChecked) {
                evidence_for_cert = true;
                status = cb;
            }
        }
        if (status != RevocationStatus::Revoked &&
            serial_in(evidence.revoked_serials, cert.info.serial_number)) {
            evidence_for_cert = true;
            status = RevocationStatus::Revoked;
        } else if (status != RevocationStatus::Revoked &&
                   serial_in(evidence.good_serials, cert.info.serial_number)) {
            evidence_for_cert = true;
            if (status == RevocationStatus::Unknown) {
                status = RevocationStatus::Good;
            }
        } else if (status != RevocationStatus::Revoked &&
                   serial_lists_authoritative) {
            evidence_for_cert = true;
            if (status == RevocationStatus::Unknown) {
                status = RevocationStatus::Good;
            }
        }

        // A CRL is authoritative only when it is issued and signed by the
        // certificate's issuer and is fresh at evaluation time.
        for (const ParsedCrl& crl : crls) {
            if (!same_der(crl.issuer_der, cert.info.issuer_der)) continue;
            std::string sig_error;
            if (!verify_signature_with_key(issuer, crl.signature_algorithm_oid,
                                           Der{}, std::nullopt,
                                           ByteView(crl.tbs),
                                           ByteView(crl.signature),
                                           sig_error)) {
                continue;
            }
            const bool fresh =
                crl.this_update <= evaluation + policy.clock_skew &&
                (!crl.has_next_update ||
                 crl.next_update >= evaluation - policy.clock_skew);
            if (!fresh) continue;
            evidence_for_cert = true;
            if (crl.revoked_serials.count(cert.info.serial_number)) {
                status = RevocationStatus::Revoked;
                break;
            }
            if (status != RevocationStatus::Revoked) {
                status = RevocationStatus::Good;
            }
        }

        if (status != RevocationStatus::Revoked) {
            for (const Bytes& raw : evidence.ocsp_responses) {
                OcspCheck ocsp;
                std::string ocsp_error;
                if (parse_ocsp_response(ByteView(raw), cert, issuer, evaluation,
                                        policy.clock_skew, ocsp, ocsp_error) &&
                    ocsp.matched && ocsp.time_valid) {
                    evidence_for_cert = true;
                    if (ocsp.status == RevocationStatus::Revoked) {
                        status = RevocationStatus::Revoked;
                        break;
                    }
                    if (ocsp.status == RevocationStatus::Good) {
                        status = RevocationStatus::Good;
                    } else if (status != RevocationStatus::Good) {
                        status = RevocationStatus::Unknown;
                    }
                } else if (!ocsp_error.empty()) {
                    last_ocsp_error = ocsp_error;
                }
            }
        }

        if (status == RevocationStatus::Revoked) {
            out.ok = false;
            out.code = ErrorCode::Revoked;
            out.message = "certificate " + cert.info.serial_number +
                          " is revoked";
            out.checked = true;
            return out;
        }
        if (evidence_for_cert) {
            any_checked = true;
            if (status != RevocationStatus::Good) {
                all_good = false;
                if (policy.revocation == RevocationMode::Required) {
                    out.ok = false;
                    out.code = ErrorCode::RevocationUnknown;
                    out.message = "revocation status is unknown for "
                                  "certificate " + cert.info.serial_number;
                    out.checked = true;
                    return out;
                }
            }
        } else if (policy.revocation == RevocationMode::Required ||
                   (policy.revocation == RevocationMode::IfProvided &&
                    evidence_supplied)) {
            out.ok = false;
            out.code = ErrorCode::RevocationUnknown;
            out.message = "no revocation evidence for certificate " +
                          cert.info.serial_number;
            if (!last_ocsp_error.empty()) {
                out.message += " (OCSP: " + last_ocsp_error + ")";
            }
            out.checked = true;
            return out;
        }
    }
    out.checked = any_checked && all_good;
    return out;
}

// ===========================================================================
// Full token verification
// ===========================================================================

static bool constant_time_equal(ByteView a, ByteView b) {
    if (a.size() != b.size()) return false;
    u8 diff = 0;
    for (std::size_t i = 0; i < a.size(); ++i) diff |= a[i] ^ b[i];
    return diff == 0;
}

static bool is_weak_digest(HashAlgorithm algorithm) {
    return algorithm == HashAlgorithm::Md5 || algorithm == HashAlgorithm::Sha1;
}

static bool matches_issuer_field(std::string_view expected,
                                 const std::vector<std::string>& candidates) {
    const std::string wanted = collapse_ws(expected);
    if (wanted.empty()) return false;
    for (const std::string& candidate : candidates) {
        if (!candidate.empty() && collapse_ws(candidate) == wanted) return true;
    }
    return false;
}

static VerificationResult verify_parsed_token(
    const ImportedTimestampToken& token, const Digest& artifact,
    const VerificationPolicy& policy, const TrustStore& trust,
    const RevocationEvidence& evidence, const RevocationChecker& callback) {
    if (!token.parsed) {
        return VerificationResult::failure(
            ErrorCode::InvalidTokenStructure, "token was not imported");
    }
    const detail::ParsedTimestampToken& parsed = *token.parsed;
    const TimestampInfo& info = parsed.info;
    VerificationResult result;

    // ---- messageImprint: algorithm and digest both bind the exact file ----
    const HashAlgorithm token_alg = info.message_imprint.algorithm;
    if (token_alg == HashAlgorithm::Unknown) {
        return VerificationResult::failure(
            ErrorCode::UnsupportedAlgorithm,
            "messageImprint uses an unsupported hash algorithm " +
                info.message_imprint.algorithm_oid);
    }
    if (policy.required_digest_algorithm &&
        *policy.required_digest_algorithm != token_alg) {
        return VerificationResult::failure(
            ErrorCode::DigestAlgorithmMismatch,
            "messageImprint algorithm does not match the required policy");
    }
    if (artifact.algorithm != token_alg) {
        return VerificationResult::failure(
            ErrorCode::DigestAlgorithmMismatch,
            "artifact digest algorithm '" +
                std::string(to_string(artifact.algorithm)) +
                "' does not match messageImprint algorithm '" +
                std::string(to_string(token_alg)) + "'");
    }
    if (!policy.allow_weak_message_imprint && is_weak_digest(token_alg)) {
        return VerificationResult::failure(
            ErrorCode::UnsupportedAlgorithm,
            "messageImprint uses a weak hash algorithm");
    }
    const std::size_t expected_len = hash_output_size(token_alg);
    if (expected_len == 0 || info.message_imprint.digest.size() != expected_len ||
        artifact.bytes.size() != expected_len) {
        return VerificationResult::failure(
            ErrorCode::MessageImprintMismatch,
            "messageImprint digest length is invalid");
    }
    result.digest_algorithm_checked = true;
    if (!constant_time_equal(ByteView(info.message_imprint.digest),
                             ByteView(artifact.bytes))) {
        return VerificationResult::failure(
            ErrorCode::MessageImprintMismatch,
            "timestamp messageImprint does not match the artifact digest");
    }
    result.message_imprint_checked = true;

    // ---- nonce: presence and exact replay value ----
    if (!info.nonce) {
        if (policy.require_nonce || policy.expected_nonce ||
            policy.require_expected_nonce) {
            return VerificationResult::failure(
                ErrorCode::MissingNonce,
                "timestamp token has no nonce; replay protection is absent");
        }
    } else {
        result.nonce_checked = true;
        if (policy.expected_nonce) {
            bool valid = false;
            const std::string wanted =
                canonical_integer_text(*policy.expected_nonce, &valid);
            if (!valid || wanted != *info.nonce) {
                return VerificationResult::failure(
                    ErrorCode::NonceMismatch,
                    "timestamp nonce does not match the expected nonce");
            }
        } else if (policy.require_expected_nonce) {
            return VerificationResult::failure(
                ErrorCode::NonceMismatch,
                "an expected nonce is required by policy but was not supplied");
        }
    }

    // ---- serialNumber ----
    if (policy.expected_serial_number) {
        bool valid = false;
        const std::string wanted =
            canonical_integer_text(*policy.expected_serial_number, &valid);
        if (!valid) {
            return VerificationResult::failure(
                ErrorCode::SerialMismatch,
                "expected serial number is not a valid integer");
        }
        if (wanted != info.serial_number) {
            return VerificationResult::failure(
                ErrorCode::SerialMismatch,
                "timestamp serialNumber does not match the expected value");
        }
        result.serial_checked = true;
    }

    // ---- policy OID ----
    if (policy.expected_policy_oid &&
        *policy.expected_policy_oid != info.policy_oid) {
        return VerificationResult::failure(
            ErrorCode::PolicyMismatch,
            "timestamp policy OID does not match the expected policy");
    }

    // ---- genTime ----
    const TimePoint evaluation =
        policy.evaluation_time.value_or(Clock::now());
    if (policy.not_before && info.gen_time < *policy.not_before) {
        return VerificationResult::failure(
            ErrorCode::TimeOutsidePolicy,
            "timestamp genTime is before the allowed window");
    }
    if (policy.not_after && info.gen_time > *policy.not_after) {
        return VerificationResult::failure(
            ErrorCode::TimeOutsidePolicy,
            "timestamp genTime is after the allowed window");
    }
    if (policy.reject_future_timestamps &&
        info.gen_time > evaluation + policy.clock_skew) {
        return VerificationResult::failure(
            ErrorCode::TimeOutsidePolicy,
            "timestamp genTime is in the future");
    }
    result.time_checked = true;

    // Build the complete certificate pool.  Certificates in the token are
    // untrusted until the path reaches a configured anchor.
    std::vector<ParsedCert> pool = parsed.certificates;
    for (const Bytes& raw : trust.intermediates) {
        const Bytes der = normalize_certificate_input(raw);
        std::string error;
        auto cert = parse_certificate_internal(ByteView(der), &error);
        if (!cert) {
            return VerificationResult::failure(
                ErrorCode::CertificateInvalid,
                "intermediate certificate: " + error);
        }
        pool.push_back(std::move(*cert));
    }
    std::vector<Bytes> anchors;
    for (const Bytes& raw : trust.trust_anchors) {
        anchors.push_back(normalize_certificate_input(raw));
    }
    for (const Bytes& der : anchors) {
        std::string error;
        auto cert = parse_certificate_internal(ByteView(der), &error);
        if (!cert) {
            return VerificationResult::failure(
                ErrorCode::CertificateInvalid,
                "trust anchor: " + error);
        }
        pool.push_back(std::move(*cert));
    }
    if (trust.allow_embedded_trust_anchor) {
        for (const ParsedCert& cert : parsed.certificates) {
            if (cert.info.self_signed) anchors.push_back(cert.info.der);
        }
    }

    ParsedCert signer;
    bool have_signer = false;
    if (parsed.signer.signer_cert_index) {
        signer = parsed.certificates[*parsed.signer.signer_cert_index];
        have_signer = true;
    } else {
        auto index = match_signer_certificate(parsed.signer, pool);
        if (index) {
            signer = pool[*index];
            have_signer = true;
        }
    }
    if (!have_signer && policy.require_signer_certificate) {
        return VerificationResult::failure(
            ErrorCode::CertificateInvalid,
            "the token does not identify an available signer certificate");
    }

    // ---- issuer / TSA identity ----
    std::vector<std::string> issuer_candidates;
    std::vector<std::string> tsa_candidates;
    if (have_signer) {
        issuer_candidates.push_back(signer.info.issuer);
        issuer_candidates.push_back(signer.info.subject);
        tsa_candidates.push_back(signer.info.subject);
    }
    if (info.tsa_name) {
        issuer_candidates.push_back(*info.tsa_name);
        tsa_candidates.push_back(*info.tsa_name);
    }
    if (policy.expected_issuer) {
        if (!matches_issuer_field(*policy.expected_issuer, issuer_candidates)) {
            return VerificationResult::failure(
                ErrorCode::IssuerMismatch,
                "timestamp issuer does not match the expected issuer");
        }
        result.issuer_checked = true;
    }
    if (policy.expected_tsa &&
        !matches_issuer_field(*policy.expected_tsa, tsa_candidates)) {
        return VerificationResult::failure(
            ErrorCode::TsaMismatch,
            "timestamp TSA does not match the expected TSA");
    }

    // ---- CMS signed attributes and signature ----
    if (policy.verify_signed_attributes) {
        if (!parsed.signer.has_signed_attrs) {
            return VerificationResult::failure(
                ErrorCode::SignedAttributeInvalid,
                "RFC 3161 token has no signed attributes");
        }
        if (!parsed.signer.has_content_type ||
            parsed.signer.content_type != "1.2.840.113549.1.9.16.1.4") {
            return VerificationResult::failure(
                ErrorCode::InvalidContentType,
                "signed content-type attribute is not id-ct-TSTInfo");
        }
        if (!parsed.signer.has_message_digest) {
            return VerificationResult::failure(
                ErrorCode::SignedAttributeInvalid,
                "signed message-digest attribute is missing");
        }
        auto cms_hash = hash_algorithm_from_oid(parsed.signer.digest_oid);
        if (!cms_hash) {
            return VerificationResult::failure(
                ErrorCode::UnsupportedAlgorithm,
                "unsupported CMS digest algorithm " +
                    parsed.signer.digest_oid);
        }
        if (std::find(parsed.cms_digest_algorithms.begin(),
                      parsed.cms_digest_algorithms.end(),
                      parsed.signer.digest_oid) ==
            parsed.cms_digest_algorithms.end()) {
            return VerificationResult::failure(
                ErrorCode::SignedAttributeInvalid,
                "SignerInfo digest algorithm is absent from digestAlgorithms");
        }
        const Bytes content_digest =
            digest_bytes(*cms_hash, ByteView(parsed.econtent));
        if (!constant_time_equal(ByteView(content_digest),
                                 ByteView(parsed.signer.message_digest))) {
            return VerificationResult::failure(
                ErrorCode::ContentDigestMismatch,
                "CMS message-digest does not match the TSTInfo content");
        }
        if (parsed.signer.has_signing_certificate && have_signer) {
            auto ess_hash =
                hash_algorithm_from_oid(parsed.signer.signing_certificate_hash_oid);
            if (!ess_hash) {
                return VerificationResult::failure(
                    ErrorCode::UnsupportedAlgorithm,
                    "unsupported signing-certificate hash algorithm");
            }
            const Bytes cert_hash =
                digest_bytes(*ess_hash, ByteView(signer.info.der));
            if (!constant_time_equal(ByteView(cert_hash),
                                     ByteView(parsed.signer.signing_certificate_hash))) {
                return VerificationResult::failure(
                    ErrorCode::SignedAttributeInvalid,
                    "signing-certificate attribute does not match the signer "
                    "certificate");
            }
            if (!parsed.signer.signing_certificate_serial.empty() &&
                parsed.signer.signing_certificate_serial !=
                    signer.info.serial_number) {
                return VerificationResult::failure(
                    ErrorCode::SignedAttributeInvalid,
                    "signing-certificate issuerSerial does not match the "
                    "signer certificate");
            }
        }
        result.signed_attributes_checked = true;
    }

    if (policy.verify_signature) {
        if (!have_signer) {
            return VerificationResult::failure(
                ErrorCode::CertificateInvalid,
                "cannot verify the CMS signature without a signer certificate");
        }
        Der params;
        std::string der_error;
        if (!parsed.signer.signature_params_der.empty() &&
            !parse_der_full(ByteView(parsed.signer.signature_params_der),
                            params, der_error)) {
            return VerificationResult::failure(
                ErrorCode::InvalidTokenStructure,
                "signature algorithm parameters: " + der_error);
        }
        const ByteView signed_bytes =
            parsed.signer.has_signed_attrs
                ? ByteView(parsed.signer.signed_attrs_der)
                : ByteView(parsed.econtent);
        std::string sig_error;
        if (!verify_signature_with_key(
                signer, parsed.signer.signature_oid, params,
                hash_algorithm_from_oid(parsed.signer.digest_oid), signed_bytes,
                ByteView(parsed.signer.signature), sig_error)) {
            return VerificationResult::failure(
                ErrorCode::SignatureInvalid,
                "CMS signature verification failed: " + sig_error);
        }
        result.signature_checked = true;
    }

    // ---- certificate path ----
    std::vector<ParsedCert> path;
    if (policy.verify_chain) {
        if (!have_signer) {
            return VerificationResult::failure(
                ErrorCode::ChainUntrusted,
                "no signer certificate is available for path validation");
        }
        if (anchors.empty() && !policy.require_trust_anchor) {
            path.push_back(signer);
            result.chain_checked = false;
        } else {
            if (anchors.empty()) {
                return VerificationResult::failure(
                    ErrorCode::ChainUntrusted,
                    "no trust anchor is configured");
            }
            std::string path_error;
            if (!build_certificate_path(signer, pool, anchors, path,
                                        path_error)) {
                return VerificationResult::failure(
                    ErrorCode::ChainUntrusted, path_error);
            }
            for (std::size_t i = 0; i + 1 < path.size(); ++i) {
                const bool is_anchor =
                    cert_is_anchor(path[i], anchors);
                if (is_anchor) continue;
                if (policy.verify_certificate_time &&
                    !cert_time_valid(path[i], info.gen_time,
                                     policy.clock_skew)) {
                    return VerificationResult::failure(
                        ErrorCode::CertificateTimeInvalid,
                        "certificate " + path[i].info.serial_number +
                            " was not valid at genTime");
                }
                if (i > 0) {
                    std::string ca_error;
                    if (!certificate_can_issue(path[i], ca_error)) {
                        return VerificationResult::failure(
                            ErrorCode::CertificateInvalid, ca_error);
                    }
                }
            }
            if (policy.verify_certificate_time &&
                !cert_is_anchor(signer, anchors) &&
                !cert_time_valid(signer, info.gen_time, policy.clock_skew)) {
                return VerificationResult::failure(
                    ErrorCode::CertificateTimeInvalid,
                    "signer certificate was not valid at genTime");
            }
            if (policy.verify_tsa_purpose) {
                if (signer.info.has_extended_key_usage &&
                    !signer.info.has_time_stamping_eku) {
                    return VerificationResult::failure(
                        ErrorCode::CertificatePurposeInvalid,
                        "signer certificate lacks the timeStamping EKU");
                }
                if (signer.info.has_key_usage &&
                    !signer.info.key_usage_digital_signature &&
                    !signer.info.key_usage_non_repudiation) {
                    return VerificationResult::failure(
                        ErrorCode::CertificatePurposeInvalid,
                        "signer certificate keyUsage does not permit signing");
                }
            }
            result.chain_checked = true;
        }
    } else {
        path.push_back(signer);
    }

    // ---- revocation ----
    const RevocationOutcome revocation = check_path_revocation(
        path, policy, evidence, callback, evaluation);
    if (!revocation.ok) {
        return VerificationResult::failure(revocation.code,
                                           revocation.message);
    }
    result.revocation_checked = revocation.checked;

    result.code = ErrorCode::Ok;
    result.message = "timestamp token is bound to the supplied artifact";
    return result;
}

}  // namespace detail

// ===========================================================================
// Public value helpers
// ===========================================================================

std::string_view to_string(HashAlgorithm algorithm) noexcept {
    switch (algorithm) {
        case HashAlgorithm::Md5: return "md5";
        case HashAlgorithm::Sha1: return "sha1";
        case HashAlgorithm::Sha224: return "sha224";
        case HashAlgorithm::Sha256: return "sha256";
        case HashAlgorithm::Sha384: return "sha384";
        case HashAlgorithm::Sha512: return "sha512";
        default: return "unknown";
    }
}

std::optional<HashAlgorithm> hash_algorithm_from_name(std::string_view name) {
    std::string s;
    for (char c : name) {
        if (c == '-' || c == '_' || c == ' ') continue;
        s.push_back(static_cast<char>(std::tolower(static_cast<unsigned char>(c))));
    }
    if (s == "md5") return HashAlgorithm::Md5;
    if (s == "sha1") return HashAlgorithm::Sha1;
    if (s == "sha224") return HashAlgorithm::Sha224;
    if (s == "sha256") return HashAlgorithm::Sha256;
    if (s == "sha384") return HashAlgorithm::Sha384;
    if (s == "sha512") return HashAlgorithm::Sha512;
    return std::nullopt;
}

std::optional<HashAlgorithm> hash_algorithm_from_oid(std::string_view oid) {
    static const std::map<std::string, HashAlgorithm> algorithms = {
        {"1.2.840.113549.2.5", HashAlgorithm::Md5},
        {"1.3.14.3.2.26", HashAlgorithm::Sha1},
        {"2.16.840.1.101.3.4.2.4", HashAlgorithm::Sha224},
        {"2.16.840.1.101.3.4.2.1", HashAlgorithm::Sha256},
        {"2.16.840.1.101.3.4.2.2", HashAlgorithm::Sha384},
        {"2.16.840.1.101.3.4.2.3", HashAlgorithm::Sha512},
    };
    const auto it = algorithms.find(std::string(oid));
    if (it == algorithms.end()) return std::nullopt;
    return it->second;
}

std::string_view hash_algorithm_oid(HashAlgorithm algorithm) noexcept {
    switch (algorithm) {
        case HashAlgorithm::Md5: return "1.2.840.113549.2.5";
        case HashAlgorithm::Sha1: return "1.3.14.3.2.26";
        case HashAlgorithm::Sha224: return "2.16.840.1.101.3.4.2.4";
        case HashAlgorithm::Sha256: return "2.16.840.1.101.3.4.2.1";
        case HashAlgorithm::Sha384: return "2.16.840.1.101.3.4.2.2";
        case HashAlgorithm::Sha512: return "2.16.840.1.101.3.4.2.3";
        default: return "";
    }
}

std::size_t hash_output_size(HashAlgorithm algorithm) noexcept {
    switch (algorithm) {
        case HashAlgorithm::Md5: return 16;
        case HashAlgorithm::Sha1: return 20;
        case HashAlgorithm::Sha224: return 28;
        case HashAlgorithm::Sha256: return 32;
        case HashAlgorithm::Sha384: return 48;
        case HashAlgorithm::Sha512: return 64;
        default: return 0;
    }
}

std::string_view to_string(ErrorCode code) noexcept {
    switch (code) {
        case ErrorCode::Ok: return "ok";
        case ErrorCode::InvalidDer: return "invalid_der";
        case ErrorCode::UnsupportedAlgorithm: return "unsupported_algorithm";
        case ErrorCode::UnsupportedStructure: return "unsupported_structure";
        case ErrorCode::InvalidTokenStructure: return "invalid_token_structure";
        case ErrorCode::InvalidContentType: return "invalid_content_type";
        case ErrorCode::ContentDigestMismatch: return "content_digest_mismatch";
        case ErrorCode::SignedAttributeInvalid: return "signed_attribute_invalid";
        case ErrorCode::MessageImprintMismatch: return "message_imprint_mismatch";
        case ErrorCode::DigestAlgorithmMismatch: return "digest_algorithm_mismatch";
        case ErrorCode::MissingNonce: return "missing_nonce";
        case ErrorCode::NonceMismatch: return "nonce_mismatch";
        case ErrorCode::SerialMismatch: return "serial_mismatch";
        case ErrorCode::IssuerMismatch: return "issuer_mismatch";
        case ErrorCode::TsaMismatch: return "tsa_mismatch";
        case ErrorCode::PolicyMismatch: return "policy_mismatch";
        case ErrorCode::InvalidTime: return "invalid_time";
        case ErrorCode::TimeOutsidePolicy: return "time_outside_policy";
        case ErrorCode::CertificateInvalid: return "certificate_invalid";
        case ErrorCode::CertificateTimeInvalid: return "certificate_time_invalid";
        case ErrorCode::CertificatePurposeInvalid:
            return "certificate_purpose_invalid";
        case ErrorCode::ChainUntrusted: return "chain_untrusted";
        case ErrorCode::SignatureInvalid: return "signature_invalid";
        case ErrorCode::Revoked: return "revoked";
        case ErrorCode::RevocationUnknown: return "revocation_unknown";
        case ErrorCode::IoError: return "io_error";
        case ErrorCode::InternalError: return "internal_error";
        default: return "unknown";
    }
}

std::string_view to_string(RevocationStatus status) noexcept {
    switch (status) {
        case RevocationStatus::Good: return "good";
        case RevocationStatus::Revoked: return "revoked";
        case RevocationStatus::Unknown: return "unknown";
        default: return "not_checked";
    }
}

VerificationResult VerificationResult::success() {
    VerificationResult r;
    r.code = ErrorCode::Ok;
    return r;
}

VerificationResult VerificationResult::failure(ErrorCode code,
                                               std::string message) {
    VerificationResult r;
    r.code = code;
    r.message = std::move(message);
    return r;
}

Digest Digest::compute(HashAlgorithm algorithm, ByteView data) {
    Digest d;
    d.algorithm = algorithm;
    d.bytes = detail::digest_bytes(algorithm, data);
    return d;
}

Digest Digest::compute(HashAlgorithm algorithm, std::string_view data) {
    return compute(algorithm,
                   ByteView(reinterpret_cast<const std::uint8_t*>(data.data()),
                            data.size()));
}

Digest Digest::compute_file(HashAlgorithm algorithm, const std::string& path,
                            std::string* error) {
    Bytes data;
    std::string local;
    if (!detail::read_file(path, data, local)) {
        if (error) *error = local;
        return {};
    }
    return compute(algorithm, ByteView(data));
}

std::string base64_encode(ByteView data) {
    return detail::base64_encode(data);
}

std::optional<Bytes> base64_decode(std::string_view text) {
    return detail::base64_decode(text);
}

std::vector<Bytes> parse_pem_blocks(std::string_view pem,
                                    std::string_view label) {
    return detail::parse_pem_blocks(pem, label);
}

std::optional<Bytes> pem_to_der(std::string_view pem, std::string_view label) {
    return detail::pem_to_der(pem, label);
}

std::string hex_encode(ByteView data, bool upper) {
    return detail::hex_encode(data, upper);
}

std::optional<Bytes> hex_decode(std::string_view text) {
    return detail::hex_decode(text);
}

bool token_has_nonce(const ImportedTimestampToken& token) noexcept {
    return token.tst_info.nonce.has_value();
}

// ===========================================================================
// Public context configuration and verification
// ===========================================================================

bool TrustStore::add_anchor_pem(std::string_view pem) {
    auto blocks = detail::parse_pem_blocks(pem, "CERTIFICATE");
    if (blocks.empty()) {
        blocks = detail::parse_pem_blocks(pem, "TRUSTED CERTIFICATE");
    }
    if (blocks.empty()) return false;
    for (Bytes& der : blocks) {
        std::string error;
        if (!detail::parse_certificate_internal(ByteView(der), &error)) {
            return false;
        }
        trust_anchors.push_back(std::move(der));
    }
    return true;
}

bool TrustStore::add_intermediate_pem(std::string_view pem) {
    auto blocks = detail::parse_pem_blocks(pem, "CERTIFICATE");
    if (blocks.empty()) {
        blocks = detail::parse_pem_blocks(pem, "TRUSTED CERTIFICATE");
    }
    if (blocks.empty()) return false;
    for (Bytes& der : blocks) {
        std::string error;
        if (!detail::parse_certificate_internal(ByteView(der), &error)) {
            return false;
        }
        intermediates.push_back(std::move(der));
    }
    return true;
}

static bool add_certificate_file(const std::string& path,
                                 std::vector<Bytes>& destination) {
    Bytes data;
    std::string error;
    if (!detail::read_file(path, data, error)) return false;
    if (detail::looks_like_text(ByteView(data)) &&
        std::string_view(reinterpret_cast<const char*>(data.data()), data.size())
                .find("BEGIN") != std::string_view::npos) {
        auto blocks = detail::parse_pem_blocks(
            std::string_view(reinterpret_cast<const char*>(data.data()),
                             data.size()),
            "CERTIFICATE");
        if (blocks.empty()) {
            blocks = detail::parse_pem_blocks(
                std::string_view(reinterpret_cast<const char*>(data.data()),
                                 data.size()),
                "TRUSTED CERTIFICATE");
        }
        for (Bytes& der : blocks) {
            std::string cert_error;
            if (!detail::parse_certificate_internal(ByteView(der),
                                                    &cert_error)) {
                return false;
            }
            destination.push_back(std::move(der));
        }
        return !blocks.empty();
    }
    std::string cert_error;
    if (!detail::parse_certificate_internal(ByteView(data), &cert_error)) {
        return false;
    }
    destination.push_back(std::move(data));
    return true;
}

bool TrustStore::add_anchor_file(const std::string& path) {
    return add_certificate_file(path, trust_anchors);
}

bool TrustStore::add_intermediate_file(const std::string& path) {
    return add_certificate_file(path, intermediates);
}

TimestampVerifier::TimestampVerifier() = default;

TimestampVerifier::TimestampVerifier(VerificationPolicy policy)
    : policy_(std::move(policy)) {}

TimestampVerifier::TimestampVerifier(VerificationPolicy policy, TrustStore trust)
    : policy_(std::move(policy)), trust_(std::move(trust)) {}

TimestampVerifier::TimestampVerifier(TrustStore trust)
    : trust_(std::move(trust)) {}

TimestampVerifier::TimestampVerifier(TrustStore trust, VerificationPolicy policy)
    : policy_(std::move(policy)), trust_(std::move(trust)) {}

void TimestampVerifier::set_policy(VerificationPolicy policy) {
    policy_ = std::move(policy);
}

bool TimestampVerifier::add_crl_pem(std::string_view pem) {
    auto blocks = detail::parse_pem_blocks(pem, "X509 CRL");
    if (blocks.empty()) blocks = detail::parse_pem_blocks(pem, "CRL");
    if (blocks.empty()) return false;
    for (Bytes& der : blocks) {
        std::string error;
        if (!detail::parse_crl_internal(ByteView(der), error)) return false;
        revocation_.crls.push_back(std::move(der));
    }
    return true;
}

bool TimestampVerifier::add_crl_file(const std::string& path) {
    Bytes data;
    std::string error;
    if (!detail::read_file(path, data, error)) return false;
    if (detail::looks_like_text(ByteView(data)) &&
        std::string_view(reinterpret_cast<const char*>(data.data()), data.size())
                .find("BEGIN") != std::string_view::npos) {
        return add_crl_pem(
            std::string_view(reinterpret_cast<const char*>(data.data()),
                             data.size()));
    }
    std::string crl_error;
    if (!detail::parse_crl_internal(ByteView(data), crl_error)) return false;
    revocation_.crls.push_back(std::move(data));
    return true;
}

bool TimestampVerifier::add_ocsp_response_file(const std::string& path) {
    Bytes data;
    std::string error;
    if (!detail::read_file(path, data, error)) return false;
    revocation_.ocsp_responses.push_back(std::move(data));
    return true;
}

VerificationResult TimestampVerifier::verify(
    const ImportedTimestampToken& token, const Digest& artifact_digest) const {
    return detail::verify_parsed_token(token, artifact_digest, policy_, trust_,
                                       revocation_, revocation_checker_);
}

VerificationResult TimestampVerifier::verify(
    ByteView token_der, const Digest& artifact_digest) const {
    const ImportResult imported = TimestampTokenImporter::from_der(token_der);
    if (!imported.ok()) {
        return VerificationResult::failure(imported.code, imported.message);
    }
    return verify(imported.token, artifact_digest);
}

VerificationResult TimestampVerifier::verify_artifact(
    const ImportedTimestampToken& token, ByteView artifact,
    HashAlgorithm algorithm) const {
    return verify(token, Digest::compute(algorithm, artifact));
}

VerificationResult TimestampVerifier::verify(ByteView token_der,
                                             ByteView artifact,
                                             HashAlgorithm algorithm) const {
    const ImportResult imported = TimestampTokenImporter::from_der(token_der);
    if (!imported.ok()) {
        return VerificationResult::failure(imported.code, imported.message);
    }
    return verify_artifact(imported.token, artifact, algorithm);
}

VerificationResult TimestampVerifier::verify_file(
    const ImportedTimestampToken& token, const std::string& artifact_path,
    HashAlgorithm algorithm) const {
    std::string error;
    const Digest digest = Digest::compute_file(algorithm, artifact_path, &error);
    if (!error.empty()) {
        return VerificationResult::failure(ErrorCode::IoError, error);
    }
    return verify(token, digest);
}

VerificationResult TimestampVerifier::verify_file(
    ByteView token_der, const std::string& artifact_path,
    HashAlgorithm algorithm) const {
    const ImportResult imported = TimestampTokenImporter::from_der(token_der);
    if (!imported.ok()) {
        return VerificationResult::failure(imported.code, imported.message);
    }
    return verify_file(imported.token, artifact_path, algorithm);
}

// ===========================================================================
// Importers
// ===========================================================================

ImportResult TimestampTokenImporter::from_der(ByteView der) {
    return detail::parse_token_der(der);
}

ImportResult ImportedTimestampToken::from_der(ByteView der) {
    return TimestampTokenImporter::from_der(der);
}

ImportResult ImportedTimestampToken::parse(ByteView der) {
    return TimestampTokenImporter::from_der(der);
}

ImportResult ImportedTimestampToken::parse(std::string_view encoded) {
    return TimestampTokenImporter::import(encoded);
}

ImportResult ImportedTimestampToken::from_file(const std::string& path) {
    return TimestampTokenImporter::from_file(path);
}

ImportResult TimestampTokenImporter::from_base64(std::string_view base64) {
    auto decoded = detail::base64_decode(base64);
    if (!decoded) {
        ImportResult result;
        result.code = ErrorCode::InvalidDer;
        result.message = "timestamp token is not valid Base64";
        return result;
    }
    return from_der(ByteView(*decoded));
}

ImportResult TimestampTokenImporter::from_pem(std::string_view pem) {
    for (std::string_view label :
         {"TIMESTAMP-TOKEN", "PKCS7", "CMS", "CMS SIGNED-DATA"}) {
        auto blocks = detail::parse_pem_blocks(pem, label);
        if (!blocks.empty()) {
            return from_der(ByteView(blocks.front()));
        }
    }
    ImportResult result;
    result.code = ErrorCode::InvalidDer;
    result.message = "no timestamp-token PEM block found";
    return result;
}

ImportResult TimestampTokenImporter::from_file(const std::string& path) {
    Bytes data;
    std::string error;
    if (!detail::read_file(path, data, error)) {
        ImportResult result;
        result.code = ErrorCode::IoError;
        result.message = error;
        return result;
    }
    return import(ByteView(data));
}

ImportResult TimestampTokenImporter::import(ByteView input) {
    if (input.empty()) {
        ImportResult result;
        result.code = ErrorCode::InvalidDer;
        result.message = "empty timestamp token";
        return result;
    }
    if (detail::looks_like_text(input)) {
        const std::string_view text(
            reinterpret_cast<const char*>(input.data()), input.size());
        if (text.find("-----BEGIN") != std::string_view::npos) {
            return from_pem(text);
        }
        return from_base64(text);
    }
    return from_der(input);
}

ImportResult TimestampTokenImporter::import(std::string_view input) {
    return import(ByteView(reinterpret_cast<const std::uint8_t*>(input.data()),
                           input.size()));
}

ImportResult import_timestamp_token(ByteView der) {
    return TimestampTokenImporter::from_der(der);
}

ImportResult import_timestamp_token(const Bytes& der) {
    return import_timestamp_token(ByteView(der));
}

ImportResult import_timestamp_token(std::string_view encoded) {
    return TimestampTokenImporter::import(encoded);
}

ImportResult import_timestamp_token(const std::string& encoded) {
    return import_timestamp_token(std::string_view(encoded));
}

ImportResult import_timestamp_token_base64(std::string_view base64) {
    return TimestampTokenImporter::from_base64(base64);
}

ImportResult import_timestamp_token_pem(std::string_view pem) {
    return TimestampTokenImporter::from_pem(pem);
}

ImportResult import_timestamp_token_file(const std::string& path) {
    return TimestampTokenImporter::from_file(path);
}

// ===========================================================================
// One-shot verification helpers
// ===========================================================================

VerificationResult verify_timestamp_token(
    const ImportedTimestampToken& token, const Digest& artifact_digest,
    const VerificationPolicy& policy, const TrustStore& trust,
    const RevocationEvidence& revocation) {
    return detail::verify_parsed_token(token, artifact_digest, policy, trust,
                                       revocation, {});
}

VerificationResult verify_timestamp_token(
    ByteView token_der, const Digest& artifact_digest,
    const VerificationPolicy& policy, const TrustStore& trust,
    const RevocationEvidence& revocation) {
    const ImportResult imported = TimestampTokenImporter::from_der(token_der);
    if (!imported.ok()) {
        return VerificationResult::failure(imported.code, imported.message);
    }
    return verify_timestamp_token(imported.token, artifact_digest, policy, trust,
                                  revocation);
}

VerificationResult verify_timestamp_token(
    const Bytes& token_der, const Digest& artifact_digest,
    const VerificationPolicy& policy, const TrustStore& trust,
    const RevocationEvidence& revocation) {
    return verify_timestamp_token(ByteView(token_der), artifact_digest, policy,
                                  trust, revocation);
}

VerificationResult verify_timestamp_token(
    ByteView token_der, ByteView artifact, HashAlgorithm algorithm,
    const VerificationPolicy& policy, const TrustStore& trust,
    const RevocationEvidence& revocation) {
    const ImportResult imported = TimestampTokenImporter::from_der(token_der);
    if (!imported.ok()) {
        return VerificationResult::failure(imported.code, imported.message);
    }
    return verify_timestamp_token(
        imported.token, Digest::compute(algorithm, artifact), policy, trust,
        revocation);
}

VerificationResult verify_token_binding(
    const Bytes& token_der, const Digest& artifact_digest,
    const VerificationPolicy& policy, const TrustStore& trust,
    const RevocationEvidence& revocation) {
    return verify_timestamp_token(token_der, artifact_digest, policy, trust,
                                  revocation);
}

VerificationResult verify_timestamp_token_binding(
    const Bytes& token_der, const Digest& artifact_digest,
    const VerificationPolicy& policy, const TrustStore& trust,
    const RevocationEvidence& revocation) {
    return verify_timestamp_token(token_der, artifact_digest, policy, trust,
                                  revocation);
}

VerificationResult ImportedTimestampToken::verify(
    const Digest& artifact_digest, const VerificationPolicy& policy,
    const TrustStore& trust, const RevocationEvidence& revocation) const {
    return verify_timestamp_token(*this, artifact_digest, policy, trust,
                                  revocation);
}

std::optional<CertificateInfo> parse_certificate(ByteView der,
                                                 std::string* error) {
    return detail::parse_certificate(der, error);
}

std::optional<Bytes> normalize_crl(ByteView der, std::string* error) {
    return detail::normalize_crl(der, error);
}

}  // namespace netsec
