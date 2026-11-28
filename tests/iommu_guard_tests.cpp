#include "iommu_guard/iommu_guard.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <map>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace {

int g_failures = 0;

void check_failed(const char* expression, const char* file, int line) {
    std::fprintf(stderr, "%s:%d: check failed: %s\n", file, line, expression);
    g_failures += 1;
}

#define CHECK(expression)                                                      \
    do {                                                                       \
        if (!(expression)) {                                                   \
            check_failed(#expression, __FILE__, __LINE__);                     \
        }                                                                      \
    } while (false)

constexpr std::uint64_t kRootTable = 0x100000ULL;
constexpr std::uint64_t kContextTable = 0x101000ULL;
constexpr std::uint64_t kPasidDirectory = 0x120000ULL;
constexpr std::uint64_t kPasidTable = 0x121000ULL;
constexpr std::uint64_t kSlptTop = 0x200000ULL;
constexpr std::uint64_t kPageSize = 4096ULL;
constexpr std::uint64_t kTablePointerMask = 0x000FFFFFFFFFF000ULL;

void append_u8(std::vector<std::uint8_t>& bytes, std::uint8_t value) {
    bytes.push_back(value);
}

void append_u16(std::vector<std::uint8_t>& bytes, std::uint16_t value) {
    bytes.push_back(static_cast<std::uint8_t>(value & 0xFFU));
    bytes.push_back(static_cast<std::uint8_t>((value >> 8U) & 0xFFU));
}

void append_u32(std::vector<std::uint8_t>& bytes, std::uint32_t value) {
    for (std::uint8_t shift = 0U; shift < 32U; shift = static_cast<std::uint8_t>(shift + 8U)) {
        bytes.push_back(
            static_cast<std::uint8_t>((value >> shift) & 0xFFU));
    }
}

void append_u64(std::vector<std::uint8_t>& bytes, std::uint64_t value) {
    for (std::uint8_t shift = 0U; shift < 64U; shift = static_cast<std::uint8_t>(shift + 8U)) {
        bytes.push_back(
            static_cast<std::uint8_t>((value >> shift) & 0xFFU));
    }
}

class DmarBuilder {
public:
    explicit DmarBuilder(std::uint8_t haw = 39U) : bytes_(48U, 0U) {
        bytes_[0] = 'D';
        bytes_[1] = 'M';
        bytes_[2] = 'A';
        bytes_[3] = 'R';
        bytes_[8] = 1U;
        bytes_[36] = haw;
    }

    void add(std::vector<std::uint8_t> structure) {
        bytes_.insert(bytes_.end(), structure.begin(), structure.end());
    }

    std::vector<std::uint8_t> finish() const {
        std::vector<std::uint8_t> result = bytes_;
        const std::uint32_t length =
            static_cast<std::uint32_t>(result.size());
        result[4] = static_cast<std::uint8_t>(length & 0xFFU);
        result[5] = static_cast<std::uint8_t>((length >> 8U) & 0xFFU);
        result[6] = static_cast<std::uint8_t>((length >> 16U) & 0xFFU);
        result[7] = static_cast<std::uint8_t>((length >> 24U) & 0xFFU);
        result[9] = 0U;
        std::uint8_t sum = 0U;
        for (const std::uint8_t byte : result) {
            sum = static_cast<std::uint8_t>(
                static_cast<unsigned int>(sum) + byte);
        }
        result[9] = static_cast<std::uint8_t>(
            0x100U - static_cast<unsigned int>(sum));
        return result;
    }

private:
    std::vector<std::uint8_t> bytes_;
};

std::vector<std::uint8_t> make_scope(
    std::uint8_t kind, std::uint8_t start_bus,
    const std::vector<std::pair<std::uint8_t, std::uint8_t>>& paths) {
    std::vector<std::uint8_t> scope;
    const std::uint8_t length = static_cast<std::uint8_t>(6U + paths.size() * 2U);
    append_u8(scope, kind);
    append_u8(scope, length);
    append_u16(scope, 0U);
    append_u8(scope, 0U);
    append_u8(scope, start_bus);
    for (const auto& path : paths) {
        append_u8(scope, path.first);
        append_u8(scope, path.second);
    }
    return scope;
}

std::vector<std::uint8_t> make_drhd(
    bool include_pci_all, std::uint16_t segment, std::uint64_t base,
    const std::vector<std::vector<std::uint8_t>>& scopes) {
    std::vector<std::uint8_t> drhd;
    std::size_t length = 16U;
    for (const auto& scope : scopes) {
        length += scope.size();
    }
    append_u16(drhd, 0U);
    append_u16(drhd, static_cast<std::uint16_t>(length));
    append_u8(drhd, include_pci_all ? 1U : 0U);
    append_u8(drhd, 0U);
    append_u16(drhd, segment);
    append_u64(drhd, base);
    for (const auto& scope : scopes) {
        drhd.insert(drhd.end(), scope.begin(), scope.end());
    }
    return drhd;
}

std::vector<std::uint8_t> make_rmrr(
    std::uint16_t segment, std::uint64_t base, std::uint64_t limit,
    const std::vector<std::vector<std::uint8_t>>& scopes) {
    std::vector<std::uint8_t> rmrr;
    std::size_t length = 24U;
    for (const auto& scope : scopes) {
        length += scope.size();
    }
    append_u16(rmrr, 1U);
    append_u16(rmrr, static_cast<std::uint16_t>(length));
    append_u16(rmrr, 0U);
    append_u16(rmrr, segment);
    append_u64(rmrr, base);
    append_u64(rmrr, limit);
    for (const auto& scope : scopes) {
        rmrr.insert(rmrr.end(), scope.begin(), scope.end());
    }
    return rmrr;
}

std::vector<std::uint8_t> make_atsr(
    std::uint16_t segment,
    const std::vector<std::vector<std::uint8_t>>& scopes) {
    std::vector<std::uint8_t> atsr;
    std::size_t length = 8U;
    for (const auto& scope : scopes) {
        length += scope.size();
    }
    append_u16(atsr, 2U);
    append_u16(atsr, static_cast<std::uint16_t>(length));
    append_u8(atsr, 0U);
    append_u8(atsr, 0U);
    append_u16(atsr, segment);
    for (const auto& scope : scopes) {
        atsr.insert(atsr.end(), scope.begin(), scope.end());
    }
    return atsr;
}

std::vector<std::uint8_t> make_rhsa(std::uint64_t base,
                                     std::uint32_t proximity_domain) {
    std::vector<std::uint8_t> rhsa;
    append_u16(rhsa, 3U);
    append_u16(rhsa, 20U);
    append_u32(rhsa, 0U);
    append_u64(rhsa, base);
    append_u32(rhsa, proximity_domain);
    return rhsa;
}

bool has_code(const std::vector<iommu_guard::Issue>& issues,
              iommu_guard::IssueCode code) {
    return std::find_if(issues.begin(), issues.end(),
                        [code](const iommu_guard::Issue& issue) {
                            return issue.code == code;
                        }) != issues.end();
}

class SparseMemory {
public:
    void ensure_page(std::uint64_t address) {
        const std::uint64_t page = address & ~0xFFFULL;
        (void)pages_.try_emplace(page, std::vector<std::uint8_t>(4096U, 0U));
    }

    bool read(std::uint64_t address, void* output, std::size_t size) const {
        std::uint8_t* target = static_cast<std::uint8_t*>(output);
        std::size_t copied = 0U;
        while (copied < size) {
            const std::uint64_t current = address + copied;
            const std::uint64_t page = current & ~0xFFFULL;
            const auto iterator = pages_.find(page);
            if (iterator == pages_.end()) {
                return false;
            }
            const std::size_t offset = static_cast<std::size_t>(current & 0xFFFULL);
            const std::size_t available = 4096U - offset;
            const std::size_t chunk = std::min(size - copied, available);
            std::memcpy(target + copied, iterator->second.data() + offset,
                        chunk);
            copied += chunk;
        }
        return true;
    }

    void write(std::uint64_t address, const std::uint8_t* data,
               std::size_t size) {
        std::size_t copied = 0U;
        while (copied < size) {
            const std::uint64_t current = address + copied;
            ensure_page(current);
            const std::uint64_t page = current & ~0xFFFULL;
            const std::size_t offset = static_cast<std::size_t>(current & 0xFFFULL);
            const std::size_t available = 4096U - offset;
            const std::size_t chunk = std::min(size - copied, available);
            std::vector<std::uint8_t>& destination = pages_.at(page);
            std::memcpy(destination.data() + offset, data + copied, chunk);
            copied += chunk;
        }
    }

    void write_u64(std::uint64_t address, std::uint64_t value) {
        std::array<std::uint8_t, 8> bytes{};
        for (std::size_t index = 0U; index < bytes.size(); ++index) {
            bytes[index] = static_cast<std::uint8_t>(
                (value >> (8U * index)) & 0xFFU);
        }
        write(address, bytes.data(), bytes.size());
    }

    std::uint64_t read_u64(std::uint64_t address) const {
        std::array<std::uint8_t, 8> bytes{};
        if (!read(address, bytes.data(), bytes.size())) {
            return 0U;
        }
        std::uint64_t value = 0U;
        for (std::size_t index = 0U; index < bytes.size(); ++index) {
            value |= static_cast<std::uint64_t>(bytes[index])
                     << (8U * index);
        }
        return value;
    }

private:
    std::map<std::uint64_t, std::vector<std::uint8_t>> pages_;
};

class PageTableBuilder {
public:
    PageTableBuilder(SparseMemory& memory, std::uint8_t address_width,
                     std::uint64_t root)
        : memory_(memory),
          root_(root),
          top_level_(address_width == 39U ? 3U : 4U),
          next_table_(0x210000ULL) {
        memory_.ensure_page(root_);
    }

    bool map(std::uint64_t gpa, std::uint64_t hpa, std::uint64_t size,
             std::uint64_t extra_flags = 0U, bool snoop = true) {
        if (size != iommu_guard::vtd::kPage4K &&
            size != iommu_guard::vtd::kPage2M &&
            size != iommu_guard::vtd::kPage1G) {
            return false;
        }
        return map_level(top_level_, root_, gpa, hpa, size, extra_flags,
                         snoop);
    }

private:
    static std::uint64_t page_size_for_level(std::uint8_t level) {
        if (level == 1U) {
            return iommu_guard::vtd::kPage4K;
        }
        if (level == 2U) {
            return iommu_guard::vtd::kPage2M;
        }
        if (level == 3U) {
            return iommu_guard::vtd::kPage1G;
        }
        return 0U;
    }

    static std::uint64_t index_for(std::uint64_t gpa, std::uint8_t level) {
        const unsigned int shift =
            12U + static_cast<unsigned int>(level - 1U) * 9U;
        return (gpa >> shift) & 0x1FFULL;
    }

    std::uint64_t allocate_table() {
        const std::uint64_t table = next_table_;
        next_table_ += kPageSize;
        memory_.ensure_page(table);
        return table;
    }

    bool map_level(std::uint8_t level, std::uint64_t table,
                   std::uint64_t gpa, std::uint64_t hpa, std::uint64_t size,
                   std::uint64_t extra_flags, bool snoop) {
        const std::uint64_t index = index_for(gpa, level);
        const std::uint64_t entry_phys = table + index * 8U;
        if (size == page_size_for_level(level)) {
            std::uint64_t flags = iommu_guard::vtd::kSlptRead |
                                  iommu_guard::vtd::kSlptWrite |
                                  iommu_guard::vtd::kSlptExecute | extra_flags;
            if (level > 1U) {
                flags |= iommu_guard::vtd::kSlptPageSize;
            }
            if (snoop) {
                flags |= iommu_guard::vtd::kSlptSnoop;
            }
            memory_.write_u64(entry_phys, hpa | flags);
            return true;
        }
        if (level <= 1U) {
            return false;
        }
        const std::uint64_t current = memory_.read_u64(entry_phys);
        std::uint64_t child = 0U;
        if ((current & iommu_guard::vtd::kSlptRead) != 0U &&
            (current & iommu_guard::vtd::kSlptPageSize) == 0U) {
            child = current & kTablePointerMask;
        } else {
            child = allocate_table();
            memory_.write_u64(entry_phys, child | iommu_guard::vtd::kSlptRead);
        }
        return map_level(static_cast<std::uint8_t>(level - 1U), child, gpa,
                         hpa, size, extra_flags, snoop);
    }

    SparseMemory& memory_;
    std::uint64_t root_;
    std::uint8_t top_level_;
    std::uint64_t next_table_;
};

std::uint64_t context_table_for_bus(std::uint8_t bus) {
    return kContextTable + static_cast<std::uint64_t>(bus) * kPageSize;
}

void write_root_entry(SparseMemory& memory, std::uint8_t bus,
                      std::uint64_t context_table) {
    const std::uint64_t entry = kRootTable + static_cast<std::uint64_t>(bus) * 16U;
    memory.write_u64(entry, context_table | iommu_guard::vtd::kRootEntryPresent);
}

void write_legacy_context(SparseMemory& memory,
                          const iommu_guard::PciBdf& bdf, std::uint8_t did,
                          std::uint8_t address_width, std::uint64_t slpt,
                          std::uint8_t translation_type = 0U) {
    const std::uint64_t context_table = context_table_for_bus(bdf.bus);
    memory.ensure_page(context_table);
    write_root_entry(memory, bdf.bus, context_table);
    const std::uint16_t devfn = static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(bdf.device) << 3U) | bdf.function);
    const std::uint64_t entry = context_table +
                                static_cast<std::uint64_t>(devfn) * 16U;
    const std::uint64_t low = iommu_guard::vtd::kContextEntryPresent |
                              (static_cast<std::uint64_t>(translation_type)
                               << iommu_guard::vtd::kContextTranslationTypeShift) |
                              (slpt & kTablePointerMask);
    const std::uint64_t high =
        static_cast<std::uint64_t>(address_width) |
        (static_cast<std::uint64_t>(did) << iommu_guard::vtd::kContextDomainIdShift);
    memory.write_u64(entry, low);
    memory.write_u64(entry + 8U, high);
}

