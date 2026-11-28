#ifndef WEBPAUDIT_WEBP_AUDIT_HPP_INCLUDED
#define WEBPAUDIT_WEBP_AUDIT_HPP_INCLUDED

#include <cstddef>
#include <cstdint>
#include <string_view>

namespace webp_audit {

// Reasons why a byte stream was rejected. kOk means the audit passed.
enum class AuditError {
    kOk = 0,
    kNullInput,
    kTooSmall,
    kBadRiffMagic,
    kBadRiffSize,
    kBadWebpMagic,
    kTruncatedChunkHeader,
    kChunkPayloadOutOfBounds,
    kMissingPaddingByte,
    kNonZeroPadding,
    kInvalidFirstChunk,
    kInvalidVp8xSize,
    kReservedVp8xByteSet,
    kReservedVp8xFlagSet,
    kFlagChunkMismatch,
    kUnexpectedChunk,
    kInvalidChunkOrder,
    kUnknownChunk,
    kDuplicateChunk,
    kMultipleImageFrames,
    kTruncatedVp8Header,
    kNotKeyFrame,
    kBadVp8StartCode,
    kTruncatedVp8lHeader,
    kBadVp8lSignature,
    kTruncatedAnmfHeader,
    kInvalidAnmfSize,
    kReservedAnmfFlagSet,
    kZeroDimension,
    kDimensionExceedsLimit,
    kPixelCountExceedsQuota,
    kFrameOutsideCanvas,
    kFrameDimensionMismatch,
    kInvalidAnimSize,
    kNoImageFrame,
    kTrailingData
};

struct AuditOptions {
    // Maximum width/height of a canvas or frame (WebP hard limit is 16384).
    std::uint32_t max_dimension = 16384;
    // Maximum number of pixels (width * height) accepted for canvas/frame.
    std::uint64_t max_pixels = (std::uint64_t{1} << 40);
};

struct ImageInfo {
    std::uint32_t width = 0;        // Simple: frame width. Extended: VP8X canvas width.
    std::uint32_t height = 0;       // Simple: frame height. Extended: VP8X canvas height.
    bool has_alpha = false;
    bool has_animation = false;
    bool has_icc = false;
    bool has_exif = false;
    bool has_xmp = false;
    std::uint32_t frame_count = 0; // ANMF frames (animated) or 1 for a static image.
};

struct AuditResult {
    AuditError error = AuditError::kOk;
    // Byte offset of the rejected structure (chunk header, padding byte, ...).
    std::size_t offset = 0;
    ImageInfo info{};

    bool ok() const noexcept { return error == AuditError::kOk; }
    explicit operator bool() const noexcept { return ok(); }
};

const char* to_string(AuditError error) noexcept;

// Audits a raw byte sequence beginning at offset 0. Never throws.
AuditResult audit(const std::uint8_t* data, std::size_t size,
                  const AuditOptions& options = {}) noexcept;

inline AuditResult audit(std::string_view bytes,
                         const AuditOptions& options = {}) noexcept {
    const auto* ptr = reinterpret_cast<const std::uint8_t*>(bytes.data());
    return audit(ptr, bytes.size(), options);
}

namespace detail {

struct FrameGeometry {
    std::uint32_t width = 0;
    std::uint32_t height = 0;
    bool alpha_hint = false;
};

struct Cursor {
    const std::uint8_t* base;
    std::size_t size;
    std::size_t pos = 0;

