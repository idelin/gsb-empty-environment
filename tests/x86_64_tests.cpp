#include "kpti/audit.hpp"

#include <cassert>
#include <cstdio>
#include <vector>

namespace {

constexpr std::uint64_t P = 1ull << 0;
constexpr std::uint64_t W = 1ull << 1;
constexpr std::uint64_t U = 1ull << 2;
constexpr std::uint64_t PS = 1ull << 7;
constexpr std::uint64_t G = 1ull << 8;
constexpr std::uint64_t NX = 1ull << 63;

// Physical frame allocator over a flat guest-physical snapshot.
class Frames {
 public:
  explicit Frames(kpti::FlatPhysicalMemory &memory) : memory_(memory) {}

  std::uint64_t allocTable() {
    std::uint64_t address = cursor_;
    cursor_ += 4096;
    assert(cursor_ <= memory_.size());
    return address;
  }

  void set(std::uint64_t table, unsigned slot, std::uint64_t entry) {
    bool written = memory_.writeU64(table + slot * 8, entry);
    assert(written);
  }

 private:
  kpti::FlatPhysicalMemory &memory_;
  std::uint64_t cursor_ = 0;
};

// Builds a path of four tables for one virtual address and returns the four
// table physical bases (PML4, PDPT, PD, PT).
struct Path {
  std::uint64_t table[4];
  unsigned index[4];
};

Path buildPath(Frames &frames, std::uint64_t root, std::uint64_t va) {
  const kpti::Geometry geom = kpti::Geometry::x86_64();
  Path path{};
  path.table[0] = root;
  for (unsigned depth = 0; depth < 4; ++depth) {
    path.index[depth] =
        static_cast<unsigned>(geom.indexOf(depth, va));
    if (depth < 3) {
      path.table[depth + 1] = frames.allocTable();
      frames.set(path.table[depth], path.index[depth],
                 path.table[depth + 1] | P | W | U);
    }
  }
  return path;
}

bool hasKind(const kpti::AuditReport &report, unsigned kind) {
  for (const auto &finding : report.findings) {
    if ((finding.kinds & kind) != 0) return true;
  }
  return false;
}

unsigned countGlobal(const kpti::AuditReport &report) {
  unsigned count = 0;
  for (const auto &finding : report.findings) {
    if ((finding.kinds & kpti::FIND_GLOBAL_BIT) != 0) ++count;
  }
  return count;
}

kpti::AuditParams baseParams(std::uint64_t kernelCr3,
                             std::uint64_t userCr3) {
  kpti::AuditParams params;
  params.kernelCr3 = kernelCr3;
  params.userCr3 = userCr3;
  params.maxRam = 0x40000000ull;  // 1 GiB
  params.sensitive = {kpti::PhysRange{0x100000, 0x200000}};
  params.kernelCode = {kpti::PhysRange{0x180000, 0x190000}};
  return params;
}

void testCleanTrees() {
  kpti::FlatPhysicalMemory memory(2 * 1024 * 1024);
  Frames frames(memory);
  std::uint64_t kernelRoot = frames.allocTable();
  std::uint64_t userRoot = frames.allocTable();

  // Kernel maps a sensitive page supervisor-only and NX.
  Path kernelPath = buildPath(frames, kernelRoot, 0xffff800000001000ull);
  frames.set(kernelPath.table[3], kernelPath.index[3], 0x100000 | P | W | NX);

  // User maps ordinary RAM below the sensitive range.
  Path userPath = buildPath(frames, userRoot, 0x400000);
  frames.set(userPath.table[3], userPath.index[3], 0x300000 | P | W | U | NX);

  kpti::Auditor auditor(memory);
  kpti::AuditReport report = auditor.run(baseParams(kernelRoot, userRoot));
  assert(report.structuralErrors.empty());
  assert(report.ok());
}

void testUserSensitiveLeak() {
  kpti::FlatPhysicalMemory memory(2 * 1024 * 1024);
  Frames frames(memory);
  std::uint64_t kernelRoot = frames.allocTable();
  std::uint64_t userRoot = frames.allocTable();

  Path userPath = buildPath(frames, userRoot, 0x400000);
  frames.set(userPath.table[3], userPath.index[3], 0x100000 | P | W | U);

  kpti::Auditor auditor(memory);
  kpti::AuditReport report = auditor.run(baseParams(kernelRoot, userRoot));
  assert(hasKind(report, kpti::FIND_USER_SENSITIVE));
  assert(hasKind(report, kpti::FIND_USER_BIT));

  std::uint64_t lowest = 0;
  kpti::EntryWitness witness{};
  assert(report.lowest(kpti::FIND_USER_SENSITIVE, &lowest, &witness));
  assert(lowest == 0x400000);
  assert(witness.raw[0] != 0 && witness.raw[3] != 0);
  assert(witness.span == 4096);
}

void testHugePagePartialOverlap() {
  kpti::FlatPhysicalMemory memory(4 * 1024 * 1024);
  Frames frames(memory);
  std::uint64_t kernelRoot = frames.allocTable();
  std::uint64_t userRoot = frames.allocTable();

  // 2 MiB user leaf at physical 0, partially overlapping sensitive
  // [0x100000, 0x200000).
  Path userPath = buildPath(frames, userRoot, 0x200000);
  frames.set(userPath.table[2], userPath.index[2], PS | P | U | 0);

  kpti::AuditParams params = baseParams(kernelRoot, userRoot);
  kpti::Auditor auditor(memory);
  kpti::AuditReport report = auditor.run(params);
  assert(hasKind(report, kpti::FIND_USER_SENSITIVE));

  std::uint64_t lowest = 0;
  kpti::EntryWitness witness{};
  assert(report.lowest(kpti::FIND_USER_SENSITIVE, &lowest, &witness));
  // Lowest covered page inside the huge leaf is the sensitive start.
  assert(lowest == 0x200000 + 0x100000);
  assert(witness.span == 2 * 1024 * 1024);
}

void testTrampolineWhitelist() {
  kpti::FlatPhysicalMemory memory(2 * 1024 * 1024);
  Frames frames(memory);
  std::uint64_t kernelRoot = frames.allocTable();
  std::uint64_t userRoot = frames.allocTable();

  constexpr std::uint64_t trampVa = 0x700000ull;
  constexpr std::uint64_t trampPa = 0x100000;
  Path userPath = buildPath(frames, userRoot, trampVa);
  frames.set(userPath.table[3], userPath.index[3], trampPa | P | U | NX);

  kpti::AuditParams params = baseParams(kernelRoot, userRoot);
  params.trampolines = {kpti::TrampolinePage{trampVa, trampPa}};
  kpti::Auditor auditor(memory);
  kpti::AuditReport report = auditor.run(params);
  assert(report.structuralErrors.empty());
  assert(!hasKind(report, kpti::FIND_USER_SENSITIVE));
  assert(report.whitelistedLeaves == 1);

  // A mismatched physical address must not be whitelisted.
  Path badPath = buildPath(frames, userRoot, trampVa + 0x2000);
  frames.set(badPath.table[3], badPath.index[3], 0x110000 | P | U | NX);
  kpti::AuditReport badReport = auditor.run(params);
  assert(hasKind(badReport, kpti::FIND_USER_SENSITIVE));
}

void testL1tfSwapPte() {
  kpti::FlatPhysicalMemory memory(2 * 1024 * 1024);
  Frames frames(memory);
  std::uint64_t kernelRoot = frames.allocTable();
  std::uint64_t userRoot = frames.allocTable();

  Path userPath = buildPath(frames, userRoot, 0x400000);
  // Present=0 swap entry whose PFN still points inside real RAM.
  std::uint64_t swapPte = (0x12345ull << 12);
  frames.set(userPath.table[3], userPath.index[3], swapPte);

  kpti::Auditor auditor(memory);
  kpti::AuditReport report = auditor.run(baseParams(kernelRoot, userRoot));
  assert(hasKind(report, kpti::FIND_L1TF));

  // Proper mitigation: invert the PFN so it lands beyond maxRAM.
  std::uint64_t inverted = (~0x12345ull & ((1ull << 40) - 1)) << 12;
  frames.set(userPath.table[3], userPath.index[3], inverted);
  kpti::AuditReport mitigated = auditor.run(baseParams(kernelRoot, userRoot));
  assert(!hasKind(mitigated, kpti::FIND_L1TF));

  // PROT_NONE present leaf with a RAM PFN is equally vulnerable.
  frames.set(userPath.table[3], userPath.index[3], 0x100000 | P | NX);
  kpti::AuditReport protNone = auditor.run(baseParams(kernelRoot, userRoot));
  assert(hasKind(protNone, kpti::FIND_L1TF));
  assert(protNone.protNoneLeaves == 1);
}

void testSmepAndWxorx() {
  kpti::FlatPhysicalMemory memory(2 * 1024 * 1024);
  Frames frames(memory);
  std::uint64_t kernelRoot = frames.allocTable();
  std::uint64_t userRoot = frames.allocTable();

  // User-accessible, executable mapping of kernel code: SMEP violation.
  Path smepPath = buildPath(frames, userRoot, 0x400000);
  frames.set(smepPath.table[3], smepPath.index[3], 0x180000 | P | U);

  // Kernel writable+executable sensitive page: W^X conflict.
  Path wxPath = buildPath(frames, kernelRoot, 0xffff800000002000ull);
  frames.set(wxPath.table[3], wxPath.index[3], 0x100000 | P | W);

  kpti::Auditor auditor(memory);
  kpti::AuditReport report = auditor.run(baseParams(kernelRoot, userRoot));
  assert(hasKind(report, kpti::FIND_SMEP));
  assert(hasKind(report, kpti::FIND_WXORX));
}

void testKernelHalfPresent() {
  kpti::FlatPhysicalMemory memory(2 * 1024 * 1024);
  Frames frames(memory);
  std::uint64_t kernelRoot = frames.allocTable();
  std::uint64_t userRoot = frames.allocTable();

  std::uint64_t leakedTable = frames.allocTable();
  frames.set(userRoot, 511, leakedTable | P | W);

  kpti::Auditor auditor(memory);
  kpti::AuditReport report = auditor.run(baseParams(kernelRoot, userRoot));
  assert(hasKind(report, kpti::FIND_KERNEL_HALF_PRESENT));
}

void testGlobalBitLeak() {
  kpti::FlatPhysicalMemory memory(2 * 1024 * 1024);
  Frames frames(memory);
  std::uint64_t kernelRoot = frames.allocTable();
  std::uint64_t userRoot = frames.allocTable();

  // Global user mapping to a sensitive page.
  Path sensitivePath = buildPath(frames, userRoot, 0x400000);
  frames.set(sensitivePath.table[3], sensitivePath.index[3],
             0x100000 | P | U | G | NX);
  // Global ordinary user page also trips the KPTI TLB check.
  Path ordinaryPath = buildPath(frames, userRoot, 0x600000);
  frames.set(ordinaryPath.table[3], ordinaryPath.index[3],
             0x300000 | P | U | G | NX);

  kpti::Auditor auditor(memory);
  kpti::AuditReport report = auditor.run(baseParams(kernelRoot, userRoot));
  assert(hasKind(report, kpti::FIND_GLOBAL_BIT));
  assert(countGlobal(report) == 1);
}

void testMisalignedCr3Rejected() {
  kpti::FlatPhysicalMemory memory(4096 * 8);
  kpti::Auditor auditor(memory);
  kpti::AuditParams params = baseParams(8, 0);
  kpti::AuditReport report = auditor.run(params);
  assert(!report.structuralErrors.empty());
  assert(!report.ok());
}

void testGeometryPrimitives() {
  const kpti::Geometry geom = kpti::Geometry::x86_64();
  assert(geom.vaBits() == 48);
  assert(geom.entriesPerTable() == 512);
  assert(geom.coverage(0) == 512ull * 512 * 512 * 4096);
  assert(geom.coverage(3) == 4096);
  assert(geom.canonicalVa(0x00007fffffffffffull));
  assert(geom.canonicalVa(0xffff800000000000ull));
  assert(!geom.canonicalVa(0x0000800000000000ull));

  kpti::FlatPhysicalMemory memory(4096);
  kpti::Auditor auditor(memory);
  // 1 GiB leaf: low PFN-window bits are reserved and must be realigned.
  std::uint64_t gigaPte = (0x40000000ull >> 12) << 12;
  assert(auditor.presentPhysBase(gigaPte, 0x40000000ull) == 0x40000000ull);
  // Swap PTE: only the 40-bit PFN window is decoded, NX excluded.
  std::uint64_t swap = (0x12345ull << 12) | (1ull << 63);
  assert(auditor.l1tfPhysBase(swap) == 0x12345000ull);

  const kpti::Geometry mini = kpti::Geometry::mini16();
  assert(mini.vaBits() == 16);
  assert(mini.pageSize() == 16);
  assert(mini.entriesPerTable() == 8);
  assert(mini.userSplitEntries() == 4);
}

void testOneGigabytePartialOverlap() {
  kpti::FlatPhysicalMemory memory(4 * 1024 * 1024);
  Frames frames(memory);
  std::uint64_t kernelRoot = frames.allocTable();
  std::uint64_t userRoot = frames.allocTable();

  // 1 GiB PDPT leaf at physical 0; the sensitive range sits near its start.
  Path userPath = buildPath(frames, userRoot, 0x40000000ull);
  frames.set(userPath.table[1], userPath.index[1], PS | P | U);

  kpti::AuditParams params = baseParams(kernelRoot, userRoot);
  kpti::Auditor auditor(memory);
  kpti::AuditReport report = auditor.run(params);
  assert(hasKind(report, kpti::FIND_USER_SENSITIVE));
  std::uint64_t lowest = 0;
  kpti::EntryWitness witness{};
  assert(report.lowest(kpti::FIND_USER_SENSITIVE, &lowest, &witness));
  assert(lowest == 0x40000000ull + 0x100000);
  assert(witness.span == 0x40000000ull);
  assert(witness.depth == 1);
}

void testHighLevelL1tfTerminal() {
  kpti::FlatPhysicalMemory memory(2 * 1024 * 1024);
  Frames frames(memory);
  std::uint64_t kernelRoot = frames.allocTable();
  std::uint64_t userRoot = frames.allocTable();

  // Non-present PDPT entry still encoding a RAM PFN: terminal at depth 1,
  // covering a 1 GiB VA region that L1 speculation could target.
  Path userPath = buildPath(frames, userRoot, 0x80000000ull);
  std::uint64_t swapPte = (0xabcull << 12);
  frames.set(userPath.table[1], userPath.index[1], swapPte);

  kpti::Auditor auditor(memory);
  kpti::AuditReport report = auditor.run(baseParams(kernelRoot, userRoot));
  assert(hasKind(report, kpti::FIND_L1TF));
  std::uint64_t lowest = 0;
  kpti::EntryWitness witness{};
  assert(report.lowest(kpti::FIND_L1TF, &lowest, &witness));
  assert(lowest == 0x80000000ull);
  assert(witness.depth == 1);
  assert(witness.span == 0x40000000ull);
}

}  // namespace

int main() {
  testGeometryPrimitives();
  testCleanTrees();
  testUserSensitiveLeak();
  testHugePagePartialOverlap();
  testTrampolineWhitelist();
  testL1tfSwapPte();
  testSmepAndWxorx();
  testKernelHalfPresent();
  testGlobalBitLeak();
  testMisalignedCr3Rejected();
  testOneGigabytePartialOverlap();
  testHighLevelL1tfTerminal();
  std::printf("x86-64 functional tests passed\n");
  return 0;
}
