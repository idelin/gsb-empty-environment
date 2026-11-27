// kpti::audit - embedded, zero-dependency symbolic isolation verifier for
// 4-level x86-64 page tables (PML4/PDPT/PD/PT), with a runtime-configurable
// geometry that also drives a 16-bit miniature address space used by the
// exhaustive cross-validation harness.
//
// The verifier never walks pages one by one: it walks the sparse page-table
// tree (at most 4 levels, 512 slots per table on x86-64) and reasons over
// symbolic virtual/physical intervals, so 1 GiB and 2 MiB leaves are handled
// in a single step including partial-overlap cases.
#ifndef KPTI_AUDIT_HPP
#define KPTI_AUDIT_HPP

#include <algorithm>
#include <cassert>
#include <cstddef>
#include <cstdint>
#include <string>
#include <utility>
#include <vector>

namespace kpti {

// A half-open physical interval [begin, end).
struct PhysRange {
  std::uint64_t begin = 0;
  std::uint64_t end = 0;

  bool overlaps(PhysRange other) const noexcept {
    return begin < other.end && other.begin < end;
  }
};

// Virtual/physical interval pair for a fully whitelisted trampoline page.
// Both addresses must be page aligned.
struct TrampolinePage {
  std::uint64_t vaddr = 0;
  std::uint64_t paddr = 0;
};

// Runtime description of a 4-level paging geometry. All arithmetic is driven
// from these constants so the exact same engine audits real x86-64 tables and
// the 16-bit miniature tables.
struct Geometry {
  unsigned levels = 4;          // always 4: PML4, PDPT, PD, PT
  unsigned levelBits = 9;       // virtual index bits per level (x86-64: 9)
  unsigned offsetBits = 12;     // page offset bits (x86-64: 12)
  unsigned pfnLowBit = 12;      // first PFN bit in a present PTE (x86-64: 12)
  unsigned pfnHighBit = 51;     // last software-available PFN bit (x86-64: 51)

  // Architectural flag bit positions.
  unsigned presentBit = 0;
  unsigned rwBit = 1;
  unsigned userBit = 2;
  unsigned pwtBit = 3;
  unsigned pcdBit = 4;
  unsigned globalBit = 8;
  unsigned hugeBit = 7;         // PS (page size) on PD/PDPT entries
  unsigned nxBit = 63;

  static constexpr Geometry x86_64() noexcept {
    return Geometry{4, 9, 12, 12, 51, 0, 1, 2, 3, 4, 8, 7, 63};
  }

  // 16-bit miniature: 4 x 3-bit indexes + 4-bit offset, 16-byte pages,
  // 64-bit PTEs whose software-available PFN window is bits 8..19.
  static constexpr Geometry mini16() noexcept {
    return Geometry{4, 3, 4, 8, 19, 0, 1, 2, 3, 4, 5, 6, 7};
  }

  std::uint64_t entriesPerTable() const noexcept {
    return std::uint64_t{1} << levelBits;
  }
  std::uint64_t pageSize() const noexcept {
    return std::uint64_t{1} << offsetBits;
  }
  std::uint64_t vaBits() const noexcept {
    return offsetBits + levelBits * levels;
  }
  std::uint64_t vaSpan() const noexcept {
    return std::uint64_t{1} << vaBits();
  }
  std::uint64_t indexMask() const noexcept {
    return entriesPerTable() - 1;
  }
  // Entries that belong to the canonical user half of the root table.
  std::uint64_t userSplitEntries() const noexcept {
    return entriesPerTable() / 2;
  }
  std::uint64_t pfnMask() const noexcept {
    unsigned width = pfnHighBit - pfnLowBit + 1;
    return ((std::uint64_t{1} << (width - 1)) << 1) - std::uint64_t{1};
  }
  std::uint64_t flagMask() const noexcept {
    // Every bit below the PFN window is architectural/software flag space.
    return (std::uint64_t{1} << pfnLowBit) - std::uint64_t{1};
  }
  // Coverage in bytes of one slot at the given depth (root depth == 0).
  std::uint64_t coverage(unsigned depth) const noexcept {
    unsigned shift = offsetBits + levelBits * (levels - 1 - depth);
    return std::uint64_t{1} << shift;
  }
  std::uint64_t indexOf(unsigned depth, std::uint64_t va) const noexcept {
    unsigned shift = offsetBits + levelBits * (levels - 1 - depth);
    return (va >> shift) & indexMask();
  }
  // True for a canonical virtual address of this geometry.
  bool canonicalVa(std::uint64_t va) const noexcept {
    std::uint64_t bits = vaBits();
    if (bits >= 64) return true;
    std::uint64_t lastValidBit = std::uint64_t{1} << (bits - 1);
    std::uint64_t upperBits = va & ~((lastValidBit << 1) - 1);
    bool signBit = (va & lastValidBit) != 0;
    if (!signBit) return upperBits == 0;
    return upperBits == ~((lastValidBit << 1) - 1);
  }
};

// Abstract physical memory backend. An embedder supplies any implementation
// (flat buffer, EPT/VMCB snapshot, crash dump parser, ...). Multi-byte
// accesses use the x86 little-endian PTE encoding.
class IPhysicalMemory {
 public:
  virtual ~IPhysicalMemory() = default;
  virtual bool readBytes(std::uint64_t paddr, void *dst, std::size_t bytes) = 0;

