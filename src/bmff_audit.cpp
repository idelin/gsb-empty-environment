#include "bmff/audit.hpp"

#include <algorithm>
#include <limits>
#include <utility>

namespace bmff {
namespace {

using u8 = std::uint8_t;
using u32 = std::uint32_t;
using u64 = std::uint64_t;
using usize = std::size_t;

constexpr u64 kU64Max = std::numeric_limits<u64>::max();
constexpr usize kNoIndex = std::numeric_limits<usize>::max();
constexpr u64 kMaxFtypBrands = 1024ULL;
constexpr u32 kUuid = make_fourcc('u', 'u', 'i', 'd');
constexpr u32 kEdts = make_fourcc('e', 'd', 't', 's');
constexpr u32 kDinf = make_fourcc('d', 'i', 'n', 'f');
constexpr u32 kMvex = make_fourcc('m', 'v', 'e', 'x');
constexpr u32 kMoof = make_fourcc('m', 'o', 'o', 'f');
constexpr u32 kTraf = make_fourcc('t', 'r', 'a', 'f');
constexpr u32 kMfra = make_fourcc('m', 'f', 'r', 'a');
constexpr u32 kUdta = make_fourcc('u', 'd', 't', 'a');
constexpr u32 kMeta = make_fourcc('m', 'e', 't', 'a');
constexpr u32 kIlst = make_fourcc('i', 'l', 's', 't');

bool checked_add(u64 a, u64 b, u64& result) noexcept
{
    if (b > kU64Max - a) {
        return false;
    }
    result = a + b;
    return true;
}

bool checked_mul(u64 a, u64 b, u64& result) noexcept
{
    if (a != 0 && b > kU64Max / a) {
        return false;
    }
    result = a * b;
    return true;
}

bool is_printable_fourcc(u32 value) noexcept
{
    for (unsigned i = 0; i < 4U; ++i) {
        const unsigned shift = (3U - i) * 8U;
        const u32 byte = (value >> shift) & 0xFFU;
        if (byte < 0x20U || byte > 0x7EU) {
            return false;
        }
    }
    return true;
}

bool is_container(u32 type) noexcept
{
    switch (type) {
    case kMoov:
    case kTrak:
    case kMdia:
    case kMinf:
    case kStbl:
    case kEdts:
    case kDinf:
    case kMvex:
    case kMoof:
    case kTraf:
    case kMfra:
    case kUdta:
    case kIlst:
        return true;
    default:
        return false;
    }
}

struct BoxHeader {
    u32 type = 0;
    u64 offset = 0;
    u64 size = 0;
    u64 header_size = 0;
    u64 body_offset = 0;
    u64 body_size = 0;
    u64 end = 0;
};

struct StscEntry {
    u32 first_chunk = 0;
    u32 samples_per_chunk = 0;
    u32 sample_description_index = 0;
};

struct SampleTable {
    bool has_stco = false;
    bool has_co64 = false;
    bool has_stsz = false;
    bool has_stsc = false;
    u64 stbl_offset = 0;
    u64 chunk_offset_box = 0;
    u64 stsz_offset = 0;
    u64 stsc_offset = 0;
    u32 sample_size = 0;
    u32 sample_count = 0;
    std::vector<u64> chunk_offsets;
    std::vector<u32> sample_sizes;
    std::vector<StscEntry> stsc;
};

struct ChunkInterval {
    u64 begin = 0;
    u64 end = 0;
    usize mdat_index = 0;
};

class Auditor {
public:
    Auditor(const u8* data, usize size, const AuditOptions& options)
        : data_(data)
        , size_(size)
        , file_size_(static_cast<u64>(size))
        , options_(options)
    {
    }

    AuditResult run()
    {
        if (data_ == nullptr || size_ < 8U) {
            fail(ErrorCode::MissingFtyp, 0,
                 "input is empty or too short to contain an ftyp box");
            finish_stats();
            return std::move(result_);
        }

        u32 first_type = 0;
        if (!read_u32(4U, file_size_, first_type) || first_type != kFtyp) {
            fail(ErrorCode::MissingFtyp, 0,
                 "the first box at offset 0 must be ftyp");
            finish_stats();
            return std::move(result_);
        }

        u64 cursor = 0;
        while (cursor < file_size_) {
            const usize before = result_.boxes.size();
            if (!parse_box(cursor, file_size_, 1U, true, kNoIndex, kNoIndex)) {
                break;
            }
            if (result_.boxes.size() == before) {
                fail(ErrorCode::InvalidBoxSize, cursor,
                     "box parser made no progress");
                break;
            }
            const u64 next = result_.boxes.back().offset + result_.boxes.back().size;
            if (next <= cursor) {
                fail(ErrorCode::InvalidBoxSize, cursor,
                     "box parser made no progress");
                break;
            }
            cursor = next;
        }

        if (result_.errors.empty()) {
            if (!saw_ftyp_) {
                fail(ErrorCode::MissingFtyp, 0, "missing ftyp box");
            } else {
                validate_sample_tables();
            }
        }

        finish_stats();
        return std::move(result_);
    }

private:
    bool fail(ErrorCode code, u64 offset, std::string message)
    {
        result_.errors.push_back(AuditError{code, offset, std::move(message)});
        return false;
    }

