#pragma once

#include "iommu_ats/InterleavingSearch.hpp"

namespace iommu_ats::canonical {

inline constexpr DomainId GUEST_DOMAIN_ID = 7;
inline constexpr Pasid PASID = 1;
inline constexpr Iova IOVA = 0x00010000ULL;
inline constexpr PhysicalAddress GUEST_PAGE_PA = 0x00100000ULL;
inline constexpr PhysicalAddress KERNEL_PAGE_PA = 0x00200000ULL;
inline constexpr u32 DMA_LENGTH = 4;

[[nodiscard]] Bdf mainBdf();
[[nodiscard]] Bdf aliasBdf();
[[nodiscard]] DmaRequest writeFrom(Bdf bdf);

// Two BDFs in one alias group, one RW guest page, and IOTLB/ATC entries
// primed by a legal read.
[[nodiscard]] Machine makePrimedMachine();
[[nodiscard]] std::vector<SearchAction> makeActions();

// A faulty reclaimer that waits for IOTLB completion, but then recycles
// without Device-ATC invalidation or the wait barrier.
[[nodiscard]] std::vector<SearchAction> makeIotlbOnlyActions();
[[nodiscard]] std::vector<ThreadProgram> makeIotlbRaceThreads();
[[nodiscard]] std::vector<ThreadProgram> makeSafeVsDmaThreads();

// A faulty alias-aware? It invalidates only the primary device ATC.
[[nodiscard]] std::vector<SearchAction> makePrimaryOnlyAliasActions();

// Completes IOTLB and primary ATC, issues InvalidationWait, but recycles
// after wait issuance without its completion and without alias ATC.
[[nodiscard]] std::vector<SearchAction> makeIssuedWaitAliasRaceActions();

} // namespace iommu_ats::canonical
