#include "netsec.hpp"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <functional>
#include <limits>
#include <map>
#include <memory>
#include <set>
#include <sstream>
#include <string>
#include <utility>

namespace netsec {
namespace detail {

struct RawComponent {
    std::uint16_t glyph = 0;
    bool args_are_xy = true;
    bool use_my_metrics = false;
    std::int32_t arg1 = 0;
    std::int32_t arg2 = 0;
    double a = 1.0;
    double b = 0.0;
    double c = 0.0;
    double d = 1.0;
};

struct RawGlyph {
    bool empty = true;
    bool composite = false;
    std::vector<Contour> contours;
    std::vector<RawComponent> components;
};

struct FontImpl {
    std::vector<std::uint8_t> file;
    std::uint32_t sfnt_version = 0;
    std::vector<TableRecord> tables;
    std::map<std::string, std::size_t, std::less<>> table_index;
    std::uint16_t num_glyphs = 0;
    std::uint16_t units_per_em = 0;
    std::int16_t index_to_loc_format = 0;
    std::vector<std::uint32_t> loca;
    std::vector<GlyphOutline> glyphs;
    std::vector<CmapRange> cmap;
};

struct FontLoadResultAccess {
    static FontFace make(std::shared_ptr<const FontImpl> impl) {
        return FontFace(std::move(impl));
    }
};

}  // namespace detail

namespace {

constexpr std::uint32_t kSfntTrueType = 0x00010000u;
constexpr std::uint32_t kSfntOtto = 0x4F54544Fu;   // 'OTTO'
constexpr std::uint32_t kSfntTrue = 0x74727565u;   // 'true'
constexpr std::uint32_t kSfntTyp1 = 0x74797031u;   // 'typ1'
constexpr std::uint32_t kHeadMagic = 0x5F0F3CF5u;
constexpr std::uint32_t kFontChecksumMagic = 0xB1B0AFBAu;

struct ParseFailure {
    Diagnostic diagnostic;
};

[[noreturn]] void fail(ErrorCode code, std::string_view table,
                       std::uint64_t offset, std::string message) {
    throw ParseFailure{
        Diagnostic{code, std::string(table), offset, std::move(message)}};
}

[[nodiscard]] bool rangeFits(std::size_t begin, std::size_t length,
                             std::size_t total) noexcept {
    return begin <= total && length <= total - begin;
}

[[nodiscard]] std::uint32_t readU32(ByteSpan bytes, std::size_t offset) noexcept {
    return (static_cast<std::uint32_t>(bytes[offset]) << 24) |
           (static_cast<std::uint32_t>(bytes[offset + 1]) << 16) |
           (static_cast<std::uint32_t>(bytes[offset + 2]) << 8) |
           static_cast<std::uint32_t>(bytes[offset + 3]);
}

[[nodiscard]] std::uint16_t readU16(ByteSpan bytes, std::size_t offset) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(bytes[offset]) << 8) |
        static_cast<std::uint16_t>(bytes[offset + 1]));
}

struct Reader {
    ByteSpan bytes;
    std::uint64_t absolute_base = 0;
    ErrorCode code = ErrorCode::InvalidDirectory;
    std::string table;

    void need(std::size_t offset, std::size_t length) const {
        if (!rangeFits(offset, length, bytes.size())) {
            fail(code, table, absolute_base + offset,
                 "read past the end of the owning table");
        }
    }

    [[nodiscard]] std::uint8_t u8(std::size_t offset) const {
        need(offset, 1);
        return bytes[offset];
    }

    [[nodiscard]] std::int8_t i8(std::size_t offset) const {
        return static_cast<std::int8_t>(u8(offset));
    }

    [[nodiscard]] std::uint16_t u16(std::size_t offset) const {
        need(offset, 2);
        return readU16(bytes, offset);
    }

    [[nodiscard]] std::int16_t i16(std::size_t offset) const {
        return static_cast<std::int16_t>(u16(offset));
    }

    [[nodiscard]] std::uint32_t u32(std::size_t offset) const {
        need(offset, 4);
        return readU32(bytes, offset);
    }

    [[nodiscard]] std::uint32_t u24(std::size_t offset) const {
        need(offset, 3);
        return (static_cast<std::uint32_t>(bytes[offset]) << 16) |
               (static_cast<std::uint32_t>(bytes[offset + 1]) << 8) |
               static_cast<std::uint32_t>(bytes[offset + 2]);
    }
};

[[nodiscard]] std::uint32_t tableChecksum(ByteSpan table, bool zero_head_adjust) {
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
        if (zero_head_adjust && word == 2) {
            value = 0;
        }
        sum += value;
    }
    return sum;
}

[[nodiscard]] std::uint32_t wholeFontChecksum(
    ByteSpan file, std::optional<std::uint32_t> head_adjust_offset) {
    std::uint32_t sum = 0;
    const std::size_t words = (file.size() + 3) / 4;
    for (std::size_t word = 0; word < words; ++word) {
        std::uint32_t value = 0;
        for (std::size_t byte = 0; byte < 4; ++byte) {
            const std::size_t index = word * 4 + byte;
            value <<= 8;
            if (index < file.size()) {
                value |= file[index];
            }
        }
        if (head_adjust_offset &&
            static_cast<std::uint64_t>(word) * 4 == *head_adjust_offset) {
            value = 0;
        }
        sum += value;
    }
    return sum;
}

[[nodiscard]] const TableRecord* findTable(const detail::FontImpl& font,
                                           std::string_view tag) noexcept {
    const auto it = font.table_index.find(tag);
    if (it == font.table_index.end()) {
        return nullptr;
    }
    return &font.tables[it->second];
}

[[nodiscard]] ByteSpan tableView(const detail::FontImpl& font,
                                 const TableRecord& record) noexcept {
    return ByteSpan(font.file.data() + record.offset, record.length);
}

[[nodiscard]] std::string tagToString(ByteSpan bytes, std::size_t offset) {
    return std::string(reinterpret_cast<const char*>(bytes.data() + offset), 4);
}

