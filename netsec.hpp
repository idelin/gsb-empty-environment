#pragma once

#include <array>
#include <cstdint>
#include <optional>
#include <vector>

namespace netsec {

// virtio vring descriptor flags
inline constexpr std::uint16_t kVringDescFNext = 1;
inline constexpr std::uint16_t kVringDescFWrite = 2;
inline constexpr std::uint16_t kVringDescFIndirect = 4;

inline constexpr std::uint32_t kVringDescSize = 16;
inline constexpr std::size_t kMaxMemoryRegions = 8;
inline constexpr std::uint64_t kMaxChainTotalBytes = std::uint64_t{1} << 30;

// One entry of the SET_MEM_TABLE payload.
struct MemoryRegion {
    std::uint64_t guest_phys_addr;
    std::uint64_t memory_size;
    std::uint64_t userspace_addr;
    std::uint64_t mmap_offset;
};

enum class MemTableError {
    kTooManyRegions,
    kZeroSizeRegion,
    kRegionWrapAround,
    kOverlappingRegions,
};

enum class TranslateError {
    kZeroLength,
    kWrapAround,
    kOutOfRegion,
};

struct TranslateResult {
    std::uint64_t host_addr = 0;
    std::optional<TranslateError> error;
    bool ok() const { return !error.has_value(); }
};

// Validated guest-phys -> host mapping built from SET_MEM_TABLE.
// Thread-compatible: setRegions() must not race with translate().
class GuestMemoryMap {
public:
    // Replaces the region table. On success every cached translation is
    // invalidated immediately (generation bump + cache flush). On failure
    // the previous table stays active.
    std::optional<MemTableError> setRegions(std::vector<MemoryRegion> regions);

    // Translates [gpa, gpa+len) to a host address. The whole range must
    // fall inside a single region; len == 0 and gpa+len wrap-around are
    // rejected.
    TranslateResult translate(std::uint64_t gpa, std::uint64_t len) const;

    const std::vector<MemoryRegion>& regions() const { return regions_; }
    std::uint64_t generation() const { return generation_; }

private:
    struct CacheEntry {
        std::uint64_t generation = 0;
        std::uint64_t gpa = 0;
        std::uint64_t len = 0;
        std::uint64_t host_addr = 0;
        bool valid = false;
    };

    TranslateResult translateUncached(std::uint64_t gpa, std::uint64_t len) const;

    std::vector<MemoryRegion> regions_;  // sorted by guest_phys_addr
    std::uint64_t generation_ = 0;
    mutable std::array<CacheEntry, 8> cache_{};
};

struct VringDesc {
    std::uint64_t addr;
    std::uint32_t len;
    std::uint16_t flags;
    std::uint16_t next;
};
static_assert(sizeof(VringDesc) == kVringDescSize, "vring descriptor must be 16 bytes");

enum class ChainError {
    kNone,
    kBadIndex,             // head or next index >= queue size
    kChainTooLong,         // descriptor count exceeds queue size
    kCycle,                // same descriptor index visited twice
    kBadIndirectLength,    // indirect table length not valid
    kNestedIndirect,       // INDIRECT flag inside an indirect table
    kTotalLengthExceeded,  // cumulative chain length > 2^30
    kBadDescriptorAddress, // descriptor payload failed translation
};

struct ChainSegment {
    std::uint64_t host_addr;
    std::uint32_t len;
    bool write;
};

struct ChainResult {
    ChainError error = ChainError::kNone;
    std::vector<ChainSegment> segments;
    bool ok() const { return error == ChainError::kNone; }
};

// Walks vring descriptor chains for one virtqueue against a GuestMemoryMap.
class Virtqueue {
public:
    Virtqueue(const GuestMemoryMap& memory, std::uint16_t queue_size)
        : memory_(memory), queue_size_(queue_size) {}

    std::uint16_t queue_size() const { return queue_size_; }

    // Follows the chain starting at desc_table[head], translating every
    // descriptor payload. Returns the first violation encountered.
    ChainResult walkChain(const VringDesc* desc_table, std::uint16_t head) const;

private:
    const GuestMemoryMap& memory_;
    std::uint16_t queue_size_;
};

const char* toString(MemTableError error);
const char* toString(TranslateError error);
const char* toString(ChainError error);

}  // namespace netsec