    bool read_u32(u64 offset, u64 limit, u32& value) const noexcept
    {
        if (offset > limit || limit > file_size_ || limit - offset < 4U) {
            return false;
        }
        const u8* p = data_ + static_cast<usize>(offset);
        value = (static_cast<u32>(p[0]) << 24U) |
                (static_cast<u32>(p[1]) << 16U) |
                (static_cast<u32>(p[2]) << 8U) |
                static_cast<u32>(p[3]);
        return true;
    }

    bool read_u64(u64 offset, u64 limit, u64& value) const noexcept
    {
        if (offset > limit || limit > file_size_ || limit - offset < 8U) {
            return false;
        }
        const u8* p = data_ + static_cast<usize>(offset);
        value = (static_cast<u64>(p[0]) << 56U) |
                (static_cast<u64>(p[1]) << 48U) |
                (static_cast<u64>(p[2]) << 40U) |
                (static_cast<u64>(p[3]) << 32U) |
                (static_cast<u64>(p[4]) << 24U) |
                (static_cast<u64>(p[5]) << 16U) |
                (static_cast<u64>(p[6]) << 8U) |
                static_cast<u64>(p[7]);
        return true;
    }

    bool parse_box(u64 offset, u64 parent_end, u32 depth, bool top_level,
                   usize parent_index, usize table_index)
    {
        if (depth > options_.max_depth) {
            return fail(ErrorCode::DepthLimitExceeded, offset,
                        "box nesting depth exceeds the configured limit");
        }
        if (offset >= parent_end || parent_end > file_size_) {
            return fail(ErrorCode::BoxExtendsPastParent, offset,
                        "box start is outside its parent boundary");
        }

        u32 size32 = 0;
        u32 type = 0;
        if (!read_u32(offset, parent_end, size32)) {
            return fail(ErrorCode::TruncatedBoxHeader, offset,
                        "truncated 32-bit box size");
        }
        if (!read_u32(offset + 4U, parent_end, type)) {
            return fail(ErrorCode::TruncatedBoxHeader, offset,
                        "truncated box type");
        }

        u64 box_size = 0;
        u64 header_size = 8U;
        if (size32 == 1U) {
            u64 large_size = 0;
            if (!read_u64(offset + 8U, parent_end, large_size)) {
                return fail(ErrorCode::TruncatedBoxHeader, offset,
                            "truncated 64-bit largesize field");
            }
            if (large_size < 16U) {
                return fail(ErrorCode::InvalidBoxSize, offset,
                            "64-bit box size is smaller than its header");
            }
            box_size = large_size;
            header_size = 16U;
        } else if (size32 == 0U) {
            if (!top_level) {
                return fail(ErrorCode::SizeZeroInsideContainer, offset,
                            "size == 0 is only valid for a final top-level box");
            }
            box_size = parent_end - offset;
        } else {
            if (size32 < 8U) {
                return fail(ErrorCode::InvalidBoxSize, offset,
                            "32-bit box size is smaller than 8");
            }
            box_size = static_cast<u64>(size32);
        }

        if (type == kUuid) {
            header_size += 16U;
        }
        if (box_size < header_size) {
            return fail(ErrorCode::InvalidBoxSize, offset,
                        "box size is smaller than its header");
        }

        u64 box_end = 0;
        if (!checked_add(offset, box_size, box_end)) {
            return fail(ErrorCode::SizeOverflow, offset,
                        "box offset + size overflows 64 bits");
        }
        if (box_end > parent_end) {
            return fail(ErrorCode::BoxExtendsPastParent, offset,
                        "box extends past its parent boundary");
        }

        if (static_cast<u64>(result_.boxes.size()) >= options_.max_boxes) {
            return fail(ErrorCode::TooManyBoxes, offset,
                        "box count exceeds the configured limit");
        }

        BoxHeader header;
        header.type = type;
        header.offset = offset;
        header.size = box_size;
        header.header_size = header_size;
        header.body_offset = offset + header_size;
        header.body_size = box_size - header_size;
        header.end = box_end;

        BoxInfo info;
        info.type = type;
        info.offset = offset;
        info.size = box_size;
        info.header_size = header_size;
        info.body_offset = header.body_offset;
        info.body_size = header.body_size;
        info.depth = depth;
        info.parent = parent_index;
        result_.boxes.push_back(info);
        result_.stats.max_depth_seen =
            std::max(result_.stats.max_depth_seen, depth);
        const usize box_index = result_.boxes.size() - 1U;

        if (type == kFtyp) {
            if (!top_level) {
                return fail(ErrorCode::MisplacedFtyp, offset,
                            "ftyp is not allowed inside a container");
            }
            if (saw_ftyp_) {
                return fail(ErrorCode::DuplicateFtyp, offset,
                            "duplicate ftyp box");
            }
            saw_ftyp_ = true;
            if (!validate_ftyp(header)) {
                return false;
            }
        }

        if (type == kMoov) {
            if (!top_level) {
                return fail(ErrorCode::MisplacedMoov, offset,
                            "moov must be a top-level box");
            }
            ++moov_count_;
            if (moov_count_ > 1U) {
                return fail(ErrorCode::DuplicateMoov, offset,
                            "duplicate top-level moov box");
            }
        }

        if (type == kMdat) {
            if (!top_level) {
                return fail(ErrorCode::MisplacedMdat, offset,
                            "mdat must be a top-level box");
            }
            MdatInfo mdat;
            mdat.box_offset = offset;
            mdat.box_size = box_size;
            mdat.payload_offset = header.body_offset;
            mdat.payload_size = header.body_size;
            mdat.payload_end = header.end;
            result_.mdats.push_back(mdat);
        }

        if (type == kStco || type == kCo64 || type == kStsz || type == kStsc) {
            if (table_index == kNoIndex) {
                return fail(ErrorCode::MisplacedSampleTable, offset,
                            "sample table box is not a direct child of stbl");
            }
            if (table_index >= sample_tables_.size()) {
                return fail(ErrorCode::InvalidSampleTable, offset,
                            "internal sample table index is invalid");
            }
        }

        switch (type) {
        case kStco:
            return parse_stco(header, table_index);
        case kCo64:
            return parse_co64(header, table_index);
        case kStsz:
            return parse_stsz(header, table_index);
        case kStsc:
            return parse_stsc(header, table_index);
        default:
            break;
        }

        if (type == kStbl) {
            if (table_index != kNoIndex) {
                return fail(ErrorCode::NestedStbl, offset,
                            "stbl must not be nested inside another stbl");
            }
            const usize new_index = sample_tables_.size();
            SampleTable table;
            table.stbl_offset = offset;
            sample_tables_.push_back(std::move(table));
            return parse_children(header.body_offset, header.end, depth + 1U,
                                  box_index, new_index);
        }

        if (type == kMeta) {
            if (header.body_size < 4U) {
                return fail(ErrorCode::InvalidBoxSize, offset,
                            "meta box is too short for its full-box header");
            }
            u64 children_begin = 0;
            if (!checked_add(header.body_offset, 4U, children_begin)) {
                return fail(ErrorCode::SizeOverflow, offset,
                            "meta children offset overflows");
            }
            return parse_children(children_begin, header.end, depth + 1U,
                                  box_index, kNoIndex);
        }

        if (is_container(type)) {
            return parse_children(header.body_offset, header.end, depth + 1U,
                                  box_index, kNoIndex);
        }

        return true;
    }

