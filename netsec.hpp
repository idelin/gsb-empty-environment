#pragma once

#include <cstddef>
#include <cstdint>
#include <limits>
#include <memory>

namespace netsec {

// A caller-chosen type domain.  Domain ids are opaque to the allocator.
using DomainId = std::uint32_t;

// Returned by PageOwner() when the address is not owned by this allocator.
inline constexpr DomainId kNoDomain = std::numeric_limits<DomainId>::max();

struct AllocatorConfig {
    // Size of one slab.  Must be a power of two, at least two pages, and
    // large enough for large_threshold.
    std::size_t slab_size = 64u * 1024u;

    // Requests larger than this (or aligned beyond a page) use a direct
    // mmap rather than a slab.
    std::size_t large_threshold = 16u * 1024u;

    // Smallest slab block size.  Must be a power of two and large enough to
    // hold the allocator's intrusive free-list node.
    std::size_t min_block_size = 16;

    // Per-domain isolation queue.  Freed blocks are held here before they
    // may return to the domain's free list.
    std::size_t quarantine_blocks = 64;
    std::size_t quarantine_bytes = 64u * 1024u;
    std::size_t quarantine_ops = 64;

    // Overwrite freed block contents while they wait in quarantine.
    bool scrub_on_free = true;
};

enum class AllocationKind : std::uint8_t {
    None = 0,
    Slab = 1,
    LargeMmap = 2,
};

struct AllocatorStats {
    std::size_t domain_count = 0;
    std::size_t live_blocks = 0;
    std::size_t live_bytes = 0;
    std::size_t slab_count = 0;
    std::size_t slab_mapped_bytes = 0;
    std::size_t large_live_blocks = 0;
    std::size_t large_live_bytes = 0;
    std::size_t large_mapped_bytes = 0;
    std::size_t quarantined_blocks = 0;
    std::size_t quarantined_bytes = 0;
    std::uint64_t epoch = 0;
};

struct DomainStats {
    std::size_t live_blocks = 0;
    std::size_t live_bytes = 0;
    std::size_t slab_count = 0;
    std::size_t slab_mapped_bytes = 0;
    std::size_t large_live_blocks = 0;
    std::size_t large_live_bytes = 0;
    std::size_t quarantined_blocks = 0;
    std::size_t quarantined_bytes = 0;
};

// A partitioned, quarantining allocator for embedding in a service.
//
// - Every slab mapping belongs to exactly one domain and one size class.
// - A freed slab block can only return to the domain that owns its slab.
// - Freed blocks wait in a per-domain FIFO isolation queue.
// - Large/over-aligned requests bypass slabs and use mmap directly.
//
// The class is thread-safe for concurrent Allocate/Deallocate/queries.  It is
// not an operator new replacement: slab metadata uses the normal C++ heap.
class PartitionedAllocator {
public:
    explicit PartitionedAllocator(AllocatorConfig config = {});
    ~PartitionedAllocator();

    PartitionedAllocator(const PartitionedAllocator&) = delete;
    PartitionedAllocator& operator=(const PartitionedAllocator&) = delete;
    PartitionedAllocator(PartitionedAllocator&&) = delete;
    PartitionedAllocator& operator=(PartitionedAllocator&&) = delete;

    // Allocate size bytes with the requested power-of-two alignment.  Returns
    // nullptr on failure or for invalid arguments.  size == 0 is treated as 1.
    void* Allocate(std::size_t size, std::size_t alignment, DomainId domain);
    void* Allocate(std::size_t size, DomainId domain) {
        return Allocate(size, alignof(std::max_align_t), domain);
    }

    // Release an exact block pointer.  Returns false and does nothing for
    // unknown, interior, double-freed, or quarantined pointers.
    bool Deallocate(void* ptr);

    // Domain that owns the page containing ptr.  This includes slab blocks,
    // large mappings, and mappings retained in quarantine.  Returns
    // kNoDomain for null, foreign, or unmapped addresses.  Never dereferences
    // ptr, so querying a stale address is safe as long as it is not
    // concurrently unmapped by another thread.
    DomainId PageOwner(const void* ptr) const;

    // True if ptr lies anywhere inside an allocator-owned mapping.
    bool Owns(const void* ptr) const;

    // True if ptr is the exact base of a currently live allocation.
    bool IsAllocated(const void* ptr) const;

    // True if ptr is an exact block base currently in a domain quarantine.
    bool IsQuarantined(const void* ptr) const;

    // How ptr is backed, if this allocator owns it.
    AllocationKind KindOf(const void* ptr) const;

    AllocatorStats Stats() const;
    DomainStats StatsFor(DomainId domain) const;

    const AllocatorConfig& Config() const noexcept;

    static std::size_t PageSize();

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

}  // namespace netsec
