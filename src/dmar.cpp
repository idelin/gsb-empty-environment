#include "internal.hpp"

#include <map>
#include <numeric>
#include <utility>

namespace iommu_guard {
namespace {

constexpr std::size_t kDmarHeaderSize = 48U;
constexpr std::size_t kStructureHeaderSize = 4U;
constexpr std::size_t kDeviceScopeHeaderSize = 6U;

void issue_at(std::vector<Issue>& issues, IssueCode code,
              std::uint64_t offset, std::string message) {
    Issue issue = detail::make_issue(code, std::move(message));
    issue.table_offset = offset;
    issues.push_back(std::move(issue));
}

bool parse_device_scopes(const std::uint8_t* data, std::size_t begin,
                         std::size_t end,
                         std::vector<DmarDeviceScope>& scopes,
                         std::vector<Issue>& issues) {
    std::size_t cursor = begin;
    bool all_valid = true;
    while (cursor < end) {
        if (end - cursor < kDeviceScopeHeaderSize) {
            issue_at(issues, IssueCode::kDmarTruncatedStructure, cursor,
                     "truncated device scope header");
            return false;
        }
        const std::uint8_t raw_kind = data[cursor];
        const std::uint8_t raw_length = data[cursor + 1U];
        const std::size_t scope_length = raw_length;
        if (scope_length < kDeviceScopeHeaderSize ||
            ((scope_length - kDeviceScopeHeaderSize) % 2U) != 0U) {
            issue_at(issues, IssueCode::kDmarInvalidDeviceScope, cursor,
                     "device scope length is smaller than its header or not "
                     "a whole number of PCI paths");
            all_valid = false;
            break;
        }
        if (scope_length > end - cursor) {
            issue_at(issues, IssueCode::kDmarTruncatedStructure, cursor,
                     "device scope extends beyond its parent structure");
            return false;
        }
        if (detail::read_le16(data, cursor + 2U) != 0U) {
            issue_at(issues, IssueCode::kDmarInvalidDeviceScope, cursor,
                     "device scope reserved field must be zero");
            all_valid = false;
        }

        bool scope_valid = true;
        DmarDeviceScope scope;
        scope.enumeration_id = data[cursor + 4U];
        scope.start_bus = data[cursor + 5U];
        const std::size_t path_count =
            (scope_length - kDeviceScopeHeaderSize) / 2U;

        if (raw_kind == 1U) {
            scope.kind = DmarScopeKind::kPciEndpoint;
        } else if (raw_kind == 2U) {
            scope.kind = DmarScopeKind::kPciSubHierarchy;
        } else if (raw_kind == 3U) {
            scope.kind = DmarScopeKind::kIoApic;
        } else if (raw_kind == 4U) {
            scope.kind = DmarScopeKind::kHpet;
        } else {
            issue_at(issues, IssueCode::kDmarInvalidDeviceScope, cursor,
                     "unsupported device scope type");
            all_valid = false;
            cursor += scope_length;
            continue;
        }

        if ((scope.kind == DmarScopeKind::kPciEndpoint && path_count != 1U) ||
            (scope.kind == DmarScopeKind::kPciSubHierarchy && path_count == 0U)) {
            issue_at(issues, IssueCode::kDmarInvalidPciPath, cursor,
                     "endpoint scopes require one path and bridge scopes "
                     "require at least one path");
            scope_valid = false;
        }

        scope.path.reserve(path_count);
        for (std::size_t index = 0; index < path_count; ++index) {
            const std::size_t path_offset =
                cursor + kDeviceScopeHeaderSize + index * 2U;
            const std::uint8_t device = data[path_offset];
            const std::uint8_t function = data[path_offset + 1U];
            if (device > 31U || function > 7U) {
                issue_at(issues, IssueCode::kDmarInvalidPciPath, path_offset,
                         "PCI path device/function is out of range");
                scope_valid = false;
                continue;
            }
            scope.path.push_back(DmarPciPath{device, function});
        }
        if (scope_valid) {
            scopes.push_back(std::move(scope));
        } else {
            all_valid = false;
        }
        cursor += scope_length;
    }
    return all_valid;
}

void collect_include_pci_all_conflicts(const DmarTable& table,
                                       std::vector<Issue>& issues) {
    struct SegmentUsage {
        std::size_t drhd_count = 0;
        std::size_t include_all_count = 0;
        std::size_t first_include_all_index = 0;
    };
    std::map<std::uint16_t, SegmentUsage> usage;
    for (std::size_t index = 0; index < table.drhds.size(); ++index) {
        const Drhd& drhd = table.drhds[index];
        SegmentUsage& item = usage[drhd.segment];
        item.drhd_count += 1U;
        if (drhd.include_pci_all) {
            if (item.include_all_count == 0U) {
                item.first_include_all_index = index;
            }
            item.include_all_count += 1U;
        }
    }

    for (const auto& entry : usage) {
        const SegmentUsage& item = entry.second;
        if (item.include_all_count > 1U) {
            const Drhd& first = table.drhds[item.first_include_all_index];
            issue_at(issues, IssueCode::kDmarDuplicateIncludePciAll,
                     first.table_offset,
                     "segment " + std::to_string(entry.first) +
                         " has more than one INCLUDE_PCI_ALL DRHD");
        }
        if (item.include_all_count == 1U && item.drhd_count > 1U) {
            const Drhd& first = table.drhds[item.first_include_all_index];
            issue_at(issues, IssueCode::kDmarIncludePciAllSegmentConflict,
                     first.table_offset,
                     "segment " + std::to_string(entry.first) +
                         " combines INCLUDE_PCI_ALL with another DRHD");
        }
    }
}

}  // namespace

DmarParseResult parse_dmar(const std::uint8_t* data, std::size_t size) {
    DmarParseResult result;
    if (data == nullptr || size < kDmarHeaderSize) {
        issue_at(result.issues, IssueCode::kDmarHeaderTooShort, 0U,
                 "input is null or shorter than the 48-byte DMAR header");
        result.ok = false;
        return result;
    }
    if (data[0] != 'D' || data[1] != 'M' || data[2] != 'A' || data[3] != 'R') {
        issue_at(result.issues, IssueCode::kDmarBadSignature, 0U,
                 "signature mismatch");
    }

    const std::uint32_t declared_length = detail::read_le32(data, 4U);
    result.table.length = declared_length;
    result.table.revision = data[8U];
    result.table.checksum = data[9U];
    result.table.host_address_width = data[36U];
    result.table.flags = data[37U];

    if (declared_length < kDmarHeaderSize ||
        static_cast<std::size_t>(declared_length) > size) {
        issue_at(result.issues, IssueCode::kDmarBadTableLength, 4U,
                 "declared length is shorter than the header or exceeds input");
        result.ok = false;
        return result;
    }

    std::uint8_t checksum = 0;
    for (std::size_t index = 0; index < declared_length; ++index) {
        checksum = static_cast<std::uint8_t>(
            checksum + static_cast<std::uint8_t>(data[index]));
    }
    if (checksum != 0U) {
        issue_at(result.issues, IssueCode::kDmarChecksumMismatch, 9U,
                 "table checksum does not sum to zero");
    }
    if (result.table.host_address_width == 0U ||
        result.table.host_address_width > 63U) {
        issue_at(result.issues, IssueCode::kDmarInvalidHaw, 36U,
                 "HAW must describe between 1 and 64 physical address bits");
    }

    std::size_t cursor = kDmarHeaderSize;
    const std::size_t table_end = static_cast<std::size_t>(declared_length);
    while (cursor < table_end) {
        if (table_end - cursor < kStructureHeaderSize) {
            issue_at(result.issues, IssueCode::kDmarTruncatedStructure, cursor,
                     "truncated remapping-structure header");
            break;
        }
        const std::uint16_t type = detail::read_le16(data, cursor);
        const std::uint16_t raw_length = detail::read_le16(data, cursor + 2U);
        const std::size_t structure_length = raw_length;
        if (structure_length < kStructureHeaderSize ||
            structure_length > table_end - cursor) {
            issue_at(result.issues, IssueCode::kDmarTruncatedStructure, cursor,
                     "remapping structure length is truncated");
            break;
        }

        const std::uint32_t structure_offset =
            static_cast<std::uint32_t>(cursor);
        switch (type) {
            case 0U: {
                if (structure_length < 16U) {
                    issue_at(result.issues,
                             IssueCode::kDmarInvalidStructureLength, cursor,
                             "DRHD is shorter than its 16-byte header");
                    break;
                }
                Drhd drhd;
                drhd.table_offset = structure_offset;
                drhd.include_pci_all = (data[cursor + 4U] & 1U) != 0U;
                if ((data[cursor + 4U] & static_cast<std::uint8_t>(~1U)) != 0U ||
                    data[cursor + 5U] != 0U) {
                    issue_at(result.issues,
                             IssueCode::kDmarInvalidStructureLength, cursor + 4U,
                             "DRHD flags contain reserved bits");
                }
                drhd.segment = detail::read_le16(data, cursor + 6U);
                drhd.register_base = detail::read_le64(data, cursor + 8U);
                if (!detail::is_page_aligned(drhd.register_base) ||
                    !detail::range_within_haw(
                        drhd.register_base, 1U,
                        result.table.host_address_width)) {
                    issue_at(result.issues, IssueCode::kDmarBadRegisterBase,
                             cursor + 8U,
                             "DRHD register base is not a valid 4KiB physical "
                             "address");
                }
                (void)parse_device_scopes(
                    data, cursor + 16U, cursor + structure_length,
                    drhd.scopes, result.issues);
                result.table.drhds.push_back(std::move(drhd));
                break;
            }
            case 1U: {
                if (structure_length < 24U) {
                    issue_at(result.issues,
                             IssueCode::kDmarInvalidStructureLength, cursor,
                             "RMRR is shorter than its 24-byte header");
                    break;
                }
                Rmrr rmrr;
                rmrr.table_offset = structure_offset;
                rmrr.segment = detail::read_le16(data, cursor + 6U);
                rmrr.base = detail::read_le64(data, cursor + 8U);
                rmrr.limit = detail::read_le64(data, cursor + 16U);
                if ((rmrr.base & 0xFFFULL) != 0U ||
                    (rmrr.limit & 0xFFFULL) != 0xFFFULL) {
                    issue_at(result.issues, IssueCode::kDmarBadRmrrAlignment,
                             cursor + 8U,
                             "RMRR base must be 4KiB aligned and limit must end "
                             "in 0xfff");
                }
                if (rmrr.limit < rmrr.base) {
                    issue_at(result.issues, IssueCode::kDmarBadRmrrLimit,
                             cursor + 16U, "RMRR limit is below base");
                }
                if (rmrr.limit == ~std::uint64_t{0}) {
                    issue_at(result.issues, IssueCode::kDmarRmrrLimitOverflow,
                             cursor + 16U,
                             "RMRR limit+1 wraps the 64-bit address space");
                }
                if (rmrr.limit >= rmrr.base &&
                    (!detail::range_within_haw(
                         rmrr.base, 1U, result.table.host_address_width) ||
                     !detail::range_within_haw(
                         rmrr.limit, 1U, result.table.host_address_width))) {
                    issue_at(result.issues, IssueCode::kDmarBadRmrrLimit,
                             cursor + 8U,
                             "RMRR exceeds the DMAR HAW physical address "
                             "range");
                }
                (void)parse_device_scopes(
                    data, cursor + 24U, cursor + structure_length,
                    rmrr.scopes, result.issues);
                if (rmrr.scopes.empty()) {
                    issue_at(result.issues, IssueCode::kDmarInvalidDeviceScope,
                             cursor + 24U,
                             "RMRR must name at least one device scope");
                }
                result.table.rmrrs.push_back(std::move(rmrr));
                break;
            }
            case 2U: {
                if (structure_length < 8U) {
                    issue_at(result.issues,
                             IssueCode::kDmarInvalidStructureLength, cursor,
                             "ATSR is shorter than its 8-byte header");
                    break;
                }
                Atsr atsr;
                atsr.table_offset = structure_offset;
                atsr.flags = data[cursor + 4U];
                if (data[cursor + 5U] != 0U) {
                    issue_at(result.issues, IssueCode::kDmarUnknownStructure,
                             cursor + 5U, "ATSR reserved byte is nonzero");
                }
                atsr.segment = detail::read_le16(data, cursor + 6U);
                (void)parse_device_scopes(
                    data, cursor + 8U, cursor + structure_length,
                    atsr.scopes, result.issues);
                if (atsr.scopes.empty()) {
                    issue_at(result.issues, IssueCode::kDmarInvalidDeviceScope,
                             cursor + 8U,
                             "ATSR must name at least one device scope");
                }
                result.table.atsrs.push_back(std::move(atsr));
                break;
            }
            case 3U: {
                if (structure_length < 20U) {
                    issue_at(result.issues,
                             IssueCode::kDmarInvalidStructureLength, cursor,
                             "RHSA is shorter than its 20-byte structure");
                    break;
                }
                Rhsa rhsa;
                rhsa.table_offset = structure_offset;
                if (detail::read_le32(data, cursor + 4U) != 0U) {
                    issue_at(result.issues, IssueCode::kDmarBadRhsa, cursor + 4U,
                             "RHSA reserved field is nonzero");
                }
                rhsa.base_address = detail::read_le64(data, cursor + 8U);
                rhsa.proximity_domain =
                    detail::read_le32(data, cursor + 16U);
                if (!detail::is_page_aligned(rhsa.base_address) ||
                    !detail::range_within_haw(
                        rhsa.base_address, 1U,
                        result.table.host_address_width)) {
                    issue_at(result.issues, IssueCode::kDmarBadRhsa, cursor + 8U,
                             "RHSA base is not a valid 4KiB physical address");
                }
                result.table.rhsas.push_back(rhsa);
                break;
            }
            default:
                issue_at(result.issues, IssueCode::kDmarUnknownStructure, cursor,
                         "unknown remapping structure type " +
                             std::to_string(type));
                break;
        }
        cursor += structure_length;
    }

    if (result.table.drhds.empty()) {
        issue_at(result.issues, IssueCode::kDmarMissingDrhd, kDmarHeaderSize,
                 "DMAR contains no DRHD");
    }
    collect_include_pci_all_conflicts(result.table, result.issues);

    result.ok = result.issues.empty();
    return result;
}

}  // namespace iommu_guard
