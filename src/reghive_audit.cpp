#include "reghive_audit.hpp"

#include <cstdint>
#include <unordered_map>
#include <unordered_set>

namespace reghive_audit {

namespace {

constexpr size_t   REGF_BASE_SIZE     = 0x1000;  // 4096
constexpr size_t   HBIN_HEADER_SIZE   = 0x20;    // 32
constexpr size_t   CELL_HEADER_SIZE   = 4;
constexpr size_t   MIN_CELL_SIZE      = 8;
constexpr uint32_t NONE_OFFSET        = 0xFFFFFFFFu;
constexpr uint32_t INLINE_DATA_FLAG   = 0x80000000u;
constexpr uint32_t INLINE_DATA_MAX    = 4u;

// regf base block field offsets.
constexpr size_t REGF_SEQ1_OFF            = 0x04;
constexpr size_t REGF_SEQ2_OFF            = 0x08;
constexpr size_t REGF_ROOT_CELL_OFF       = 0x24;
constexpr size_t REGF_HBINS_DATA_SIZE_OFF = 0x28;
constexpr size_t REGF_CHECKSUM_OFF        = 0x1FC;

// hbin header field offsets (relative to hbin start).
constexpr size_t HBIN_REL_OFF  = 0x04;
constexpr size_t HBIN_SIZE_OFF = 0x08;

// nk fixed payload layout (payload begins after the 4-byte cell size).
constexpr size_t NK_PAYLOAD_SIZE       = 0x50;
constexpr size_t NK_PARENT_OFF         = 0x14;
constexpr size_t NK_STABLE_LIST_OFF    = 0x18;
constexpr size_t NK_VOLATILE_LIST_OFF  = 0x1C;
constexpr size_t NK_STABLE_COUNT_OFF   = 0x20;
constexpr size_t NK_VOLATILE_COUNT_OFF = 0x24;
constexpr size_t NK_VALUE_LIST_OFF     = 0x28;
constexpr size_t NK_VALUE_COUNT_OFF    = 0x2C;
constexpr size_t NK_NAME_LEN_OFF       = 0x4C;

// vk fixed payload layout.
constexpr size_t VK_PAYLOAD_SIZE    = 0x18;
constexpr size_t VK_NAME_OFF        = 0x06;
constexpr size_t VK_DATA_SIZE_OFF   = 0x08;
constexpr size_t VK_DATA_OFF        = 0x0C;

// db (big data) fixed payload layout.
constexpr size_t DB_PAYLOAD_SIZE      = 0x0E;
constexpr size_t DB_SEGMENT_COUNT_OFF = 0x08;
constexpr size_t DB_SEGMENT_LIST_OFF  = 0x0A;

constexpr size_t LIST_FIXED_SHORT = 0x06;  // lf/lh: u16 count
constexpr size_t LIST_FIXED_LONG  = 0x08;  // li/ri: u32 count

struct CellInfo {
    uint32_t hive_offset = 0;   // relative to first hbin
    size_t   file_start  = 0;   // absolute file offset of size field
    size_t   file_end    = 0;
    bool     allocated   = false;
    char     kind        = 0;   // 0=unknown, n/v/L/V/D/d/S
};

struct Context {
    const uint8_t* data = nullptr;
    size_t size = 0;
    AuditOptions options;
    AuditReport report;
    std::vector<CellInfo> cells;                          // walk order
    std::unordered_map<uint32_t, size_t> boundary_index;  // hive off -> idx
    std::unordered_set<uint32_t> visited_nk;
    int64_t last_hive_offset = -1;
};

uint16_t rd16(const uint8_t* p) {
    return static_cast<uint16_t>(
        static_cast<uint16_t>(p[0]) |
        (static_cast<uint16_t>(p[1]) << 8));
}

uint32_t rd32(const uint8_t* p) {
    return static_cast<uint32_t>(p[0]) |
           (static_cast<uint32_t>(p[1]) << 8) |
           (static_cast<uint32_t>(p[2]) << 16) |
           (static_cast<uint32_t>(p[3]) << 24);
}

bool sig_is(const uint8_t* p, char a, char b) {
    return p[0] == static_cast<uint8_t>(a) &&
           p[1] == static_cast<uint8_t>(b);
}

bool known_structure_signature(const uint8_t* p) {
    return sig_is(p, 'n', 'k') || sig_is(p, 'v', 'k') ||
           sig_is(p, 'l', 'f') || sig_is(p, 'l', 'h') ||
           sig_is(p, 'l', 'i') || sig_is(p, 'r', 'i') ||
           sig_is(p, 'd', 'b') || sig_is(p, 's', 'k');
}

AuditReport fail(Context& ctx, AuditStatus status, const char* message,
                 size_t file_offset, int64_t hive_offset) {
    int64_t resolved_hive =
        hive_offset >= 0 ? hive_offset : ctx.last_hive_offset;
    ctx.report.status = status;
    ctx.report.ok = false;
    ctx.report.message = message;
    ctx.report.hive_offset = resolved_hive;
    if (file_offset != 0u) {
        ctx.report.file_offset = file_offset;
    } else if (resolved_hive >= 0) {
        ctx.report.file_offset =
            REGF_BASE_SIZE + static_cast<size_t>(resolved_hive);
    } else {
        ctx.report.file_offset = 0;
    }
    return ctx.report;
}

const uint8_t* cell_payload(const Context& ctx, const CellInfo& cell) {
    return ctx.data + cell.file_start + CELL_HEADER_SIZE;
}

size_t cell_payload_size(const CellInfo& cell) {
    return cell.file_end - cell.file_start - CELL_HEADER_SIZE;
}

// Resolve a hive-relative cell reference.  Distinguishes a clean boundary
// hit from a pointer landing strictly inside another cell's footprint
// (allocated interval overlap) and from out-of-bounds / free targets.
AuditStatus resolve_cell(Context& ctx, uint32_t hive_off, size_t& out_index) {
    ctx.last_hive_offset = static_cast<int64_t>(hive_off);
    if ((hive_off & 7u) != 0u) {
        return AuditStatus::CellOffsetUnaligned;
    }
    uint64_t fpos = static_cast<uint64_t>(REGF_BASE_SIZE) + hive_off;
    if (fpos + CELL_HEADER_SIZE > ctx.size) {
        return AuditStatus::CellOutOfBounds;
    }
    auto it = ctx.boundary_index.find(hive_off);
    if (it != ctx.boundary_index.end()) {
        const CellInfo& target = ctx.cells[it->second];
        if (!target.allocated) {
            return AuditStatus::DanglingReference;
        }
        out_index = it->second;
        return AuditStatus::Ok;
    }
    size_t pos = static_cast<size_t>(fpos);
    for (const CellInfo& cell : ctx.cells) {
        if (cell.file_start <= pos && pos < cell.file_end) {
            return AuditStatus::CellOverlap;
        }
    }
    return AuditStatus::CellOutOfBounds;
}

AuditStatus check_regf(Context& ctx) {
    if (ctx.size < REGF_BASE_SIZE) {
        return AuditStatus::TooSmall;
    }
    const uint8_t* h = ctx.data;
    if (!sig_is(h, 'r', 'e') || h[2] != 'g' || h[3] != 'f') {
        return AuditStatus::BadRegfMagic;
    }
    uint32_t seq1 = rd32(h + REGF_SEQ1_OFF);
    uint32_t seq2 = rd32(h + REGF_SEQ2_OFF);
    if (seq1 != seq2) {
        return AuditStatus::SequenceMismatch;
    }
    uint32_t hbins_data_size = rd32(h + REGF_HBINS_DATA_SIZE_OFF);
    uint64_t expected_size =
        static_cast<uint64_t>(REGF_BASE_SIZE) + hbins_data_size;
    if (expected_size != static_cast<uint64_t>(ctx.size)) {
        return AuditStatus::HiveBinsSizeMismatch;
    }
    uint32_t stored_checksum = rd32(h + REGF_CHECKSUM_OFF);
    uint32_t actual_checksum = regf_checksum(h, REGF_CHECKSUM_OFF);
    if (stored_checksum != actual_checksum) {
        return AuditStatus::ChecksumMismatch;
    }
    uint32_t root = rd32(h + REGF_ROOT_CELL_OFF);
    if (root == NONE_OFFSET) {
        return AuditStatus::RootOffsetInvalid;
    }
    return AuditStatus::Ok;
}

AuditStatus scan_hbins(Context& ctx) {
    size_t cursor = REGF_BASE_SIZE;
    while (cursor < ctx.size) {
        if (ctx.size - cursor < REGF_BASE_SIZE) {
            return AuditStatus::TruncatedStructure;
        }
        const uint8_t* hbin = ctx.data + cursor;
        if (!sig_is(hbin, 'h', 'b') || hbin[2] != 'i' || hbin[3] != 'n') {
            return AuditStatus::BadHbinMagic;
        }
        uint32_t rel_off = rd32(hbin + HBIN_REL_OFF);
        uint64_t expected_rel = cursor - REGF_BASE_SIZE;
        if ((rel_off % REGF_BASE_SIZE) != 0u) {
            return AuditStatus::HbinOffsetMisaligned;
        }
        if (static_cast<uint64_t>(rel_off) != expected_rel) {
            return AuditStatus::HbinOffsetDiscontinuity;
        }
        uint32_t hbin_size = rd32(hbin + HBIN_SIZE_OFF);
        if (hbin_size < REGF_BASE_SIZE ||
            (hbin_size % REGF_BASE_SIZE) != 0u) {
            return AuditStatus::HbinSizeInvalid;
        }
        uint64_t hbin_end = static_cast<uint64_t>(cursor) + hbin_size;
        if (hbin_end > ctx.size) {
            return AuditStatus::HbinSizeInvalid;
        }

        size_t cell_cursor = cursor + HBIN_HEADER_SIZE;
        const size_t hbin_end_sz = static_cast<size_t>(hbin_end);
        while (cell_cursor < hbin_end_sz) {
            size_t remaining = hbin_end_sz - cell_cursor;
            if (remaining < CELL_HEADER_SIZE) {
                return AuditStatus::TruncatedStructure;
            }
            const uint8_t* cell_hdr = ctx.data + cell_cursor;
            uint32_t raw_size = rd32(cell_hdr);
            int32_t signed_size = static_cast<int32_t>(raw_size);
            bool allocated = signed_size < 0;
            uint32_t abs_size;
            // Allocated cells store the negated size with the high bit set;
            // clearing it yields the magnitude without signed overflow.
            abs_size = raw_size & 0x7FFFFFFFu;
            if (abs_size == 0u) {
                return AuditStatus::CellSizeZero;
            }
            if ((abs_size & 7u) != 0u) {
                return AuditStatus::CellSizeMisaligned;
            }
            if (abs_size < MIN_CELL_SIZE) {
                return AuditStatus::CellSizeZero;
            }
            if (static_cast<uint64_t>(abs_size) > remaining) {
                return AuditStatus::CellOutOfBounds;
            }

            uint32_t hive_off =
                static_cast<uint32_t>(cell_cursor - REGF_BASE_SIZE);
            CellInfo cell;
            cell.hive_offset = hive_off;
            cell.file_start = cell_cursor;
            cell.file_end = cell_cursor + static_cast<size_t>(abs_size);
            cell.allocated = allocated;
            ctx.boundary_index.emplace(hive_off, ctx.cells.size());
            ctx.cells.push_back(cell);

            cell_cursor += static_cast<size_t>(abs_size);
        }
        if (cell_cursor != hbin_end_sz) {
            return AuditStatus::TruncatedStructure;
        }
        cursor = hbin_end_sz;
        ctx.report.hbin_count++;
    }
    ctx.report.cell_count = ctx.cells.size();
    size_t allocated_count = 0;
    for (const CellInfo& cell : ctx.cells) {
        if (cell.allocated) {
            allocated_count++;
        }
    }
    ctx.report.allocated_cell_count = allocated_count;
    return AuditStatus::Ok;
}

AuditStatus visit_nk(Context& ctx, size_t cell_index, uint32_t depth);
AuditStatus visit_list(Context& ctx, uint32_t hive_off, uint32_t depth,
                       uint32_t& leaves_out);

AuditStatus visit_vk(Context& ctx, size_t cell_index) {
    CellInfo& vk_cell = ctx.cells[cell_index];
    if (vk_cell.kind == 'v') {
        return AuditStatus::Ok;  // reached via another value list
    }
    const uint8_t* p = cell_payload(ctx, vk_cell);
    size_t payload = cell_payload_size(vk_cell);
    if (payload < VK_PAYLOAD_SIZE || !sig_is(p, 'v', 'k')) {
        return AuditStatus::BadCellSignature;
    }
    vk_cell.kind = 'v';
    ctx.report.vk_count++;

    uint16_t name_off = rd16(p + VK_NAME_OFF);
    uint32_t data_size_raw = rd32(p + VK_DATA_SIZE_OFF);
    uint32_t data_off = rd32(p + VK_DATA_OFF);

    if (name_off != 0u && static_cast<size_t>(name_off) >= payload) {
        return AuditStatus::TruncatedStructure;
    }

    if ((data_size_raw & INLINE_DATA_FLAG) != 0u) {
        uint32_t inline_len = data_size_raw & ~INLINE_DATA_FLAG;
        if (inline_len > INLINE_DATA_MAX) {
            return AuditStatus::InlineDataTooLong;
        }
        return AuditStatus::Ok;  // bytes live inside the vk header
    }

    uint32_t data_len = data_size_raw;
    if (data_off == NONE_OFFSET) {
        if (data_len != 0u) {
            return AuditStatus::DanglingReference;
        }
        return AuditStatus::Ok;
    }

    size_t target_index = 0;
    AuditStatus resolved = resolve_cell(ctx, data_off, target_index);
    if (resolved != AuditStatus::Ok) {
        if (resolved == AuditStatus::CellOutOfBounds) {
            return AuditStatus::ValueDataOutOfBounds;
        }
        return resolved;
    }
    CellInfo& target = ctx.cells[target_index];
    const uint8_t* tp = cell_payload(ctx, target);
    size_t target_payload = cell_payload_size(target);

    if (target.kind != 0) {
        return AuditStatus::MetadataDataOverlap;
    }

    if (target_payload >= 2 && sig_is(tp, 'd', 'b')) {
        if (target_payload < DB_PAYLOAD_SIZE) {
            return AuditStatus::BigDataInvalid;
        }
        target.kind = 'D';
        uint32_t segment_count =
            static_cast<uint32_t>(rd16(tp + DB_SEGMENT_COUNT_OFF));
        uint32_t list_off =
            static_cast<uint32_t>(rd16(tp + DB_SEGMENT_LIST_OFF));
        if (list_off == NONE_OFFSET || (list_off & 7u) != 0u) {
            return AuditStatus::BigDataInvalid;
        }
        size_t list_index = 0;
        AuditStatus list_resolved = resolve_cell(ctx, list_off, list_index);
        if (list_resolved != AuditStatus::Ok) {
            return AuditStatus::BigDataInvalid;
        }
        CellInfo& list_cell = ctx.cells[list_index];
        if (list_cell.kind != 0) {
            return AuditStatus::MetadataDataOverlap;
        }
        size_t list_payload = cell_payload_size(list_cell);
        uint64_t need = static_cast<uint64_t>(segment_count) * 4u;
        if (need > list_payload) {
            return AuditStatus::BigDataInvalid;
        }
        list_cell.kind = 'd';

        const uint8_t* lp = cell_payload(ctx, list_cell);
        uint64_t total = 0;
        for (uint32_t i = 0; i < segment_count; ++i) {
            uint32_t seg_off = rd32(lp + static_cast<size_t>(i) * 4u);
            if (seg_off == NONE_OFFSET || (seg_off & 7u) != 0u) {
                return AuditStatus::BigDataInvalid;
            }
            size_t seg_index = 0;
            AuditStatus seg_resolved =
                resolve_cell(ctx, seg_off, seg_index);
            if (seg_resolved != AuditStatus::Ok) {
                return AuditStatus::BigDataInvalid;
            }
            CellInfo& seg_cell = ctx.cells[seg_index];
            if (seg_cell.kind != 0) {
                return AuditStatus::MetadataDataOverlap;
            }
            seg_cell.kind = 'S';
            total += cell_payload_size(seg_cell);
        }
        if (total < static_cast<uint64_t>(data_len)) {
            return AuditStatus::BigDataInvalid;
        }
        return AuditStatus::Ok;
    }

    if (target_payload >= 2 && known_structure_signature(tp)) {
        return AuditStatus::MetadataDataOverlap;
    }
    target.kind = 'P';  // plain data cell
    if (target_payload < static_cast<size_t>(data_len)) {
        return AuditStatus::ValueDataOutOfBounds;
    }
    return AuditStatus::Ok;
}

AuditStatus visit_list(Context& ctx, uint32_t hive_off, uint32_t depth,
                       uint32_t& leaves_out) {
    leaves_out = 0;
    if (hive_off == NONE_OFFSET) {
        return AuditStatus::Ok;
    }
    if (depth > ctx.options.max_tree_depth) {
        return AuditStatus::DepthLimitExceeded;
    }
    size_t cell_index = 0;
    AuditStatus resolved = resolve_cell(ctx, hive_off, cell_index);
    if (resolved != AuditStatus::Ok) {
        return resolved;
    }
    CellInfo& list_cell = ctx.cells[cell_index];
    const uint8_t* p = cell_payload(ctx, list_cell);
    size_t payload = cell_payload_size(list_cell);

    char type = 0;
    size_t fixed_size = 0;
    size_t item_size = 0;
    uint32_t count = 0;
    if (payload >= 2 && (sig_is(p, 'l', 'f') || sig_is(p, 'l', 'h'))) {
        type = sig_is(p, 'l', 'f') ? 'f' : 'h';
        fixed_size = LIST_FIXED_SHORT;
        item_size = 8;
        if (payload < fixed_size) {
            return AuditStatus::BadCellSignature;
        }
        count = static_cast<uint32_t>(rd16(p + 4));
    } else if (payload >= 2 &&
               (sig_is(p, 'l', 'i') || sig_is(p, 'r', 'i'))) {
        type = sig_is(p, 'l', 'i') ? 'i' : 'r';
        fixed_size = LIST_FIXED_LONG;
        item_size = 4;
        if (payload < fixed_size) {
            return AuditStatus::BadCellSignature;
        }
        count = rd32(p + 4);
    } else {
        return AuditStatus::BadCellSignature;
    }

    uint64_t need = static_cast<uint64_t>(fixed_size) +
                    static_cast<uint64_t>(count) * item_size;
    if (need > payload) {
        return AuditStatus::TruncatedStructure;
    }
    if (list_cell.kind != 0 && list_cell.kind != type) {
        return AuditStatus::MetadataDataOverlap;
    }
    list_cell.kind = type;

    for (uint32_t i = 0; i < count; ++i) {
        uint32_t child_off;
        if (item_size == 8) {
            child_off = rd32(p + fixed_size + static_cast<size_t>(i) * 8u);
        } else {
            child_off = rd32(p + fixed_size + static_cast<size_t>(i) * 4u);
        }
        size_t child_index = 0;
        AuditStatus child_resolved =
            resolve_cell(ctx, child_off, child_index);
        if (child_resolved != AuditStatus::Ok) {
            return child_resolved;
        }
        CellInfo& child = ctx.cells[child_index];
        const uint8_t* cp = cell_payload(ctx, child);
        size_t child_payload = cell_payload_size(child);
        if (child_payload < 2) {
            return AuditStatus::BadCellSignature;
        }
        if (type == 'r') {
            if (!sig_is(cp, 'l', 'i')) {
                return AuditStatus::BadCellSignature;
            }
            uint32_t nested = 0;
            AuditStatus nested_status =
                visit_list(ctx, child_off, depth + 1u, nested);
            if (nested_status != AuditStatus::Ok) {
                return nested_status;
            }
            if (nested > 0xFFFFFFFFu - leaves_out) {
                return AuditStatus::TruncatedStructure;
            }
            leaves_out += nested;
        } else {
            if (!sig_is(cp, 'n', 'k')) {
                return AuditStatus::BadCellSignature;
            }
            AuditStatus nk_status =
                visit_nk(ctx, child_index, depth + 1u);
            if (nk_status != AuditStatus::Ok) {
                return nk_status;
            }
            if (leaves_out == 0xFFFFFFFFu) {
                return AuditStatus::TruncatedStructure;
            }
            leaves_out++;
        }
    }
    return AuditStatus::Ok;
}

AuditStatus visit_nk(Context& ctx, size_t cell_index, uint32_t depth) {
    if (depth > ctx.options.max_tree_depth) {
        return AuditStatus::DepthLimitExceeded;
    }
    CellInfo& nk_cell = ctx.cells[cell_index];
    uint32_t hive_off = nk_cell.hive_offset;
    ctx.last_hive_offset = static_cast<int64_t>(hive_off);
    if (ctx.visited_nk.find(hive_off) != ctx.visited_nk.end()) {
        return AuditStatus::CycleDetected;  // loop or illegal cross-parent share
    }
    const uint8_t* p = cell_payload(ctx, nk_cell);
    size_t payload = cell_payload_size(nk_cell);
    if (payload < NK_PAYLOAD_SIZE || !sig_is(p, 'n', 'k')) {
        return AuditStatus::BadCellSignature;
    }
    nk_cell.kind = 'n';
    ctx.visited_nk.insert(hive_off);
    ctx.report.nk_count++;
    if (depth > ctx.report.max_depth_reached) {
        ctx.report.max_depth_reached = depth;
    }

    uint32_t stable_list = rd32(p + NK_STABLE_LIST_OFF);
    uint32_t volatile_list = rd32(p + NK_VOLATILE_LIST_OFF);
    uint32_t stable_count = rd32(p + NK_STABLE_COUNT_OFF);
    uint32_t volatile_count = rd32(p + NK_VOLATILE_COUNT_OFF);
    uint32_t value_list = rd32(p + NK_VALUE_LIST_OFF);
    uint32_t value_count = rd32(p + NK_VALUE_COUNT_OFF);
    uint32_t name_len =
        static_cast<uint32_t>(rd16(p + NK_NAME_LEN_OFF));
    uint32_t parent_off = rd32(p + NK_PARENT_OFF);

    if (parent_off != NONE_OFFSET && (parent_off & 7u) != 0u) {
        return AuditStatus::CellOffsetUnaligned;
    }

    uint64_t name_end =
        static_cast<uint64_t>(NK_PAYLOAD_SIZE) + name_len;
    if (name_end > payload) {
        return AuditStatus::TruncatedStructure;
    }

    uint32_t stable_seen = 0;
    if (stable_count != 0u) {
        if (stable_list == NONE_OFFSET || (stable_list & 7u) != 0u) {
            return AuditStatus::DanglingReference;
        }
        AuditStatus st =
            visit_list(ctx, stable_list, depth, stable_seen);
        if (st != AuditStatus::Ok) {
            return st;
        }
        if (stable_seen != stable_count) {
            return AuditStatus::BadCellSignature;
        }
    } else if (stable_list != NONE_OFFSET) {
        return AuditStatus::DanglingReference;
    }

    uint32_t volatile_seen = 0;
    if (volatile_count != 0u) {
        if (volatile_list == NONE_OFFSET || (volatile_list & 7u) != 0u) {
            return AuditStatus::DanglingReference;
        }
        AuditStatus st =
            visit_list(ctx, volatile_list, depth, volatile_seen);
        if (st != AuditStatus::Ok) {
            return st;
        }
        if (volatile_seen != volatile_count) {
            return AuditStatus::BadCellSignature;
        }
    } else if (volatile_list != NONE_OFFSET) {
        return AuditStatus::DanglingReference;
    }

    if (value_count != 0u) {
        if (value_list == NONE_OFFSET || (value_list & 7u) != 0u) {
            return AuditStatus::ValueListInvalid;
        }
        size_t vl_index = 0;
        AuditStatus vl_resolved =
            resolve_cell(ctx, value_list, vl_index);
        if (vl_resolved != AuditStatus::Ok) {
            return AuditStatus::ValueListInvalid;
        }
        CellInfo& vl_cell = ctx.cells[vl_index];
        size_t vl_payload = cell_payload_size(vl_cell);
        uint64_t vl_need = static_cast<uint64_t>(value_count) * 4u;
        if (vl_need > vl_payload) {
            return AuditStatus::ValueListInvalid;
        }
        if (vl_cell.kind != 0) {
            return AuditStatus::MetadataDataOverlap;
        }
        vl_cell.kind = 'V';
        const uint8_t* vp = cell_payload(ctx, vl_cell);
        for (uint32_t i = 0; i < value_count; ++i) {
            uint32_t vk_off = rd32(vp + static_cast<size_t>(i) * 4u);
            if (vk_off == NONE_OFFSET || (vk_off & 7u) != 0u) {
                return AuditStatus::ValueListInvalid;
            }
            size_t vk_index = 0;
            AuditStatus vk_resolved =
                resolve_cell(ctx, vk_off, vk_index);
            if (vk_resolved != AuditStatus::Ok) {
                if (vk_resolved == AuditStatus::CellOutOfBounds) {
                    return AuditStatus::ValueDataOutOfBounds;
                }
                return AuditStatus::ValueListInvalid;
            }
            AuditStatus vk_status = visit_vk(ctx, vk_index);
            if (vk_status != AuditStatus::Ok) {
                return vk_status;
            }
        }
    } else if (value_list != NONE_OFFSET) {
        return AuditStatus::ValueListInvalid;
    }

    return AuditStatus::Ok;
}

AuditStatus traverse_tree(Context& ctx) {
    uint32_t root_off = rd32(ctx.data + REGF_ROOT_CELL_OFF);
    size_t root_index = 0;
    AuditStatus resolved = resolve_cell(ctx, root_off, root_index);
    if (resolved != AuditStatus::Ok) {
        if (resolved == AuditStatus::CellOutOfBounds) {
            return AuditStatus::RootOffsetInvalid;
        }
        return resolved;
    }
    return visit_nk(ctx, root_index, 0);
}

}  // namespace

uint32_t regf_checksum(const uint8_t* data, size_t size) {
    constexpr size_t WORD_COUNT = 127;  // 508 bytes / 4
    if (data == nullptr || size != WORD_COUNT * 4u) {
        return 0u;
    }
    uint32_t checksum = 0u;
    for (size_t i = 0; i < WORD_COUNT; ++i) {
        checksum ^= rd32(data + i * 4u);
    }
    return checksum;
}

AuditReport audit(const uint8_t* data, size_t size,
                  const AuditOptions& options) {
    AuditReport report;
    if (data == nullptr) {
        report.ok = false;
        report.status = AuditStatus::NullPointer;
        report.message = "input buffer is null";
        return report;
    }

    Context ctx;
    ctx.data = data;
    ctx.size = size;
    ctx.options = options;

    AuditStatus regf_status = check_regf(ctx);
    if (regf_status != AuditStatus::Ok) {
        return fail(ctx, regf_status, "regf base block validation failed",
                    0, -1);
    }

    AuditStatus hbin_status = scan_hbins(ctx);
    if (hbin_status != AuditStatus::Ok) {
        return fail(ctx, hbin_status, "hbin/cell scan failed", 0, -1);
    }

    AuditStatus tree_status = traverse_tree(ctx);
    if (tree_status != AuditStatus::Ok) {
        return fail(ctx, tree_status, "key tree traversal failed", 0, -1);
    }

    ctx.report.status = AuditStatus::Ok;
    ctx.report.ok = true;
    ctx.report.message = "ok";
    return ctx.report;
}

AuditReport audit(const std::vector<uint8_t>& data,
                  const AuditOptions& options) {
    return audit(data.empty() ? nullptr : data.data(), data.size(), options);
}

const char* status_name(AuditStatus status) {
    switch (status) {
        case AuditStatus::Ok:                     return "Ok";
        case AuditStatus::TooSmall:               return "TooSmall";
        case AuditStatus::BadRegfMagic:           return "BadRegfMagic";
        case AuditStatus::SequenceMismatch:       return "SequenceMismatch";
        case AuditStatus::HiveBinsSizeMismatch:   return "HiveBinsSizeMismatch";
        case AuditStatus::ChecksumMismatch:       return "ChecksumMismatch";
        case AuditStatus::RootOffsetInvalid:      return "RootOffsetInvalid";
        case AuditStatus::BadHbinMagic:           return "BadHbinMagic";
        case AuditStatus::HbinOffsetMisaligned:   return "HbinOffsetMisaligned";
        case AuditStatus::HbinOffsetDiscontinuity:
            return "HbinOffsetDiscontinuity";
        case AuditStatus::HbinSizeInvalid:        return "HbinSizeInvalid";
        case AuditStatus::CellSizeZero:           return "CellSizeZero";
        case AuditStatus::CellSizeMisaligned:     return "CellSizeMisaligned";
        case AuditStatus::CellOutOfBounds:        return "CellOutOfBounds";
        case AuditStatus::CellOverlap:            return "CellOverlap";
        case AuditStatus::CellOffsetUnaligned:    return "CellOffsetUnaligned";
        case AuditStatus::DanglingReference:      return "DanglingReference";
        case AuditStatus::BadCellSignature:       return "BadCellSignature";
        case AuditStatus::DepthLimitExceeded:     return "DepthLimitExceeded";
        case AuditStatus::CycleDetected:          return "CycleDetected";
        case AuditStatus::ValueListInvalid:       return "ValueListInvalid";
        case AuditStatus::InlineDataTooLong:      return "InlineDataTooLong";
        case AuditStatus::ValueDataOutOfBounds:   return "ValueDataOutOfBounds";
        case AuditStatus::BigDataInvalid:         return "BigDataInvalid";
        case AuditStatus::MetadataDataOverlap:    return "MetadataDataOverlap";
        case AuditStatus::TruncatedStructure:     return "TruncatedStructure";
        case AuditStatus::NullPointer:            return "NullPointer";
    }
    return "Unknown";
}

}  // namespace reghive_audit