  bool readU64(std::uint64_t paddr, std::uint64_t *out) {
    std::uint8_t raw[8] = {};
    if (!readBytes(paddr, raw, sizeof(raw))) return false;
    std::uint64_t value = 0;
    for (unsigned i = 0; i < 8; ++i) {
      value |= std::uint64_t{raw[i]} << (8 * i);
    }
    *out = value;
    return true;
  }
};

// Simple flat byte-array backend, suitable for tests and for hosts that hold
// a complete guest-physical snapshot.
class FlatPhysicalMemory final : public IPhysicalMemory {
 public:
  explicit FlatPhysicalMemory(std::size_t bytes)
      : buffer_(bytes, std::uint8_t{0}) {}

  bool readBytes(std::uint64_t paddr, void *dst,
                 std::size_t bytes) override {
    if (paddr > buffer_.size() || bytes > buffer_.size() - paddr) {
      return false;
    }
    std::copy(buffer_.begin() + static_cast<std::ptrdiff_t>(paddr),
              buffer_.begin() + static_cast<std::ptrdiff_t>(paddr + bytes),
              static_cast<std::uint8_t *>(dst));
    return true;
  }

  bool writeBytes(std::uint64_t paddr, const void *src, std::size_t bytes) {
    if (paddr > buffer_.size() || bytes > buffer_.size() - paddr) {
      return false;
    }
    std::copy(static_cast<const std::uint8_t *>(src),
              static_cast<const std::uint8_t *>(src) + bytes,
              buffer_.begin() + static_cast<std::ptrdiff_t>(paddr));
    return true;
  }
  bool writeU64(std::uint64_t paddr, std::uint64_t value) {
    std::uint8_t raw[8];
    for (unsigned i = 0; i < 8; ++i) {
      raw[i] = static_cast<std::uint8_t>(value >> (8 * i));
    }
    return writeBytes(paddr, raw, sizeof(raw));
  }
  std::size_t size() const noexcept { return buffer_.size(); }

