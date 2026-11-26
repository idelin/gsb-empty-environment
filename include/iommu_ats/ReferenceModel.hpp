#pragma once

#include <cstdint>
#include <optional>
#include <string>
#include <vector>

namespace iommu_ats {

// Tiny independent oracle: one IOVA page, one host frame, one main BDF and
// one alias BDF. It deliberately uses abstract cache-valid bits rather than
// the production containers so exhaustive enumeration checks a second model.
struct ReferenceState {
    bool pageTableMapped = true;
    bool iotlbValid = true;
    bool atcMainValid = true;
    bool atcAliasValid = true;
    bool waitPassed = false;
    bool frameIsKernel = false;
    bool illegalWriteOccurred = false;

    [[nodiscard]] std::uint8_t bits() const noexcept;
    static ReferenceState fromBits(std::uint8_t bits) noexcept;
};

enum class ReferenceAction {
    Unmap = 0,
    InvalidateIotlb,
    InvalidateMainAtc,
    InvalidateAliasAtc,
    Wait,
    ReallocateToKernel,
    DmaFromMain,
    DmaFromAlias,
    ActionCount,
};

[[nodiscard]] const char* actionName(ReferenceAction action) noexcept;
[[nodiscard]] std::optional<ReferenceAction> actionFromName(
    const std::string& name);

struct ReferenceStepResult {
    bool accessGranted = false;
    bool violation = false;
    const char* usedCacheLayer = "none";
};

ReferenceStepResult stepReference(ReferenceState& state,
                                  ReferenceAction action);

struct ExhaustiveReport {
    bool found = false;
    std::uint32_t shortestDepth = 9;
    std::vector<std::string> shortestSequence;
    std::uint64_t sequencesEnumerated = 0;
};

ExhaustiveReport exhaustivelyEnumerateReference(
    std::uint32_t maxDepth = 8);
bool replayReferenceSequence(const std::vector<std::string>& actionNames);

} // namespace iommu_ats
