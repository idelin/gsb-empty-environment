#include "smmuv3_verify/smmuv3_verify.hpp"

#include <array>
#include <cstdint>
#include <iostream>
#include <optional>
#include <random>
#include <string>
#include <utility>
#include <vector>

namespace {

using smmuv3::Cd;
using smmuv3::CdTable;
using smmuv3::Counterexample;
using smmuv3::DescriptorKind;
using smmuv3::DeviceScope;
using smmuv3::Granule;
using smmuv3::PageTable;
using smmuv3::PageTableEntry;
using smmuv3::Permission;
using smmuv3::ProtectedRegion;
using smmuv3::Range;
using smmuv3::Ste;
using smmuv3::StreamTable;
using smmuv3::StrtabFormat;
using smmuv3::U32;
using smmuv3::U64;
using smmuv3::U8;
using smmuv3::VerificationInput;
using smmuv3::ViolationCode;
using smmuv3::VmRegion;

int failures = 0;

void check(bool condition, const std::string& message) {
    if (!condition) {
        ++failures;
        std::cerr << "FAIL: " << message << '\n';
    }
}

void check_key(const smmuv3::Result& result, U32 stream_id, U32 substream_id,
               U64 iova, ViolationCode code, const std::string& message) {
    const bool present = result.violation.has_value();
    check(present, message + " (no violation)");
    if (!present) {
        return;
    }
    const Counterexample& violation = *result.violation;
    check(violation.stream_id == stream_id, message + " (stream id)");
    check(violation.substream_id == substream_id, message + " (substream id)");
    check(violation.iova == iova, message + " (iova)");
    check(violation.code == code, message + " (code)");
}

PageTableEntry page(U64 index, U64 output, Permission permissions) {
    PageTableEntry entry =
        PageTableEntry::leaf(DescriptorKind::Page, index, output, permissions);
    entry.explicit_input = Range{index * U64{4096}, (index + U64{1}) * U64{4096}};
    return entry;
}

PageTableEntry block2m(U64 index, U64 output, Permission permissions) {
    PageTableEntry entry =
        PageTableEntry::leaf(DescriptorKind::Block, index, output, permissions);
    entry.explicit_input = Range{index * U64{0x200000},
                                 (index + U64{1}) * U64{0x200000}};
    return entry;
}

PageTable table4k(U8 input_bits, U8 root_level, std::vector<PageTableEntry> entries) {
    PageTable table;
    table.granule = Granule::k4K;
    table.input_bits = input_bits;
    table.root_level = root_level;
    table.entries = std::move(entries);
    return table;
}

StreamTable two_level_stream_table(std::vector<smmuv3::L1StreamDescriptor> l1) {
    StreamTable table;
    table.format = StrtabFormat::TwoLevel;
    table.log2size = 16;
    table.split = 8;
    table.l1 = std::move(l1);
    return table;
}

smmuv3::L1StreamDescriptor l1_with_entries(std::vector<Ste> entries, U8 span = 9) {
    smmuv3::L1StreamDescriptor descriptor;
    descriptor.valid = true;
    descriptor.span = span;
    descriptor.entries = std::move(entries);
    return descriptor;
}

Ste nested_ste(CdTable cds, PageTable stage2) {
    Ste ste;
    ste.valid = true;
    ste.config = 7;
    ste.s1fmt = 0;
    ste.s1cdmax = 0;
    ste.cd_table = std::move(cds);
    ste.stage2 = std::move(stage2);
    return ste;
}

Cd nested_cd(PageTable stage1) {
    Cd cd;
    cd.valid = true;
    cd.t0sz = 16;
    cd.tg0 = 0;
    cd.stage1 = std::move(stage1);
    return cd;
}

VerificationInput minimal_input(Ste ste, U32 stream_id = 0x40) {
    std::vector<Ste> l2(static_cast<std::size_t>(stream_id) + 1U);
    l2[stream_id] = std::move(ste);
    VerificationInput input;
    input.stream_table = two_level_stream_table({l1_with_entries(std::move(l2))});
    input.device = DeviceScope::stream_ids_inclusive(stream_id, stream_id, 0);
    return input;
}

void test_parse_base_cfg_and_ste() {
    const U32 cfg_raw =
        U32{16} | (U32{8} << 6U) | (U32{1} << 16U);
    const StreamTable table = StreamTable::from_base_cfg(cfg_raw);
    check(table.format == StrtabFormat::TwoLevel, "parse STRTAB FMT");
    check(table.log2size == 16U, "parse STRTAB LOG2SIZE");
    check(table.split == 8U, "parse STRTAB SPLIT");
    check(table.reserved_bits == 0U, "parse STRTAB reserved");
    check(StreamTable::from_base_cfg(cfg_raw | (U32{1} << 11U)).reserved_bits != 0U,
          "detect STRTAB reserved bit 11");

    std::array<U64, 8> words{};
    words[0] = U64{1} | (U64{4} << 1U) | (U64{9} << 59U);
    const Ste bypass = Ste::from_words(words);
    check(bypass.valid, "parse STE.V");
    check(bypass.config == 4U, "parse STE.Config");
    check(bypass.s1cdmax == 9U, "parse STE.S1CDMax");

    words.fill(0);
    words[0] = U64{1} | (U64{5} << 1U) | (U64{1} << 52U);
    const Ste reserved = Ste::from_words(words);
    check(reserved.reserved_bits != 0U, "detect STE reserved bits");
}

void test_bypass_and_span() {
    Ste bypass;
    bypass.valid = true;
    bypass.config = 4;
    VerificationInput input = minimal_input(bypass, 0x40);
    check_key(smmuv3::verify(input), 0x40, 0, 0, ViolationCode::SteBypass,
              "STE bypass");

    VerificationInput out_of_scope = minimal_input(bypass, 0x40);
    out_of_scope.device = DeviceScope::stream_ids_inclusive(0x41, 0x41, 0);
    check(smmuv3::verify(out_of_scope).secure,
          "bypass outside the legal StreamID scope is not reachable");

    Ste reserved;
    reserved.valid = true;
    reserved.config = 0;
    reserved.reserved_bits = 1;
    check_key(smmuv3::verify(minimal_input(reserved)), 0x40, 0, 0,
              ViolationCode::SteReservedBits, "STE reserved bits");

    Ste reserved_config;
    reserved_config.valid = true;
    reserved_config.config = 1;
    check_key(smmuv3::verify(minimal_input(reserved_config)), 0x40, 0, 0,
              ViolationCode::SteReservedConfig, "STE reserved config");

    Ste safe;
    safe.valid = true;
    safe.config = 0;
    VerificationInput abort_input = minimal_input(safe, 0x40);
    check(smmuv3::verify(abort_input).secure, "abort is safe");

    std::vector<Ste> entries(1);
    entries[0].valid = true;
    entries[0].config = 0;
    VerificationInput span_input;
    span_input.stream_table =
        two_level_stream_table({l1_with_entries(std::move(entries), 10)});
    span_input.device = DeviceScope::stream_ids_inclusive(0, 0, 0);
    check_key(smmuv3::verify(span_input), 0, 0, 0,
              ViolationCode::L1SpanOutOfBounds, "L1 span overflow");
}

void test_cd_max_overflow() {
    Ste ste;
    ste.valid = true;
    ste.config = 7;
    ste.s1cdmax = 3;
    ste.cd_table.capacity = 2;
    ste.cd_table.format = smmuv3::CdTableFormat::Linear;
    ste.stage2 = table4k(16, 3, {page(0, 0x2000, Permission::read_write())});
    VerificationInput input = minimal_input(ste);
    check_key(smmuv3::verify(input), 0x40, 2, 0,
              ViolationCode::CdSlotOutOfBounds, "S1CDMax CD slot overflow");
}

void test_descriptor_and_granule_faults() {
    Ste page_reserved;
    page_reserved.valid = true;
    page_reserved.config = 6;
    PageTableEntry bad_page = page(0, 0x2000, Permission::read_write());
    bad_page.reserved_bits = 1;
    page_reserved.stage2 = table4k(16, 3, {bad_page});
    check_key(smmuv3::verify(minimal_input(page_reserved)), 0x40, 0, 0,
              ViolationCode::PageDescriptorReservedBits,
              "page-table reserved bits");

    Ste bad_level;
    bad_level.valid = true;
    bad_level.config = 6;
    PageTableEntry level0_block =
        PageTableEntry::leaf(DescriptorKind::Block, 0, 0x200000,
                             Permission::read_write());
    level0_block.explicit_input = Range{0, 0x200000};
    bad_level.stage2 = table4k(32, 0, {level0_block});
    check_key(smmuv3::verify(minimal_input(bad_level)), 0x40, 0, 0,
              ViolationCode::PageDescriptorLevel, "level-0 block descriptor");

    Cd cd = nested_cd(table4k(16, 3, {page(0, 0x1000, Permission::read_write())}));
    cd.tg0 = 2;
    Ste ste = nested_ste(CdTable::linear({cd}),
                         table4k(16, 3, {page(0, 0x2000, Permission::read_write())}));
    check_key(smmuv3::verify(minimal_input(ste)), 0x40, 0, 0,
              ViolationCode::CdUnsupportedGranule, "16KB CD granule");
}

void test_stage2_block_partial_host_overlap() {
    Ste ste;
    ste.valid = true;
    ste.config = 6;
    ste.stage2 = table4k(32, 2, {block2m(0, 0x400000, Permission::read_write())});
    VerificationInput input = minimal_input(ste);
    input.host_protected.push_back(
        ProtectedRegion{Range{0x400800, 0x400c00}, false});
    const smmuv3::Result result = smmuv3::verify(input);
    check_key(result, 0x40, 0, 0x800, ViolationCode::HostProtectedPhysical,
              "2MB block partial host overlap");
    if (result.violation.has_value()) {
        check(result.violation->l1.present, "witness contains L1STD");
        check(result.violation->ste.present, "witness contains STE");
        check(result.violation->stage2_path.size() == 1U,
              "2MB block witness has stage-2 leaf");
    }
}

void test_1gb_block_far_offset_is_symbolic() {
    Ste ste;
    ste.valid = true;
    ste.config = 6;
    PageTableEntry entry =
        PageTableEntry::leaf(DescriptorKind::Block, 0, 0x40000000,
                             Permission::read_write());
    entry.explicit_input = Range{0, U64{0x40000000}};
    ste.stage2 = table4k(34, 1, {entry});
    VerificationInput input = minimal_input(ste);
    input.host_protected.push_back(
        ProtectedRegion{Range{0x7ffff000, 0x80000000}, false});
    check_key(smmuv3::verify(input), 0x40, 0, 0x3ffff000,
              ViolationCode::HostProtectedPhysical, "1GB block far overlap");
}

void test_cross_vm_and_read_only() {
    Ste ste;
    ste.valid = true;
    ste.config = 6;
    ste.stage2 = table4k(16, 3, {page(1, 0x3000, Permission::read_write())});
    VerificationInput cross = minimal_input(ste);
    cross.vm_regions.push_back(VmRegion{0, Range{0x2000, 0x3000}, true});
    cross.vm_regions.push_back(VmRegion{1, Range{0x3000, 0x4000}, true});
    check_key(smmuv3::verify(cross), 0x40, 0, 0x1000,
              ViolationCode::CrossVmPhysical, "cross-VM physical reach");

    Ste ro_ste;
    ro_ste.valid = true;
    ro_ste.config = 6;
    ro_ste.stage2 = table4k(16, 3, {page(1, 0x2000, Permission::read_write())});
    VerificationInput ro = minimal_input(ro_ste);
    ro.vm_regions.push_back(VmRegion{0, Range{0x2000, 0x3000}, false});
    check_key(smmuv3::verify(ro), 0x40, 0, 0x1000,
              ViolationCode::ReadOnlyViolation, "writable DMA to read-only VM range");

    Ste ro_s2 = ro_ste;
    ro_s2.stage2 = table4k(16, 3, {page(1, 0x2000, Permission::read_only())});
    VerificationInput allowed = minimal_input(ro_s2);
    allowed.vm_regions.push_back(VmRegion{0, Range{0x2000, 0x3000}, false});
    check(smmuv3::verify(allowed).secure, "read-only stage-2 access is permitted");
}

void test_64k_stage1_and_nested_intersection() {
    PageTableEntry stage1_page =
        PageTableEntry::leaf(DescriptorKind::Page, 0, 0x100000,
                             Permission::read_write());
    stage1_page.explicit_input = Range{0, 0x10000};
    PageTable stage1;
    stage1.granule = Granule::k64K;
    stage1.root_level = 3;
    stage1.input_bits = 16;
    stage1.entries = {stage1_page};

    Cd cd;
    cd.valid = true;
    cd.t0sz = 16;
    cd.tg0 = 1;
    cd.stage1 = stage1;

    Ste ste = nested_ste(CdTable::linear({cd}),
                         table4k(32, 3, {page(0x100, 0x2000,
                                              Permission::read_write())}));
    VerificationInput input = minimal_input(ste);
    input.host_protected.push_back(ProtectedRegion{Range{0x2000, 0x3000}, false});
    const smmuv3::Result result = smmuv3::verify(input);
    check_key(result, 0x40, 0, 0, ViolationCode::HostProtectedPhysical,
              "64KB stage-1 nested translation");
    if (result.violation.has_value()) {
        check(result.violation->cd.present, "witness contains CD");
        check(result.violation->stage1_path.size() == 1U,
              "64KB stage-1 witness present");
        check(result.violation->stage2_path.size() == 1U,
              "nested stage-2 witness present");
    }
}

void test_sparse_multilevel_cascade() {
    PageTableEntry stage1_page =
        PageTableEntry::leaf(DescriptorKind::Page, 1, 0x800000,
                             Permission::read_write());
    PageTableEntry stage1_l3 =
        PageTableEntry::table(0, {stage1_page});
    PageTableEntry stage1_l2 =
        PageTableEntry::table(0, {stage1_l3});
    PageTableEntry stage1_l1 =
        PageTableEntry::table(0, {stage1_l2});
    PageTable stage1 = table4k(32, 0, {stage1_l1});

    PageTableEntry stage2_block =
        PageTableEntry::leaf(DescriptorKind::Block, 4, 0x200000,
                             Permission::read_write());
    PageTableEntry stage2_l1 =
        PageTableEntry::table(0, {stage2_block});
    PageTable stage2 = table4k(32, 1, {stage2_l1});

    Ste ste = nested_ste(CdTable::linear({nested_cd(stage1)}), stage2);
    VerificationInput input = minimal_input(ste);
    input.host_protected.push_back(
        ProtectedRegion{Range{0x200000, 0x200100}, false});
    const smmuv3::Result result = smmuv3::verify(input);
    check_key(result, 0x40, 0, 0x1000, ViolationCode::HostProtectedPhysical,
              "computed sparse multilevel cascade");
    if (result.violation.has_value()) {
        check(result.violation->stage1_path.size() == 4U,
              "stage-1 witness includes every level");
        check(result.violation->stage2_path.size() == 2U,
              "stage-2 witness includes block descriptor");
    }
}

void test_lexicographic_order() {
    Ste high_iova;
    high_iova.valid = true;
    high_iova.config = 6;
    high_iova.stage2 = table4k(32, 3, {page(8, 0x9000, Permission::read_write())});

    Ste low_sid_bypass;
    low_sid_bypass.valid = true;
    low_sid_bypass.config = 4;

    std::vector<Ste> l2(0x12);
    l2[0x10] = high_iova;
    l2[0x11] = low_sid_bypass;
    VerificationInput input;
    input.stream_table = two_level_stream_table({l1_with_entries(std::move(l2))});
    input.device = DeviceScope::stream_ids_inclusive(0x10, 0x11, 0);
    input.host_protected.push_back(ProtectedRegion{Range{0x9000, 0xa000}, false});
    check_key(smmuv3::verify(input), 0x10, 0, 0x8000,
              ViolationCode::HostProtectedPhysical,
              "smallest StreamID wins even with larger IOVA");

    Cd cd0 = nested_cd(table4k(16, 3, {page(0, 0x1000, Permission::read_write())}));
    Cd cd1 = nested_cd(table4k(16, 3, {page(0, 0x1000, Permission::read_write())}));
    Ste ssid_ste = nested_ste(CdTable::linear({cd0, cd1}),
                              table4k(16, 3, {page(1, 0x1000,
                                                   Permission::read_write())}));
    ssid_ste.s1cdmax = 1;
    VerificationInput ssid_input = minimal_input(ssid_ste);
    ssid_input.host_protected.push_back(
        ProtectedRegion{Range{0x1000, 0x2000}, false});
    check_key(smmuv3::verify(ssid_input), 0x40, 0, 0,
              ViolationCode::HostProtectedPhysical,
              "smallest SubstreamID wins");

    Cd cd_a =
        nested_cd(table4k(16, 3, {page(0, 0x2000, Permission::read_write()),
                                  page(1, 0x3000, Permission::read_write())}));
    Ste multi_ste = nested_ste(CdTable::linear({cd_a}),
                               table4k(16, 3, {page(2, 0x2000,
                                                    Permission::read_write()),
                                               page(3, 0x3000,
                                                    Permission::read_write())}));
    VerificationInput multi = minimal_input(multi_ste);
    multi.host_protected.push_back(
        ProtectedRegion{Range{0x2000, 0x4000}, false});
    check_key(smmuv3::verify(multi), 0x40, 0, 0,
              ViolationCode::HostProtectedPhysical,
              "smallest IOVA wins inside one CD");
}

struct BruteLeaf {
    Range input;
    U64 output;
    Permission permissions;
};

std::optional<BruteLeaf> brute_find_leaf(const PageTable& table, U64 address) {
    for (const PageTableEntry& entry : table.entries) {
        if (entry.kind == DescriptorKind::Invalid) {
            continue;
        }
        const Range input = entry.explicit_input.empty()
                                ? Range{entry.index
                                            << smmuv3::detail::level_span_bits(
                                                  table.granule, table.root_level),
                                        (entry.index
                                         << smmuv3::detail::level_span_bits(
                                               table.granule, table.root_level))
                                            +
                                            smmuv3::one_shift(
                                                smmuv3::detail::level_span_bits(
                                                    table.granule, table.root_level))}
                                : entry.explicit_input;
        if (!input.contains(address)) {
            continue;
        }
        if (entry.kind == DescriptorKind::Table) {
            PageTable next = table;
            next.root_level = static_cast<smmuv3::U8>(table.root_level + 1U);
            next.entries = entry.children;
            return brute_find_leaf(next, address);
        }
        return BruteLeaf{input, entry.output_address, entry.permissions};
    }
    return std::nullopt;
}

bool ranges_hit(U64 pa, const std::vector<Range>& ranges) {
    for (const Range range : ranges) {
        if (range.contains(pa)) {
            return true;
        }
    }
    return false;
}

std::optional<std::array<U64, 3>> brute_violation(const VerificationInput& input) {
    std::optional<std::array<U64, 3>> best;
    const Ste& ste = input.stream_table.l1[0].entries[0x40];
    for (U64 substream = 0; substream <= U64{1}; ++substream) {
        const Cd& selected = ste.cd_table.slots.at(static_cast<U32>(substream));
        for (U64 iova = 0; iova < U64{65536}; ++iova) {
            const auto stage1 = brute_find_leaf(selected.stage1, iova);
            if (!stage1.has_value() || !stage1->permissions.any()) {
                continue;
            }
            const U64 ipa = stage1->output + (iova - stage1->input.begin);
            const auto stage2 = brute_find_leaf(ste.stage2, ipa);
            if (!stage2.has_value()) {
                continue;
            }
            const Permission effective =
                smmuv3::intersect(stage1->permissions, stage2->permissions);
            if (!effective.any()) {
                continue;
            }
            const U64 pa = stage2->output + (ipa - stage2->input.begin);
            bool bad = false;
            for (const ProtectedRegion& host : input.host_protected) {
                bad = bad || host.range.contains(pa);
            }
            for (const VmRegion& vm : input.vm_regions) {
                if (vm.vm_id != input.device.vm_id && vm.range.contains(pa)) {
                    bad = true;
                }
                if (vm.vm_id == input.device.vm_id && !vm.writable &&
                    effective.write && vm.range.contains(pa)) {
                    bad = true;
                }
            }
            const bool own = ranges_hit(
                pa, [&input]() {
                    std::vector<Range> result;
                    for (const VmRegion& vm : input.vm_regions) {
                        if (vm.vm_id == input.device.vm_id) {
                            result.push_back(vm.range);
                        }
                    }
                    return result;
                }());
            std::vector<Range> known;
            for (const VmRegion& vm : input.vm_regions) {
                known.push_back(vm.range);
            }
            for (const ProtectedRegion& host : input.host_protected) {
                known.push_back(host.range);
            }
            if (!own && !ranges_hit(pa, known)) {
                bad = true;
            }
            if (!bad) {
                continue;
            }
            const std::array<U64, 3> key{U64{0x40}, substream, iova};
            if (!best.has_value() || key < *best) {
                best = key;
            }
        }
    }
    return best;
}

std::vector<PageTableEntry> random_table(std::mt19937& rng) {
    std::vector<PageTableEntry> entries;
    std::uniform_int_distribution<unsigned> page_dist(0, 15);
    std::uniform_int_distribution<unsigned> perm_dist(0, 3);
    for (U64 index = 0; index < U64{16}; ++index) {
        const unsigned perm_value = perm_dist(rng);
        Permission permissions = Permission::none();
        if (perm_value == 1U) {
            permissions = Permission::read_only();
        } else if (perm_value == 2U) {
            permissions = Permission::read_write();
        }
        const U64 output = static_cast<U64>(page_dist(rng)) * U64{4096};
        entries.push_back(page(index, output, permissions));
    }
    return entries;
}

std::vector<Range> random_ranges(std::mt19937& rng) {
    std::uniform_int_distribution<unsigned> page_dist(0, 15);
    std::vector<Range> ranges;
    for (unsigned i = 0; i < 3U; ++i) {
        const U64 begin = static_cast<U64>(page_dist(rng)) * U64{4096};
        const U64 end = begin + U64{4096};
        ranges.push_back(Range{begin, end});
    }
    return ranges;
}

void test_16bit_full_enumeration_cross_validation() {
    std::mt19937 rng(0x5a17U);
    for (unsigned iteration = 0; iteration < 48U; ++iteration) {
        Cd cd0 = nested_cd(table4k(16, 3, random_table(rng)));
        Cd cd1 = nested_cd(table4k(16, 3, random_table(rng)));
        Ste ste = nested_ste(CdTable::linear({cd0, cd1}),
                             table4k(16, 3, random_table(rng)));
        ste.s1cdmax = 1;
        VerificationInput input = minimal_input(ste);
        const std::vector<Range> vm0 = random_ranges(rng);
        const std::vector<Range> vm1 = random_ranges(rng);
        const std::vector<Range> host = random_ranges(rng);
        std::uniform_int_distribution<unsigned> writable_dist(0, 1);
        for (const Range range : vm0) {
            input.vm_regions.push_back(
                VmRegion{0, range, writable_dist(rng) != 0U});
        }
        for (const Range range : vm1) {
            input.vm_regions.push_back(
                VmRegion{1, range, writable_dist(rng) != 0U});
        }
        for (const Range range : host) {
            input.host_protected.push_back(ProtectedRegion{range, false});
        }

        const std::optional<std::array<U64, 3>> brute = brute_violation(input);
        const smmuv3::Result symbolic = smmuv3::verify(input);
        check(symbolic.violation.has_value() == brute.has_value(),
              "16-bit enumeration existence mismatch");
        if (symbolic.violation.has_value() && brute.has_value()) {
            const std::array<U64, 3> symbol_key{
                static_cast<U64>(symbolic.violation->stream_id),
                static_cast<U64>(symbolic.violation->substream_id),
                symbolic.violation->iova};
            check(symbol_key == *brute, "16-bit enumeration key mismatch");
        }
    }
}

}  // namespace

int main() {
    test_parse_base_cfg_and_ste();
    test_bypass_and_span();
    test_cd_max_overflow();
    test_descriptor_and_granule_faults();
    test_stage2_block_partial_host_overlap();
    test_1gb_block_far_offset_is_symbolic();
    test_cross_vm_and_read_only();
    test_64k_stage1_and_nested_intersection();
    test_sparse_multilevel_cascade();
    test_lexicographic_order();
    test_16bit_full_enumeration_cross_validation();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    std::cout << "all smmuv3 verifier tests passed\n";
    return 0;
}