void validateDirectory(ByteSpan file, const ValidationOptions& options,
                       detail::FontImpl& font) {
    if (file.size() < 12) {
        fail(ErrorCode::FileTooSmall, "", 0,
             "an sfnt header needs at least 12 bytes");
    }

    font.sfnt_version = readU32(file, 0);
    const bool true_type = font.sfnt_version == kSfntTrueType ||
                           font.sfnt_version == kSfntTrue;
    const bool cff_like = font.sfnt_version == kSfntOtto ||
                          font.sfnt_version == kSfntTyp1;
    if (!true_type && !cff_like) {
        fail(ErrorCode::BadSfntVersion, "", 0,
             "unsupported sfnt version (expected TrueType or OTTO)");
    }

    const std::uint16_t table_count = readU16(file, 4);
    if (table_count == 0) {
        fail(ErrorCode::InvalidDirectory, "", 4,
             "the table directory is empty");
    }

    const std::uint64_t directory_end =
        12u + static_cast<std::uint64_t>(table_count) * 16u;
    if (directory_end > file.size()) {
        fail(ErrorCode::InvalidDirectory, "", 4,
             "the table directory extends past the end of the file");
    }

    std::set<std::string> seen_tags;
    font.tables.reserve(table_count);
    for (std::uint16_t index = 0; index < table_count; ++index) {
        const std::size_t entry = 12u + static_cast<std::size_t>(index) * 16u;
        const std::string tag = tagToString(file, entry);
        const std::uint32_t checksum = readU32(file, entry + 4);
        const std::uint32_t offset = readU32(file, entry + 8);
        const std::uint32_t length = readU32(file, entry + 12);

        if (!seen_tags.insert(tag).second) {
            fail(ErrorCode::DuplicateTable, tag, offset,
                 "duplicate table tag in the directory");
        }
        if ((offset & 3u) != 0u) {
            fail(ErrorCode::TableNotAligned, tag, offset,
                 "table offset is not a multiple of four");
        }
        if (!rangeFits(offset, length, file.size())) {
            fail(ErrorCode::TableOutOfBounds, tag, offset,
                 "table range lies outside the font file");
        }
        if (offset < directory_end) {
            fail(ErrorCode::DirectoryOverlap, tag, offset,
                 "table data overlaps the sfnt directory");
        }

        const std::uint64_t padded_end64 =
            static_cast<std::uint64_t>(offset) +
            ((static_cast<std::uint64_t>(length) + 3u) & ~std::uint64_t{3});
        if (padded_end64 > std::numeric_limits<std::uint32_t>::max()) {
            fail(ErrorCode::TableOutOfBounds, tag, offset,
                 "padded table range overflows a 32-bit sfnt offset");
        }

        TableRecord record;
        record.tag = tag;
        record.checksum = checksum;
        record.offset = offset;
        record.length = length;
        record.padded_end = static_cast<std::uint32_t>(padded_end64);

        font.table_index.emplace(tag, font.tables.size());
        font.tables.push_back(std::move(record));
    }

    std::vector<const TableRecord*> ordered;
    ordered.reserve(font.tables.size());
    for (const TableRecord& record : font.tables) {
        ordered.push_back(&record);
    }
    std::sort(ordered.begin(), ordered.end(),
              [](const TableRecord* lhs, const TableRecord* rhs) {
                  if (lhs->offset != rhs->offset) {
                      return lhs->offset < rhs->offset;
                  }
                  return lhs->length < rhs->length;
              });

    for (std::size_t index = 1; index < ordered.size(); ++index) {
        const TableRecord& previous = *ordered[index - 1];
        const TableRecord& current = *ordered[index];
        // The padded region is part of the table's allocation. Letting a
        // second directory entry start inside it would permit exactly the
        // overlapping-interpretation attack this component is meant to stop.
        if (current.offset < previous.padded_end) {
            fail(ErrorCode::TableOverlap, current.tag, current.offset,
                 "table data (including four-byte padding) overlaps table '" +
                     previous.tag + "'");
        }
    }

    if (options.verify_checksums) {
        for (const TableRecord& record : font.tables) {
            const ByteSpan data =
                ByteSpan(file.data() + record.offset, record.length);
            const std::uint32_t actual =
                tableChecksum(data, record.tag == "head");
            if (actual != record.checksum) {
                char message[96];
                std::snprintf(message, sizeof(message),
                              "table checksum mismatch (stored 0x%08X, "
                              "computed 0x%08X)",
                              record.checksum, actual);
                fail(ErrorCode::ChecksumMismatch, record.tag, record.offset,
                     message);
            }
        }
    }

    if (options.verify_font_checksum) {
        const TableRecord* head = findTable(font, "head");
        if (head == nullptr || head->length < 12) {
            fail(ErrorCode::MissingTable, "head", 0,
                 "whole-font checksum requires a valid head table");
        }
        const std::uint64_t adjust_offset =
            static_cast<std::uint64_t>(head->offset) + 8u;
        const std::uint32_t stored = readU32(file, adjust_offset);
        const std::uint32_t sum =
            wholeFontChecksum(file, static_cast<std::uint32_t>(adjust_offset));
        const std::uint32_t expected = kFontChecksumMagic - sum;
        if (stored != expected) {
            fail(ErrorCode::FontChecksumMismatch, "head", adjust_offset,
                 "head.checkSumAdjustment does not match the font checksum");
        }
    }
}

void parseHead(detail::FontImpl& font, const TableRecord& record) {
    Reader reader{tableView(font, record), record.offset,
                  ErrorCode::InvalidHead, "head"};
    reader.need(0, 54);
    if (reader.u32(0) != kSfntTrueType) {
        fail(ErrorCode::InvalidHead, "head", record.offset,
             "unsupported head table version");
    }
    if (reader.u32(12) != kHeadMagic) {
        fail(ErrorCode::InvalidHead, "head", record.offset + 12,
             "invalid head magic number");
    }
    const std::uint16_t units = reader.u16(18);
    if (units < 16 || units > 16384) {
        fail(ErrorCode::InvalidHead, "head", record.offset + 18,
             "unitsPerEm is outside the OpenType range 16..16384");
    }
    const std::int16_t loc_format = reader.i16(50);
    if (loc_format != 0 && loc_format != 1) {
        fail(ErrorCode::InvalidHead, "head", record.offset + 50,
             "indexToLocFormat must be 0 or 1");
    }
    if (reader.i16(52) != 0) {
        fail(ErrorCode::InvalidHead, "head", record.offset + 52,
             "glyphDataFormat must be zero");
    }
    font.units_per_em = units;
    font.index_to_loc_format = loc_format;
}

void parseMaxp(detail::FontImpl& font, const TableRecord& record) {
    Reader reader{tableView(font, record), record.offset,
                  ErrorCode::InvalidMaxp, "maxp"};
    reader.need(0, 6);
    const std::uint32_t version = reader.u32(0);
    if (version != 0x00005000u && version != kSfntTrueType) {
        fail(ErrorCode::InvalidMaxp, "maxp", record.offset,
             "unsupported maxp version");
    }
    if (version == kSfntTrueType && record.length < 32) {
        fail(ErrorCode::InvalidMaxp, "maxp", record.offset,
             "maxp version 1.0 requires the 32-byte TrueType layout");
    }
    const std::uint16_t glyphs = reader.u16(4);
    if (glyphs == 0) {
        fail(ErrorCode::InvalidMaxp, "maxp", record.offset + 4,
             "numGlyphs must be non-zero");
    }
    font.num_glyphs = glyphs;
}

struct Metrics {
    std::vector<std::int32_t> advance;
    std::vector<std::int32_t> bearing;
};

[[nodiscard]] Metrics parseMetrics(const detail::FontImpl& font) {
    Metrics metrics;
    metrics.advance.assign(font.num_glyphs, 0);
    metrics.bearing.assign(font.num_glyphs, 0);

    const TableRecord* hhea = findTable(font, "hhea");
    const TableRecord* hmtx = findTable(font, "hmtx");
    if (hhea == nullptr || hmtx == nullptr) {
        return metrics;
    }

    Reader hhea_reader{tableView(font, *hhea), hhea->offset,
                       ErrorCode::InvalidHhea, "hhea"};
    hhea_reader.need(0, 36);
    if (hhea_reader.u32(0) != kSfntTrueType) {
        fail(ErrorCode::InvalidHhea, "hhea", hhea->offset,
             "unsupported hhea version");
    }
    const std::uint16_t metric_count = hhea_reader.u16(34);
    if (metric_count == 0 || metric_count > font.num_glyphs) {
        fail(ErrorCode::InvalidHhea, "hhea", hhea->offset + 34,
             "numberOfHMetrics is outside 1..numGlyphs");
    }

    Reader hmtx_reader{tableView(font, *hmtx), hmtx->offset,
                       ErrorCode::InvalidHmtx, "hmtx"};
    const std::uint64_t needed =
        static_cast<std::uint64_t>(metric_count) * 4u +
        static_cast<std::uint64_t>(font.num_glyphs - metric_count) * 2u;
    if (needed > hmtx->length) {
        fail(ErrorCode::InvalidHmtx, "hmtx", hmtx->offset,
             "hmtx is shorter than hhea.numberOfHMetrics and numGlyphs "
             "require");
    }

    std::int32_t last_advance = 0;
    for (std::uint16_t glyph = 0; glyph < font.num_glyphs; ++glyph) {
        if (glyph < metric_count) {
            last_advance = hmtx_reader.u16(static_cast<std::size_t>(glyph) * 4);
            metrics.advance[glyph] = last_advance;
            metrics.bearing[glyph] = hmtx_reader.i16(
                static_cast<std::size_t>(glyph) * 4 + 2);
        } else {
            metrics.advance[glyph] = last_advance;
            const std::size_t offset =
                static_cast<std::size_t>(metric_count) * 4 +
                static_cast<std::size_t>(glyph - metric_count) * 2;
            metrics.bearing[glyph] = hmtx_reader.i16(offset);
        }
    }
    return metrics;
}

