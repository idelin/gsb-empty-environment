// gif_audit.hpp - Raw-byte security auditor for GIF87a/GIF89a containers.
//
// Strict, allocation-bounded parse of a complete GIF byte stream:
//   * 13-byte header / Logical Screen Descriptor validation.
//   * Exact global/local color table byte accounting and canvas quotas.
//   * Sequential block parsing (Image 0x2C, Extension 0x21, single Trailer
//     0x3B) with hard rejection of sub-block overruns, missing 0x00 block
//     terminators and any trailing bytes after the Trailer (GIFAR/polyglot).
//   * Full emulation of the LZW variable-width (LSB-first) bit stream:
//     min-code-size, future-code, 4096-entry dictionary cap, output-pixel
//     (decompression bomb) and deferred-clear checks.
//
// C++17, standard library only, clean under
// -Wall -Wextra -Wconversion -Werror. No pixel buffer is allocated; audit
// memory stays O(4096) dictionary entries.

#ifndef GIF_AUDIT_GIF_AUDIT_HPP
#define GIF_AUDIT_GIF_AUDIT_HPP

#include <cstddef>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

namespace gif_audit {

// Stable, machine-readable rejection codes for gateway telemetry.
enum class ErrorCode : int {
    Ok = 0,
    EmptyInput,
    TruncatedHeader,
    BadSignature,
    CanvasWidthZero,
    CanvasHeightZero,
    CanvasWidthQuota,
    CanvasHeightQuota,
    CanvasPixelsQuota,
    BackgroundIndexOutOfRange,
    TruncatedGlobalColorTable,
    TruncatedBlock,
    UnknownBlockIntroducer,
    UnknownExtensionLabel,
    InvalidExtensionData,
    TruncatedImageDescriptor,
    ImageReservedBitsSet,
    TruncatedLocalColorTable,
    ImageFrameZeroDimension,
    ImageFrameOutOfBounds,
    InvalidLzwMinCodeSize,
    SubBlockOverrun,
    MissingBlockTerminator,
    LzwTruncated,
    LzwFutureCode,
    LzwOutputOverflow,
    LzwTableFullNoClear,
    LzwOutputShort,
    MissingTrailer,
    TrailingBytesAfterTrailer
};

// Host-tunable resource quotas bounding the worst-case forced work.
struct Limits {
    std::uint32_t max_canvas_width  = 65535;   // spec ceiling
    std::uint32_t max_canvas_height = 65535;   // spec ceiling
    std::uint32_t max_frame_pixels  = 67108864;   // 64 Mi pixels/frame
    std::uint64_t max_total_pixels  = 268435456;  // 256 Mi pixels/file

    static Limits strict_defaults() noexcept { return Limits{}; }
};

struct AuditResult {
    bool ok = false;
    ErrorCode code = ErrorCode::Ok;
    // Byte offset that triggered rejection, or input length on success.
    std::size_t offset = 0;
    std::string detail;

    std::uint16_t screen_width = 0;
    std::uint16_t screen_height = 0;
    bool has_global_color_table = false;
    std::uint32_t global_color_table_entries = 0;
    bool is_gif89a = false;
    std::uint32_t image_count = 0;
    std::uint64_t total_decoded_pixels = 0;

    explicit operator bool() const noexcept { return ok; }
};

// Audit a contiguous byte buffer. The caller retains ownership.
AuditResult audit(const std::uint8_t* data, std::size_t size,
                  const Limits& limits = Limits{});

inline AuditResult audit(const std::vector<std::uint8_t>& data,
                         const Limits& limits = Limits{}) {
    return audit(data.data(), data.size(), limits);
}

const char* error_code_name(ErrorCode code) noexcept;

namespace detail {

// Bounds-checked cursor over the caller-owned, immutable input.
class Reader {
public:
    Reader(const std::uint8_t* data, std::size_t size) noexcept
        : data_(data), size_(size), pos_(0) {}

    std::size_t pos() const noexcept { return pos_; }
    std::size_t remaining() const noexcept { return size_ - pos_; }
    bool eof() const noexcept { return pos_ >= size_; }
    const std::uint8_t* cursor() const noexcept { return data_ + pos_; }

    std::uint8_t peek() const noexcept { return data_[pos_]; }

