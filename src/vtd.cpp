#include "internal.hpp"

#include <array>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <optional>
#include <utility>
#include <vector>

namespace iommu_guard {
namespace {

struct ContextEntry {
    std::uint64_t low = 0;
    std::uint64_t high = 0;
    std::uint64_t extra0 = 0;
    std::uint64_t extra1 = 0;
};

std::uint64_t address_mask(std::uint8_t haw) noexcept {
    return detail::max_address_for_haw(haw) & ~0xFFFULL;
}

bool read_bytes(const MemoryRead& read, PhysicalAddress address, void* data,
                std::size_t size) {
    return static_cast<bool>(read) && read(address, data, size);
}

void add_walk_issue(TranslationWalk& walk, IssueCode code, std::string message,
                    std::vector<PhysicalAddress> entries = {}) {
    Issue issue = detail::make_issue(code, std::move(message));
    issue.bdf = walk.bdf;
    issue.pasid = walk.pasid;
    issue.did = walk.did;
    issue.entry_phys = std::move(entries);
    walk.issues.push_back(std::move(issue));
}

class SlptWalker {
public:
    SlptWalker(const MemoryRead& read, std::uint8_t haw,
               std::uint8_t address_width_bits, TranslationWalk& walk,
               std::size_t max_mappings)
        : read_(read),
          haw_(haw),
          aw_bits_(address_width_bits),
          walk_(walk),
          max_mappings_(max_mappings),
          levels_(address_width_bits == 39U ? 3U : 4U) {}

    void walk(PhysicalAddress root, std::vector<PhysicalAddress> path) {
        if (levels_ != 3U && levels_ != 4U) {
            return;
        }
        walk_level(root, levels_, 0U, std::move(path));
    }

