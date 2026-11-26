#include "iommu_ats/iommu_ats.hpp"

#include <iostream>
#include <memory>
#include <string>

using namespace iommu_ats;

namespace {

int g_failures = 0;
int g_checks = 0;

void expect(bool condition, const std::string& expression,
            const char* file, int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "CHECK failed: " << expression << " (" << file << ':'
                  << line << ")\n";
    }
}

#define EXPECT_TRUE(expression)                                              \
    expect(static_cast<bool>(expression), #expression, __FILE__, __LINE__)
#define EXPECT_FALSE(expression)                                             \
    expect(!static_cast<bool>(expression), #expression, __FILE__, __LINE__)

void testBdfAndAliases() {
    const auto parsed = Bdf::parse("01:00.7");
    EXPECT_TRUE(parsed.has_value());
    if (parsed) {
        EXPECT_TRUE(parsed->bus == 0x01);
        EXPECT_TRUE(parsed->device == 0);
        EXPECT_TRUE(parsed->function == 7);
        EXPECT_TRUE(parsed->toString() == "01:00.7");
    }
    EXPECT_FALSE(Bdf::parse("gg:00.0").has_value());

    Machine machine = canonical::makePrimedMachine();
    const Bdf main = canonical::mainBdf();
    const Bdf alias = canonical::aliasBdf();
    EXPECT_TRUE(machine.topology().areAliases(main, alias));
    EXPECT_TRUE(machine.pasidTable().lookup(alias, canonical::PASID) ==
                canonical::GUEST_DOMAIN_ID);
}

void testPageTableLargePagesAndPermissions() {
    PageTable table;
    EXPECT_TRUE(table.rootPhysicalAddressValid());

    EXPECT_TRUE(table.map(0x10000, 0x100000, PAGE_SIZE_4K,
                          AccessPermissions::readWrite()));
    const auto walk = table.walk(0x10000);
    EXPECT_TRUE(walk.size() == 4);
    if (walk.size() == 4) {
        EXPECT_TRUE(walk[0].level == 4);
        EXPECT_TRUE(walk[3].leaf);
        EXPECT_TRUE(walk[3].physicalAddressValid);
        EXPECT_TRUE(walk[3].referencedPhysicalAddress == 0x100000);
    }
    auto small = table.translate(0x10123);
    EXPECT_TRUE(small.has_value());
    if (small) {
        EXPECT_TRUE(small->physicalAt(0x10123) == 0x100123);
        EXPECT_TRUE(small->pageSize == PAGE_SIZE_4K);
    }

    EXPECT_TRUE(table.map(0x200000, 0x400000, PAGE_SIZE_2M,
                          AccessPermissions::readOnly()));
    auto two_m = table.translate(0x201234);
    EXPECT_TRUE(two_m.has_value());
    if (two_m) {
        EXPECT_TRUE(two_m->pageSize == PAGE_SIZE_2M);
        EXPECT_TRUE(two_m->physicalAt(0x201234) == 0x401234);
    }
    RangeTranslation write_denied =
        table.translateRange(0x201000, 4, DmaOp::Write);
    EXPECT_FALSE(write_denied.permitted);
    EXPECT_TRUE(table.translateRange(0x201000, 4, DmaOp::Read).permitted);

    const Iova gb_iova = 0x40000000ULL;
    const PhysicalAddress gb_pa = 0x80000000ULL;
    EXPECT_TRUE(table.map(gb_iova, gb_pa, PAGE_SIZE_1G,
                          AccessPermissions::readExecute()));
    auto one_g = table.translate(gb_iova + 0x12345678ULL);
    EXPECT_TRUE(one_g.has_value());
    if (one_g) {
        EXPECT_TRUE(one_g->pageSize == PAGE_SIZE_1G);
        EXPECT_TRUE(one_g->physicalBase == gb_pa);
        EXPECT_TRUE(one_g->permissions.execute);
    }

    EXPECT_TRUE(table.unmap(0x10000, PAGE_SIZE_4K));
    EXPECT_FALSE(table.translate(0x10000));
    EXPECT_FALSE(table.unmap(0x10000, PAGE_SIZE_4K));
}

void testOrderVerifierAcceptsCompleteAliasBarrier() {
    AtsOrderVerifier verifier;
    const TicketId ticket = 9;
    const Bdf main = canonical::mainBdf();
    const Bdf alias = canonical::aliasBdf();
    verifier.setAssociatedBdfs(ticket, {main, alias});

    auto record = [&](OrderEventType type, Bdf bdf = Bdf{}) {
        verifier.record(OrderEvent{type, ticket, bdf});
    };
    record(OrderEventType::Unmapped);
    record(OrderEventType::IotlbIssued);
    record(OrderEventType::IotlbCompleted);
    record(OrderEventType::AtsIssued, main);
    record(OrderEventType::AtsCompleted, main);
    record(OrderEventType::AtsIssued, alias);
    record(OrderEventType::AtsCompleted, alias);
    record(OrderEventType::WaitIssued);
    record(OrderEventType::WaitCompleted);

    EXPECT_TRUE(verifier.valid());
    EXPECT_TRUE(verifier.isReleasable(ticket));
}

void testOrderVerifierRejectsMissingAlias() {
    AtsOrderVerifier verifier;
    const TicketId ticket = 10;
    const Bdf main = canonical::mainBdf();
    const Bdf alias = canonical::aliasBdf();
    verifier.setAssociatedBdfs(ticket, {main, alias});

    auto record = [&](OrderEventType type, Bdf bdf = Bdf{}) {
        verifier.record(OrderEvent{type, ticket, bdf});
    };
    record(OrderEventType::Unmapped);
    record(OrderEventType::IotlbIssued);
    record(OrderEventType::IotlbCompleted);
    record(OrderEventType::AtsIssued, main);
    record(OrderEventType::AtsCompleted, main);
    record(OrderEventType::WaitIssued);
    record(OrderEventType::WaitCompleted);
    record(OrderEventType::ReleaseAttempt);

    EXPECT_FALSE(verifier.valid());
    EXPECT_FALSE(verifier.isReleasable(ticket));
    bool mentions_alias = false;
    for (const std::string& message : verifier.violations()) {
        if (message.find(alias.toString()) != std::string::npos) {
            mentions_alias = true;
        }
    }
    EXPECT_TRUE(mentions_alias);
}

void testSafeInvalidationSequence() {
    Machine machine = canonical::makePrimedMachine();
    const Bdf main = canonical::mainBdf();
    const Bdf alias = canonical::aliasBdf();

    const TicketId ticket = machine.unmapAndOpenTicket(
        main, canonical::PASID, canonical::IOVA, PAGE_SIZE_4K);
    EXPECT_TRUE(ticket != 0);
    EXPECT_FALSE(machine.safeRelease(ticket));

    EXPECT_TRUE(machine.invalidateIotlbNow(ticket));
    EXPECT_TRUE(machine.invalidateAtsNow(ticket, main));
    EXPECT_FALSE(machine.waitBarrierNow(ticket));
    EXPECT_TRUE(machine.invalidateAtsNow(ticket, alias));
    EXPECT_TRUE(machine.waitBarrierNow(ticket));

    std::vector<std::string> reasons;
    PhysicalAddress kernel_pa = 0;
    EXPECT_TRUE(machine.safeReleaseAndReallocate(
        ticket, PageOwner{PageOwnerKind::Kernel, 0}, &kernel_pa, &reasons));
    EXPECT_TRUE(reasons.empty());
    EXPECT_TRUE(kernel_pa == canonical::GUEST_PAGE_PA);

    DmaResult main_dma =
        machine.executeDma(canonical::writeFrom(main));
    DmaResult alias_dma =
        machine.executeDma(canonical::writeFrom(alias));
    EXPECT_FALSE(main_dma.hardwareGranted);
    EXPECT_FALSE(alias_dma.hardwareGranted);
    EXPECT_FALSE(main_dma.violation);
    EXPECT_FALSE(alias_dma.violation);
}

void testAsyncInvalidationCompletionIsRequired() {
    Machine machine = canonical::makePrimedMachine();
    EXPECT_TRUE(machine.iotlb().size() == 1);

    const TicketId ticket = machine.unmapAndOpenTicket(
        canonical::mainBdf(), canonical::PASID, canonical::IOVA,
        PAGE_SIZE_4K);
    const CommandId iotlb_command =
        machine.submitIotlbInvalidation(ticket);
    EXPECT_TRUE(iotlb_command != 0);
    EXPECT_TRUE(machine.invalidationQueue().queued().size() == 1);
    EXPECT_TRUE(machine.iotlb().size() == 1);
    const ReclaimTicket* state = machine.ticket(ticket);
    EXPECT_TRUE(state != nullptr);
    if (state != nullptr) {
        EXPECT_TRUE(state->iotlbIssued);
        EXPECT_FALSE(state->iotlbCompleted);
    }

    EXPECT_TRUE(machine.completeCommand(iotlb_command));
    EXPECT_TRUE(machine.iotlb().empty());

    const CommandId wait_command = machine.submitWaitBarrier(ticket);
    EXPECT_TRUE(wait_command != 0);
    EXPECT_FALSE(machine.completeCommand(wait_command));
    state = machine.ticket(ticket);
    if (state != nullptr) {
        EXPECT_TRUE(state->waitIssued);
        EXPECT_FALSE(state->waitCompleted);
    }
}

void testStaleAliasDmaIsDetected() {
    Machine machine = canonical::makePrimedMachine();
    const Bdf main = canonical::mainBdf();
    const Bdf alias = canonical::aliasBdf();
    const TicketId ticket = machine.unmapAndOpenTicket(
        main, canonical::PASID, canonical::IOVA, PAGE_SIZE_4K);
    EXPECT_TRUE(ticket != 0);
    EXPECT_TRUE(machine.unsafeReleaseAndReallocate(
        ticket, PageOwner{PageOwnerKind::Kernel, 0}));

    DmaResult result = machine.executeDma(canonical::writeFrom(alias));
    EXPECT_TRUE(result.violation);
    EXPECT_TRUE(result.hardwareGranted);
    EXPECT_TRUE(result.usedAts || result.usedIotlb);
    EXPECT_TRUE(result.staleFindings.size() >= 1);
    EXPECT_FALSE(result.currentTranslation.mapped);
    bool found_permitting_ats = false;
    for (const auto& finding : result.staleFindings) {
        if (finding.layer == StaleTranslationFinding::Layer::Ats &&
            finding.permitsRequestedAccess) {
            found_permitting_ats = true;
        }
    }
    EXPECT_TRUE(found_permitting_ats);
    const PhysicalFrame* frame =
        machine.pool().frame(canonical::GUEST_PAGE_PA);
    EXPECT_TRUE(frame != nullptr);
    if (frame != nullptr) {
        EXPECT_TRUE(frame->owner.kind == PageOwnerKind::Kernel);
        EXPECT_TRUE(frame->writeCount > 0);
    }
}

void testCrossTenantReallocationIsAlsoViolation() {
    Machine machine = canonical::makePrimedMachine();
    const TicketId ticket = machine.unmapAndOpenTicket(
        canonical::mainBdf(), canonical::PASID, canonical::IOVA,
        PAGE_SIZE_4K);
    EXPECT_TRUE(machine.unsafeReleaseAndReallocate(
        ticket, PageOwner{PageOwnerKind::OtherDomain, 42}));

    DmaResult result = machine.executeDma(
        DmaRequest{canonical::mainBdf(), canonical::PASID, canonical::IOVA,
                   canonical::DMA_LENGTH, DmaOp::Read});
    EXPECT_TRUE(result.violation);
    EXPECT_TRUE(result.hardwareGranted);
    const PhysicalFrame* frame =
        machine.pool().frame(canonical::GUEST_PAGE_PA);
    EXPECT_TRUE(frame != nullptr);
    if (frame != nullptr) {
        EXPECT_TRUE(frame->owner.kind == PageOwnerKind::OtherDomain);
        EXPECT_TRUE(frame->owner.domain == 42);
    }
}

void testOnlineInterceptorObservesViolation() {
    Machine machine = canonical::makePrimedMachine();
    auto monitor = std::make_shared<OnlineSafetyInterceptor>();
    auto log = std::make_shared<EventLogInterceptor>();
    machine.events().subscribe(monitor);
    machine.events().subscribe(log);

    const TicketId ticket = machine.unmapAndOpenTicket(
        canonical::mainBdf(), canonical::PASID, canonical::IOVA,
        PAGE_SIZE_4K);
    EXPECT_TRUE(machine.unsafeReleaseAndReallocate(
        ticket, PageOwner{PageOwnerKind::Kernel, 0}));
    (void)machine.executeDma(
        canonical::writeFrom(canonical::aliasBdf()));

    EXPECT_TRUE(monitor->sawViolation());
    EXPECT_TRUE(!monitor->violations().empty());
    EXPECT_TRUE(!log->ofKind(EventKind::SecurityViolation).empty());
}

void testBreadthFirstSearchFindsShortestViolation() {
    const SearchResult result = InterleavingSearcher::search(
        canonical::makePrimedMachine(), canonical::makeActions(), 8);
    EXPECT_TRUE(result.found);
    EXPECT_TRUE(result.depth == 3);
    const std::vector<std::string> expected{
        "Unmap", "ReallocateKernel", "DmaMain"};
    EXPECT_TRUE(result.sequence == expected);
    EXPECT_TRUE(result.violation.has_value());
    if (result.violation) {
        EXPECT_TRUE(result.violation->violation);
    }

    Machine replay = canonical::makePrimedMachine();
    for (const std::string& name : result.sequence) {
        for (const SearchAction& action : canonical::makeActions()) {
            if (action.name == name) {
                (void)action.apply(replay);
                break;
            }
        }
    }
}

void testConstrainedSearchesFindOrderingBugs() {
    const SearchResult iotlb_only = InterleavingSearcher::search(
        canonical::makePrimedMachine(), canonical::makeIotlbOnlyActions(), 8);
    EXPECT_TRUE(iotlb_only.found);
    EXPECT_TRUE(iotlb_only.depth == 4);

    const SearchResult alias = InterleavingSearcher::search(
        canonical::makePrimedMachine(),
        canonical::makePrimaryOnlyAliasActions(), 8);
    EXPECT_TRUE(alias.found);
    EXPECT_TRUE(alias.depth == 5);
    EXPECT_TRUE(alias.sequence.back() == "DmaAlias");

    const SearchResult issued_wait = InterleavingSearcher::search(
        canonical::makePrimedMachine(),
        canonical::makeIssuedWaitAliasRaceActions(), 8);
    EXPECT_TRUE(issued_wait.found);
    EXPECT_TRUE(issued_wait.depth == 6);
    const std::vector<std::string> wait_expected{
        "Unmap", "InvalidateIOTLB", "InvalidateMainATC",
        "InvalidationWait", "ReallocateKernel", "DmaAlias"};
    EXPECT_TRUE(issued_wait.sequence == wait_expected);
    EXPECT_TRUE(replayReferenceSequence(issued_wait.sequence));
}

void testTrueThreadInterleavingSearch() {
    const SearchResult race = InterleavingSearcher::searchInterleavings(
        canonical::makePrimedMachine(), canonical::makeIotlbRaceThreads(), 8);
    EXPECT_TRUE(race.found);
    EXPECT_TRUE(race.depth == 4);
    const std::vector<std::string> expected{
        "Unmap", "InvalidateIOTLB", "ReallocateKernel", "DmaAlias"};
    EXPECT_TRUE(race.sequence == expected);

    const SearchResult safe = InterleavingSearcher::searchInterleavings(
        canonical::makePrimedMachine(), canonical::makeSafeVsDmaThreads(), 8);
    EXPECT_FALSE(safe.found);
}

void testReferenceExhaustiveOracle() {
    const ExhaustiveReport report =
        exhaustivelyEnumerateReference(8);
    EXPECT_TRUE(report.found);
    EXPECT_TRUE(report.shortestDepth == 3);
    EXPECT_TRUE(report.sequencesEnumerated == 19173960ULL);
    const std::vector<std::string> expected{
        "Unmap", "ReallocateKernel", "DmaMain"};
    EXPECT_TRUE(report.shortestSequence == expected);
    EXPECT_TRUE(replayReferenceSequence(expected));
    EXPECT_FALSE(replayReferenceSequence(
        {"Unmap", "InvalidateIOTLB", "InvalidateMainATC",
         "InvalidateAliasATC", "InvalidationWait"}));
}

} // namespace

int main() {
    testBdfAndAliases();
    testPageTableLargePagesAndPermissions();
    testOrderVerifierAcceptsCompleteAliasBarrier();
    testOrderVerifierRejectsMissingAlias();
    testSafeInvalidationSequence();
    testAsyncInvalidationCompletionIsRequired();
    testStaleAliasDmaIsDetected();
    testCrossTenantReallocationIsAlsoViolation();
    testOnlineInterceptorObservesViolation();
    testBreadthFirstSearchFindsShortestViolation();
    testConstrainedSearchesFindOrderingBugs();
    testTrueThreadInterleavingSearch();
    testReferenceExhaustiveOracle();

    if (g_failures != 0) {
        std::cerr << g_failures << " failure(s) in " << g_checks
                  << " checks\n";
        return 1;
    }
    std::cout << "all " << g_checks << " checks passed\n";
    return 0;
}
