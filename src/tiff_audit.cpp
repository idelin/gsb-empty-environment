#include "mediasafe/common.h"
#include "reader.h"

#include <string>
#include <unordered_set>
#include <vector>

namespace mediasafe {

namespace {

namespace tag {
constexpr uint16_t ImageWidth = 256;
constexpr uint16_t ImageLength = 257;
constexpr uint16_t BitsPerSample = 258;
constexpr uint16_t StripOffsets = 273;
constexpr uint16_t SamplesPerPixel = 277;
constexpr uint16_t StripByteCounts = 279;
constexpr uint16_t SubIFDs = 330;
constexpr uint16_t ExifIFD = 34665;
constexpr uint16_t GpsIFD = 34853;
} // namespace tag

// TIFF / BigTIFF baseline field type sizes. 0 means unsupported/unknown.
uint32_t tiff_type_size(uint16_t type, bool big_tiff) {
    switch (type) {
        case 1: return 1;  // BYTE
        case 2: return 1;  // ASCII
        case 3: return 2;  // SHORT
        case 4: return 4;  // LONG
        case 5: return 8;  // RATIONAL
        case 6: return 1;  // SBYTE
        case 7: return 1;  // UNDEFINED
        case 8: return 2;  // SSHORT
        case 9: return 4;  // SLONG
        case 10: return 8; // SRATIONAL
        case 11: return 4; // FLOAT
        case 12: return 8; // DOUBLE
        case 13: // IFD (BigTIFF only)
        case 14: // LONG8 (BigTIFF only)
        case 15: // SLONG8 (BigTIFF only)
        case 16: // IFD8 (BigTIFF only)
        case 17:
        case 18:
            return big_tiff ? 8u : 0u;
        default:
            return 0;
    }
}

struct TiffContext {
    const detail::ByteReader& r;
    const AuditLimits& limits;
    bool big_tiff;
    std::size_t header_size;
    std::unordered_set<uint64_t> visited;
    AuditResult result;

    TiffContext(const detail::ByteReader& reader, const AuditLimits& lim,
                bool bt, std::size_t hsize)
        : r(reader), limits(lim), big_tiff(bt), header_size(hsize),
          result(AuditResult::pass(MediaFormat::Tiff)) {}
};

bool read_scalar(const TiffContext& ctx, uint16_t type, std::size_t at,
                 uint64_t& value) {
    const detail::ByteReader& r = ctx.r;
    switch (type) {
        case 1: case 2: case 6: case 7: {
            uint8_t v = 0;
            if (!r.u8(at, v)) return false;
            value = v;
            return true;
        }
        case 3: case 8: {
            uint16_t v = 0;
            if (!r.u16(at, v)) return false;
            value = v;
            return true;
        }
        case 4: case 9: case 11: {
            uint32_t v = 0;
            if (!r.u32(at, v)) return false;
            value = v;
            return true;
        }
        case 5: case 10: case 12:
        case 13: case 14: case 15: case 16: case 17: case 18: {
            uint64_t v = 0;
            if (!r.u64(at, v)) return false;
            value = v;
            return true;
        }
        default:
            return false;
    }
}

// Read the complete value array of a tag, following its out-of-line pointer
// when the payload does not fit in the inline value slot.
bool read_value_array(TiffContext& ctx, uint16_t type, uint64_t count,
                      std::size_t inline_at, std::size_t inline_size,
                      std::vector<uint64_t>& values) {
    const detail::ByteReader& r = ctx.r;
    const uint32_t elem = tiff_type_size(type, ctx.big_tiff);
    if (elem == 0) return false;

    uint64_t byte_count = 0;
    if (mul_overflow_u64(count, static_cast<uint64_t>(elem), byte_count)) {
        ctx.result = AuditResult::fail(MediaFormat::Tiff,
            "tiff: count * type_size overflow");
        return false;
    }

    std::size_t base = inline_at;
    if (byte_count > inline_size) {
        uint64_t pointer = 0;
        if (ctx.big_tiff) {
            if (!r.u64(inline_at, pointer)) return false;
        } else {
            uint32_t p32 = 0;
            if (!r.u32(inline_at, p32)) return false;
            pointer = p32;
        }
        if (pointer >= r.size() ||
            byte_count > (r.size() - static_cast<std::size_t>(pointer))) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                "tiff: tag value offset outside file");
            return false;
        }
        base = static_cast<std::size_t>(pointer);
    }
    values.clear();
    values.reserve(static_cast<std::size_t>(count));
    for (uint64_t i = 0; i < count; ++i) {
        const std::size_t at =
            base + static_cast<std::size_t>(i) *
                       static_cast<std::size_t>(elem);
        uint64_t v = 0;
        if (!read_scalar(ctx, type, at, v)) return false;
        values.push_back(v);
    }
    return true;
}