[[nodiscard]] std::vector<std::uint32_t> parseLoca(
    const detail::FontImpl& font, const TableRecord& loca,
    std::uint32_t glyf_length) {
    Reader reader{tableView(font, loca), loca.offset,
                  ErrorCode::InvalidLoca, "loca"};
    const std::size_t entry_size = font.index_to_loc_format == 0 ? 2u : 4u;
    const std::uint64_t needed =
        static_cast<std::uint64_t>(font.num_glyphs + 1u) * entry_size;
    if (needed > loca.length) {
        fail(ErrorCode::InvalidLoca, "loca", loca.offset,
             "loca does not contain numGlyphs + 1 offsets");
    }

    std::vector<std::uint32_t> offsets;
    offsets.reserve(static_cast<std::size_t>(font.num_glyphs) + 1u);
    for (std::uint32_t index = 0; index <= font.num_glyphs; ++index) {
        std::uint32_t value = 0;
        if (font.index_to_loc_format == 0) {
            value = static_cast<std::uint32_t>(
                        reader.u16(static_cast<std::size_t>(index) * 2)) *
                    2u;
        } else {
            value = reader.u32(static_cast<std::size_t>(index) * 4);
        }
        offsets.push_back(value);
    }

    if (offsets.front() != 0) {
        fail(ErrorCode::InvalidLoca, "loca", loca.offset,
             "the first loca entry must be zero");
    }
    for (std::size_t index = 1; index < offsets.size(); ++index) {
        if (offsets[index] < offsets[index - 1]) {
            fail(ErrorCode::InvalidLoca, "loca",
                 static_cast<std::uint64_t>(loca.offset) + index * entry_size,
                 "loca offsets must be monotonically non-decreasing");
        }
        if (offsets[index] > glyf_length) {
            fail(ErrorCode::InvalidLoca, "loca",
                 static_cast<std::uint64_t>(loca.offset) + index * entry_size,
                 "loca offset points past the end of glyf");
        }
    }
    return offsets;
}

[[nodiscard]] detail::RawGlyph parseGlyph(ByteSpan glyph,
                                          std::uint64_t absolute_offset,
                                          std::uint16_t num_glyphs,
                                          std::uint16_t glyph_id) {
    (void)glyph_id;
    detail::RawGlyph result;
    if (glyph.empty()) {
        return result;
    }

    Reader reader{glyph, absolute_offset, ErrorCode::InvalidGlyph, "glyf"};
    reader.need(0, 10);
    const std::int16_t contour_count = reader.i16(0);
    result.empty = false;

    if (contour_count >= 0) {
        result.composite = false;
        const std::size_t contours = static_cast<std::size_t>(contour_count);
        const std::size_t endpoints_offset = 10;
        if (contours > (glyph.size() - endpoints_offset) / 2u) {
            fail(ErrorCode::InvalidGlyph, "glyf", absolute_offset,
                 "simple glyph contour endpoints exceed the glyph record");
        }

        std::vector<std::uint16_t> endpoints;
        endpoints.reserve(contours);
        std::uint32_t point_count = 0;
        for (std::size_t index = 0; index < contours; ++index) {
            const std::uint16_t endpoint =
                reader.u16(endpoints_offset + index * 2);
            if (index != 0 && endpoint <= endpoints.back()) {
                fail(ErrorCode::InvalidGlyph, "glyf",
                     absolute_offset + endpoints_offset + index * 2,
                     "simple glyph contours must be non-empty and ordered");
            }
            endpoints.push_back(endpoint);
            point_count = static_cast<std::uint32_t>(endpoint) + 1u;
        }

        const std::size_t instruction_length_offset =
            endpoints_offset + contours * 2;
        if (!rangeFits(instruction_length_offset, 2, glyph.size())) {
            fail(ErrorCode::InvalidGlyph, "glyf", absolute_offset,
                 "simple glyph is missing instructionLength");
        }
        const std::uint16_t instruction_length =
            reader.u16(instruction_length_offset);
        std::size_t cursor = instruction_length_offset + 2;
        if (!rangeFits(cursor, instruction_length, glyph.size())) {
            fail(ErrorCode::InvalidGlyph, "glyf", absolute_offset + cursor,
                 "simple glyph instructions exceed the glyph record");
        }
        cursor += instruction_length;

        std::vector<std::uint8_t> flags(point_count, 0);
        std::uint32_t point = 0;
        while (point < point_count) {
            if (cursor >= glyph.size()) {
                fail(ErrorCode::InvalidGlyph, "glyf", absolute_offset + cursor,
                     "simple glyph flag array is truncated");
            }
            const std::uint8_t flag = reader.u8(cursor++);
            std::uint32_t repeat = 1;
            if ((flag & 0x08u) != 0u) {
                if (cursor >= glyph.size()) {
                    fail(ErrorCode::InvalidGlyph, "glyf",
                         absolute_offset + cursor,
                         "simple glyph repeat count is truncated");
                }
                repeat += reader.u8(cursor++);
            }
            if (point + repeat > point_count) {
                fail(ErrorCode::InvalidGlyph, "glyf", absolute_offset + cursor,
                     "simple glyph flags describe too many points");
            }
            for (std::uint32_t copy = 0; copy < repeat; ++copy) {
                flags[point + copy] = flag;
            }
            point += repeat;
        }

        std::vector<GlyphPoint> points(point_count);
        std::int64_t current = 0;
        for (std::uint32_t index = 0; index < point_count; ++index) {
            const std::uint8_t flag = flags[index];
            std::int64_t delta = 0;
            if ((flag & 0x02u) != 0u) {
                delta = reader.u8(cursor++);
                if ((flag & 0x10u) == 0u) {
                    delta = -delta;
                }
            } else if ((flag & 0x10u) == 0u) {
                delta = reader.i16(cursor);
                cursor += 2;
            }
            current += delta;
            if (current < std::numeric_limits<std::int32_t>::min() ||
                current > std::numeric_limits<std::int32_t>::max()) {
                fail(ErrorCode::InvalidGlyph, "glyf", absolute_offset,
                     "simple glyph x coordinate overflows 32 bits");
            }
            points[index].x = static_cast<double>(current);
            points[index].on_curve = (flag & 0x01u) != 0u;
        }

        current = 0;
        for (std::uint32_t index = 0; index < point_count; ++index) {
            const std::uint8_t flag = flags[index];
            std::int64_t delta = 0;
            if ((flag & 0x04u) != 0u) {
                delta = reader.u8(cursor++);
                if ((flag & 0x20u) == 0u) {
                    delta = -delta;
                }
            } else if ((flag & 0x20u) == 0u) {
                delta = reader.i16(cursor);
                cursor += 2;
            }
            current += delta;
            if (current < std::numeric_limits<std::int32_t>::min() ||
                current > std::numeric_limits<std::int32_t>::max()) {
                fail(ErrorCode::InvalidGlyph, "glyf", absolute_offset,
                     "simple glyph y coordinate overflows 32 bits");
            }
            points[index].y = static_cast<double>(current);
        }

        std::size_t start = 0;
        for (const std::uint16_t endpoint : endpoints) {
            Contour contour;
            contour.points.assign(points.begin() + start,
                                  points.begin() + endpoint + 1);
            result.contours.push_back(std::move(contour));
            start = static_cast<std::size_t>(endpoint) + 1;
        }
        return result;
    }

    result.composite = true;
    if (contour_count != -1) {
        fail(ErrorCode::InvalidGlyph, "glyf", absolute_offset,
             "negative numberOfContours must be -1 for a composite glyph");
    }
    std::size_t cursor = 10;
    std::uint16_t flags = 0;
    std::size_t component_count = 0;
    do {
        if (++component_count > 4096) {
            fail(ErrorCode::InvalidGlyph, "glyf", absolute_offset + cursor,
                 "composite glyph has too many components");
        }
        reader.need(cursor, 4);
        flags = reader.u16(cursor);
        const std::uint16_t component_glyph = reader.u16(cursor + 2);
        cursor += 4;
        if (component_glyph >= num_glyphs) {
            fail(ErrorCode::GlyphIndexOutOfRange, "glyf",
                 absolute_offset + cursor - 2,
                 "composite component references a glyph outside numGlyphs");
        }

        detail::RawComponent component;
        component.glyph = component_glyph;
        component.args_are_xy = (flags & 0x0002u) != 0u;
        component.use_my_metrics = (flags & 0x0200u) != 0u;

        if ((flags & 0x0001u) != 0u) {
            reader.need(cursor, 4);
            if (component.args_are_xy) {
                component.arg1 = reader.i16(cursor);
                component.arg2 = reader.i16(cursor + 2);
            } else {
                component.arg1 = reader.u16(cursor);
                component.arg2 = reader.u16(cursor + 2);
            }
            cursor += 4;
        } else {
            reader.need(cursor, 2);
            if (component.args_are_xy) {
                component.arg1 = reader.i8(cursor);
                component.arg2 = reader.i8(cursor + 1);
            } else {
                component.arg1 = reader.u8(cursor);
                component.arg2 = reader.u8(cursor + 1);
            }
            cursor += 2;
        }

        const unsigned transform_flags =
            (flags & 0x0008u ? 1u : 0u) | (flags & 0x0040u ? 1u : 0u) |
            (flags & 0x0080u ? 1u : 0u);
        if (transform_flags > 1u) {
            fail(ErrorCode::InvalidGlyph, "glyf", absolute_offset + cursor,
                 "composite glyph has conflicting transform flags");
        }
        if ((flags & 0x0008u) != 0u) {
            reader.need(cursor, 2);
            const double scale = static_cast<double>(reader.i16(cursor)) /
                                 16384.0;
            component.a = scale;
            component.d = scale;
            cursor += 2;
        } else if ((flags & 0x0040u) != 0u) {
            reader.need(cursor, 4);
            component.a =
                static_cast<double>(reader.i16(cursor)) / 16384.0;
            component.d =
                static_cast<double>(reader.i16(cursor + 2)) / 16384.0;
            cursor += 4;
        } else if ((flags & 0x0080u) != 0u) {
            reader.need(cursor, 8);
            component.a =
                static_cast<double>(reader.i16(cursor)) / 16384.0;
            component.b =
                static_cast<double>(reader.i16(cursor + 2)) / 16384.0;
            component.c =
                static_cast<double>(reader.i16(cursor + 4)) / 16384.0;
            component.d =
                static_cast<double>(reader.i16(cursor + 6)) / 16384.0;
            cursor += 8;
        }

        result.components.push_back(component);
        if ((flags & 0x0020u) == 0u) {
            break;
        }
    } while (true);

    if ((flags & 0x0100u) != 0u) {
        reader.need(cursor, 2);
        const std::uint16_t instruction_length = reader.u16(cursor);
        cursor += 2;
        if (!rangeFits(cursor, instruction_length, glyph.size())) {
            fail(ErrorCode::InvalidGlyph, "glyf", absolute_offset + cursor,
                 "composite instruction data exceeds the glyph record");
        }
        cursor += instruction_length;
    }

    return result;
}

