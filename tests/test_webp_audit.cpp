#include "webp_audit.hpp"

#include <cstdint>
#include <cstdio>
#include <string>
#include <vector>

namespace {

using Bytes = std::vector<std::uint8_t>;
using webp_audit::AuditError;
using webp_audit::AuditOptions;
using webp_audit::AuditResult;

void append_u16(Bytes& b, std::uint16_t v) {
    b.push_back(static_cast<std::uint8_t>(v & 0xffu));
    b.push_back(static_cast<std::uint8_t>((v >> 8) & 0xffu));
}

void append_u24(Bytes& b, std::uint32_t v) {
    b.push_back(static_cast<std::uint8_t>(v & 0xffu));
    b.push_back(static_cast<std::uint8_t>(v >> 8) & 0xffu);
    b.push_back(static_cast<std::uint8_t>(v >> 16) & 0xffu);
}

void append_u32(Bytes& b, std::uint32_t v) {
    b.push_back(static_cast<std::uint8_t>(v & 0xffu));
    b.push_back(static_cast<std::uint8_t>(v >> 8) & 0xffu);
    b.push_back(static_cast<std::uint8_t>(v >> 16) & 0xffu);
    b.push_back(static_cast<std::uint8_t>(v >> 24) & 0xffu);
}

void append_tag(Bytes& b, const char* tag) {
    for (int i = 0; i < 4; ++i) {
        b.push_back(static_cast<std::uint8_t>(tag[i]));
    }
}

// Appends a chunk with proper RIFF odd-size zero padding.
void append_chunk(Bytes& b, const char* tag, const Bytes& payload) {
    append_tag(b, tag);
    append_u32(b, static_cast<std::uint32_t>(payload.size()));
    b.insert(b.end(), payload.begin(), payload.end());
    if ((payload.size() & 1u) != 0) {
        b.push_back(0x00u);
    }
}

// Wraps a chunk body (already including fourcc/size/padding) in a RIFF/WEBP.
Bytes riff_wrap(const Bytes& body) {
    Bytes out;
    append_tag(out, "RIFF");
    append_u32(out, static_cast<std::uint32_t>(body.size() + 4u));
    append_tag(out, "WEBP");
    out.insert(out.end(), body.begin(), body.end());
    return out;
}

// Minimal 10-byte VP8 lossy keyframe payload encoding width/height (1..16383).
Bytes make_vp8(std::uint16_t width, std::uint16_t height) {
    Bytes p;
    p.push_back(0x00u); // frame tag: key frame (bit0 = 0), version 0
    p.push_back(0x00u);
    p.push_back(0x00u);
    p.push_back(0x9du); // start code
    p.push_back(0x01u);
    p.push_back(0x2au);
    append_u16(p, width);
    append_u16(p, height);
    return p;
}

// Minimal 5-byte VP8L payload encoding width/height (1..16384), alpha hint.
Bytes make_vp8l(std::uint32_t width, std::uint32_t height, bool alpha) {
    const std::uint32_t w = width - 1u;
    const std::uint32_t h = height - 1u;
    Bytes p;
    p.push_back(0x2fu); // signature
    p.push_back(static_cast<std::uint8_t>(w & 0xffu));
    p.push_back(static_cast<std::uint8_t>(((w >> 8) & 0x3fu) | ((h & 0x03u) << 6)));
    p.push_back(static_cast<std::uint8_t>((h >> 2) & 0xffu));
    std::uint8_t b4 = static_cast<std::uint8_t>((h >> 10) & 0x3fu);
    if (alpha) b4 |= 0x20u;
    p.push_back(b4);
    return p;
}

Bytes make_vp8x(std::uint32_t canvas_w, std::uint32_t canvas_h,
                std::uint8_t flags) {
    Bytes p;
    p.push_back(flags);
    p.push_back(0x00u);
    p.push_back(0x00u);
    p.push_back(0x00u);
    append_u24(p, canvas_w - 1u);
    append_u24(p, canvas_h - 1u);
    return p;
}

Bytes make_alph(std::uint8_t filler = 0x00u) {
    return Bytes{0x00u, 0x00u, filler}; // odd size -> exercises zero padding
}

Bytes make_anim() {
    Bytes p;
    append_u32(p, 0u);       // background color
    append_u16(p, 0u);       // loop count
    return p;
}

// Builds an ANMF frame with an inner VP8L image and optional ALPH.
Bytes make_anmf(std::uint32_t x, std::uint32_t y, std::uint32_t w,
                std::uint32_t h, bool alph, bool alpha_hint = false) {
    Bytes inner;
    if (alph) append_chunk(inner, "ALPH", make_alph());
    append_chunk(inner, "VP8L", make_vp8l(w, h, alpha_hint));

    Bytes p;
    append_u24(p, x);
    append_u24(p, y);
    append_u24(p, w - 1u);
    append_u24(p, h - 1u);
    p.push_back(0x00u); // flags
    p.push_back(0x00u);
    p.push_back(0x00u); // 3 bytes duration
    p.push_back(0x00u);
    p.insert(p.end(), inner.begin(), inner.end());
    return p;
}

int g_failures = 0;
int g_checks = 0;

void check(bool cond, const char* name) {
    ++g_checks;
    if (!cond) {
        ++g_failures;
        std::printf("FAIL: %s\n", name);
    }
}

void expect_ok(const Bytes& b, const char* name) {
    const AuditResult r =
        webp_audit::audit(b.data(), b.size(), AuditOptions{});
    if (!r.ok()) {
        ++g_failures;
        std::printf("FAIL: %s -> rejected: %s (offset %zu)\n", name,
                    webp_audit::to_string(r.error), r.offset);
    }
}

void expect_err(const Bytes& b, AuditError expected, const char* name) {
    const AuditResult r =
        webp_audit::audit(b.data(), b.size(), AuditOptions{});
    ++g_checks;
    if (r.ok() || r.error != expected) {
        ++g_failures;
        std::printf("FAIL: %s -> expected %d, got %d (%s)\n", name,
                    static_cast<int>(expected), static_cast<int>(r.error),
                    webp_audit::to_string(r.error));
    }
}

} // namespace

