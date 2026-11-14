#pragma once

// A small, importable sfnt (TrueType/OpenType) table validator and
// normalizer. Validation and rendering deliberately consume the same
// immutable, normalized object. Rendering never indexes the font file or the
// table directory directly, so a second interpretation of an overlapping
// region cannot be used to substitute a glyph after validation.

#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Byte = std::uint8_t;
using ByteSpan = std::span<const Byte>;

enum class ErrorCode {
    FileTooSmall,
    BadSfntVersion,
    InvalidDirectory,
    DuplicateTable,
    TableOutOfBounds,
    TableNotAligned,
    DirectoryOverlap,
    TableOverlap,
    ChecksumMismatch,
    FontChecksumMismatch,
    MissingTable,
    InvalidHead,
    InvalidMaxp,
    InvalidHhea,
    InvalidHmtx,
    InvalidLoca,
    InvalidGlyph,
    GlyphIndexOutOfRange,
    CompositeCycle,
    CompositeDepthExceeded,
    InvalidCmap,
    UnsupportedCmapFormat,
    CmapOutOfBounds,
    CmapOverlap,
    InvalidCmapGlyph,
    UnsupportedFont,
};

struct Diagnostic {
    ErrorCode code = ErrorCode::InvalidDirectory;
    std::string table;
    std::uint64_t offset = 0;
    std::string message;

    [[nodiscard]] std::string toString() const;
};

struct ValidationOptions {
    bool verify_checksums = true;
    // The optional whole-font check is off by default because many
    // programmatically generated fonts have correct per-table sums but
    // deliberately leave head.checkSumAdjustment untouched.
    bool verify_font_checksum = false;
    std::size_t max_composite_depth = 64;
};

struct TableRecord {
    std::string tag;
    std::uint32_t checksum = 0;
    std::uint32_t offset = 0;
    std::uint32_t length = 0;
    std::uint32_t padded_end = 0;
};

struct GlyphPoint {
    double x = 0.0;
    double y = 0.0;
    bool on_curve = true;
};

struct Contour {
    std::vector<GlyphPoint> points;
};

struct GlyphOutline {
    std::vector<Contour> contours;
    std::int32_t advance_width = 0;
    std::int32_t left_side_bearing = 0;
    bool composite = false;
};

// A normalized cmap interval. For a non-constant interval the glyph id is
// start_glyph + (codepoint - first); a constant interval is used by cmap
// format 13.
struct CmapRange {
    std::uint32_t first = 0;
    std::uint32_t last = 0;
    std::uint32_t glyph = 0;
    bool constant = false;
};

enum class PathVerb {
    MoveTo,
    LineTo,
    QuadraticTo,
    Close,
};

// Quadratic commands use (x1,y1) as the control point and (x2,y2) as the
// endpoint. MoveTo/LineTo use (x1,y1) as the endpoint.
struct PathCommand {
    PathVerb verb = PathVerb::MoveTo;
    double x1 = 0.0;
    double y1 = 0.0;
    double x2 = 0.0;
    double y2 = 0.0;
};

struct RenderOptions {
    // Font units are multiplied by this value. A caller rendering at a
    // particular pixel size can pass pixels_per_em / units_per_em.
    double scale = 1.0;
    bool flip_y = false;
};

struct RenderedGlyph {
    std::uint16_t glyph_id = 0;
    std::optional<std::uint32_t> codepoint;
    double advance = 0.0;
    std::vector<PathCommand> path;
    // Stable serialization of the normalized outline and path, useful for
    // comparing the validation-side and rendering-side interpretation.
    std::string fingerprint;
};

namespace detail {
struct FontImpl;
struct FontLoadResultAccess;
}

struct FontLoadResult;

class FontFace {
public:
    FontFace() = default;

    [[nodiscard]] bool valid() const noexcept;
    [[nodiscard]] std::uint32_t sfntVersion() const noexcept;
    [[nodiscard]] std::uint16_t numGlyphs() const noexcept;
    [[nodiscard]] std::uint16_t unitsPerEm() const noexcept;

    [[nodiscard]] const std::vector<TableRecord>& tables() const;
    [[nodiscard]] const TableRecord* table(std::string_view tag) const noexcept;
    [[nodiscard]] bool hasTable(std::string_view tag) const noexcept;

    // Both functions use the same normalized cmap intervals. A zero glyph
    // id (or an absent value) means that the character is not mapped.
    [[nodiscard]] std::optional<std::uint16_t> glyphIndex(
        std::uint32_t codepoint) const noexcept;
    [[nodiscard]] std::optional<std::uint16_t> codepointToGlyph(
        std::uint32_t codepoint) const noexcept {
        return glyphIndex(codepoint);
    }
    [[nodiscard]] std::optional<std::uint16_t> lookupGlyph(
        std::uint32_t codepoint) const noexcept {
        return glyphIndex(codepoint);
    }