    bool read_u8(std::uint8_t& out) noexcept {
        if (pos_ >= size_) return false;
        out = data_[pos_];
        ++pos_;
        return true;
    }

    bool read_u16_le(std::uint16_t& out) noexcept {
        if (remaining() < 2) return false;
        out = static_cast<std::uint16_t>(
            static_cast<std::uint16_t>(data_[pos_]) |
            (static_cast<std::uint16_t>(data_[pos_ + 1]) << 8));
        pos_ += 2;
        return true;
    }

    bool skip(std::size_t count) noexcept {
        if (count > remaining()) return false;
        pos_ += count;
        return true;
    }

    bool bytes_equal(const char* literal, std::size_t count) const noexcept {
        if (pos_ + count > size_) return false;
        return std::memcmp(data_ + pos_, literal, count) == 0;
    }

private:
    const std::uint8_t* data_;
    std::size_t size_;
    std::size_t pos_;
};

struct Error {
    ErrorCode code;
    std::size_t offset;
    const char* detail;
};

inline AuditResult fail(const Error& err) {
    AuditResult result;
    result.ok = false;
    result.code = err.code;
    result.offset = err.offset;
    result.detail = err.detail;
    return result;
}

inline std::uint16_t palette_size_entries(std::uint8_t packed) noexcept {
    const unsigned exponent = static_cast<unsigned>(packed & 0x07U);
    return static_cast<std::uint16_t>(1U << (exponent + 1U));
}

// Raw image sub-block bytes kept for the LZW bit reader; pixels are never
// materialized.
struct LzwStream {
    std::vector<std::uint8_t> bytes;
};

// Consume a [length][bytes...]...[0x00] sub-block chain. Every length and
// payload byte is validated against the input boundary. When body is non-null
// the payload bytes are accumulated (LZW data); otherwise they are skipped
// (extension payloads).
inline Error read_sub_blocks(Reader& reader,
                             std::vector<std::uint8_t>* body) {
    for (;;) {
        std::uint8_t length = 0;
        if (!reader.read_u8(length)) {
            return Error{ErrorCode::TruncatedBlock, reader.pos(),
                         "sub-block length byte missing"};
        }
        if (length == 0) {
            return Error{ErrorCode::Ok, reader.pos(), ""};
        }
        if (static_cast<std::size_t>(length) > reader.remaining()) {
            return Error{ErrorCode::SubBlockOverrun, reader.pos(),
                         "sub-block payload extends past end of input"};
        }
        if (body != nullptr) {
            const std::uint8_t* start = reader.cursor();
            body->insert(body->end(), start,
                         start + static_cast<std::size_t>(length));
        }
        if (!reader.skip(static_cast<std::size_t>(length))) {
            return Error{ErrorCode::SubBlockOverrun, reader.pos(),
                         "sub-block payload extends past end of input"};
        }
    }
}

}  // namespace detail

// ---------------------------------------------------------------------------
// LZW variable-width (LSB-first) emulation
// ---------------------------------------------------------------------------

namespace detail {

// LSB-first bit reader over the concatenated image sub-blocks.
class BitReader {
public:
    explicit BitReader(const std::vector<std::uint8_t>& bytes) noexcept
        : bytes_(bytes) {}

