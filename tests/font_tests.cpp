#include "netsec.hpp"

#include <algorithm>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace {

using Bytes = std::vector<std::uint8_t>;
using namespace netsec;

int failures = 0;

void check(bool condition, std::string_view what) {
    if (!condition) {
        std::cerr << "FAIL: " << what << '\n';
        ++failures;
    }
}

void checkCode(const FontLoadResult& result, ErrorCode code,
               std::string_view what) {
    const bool matched = !result.ok() && !result.diagnostics.empty() &&
                         result.diagnostics.front().code == code;
    if (!matched) {
        std::cerr << "FAIL: " << what << " (expected error code "
                  << static_cast<int>(code) << ")";
        if (result.ok()) {
            std::cerr << " but validation succeeded";
        } else if (!result.diagnostics.empty()) {
            std::cerr << " got: " << result.diagnostics.front().toString();
        }
        std::cerr << '\n';
        ++failures;
    }
}

void appendU16(Bytes& bytes, std::uint32_t value) {
    bytes.push_back(static_cast<std::uint8_t>(value >> 8));
    bytes.push_back(static_cast<std::uint8_t>(value));
}

void appendI16(Bytes& bytes, std::int32_t value) {
    appendU16(bytes, static_cast<std::uint16_t>(value));
}

void appendU32(Bytes& bytes, std::uint32_t value) {
    bytes.push_back(static_cast<std::uint8_t>(value >> 24));
    bytes.push_back(static_cast<std::uint8_t>(value >> 16));
    bytes.push_back(static_cast<std::uint8_t>(value >> 8));
    bytes.push_back(static_cast<std::uint8_t>(value));
}

void putU16(Bytes& bytes, std::size_t offset, std::uint32_t value) {
    bytes[offset] = static_cast<std::uint8_t>(value >> 8);
    bytes[offset + 1] = static_cast<std::uint8_t>(value);
}

void putU32(Bytes& bytes, std::size_t offset, std::uint32_t value) {
    bytes[offset] = static_cast<std::uint8_t>(value >> 24);
    bytes[offset + 1] = static_cast<std::uint8_t>(value >> 16);
    bytes[offset + 2] = static_cast<std::uint8_t>(value >> 8);
    bytes[offset + 3] = static_cast<std::uint8_t>(value);
}

std::uint16_t readU16(const Bytes& bytes, std::size_t offset) {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(bytes[offset]) << 8) |
        bytes[offset + 1]);
}

std::uint32_t readU32(const Bytes& bytes, std::size_t offset) {
    return (static_cast<std::uint32_t>(bytes[offset]) << 24) |
           (static_cast<std::uint32_t>(bytes[offset + 1]) << 16) |
           (static_cast<std::uint32_t>(bytes[offset + 2]) << 8) |
           bytes[offset + 3];
}

std::uint32_t tableChecksum(const Bytes& table, bool is_head) {
    std::uint32_t sum = 0;
    const std::size_t words = (table.size() + 3) / 4;
    for (std::size_t word = 0; word < words; ++word) {
        std::uint32_t value = 0;
        for (std::size_t byte = 0; byte < 4; ++byte) {
            const std::size_t index = word * 4 + byte;
            value <<= 8;
            if (index < table.size()) {
                value |= table[index];
            }
        }
        if (is_head && word == 2) {
            value = 0;
        }
        sum += value;
    }
    return sum;
}

std::uint32_t entireChecksum(const Bytes& bytes,
                             std::size_t zero_word_offset) {
    std::uint32_t sum = 0;
    const std::size_t words = (bytes.size() + 3) / 4;
    for (std::size_t word = 0; word < words; ++word) {
        std::uint32_t value = 0;
        for (std::size_t byte = 0; byte < 4; ++byte) {
            const std::size_t index = word * 4 + byte;
            value <<= 8;
            if (index < bytes.size()) {
                value |= bytes[index];
            }
        }
        if (word * 4 == zero_word_offset) {
            value = 0;
        }
        sum += value;
    }
    return sum;
}

