#include "vtd_policy.hpp"

#include <cstdio>
#include <cstdlib>

using namespace vtd;

namespace {

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                    \
  do {                                                                 \
    ++g_checks;                                                        \
    if (!(cond)) {                                                     \
      ++g_failures;                                                    \
      std::printf("FAIL %s:%d  %s\n", __FILE__, __LINE__, #cond);      \
    }                                                                  \
  } while (0)

#define CHECK_EQ(actual, expected)                                     \
  do {                                                                 \
    ++g_checks;                                                        \
    auto actual_value = (actual);                                      \
    auto expected_value = (expected);                                  \
    if (!(actual_value == expected_value)) {                           \
      ++g_failures;                                                    \
      std::printf("FAIL %s:%d  %s == %s (got %lld, want %lld)\n",      \
                  __FILE__, __LINE__, #actual, #expected,              \
                  static_cast<long long>(actual_value),                \
                  static_cast<long long>(expected_value));             \
    }                                                                  \
  } while (0)

// ---- PTE builders ---------------------------------------------------------

u64 leafPte(u64 phys, bool write = true, bool execute = false) {
  SlPte pte;
  pte.present = true;
  pte.read = true;
  pte.write = write;
  pte.execute = execute;
  pte.address = phys;
  return encodeSlPte(pte);
}

u64 superPte(u64 phys, unsigned shift, bool write = true) {
  SlPte pte;
  pte.present = true;
  pte.read = true;
  pte.write = write;
  pte.superpage = true;
  pte.address = phys;
  (void)shift;  // shift only determines placement in the walk
  return encodeSlPte(pte);
}

u64 linkPte(u64 nextTable) {
  SlPte pte;
  pte.present = true;
  pte.read = true;
  pte.address = nextTable;
  return encodeSlPte(pte);
}

// ---- IRTE builder ---------------------------------------------------------

Raw128 makeIrte(u16 sid, u8 svt, u8 sq, u8 delivery, u32 dest,
                bool present = true, u8 vector = 0x40) {
  Irte irte;
  irte.present = present;
  irte.sourceId = sid;
  irte.sourceValidationType = svt;
  irte.sourceQualifier = sq;
  irte.deliveryMode = delivery;
  irte.destinationLapicId = dest;
  irte.vector = vector;
  return encodeIrte(irte);
}

// ---- Canonical topology constants -----------------------------------------

constexpr u64 kCtxBase = 0x10000ull;
constexpr u64 kPasidDirBase = 0x20000ull;
constexpr u64 kPasidTableBase = 0x30000ull;
constexpr u64 kSlRootVm0 = 0x40000ull;
constexpr u64 kSlRootForeign = 0x90000ull;
constexpr u16 kVm0 = 0;
constexpr u16 kVmForeign = 7;

// A second-level node address family keeps distinct tables apart.
constexpr u64 kSlL3 = 0x41000ull;
constexpr u64 kSlL2 = 0x42000ull;
constexpr u64 kSlL1 = 0x43000ull;

constexpr Region kGuestRam{0x100000000ull, 0x37FFFFFFFull};
constexpr Region kHostKernel{0x400000000ull, 0x40FFFFFFFull};
constexpr Region kMmioHole{0x800000000ull, 0x800000FFFull};

// Build the root/context/PASID-directory/PASID-table chain for one device
// and install one PASID entry (raw). Returns the chosen PASID value.
u32 installDevice(PolicyInput& policy, u16 bdf, Raw128 pasidRaw,
                  u8 pasidBits = 12) {
  const u8 bus = busOf(bdf);
  const u16 devfn =
      static_cast<u16>((static_cast<u32>(devOf(bdf)) << 3) |
                       funcOf(bdf));

  RootEntry root;
  root.present = true;
  root.contextTableBase = kCtxBase + static_cast<u64>(bdf) * 0x1000ull;
  policy.rootTable[bus] = encodeRootEntry(root);

  ContextEntry context;
  context.present = true;
  context.pasidDirEnable = true;
  context.pasidBits = pasidBits;
  context.domainId = 0;
  context.pasidDirBase =
      kPasidDirBase + static_cast<u64>(bdf) * 0x1000ull;
  policy.contextTables[root.contextTableBase][devfn] =
      encodeContextEntry(context);

  const u32 dirIndex = 0;
  PasidDirEntry dirEntry;
  dirEntry.present = true;
  dirEntry.pasidTableBase =
      kPasidTableBase + static_cast<u64>(bdf) * 0x1000ull;
  policy.pasidDirectories[context.pasidDirBase][dirIndex] =
      encodePasidDirEntry(dirEntry);

  const u32 pasid = 3;
  policy.pasidTables[dirEntry.pasidTableBase][pasid] = pasidRaw;
  return pasid;
}

PasidEntry baseNestedPasid(u64 slRoot) {
  PasidEntry entry;
  entry.present = true;
  entry.pgtt = kPgttNested;
  entry.secondLevelRoot = slRoot;
  entry.domainId = 0;
  return entry;
}

PolicyInput basePolicy() {
  PolicyInput policy;
  policy.legalPhysical = RegionSet(std::vector<Region>{
      kGuestRam,
      {0x500000000ull, 0x6FFFFFFFFull},
      kHostKernel, kMmioHole});
  policy.protectedPhysical =
      RegionSet(std::vector<Region>{kHostKernel, kMmioHole});
  VmConfig vm;
  vm.vmId = kVm0;
  vm.allowedSlRoots.insert(kSlRootVm0);
  policy.vms[kVm0] = vm;
  VmConfig foreign;
  foreign.vmId = kVmForeign;
  foreign.allowedSlRoots.insert(kSlRootForeign);
  policy.vms[kVmForeign] = foreign;
  policy.authorizedHighPriorityLapics.insert(0);
  return policy;
}

}  // namespace

// Test 1: golden clean nested configuration, no violations.
void testGoldenClean() {
  PolicyInput policy = basePolicy();
  const u16 bdf = packBdf(0x03, 0, 0);
  const u32 pasid =
      installDevice(policy, bdf,
                    encodePasidEntry(baseNestedPasid(kSlRootVm0)));
  policy.deviceVm[bdf] = kVm0;

  policy.slPool[kSlRootVm0][0] = linkPte(kSlL3);
  policy.slPool[kSlL3][1] = linkPte(kSlL2);
  policy.slPool[kSlL2][2] = linkPte(kSlL1);
  policy.slPool[kSlL1][7] = leafPte(kGuestRam.base);

  const VerifyResult result = verify(policy);
  CHECK(result.ok());
  CHECK_EQ(result.stats.devicesAudited, 1u);
  CHECK_EQ(result.stats.pasidEntriesAudited, 1u);
  CHECK_EQ(result.stats.slMappingsAudited, 1u);
  CHECK_EQ(result.stats.irteAudited, 0u);
  (void)pasid;
}

// Test 2: Pass-Through PGTT escape is hard-rejected.
void testPassthroughEscape() {
  PolicyInput policy = basePolicy();
  const u16 bdf = packBdf(0x05, 1, 2);
  PasidEntry entry = baseNestedPasid(kSlRootVm0);
  entry.pgtt = kPgttPassthrough;
  installDevice(policy, bdf, encodePasidEntry(entry));
  policy.deviceVm[bdf] = kVm0;

  const VerifyResult result = verify(policy);
  CHECK(!result.ok());
  bool found = false;
  for (const Violation& violation : result.violations) {
    if (violation.kind == ViolationKind::PassthroughPgtt) {
      found = true;
      CHECK_EQ(violation.bdf, bdf);
      CHECK_EQ(violation.pasid, 3u);
    }
  }
  CHECK(found);
}

// Test 3: 1 GB super-page partially overlaps host kernel and crosses out of
// legal machine memory; the minimal violating IOVA must be reported.
void testOneGbPartialOverlap() {
  PolicyInput policy = basePolicy();
  const u16 bdf = packBdf(0x07, 0, 0);
  const u32 pasid =
      installDevice(policy, bdf,
                    encodePasidEntry(baseNestedPasid(kSlRootVm0)));
  policy.deviceVm[bdf] = kVm0;

  // First 0x8000_0000 bytes are guest RAM, then the host kernel is hit and
  // the tail finally falls into an illegal physical hole.
  constexpr u64 kPhys = 0x280000000ull;
  constexpr u64 kProtectedIova = 0x180000000ull;
  policy.slPool[kSlRootVm0][0] = superPte(kPhys, 30);

  const VerifyResult result = verify(policy);
  CHECK_EQ(result.stats.slMappingsAudited, 1u);
  bool overlapFound = false;
  bool illegalFound = false;
  for (const Violation& violation : result.violations) {
    if (violation.kind == ViolationKind::SlProtectedRegionOverlap) {
      overlapFound = true;
      CHECK_EQ(violation.bdf, bdf);
      CHECK_EQ(violation.pasid, pasid);
      CHECK_EQ(violation.iova, kProtectedIova);
    }
    if (violation.kind == ViolationKind::SlIllegalPhysicalRange) {
      illegalFound = true;
      CHECK_EQ(violation.iova, 0x100000000ull);
    }
  }
  CHECK(overlapFound);
  CHECK(illegalFound);
}

// Test 4: 2 MB super-page partially overlaps the protected host kernel.
void testTwoMbPartialOverlap() {
  PolicyInput policy = basePolicy();
  const u16 bdf = packBdf(0x08, 0, 0);
  installDevice(policy, bdf,
                encodePasidEntry(baseNestedPasid(kSlRootVm0)));
  policy.deviceVm[bdf] = kVm0;

  policy.slPool[kSlRootVm0][0] = linkPte(kSlL3);
  policy.slPool[kSlL3][0] = linkPte(kSlL2);
  constexpr u64 kPhys = kHostKernel.base - 0x100000ull;
  policy.slPool[kSlL2][1] = superPte(kPhys, 21);

  const VerifyResult result = verify(policy);
  bool found = false;
  for (const Violation& violation : result.violations) {
    if (violation.kind == ViolationKind::SlProtectedRegionOverlap) {
      found = true;
      CHECK_EQ(violation.iova, 0x300000ull);
    }
  }
  CHECK(found);
}

// Test 5: cross-VM second-level root pointer is rejected.
void testCrossVmRoot() {
  PolicyInput policy = basePolicy();
  const u16 bdf = packBdf(0x09, 0, 0);
  installDevice(policy, bdf,
                encodePasidEntry(baseNestedPasid(kSlRootForeign)));
  policy.deviceVm[bdf] = kVm0;

  const VerifyResult result = verify(policy);
  bool found = false;
  for (const Violation& violation : result.violations) {
    if (violation.kind == ViolationKind::CrossVmSlRoot) {
      found = true;
      CHECK_EQ(violation.detail, kSlRootForeign);
    }
  }
  CHECK(found);
}

// Test 6: 5-level second-level walk produces correct IOVA at PML5.
void testFiveLevelWalk() {
  PolicyInput policy = basePolicy();
  policy.fiveLevelSecondLevel = true;
  const u16 bdf = packBdf(0x0A, 0, 0);
  installDevice(policy, bdf,
                encodePasidEntry(baseNestedPasid(kSlRootVm0)));
  policy.deviceVm[bdf] = kVm0;

  constexpr u64 kPml4 = 0x51000ull;
  constexpr u64 kPml3 = 0x52000ull;
  constexpr u64 kPml2 = 0x53000ull;
  constexpr u64 kPml1 = 0x54000ull;
  policy.slPool[kSlRootVm0][1] = linkPte(kPml4);
  policy.slPool[kPml4][2] = linkPte(kPml3);
  policy.slPool[kPml3][3] = linkPte(kPml2);
  policy.slPool[kPml2][4] = linkPte(kPml1);
  policy.slPool[kPml1][5] = leafPte(kGuestRam.base);

  const VerifyResult result = verify(policy);
  CHECK(result.ok());
  CHECK_EQ(result.stats.slMappingsAudited, 1u);
}

// Test 7: IRTE with loose SQ mask cannot pass for a valid device.
void testLooseSourceQualifier() {
  PolicyInput policy = basePolicy();
  const u16 bdf = packBdf(0x0B, 0, 0);
  installDevice(policy, bdf,
                encodePasidEntry(baseNestedPasid(kSlRootVm0)));
  policy.deviceVm[bdf] = kVm0;

  policy.irteTable[10] =
      makeIrte(bdf, kSvtExact, kSqExact, kIrteDeliveryFixed, 0);
  policy.irteTable[11] =
      makeIrte(bdf, kSvtExact, 3, kIrteDeliveryFixed, 0);

  const VerifyResult result = verify(policy);
  bool looseFound = false;
  bool baselineBad = false;
  for (const Violation& violation : result.violations) {
    if (violation.kind == ViolationKind::IrteLooseSourceQualifier &&
        violation.irteIndex == 11u) {
      looseFound = true;
      CHECK_EQ(violation.bdf, bdf);
      CHECK_EQ(violation.detail, 3u);
    }
    if (violation.irteIndex == 10u) {
      baselineBad = true;
    }
  }
  CHECK(looseFound);
  CHECK(!baselineBad);
}

// Test 8: forged NMI/SMI/ExtINT to unauthorized LAPIC IDs are rejected and
// disabled source validation is rejected; authorized NMI stays clean.
void testForgedHighPriorityInterrupts() {
  PolicyInput policy = basePolicy();
  const u16 bdf = packBdf(0x0C, 0, 0);
  installDevice(policy, bdf,
                encodePasidEntry(baseNestedPasid(kSlRootVm0)));
  policy.deviceVm[bdf] = kVm0;

  policy.irteTable[20] =
      makeIrte(bdf, kSvtExact, kSqExact, kIrteDeliveryNmi, 9);
  policy.irteTable[21] =
      makeIrte(bdf, kSvtExact, kSqExact, kIrteDeliverySmi, 4);
  policy.irteTable[22] =
      makeIrte(bdf, kSvtExact, kSqExact, kIrteDeliveryExtInt, 7);
  policy.irteTable[23] =
      makeIrte(bdf, kSvtDisabled, kSqExact, kIrteDeliveryNmi, 0);
  policy.irteTable[24] =
      makeIrte(bdf, kSvtExact, kSqExact, kIrteDeliveryNmi, 0);

  const VerifyResult result = verify(policy);
  int unauthorized = 0;
  bool svtDisabled = false;
  bool authorizedClean = true;
  for (const Violation& violation : result.violations) {
    if (violation.kind ==
        ViolationKind::IrteUnauthorizedHighPriorityTarget) {
      ++unauthorized;
      CHECK(violation.irteIndex >= 20u && violation.irteIndex <= 22u);
    }
    if (violation.kind ==
            ViolationKind::IrteSourceValidationDisabled &&
        violation.irteIndex == 23u) {
      svtDisabled = true;
    }
    if (violation.irteIndex == 24u) {
      authorizedClean = false;
    }
  }
  CHECK_EQ(unauthorized, 3);
  CHECK(svtDisabled);
  CHECK(authorizedClean);
}

// Test 9: unknown SID and IRTE reserved bits are both reported.
void testIrteUnknownSidAndReserved() {
  PolicyInput policy = basePolicy();
  const u16 bdf = packBdf(0x0D, 0, 0);
  installDevice(policy, bdf,
                encodePasidEntry(baseNestedPasid(kSlRootVm0)));
  policy.deviceVm[bdf] = kVm0;

  const u16 forgedSid = packBdf(0x0E, 3, 1);
  policy.irteTable[30] =
      makeIrte(forgedSid, kSvtExact, kSqExact, kIrteDeliveryFixed, 0);

  Raw128 reservedRaw =
      makeIrte(bdf, kSvtExact, kSqExact, kIrteDeliveryFixed, 0);
  reservedRaw.lo |= (1ull << 55);
  policy.irteTable[31] = reservedRaw;

  const VerifyResult result = verify(policy);
  bool unknownSid = false;
  bool reserved = false;
  for (const Violation& violation : result.violations) {
    if (violation.kind == ViolationKind::IrteUnknownSourceId &&
        violation.irteIndex == 30u) {
      unknownSid = true;
    }
    if (violation.kind == ViolationKind::IrteReservedBitNonZero &&
        violation.irteIndex == 31u) {
      reserved = true;
    }
  }
  CHECK(unknownSid);
  CHECK(reserved);
}

// Test 10: root reserved bits and a disabled PASID directory are detected.
void testTreeReservedAndNoPasidDir() {
  PolicyInput policy = basePolicy();

  Raw128 badRoot = encodeRootEntry(RootEntry{});
  badRoot.lo |= (1ull << 11);
  policy.rootTable[0x0F] = badRoot;

  RootEntry goodRoot;
  goodRoot.present = true;
  goodRoot.contextTableBase = 0x70000ull;
  policy.rootTable[0x10] = encodeRootEntry(goodRoot);

  ContextEntry noDir;
  noDir.present = true;
  noDir.pasidDirEnable = false;
  noDir.pasidBits = 12;
  const u16 otherBdf = packBdf(0x10, 0, 0);
  const u16 devfn =
      static_cast<u16>((static_cast<u32>(devOf(otherBdf)) << 3) |
                       funcOf(otherBdf));
  policy.contextTables[0x70000ull][devfn] = encodeContextEntry(noDir);
  policy.deviceVm[otherBdf] = kVm0;

  const VerifyResult result = verify(policy);
  bool rootReserved = false;
  bool dirDisabled = false;
  for (const Violation& violation : result.violations) {
    if (violation.kind == ViolationKind::ReservedBitNonZero) {
      rootReserved = true;
    }
    if (violation.kind == ViolationKind::PasidDirectoryDisabled) {
      dirDisabled = true;
      CHECK_EQ(violation.bdf, otherBdf);
    }
  }
  CHECK(rootReserved);
  CHECK(dirDisabled);
}

// Test 11: reserved PGTT encodings and missing second-level nodes are caught.
void testReservedPgttAndMissingNode() {
  PolicyInput policy = basePolicy();
  const u16 bdfA = packBdf(0x11, 0, 0);
  PasidEntry reserved = baseNestedPasid(kSlRootVm0);
  reserved.pgtt = kPgttReserved2;
  installDevice(policy, bdfA, encodePasidEntry(reserved));
  policy.deviceVm[bdfA] = kVm0;

  const u16 bdfB = packBdf(0x12, 0, 0);
  installDevice(policy, bdfB,
                encodePasidEntry(baseNestedPasid(kSlRootVm0)));
  policy.deviceVm[bdfB] = kVm0;
  policy.slPool[kSlRootVm0][0] = linkPte(0x60000ull);  // node never inserted

  const VerifyResult result = verify(policy);
  bool badPgtt = false;
  bool missingNode = false;
  for (const Violation& violation : result.violations) {
    if (violation.kind == ViolationKind::InvalidPgtt &&
        violation.bdf == bdfA) {
      badPgtt = true;
    }
    if (violation.kind == ViolationKind::MissingSlTable &&
        violation.bdf == bdfB) {
      missingNode = true;
    }
  }
  CHECK(badPgtt);
  CHECK(missingNode);
}

int main() {
  testGoldenClean();
  testPassthroughEscape();
  testOneGbPartialOverlap();
  testTwoMbPartialOverlap();
  testCrossVmRoot();
  testFiveLevelWalk();
  testLooseSourceQualifier();
  testForgedHighPriorityInterrupts();
  testIrteUnknownSidAndReserved();
  testTreeReservedAndNoPasidDir();
  testReservedPgttAndMissingNode();
  std::printf("checks=%d failures=%d\n", g_checks, g_failures);
  return g_failures == 0 ? EXIT_SUCCESS : EXIT_FAILURE;
}