    bool stopped() const noexcept { return stopped_; }

private:
    void walk_level(PhysicalAddress table, std::uint8_t level,
                    std::uint64_t gpa_prefix,
                    std::vector<PhysicalAddress> path) {
        if (stopped_) {
            return;
        }
        const std::uint64_t max_gpa = 1ULL << aw_bits_;
        for (std::uint64_t index = 0U; index < 512U; ++index) {
            const std::uint64_t offset = index * 8U;
            if (table > detail::max_address_for_haw(haw_) - offset) {
                add_walk_issue(walk_, IssueCode::kVtdHpaOutOfRange,
                               "page-table entry address exceeds HAW+1 bits",
                               path);
                return;
            }
            const PhysicalAddress entry_phys = table + offset;
            std::uint64_t entry = 0;
            if (!read_bytes(read_, entry_phys, &entry, sizeof(entry))) {
                add_walk_issue(walk_, IssueCode::kVtdMemoryReadFailed,
                               "failed to read second-level page-table entry",
                               path);
                return;
            }
            if ((entry & vtd::kSlptRead) == 0U) {
                if ((entry & (vtd::kSlptWrite | vtd::kSlptExecute)) != 0U) {
                    add_walk_issue(
                        walk_, IssueCode::kVtdPagePermissionsInvalid,
                        "write/execute permission is set without read",
                        append_path(path, entry_phys));
                }
                continue;
            }

            if ((entry & ~detail::max_address_for_haw(haw_)) != 0U) {
                add_walk_issue(walk_, IssueCode::kVtdHpaOutOfRange,
                               "page-table entry sets address bits above "
                               "HAW+1",
                               append_path(path, entry_phys));
                continue;
            }

            const std::uint64_t shift =
                12U + (static_cast<std::uint64_t>(level) - 1U) * 9U;
            const std::uint64_t gpa_here =
                gpa_prefix | (index << shift);
            const bool page_size_bit = (entry & vtd::kSlptPageSize) != 0U;
            const bool leaf =
                level == 1U ||
                (page_size_bit && (level == 2U || level == 3U));

            std::uint64_t allowed =
                vtd::kSlptRead | vtd::kSlptAccessed | address_mask(haw_);
            std::uint64_t page_size = vtd::kPage4K;
            if (leaf) {
                allowed |= vtd::kSlptWrite | vtd::kSlptExecute |
                           vtd::kSlptDirty | vtd::kSlptSnoop;
                if (level == 2U) {
                    allowed |= vtd::kSlptPageSize;
                    page_size = vtd::kPage2M;
                } else if (level == 3U) {
                    allowed |= vtd::kSlptPageSize;
                    page_size = vtd::kPage1G;
                }
            }

            if ((entry & ~allowed) != 0U) {
                add_walk_issue(walk_, IssueCode::kVtdPageTableReserved,
                               "second-level page-table entry has reserved "
                               "bits set",
                               append_path(path, entry_phys));
                continue;
            }

            if (!leaf) {
                if (level == 1U) {
                    continue;
                }
                const PhysicalAddress child = entry & address_mask(haw_);
                if (!detail::is_page_aligned(child) ||
                    !detail::range_within_haw(child, vtd::kPage4K, haw_)) {
                    add_walk_issue(walk_, IssueCode::kVtdHpaOutOfRange,
                                   "child page-table pointer exceeds HAW+1 "
                                   "bits",
                                   append_path(path, entry_phys));
                    continue;
                }
                walk_level(child, static_cast<std::uint8_t>(level - 1U),
                           gpa_here, append_path(path, entry_phys));
                if (stopped_) {
                    return;
                }
                continue;
            }

            const PhysicalAddress hpa_base =
                entry & address_mask(haw_);
            if (!detail::is_aligned_to(hpa_base, page_size)) {
                add_walk_issue(walk_, IssueCode::kVtdSuperpageMisaligned,
                               "leaf HPA is not aligned to its page size",
                               append_path(path, entry_phys));
                continue;
            }
            if (gpa_here >= max_gpa || page_size > max_gpa - gpa_here) {
                add_walk_issue(walk_, IssueCode::kVtdGpaOutOfRange,
                               "leaf GPA exceeds the context address width",
                               append_path(path, entry_phys));
                continue;
            }
            if (!detail::range_within_haw(hpa_base, page_size, haw_)) {
                add_walk_issue(walk_, IssueCode::kVtdHpaOutOfRange,
                               "leaf HPA+size exceeds HAW+1 bits",
                               append_path(path, entry_phys));
                continue;
            }
            if (mapping_count_ >= max_mappings_) {
                add_walk_issue(walk_, IssueCode::kVtdMappingLimitExceeded,
                               "page-walk mapping limit exceeded",
                               append_path(path, entry_phys));
                stopped_ = true;
                return;
            }

            SlptMapping mapping;
            mapping.gpa = AddressRange{gpa_here, gpa_here + page_size};
            mapping.hpa_begin = hpa_base;
            mapping.page_size = page_size;
            mapping.read = (entry & vtd::kSlptRead) != 0U;
            mapping.write = (entry & vtd::kSlptWrite) != 0U;
            mapping.execute = (entry & vtd::kSlptExecute) != 0U;
            mapping.snoop = (entry & vtd::kSlptSnoop) != 0U;
            mapping.leaf_entry_phys = entry_phys;
            mapping.walk_entry_phys = append_path(path, entry_phys);
            walk_.mappings.push_back(std::move(mapping));
            mapping_count_ += 1U;
        }
    }

    static std::vector<PhysicalAddress> append_path(
        const std::vector<PhysicalAddress>& path, PhysicalAddress entry) {
        std::vector<PhysicalAddress> result = path;
        result.push_back(entry);
        return result;
    }