bool audit_ifd(TiffContext& ctx, uint64_t ifd_offset, uint32_t depth);

bool validate_image_data(TiffContext& ctx,
                         const std::vector<uint64_t>& strip_offsets,
                         const std::vector<uint64_t>& strip_counts,
                         uint64_t width, uint64_t height,
                         uint64_t samples, uint64_t bits_per_sample) {
    const detail::ByteReader& r = ctx.r;
    if (strip_offsets.size() != strip_counts.size()) {
        ctx.result = AuditResult::fail(MediaFormat::Tiff,
            "tiff: StripOffsets / StripByteCounts count mismatch");
        return false;
    }
    for (std::size_t i = 0; i < strip_offsets.size(); ++i) {
        const uint64_t off = strip_offsets[i];
        const uint64_t len = strip_counts[i];
        if (off < ctx.header_size ||
            len > (r.size() - static_cast<std::size_t>(off))) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                "tiff: strip range extends beyond file");
            return false;
        }
        for (std::size_t j = 0; j < i; ++j) {
            const uint64_t o0 = strip_offsets[j];
            const uint64_t l0 = strip_counts[j];
            const bool overlap =
                (off >= o0 && off < (o0 + l0)) ||
                (o0 >= off && o0 < (off + len));
            if (overlap) {
                ctx.result = AuditResult::fail(MediaFormat::Tiff,
                    "tiff: overlapping strip byte ranges");
                return false;
            }
        }
    }

    // Checked uncompressed pixel budget:
    // ceil(width * samples * bits / 8) * height.
    uint64_t bits_row = 0;
    uint64_t total = 0;
    if (mul_overflow_u64(width, samples, bits_row) ||
        mul_overflow_u64(bits_row, bits_per_sample, bits_row) ||
        mul_overflow_u64(bits_row, height, total)) {
        ctx.result = AuditResult::fail(MediaFormat::Tiff,
            "tiff: pixel dimension multiplication overflow");
        return false;
    }
    uint64_t bytes_row = total / 8u;
    if ((total % 8u) != 0u && add_overflow_u64(bytes_row, 1, bytes_row)) {
        ctx.result = AuditResult::fail(MediaFormat::Tiff,
            "tiff: row size overflow");
        return false;
    }
    if (mul_overflow_u64(bytes_row, height, total)) {
        ctx.result = AuditResult::fail(MediaFormat::Tiff,
            "tiff: pixel buffer size overflow");
        return false;
    }
    if (total > ctx.limits.max_pixel_bytes) {
        ctx.result = AuditResult::fail(MediaFormat::Tiff,
            "tiff: decoded pixel buffer exceeds host limit");
        return false;
    }
    if (total > ctx.result.pixel_bytes) {
        ctx.result.pixel_bytes = total;
        ctx.result.width = width;
        ctx.result.height = height;
    }
    return true;
}