void write_scalable_context(SparseMemory& memory,
                            const iommu_guard::PciBdf& bdf,
                            std::uint64_t pasid_directory) {
    const std::uint64_t context_table = context_table_for_bus(bdf.bus);
    memory.ensure_page(context_table);
    write_root_entry(memory, bdf.bus, context_table);
    const std::uint16_t devfn = static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(bdf.device) << 3U) | bdf.function);
    const std::uint64_t entry = context_table +
                                static_cast<std::uint64_t>(devfn) *
                                    iommu_guard::vtd::kScalableContextEntrySize;
    const std::uint64_t low =
        iommu_guard::vtd::kContextEntryPresent |
        (7ULL << iommu_guard::vtd::kScalableContextPdsShift) |
        (pasid_directory & kTablePointerMask);
    memory.write_u64(entry, low);
    memory.write_u64(entry + 8U, 0U);
    memory.write_u64(entry + 16U, 0U);
    memory.write_u64(entry + 24U, 0U);
}

void write_pasid_entry(SparseMemory& memory, std::uint32_t pasid,
                       std::uint8_t pgtt, std::uint64_t slpt,
                       std::uint8_t address_width,
                       std::uint16_t did) {
    memory.ensure_page(kPasidDirectory);
    memory.ensure_page(kPasidTable);
    const std::uint64_t directory_index =
        static_cast<std::uint64_t>(pasid) >>
        iommu_guard::vtd::kPasidDirectoryShift;
    memory.write_u64(
        kPasidDirectory + directory_index * 8U,
        kPasidTable | iommu_guard::vtd::kPasidDirectoryPresent);
    const std::uint64_t table_index =
        static_cast<std::uint64_t>(pasid) &
        iommu_guard::vtd::kPasidTableIndexMask;
    const std::uint64_t entry = kPasidTable +
                                table_index *
                                    iommu_guard::vtd::kPasidTableEntrySize;
    const std::uint64_t value =
        iommu_guard::vtd::kPasidTablePresent |
        (static_cast<std::uint64_t>(address_width)
         << iommu_guard::vtd::kPasidAddressWidthShift) |
        (static_cast<std::uint64_t>(pgtt)
         << iommu_guard::vtd::kPasidPgttShift) |
        (slpt & kTablePointerMask);
    memory.write_u64(entry, value);
    memory.write_u64(
        entry + 8U,
        static_cast<std::uint64_t>(did)
            << iommu_guard::vtd::kPasidDomainIdShift);
}

