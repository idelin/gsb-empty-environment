#pragma once

#include "iommu_ats/Types.hpp"

#include <string>
#include <vector>

namespace iommu_ats {

struct CacheEntry {
    DomainId domain = 0;
    Pasid pasid = 0;
    Iova iovaBase = 0;
    u64 size = PAGE_SIZE_4K;
    PhysicalAddress physicalBase = 0;
    AccessPermissions permissions = AccessPermissions::none();
    TicketId ticketId = 0;

    [[nodiscard]] constexpr Iova iovaEnd() const noexcept {
        return iovaBase + size;
    }

    [[nodiscard]] constexpr bool covers(Iova address) const noexcept {
        return address >= iovaBase && address < iovaEnd();
    }

    [[nodiscard]] constexpr bool overlaps(Iova start,
                                          u64 length) const noexcept {
        const Iova end = start + length;
        return start < iovaEnd() && iovaBase < end;
    }

    [[nodiscard]] constexpr bool permits(DmaOp op) const noexcept {
        return permissions.allows(op);
    }
};

struct CacheHit {
    std::size_t index = 0;
    CacheEntry entry;
};

class TranslationCache {
public:
    explicit TranslationCache(std::string name = "cache");

    void insert(CacheEntry entry);
    [[nodiscard]] std::vector<CacheHit> lookup(DomainId domain, Pasid pasid,
                                               Iova iova, u64 length,
                                               DmaOp op) const;
    [[nodiscard]] std::vector<CacheHit> lookupOverlap(
        DomainId domain, Pasid pasid, Iova iova, u64 length) const;

    std::size_t invalidateDomain(DomainId domain);
    std::size_t invalidatePasid(DomainId domain, Pasid pasid);
    std::size_t invalidateRange(DomainId domain, Pasid pasid, Iova iovaBase,
                                u64 size);
    std::size_t invalidateTicket(TicketId ticketId);
    void clear();

    [[nodiscard]] const std::vector<CacheEntry>& entries() const noexcept;
    [[nodiscard]] std::size_t size() const noexcept;
    [[nodiscard]] bool empty() const noexcept;
    [[nodiscard]] std::string canonicalState() const;

private:
    std::string name_;
    std::vector<CacheEntry> entries_;
};

} // namespace iommu_ats