 private:
  std::vector<std::uint8_t> buffer_;
};

// Bit-mask of finding categories attached to a single leaf/terminal witness.
enum FindingKind : unsigned {
  FIND_NONE = 0,
  // User CR3 maps a kernel-sensitive physical page outside the trampoline
  // whitelist (covers 1 GiB/2 MiB partial overlaps).
  FIND_USER_SENSITIVE = 1u << 0,
  // A non-whitelisted kernel mapping is reachable from user mode (U/S=1).
  FIND_USER_BIT = 1u << 1,
  // A non-whitelisted kernel mapping keeps the global bit (G=1), leaking TLB
  // entries across PCID/CR3 switches.
  FIND_GLOBAL_BIT = 1u << 2,
  // U/S=1 and NX=0 on a kernel code page: SMEP violation.
  FIND_SMEP = 1u << 3,
  // W=1 and NX=0 on a kernel page: writable+executable W^X conflict.
  FIND_WXORX = 1u << 4,
  // A non-present / PROT_NONE terminal still encodes a PFN inside real RAM,
  // which L1 terminal-fault speculation can load (L1TF/Foreshadow).
  FIND_L1TF = 1u << 5,
  // The user shadow root table still has a present upper-half (kernel) slot.
  FIND_KERNEL_HALF_PRESENT = 1u << 6,
};

// Raw entries along the PML4 -> PT path (unused levels are zero).
struct EntryWitness {
  std::uint64_t va = 0;                 // base virtual address of the leaf
  std::uint64_t lowestViolationVa = 0;  // lowest offending address inside it
  std::uint64_t physBase = 0;           // decoded physical base of the leaf
  std::uint64_t span = 0;               // mapped/terminal span in bytes
  unsigned depth = 0;                   // 0=PML4 ... 3=PT
  unsigned kinds = 0;                   // bitmask of FindingKind
  std::uint64_t raw[4] = {0, 0, 0, 0};
};

struct AuditParams {
  Geometry geometry = Geometry::x86_64();
  std::uint64_t kernelCr3 = 0;  // physical base of the kernel PML4
  std::uint64_t userCr3 = 0;    // physical base of the user shadow PML4
  std::uint64_t maxRam = 0;     // exclusive bound of real physical RAM
  std::vector<PhysRange> sensitive;       // kernel-sensitive physical ranges
  std::vector<PhysRange> kernelCode;      // executable kernel code ranges
  std::vector<TrampolinePage> trampolines;
};

struct AuditReport {
  bool ok() const noexcept {
    return findings.empty() && structuralErrors.empty();
  }
  std::vector<EntryWitness> findings;
  std::vector<std::string> structuralErrors;
  std::uint64_t presentLeaves = 0;
  std::uint64_t nonPresentTerminals = 0;
  std::uint64_t protNoneLeaves = 0;
  std::uint64_t entriesScanned = 0;
  std::uint64_t whitelistedLeaves = 0;

  // Lowest virtual address carrying any of the requested finding kinds.
  bool lowest(unsigned kindMask, std::uint64_t *vaOut,
              EntryWitness *witnessOut = nullptr) const noexcept {
    const EntryWitness *best = nullptr;
    for (const EntryWitness &finding : findings) {
      if ((finding.kinds & kindMask) == 0) continue;
      if (best == nullptr ||
          finding.lowestViolationVa < best->lowestViolationVa) {
        best = &finding;
      }
    }
    if (best == nullptr) return false;
    *vaOut = best->lowestViolationVa;
    if (witnessOut != nullptr) *witnessOut = *best;
    return true;
  }
};

namespace detail {

inline std::vector<PhysRange> normalizeRanges(std::vector<PhysRange> ranges) {
  ranges.erase(std::remove_if(ranges.begin(), ranges.end(),
                              [](PhysRange r) { return r.end <= r.begin; }),
               ranges.end());
  std::sort(ranges.begin(), ranges.end(),
            [](PhysRange a, PhysRange b) { return a.begin < b.begin; });
  std::vector<PhysRange> merged;
  for (PhysRange range : ranges) {
    if (!merged.empty() && range.begin <= merged.back().end) {
      if (range.end > merged.back().end) merged.back().end = range.end;
    } else {
      merged.push_back(range);
    }
  }
  return merged;
}

// Intersect a physical mapping with a sorted range set, returning the
// intersections expressed as byte offsets inside [mapBase, mapBase + span).
inline std::vector<PhysRange> intersectRanges(
    std::uint64_t mapBase, std::uint64_t span,
    const std::vector<PhysRange> &ranges) {
  std::vector<PhysRange> hits;
  if (span == 0) return hits;
  std::uint64_t mapEnd = mapBase + span;
  auto it = std::lower_bound(ranges.begin(), ranges.end(), mapBase,
                             [](PhysRange r, std::uint64_t value) {
                               return r.end <= value;
                             });
  for (; it != ranges.end() && it->begin < mapEnd; ++it) {
    std::uint64_t lo = std::max(it->begin, mapBase);
    std::uint64_t hi = std::min(it->end, mapEnd);
    if (hi > lo) hits.push_back(PhysRange{lo - mapBase, hi - mapBase});
  }
  return hits;
}

}  // namespace detail

class Auditor {
 public:
  explicit Auditor(IPhysicalMemory &memory) : memory_(memory) {}