iommu_guard::PciDeviceInfo make_device(const iommu_guard::PciBdf& bdf,
                                       std::uint32_t security_domain,
                                       std::uint8_t phantom_mask = 0U) {
    iommu_guard::PciDeviceInfo device;
    device.bdf = bdf;
    device.security_domain = security_domain;
    device.phantom_alias_mask = phantom_mask;
    return device;
}

iommu_guard::DmarTable parse_one_include_all(
    std::uint16_t segment = 0U, bool add_rmrr = false) {
    DmarBuilder builder;
    const auto endpoint_scope = make_scope(1U, 0U, {{2U, 0U}});
    builder.add(make_drhd(true, segment, 0xFED90000ULL, {}));
    if (add_rmrr) {
        builder.add(make_rmrr(segment, 0x300000ULL, 0x300FFFU,
                              {endpoint_scope}));
    }
    const auto parsed = iommu_guard::parse_dmar(builder.finish());
    CHECK(parsed.ok);
    return parsed.table;
}

iommu_guard::MemoryRead memory_reader(const SparseMemory* memory) {
    return [memory](iommu_guard::PhysicalAddress address, void* output,
                    std::size_t size) {
        return memory->read(address, output, size);
    };
}

void test_interval_merge() {
    const auto merged = iommu_guard::merge_ranges(
        {{1U, 5U}, {3U, 7U}, {8U, 10U}, {10U, 11U}, {2U, 3U}});
    CHECK(merged.size() == 2U);
    if (merged.size() == 2U) {
        CHECK(merged[0].begin == 1U && merged[0].end == 7U);
        CHECK(merged[1].begin == 8U && merged[1].end == 11U);
    }
}

