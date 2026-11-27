#include "mediasafe/common.h"
#include "reader.h"

#include <cstring>
#include <string>

namespace mediasafe {

namespace {

constexpr std::size_t PNG_SIGNATURE_SIZE = 8;
constexpr uint32_t MAX_CHUNK_LEN = 0x7FFFFFFFu;

const uint8_t kPngSignature[PNG_SIGNATURE_SIZE] = {
    0x89u, 0x50u, 0x4Eu, 0x47u, 0x0Du, 0x0Au, 0x1Au, 0x0Au};

bool type_is(const uint8_t* p, char a, char b, char c, char d) {
    return p[0] == static_cast<uint8_t>(a) &&
           p[1] == static_cast<uint8_t>(b) &&
           p[2] == static_cast<uint8_t>(c) &&
           p[3] == static_cast<uint8_t>(d);
}

bool bit_depth_allowed(uint8_t color_type, uint8_t bit_depth) {
    switch (color_type) {
        case 0: // grayscale
            return bit_depth == 1 || bit_depth == 2 || bit_depth == 4 ||
                   bit_depth == 8 || bit_depth == 16;
        case 2: // RGB
        case 4: // grayscale + alpha
        case 6: // RGBA
            return bit_depth == 8 || bit_depth == 16;
        case 3: // indexed palette
            return bit_depth == 1 || bit_depth == 2 || bit_depth == 4 ||
                   bit_depth == 8;
        default:
            return false;
    }
}

bool channels_for(uint8_t color_type, uint32_t& channels) {
    switch (color_type) {
        case 0: channels = 1; return true;
        case 2: channels = 3; return true;
        case 3: channels = 1; return true;
        case 4: channels = 2; return true;
        case 6: channels = 4; return true;
        default: return false;
    }
}

} // namespace

AuditResult audit_png(const uint8_t* data, std::size_t size,
                      const AuditLimits& limits) {
    const MediaFormat fmt = MediaFormat::Png;
    if (data == nullptr) {
        return AuditResult::fail(fmt, "png: null input");
    }
    if (size > limits.max_file_bytes) {
        return AuditResult::fail(fmt, "png: file exceeds host size limit");
    }
    if (size < PNG_SIGNATURE_SIZE ||
        std::memcmp(data, kPngSignature, PNG_SIGNATURE_SIZE) != 0) {
        return AuditResult::fail(fmt, "png: bad or missing 8-byte signature");
    }

    const detail::ByteReader be(data, size, /*little_endian=*/false);

    bool seen_ihdr = false;
    bool seen_plte = false;
    bool seen_idat = false;
    bool idat_closed = false; // a non-IDAT chunk appeared after first IDAT
    bool seen_iend = false;
    bool palette_required = false;

    AuditResult result = AuditResult::pass(fmt);
    std::size_t offset = PNG_SIGNATURE_SIZE;

    while (offset < size) {
        // Chunk header: 4-byte length + 4-byte type.
        uint32_t length = 0;
        if (!be.u32(offset, length)) {
            return AuditResult::fail(fmt, "png: truncated chunk length");
        }
        const uint8_t* type_ptr = nullptr;
        if (!be.bytes(offset + 4, 4, type_ptr)) {
            return AuditResult::fail(fmt, "png: truncated chunk type");
        }
        // High bit of the length must be 0 (valid chunk length <= 2^31-1).
        if (length > MAX_CHUNK_LEN) {
            return AuditResult::fail(fmt,
                "png: chunk length exceeds 2^31-1 (high bit set)");
        }
        const std::size_t data_off = offset + 8;
        const uint8_t* chunk_data = nullptr;
        if (!be.bytes(data_off, length, chunk_data)) {
            return AuditResult::fail(fmt, "png: chunk data runs past EOF");
        }
        uint32_t stored_crc = 0;
        if (!be.u32(data_off + length, stored_crc)) {
            return AuditResult::fail(fmt, "png: truncated chunk CRC");
        }

        // Real IEEE CRC-32 over type + data (running state, finalize once).
        const uint32_t actual_crc =
            crc32_ieee(chunk_data, length,
                       crc32_ieee(type_ptr, 4)) ^ 0xFFFFFFFFu;
        if (actual_crc != stored_crc) {
            return AuditResult::fail(fmt,
                "png: CRC-32 mismatch (forged or corrupted chunk)");
        }

        const bool is_ihdr = type_is(type_ptr, 'I', 'H', 'D', 'R');
        const bool is_plte = type_is(type_ptr, 'P', 'L', 'T', 'E');
        const bool is_idat = type_is(type_ptr, 'I', 'D', 'A', 'T');
        const bool is_iend = type_is(type_ptr, 'I', 'E', 'N', 'D');

        // IHDR must be the first chunk and appear exactly once.
        if (!seen_ihdr) {
            if (!is_ihdr) {
                return AuditResult::fail(fmt,
                    "png: first chunk must be IHDR");
            }
        } else if (is_ihdr) {
            return AuditResult::fail(fmt, "png: duplicate IHDR chunk");
        }

        if (is_ihdr) {
            seen_ihdr = true;
            if (length != 13) {
                return AuditResult::fail(fmt,
                    "png: IHDR must be exactly 13 bytes");
            }
            uint32_t width = 0;
            uint32_t height = 0;
            if (!be.u32(data_off, width) ||
                !be.u32(data_off + 4, height)) {
                return AuditResult::fail(fmt, "png: truncated IHDR");
            }
            const uint8_t bit_depth = chunk_data[8];
            const uint8_t color_type = chunk_data[9];
            const uint8_t compression = chunk_data[10];
            const uint8_t filter_method = chunk_data[11];
            const uint8_t interlace = chunk_data[12];

            if (width == 0 || height == 0) {
                return AuditResult::fail(fmt,
                    "png: IHDR width/height must be non-zero");
            }
            if (width > MAX_CHUNK_LEN || height > MAX_CHUNK_LEN) {
                return AuditResult::fail(fmt,
                    "png: IHDR dimension exceeds 2^31-1");
            }
            if (!bit_depth_allowed(color_type, bit_depth)) {
                return AuditResult::fail(fmt,
                    "png: illegal bit depth / color type combination");
            }
            if (compression != 0 || filter_method != 0) {
                return AuditResult::fail(fmt,
                    "png: unsupported compression or filter method");
            }
            if (interlace > 1) {
                return AuditResult::fail(fmt,
                    "png: unsupported interlace method");
            }

            uint32_t channels = 0;
            if (!channels_for(color_type, channels)) {
                return AuditResult::fail(fmt, "png: invalid color type");
            }
            palette_required = (color_type == 3);

            // Checked 64-bit decoded-pixel budget.
            uint64_t bits_per_pixel = 0;
            if (mul_overflow_u64(static_cast<uint64_t>(width),
                                  static_cast<uint64_t>(channels),
                                  bits_per_pixel) ||
                mul_overflow_u64(bits_per_pixel,
                                  static_cast<uint64_t>(bit_depth),
                                  bits_per_pixel)) {
                return AuditResult::fail(fmt,
                    "png: pixel dimension multiplication overflow");
            }
            const uint64_t row_bits = bits_per_pixel;
            uint64_t row_bytes = row_bits / 8u;
            if ((row_bits % 8u) != 0u) {
                uint64_t plus_one = 0;
                if (add_overflow_u64(row_bytes, 1, plus_one)) {
                    return AuditResult::fail(fmt,
                        "png: row size overflow");
                }
                row_bytes = plus_one;
            }
            uint64_t with_filter = 0;
            uint64_t total = 0;
            if (add_overflow_u64(row_bytes, 1, with_filter) ||
                mul_overflow_u64(with_filter,
                                  static_cast<uint64_t>(height), total)) {
                return AuditResult::fail(fmt,
                    "png: decoded pixel buffer size overflow");
            }
            if (total > limits.max_pixel_bytes) {
                return AuditResult::fail(fmt,
                    "png: decoded pixel buffer exceeds host limit");
            }
            result.width = width;
            result.height = height;
            result.pixel_bytes = total;
        }

        if (is_plte) {
            if (seen_plte) {
                return AuditResult::fail(fmt, "png: duplicate PLTE chunk");
            }
            seen_plte = true;
            if ((length % 3u) != 0u) {
                return AuditResult::fail(fmt,
                    "png: PLTE length must be a multiple of 3");
            }
            if ((length / 3u) > 256u) {
                return AuditResult::fail(fmt,
                    "png: PLTE contains more than 256 entries");
            }
            if (seen_idat) {
                return AuditResult::fail(fmt,
                    "png: PLTE must precede IDAT");
            }
        }

        if (is_idat) {
            if (length == 0) {
                return AuditResult::fail(fmt, "png: truncated/empty IDAT");
            }
            if (idat_closed) {
                return AuditResult::fail(fmt,
                    "png: IDAT chunks must be consecutive");
            }
            seen_idat = true;
        } else if (seen_idat) {
            idat_closed = true;
        }

        if (is_iend) {
            if (length != 0) {
                return AuditResult::fail(fmt, "png: IEND must be empty");
            }
            if (seen_iend) {
                return AuditResult::fail(fmt, "png: duplicate IEND chunk");
            }
            seen_iend = true;
            const std::size_t end = data_off + length + 4;
            if (end != size) {
                return AuditResult::fail(fmt,
                    "png: trailing data hidden after IEND");
            }
        }

        // Advance to the next chunk (length + type + data + CRC).
        uint64_t advance = 0;
        if (add_overflow_u64(static_cast<uint64_t>(length), 12u, advance) ||
            advance > (size - offset)) {
            return AuditResult::fail(fmt, "png: chunk layout overflow");
        }
        offset += static_cast<std::size_t>(advance);
    }

    if (!seen_ihdr) {
        return AuditResult::fail(fmt, "png: missing IHDR");
    }
    if (!seen_iend) {
        return AuditResult::fail(fmt, "png: missing IEND terminator");
    }
    if (!seen_idat) {
        return AuditResult::fail(fmt, "png: missing IDAT image data");
    }
    if (palette_required && !seen_plte) {
        return AuditResult::fail(fmt,
            "png: indexed color image requires PLTE");
    }
    return result;
}

} // namespace mediasafe