struct Point {
    int x = 0;
    int y = 0;
};

Bytes simpleGlyph(const std::vector<Point>& points) {
    if (points.empty()) {
        return {};
    }
    int min_x = points.front().x;
    int max_x = points.front().x;
    int min_y = points.front().y;
    int max_y = points.front().y;
    for (const Point& point : points) {
        min_x = std::min(min_x, point.x);
        max_x = std::max(max_x, point.x);
        min_y = std::min(min_y, point.y);
        max_y = std::max(max_y, point.y);
    }

    Bytes glyph(10, 0);
    putU16(glyph, 0, 1);
    putU16(glyph, 2, static_cast<std::uint16_t>(min_x));
    putU16(glyph, 4, static_cast<std::uint16_t>(min_y));
    putU16(glyph, 6, static_cast<std::uint16_t>(max_x));
    putU16(glyph, 8, static_cast<std::uint16_t>(max_y));
    appendU16(glyph, static_cast<std::uint32_t>(points.size() - 1));
    appendU16(glyph, 0);  // instructionLength

    for (std::size_t index = 0; index < points.size(); ++index) {
        glyph.push_back(0x01);  // on-curve, 16-bit deltas
    }
    int previous = 0;
    for (const Point& point : points) {
        appendI16(glyph, point.x - previous);
        previous = point.x;
    }
    previous = 0;
    for (const Point& point : points) {
        appendI16(glyph, point.y - previous);
        previous = point.y;
    }
    return glyph;
}

Bytes compositeGlyph(std::uint16_t component, std::int16_t dx,
                     std::int16_t dy) {
    Bytes glyph(10, 0);
    putU16(glyph, 0, 0xFFFFu);  // -1
    appendU16(glyph, 0x0003u);  // ARG_1_AND_2_ARE_WORDS | ARGS_ARE_XY_VALUES
    appendU16(glyph, component);
    appendI16(glyph, dx);
    appendI16(glyph, dy);
    return glyph;
}

struct GlyphSet {
    Bytes glyf;
    Bytes loca;
};

GlyphSet packGlyphs(const std::vector<Bytes>& glyphs) {
    GlyphSet set;
    set.loca.clear();
    appendU32(set.loca, 0);
    for (const Bytes& glyph : glyphs) {
        set.glyf.insert(set.glyf.end(), glyph.begin(), glyph.end());
        appendU32(set.loca, static_cast<std::uint32_t>(set.glyf.size()));
    }
    return set;
}

GlyphSet packGlyphsShortLoca(const std::vector<Bytes>& glyphs) {
    GlyphSet set;
    appendU16(set.loca, 0);
    for (const Bytes& glyph : glyphs) {
        set.glyf.insert(set.glyf.end(), glyph.begin(), glyph.end());
        if ((set.glyf.size() & 1u) != 0u) {
            set.glyf.push_back(0);
        }
        appendU16(set.loca, static_cast<std::uint32_t>(set.glyf.size() / 2));
    }
    return set;
}

Bytes makeHead() {
    Bytes head(54, 0);
    putU32(head, 0, 0x00010000u);
    putU32(head, 4, 0x00010000u);
    putU32(head, 12, 0x5F0F3CF5u);
    putU16(head, 18, 1000);
    putU16(head, 48, 2);
    putU16(head, 50, 1);  // long loca offsets
    putU16(head, 52, 0);
    return head;
}

Bytes makeMaxp(std::uint16_t num_glyphs) {
    Bytes maxp(32, 0);
    putU32(maxp, 0, 0x00010000u);
    putU16(maxp, 4, num_glyphs);
    return maxp;
}

Bytes makeHhea(std::uint16_t num_glyphs) {
    Bytes hhea(36, 0);
    putU32(hhea, 0, 0x00010000u);
    putU16(hhea, 34, num_glyphs);
    return hhea;
}