bool audit_ifd(TiffContext& ctx, uint64_t ifd_offset, uint32_t depth) {
    const detail::ByteReader& r = ctx.r;

    if (depth > ctx.limits.max_ifd_depth) {
        ctx.result = AuditResult::fail(MediaFormat::Tiff,
            "tiff: IFD nesting depth exceeds limit");
        return false;
    }
    if (ifd_offset < ctx.header_size || ifd_offset >= r.size()) {
        ctx.result = AuditResult::fail(MediaFormat::Tiff,
            "tiff: IFD offset outside file");
        return false;
    }
    if (ctx.visited.find(ifd_offset) != ctx.visited.end()) {
        ctx.result = AuditResult::fail(MediaFormat::Tiff,
            "tiff: IFD reference cycle detected");
        return false;
    }
    ctx.visited.insert(ifd_offset);
    ctx.result.ifd_count += 1;
    if (depth > ctx.result.max_depth) {
        ctx.result.max_depth = depth;
    }

    const std::size_t base = static_cast<std::size_t>(ifd_offset);
    uint64_t entry_count = 0;
    const std::size_t entry_size = ctx.big_tiff ? 20u : 12u;
    const std::size_t count_field_size = ctx.big_tiff ? 8u : 2u;

    if (ctx.big_tiff) {
        uint64_t count64 = 0;
        if (!r.u64(base, count64)) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                "tiff: truncated BigTIFF entry count");
            return false;
        }
        entry_count = count64;
    } else {
        uint16_t count16 = 0;
        if (!r.u16(base, count16)) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                "tiff: truncated IFD entry count");
            return false;
        }
        entry_count = count16;
    }

    const uint64_t pointer_size = ctx.big_tiff ? 8u : 4u;
    uint64_t block_bytes = 0;
    if (mul_overflow_u64(entry_count,
                         static_cast<uint64_t>(entry_size), block_bytes) ||
        add_overflow_u64(block_bytes, pointer_size, block_bytes) ||
        block_bytes > (r.size() - base)) {
        ctx.result = AuditResult::fail(MediaFormat::Tiff,
            "tiff: IFD entries run past end of file");
        return false;
    }
    // Derived only after the overflow-checked block validation above.
    const std::size_t next_off_field =
        base + count_field_size +
        static_cast<std::size_t>(entry_count) * entry_size;

    const std::size_t inline_value_size = ctx.big_tiff ? 8u : 4u;
    std::vector<uint64_t> strip_offsets;
    std::vector<uint64_t> strip_counts;
    std::vector<uint64_t> sub_offsets;
    std::vector<uint64_t> exif_offsets;
    bool have_strip_offsets = false;
    bool have_strip_counts = false;
    uint64_t width = 0;
    uint64_t height = 0;
    uint64_t samples = 1;
    uint64_t bits_first = 1;
    bool have_width = false;
    bool have_height = false;

    auto or_malformed = [&](const char* msg) {
        // read_value_array may already have recorded a precise root cause.
        if (ctx.result.ok) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff, msg);
        }
        return false;
    };

    for (uint64_t e = 0; e < entry_count; ++e) {
        const std::size_t entry_at =
            (ctx.big_tiff ? base + 8u : base + 2u) +
            static_cast<std::size_t>(e) * entry_size;

        uint16_t t = 0;
        uint16_t field_type = 0;
        uint64_t count = 0;
        if (!r.u16(entry_at, t) || !r.u16(entry_at + 2u, field_type)) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                "tiff: truncated IFD entry header");
            return false;
        }
        // Classic entry: tag(2) type(2) count(4) value@8.
        // BigTIFF entry: tag(2) type(2) count(8) value@12.
        const std::size_t value_at =
            entry_at + (ctx.big_tiff ? 12u : 8u);
        if (ctx.big_tiff) {
            uint64_t c64 = 0;
            if (!r.u64(entry_at + 4u, c64)) {
                ctx.result = AuditResult::fail(MediaFormat::Tiff,
                    "tiff: truncated BigTIFF entry count");
                return false;
            }
            count = c64;
        } else {
            uint32_t c32 = 0;
            if (!r.u32(entry_at + 4u, c32)) {
                ctx.result = AuditResult::fail(MediaFormat::Tiff,
                    "tiff: truncated entry count");
                return false;
            }
            count = c32;
        }

        const uint32_t elem = tiff_type_size(field_type, ctx.big_tiff);
        if (elem == 0) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                "tiff: unknown or unsupported field type");
            return false;
        }
        uint64_t total_bytes = 0;
        if (mul_overflow_u64(count, static_cast<uint64_t>(elem),
                             total_bytes)) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                "tiff: count * type_size overflow");
            return false;
        }

        std::vector<uint64_t> values;
        switch (t) {
            case tag::ImageWidth:
                if (!read_value_array(ctx, field_type, count, value_at,
                                      inline_value_size, values) ||
                    values.empty()) {
                    return or_malformed("tiff: malformed ImageWidth");
                }
                width = values.front();
                have_width = true;
                break;
            case tag::ImageLength:
                if (!read_value_array(ctx, field_type, count, value_at,
                                      inline_value_size, values) ||
                    values.empty()) {
                    return or_malformed("tiff: malformed ImageLength");
                }
                height = values.front();
                have_height = true;
                break;
            case tag::SamplesPerPixel:
                if (!read_value_array(ctx, field_type, count, value_at,
                                      inline_value_size, values) ||
                    values.empty()) {
                    return or_malformed("tiff: malformed SamplesPerPixel");
                }
                samples = values.front();
                break;
            case tag::BitsPerSample:
                if (!read_value_array(ctx, field_type, count, value_at,
                                      inline_value_size, values) ||
                    values.empty()) {
                    return or_malformed("tiff: malformed BitsPerSample");
                }
                bits_first = values.front();
                break;
            case tag::StripOffsets:
                if (!read_value_array(ctx, field_type, count, value_at,
                                      inline_value_size, strip_offsets)) {
                    return or_malformed("tiff: malformed StripOffsets");
                }
                have_strip_offsets = true;
                break;
            case tag::StripByteCounts:
                if (!read_value_array(ctx, field_type, count, value_at,
                                      inline_value_size, strip_counts)) {
                    return or_malformed("tiff: malformed StripByteCounts");
                }
                have_strip_counts = true;
                break;
            case tag::SubIFDs:
                if (!read_value_array(ctx, field_type, count, value_at,
                                      inline_value_size, sub_offsets)) {
                    return or_malformed("tiff: malformed SubIFD pointer array");
                }
                break;
            case tag::ExifIFD:
            case tag::GpsIFD:
                if (!read_value_array(ctx, field_type, count, value_at,
                                      inline_value_size, exif_offsets)) {
                    return or_malformed("tiff: malformed Exif/GPS IFD pointer");
                }
                break;
            default:
                // Generic structural check for every other tag: when the
                // payload does not fit inline, its pointer/range must be
                // fully contained in the file.
                if (total_bytes > inline_value_size) {
                    uint64_t pointer = 0;
                    if (ctx.big_tiff) {
                        if (!r.u64(value_at, pointer)) {
                            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                                "tiff: truncated value offset");
                            return false;
                        }
                    } else {
                        uint32_t p32 = 0;
                        if (!r.u32(value_at, p32)) {
                            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                                "tiff: truncated value offset");
                            return false;
                        }
                        pointer = p32;
                    }
                    if (pointer >= r.size() ||
                        total_bytes >
                            (r.size() -
                             static_cast<std::size_t>(pointer))) {
                        ctx.result = AuditResult::fail(MediaFormat::Tiff,
                            "tiff: tag value offset outside file");
                        return false;
                    }
                }
                break;
        }
    }

    if (have_strip_offsets || have_strip_counts) {
        if (!have_strip_offsets || !have_strip_counts) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                "tiff: StripOffsets and StripByteCounts must pair");
            return false;
        }
        if (have_width && have_height) {
            if (width == 0 || height == 0 || samples == 0 ||
                bits_first == 0) {
                ctx.result = AuditResult::fail(MediaFormat::Tiff,
                    "tiff: non-positive image dimension");
                return false;
            }
            if (!validate_image_data(ctx, strip_offsets, strip_counts,
                                     width, height, samples, bits_first)) {
                return false;
            }
        }
    }

    // Recurse into nested SubIFD / ExifIFD / GPS IFD with a hard depth cap.
    for (uint64_t child : sub_offsets) {
        if (!audit_ifd(ctx, child, depth + 1)) return false;
    }
    for (uint64_t child : exif_offsets) {
        if (!audit_ifd(ctx, child, depth + 1)) return false;
    }

    // Follow the main linked-list of IFDs (depth stays constant; the visited
    // set breaks any chain cycle).
    uint64_t next_ifd = 0;
    if (ctx.big_tiff) {
        if (!r.u64(next_off_field, next_ifd)) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                "tiff: truncated next-IFD pointer");
            return false;
        }
    } else {
        uint32_t n32 = 0;
        if (!r.u32(next_off_field, n32)) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                "tiff: truncated next-IFD pointer");
            return false;
        }
        next_ifd = n32;
    }
    if (next_ifd != 0) {
        if (next_ifd < ctx.header_size || next_ifd >= r.size()) {
            ctx.result = AuditResult::fail(MediaFormat::Tiff,
                "tiff: next-IFD offset outside file");
            return false;
        }
        if (!audit_ifd(ctx, next_ifd, depth)) return false;
    }
    return true;
}

} // namespace

