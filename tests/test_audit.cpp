#include "mediasafe/common.h"

#include <cassert>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <string>
#include <vector>

using mediasafe::AuditLimits;
using mediasafe::AuditResult;
using mediasafe::MediaFormat;
using mediasafe::crc32_ieee;

void test_tiff(); // defined in test_tiff.cpp

namespace {

int g_checks = 0;

#define EXPECT_OK(buf, limits)                                              \
    do {                                                                    \
        ++g_checks;                                                         \
        AuditResult r_ = mediasafe::audit((buf).data(), (buf).size(),       \
                                          (limits));                        \
        if (!r_.ok) {                                                       \
            std::cerr << "FAIL expected ok: " << r_.error                   \
                      << " (" << __FILE__ << ":" << __LINE__ << ")\n";      \
            std::abort();                                                   \
        }                                                                   \
    } while (0)

#define EXPECT_REJECT(buf, limits, needle)                                  \
    do {                                                                    \
        ++g_checks;                                                         \
        AuditResult r_ = mediasafe::audit((buf).data(), (buf).size(),       \
                                          (limits));                        \
        if (r_.ok || r_.error.find(needle) == std::string::npos) {         \
            std::cerr << "FAIL expected rejection containing '" << needle   \
                      << "', got ok=" << r_.ok << " err='" << r_.error      \
                      << "' (" << __FILE__ << ":" << __LINE__ << ")\n";     \
            std::abort();                                                   \
        }                                                                   \
    } while (0)

// ---------------------------------------------------------------- PNG utils

const uint8_t kPngSig[8] = {0x89u, 0x50u, 0x4Eu, 0x47u,
                            0x0Du, 0x0Au, 0x1Au, 0x0Au};

void put_be32(std::vector<uint8_t>& b, uint32_t v) {
    b.push_back(static_cast<uint8_t>((v >> 24) & 0xFFu));
    b.push_back(static_cast<uint8_t>((v >> 16) & 0xFFu));
    b.push_back(static_cast<uint8_t>((v >> 8) & 0xFFu));
    b.push_back(static_cast<uint8_t>(v & 0xFFu));
}

void append_chunk(std::vector<uint8_t>& b, const char type[4],
                  const std::vector<uint8_t>& payload,
                  uint32_t crc_override = 0,
                  bool use_override = false) {
    put_be32(b, static_cast<uint32_t>(payload.size()));
    const std::size_t type_pos = b.size();
    for (int i = 0; i < 4; ++i) {
        b.push_back(static_cast<uint8_t>(type[i]));
    }
    const std::size_t data_pos = b.size();
    b.insert(b.end(), payload.begin(), payload.end());
    const uint32_t crc =
        crc32_ieee(b.data() + type_pos, 4 + payload.size()) ^ 0xFFFFFFFFu;
    put_be32(b, use_override ? crc_override : crc);
    (void)data_pos;
}

std::vector<uint8_t> make_ihdr(uint32_t w, uint32_t h, uint8_t depth,
                               uint8_t color) {
    std::vector<uint8_t> p;
    put_be32(p, w);
    put_be32(p, h);
    p.push_back(depth);
    p.push_back(color);
    p.push_back(0); // compression
    p.push_back(0); // filter
    p.push_back(0); // interlace
    return p;
}

std::vector<uint8_t> minimal_png(uint32_t w = 4, uint32_t h = 4,
                                 uint8_t depth = 8, uint8_t color = 6) {
    std::vector<uint8_t> b(kPngSig, kPngSig + 8);
    append_chunk(b, "IHDR", make_ihdr(w, h, depth, color));
    std::vector<uint8_t> idat(16, 0xABu);
    append_chunk(b, "IDAT", idat);
    append_chunk(b, "IEND", {});
    return b;
}

void test_png() {
    const AuditLimits lim{};

    // Valid RGBA / grayscale / indexed samples.
    EXPECT_OK(minimal_png(4, 4, 8, 6), lim);
    EXPECT_OK(minimal_png(8, 8, 16, 2), lim);
    {
        std::vector<uint8_t> b(kPngSig, kPngSig + 8);
        append_chunk(b, "IHDR", make_ihdr(2, 2, 8, 3));
        std::vector<uint8_t> plte(3 * 4, 0u); // 4 palette entries
        append_chunk(b, "PLTE", plte);
        append_chunk(b, "IDAT", std::vector<uint8_t>(8, 1u));
        append_chunk(b, "IEND", {});
        EXPECT_OK(b, lim);
    }

    // Bad magic.
    {
        std::vector<uint8_t> b = minimal_png();
        b[0] = 0x00u;
        EXPECT_REJECT(b, lim, "signature");
    }

    // Truncated chunk (cut off CRC).
    {
        std::vector<uint8_t> b = minimal_png();
        b.resize(b.size() - 2);
        EXPECT_REJECT(b, lim, "CRC");
    }

    // Forged CRC on IHDR.
    {
        std::vector<uint8_t> b(kPngSig, kPngSig + 8);
        append_chunk(b, "IHDR", make_ihdr(4, 4, 8, 6),
                     0xDEADBEEFu, true);
        append_chunk(b, "IDAT", std::vector<uint8_t>(16, 1u));
        append_chunk(b, "IEND", {});
        EXPECT_REJECT(b, lim, "CRC");
    }

    // Width/height integer overflow: valid-range dims whose pixel product
    // overflows a 32-bit counter (70000*70000*4 > 2^32) and blows the budget.
    {
        std::vector<uint8_t> b(kPngSig, kPngSig + 8);
        append_chunk(b, "IHDR", make_ihdr(70000u, 70000u, 8, 6));
        append_chunk(b, "IDAT", std::vector<uint8_t>(16, 1u));
        append_chunk(b, "IEND", {});
        EXPECT_REJECT(b, lim, "host limit");
    }

    // Individual dimension beyond the 2^31-1 hard ceiling.
    {
        std::vector<uint8_t> b(kPngSig, kPngSig + 8);
        append_chunk(b, "IHDR", make_ihdr(0x80000000u, 0x80000000u, 8, 6));
        append_chunk(b, "IDAT", std::vector<uint8_t>(16, 1u));
        append_chunk(b, "IEND", {});
        EXPECT_REJECT(b, lim, "2^31-1");
    }

    // Dimensions exceeding 2^31-1.
    {
        std::vector<uint8_t> b(kPngSig, kPngSig + 8);
        append_chunk(b, "IHDR", make_ihdr(0xFFFFFFFFu, 4, 8, 6));
        append_chunk(b, "IDAT", std::vector<uint8_t>(16, 1u));
        append_chunk(b, "IEND", {});
        EXPECT_REJECT(b, lim, "2^31-1");
    }

    // Illegal bit depth / color type combination.
    {
        std::vector<uint8_t> b(kPngSig, kPngSig + 8);
        append_chunk(b, "IHDR", make_ihdr(4, 4, 16, 3));
        append_chunk(b, "IDAT", std::vector<uint8_t>(16, 1u));
        append_chunk(b, "IEND", {});
        EXPECT_REJECT(b, lim, "bit depth");
    }

    // Duplicate IHDR.
    {
        std::vector<uint8_t> b(kPngSig, kPngSig + 8);
        append_chunk(b, "IHDR", make_ihdr(4, 4, 8, 6));
        append_chunk(b, "IHDR", make_ihdr(4, 4, 8, 6));
        append_chunk(b, "IDAT", std::vector<uint8_t>(16, 1u));
        append_chunk(b, "IEND", {});
        EXPECT_REJECT(b, lim, "duplicate IHDR");
    }

    // Trailing payload hidden after IEND.
    {
        std::vector<uint8_t> b = minimal_png();
        b.push_back(0x77u);
        EXPECT_REJECT(b, lim, "after IEND");
    }

    // Chunk length high bit set (> 2^31-1).
    {
        std::vector<uint8_t> b(kPngSig, kPngSig + 8);
        put_be32(b, 0x80000000u);
        b.insert(b.end(), {'I', 'H', 'D', 'R'});
        b.insert(b.end(), 13, 0u);
        put_be32(b, 0u);
        EXPECT_REJECT(b, lim, "2^31-1");
    }

    // PLTE length not a multiple of 3.
    {
        std::vector<uint8_t> b(kPngSig, kPngSig + 8);
        append_chunk(b, "IHDR", make_ihdr(2, 2, 8, 3));
        append_chunk(b, "PLTE", std::vector<uint8_t>(7, 0u));
        append_chunk(b, "IDAT", std::vector<uint8_t>(8, 1u));
        append_chunk(b, "IEND", {});
        EXPECT_REJECT(b, lim, "multiple of 3");
    }

    // PLTE with more than 256 entries.
    {
        std::vector<uint8_t> b(kPngSig, kPngSig + 8);
        append_chunk(b, "IHDR", make_ihdr(2, 2, 8, 3));
        append_chunk(b, "PLTE", std::vector<uint8_t>(3 * 257, 0u));
        append_chunk(b, "IDAT", std::vector<uint8_t>(8, 1u));
        append_chunk(b, "IEND", {});
        EXPECT_REJECT(b, lim, "256 entries");
    }

    // Non-consecutive IDAT chunks.
    {
        std::vector<uint8_t> b(kPngSig, kPngSig + 8);
        append_chunk(b, "IHDR", make_ihdr(4, 4, 8, 6));
        append_chunk(b, "IDAT", std::vector<uint8_t>(8, 1u));
        append_chunk(b, "tEXt", std::vector<uint8_t>{'x', 0u});
        append_chunk(b, "IDAT", std::vector<uint8_t>(8, 2u));
        append_chunk(b, "IEND", {});
        EXPECT_REJECT(b, lim, "consecutive");
    }

    // Host pixel-budget enforcement.
    {
        AuditLimits tight{};
        tight.max_pixel_bytes = 64;
        EXPECT_REJECT(minimal_png(16, 16, 8, 6), tight, "host limit");
        EXPECT_OK(minimal_png(2, 2, 8, 6), tight);
    }
}

} // namespace

int main() {
    // Self-check of the embedded CRC against the known CRC32 of "123456789".
    const char* check = "123456789";
    const uint32_t got =
        crc32_ieee(reinterpret_cast<const uint8_t*>(check), 9) ^
        0xFFFFFFFFu;
    assert(got == 0xCBF43926u);

    test_png();
    test_tiff();
    std::cout << "All " << g_checks << " audit checks passed.\n";
    return 0;
}