  AuditReport run(const AuditParams &params) {
    params_ = params;
    sensitive_ = detail::normalizeRanges(params.sensitive);
    kernelCode_ = detail::normalizeRanges(params.kernelCode);
    trampolines_ = params.trampolines;
    std::sort(trampolines_.begin(), trampolines_.end(),
              [](const TrampolinePage &a, const TrampolinePage &b) {
                return a.vaddr < b.vaddr;
              });
    validateInputs();
    if (!report_.structuralErrors.empty()) return finish();

    walkTree(params_.kernelCr3, /*isUserTree=*/false);
    walkTree(params_.userCr3, /*isUserTree=*/true);
    return finish();
  }

  // Decode the physical base of a present entry, masking all flag bits and
  // aligning to the leaf span (huge pages reserve low flag bits inside the
  // PFN window).
  std::uint64_t presentPhysBase(std::uint64_t entry,
                                std::uint64_t span) const {
    const Geometry &g = params_.geometry;
    std::uint64_t pfn = (entry >> g.pfnLowBit) & g.pfnMask();
    std::uint64_t base = pfn << g.pfnLowBit;
    return base - (base % span);
  }

  // Decode the raw PFN field the CPU may feed to the L1 data cache on a
  // terminal fault (Swap PTE / non-present / PROT_NONE). The NX bit lives
  // outside the PFN window on both supported geometries and is excluded.
  std::uint64_t l1tfPhysBase(std::uint64_t entry) const {
    const Geometry &g = params_.geometry;
    std::uint64_t pfn = (entry >> g.pfnLowBit) & g.pfnMask();
    return pfn << g.pfnLowBit;
  }

 private:
  static bool bit(std::uint64_t value, unsigned position) noexcept {
    return ((value >> position) & std::uint64_t{1}) != 0;
  }

  void validateInputs() {
    const Geometry &g = params_.geometry;
    std::uint64_t tableBytes = g.entriesPerTable() * 8;
    if (g.levels != 4) {
      report_.structuralErrors.push_back("geometry must describe 4 levels");
    }
    if (g.pfnHighBit <= g.pfnLowBit || g.pfnHighBit >= 64) {
      report_.structuralErrors.push_back("invalid PFN window");
    }
    for (std::uint64_t cr3 : {params_.kernelCr3, params_.userCr3}) {
      if (cr3 % tableBytes != 0) {
        report_.structuralErrors.push_back(
            "CR3 root table is not table aligned");
      }
    }
    for (const PhysRange &range : sensitive_) {
      if (range.end < range.begin) {
        report_.structuralErrors.push_back("sensitive range overflow");
      }
    }
    std::uint64_t pageSize = g.pageSize();
    for (const TrampolinePage &trampoline : trampolines_) {
      if (trampoline.vaddr % pageSize != 0 ||
          trampoline.paddr % pageSize != 0) {
        report_.structuralErrors.push_back(
            "trampoline page is not page aligned");
      }
    }
    if (params_.maxRam != 0) {
      for (const PhysRange &range : sensitive_) {
        if (range.begin >= params_.maxRam) {
          report_.structuralErrors.push_back(
              "sensitive range starts beyond maxRAM");
        }
      }
    }
  }

  AuditReport finish() {
    std::sort(report_.findings.begin(), report_.findings.end(),
              [](const EntryWitness &a, const EntryWitness &b) {
                if (a.va != b.va) return a.va < b.va;
                return a.depth < b.depth;
              });
    return std::move(report_);
  }

  void walkTree(std::uint64_t rootPhys, bool isUserTree) {
    std::vector<std::uint64_t> chain(params_.geometry.levels, 0);
    std::uint64_t pathStack[4] = {0, 0, 0, 0};
    walkLevel(rootPhys, /*depth=*/0, /*vaBase=*/0, chain, pathStack,
              isUserTree);
  }

