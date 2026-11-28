#pragma once

#include "iommu_guard/iommu_guard.hpp"

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <string>
#include <vector>

namespace iommu_guard::detail {

std::uint16_t read_le16(const std::uint8_t* data, std::size_t offset);
std::uint32_t read_le32(const std::uint8_t* data, std::size_t offset);
std::uint64_t read_le64(const std::uint8_t* data, std::size_t offset);

std::uint64_t max_address_for_haw(std::uint8_t haw) noexcept;
bool range_within_haw(std::uint64_t base, std::uint64_t size,
                      std::uint8_t haw) noexcept;
bool is_page_aligned(std::uint64_t value) noexcept;
bool is_aligned_to(std::uint64_t value, std::uint64_t alignment) noexcept;
std::string format_range(const AddressRange& range);

Issue make_issue(IssueCode code, std::string message);
void add_issue(std::vector<Issue>& issues, IssueCode code, std::string message);

std::optional<PciBdf> scope_target_bdf(const DmarDeviceScope& scope,
                                       std::uint16_t segment);
const PciDeviceInfo* find_device(const PciTopology& topology,
                                 const PciBdf& bdf) noexcept;
bool scope_contains_bdf(const DmarDeviceScope& scope, std::uint16_t segment,
                        const PciBdf& bdf, const PciTopology* topology);
bool device_is_under_bridge(const PciTopology& topology, const PciBdf& bdf,
                            const PciBdf& bridge);

}  // namespace iommu_guard::detail