namespace {

void test_valid_simple_vp8() {
    Bytes body;
    append_chunk(body, "VP8 ", make_vp8(320, 240));
    const Bytes file = riff_wrap(body);
    expect_ok(file, "valid simple VP8");
    const AuditResult r = webp_audit::audit(file.data(), file.size());
    check(r.info.width == 320 && r.info.height == 240, "simple VP8 geometry");
    check(r.info.frame_count == 1 && !r.info.has_animation,
          "simple VP8 frame count");
}

void test_valid_simple_vp8l_odd_padding() {
    Bytes body;
    // VP8L payload is 5 bytes (odd) -> must carry one 0x00 pad byte.
    append_chunk(body, "VP8L", make_vp8l(33, 17, true));
    const Bytes file = riff_wrap(body);
    expect_ok(file, "valid simple VP8L with odd padding");
    const AuditResult r = webp_audit::audit(file.data(), file.size());
    check(r.info.width == 33 && r.info.height == 17, "VP8L geometry");
    check(r.info.has_alpha, "VP8L alpha hint propagated");
}

void test_valid_extended_static() {
    Bytes body;
    const std::uint8_t flags = 0x20u | 0x10u | 0x08u | 0x04u;
    append_chunk(body, "VP8X", make_vp8x(64, 48, flags));
    append_chunk(body, "ICCP", Bytes{1u, 2u, 3u});
    append_chunk(body, "ALPH", make_alph());
    append_chunk(body, "VP8L", make_vp8l(64, 48, false));
    append_chunk(body, "EXIF", Bytes{7u, 8u});
    append_chunk(body, "XMP ", Bytes{9u});
    const Bytes file = riff_wrap(body);
    expect_ok(file, "valid extended static WebP");
    const AuditResult r = webp_audit::audit(file.data(), file.size());
    check(r.info.width == 64 && r.info.height == 48, "extended canvas");
    check(r.info.has_alpha && r.info.has_icc && r.info.has_exif &&
              r.info.has_xmp && !r.info.has_animation,
          "extended feature flags");
}

void test_valid_animation() {
    Bytes body;
    const std::uint8_t flags = 0x10u | 0x02u; // alpha + animation
    append_chunk(body, "VP8X", make_vp8x(32, 16, flags));
    append_chunk(body, "ANIM", make_anim());
    append_chunk(body, "ANMF", make_anmf(0, 0, 32, 16, true, true));
    append_chunk(body, "ANMF", make_anmf(8, 4, 16, 8, false));
    const Bytes file = riff_wrap(body);
    expect_ok(file, "valid animated WebP");
    const AuditResult r = webp_audit::audit(file.data(), file.size());
    check(r.info.has_animation && r.info.has_alpha, "animation flags");
    check(r.info.frame_count == 2, "animation frame count");
    check(r.info.width == 32 && r.info.height == 16, "animation canvas");
}

} // namespace