Bytes makeHmtx(std::uint16_t num_glyphs) {
    Bytes hmtx;
    for (std::uint16_t glyph = 0; glyph < num_glyphs; ++glyph) {
        appendU16(hmtx, 600 + glyph);
        appendI16(hmtx, 0);
    }
    return hmtx;
}

struct Mapping {
    std::uint16_t codepoint = 0;
    std::uint16_t glyph = 0;
};

struct Mapping32 {
    std::uint32_t codepoint = 0;
    std::uint32_t glyph = 0;
};

Bytes makeCmap(std::vector<Mapping> mappings) {
    std::sort(mappings.begin(), mappings.end(),
              [](const Mapping& lhs, const Mapping& rhs) {
                  return lhs.codepoint < rhs.codepoint;
              });
    const std::size_t segment_count = mappings.size() + 1;
    const std::size_t subtable_length = 16 + segment_count * 8;

    Bytes subtable;
    appendU16(subtable, 4);
    appendU16(subtable, static_cast<std::uint32_t>(subtable_length));
    appendU16(subtable, 0);  // language
    appendU16(subtable, static_cast<std::uint32_t>(segment_count * 2));

    std::uint16_t power = 1;
    std::uint16_t selector = 0;
    while (static_cast<std::uint32_t>(power) * 2 <= segment_count) {
        power = static_cast<std::uint16_t>(power * 2);
        ++selector;
    }
    appendU16(subtable, static_cast<std::uint32_t>(power * 2));
    appendU16(subtable, selector);
    appendU16(subtable,
              static_cast<std::uint32_t>(segment_count * 2 - power * 2));

    for (const Mapping& mapping : mappings) {
        appendU16(subtable, mapping.codepoint);
    }
    appendU16(subtable, 0xFFFF);
    appendU16(subtable, 0);  // reservedPad
    for (const Mapping& mapping : mappings) {
        appendU16(subtable, mapping.codepoint);
    }
    appendU16(subtable, 0xFFFF);
    for (const Mapping& mapping : mappings) {
        appendI16(subtable,
                  static_cast<std::int32_t>(mapping.glyph) -
                      mapping.codepoint);
    }
    appendU16(subtable, 1);
    for (std::size_t index = 0; index < mappings.size(); ++index) {
        appendU16(subtable, 0);
    }
    appendU16(subtable, 0);

    Bytes cmap;
    appendU16(cmap, 0);
    appendU16(cmap, 1);
    appendU16(cmap, 3);  // Windows
    appendU16(cmap, 1);  // Unicode BMP
    appendU32(cmap, 12);
    cmap.insert(cmap.end(), subtable.begin(), subtable.end());
    return cmap;
}

Bytes makeOverlappingCmap() {
    Bytes cmap(40, 0);
    putU16(cmap, 0, 0);
    putU16(cmap, 2, 2);
    putU16(cmap, 4, 3);
    putU16(cmap, 6, 1);
    putU32(cmap, 8, 20);
    putU16(cmap, 12, 3);
    putU16(cmap, 14, 10);
    putU32(cmap, 16, 22);

    putU16(cmap, 20, 4);    // first subtable: format 4, declared length 12
    putU16(cmap, 22, 12);   // bytes at 22 are also a format 12 header
    putU16(cmap, 24, 0);    // reserved
    putU32(cmap, 26, 16);   // second length; also the first segCountX2
    return cmap;
}

