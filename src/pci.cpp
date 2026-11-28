#include "internal.hpp"

#include <algorithm>
#include <set>
#include <tuple>
#include <utility>

namespace iommu_guard {
namespace {

PciOwnership resolve_without_duplicate_cleanup(const DmarTable& dmar,
                                               const PciTopology& topology,
                                               const PciBdf& bdf) {
    PciOwnership ownership;
    ownership.bdf = bdf;
    for (std::size_t index = 0; index < dmar.drhds.size(); ++index) {
        const Drhd& drhd = dmar.drhds[index];
        bool responsible = drhd.include_pci_all && drhd.segment == bdf.segment;
        for (const DmarDeviceScope& scope : drhd.scopes) {
            if (scope.kind != DmarScopeKind::kPciEndpoint &&
                scope.kind != DmarScopeKind::kPciSubHierarchy) {
                continue;
            }
            if (detail::scope_contains_bdf(scope, drhd.segment, bdf,
                                           &topology)) {
                responsible = true;
                break;
            }
        }
        if (responsible &&
            std::find(ownership.drhd_indices.begin(),
                      ownership.drhd_indices.end(),
                      index) == ownership.drhd_indices.end()) {
            ownership.drhd_indices.push_back(index);
        }
    }
    if (ownership.drhd_indices.size() == 1U) {
        ownership.unique_owner = ownership.drhd_indices.front();
    }
    return ownership;
}

void add_bdf_issue(std::vector<Issue>& issues, IssueCode code,
                   const PciBdf& bdf, std::string message) {
    Issue issue = detail::make_issue(code, std::move(message));
    issue.bdf = bdf;
    issues.push_back(std::move(issue));
}

std::uint8_t phantom_alias(std::uint8_t function, std::uint8_t mask,
                           std::uint8_t alias_bits) {
    const std::uint8_t effective_mask = static_cast<std::uint8_t>(mask & 0x7U);
    const std::uint8_t cleared = static_cast<std::uint8_t>(
        alias_bits & effective_mask);
    return static_cast<std::uint8_t>(
        (function & static_cast<std::uint8_t>(~effective_mask)) | cleared);
}

}  // namespace

PciOwnership resolve_pci_ownership(const DmarTable& dmar,
                                   const PciTopology& topology,
                                   const PciBdf& bdf) {
    return resolve_without_duplicate_cleanup(dmar, topology, bdf);
}

PciTopologyAudit audit_pci_topology(const DmarTable& dmar,
                                    const PciTopology& topology) {
    PciTopologyAudit report;

    std::set<std::tuple<std::uint16_t, std::uint8_t, std::uint8_t, std::uint8_t>>
        seen_bdfs;
    for (const PciDeviceInfo& device : topology.devices) {
        const auto key = std::make_tuple(device.bdf.segment, device.bdf.bus,
                                         device.bdf.device, device.bdf.function);
        if (!seen_bdfs.insert(key).second) {
            add_bdf_issue(report.issues, IssueCode::kInvalidArgument, device.bdf,
                          "duplicate PCI device metadata");
        }
        PciOwnership ownership =
            resolve_without_duplicate_cleanup(dmar, topology, device.bdf);
        if (ownership.drhd_indices.empty()) {
            add_bdf_issue(report.issues, IssueCode::kPciNoOwner, device.bdf,
                          "no DRHD is responsible for " +
                              format_bdf(device.bdf));
        } else if (ownership.drhd_indices.size() > 1U) {
            add_bdf_issue(report.issues, IssueCode::kPciMultipleOwners,
                          device.bdf,
                          "multiple DRHDs claim " + format_bdf(device.bdf));
        }
        report.ownership.push_back(std::move(ownership));
    }

    for (std::size_t drhd_index = 0; drhd_index < dmar.drhds.size();
         ++drhd_index) {
        const Drhd& drhd = dmar.drhds[drhd_index];
        for (const DmarDeviceScope& scope : drhd.scopes) {
            if (scope.kind != DmarScopeKind::kPciSubHierarchy) {
                continue;
            }
            const std::optional<PciBdf> bridge_bdf =
                detail::scope_target_bdf(scope, drhd.segment);
            if (!bridge_bdf.has_value()) {
                continue;
            }
            const PciDeviceInfo* bridge =
                detail::find_device(topology, *bridge_bdf);
            if (bridge == nullptr || !bridge->is_bridge) {
                add_bdf_issue(report.issues,
                              IssueCode::kPciBridgeMetadataMissing, *bridge_bdf,
                              "bridge scope target has no bridge metadata");
                continue;
            }

            const PciOwnership bridge_ownership =
                resolve_without_duplicate_cleanup(dmar, topology, *bridge_bdf);
            if (std::find(bridge_ownership.drhd_indices.begin(),
                          bridge_ownership.drhd_indices.end(),
                          drhd_index) ==
                bridge_ownership.drhd_indices.end()) {
                add_bdf_issue(report.issues, IssueCode::kPciBridgeScopeConflict,
                              *bridge_bdf,
                              "upstream bridge is owned by another DRHD");
            }

            for (const PciDeviceInfo& downstream : topology.devices) {
                if (downstream.bdf == *bridge_bdf ||
                    !detail::device_is_under_bridge(topology, downstream.bdf,
                                                    *bridge_bdf)) {
                    continue;
                }
                const PciOwnership downstream_ownership =
                    resolve_without_duplicate_cleanup(dmar, topology,
                                                      downstream.bdf);
                if (std::find(downstream_ownership.drhd_indices.begin(),
                              downstream_ownership.drhd_indices.end(),
                              drhd_index) ==
                    downstream_ownership.drhd_indices.end()) {
                    add_bdf_issue(
                        report.issues, IssueCode::kPciBridgeScopeConflict,
                        downstream.bdf,
                        "downstream device escapes its upstream bridge scope");
                }
                if (downstream.security_domain != bridge->security_domain) {
                    add_bdf_issue(report.issues, IssueCode::kPciBridgeScopeConflict,
                                  downstream.bdf,
                                  "downstream device crosses bridge security "
                                  "domain");
                }
            }
        }
    }

    std::set<std::tuple<std::uint16_t, std::uint8_t, std::uint8_t,
                        std::uint8_t, std::uint16_t, std::uint8_t,
                        std::uint8_t, std::uint8_t>>
        reported_aliases;
    for (const PciDeviceInfo& device : topology.devices) {
        if (device.phantom_alias_mask == 0U) {
            continue;
        }
        const PciOwnership original =
            resolve_without_duplicate_cleanup(dmar, topology, device.bdf);
        for (std::uint8_t bits = 0U; bits < 8U; ++bits) {
            const std::uint8_t function =
                phantom_alias(device.bdf.function, device.phantom_alias_mask,
                              bits);
            if (function == device.bdf.function) {
                continue;
            }
            PciBdf alias = device.bdf;
            alias.function = function;
            const PciOwnership alias_ownership =
                resolve_without_duplicate_cleanup(dmar, topology, alias);
            const bool owner_conflict =
                !alias_ownership.drhd_indices.empty() &&
                !std::equal(alias_ownership.drhd_indices.begin(),
                            alias_ownership.drhd_indices.end(),
                            original.drhd_indices.begin(),
                            original.drhd_indices.end());
            const PciDeviceInfo* alias_device =
                detail::find_device(topology, alias);
            const bool domain_conflict =
                alias_device != nullptr &&
                alias_device->security_domain != device.security_domain;
            if (!owner_conflict && !domain_conflict) {
                continue;
            }
            const auto key = std::make_tuple(
                device.bdf.segment, device.bdf.bus, device.bdf.device,
                device.bdf.function, alias.segment, alias.bus, alias.device,
                alias.function);
            if (reported_aliases.insert(key).second) {
                add_bdf_issue(report.issues, IssueCode::kPhantomAliasConflict,
                              device.bdf,
                              "phantom alias " + format_bdf(alias) +
                                  " resolves outside the isolation domain");
            }
        }
    }

    report.ok = report.issues.empty();
    return report;
}

}  // namespace iommu_guard