void test_dmar_valid_and_integrity() {
    const auto endpoint_scope = make_scope(1U, 0U, {{2U, 0U}});
    DmarBuilder builder;
    builder.add(make_drhd(true, 0U, 0xFED90000ULL, {}));
    builder.add(make_rmrr(0U, 0x300000ULL, 0x300FFFU, {endpoint_scope}));
    builder.add(make_atsr(0U, {endpoint_scope}));
    builder.add(make_rhsa(0xFEE00000ULL, 3U));
    const auto bytes = builder.finish();

    const auto parsed = iommu_guard::parse_dmar(bytes);
    CHECK(parsed.ok);
    CHECK(parsed.table.host_address_width == 39U);
    CHECK(parsed.table.drhds.size() == 1U);
    CHECK(parsed.table.rmrrs.size() == 1U);
    CHECK(parsed.table.atsrs.size() == 1U);
    CHECK(parsed.table.rhsas.size() == 1U);

    auto checksum_bad = bytes;
    checksum_bad[9] = static_cast<std::uint8_t>(checksum_bad[9] + 1U);
    const auto checksum_result = iommu_guard::parse_dmar(checksum_bad);
    CHECK(!checksum_result.ok);
    CHECK(has_code(checksum_result.issues,
                   iommu_guard::IssueCode::kDmarChecksumMismatch));

    const auto truncated_input =
        iommu_guard::parse_dmar(bytes.data(), bytes.size() - 1U);
    CHECK(!truncated_input.ok);
    CHECK(has_code(truncated_input.issues,
                   iommu_guard::IssueCode::kDmarBadTableLength));

    DmarBuilder only_drhd;
    only_drhd.add(make_drhd(true, 0U, 0xFED90000ULL, {}));
    auto structure_bad = only_drhd.finish();
    structure_bad[50] = 0xFFU;
    structure_bad[51] = 0xFFU;
    const auto structure_result = iommu_guard::parse_dmar(structure_bad);
    CHECK(!structure_result.ok);
    CHECK(has_code(structure_result.issues,
                   iommu_guard::IssueCode::kDmarTruncatedStructure));
}

void test_dmar_include_all_conflicts() {
    DmarBuilder duplicate;
    duplicate.add(make_drhd(true, 0U, 0xFED90000ULL, {}));
    duplicate.add(make_drhd(true, 0U, 0xFED92000ULL, {}));
    const auto duplicate_result =
        iommu_guard::parse_dmar(duplicate.finish());
    CHECK(!duplicate_result.ok);
    CHECK(has_code(duplicate_result.issues,
                   iommu_guard::IssueCode::kDmarDuplicateIncludePciAll));

    DmarBuilder mixed;
    mixed.add(make_drhd(true, 0U, 0xFED90000ULL, {}));
    mixed.add(make_drhd(false, 0U, 0xFED92000ULL,
                        {make_scope(1U, 0U, {{2U, 0U}})}));
    const auto mixed_result = iommu_guard::parse_dmar(mixed.finish());
    CHECK(!mixed_result.ok);
    CHECK(has_code(
        mixed_result.issues,
        iommu_guard::IssueCode::kDmarIncludePciAllSegmentConflict));
}

void test_dmar_rmrr_validation() {
    const auto scope = make_scope(1U, 0U, {{2U, 0U}});

    auto build_and_parse = [&scope](std::uint64_t base, std::uint64_t limit) {
        DmarBuilder builder;
        builder.add(make_drhd(true, 0U, 0xFED90000ULL, {}));
        builder.add(make_rmrr(0U, base, limit, {scope}));
        return iommu_guard::parse_dmar(builder.finish());
    };

    const auto misaligned = build_and_parse(0x300001ULL, 0x300FFFU);
    CHECK(!misaligned.ok);
    CHECK(has_code(misaligned.issues,
                   iommu_guard::IssueCode::kDmarBadRmrrAlignment));

    const auto bad_limit_low = build_and_parse(0x300000ULL, 0x300FFEULL);
    CHECK(!bad_limit_low.ok);
    CHECK(has_code(bad_limit_low.issues,
                   iommu_guard::IssueCode::kDmarBadRmrrAlignment));

    const auto reversed = build_and_parse(0x400000ULL, 0x300FFFU);
    CHECK(!reversed.ok);
    CHECK(has_code(reversed.issues,
                   iommu_guard::IssueCode::kDmarBadRmrrLimit));

    const auto overflow = build_and_parse(0x300000ULL, ~0ULL);
    CHECK(!overflow.ok);
    CHECK(has_code(overflow.issues,
                   iommu_guard::IssueCode::kDmarRmrrLimitOverflow));

    const auto valid = build_and_parse(0x300000ULL, 0x300FFFU);
    CHECK(valid.ok);
}

