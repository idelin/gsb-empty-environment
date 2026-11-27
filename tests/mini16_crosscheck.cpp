// Exhaustive cross-validation on a 16-bit miniature 4-level address space.
// An independent oracle enumerates every one of the 4096 pages and its
// classification is compared against the symbolic auditor; the two must
// agree on every page and on every lowest-violation virtual address.
#include "kpti/audit.hpp"

#include <cassert>
#include <cstdio>
#include <set>
#include <vector>

namespace {

constexpr unsigned P = 1u << 0;
constexpr unsigned W = 1u << 1;
constexpr unsigned U = 1u << 2;
constexpr unsigned G = 1u << 5;
constexpr unsigned PS = 1u << 6;
constexpr unsigned NX = 1u << 7;

struct Rng {
  std::uint64_t state;
  explicit Rng(std::uint64_t seed)
      : state(seed ? seed : 0x9e3779b97f4a7c15ull) {}
  std::uint64_t next() {
    state ^= state << 13;
    state ^= state >> 7;
    state ^= state << 17;
    return state;
  }
  unsigned below(unsigned bound) {
    return static_cast<unsigned>(next() % bound);
  }
  bool chance(unsigned percent) { return below(100) < percent; }
};

constexpr kpti::Geometry kGeom = kpti::Geometry::mini16();
constexpr std::uint64_t kPageSize = 16;
constexpr std::uint64_t kPages = 4096;       // 2^16 / 16
constexpr std::uint64_t kTableBytes = 8 * 8;
constexpr std::uint64_t kTableAlign = 1ull << kGeom.pfnLowBit;
constexpr std::uint64_t kMaxTables = 1200;
constexpr std::uint64_t kRamBytes = 1ull << 20;

struct World {
  kpti::FlatPhysicalMemory memory{kRamBytes};
  std::uint64_t tableCursor = 0;

