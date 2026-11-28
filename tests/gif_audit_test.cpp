// Unit tests for the gif_audit security auditor.
//
// GIF byte streams (including a minimal LZW encoder) are built from scratch
// so every malicious sample is constructed at the raw-byte level with no
// dependency on any image library.

#include "gif_audit/gif_audit.hpp"

#include <algorithm>
#include <cstdint>
#include <cstdio>
#include <map>
#include <string>
#include <vector>

namespace {

using gif_audit::AuditResult;
using gif_audit::ErrorCode;
using gif_audit::Limits;
using Bytes = std::vector<std::uint8_t>;

int g_checks = 0;
int g_failures = 0;

void check(bool condition, const std::string& name) {
    ++g_checks;
    if (condition) {
        std::printf("  [PASS] %s\n", name.c_str());
    } else {
        ++g_failures;
        std::printf("  [FAIL] %s\n", name.c_str());
    }
}

void expect_ok(const Bytes& data, const std::string& name) {
    const AuditResult result = gif_audit::audit(data);
    if (!result.ok) {
        std::printf("         unexpected %s at %zu: %s\n",
                    gif_audit::error_code_name(result.code), result.offset,
                    result.detail.c_str());
    }
    check(result.ok, name);
}

void expect_error(const Bytes& data, ErrorCode expected,
                  const std::string& name) {
    const AuditResult result = gif_audit::audit(data);
    const bool matched = !result.ok && result.code == expected;
    if (!matched) {
        std::printf("         expected %s, got %s at %zu: %s\n",
                    gif_audit::error_code_name(expected),
                    gif_audit::error_code_name(result.code), result.offset,
                    result.detail.c_str());
    }
    check(matched, name);
}

void append_u16(Bytes& out, std::uint16_t value) {
    out.push_back(static_cast<std::uint8_t>(value & 0xFFU));
    out.push_back(static_cast<std::uint8_t>((value >> 8) & 0xFFU));
}

void append_bytes(Bytes& out, const Bytes& tail) {
    out.insert(out.end(), tail.begin(), tail.end());
}

// --- Minimal LZW encoder (GIF variant, LSB-first, width up to 12 bits). ----

class BitWriter {
public:
    void write(std::uint16_t code, unsigned width) {
        for (unsigned bit = 0; bit < width; ++bit) {
            const unsigned value =
                (static_cast<unsigned>(code) >> bit) & 1U;
            if (bits_in_buffer_ == 8U) {
                out_.push_back(buffer_);
                buffer_ = 0;
                bits_in_buffer_ = 0;
            }
            buffer_ = static_cast<std::uint8_t>(
                buffer_ | static_cast<std::uint8_t>(value
                                                     << bits_in_buffer_));
            ++bits_in_buffer_;
        }
    }

