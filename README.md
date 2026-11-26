# IOMMU/ATS Isolation and Invalidation-Order Verifier

A zero-external-dependency C++17 library for modeling and searching IOMMU
isolation failures caused by stale IOTLB/ATS translations, incomplete
invalidations, and PCIe requester aliases.

The library builds under:

```bash
cmake -S . -B build
cmake --build build -j
ctest --test-dir build --output-on-failure
./build/iommu_ats_search_demo
```

Targets use `-Wall -Wextra -Wpedantic -Wconversion -Werror`.

## Modeled State

- Multiple PCIe BDF requesters and explicit alias groups.
- Per-BDF/PASID to domain resolution through `PasidDomainTable`.
- Per-domain 4-level page tables with 4 KiB, 2 MiB, and 1 GiB leaves.
- Read/write/execute permissions and valid physical-address bits for root,
  intermediate tables, and leaf entries.
- One IOMMU IOTLB (`TranslationCache`) and one ATS Device-TLB per BDF.
- Asynchronous invalidation commands with queued/completed states:
  `InvalidateIOTLB` -> `InvalidateDeviceATC` -> `InvalidationWait`.
- Host physical-frame ownership and allocation generations.
- Online event interception through `IEventInterceptor`, including an
  `OnlineSafetyInterceptor`.
- A standalone `AtsOrderVerifier` for event logs and alias coverage.

## Safe Reclamation Sequence

A physical frame may be returned to the free pool and reallocated only after:

1. The current page-table leaf is unmapped.
2. The domain IOTLB invalidation has completed.
3. Device-ATC invalidation has completed for every associated BDF and every
   alias BDF in its `AliasGroup`.
4. The `InvalidationWait` barrier has passed.

`Machine::safeRelease` and `Machine::safeReleaseAndReallocate` enforce those
conditions and also scan for residual overlapping IOTLB/ATC entries. The
low-level `unsafeReleaseAndReallocate` method intentionally models a buggy
hypervisor path for the concurrency searcher.

Every DMA is audited against the current page table and all permitting stale
IOTLB and ATC hits. A stale hit is a security violation when its physical page
is no longer owned by the requesting domain, for example after reallocation to
the kernel or another tenant.

## Bounded Interleaving Search

`InterleavingSearcher::search` performs BFS over canonical machine states and
returns the shortest action sequence, capped at eight steps.
`searchInterleavings` preserves per-thread program order while exploring true
atomic-step interleavings.

The supplied canonical scenario contains BDFs `01:00.0` and `01:00.1` in one
alias group, PASID `1`, domain `7`, one RW page, and primed IOTLB/ATC state.
The broad search finds:

```text
Unmap -> ReallocateKernel -> DmaMain
```

Constrained searches also find:

```text
Unmap -> InvalidateIOTLB -> ReallocateKernel -> DmaMain
Unmap -> InvalidateIOTLB -> InvalidateMainATC -> ReallocateKernel -> DmaAlias
Unmap -> InvalidateIOTLB -> InvalidateMainATC -> InvalidationWait
       -> ReallocateKernel -> DmaAlias
```

The last sequence issues `InvalidationWait` but recycles after issuance rather
than completion and omits the alias ATC. The thread interleaver finds the
four-step IOTLB-only reclaimer race.

## Reference Oracle

`ReferenceModel.hpp` contains an independent tiny address-space model with one
IOVA page, one physical frame, a main BDF, and an alias BDF.
`exhaustivelyEnumerateReference(8)` enumerates all
`19,173,960` non-empty action sequences through depth eight and checks that the
shortest reference violation agrees with the machine search.

## Embedding Example

```cpp
#include "iommu_ats/iommu_ats.hpp"

using namespace iommu_ats;

Machine machine = canonical::makePrimedMachine();
const Bdf main = canonical::mainBdf();
const Bdf alias = canonical::aliasBdf();

TicketId ticket = machine.unmapAndOpenTicket(
    main, canonical::PASID, canonical::IOVA, PAGE_SIZE_4K);

machine.invalidateIotlbNow(ticket);
machine.invalidateAtsNow(ticket, main);
machine.invalidateAtsNow(ticket, alias);
machine.waitBarrierNow(ticket);

PhysicalAddress pa = 0;
std::vector<std::string> reasons;
bool ok = machine.safeReleaseAndReallocate(
    ticket, PageOwner{PageOwnerKind::Kernel, 0}, &pa, &reasons);
```

Include `include/` and link the `iommu_ats` CMake target, or compile the
`src/*.cpp` files directly. There are no third-party dependencies.