  std::uint64_t allocTable() {
    tableCursor = (tableCursor + kTableAlign - 1) / kTableAlign * kTableAlign;
    assert(tableCursor + kTableBytes <= kRamBytes);
    std::uint64_t address = tableCursor;
    tableCursor += kTableAlign;
    return address;
  }
  void setEntry(std::uint64_t table, unsigned slot, std::uint64_t entry) {
    bool ok = memory.writeU64(table + slot * 8, entry);
    assert(ok);
  }
};

std::uint64_t encodePresent(std::uint64_t phys, unsigned flags) {
  return (phys & ~((1ull << kGeom.pfnLowBit) - 1)) |
         (static_cast<std::uint64_t>(flags) & 0xffull);
}

// Build one random sparse 4-level tree. depth zero is the PML4 root.
void buildTree(World &world, Rng &rng, std::uint64_t tablePhys,
               unsigned depth, std::uint64_t vaBase,
               std::set<std::uint64_t> &usedTables) {
  unsigned entries = static_cast<unsigned>(kGeom.entriesPerTable());
  std::uint64_t span = kGeom.coverage(depth);
  for (unsigned slot = 0; slot < entries; ++slot) {
    std::uint64_t va = vaBase + slot * span;
    if (!rng.chance(depth == 0 ? 55 : 45)) continue;

    if (depth < 3 && rng.chance(25)) {
      // Non-present terminal: zero entry or a swap-style encoded PFN.
      if (rng.chance(60)) continue;
      std::uint64_t phys =
          static_cast<std::uint64_t>(rng.below(2000)) * kPageSize;
      std::uint64_t entry = (phys >> kGeom.pfnLowBit) << kGeom.pfnLowBit;
      world.setEntry(tablePhys, slot, entry);
      continue;
    }

    bool leaf = depth > 0 && rng.chance(35);
    if (depth == 3) leaf = true;

    unsigned flags = P;
    if (rng.chance(55)) flags |= W;
    if (rng.chance(55)) flags |= U;
    if (rng.chance(30)) flags |= G;
    if (rng.chance(55)) flags |= NX;

    if (leaf) {
      std::uint64_t phys;
      if (rng.chance(55)) {
        // Bias leaf targets into kernel-sensitive RAM so every violation
        // class is exercised; ranges are [0x10000,0x18000) and
        // [0x20000,0x24000), i.e. 2048 contiguous page frames.
        phys = (0x10000 / kPageSize + rng.below(2048)) * kPageSize;
        if (phys >= 0x18000 && phys < 0x20000) phys = 0x20000;
      } else {
        phys = static_cast<std::uint64_t>(rng.below(1800)) * kPageSize;
      }
      unsigned leafFlags = flags | (depth < 3 ? PS : 0u);
      world.setEntry(tablePhys, slot, encodePresent(phys, leafFlags));
      continue;
    }

    if (usedTables.size() >= kMaxTables) continue;
    std::uint64_t child = world.allocTable();
    usedTables.insert(child);
    world.setEntry(tablePhys, slot, encodePresent(child, flags));
    buildTree(world, rng, child, depth + 1, va, usedTables);
  }
}

struct OraclePage {
  unsigned kinds = 0;  // expected FindingKind mask
};

bool rangeContains(const std::vector<kpti::PhysRange> &ranges,
                   std::uint64_t value) {
  for (const auto &range : ranges) {
    if (value >= range.begin && value < range.end) return true;
  }
  return false;
}

// Independent page-by-page walker. No logic is shared with the auditor
// beyond the geometry constants and the physical-memory snapshot.
void oracleWalk(World &world, std::uint64_t root, bool isUserTree,
                const kpti::AuditParams &params,
                std::vector<OraclePage> &pages) {
  auto read = [&](std::uint64_t paddr) {
    std::uint64_t value = 0;
    bool ok = world.memory.readU64(paddr, &value);
    assert(ok);
    return value;
  };
  auto has = [](std::uint64_t entry, unsigned bitPos) {
    return ((entry >> bitPos) & 1ull) != 0;
  };

  for (std::uint64_t page = 0; page < kPages; ++page) {
    std::uint64_t va = page * kPageSize;
    std::uint64_t table = root;
    std::uint64_t entry = 0;
    unsigned depth = 0;
    bool presentLeaf = false;

    while (depth < 4) {
      unsigned index =
          static_cast<unsigned>(kGeom.indexOf(depth, va));
      entry = read(table + index * 8);
      if (depth == 0 && isUserTree && index >= 4 && has(entry, 0)) {
        pages[page].kinds |= kpti::FIND_KERNEL_HALF_PRESENT;
      }
      if (!has(entry, 0)) break;  // non-present terminal
      bool leaf = depth > 0 && has(entry, kGeom.hugeBit);
      if (depth == 3) leaf = true;
      if (leaf) {
        presentLeaf = true;
        break;
      }
      std::uint64_t childPfn =
          (entry >> kGeom.pfnLowBit) & kGeom.pfnMask();
      table = childPfn << kGeom.pfnLowBit;
      ++depth;
    }

    if (!presentLeaf) {
      if (entry != 0) {
        std::uint64_t pfn =
            (entry >> kGeom.pfnLowBit) & kGeom.pfnMask();
        std::uint64_t phys = pfn << kGeom.pfnLowBit;
        if (phys < params.maxRam) pages[page].kinds |= kpti::FIND_L1TF;
      }
      continue;
    }

    bool protNone =
        has(entry, 0) && !has(entry, 1) && !has(entry, 2) && has(entry, 7);
    if (protNone) {
      std::uint64_t pfn =
          (entry >> kGeom.pfnLowBit) & kGeom.pfnMask();
      std::uint64_t phys = pfn << kGeom.pfnLowBit;
      if (phys < params.maxRam) pages[page].kinds |= kpti::FIND_L1TF;
      continue;
    }

    std::uint64_t span = kGeom.coverage(depth);
    std::uint64_t pfn = (entry >> kGeom.pfnLowBit) & kGeom.pfnMask();
    std::uint64_t unaligned = pfn << kGeom.pfnLowBit;
    std::uint64_t physBase = unaligned - (unaligned % span);
    std::uint64_t phys = physBase + (va % span);

    bool sensitive = rangeContains(params.sensitive, phys);
    bool code = rangeContains(params.kernelCode, phys);
    bool trampolined = false;
    if (isUserTree) {
      for (const auto &trampoline : params.trampolines) {
        if (trampoline.vaddr == va && trampoline.paddr == phys) {
          trampolined = true;
        }
      }
    }

    if (sensitive && isUserTree && !trampolined) {
      pages[page].kinds |= kpti::FIND_USER_SENSITIVE;
      if (has(entry, 2)) pages[page].kinds |= kpti::FIND_USER_BIT;
      if (has(entry, 5)) pages[page].kinds |= kpti::FIND_GLOBAL_BIT;
    } else if (!sensitive && isUserTree && has(entry, 5)) {
      pages[page].kinds |= kpti::FIND_GLOBAL_BIT;
    }
    if (!has(entry, 7) && has(entry, 2) && code &&
        !(isUserTree && sensitive && trampolined)) {
      pages[page].kinds |= kpti::FIND_SMEP;
    }
    if (!has(entry, 7) && has(entry, 1) && sensitive &&
        !(isUserTree && trampolined)) {
      pages[page].kinds |= kpti::FIND_WXORX;
    }
  }
}

}  // namespace