    Bytes finish() && {
        if (bits_in_buffer_ > 0U) {
            out_.push_back(buffer_);
            buffer_ = 0;
            bits_in_buffer_ = 0;
        }
        return std::move(out_);
    }

private:
    Bytes out_;
    std::uint8_t buffer_ = 0;
    unsigned bits_in_buffer_ = 0;
};

using Sequence = std::vector<std::uint16_t>;

Bytes lzw_encode(const std::vector<std::uint8_t>& pixels,
                 std::uint8_t min_code_size, bool emit_clears) {
    const std::uint16_t clear_code =
        static_cast<std::uint16_t>(1U << min_code_size);
    const std::uint16_t eoi_code =
        static_cast<std::uint16_t>(clear_code + 1U);

    std::map<Sequence, std::uint16_t> table;
    auto reset_table = [&]() -> std::uint16_t {
        table.clear();
        const std::uint16_t root_count =
            static_cast<std::uint16_t>(clear_code + 2U);
        for (std::uint16_t symbol = 0; symbol < clear_code; ++symbol) {
            table.emplace(Sequence{symbol}, symbol);
        }
        return root_count;
    };

    std::uint16_t next_code = reset_table();
    unsigned code_width = static_cast<unsigned>(min_code_size) + 1U;

    BitWriter writer;
    writer.write(clear_code, code_width);

    Sequence current;
    for (std::uint8_t pixel : pixels) {
        Sequence candidate = current;
        candidate.push_back(static_cast<std::uint16_t>(pixel));
        if (table.find(candidate) != table.end()) {
            current = candidate;
        } else {
            writer.write(table.at(current), code_width);
            const bool room = next_code < 4096;
            if (room) {
                table.emplace(candidate, next_code);
                ++next_code;
                if (next_code ==
                        static_cast<std::uint16_t>(1U << code_width) &&
                    code_width < 12U) {
                    ++code_width;
                }
            } else if (emit_clears) {
                writer.write(clear_code, code_width);
                next_code = reset_table();
                code_width = static_cast<unsigned>(min_code_size) + 1U;
            }
            current = Sequence{static_cast<std::uint16_t>(pixel)};
        }
    }
    if (!current.empty()) {
        writer.write(table.at(current), code_width);
    }
    writer.write(eoi_code, code_width);
    return std::move(writer).finish();
}

// Wrap packed LZW bytes into <=255 byte sub-blocks plus a 0x00 terminator.
Bytes frame_sub_blocks(const Bytes& payload) {
    Bytes out;
    constexpr std::size_t kChunk = 255;
    for (std::size_t i = 0; i < payload.size(); i += kChunk) {
        const std::size_t len = std::min(kChunk, payload.size() - i);
        out.push_back(static_cast<std::uint8_t>(len));
        out.insert(out.end(),
                   payload.begin() + static_cast<std::ptrdiff_t>(i),
                   payload.begin() +
                       static_cast<std::ptrdiff_t>(i + len));
    }
    out.push_back(0);
    return out;
}

Bytes gif_header(bool gif89a, std::uint16_t width, std::uint16_t height,
                 std::uint8_t gct_packed = 0xF1) {
    Bytes out;
    const char* magic = gif89a ? "GIF89a" : "GIF87a";
    for (int i = 0; i < 6; ++i) {
        out.push_back(static_cast<std::uint8_t>(magic[i]));
    }
    append_u16(out, width);
    append_u16(out, height);
    out.push_back(gct_packed);  // size field 1 -> 4-entry GCT
    out.push_back(0);
    out.push_back(0);
    return out;
}

Bytes gct_4_entries() {
    return Bytes{0x00, 0x00, 0x00,  // 0 black
                 0xFF, 0x00, 0x00,  // 1 red
                 0x00, 0xFF, 0x00,  // 2 green
                 0x00, 0x00, 0xFF}; // 3 blue
}

Bytes image_descriptor(std::uint16_t left, std::uint16_t top,
                       std::uint16_t width, std::uint16_t height,
                       std::uint8_t packed = 0x00) {
    Bytes out;
    out.push_back(0x2C);
    append_u16(out, left);
    append_u16(out, top);
    append_u16(out, width);
    append_u16(out, height);
    out.push_back(packed);
    return out;
}

// Fully valid single-frame GIF exercising GCE + comment + application
// extension and a genuinely encoded 4x2 image.
Bytes build_valid_gif(bool gif89a) {
    Bytes out = gif_header(gif89a, 4, 2);
    append_bytes(out, gct_4_entries());

    out.push_back(0x21);  // Graphic Control Extension
    out.push_back(0xF9);
    out.push_back(0x04);
    out.push_back(0x00);
    append_u16(out, 10);  // delay
    out.push_back(0x00);  // transparency index
    out.push_back(0x00);  // terminator

    out.push_back(0x21);  // Comment Extension
    out.push_back(0xFE);
    out.push_back(0x05);
    const char note[] = "hello";
    for (std::size_t i = 0; i < sizeof(note) - 1; ++i) {
        out.push_back(static_cast<std::uint8_t>(note[i]));
    }
    out.push_back(0x00);

    out.push_back(0x21);  // Application Extension (NETSCAPE2.0)
    out.push_back(0xFF);
    out.push_back(0x0B);
    const char app[] = "NETSCAPE2.0";
    for (std::size_t i = 0; i < sizeof(app) - 1; ++i) {
        out.push_back(static_cast<std::uint8_t>(app[i]));
    }
    out.push_back(0x03);
    out.push_back(0x01);
    append_u16(out, 0);
    out.push_back(0x00);

    append_bytes(out, image_descriptor(0, 0, 4, 2));

    const std::vector<std::uint8_t> pixels = {0, 1, 2, 3, 3, 2, 1, 0};
    constexpr std::uint8_t kMinCodeSize = 2;
    const Bytes encoded = lzw_encode(pixels, kMinCodeSize, true);
    out.push_back(kMinCodeSize);
    append_bytes(out, frame_sub_blocks(encoded));
    out.push_back(0x3B);
    return out;
}

}  // namespace