    const MemoryRead& read_;
    std::uint8_t haw_;
    std::uint8_t aw_bits_;
    TranslationWalk& walk_;
    std::size_t max_mappings_;
    std::uint8_t levels_;
    std::size_t mapping_count_ = 0U;
    bool stopped_ = false;
};

bool read_root_entry(const MemoryRead& read, const TranslationRoot& root,
                     const PciBdf& bdf, TranslationWalk& walk,
                     ContextEntry& entry, PhysicalAddress& entry_phys) {
    if (!detail::is_page_aligned(root.root_table_phys) ||
        !detail::range_within_haw(root.root_table_phys, vtd::kPage4K,
                                  root.host_address_width)) {
        add_walk_issue(walk, IssueCode::kVtdRootEntryOutOfRange,
                       "root table address is invalid");
        return false;
    }
    entry_phys = root.root_table_phys +
                 static_cast<PhysicalAddress>(bdf.bus) * 16U;
    walk.root_entry_phys = entry_phys;
    std::array<std::uint8_t, 16> bytes{};
    if (!read_bytes(read, entry_phys, bytes.data(), bytes.size())) {
        add_walk_issue(walk, IssueCode::kVtdMemoryReadFailed,
                       "failed to read root entry", {entry_phys});
        return false;
    }
    entry.low = detail::read_le64(bytes.data(), 0U);
    entry.high = detail::read_le64(bytes.data(), 8U);
    return true;
}

bool read_context_entry(const MemoryRead& read, PhysicalAddress context_table,
                        const PciBdf& bdf, std::uint8_t haw,
                        bool scalable_mode, TranslationWalk& walk,
                        ContextEntry& entry, PhysicalAddress& entry_phys) {
    const std::uint64_t context_table_size =
        scalable_mode ? (vtd::kScalableContextEntrySize * 256U)
                      : vtd::kPage4K;
    if (!detail::is_page_aligned(context_table) ||
        !detail::range_within_haw(context_table, context_table_size, haw)) {
        add_walk_issue(walk, IssueCode::kVtdContextEntryOutOfRange,
                       "context table address is invalid");
        return false;
    }
    const std::uint16_t devfn = static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(bdf.device) << 3U) | bdf.function);
    const std::uint64_t entry_size =
        scalable_mode ? vtd::kScalableContextEntrySize
                      : static_cast<std::uint64_t>(16U);
    entry_phys = context_table +
                 static_cast<PhysicalAddress>(devfn) * entry_size;
    walk.context_entry_phys = entry_phys;
    std::array<std::uint8_t, 32> bytes{};
    const std::size_t read_size = static_cast<std::size_t>(entry_size);
    if (!read_bytes(read, entry_phys, bytes.data(), read_size)) {
        add_walk_issue(walk, IssueCode::kVtdMemoryReadFailed,
                       "failed to read context entry", {entry_phys});
        return false;
    }
    entry.low = detail::read_le64(bytes.data(), 0U);
    entry.high = detail::read_le64(bytes.data(), 8U);
    if (scalable_mode) {
        entry.extra0 = detail::read_le64(bytes.data(), 16U);
        entry.extra1 = detail::read_le64(bytes.data(), 24U);
    }
    return true;
}

bool valid_table_pointer(PhysicalAddress pointer, std::uint8_t haw) {
    return detail::is_page_aligned(pointer) &&
           detail::range_within_haw(pointer, vtd::kPage4K, haw);
}

}  // namespace