Bytes makeCmapFormat12(std::vector<Mapping32> mappings) {
    std::sort(mappings.begin(), mappings.end(),
              [](const Mapping32& lhs, const Mapping32& rhs) {
                  return lhs.codepoint < rhs.codepoint;
              });
    const std::uint32_t group_count =
        static_cast<std::uint32_t>(mappings.size());
    const std::uint32_t subtable_length = 16 + group_count * 12;

    Bytes subtable;
    appendU16(subtable, 12);
    appendU16(subtable, 0);
    appendU32(subtable, subtable_length);
    appendU32(subtable, 0);
    appendU32(subtable, group_count);
    for (const Mapping32& mapping : mappings) {
        appendU32(subtable, mapping.codepoint);
        appendU32(subtable, mapping.codepoint);
        appendU32(subtable, mapping.glyph);
    }

    Bytes cmap;
    appendU16(cmap, 0);
    appendU16(cmap, 1);
    appendU16(cmap, 3);   // Windows
    appendU16(cmap, 10);  // Unicode full repertoire
    appendU32(cmap, 12);
    cmap.insert(cmap.end(), subtable.begin(), subtable.end());
    return cmap;
}

struct TableData {
    std::string tag;
    Bytes data;
};

struct DirectoryEntry {
    std::size_t record_offset = 0;
    std::uint32_t checksum = 0;
    std::uint32_t offset = 0;
    std::uint32_t length = 0;
};

Bytes buildFont(std::vector<TableData> tables) {
    std::sort(tables.begin(), tables.end(),
              [](const TableData& lhs, const TableData& rhs) {
                  return lhs.tag < rhs.tag;
              });
    const std::size_t count = tables.size();
    const std::size_t header_size = 12 + count * 16;
    const std::size_t aligned_header = (header_size + 3) & ~std::size_t{3};

    std::vector<std::uint32_t> offsets(count, 0);
    std::size_t cursor = aligned_header;
    for (std::size_t index = 0; index < count; ++index) {
        cursor = (cursor + 3) & ~std::size_t{3};
        offsets[index] = static_cast<std::uint32_t>(cursor);
        cursor += tables[index].data.size();
    }
    cursor = (cursor + 3) & ~std::size_t{3};
    Bytes font(cursor, 0);

    putU32(font, 0, 0x00010000u);
    putU16(font, 4, static_cast<std::uint32_t>(count));
    std::uint16_t power = 1;
    std::uint16_t selector = 0;
    while (static_cast<std::uint32_t>(power) * 2 <= count) {
        power = static_cast<std::uint16_t>(power * 2);
        ++selector;
    }
    putU16(font, 6, static_cast<std::uint32_t>(power * 16));
    putU16(font, 8, selector);
    putU16(font, 10, static_cast<std::uint32_t>(count * 16 - power * 16));

    std::size_t head_offset = 0;
    for (std::size_t index = 0; index < count; ++index) {
        const std::size_t entry = 12 + index * 16;
        std::memcpy(font.data() + entry, tables[index].tag.data(), 4);
        putU32(font, entry + 4,
               tableChecksum(tables[index].data,
                             tables[index].tag == "head"));
        putU32(font, entry + 8, offsets[index]);
        putU32(font, entry + 12,
               static_cast<std::uint32_t>(tables[index].data.size()));
        std::copy(tables[index].data.begin(), tables[index].data.end(),
                  font.begin() + offsets[index]);
        if (tables[index].tag == "head") {
            head_offset = offsets[index];
        }
    }

    if (head_offset != 0) {
        const std::uint32_t sum = entireChecksum(font, head_offset + 8);
        putU32(font, head_offset + 8, 0xB1B0AFBAu - sum);
    }
    return font;
}

DirectoryEntry findEntry(const Bytes& font, std::string_view tag) {
    const std::uint16_t count = readU16(font, 4);
    for (std::uint16_t index = 0; index < count; ++index) {
        const std::size_t entry = 12 + static_cast<std::size_t>(index) * 16;
        if (std::memcmp(font.data() + entry, tag.data(), 4) == 0) {
            return DirectoryEntry{entry, readU32(font, entry + 4),
                                  readU32(font, entry + 8),
                                  readU32(font, entry + 12)};
        }
    }
    return {};
}