namespace {

// Locates the single odd-size padding byte of a chunk and overwrites it.
void corrupt_first_padding(Bytes& b) {
    // Skip 12-byte RIFF header, then find first chunk with an odd chunk_size.
    std::size_t pos = 12;
    while (pos + 8 <= b.size()) {
        std::uint32_t sz = static_cast<std::uint32_t>(b[pos + 4]) |
                           (static_cast<std::uint32_t>(b[pos + 5]) << 8) |
                           (static_cast<std::uint32_t>(b[pos + 6]) << 16) |
                           (static_cast<std::uint32_t>(b[pos + 7]) << 24);
        std::size_t pad = pos + 8 + sz;
        if ((sz & 1u) != 0 && pad < b.size()) {
            b[pad] = 0x41u; // smuggle a non-zero byte where 0x00 is mandated
            return;
        }
        pos = pad + ((sz & 1u) != 0 ? 1u : 0u);
    }
}

void test_nonzero_padding() {
    Bytes body;
    append_chunk(body, "VP8L", make_vp8l(33, 17, false)); // 5-byte odd payload
    Bytes file = riff_wrap(body);
    corrupt_first_padding(file);
    expect_err(file, AuditError::kNonZeroPadding, "non-zero odd pad byte");
}

void test_missing_padding() {
    Bytes body;
    append_chunk(body, "VP8L", make_vp8l(33, 17, false));
    Bytes file = riff_wrap(body);
    file.pop_back(); // drop the mandatory zero pad -> RIFF size now inconsistent
    expect_err(file, AuditError::kBadRiffSize, "missing pad shifts RIFF size");

    // Rebuild a container whose RIFF size matches the truncated bytes.
    Bytes file2 = riff_wrap(body);
    file2.pop_back();
    const std::size_t new_size = file2.size() - 8;
    file2[4] = static_cast<std::uint8_t>(new_size & 0xffu);
    file2[5] = static_cast<std::uint8_t>((new_size >> 8) & 0xffu);
    file2[6] = static_cast<std::uint8_t>((new_size >> 16) & 0xffu);
    file2[7] = static_cast<std::uint8_t>((new_size >> 24) & 0xffu);
    expect_err(file2, AuditError::kMissingPaddingByte, "missing pad byte detected");
}

void test_riff_size_wraparound() {
    Bytes body;
    append_chunk(body, "VP8 ", make_vp8(16, 16));
    Bytes file = riff_wrap(body);
    // Claim a 32-bit size that, after +8, would wrap; real size is tiny.
    file[4] = 0xffu;
    file[5] = 0xffu;
    file[6] = 0xffu;
    file[7] = 0xffu;
    expect_err(file, AuditError::kBadRiffSize, "RIFF size 32-bit wraparound");
}

void test_trailing_hidden_data() {
    Bytes body;
    append_chunk(body, "VP8 ", make_vp8(16, 16));
    Bytes file = riff_wrap(body);
    file.push_back(0xdeu); // undeclared payload past the container
    file.push_back(0xadu);
    expect_err(file, AuditError::kBadRiffSize, "trailing hidden data");
}

void test_vp8x_flag_contradiction() {
    Bytes body;
    append_chunk(body, "VP8X", make_vp8x(32, 32, 0x08u)); // claims EXIF
    append_chunk(body, "VP8L", make_vp8l(32, 32, false));  // but no EXIF chunk
    expect_err(riff_wrap(body), AuditError::kFlagChunkMismatch,
               "VP8X EXIF flag without EXIF chunk");

    Bytes body2;
    append_chunk(body2, "VP8X", make_vp8x(32, 32, 0x00u)); // no flags
    append_chunk(body2, "ICCP", Bytes{1u}); // undeclared ICCP
    append_chunk(body2, "VP8L", make_vp8l(32, 32, false));
    expect_err(riff_wrap(body2), AuditError::kFlagChunkMismatch,
               "ICCP chunk without VP8X ICC flag");

    Bytes body3;
    append_chunk(body3, "VP8X", make_vp8x(32, 32, 0x10u)); // claims alpha
    append_chunk(body3, "VP8 ", make_vp8(32, 32));          // no ALPH present
    expect_err(riff_wrap(body3), AuditError::kFlagChunkMismatch,
               "VP8X alpha flag without alpha source");
}

void test_anmf_outside_canvas() {
    Bytes body;
    append_chunk(body, "VP8X", make_vp8x(32, 16, 0x02u));
    append_chunk(body, "ANIM", make_anim());
    // x=24 + width=16 = 40 > canvas width 32
    append_chunk(body, "ANMF", make_anmf(24, 0, 16, 16, false));
    expect_err(riff_wrap(body), AuditError::kFrameOutsideCanvas,
               "ANMF frame extends past canvas");
}

void test_truncated_and_oversize_chunks() {
    Bytes body;
    append_chunk(body, "VP8 ", make_vp8(16, 16));
    Bytes file = riff_wrap(body);
    Bytes truncated = file;
    truncated.pop_back();
    truncated.pop_back(); // cut mid last chunk; RIFF size no longer matches
    expect_err(truncated, AuditError::kBadRiffSize, "truncated container");

    Bytes body2;
    append_chunk(body2, "VP8 ", make_vp8(16, 16));
    Bytes file2 = riff_wrap(body2);
    // Inflate chunk_size beyond remaining bytes while keeping RIFF size valid.
    file2[12 + 4] = 0xffu;
    file2[12 + 5] = 0xffu;
    expect_err(file2, AuditError::kChunkPayloadOutOfBounds,
               "chunk size exceeds container");
}

void test_duplicate_and_ordering() {
    Bytes body;
    append_chunk(body, "VP8 ", make_vp8(16, 16));
    append_chunk(body, "VP8 ", make_vp8(16, 16));
    expect_err(riff_wrap(body), AuditError::kUnexpectedChunk,
               "simple format rejects second chunk");

    Bytes body2;
    append_chunk(body2, "VP8X", make_vp8x(16, 16, 0x00u));
    append_chunk(body2, "VP8L", make_vp8l(16, 16, false));
    append_chunk(body2, "VP8L", make_vp8l(16, 16, false));
    expect_err(riff_wrap(body2), AuditError::kMultipleImageFrames,
               "duplicate image frame rejected");

    Bytes body3;
    append_chunk(body3, "VP8X", make_vp8x(16, 16, 0x00u));
    append_chunk(body3, "VP8L", make_vp8l(16, 16, false));
    append_chunk(body3, "ICCP", Bytes{1u}); // ICCP must precede bitstream
    expect_err(riff_wrap(body3), AuditError::kInvalidChunkOrder,
               "ICCP after bitstream rejected");
}

void test_bad_frame_headers() {
    Bytes bad_vp8 = make_vp8(16, 16);
    bad_vp8[0] = 0x01u; // not a key frame
    Bytes body;
    append_chunk(body, "VP8 ", bad_vp8);
    expect_err(riff_wrap(body), AuditError::kNotKeyFrame, "VP8 non-keyframe");

    Bytes bad_code = make_vp8(16, 16);
    bad_code[3] = 0x00u; // destroy start code
    Bytes body2;
    append_chunk(body2, "VP8 ", bad_code);
    expect_err(riff_wrap(body2), AuditError::kBadVp8StartCode,
               "VP8 bad start code");

    Bytes bad_l = make_vp8l(16, 16, false);
    bad_l[0] = 0x00u; // destroy 0x2f signature
    Bytes body3;
    append_chunk(body3, "VP8L", bad_l);
    expect_err(riff_wrap(body3), AuditError::kBadVp8lSignature,
               "VP8L bad signature");
}

void test_pixel_quota_and_unknown_chunk() {
    AuditOptions tight{};
    tight.max_pixels = 100u;
    Bytes body;
    append_chunk(body, "VP8 ", make_vp8(16, 16)); // 256 pixels
    const Bytes file = riff_wrap(body);
    const AuditResult r = webp_audit::audit(file.data(), file.size(), tight);
    check(!r.ok() && r.error == AuditError::kPixelCountExceedsQuota,
          "pixel quota enforced");

    Bytes body2;
    append_chunk(body2, "JUNK", Bytes{1u, 2u});
    expect_err(riff_wrap(body2), AuditError::kInvalidFirstChunk,
               "unknown first chunk rejected");

    Bytes body3;
    append_chunk(body3, "VP8X", make_vp8x(16, 16, 0x00u));
    append_chunk(body3, "VP8L", make_vp8l(16, 16, false));
    append_chunk(body3, "JUNK", Bytes{3u});
    expect_err(riff_wrap(body3), AuditError::kUnknownChunk,
               "unknown extended chunk rejected");
}

void test_vp8x_and_anmf_strictness() {
    Bytes body;
    append_chunk(body, "VP8X", make_vp8x(16, 16, 0x40u)); // reserved flag set
    append_chunk(body, "VP8L", make_vp8l(16, 16, false));
    expect_err(riff_wrap(body), AuditError::kReservedVp8xFlagSet,
               "reserved VP8X flag rejected");

    Bytes body2;
    Bytes bad_vp8x = make_vp8x(16, 16, 0x00u);
    bad_vp8x.push_back(0x00u); // 11 bytes, VP8X must be exactly 10
    append_chunk(body2, "VP8X", bad_vp8x);
    append_chunk(body2, "VP8L", make_vp8l(16, 16, false));
    expect_err(riff_wrap(body2), AuditError::kInvalidVp8xSize,
               "VP8X wrong size rejected");

    // ANMF whose inner bitstream dimensions disagree with frame header.
    Bytes body3;
    append_chunk(body3, "VP8X", make_vp8x(32, 32, 0x02u));
    append_chunk(body3, "ANIM", make_anim());
    append_chunk(body3, "ANMF", make_anmf(0, 0, 32, 32, false));
    Bytes mismatched = riff_wrap(body3);
    std::size_t anmf_tag = 0;
    for (std::size_t i = 12; i + 4 <= mismatched.size(); ++i) {
        if (mismatched[i] == 'A' && mismatched[i + 1] == 'N' &&
            mismatched[i + 2] == 'M' && mismatched[i + 3] == 'F') {
            anmf_tag = i;
            break;
        }
    }
    // Frame width is the 24-bit field at ANMF payload offset 6.
    mismatched[anmf_tag + 8 + 6] = 30u; // width becomes 31 vs inner 32
    expect_err(mismatched, AuditError::kFrameDimensionMismatch,
               "ANMF/bitstream dimension mismatch rejected");
}

void test_null_and_tiny_inputs() {
    const AuditResult null_r = webp_audit::audit(nullptr, 4);
    check(null_r.error == AuditError::kNullInput, "null input rejected");

    Bytes tiny{'R', 'I', 'F', 'F'};
    const AuditResult tiny_r =
        webp_audit::audit(tiny.data(), tiny.size());
    check(tiny_r.error == AuditError::kTooSmall, "sub-header input rejected");
}

} // namespace

int main() {
    test_valid_simple_vp8();
    test_valid_simple_vp8l_odd_padding();
    test_valid_extended_static();
    test_valid_animation();
    test_nonzero_padding();
    test_missing_padding();
    test_riff_size_wraparound();
    test_trailing_hidden_data();
    test_vp8x_flag_contradiction();
    test_anmf_outside_canvas();
    test_truncated_and_oversize_chunks();
    test_duplicate_and_ordering();
    test_bad_frame_headers();
    test_pixel_quota_and_unknown_chunk();
    test_vp8x_and_anmf_strictness();
    test_null_and_tiny_inputs();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
