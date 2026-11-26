#include "iommu_ats/iommu_ats.hpp"

#include <iostream>
#include <memory>

namespace {

void printSequence(const iommu_ats::SearchResult& result) {
    if (!result.found) {
        std::cout << "no violation found within depth 8\n";
        return;
    }
    std::cout << "shortest depth=" << result.depth << " sequence:\n  ";
    for (std::size_t i = 0; i < result.sequence.size(); ++i) {
        if (i != 0) {
            std::cout << " -> ";
        }
        std::cout << result.sequence[i];
    }
    std::cout << "\n";
}

} // namespace

int main() {
    using namespace iommu_ats;

    const Machine primed = canonical::makePrimedMachine();
    const std::vector<SearchAction> actions = canonical::makeActions();

    const SearchResult shortest =
        InterleavingSearcher::search(primed, actions, 8);
    std::cout << "[broad] ";
    printSequence(shortest);
    std::cout << "states explored=" << shortest.statesExplored << "\n";

    const SearchResult iotlb_only = InterleavingSearcher::search(
        canonical::makePrimedMachine(), canonical::makeIotlbOnlyActions(), 8);
    std::cout << "[iotlb-only faulty reclaimer] ";
    printSequence(iotlb_only);

    const SearchResult alias_omission =
        InterleavingSearcher::search(canonical::makePrimedMachine(),
                                     canonical::makePrimaryOnlyAliasActions(),
                                     8);
    std::cout << "[alias omission] ";
    printSequence(alias_omission);

    const SearchResult issued_wait = InterleavingSearcher::search(
        canonical::makePrimedMachine(),
        canonical::makeIssuedWaitAliasRaceActions(), 8);
    std::cout << "[issued-but-uncompleted wait + alias omission] ";
    printSequence(issued_wait);

    const SearchResult thread_race =
        InterleavingSearcher::searchInterleavings(
            canonical::makePrimedMachine(),
            canonical::makeIotlbRaceThreads(), 8);
    std::cout << "[thread interleaving] ";
    printSequence(thread_race);

    const ExhaustiveReport reference =
        exhaustivelyEnumerateReference(8);
    std::cout << "[reference] enumerated="
              << reference.sequencesEnumerated
              << " shortestDepth=" << reference.shortestDepth << "\n";

    Machine replay = canonical::makePrimedMachine();
    auto monitor = std::make_shared<OnlineSafetyInterceptor>();
    replay.events().subscribe(monitor);
    for (const std::string& name : shortest.sequence) {
        for (const SearchAction& action : actions) {
            if (action.name == name) {
                action.apply(replay);
                break;
            }
        }
    }
    std::cout << "[online monitor] violations="
              << monitor->violations().size() << "\n";

    const bool reference_replay =
        replayReferenceSequence(shortest.sequence);
    std::cout << "[reference replay] violation=" << std::boolalpha
              << reference_replay << "\n";

    return shortest.found && iotlb_only.found && alias_omission.found &&
                   issued_wait.found &&
                   thread_race.found &&
                   reference.found && reference_replay &&
                   monitor->sawViolation()
               ? 0
               : 1;
}
