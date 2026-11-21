// Multi-threaded randomized test of netsec::PartitionAllocator against a
// reference model. Verifies, under concurrency:
//   1. pages are never shared between different domains;
//   2. quarantined blocks are not reused during the isolation period;
//   3. after a domain generation bump, stale PageOwner tokens never match
//      the new owner of a recycled page;
//   4. oversized blocks go straight to mmap and every interior offset
//      reports the owning domain.
#include "netsec.hpp"

#include <atomic>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <random>
#include <thread>
#include <unordered_map>
#include <unordered_set>
#include <vector>

using netsec::PartitionAllocator;

namespace {

#define CHECK(cond)                                                        \
  do {                                                                     \
    if (!(cond)) {                                                         \
      std::fprintf(stderr, "CHECK failed %s:%d: %s\n", __FILE__, __LINE__, \
                   #cond);                                                 \
      std::abort();                                                        \
    }                                                                      \
  } while (0)

struct Block {
  void* ptr;
  size_t size;
  uint32_t domain;
  uint64_t generation;  // PageOwner token captured at alloc time
};

// Per-thread reference model: which blocks are live, owned by which domain.
struct ThreadModel {
  std::vector<uint32_t> domains;
  std::unordered_map<uint32_t, std::vector<Block>> live;
};

void checkBlockAgainstModel(PartitionAllocator& alloc, const Block& b,
                            std::mt19937_64& rng) {
  size_t off = b.size > 1 ? static_cast<size_t>(rng() % b.size) : 0;
  auto owner = alloc.pageOwner(static_cast<char*>(b.ptr) + off);
  CHECK(owner.valid);
  CHECK(owner.domain == b.domain);
  CHECK(owner.generation == b.generation);
}

void worker(PartitionAllocator& alloc, uint64_t seed, int ops,
            ThreadModel& model) {
  std::mt19937_64 rng(seed);
  model.domains.push_back(alloc.createDomain());

  for (int i = 0; i < ops; ++i) {
    uint32_t dom =
        model.domains[static_cast<size_t>(rng() % model.domains.size())];
    int roll = static_cast<int>(rng() % 100);

    if (roll < 55) {  // small alloc
      size_t size = 1 + static_cast<size_t>(rng() %
                                            PartitionAllocator::kMaxSmallSize);
      void* p = alloc.alloc(dom, size);
      CHECK(p != nullptr);
      auto owner = alloc.pageOwner(p);
      CHECK(owner.valid && owner.domain == dom);
      model.live[dom].push_back(Block{p, size, dom, owner.generation});
    } else if (roll < 65) {  // large alloc (direct mmap)
      size_t size = PartitionAllocator::kMaxSmallSize + 1 +
                    static_cast<size_t>(rng() % 131072);
      void* p = alloc.alloc(dom, size);
      CHECK(p != nullptr);
      auto owner = alloc.pageOwner(p);
      CHECK(owner.valid && owner.domain == dom);
      // Interior offsets of the extent must report the same owner.
      auto tail = alloc.pageOwner(static_cast<char*>(p) + size - 1);
      CHECK(tail == owner);
      model.live[dom].push_back(Block{p, size, dom, owner.generation});
    } else if (roll < 92) {  // free a random live block
      auto& vec = model.live[dom];
      if (!vec.empty()) {
        size_t idx = static_cast<size_t>(rng() % vec.size());
        alloc.free(vec[idx].ptr);
        vec[idx] = vec.back();
        vec.pop_back();
      }
    } else if (roll < 97) {  // destroy a domain (keep at least one)
      if (model.domains.size() > 1) {
        size_t idx = static_cast<size_t>(rng() % model.domains.size());
        uint32_t victim = model.domains[idx];
        alloc.destroyDomain(victim);
        model.live.erase(victim);
        model.domains[idx] = model.domains.back();
        model.domains.pop_back();
      }
    } else {  // create a domain
      if (model.domains.size() < 12)
        model.domains.push_back(alloc.createDomain());
    }

    // Randomly re-validate a live block against the reference model.
    if (rng() % 16 == 0) {
      auto& vec = model.live[dom];
      if (!vec.empty())
        checkBlockAgainstModel(
            alloc, vec[static_cast<size_t>(rng() % vec.size())], rng);
    }
  }
}

// Phase A: concurrent random ops, each thread owning private domains.
void concurrentPhase(PartitionAllocator& alloc,
                     std::vector<ThreadModel>& models) {
  constexpr int kThreads = 4;
  constexpr int kOpsPerThread = 20000;
  models.resize(kThreads);
  std::vector<std::thread> threads;
  for (int t = 0; t < kThreads; ++t)
    threads.emplace_back(worker, std::ref(alloc),
                         0x9e3779b97f4a7c15ull + static_cast<uint64_t>(t),
                         kOpsPerThread, std::ref(models[t]));
  for (auto& th : threads) th.join();
}

// Phase B: whole-heap invariants over the merged reference model.
void globalInvariantPhase(PartitionAllocator& alloc,
                          const std::vector<ThreadModel>& models) {
  std::unordered_map<uint64_t, uint32_t> pageToDomain;
  size_t liveBlocks = 0;
  for (const auto& model : models) {
    for (const auto& kv : model.live) {
      for (const Block& b : kv.second) {
        ++liveBlocks;
        uintptr_t begin = reinterpret_cast<uintptr_t>(b.ptr);
        uintptr_t end = begin + b.size - 1;
        for (uint64_t pg = begin / PartitionAllocator::kPageSize;
             pg <= end / PartitionAllocator::kPageSize; ++pg) {
          auto res = pageToDomain.emplace(pg, b.domain);
          // Invariant 1: a page is never shared by two domains.
          CHECK(res.second || res.first->second == b.domain);
        }
        // Every live block must still resolve to its recorded token.
        auto owner = alloc.pageOwner(b.ptr);
        CHECK(owner.valid);
        CHECK(owner.domain == b.domain);
        CHECK(owner.generation == b.generation);
      }
    }
  }
  std::printf("phase B: %zu live blocks across %zu pages OK\n", liveBlocks,
              pageToDomain.size());
}

// Invariant 2: quarantined blocks are not reused during isolation.
void quarantinePhase(PartitionAllocator& alloc) {
  uint32_t dom = alloc.createDomain();
  std::unordered_set<void*> freed;
  std::vector<void*> first;
  for (size_t i = 0; i < PartitionAllocator::kQuarantineCapacity; ++i) {
    void* p = alloc.alloc(dom, 64);
    CHECK(p != nullptr);
    first.push_back(p);
  }
  for (void* p : first) {
    alloc.free(p);
    freed.insert(p);
  }
  // The slab still has fresh blocks; the quarantined ones must stay isolated.
  for (size_t i = 0; i < PartitionAllocator::kQuarantineCapacity; ++i) {
    void* p = alloc.alloc(dom, 64);
    CHECK(p != nullptr);
    CHECK(freed.count(p) == 0);
  }
  alloc.destroyDomain(dom);
  std::printf("quarantine isolation OK\n");
}

// Invariant 3: after destroyDomain, zeroed pages are recycled with a new
// generation; stale tokens never match the new domain.
void generationPhase(PartitionAllocator& alloc) {
  uint32_t a = alloc.createDomain();
  void* victim = alloc.alloc(a, 128);
  CHECK(victim != nullptr);
  auto token = alloc.pageOwner(victim);
  CHECK(token.valid && token.domain == a);
  alloc.destroyDomain(a);

  uint32_t b = alloc.createDomain();
  std::vector<void*> keep;
  keep.reserve(8192);
  for (int i = 0; i < 8192; ++i) {  // force consumption of recycled pages
    void* p = alloc.alloc(b, 128);
    CHECK(p != nullptr);
    keep.push_back(p);
  }
  auto now = alloc.pageOwner(victim);
  CHECK(now != token);  // stale token must not match
  if (now.valid) CHECK(now.domain != a);
  for (void* p : keep) {
    auto owner = alloc.pageOwner(p);
    CHECK(owner.valid && owner.domain == b);
  }
  alloc.destroyDomain(b);
  std::printf("domain generation recycling OK\n");
}

// Invariant 4: large blocks are direct mmap extents owned by their domain.
void largePhase(PartitionAllocator& alloc) {
  uint32_t c = alloc.createDomain();
  constexpr size_t kBig = 100000;
  char* p = static_cast<char*>(alloc.alloc(c, kBig));
  CHECK(p != nullptr);
  auto token = alloc.pageOwner(p);
  CHECK(token.valid && token.domain == c);
  const size_t offsets[] = {0, 1, 4095, 4096, 4097, 50000, kBig - 1};
  for (size_t off : offsets) CHECK(alloc.pageOwner(p + off) == token);
  std::memset(p, 0xAB, kBig);  // extent is usable end to end
  alloc.free(p);
  CHECK(!alloc.pageOwner(p).valid);  // mapping is gone after free
  alloc.destroyDomain(c);
  std::printf("large mmap ownership OK\n");
}

}  // namespace

int main() {
  PartitionAllocator alloc;
  std::vector<ThreadModel> models;

  concurrentPhase(alloc, models);
  std::printf("phase A: concurrent random ops OK\n");

  globalInvariantPhase(alloc, models);
  quarantinePhase(alloc);
  generationPhase(alloc);
  largePhase(alloc);

  std::printf("ALL PARTITION ALLOCATOR TESTS PASSED\n");
  return 0;
}