void test_device_scope_validation() {
    DmarBuilder out_of_range;
    out_of_range.add(make_drhd(
        false, 0U, 0xFED90000ULL,
        {make_scope(1U, 0U, {{32U, 0U}})}));
    const auto out_of_range_result =
        iommu_guard::parse_dmar(out_of_range.finish());
    CHECK(!out_of_range_result.ok);
    CHECK(has_code(out_of_range_result.issues,
                   iommu_guard::IssueCode::kDmarInvalidPciPath));

    DmarBuilder endpoint_multipath;
    endpoint_multipath.add(make_drhd(
        false, 0U, 0xFED90000ULL,
        {make_scope(1U, 0U, {{1U, 0U}, {2U, 0U}})}));
    const auto multipath_result =
        iommu_guard::parse_dmar(endpoint_multipath.finish());
    CHECK(!multipath_result.ok);
    CHECK(has_code(multipath_result.issues,
                   iommu_guard::IssueCode::kDmarInvalidPciPath));
}

iommu_guard::PciBdf bdf(std::uint8_t bus, std::uint8_t device,
                        std::uint8_t function) {
    return iommu_guard::PciBdf{0U, bus, device, function};
}

void test_pci_ownership_and_bridge_conflicts() {
    const auto endpoint = make_scope(1U, 0U, {{2U, 0U}});
    DmarBuilder overlapping;
    overlapping.add(make_drhd(false, 0U, 0xFED90000ULL, {endpoint}));
    overlapping.add(make_drhd(false, 0U, 0xFED92000ULL, {endpoint}));
    const auto parsed = iommu_guard::parse_dmar(overlapping.finish());
    CHECK(parsed.ok);
    iommu_guard::PciTopology topology;
    topology.devices.push_back(make_device(bdf(0U, 2U, 0U), 1U));
    const auto report = iommu_guard::audit_pci_topology(parsed.table,
                                                        topology);
    CHECK(!report.ok);
    CHECK(has_code(report.issues,
                   iommu_guard::IssueCode::kPciMultipleOwners));

    DmarBuilder bridge_conflict;
    bridge_conflict.add(make_drhd(
        false, 0U, 0xFED90000ULL,
        {make_scope(2U, 0U, {{1U, 0U}})}));
    bridge_conflict.add(make_drhd(
        false, 0U, 0xFED92000ULL,
        {make_scope(1U, 0x10U, {{0U, 0U}})}));
    const auto bridge_parsed =
        iommu_guard::parse_dmar(bridge_conflict.finish());
    CHECK(bridge_parsed.ok);

    topology.devices.clear();
    iommu_guard::PciDeviceInfo bridge = make_device(bdf(0U, 1U, 0U), 1U);
    bridge.is_bridge = true;
    bridge.secondary_bus = 0x10U;
    bridge.subordinate_bus = 0x1FU;
    topology.devices.push_back(bridge);
    topology.devices.push_back(make_device(bdf(0x10U, 0U, 0U), 2U));
    const auto bridge_report =
        iommu_guard::audit_pci_topology(bridge_parsed.table, topology);
    CHECK(!bridge_report.ok);
    CHECK(has_code(bridge_report.issues,
                   iommu_guard::IssueCode::kPciBridgeScopeConflict));
}

void test_phantom_alias_conflict() {
    DmarBuilder builder;
    builder.add(make_drhd(
        false, 0U, 0xFED90000ULL,
        {make_scope(1U, 0U, {{1U, 0U}})}));
    builder.add(make_drhd(
        false, 0U, 0xFED92000ULL,
        {make_scope(1U, 0U, {{1U, 4U}})}));
    const auto parsed = iommu_guard::parse_dmar(builder.finish());
    CHECK(parsed.ok);

    iommu_guard::PciTopology topology;
    topology.devices.push_back(make_device(bdf(0U, 1U, 0U), 1U, 0x7U));
    topology.devices.push_back(make_device(bdf(0U, 1U, 4U), 2U));
    const auto report = iommu_guard::audit_pci_topology(parsed.table,
                                                        topology);
    CHECK(!report.ok);
    CHECK(has_code(report.issues,
                   iommu_guard::IssueCode::kPhantomAliasConflict));
}

struct MappingSpec {
    std::uint64_t gpa;
    std::uint64_t hpa;
    std::uint64_t size;
    std::uint64_t extra_flags = 0U;
};

void build_legacy_translation(SparseMemory& memory,
                              const iommu_guard::PciBdf& device,
                              std::uint8_t did,
                              const std::vector<MappingSpec>& specs,
                              std::uint8_t translation_type = 0U) {
    PageTableBuilder builder(memory, 48U, kSlptTop);
    for (const MappingSpec& spec : specs) {
        CHECK(builder.map(spec.gpa, spec.hpa, spec.size, spec.extra_flags));
    }
    write_legacy_context(memory, device, did, 2U, kSlptTop,
                         translation_type);
}