void requireTable(const detail::FontImpl& font, std::string_view tag) {
    if (findTable(font, tag) == nullptr) {
        fail(ErrorCode::MissingTable, tag, 0,
             "required table is missing");
    }
}

struct CmapSubtableInfo {
    std::uint16_t format = 0;
    std::uint32_t extent = 0;
};

[[nodiscard]] CmapSubtableInfo cmapSubtableInfo(ByteSpan cmap,
                                                std::uint32_t offset,
                                                std::uint64_t absolute_base) {
    if (offset > cmap.size() || cmap.size() - offset < 2) {
        fail(ErrorCode::CmapOutOfBounds, "cmap",
             absolute_base + offset,
             "encoding record points outside the cmap table");
    }
    const std::uint16_t format = readU16(cmap, offset);
    std::uint32_t length = 0;
    switch (format) {
        case 0:
        case 2:
        case 4:
        case 6:
            if (cmap.size() - offset < 4) {
                fail(ErrorCode::CmapOutOfBounds, "cmap",
                     absolute_base + offset,
                     "truncated 16-bit cmap subtable header");
            }
            length = readU16(cmap, offset + 2);
            break;
        case 8:
        case 10:
        case 12:
        case 13:
            if (cmap.size() - offset < 8) {
                fail(ErrorCode::CmapOutOfBounds, "cmap",
                     absolute_base + offset,
                     "truncated 32-bit cmap subtable header");
            }
            length = readU32(cmap, offset + 4);
            break;
        case 14:
            if (cmap.size() - offset < 6) {
                fail(ErrorCode::CmapOutOfBounds, "cmap",
                     absolute_base + offset,
                     "truncated format 14 cmap subtable header");
            }
            length = readU32(cmap, offset + 2);
            break;
        default:
            fail(ErrorCode::UnsupportedCmapFormat, "cmap",
                 absolute_base + offset,
                 "unknown cmap subtable format");
    }
    if (length == 0 || length > cmap.size() - offset) {
        fail(ErrorCode::CmapOutOfBounds, "cmap", absolute_base + offset,
             "cmap subtable length is zero or exceeds the cmap table");
    }
    return CmapSubtableInfo{format, length};
}

void validateCmapGlyph(std::uint32_t glyph, std::uint16_t num_glyphs,
                       std::uint64_t absolute_offset,
                       std::string_view reason) {
    if (glyph == 0) {
        return;
    }
    if (glyph >= num_glyphs) {
        fail(ErrorCode::CmapOutOfBounds, "cmap", absolute_offset,
             std::string(reason) +
                 ": glyph id is outside maxp.numGlyphs");
    }
}