    bool parse_children(u64 begin, u64 end, u32 depth, usize parent_index,
                        usize table_index)
    {
        u64 cursor = begin;
        while (cursor < end) {
            const usize before = result_.boxes.size();
            if (!parse_box(cursor, end, depth, false, parent_index,
                           table_index)) {
                return false;
            }
            if (result_.boxes.size() == before) {
                return fail(ErrorCode::InvalidBoxSize, cursor,
                            "container child parser made no progress");
            }
            const u64 next =
                result_.boxes.back().offset + result_.boxes.back().size;
            if (next <= cursor || next > end) {
                return fail(ErrorCode::BoxExtendsPastParent, cursor,
                            "container child does not advance inside parent");
            }
            cursor = next;
        }
        return true;
    }

    bool validate_ftyp(const BoxHeader& header)
    {
        if (header.body_size < 8U || ((header.body_size - 8U) % 4U) != 0U) {
            return fail(ErrorCode::InvalidFtyp, header.offset,
                        "ftyp body must contain brand, minor version, and "
                        "whole compatible brands");
        }

        u32 major_brand = 0;
        if (!read_u32(header.body_offset, header.end, major_brand)) {
            return fail(ErrorCode::InvalidFtyp, header.offset,
                        "cannot read ftyp major brand");
        }
        if (!is_printable_fourcc(major_brand)) {
            return fail(ErrorCode::InvalidFtyp, header.offset,
                        "ftyp major brand is not printable ASCII");
        }

        const u64 compatible_count = (header.body_size - 8U) / 4U;
        if (compatible_count > kMaxFtypBrands) {
            return fail(ErrorCode::InvalidFtyp, header.offset,
                        "ftyp contains too many compatible brands");
        }
        for (u64 i = 0; i < compatible_count; ++i) {
            u32 brand = 0;
            const u64 brand_offset =
                header.body_offset + 8U + i * 4U;
            if (!read_u32(brand_offset, header.end, brand)) {
                return fail(ErrorCode::InvalidFtyp, header.offset,
                            "cannot read ftyp compatible brand");
            }
            if (!is_printable_fourcc(brand)) {
                return fail(ErrorCode::InvalidFtyp, header.offset,
                            "ftyp compatible brand is not printable ASCII");
            }
        }
        return true;
    }

    bool parse_stco(const BoxHeader& header, usize table_index)
    {
        SampleTable& table = sample_tables_[table_index];
        if (table.has_stco || table.has_co64) {
            return fail(ErrorCode::InvalidSampleTable, header.offset,
                        "multiple chunk offset tables in one stbl");
        }
        if (header.body_size < 8U) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "stco body is shorter than its full-box header");
        }