std::vector<TableData> standardTables(
    std::uint16_t num_glyphs, const std::vector<Bytes>& glyphs,
    const std::vector<Mapping>& mappings) {
    const GlyphSet packed = packGlyphs(glyphs);
    return {
        {"cmap", makeCmap(mappings)},
        {"glyf", packed.glyf},
        {"head", makeHead()},
        {"hhea", makeHhea(num_glyphs)},
        {"hmtx", makeHmtx(num_glyphs)},
        {"loca", packed.loca},
        {"maxp", makeMaxp(num_glyphs)},
    };
}

const std::vector<Point> kNotdefSquare = {
    {0, 0}, {100, 0}, {100, 100}, {0, 100}};
const std::vector<Point> kATriangle = {
    {0, 0}, {100, 0}, {50, 100}};
const std::vector<Point> kCRectangle = {
    {0, 0}, {80, 0}, {80, 80}, {0, 80}};

std::vector<Bytes> validGlyphs() {
    return {simpleGlyph(kNotdefSquare), simpleGlyph(kATriangle),
            compositeGlyph(1, 300, 0), simpleGlyph(kCRectangle)};
}

void testValidFontAndRendering() {
    const std::vector<Mapping> mappings = {{0x41, 1}, {0x42, 2}, {0x43, 3}};
    const Bytes font = buildFont(
        standardTables(4, validGlyphs(), mappings));

    ValidationOptions options;
    options.verify_font_checksum = true;
    const FontLoadResult result = loadFont(font, options);
    check(result.ok(), "valid font is accepted");
    if (!result.ok()) {
        for (const Diagnostic& diagnostic : result.diagnostics) {
            std::cerr << "  " << diagnostic.toString() << '\n';
        }
        return;
    }
    const FontFace& face = *result.font;
    check(face.valid(), "font object is valid");
    check(face.numGlyphs() == 4, "numGlyphs is 4");
    check(face.unitsPerEm() == 1000, "unitsPerEm is 1000");
    check(face.tables().size() == 7, "all seven tables are published");

    for (const TableRecord& record : face.tables()) {
        check((record.offset & 3u) == 0u,
              "every table offset is four-byte aligned");
        check(static_cast<std::size_t>(record.offset) + record.length <=
                  font.size(),
              "every table range is inside the file");
        check(face.table(record.tag) != nullptr,
              "table lookup uses the normalized directory");
    }

    const std::optional<std::uint16_t> a = face.glyphIndex(0x41);
    const std::optional<std::uint16_t> b = face.glyphIndex(0x42);
    const std::optional<std::uint16_t> c = face.glyphIndex(0x43);
    check(a && *a == 1, "cmap maps A to glyph 1");
    check(b && *b == 2, "cmap maps B to glyph 2");
    check(c && *c == 3, "cmap maps C to glyph 3");
    check(!face.glyphIndex(0x5A), "unmapped character is absent");

    const GlyphOutline* triangle = face.glyphOutline(1);
    check(triangle != nullptr && triangle->contours.size() == 1 &&
              triangle->contours[0].points.size() == 3,
          "simple glyph is normalized to one three-point contour");

    const GlyphOutline* composite = face.glyphOutline(2);
    check(composite != nullptr && composite->composite &&
              composite->contours.size() == 1,
          "composite glyph is normalized before publication");
    if (triangle != nullptr && composite != nullptr) {
        check(composite->contours[0].points[0].x ==
                  triangle->contours[0].points[0].x + 300.0,
              "composite translation is applied to normalized points");
        check(composite->contours[0].points[0].y ==
                  triangle->contours[0].points[0].y,
              "composite Y translation is preserved");
    }

    const RenderedGlyph throughCmap = face.renderCodepoint(0x42);
    const RenderedGlyph direct = face.renderGlyph(*b);
    check(throughCmap.glyph_id == *b,
          "renderer resolves the same glyph id as the validator");
    check(throughCmap.codepoint && *throughCmap.codepoint == 0x42,
          "renderer reports the requested codepoint");
    check(!throughCmap.path.empty(), "rendered glyph has a path");
    check(throughCmap.fingerprint == direct.fingerprint,
          "codepoint rendering and direct glyph rendering are identical");
    check(face.codepointFingerprint(0x42) ==
              face.glyphFingerprint(*b),
          "fingerprints use the same normalized glyph");

    RenderOptions scaled;
    scaled.scale = 0.5;
    const RenderedGlyph scaledGlyph = face.renderGlyph(*b, scaled);
    check(!scaledGlyph.path.empty() &&
              scaledGlyph.fingerprint != direct.fingerprint,
          "render options are applied to the shared outline");

    const RenderedGlyph missing = face.renderCodepoint(0x5A);
    check(missing.glyph_id == 0, "unmapped character renders .notdef");
}