void test_normal_4k_2m_1g_and_policy() {
    SparseMemory memory;
    const auto device = bdf(0U, 2U, 0U);
    build_legacy_translation(
        memory, device, 7U,
        {{0x100000ULL, 0x200000ULL, iommu_guard::vtd::kPage4K},
         {0x200000ULL, 0x400000ULL, iommu_guard::vtd::kPage2M,
          iommu_guard::vtd::kSlptAccessed | iommu_guard::vtd::kSlptDirty},
         {0x40000000ULL, 0x80000000ULL, iommu_guard::vtd::kPage1G}});
    const auto read = memory_reader(&memory);
    const iommu_guard::TranslationRoot root{kRootTable, 39U, false};
    const auto walk = iommu_guard::walk_device_translation(
        root, device, std::nullopt, read);
    CHECK(walk.issues.empty());
    CHECK(walk.mappings.size() == 3U);
    CHECK(walk.did.has_value() && *walk.did == 7U);
    for (const iommu_guard::SlptMapping& mapping : walk.mappings) {
        CHECK(mapping.snoop);
        CHECK(!mapping.walk_entry_phys.empty());
    }

    const auto table = parse_one_include_all();
    iommu_guard::PciTopology topology;
    topology.devices.push_back(make_device(device, 7U));
    iommu_guard::IsolationPolicy policy;
    policy.protected_memory.push_back(
        {{0x100000000ULL, 0x100001000ULL}, "hypervisor"});
    iommu_guard::DeviceAssignment assignment;
    assignment.bdf = device;
    assignment.security_domain = 7U;
    const auto report = iommu_guard::audit_isolation(
        table, topology, root, read, policy, {assignment});
    CHECK(report.ok);
}

void test_39bit_4k_2m_1g_walk() {
    SparseMemory memory;
    const auto device = bdf(0U, 2U, 0U);
    PageTableBuilder builder(memory, 39U, kSlptTop);
    CHECK(builder.map(0x100000ULL, 0x200000ULL,
                      iommu_guard::vtd::kPage4K));
    CHECK(builder.map(0x200000ULL, 0x400000ULL,
                      iommu_guard::vtd::kPage2M));
    CHECK(builder.map(0x40000000ULL, 0x80000000ULL,
                      iommu_guard::vtd::kPage1G));
    write_legacy_context(memory, device, 3U, 1U, kSlptTop);
    const auto read = memory_reader(&memory);
    const iommu_guard::TranslationRoot root{kRootTable, 39U, false};
    const auto walk = iommu_guard::walk_device_translation(
        root, device, std::nullopt, read);
    CHECK(walk.issues.empty());
    CHECK(walk.mappings.size() == 3U);
    CHECK(walk.address_width_bits.has_value() &&
          *walk.address_width_bits == 39U);
}

void test_large_page_protected_overlap() {
    const std::uint64_t sizes[] = {iommu_guard::vtd::kPage2M,
                                   iommu_guard::vtd::kPage1G};
    const std::uint64_t hpas[] = {0x400000ULL, 0x80000000ULL};
    const std::uint64_t gpas[] = {0x200000ULL, 0x40000000ULL};
    const std::uint64_t protected_starts[] = {0x401000ULL, 0x80010000ULL};

    for (std::size_t index = 0U; index < 2U; ++index) {
        SparseMemory memory;
        const auto device = bdf(0U, 2U, 0U);
        build_legacy_translation(
            memory, device, 7U, {{gpas[index], hpas[index], sizes[index]}});
        const auto read = memory_reader(&memory);
        const iommu_guard::TranslationRoot root{kRootTable, 39U, false};
        const auto table = parse_one_include_all();
        iommu_guard::PciTopology topology;
        topology.devices.push_back(make_device(device, 7U));
        iommu_guard::IsolationPolicy policy;
        policy.protected_memory.push_back(
            {{protected_starts[index], protected_starts[index] + 0x1000ULL},
             "secure-monitor"});
        iommu_guard::DeviceAssignment assignment;
        assignment.bdf = device;
        assignment.security_domain = 7U;
        const auto report = iommu_guard::audit_isolation(
            table, topology, root, read, policy, {assignment});
        CHECK(!report.ok);
        CHECK(has_code(report.issues,
                       iommu_guard::IssueCode::kMappingInProtectedMemory));
        bool found_exact = false;
        for (const iommu_guard::Issue& issue : report.issues) {
            if (issue.code ==
                    iommu_guard::IssueCode::kMappingInProtectedMemory &&
                issue.hpa.has_value() &&
                issue.hpa->begin == protected_starts[index] &&
                issue.hpa->size() == 0x1000ULL) {
                found_exact = true;
            }
        }
        CHECK(found_exact);
    }
}

void test_mmio_authorization() {
    SparseMemory memory;
    const auto device = bdf(0U, 2U, 0U);
    build_legacy_translation(
        memory, device, 7U,
        {{0x100000ULL, 0x500000ULL, iommu_guard::vtd::kPage4K}});
    const auto read = memory_reader(&memory);
    const iommu_guard::TranslationRoot root{kRootTable, 39U, false};
    const auto table = parse_one_include_all();
    iommu_guard::PciTopology topology;
    topology.devices.push_back(make_device(device, 7U));
    iommu_guard::IsolationPolicy policy;
    iommu_guard::SensitiveMmioRange mmio{
        {0x500000ULL, 0x501000ULL}, "IOMMU-registers", {}};
    policy.sensitive_mmio.push_back(mmio);
    iommu_guard::DeviceAssignment assignment;
    assignment.bdf = device;
    assignment.security_domain = 7U;

    const auto denied = iommu_guard::audit_isolation(
        table, topology, root, read, policy, {assignment});
    CHECK(!denied.ok);
    CHECK(has_code(denied.issues,
                   iommu_guard::IssueCode::kMappingInUnauthorizedMmio));

    policy.sensitive_mmio[0].allowed_security_domains = {7U};
    const auto allowed = iommu_guard::audit_isolation(
        table, topology, root, read, policy, {assignment});
    CHECK(allowed.ok);
}