    bool have(std::size_t count) const noexcept {
        return count <= size - pos; // pos <= size is an invariant
    }
};

inline std::uint16_t le16(const std::uint8_t* p) noexcept {
    return static_cast<std::uint16_t>(
        static_cast<std::uint16_t>(p[0]) |
        (static_cast<std::uint16_t>(p[1]) << 8));
}

inline std::uint32_t le24(const std::uint8_t* p) noexcept {
    return static_cast<std::uint32_t>(p[0]) |
           (static_cast<std::uint32_t>(p[1]) << 8) |
           (static_cast<std::uint32_t>(p[2]) << 16);
}

inline std::uint32_t le32(const std::uint8_t* p) noexcept {
    return static_cast<std::uint32_t>(p[0]) |
           (static_cast<std::uint32_t>(p[1]) << 8) |
           (static_cast<std::uint32_t>(p[2]) << 16) |
           (static_cast<std::uint32_t>(p[3]) << 24);
}

inline bool tag_eq(const std::uint8_t* p, const char* fourcc) noexcept {
    return p[0] == static_cast<std::uint8_t>(fourcc[0]) &&
           p[1] == static_cast<std::uint8_t>(fourcc[1]) &&
           p[2] == static_cast<std::uint8_t>(fourcc[2]) &&
           p[3] == static_cast<std::uint8_t>(fourcc[3]);
}

enum class ChunkKind {
    kVp8x, kIccp, kAnim, kAlph, kVp8, kVp8l, kAnmf, kExif, kXmp, kUnknown
};

inline ChunkKind classify(const std::uint8_t* tag) noexcept {
    if (tag_eq(tag, "VP8X")) return ChunkKind::kVp8x;
    if (tag_eq(tag, "ICCP")) return ChunkKind::kIccp;
    if (tag_eq(tag, "ANIM")) return ChunkKind::kAnim;
    if (tag_eq(tag, "ALPH")) return ChunkKind::kAlph;
    if (tag_eq(tag, "VP8 ")) return ChunkKind::kVp8;
    if (tag_eq(tag, "VP8L")) return ChunkKind::kVp8l;
    if (tag_eq(tag, "ANMF")) return ChunkKind::kAnmf;
    if (tag_eq(tag, "EXIF")) return ChunkKind::kExif;
    if (tag_eq(tag, "XMP ")) return ChunkKind::kXmp;
    return ChunkKind::kUnknown;
}

enum class Phase { kBeforeBitstream, kBitstream, kMetadata };

// Parses a VP8 (lossy) bitstream chunk payload.
inline AuditError parse_vp8(const std::uint8_t* p, std::size_t n,
                            FrameGeometry& out) noexcept {
    // frame_tag(3) + start_code(3) + width(2) + height(2)
    if (n < 10) return AuditError::kTruncatedVp8Header;
    if ((p[0] & 0x01u) != 0) return AuditError::kNotKeyFrame;
    if (p[3] != 0x9du || p[4] != 0x01u || p[5] != 0x2au) {
        return AuditError::kBadVp8StartCode;
    }
    out.width = static_cast<std::uint32_t>(le16(p + 6) & 0x3fffu);
    out.height = static_cast<std::uint32_t>(le16(p + 8) & 0x3fffu);
    out.alpha_hint = false;
    return AuditError::kOk;
}

// Parses a VP8L (lossless) bitstream chunk payload.
inline AuditError parse_vp8l(const std::uint8_t* p, std::size_t n,
                             FrameGeometry& out) noexcept {
    if (n < 5) return AuditError::kTruncatedVp8lHeader;
    if (p[0] != 0x2fu) return AuditError::kBadVp8lSignature;
    const std::uint32_t b1 = p[1];
    const std::uint32_t b2 = p[2];
    const std::uint32_t b3 = p[3];
    const std::uint32_t b4 = p[4];
    out.width = 1u + (((b2 & 0x3fu) << 8) | b1);
    out.height = 1u + ((((b4 & 0x0fu) << 8) | b3 << 2) | ((b2 >> 6) & 0x03u));
    out.alpha_hint = (b4 & 0x20u) != 0;
    return AuditError::kOk;
}

// Validates width/height against the host dimension and pixel quotas.
inline AuditError check_geometry(std::uint32_t width, std::uint32_t height,
                                 const AuditOptions& opt) noexcept {
    if (width == 0 || height == 0) return AuditError::kZeroDimension;
    if (width > opt.max_dimension || height > opt.max_dimension) {
        return AuditError::kDimensionExceedsLimit;
    }
    const std::uint64_t pixels =
        static_cast<std::uint64_t>(width) * static_cast<std::uint64_t>(height);
    if (pixels > opt.max_pixels) return AuditError::kPixelCountExceedsQuota;
    return AuditError::kOk;
}

// Parses an ANMF chunk: 16-byte fixed header followed by ALPH/VP8/VP8L.
inline AuditError parse_anmf(const std::uint8_t* p, std::size_t n,
                             std::uint32_t canvas_width,
                             std::uint32_t canvas_height,
                             const AuditOptions& opt,
                             FrameGeometry& frame_out,
                             bool& alpha_seen) noexcept {
    if (n < 16) return AuditError::kTruncatedAnmfHeader;
    const std::uint32_t x = le24(p + 0);
    const std::uint32_t y = le24(p + 3);
    const std::uint32_t width = 1u + le24(p + 6);
    const std::uint32_t height = 1u + le24(p + 9);
    const std::uint8_t flags = p[12];
    if ((flags & 0xfeu) != 0) return AuditError::kReservedAnmfFlagSet;

    AuditError err = check_geometry(width, height, opt);
    if (err != AuditError::kOk) return err;

    // Overflow-safe canvas containment: x + width <= canvasWidth, etc.
    if (x > canvas_width || width > canvas_width - x ||
        y > canvas_height || height > canvas_height - y) {
        return AuditError::kFrameOutsideCanvas;
    }

    std::size_t sub = 16;
    bool inner_image = false;
    while (sub < n) {
        if (n - sub < 8) return AuditError::kInvalidAnmfSize;
        const std::uint8_t* tag = p + sub;
        const std::uint32_t sub_size = le32(p + sub + 4);
        const std::size_t sub_head = sub + 8;
        if (sub_size > n - sub_head) return AuditError::kInvalidAnmfSize;
        const std::uint8_t* sub_payload = p + sub_head;
        const std::size_t sub_total = 8u + static_cast<std::size_t>(sub_size);

        const ChunkKind kind = classify(tag);
        if (kind == ChunkKind::kAlph) {
            if (alpha_seen) return AuditError::kDuplicateChunk;
            alpha_seen = true;
        } else if (kind == ChunkKind::kVp8 || kind == ChunkKind::kVp8l) {
            if (inner_image) return AuditError::kMultipleImageFrames;
            inner_image = true;
            FrameGeometry geo{};
            err = (kind == ChunkKind::kVp8)
                      ? parse_vp8(sub_payload, sub_size, geo)
                      : parse_vp8l(sub_payload, sub_size, geo);
            if (err != AuditError::kOk) return err;
            err = check_geometry(geo.width, geo.height, opt);
            if (err != AuditError::kOk) return err;
            if (geo.width != width || geo.height != height) {
                return AuditError::kFrameDimensionMismatch;
            }
            frame_out = geo;
            if (kind == ChunkKind::kVp8l && geo.alpha_hint) alpha_seen = true;
        } else {
            return AuditError::kUnexpectedChunk;
        }

        sub += sub_total;
        if ((sub_size & 1u) != 0) {
            if (sub >= n) return AuditError::kMissingPaddingByte;
            if (p[sub] != 0x00u) return AuditError::kNonZeroPadding;
            ++sub;
        }
    }

    if (!inner_image) return AuditError::kNoImageFrame;
    return AuditError::kOk;
}

} // namespace detail

inline AuditResult audit(const std::uint8_t* data, std::size_t size,
                         const AuditOptions& options) noexcept {
    AuditResult result{};
    auto fail = [&result](AuditError e, std::size_t off) noexcept -> AuditResult {
        result.error = e;
        result.offset = off;
        return result;
    };

    if (data == nullptr) {
        return fail(AuditError::kNullInput, 0);
    }
    if (size < 12) {
        return fail(AuditError::kTooSmall, size);
    }
    if (!detail::tag_eq(data, "RIFF")) {
        return fail(AuditError::kBadRiffMagic, 0);
    }

    const std::uint64_t declared = detail::le32(data + 4);
    // 64-bit arithmetic cannot wrap for any 32-bit declared size.
    if (declared + 8u != static_cast<std::uint64_t>(size)) {
        return fail(AuditError::kBadRiffSize, 4);
    }
    if (!detail::tag_eq(data + 8, "WEBP")) {
        return fail(AuditError::kBadWebpMagic, 8);
    }

    detail::Cursor cur{data, size, 12};

    bool extended = false;
    bool flag_icc = false;
    bool flag_alpha = false;
    bool flag_exif = false;
    bool flag_xmp = false;
    bool flag_anim = false;

    std::uint32_t canvas_width = 0;
    std::uint32_t canvas_height = 0;

    detail::Phase phase = detail::Phase::kBeforeBitstream;
    bool iccp_seen = false;
    bool anim_seen = false;
    bool top_alph_seen = false;
    bool exif_seen = false;
    bool xmp_seen = false;
    bool image_seen = false;
    bool anmf_seen = false;
    bool alpha_any = false;
    std::uint32_t frame_count = 0;
    detail::FrameGeometry first_geo{};

    while (cur.pos < cur.size) {
        const std::size_t header_pos = cur.pos;
        if (!cur.have(8)) {
            return fail(AuditError::kTruncatedChunkHeader, header_pos);
        }
        const std::uint8_t* tag = cur.base + cur.pos;
        const std::uint32_t chunk_size = detail::le32(cur.base + cur.pos + 4);
        const std::size_t payload_pos = cur.pos + 8;
        if (chunk_size > cur.size - payload_pos) {
            return fail(AuditError::kChunkPayloadOutOfBounds, header_pos + 4);
        }
        const std::uint8_t* payload = cur.base + payload_pos;
        const std::size_t chunk_total = 8u + static_cast<std::size_t>(chunk_size);

        cur.pos += chunk_total;
        if ((chunk_size & 1u) != 0) {
            // Odd-sized chunks must be followed by exactly one 0x00 pad byte.
            if (cur.pos >= cur.size) {
                return fail(AuditError::kMissingPaddingByte, cur.pos);
            }
            if (cur.base[cur.pos] != 0x00u) {
                return fail(AuditError::kNonZeroPadding, cur.pos);
            }
            ++cur.pos;
        }

        const detail::ChunkKind kind = detail::classify(tag);
        const bool first_chunk = (header_pos == 12);

        if (first_chunk && kind == detail::ChunkKind::kVp8x) {
            extended = true;
            if (chunk_size != 10) {
                return fail(AuditError::kInvalidVp8xSize, header_pos + 4);
            }
            const std::uint8_t flags = payload[0];
            if ((flags & 0xc0u) != 0) {
                return fail(AuditError::kReservedVp8xFlagSet, header_pos + 8);
            }
            if (payload[1] != 0u || payload[2] != 0u || payload[3] != 0u) {
                return fail(AuditError::kReservedVp8xByteSet, header_pos + 9);
            }
            flag_icc = (flags & 0x20u) != 0;
            flag_alpha = (flags & 0x10u) != 0;
            flag_exif = (flags & 0x08u) != 0;
            flag_xmp = (flags & 0x04u) != 0;
            flag_anim = (flags & 0x02u) != 0;
            canvas_width = 1u + detail::le24(payload + 4);
            canvas_height = 1u + detail::le24(payload + 7);
            AuditError verr =
                detail::check_geometry(canvas_width, canvas_height, options);
            if (verr != AuditError::kOk) {
                return fail(verr, header_pos + 8);
            }
            continue;
        }

        if (first_chunk && kind != detail::ChunkKind::kVp8 &&
            kind != detail::ChunkKind::kVp8l) {
            return fail(AuditError::kInvalidFirstChunk, header_pos);
        }

        if (!extended) {
            // Simple format: exactly one VP8/VP8L and nothing else.
            if (!first_chunk) {
                return fail(AuditError::kUnexpectedChunk, header_pos);
            }
            detail::FrameGeometry geo{};
            AuditError gerr = (kind == detail::ChunkKind::kVp8)
                                  ? detail::parse_vp8(payload, chunk_size, geo)
                                  : detail::parse_vp8l(payload, chunk_size, geo);
            if (gerr != AuditError::kOk) {
                return fail(gerr, header_pos + 8);
            }
            gerr = detail::check_geometry(geo.width, geo.height, options);
            if (gerr != AuditError::kOk) {
                return fail(gerr, header_pos + 8);
            }
            first_geo = geo;
            image_seen = true;
            alpha_any = geo.alpha_hint;
            frame_count = 1;
            continue;
        }

        // ---- Extended format: ordered chunk state machine ----
        switch (kind) {
        case detail::ChunkKind::kVp8x:
            return fail(AuditError::kUnexpectedChunk, header_pos);

        case detail::ChunkKind::kIccp:
            if (phase != detail::Phase::kBeforeBitstream) {
                return fail(AuditError::kInvalidChunkOrder, header_pos);
            }
            if (iccp_seen) {
                return fail(AuditError::kDuplicateChunk, header_pos);
            }
            iccp_seen = true;
            break;

        case detail::ChunkKind::kAnim:
            if (phase != detail::Phase::kBeforeBitstream) {
                return fail(AuditError::kInvalidChunkOrder, header_pos);
            }
            if (anim_seen) {
                return fail(AuditError::kDuplicateChunk, header_pos);
            }
            if (chunk_size != 6) {
                return fail(AuditError::kInvalidAnimSize, header_pos + 4);
            }
            anim_seen = true;
            break;

        case detail::ChunkKind::kAlph:
            if (flag_anim) {
                // In animated files ALPH belongs inside ANMF frames.
                return fail(AuditError::kUnexpectedChunk, header_pos);
            }
            if (phase == detail::Phase::kMetadata) {
                return fail(AuditError::kInvalidChunkOrder, header_pos);
            }
            if (top_alph_seen) {
                return fail(AuditError::kDuplicateChunk, header_pos);
            }
            top_alph_seen = true;
            alpha_any = true;
            break;

        case detail::ChunkKind::kVp8:
        case detail::ChunkKind::kVp8l: {
            if (flag_anim) {
                return fail(AuditError::kUnexpectedChunk, header_pos);
            }
            if (phase == detail::Phase::kMetadata) {
                return fail(AuditError::kInvalidChunkOrder, header_pos);
            }
            if (image_seen) {
                return fail(AuditError::kMultipleImageFrames, header_pos);
            }
            detail::FrameGeometry geo{};
            AuditError gerr = (kind == detail::ChunkKind::kVp8)
                                  ? detail::parse_vp8(payload, chunk_size, geo)
                                  : detail::parse_vp8l(payload, chunk_size, geo);
            if (gerr != AuditError::kOk) {
                return fail(gerr, header_pos + 8);
            }
            gerr = detail::check_geometry(geo.width, geo.height, options);
            if (gerr != AuditError::kOk) {
                return fail(gerr, header_pos + 8);
            }
            if (geo.width > canvas_width || geo.height > canvas_height) {
                return fail(AuditError::kFrameOutsideCanvas, header_pos + 8);
            }
            first_geo = geo;
            image_seen = true;
            frame_count = 1;
            if (kind == detail::ChunkKind::kVp8l && geo.alpha_hint) {
                alpha_any = true;
            }
            phase = detail::Phase::kBitstream;
            break;
        }

        case detail::ChunkKind::kAnmf: {
            if (!flag_anim) {
                return fail(AuditError::kUnexpectedChunk, header_pos);
            }
            if (phase == detail::Phase::kMetadata) {
                return fail(AuditError::kInvalidChunkOrder, header_pos);
            }
            anmf_seen = true;
            detail::FrameGeometry geo{};
            bool frame_alpha = false;
            AuditError aerr = detail::parse_anmf(
                payload, chunk_size, canvas_width, canvas_height, options, geo,
                frame_alpha);
            if (aerr != AuditError::kOk) {
                return fail(aerr, header_pos + 8);
            }
            if (frame_alpha) alpha_any = true;
            ++frame_count;
            phase = detail::Phase::kBitstream;
            break;
        }

        case detail::ChunkKind::kExif:
            if (exif_seen) {
                return fail(AuditError::kDuplicateChunk, header_pos);
            }
            exif_seen = true;
            phase = detail::Phase::kMetadata;
            break;

        case detail::ChunkKind::kXmp:
            if (xmp_seen) {
                return fail(AuditError::kDuplicateChunk, header_pos);
            }
            xmp_seen = true;
            phase = detail::Phase::kMetadata;
            break;

        case detail::ChunkKind::kUnknown:
            return fail(AuditError::kUnknownChunk, header_pos);
        }
    }

    if (extended) {
        if (flag_icc != iccp_seen || flag_exif != exif_seen ||
            flag_xmp != xmp_seen || flag_anim != (anim_seen || anmf_seen) ||
            (flag_alpha && !alpha_any)) {
            return fail(AuditError::kFlagChunkMismatch, 12);
        }
        if (flag_anim) {
            if (!anim_seen || frame_count == 0) {
                return fail(AuditError::kNoImageFrame, 12);
            }
        } else if (!image_seen) {
            return fail(AuditError::kNoImageFrame, 12);
        }

        result.info.width = canvas_width;
        result.info.height = canvas_height;
        result.info.has_alpha = alpha_any;
        result.info.has_animation = flag_anim;
        result.info.has_icc = iccp_seen;
        result.info.has_exif = exif_seen;
        result.info.has_xmp = xmp_seen;
        result.info.frame_count = frame_count;
    } else {
        result.info.width = first_geo.width;
        result.info.height = first_geo.height;
        result.info.has_alpha = alpha_any;
        result.info.frame_count = 1;
    }

    return result;
}

inline const char* to_string(AuditError error) noexcept {
    switch (error) {
    case AuditError::kOk: return "ok";
    case AuditError::kNullInput: return "null input";
    case AuditError::kTooSmall: return "input smaller than RIFF header";
    case AuditError::kBadRiffMagic: return "missing RIFF magic";
    case AuditError::kBadRiffSize: return "RIFF size does not match input length";
    case AuditError::kBadWebpMagic: return "missing WEBP fourcc";
    case AuditError::kTruncatedChunkHeader: return "truncated 8-byte chunk header";
    case AuditError::kChunkPayloadOutOfBounds: return "chunk payload exceeds input";
    case AuditError::kMissingPaddingByte: return "missing odd-size chunk pad byte";
    case AuditError::kNonZeroPadding: return "non-zero odd-size chunk pad byte";
    case AuditError::kInvalidFirstChunk: return "first chunk must be VP8X/VP8/VP8L";
    case AuditError::kInvalidVp8xSize: return "VP8X chunk must be 10 bytes";
    case AuditError::kReservedVp8xByteSet: return "reserved VP8X byte is non-zero";
    case AuditError::kReservedVp8xFlagSet: return "reserved VP8X flag bit is set";
    case AuditError::kFlagChunkMismatch: return "VP8X flags contradict present chunks";
    case AuditError::kUnexpectedChunk: return "unexpected chunk for this format";
    case AuditError::kInvalidChunkOrder: return "chunk ordering violation";
    case AuditError::kUnknownChunk: return "unknown RIFF chunk fourcc";
    case AuditError::kDuplicateChunk: return "duplicate chunk";
    case AuditError::kMultipleImageFrames: return "multiple conflicting image frames";
    case AuditError::kTruncatedVp8Header: return "truncated VP8 frame header";
    case AuditError::kNotKeyFrame: return "VP8 frame is not a key frame";
    case AuditError::kBadVp8StartCode: return "bad VP8 start code 0x9d012a";
    case AuditError::kTruncatedVp8lHeader: return "truncated VP8L header";
    case AuditError::kBadVp8lSignature: return "bad VP8L signature 0x2f";
    case AuditError::kTruncatedAnmfHeader: return "truncated ANMF frame header";
    case AuditError::kInvalidAnmfSize: return "malformed ANMF inner chunk length";
    case AuditError::kReservedAnmfFlagSet: return "reserved ANMF flag bit is set";
    case AuditError::kZeroDimension: return "zero width or height";
    case AuditError::kDimensionExceedsLimit: return "dimension exceeds host limit";
    case AuditError::kPixelCountExceedsQuota: return "pixel count exceeds quota";
    case AuditError::kFrameOutsideCanvas: return "frame extends outside canvas";
    case AuditError::kFrameDimensionMismatch: return "frame bitstream size mismatch";
    case AuditError::kInvalidAnimSize: return "ANIM chunk must be 6 bytes";
    case AuditError::kNoImageFrame: return "no image frame present";
    case AuditError::kTrailingData: return "trailing data after RIFF container";
    }
    return "unknown error";
}

} // namespace webp_audit

#endif // WEBPAUDIT_WEBP_AUDIT_HPP_INCLUDED
