#pragma once

#include "iommu_ats/Machine.hpp"

#include <functional>
#include <optional>
#include <string>
#include <vector>

namespace iommu_ats {

struct ActionOutcome {
    bool securityGoal = false;
    std::optional<DmaResult> dma;
};

struct SearchAction {
    std::string name;
    std::function<ActionOutcome(Machine&)> apply;
};

struct ThreadProgram {
    std::string name;
    std::vector<SearchAction> steps;
};

struct SearchResult {
    bool found = false;
    uint32_t depth = 0;
    std::vector<std::string> sequence;
    std::size_t statesExplored = 0;
    std::optional<DmaResult> violation;
    std::string finalCanonicalState;
};

class InterleavingSearcher {
public:
    // BFS over meaningful machine transitions. maxDepth is capped at 8 by
    // requirement and clamped by the implementation as well.
    [[nodiscard]] static SearchResult search(
        const Machine& initial, const std::vector<SearchAction>& actions,
        uint32_t maxDepth = 8);

    // Enumerates true interleavings: every thread's program-counter order is
    // preserved while runnable threads may switch at each atomic step.
    [[nodiscard]] static SearchResult searchInterleavings(
        const Machine& initial, const std::vector<ThreadProgram>& threads,
        uint32_t maxDepth = 8);
};

using BoundedInterleavingSearcher = InterleavingSearcher;
using ConcurrencyInterleavingSearcher = InterleavingSearcher;

} // namespace iommu_ats