TranslationWalk walk_device_translation(
    const TranslationRoot& root, const PciBdf& bdf,
    std::optional<std::uint32_t> pasid, const MemoryRead& read,
    const WalkOptions& options) {
    TranslationWalk walk;
    walk.bdf = bdf;
    walk.pasid = pasid;
    const std::uint8_t haw = root.host_address_width;
    const std::uint64_t addr_mask = address_mask(haw);

    if (!bdf.valid()) {
        add_walk_issue(walk, IssueCode::kInvalidArgument,
                       "requesting BDF is out of range");
        return walk;
    }
    if (!static_cast<bool>(read)) {
        add_walk_issue(walk, IssueCode::kInvalidArgument,
                       "physical-memory read callback is empty");
        return walk;
    }
    if (haw == 0U || haw > 63U) {
        add_walk_issue(walk, IssueCode::kInvalidArgument,
                       "HAW is out of range");
        return walk;
    }

    ContextEntry root_entry;
    PhysicalAddress root_entry_phys = 0;
    if (!read_root_entry(read, root, bdf, walk, root_entry,
                         root_entry_phys)) {
        return walk;
    }
    if ((root_entry.low & vtd::kRootEntryPresent) == 0U) {
        add_walk_issue(walk, IssueCode::kVtdRootEntryNotPresent,
                       "root entry is not present", {root_entry_phys});
        return walk;
    }
    if ((root_entry.low & ~detail::max_address_for_haw(haw)) != 0U) {
        add_walk_issue(walk, IssueCode::kVtdRootEntryOutOfRange,
                       "root entry sets address bits above HAW+1",
                       {root_entry_phys});
        return walk;
    }
    const std::uint64_t root_allowed =
        vtd::kRootEntryPresent | addr_mask;
    if ((root_entry.low & ~root_allowed) != 0U || root_entry.high != 0U) {
        add_walk_issue(walk, IssueCode::kVtdRootEntryReserved,
                       "root entry contains reserved bits",
                       {root_entry_phys});
        return walk;
    }
    const PhysicalAddress context_table = root_entry.low & addr_mask;
    if (!valid_table_pointer(context_table, haw)) {
        add_walk_issue(walk, IssueCode::kVtdRootEntryOutOfRange,
                       "context-table pointer exceeds HAW+1 bits",
                       {root_entry_phys});
        return walk;
    }

    ContextEntry context;
    PhysicalAddress context_entry_phys = 0;
    if (!read_context_entry(read, context_table, bdf, haw,
                            root.scalable_mode, walk, context,
                            context_entry_phys)) {
        return walk;
    }
    if ((context.low & vtd::kContextEntryPresent) == 0U) {
        add_walk_issue(walk, IssueCode::kVtdContextEntryNotPresent,
                       "context entry is not present",
                       {root_entry_phys, context_entry_phys});
        return walk;
    }
    if ((context.low & ~detail::max_address_for_haw(haw)) != 0U) {
        add_walk_issue(walk, IssueCode::kVtdContextEntryOutOfRange,
                       "context entry sets address bits above HAW+1",
                       {root_entry_phys, context_entry_phys});
        return walk;
    }

    if (!root.scalable_mode) {
        const std::uint64_t low_allowed =
            vtd::kContextEntryPresent | vtd::kContextEntryFaultDisable |
            vtd::kContextTranslationTypeMask | addr_mask;
        const std::uint64_t high_allowed =
            vtd::kContextAddressWidthMask |
            (0xFULL << 3U) |  // AVAL[6:3]
            (0xFFFFULL << vtd::kContextDomainIdShift);
        if ((context.low & ~low_allowed) != 0U ||
            (context.high & ~high_allowed) != 0U) {
            add_walk_issue(walk, IssueCode::kVtdContextEntryReserved,
                           "legacy context entry contains reserved bits",
                           {root_entry_phys, context_entry_phys});
            return walk;
        }

        const std::uint8_t aw_field = static_cast<std::uint8_t>(
            context.high & vtd::kContextAddressWidthMask);
        if (aw_field != vtd::kAddressWidth39 &&
            aw_field != vtd::kAddressWidth48) {
            add_walk_issue(walk, IssueCode::kVtdAddressWidthUnsupported,
                           "legacy context address width must be 39 or 48 "
                           "bits",
                           {root_entry_phys, context_entry_phys});
            return walk;
        }
        walk.address_width_bits =
            (aw_field == vtd::kAddressWidth39) ? 39U : 48U;
        walk.did = static_cast<DomainId>(
            (context.high >> vtd::kContextDomainIdShift) & 0xFFFFU);

        const std::uint8_t translation_type = static_cast<std::uint8_t>(
            (context.low & vtd::kContextTranslationTypeMask) >>
            vtd::kContextTranslationTypeShift);
        if (translation_type == vtd::kTranslationPassThrough) {
            walk.pass_through = true;
            add_walk_issue(walk, IssueCode::kVtdTranslationTypePassThrough,
                           "legacy context entry is pass-through",
                           {root_entry_phys, context_entry_phys});
            return walk;
        }
        if (translation_type == vtd::kTranslationReserved) {
            add_walk_issue(walk, IssueCode::kVtdTranslationTypeUnsupported,
                           "legacy context entry uses reserved TT=3",
                           {root_entry_phys, context_entry_phys});
            return walk;
        }

        const PhysicalAddress slpt = context.low & addr_mask;
        if (!valid_table_pointer(slpt, haw)) {
            add_walk_issue(walk, IssueCode::kVtdContextEntryOutOfRange,
                           "second-level page-table pointer exceeds HAW+1 "
                           "bits",
                           {root_entry_phys, context_entry_phys});
            return walk;
        }
        walk.translation_active = true;
        SlptWalker walker(
            read, haw,
            static_cast<std::uint8_t>(walk.address_width_bits.value_or(0U)),
            walk, options.max_mappings);
        walker.walk(slpt, {root_entry_phys, context_entry_phys});
        return walk;
    }

    const std::uint64_t low_allowed =
        vtd::kContextEntryPresent | 0x1EULL |
        vtd::kScalableContextPdsMask | addr_mask;
    const std::uint64_t high_allowed =
        (1ULL << 21U) - 1ULL;  // RID_PASID[19:0] and DTE[20].
    if ((context.low & ~low_allowed) != 0U ||
        (context.high & ~high_allowed) != 0U || context.extra0 != 0U ||
        context.extra1 != 0U) {
        add_walk_issue(walk, IssueCode::kVtdContextEntryReserved,
                       "scalable context entry contains reserved bits",
                       {root_entry_phys, context_entry_phys});
        return walk;
    }

    std::uint32_t selected_pasid = 0U;
    if (pasid.has_value()) {
        selected_pasid = *pasid;
    } else {
        selected_pasid = static_cast<std::uint32_t>(
            (context.high >> vtd::kContextRidPasidShift) &
            vtd::kContextRidPasidMask);
    }
    walk.pasid = selected_pasid;
    if (selected_pasid > 0xFFFFFU) {
        add_walk_issue(walk, IssueCode::kVtdScalableLayoutInvalid,
                       "PASID exceeds the 20-bit architectural limit",
                       {root_entry_phys, context_entry_phys});
        return walk;
    }

    const PhysicalAddress pasid_directory = context.low & addr_mask;
    if (!valid_table_pointer(pasid_directory, haw)) {
        add_walk_issue(walk, IssueCode::kVtdScalableLayoutInvalid,
                       "PASID directory pointer exceeds HAW+1 bits",
                       {root_entry_phys, context_entry_phys});
        return walk;
    }

    const std::uint64_t directory_index =
        static_cast<std::uint64_t>(selected_pasid) >>
        vtd::kPasidDirectoryShift;
    const std::uint8_t pds = static_cast<std::uint8_t>(
        (context.low & vtd::kScalableContextPdsMask) >>
        vtd::kScalableContextPdsShift);
    const std::uint64_t directory_entries = 1ULL << (pds + 7U);
    if (directory_index >= directory_entries) {
        add_walk_issue(walk, IssueCode::kVtdScalableLayoutInvalid,
                       "PASID is beyond the configured PASID directory size",
                       {root_entry_phys, context_entry_phys});
        return walk;
    }
    const std::uint64_t directory_offset = directory_index * 8U;
    if (pasid_directory >
        detail::max_address_for_haw(haw) - directory_offset) {
        add_walk_issue(walk, IssueCode::kVtdScalableLayoutInvalid,
                       "PASID directory entry address exceeds HAW+1 bits",
                       {root_entry_phys, context_entry_phys});
        return walk;
    }
    const PhysicalAddress directory_entry_phys =
        pasid_directory + directory_offset;
    walk.pasid_directory_entry_phys = directory_entry_phys;
    std::uint64_t directory_entry = 0U;
    if (!read_bytes(read, directory_entry_phys, &directory_entry,
                    sizeof(directory_entry))) {
        add_walk_issue(walk, IssueCode::kVtdMemoryReadFailed,
                       "failed to read PASID directory entry",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys});
        return walk;
    }
    if ((directory_entry & vtd::kPasidDirectoryPresent) == 0U) {
        add_walk_issue(walk, IssueCode::kVtdPasidDirectoryNotPresent,
                       "PASID directory entry is not present",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys});
        return walk;
    }
    const std::uint64_t directory_allowed =
        vtd::kPasidDirectoryPresent | addr_mask;
    if ((directory_entry & ~directory_allowed) != 0U) {
        add_walk_issue(walk, IssueCode::kVtdScalableLayoutInvalid,
                       "PASID directory entry has reserved bits",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys});
        return walk;
    }

    const PhysicalAddress pasid_table = directory_entry & addr_mask;
    if (!valid_table_pointer(pasid_table, haw)) {
        add_walk_issue(walk, IssueCode::kVtdScalableLayoutInvalid,
                       "PASID table pointer exceeds HAW+1 bits",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys});
        return walk;
    }
    const std::uint64_t table_index =
        static_cast<std::uint64_t>(selected_pasid) &
        vtd::kPasidTableIndexMask;
    const std::uint64_t pasid_entry_offset =
        table_index * vtd::kPasidTableEntrySize;
    if (pasid_table >
        detail::max_address_for_haw(haw) - pasid_entry_offset) {
        add_walk_issue(walk, IssueCode::kVtdScalableLayoutInvalid,
                       "PASID table entry address exceeds HAW+1 bits",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys});
        return walk;
    }
    const PhysicalAddress pasid_entry_phys =
        pasid_table + pasid_entry_offset;
    walk.pasid_table_entry_phys = pasid_entry_phys;
    std::array<std::uint8_t, 64> pasid_bytes{};
    if (!read_bytes(read, pasid_entry_phys, pasid_bytes.data(),
                    pasid_bytes.size())) {
        add_walk_issue(walk, IssueCode::kVtdMemoryReadFailed,
                       "failed to read PASID table entry",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys, pasid_entry_phys});
        return walk;
    }
    const std::uint64_t pasid_low =
        detail::read_le64(pasid_bytes.data(), 0U);
    if ((pasid_low & vtd::kPasidTablePresent) == 0U) {
        add_walk_issue(walk, IssueCode::kVtdPasidTableEntryNotPresent,
                       "PASID table entry is not present",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys, pasid_entry_phys});
        return walk;
    }
    const std::uint64_t pasid_allowed =
        vtd::kPasidTablePresent | vtd::kPasidTableFaultDisable |
        vtd::kPasidAddressWidthMask | vtd::kPasidPgttMask | addr_mask;
    if ((pasid_low & ~pasid_allowed) != 0U) {
        add_walk_issue(walk, IssueCode::kVtdScalableLayoutInvalid,
                       "PASID table entry has reserved bits",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys, pasid_entry_phys});
        return walk;
    }
    walk.did = static_cast<DomainId>(
        (detail::read_le64(pasid_bytes.data(), 8U) &
         (0xFFFFULL << vtd::kPasidDomainIdShift)) >>
        vtd::kPasidDomainIdShift);
    const std::uint8_t pgtt = static_cast<std::uint8_t>(
        (pasid_low & vtd::kPasidPgttMask) >> vtd::kPasidPgttShift);
    if (pgtt == vtd::kPgttPassThrough) {
        walk.pass_through = true;
        add_walk_issue(walk, IssueCode::kVtdPgttPassThrough,
                       "PASID PGTT is pass-through",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys, pasid_entry_phys});
        return walk;
    }
    if (pgtt != vtd::kPgttSecondLevel && pgtt != vtd::kPgttNested) {
        add_walk_issue(walk, IssueCode::kVtdPgttUnsupported,
                       "PASID PGTT must be second-level or nested",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys, pasid_entry_phys});
        return walk;
    }

    const std::uint8_t aw_field = static_cast<std::uint8_t>(
        (pasid_low & vtd::kPasidAddressWidthMask) >>
        vtd::kPasidAddressWidthShift);
    if (aw_field != vtd::kAddressWidth39 &&
        aw_field != vtd::kAddressWidth48) {
        add_walk_issue(walk, IssueCode::kVtdAddressWidthUnsupported,
                       "PASID entry address width must be 39 or 48 bits",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys, pasid_entry_phys});
        return walk;
    }
    walk.address_width_bits = (aw_field == vtd::kAddressWidth39) ? 39U : 48U;

    const PhysicalAddress slpt = pasid_low & addr_mask;
    if (!valid_table_pointer(slpt, haw)) {
        add_walk_issue(walk, IssueCode::kVtdScalableLayoutInvalid,
                       "PASID second-level page-table pointer exceeds HAW+1 "
                       "bits",
                       {root_entry_phys, context_entry_phys,
                        directory_entry_phys, pasid_entry_phys});
        return walk;
    }
    walk.translation_active = true;
    SlptWalker walker(
        read, haw,
        static_cast<std::uint8_t>(walk.address_width_bits.value_or(0U)), walk,
        options.max_mappings);
    walker.walk(slpt, {root_entry_phys, context_entry_phys,
                       directory_entry_phys, pasid_entry_phys});
    return walk;
}

}  // namespace iommu_guard
