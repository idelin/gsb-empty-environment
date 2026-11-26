#include "iommu_ats/Machine.hpp"

#include <algorithm>
#include <sstream>
#include <utility>

namespace iommu_ats {

bool DmaResult::staleIotlbHit() const {
    return std::any_of(staleFindings.begin(), staleFindings.end(),
                       [](const StaleTranslationFinding& finding) {
                           return finding.unauthorized &&
                                  finding.layer ==
                                      StaleTranslationFinding::Layer::Iotlb;
                       });
}

bool DmaResult::staleAtsHit() const {
    return std::any_of(staleFindings.begin(), staleFindings.end(),
                       [](const StaleTranslationFinding& finding) {
                           return finding.unauthorized &&
                                  finding.layer ==
                                      StaleTranslationFinding::Layer::Ats;
                       });
}

Machine::Machine() : iotlb_("IOMMU.IOTLB") {}

AliasTopology& Machine::topology() noexcept {
    return topology_;
}

const AliasTopology& Machine::topology() const noexcept {
    return topology_;
}

PasidDomainTable& Machine::pasidTable() noexcept {
    return pasidTable_;
}

const PasidDomainTable& Machine::pasidTable() const noexcept {
    return pasidTable_;
}

HostPagePool& Machine::pool() noexcept {
    return pool_;
}

const HostPagePool& Machine::pool() const noexcept {
    return pool_;
}

EventBus& Machine::events() noexcept {
    return eventBus_;
}

PageTable& Machine::createAddressSpace(DomainId domain,
                                       PhysicalAddress rootPhysicalAddress) {
    const PhysicalAddress root =
        rootPhysicalAddress != 0
            ? rootPhysicalAddress
            : 0x10000ULL + static_cast<u64>(domain) * 0x10000ULL;
    auto inserted = addressSpaces_.try_emplace(domain, root);
    return inserted.first->second;
}

PageTable* Machine::addressSpace(DomainId domain) {
    const auto it = addressSpaces_.find(domain);
    return it == addressSpaces_.end() ? nullptr : &it->second;
}

const PageTable* Machine::addressSpace(DomainId domain) const {
    const auto it = addressSpaces_.find(domain);
    return it == addressSpaces_.end() ? nullptr : &it->second;
}

bool Machine::mapPage(DomainId domain, Iova iova, PhysicalAddress pa,
                      u64 size, AccessPermissions permissions,
                      std::string* error) {
    PageTable* table = addressSpace(domain);
    if (table == nullptr) {
        if (error != nullptr) {
            *error = "domain has no PASID page table";
        }
        return false;
    }
    const bool ok = table->map(iova, pa, size, permissions, error);
    if (ok) {
        eventBus_.emit(EventKind::MappingChanged, "page mapped");
    }
    return ok;
}

TranslationCache& Machine::iotlb() noexcept {
    return iotlb_;
}

TranslationCache& Machine::atsCache(Bdf bdf) {
    auto inserted = atsCaches_.try_emplace(bdf, bdf.toString() + ".ATC");
    return inserted.first->second;
}

const std::map<Bdf, TranslationCache>& Machine::atsCaches() const noexcept {
    return atsCaches_;
}

TicketId Machine::unmapAndOpenTicket(Bdf requester, Pasid pasid, Iova iova,
                                     u64 size) {
    const auto domain = pasidTable_.lookup(requester, pasid);
    if (!domain) {
        return 0;
    }
    PageTable* table = addressSpace(*domain);
    if (table == nullptr) {
        return 0;
    }
    auto segment = table->translate(iova);
    if (!segment) {
        return 0;
    }
    const u64 page_size = size == 0 ? segment->pageSize : size;
    if (page_size != segment->pageSize) {
        return 0;
    }
    if (!table->unmap(segment->iovaBase, segment->pageSize)) {
        return 0;
    }

    ReclaimTicket ticket;
    ticket.id = nextTicketId_++;
    ticket.domain = *domain;
    ticket.pasid = pasid;
    ticket.iovaBase = segment->iovaBase;
    ticket.size = segment->pageSize;
    ticket.physicalBase = segment->physicalBase;
    ticket.associatedBdfs = topology_.aliasesOf(requester);
    ticket.unmapped = true;
    tickets_.push_back(std::move(ticket));
    eventBus_.emit(EventKind::MappingChanged, "page unmapped and reclaim ticket opened",
                   std::nullopt, tickets_.back().id);
    return tickets_.back().id;
}

ReclaimTicket* Machine::findTicket(TicketId id) {
    const auto it = std::find_if(tickets_.begin(), tickets_.end(),
                                 [id](const ReclaimTicket& ticket) {
                                     return ticket.id == id;
                                 });
    return it == tickets_.end() ? nullptr : &*it;
}

const ReclaimTicket* Machine::findTicket(TicketId id) const {
    const auto it = std::find_if(tickets_.begin(), tickets_.end(),
                                 [id](const ReclaimTicket& ticket) {
                                     return ticket.id == id;
                                 });
    return it == tickets_.end() ? nullptr : &*it;
}

ReclaimTicket* Machine::ticket(TicketId id) {
    return findTicket(id);
}

const ReclaimTicket* Machine::ticket(TicketId id) const {
    return findTicket(id);
}

std::vector<TicketId> Machine::ticketIds() const {
    std::vector<TicketId> ids;
    ids.reserve(tickets_.size());
    for (const ReclaimTicket& ticket : tickets_) {
        ids.push_back(ticket.id);
    }
    return ids;
}

CommandId Machine::submitIotlbInvalidation(TicketId ticketId) {
    ReclaimTicket* ticket = findTicket(ticketId);
    if (ticket == nullptr) {
        return 0;
    }
    if (ticket->iotlbIssued) {
        for (const auto& command : invalidationQueue_.commands()) {
            if (command.ticketId == ticketId &&
                command.kind == InvalidationKind::InvalidateIOTLB) {
                return command.id;
            }
        }
    }

    InvalidationCommand command;
    command.kind = InvalidationKind::InvalidateIOTLB;
    command.ticketId = ticketId;
    command.domain = ticket->domain;
    command.allPasids = true;
    command.pasid = ticket->pasid;
    command.iovaBase = ticket->iovaBase;
    command.size = ticket->size;
    const CommandId id = invalidationQueue_.submit(std::move(command)).id;
    ticket->iotlbIssued = true;
    eventBus_.emit(EventKind::InvalidationIssued,
                   "InvalidateIOTLB queued", std::nullopt, ticketId);
    return id;
}

CommandId Machine::submitAtsInvalidation(TicketId ticketId, Bdf bdf) {
    ReclaimTicket* ticket = findTicket(ticketId);
    if (ticket == nullptr) {
        return 0;
    }
    if (ticket->atsCompletedFor(bdf) ||
        std::find(ticket->atsIssued.begin(), ticket->atsIssued.end(), bdf) !=
            ticket->atsIssued.end()) {
        for (const auto& command : invalidationQueue_.commands()) {
            if (command.ticketId == ticketId &&
                command.kind == InvalidationKind::InvalidateDeviceATC &&
                command.bdf == bdf) {
                return command.id;
            }
        }
    }

    InvalidationCommand command;
    command.kind = InvalidationKind::InvalidateDeviceATC;
    command.ticketId = ticketId;
    command.domain = ticket->domain;
    command.bdf = bdf;
    command.allPasids = true;
    command.pasid = ticket->pasid;
    command.iovaBase = ticket->iovaBase;
    command.size = ticket->size;
    const CommandId id = invalidationQueue_.submit(std::move(command)).id;
    ticket->markAtsIssued(bdf);
    eventBus_.emit(EventKind::InvalidationIssued,
                   "InvalidateDeviceATC queued for " + bdf.toString(),
                   std::nullopt, ticketId);
    return id;
}

CommandId Machine::submitWaitBarrier(TicketId ticketId) {
    ReclaimTicket* ticket = findTicket(ticketId);
    if (ticket == nullptr) {
        return 0;
    }
    if (ticket->waitIssued) {
        for (const auto& command : invalidationQueue_.commands()) {
            if (command.ticketId == ticketId &&
                command.kind == InvalidationKind::InvalidationWait) {
                return command.id;
            }
        }
    }

    InvalidationCommand command;
    command.kind = InvalidationKind::InvalidationWait;
    command.ticketId = ticketId;
    command.domain = ticket->domain;
    const CommandId id = invalidationQueue_.submit(std::move(command)).id;
    ticket->waitIssued = true;
    eventBus_.emit(EventKind::WaitBarrier, "InvalidationWait queued",
                   std::nullopt, ticketId);
    return id;
}

bool Machine::completeCommand(CommandId commandId) {
    InvalidationCommand* command = invalidationQueue_.find(commandId);
    if (command == nullptr ||
        command->state == CommandState::Completed || command->id == 0) {
        return false;
    }
    ReclaimTicket* ticket = findTicket(command->ticketId);
    if (ticket == nullptr) {
        return false;
    }

    switch (command->kind) {
    case InvalidationKind::InvalidateIOTLB:
        iotlb_.invalidateDomain(command->domain);
        ticket->iotlbCompleted = true;
        command->state = CommandState::Completed;
        eventBus_.emit(EventKind::InvalidationCompleted,
                       "InvalidateIOTLB completed", std::nullopt,
                       ticket->id);
        return true;

    case InvalidationKind::InvalidateDeviceATC:
        atsCache(command->bdf).invalidateDomain(command->domain);
        ticket->markAtsCompleted(command->bdf);
        command->state = CommandState::Completed;
        eventBus_.emit(EventKind::InvalidationCompleted,
                       "InvalidateDeviceATC completed for " +
                           command->bdf.toString(),
                       std::nullopt, ticket->id);
        return true;

    case InvalidationKind::InvalidationWait:
        if (!ticket->unmapped || !ticket->iotlbCompleted ||
            !ticket->allAtsCompleted()) {
            return false;
        }
        ticket->waitCompleted = true;
        command->state = CommandState::Completed;
        eventBus_.emit(EventKind::WaitBarrier,
                       "InvalidationWait barrier passed", std::nullopt,
                       ticket->id);
        return true;
    }
    return false;
}

bool Machine::invalidateIotlbNow(TicketId ticketId,
                                 CommandId* commandIdOut) {
    const CommandId id = submitIotlbInvalidation(ticketId);
    if (id == 0) {
        return false;
    }
    if (commandIdOut != nullptr) {
        *commandIdOut = id;
    }
    const InvalidationCommand* existing = invalidationQueue_.find(id);
    if (existing != nullptr &&
        existing->state == CommandState::Completed) {
        return true;
    }
    return completeCommand(id);
}

bool Machine::invalidateAtsNow(TicketId ticketId, Bdf bdf,
                               CommandId* commandIdOut) {
    const CommandId id = submitAtsInvalidation(ticketId, bdf);
    if (id == 0) {
        return false;
    }
    if (commandIdOut != nullptr) {
        *commandIdOut = id;
    }
    const InvalidationCommand* existing = invalidationQueue_.find(id);
    if (existing != nullptr &&
        existing->state == CommandState::Completed) {
        return true;
    }
    return completeCommand(id);
}

bool Machine::waitBarrierNow(TicketId ticketId) {
    ReclaimTicket* ticket = findTicket(ticketId);
    if (ticket == nullptr) {
        return false;
    }
    if (ticket->waitCompleted) {
        return true;
    }
    const CommandId id = submitWaitBarrier(ticketId);
    return id != 0 && completeCommand(id);
}

std::vector<std::string> Machine::releaseReadiness(TicketId ticketId) const {
    std::vector<std::string> reasons;
    const ReclaimTicket* ticket = findTicket(ticketId);
    if (ticket == nullptr) {
        reasons.emplace_back("reclaim ticket does not exist");
        return reasons;
    }
    if (!ticket->unmapped) {
        reasons.emplace_back("page table entry has not been unmapped");
    }
    if (!ticket->iotlbCompleted) {
        reasons.emplace_back("domain IOTLB invalidation has not completed");
    }
    if (!ticket->allAtsCompleted()) {
        reasons.emplace_back("not all associated/alias device ATCs were invalidated");
    }
    if (!ticket->waitCompleted) {
        reasons.emplace_back("InvalidationWait barrier has not passed");
    }

    const PageTable* table = addressSpace(ticket->domain);
    if (table != nullptr && table->translate(ticket->iovaBase)) {
        reasons.emplace_back("current page table still maps the IOVA");
    }

    for (const CacheEntry& entry : iotlb_.entries()) {
        if (entry.domain == ticket->domain &&
            entry.overlaps(ticket->iovaBase, ticket->size)) {
            reasons.emplace_back("stale IOTLB entry still overlaps reclaim range");
            break;
        }
    }

    for (const Bdf bdf : ticket->associatedBdfs) {
        const auto cacheIt = atsCaches_.find(bdf);
        bool dirty = cacheIt == atsCaches_.end();
        if (cacheIt != atsCaches_.end()) {
            for (const CacheEntry& entry : cacheIt->second.entries()) {
                if (entry.domain == ticket->domain &&
                    entry.overlaps(ticket->iovaBase, ticket->size)) {
                    dirty = true;
                    break;
                }
            }
        }
        if (dirty) {
            reasons.push_back("stale ATC entry remains for " +
                               bdf.toString());
        }
    }

    return reasons;
}

bool Machine::safeRelease(TicketId ticketId,
                          std::vector<std::string>* reasons) {
    std::vector<std::string> local = releaseReadiness(ticketId);
    if (!local.empty()) {
        eventBus_.emit(EventKind::SafetyBlocked,
                       "safe release blocked: " + local.front(),
                       std::nullopt, ticketId);
        if (reasons != nullptr) {
            *reasons = std::move(local);
        }
        return false;
    }

    ReclaimTicket* ticket = findTicket(ticketId);
    if (ticket == nullptr ||
        !pool_.releaseToFreePool(ticket->physicalBase)) {
        if (reasons != nullptr) {
            reasons->emplace_back("host page pool rejected release");
        }
        return false;
    }
    ticket->released = true;
    eventBus_.emit(EventKind::PageReleased,
                   "physical page released after complete invalidation barrier",
                   std::nullopt, ticketId);
    return true;
}

bool Machine::safeReleaseAndReallocate(TicketId ticketId,
                                       PageOwner newOwner,
                                       PhysicalAddress* allocatedPa,
                                       std::vector<std::string>* reasons) {
    if (!safeRelease(ticketId, reasons)) {
        return false;
    }
    ReclaimTicket* ticket = findTicket(ticketId);
    if (ticket == nullptr) {
        return false;
    }
    const auto pa = pool_.allocateSpecific(ticket->physicalBase, newOwner);
    if (!pa) {
        if (reasons != nullptr) {
            reasons->emplace_back("host pool could not reallocate page");
        }
        return false;
    }
    if (allocatedPa != nullptr) {
        *allocatedPa = *pa;
    }
    eventBus_.emit(EventKind::PageAllocated,
                   "page reallocated only after the complete safety barrier",
                   std::nullopt, ticketId);
    return true;
}

bool Machine::unsafeReleaseAndReallocate(TicketId ticketId,
                                         PageOwner newOwner) {
    ReclaimTicket* ticket = findTicket(ticketId);
    if (ticket == nullptr) {
        return false;
    }
    pool_.releaseToFreePool(ticket->physicalBase);
    const auto allocated =
        pool_.allocateSpecific(ticket->physicalBase, newOwner);
    if (!allocated) {
        return false;
    }
    ticket->released = true;
    eventBus_.emit(EventKind::SecurityViolation,
                   "UNSAFE release/reallocation before IOTLB/ATC/wait barrier",
                   std::nullopt, ticketId);
    eventBus_.emit(EventKind::PageAllocated,
                   "reclaimed physical page reallocated across security domain",
                   std::nullopt, ticketId);
    return true;
}

StaleTranslationFinding Machine::classifyCacheEntry(
    DomainId domain, Pasid pasid, Iova iova, u64 length, Bdf bdf,
    StaleTranslationFinding::Layer layer, const CacheEntry& entry,
    DmaOp op) const {
    (void)pasid;
    (void)length;
    StaleTranslationFinding finding;
    finding.layer = layer;
    finding.bdf = bdf;
    finding.entry = entry;
    finding.permitsRequestedAccess = entry.permits(op);

    const Iova overlap_iova = std::max(iova, entry.iovaBase);
    finding.observedPhysicalAddress =
        entry.physicalBase + (overlap_iova - entry.iovaBase);

    const PageTable* table = addressSpace(domain);
    const auto current =
        table != nullptr ? table->translate(entry.iovaBase) : std::nullopt;
    finding.currentPageTableMatches =
        current.has_value() &&
        current->physicalBase == entry.physicalBase &&
        current->pageSize == entry.size &&
        current->permissions.allows(
            entry.permissions.write ? DmaOp::Write : DmaOp::Read);

    finding.owner = pool_.owner(finding.observedPhysicalAddress);
    finding.unauthorized =
        finding.permitsRequestedAccess &&
        !finding.currentPageTableMatches &&
        !pool_.isOwnedByRequester(finding.observedPhysicalAddress, domain);
    if (finding.unauthorized) {
        std::ostringstream out;
        out << (layer == StaleTranslationFinding::Layer::Iotlb ? "IOTLB"
                                                               : "ATC")
            << " stale translation reaches foreign/kernel page at PA=0x"
            << std::hex << finding.observedPhysicalAddress;
        finding.explanation = out.str();
    } else if (!finding.currentPageTableMatches) {
        finding.explanation =
            "cache entry is stale but still references requester-owned memory";
    }
    return finding;
}

void Machine::auditLayer(const DmaRequest& request, DomainId domain,
                         StaleTranslationFinding::Layer layer, Bdf bdf,
                         const TranslationCache& cache,
                         DmaResult& result) const {
    const u64 length = request.length;
    const auto hits = cache.lookupOverlap(domain, request.pasid,
                                          request.iova, length);
    for (const CacheHit& hit : hits) {
        auto finding = classifyCacheEntry(domain, request.pasid, request.iova,
                                          length, bdf, layer, hit.entry,
                                          request.op);
        if (finding.unauthorized) {
            result.violation = true;
            result.messages.push_back(finding.explanation);
        }
        result.staleFindings.push_back(std::move(finding));
    }
}

void Machine::touchPhysicalRange(PhysicalAddress start, u64 length,
                                 DomainId writer, Bdf bdf) {
    if (length == 0) {
        return;
    }
    const PhysicalAddress first_frame = start & ~(PAGE_SIZE_4K - 1ULL);
    const PhysicalAddress last_byte = start + length - 1ULL;
    const PhysicalAddress last_frame =
        last_byte & ~(PAGE_SIZE_4K - 1ULL);
    for (PhysicalAddress pa = first_frame; pa <= last_frame;
         pa += PAGE_SIZE_4K) {
        pool_.recordWrite(pa, writer, bdf);
        if (pa == last_frame) {
            break;
        }
    }
}

void Machine::fillCachesFromSegment(Bdf bdf, Pasid pasid, DomainId domain,
                                   const TranslationSegment& segment) {
    CacheEntry entry;
    entry.domain = domain;
    entry.pasid = pasid;
    entry.iovaBase = segment.iovaBase;
    entry.size = segment.pageSize;
    entry.physicalBase = segment.physicalBase;
    entry.permissions = segment.permissions;
    iotlb_.insert(entry);
    atsCache(bdf).insert(entry);
}

DmaResult Machine::executeDma(const DmaRequest& request) {
    DmaResult result;
    eventBus_.emit(EventKind::DmaAttempt, "DMA request", request);

    const auto domain = pasidTable_.lookup(request.bdf, request.pasid);
    if (!domain) {
        result.messages.emplace_back("no BDF/PASID domain binding");
        eventBus_.emit(EventKind::DmaDenied, result.messages.front(), request);
        return result;
    }
    result.domainResolved = true;
    result.domain = *domain;

    auditLayer(request, *domain, StaleTranslationFinding::Layer::Iotlb,
               Bdf{}, iotlb_, result);
    const auto atcIt = atsCaches_.find(request.bdf);
    if (atcIt != atsCaches_.end()) {
        auditLayer(request, *domain, StaleTranslationFinding::Layer::Ats,
                   request.bdf, atcIt->second, result);
    }

    PageTable* table = addressSpace(*domain);
    if (table == nullptr) {
        result.messages.emplace_back("domain page table missing");
        eventBus_.emit(EventKind::DmaDenied, result.messages.front(), request);
        return result;
    }
    result.currentTranslation =
        table->translateRange(request.iova, request.length, request.op);

    const Iova request_end = request.end();
    Iova current_iova = request.iova;
    bool complete = true;

    while (current_iova < request_end) {
        const u64 remaining = request_end - current_iova;
        const CacheEntry* selected = nullptr;
        bool from_atc = false;

        TranslationCache& device_cache = atsCache(request.bdf);
        auto atc_hits = device_cache.lookup(*domain, request.pasid,
                                            current_iova, 1, request.op);
        for (const CacheHit& hit : atc_hits) {
            if (hit.entry.covers(current_iova)) {
                selected = &hit.entry;
                from_atc = true;
                result.usedAts = true;
                break;
            }
        }

        CacheEntry iotlb_entry_copy;
        if (selected == nullptr) {
            auto iotlb_hits = iotlb_.lookup(*domain, request.pasid,
                                             current_iova, 1, request.op);
            for (const CacheHit& hit : iotlb_hits) {
                if (hit.entry.covers(current_iova)) {
                    iotlb_entry_copy = hit.entry;
                    selected = &iotlb_entry_copy;
                    result.usedIotlb = true;
                    break;
                }
            }
        }

        Iova chunk_end = current_iova + remaining;
        PhysicalAddress chunk_pa = 0;

        if (selected != nullptr) {
            chunk_end = std::min(request_end, selected->iovaEnd());
            const u64 offset = current_iova - selected->iovaBase;
            chunk_pa = selected->physicalBase + offset;
            result.accessedPhysicalAddresses.push_back(chunk_pa);
            if (!from_atc) {
                atsCache(request.bdf).insert(*selected);
            }
        } else {
            auto segment = table->translate(current_iova);
            if (!segment || !segment->permissions.allows(request.op)) {
                complete = false;
                result.messages.emplace_back(
                    "DMA fault: no valid current or cached translation");
                break;
            }
            chunk_end = std::min(request_end, segment->iovaEnd);
            const u64 offset = current_iova - segment->iovaBase;
            chunk_pa = segment->physicalBase + offset;
            result.accessedPhysicalAddresses.push_back(chunk_pa);
            fillCachesFromSegment(request.bdf, request.pasid, *domain,
                                  *segment);
        }

        const u64 chunk_length = chunk_end - current_iova;
        if (request.op == DmaOp::Write) {
            touchPhysicalRange(chunk_pa, chunk_length, *domain,
                               request.bdf);
        }
        current_iova = chunk_end;
    }

    result.hardwareGranted = complete;
    if (complete) {
        eventBus_.emit(EventKind::DmaGranted, "DMA translation accepted",
                       request);
    } else {
        eventBus_.emit(EventKind::DmaDenied,
                       result.messages.empty() ? "DMA denied"
                                               : result.messages.front(),
                       request);
    }
    if (result.violation) {
        eventBus_.emit(EventKind::SecurityViolation,
                       "stale translation DMA reached a foreign/kernel page",
                       request);
    }
    return result;
}

InvalidationQueue& Machine::invalidationQueue() noexcept {
    return invalidationQueue_;
}

const InvalidationQueue& Machine::invalidationQueue() const noexcept {
    return invalidationQueue_;
}

std::string Machine::canonicalState() const {
    std::string result = "Machine{";
    result += topology_.canonicalState();
    result += pasidTable_.canonicalState();
    for (const auto& [domain, table] : addressSpaces_) {
        result += "{domain=";
        result += std::to_string(domain);
        result += table.canonicalState();
        result += "}";
    }
    result += pool_.canonicalState();
    result += iotlb_.canonicalState();
    for (const auto& [bdf, cache] : atsCaches_) {
        result += bdf.toString();
        result += cache.canonicalState();
    }
    for (const ReclaimTicket& ticket : tickets_) {
        result += ticket.canonicalState();
    }
    result += invalidationQueue_.canonicalState();
    result += "}";
    return result;
}

} // namespace iommu_ats