    // Read 1..12 bits. Returns false only when source bytes run out before the
    // full code width is available.
    bool read(unsigned width, std::uint16_t& code) noexcept {
        std::uint32_t value = 0;
        for (unsigned bit = 0; bit < width; ++bit) {
            if (bits_left_ == 0) {
                if (byte_pos_ >= bytes_.size()) return false;
                buffer_ = bytes_[byte_pos_++];
                bits_left_ = 8;
            }
            const unsigned bit_value =
                static_cast<unsigned>(buffer_ & 0x01U);
            value |= bit_value << bit;
            buffer_ = static_cast<std::uint8_t>(buffer_ >> 1);
            --bits_left_;
        }
        code = static_cast<std::uint16_t>(value);
        return true;
    }

private:
    const std::vector<std::uint8_t>& bytes_;
    std::size_t byte_pos_ = 0;
    std::uint8_t buffer_ = 0;
    unsigned bits_left_ = 0;
};

struct DictEntry {
    std::uint16_t prefix = 0;  // parent dictionary/literal index
    std::uint16_t first = 0;   // first output symbol of the entry
    std::uint16_t length = 0;  // emitted pixel count of the entry
};

// Full LZW validation. Pixels are never materialized; only the output length
// is tracked, which is exactly what decompression-bomb protection requires.
inline Error audit_lzw(const LzwStream& stream, std::uint8_t min_code_size,
                       std::uint64_t frame_pixels,
                       std::uint64_t& running_total,
                       const Limits& limits) {
    if (min_code_size < 2 || min_code_size > 8) {
        return Error{ErrorCode::InvalidLzwMinCodeSize, 0,
                     "LZW minimum code size must be in [2, 8]"};
    }
    if (stream.bytes.empty()) {
        return Error{ErrorCode::LzwTruncated, 0,
                     "image data contains no LZW sub-blocks"};
    }

    const unsigned initial_width =
        static_cast<unsigned>(min_code_size) + 1U;
    const std::uint16_t clear_code =
        static_cast<std::uint16_t>(1U << min_code_size);
    const std::uint16_t eoi_code =
        static_cast<std::uint16_t>(clear_code + 1U);
    const std::uint16_t root_count =
        static_cast<std::uint16_t>(clear_code + 2U);

    // Heap-allocated 4096-entry dictionary keeps worst-case stack tiny.
    std::vector<DictEntry> dictionary(4096);
    for (std::uint16_t symbol = 0; symbol < clear_code; ++symbol) {
        dictionary[symbol].prefix = symbol;
        dictionary[symbol].first = symbol;
        dictionary[symbol].length = 1;
    }

    BitReader bits(stream.bytes);
    std::uint16_t next_code = root_count;
    unsigned code_width = initial_width;
    bool have_previous = false;
    std::uint16_t previous = 0;
    std::uint64_t emitted = 0;

    auto emit_count = [&](std::uint32_t count) -> Error {
        if (static_cast<std::uint64_t>(count) > frame_pixels - emitted) {
            return Error{ErrorCode::LzwOutputOverflow, 0,
                         "LZW output exceeds frame width*height"};
        }
        emitted += static_cast<std::uint64_t>(count);
        if (emitted > limits.max_frame_pixels ||
            running_total + emitted > limits.max_total_pixels) {
            return Error{ErrorCode::LzwOutputOverflow, 0,
                         "LZW output exceeds configured pixel quota"};
        }
        return Error{ErrorCode::Ok, 0, ""};
    };

    for (;;) {
        std::uint16_t code = 0;
        if (!bits.read(code_width, code)) {
            return Error{ErrorCode::LzwTruncated, 0,
                         "LZW bit stream ended before End Of Information"};
        }
        if (code == clear_code) {
            // Deferred clear: table reset is always bounded by the fixed
            // 4096-entry allocation, so a delayed/absent clear can never
            // overflow the dictionary storage.
            next_code = root_count;
            code_width = initial_width;
            have_previous = false;
            continue;
        }
        if (code == eoi_code) {
            if (emitted != frame_pixels) {
                return Error{ErrorCode::LzwOutputShort, 0,
                             "LZW stream produced fewer pixels than "
                             "frame width*height"};
            }
            running_total += emitted;
            return Error{ErrorCode::Ok, 0, ""};
        }

        std::uint32_t entry_length = 0;
        if (code < clear_code) {
            entry_length = 1;
        } else if (code < next_code) {
            entry_length =
                static_cast<std::uint32_t>(dictionary[code].length);
        } else if (code == next_code) {
            // KwKw: one-past-the-end code, valid only with a prior entry.
            if (!have_previous) {
                return Error{ErrorCode::LzwFutureCode, 0,
                             "LZW code references an undefined dictionary "
                             "entry"};
            }
            entry_length =
                static_cast<std::uint32_t>(dictionary[previous].length) + 1U;
        } else {
            return Error{ErrorCode::LzwFutureCode, 0,
                         "LZW code references a future dictionary entry"};
        }

        Error emit_err = emit_count(entry_length);
        if (emit_err.code != ErrorCode::Ok) return emit_err;

        if (have_previous) {
            if (next_code >= 4096) {
                // Spec requires a Clear Code before any 4097th entry; without
                // it naive decoders write past their dictionary allocation.
                return Error{ErrorCode::LzwTableFullNoClear, 0,
                             "LZW dictionary exceeded 4096 entries without a "
                             "Clear Code"};
            }
            const std::uint32_t new_length =
                static_cast<std::uint32_t>(dictionary[previous].length) + 1U;
            if (new_length > 0xFFFFU) {
                return Error{ErrorCode::LzwOutputOverflow, 0,
                             "LZW phrase length exceeds 16-bit storage"};
            }
            DictEntry& added = dictionary[next_code];
            added.prefix = previous;
            added.first = dictionary[previous].first;
            added.length = static_cast<std::uint16_t>(new_length);
            ++next_code;
            if (next_code == static_cast<std::uint16_t>(
                                 (1U << code_width) - 1U) &&
                code_width < 12U) {
                ++code_width;
            }
        }

        previous = code;
        have_previous = true;
    }
}

}  // namespace detail

// ---------------------------------------------------------------------------
// Container parsing
// ---------------------------------------------------------------------------

namespace detail {

inline Error parse_extension(Reader& reader) {
    std::uint8_t label = 0;
    if (!reader.read_u8(label)) {
        return Error{ErrorCode::TruncatedBlock, reader.pos(),
                     "extension label missing"};
    }

    switch (label) {
        case 0xF9: {  // Graphic Control Extension
            std::uint8_t block_size = 0;
            if (!reader.read_u8(block_size) || block_size != 4U) {
                return Error{ErrorCode::InvalidExtensionData, reader.pos(),
                             "GCE block size must be 4"};
            }
            if (reader.remaining() < 5U) {
                return Error{ErrorCode::TruncatedBlock, reader.pos(),
                             "truncated Graphic Control Extension"};
            }
            const std::uint8_t gce_packed = reader.peek();
            if ((gce_packed & 0x07U) != 0U) {
                return Error{ErrorCode::InvalidExtensionData, reader.pos(),
                             "GCE reserved bits must be zero"};
            }
            const unsigned disposal =
                (static_cast<unsigned>(gce_packed) >> 2U) & 0x07U;
            if (disposal > 3U) {
                return Error{ErrorCode::InvalidExtensionData, reader.pos(),
                             "GCE disposal method is reserved"};
            }
            // 4 body bytes (packed, delay lo/hi, transparency index).
            if (!reader.skip(4)) {
                return Error{ErrorCode::TruncatedBlock, reader.pos(),
                             "truncated GCE body"};
            }
            std::uint8_t terminator = 0;
            if (!reader.read_u8(terminator) || terminator != 0U) {
                return Error{ErrorCode::MissingBlockTerminator, reader.pos(),
                             "GCE missing 0x00 block terminator"};
            }
            return Error{ErrorCode::Ok, reader.pos(), ""};
        }
        case 0xFF: {  // Application Extension
            std::uint8_t block_size = 0;
            if (!reader.read_u8(block_size) || block_size != 11U) {
                return Error{ErrorCode::InvalidExtensionData, reader.pos(),
                             "application extension block size must be 11"};
            }
            if (!reader.skip(11)) {
                return Error{ErrorCode::TruncatedBlock, reader.pos(),
                             "truncated application identifier"};
            }
            return read_sub_blocks(reader, nullptr);
        }
        case 0x01: {  // Plain Text Extension
            std::uint8_t block_size = 0;
            if (!reader.read_u8(block_size) || block_size != 12U) {
                return Error{ErrorCode::InvalidExtensionData, reader.pos(),
                             "plain text block size must be 12"};
            }
            if (!reader.skip(12)) {
                return Error{ErrorCode::TruncatedBlock, reader.pos(),
                             "truncated plain text header"};
            }
            return read_sub_blocks(reader, nullptr);
        }
        case 0xFE:  // Comment Extension
            return read_sub_blocks(reader, nullptr);
        default:
            return Error{ErrorCode::UnknownExtensionLabel, reader.pos(),
                         "unknown GIF extension label"};
    }
}

inline Error parse_image(Reader& reader, std::uint16_t screen_width,
                         std::uint16_t screen_height,
                         std::uint64_t& running_total,
                         const Limits& limits) {
    // Image Descriptor: left(2) top(2) width(2) height(2) packed(1).
    if (reader.remaining() < 9U) {
        return Error{ErrorCode::TruncatedImageDescriptor, reader.pos(),
                     "truncated Image Descriptor"};
    }
    std::uint16_t left = 0;
    std::uint16_t top = 0;
    std::uint16_t width = 0;
    std::uint16_t height = 0;
    if (!reader.read_u16_le(left) || !reader.read_u16_le(top) ||
        !reader.read_u16_le(width) || !reader.read_u16_le(height)) {
        return Error{ErrorCode::TruncatedImageDescriptor, reader.pos(),
                     "truncated Image Descriptor geometry"};
    }
    std::uint8_t img_packed = 0;
    if (!reader.read_u8(img_packed)) {
        return Error{ErrorCode::TruncatedImageDescriptor, reader.pos(),
                     "Image Descriptor packed byte missing"};
    }
    if ((img_packed & 0x40U) != 0U) {  // bit 6 reserved, must be zero
        return Error{ErrorCode::ImageReservedBitsSet, reader.pos(),
                     "Image Descriptor reserved bit is set"};
    }
    if (width == 0U || height == 0U) {
        return Error{ErrorCode::ImageFrameZeroDimension, reader.pos(),
                     "image frame width/height must be non-zero"};
    }

    // Frame geometry must lie fully inside the logical screen. 32-bit
    // promotion prevents any addition wrap.
    if (static_cast<std::uint32_t>(left) +
            static_cast<std::uint32_t>(width) >
        static_cast<std::uint32_t>(screen_width)) {
        return Error{ErrorCode::ImageFrameOutOfBounds, reader.pos(),
                     "frame left+width exceeds screen width"};
    }
    if (static_cast<std::uint32_t>(top) +
            static_cast<std::uint32_t>(height) >
        static_cast<std::uint32_t>(screen_height)) {
        return Error{ErrorCode::ImageFrameOutOfBounds, reader.pos(),
                     "frame top+height exceeds screen height"};
    }

    const std::uint64_t frame_pixels =
        static_cast<std::uint64_t>(width) *
        static_cast<std::uint64_t>(height);
    if (frame_pixels > static_cast<std::uint64_t>(limits.max_frame_pixels)) {
        return Error{ErrorCode::LzwOutputOverflow, reader.pos(),
                     "frame pixel count exceeds frame quota"};
    }

    if ((img_packed & 0x80U) != 0U) {  // Local Color Table
        const std::uint16_t entries = palette_size_entries(img_packed);
        const std::size_t table_bytes =
            static_cast<std::size_t>(entries) * 3U;
        if (table_bytes > reader.remaining()) {
            return Error{ErrorCode::TruncatedLocalColorTable, reader.pos(),
                         "local color table extends past end of input"};
        }
        if (!reader.skip(table_bytes)) {
            return Error{ErrorCode::TruncatedLocalColorTable, reader.pos(),
                         "truncated local color table"};
        }
    }

    std::uint8_t min_code_size = 0;
    if (!reader.read_u8(min_code_size)) {
        return Error{ErrorCode::TruncatedBlock, reader.pos(),
                     "LZW minimum code size byte missing"};
    }
    if (min_code_size < 2U || min_code_size > 8U) {
        return Error{ErrorCode::InvalidLzwMinCodeSize, reader.pos(),
                     "LZW minimum code size must be in [2, 8]"};
    }

    LzwStream stream;
    const Error sub_err = read_sub_blocks(reader, &stream.bytes);
    if (sub_err.code != ErrorCode::Ok) return sub_err;
    if (stream.bytes.empty()) {
        return Error{ErrorCode::MissingBlockTerminator, reader.pos(),
                     "image data has no data sub-blocks"};
    }

    Error lzw_err = audit_lzw(stream, min_code_size, frame_pixels,
                              running_total, limits);
    if (lzw_err.code != ErrorCode::Ok) {
        lzw_err.offset = reader.pos();
        return lzw_err;
    }
    return Error{ErrorCode::Ok, reader.pos(), ""};
}

}  // namespace detail

inline AuditResult audit(const std::uint8_t* data, std::size_t size,
                         const Limits& limits) {
    using detail::Error;
    using detail::fail;
    using detail::palette_size_entries;
    using detail::parse_extension;
    using detail::parse_image;
    using detail::Reader;

    if (data == nullptr || size == 0U) {
        return fail(Error{ErrorCode::EmptyInput, 0, "empty input"});
    }
    Reader reader(data, size);

    // --- Signature (6 bytes) ----------------------------------------------
    if (reader.remaining() < 6U) {
        return fail(Error{ErrorCode::TruncatedHeader, reader.pos(),
                          "shorter than GIF signature"});
    }
    bool is_gif89a = false;
    if (reader.bytes_equal("GIF89a", 6)) {
        is_gif89a = true;
    } else if (!reader.bytes_equal("GIF87a", 6)) {
        return fail(Error{ErrorCode::BadSignature, reader.pos(),
                          "magic must be GIF87a or GIF89a"});
    }
    reader.skip(6);

    // --- Logical Screen Descriptor (7 bytes) ------------------------------
    if (reader.remaining() < 7U) {
        return fail(Error{ErrorCode::TruncatedHeader, reader.pos(),
                          "truncated Logical Screen Descriptor"});
    }
    std::uint16_t screen_width = 0;
    std::uint16_t screen_height = 0;
    if (!reader.read_u16_le(screen_width) ||
        !reader.read_u16_le(screen_height)) {
        return fail(Error{ErrorCode::TruncatedHeader, reader.pos(),
                          "truncated canvas dimensions"});
    }
    std::uint8_t packed = 0;
    std::uint8_t background = 0;
    std::uint8_t aspect = 0;
    if (!reader.read_u8(packed) || !reader.read_u8(background) ||
        !reader.read_u8(aspect)) {
        return fail(Error{ErrorCode::TruncatedHeader, reader.pos(),
                          "truncated LSD fields"});
    }

    if (screen_width == 0U) {
        return fail(Error{ErrorCode::CanvasWidthZero, 6,
                          "logical screen width must be non-zero"});
    }
    if (screen_height == 0U) {
        return fail(Error{ErrorCode::CanvasHeightZero, 8,
                          "logical screen height must be non-zero"});
    }
    if (static_cast<std::uint64_t>(screen_width) >
        limits.max_canvas_width) {
        return fail(Error{ErrorCode::CanvasWidthQuota, reader.pos(),
                          "screen width exceeds quota"});
    }
    if (static_cast<std::uint64_t>(screen_height) >
        limits.max_canvas_height) {
        return fail(Error{ErrorCode::CanvasHeightQuota, reader.pos(),
                          "screen height exceeds quota"});
    }
    const std::uint64_t canvas_pixels =
        static_cast<std::uint64_t>(screen_width) *
        static_cast<std::uint64_t>(screen_height);
    if (canvas_pixels > limits.max_total_pixels) {
        return fail(Error{ErrorCode::CanvasPixelsQuota, reader.pos(),
                          "canvas pixel count exceeds quota"});
    }

    // --- Global Color Table -----------------------------------------------
    const bool has_gct = (packed & 0x80U) != 0U;
    std::uint32_t gct_entries = 0;
    if (has_gct) {
        gct_entries =
            static_cast<std::uint32_t>(palette_size_entries(packed));
        const std::size_t gct_bytes =
            static_cast<std::size_t>(gct_entries) * 3U;
        if (gct_bytes > reader.remaining()) {
            return fail(Error{ErrorCode::TruncatedGlobalColorTable,
                              reader.pos(),
                              "global color table extends past input"});
        }
        if (static_cast<std::uint32_t>(background) >= gct_entries) {
            return fail(Error{ErrorCode::BackgroundIndexOutOfRange,
                              reader.pos(),
                              "background color index outside GCT"});
        }
        reader.skip(gct_bytes);
    } else if (background != 0U) {
        return fail(Error{ErrorCode::BackgroundIndexOutOfRange,
                          reader.pos(),
                          "background index set without global color table"});
    }

    // --- Block sequence ----------------------------------------------------
    std::uint32_t image_count = 0;
    std::uint64_t running_total = 0;
    for (;;) {
        if (reader.eof()) {
            return fail(Error{ErrorCode::MissingTrailer, reader.pos(),
                              "stream ended without GIF Trailer 0x3B"});
        }
        const std::uint8_t introducer = reader.peek();
        if (introducer == 0x3BU) {
            reader.skip(1);
            break;  // unique trailer
        }
        reader.skip(1);
        if (introducer == 0x2CU) {
            Error img_err = parse_image(reader, screen_width, screen_height,
                                        running_total, limits);
            if (img_err.code != ErrorCode::Ok) return fail(img_err);
            ++image_count;
        } else if (introducer == 0x21U) {
            Error ext_err = parse_extension(reader);
            if (ext_err.code != ErrorCode::Ok) return fail(ext_err);
        } else {
            return fail(Error{ErrorCode::UnknownBlockIntroducer,
                              reader.pos() - 1,
                              "unknown block introducer"});
        }
    }

    // Hard reject anything after the Trailer: defeats GIFAR/polyglot tail
    // smuggling of JS, HTML or archive payloads.
    if (!reader.eof()) {
        return fail(Error{ErrorCode::TrailingBytesAfterTrailer,
                          reader.pos(),
                          "bytes present after GIF Trailer 0x3B"});
    }

    AuditResult summary;
    summary.ok = true;
    summary.code = ErrorCode::Ok;
    summary.offset = reader.pos();
    summary.detail = "ok";
    summary.screen_width = screen_width;
    summary.screen_height = screen_height;
    summary.has_global_color_table = has_gct;
    summary.global_color_table_entries = gct_entries;
    summary.is_gif89a = is_gif89a;
    summary.image_count = image_count;
    summary.total_decoded_pixels = running_total;
    return summary;
}

inline const char* error_code_name(ErrorCode code) noexcept {
    switch (code) {
        case ErrorCode::Ok: return "Ok";
        case ErrorCode::EmptyInput: return "EmptyInput";
        case ErrorCode::TruncatedHeader: return "TruncatedHeader";
        case ErrorCode::BadSignature: return "BadSignature";
        case ErrorCode::CanvasWidthZero: return "CanvasWidthZero";
        case ErrorCode::CanvasHeightZero: return "CanvasHeightZero";
        case ErrorCode::CanvasWidthQuota: return "CanvasWidthQuota";
        case ErrorCode::CanvasHeightQuota: return "CanvasHeightQuota";
        case ErrorCode::CanvasPixelsQuota: return "CanvasPixelsQuota";
        case ErrorCode::BackgroundIndexOutOfRange:
            return "BackgroundIndexOutOfRange";
        case ErrorCode::TruncatedGlobalColorTable:
            return "TruncatedGlobalColorTable";
        case ErrorCode::TruncatedBlock: return "TruncatedBlock";
        case ErrorCode::UnknownBlockIntroducer:
            return "UnknownBlockIntroducer";
        case ErrorCode::UnknownExtensionLabel:
            return "UnknownExtensionLabel";
        case ErrorCode::InvalidExtensionData: return "InvalidExtensionData";
        case ErrorCode::TruncatedImageDescriptor:
            return "TruncatedImageDescriptor";
        case ErrorCode::ImageReservedBitsSet: return "ImageReservedBitsSet";
        case ErrorCode::TruncatedLocalColorTable:
            return "TruncatedLocalColorTable";
        case ErrorCode::ImageFrameZeroDimension:
            return "ImageFrameZeroDimension";
        case ErrorCode::ImageFrameOutOfBounds:
            return "ImageFrameOutOfBounds";
        case ErrorCode::InvalidLzwMinCodeSize:
            return "InvalidLzwMinCodeSize";
        case ErrorCode::SubBlockOverrun: return "SubBlockOverrun";
        case ErrorCode::MissingBlockTerminator:
            return "MissingBlockTerminator";
        case ErrorCode::LzwTruncated: return "LzwTruncated";
        case ErrorCode::LzwFutureCode: return "LzwFutureCode";
        case ErrorCode::LzwOutputOverflow: return "LzwOutputOverflow";
        case ErrorCode::LzwTableFullNoClear: return "LzwTableFullNoClear";
        case ErrorCode::LzwOutputShort: return "LzwOutputShort";
        case ErrorCode::MissingTrailer: return "MissingTrailer";
        case ErrorCode::TrailingBytesAfterTrailer:
            return "TrailingBytesAfterTrailer";
    }
    return "Unknown";
}

}  // namespace gif_audit

#endif  // GIF_AUDIT_GIF_AUDIT_HPP
