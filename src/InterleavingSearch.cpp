#include "iommu_ats/InterleavingSearch.hpp"

#include <algorithm>
#include <deque>
#include <unordered_set>
#include <utility>

namespace iommu_ats {
namespace {

struct SearchNode {
    Machine machine;
    std::vector<std::string> path;
    std::uint32_t depth = 0;
};

struct InterleavingNode {
    Machine machine;
    std::vector<std::size_t> programCounters;
    std::vector<std::string> path;
    std::uint32_t depth = 0;
};

std::string interleavingKey(
    const Machine& machine,
    const std::vector<std::size_t>& programCounters) {
    std::string key = machine.canonicalState();
    key += "|pcs=";
    for (const std::size_t pc : programCounters) {
        key += std::to_string(pc);
        key.push_back(',');
    }
    return key;
}

} // namespace

SearchResult InterleavingSearcher::search(
    const Machine& initial, const std::vector<SearchAction>& actions,
    uint32_t maxDepth) {
    maxDepth = std::min(maxDepth, 8U);

    SearchResult result;
    std::deque<SearchNode> queue;
    std::unordered_set<std::string> visited;

    SearchNode root;
    root.machine = initial;
    root.depth = 0;
    visited.insert(root.machine.canonicalState());
    queue.push_back(std::move(root));

    while (!queue.empty()) {
        SearchNode node = std::move(queue.front());
        queue.pop_front();
        ++result.statesExplored;

        if (node.depth >= maxDepth) {
            continue;
        }

        for (const SearchAction& action : actions) {
            Machine next = node.machine;
            const std::string before = next.canonicalState();
            ActionOutcome outcome;
            if (action.apply) {
                outcome = action.apply(next);
            }
            const std::string after = next.canonicalState();

            std::vector<std::string> childPath = node.path;
            childPath.push_back(action.name);

            if (outcome.securityGoal) {
                result.found = true;
                result.depth = static_cast<uint32_t>(childPath.size());
                result.sequence = std::move(childPath);
                result.violation = outcome.dma;
                result.finalCanonicalState = after;
                return result;
            }

            if (after == before) {
                continue;
            }
            if (!visited.insert(after).second) {
                continue;
            }

            if (static_cast<uint32_t>(node.depth + 1U) < maxDepth) {
                SearchNode child;
                child.machine = std::move(next);
                child.path = std::move(childPath);
                child.depth = node.depth + 1U;
                queue.push_back(std::move(child));
            }
        }
    }

    result.found = false;
    return result;
}

SearchResult InterleavingSearcher::searchInterleavings(
    const Machine& initial, const std::vector<ThreadProgram>& threads,
    uint32_t maxDepth) {
    maxDepth = std::min(maxDepth, 8U);
    SearchResult result;

    std::vector<std::size_t> initial_pcs(threads.size(), 0);
    std::deque<InterleavingNode> queue;
    std::unordered_set<std::string> visited;

    InterleavingNode root;
    root.machine = initial;
    root.programCounters = initial_pcs;
    visited.insert(interleavingKey(root.machine, root.programCounters));
    queue.push_back(std::move(root));

    while (!queue.empty()) {
        InterleavingNode node = std::move(queue.front());
        queue.pop_front();
        ++result.statesExplored;

        if (node.depth >= maxDepth) {
            continue;
        }

        for (std::size_t thread_index = 0;
             thread_index < threads.size(); ++thread_index) {
            const ThreadProgram& thread = threads[thread_index];
            const std::size_t pc = node.programCounters[thread_index];
            if (pc >= thread.steps.size()) {
                continue;
            }

            const SearchAction& action = thread.steps[pc];
            InterleavingNode child;
            child.machine = node.machine;
            child.programCounters = node.programCounters;
            child.path = node.path;
            child.depth = node.depth + 1U;

            ActionOutcome outcome;
            if (action.apply) {
                outcome = action.apply(child.machine);
            }
            child.programCounters[thread_index] = pc + 1U;
            child.path.push_back(action.name);

            if (outcome.securityGoal) {
                result.found = true;
                result.depth = child.depth;
                result.sequence = child.path;
                result.violation = outcome.dma;
                result.finalCanonicalState =
                    child.machine.canonicalState();
                return result;
            }

            const std::string key = interleavingKey(
                child.machine, child.programCounters);
            if (!visited.insert(key).second) {
                continue;
            }

            if (child.depth < maxDepth) {
                queue.push_back(std::move(child));
            }
        }
    }

    return result;
}

} // namespace iommu_ats