[[nodiscard]] std::vector<CmapRange> parseCmapSubtable(
    ByteSpan cmap, const CmapSubtableInfo& info, std::uint64_t absolute_base,
    std::uint16_t num_glyphs) {
    std::vector<CmapRange> ranges;
    if (info.format == 2 || info.format == 8) {
        // These legacy formats are structurally bounded here but are not
        // candidates for the canonical Unicode map. They cannot be used to
        // smuggle a second interpretation into the normalized tree.
        return ranges;
    }

    const ByteSpan sub = cmap;
    const std::uint64_t base = absolute_base;
    auto add_mapping = [&](std::uint32_t codepoint, std::uint32_t glyph) {
        if (glyph == 0) {
            return;
        }
        if (glyph >= num_glyphs) {
            fail(ErrorCode::InvalidCmapGlyph, "cmap", base,
                 "cmap maps a character to a glyph outside numGlyphs");
        }
        ranges.push_back(CmapRange{codepoint, codepoint, glyph, false});
    };

    if (info.format == 0) {
        Reader reader{sub, base, ErrorCode::InvalidCmap, "cmap"};
        reader.need(0, 262);
        for (std::uint32_t codepoint = 0; codepoint < 256; ++codepoint) {
            const std::uint32_t glyph = reader.u8(6 + codepoint);
            add_mapping(codepoint, glyph);
        }
        return ranges;
    }

    if (info.format == 4) {
        Reader reader{sub, base, ErrorCode::InvalidCmap, "cmap"};
        reader.need(0, 16);
        const std::uint16_t seg_count_x2 = reader.u16(6);
        if ((seg_count_x2 & 1u) != 0u || seg_count_x2 == 0u) {
            fail(ErrorCode::InvalidCmap, "cmap", base + 6,
                 "format 4 segCountX2 is invalid");
        }
        const std::size_t seg_count = seg_count_x2 / 2u;
        const std::size_t end_codes = 14;
        const std::size_t start_codes = end_codes + seg_count * 2u + 2u;
        const std::size_t deltas = start_codes + seg_count * 2u;
        const std::size_t range_offsets = deltas + seg_count * 2u;
        const std::size_t arrays_end = range_offsets + seg_count * 2u;
        if (arrays_end > sub.size()) {
            fail(ErrorCode::CmapOutOfBounds, "cmap", base,
                 "format 4 segment arrays exceed the subtable length");
        }

        std::uint32_t previous_end = 0;
        bool have_previous = false;
        for (std::size_t segment = 0; segment < seg_count; ++segment) {
            const std::uint32_t start = reader.u16(start_codes + segment * 2);
            const std::uint32_t end = reader.u16(end_codes + segment * 2);
            const std::int32_t delta =
                reader.i16(deltas + segment * 2);
            const std::uint16_t range_offset =
                reader.u16(range_offsets + segment * 2);
            if (start > end) {
                fail(ErrorCode::InvalidCmap, "cmap",
                     base + start_codes + segment * 2,
                     "format 4 segment start is greater than its end");
            }
            if (have_previous && start <= previous_end) {
                fail(ErrorCode::InvalidCmap, "cmap",
                     base + start_codes + segment * 2,
                     "format 4 segments overlap or are out of order");
            }
            previous_end = end;
            have_previous = true;
            if ((range_offset & 1u) != 0u) {
                fail(ErrorCode::InvalidCmap, "cmap",
                     base + range_offsets + segment * 2,
                     "format 4 idRangeOffset is not even");
            }

            for (std::uint32_t cp = start; cp <= end; ++cp) {
                std::uint32_t glyph = 0;
                if (range_offset == 0) {
                    glyph = static_cast<std::uint16_t>(
                        static_cast<std::int32_t>(cp) + delta);
                } else {
                    const std::uint64_t address =
                        static_cast<std::uint64_t>(
                            range_offsets + segment * 2) +
                        range_offset + (cp - start) * 2u;
                    if (address + 2u > sub.size()) {
                        fail(ErrorCode::CmapOutOfBounds, "cmap",
                             base + address,
                             "format 4 glyphIdArray index is outside the "
                             "subtable");
                    }
                    const std::uint16_t raw = readU16(sub, address);
                    if (raw != 0) {
                        glyph = static_cast<std::uint16_t>(
                            static_cast<std::int32_t>(raw) + delta);
                    }
                }
                add_mapping(cp, glyph);
            }
        }
        return ranges;
    }

    if (info.format == 6) {
        Reader reader{sub, base, ErrorCode::InvalidCmap, "cmap"};
        reader.need(0, 10);
        const std::uint32_t first = reader.u16(6);
        const std::uint32_t count = reader.u16(8);
        if (static_cast<std::uint64_t>(first) + count > 0x10000u) {
            fail(ErrorCode::InvalidCmap, "cmap", base,
                 "format 6 character range exceeds the BMP");
        }
        if (10u + static_cast<std::uint64_t>(count) * 2u > sub.size()) {
            fail(ErrorCode::CmapOutOfBounds, "cmap", base,
                 "format 6 glyph array exceeds the subtable length");
        }
        for (std::uint32_t index = 0; index < count; ++index) {
            add_mapping(first + index, reader.u16(10 + index * 2));
        }
        return ranges;
    }

    if (info.format == 10) {
        Reader reader{sub, base, ErrorCode::InvalidCmap, "cmap"};
        reader.need(0, 20);
        const std::uint32_t first = reader.u32(12);
        const std::uint32_t count = reader.u32(16);
        const std::uint64_t last = static_cast<std::uint64_t>(first) + count;
        if (last > 0x110000u || 20u + static_cast<std::uint64_t>(count) * 2u >
                                  sub.size()) {
            fail(ErrorCode::CmapOutOfBounds, "cmap", base,
                 "format 10 character range exceeds Unicode or the subtable");
        }
        for (std::uint32_t index = 0; index < count; ++index) {
            add_mapping(first + index, reader.u16(20 + index * 2));
        }
        return ranges;
    }

    if (info.format == 12 || info.format == 13) {
        Reader reader{sub, base, ErrorCode::InvalidCmap, "cmap"};
        reader.need(0, 16);
        const std::uint32_t group_count = reader.u32(12);
        if (16u + static_cast<std::uint64_t>(group_count) * 12u >
            sub.size()) {
            fail(ErrorCode::CmapOutOfBounds, "cmap", base,
                 "format 12/13 groups exceed the subtable length");
        }
        std::uint32_t previous_end = 0;
        bool have_previous = false;
        for (std::uint32_t group = 0; group < group_count; ++group) {
            const std::size_t entry = 16 + static_cast<std::size_t>(group) * 12;
            const std::uint32_t start = reader.u32(entry);
            const std::uint32_t end = reader.u32(entry + 4);
            const std::uint32_t glyph = reader.u32(entry + 8);
            if (start > end || end > 0x10FFFFu) {
                fail(ErrorCode::InvalidCmap, "cmap", base + entry,
                     "format 12/13 group has an invalid character range");
            }
            if (have_previous && start <= previous_end) {
                fail(ErrorCode::InvalidCmap, "cmap", base + entry,
                     "format 12/13 groups overlap or are out of order");
            }
            previous_end = end;
            have_previous = true;

            if (info.format == 12) {
                const std::uint64_t last_glyph =
                    static_cast<std::uint64_t>(glyph) + (end - start);
                if (last_glyph >= num_glyphs) {
                    fail(ErrorCode::InvalidCmapGlyph, "cmap", base + entry,
                         "format 12 group maps past maxp.numGlyphs");
                }
                ranges.push_back(CmapRange{start, end, glyph, false});
            } else {
                if (glyph >= num_glyphs && glyph != 0) {
                    fail(ErrorCode::InvalidCmapGlyph, "cmap", base + entry,
                         "format 13 constant glyph is outside numGlyphs");
                }
                if (glyph != 0) {
                    ranges.push_back(CmapRange{start, end, glyph, true});
                }
            }
        }
        return ranges;
    }

    if (info.format == 14) {
        Reader reader{sub, base, ErrorCode::InvalidCmap, "cmap"};
        reader.need(0, 10);
        const std::uint32_t record_count = reader.u32(6);
        if (10u + static_cast<std::uint64_t>(record_count) * 11u >
            sub.size()) {
            fail(ErrorCode::CmapOutOfBounds, "cmap", base,
                 "format 14 variation selector records are truncated");
        }
        for (std::uint32_t record = 0; record < record_count; ++record) {
            const std::size_t entry = 10 + static_cast<std::size_t>(record) * 11;
            const std::uint32_t selector = reader.u24(entry);
            (void)selector;
            const std::uint32_t default_offset = reader.u32(entry + 3);
            const std::uint32_t non_default_offset = reader.u32(entry + 7);

            if (default_offset != 0) {
                if (default_offset > sub.size() ||
                    sub.size() - default_offset < 4) {
                    fail(ErrorCode::CmapOutOfBounds, "cmap",
                         base + default_offset,
                         "format 14 default UVS table is truncated");
                }
                const std::uint32_t ranges_count =
                    reader.u32(default_offset);
                if (default_offset + 4u +
                        static_cast<std::uint64_t>(ranges_count) * 4u >
                    sub.size()) {
                    fail(ErrorCode::CmapOutOfBounds, "cmap",
                         base + default_offset,
                         "format 14 default UVS ranges are truncated");
                }
                std::uint32_t previous = 0;
                bool have_previous = false;
                for (std::uint32_t item = 0; item < ranges_count; ++item) {
                    const std::size_t range =
                        default_offset + 4u + static_cast<std::size_t>(item) * 4u;
                    const std::uint32_t start = reader.u24(range);
                    const std::uint32_t additional = reader.u8(range + 3);
                    const std::uint64_t end =
                        static_cast<std::uint64_t>(start) + additional;
                    if (start > 0x10FFFFu || end > 0x10FFFFu ||
                        (have_previous && start <= previous)) {
                        fail(ErrorCode::InvalidCmap, "cmap", base + range,
                             "format 14 default UVS range is invalid or "
                             "overlaps");
                    }
                    previous = static_cast<std::uint32_t>(end);
                    have_previous = true;
                }
            }

            if (non_default_offset != 0) {
                if (non_default_offset > sub.size() ||
                    sub.size() - non_default_offset < 4) {
                    fail(ErrorCode::CmapOutOfBounds, "cmap",
                         base + non_default_offset,
                         "format 14 non-default UVS table is truncated");
                }
                const std::uint32_t mappings_count =
                    reader.u32(non_default_offset);
                if (non_default_offset + 4u +
                        static_cast<std::uint64_t>(mappings_count) * 5u >
                    sub.size()) {
                    fail(ErrorCode::CmapOutOfBounds, "cmap",
                         base + non_default_offset,
                         "format 14 non-default UVS mappings are truncated");
                }
                std::uint32_t previous = 0;
                bool have_previous = false;
                for (std::uint32_t item = 0; item < mappings_count; ++item) {
                    const std::size_t mapping =
                        non_default_offset + 4u +
                        static_cast<std::size_t>(item) * 5u;
                    const std::uint32_t codepoint = reader.u24(mapping);
                    const std::uint16_t glyph = reader.u16(mapping + 3);
                    if (codepoint > 0x10FFFFu ||
                        (have_previous && codepoint <= previous)) {
                        fail(ErrorCode::InvalidCmap, "cmap", base + mapping,
                             "format 14 non-default UVS mapping is invalid "
                             "or overlaps");
                    }
                    validateCmapGlyph(glyph, num_glyphs, base + mapping,
                                      "format 14 mapping");
                    previous = codepoint;
                    have_previous = true;
                }
            }
        }
        return ranges;
    }

    fail(ErrorCode::UnsupportedCmapFormat, "cmap", base,
         "unsupported cmap subtable format");
}

[[nodiscard]] int cmapScore(std::uint16_t platform, std::uint16_t encoding,
                            std::uint16_t format) {
    int score = 0;
    if (platform == 3 && encoding == 10) {
        score = 1000;
    } else if (platform == 0 && encoding == 6) {
        score = 950;
    } else if (platform == 0 && encoding == 4) {
        score = 900;
    } else if (platform == 3 && encoding == 1) {
        score = 850;
    } else if (platform == 0) {
        score = 800;
    } else if (platform == 3 && encoding == 0) {
        score = 500;
    } else if (platform == 1) {
        score = 100;
    }
    if (format == 12) {
        score += 40;
    } else if (format == 13) {
        score += 30;
    } else if (format == 10) {
        score += 25;
    } else if (format == 4) {
        score += 20;
    } else if (format == 6) {
        score += 10;
    } else if (format == 0) {
        score += 5;
    }
    return score;
}

