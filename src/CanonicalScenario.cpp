#include "iommu_ats/CanonicalScenario.hpp"

namespace iommu_ats::canonical {

Bdf mainBdf() {
    return Bdf{0x01, 0x00, 0x0};
}

Bdf aliasBdf() {
    return Bdf{0x01, 0x00, 0x1};
}

DmaRequest writeFrom(Bdf bdf) {
    return DmaRequest{bdf, PASID, IOVA, DMA_LENGTH, DmaOp::Write};
}

namespace {

TicketId currentTicket(Machine& machine) {
    const auto ids = machine.ticketIds();
    return ids.empty() ? 0U : ids.back();
}

ActionOutcome dmaAction(Machine& machine, Bdf bdf) {
    DmaResult result = machine.executeDma(writeFrom(bdf));
    return ActionOutcome{result.violation, std::move(result)};
}

SearchAction unmapAction() {
    return SearchAction{"Unmap", [](Machine& machine) {
                            machine.unmapAndOpenTicket(
                                mainBdf(), PASID, IOVA, PAGE_SIZE_4K);
                            return ActionOutcome{};
                        }};
}

SearchAction iotlbAction() {
    return SearchAction{"InvalidateIOTLB", [](Machine& machine) {
                            const TicketId id = currentTicket(machine);
                            if (id != 0) {
                                machine.invalidateIotlbNow(id);
                            }
                            return ActionOutcome{};
                        }};
}

SearchAction mainAtcAction() {
    return SearchAction{"InvalidateMainATC", [](Machine& machine) {
                            const TicketId id = currentTicket(machine);
                            if (id != 0) {
                                machine.invalidateAtsNow(id, mainBdf());
                            }
                            return ActionOutcome{};
                        }};
}

SearchAction aliasAtcAction() {
    return SearchAction{"InvalidateAliasATC", [](Machine& machine) {
                            const TicketId id = currentTicket(machine);
                            if (id != 0) {
                                machine.invalidateAtsNow(id, aliasBdf());
                            }
                            return ActionOutcome{};
                        }};
}

SearchAction waitAction() {
    return SearchAction{"InvalidationWait", [](Machine& machine) {
                            const TicketId id = currentTicket(machine);
                            if (id != 0) {
                                machine.waitBarrierNow(id);
                            }
                            return ActionOutcome{};
                        }};
}

SearchAction reallocAction() {
    return SearchAction{"ReallocateKernel", [](Machine& machine) {
                            const TicketId id = currentTicket(machine);
                            if (id != 0) {
                                machine.unsafeReleaseAndReallocate(
                                    id, PageOwner{PageOwnerKind::Kernel, 0});
                            }
                            return ActionOutcome{};
                        }};
}

SearchAction safeReleaseAction() {
    return SearchAction{"SafeRelease", [](Machine& machine) {
                            const TicketId id = currentTicket(machine);
                            if (id != 0) {
                                (void)machine.safeRelease(id);
                            }
                            return ActionOutcome{};
                        }};
}

SearchAction allocateKernelAction() {
    return SearchAction{"AllocateKernel", [](Machine& machine) {
                            (void)machine.pool().allocateSpecific(
                                GUEST_PAGE_PA,
                                PageOwner{PageOwnerKind::Kernel, 0});
                            return ActionOutcome{};
                        }};
}

SearchAction dmaMainAction() {
    return SearchAction{"DmaMain", [](Machine& machine) {
                            return dmaAction(machine, mainBdf());
                        }};
}

SearchAction dmaAliasAction() {
    return SearchAction{"DmaAlias", [](Machine& machine) {
                            return dmaAction(machine, aliasBdf());
                        }};
}

} // namespace

Machine makePrimedMachine() {
    Machine machine;
    const Bdf main = mainBdf();
    const Bdf alias = aliasBdf();
    machine.topology().addGroup(1, {main, alias});
    machine.pasidTable().bind(main, PASID, GUEST_DOMAIN_ID);
    machine.pasidTable().bind(alias, PASID, GUEST_DOMAIN_ID);
    machine.createAddressSpace(GUEST_DOMAIN_ID);

    std::string error;
    if (!machine.mapPage(GUEST_DOMAIN_ID, IOVA, GUEST_PAGE_PA, PAGE_SIZE_4K,
                         AccessPermissions::readWrite(), &error)) {
        return machine;
    }

    machine.pool().addFrame(GUEST_PAGE_PA, PAGE_SIZE_4K,
                            PageOwner{PageOwnerKind::Guest, GUEST_DOMAIN_ID});
    machine.pool().addFrame(KERNEL_PAGE_PA, PAGE_SIZE_4K,
                            PageOwner{PageOwnerKind::Kernel, 0});

    // Legal reads walk the page table and prime IOTLB plus each BDF ATC.
    DmaRequest prime{main, PASID, IOVA, DMA_LENGTH, DmaOp::Read};
    (void)machine.executeDma(prime);
    DmaRequest prime_alias{alias, PASID, IOVA, DMA_LENGTH, DmaOp::Read};
    (void)machine.executeDma(prime_alias);
    return machine;
}

std::vector<SearchAction> makeActions() {
    return {unmapAction(),      iotlbAction(),    mainAtcAction(),
            aliasAtcAction(),   waitAction(),     reallocAction(),
            dmaMainAction(),    dmaAliasAction()};
}

std::vector<SearchAction> makeIotlbOnlyActions() {
    SearchAction gatedRealloc{
        "ReallocateKernel", [](Machine& machine) {
            const TicketId id = currentTicket(machine);
            const ReclaimTicket* ticket = machine.ticket(id);
            if (ticket != nullptr && ticket->iotlbCompleted) {
                machine.unsafeReleaseAndReallocate(
                    id, PageOwner{PageOwnerKind::Kernel, 0});
            }
            return ActionOutcome{};
        }};

    return {unmapAction(), iotlbAction(), gatedRealloc, dmaMainAction()};
}

std::vector<ThreadProgram> makeIotlbRaceThreads() {
    return {
        ThreadProgram{"faulty-reclaimer",
                      {unmapAction(), iotlbAction(), reallocAction()}},
        ThreadProgram{"malicious-device", {dmaAliasAction()}},
    };
}

std::vector<ThreadProgram> makeSafeVsDmaThreads() {
    return {
        ThreadProgram{"safe-reclaimer",
                      {unmapAction(), iotlbAction(), mainAtcAction(),
                       aliasAtcAction(), waitAction(), safeReleaseAction(),
                       allocateKernelAction()}},
        ThreadProgram{"malicious-device", {dmaAliasAction()}},
    };
}

std::vector<SearchAction> makePrimaryOnlyAliasActions() {
    SearchAction gatedRealloc{
        "ReallocateKernel", [](Machine& machine) {
            const TicketId id = currentTicket(machine);
            const ReclaimTicket* ticket = machine.ticket(id);
            if (ticket != nullptr && ticket->iotlbCompleted &&
                ticket->atsCompletedFor(mainBdf())) {
                machine.unsafeReleaseAndReallocate(
                    id, PageOwner{PageOwnerKind::Kernel, 0});
            }
            return ActionOutcome{};
        }};

    return {unmapAction(), iotlbAction(), mainAtcAction(), gatedRealloc,
            dmaAliasAction()};
}

std::vector<SearchAction> makeIssuedWaitAliasRaceActions() {
    SearchAction gatedRealloc{
        "ReallocateKernel", [](Machine& machine) {
            const TicketId id = currentTicket(machine);
            const ReclaimTicket* ticket = machine.ticket(id);
            if (ticket != nullptr && ticket->iotlbCompleted &&
                ticket->atsCompletedFor(mainBdf()) &&
                ticket->waitIssued) {
                machine.unsafeReleaseAndReallocate(
                    id, PageOwner{PageOwnerKind::Kernel, 0});
            }
            return ActionOutcome{};
        }};

    return {unmapAction(),  iotlbAction(),  mainAtcAction(),
            waitAction(),   gatedRealloc,   dmaAliasAction()};
}

} // namespace iommu_ats::canonical