void test_rmrr_identity_and_overlap() {
    const auto device = bdf(0U, 2U, 0U);
    const auto table = parse_one_include_all(0U, true);
    CHECK(table.rmrrs.size() == 1U);
    const iommu_guard::TranslationRoot root{kRootTable, 39U, false};
    iommu_guard::PciTopology topology;
    topology.devices.push_back(make_device(device, 7U));
    iommu_guard::DeviceAssignment assignment;
    assignment.bdf = device;
    assignment.security_domain = 7U;

    {
        SparseMemory memory;
        build_legacy_translation(
            memory, device, 7U,
            {{0x300000ULL, 0x300000ULL, iommu_guard::vtd::kPage4K}});
        const auto read = memory_reader(&memory);
        const auto report = iommu_guard::audit_isolation(
            table, topology, root, read, {}, {assignment});
        CHECK(report.ok);
    }

    {
        SparseMemory memory;
        build_legacy_translation(
            memory, device, 7U,
            {{0x300000ULL, 0x400000ULL, iommu_guard::vtd::kPage4K}});
        const auto read = memory_reader(&memory);
        const auto report = iommu_guard::audit_isolation(
            table, topology, root, read, {}, {assignment});
        CHECK(!report.ok);
        CHECK(has_code(report.issues,
                       iommu_guard::IssueCode::kRmrrIdentityMappingMissing));
    }

    {
        SparseMemory memory;
        build_legacy_translation(
            memory, device, 7U,
            {{0x300000ULL, 0x300000ULL, iommu_guard::vtd::kPage4K}});
        const auto read = memory_reader(&memory);
        iommu_guard::IsolationPolicy policy;
        policy.protected_memory.push_back(
            {{0x300000ULL, 0x301000ULL}, "kernel"});
        const auto report = iommu_guard::audit_isolation(
            table, topology, root, read, policy, {assignment});
        CHECK(!report.ok);
        CHECK(has_code(report.issues,
                       iommu_guard::IssueCode::kRmrrOverlapsProtectedMemory));
        CHECK(has_code(report.issues,
                       iommu_guard::IssueCode::kMappingInProtectedMemory));
    }
}

void test_legacy_pass_through_and_disabled_remap() {
    SparseMemory memory;
    const auto device = bdf(0U, 2U, 0U);
    write_legacy_context(memory, device, 7U, 2U, 0U, 2U);
    const auto read = memory_reader(&memory);
    const iommu_guard::TranslationRoot root{kRootTable, 39U, false};
    const auto walk = iommu_guard::walk_device_translation(
        root, device, std::nullopt, read);
    CHECK(walk.pass_through);
    CHECK(has_code(walk.issues,
                   iommu_guard::IssueCode::kVtdTranslationTypePassThrough));

    const auto table = parse_one_include_all();
    iommu_guard::PciTopology topology;
    topology.devices.push_back(make_device(device, 7U));
    iommu_guard::DeviceAssignment disabled;
    disabled.bdf = device;
    disabled.security_domain = 7U;
    disabled.dma_remapping_enabled = false;
    const auto report = iommu_guard::audit_isolation(
        table, topology, root, read, {}, {disabled});
    CHECK(!report.ok);
    CHECK(has_code(report.issues,
                   iommu_guard::IssueCode::kDmaRemappingDisabled));
    CHECK(has_code(report.issues,
                   iommu_guard::IssueCode::kVtdTranslationTypePassThrough));
}

void test_page_table_security_validation() {
    const auto device = bdf(0U, 2U, 0U);
    const iommu_guard::TranslationRoot root{kRootTable, 39U, false};

    {
        SparseMemory memory;
        build_legacy_translation(
            memory, device, 7U,
            {{0x100000ULL, 0x200000ULL, iommu_guard::vtd::kPage4K,
              1ULL << 10U}});
        const auto read = memory_reader(&memory);
        const auto walk = iommu_guard::walk_device_translation(
            root, device, std::nullopt, read);
        CHECK(!walk.issues.empty());
        CHECK(has_code(walk.issues,
                       iommu_guard::IssueCode::kVtdPageTableReserved));
    }

    {
        SparseMemory memory;
        build_legacy_translation(
            memory, device, 7U,
            {{0x40000000ULL, 0x80001000ULL, iommu_guard::vtd::kPage1G}});
        const auto read = memory_reader(&memory);
        const auto walk = iommu_guard::walk_device_translation(
            root, device, std::nullopt, read);
        CHECK(!walk.issues.empty());
        CHECK(has_code(walk.issues,
                       iommu_guard::IssueCode::kVtdSuperpageMisaligned));
    }

    {
        SparseMemory memory;
        build_legacy_translation(
            memory, device, 7U,
            {{0x100000ULL, 0x200000ULL, iommu_guard::vtd::kPage4K,
              1ULL << 40U}});
        const auto read = memory_reader(&memory);
        const auto walk = iommu_guard::walk_device_translation(
            root, device, std::nullopt, read);
        CHECK(!walk.issues.empty());
        CHECK(has_code(walk.issues,
                       iommu_guard::IssueCode::kVtdHpaOutOfRange));
    }

    {
        SparseMemory memory;
        build_legacy_translation(
            memory, device, 7U,
            {{0x100000ULL, 0x200000ULL, iommu_guard::vtd::kPage4K}});
        const auto read = memory_reader(&memory);
        const auto before = iommu_guard::walk_device_translation(
            root, device, std::nullopt, read);
        CHECK(before.mappings.size() == 1U);
        if (before.mappings.size() == 1U) {
            memory.write_u64(before.mappings[0].leaf_entry_phys,
                             iommu_guard::vtd::kSlptWrite);
            const auto after = iommu_guard::walk_device_translation(
                root, device, std::nullopt, read);
            CHECK(has_code(
                after.issues,
                iommu_guard::IssueCode::kVtdPagePermissionsInvalid));
        }
    }

    {
        SparseMemory memory;
        build_legacy_translation(
            memory, device, 7U,
            {{0x100000ULL, 0x200000ULL, iommu_guard::vtd::kPage4K}});
        const auto read = memory_reader(&memory);
        const auto before = iommu_guard::walk_device_translation(
            root, device, std::nullopt, read);
        CHECK(before.mappings.size() == 1U);
        if (before.mappings.size() == 1U &&
            before.mappings[0].walk_entry_phys.size() >= 3U) {
            memory.write_u64(before.mappings[0].walk_entry_phys[2],
                             iommu_guard::vtd::kSlptRead | 0x900000ULL);
            const auto after = iommu_guard::walk_device_translation(
                root, device, std::nullopt, read);
            CHECK(has_code(after.issues,
                           iommu_guard::IssueCode::kVtdMemoryReadFailed));
        }
    }
}