  void walkLevel(std::uint64_t tablePhys, unsigned depth,
                 std::uint64_t vaBase, std::vector<std::uint64_t> &chain,
                 std::uint64_t *pathStack, bool isUserTree) {
    const Geometry &g = params_.geometry;
    if (depth >= g.levels) return;
    // pathStack is sized for the fixed 4-level PML4/PDPT/PD/PT walk.
    assert(depth < 4);
    // Only ancestors on the current root-to-node path can form a cycle; a
    // shared table reached through multiple parents is legitimate.
    for (unsigned i = 0; i < depth; ++i) {
      if (pathStack[i] == tablePhys) {
        report_.structuralErrors.push_back(
            "page-table cycle detected; subtree truncated");
        return;
      }
    }
    pathStack[depth] = tablePhys;

    std::uint64_t slotSpan = g.coverage(depth);
    std::uint64_t entries = g.entriesPerTable();
    for (std::uint64_t slot = 0; slot < entries; ++slot) {
      std::uint64_t entryPhys = tablePhys + slot * 8;
      std::uint64_t entry = 0;
      if (!memory_.readU64(entryPhys, &entry)) {
        report_.structuralErrors.push_back(
            "unreadable page-table entry at physical address");
        continue;
      }
      ++report_.entriesScanned;
      std::uint64_t va = vaBase + slot * slotSpan;
      chain[depth] = entry;

      if (depth == 0 && isUserTree && slot >= g.userSplitEntries() &&
          bit(entry, g.presentBit)) {
        EntryWitness witness =
            makeWitness(va, depth, entry, chain, slotSpan);
        witness.kinds |= FIND_KERNEL_HALF_PRESENT;
        report_.findings.push_back(witness);
      }

      if (!bit(entry, g.presentBit)) {
        // A non-present entry terminates the walk at every level: hardware
        // never descends, yet its encoded PFN can still drive L1 speculative
        // loads (L1TF).
        handleTerminal(va, depth, entry, chain, slotSpan,
                       /*presentLeaf=*/false);
        continue;
      }

      bool leaf = depth > 0 && bit(entry, g.hugeBit);
      if (depth == g.levels - 1) leaf = true;  // PT entries are always leaves
      if (leaf) {
        handlePresentLeaf(va, depth, entry, chain, slotSpan, isUserTree);
      } else {
        std::uint64_t nextPhys = presentPhysBase(entry, g.pageSize());
        walkLevel(nextPhys, depth + 1, va, chain, pathStack, isUserTree);
      }
    }
  }

  EntryWitness makeWitness(std::uint64_t va, unsigned depth,
                          std::uint64_t entry,
                          const std::vector<std::uint64_t> &chain,
                          std::uint64_t span) const {
    EntryWitness witness;
    witness.va = va;
    witness.lowestViolationVa = va;
    witness.depth = depth;
    witness.span = span;
    witness.physBase = bit(entry, params_.geometry.presentBit)
                           ? presentPhysBase(entry, span)
                           : l1tfPhysBase(entry);
    for (unsigned i = 0; i < params_.geometry.levels; ++i) {
      witness.raw[i] = chain[i];
    }
    return witness;
  }

  void handleTerminal(std::uint64_t va, unsigned depth, std::uint64_t entry,
                      std::vector<std::uint64_t> &chain,
                      std::uint64_t span, bool presentLeaf) {
    if (presentLeaf) {
      ++report_.protNoneLeaves;
    } else {
      ++report_.nonPresentTerminals;
    }
    if (entry == 0) return;  // no PFN encoded: nothing to speculate with
    std::uint64_t phys = l1tfPhysBase(entry);
    if (phys < params_.maxRam) {
      EntryWitness witness = makeWitness(va, depth, entry, chain, span);
      witness.kinds |= FIND_L1TF;
      report_.findings.push_back(witness);
    }
  }

  bool isProtNone(std::uint64_t entry) const {
    const Geometry &g = params_.geometry;
    return bit(entry, g.presentBit) && !bit(entry, g.rwBit) &&
           !bit(entry, g.userBit) && bit(entry, g.nxBit);
  }

