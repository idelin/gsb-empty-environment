#include "internal.hpp"

#include <algorithm>
#include <map>
#include <optional>
#include <tuple>
#include <utility>
#include <vector>

namespace iommu_guard {
namespace {

void add_audit_issue(std::vector<Issue>& issues, IssueCode code,
                     const DeviceAssignment& assignment,
                     const TranslationWalk& walk, std::string message,
                     std::optional<AddressRange> gpa = std::nullopt,
                     std::optional<AddressRange> hpa = std::nullopt,
                     std::vector<PhysicalAddress> entries = {}) {
    Issue issue = detail::make_issue(code, std::move(message));
    issue.bdf = assignment.bdf;
    issue.pasid = assignment.pasid;
    issue.did = walk.did;
    issue.gpa = gpa;
    issue.hpa = hpa;
    issue.entry_phys = std::move(entries);
    issues.push_back(std::move(issue));
}

AddressRange gpa_for_hpa_conflict(const SlptMapping& mapping,
                                  const AddressRange& hpa_conflict) {
    const std::uint64_t offset = hpa_conflict.begin - mapping.hpa_begin;
    return AddressRange{mapping.gpa.begin + offset,
                        mapping.gpa.begin + offset + hpa_conflict.size()};
}

std::optional<AddressRange> first_identity_gap(
    std::vector<SlptMapping> mappings, const AddressRange& needed) {
    std::sort(mappings.begin(), mappings.end(),
              [](const SlptMapping& lhs, const SlptMapping& rhs) {
                  return lhs.gpa < rhs.gpa;
              });
    std::uint64_t cursor = needed.begin;
    for (const SlptMapping& mapping : mappings) {
        if (mapping.gpa.end <= cursor) {
            continue;
        }
        if (mapping.gpa.begin > cursor) {
            return AddressRange{cursor,
                                std::min(mapping.gpa.begin, needed.end)};
        }
        const std::uint64_t delta = cursor - mapping.gpa.begin;
        if (mapping.hpa_begin + delta != cursor) {
            return AddressRange{cursor,
                                std::min(mapping.gpa.end, needed.end)};
        }
        cursor = std::min(mapping.gpa.end, needed.end);
        if (cursor >= needed.end) {
            return std::nullopt;
        }
    }
    return AddressRange{cursor, needed.end};
}

bool rmrr_applies(const Rmrr& rmrr, const PciBdf& bdf,
                  const PciTopology& topology) {
    if (rmrr.segment != bdf.segment) {
        return false;
    }
    for (const DmarDeviceScope& scope : rmrr.scopes) {
        if ((scope.kind == DmarScopeKind::kPciEndpoint ||
             scope.kind == DmarScopeKind::kPciSubHierarchy) &&
            detail::scope_contains_bdf(scope, rmrr.segment, bdf, &topology)) {
            return true;
        }
    }
    return false;
}

bool has_bdf_in_topology(const PciTopology& topology, const PciBdf& bdf) {
    return detail::find_device(topology, bdf) != nullptr;
}

void append_assignment_ownership_issues(
    std::vector<Issue>& issues, const PciTopology& topology,
    const DeviceAssignment& assignment, const PciOwnership& ownership) {
    if (has_bdf_in_topology(topology, assignment.bdf)) {
        return;
    }
    if (ownership.drhd_indices.empty()) {
        Issue issue = detail::make_issue(
            IssueCode::kPciNoOwner,
            "assignment device has no responsible DRHD");
        issue.bdf = assignment.bdf;
        issues.push_back(std::move(issue));
    } else if (ownership.drhd_indices.size() > 1U) {
        Issue issue = detail::make_issue(
            IssueCode::kPciMultipleOwners,
            "assignment device has multiple responsible DRHDs");
        issue.bdf = assignment.bdf;
        issues.push_back(std::move(issue));
    }
}

struct DidUse {
    SecurityDomainId security_domain = 0;
    PciBdf bdf;
    std::optional<std::uint32_t> pasid;
    std::vector<PhysicalAddress> entries;
};

}  // namespace

std::vector<DeviceAssignment> assignments_from_topology(
    const PciTopology& topology, bool scalable_mode) {
    std::vector<DeviceAssignment> assignments;
    for (const PciDeviceInfo& device : topology.devices) {
        if (scalable_mode && !device.pasids.empty()) {
            for (const std::uint32_t pasid : device.pasids) {
                DeviceAssignment assignment;
                assignment.bdf = device.bdf;
                assignment.security_domain = device.security_domain;
                assignment.dma_remapping_enabled =
                    device.dma_remapping_enabled;
                assignment.pasid = pasid;
                assignments.push_back(assignment);
            }
        } else {
            DeviceAssignment assignment;
            assignment.bdf = device.bdf;
            assignment.security_domain = device.security_domain;
            assignment.dma_remapping_enabled = device.dma_remapping_enabled;
            assignments.push_back(assignment);
        }
    }
    return assignments;
}

AuditReport audit_isolation(
    const DmarTable& dmar, const PciTopology& topology,
    const TranslationRoot& translation_root, const MemoryRead& read,
    const IsolationPolicy& policy,
    const std::vector<DeviceAssignment>& assignments,
    const WalkOptions& options) {
    AuditReport report;
    const PciTopologyAudit topology_audit =
        audit_pci_topology(dmar, topology);
    report.issues.insert(report.issues.end(), topology_audit.issues.begin(),
                         topology_audit.issues.end());

    std::vector<AddressRange> protected_ranges;
    protected_ranges.reserve(policy.protected_memory.size());
    for (const ProtectedRange& region : policy.protected_memory) {
        if (region.range.valid()) {
            protected_ranges.push_back(region.range);
        }
    }
    const std::vector<AddressRange> protected_merged =
        merge_ranges(std::move(protected_ranges));

    std::map<DomainId, DidUse> did_owners;

    for (const DeviceAssignment& assignment : assignments) {
        DeviceAuditResult device_result;
        device_result.assignment = assignment;
        device_result.ownership =
            resolve_pci_ownership(dmar, topology, assignment.bdf);
        append_assignment_ownership_issues(report.issues, topology, assignment,
                                           device_result.ownership);

        if (!assignment.dma_remapping_enabled) {
            Issue issue = detail::make_issue(
                IssueCode::kDmaRemappingDisabled,
                "assignments marks DMA remapping disabled for " +
                    format_bdf(assignment.bdf));
            issue.bdf = assignment.bdf;
            issue.pasid = assignment.pasid;
            report.issues.push_back(std::move(issue));
        }

        const TranslationWalk walk = walk_device_translation(
            translation_root, assignment.bdf, assignment.pasid, read, options);
        device_result.walk = walk;
        report.issues.insert(report.issues.end(), walk.issues.begin(),
                             walk.issues.end());

        for (const SlptMapping& mapping : walk.mappings) {
            const AddressRange hpa_range{
                mapping.hpa_begin,
                mapping.hpa_begin + mapping.page_size};
            for (const AddressRange& protected_range : protected_merged) {
                const std::optional<AddressRange> conflict =
                    intersect_ranges(hpa_range, protected_range);
                if (!conflict.has_value()) {
                    continue;
                }
                const AddressRange gpa_conflict =
                    gpa_for_hpa_conflict(mapping, *conflict);
                add_audit_issue(
                    report.issues, IssueCode::kMappingInProtectedMemory,
                    assignment, walk,
                    "SLPT mapping intersects protected physical memory",
                    gpa_conflict, *conflict, mapping.walk_entry_phys);
                device_result.gpa_conflicts.push_back(gpa_conflict);
                device_result.hpa_conflicts.push_back(*conflict);
            }

            for (const SensitiveMmioRange& mmio : policy.sensitive_mmio) {
                if (!mmio.range.valid()) {
                    continue;
                }
                const std::optional<AddressRange> conflict =
                    intersect_ranges(hpa_range, mmio.range);
                if (!conflict.has_value()) {
                    continue;
                }
                const bool authorized =
                    std::find(mmio.allowed_security_domains.begin(),
                              mmio.allowed_security_domains.end(),
                              assignment.security_domain) !=
                    mmio.allowed_security_domains.end();
                if (authorized) {
                    continue;
                }
                const AddressRange gpa_conflict =
                    gpa_for_hpa_conflict(mapping, *conflict);
                add_audit_issue(
                    report.issues, IssueCode::kMappingInUnauthorizedMmio,
                    assignment, walk,
                    "SLPT mapping targets sensitive MMIO not authorized for "
                    "security domain " +
                        std::to_string(assignment.security_domain) +
                        " (" + mmio.label + ")",
                    gpa_conflict, *conflict, mapping.walk_entry_phys);
                device_result.gpa_conflicts.push_back(gpa_conflict);
                device_result.hpa_conflicts.push_back(*conflict);
            }
        }

        for (const Rmrr& rmrr : dmar.rmrrs) {
            if (!rmrr_applies(rmrr, assignment.bdf, topology)) {
                continue;
            }
            const AddressRange range = rmrr.range();
            if (!range.valid()) {
                continue;
            }
            device_result.rmrr_regions.push_back(range);
            for (const AddressRange& protected_range : protected_merged) {
                const std::optional<AddressRange> conflict =
                    intersect_ranges(range, protected_range);
                if (!conflict.has_value()) {
                    continue;
                }
                Issue issue = detail::make_issue(
                    IssueCode::kRmrrOverlapsProtectedMemory,
                    "RMRR intersects host protected memory");
                issue.table_offset = rmrr.table_offset;
                issue.bdf = assignment.bdf;
                issue.pasid = assignment.pasid;
                issue.did = walk.did;
                issue.hpa = conflict;
                report.issues.push_back(std::move(issue));
            }

            const std::optional<AddressRange> gap =
                first_identity_gap(walk.mappings, range);
            if (gap.has_value()) {
                add_audit_issue(
                    report.issues, IssueCode::kRmrrIdentityMappingMissing,
                    assignment, walk,
                    "RMRR has no strict identity mapping over " +
                        detail::format_range(*gap),
                    *gap, *gap);
            }
        }

        if (walk.did.has_value()) {
            const DomainId did = *walk.did;
            const auto existing = did_owners.find(did);
            if (existing == did_owners.end()) {
                DidUse use;
                use.security_domain = assignment.security_domain;
                use.bdf = assignment.bdf;
                use.pasid = assignment.pasid;
                if (walk.context_entry_phys.has_value()) {
                    use.entries.push_back(*walk.context_entry_phys);
                }
                did_owners.emplace(did, std::move(use));
            } else if (existing->second.security_domain !=
                       assignment.security_domain) {
                std::vector<PhysicalAddress> entries = existing->second.entries;
                if (walk.context_entry_phys.has_value()) {
                    entries.push_back(*walk.context_entry_phys);
                }
                Issue issue = detail::make_issue(
                    IssueCode::kDidSharedAcrossSecurityDomains,
                    "DID " + std::to_string(did) + " is shared by " +
                        format_bdf(existing->second.bdf) + " (domain " +
                        std::to_string(existing->second.security_domain) +
                        ") and " + format_bdf(assignment.bdf) + " (domain " +
                        std::to_string(assignment.security_domain) + ")");
                issue.bdf = assignment.bdf;
                issue.pasid = assignment.pasid;
                issue.did = did;
                issue.entry_phys = std::move(entries);
                report.issues.push_back(std::move(issue));
            }
        }

        report.devices.push_back(std::move(device_result));
    }

    report.ok = report.issues.empty();
    return report;
}

}  // namespace iommu_guard