[[nodiscard]] std::vector<CmapRange> compactRanges(
    std::vector<CmapRange> ranges, std::string_view table,
    std::uint64_t base_offset) {
    if (ranges.empty()) {
        return ranges;
    }
    std::sort(ranges.begin(), ranges.end(),
              [](const CmapRange& lhs, const CmapRange& rhs) {
                  if (lhs.first != rhs.first) {
                      return lhs.first < rhs.first;
                  }
                  return lhs.last < rhs.last;
              });

    std::vector<CmapRange> result;
    result.reserve(ranges.size());
    for (const CmapRange& current : ranges) {
        if (current.first > current.last || current.last > 0x10FFFFu) {
            fail(ErrorCode::InvalidCmap, table, base_offset,
                 "normalized cmap range is invalid");
        }
        if (!result.empty()) {
            CmapRange& previous = result.back();
            if (static_cast<std::uint64_t>(current.first) <=
                static_cast<std::uint64_t>(previous.last)) {
                fail(ErrorCode::CmapOverlap, table, base_offset,
                     "cmap subtables or ranges map the same character twice");
            }
            const bool adjacent =
                static_cast<std::uint64_t>(current.first) ==
                static_cast<std::uint64_t>(previous.last) + 1u;
            if (adjacent) {
                if (!previous.constant && !current.constant) {
                    const std::uint64_t expected =
                        static_cast<std::uint64_t>(previous.glyph) +
                        (previous.last - previous.first) + 1u;
                    if (expected == current.glyph) {
                        previous.last = current.last;
                        continue;
                    }
                } else if (previous.constant && current.constant &&
                           previous.glyph == current.glyph) {
                    previous.last = current.last;
                    continue;
                }
            }
        }
        result.push_back(current);
    }
    return result;
}

[[nodiscard]] std::vector<CmapRange> parseCmap(
    const detail::FontImpl& font, const TableRecord& record) {
    Reader reader{tableView(font, record), record.offset,
                  ErrorCode::InvalidCmap, "cmap"};
    reader.need(0, 4);
    if (reader.u16(0) != 0) {
        fail(ErrorCode::InvalidCmap, "cmap", record.offset,
             "cmap version must be zero");
    }
    const std::uint16_t count = reader.u16(2);
    const std::size_t header_size = 4u + static_cast<std::size_t>(count) * 8u;
    if (header_size > record.length) {
        fail(ErrorCode::InvalidCmap, "cmap", record.offset,
             "cmap encoding records exceed the table");
    }

    struct EncodingRecord {
        std::uint16_t platform = 0;
        std::uint16_t encoding = 0;
        std::uint32_t offset = 0;
        std::uint16_t format = 0;
        bool supported = false;
        bool variation = false;
        std::size_t parsed_index = 0;
        int score = 0;
    };
    struct ParsedSubtable {
        std::uint16_t format = 0;
        bool supported = false;
        std::vector<CmapRange> ranges;
    };

    std::vector<EncodingRecord> records;
    records.reserve(count);
    std::map<std::uint32_t, std::uint32_t> extent_by_offset;
    std::map<std::pair<std::uint16_t, std::uint16_t>, std::uint32_t>
        encoding_offsets;

    for (std::uint16_t index = 0; index < count; ++index) {
        const std::size_t entry = 4u + static_cast<std::size_t>(index) * 8u;
        EncodingRecord encoding;
        encoding.platform = reader.u16(entry);
        encoding.encoding = reader.u16(entry + 2);
        encoding.offset = reader.u32(entry + 4);

        if (encoding.offset < header_size ||
            encoding.offset > record.length) {
            fail(ErrorCode::CmapOutOfBounds, "cmap",
                 static_cast<std::uint64_t>(record.offset) + encoding.offset,
                 "cmap subtable offset is outside the cmap body");
        }
        const auto duplicate =
            encoding_offsets.emplace(
                std::make_pair(encoding.platform, encoding.encoding),
                encoding.offset);
        if (!duplicate.second && duplicate.first->second != encoding.offset) {
            fail(ErrorCode::InvalidCmap, "cmap",
                 static_cast<std::uint64_t>(record.offset) + encoding.offset,
                 "duplicate platform/encoding records select different cmap "
                 "subtables");
        }

        const CmapSubtableInfo info = cmapSubtableInfo(
            tableView(font, record), encoding.offset, record.offset);
        encoding.format = info.format;
        encoding.variation = info.format == 14;
        encoding.supported = info.format == 0 || info.format == 4 ||
                             info.format == 6 || info.format == 10 ||
                             info.format == 12 || info.format == 13;
        encoding.score = encoding.supported
                             ? cmapScore(encoding.platform, encoding.encoding,
                                         info.format)
                             : -1;

        extent_by_offset[encoding.offset] = info.extent;
        records.push_back(encoding);
    }

    std::vector<std::pair<std::uint32_t, std::uint32_t>> intervals;
    intervals.reserve(extent_by_offset.size());
    for (const auto& item : extent_by_offset) {
        intervals.emplace_back(item.first, item.second);
    }
    std::sort(intervals.begin(), intervals.end());
    for (std::size_t index = 1; index < intervals.size(); ++index) {
        const std::uint64_t previous_end =
            static_cast<std::uint64_t>(intervals[index - 1].first) +
            intervals[index - 1].second;
        if (intervals[index].first < previous_end &&
            intervals[index].first != intervals[index - 1].first) {
            fail(ErrorCode::CmapOverlap, "cmap",
                 static_cast<std::uint64_t>(record.offset) +
                     intervals[index].first,
                 "cmap subtables overlap, permitting two interpretations");
        }
    }

    // Only after the physical subtable intervals are known to be disjoint
    // is any subtable parsed. This prevents a malformed or alternate table
    // from influencing the normalized result before overlap is rejected.
    std::map<std::uint32_t, std::size_t> parsed_by_offset;
    std::vector<ParsedSubtable> parsed;
    for (EncodingRecord& encoding : records) {
        const auto existing = parsed_by_offset.find(encoding.offset);
        if (existing == parsed_by_offset.end()) {
            ParsedSubtable subtable;
            subtable.format = encoding.format;
            subtable.supported = encoding.supported;
            if (encoding.supported) {
                const CmapSubtableInfo info{
                    encoding.format, extent_by_offset[encoding.offset]};
                const ByteSpan sub = tableView(font, record).subspan(
                    encoding.offset, info.extent);
                subtable.ranges = parseCmapSubtable(
                    sub, info,
                    static_cast<std::uint64_t>(record.offset) +
                        encoding.offset,
                    font.num_glyphs);
            }
            encoding.parsed_index = parsed.size();
            parsed.push_back(std::move(subtable));
            parsed_by_offset.emplace(encoding.offset, encoding.parsed_index);
        } else {
            encoding.parsed_index = existing->second;
            // A shared offset is permitted, but its format must be the same.
            if (parsed[encoding.parsed_index].format != encoding.format) {
                fail(ErrorCode::InvalidCmap, "cmap",
                     static_cast<std::uint64_t>(record.offset) + encoding.offset,
                     "shared cmap offset has inconsistent formats");
            }
        }
    }

    std::optional<std::size_t> chosen;
    int best_score = std::numeric_limits<int>::min();
    for (const EncodingRecord& encoding : records) {
        if (!encoding.supported) {
            continue;
        }
        if (!chosen || encoding.score > best_score) {
            chosen = encoding.parsed_index;
            best_score = encoding.score;
        }
    }
    if (!chosen) {
        fail(ErrorCode::UnsupportedCmapFormat, "cmap", record.offset,
             "no supported Unicode cmap subtable is available");
    }

    return compactRanges(std::move(parsed[*chosen].ranges), "cmap",
                         record.offset);
}

[[nodiscard]] bool pointAt(const std::vector<Contour>& contours,
                           std::size_t index, GlyphPoint& point) {
    std::size_t current = 0;
    for (const Contour& contour : contours) {
        if (index < current + contour.points.size()) {
            point = contour.points[index - current];
            return true;
        }
        current += contour.points.size();
    }
    return false;
}

