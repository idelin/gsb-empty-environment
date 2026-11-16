#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <unordered_map>
#include <vector>

namespace netsec {

// One entry of the SET_MEM_TABLE payload.
struct MemoryRegion {
  uint64_t guest_phys_addr;
  uint64_t memory_size;
  uint64_t userspace_addr;
  uint64_t mmap_offset;
};

constexpr std::size_t kMaxRegions = 8;
constexpr uint64_t kMaxChainTotalLen = 1ull << 30;  // 2^30

enum class MemTableError {
  kNone,
  kTooManyRegions,
  kZeroSizeRegion,
  kRegionWrapAround,
  kOverlappingRegions,
};

enum class TranslateError {
  kNone,
  kZeroLength,
  kWrapAround,
  kOutOfRange,  // no single region covers [gpa, gpa+len)
};

// Guest physical address -> backend userspace address translation,
// driven by the vhost-user SET_MEM_TABLE region list.
class GuestMemTable {
 public:
  // Replaces the region table. On success all cached translations are
  // invalidated immediately. On failure the previous table is kept.
  MemTableError set_regions(const std::vector<MemoryRegion>& regions);

  // Translates (gpa, len) to a userspace address. The whole range must
  // fall inside a single region; len == 0 and gpa + len wrap-around are
  // rejected.
  std::optional<uint64_t> translate(uint64_t gpa, uint64_t len) const;
  std::optional<uint64_t> translate(uint64_t gpa, uint64_t len,
                                    TranslateError& err) const;

  uint64_t generation() const { return generation_; }
  std::size_t region_count() const { return regions_.size(); }
  std::size_t cache_size() const { return cache_.size(); }

 private:
  struct CacheEntry {
    uint64_t len;
    uint64_t host_addr;
  };

  std::vector<MemoryRegion> regions_;
  uint64_t generation_ = 0;
  mutable std::unordered_map<uint64_t, CacheEntry> cache_;  // key: gpa
};

// Virtio split-ring descriptor (16 bytes).
struct Descriptor {
  uint64_t addr;
  uint32_t len;
  uint16_t flags;
  uint16_t next;
};
static_assert(sizeof(Descriptor) == 16, "descriptor must be 16 bytes");

constexpr uint16_t kDescFNext = 1;
constexpr uint16_t kDescFWrite = 2;
constexpr uint16_t kDescFIndirect = 4;

enum class ChainError {
  kNone,
  kHeadOutOfRange,
  kIndexOutOfRange,
  kChainTooLong,
  kCycle,
  kIndirectWithNext,
  kBadIndirectLength,
  kNestedIndirect,
  kTotalLengthTooLarge,
  kAddressTranslationFailed,
};

struct ChainResult {
  std::vector<Descriptor> descriptors;  // indirect tables expanded in place
  uint64_t total_length = 0;
};

// Walks descriptor chains of one virtqueue, validating every descriptor
// and every buffer address against the guest memory table.
class VirtqueueWalker {
 public:
  VirtqueueWalker(const GuestMemTable& mem, uint64_t desc_table_gpa,
                  uint16_t queue_size);

  std::optional<ChainResult> walk(uint16_t head, ChainError& err) const;

 private:
  const Descriptor* desc_at(uint16_t index) const;

  const GuestMemTable& mem_;
  uint64_t desc_table_gpa_;
  uint16_t queue_size_;
};

}  // namespace netsec