void testOverlappingTables() {
    Bytes font = buildFont(standardTables(
        4, validGlyphs(), {{0x41, 1}, {0x42, 2}, {0x43, 3}}));
    const DirectoryEntry glyf = findEntry(font, "glyf");
    const DirectoryEntry cmap = findEntry(font, "cmap");
    putU32(font, static_cast<std::size_t>(cmap.record_offset) + 8,
           glyf.offset);
    checkCode(loadFont(font), ErrorCode::TableOverlap,
              "overlapping table offsets are rejected");
}

void testOutOfBoundsTable() {
    Bytes font = buildFont(standardTables(
        4, validGlyphs(), {{0x41, 1}, {0x42, 2}, {0x43, 3}}));
    const DirectoryEntry cmap = findEntry(font, "cmap");
    putU32(font, static_cast<std::size_t>(cmap.record_offset) + 8,
           static_cast<std::uint32_t>(font.size()));
    checkCode(loadFont(font), ErrorCode::TableOutOfBounds,
              "a table offset at end-of-file with non-zero length is "
              "rejected");
}

void testUnalignedTable() {
    Bytes font = buildFont(standardTables(
        4, validGlyphs(), {{0x41, 1}, {0x42, 2}, {0x43, 3}}));
    const DirectoryEntry cmap = findEntry(font, "cmap");
    putU32(font, static_cast<std::size_t>(cmap.record_offset) + 8,
           cmap.offset + 1);
    checkCode(loadFont(font), ErrorCode::TableNotAligned,
              "unaligned table offsets are rejected");
}

void testChecksumMismatch() {
    Bytes font = buildFont(standardTables(
        4, validGlyphs(), {{0x41, 1}, {0x42, 2}, {0x43, 3}}));
    const DirectoryEntry cmap = findEntry(font, "cmap");
    font[cmap.offset + cmap.length - 1] ^= 0x5Au;
    checkCode(loadFont(font), ErrorCode::ChecksumMismatch,
              "per-table checksum mismatch is rejected");
}

void testCmapGlyphOutOfRange() {
    const Bytes font = buildFont(
        standardTables(4, validGlyphs(), {{0x41, 99}}));
    checkCode(loadFont(font), ErrorCode::InvalidCmapGlyph,
              "cmap glyph reference outside numGlyphs is rejected");
}

void testCompositeCycle() {
    std::vector<Bytes> glyphs = validGlyphs();
    glyphs[2] = compositeGlyph(3, 10, 0);
    glyphs[3] = compositeGlyph(2, 10, 0);
    const Bytes font = buildFont(
        standardTables(4, glyphs, {{0x41, 1}, {0x42, 2}}));
    checkCode(loadFont(font), ErrorCode::CompositeCycle,
              "composite glyph reference cycle is rejected");
}

void testCompositeOutOfRange() {
    std::vector<Bytes> glyphs = validGlyphs();
    glyphs[2] = compositeGlyph(99, 10, 0);
    const Bytes font = buildFont(
        standardTables(4, glyphs, {{0x41, 1}, {0x42, 2}}));
    checkCode(loadFont(font), ErrorCode::GlyphIndexOutOfRange,
              "composite component outside numGlyphs is rejected");
}