    [[nodiscard]] const std::vector<CmapRange>& cmapRanges() const;

    [[nodiscard]] const GlyphOutline* glyphOutline(
        std::uint16_t glyph_id) const noexcept;
    [[nodiscard]] std::optional<GlyphOutline> glyph(
        std::uint16_t glyph_id) const;

    // renderCodepoint resolves the character through glyphIndex() and then
    // calls renderGlyph() on the returned id. If the character is absent it
    // renders .notdef (glyph 0), exactly like a text engine would.
    [[nodiscard]] RenderedGlyph renderGlyph(
        std::uint16_t glyph_id, const RenderOptions& options = {}) const;
    [[nodiscard]] RenderedGlyph renderCodepoint(
        std::uint32_t codepoint, const RenderOptions& options = {}) const;

    [[nodiscard]] std::string glyphFingerprint(
        std::uint16_t glyph_id, const RenderOptions& options = {}) const;
    [[nodiscard]] std::string codepointFingerprint(
        std::uint32_t codepoint, const RenderOptions& options = {}) const;

    // Equivalent to loadFont(); provided where a service-like static entry
    // point is more convenient.
    static FontLoadResult load(ByteSpan bytes,
                               const ValidationOptions& options = {});
    static FontLoadResult load(const std::vector<Byte>& bytes,
                               const ValidationOptions& options = {});
    static FontLoadResult parse(ByteSpan bytes,
                                const ValidationOptions& options = {});
    static FontLoadResult parse(const std::vector<Byte>& bytes,
                                const ValidationOptions& options = {});
    static FontLoadResult fromBytes(ByteSpan bytes,
                                    const ValidationOptions& options = {});

private:
    explicit FontFace(std::shared_ptr<const detail::FontImpl> impl);

    std::shared_ptr<const detail::FontImpl> impl_;

    friend detail::FontLoadResultAccess;
};

using Font = FontFace;

struct FontLoadResult {
    std::optional<FontFace> font;
    std::vector<Diagnostic> diagnostics;

    [[nodiscard]] bool ok() const noexcept { return font.has_value(); }
    [[nodiscard]] bool valid() const noexcept { return ok(); }
    explicit operator bool() const noexcept { return ok(); }
};

using ValidationResult = FontLoadResult;
using ParseResult = FontLoadResult;

FontLoadResult loadFont(ByteSpan bytes,
                        const ValidationOptions& options = {});

inline FontLoadResult loadFont(const std::vector<Byte>& bytes,
                               const ValidationOptions& options = {}) {
    return loadFont(ByteSpan(bytes.data(), bytes.size()), options);
}

inline FontLoadResult parseFont(ByteSpan bytes,
                                const ValidationOptions& options = {}) {
    return loadFont(bytes, options);
}

inline FontLoadResult parseFont(const std::vector<Byte>& bytes,
                                const ValidationOptions& options = {}) {
    return loadFont(bytes, options);
}

inline FontLoadResult validateFont(ByteSpan bytes,
                                   const ValidationOptions& options = {}) {
    return loadFont(bytes, options);
}

inline FontLoadResult validateFont(const std::vector<Byte>& bytes,
                                   const ValidationOptions& options = {}) {
    return loadFont(bytes, options);
}

inline FontLoadResult parse(ByteSpan bytes,
                            const ValidationOptions& options = {}) {
    return loadFont(bytes, options);
}

inline FontLoadResult parse(const std::vector<Byte>& bytes,
                            const ValidationOptions& options = {}) {
    return loadFont(bytes, options);
}

inline FontLoadResult validate(ByteSpan bytes,
                               const ValidationOptions& options = {}) {
    return loadFont(bytes, options);
}

inline FontLoadResult validate(const std::vector<Byte>& bytes,
                               const ValidationOptions& options = {}) {
    return loadFont(bytes, options);
}

// Convenience boolean entry points for services that only need a verdict.
[[nodiscard]] bool validateTableStructure(
    ByteSpan bytes, std::vector<Diagnostic>* diagnostics = nullptr,
    const ValidationOptions& options = {});

[[nodiscard]] bool validateTableStructure(
    const std::vector<Byte>& bytes,
    std::vector<Diagnostic>* diagnostics = nullptr,
    const ValidationOptions& options = {});

}  // namespace netsec