[[nodiscard]] std::vector<GlyphOutline> resolveGlyphs(
    const std::vector<detail::RawGlyph>& raw, std::uint16_t num_glyphs,
    const Metrics& metrics, std::size_t max_depth) {
    std::vector<GlyphOutline> outlines(num_glyphs);
    std::vector<std::uint8_t> state(num_glyphs, 0);  // 0 white, 1 gray, 2 black

    std::function<void(std::uint16_t, std::size_t)> resolve =
        [&](std::uint16_t glyph_id, std::size_t depth) {
            if (glyph_id >= num_glyphs) {
                fail(ErrorCode::GlyphIndexOutOfRange, "glyf", 0,
                     "glyph index is outside numGlyphs");
            }
            if (state[glyph_id] == 2) {
                return;
            }
            if (state[glyph_id] == 1) {
                fail(ErrorCode::CompositeCycle, "glyf", 0,
                     "composite glyph reference cycle detected");
            }
            if (depth > max_depth) {
                fail(ErrorCode::CompositeDepthExceeded, "glyf", 0,
                     "composite glyph nesting exceeds the configured limit");
            }
            state[glyph_id] = 1;

            const detail::RawGlyph& record = raw[glyph_id];
            GlyphOutline& outline = outlines[glyph_id];
            outline.advance_width = metrics.advance[glyph_id];
            outline.left_side_bearing = metrics.bearing[glyph_id];
            outline.composite = record.composite;

            if (!record.composite) {
                outline.contours = record.contours;
            } else {
                for (const detail::RawComponent& component :
                     record.components) {
                    resolve(component.glyph, depth + 1);
                    const GlyphOutline& child = outlines[component.glyph];

                    std::vector<Contour> transformed = child.contours;
                    for (Contour& contour : transformed) {
                        for (GlyphPoint& point : contour.points) {
                            const double x =
                                component.a * point.x + component.c * point.y;
                            const double y =
                                component.b * point.x + component.d * point.y;
                            point.x = x;
                            point.y = y;
                        }
                    }

                    double offset_x = 0.0;
                    double offset_y = 0.0;
                    if (component.args_are_xy) {
                        offset_x = static_cast<double>(component.arg1);
                        offset_y = static_cast<double>(component.arg2);
                    } else {
                        const std::uint32_t parent_index =
                            static_cast<std::uint32_t>(component.arg1);
                        const std::uint32_t child_index =
                            static_cast<std::uint32_t>(component.arg2);
                        GlyphPoint parent_point;
                        GlyphPoint child_point;
                        if (!pointAt(outline.contours, parent_index,
                                     parent_point)) {
                            fail(ErrorCode::InvalidGlyph, "glyf", 0,
                                 "composite point-matching parent index is "
                                 "outside the composite outline");
                        }
                        if (!pointAt(transformed, child_index, child_point)) {
                            fail(ErrorCode::InvalidGlyph, "glyf", 0,
                                 "composite point-matching component index "
                                 "is outside the component outline");
                        }
                        offset_x = parent_point.x - child_point.x;
                        offset_y = parent_point.y - child_point.y;
                    }
                    for (Contour& contour : transformed) {
                        for (GlyphPoint& point : contour.points) {
                            point.x += offset_x;
                            point.y += offset_y;
                        }
                    }
                    for (Contour& contour : transformed) {
                        outline.contours.push_back(std::move(contour));
                    }
                    if (component.use_my_metrics) {
                        outline.advance_width = child.advance_width;
                        outline.left_side_bearing = child.left_side_bearing;
                    }
                }
            }
            state[glyph_id] = 2;
        };

    for (std::uint16_t glyph = 0; glyph < num_glyphs; ++glyph) {
        resolve(glyph, 0);
    }
    return outlines;
}

void parseFontImpl(ByteSpan input, const ValidationOptions& options,
                   detail::FontImpl& font) {
    font.file.assign(input.begin(), input.end());
    const ByteSpan file(font.file.data(), font.file.size());

    validateDirectory(file, options, font);
    requireTable(font, "head");
    requireTable(font, "maxp");
    requireTable(font, "cmap");

    const bool requires_glyf = font.sfnt_version == kSfntTrueType ||
                               font.sfnt_version == kSfntTrue;
    const bool has_glyf = findTable(font, "glyf") != nullptr;
    const bool has_loca = findTable(font, "loca") != nullptr;
    if (requires_glyf && !has_glyf) {
        fail(ErrorCode::MissingTable, "glyf", 0,
             "a TrueType font requires a glyf table");
    }
    if (has_glyf != has_loca) {
        fail(ErrorCode::MissingTable, has_glyf ? "loca" : "glyf", 0,
             "glyf and loca must be present together");
    }

    const TableRecord& head = *findTable(font, "head");
    parseHead(font, head);
    const TableRecord& maxp = *findTable(font, "maxp");
    parseMaxp(font, maxp);
    const Metrics metrics = parseMetrics(font);
    font.cmap = parseCmap(font, *findTable(font, "cmap"));

    if (has_glyf) {
        const TableRecord& loca = *findTable(font, "loca");
        const TableRecord& glyf = *findTable(font, "glyf");
        font.loca = parseLoca(font, loca, glyf.length);

        std::vector<detail::RawGlyph> raw(font.num_glyphs);
        const ByteSpan glyf_view = tableView(font, glyf);
        for (std::uint16_t glyph = 0; glyph < font.num_glyphs; ++glyph) {
            const std::uint32_t start = font.loca[glyph];
            const std::uint32_t end = font.loca[glyph + 1u];
            const ByteSpan record = glyf_view.subspan(start, end - start);
            raw[glyph] = parseGlyph(
                record, static_cast<std::uint64_t>(glyf.offset) + start,
                font.num_glyphs, glyph);
        }
        font.glyphs = resolveGlyphs(raw, font.num_glyphs, metrics,
                                    options.max_composite_depth);
    } else {
        font.glyphs.assign(font.num_glyphs, GlyphOutline{});
        for (std::uint16_t glyph = 0; glyph < font.num_glyphs; ++glyph) {
            font.glyphs[glyph].advance_width = metrics.advance[glyph];
            font.glyphs[glyph].left_side_bearing = metrics.bearing[glyph];
        }
    }
}

[[nodiscard]] double mappedCoordinate(double value, double scale,
                                      bool flip_y) {
    const double scaled = value * scale;
    return flip_y ? -scaled : scaled;
}

void appendContourPath(const Contour& contour, double scale, bool flip_y,
                       std::vector<PathCommand>& path) {
    if (contour.points.empty()) {
        return;
    }
    const std::size_t count = contour.points.size();

    std::optional<std::size_t> start;
    for (std::size_t index = 0; index < count; ++index) {
        if (contour.points[index].on_curve) {
            start = index;
            break;
        }
    }

    std::vector<GlyphPoint> sequence;
    if (start) {
        sequence.reserve(count + count / 2 + 1);
        sequence.push_back(contour.points[*start]);
        GlyphPoint previous = contour.points[*start];
        for (std::size_t step = 1; step < count; ++step) {
            const GlyphPoint& current =
                contour.points[(*start + step) % count];
            if (!previous.on_curve && !current.on_curve) {
                sequence.push_back(GlyphPoint{
                    (previous.x + current.x) / 2.0,
                    (previous.y + current.y) / 2.0, true});
            }
            sequence.push_back(current);
            previous = current;
        }
    } else {
        sequence.reserve(count + count / 2 + 1);
        const GlyphPoint& first = contour.points.front();
        const GlyphPoint& last = contour.points.back();
        const GlyphPoint synthetic{(last.x + first.x) / 2.0,
                                   (last.y + first.y) / 2.0, true};
        sequence.push_back(synthetic);
        GlyphPoint previous = first;
        sequence.push_back(first);
        for (std::size_t index = 1; index < count; ++index) {
            const GlyphPoint& current = contour.points[index];
            if (!previous.on_curve && !current.on_curve) {
                sequence.push_back(GlyphPoint{
                    (previous.x + current.x) / 2.0,
                    (previous.y + current.y) / 2.0, true});
            }
            sequence.push_back(current);
            previous = current;
        }
    }

    PathCommand move;
    move.verb = PathVerb::MoveTo;
    move.x1 = mappedCoordinate(sequence.front().x, scale, flip_y);
    move.y1 = mappedCoordinate(sequence.front().y, scale, flip_y);
    path.push_back(move);

    std::size_t index = 1;
    while (index < sequence.size()) {
        if (sequence[index].on_curve) {
            PathCommand line;
            line.verb = PathVerb::LineTo;
            line.x1 = mappedCoordinate(sequence[index].x, scale, flip_y);
            line.y1 = mappedCoordinate(sequence[index].y, scale, flip_y);
            path.push_back(line);
            ++index;
        } else {
            PathCommand quad;
            quad.verb = PathVerb::QuadraticTo;
            quad.x1 = mappedCoordinate(sequence[index].x, scale, flip_y);
            quad.y1 = mappedCoordinate(sequence[index].y, scale, flip_y);
            if (index + 1 < sequence.size()) {
                quad.x2 =
                    mappedCoordinate(sequence[index + 1].x, scale, flip_y);
                quad.y2 =
                    mappedCoordinate(sequence[index + 1].y, scale, flip_y);
                index += 2;
            } else {
                quad.x2 = mappedCoordinate(sequence.front().x, scale, flip_y);
                quad.y2 = mappedCoordinate(sequence.front().y, scale, flip_y);
                ++index;
            }
            path.push_back(quad);
        }
    }
    PathCommand close;
    close.verb = PathVerb::Close;
    path.push_back(close);
}