        u32 version_flags = 0;
        u32 count = 0;
        if (!read_u32(header.body_offset, header.end, version_flags) ||
            !read_u32(header.body_offset + 4U, header.end, count)) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "truncated stco header");
        }
        if ((version_flags >> 24U) != 0U) {
            return fail(ErrorCode::InvalidSampleTable, header.offset,
                        "unsupported stco version");
        }

        const u64 expected = 8U + static_cast<u64>(count) * 4U;
        if (expected != header.body_size) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "stco entry count does not match its body length");
        }
        if (static_cast<u64>(count) > options_.max_chunk_count) {
            return fail(ErrorCode::TooManyChunks, header.offset,
                        "stco chunk count exceeds the configured limit");
        }
        u64 next_chunks = 0;
        if (!checked_add(seen_chunks_, static_cast<u64>(count),
                         next_chunks)) {
            return fail(ErrorCode::TooManyChunks, header.offset,
                        "aggregate chunk count overflows 64 bits");
        }
        if (next_chunks > options_.max_chunk_count) {
            return fail(ErrorCode::TooManyChunks, header.offset,
                        "aggregate chunk count exceeds the configured limit");
        }

        table.chunk_offsets.resize(static_cast<usize>(count));
        for (u32 i = 0; i < count; ++i) {
            u32 value = 0;
            const u64 entry_offset =
                header.body_offset + 8U + static_cast<u64>(i) * 4U;
            if (!read_u32(entry_offset, header.end, value)) {
                return fail(ErrorCode::TableLengthMismatch, header.offset,
                            "truncated stco entry");
            }
            table.chunk_offsets[static_cast<usize>(i)] =
                static_cast<u64>(value);
        }
        seen_chunks_ = next_chunks;
        table.has_stco = true;
        table.chunk_offset_box = header.offset;
        return true;
    }

    bool parse_co64(const BoxHeader& header, usize table_index)
    {
        SampleTable& table = sample_tables_[table_index];
        if (table.has_stco || table.has_co64) {
            return fail(ErrorCode::InvalidSampleTable, header.offset,
                        "multiple chunk offset tables in one stbl");
        }
        if (header.body_size < 8U) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "co64 body is shorter than its full-box header");
        }

        u32 version_flags = 0;
        u32 count = 0;
        if (!read_u32(header.body_offset, header.end, version_flags) ||
            !read_u32(header.body_offset + 4U, header.end, count)) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "truncated co64 header");
        }
        if ((version_flags >> 24U) != 0U) {
            return fail(ErrorCode::InvalidSampleTable, header.offset,
                        "unsupported co64 version");
        }

        const u64 expected = 8U + static_cast<u64>(count) * 8U;
        if (expected != header.body_size) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "co64 entry count does not match its body length");
        }
        if (static_cast<u64>(count) > options_.max_chunk_count) {
            return fail(ErrorCode::TooManyChunks, header.offset,
                        "co64 chunk count exceeds the configured limit");
        }
        u64 next_chunks = 0;
        if (!checked_add(seen_chunks_, static_cast<u64>(count),
                         next_chunks)) {
            return fail(ErrorCode::TooManyChunks, header.offset,
                        "aggregate chunk count overflows 64 bits");
        }
        if (next_chunks > options_.max_chunk_count) {
            return fail(ErrorCode::TooManyChunks, header.offset,
                        "aggregate chunk count exceeds the configured limit");
        }

        table.chunk_offsets.resize(static_cast<usize>(count));
        for (u32 i = 0; i < count; ++i) {
            u64 value = 0;
            const u64 entry_offset =
                header.body_offset + 8U + static_cast<u64>(i) * 8U;
            if (!read_u64(entry_offset, header.end, value)) {
                return fail(ErrorCode::TableLengthMismatch, header.offset,
                            "truncated co64 entry");
            }
            table.chunk_offsets[static_cast<usize>(i)] = value;
        }
        seen_chunks_ = next_chunks;
        table.has_co64 = true;
        table.chunk_offset_box = header.offset;
        return true;
    }

    bool parse_stsz(const BoxHeader& header, usize table_index)
    {
        SampleTable& table = sample_tables_[table_index];
        if (table.has_stsz) {
            return fail(ErrorCode::InvalidSampleTable, header.offset,
                        "duplicate stsz box in one stbl");
        }
        if (header.body_size < 12U) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "stsz body is shorter than its fixed header");
        }

        u32 version_flags = 0;
        u32 sample_size = 0;
        u32 sample_count = 0;
        if (!read_u32(header.body_offset, header.end, version_flags) ||
            !read_u32(header.body_offset + 4U, header.end, sample_size) ||
            !read_u32(header.body_offset + 8U, header.end, sample_count)) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "truncated stsz header");
        }
        if ((version_flags >> 24U) != 0U) {
            return fail(ErrorCode::InvalidSampleTable, header.offset,
                        "unsupported stsz version");
        }

        u64 expected = 12U;
        if (sample_size == 0U) {
            expected += static_cast<u64>(sample_count) * 4U;
        }
        if (expected != header.body_size) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "stsz sample count does not match its body length");
        }
        if (static_cast<u64>(sample_count) > options_.max_sample_count) {
            return fail(ErrorCode::TooManySamples, header.offset,
                        "stsz sample count exceeds the configured limit");
        }
        if (static_cast<u64>(sample_count) >
            static_cast<u64>(std::numeric_limits<usize>::max())) {
            return fail(ErrorCode::TooManySamples, header.offset,
                        "stsz sample count exceeds host address space");
        }
        if (sample_size != 0U &&
            static_cast<u64>(sample_size) > options_.max_single_sample_size) {
            return fail(ErrorCode::SampleTooLarge, header.offset,
                        "stsz constant sample size exceeds the limit");
        }

        u64 next_samples = 0;
        if (!checked_add(seen_samples_, static_cast<u64>(sample_count),
                         next_samples)) {
            return fail(ErrorCode::TooManySamples, header.offset,
                        "aggregate sample count overflows 64 bits");
        }
        if (next_samples > options_.max_sample_count) {
            return fail(ErrorCode::TooManySamples, header.offset,
                        "aggregate sample count exceeds the configured limit");
        }

        u64 next_bytes = seen_sample_bytes_;
        if (sample_size == 0U) {
            table.sample_sizes.resize(static_cast<usize>(sample_count));
            for (u32 i = 0; i < sample_count; ++i) {
                u32 value = 0;
                const u64 entry_offset =
                    header.body_offset + 12U + static_cast<u64>(i) * 4U;
                if (!read_u32(entry_offset, header.end, value)) {
                    return fail(ErrorCode::TableLengthMismatch, header.offset,
                                "truncated stsz entry");
                }
                if (static_cast<u64>(value) >
                    options_.max_single_sample_size) {
                    return fail(ErrorCode::SampleTooLarge, header.offset,
                                "stsz sample entry exceeds the limit");
                }
                if (!checked_add(next_bytes, static_cast<u64>(value),
                                 next_bytes)) {
                    return fail(ErrorCode::TotalSampleBytesTooLarge,
                                header.offset,
                                "aggregate sample bytes overflow 64 bits");
                }
                if (next_bytes > options_.max_total_sample_bytes) {
                    return fail(ErrorCode::TotalSampleBytesTooLarge,
                                header.offset,
                                "aggregate sample bytes exceed the configured "
                                "limit");
                }
                table.sample_sizes[static_cast<usize>(i)] = value;
            }
        } else {
            u64 table_bytes = 0;
            if (!checked_mul(static_cast<u64>(sample_count),
                             static_cast<u64>(sample_size), table_bytes)) {
                return fail(ErrorCode::TotalSampleBytesTooLarge,
                            header.offset,
                            "aggregate sample bytes overflow 64 bits");
            }
            if (!checked_add(next_bytes, table_bytes, next_bytes)) {
                return fail(ErrorCode::TotalSampleBytesTooLarge,
                            header.offset,
                            "aggregate sample bytes overflow 64 bits");
            }
            if (next_bytes > options_.max_total_sample_bytes) {
                return fail(ErrorCode::TotalSampleBytesTooLarge, header.offset,
                            "aggregate sample bytes exceed the configured "
                            "limit");
            }
        }

        seen_samples_ = next_samples;
        seen_sample_bytes_ = next_bytes;
        table.sample_size = sample_size;
        table.sample_count = sample_count;
        table.stsz_offset = header.offset;
        table.has_stsz = true;
        return true;
    }

    bool parse_stsc(const BoxHeader& header, usize table_index)
    {
        SampleTable& table = sample_tables_[table_index];
        if (table.has_stsc) {
            return fail(ErrorCode::InvalidSampleTable, header.offset,
                        "duplicate stsc box in one stbl");
        }
        if (header.body_size < 8U) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "stsc body is shorter than its full-box header");
        }

        u32 version_flags = 0;
        u32 count = 0;
        if (!read_u32(header.body_offset, header.end, version_flags) ||
            !read_u32(header.body_offset + 4U, header.end, count)) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "truncated stsc header");
        }
        if ((version_flags >> 24U) != 0U) {
            return fail(ErrorCode::InvalidSampleTable, header.offset,
                        "unsupported stsc version");
        }

        const u64 expected = 8U + static_cast<u64>(count) * 12U;
        if (expected != header.body_size) {
            return fail(ErrorCode::TableLengthMismatch, header.offset,
                        "stsc entry count does not match its body length");
        }
        if (static_cast<u64>(count) > options_.max_chunk_count) {
            return fail(ErrorCode::TooManyChunks, header.offset,
                        "stsc entry count exceeds the configured limit");
        }
        u64 next_entries = 0;
        if (!checked_add(seen_stsc_entries_, static_cast<u64>(count),
                         next_entries)) {
            return fail(ErrorCode::TooManyChunks, header.offset,
                        "aggregate stsc entry count overflows 64 bits");
        }
        if (next_entries > options_.max_chunk_count) {
            return fail(ErrorCode::TooManyChunks, header.offset,
                        "aggregate stsc entry count exceeds the configured "
                        "limit");
        }

        table.stsc.resize(static_cast<usize>(count));
        u32 previous_first_chunk = 0;
        for (u32 i = 0; i < count; ++i) {
            StscEntry entry;
            const u64 entry_offset =
                header.body_offset + 8U + static_cast<u64>(i) * 12U;
            if (!read_u32(entry_offset, header.end, entry.first_chunk) ||
                !read_u32(entry_offset + 4U, header.end,
                          entry.samples_per_chunk) ||
                !read_u32(entry_offset + 8U, header.end,
                          entry.sample_description_index)) {
                return fail(ErrorCode::TableLengthMismatch, header.offset,
                            "truncated stsc entry");
            }
            if (entry.first_chunk == 0U ||
                (i == 0U && entry.first_chunk != 1U) ||
                (i != 0U && entry.first_chunk <= previous_first_chunk)) {
                return fail(ErrorCode::InvalidSampleTable, header.offset,
                            "stsc first_chunk values must start at 1 and "
                            "increase strictly");
            }
            if (entry.samples_per_chunk == 0U) {
                return fail(ErrorCode::InvalidSampleTable, header.offset,
                            "stsc samples_per_chunk must be non-zero");
            }
            if (entry.sample_description_index == 0U) {
                return fail(ErrorCode::InvalidSampleTable, header.offset,
                            "stsc sample_description_index must be non-zero");
            }
            table.stsc[static_cast<usize>(i)] = entry;
            previous_first_chunk = entry.first_chunk;
        }

        seen_stsc_entries_ = next_entries;
        table.stsc_offset = header.offset;
        table.has_stsc = true;
        return true;
    }

    bool sample_size_at(const SampleTable& table, u64 index, u64& size) const
    {
        if (index >= table.sample_count) {
            return false;
        }
        if (table.sample_size != 0U) {
            size = static_cast<u64>(table.sample_size);
            return true;
        }
        size = static_cast<u64>(
            table.sample_sizes[static_cast<usize>(index)]);
        return true;
    }

    bool chunk_size_for(const SampleTable& table, u64 first_sample,
                        u64 sample_count, u64& byte_count)
    {
        u64 total = 0;
        for (u64 i = 0; i < sample_count; ++i) {
            u64 sample_bytes = 0;
            if (!sample_size_at(table, first_sample + i, sample_bytes)) {
                return fail(ErrorCode::InvalidSampleTable, table.stsz_offset,
                            "stsc maps more samples than stsz contains");
            }
            if (!checked_add(total, sample_bytes, total)) {
                return fail(ErrorCode::TotalSampleBytesTooLarge,
                            table.stsz_offset,
                            "chunk byte size overflows 64 bits");
            }
        }
        byte_count = total;
        return true;
    }

    bool validate_sample_tables()
    {
        u64 total_samples = 0;
        u64 total_chunks = 0;
        u64 total_bytes = 0;
        bool saw_sample_table = false;
        std::vector<ChunkInterval> intervals;

        for (usize table_index = 0; table_index < sample_tables_.size();
             ++table_index) {
            const SampleTable& table = sample_tables_[table_index];
            const bool present = table.has_stco || table.has_co64 ||
                                 table.has_stsz || table.has_stsc;
            if (!present) {
                continue;
            }
            saw_sample_table = true;

            if (!table.has_stsz) {
                return fail(ErrorCode::MissingSampleTable, table.stbl_offset,
                            "stbl has chunk tables but no stsz box");
            }
            if (table.has_stco && table.has_co64) {
                return fail(ErrorCode::InvalidSampleTable,
                            table.chunk_offset_box,
                            "stbl contains both stco and co64");
            }

            if (!checked_add(total_samples,
                             static_cast<u64>(table.sample_count),
                             total_samples)) {
                return fail(ErrorCode::TooManySamples, table.stsz_offset,
                            "total sample count overflows 64 bits");
            }
            if (total_samples > options_.max_sample_count) {
                return fail(ErrorCode::TooManySamples, table.stsz_offset,
                            "total sample count exceeds the configured limit");
            }

            u64 table_bytes = 0;
            if (table.sample_size != 0U) {
                if (!checked_mul(static_cast<u64>(table.sample_count),
                                 static_cast<u64>(table.sample_size),
                                 table_bytes)) {
                    return fail(ErrorCode::TotalSampleBytesTooLarge,
                                table.stsz_offset,
                                "total sample bytes overflow 64 bits");
                }
            } else {
                for (u32 sample_size : table.sample_sizes) {
                    if (!checked_add(table_bytes,
                                     static_cast<u64>(sample_size),
                                     table_bytes)) {
                        return fail(ErrorCode::TotalSampleBytesTooLarge,
                                    table.stsz_offset,
                                    "total sample bytes overflow 64 bits");
                    }
                }
            }
            if (!checked_add(total_bytes, table_bytes, total_bytes)) {
                return fail(ErrorCode::TotalSampleBytesTooLarge,
                            table.stsz_offset,
                            "aggregate sample bytes overflow 64 bits");
            }
            if (total_bytes > options_.max_total_sample_bytes) {
                return fail(ErrorCode::TotalSampleBytesTooLarge,
                            table.stsz_offset,
                            "aggregate sample bytes exceed the configured limit");
            }

            const u64 chunk_count =
                static_cast<u64>(table.chunk_offsets.size());
            if (!checked_add(total_chunks, chunk_count, total_chunks)) {
                return fail(ErrorCode::TooManyChunks,
                            table.chunk_offset_box,
                            "total chunk count overflows 64 bits");
            }
            if (total_chunks > options_.max_chunk_count) {
                return fail(ErrorCode::TooManyChunks,
                            table.chunk_offset_box,
                            "total chunk count exceeds the configured limit");
            }

            if (table.sample_count == 0U) {
                if (chunk_count != 0U || !table.stsc.empty()) {
                    return fail(ErrorCode::InvalidSampleTable,
                                table.stbl_offset,
                                "zero-sample stbl has chunk or stsc entries");
                }
                continue;
            }
            if (chunk_count == 0U) {
                return fail(ErrorCode::MissingSampleTable,
                            table.chunk_offset_box,
                            "samples exist but no stco/co64 entries were found");
            }

            if (table.has_stsc) {
                if (table.stsc.empty()) {
                    return fail(ErrorCode::InvalidSampleTable,
                                table.stsc_offset,
                                "non-empty chunk table has empty stsc");
                }
                for (const StscEntry& entry : table.stsc) {
                    if (static_cast<u64>(entry.first_chunk) > chunk_count) {
                        return fail(ErrorCode::InvalidSampleTable,
                                    table.stsc_offset,
                                    "stsc first_chunk exceeds chunk count");
                    }
                }

                usize entry_index = 0;
                u64 sample_cursor = 0;
                for (u64 chunk = 1; chunk <= chunk_count; ++chunk) {
                    while (entry_index + 1U < table.stsc.size() &&
                           static_cast<u64>(
                               table.stsc[entry_index + 1U].first_chunk) <=
                               chunk) {
                        ++entry_index;
                    }
                    const u64 samples_per_chunk = static_cast<u64>(
                        table.stsc[entry_index].samples_per_chunk);
                    if (samples_per_chunk >
                        static_cast<u64>(table.sample_count) - sample_cursor) {
                        return fail(ErrorCode::InvalidSampleTable,
                                    table.stsc_offset,
                                    "stsc maps more samples than stsz contains");
                    }
                    if (!emit_chunk(table, chunk, sample_cursor,
                                    samples_per_chunk, intervals)) {
                        return false;
                    }
                    sample_cursor += samples_per_chunk;
                }
                if (sample_cursor != static_cast<u64>(table.sample_count)) {
                    return fail(ErrorCode::InvalidSampleTable,
                                table.stsc_offset,
                                "stsc maps fewer samples than stsz contains");
                }
            } else {
                if (chunk_count == static_cast<u64>(table.sample_count)) {
                    for (u64 chunk = 1; chunk <= chunk_count; ++chunk) {
                        if (!emit_chunk(table, chunk, chunk - 1U,
                                        1U, intervals)) {
                            return false;
                        }
                    }
                } else if (chunk_count == 1U) {
                    if (!emit_chunk(table, 1U, 0U,
                                    static_cast<u64>(table.sample_count),
                                    intervals)) {
                        return false;
                    }
                } else {
                    return fail(ErrorCode::MissingSampleTable,
                                table.chunk_offset_box,
                                "stsc is required when chunk and sample counts "
                                "cannot be mapped unambiguously");
                }
            }
        }

        if (!saw_sample_table) {
            return true;
        }

        std::sort(intervals.begin(), intervals.end(),
                  [](const ChunkInterval& a, const ChunkInterval& b) {
                      if (a.begin != b.begin) {
                          return a.begin < b.begin;
                      }
                      return a.end < b.end;
                  });

        for (usize i = 1; i < intervals.size(); ++i) {
            if (intervals[i].begin < intervals[i - 1U].end) {
                return fail(ErrorCode::OverlappingChunks,
                            intervals[i].begin,
                            "sample chunk intervals overlap");
            }
        }

        if (options_.require_mdat_coverage) {
            std::vector<std::vector<usize>> intervals_by_mdat(
                result_.mdats.size());
            for (usize i = 0; i < intervals.size(); ++i) {
                const usize mdat_index = intervals[i].mdat_index;
                intervals_by_mdat[mdat_index].push_back(i);
            }

            for (usize mdat_index = 0; mdat_index < result_.mdats.size();
                 ++mdat_index) {
                const MdatInfo& mdat = result_.mdats[mdat_index];
                const std::vector<usize>& indexes =
                    intervals_by_mdat[mdat_index];
                if (indexes.empty()) {
                    if (mdat.payload_size != 0U) {
                        return fail(ErrorCode::MdatCoverageGap,
                                    mdat.payload_offset,
                                    "mdat payload is not covered by samples");
                    }
                    continue;
                }

                u64 expected = mdat.payload_offset;
                for (usize index : indexes) {
                    const ChunkInterval& interval = intervals[index];
                    if (interval.begin > expected) {
                        return fail(ErrorCode::MdatCoverageGap, expected,
                                    "uncovered gap in mdat payload");
                    }
                    expected = interval.end;
                }
                if (expected != mdat.payload_end) {
                    return fail(ErrorCode::MdatCoverageGap, expected,
                                "mdat payload has trailing uncovered bytes");
                }
            }
        }

        result_.stats.samples = total_samples;
        result_.stats.chunks = total_chunks;
        result_.stats.sample_bytes = total_bytes;
        return true;
    }

    bool emit_chunk(const SampleTable& table, u64 chunk, u64 first_sample,
                    u64 sample_count,
                    std::vector<ChunkInterval>& intervals)
    {
        u64 chunk_bytes = 0;
        if (!chunk_size_for(table, first_sample, sample_count, chunk_bytes)) {
            return false;
        }
        const usize offset_index = static_cast<usize>(chunk - 1U);
        if (offset_index >= table.chunk_offsets.size()) {
            return fail(ErrorCode::InvalidSampleTable,
                        table.chunk_offset_box,
                        "chunk index is outside stco/co64");
        }

        const u64 chunk_offset = table.chunk_offsets[offset_index];
        u64 chunk_end = 0;
        if (!checked_add(chunk_offset, chunk_bytes, chunk_end)) {
            return fail(ErrorCode::ChunkOutsideMdat,
                        table.chunk_offset_box,
                        "chunk offset + size overflows 64 bits");
        }

        usize mdat_index = kNoIndex;
        for (usize i = 0; i < result_.mdats.size(); ++i) {
            const MdatInfo& mdat = result_.mdats[i];
            if (chunk_offset >= mdat.payload_offset &&
                chunk_end <= mdat.payload_end) {
                mdat_index = i;
                break;
            }
        }
        if (mdat_index == kNoIndex) {
            return fail(ErrorCode::ChunkOutsideMdat,
                        table.chunk_offset_box,
                        "sample chunk points outside every mdat payload");
        }

        intervals.push_back(
            ChunkInterval{chunk_offset, chunk_end, mdat_index});
        return true;
    }

    void finish_stats()
    {
        result_.stats.boxes = static_cast<u64>(result_.boxes.size());
        result_.stats.mdats = static_cast<u64>(result_.mdats.size());
        result_.stats.sample_tables =
            static_cast<u64>(sample_tables_.size());
        u64 top_level = 0;
        u64 mdat_bytes = 0;
        for (const BoxInfo& box : result_.boxes) {
            if (box.depth == 1U) {
                ++top_level;
            }
        }
        for (const MdatInfo& mdat : result_.mdats) {
            if (!checked_add(mdat_bytes, mdat.payload_size, mdat_bytes)) {
                mdat_bytes = kU64Max;
            }
        }
        result_.stats.top_level_boxes = top_level;
        result_.stats.mdat_payload_bytes = mdat_bytes;
    }

    const u8* data_ = nullptr;
    usize size_ = 0;
    u64 file_size_ = 0;
    const AuditOptions& options_;
    AuditResult result_;
    std::vector<SampleTable> sample_tables_;
    u64 moov_count_ = 0;
    bool saw_ftyp_ = false;
    u64 seen_samples_ = 0;
    u64 seen_chunks_ = 0;
    u64 seen_stsc_entries_ = 0;
    u64 seen_sample_bytes_ = 0;
};

} // namespace

