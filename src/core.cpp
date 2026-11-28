#include "internal.hpp"

#include <cstdio>
#include <limits>
#include <tuple>

namespace iommu_guard {

bool operator<(const AddressRange& lhs, const AddressRange& rhs) noexcept {
    return std::tie(lhs.begin, lhs.end) < std::tie(rhs.begin, rhs.end);
}

std::optional<AddressRange> intersect_ranges(const AddressRange& lhs,
                                              const AddressRange& rhs) noexcept {
    if (!lhs.valid() || !rhs.valid()) {
        return std::nullopt;
    }
    const std::uint64_t begin = std::max(lhs.begin, rhs.begin);
    const std::uint64_t end = std::min(lhs.end, rhs.end);
    if (begin >= end) {
        return std::nullopt;
    }
    return AddressRange{begin, end};
}

std::vector<AddressRange> merge_ranges(std::vector<AddressRange> ranges) {
    ranges.erase(std::remove_if(ranges.begin(), ranges.end(),
                                [](const AddressRange& range) {
                                    return !range.valid();
                                }),
                 ranges.end());
    std::sort(ranges.begin(), ranges.end(),
              [](const AddressRange& lhs, const AddressRange& rhs) {
                  return std::tie(lhs.begin, lhs.end) <
                         std::tie(rhs.begin, rhs.end);
              });

    std::vector<AddressRange> merged;
    merged.reserve(ranges.size());
    for (const AddressRange& range : ranges) {
        if (merged.empty() || range.begin > merged.back().end) {
            merged.push_back(range);
        } else {
            merged.back().end = std::max(merged.back().end, range.end);
        }
    }
    return merged;
}

bool operator==(const PciBdf& lhs, const PciBdf& rhs) noexcept {
    return lhs.segment == rhs.segment && lhs.bus == rhs.bus &&
           lhs.device == rhs.device && lhs.function == rhs.function;
}

bool operator!=(const PciBdf& lhs, const PciBdf& rhs) noexcept {
    return !(lhs == rhs);
}

bool operator<(const PciBdf& lhs, const PciBdf& rhs) noexcept {
    return std::tie(lhs.segment, lhs.bus, lhs.device, lhs.function) <
           std::tie(rhs.segment, rhs.bus, rhs.device, rhs.function);
}

std::string format_bdf(const PciBdf& bdf) {
    char buffer[48] = {};
    std::snprintf(buffer, sizeof(buffer), "%04x:%02x:%02x.%x",
                  static_cast<unsigned int>(bdf.segment),
                  static_cast<unsigned int>(bdf.bus),
                  static_cast<unsigned int>(bdf.device),
                  static_cast<unsigned int>(bdf.function));
    return std::string(buffer);
}

const char* to_string(IssueCode code) noexcept {
    switch (code) {
        case IssueCode::kInvalidArgument:
            return "invalid argument";
        case IssueCode::kDmarHeaderTooShort:
            return "DMAR header is shorter than 48 bytes";
        case IssueCode::kDmarBadSignature:
            return "DMAR signature is not 'DMAR'";
        case IssueCode::kDmarBadTableLength:
            return "DMAR table length is invalid";
        case IssueCode::kDmarChecksumMismatch:
            return "DMAR checksum mismatch";
        case IssueCode::kDmarInvalidHaw:
            return "DMAR host address width is invalid";
        case IssueCode::kDmarTruncatedStructure:
            return "DMAR remapping structure is truncated";
        case IssueCode::kDmarInvalidStructureLength:
            return "DMAR remapping structure length is invalid";
        case IssueCode::kDmarUnknownStructure:
            return "DMAR remapping structure type is unknown";
        case IssueCode::kDmarDuplicateIncludePciAll:
            return "multiple INCLUDE_PCI_ALL DRHDs in one segment";
        case IssueCode::kDmarIncludePciAllSegmentConflict:
            return "INCLUDE_PCI_ALL DRHD coexists with another DRHD";
        case IssueCode::kDmarInvalidDeviceScope:
            return "DMAR device scope is invalid";
        case IssueCode::kDmarInvalidPciPath:
            return "DMAR PCI path is invalid or out of range";
        case IssueCode::kDmarMissingDrhd:
            return "DMAR has no DRHD";
        case IssueCode::kDmarBadRegisterBase:
            return "DMAR remapping hardware base is invalid";
        case IssueCode::kDmarBadRmrrAlignment:
            return "RMRR range is not 4KiB aligned";
        case IssueCode::kDmarBadRmrrLimit:
            return "RMRR limit is below base or is not ...fff";
        case IssueCode::kDmarRmrrLimitOverflow:
            return "RMRR limit+1 overflows 64 bits";
        case IssueCode::kDmarBadRhsa:
            return "RHSA structure is invalid";
        case IssueCode::kPciMultipleOwners:
            return "PCI device is owned by multiple DRHDs";
        case IssueCode::kPciNoOwner:
            return "PCI device has no responsible DRHD";
        case IssueCode::kPciBridgeMetadataMissing:
            return "PCI bridge scope has no topology metadata";
        case IssueCode::kPciBridgeScopeConflict:
            return "PCI bridge scope conflicts with a downstream owner";
        case IssueCode::kPhantomAliasConflict:
            return "phantom function alias crosses an isolation boundary";
        case IssueCode::kVtdMemoryReadFailed:
            return "physical memory read failed";
        case IssueCode::kVtdRootEntryNotPresent:
            return "VT-d root entry is not present";
        case IssueCode::kVtdRootEntryReserved:
            return "VT-d root entry has reserved bits set";
        case IssueCode::kVtdRootEntryOutOfRange:
            return "VT-d root entry pointer exceeds HAW";
        case IssueCode::kVtdContextEntryNotPresent:
            return "VT-d context entry is not present";
        case IssueCode::kVtdContextEntryReserved:
            return "VT-d context entry has reserved bits set";
        case IssueCode::kVtdContextEntryOutOfRange:
            return "VT-d context entry pointer exceeds HAW";
        case IssueCode::kVtdTranslationTypePassThrough:
            return "VT-d context uses pass-through translation";
        case IssueCode::kVtdTranslationTypeUnsupported:
            return "VT-d context translation type is unsupported";
        case IssueCode::kVtdAddressWidthUnsupported:
            return "VT-d context address width is not 39/48-bit";
        case IssueCode::kVtdScalableLayoutInvalid:
            return "VT-d scalable-mode layout is invalid";
        case IssueCode::kVtdPasidDirectoryNotPresent:
            return "VT-d PASID directory entry is not present";
        case IssueCode::kVtdPasidTableEntryNotPresent:
            return "VT-d PASID table entry is not present";
        case IssueCode::kVtdPgttPassThrough:
            return "VT-d PASID PGTT is pass-through";
        case IssueCode::kVtdPgttUnsupported:
            return "VT-d PASID PGTT is unsupported";
        case IssueCode::kVtdPageTableReserved:
            return "VT-d page-table entry has reserved bits set";
        case IssueCode::kVtdPagePermissionsInvalid:
            return "VT-d page-table entry permissions are invalid";
        case IssueCode::kVtdSuperpageMisaligned:
            return "VT-d superpage is misaligned or truncated";
        case IssueCode::kVtdHpaOutOfRange:
            return "VT-d output HPA exceeds HAW+1 bits";
        case IssueCode::kVtdGpaOutOfRange:
            return "VT-d GPA exceeds the context address width";
        case IssueCode::kVtdMappingLimitExceeded:
            return "VT-d page-walk mapping limit exceeded";
        case IssueCode::kMappingInProtectedMemory:
            return "DMA mapping intersects protected memory";
        case IssueCode::kMappingInUnauthorizedMmio:
            return "DMA mapping targets unauthorized sensitive MMIO";
        case IssueCode::kRmrrOverlapsProtectedMemory:
            return "RMRR intersects protected memory";
        case IssueCode::kRmrrIdentityMappingMissing:
            return "RMRR lacks a strict identity mapping";
        case IssueCode::kDmaRemappingDisabled:
            return "DMA remapping is disabled for the device";
        case IssueCode::kDidSharedAcrossSecurityDomains:
            return "DID is shared by mutually exclusive security domains";
    }
    return "unknown issue";
}

std::uint64_t DmarTable::physical_address_mask() const noexcept {
    return detail::max_address_for_haw(host_address_width);
}

DmarParseResult parse_dmar(const std::vector<std::uint8_t>& data) {
    return parse_dmar(data.data(), data.size());
}

namespace detail {

std::uint16_t read_le16(const std::uint8_t* data, std::size_t offset) {
    return static_cast<std::uint16_t>(
        static_cast<std::uint16_t>(data[offset]) |
        (static_cast<std::uint16_t>(data[offset + 1U]) << 8U));
}

std::uint32_t read_le32(const std::uint8_t* data, std::size_t offset) {
    return static_cast<std::uint32_t>(data[offset]) |
           (static_cast<std::uint32_t>(data[offset + 1U]) << 8U) |
           (static_cast<std::uint32_t>(data[offset + 2U]) << 16U) |
           (static_cast<std::uint32_t>(data[offset + 3U]) << 24U);
}

std::uint64_t read_le64(const std::uint8_t* data, std::size_t offset) {
    std::uint64_t value = 0;
    for (std::size_t i = 0; i < 8U; ++i) {
        value |= static_cast<std::uint64_t>(data[offset + i]) << (8U * i);
    }
    return value;
}

std::uint64_t max_address_for_haw(std::uint8_t haw) noexcept {
    if (haw >= 63U) {
        return std::numeric_limits<std::uint64_t>::max();
    }
    return (1ULL << (static_cast<unsigned int>(haw) + 1U)) - 1ULL;
}

bool range_within_haw(std::uint64_t base, std::uint64_t size,
                      std::uint8_t haw) noexcept {
    const std::uint64_t max_address = max_address_for_haw(haw);
    if (base > max_address) {
        return false;
    }
    if (size == 0U) {
        return true;
    }
    return (size - 1U) <= (max_address - base);
}

bool is_page_aligned(std::uint64_t value) noexcept {
    return (value & 0xFFFULL) == 0U;
}

bool is_aligned_to(std::uint64_t value, std::uint64_t alignment) noexcept {
    return alignment != 0U && (value & (alignment - 1U)) == 0U;
}

std::string format_range(const AddressRange& range) {
    char buffer[64] = {};
    std::snprintf(buffer, sizeof(buffer), "[0x%llx, 0x%llx)",
                  static_cast<unsigned long long>(range.begin),
                  static_cast<unsigned long long>(range.end));
    return std::string(buffer);
}

Issue make_issue(IssueCode code, std::string message) {
    Issue issue;
    issue.code = code;
    issue.message = std::move(message);
    return issue;
}

void add_issue(std::vector<Issue>& issues, IssueCode code, std::string message) {
    issues.push_back(make_issue(code, std::move(message)));
}

std::optional<PciBdf> scope_target_bdf(const DmarDeviceScope& scope,
                                       std::uint16_t segment) {
    if (scope.path.empty()) {
        return std::nullopt;
    }
    const DmarPciPath& path = scope.path.back();
    return PciBdf{segment, scope.start_bus, path.device, path.function};
}

const PciDeviceInfo* find_device(const PciTopology& topology,
                                 const PciBdf& bdf) noexcept {
    for (const PciDeviceInfo& device : topology.devices) {
        if (device.bdf == bdf) {
            return &device;
        }
    }
    return nullptr;
}

bool device_is_under_bridge(const PciTopology& topology, const PciBdf& bdf,
                            const PciBdf& bridge) {
    const PciDeviceInfo* bridge_info = find_device(topology, bridge);
    if (bridge_info == nullptr || !bridge_info->is_bridge) {
        return false;
    }
    if (bdf == bridge) {
        return true;
    }
    if (bridge_info->secondary_bus <= bridge_info->subordinate_bus &&
        bdf.segment == bridge.segment && bdf.bus >= bridge_info->secondary_bus &&
        bdf.bus <= bridge_info->subordinate_bus) {
        return true;
    }

    const PciDeviceInfo* current = find_device(topology, bdf);
    for (std::size_t depth = 0; depth < topology.devices.size() + 1U; ++depth) {
        if (current == nullptr || !current->upstream_bridge.has_value()) {
            return false;
        }
        if (*current->upstream_bridge == bridge) {
            return true;
        }
        current = find_device(topology, *current->upstream_bridge);
    }
    return false;
}

bool scope_contains_bdf(const DmarDeviceScope& scope, std::uint16_t segment,
                        const PciBdf& bdf, const PciTopology* topology) {
    const std::optional<PciBdf> target = scope_target_bdf(scope, segment);
    if (!target.has_value()) {
        return false;
    }
    if (scope.kind == DmarScopeKind::kPciEndpoint) {
        return *target == bdf;
    }
    if (bdf == *target) {
        return true;
    }
    return topology != nullptr &&
           device_is_under_bridge(*topology, bdf, *target);
}

}  // namespace detail

}  // namespace iommu_guard
