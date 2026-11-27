#ifndef SMMUV3_VERIFY_SMMUV3_VERIFY_HPP
#define SMMUV3_VERIFY_SMMUV3_VERIFY_HPP

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <map>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace smmuv3 {

using U64 = std::uint64_t;
using U32 = std::uint32_t;
using U16 = std::uint16_t;
using U8 = std::uint8_t;

inline constexpr U64 kU64Max = std::numeric_limits<U64>::max();
inline constexpr U64 kU32Max = std::numeric_limits<U32>::max();
inline constexpr U64 kU16Max = std::numeric_limits<U16>::max();

struct Range {
    U64 begin{0};
    U64 end{0};

    constexpr bool empty() const noexcept { return begin >= end; }
    constexpr U64 size() const noexcept { return empty() ? U64{0} : end - begin; }

    constexpr bool contains(U64 value) const noexcept {
        return begin <= value && value < end;
    }
};

constexpr Range make_range(U64 begin, U64 end) noexcept {
    return Range{begin, end};
}

constexpr Range inclusive_range(U64 first, U64 last) noexcept {
    return Range{first, last == kU64Max ? kU64Max : last + U64{1}};
}

inline Range clamp_range(Range range, Range limit) noexcept {
    const U64 begin = std::max(range.begin, limit.begin);
    const U64 end = std::min(range.end, limit.end);
    if (begin >= end) {
        return Range{0, 0};
    }
    return Range{begin, end};
}

inline std::optional<Range> intersect(Range lhs, Range rhs) noexcept {
    const U64 begin = std::max(lhs.begin, rhs.begin);
    const U64 end = std::min(lhs.end, rhs.end);
    if (begin >= end) {
        return std::nullopt;
    }
    return Range{begin, end};
}

inline U64 saturating_add(U64 lhs, U64 rhs) noexcept {
    if (rhs > kU64Max - lhs) {
        return kU64Max;
    }
    return lhs + rhs;
}

inline U64 saturating_sub(U64 lhs, U64 rhs) noexcept {
    return lhs < rhs ? U64{0} : lhs - rhs;
}

constexpr U64 bit_mask(unsigned bits) noexcept {
    if (bits >= 64U) {
        return kU64Max;
    }
    if (bits == 0U) {
        return U64{0};
    }
    return (U64{1} << bits) - U64{1};
}

constexpr U64 one_shift(unsigned bits) noexcept {
    if (bits >= 64U) {
        return kU64Max;
    }
    return U64{1} << bits;
}

inline U64 extract_bits(U64 value, unsigned lo, unsigned width) noexcept {
    if (width == 0U || lo >= 64U) {
        return U64{0};
    }
    const unsigned available = 64U - lo;
    const unsigned use_width = std::min(width, available);
    return (value >> lo) & bit_mask(use_width);
}

struct Permission {
    bool read{false};
    bool write{false};

    constexpr bool any() const noexcept { return read || write; }

    static constexpr Permission none() noexcept { return Permission{false, false}; }
    static constexpr Permission read_only() noexcept { return Permission{true, false}; }
    static constexpr Permission write_only() noexcept { return Permission{false, true}; }
    static constexpr Permission read_write() noexcept { return Permission{true, true}; }
};

constexpr Permission intersect(Permission lhs, Permission rhs) noexcept {
    return Permission{lhs.read && rhs.read, lhs.write && rhs.write};
}

enum class Granule : U8 {
    k4K = 12U,
    k16K = 14U,
    k64K = 16U,
};

constexpr unsigned granule_log2(Granule granule) noexcept {
    return static_cast<unsigned>(granule);
}

constexpr unsigned granule_index_bits(Granule granule) noexcept {
    return granule_log2(granule) - 3U;
}

enum class DescriptorKind : U8 {
    Invalid = 0U,
    Table = 1U,
    Block = 2U,
    Page = 3U,
};

struct PageTableEntry {
    DescriptorKind kind{DescriptorKind::Invalid};
    U64 index{0};
    bool explicit_index{false};
    U64 descriptor{0};
    U64 output_address{0};
    Permission permissions{Permission::none()};
    U64 reserved_bits{0};
    Range explicit_input{0, 0};
    std::vector<PageTableEntry> children{};

    static PageTableEntry table(U64 index_value, std::vector<PageTableEntry> child_entries) {
        PageTableEntry entry;
        entry.kind = DescriptorKind::Table;
        entry.index = index_value;
        entry.explicit_index = true;
        entry.children = std::move(child_entries);
        return entry;
    }

    static PageTableEntry leaf(DescriptorKind kind_value, U64 index_value,
                               U64 output, Permission permissions_value) {
        PageTableEntry entry;
        entry.kind = kind_value;
        entry.index = index_value;
        entry.explicit_index = true;
        entry.output_address = output;
        entry.permissions = permissions_value;
        return entry;
    }
};

struct PageTable {
    Granule granule{Granule::k4K};
    U8 root_level{0};
    U8 input_bits{48};
    U64 table_base{0};
    U64 reserved_bits{0};
    std::vector<PageTableEntry> entries{};
};

enum class StrtabFormat : U8 {
    Invalid = 0U,
    Linear = 1U,
    TwoLevel = 2U,
};

struct StrtabBaseCfg {
    U32 raw{0};

    constexpr U8 log2size() const noexcept {
        return static_cast<U8>(raw & U32{0x3f});
    }

    constexpr U8 split() const noexcept {
        return static_cast<U8>((raw >> 6U) & U32{0x1f});
    }

    constexpr U8 fmt() const noexcept {
        return static_cast<U8>((raw >> 16U) & U32{0x3});
    }

    constexpr U64 reserved_bits() const noexcept {
        const U64 known = bit_mask(11U) | (U64{0x3} << 16U);
        return static_cast<U64>(raw) & ~known;
    }
};

struct Cd {
    bool valid{false};
    U8 t0sz{0};
    U8 tg0{0};
    bool epd0{false};
    bool aa64{true};
    U64 ttbr0{0};
    U64 reserved_bits{0};
    std::array<U64, 8> raw_words{};
    PageTable stage1{};

    static Cd from_words(const std::array<U64, 8>& words) {
        Cd cd;
        cd.raw_words = words;
        const U64 word0 = words[0];
        cd.valid = (word0 & U64{1}) != 0U;
        cd.t0sz = static_cast<U8>(extract_bits(word0, 1U, 6U));
        cd.tg0 = static_cast<U8>(extract_bits(word0, 7U, 2U));
        cd.epd0 = extract_bits(word0, 14U, 1U) != 0U;
        cd.aa64 = extract_bits(word0, 15U, 1U) != 0U;
        cd.ttbr0 = words[1] & ~U64{0xf};
        cd.reserved_bits = word0 & (bit_mask(5U) << 59U);
        return cd;
    }
};

enum class CdTableFormat : U8 {
    Linear = 0U,
    TwoLevel = 1U,
};

struct CdTable {
    CdTableFormat format{CdTableFormat::Linear};
    U64 capacity{0};
    std::map<U32, Cd> slots{};

    static CdTable linear(std::vector<Cd> cds) {
        CdTable table;
        table.format = CdTableFormat::Linear;
        table.capacity = static_cast<U64>(cds.size());
        for (std::size_t i = 0; i < cds.size(); ++i) {
            table.slots.emplace(static_cast<U32>(i), std::move(cds[i]));
        }
        return table;
    }

    static CdTable two_level(U64 slot_capacity, std::map<U32, Cd> cds) {
        CdTable table;
        table.format = CdTableFormat::TwoLevel;
        table.capacity = slot_capacity;
        table.slots = std::move(cds);
        return table;
    }

    U64 effective_capacity() const {
        if (capacity != 0U) {
            return capacity;
        }
        if (slots.empty()) {
            return 0U;
        }
        return static_cast<U64>(slots.rbegin()->first) + U64{1};
    }

    const Cd* find(U32 substream_id) const {
        if (static_cast<U64>(substream_id) >= effective_capacity()) {
            return nullptr;
        }
        const auto it = slots.find(substream_id);
        return it == slots.end() ? nullptr : &it->second;
    }
};

struct Ste {
    bool valid{false};
    U8 config{0};
    U8 s1fmt{0};
    U8 s1cdmax{0};
    U64 s1_context_ptr{0};
    U64 s2ttb{0};
    U64 reserved_bits{0};
    std::array<U64, 8> raw_words{};
    CdTable cd_table{};
    PageTable stage2{};

    static Ste from_words(const std::array<U64, 8>& words) {
        Ste ste;
        ste.raw_words = words;
        const U64 word0 = words[0];
        ste.valid = (word0 & U64{1}) != 0U;
        ste.config = static_cast<U8>(extract_bits(word0, 1U, 3U));
        ste.s1fmt = static_cast<U8>(extract_bits(word0, 4U, 2U));
        ste.s1_context_ptr = extract_bits(word0, 6U, 46U);
        ste.s1cdmax = static_cast<U8>(extract_bits(word0, 59U, 5U));
        ste.s2ttb = (extract_bits(words[7], 0U, 16U) << 32U) |
                    (words[6] & (bit_mask(28U) << 4U));
        ste.reserved_bits = word0 & (bit_mask(7U) << 52U);
        return ste;
    }
};

struct L1StreamDescriptor {
    bool valid{false};
    U8 span{0};
    U64 l2_ptr{0};
    U64 raw{0};
    U64 reserved_bits{0};
    std::vector<Ste> entries{};

    static L1StreamDescriptor from_raw(U64 raw, std::vector<Ste> l2_entries = {}) {
        L1StreamDescriptor descriptor;
        descriptor.raw = raw;
        descriptor.span = static_cast<U8>(raw & U64{0x1f});
        descriptor.valid = descriptor.span != 0U;
        descriptor.l2_ptr = extract_bits(raw, 6U, 46U);
        descriptor.reserved_bits =
            raw & (~((bit_mask(46U) << 6U) | bit_mask(5U)));
        descriptor.entries = std::move(l2_entries);
        return descriptor;
    }
};

struct StreamTable {
    StrtabFormat format{StrtabFormat::Invalid};
    U8 log2size{16U};
    U8 split{8U};
    U64 base{0};
    U64 reserved_bits{0};
    std::vector<Ste> linear{};
    std::vector<L1StreamDescriptor> l1{};

    static StreamTable from_base_cfg(U32 raw, U64 table_base = 0) {
        const StrtabBaseCfg cfg{raw};
        StreamTable table;
        table.format = cfg.fmt() == 0U ? StrtabFormat::Linear
                                       : (cfg.fmt() == 1U ? StrtabFormat::TwoLevel
                                                         : StrtabFormat::Invalid);
        table.log2size = cfg.log2size();
        table.split = cfg.split();
        table.base = table_base;
        table.reserved_bits = cfg.reserved_bits();
        return table;
    }
};

struct VmRegion {
    U32 vm_id{0};
    Range range{0, 0};
    bool writable{true};
};

struct ProtectedRegion {
    Range range{0, 0};
    bool writable{false};
};

struct DeviceScope {
    U32 vm_id{0};
    std::vector<Range> stream_ids{};

    static DeviceScope stream_ids_inclusive(U32 first, U32 last, U32 vm = 0U) {
        DeviceScope scope;
        scope.vm_id = vm;
        scope.stream_ids.push_back(
            Range{static_cast<U64>(first), static_cast<U64>(last) + U64{1}});
        return scope;
    }
};

struct VerificationInput {
    StreamTable stream_table{};
    DeviceScope device{};
    std::vector<VmRegion> vm_regions{};
    std::vector<ProtectedRegion> host_protected{};
};

enum class ViolationCode : U8 {
    None = 0U,
    StreamTableFormat,
    StreamTableReservedBits,
    StreamTableBounds,
    L1ReservedBits,
    L1SpanOutOfBounds,
    SteReservedBits,
    SteBypass,
    SteReservedConfig,
    SteInvalidS1Format,
    SteMissingContext,
    CdSlotOutOfBounds,
    CdReservedBits,
    CdGranuleMismatch,
    CdUnsupportedGranule,
    PageDescriptorReservedBits,
    PageDescriptorLevel,
    PageGranuleUnsupported,
    HostProtectedPhysical,
    CrossVmPhysical,
    ReadOnlyViolation,
    PhysicalOutsideVm,
};

struct L1StdWitness {
    U64 index{0};
    bool present{false};
    bool valid{false};
    U8 span{0};
    U64 l2_ptr{0};
    U64 raw{0};
    U64 reserved_bits{0};
    Range covered_stream_ids{0, 0};
};

struct SteWitness {
    U64 l2_index{0};
    bool present{false};
    bool valid{false};
    U8 config{0};
    U8 s1fmt{0};
    U8 s1cdmax{0};
    U64 s1_context_ptr{0};
    U64 s2ttb{0};
    U64 reserved_bits{0};
    std::array<U64, 8> raw_words{};
};

struct CdWitness {
    U32 substream_id{0};
    bool present{false};
    bool valid{false};
    U8 t0sz{0};
    U8 tg0{0};
    bool epd0{false};
    U64 ttbr0{0};
    U64 reserved_bits{0};
    std::array<U64, 8> raw_words{};
};

struct PageWalkStep {
    bool stage2{false};
    U8 level{0};
    U64 index{0};
    DescriptorKind kind{DescriptorKind::Invalid};
    U64 descriptor{0};
    U64 output_address{0};
    Range input{0, 0};
    Range output{0, 0};
    Permission permissions{Permission::none()};
};

struct Counterexample {
    U32 stream_id{0};
    U32 substream_id{0};
    U64 iova{0};
    U64 ipa{0};
    U64 pa{0};
    ViolationCode code{ViolationCode::None};
    std::string message{};
    Permission permissions{Permission::none()};
    Range iova_range{0, 0};
    Range ipa_range{0, 0};
    Range pa_range{0, 0};
    L1StdWitness l1{};
    SteWitness ste{};
    CdWitness cd{};
    std::vector<PageWalkStep> stage1_path{};
    std::vector<PageWalkStep> stage2_path{};
};

struct Result {
    bool secure{true};
    std::optional<Counterexample> violation{};
    std::vector<std::string> diagnostics{};
};

namespace detail {

inline bool key_less(const Counterexample& lhs, const Counterexample& rhs) noexcept {
    if (lhs.stream_id != rhs.stream_id) {
        return lhs.stream_id < rhs.stream_id;
    }
    if (lhs.substream_id != rhs.substream_id) {
        return lhs.substream_id < rhs.substream_id;
    }
    if (lhs.iova != rhs.iova) {
        return lhs.iova < rhs.iova;
    }
    return static_cast<unsigned>(lhs.code) < static_cast<unsigned>(rhs.code);
}

inline void consider(Result& result, Counterexample candidate) {
    result.secure = false;
    if (!result.violation.has_value() || key_less(candidate, *result.violation)) {
        result.violation = std::move(candidate);
    }
}

inline std::optional<U64> first_stream_id(const DeviceScope& scope, Range within) {
    std::optional<U64> best;
    for (const Range candidate_range : scope.stream_ids) {
        const auto overlap = intersect(candidate_range, within);
        if (!overlap.has_value()) {
            continue;
        }
        if (overlap->begin > kU32Max) {
            continue;
        }
        if (!best.has_value() || overlap->begin < *best) {
            best = overlap->begin;
        }
    }
    return best;
}

inline std::optional<U64> first_stream_id(const DeviceScope& scope, U64 begin, U64 end) {
    return first_stream_id(scope, Range{begin, end});
}

inline Range stream_id_span_for_l1(std::size_t l1_index, U8 split) noexcept {
    const U64 base = static_cast<U64>(l1_index) << split;
    return Range{base, saturating_add(base, one_shift(split))};
}

inline Range stream_id_span_for_linear(std::size_t index) noexcept {
    const U64 begin = static_cast<U64>(index);
    return Range{begin, begin + U64{1}};
}

inline std::optional<U64> l2_entries_for_span(U8 span) noexcept {
    if (span == 0U || span > 11U) {
        return std::nullopt;
    }
    return one_shift(static_cast<unsigned>(span) - 1U);
}

inline unsigned level_span_bits(Granule granule, unsigned level) noexcept {
    const unsigned index_bits = granule_index_bits(granule);
    if (level >= 3U) {
        return granule_log2(granule);
    }
    return granule_log2(granule) + index_bits * (3U - level);
}

inline Range computed_entry_range(const PageTableEntry& entry, unsigned level,
                                  Granule granule) noexcept {
    if (!entry.explicit_input.empty()) {
        return entry.explicit_input;
    }
    const unsigned span_bits = level_span_bits(granule, level);
    if (span_bits >= 64U) {
        return Range{0, kU64Max};
    }
    const U64 begin = entry.index << span_bits;
    return Range{begin, saturating_add(begin, one_shift(span_bits))};
}

inline PageWalkStep make_walk_step(bool stage2, unsigned level, U64 index,
                                   DescriptorKind kind, U64 descriptor,
                                   U64 output_address, Range input,
                                   Range output, Permission permissions) {
    PageWalkStep step;
    step.stage2 = stage2;
    step.level = static_cast<U8>(level);
    step.index = index;
    step.kind = kind;
    step.descriptor = descriptor;
    step.output_address = output_address;
    step.input = input;
    step.output = output;
    step.permissions = permissions;
    return step;
}

struct LeafMapping {
    Range input{0, 0};
    Range output{0, 0};
    Permission permissions{Permission::none()};
    U64 descriptor{0};
    std::vector<PageWalkStep> path{};
};

struct PageFault {
    ViolationCode code{ViolationCode::PageDescriptorLevel};
    U64 iova{0};
    std::string message{};
    std::vector<PageWalkStep> path{};
};

inline void walk_page_table(const PageTable& table, bool stage2,
                            const std::vector<PageTableEntry>& entries,
                            unsigned level, std::vector<PageWalkStep>& path,
                            std::vector<LeafMapping>& leaves,
                            std::vector<PageFault>& faults) {
    const unsigned leaf_level = 3U;
    for (std::size_t position = 0; position < entries.size(); ++position) {
        const PageTableEntry& entry = entries[position];
        if (entry.kind == DescriptorKind::Invalid) {
            continue;
        }
        const U64 index = entry.explicit_index ? entry.index : static_cast<U64>(position);
        const Range input = computed_entry_range(entry, level, table.granule);
        if (input.empty()) {
            continue;
        }
        if (table.input_bits < 64U &&
            input.begin >= one_shift(static_cast<unsigned>(table.input_bits))) {
            continue;
        }

        if (entry.reserved_bits != 0U) {
            PageFault fault;
            fault.code = ViolationCode::PageDescriptorReservedBits;
            fault.iova = input.begin;
            fault.message = "nonzero reserved bits in page-table descriptor";
            fault.path = path;
            fault.path.push_back(make_walk_step(
                stage2, level, index, entry.kind, entry.descriptor,
                entry.output_address, input, Range{0, 0}, entry.permissions));
            faults.push_back(std::move(fault));
            continue;
        }

        if (entry.kind == DescriptorKind::Table) {
            if (level >= leaf_level) {
                PageFault fault;
                fault.code = ViolationCode::PageDescriptorLevel;
                fault.iova = input.begin;
                fault.message = "table descriptor at leaf level";
                fault.path = path;
                fault.path.push_back(make_walk_step(
                    stage2, level, index, entry.kind, entry.descriptor,
                    entry.output_address, input, Range{0, 0}, entry.permissions));
                faults.push_back(std::move(fault));
                continue;
            }
            path.push_back(make_walk_step(
                stage2, level, index, entry.kind, entry.descriptor,
                entry.output_address, input, Range{0, 0}, entry.permissions));
            walk_page_table(table, stage2, entry.children, level + 1U, path,
                            leaves, faults);
            path.pop_back();
            continue;
        }

        if (entry.kind == DescriptorKind::Block &&
            (level < 1U || level >= leaf_level)) {
            PageFault fault;
            fault.code = ViolationCode::PageDescriptorLevel;
            fault.iova = input.begin;
            fault.message = "block descriptor at an illegal level";
            fault.path = path;
            fault.path.push_back(make_walk_step(
                stage2, level, index, entry.kind, entry.descriptor,
                entry.output_address, input, Range{0, 0}, entry.permissions));
            faults.push_back(std::move(fault));
            continue;
        }
        if (entry.kind == DescriptorKind::Page && level != leaf_level) {
            PageFault fault;
            fault.code = ViolationCode::PageDescriptorLevel;
            fault.iova = input.begin;
            fault.message = "page descriptor at non-leaf level";
            fault.path = path;
            fault.path.push_back(make_walk_step(
                stage2, level, index, entry.kind, entry.descriptor,
                entry.output_address, input, Range{0, 0}, entry.permissions));
            faults.push_back(std::move(fault));
            continue;
        }

        const Range output{
            entry.output_address,
            saturating_add(entry.output_address, input.size())};
        path.push_back(make_walk_step(
            stage2, level, index, entry.kind, entry.descriptor,
            entry.output_address, input, output, entry.permissions));
        LeafMapping leaf;
        leaf.input = input;
        leaf.output = output;
        leaf.permissions = entry.permissions;
        leaf.descriptor = entry.descriptor;
        leaf.path = path;
        leaves.push_back(std::move(leaf));
        path.pop_back();
    }
}

inline std::vector<LeafMapping> collect_leaves(const PageTable& table, bool stage2,
                                               std::vector<PageFault>& faults) {
    std::vector<LeafMapping> leaves;
    std::vector<PageWalkStep> path;
    if (table.reserved_bits != 0U) {
        PageFault fault;
        fault.code = ViolationCode::PageDescriptorReservedBits;
        fault.iova = 0;
        fault.message = "nonzero reserved bits in page-table base";
        faults.push_back(std::move(fault));
    }
    walk_page_table(table, stage2, table.entries, table.root_level, path, leaves, faults);
    std::sort(leaves.begin(), leaves.end(), [](const LeafMapping& lhs, const LeafMapping& rhs) {
        if (lhs.input.begin != rhs.input.begin) {
            return lhs.input.begin < rhs.input.begin;
        }
        return lhs.input.end < rhs.input.end;
    });
    return leaves;
}

inline std::optional<Range> first_intersection(
    Range range, const std::vector<Range>& candidates) {
    std::optional<Range> best;
    for (const Range candidate : candidates) {
        const auto overlap = intersect(range, candidate);
        if (!overlap.has_value()) {
            continue;
        }
        if (!best.has_value() || overlap->begin < best->begin) {
            best = overlap;
        }
    }
    return best;
}

inline std::vector<Range> merged_ranges(std::vector<Range> ranges) {
    ranges.erase(std::remove_if(ranges.begin(), ranges.end(),
                                [](const Range range) { return range.empty(); }),
                 ranges.end());
    std::sort(ranges.begin(), ranges.end(), [](const Range lhs, const Range rhs) {
        if (lhs.begin != rhs.begin) {
            return lhs.begin < rhs.begin;
        }
        return lhs.end < rhs.end;
    });
    std::vector<Range> merged;
    for (const Range range : ranges) {
        if (merged.empty() || range.begin > merged.back().end) {
            merged.push_back(range);
            continue;
        }
        merged.back().end = std::max(merged.back().end, range.end);
    }
    return merged;
}

inline std::optional<U64> first_uncovered(Range range, const std::vector<Range>& covered) {
    U64 cursor = range.begin;
    for (const Range candidate : covered) {
        if (candidate.end <= cursor) {
            continue;
        }
        if (candidate.begin >= range.end) {
            break;
        }
        if (candidate.begin > cursor) {
            return cursor;
        }
        cursor = std::max(cursor, candidate.end);
        if (cursor >= range.end) {
            return std::nullopt;
        }
    }
    if (cursor < range.end) {
        return cursor;
    }
    return std::nullopt;
}

inline U64 iova_for_pa(const LeafMapping& stage1_leaf, U64 ipa) {
    const U64 ipa_offset = saturating_sub(ipa, stage1_leaf.output.begin);
    return saturating_add(stage1_leaf.input.begin, ipa_offset);
}

inline U64 pa_for_iova(const LeafMapping& stage2_leaf, U64 iova) {
    const U64 input_offset = saturating_sub(iova, stage2_leaf.input.begin);
    return saturating_add(stage2_leaf.output.begin, input_offset);
}

struct EffectiveMapping {
    Range iova{0, 0};
    Range ipa{0, 0};
    Range pa{0, 0};
    Permission permissions{Permission::none()};
    std::vector<PageWalkStep> stage1_path{};
    std::vector<PageWalkStep> stage2_path{};
};

inline std::vector<EffectiveMapping> combine_nested(
    const std::vector<LeafMapping>& stage1, const std::vector<LeafMapping>& stage2) {
    std::vector<EffectiveMapping> result;
    for (const LeafMapping& s1 : stage1) {
        for (const LeafMapping& s2 : stage2) {
            if (s2.input.end <= s1.output.begin) {
                continue;
            }
            if (s2.input.begin >= s1.output.end) {
                break;
            }
            const auto overlap = intersect(s1.output, s2.input);
            if (!overlap.has_value()) {
                continue;
            }
            const U64 iova_begin = iova_for_pa(s1, overlap->begin);
            const U64 iova_end = saturating_add(
                iova_begin, overlap->size());
            const U64 pa_begin = pa_for_iova(s2, overlap->begin);
            const U64 pa_end = saturating_add(pa_begin, overlap->size());
            EffectiveMapping mapping;
            mapping.iova = Range{iova_begin, iova_end};
            mapping.ipa = *overlap;
            mapping.pa = Range{pa_begin, pa_end};
            mapping.permissions = intersect(s1.permissions, s2.permissions);
            mapping.stage1_path = s1.path;
            mapping.stage2_path = s2.path;
            result.push_back(std::move(mapping));
        }
    }
    return result;
}

inline std::vector<EffectiveMapping> direct_stage1(
    const std::vector<LeafMapping>& stage1) {
    std::vector<EffectiveMapping> result;
    for (const LeafMapping& s1 : stage1) {
        EffectiveMapping mapping;
        mapping.iova = s1.input;
        mapping.ipa = s1.output;
        mapping.pa = s1.output;
        mapping.permissions = s1.permissions;
        mapping.stage1_path = s1.path;
        result.push_back(std::move(mapping));
    }
    return result;
}

inline std::vector<EffectiveMapping> direct_stage2(
    const std::vector<LeafMapping>& stage2) {
    std::vector<EffectiveMapping> result;
    for (const LeafMapping& s2 : stage2) {
        EffectiveMapping mapping;
        mapping.iova = s2.input;
        mapping.ipa = s2.input;
        mapping.pa = s2.output;
        mapping.permissions = s2.permissions;
        mapping.stage2_path = s2.path;
        result.push_back(std::move(mapping));
    }
    return result;
}

inline U64 make_key_iova(U64 value) noexcept { return value; }

struct CandidateContext {
    U32 stream_id{0};
    U32 substream_id{0};
    L1StdWitness l1{};
    SteWitness ste{};
    CdWitness cd{};
    bool has_cd{false};
    U32 vm_id{0};
    std::vector<PageWalkStep> stage1_path{};
    std::vector<PageWalkStep> stage2_path{};
};

inline SteWitness make_ste_witness(const Ste& ste, U64 l2_index) {
    SteWitness witness;
    witness.l2_index = l2_index;
    witness.present = true;
    witness.valid = ste.valid;
    witness.config = ste.config;
    witness.s1fmt = ste.s1fmt;
    witness.s1cdmax = ste.s1cdmax;
    witness.s1_context_ptr = ste.s1_context_ptr;
    witness.s2ttb = ste.s2ttb;
    witness.reserved_bits = ste.reserved_bits;
    witness.raw_words = ste.raw_words;
    return witness;
}

inline L1StdWitness make_l1_witness(const L1StreamDescriptor& descriptor,
                                    std::size_t l1_index, U8 split) {
    L1StdWitness witness;
    witness.index = static_cast<U64>(l1_index);
    witness.present = true;
    witness.valid = descriptor.valid;
    witness.span = descriptor.span;
    witness.l2_ptr = descriptor.l2_ptr;
    witness.raw = descriptor.raw;
    witness.reserved_bits = descriptor.reserved_bits;
    witness.covered_stream_ids = stream_id_span_for_l1(l1_index, split);
    return witness;
}

inline CdWitness make_cd_witness(const Cd& cd, U32 substream_id) {
    CdWitness witness;
    witness.substream_id = substream_id;
    witness.present = true;
    witness.valid = cd.valid;
    witness.t0sz = cd.t0sz;
    witness.tg0 = cd.tg0;
    witness.epd0 = cd.epd0;
    witness.ttbr0 = cd.ttbr0;
    witness.reserved_bits = cd.reserved_bits;
    witness.raw_words = cd.raw_words;
    return witness;
}

inline Counterexample base_counterexample(const CandidateContext& context,
                                          ViolationCode code,
                                          std::string message) {
    Counterexample result;
    result.stream_id = context.stream_id;
    result.substream_id = context.substream_id;
    result.iova = 0;
    result.code = code;
    result.message = std::move(message);
    result.l1 = context.l1;
    result.ste = context.ste;
    result.cd = context.cd;
    result.stage1_path = context.stage1_path;
    result.stage2_path = context.stage2_path;
    return result;
}

inline Counterexample structural_counterexample(const CandidateContext& context,
                                                ViolationCode code,
                                                std::string message) {
    return base_counterexample(context, code, std::move(message));
}

inline std::vector<Range> vm_ranges_for(const VerificationInput& input, U32 vm_id,
                                        bool only_writable,
                                        bool only_read_only) {
    std::vector<Range> ranges;
    for (const VmRegion& region : input.vm_regions) {
        if (region.vm_id != vm_id || region.range.empty()) {
            continue;
        }
        if (only_writable && !region.writable) {
            continue;
        }
        if (only_read_only && region.writable) {
            continue;
        }
        ranges.push_back(region.range);
    }
    return merged_ranges(std::move(ranges));
}

inline bool intersects_any(Range range, const std::vector<Range>& candidates) {
    for (const Range candidate : candidates) {
        if (intersect(range, candidate).has_value()) {
            return true;
        }
    }
    return false;
}

inline std::vector<Range> subtract_ranges(Range range,
                                          const std::vector<Range>& covered) {
    std::vector<Range> gaps;
    U64 cursor = range.begin;
    for (const Range item : covered) {
        if (item.end <= cursor) {
            continue;
        }
        if (item.begin >= range.end) {
            break;
        }
        if (item.begin > cursor) {
            gaps.push_back(Range{cursor, std::min(item.begin, range.end)});
        }
        cursor = std::max(cursor, item.end);
        if (cursor >= range.end) {
            return gaps;
        }
    }
    if (cursor < range.end) {
        gaps.push_back(Range{cursor, range.end});
    }
    return gaps;
}

inline Counterexample mapping_counterexample(const CandidateContext& context,
                                             const EffectiveMapping& mapping,
                                             ViolationCode code,
                                             Range pa_overlap,
                                             U64 iova,
                                             std::string message) {
    Counterexample result = base_counterexample(context, code, std::move(message));
    result.iova = iova;
    result.ipa = saturating_add(
        mapping.ipa.begin, saturating_sub(iova, mapping.iova.begin));
    result.pa = saturating_add(
        mapping.pa.begin, saturating_sub(iova, mapping.iova.begin));
    result.permissions = mapping.permissions;
    const U64 iova_begin = saturating_add(
        mapping.iova.begin, saturating_sub(pa_overlap.begin, mapping.pa.begin));
    const U64 iova_end = saturating_add(
        iova_begin, pa_overlap.size());
    result.iova_range = Range{iova_begin, iova_end};
    result.pa_range = pa_overlap;
    const U64 ipa_begin = saturating_add(
        mapping.ipa.begin, saturating_sub(pa_overlap.begin, mapping.pa.begin));
    result.ipa_range = Range{ipa_begin, saturating_add(ipa_begin, pa_overlap.size())};
    result.stage1_path = mapping.stage1_path;
    result.stage2_path = mapping.stage2_path;
    return result;
}

inline void analyze_mapping(Result& result, const VerificationInput& input,
                            const CandidateContext& context,
                            const EffectiveMapping& mapping) {
    if (!mapping.permissions.any() || mapping.pa.empty()) {
        return;
    }

    for (const ProtectedRegion& host : input.host_protected) {
        const auto overlap = intersect(mapping.pa, host.range);
        if (!overlap.has_value()) {
            continue;
        }
        const U64 iova = saturating_add(
            mapping.iova.begin, saturating_sub(overlap->begin, mapping.pa.begin));
        consider(result, mapping_counterexample(
            context, mapping, ViolationCode::HostProtectedPhysical, *overlap, iova,
            "DMA mapping reaches a host-protected physical range"));
    }

    std::vector<Range> other_vm_ranges;
    std::vector<Range> read_only_own_ranges;
    for (const VmRegion& vm : input.vm_regions) {
        if (vm.vm_id == context.vm_id) {
            if (!vm.writable) {
                read_only_own_ranges.push_back(vm.range);
            }
        } else {
            other_vm_ranges.push_back(vm.range);
        }
    }
    other_vm_ranges = merged_ranges(std::move(other_vm_ranges));
    read_only_own_ranges = merged_ranges(std::move(read_only_own_ranges));

    for (const Range range : other_vm_ranges) {
        const auto overlap = intersect(mapping.pa, range);
        if (!overlap.has_value()) {
            continue;
        }
        const U64 iova = saturating_add(
            mapping.iova.begin, saturating_sub(overlap->begin, mapping.pa.begin));
        consider(result, mapping_counterexample(
            context, mapping, ViolationCode::CrossVmPhysical, *overlap, iova,
            "DMA mapping reaches physical memory owned by another VM"));
    }

    if (mapping.permissions.write) {
        for (const Range range : read_only_own_ranges) {
            const auto overlap = intersect(mapping.pa, range);
            if (!overlap.has_value()) {
                continue;
            }
            const U64 iova = saturating_add(
                mapping.iova.begin, saturating_sub(overlap->begin, mapping.pa.begin));
            consider(result, mapping_counterexample(
                context, mapping, ViolationCode::ReadOnlyViolation, *overlap, iova,
                "writable DMA mapping reaches a read-only VM physical range"));
        }
    }

    const std::vector<Range> own_ranges =
        vm_ranges_for(input, context.vm_id, false, false);
    const std::vector<Range> gaps = subtract_ranges(mapping.pa, own_ranges);
    if (!gaps.empty()) {
        std::vector<Range> all_known;
        for (const VmRegion& vm : input.vm_regions) {
            all_known.push_back(vm.range);
        }
        for (const ProtectedRegion& host : input.host_protected) {
            all_known.push_back(host.range);
        }
        all_known = merged_ranges(std::move(all_known));
        for (const Range gap : gaps) {
            if (intersects_any(gap, all_known)) {
                continue;
            }
            const U64 iova = saturating_add(
                mapping.iova.begin, saturating_sub(gap.begin, mapping.pa.begin));
            consider(result, mapping_counterexample(
                context, mapping, ViolationCode::PhysicalOutsideVm, gap, iova,
                "DMA mapping reaches physical memory outside every supplied VM region"));
            break;
        }
    }
}

struct StreamCandidate {
    U32 stream_id{0};
    L1StdWitness l1{};
    SteWitness ste{};
    const Ste* ste_pointer{nullptr};
    const L1StreamDescriptor* l1_pointer{nullptr};
    U64 l2_index{0};
};

inline CandidateContext candidate_context(const StreamCandidate& candidate,
                                          U32 vm_id) {
    CandidateContext context;
    context.stream_id = candidate.stream_id;
    context.l1 = candidate.l1;
    context.ste = candidate.ste;
    context.vm_id = vm_id;
    return context;
}

inline void consider_page_faults(Result& result, const CandidateContext& context,
                                 const std::vector<PageFault>& faults,
                                 bool stage2) {
    for (const PageFault& fault : faults) {
        Counterexample candidate = base_counterexample(context, fault.code, fault.message);
        candidate.iova = fault.iova;
        if (stage2) {
            candidate.stage2_path = fault.path;
        } else {
            candidate.stage1_path = fault.path;
        }
        consider(result, std::move(candidate));
    }
}

inline void analyze_page_tables(Result& result, const VerificationInput& input,
                                const CandidateContext& context,
                                const std::vector<LeafMapping>& stage1,
                                const std::vector<LeafMapping>& stage2,
                                bool use_stage1, bool use_stage2) {
    if (use_stage1 && use_stage2) {
        const std::vector<EffectiveMapping> mappings =
            combine_nested(stage1, stage2);
        for (const EffectiveMapping& mapping : mappings) {
            analyze_mapping(result, input, context, mapping);
        }
        return;
    }
    if (use_stage1) {
        for (const EffectiveMapping& mapping : direct_stage1(stage1)) {
            analyze_mapping(result, input, context, mapping);
        }
        return;
    }
    if (use_stage2) {
        for (const EffectiveMapping& mapping : direct_stage2(stage2)) {
            analyze_mapping(result, input, context, mapping);
        }
    }
}

inline Granule granule_from_tg(U8 tg, bool& reserved) {
    reserved = false;
    switch (tg) {
        case 0U:
            return Granule::k4K;
        case 1U:
            return Granule::k64K;
        case 2U:
            return Granule::k16K;
        default:
            reserved = true;
            return Granule::k4K;
    }
}

inline void analyze_cd(Result& result, const VerificationInput& input,
                       const CandidateContext& context, const Cd& cd,
                       const std::vector<LeafMapping>& stage2,
                       bool use_stage2) {
    if (!cd.valid) {
        return;
    }
    if (cd.reserved_bits != 0U) {
        consider(result, structural_counterexample(
            context, ViolationCode::CdReservedBits,
            "nonzero reserved bits in context descriptor"));
    }

    if (cd.epd0) {
        if (!use_stage2) {
            consider(result, structural_counterexample(
                context, ViolationCode::SteBypass,
                "stage-1-only context has EPD0 set, leaving DMA untranslated"));
        } else {
            analyze_page_tables(result, input, context, {}, stage2, false, true);
        }
        return;
    }

    bool reserved_tg = false;
    const Granule cd_granule = granule_from_tg(cd.tg0, reserved_tg);
    if (reserved_tg || cd_granule == Granule::k16K) {
        consider(result, structural_counterexample(
            context, ViolationCode::CdUnsupportedGranule,
            "context descriptor selects an unsupported or reserved granule"));
    }
    if (!reserved_tg && cd_granule != Granule::k16K &&
        cd_granule != cd.stage1.granule) {
        consider(result, structural_counterexample(
            context, ViolationCode::CdGranuleMismatch,
            "CD.TG0 does not match the supplied stage-1 page-table granule"));
    }

    std::vector<PageFault> stage1_faults;
    const std::vector<LeafMapping> stage1 =
        collect_leaves(cd.stage1, false, stage1_faults);
    consider_page_faults(result, context, stage1_faults, false);
    analyze_page_tables(result, input, context, stage1, stage2, true, use_stage2);
}

inline void analyze_ste(Result& result, const VerificationInput& input,
                        const StreamCandidate& candidate) {
    if (candidate.ste_pointer == nullptr) {
        return;
    }
    const Ste& ste = *candidate.ste_pointer;
    CandidateContext context = candidate_context(candidate, input.device.vm_id);
    if (!ste.valid) {
        return;
    }

    if (ste.reserved_bits != 0U) {
        consider(result, structural_counterexample(
            context, ViolationCode::SteReservedBits,
            "nonzero reserved bits in stream table entry"));
    }

    if (ste.config == 4U) {
        consider(result, structural_counterexample(
            context, ViolationCode::SteBypass,
            "STE.Config selects Bypass, allowing untranslated DMA"));
        return;
    }
    if (ste.config >= 1U && ste.config <= 3U) {
        consider(result, structural_counterexample(
            context, ViolationCode::SteReservedConfig,
            "STE.Config contains a reserved encoding"));
        return;
    }
    if (ste.config == 0U) {
        return;
    }
    if (ste.config != 5U && ste.config != 6U && ste.config != 7U) {
        consider(result, structural_counterexample(
            context, ViolationCode::SteReservedConfig,
            "STE.Config is not a defined translation mode"));
        return;
    }

    const bool use_stage1 = ste.config == 5U || ste.config == 7U;
    const bool use_stage2 = ste.config == 6U || ste.config == 7U;

    std::vector<PageFault> stage2_faults;
    const std::vector<LeafMapping> stage2 =
        use_stage2 ? collect_leaves(ste.stage2, true, stage2_faults)
                   : std::vector<LeafMapping>{};
    consider_page_faults(result, context, stage2_faults, true);

    if (!use_stage1) {
        analyze_page_tables(result, input, context, {}, stage2, false, true);
        return;
    }

    if (ste.s1fmt > 1U) {
        consider(result, structural_counterexample(
            context, ViolationCode::SteInvalidS1Format,
            "STE.S1FMT contains a reserved CD-table format"));
    }

    const U64 configured_capacity = one_shift(ste.s1cdmax);
    const U64 actual_capacity = ste.cd_table.effective_capacity();
    if (actual_capacity < configured_capacity && actual_capacity <= kU32Max) {
        CandidateContext overflow = context;
        overflow.substream_id = static_cast<U32>(actual_capacity);
        consider(result, structural_counterexample(
            overflow, ViolationCode::CdSlotOutOfBounds,
            "STE.S1CDMax addresses a CD slot beyond the allocated CD table"));
    }

    for (const auto& item : ste.cd_table.slots) {
        const U32 substream_id = item.first;
        if (static_cast<U64>(substream_id) >= actual_capacity ||
            static_cast<U64>(substream_id) >= configured_capacity) {
            continue;
        }
        CandidateContext cd_context = context;
        cd_context.substream_id = substream_id;
        cd_context.cd = make_cd_witness(item.second, substream_id);
        cd_context.has_cd = true;
        analyze_cd(result, input, cd_context, item.second, stage2, use_stage2);
    }
}

inline Counterexample stream_table_counterexample(U64 stream_id,
                                                 ViolationCode code,
                                                 std::string message) {
    Counterexample result;
    result.stream_id = static_cast<U32>(std::min(stream_id, kU32Max));
    result.iova = 0;
    result.code = code;
    result.message = std::move(message);
    return result;
}

inline std::vector<StreamCandidate> scan_stream_table(Result& result,
                                                      const VerificationInput& input) {
    std::vector<StreamCandidate> candidates;
    const StreamTable& table = input.stream_table;

    const std::optional<U64> first_scope =
        first_stream_id(input.device, Range{0, one_shift(32U)});
    const U64 fallback_sid = first_scope.value_or(U64{0});

    if (table.reserved_bits != 0U) {
        consider(result, stream_table_counterexample(
            fallback_sid, ViolationCode::StreamTableReservedBits,
            "nonzero reserved bits in STRTAB_BASE_CFG"));
    }
    if (table.format == StrtabFormat::Invalid) {
        consider(result, stream_table_counterexample(
            fallback_sid, ViolationCode::StreamTableFormat,
            "STRTAB_BASE_CFG selects an unsupported stream-table format"));
        return candidates;
    }
    if (table.log2size > 32U) {
        consider(result, stream_table_counterexample(
            fallback_sid, ViolationCode::StreamTableBounds,
            "STRTAB_BASE_CFG.LOG2SIZE exceeds the 32-bit StreamID space"));
    }

    if (table.format == StrtabFormat::Linear) {
        for (std::size_t index = 0; index < table.linear.size(); ++index) {
            const Ste& ste = table.linear[index];
            if (!ste.valid) {
                continue;
            }
            const std::optional<U64> sid =
                first_stream_id(input.device, stream_id_span_for_linear(index));
            if (!sid.has_value() || *sid > kU32Max) {
                continue;
            }
            StreamCandidate candidate;
            candidate.stream_id = static_cast<U32>(*sid);
            candidate.ste = make_ste_witness(ste, 0);
            candidate.ste_pointer = &ste;
            candidate.l2_index = 0;
            candidates.push_back(candidate);
        }
        std::sort(candidates.begin(), candidates.end(),
                  [](const StreamCandidate& lhs, const StreamCandidate& rhs) {
                      return lhs.stream_id < rhs.stream_id;
                  });
        return candidates;
    }

    if (table.split > 31U || table.log2size < table.split) {
        consider(result, stream_table_counterexample(
            fallback_sid, ViolationCode::StreamTableBounds,
            "STRTAB_BASE_CFG has an invalid LOG2SIZE/SPLIT combination"));
        return candidates;
    }

    const unsigned l1_bits = static_cast<unsigned>(table.log2size - table.split);
    const U64 l1_count = one_shift(l1_bits);
    for (std::size_t l1_index = 0; l1_index < table.l1.size(); ++l1_index) {
        if (static_cast<U64>(l1_index) >= l1_count) {
            break;
        }
        const L1StreamDescriptor& descriptor = table.l1[l1_index];
        const Range l1_range = stream_id_span_for_l1(l1_index, table.split);
        const std::optional<U64> l1_sid = first_stream_id(input.device, l1_range);
        if (!l1_sid.has_value()) {
            continue;
        }
        const L1StdWitness l1_witness =
            make_l1_witness(descriptor, l1_index, table.split);

        CandidateContext span_context;
        span_context.stream_id = static_cast<U32>(*l1_sid);
        span_context.l1 = l1_witness;
        span_context.vm_id = input.device.vm_id;
        if (!descriptor.valid || descriptor.span == 0U) {
            continue;
        }
        if (descriptor.span > static_cast<U8>(table.split + 1U)) {
            consider(result, structural_counterexample(
                span_context, ViolationCode::L1SpanOutOfBounds,
                "L1STD.Span exceeds STRTAB_BASE_CFG.SPLIT+1 and can spill into an adjacent L2 table"));
        }
        const std::optional<U64> span_entries = l2_entries_for_span(descriptor.span);
        if (!span_entries.has_value()) {
            consider(result, structural_counterexample(
                span_context, ViolationCode::L1SpanOutOfBounds,
                "L1STD.Span contains a reserved encoding"));
            continue;
        }
        if (descriptor.reserved_bits != 0U) {
            consider(result, structural_counterexample(
                span_context, ViolationCode::L1ReservedBits,
                "nonzero reserved bits in L1 stream table descriptor"));
        }
        const U64 usable_entries =
            std::min<U64>(*span_entries, one_shift(table.split));
        for (std::size_t l2_index = 0;
             l2_index < descriptor.entries.size() &&
             static_cast<U64>(l2_index) < usable_entries;
             ++l2_index) {
            const Ste& ste = descriptor.entries[l2_index];
            if (!ste.valid) {
                continue;
            }
            const U64 sid_value =
                l1_range.begin + static_cast<U64>(l2_index);
            if (sid_value >= one_shift(static_cast<unsigned>(table.log2size))) {
                continue;
            }
            const std::optional<U64> sid = first_stream_id(
                input.device, Range{sid_value, sid_value + U64{1}});
            if (!sid.has_value() || *sid > kU32Max) {
                continue;
            }
            StreamCandidate candidate;
            candidate.stream_id = static_cast<U32>(*sid);
            candidate.l1 = l1_witness;
            candidate.ste = make_ste_witness(ste, static_cast<U64>(l2_index));
            candidate.ste_pointer = &ste;
            candidate.l1_pointer = &descriptor;
            candidate.l2_index = static_cast<U64>(l2_index);
            candidates.push_back(candidate);
        }
    }
    std::sort(candidates.begin(), candidates.end(),
              [](const StreamCandidate& lhs, const StreamCandidate& rhs) {
                  if (lhs.stream_id != rhs.stream_id) {
                      return lhs.stream_id < rhs.stream_id;
                  }
                  return lhs.l2_index < rhs.l2_index;
              });
    return candidates;
}

}  // namespace detail

inline Result verify(const VerificationInput& input) {
    Result result;
    const std::vector<detail::StreamCandidate> candidates =
        detail::scan_stream_table(result, input);
    for (const detail::StreamCandidate& candidate : candidates) {
        detail::analyze_ste(result, input, candidate);
    }
    result.secure = !result.violation.has_value();
    return result;
}

}  // namespace smmuv3

#endif