void testOverlappingCmapSubtables() {
    std::vector<TableData> tables = standardTables(
        4, validGlyphs(), {{0x41, 1}, {0x42, 2}});
    for (TableData& table : tables) {
        if (table.tag == "cmap") {
            table.data = makeOverlappingCmap();
        }
    }
    checkCode(loadFont(buildFont(std::move(tables))),
              ErrorCode::CmapOverlap,
              "overlapping cmap subtables are rejected before parsing");
}

void testFormat12CmapAndSupplementaryRendering() {
    std::vector<TableData> tables = standardTables(
        4, validGlyphs(), {{0x41, 1}});
    for (TableData& table : tables) {
        if (table.tag == "cmap") {
            table.data = makeCmapFormat12({{0x1F600u, 2}});
        }
    }
    const FontLoadResult result = loadFont(buildFont(std::move(tables)));
    check(result.ok(), "format 12 cmap is accepted");
    if (!result.ok()) {
        return;
    }
    const FontFace& face = *result.font;
    const std::optional<std::uint16_t> smile = face.glyphIndex(0x1F600u);
    check(smile && *smile == 2,
          "format 12 maps a supplementary codepoint to glyph 2");
    const RenderedGlyph viaCmap = face.renderCodepoint(0x1F600u);
    const RenderedGlyph direct = face.renderGlyph(2);
    check(viaCmap.glyph_id == 2,
          "supplementary codepoint uses the same normalized glyph");
    check(viaCmap.fingerprint == direct.fingerprint,
          "format 12 rendering and direct glyph rendering agree");
}

void testShortLocaFormat() {
    std::vector<TableData> tables = standardTables(
        4, validGlyphs(), {{0x41, 1}, {0x42, 2}});
    const GlyphSet packed = packGlyphsShortLoca(validGlyphs());
    for (TableData& table : tables) {
        if (table.tag == "glyf") {
            table.data = packed.glyf;
        } else if (table.tag == "loca") {
            table.data = packed.loca;
        } else if (table.tag == "head") {
            putU16(table.data, 50, 0);  // indexToLocFormat = short
        }
    }
    const FontLoadResult result = loadFont(buildFont(std::move(tables)));
    check(result.ok(), "short loca offsets are accepted");
    if (result.ok()) {
        check(result.font->glyphIndex(0x41).value_or(0) == 1,
              "short loca font still maps A to glyph 1");
        check(result.font->glyphOutline(1) != nullptr,
              "short loca font publishes glyph outlines");
    }
}

void testMutatedInputsRemainSafe() {
    const Bytes seed = buildFont(standardTables(
        4, validGlyphs(), {{0x41, 1}, {0x42, 2}, {0x43, 3}}));
    std::uint32_t state = 0x12345678u;
    ValidationOptions options;
    options.verify_checksums = false;
    options.verify_font_checksum = false;
    for (int iteration = 0; iteration < 400; ++iteration) {
        Bytes mutated = seed;
        state = state * 1664525u + 1013904223u;
        const std::size_t first = state % mutated.size();
        mutated[first] ^= static_cast<std::uint8_t>(0xA5u);
        state = state * 1664525u + 1013904223u;
        const std::size_t second = state % mutated.size();
        mutated[second] ^= static_cast<std::uint8_t>(0x5Au);
        const FontLoadResult result = loadFont(mutated, options);
        (void)result;  // reaching this point means no crash or UB
    }
}

}  // namespace

int main() {
    testValidFontAndRendering();
    testOverlappingTables();
    testOutOfBoundsTable();
    testUnalignedTable();
    testChecksumMismatch();
    testCmapGlyphOutOfRange();
    testCompositeCycle();
    testCompositeOutOfRange();
    testOverlappingCmapSubtables();
    testFormat12CmapAndSupplementaryRendering();
    testShortLocaFormat();
    testMutatedInputsRemainSafe();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    std::cout << "all font structure tests passed\n";
    return 0;
}