const char* to_string(ErrorCode code) noexcept
{
    switch (code) {
    case ErrorCode::EmptyInput:
        return "empty input";
    case ErrorCode::MissingFtyp:
        return "missing or non-leading ftyp";
    case ErrorCode::InvalidFtyp:
        return "invalid ftyp";
    case ErrorCode::DuplicateFtyp:
        return "duplicate ftyp";
    case ErrorCode::MisplacedFtyp:
        return "misplaced ftyp";
    case ErrorCode::TruncatedBoxHeader:
        return "truncated box header";
    case ErrorCode::InvalidBoxSize:
        return "invalid box size";
    case ErrorCode::SizeOverflow:
        return "box size overflow";
    case ErrorCode::BoxExtendsPastParent:
        return "box extends past parent";
    case ErrorCode::SizeZeroInsideContainer:
        return "size zero inside container";
    case ErrorCode::DepthLimitExceeded:
        return "depth limit exceeded";
    case ErrorCode::TooManyBoxes:
        return "too many boxes";
    case ErrorCode::DuplicateMoov:
        return "duplicate moov";
    case ErrorCode::MisplacedMoov:
        return "misplaced moov";
    case ErrorCode::MisplacedMdat:
        return "misplaced mdat";
    case ErrorCode::MisplacedSampleTable:
        return "misplaced sample table";
    case ErrorCode::NestedStbl:
        return "nested stbl";
    case ErrorCode::TableLengthMismatch:
        return "sample table length mismatch";
    case ErrorCode::InvalidSampleTable:
        return "invalid sample table";
    case ErrorCode::MissingSampleTable:
        return "missing sample table";
    case ErrorCode::TooManySamples:
        return "too many samples";
    case ErrorCode::TooManyChunks:
        return "too many chunks";
    case ErrorCode::SampleTooLarge:
        return "sample too large";
    case ErrorCode::TotalSampleBytesTooLarge:
        return "total sample bytes too large";
    case ErrorCode::ChunkOutsideMdat:
        return "chunk outside mdat payload";
    case ErrorCode::OverlappingChunks:
        return "overlapping chunk intervals";
    case ErrorCode::MdatCoverageGap:
        return "mdat coverage gap";
    }
    return "unknown error";
}

std::string fourcc_to_string(u32 type)
{
    std::string result(4U, '?');
    for (unsigned i = 0; i < 4U; ++i) {
        const unsigned shift = (3U - i) * 8U;
        const u32 value = (type >> shift) & 0xFFU;
        result[static_cast<usize>(i)] =
            (value >= 0x20U && value <= 0x7EU)
                ? static_cast<char>(value)
                : '?';
    }
    return result;
}

AuditResult audit(const u8* data, usize size, const AuditOptions& options)
{
    Auditor auditor(data, size, options);
    return auditor.run();
}

} // namespace bmff
