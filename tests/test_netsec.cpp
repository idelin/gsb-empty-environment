#include "netsec.hpp"

#include <cstdio>
#include <vector>

using namespace netsec;

static int g_checks = 0;
static int g_failures = 0;

#define CHECK(...)                                          \
    do {                                                    \
        ++g_checks;                                         \
        if (!(__VA_ARGS__)) {                               \
            ++g_failures;                                   \
            std::printf("FAIL %s:%d: %s\n", __FILE__,       \
                        __LINE__, #__VA_ARGS__);            \
        }                                                   \
    } while (0)

static MemoryRegion regionFor(const void* base, std::uint64_t gpa, std::uint64_t size) {
    return MemoryRegion{gpa, size, reinterpret_cast<std::uint64_t>(base), 0};
}

static VringDesc makeDesc(std::uint64_t addr, std::uint32_t len, std::uint16_t flags,
                          std::uint16_t next) {
    VringDesc desc;
    desc.addr = addr;
    desc.len = len;
    desc.flags = flags;
    desc.next = next;
    return desc;
}

static void testTranslateBasic() {
    alignas(16) unsigned char buffer[0x1000] = {};
    GuestMemoryMap map;
    CHECK(!map.setRegions({regionFor(buffer, 0x1000, 0x1000)}).has_value());

    TranslateResult r = map.translate(0x1000, 0x100);
    CHECK(r.ok());
    CHECK(r.host_addr == reinterpret_cast<std::uint64_t>(buffer));
    r = map.translate(0x1ff0, 0x10);
    CHECK(r.ok());
    CHECK(r.host_addr == reinterpret_cast<std::uint64_t>(buffer) + 0xff0);
    // range ending exactly at the region end is valid
    CHECK(map.translate(0x1800, 0x800).ok());
    // zero length rejected
    CHECK(map.translate(0x1000, 0).error == TranslateError::kZeroLength);
    // gpa+len wraps the 64-bit address space
    CHECK(map.translate(UINT64_MAX - 7, 0x10).error == TranslateError::kWrapAround);
    CHECK(map.translate(UINT64_MAX, 2).error == TranslateError::kWrapAround);
    // gpa+len == 2^64 exactly also wraps
    CHECK(map.translate(0, 0).error == TranslateError::kZeroLength);
    // one byte past the region end
    CHECK(map.translate(0x1fff, 2).error == TranslateError::kOutOfRegion);
    CHECK(map.translate(0x0fff, 1).error == TranslateError::kOutOfRegion);
}

static void testCrossRegion() {
    alignas(16) unsigned char buf_a[0x1000] = {};
    alignas(16) unsigned char buf_b[0x1000] = {};
    GuestMemoryMap map;
    CHECK(!map.setRegions({regionFor(buf_a, 0x1000, 0x1000),
                           regionFor(buf_b, 0x2000, 0x1000)})
               .has_value());
    // entirely inside one region: ok
    CHECK(map.translate(0x1800, 0x800).ok());
    CHECK(map.translate(0x2000, 0x1000).ok());
    // spans the boundary between two adjacent regions: rejected
    CHECK(map.translate(0x1800, 0x1000).error == TranslateError::kOutOfRegion);
    CHECK(map.translate(0x1fff, 2).error == TranslateError::kOutOfRegion);
    // gap before the first region
    CHECK(map.translate(0x0800, 0x1000).error == TranslateError::kOutOfRegion);
    // beyond the last region
    CHECK(map.translate(0x2000, 0x2000).error == TranslateError::kOutOfRegion);
}

static void testMemTableValidation() {
    GuestMemoryMap map;
    // more than 8 regions
    std::vector<MemoryRegion> many;
    for (std::uint64_t i = 0; i < kMaxMemoryRegions + 1; ++i) {
        many.push_back(MemoryRegion{0x1000 + i * 0x1000, 0x1000, 0, 0});
    }
    CHECK(map.setRegions(many) == MemTableError::kTooManyRegions);
    // zero-size region
    CHECK(map.setRegions({MemoryRegion{0x1000, 0, 0, 0}}) == MemTableError::kZeroSizeRegion);
    // region wraps the address space
    CHECK(map.setRegions({MemoryRegion{UINT64_MAX - 0xfff, 0x2000, 0, 0}}) ==
          MemTableError::kRegionWrapAround);
    // overlapping regions (given unsorted on purpose)
    CHECK(map.setRegions({MemoryRegion{0x2000, 0x1000, 0, 0},
                          MemoryRegion{0x1000, 0x2000, 0, 0}}) ==
          MemTableError::kOverlappingRegions);
    // adjacent but not overlapping: accepted
    CHECK(!map.setRegions({MemoryRegion{0x1000, 0x1000, 0, 0},
                           MemoryRegion{0x2000, 0x1000, 0, 0}})
               .has_value());
    CHECK(map.translate(0x1000, 1).ok());
    // a rejected update keeps the previous table active
    CHECK(map.setRegions({MemoryRegion{0x1000, 0x1000, 0, 0},
                          MemoryRegion{0x1800, 0x1000, 0, 0}}) ==
          MemTableError::kOverlappingRegions);
    CHECK(map.translate(0x1000, 1).ok());
    CHECK(map.translate(0x2fff, 1).ok());
}

struct ChainFixture {
    alignas(16) unsigned char buffer[0x10000] = {};
    GuestMemoryMap map;

    ChainFixture() { map.setRegions({regionFor(buffer, 0, sizeof(buffer))}); }

    VringDesc* descs() { return reinterpret_cast<VringDesc*>(buffer); }
};

static void testChainBasic() {
    ChainFixture f;
    VringDesc* d = f.descs();
    d[0] = makeDesc(0x1000, 0x100, kVringDescFNext, 1);
    d[1] = makeDesc(0x2000, 0x200, kVringDescFNext | kVringDescFWrite, 2);
    d[2] = makeDesc(0x3000, 0x80, 0, 0);
    Virtqueue vq(f.map, 8);
    ChainResult res = vq.walkChain(d, 0);
    CHECK(res.ok());
    CHECK(res.segments.size() == 3);
    CHECK(res.segments[0].host_addr == reinterpret_cast<std::uint64_t>(f.buffer) + 0x1000);
    CHECK(res.segments[0].len == 0x100);
    CHECK(!res.segments[0].write);
    CHECK(res.segments[1].write);
    CHECK(res.segments[2].host_addr == reinterpret_cast<std::uint64_t>(f.buffer) + 0x3000);
    // head index out of range
    CHECK(vq.walkChain(d, 8).error == ChainError::kBadIndex);
    // descriptor payload outside any region
    d[3] = makeDesc(0x1'0000'0000, 0x10, 0, 0);
    CHECK(vq.walkChain(d, 3).error == ChainError::kBadDescriptorAddress);
    // zero-length descriptor
    d[4] = makeDesc(0x1000, 0, 0, 0);
    CHECK(vq.walkChain(d, 4).error == ChainError::kBadDescriptorAddress);
}

static void testChainCycle() {
    ChainFixture f;
    VringDesc* d = f.descs();
    Virtqueue vq(f.map, 8);
    // 0 -> 1 -> 0
    d[0] = makeDesc(0x1000, 0x10, kVringDescFNext, 1);
    d[1] = makeDesc(0x1000, 0x10, kVringDescFNext, 0);
    CHECK(vq.walkChain(d, 0).error == ChainError::kCycle);
    // self loop
    d[2] = makeDesc(0x1000, 0x10, kVringDescFNext, 2);
    CHECK(vq.walkChain(d, 2).error == ChainError::kCycle);
    // next index out of range
    d[3] = makeDesc(0x1000, 0x10, kVringDescFNext, 99);
    CHECK(vq.walkChain(d, 3).error == ChainError::kBadIndex);
}

static void testIndirect() {
    ChainFixture f;
    VringDesc* d = f.descs();
    Virtqueue vq(f.map, 4);
    auto* table = reinterpret_cast<VringDesc*>(f.buffer + 0x4000);
    table[0] = makeDesc(0x1000, 0x10, 0, 0);
    table[1] = makeDesc(0x2000, 0x10, kVringDescFWrite, 0);
    // valid indirect table with 2 entries
    d[0] = makeDesc(0x4000, 2 * kVringDescSize, kVringDescFIndirect, 0);
    ChainResult res = vq.walkChain(d, 0);
    CHECK(res.ok());
    CHECK(res.segments.size() == 2);
    CHECK(res.segments[1].write);
    // length not a multiple of 16
    d[1] = makeDesc(0x4000, 24, kVringDescFIndirect, 0);
    CHECK(vq.walkChain(d, 1).error == ChainError::kBadIndirectLength);
    // length larger than queue_size * 16
    d[2] = makeDesc(0x4000, 5 * kVringDescSize, kVringDescFIndirect, 0);
    CHECK(vq.walkChain(d, 2).error == ChainError::kBadIndirectLength);
    // zero-length indirect table
    d[3] = makeDesc(0x4000, 0, kVringDescFIndirect, 0);
    CHECK(vq.walkChain(d, 3).error == ChainError::kBadIndirectLength);
    // indirect table itself outside any region
    ChainFixture f2;
    VringDesc* d2 = f2.descs();
    Virtqueue vq2(f2.map, 4);
    d2[0] = makeDesc(0x1'0000'0000, kVringDescSize, kVringDescFIndirect, 0);
    CHECK(vq2.walkChain(d2, 0).error == ChainError::kBadDescriptorAddress);
}

static void testChainTooLong() {
    ChainFixture f;
    VringDesc* d = f.descs();
    Virtqueue vq(f.map, 4);
    // indirect descriptor (1) + 4 table entries = 5 > queue_size 4
    auto* table = reinterpret_cast<VringDesc*>(f.buffer + 0x4000);
    for (int i = 0; i < 4; ++i) {
        table[i] = makeDesc(0x1000, 0x10, 0, 0);
    }
    d[0] = makeDesc(0x4000, 4 * kVringDescSize, kVringDescFIndirect, 0);
    CHECK(vq.walkChain(d, 0).error == ChainError::kChainTooLong);
}

static void testNestedIndirect() {
    ChainFixture f;
    VringDesc* d = f.descs();
    Virtqueue vq(f.map, 8);
    auto* table = reinterpret_cast<VringDesc*>(f.buffer + 0x4000);
    table[0] = makeDesc(0x5000, kVringDescSize, kVringDescFIndirect, 0);
    d[0] = makeDesc(0x4000, kVringDescSize, kVringDescFIndirect, 0);
    CHECK(vq.walkChain(d, 0).error == ChainError::kNestedIndirect);
}

static void testTotalLengthLimit() {
    GuestMemoryMap map;
    // 4 GiB region; host addresses are never dereferenced by the walker
    CHECK(!map.setRegions({MemoryRegion{0, 0x1'0000'0000ull, 0x1'0000'0000ull, 0}})
               .has_value());
    Virtqueue vq(map, 8);
    VringDesc d[8] = {};
    d[0] = makeDesc(0, 0x2000'0000, kVringDescFNext, 1);
    d[1] = makeDesc(0x2000'0000, 0x2000'0000, kVringDescFNext, 2);
    d[2] = makeDesc(0x4000'0000, 0x2000'0000, 0, 0);
    // 3 x 512 MiB = 1.5 GiB > 2^30
    CHECK(vq.walkChain(d, 0).error == ChainError::kTotalLengthExceeded);
    // exactly 2^30 is still allowed
    VringDesc single[1] = {makeDesc(0, 0x4000'0000, 0, 0)};
    CHECK(vq.walkChain(single, 0).ok());
}

static void testCacheInvalidation() {
    alignas(16) unsigned char buf_a[0x1000] = {};
    alignas(16) unsigned char buf_b[0x1000] = {};
    GuestMemoryMap map;
    CHECK(!map.setRegions({regionFor(buf_a, 0x1000, 0x1000)}).has_value());
    const std::uint64_t gen1 = map.generation();
    TranslateResult r1 = map.translate(0x1000, 0x10);
    CHECK(r1.ok());
    CHECK(r1.host_addr == reinterpret_cast<std::uint64_t>(buf_a));
    // prime every cache slot
    for (std::uint64_t gpa = 0x1000; gpa < 0x1100; gpa += 8) {
        CHECK(map.translate(gpa, 8).ok());
    }
    // table update: same guest range, different host mapping
    CHECK(!map.setRegions({regionFor(buf_b, 0x1000, 0x1000)}).has_value());
    CHECK(map.generation() != gen1);
    TranslateResult r2 = map.translate(0x1000, 0x10);
    CHECK(r2.ok());
    CHECK(r2.host_addr == reinterpret_cast<std::uint64_t>(buf_b));
    // table update: old range no longer covered at all
    CHECK(!map.setRegions({regionFor(buf_b, 0x8000, 0x1000)}).has_value());
    CHECK(map.translate(0x1000, 0x10).error == TranslateError::kOutOfRegion);
    CHECK(map.translate(0x8000, 0x10).ok());
}

int main() {
    testTranslateBasic();
    testCrossRegion();
    testMemTableValidation();
    testChainBasic();
    testChainCycle();
    testIndirect();
    testChainTooLong();
    testNestedIndirect();
    testTotalLengthLimit();
    testCacheInvalidation();
    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