  void handlePresentLeaf(std::uint64_t va, unsigned depth,
                         std::uint64_t entry,
                         std::vector<std::uint64_t> &chain,
                         std::uint64_t span, bool isUserTree) {
    const Geometry &g = params_.geometry;
    ++report_.presentLeaves;
    if (isProtNone(entry)) {
      // PROT_NONE leaves fault architecturally but retain a candidate PFN for
      // L1 terminal-fault speculation.
      handleTerminal(va, depth, entry, chain, span, /*presentLeaf=*/true);
      return;
    }

    std::uint64_t phys = presentPhysBase(entry, span);
    std::vector<PhysRange> sensitiveHits =
        detail::intersectRanges(phys, span, sensitive_);
    bool mapsSensitive = !sensitiveHits.empty();

    if (mapsSensitive) {
      std::uint64_t lowestUncoveredVa = va;
      bool fullyCovered =
          isUserTree &&
          allSensitiveCoveredByTrampoline(va, phys, sensitiveHits,
                                          &lowestUncoveredVa);
      if (isUserTree && !fullyCovered) {
        EntryWitness witness =
            makeWitness(va, depth, entry, chain, span);
        witness.lowestViolationVa = lowestUncoveredVa;
        witness.kinds |= FIND_USER_SENSITIVE;
        if (bit(entry, g.userBit)) witness.kinds |= FIND_USER_BIT;
        if (bit(entry, g.globalBit)) witness.kinds |= FIND_GLOBAL_BIT;
        addSmepexWxorx(witness, entry, phys, span,
                       /*whitelisted=*/false);
        report_.findings.push_back(witness);
      } else if (fullyCovered) {
        ++report_.whitelistedLeaves;
      } else {
        // Kernel tree: audit W^X on kernel-sensitive writable+exec pages.
        EntryWitness witness = makeWitness(va, depth, entry, chain, span);
        addSmepexWxorx(witness, entry, phys, span,
                       /*whitelisted=*/isUserTree);
        if (witness.kinds != FIND_NONE) report_.findings.push_back(witness);
      }
    } else if (isUserTree && bit(entry, g.globalBit)) {
      // Global user pages still defeat KPTI TLB isolation on PCID switches.
      EntryWitness witness = makeWitness(va, depth, entry, chain, span);
      witness.kinds |= FIND_GLOBAL_BIT;
      report_.findings.push_back(witness);
    }
  }

  void addSmepexWxorx(EntryWitness &witness, std::uint64_t entry,
                      std::uint64_t phys, std::uint64_t span,
                      bool whitelisted) const {
    const Geometry &g = params_.geometry;
    bool noExecute = bit(entry, g.nxBit);
    if (!whitelisted && !noExecute && bit(entry, g.userBit) &&
        !detail::intersectRanges(phys, span, kernelCode_).empty()) {
      witness.kinds |= FIND_SMEP;
    }
    if (!noExecute && bit(entry, g.rwBit) &&
        !detail::intersectRanges(phys, span, sensitive_).empty()) {
      witness.kinds |= FIND_WXORX;
    }
  }

  // True only when every sensitive byte of the leaf is an identity-matched
  // trampoline page (identical virtual and physical offset). Reports the
  // lowest virtual address left uncovered.
  bool allSensitiveCoveredByTrampoline(
      std::uint64_t va, std::uint64_t phys,
      const std::vector<PhysRange> &sensitiveOffsets,
      std::uint64_t *lowestUncoveredVa) const {
    std::uint64_t pageSize = params_.geometry.pageSize();
    bool anyUncovered = false;
    for (const PhysRange &fragment : sensitiveOffsets) {
      std::uint64_t firstPage = fragment.begin - (fragment.begin % pageSize);
      std::uint64_t lastPage =
          (fragment.end - 1) - ((fragment.end - 1) % pageSize);
      std::uint64_t cursor = firstPage;
      while (cursor < fragment.end) {
        bool covered = std::any_of(
            trampolines_.begin(), trampolines_.end(),
            [&](const TrampolinePage &trampoline) {
              return trampoline.vaddr == va + cursor &&
                     trampoline.paddr == phys + cursor;
            });
        if (!covered) {
          if (!anyUncovered || va + cursor < *lowestUncoveredVa) {
            *lowestUncoveredVa = va + cursor;
          }
          anyUncovered = true;
        }
        if (cursor == lastPage) break;
        cursor += pageSize;
      }
    }
    return !anyUncovered;
  }

  IPhysicalMemory &memory_;
  AuditParams params_;
  AuditReport report_;
  std::vector<PhysRange> sensitive_;
  std::vector<PhysRange> kernelCode_;
  std::vector<TrampolinePage> trampolines_;
};

}  // namespace kpti

#endif  // KPTI_AUDIT_HPP
