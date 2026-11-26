#pragma once

#include "iommu_ats/Types.hpp"

#include <map>
#include <optional>
#include <string>
#include <vector>

namespace iommu_ats {

enum class PageOwnerKind {
    Free,
    Guest,
    Kernel,
    OtherDomain,
};

struct PageOwner {
    PageOwnerKind kind = PageOwnerKind::Free;
    DomainId domain = 0;

    [[nodiscard]] constexpr bool operator==(
        const PageOwner& other) const noexcept {
        return kind == other.kind && domain == other.domain;
    }
};

struct PhysicalFrame {
    PhysicalAddress physicalBase = 0;
    u64 size = PAGE_SIZE_4K;
    PageOwner owner;
    u64 allocationGeneration = 0;
    u64 writeCount = 0;
    DomainId lastWriterDomain = 0;
    Bdf lastWriterBdf{};
};

class HostPagePool {
public:
    void addFrame(PhysicalAddress physicalBase,
                  u64 size = PAGE_SIZE_4K,
                  PageOwner owner = {PageOwnerKind::Kernel, 0});

    [[nodiscard]] const PhysicalFrame* frame(PhysicalAddress pa) const;
    [[nodiscard]] PhysicalFrame* frame(PhysicalAddress pa);
    [[nodiscard]] PageOwner owner(PhysicalAddress pa) const;

    bool releaseToFreePool(PhysicalAddress pa);
    std::optional<PhysicalAddress> allocate(PageOwner owner);
    std::optional<PhysicalAddress> allocateSpecific(PhysicalAddress pa,
                                                    PageOwner owner);

    void recordWrite(PhysicalAddress pa, DomainId writer, Bdf bdf);

    [[nodiscard]] bool isOwnedByRequester(PhysicalAddress pa,
                                          DomainId domain) const;
    [[nodiscard]] std::string canonicalState() const;

private:
    std::map<PhysicalAddress, PhysicalFrame> frames_;
    std::vector<PhysicalAddress> freeLifo_;
    u64 nextGeneration_ = 1;
};

} // namespace iommu_ats