namespace {

unsigned runOneScenario(unsigned seed) {
  Rng rng(seed);
  World world;
  std::set<std::uint64_t> usedKernel;
  std::set<std::uint64_t> usedUser;
  std::uint64_t kernelRoot = world.allocTable();
  std::uint64_t userRoot = world.allocTable();
  usedKernel.insert(kernelRoot);
  usedUser.insert(userRoot);
  buildTree(world, rng, kernelRoot, 0, 0, usedKernel);
  buildTree(world, rng, userRoot, 0, 0, usedUser);

  kpti::AuditParams params;
  params.geometry = kGeom;
  params.kernelCr3 = kernelRoot;
  params.userCr3 = userRoot;
  params.maxRam = kRamBytes;
  params.sensitive = {
      kpti::PhysRange{0x10000, 0x18000},
      kpti::PhysRange{0x20000, 0x24000},
  };
  params.kernelCode = {kpti::PhysRange{0x12000, 0x14000}};

  for (unsigned i = 0; i < 8; ++i) {
    std::uint64_t va =
        static_cast<std::uint64_t>(rng.below(2048)) * kPageSize;
    std::uint64_t pa =
        0x10000 + static_cast<std::uint64_t>(rng.below(32)) * kPageSize;
    params.trampolines.push_back(kpti::TrampolinePage{va, pa});
  }

  std::vector<OraclePage> expected(kPages);
  oracleWalk(world, kernelRoot, false, params, expected);
  oracleWalk(world, userRoot, true, params, expected);

  kpti::Auditor auditor(world.memory);
  kpti::AuditReport actual = auditor.run(params);
  if (!actual.structuralErrors.empty()) {
    for (const auto &error : actual.structuralErrors) {
      std::fprintf(stderr, "seed=%u structural: %s\n", seed,
                   error.c_str());
    }
    assert(false);
  }

  // Expand symbolic findings onto individual pages through their spans and
  // compare against the exhaustive oracle page by page. Huge leaves that
  // only partially overlap a sensitive range are re-evaluated per page.
  std::vector<unsigned> actualKinds(kPages, 0);
  for (const auto &finding : actual.findings) {
    std::uint64_t firstPage = finding.va / kPageSize;
    std::uint64_t count = finding.span / kPageSize;
    for (std::uint64_t i = 0; i < count; ++i) {
      std::uint64_t pageVa = (firstPage + i) * kPageSize;
      std::uint64_t pagePhys = finding.physBase + pageVa - finding.va;
      bool sensitive = rangeContains(params.sensitive, pagePhys);
      bool code = rangeContains(params.kernelCode, pagePhys);
      bool trampolined = false;
      for (const auto &trampoline : params.trampolines) {
        if (trampoline.vaddr == pageVa && trampoline.paddr == pagePhys) {
          trampolined = true;
        }
      }
      unsigned mask = finding.kinds;
      if (sensitive && !trampolined) {
        actualKinds[firstPage + i] |= mask;
      } else {
        // On non-sensitive (or whitelisted) pages the sensitive-derived
        // categories do not apply; only TLB/L1TF/root-half categories do.
        actualKinds[firstPage + i] |=
            mask & (kpti::FIND_GLOBAL_BIT | kpti::FIND_L1TF |
                    kpti::FIND_KERNEL_HALF_PRESENT);
      }
      (void)code;
    }
  }

  for (std::uint64_t page = 0; page < kPages; ++page) {
    if (actualKinds[page] != expected[page].kinds) {
      std::fprintf(stderr,
                   "seed=%u page=%llu va=%llx expected=%u actual=%u\n",
                   seed, static_cast<unsigned long long>(page),
                   static_cast<unsigned long long>(page * kPageSize),
                   expected[page].kinds, actualKinds[page]);
      assert(false && "oracle mismatch");
    }
  }

  constexpr unsigned allKinds =
      kpti::FIND_USER_SENSITIVE | kpti::FIND_USER_BIT |
      kpti::FIND_GLOBAL_BIT | kpti::FIND_SMEP | kpti::FIND_WXORX |
      kpti::FIND_L1TF | kpti::FIND_KERNEL_HALF_PRESENT;
  for (unsigned kind = 1; kind <= allKinds; kind <<= 1) {
    std::uint64_t expectedLowest = 0;
    bool expectedHas = false;
    for (std::uint64_t page = 0; page < kPages; ++page) {
      if ((expected[page].kinds & kind) != 0) {
        expectedLowest = page * kPageSize;
        expectedHas = true;
        break;
      }
    }
    std::uint64_t actualLowest = 0;
    bool actualHas = actual.lowest(kind, &actualLowest);
    assert(expectedHas == actualHas);
    if (expectedHas) assert(expectedLowest == actualLowest);
  }
  unsigned observed = 0;
  for (const auto &page : expected) observed |= page.kinds;
  return observed;
}

}  // namespace

int main() {
  unsigned coverage = 0;
  for (unsigned seed = 1; seed <= 60; ++seed) {
    coverage |= runOneScenario(seed);
  }
  constexpr unsigned allKinds =
      kpti::FIND_USER_SENSITIVE | kpti::FIND_USER_BIT |
      kpti::FIND_GLOBAL_BIT | kpti::FIND_SMEP | kpti::FIND_WXORX |
      kpti::FIND_L1TF | kpti::FIND_KERNEL_HALF_PRESENT;
  assert(coverage == allKinds);
  std::printf("mini16 exhaustive cross-validation passed for 60 scenarios\n");
  return 0;
}
