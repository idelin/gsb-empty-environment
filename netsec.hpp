#pragma once

#include <cstddef>
#include <cstdint>
#include <shared_mutex>
#include <unordered_map>
#include <unordered_set>
#include <vector>

namespace netsec {

// PartitionAllocator: type-domain isolated heap partition allocator.
//
//  - Every allocation is tagged with a caller-provided domain id.
//  - Small blocks live on single-page slabs; a slab page never mixes domains.
//  - Freed blocks enter a per-slab quarantine queue and are only reused
//    inside the same domain after the quarantine drains (FIFO).
//  - destroyDomain() zeroes every page of the domain; recycled pages are
//    re-assigned with a fresh generation, so stale PageOwner tokens never
//    match the new owner.
//  - Oversized allocations are served by a dedicated mmap extent whose
//    pages are all registered to the owning domain.
class PartitionAllocator {
public:
  struct OwnerInfo {
    uint32_t domain = 0;
    uint64_t generation = 0;
    bool valid = false;
    bool operator==(const OwnerInfo& o) const {
      return domain == o.domain && generation == o.generation &&
             valid == o.valid;
    }
    bool operator!=(const OwnerInfo& o) const { return !(*this == o); }
  };

  static constexpr size_t kPageSize = 4096;
  static constexpr size_t kMaxSmallSize = 2048;
  static constexpr size_t kQuarantineCapacity = 16;

  PartitionAllocator() = default;
  ~PartitionAllocator();
  PartitionAllocator(const PartitionAllocator&) = delete;
  PartitionAllocator& operator=(const PartitionAllocator&) = delete;

  uint32_t createDomain();
  void destroyDomain(uint32_t domain);

  void* alloc(uint32_t domain, size_t size);
  void free(void* ptr);

  // Owner of the page containing `ptr`; works for any interior offset of a
  // live allocation.
  OwnerInfo pageOwner(const void* ptr) const;

  // Test introspection.
  size_t debugRecycledPageCount() const;
  size_t debugLivePageCount() const;

private:
  struct Slab {
    uint32_t domain = 0;
    size_t blockSize = 0;
    void* freeHead = nullptr;
    std::vector<void*> quarantine;  // FIFO isolation queue
  };

  struct PageInfo {
    uint32_t domain = 0;
    uint64_t generation = 0;
    Slab* slab = nullptr;        // set for slab pages
    bool large = false;          // true for direct-mmap extents
    void* extentBase = nullptr;  // large extents: base of mapping
    size_t extentSize = 0;       // large extents: mapping length
  };

  struct Domain {
    std::unordered_set<uint64_t> slabPages;  // page indices owned
    std::unordered_map<size_t, std::vector<Slab*>> slabsByClass;
    std::vector<std::pair<void*, size_t>> larges;  // live mmap extents
  };

  static size_t sizeClassFor(size_t size);

  void* allocSmall(Domain& dom, uint32_t domain, size_t sizeClass);
  void* allocLarge(Domain& dom, uint32_t domain, size_t size);
  Slab* newSlab(Domain& dom, uint32_t domain, size_t sizeClass);
  void* acquirePage(uint64_t* generationOut);
  void freeLargeLocked(const PageInfo& info);

  mutable std::shared_mutex mutex_;
  std::unordered_map<uint64_t, PageInfo> pages_;  // page index -> info
  std::unordered_map<uint32_t, Domain> domains_;
  std::vector<void*> recycledPages_;  // zeroed pages awaiting a new domain
  std::vector<std::pair<void*, size_t>> extents_;  // slab backing extents
  char* extentCursor_ = nullptr;
  size_t extentRemaining_ = 0;
  uint32_t nextDomainId_ = 1;
  uint64_t nextGeneration_ = 1;

  static constexpr size_t kExtentSize = 4ull << 20;  // 4 MiB
};

}  // namespace netsec