void test_scalable_second_level_and_pass_through() {
    const auto device = bdf(0U, 2U, 0U);
    constexpr std::uint32_t pasid = 0x42U;

    {
        SparseMemory memory;
        PageTableBuilder builder(memory, 48U, kSlptTop);
        CHECK(builder.map(0x100000ULL, 0x200000ULL,
                          iommu_guard::vtd::kPage4K));
        write_scalable_context(memory, device, kPasidDirectory);
        write_pasid_entry(memory, pasid,
                          iommu_guard::vtd::kPgttSecondLevel, kSlptTop, 2U,
                          9U);
        const auto read = memory_reader(&memory);
        const iommu_guard::TranslationRoot root{kRootTable, 39U, true};
        const auto walk = iommu_guard::walk_device_translation(
            root, device, pasid, read);
        CHECK(walk.issues.empty());
        CHECK(walk.mappings.size() == 1U);
        CHECK(walk.pasid.has_value() && *walk.pasid == pasid);
        CHECK(walk.did.has_value() && *walk.did == 9U);

        write_pasid_entry(memory, pasid, iommu_guard::vtd::kPgttNested,
                          kSlptTop, 2U, 9U);
        const auto nested = iommu_guard::walk_device_translation(
            root, device, pasid, read);
        CHECK(nested.issues.empty());
        CHECK(nested.mappings.size() == 1U);
    }

    {
        SparseMemory memory;
        write_scalable_context(memory, device, kPasidDirectory);
        write_pasid_entry(memory, pasid,
                          iommu_guard::vtd::kPgttPassThrough, 0U, 2U, 9U);
        const auto read = memory_reader(&memory);
        const iommu_guard::TranslationRoot root{kRootTable, 39U, true};
        const auto walk = iommu_guard::walk_device_translation(
            root, device, pasid, read);
        CHECK(walk.pass_through);
        CHECK(has_code(walk.issues,
                       iommu_guard::IssueCode::kVtdPgttPassThrough));
    }

    {
        SparseMemory memory;
        write_scalable_context(memory, device, kPasidDirectory);
        write_pasid_entry(memory, pasid,
                          iommu_guard::vtd::kPgttFirstLevel, kSlptTop, 2U,
                          9U);
        const auto read = memory_reader(&memory);
        const iommu_guard::TranslationRoot root{kRootTable, 39U, true};
        const auto walk = iommu_guard::walk_device_translation(
            root, device, pasid, read);
        CHECK(has_code(walk.issues,
                       iommu_guard::IssueCode::kVtdPgttUnsupported));
    }
}

void test_cross_tenant_did_reuse() {
    const auto first = bdf(0U, 1U, 0U);
    const auto second = bdf(0U, 2U, 0U);
    SparseMemory memory;
    memory.ensure_page(kSlptTop);
    write_legacy_context(memory, first, 0x55U, 2U, kSlptTop);
    write_legacy_context(memory, second, 0x55U, 2U, kSlptTop);
    const auto read = memory_reader(&memory);

    const auto table = parse_one_include_all();
    iommu_guard::PciTopology topology;
    topology.devices.push_back(make_device(first, 1U));
    topology.devices.push_back(make_device(second, 2U));
    iommu_guard::DeviceAssignment first_assignment;
    first_assignment.bdf = first;
    first_assignment.security_domain = 1U;
    iommu_guard::DeviceAssignment second_assignment;
    second_assignment.bdf = second;
    second_assignment.security_domain = 2U;
    const iommu_guard::TranslationRoot root{kRootTable, 39U, false};
    const auto report = iommu_guard::audit_isolation(
        table, topology, root, read, {},
        {first_assignment, second_assignment});
    CHECK(!report.ok);
    CHECK(has_code(report.issues,
                   iommu_guard::IssueCode::kDidSharedAcrossSecurityDomains));
}

}  // namespace

int main() {
    test_interval_merge();
    test_dmar_valid_and_integrity();
    test_dmar_include_all_conflicts();
    test_dmar_rmrr_validation();
    test_device_scope_validation();
    test_pci_ownership_and_bridge_conflicts();
    test_phantom_alias_conflict();
    test_normal_4k_2m_1g_and_policy();
    test_39bit_4k_2m_1g_walk();
    test_large_page_protected_overlap();
    test_mmio_authorization();
    test_rmrr_identity_and_overlap();
    test_legacy_pass_through_and_disabled_remap();
    test_page_table_security_validation();
    test_scalable_second_level_and_pass_through();
    test_cross_tenant_did_reuse();

    if (g_failures == 0) {
        std::printf("all iommu_guard tests passed\n");
        return 0;
    }
    std::fprintf(stderr, "%d test checks failed\n", g_failures);
    return 1;
}