int main() {
    std::printf("gif_audit test suite\n");

    // --- Valid containers -------------------------------------------------
    std::printf("[valid containers]\n");
    expect_ok(build_valid_gif(true),
              "valid GIF89a with GCE/comment/application extensions");
    expect_ok(build_valid_gif(false), "valid GIF87a single frame");
    {
        const AuditResult result = gif_audit::audit(build_valid_gif(true));
        check(result.screen_width == 4 && result.screen_height == 2,
              "summary reports canvas 4x2");
        check(result.image_count == 1, "summary reports one image");
        check(result.total_decoded_pixels == 8,
              "summary reports eight decoded pixels");
        check(result.is_gif89a, "summary flags GIF89a");
        check(result.global_color_table_entries == 4,
              "summary reports four GCT entries");
    }

    // --- Header / signature ----------------------------------------------
    std::printf("[header attacks]\n");
    expect_error(Bytes{}, ErrorCode::EmptyInput, "empty input rejected");
    expect_error(Bytes{0x00}, ErrorCode::TruncatedHeader,
                 "single byte is truncated header");
    {
        Bytes bad = build_valid_gif(true);
        bad[0] = 'P';
        bad[1] = 'N';
        bad[2] = 'G';
        expect_error(bad, ErrorCode::BadSignature, "bad magic rejected");
    }
    {
        Bytes zero_canvas = gif_header(true, 0, 2);
        append_bytes(zero_canvas, gct_4_entries());
        zero_canvas.push_back(0x3B);
        expect_error(zero_canvas, ErrorCode::CanvasWidthZero,
                     "zero canvas width rejected");
    }
    {
        Bytes truncated = gif_header(true, 4, 2);
        append_bytes(truncated, gct_4_entries());
        truncated.pop_back();  // chop last GCT byte
        expect_error(truncated, ErrorCode::TruncatedGlobalColorTable,
                     "truncated global color table rejected");
    }

    // --- LZW attacks ------------------------------------------------------
    std::printf("[LZW attacks]\n");
    {
        // minCodeSize=12: would drive >12-bit shifts / oversized tables.
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(0, 0, 4, 2));
        gif.push_back(12);
        const std::vector<std::uint8_t> pixels = {0, 1, 2, 3,
                                                  3, 2, 1, 0};
        append_bytes(gif, frame_sub_blocks(lzw_encode(pixels, 2, true)));
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::InvalidLzwMinCodeSize,
                     "minCodeSize=12 hard rejected");
    }
    {
        // Width-3 stream: clear=4, eoi=5, next_code starts at 6. Emit 7.
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(0, 0, 4, 2));
        gif.push_back(2);
        const Bytes malicious{0x07, 0x00};  // code 7 in low bits
        append_bytes(gif, frame_sub_blocks(malicious));
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::LzwFutureCode,
                     "future LZW code rejected");
    }
    {
        // Decompression bomb: 1x1 frame fed 16 pixels of encoded output.
        Bytes gif = gif_header(true, 1, 1);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(0, 0, 1, 1));
        gif.push_back(2);
        const std::vector<std::uint8_t> pixels(16, 0);
        append_bytes(gif, frame_sub_blocks(lzw_encode(pixels, 2, true)));
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::LzwOutputOverflow,
                     "decompression bomb (pixels > width*height) rejected");
    }
    {
        // Dictionary saturation without a Clear Code. Enumerating every
        // length-6 tuple over a 4-symbol alphabet creates >4094 distinct
        // phrases; the frame (256x96 = 24576 pixels) gives enough headroom
        // that the 4096-entry dictionary cap, not pixel overflow, fires.
        constexpr std::uint16_t kWidth = 256;
        constexpr std::uint16_t kHeight = 96;
        Bytes gif = gif_header(true, kWidth, kHeight);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(0, 0, kWidth, kHeight));
        gif.push_back(2);
        std::vector<std::uint8_t> pixels;
        pixels.reserve(4096U * 6U);
        for (std::uint32_t i = 0; i < 4096U; ++i) {
            std::uint32_t value = i;
            for (int digit = 0; digit < 6; ++digit) {
                pixels.push_back(
                    static_cast<std::uint8_t>(value & 0x03U));
                value >>= 2;
            }
        }
        append_bytes(gif,
                     frame_sub_blocks(lzw_encode(pixels, 2, false)));
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::LzwTableFullNoClear,
                     "dictionary >4096 without Clear Code rejected");
    }
    {
        // Truncated LZW stream (no EOI, bits run out mid-code).
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(0, 0, 4, 2));
        gif.push_back(2);
        const Bytes truncated_stream{0x84};  // clear then dangling bits
        append_bytes(gif, frame_sub_blocks(truncated_stream));
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::LzwTruncated,
                     "truncated LZW bit stream rejected");
    }
    {
        // Fewer pixels than the frame declares, ending cleanly with EOI.
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(0, 0, 4, 2));
        gif.push_back(2);
        const std::vector<std::uint8_t> pixels = {0, 1, 2, 3};  // 4 of 8
        append_bytes(gif, frame_sub_blocks(lzw_encode(pixels, 2, true)));
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::LzwOutputShort,
                     "short LZW output vs frame size rejected");
    }

    // --- Container / block attacks ----------------------------------------
    std::printf("[container attacks]\n");
    {
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(2, 0, 4, 2));  // 2 + 4 > 4
        gif.push_back(2);
        const std::vector<std::uint8_t> pixels(8, 0);
        append_bytes(gif, frame_sub_blocks(lzw_encode(pixels, 2, true)));
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::ImageFrameOutOfBounds,
                     "frame left+width beyond canvas rejected");
    }
    {
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(0, 1, 4, 2));  // 1 + 2 > 2
        gif.push_back(2);
        const std::vector<std::uint8_t> pixels(8, 0);
        append_bytes(gif, frame_sub_blocks(lzw_encode(pixels, 2, true)));
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::ImageFrameOutOfBounds,
                     "frame top+height beyond canvas rejected");
    }
    {
        // Missing 0x00 block terminator on image data.
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(0, 0, 4, 2));
        gif.push_back(2);
        const std::vector<std::uint8_t> pixels = {0, 1, 2, 3,
                                                  3, 2, 1, 0};
        Bytes framed = frame_sub_blocks(lzw_encode(pixels, 2, true));
        framed.pop_back();  // remove mandatory terminator
        append_bytes(gif, framed);
        // Stream now ends where the mandatory 0x00 terminator should be.
        expect_error(gif, ErrorCode::TruncatedBlock,
                     "missing image block terminator rejected");
    }
    {
        // Terminator replaced by a non-zero byte: parser keeps expecting a
        // sub-block length and runs past the end of the input.
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(0, 0, 4, 2));
        gif.push_back(2);
        const std::vector<std::uint8_t> pixels = {0, 1, 2, 3,
                                                  3, 2, 1, 0};
        Bytes framed = frame_sub_blocks(lzw_encode(pixels, 2, true));
        framed.back() = 0x7F;  // claims 127 bytes, none remain
        append_bytes(gif, framed);
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::SubBlockOverrun,
                     "corrupt terminator leading to overrun rejected");
    }
    {
        // Sub-block length claims more bytes than remain in the input.
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(0, 0, 4, 2));
        gif.push_back(2);
        gif.push_back(0x10);  // claims 16 bytes
        gif.push_back(0x00);  // only one payload byte follows
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::SubBlockOverrun,
                     "sub-block length overrun rejected");
    }
    {
        // GIFAR: valid GIF with trailing HTML/JS polyglot after Trailer.
        Bytes gif = build_valid_gif(true);
        const char payload[] = "<script>alert(1)</script>";
        for (char c : payload) gif.push_back(static_cast<std::uint8_t>(c));
        expect_error(gif, ErrorCode::TrailingBytesAfterTrailer,
                     "polyglot payload after Trailer rejected");
    }
    {
        // No Trailer at all.
        Bytes gif = build_valid_gif(true);
        gif.pop_back();  // remove 0x3B
        expect_error(gif, ErrorCode::MissingTrailer,
                     "missing GIF Trailer rejected");
    }
    {
        // Polyglot inside a well-formed Comment Extension stays legal; only
        // post-Trailer smuggling is blocked.
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        gif.push_back(0x21);
        gif.push_back(0xFE);
        const char comment[] = "<script>alert(1)</script>";
        gif.push_back(static_cast<std::uint8_t>(sizeof(comment) - 1));
        for (std::size_t i = 0; i < sizeof(comment) - 1; ++i) {
            gif.push_back(static_cast<std::uint8_t>(comment[i]));
        }
        gif.push_back(0x00);
        append_bytes(gif, image_descriptor(0, 0, 4, 2));
        gif.push_back(2);
        const std::vector<std::uint8_t> pixels = {0, 1, 2, 3,
                                                  3, 2, 1, 0};
        append_bytes(gif, frame_sub_blocks(lzw_encode(pixels, 2, true)));
        gif.push_back(0x3B);
        expect_ok(gif, "in-extension polyglot stays structurally valid");
    }
    {
        // Unknown extension label is hard rejected, not skipped.
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        gif.push_back(0x21);
        gif.push_back(0x42);
        gif.push_back(0x00);
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::UnknownExtensionLabel,
                     "unknown extension label rejected");
    }
    {
        // GCE with wrong block size.
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        gif.push_back(0x21);
        gif.push_back(0xF9);
        gif.push_back(0x05);  // must be 4
        gif.push_back(0x00);
        gif.push_back(0x00);
        gif.push_back(0x00);
        gif.push_back(0x00);
        gif.push_back(0x00);
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::InvalidExtensionData,
                     "malformed GCE block size rejected");
    }
    {
        // Image Descriptor reserved bit (bit 6) set.
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        append_bytes(gif, image_descriptor(0, 0, 4, 2, 0x40));
        gif.push_back(2);
        const std::vector<std::uint8_t> pixels(8, 0);
        append_bytes(gif, frame_sub_blocks(lzw_encode(pixels, 2, true)));
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::ImageReservedBitsSet,
                     "image descriptor reserved bit rejected");
    }
    {
        // Unknown block introducer.
        Bytes gif = gif_header(true, 4, 2);
        append_bytes(gif, gct_4_entries());
        gif.push_back(0x99);
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::UnknownBlockIntroducer,
                     "unknown block introducer rejected");
    }
    {
        // Local Color Table (2 entries, size field 0) plus a valid stream.
        Bytes gif = gif_header(true, 4, 2, 0x00);  // no GCT
        append_bytes(gif, image_descriptor(0, 0, 4, 2, 0x80));
        append_bytes(gif, Bytes{0x00, 0x00, 0x00});  // local entry 0
        append_bytes(gif, Bytes{0xFF, 0xFF, 0xFF});  // local entry 1
        gif.push_back(2);
        const std::vector<std::uint8_t> pixels(8, 1);
        append_bytes(gif, frame_sub_blocks(lzw_encode(pixels, 2, true)));
        gif.push_back(0x3B);
        expect_ok(gif, "local color table image accepted");
    }
    {
        // Largest legal minCodeSize (8) round-trips through the auditor.
        Bytes gif = gif_header(true, 2, 2, 0x00);  // no GCT
        append_bytes(gif, image_descriptor(0, 0, 2, 2));
        gif.push_back(8);
        const std::vector<std::uint8_t> pixels = {0, 1, 2, 3};
        append_bytes(gif, frame_sub_blocks(lzw_encode(pixels, 8, true)));
        gif.push_back(0x3B);
        expect_ok(gif, "minCodeSize=8 accepted");
    }
    {
        // minCodeSize=1 is below the legal floor of 2.
        Bytes gif = gif_header(true, 2, 2, 0x00);
        append_bytes(gif, image_descriptor(0, 0, 2, 2));
        gif.push_back(1);
        const std::vector<std::uint8_t> pixels = {0, 1, 0, 1};
        append_bytes(gif, frame_sub_blocks(lzw_encode(pixels, 2, true)));
        gif.push_back(0x3B);
        expect_error(gif, ErrorCode::InvalidLzwMinCodeSize,
                     "minCodeSize=1 hard rejected");
    }

    // --- Quota enforcement ------------------------------------------------
    std::printf("[quota enforcement]\n");
    {
        Limits small;
        small.max_canvas_width = 3;
        const AuditResult result =
            gif_audit::audit(build_valid_gif(true), small);
        check(!result.ok && result.code == ErrorCode::CanvasWidthQuota,
              "custom canvas width quota enforced");
    }

    return g_failures == 0 ? 0 : 1;
}
