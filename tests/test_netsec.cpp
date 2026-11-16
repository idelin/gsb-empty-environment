#include <cstdio>
#include <cstdlib>
#include <cstring>

#include "netsec.hpp"

namespace {

int g_failures = 0;

#define CHECK(cond)                                              \
  do {                                                           \
    if (!(cond)) {                                               \
      std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
      ++g_failures;                                              \
    }                                                            \
  } while (0)

using netsec::ChainError;
using netsec::Descriptor;
using netsec::GuestMemTable;
using netsec::MemTableError;
using netsec::MemoryRegion;
using netsec::TranslateError;
using netsec::VirtqueueWalker;

constexpr uint64_t kGpaA = 0x10000;
constexpr uint64_t kGpaB = 0x20000;
constexpr uint64_t kRegionSize = 0x1000;

uint8_t g_region_a[kRegionSize];
uint8_t g_region_b[kRegionSize];

MemoryRegion RegionA() {
  return MemoryRegion{kGpaA, kRegionSize,
                      reinterpret_cast<uint64_t>(&g_region_a[0]), 0};
}

MemoryRegion RegionB() {
  return MemoryRegion{kGpaB, kRegionSize,
                      reinterpret_cast<uint64_t>(&g_region_b[0]), 0};
}

Descriptor* RingDesc(uint16_t index) {
  return reinterpret_cast<Descriptor*>(&g_region_a[0]) + index;
}

void TestTranslateBasic() {
  GuestMemTable mem;
  CHECK(mem.set_regions({RegionA(), RegionB()}) == MemTableError::kNone);

  const auto host = mem.translate(kGpaA + 0x40, 0x20);
  CHECK(host.has_value());
  CHECK(*host == reinterpret_cast<uint64_t>(&g_region_a[0]) + 0x40);

  // Range ending exactly at the region boundary is valid.
  CHECK(mem.translate(kGpaA, kRegionSize).has_value());
}

void TestTranslateRejects() {
  GuestMemTable mem;
  CHECK(mem.set_regions({RegionA(), RegionB()}) == MemTableError::kNone);
  TranslateError err = TranslateError::kNone;

  CHECK(!mem.translate(kGpaA, 0, err).has_value());
  CHECK(err == TranslateError::kZeroLength);

  // gpa + len wraps around the 64-bit address space.
  CHECK(!mem.translate(UINT64_MAX - 3, 8, err).has_value());
  CHECK(err == TranslateError::kWrapAround);

  // Crosses from region A into the (adjacent-gpa) gap / region B.
  CHECK(!mem.translate(kGpaA + kRegionSize - 8, 16, err).has_value());
  CHECK(err == TranslateError::kOutOfRange);

  // One byte past the end of a region.
  CHECK(!mem.translate(kGpaA, kRegionSize + 1, err).has_value());

  // No region at all.
  CHECK(!mem.translate(0x90000, 16, err).has_value());
  CHECK(err == TranslateError::kOutOfRange);
}

void TestRegionTableValidation() {
  GuestMemTable mem;

  std::vector<MemoryRegion> too_many(9, RegionA());
  CHECK(mem.set_regions(too_many) == MemTableError::kTooManyRegions);

  MemoryRegion zero = RegionA();
  zero.memory_size = 0;
  CHECK(mem.set_regions({zero}) == MemTableError::kZeroSizeRegion);

  MemoryRegion wrap = RegionA();
  wrap.guest_phys_addr = UINT64_MAX - 0x10;
  wrap.memory_size = 0x100;
  CHECK(mem.set_regions({wrap}) == MemTableError::kRegionWrapAround);

  MemoryRegion overlap = RegionA();
  overlap.guest_phys_addr = kGpaA + 0x800;
  CHECK(mem.set_regions({RegionA(), overlap}) ==
        MemTableError::kOverlappingRegions);

  // Failed updates keep the previous table untouched.
  CHECK(mem.set_regions({RegionA()}) == MemTableError::kNone);
  CHECK(mem.set_regions({RegionA(), overlap}) ==
        MemTableError::kOverlappingRegions);
  CHECK(mem.translate(kGpaA, 16).has_value());
}

void TestCacheInvalidation() {
  GuestMemTable mem;
  CHECK(mem.set_regions({RegionA()}) == MemTableError::kNone);

  const auto before = mem.translate(kGpaA + 0x10, 0x10);
  CHECK(before.has_value());
  CHECK(mem.cache_size() == 1);
  const uint64_t gen_before = mem.generation();

  // Same gpa now mapped to region B's backing buffer.
  MemoryRegion remapped = RegionA();
  remapped.userspace_addr = reinterpret_cast<uint64_t>(&g_region_b[0]);
  CHECK(mem.set_regions({remapped}) == MemTableError::kNone);
  CHECK(mem.cache_size() == 0);  // stale cache dropped immediately
  CHECK(mem.generation() > gen_before);

  const auto after = mem.translate(kGpaA + 0x10, 0x10);
  CHECK(after.has_value());
  CHECK(*after == reinterpret_cast<uint64_t>(&g_region_b[0]) + 0x10);
  CHECK(*after != *before);

  // A gpa valid under the old table but invalid now must fail.
  CHECK(mem.set_regions({RegionA(), RegionB()}) == MemTableError::kNone);
  CHECK(mem.translate(kGpaB, 0x10).has_value());
  CHECK(mem.set_regions({RegionA()}) == MemTableError::kNone);
  CHECK(!mem.translate(kGpaB, 0x10).has_value());
}

GuestMemTable* MakeRingMem() {
  static GuestMemTable mem;
  std::memset(g_region_a, 0, sizeof(g_region_a));
  std::memset(g_region_b, 0, sizeof(g_region_b));
  CHECK(mem.set_regions({RegionA(), RegionB()}) == MemTableError::kNone);
  return &mem;
}

void TestChainBasic() {
  GuestMemTable* mem = MakeRingMem();
  constexpr uint16_t kQueueSize = 8;
  *RingDesc(0) = Descriptor{kGpaB, 0x100, netsec::kDescFNext, 1};
  *RingDesc(1) = Descriptor{kGpaB + 0x100, 0x80, 0, 0};

  VirtqueueWalker walker(*mem, kGpaA, kQueueSize);
  ChainError err = ChainError::kNone;
  const auto result = walker.walk(0, err);
  CHECK(result.has_value());
  CHECK(err == ChainError::kNone);
  CHECK(result->descriptors.size() == 2);
  CHECK(result->total_length == 0x180);

  CHECK(!walker.walk(kQueueSize, err).has_value());
  CHECK(err == ChainError::kHeadOutOfRange);
}

void TestChainCycle() {
  GuestMemTable* mem = MakeRingMem();
  constexpr uint16_t kQueueSize = 8;
  *RingDesc(0) = Descriptor{kGpaB, 0x10, netsec::kDescFNext, 1};
  *RingDesc(1) = Descriptor{kGpaB, 0x10, netsec::kDescFNext, 0};  // loop

  VirtqueueWalker walker(*mem, kGpaA, kQueueSize);
  ChainError err = ChainError::kNone;
  CHECK(!walker.walk(0, err).has_value());
  CHECK(err == ChainError::kCycle);

  // Self-loop.
  *RingDesc(2) = Descriptor{kGpaB, 0x10, netsec::kDescFNext, 2};
  CHECK(!walker.walk(2, err).has_value());
  CHECK(err == ChainError::kCycle);
}

void TestChainIndexOutOfRange() {
  GuestMemTable* mem = MakeRingMem();
  constexpr uint16_t kQueueSize = 8;
  *RingDesc(0) = Descriptor{kGpaB, 0x10, netsec::kDescFNext, 100};

  VirtqueueWalker walker(*mem, kGpaA, kQueueSize);
  ChainError err = ChainError::kNone;
  CHECK(!walker.walk(0, err).has_value());
  CHECK(err == ChainError::kIndexOutOfRange);
}

void TestChainBadBuffer() {
  GuestMemTable* mem = MakeRingMem();
  constexpr uint16_t kQueueSize = 8;
  // Buffer crosses the region boundary.
  *RingDesc(0) = Descriptor{kGpaB + kRegionSize - 4, 8, 0, 0};

  VirtqueueWalker walker(*mem, kGpaA, kQueueSize);
  ChainError err = ChainError::kNone;
  CHECK(!walker.walk(0, err).has_value());
  CHECK(err == ChainError::kAddressTranslationFailed);
}

Descriptor* IndirectTable() {
  return reinterpret_cast<Descriptor*>(&g_region_b[0x800]);
}
constexpr uint64_t kIndirectGpa = kGpaB + 0x800;

void TestChainIndirect() {
  GuestMemTable* mem = MakeRingMem();
  constexpr uint16_t kQueueSize = 8;
  IndirectTable()[0] = Descriptor{kGpaB, 0x40, 0, 0};
  IndirectTable()[1] = Descriptor{kGpaB + 0x40, 0x40, netsec::kDescFWrite, 0};
  *RingDesc(0) =
      Descriptor{kIndirectGpa, 2 * 16, netsec::kDescFIndirect, 0};

  VirtqueueWalker walker(*mem, kGpaA, kQueueSize);
  ChainError err = ChainError::kNone;
  const auto result = walker.walk(0, err);
  CHECK(result.has_value());
  CHECK(result->descriptors.size() == 2);
  CHECK(result->total_length == 0x80);
}

void TestChainIndirectBadLength() {
  GuestMemTable* mem = MakeRingMem();
  constexpr uint16_t kQueueSize = 8;
  VirtqueueWalker walker(*mem, kGpaA, kQueueSize);
  ChainError err = ChainError::kNone;

  // Not a multiple of 16.
  *RingDesc(0) = Descriptor{kIndirectGpa, 24, netsec::kDescFIndirect, 0};
  CHECK(!walker.walk(0, err).has_value());
  CHECK(err == ChainError::kBadIndirectLength);

  // Larger than queue_size * 16.
  *RingDesc(0) = Descriptor{kIndirectGpa, (kQueueSize + 1) * 16,
                            netsec::kDescFIndirect, 0};
  CHECK(!walker.walk(0, err).has_value());
  CHECK(err == ChainError::kBadIndirectLength);

  // Zero length.
  *RingDesc(0) = Descriptor{kIndirectGpa, 0, netsec::kDescFIndirect, 0};
  CHECK(!walker.walk(0, err).has_value());
  CHECK(err == ChainError::kBadIndirectLength);

  // INDIRECT combined with NEXT.
  *RingDesc(0) = Descriptor{kIndirectGpa, 16,
                            netsec::kDescFIndirect | netsec::kDescFNext, 1};
  CHECK(!walker.walk(0, err).has_value());
  CHECK(err == ChainError::kIndirectWithNext);
}

void TestChainNestedIndirect() {
  GuestMemTable* mem = MakeRingMem();
  constexpr uint16_t kQueueSize = 8;
  IndirectTable()[0] = Descriptor{kGpaB, 0x40, 0, 0};
  IndirectTable()[1] =
      Descriptor{kIndirectGpa, 16, netsec::kDescFIndirect, 0};  // nested
  *RingDesc(0) =
      Descriptor{kIndirectGpa, 2 * 16, netsec::kDescFIndirect, 0};

  VirtqueueWalker walker(*mem, kGpaA, kQueueSize);
  ChainError err = ChainError::kNone;
  CHECK(!walker.walk(0, err).has_value());
  CHECK(err == ChainError::kNestedIndirect);
}

void TestChainTotalLength() {
  // 1 GiB + 16 backing buffer so two huge descriptors translate cleanly.
  const uint64_t big_size = (1ull << 30) + 16;
  auto* big = static_cast<uint8_t*>(std::calloc(1, big_size));
  CHECK(big != nullptr);

  GuestMemTable mem;
  constexpr uint64_t kGpaBig = 0x400000;
  CHECK(mem.set_regions({RegionA(),
                         MemoryRegion{kGpaBig, big_size,
                                      reinterpret_cast<uint64_t>(big), 0}}) ==
        MemTableError::kNone);

  constexpr uint16_t kQueueSize = 8;
  const uint32_t half = (1u << 29) + 1;  // two of these exceed 2^30
  *RingDesc(0) = Descriptor{kGpaBig, half, netsec::kDescFNext, 1};
  *RingDesc(1) = Descriptor{kGpaBig + half, half, 0, 0};

  VirtqueueWalker walker(mem, kGpaA, kQueueSize);
  ChainError err = ChainError::kNone;
  CHECK(!walker.walk(0, err).has_value());
  CHECK(err == ChainError::kTotalLengthTooLarge);

  // Exactly 2^30 is accepted.
  *RingDesc(0) = Descriptor{kGpaBig, 1u << 29, netsec::kDescFNext, 1};
  *RingDesc(1) = Descriptor{kGpaBig + (1u << 29), 1u << 29, 0, 0};
  const auto result = walker.walk(0, err);
  CHECK(result.has_value());
  CHECK(result->total_length == (1ull << 30));

  std::free(big);
}

}  // namespace

int main() {
  TestTranslateBasic();
  TestTranslateRejects();
  TestRegionTableValidation();
  TestCacheInvalidation();
  TestChainBasic();
  TestChainCycle();
  TestChainIndexOutOfRange();
  TestChainBadBuffer();
  TestChainIndirect();
  TestChainIndirectBadLength();
  TestChainNestedIndirect();
  TestChainTotalLength();

  if (g_failures == 0) {
    std::printf("all tests passed\n");
    return 0;
  }
  std::printf("%d check(s) failed\n", g_failures);
  return 1;
}