AuditResult audit_tiff(const uint8_t* data, std::size_t size,
                       const AuditLimits& limits) {
    const MediaFormat fmt = MediaFormat::Tiff;
    if (data == nullptr) {
        return AuditResult::fail(fmt, "tiff: null input");
    }
    if (size > limits.max_file_bytes) {
        return AuditResult::fail(fmt, "tiff: file exceeds host size limit");
    }
    if (size < 8) {
        return AuditResult::fail(fmt, "tiff: truncated header");
    }
    const bool le_magic = data[0] == 0x49u && data[1] == 0x49u; // "II"
    const bool be_magic = data[0] == 0x4Du && data[1] == 0x4Du; // "MM"
    if (!le_magic && !be_magic) {
        return AuditResult::fail(fmt, "tiff: bad byte-order magic");
    }
    const detail::ByteReader r(data, size, le_magic);

    uint16_t version = 0;
    if (!r.u16(2, version)) {
        return AuditResult::fail(fmt, "tiff: truncated version field");
    }
    bool big_tiff = false;
    std::size_t header_size = 8;
    uint64_t first_ifd = 0;

    if (version == 42) {
        big_tiff = false;
        header_size = 8;
        uint32_t off32 = 0;
        if (!r.u32(4, off32)) {
            return AuditResult::fail(fmt, "tiff: truncated IFD0 offset");
        }
        first_ifd = off32;
    } else if (version == 43) {
        big_tiff = true;
        header_size = 16;
        if (size < 16) {
            return AuditResult::fail(fmt, "tiff: truncated BigTIFF header");
        }
        uint16_t offset_size = 0;
        uint16_t reserved = 0;
        if (!r.u16(4, offset_size) || !r.u16(6, reserved)) {
            return AuditResult::fail(fmt, "tiff: truncated BigTIFF fields");
        }
        if (offset_size != 8 || reserved != 0) {
            return AuditResult::fail(fmt, "tiff: malformed BigTIFF header");
        }
        if (!r.u64(8, first_ifd)) {
            return AuditResult::fail(fmt, "tiff: truncated BigTIFF IFD0");
        }
    } else {
        return AuditResult::fail(fmt, "tiff: unknown TIFF version");
    }

    if (first_ifd == 0) {
        return AuditResult::fail(fmt, "tiff: missing first IFD");
    }
    if (first_ifd < header_size || first_ifd >= size) {
        return AuditResult::fail(fmt, "tiff: first IFD offset outside file");
    }

    TiffContext ctx(r, limits, big_tiff, header_size);
    if (!audit_ifd(ctx, first_ifd, 0)) {
        return ctx.result;
    }
    return ctx.result;
}

} // namespace mediasafe