void appendNumber(std::string& output, double value) {
    char buffer[64];
    const int length = std::snprintf(buffer, sizeof(buffer), "%.17g", value);
    if (length > 0) {
        output.append(buffer, static_cast<std::size_t>(length));
    }
}

[[nodiscard]] std::string makeFingerprint(const GlyphOutline& outline,
                                          std::uint16_t glyph_id,
                                          double advance,
                                          const std::vector<PathCommand>& path) {
    std::string output;
    output.reserve(32 + path.size() * 64);
    output += "gid=";
    output += std::to_string(glyph_id);
    output += ";advance=";
    appendNumber(output, advance);
    output += ";contours=";
    output += std::to_string(outline.contours.size());
    output += ";";
    for (const PathCommand& command : path) {
        switch (command.verb) {
            case PathVerb::MoveTo:
                output += "M";
                break;
            case PathVerb::LineTo:
                output += "L";
                break;
            case PathVerb::QuadraticTo:
                output += "Q";
                break;
            case PathVerb::Close:
                output += "Z;";
                continue;
        }
        appendNumber(output, command.x1);
        output += ",";
        appendNumber(output, command.y1);
        if (command.verb == PathVerb::QuadraticTo) {
            output += ",";
            appendNumber(output, command.x2);
            output += ",";
            appendNumber(output, command.y2);
        }
        output += ";";
    }
    return output;
}

}  // namespace

std::string Diagnostic::toString() const {
    std::string result = "error ";
    result += std::to_string(static_cast<unsigned>(code));
    if (!table.empty()) {
        result += " in table '" + table + "'";
    }
    if (offset != 0) {
        result += " at offset " + std::to_string(offset);
    }
    if (!message.empty()) {
        result += ": " + message;
    }
    return result;
}

FontFace::FontFace(std::shared_ptr<const detail::FontImpl> impl)
    : impl_(std::move(impl)) {}

bool FontFace::valid() const noexcept {
    return impl_ != nullptr;
}

std::uint32_t FontFace::sfntVersion() const noexcept {
    return impl_ ? impl_->sfnt_version : 0;
}

std::uint16_t FontFace::numGlyphs() const noexcept {
    return impl_ ? impl_->num_glyphs : 0;
}

std::uint16_t FontFace::unitsPerEm() const noexcept {
    return impl_ ? impl_->units_per_em : 0;
}

const std::vector<TableRecord>& FontFace::tables() const {
    static const std::vector<TableRecord> empty;
    return impl_ ? impl_->tables : empty;
}

const TableRecord* FontFace::table(std::string_view tag) const noexcept {
    if (!impl_) {
        return nullptr;
    }
    return findTable(*impl_, tag);
}

bool FontFace::hasTable(std::string_view tag) const noexcept {
    return table(tag) != nullptr;
}

std::optional<std::uint16_t> FontFace::glyphIndex(
    std::uint32_t codepoint) const noexcept {
    if (!impl_ || codepoint > 0x10FFFFu) {
        return std::nullopt;
    }
    const std::vector<CmapRange>& ranges = impl_->cmap;
    auto iterator = std::upper_bound(
        ranges.begin(), ranges.end(), codepoint,
        [](std::uint32_t value, const CmapRange& range) {
            return value < range.first;
        });
    if (iterator == ranges.begin()) {
        return std::nullopt;
    }
    --iterator;
    if (codepoint < iterator->first || codepoint > iterator->last) {
        return std::nullopt;
    }
    std::uint32_t glyph = iterator->glyph;
    if (!iterator->constant) {
        glyph += codepoint - iterator->first;
    }
    if (glyph == 0 || glyph >= impl_->num_glyphs) {
        return std::nullopt;
    }
    return static_cast<std::uint16_t>(glyph);
}

const std::vector<CmapRange>& FontFace::cmapRanges() const {
    static const std::vector<CmapRange> empty;
    return impl_ ? impl_->cmap : empty;
}

const GlyphOutline* FontFace::glyphOutline(std::uint16_t glyph_id) const
    noexcept {
    if (!impl_ || glyph_id >= impl_->glyphs.size()) {
        return nullptr;
    }
    return &impl_->glyphs[glyph_id];
}

std::optional<GlyphOutline> FontFace::glyph(std::uint16_t glyph_id) const {
    const GlyphOutline* outline = glyphOutline(glyph_id);
    if (outline == nullptr) {
        return std::nullopt;
    }
    return *outline;
}

RenderedGlyph FontFace::renderGlyph(std::uint16_t glyph_id,
                                    const RenderOptions& options) const {
    RenderedGlyph rendered;
    rendered.glyph_id = glyph_id;
    const GlyphOutline* outline = glyphOutline(glyph_id);
    if (outline == nullptr) {
        rendered.fingerprint =
            "gid=" + std::to_string(glyph_id) + ";missing;";
        return rendered;
    }

    for (const Contour& contour : outline->contours) {
        appendContourPath(contour, options.scale, options.flip_y,
                          rendered.path);
    }
    rendered.advance =
        static_cast<double>(outline->advance_width) * options.scale;
    rendered.fingerprint = makeFingerprint(*outline, glyph_id,
                                           rendered.advance, rendered.path);
    return rendered;
}

RenderedGlyph FontFace::renderCodepoint(
    std::uint32_t codepoint, const RenderOptions& options) const {
    const std::optional<std::uint16_t> mapped = glyphIndex(codepoint);
    RenderedGlyph rendered = renderGlyph(mapped.value_or(0), options);
    rendered.codepoint = codepoint;
    return rendered;
}

std::string FontFace::glyphFingerprint(
    std::uint16_t glyph_id, const RenderOptions& options) const {
    return renderGlyph(glyph_id, options).fingerprint;
}

std::string FontFace::codepointFingerprint(
    std::uint32_t codepoint, const RenderOptions& options) const {
    return renderCodepoint(codepoint, options).fingerprint;
}

FontLoadResult FontFace::load(ByteSpan bytes,
                              const ValidationOptions& options) {
    return loadFont(bytes, options);
}

FontLoadResult FontFace::load(const std::vector<Byte>& bytes,
                              const ValidationOptions& options) {
    return loadFont(bytes, options);
}

FontLoadResult FontFace::parse(ByteSpan bytes,
                               const ValidationOptions& options) {
    return loadFont(bytes, options);
}

FontLoadResult FontFace::parse(const std::vector<Byte>& bytes,
                               const ValidationOptions& options) {
    return loadFont(bytes, options);
}

FontLoadResult FontFace::fromBytes(ByteSpan bytes,
                                   const ValidationOptions& options) {
    return loadFont(bytes, options);
}

FontLoadResult loadFont(ByteSpan bytes, const ValidationOptions& options) {
    FontLoadResult result;
    try {
        auto impl = std::make_shared<detail::FontImpl>();
        parseFontImpl(bytes, options, *impl);
        result.font = detail::FontLoadResultAccess::make(std::move(impl));
    } catch (const ParseFailure& failure) {
        result.diagnostics.push_back(failure.diagnostic);
    } catch (const std::exception& exception) {
        result.diagnostics.push_back(
            Diagnostic{ErrorCode::InvalidDirectory, "", 0, exception.what()});
    }
    return result;
}

bool validateTableStructure(ByteSpan bytes,
                            std::vector<Diagnostic>* diagnostics,
                            const ValidationOptions& options) {
    FontLoadResult result = loadFont(bytes, options);
    if (diagnostics != nullptr) {
        *diagnostics = std::move(result.diagnostics);
    }
    return result.ok();
}

bool validateTableStructure(const std::vector<Byte>& bytes,
                            std::vector<Diagnostic>* diagnostics,
                            const ValidationOptions& options) {
    return validateTableStructure(
        ByteSpan(bytes.data(), bytes.size()), diagnostics, options);
}

}  // namespace netsec
