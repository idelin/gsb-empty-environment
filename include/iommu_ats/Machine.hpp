#pragma once

#include "iommu_ats/HostPagePool.hpp"
#include "iommu_ats/Interceptor.hpp"
#include "iommu_ats/Invalidation.hpp"
#include "iommu_ats/PageTable.hpp"
#include "iommu_ats/Topology.hpp"
#include "iommu_ats/TranslationCache.hpp"
#include "iommu_ats/Types.hpp"

#include <map>
#include <string>
#include <vector>

namespace iommu_ats {

struct StaleTranslationFinding {
    enum class Layer {
        Iotlb,
        Ats,
    };

    Layer layer = Layer::Iotlb;
    Bdf bdf{};
    CacheEntry entry;
    PhysicalAddress observedPhysicalAddress = 0;
    bool currentPageTableMatches = false;
    bool permitsRequestedAccess = false;
    PageOwner owner;
    bool unauthorized = false;
    std::string explanation;
};

struct DmaResult {
    bool domainResolved = false;
    DomainId domain = 0;
    bool hardwareGranted = false;
    bool violation = false;
    bool usedIotlb = false;
    bool usedAts = false;
    std::vector<PhysicalAddress> accessedPhysicalAddresses;
    std::vector<StaleTranslationFinding> staleFindings;
    RangeTranslation currentTranslation;
    std::vector<std::string> messages;

    [[nodiscard]] bool staleIotlbHit() const;
    [[nodiscard]] bool staleAtsHit() const;
};

class Machine {
public:
    Machine();

    [[nodiscard]] AliasTopology& topology() noexcept;
    [[nodiscard]] const AliasTopology& topology() const noexcept;
    [[nodiscard]] PasidDomainTable& pasidTable() noexcept;
    [[nodiscard]] const PasidDomainTable& pasidTable() const noexcept;
    [[nodiscard]] HostPagePool& pool() noexcept;
    [[nodiscard]] const HostPagePool& pool() const noexcept;
    [[nodiscard]] EventBus& events() noexcept;

    PageTable& createAddressSpace(
        DomainId domain,
        PhysicalAddress rootPhysicalAddress = 0);
    [[nodiscard]] PageTable* addressSpace(DomainId domain);
    [[nodiscard]] const PageTable* addressSpace(DomainId domain) const;

    bool mapPage(DomainId domain, Iova iova, PhysicalAddress pa, u64 size,
                 AccessPermissions permissions, std::string* error = nullptr);

    TranslationCache& iotlb() noexcept;
    TranslationCache& atsCache(Bdf bdf);
    [[nodiscard]] const std::map<Bdf, TranslationCache>& atsCaches()
        const noexcept;

    TicketId unmapAndOpenTicket(Bdf requester, Pasid pasid, Iova iova,
                                 u64 size);
    [[nodiscard]] ReclaimTicket* ticket(TicketId id);
    [[nodiscard]] const ReclaimTicket* ticket(TicketId id) const;
    [[nodiscard]] std::vector<TicketId> ticketIds() const;

    CommandId submitIotlbInvalidation(TicketId ticketId);
    CommandId submitAtsInvalidation(TicketId ticketId, Bdf bdf);
    CommandId submitWaitBarrier(TicketId ticketId);
    bool completeCommand(CommandId commandId);
    bool invalidateIotlbNow(TicketId ticketId,
                            CommandId* commandIdOut = nullptr);
    bool invalidateAtsNow(TicketId ticketId, Bdf bdf,
                          CommandId* commandIdOut = nullptr);
    bool waitBarrierNow(TicketId ticketId);

    [[nodiscard]] std::vector<std::string> releaseReadiness(
        TicketId ticketId) const;
    bool safeRelease(TicketId ticketId,
                     std::vector<std::string>* reasons = nullptr);
    bool safeReleaseAndReallocate(TicketId ticketId, PageOwner newOwner,
                                  PhysicalAddress* allocatedPa = nullptr,
                                  std::vector<std::string>* reasons = nullptr);
    bool unsafeReleaseAndReallocate(TicketId ticketId,
                                    PageOwner newOwner);

    DmaResult executeDma(const DmaRequest& request);

    [[nodiscard]] InvalidationQueue& invalidationQueue() noexcept;
    [[nodiscard]] const InvalidationQueue& invalidationQueue() const noexcept;
    [[nodiscard]] std::string canonicalState() const;

private:
    AliasTopology topology_;
    PasidDomainTable pasidTable_;
    HostPagePool pool_;
    EventBus eventBus_;
    std::map<DomainId, PageTable> addressSpaces_;
    TranslationCache iotlb_;
    std::map<Bdf, TranslationCache> atsCaches_;
    std::vector<ReclaimTicket> tickets_;
    InvalidationQueue invalidationQueue_;
    TicketId nextTicketId_ = 1;

    [[nodiscard]] ReclaimTicket* findTicket(TicketId id);
    [[nodiscard]] const ReclaimTicket* findTicket(TicketId id) const;
    [[nodiscard]] StaleTranslationFinding classifyCacheEntry(
        DomainId domain, Pasid pasid, Iova iova, u64 length,
        Bdf bdf, StaleTranslationFinding::Layer layer,
        const CacheEntry& entry, DmaOp op) const;
    void auditLayer(const DmaRequest& request, DomainId domain,
                    StaleTranslationFinding::Layer layer, Bdf bdf,
                    const TranslationCache& cache, DmaResult& result) const;
    void touchPhysicalRange(PhysicalAddress start, u64 length,
                            DomainId writer, Bdf bdf);
    void fillCachesFromSegment(Bdf bdf, Pasid pasid, DomainId domain,
                               const TranslationSegment& segment);
};

using Iommu = Machine;
using IommuModel = Machine;

} // namespace iommu_ats
