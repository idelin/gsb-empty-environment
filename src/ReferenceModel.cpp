#include "iommu_ats/ReferenceModel.hpp"

#include <algorithm>
#include <array>

namespace iommu_ats {

std::uint8_t ReferenceState::bits() const noexcept {
    std::uint8_t value = 0;
    value |= static_cast<std::uint8_t>(pageTableMapped ? 1U << 0U : 0U);
    value |= static_cast<std::uint8_t>(iotlbValid ? 1U << 1U : 0U);
    value |= static_cast<std::uint8_t>(atcMainValid ? 1U << 2U : 0U);
    value |= static_cast<std::uint8_t>(atcAliasValid ? 1U << 3U : 0U);
    value |= static_cast<std::uint8_t>(waitPassed ? 1U << 4U : 0U);
    value |= static_cast<std::uint8_t>(frameIsKernel ? 1U << 5U : 0U);
    value |= static_cast<std::uint8_t>(illegalWriteOccurred ? 1U << 6U : 0U);
    return value;
}

ReferenceState ReferenceState::fromBits(std::uint8_t bits) noexcept {
    ReferenceState state;
    state.pageTableMapped = (bits & (1U << 0U)) != 0;
    state.iotlbValid = (bits & (1U << 1U)) != 0;
    state.atcMainValid = (bits & (1U << 2U)) != 0;
    state.atcAliasValid = (bits & (1U << 3U)) != 0;
    state.waitPassed = (bits & (1U << 4U)) != 0;
    state.frameIsKernel = (bits & (1U << 5U)) != 0;
    state.illegalWriteOccurred = (bits & (1U << 6U)) != 0;
    return state;
}

const char* actionName(ReferenceAction action) noexcept {
    switch (action) {
    case ReferenceAction::Unmap:
        return "Unmap";
    case ReferenceAction::InvalidateIotlb:
        return "InvalidateIOTLB";
    case ReferenceAction::InvalidateMainAtc:
        return "InvalidateMainATC";
    case ReferenceAction::InvalidateAliasAtc:
        return "InvalidateAliasATC";
    case ReferenceAction::Wait:
        return "InvalidationWait";
    case ReferenceAction::ReallocateToKernel:
        return "ReallocateKernel";
    case ReferenceAction::DmaFromMain:
        return "DmaMain";
    case ReferenceAction::DmaFromAlias:
        return "DmaAlias";
    case ReferenceAction::ActionCount:
        break;
    }
    return "Unknown";
}

std::optional<ReferenceAction> actionFromName(const std::string& name) {
    for (int i = 0; i < static_cast<int>(ReferenceAction::ActionCount); ++i) {
        const auto action = static_cast<ReferenceAction>(i);
        if (name == actionName(action)) {
            return action;
        }
    }
    return std::nullopt;
}

ReferenceStepResult stepReference(ReferenceState& state,
                                  ReferenceAction action) {
    ReferenceStepResult result;

    switch (action) {
    case ReferenceAction::Unmap:
        state.pageTableMapped = false;
        return result;

    case ReferenceAction::InvalidateIotlb:
        state.iotlbValid = false;
        return result;

    case ReferenceAction::InvalidateMainAtc:
        state.atcMainValid = false;
        return result;

    case ReferenceAction::InvalidateAliasAtc:
        state.atcAliasValid = false;
        return result;

    case ReferenceAction::Wait:
        if (!state.iotlbValid && !state.atcMainValid &&
            !state.atcAliasValid) {
            state.waitPassed = true;
        }
        return result;

    case ReferenceAction::ReallocateToKernel:
        // The search's raw reclamation action is only reachable after Unmap
        // opens a ticket. Keeping that precondition makes the oracle match.
        if (!state.pageTableMapped) {
            state.frameIsKernel = true;
        }
        return result;

    case ReferenceAction::DmaFromMain:
    case ReferenceAction::DmaFromAlias: {
        const bool main = action == ReferenceAction::DmaFromMain;
        bool& atc_valid = main ? state.atcMainValid : state.atcAliasValid;

        if (atc_valid) {
            result.accessGranted = true;
            result.usedCacheLayer = "ATC";
        } else if (state.iotlbValid) {
            result.accessGranted = true;
            result.usedCacheLayer = "IOTLB";
            atc_valid = true;
        } else if (state.pageTableMapped) {
            result.accessGranted = true;
            result.usedCacheLayer = "PT";
            state.iotlbValid = true;
            atc_valid = true;
        }

        if (result.accessGranted && state.frameIsKernel) {
            result.violation = true;
            state.illegalWriteOccurred = true;
        }
        return result;
    }

    case ReferenceAction::ActionCount:
        break;
    }

    return result;
}

namespace {

void enumerateDfs(ReferenceState& state, std::uint32_t depth,
                  std::uint32_t maxDepth, std::vector<std::string>& path,
                  ExhaustiveReport& report) {
    if (depth >= maxDepth) {
        return;
    }

    for (int raw_action = 0;
         raw_action < static_cast<int>(ReferenceAction::ActionCount);
         ++raw_action) {
        const auto action = static_cast<ReferenceAction>(raw_action);
        ReferenceState next = state;
        const ReferenceStepResult step = stepReference(next, action);
        path.push_back(actionName(action));
        ++report.sequencesEnumerated;

        if (step.violation &&
            static_cast<std::uint32_t>(path.size()) <
                report.shortestDepth) {
            report.found = true;
            report.shortestDepth = static_cast<std::uint32_t>(path.size());
            report.shortestSequence = path;
        }

        enumerateDfs(next, depth + 1U, maxDepth, path, report);
        path.pop_back();
    }
}

} // namespace

ExhaustiveReport exhaustivelyEnumerateReference(std::uint32_t maxDepth) {
    maxDepth = std::min(maxDepth, 8U);
    ExhaustiveReport report;
    report.shortestDepth = 9;
    ReferenceState initial;
    std::vector<std::string> path;
    enumerateDfs(initial, 0, maxDepth, path, report);
    return report;
}

bool replayReferenceSequence(const std::vector<std::string>& actionNames) {
    ReferenceState state;
    bool saw_violation = false;
    for (const std::string& name : actionNames) {
        const auto action = actionFromName(name);
        if (!action) {
            return false;
        }
        const ReferenceStepResult result = stepReference(state, *action);
        saw_violation = saw_violation || result.violation;
    }
    return saw_violation;
}

} // namespace iommu_ats
