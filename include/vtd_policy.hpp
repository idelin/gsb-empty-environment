#ifndef VTD_POLICY_HPP
#define VTD_POLICY_HPP

// Embeddable, zero-dependency VT-d Scalable Mode + IRTE policy verifier.
//
// The library operates on symbolic, sparse representations of the hardware
// tables. No physical page is ever enumerated: second-level translation is
// validated by walking only the present entries of the sparse page-table tree
// and performing closed-form half-open interval intersection against the
// protected/legal host physical ranges (a 1 GB super-page costs exactly the
// same work as a 4 KB page).

#include <algorithm>
#include <cstdint>
#include <map>
#include <set>
#include <string>
#include <utility>
#include <vector>

namespace vtd {

using u8 = std::uint8_t;
using u16 = std::uint16_t;
using u32 = std::uint32_t;
using u64 = std::uint64_t;

constexpr u32 kInvalidPasid = 0xFFFFFFFFu;
constexpr u64 kInvalidIova = 0xFFFFFFFFFFFFFFFFull;
constexpr u32 kNoIrteIndex = 0xFFFFFFFFu;

// ---------------------------------------------------------------------------
// Bit helpers
// ---------------------------------------------------------------------------

constexpr bool bitAt(u64 value, unsigned pos) {
  return ((value >> pos) & 1ull) != 0ull;
}

constexpr u64 bitsOf(u64 value, unsigned hi, unsigned lo) {
  return (value >> lo) & ((1ull << (hi - lo + 1u)) - 1ull);
}

constexpr u64 maskLow(unsigned bits) {
  return bits == 0u ? 0ull
                    : (bits >= 64u ? ~0ull : ((1ull << bits) - 1ull));
}

constexpr bool isAligned(u64 value, u64 alignment) {
  return alignment != 0u && (value % alignment) == 0u;
}

// ---------------------------------------------------------------------------
// PCI Bus/Device/Function
// ---------------------------------------------------------------------------

constexpr u16 packBdf(u8 bus, u8 dev, u8 func) {
  const u32 packed = (static_cast<u32>(bus) << 8) |
                     ((static_cast<u32>(dev) & 0x1Fu) << 3) |
                     (static_cast<u32>(func) & 0x7u);
  return static_cast<u16>(packed);
}

constexpr u8 busOf(u16 bdf) {
  return static_cast<u8>((static_cast<u32>(bdf) >> 8) & 0xFFu);
}

constexpr u8 devOf(u16 bdf) {
  return static_cast<u8>((static_cast<u32>(bdf) >> 3) & 0x1Fu);
}

constexpr u8 funcOf(u16 bdf) {
  return static_cast<u8>(static_cast<u32>(bdf) & 0x7u);
}

// ---------------------------------------------------------------------------
// Half-open physical intervals stored as [base, last] inclusive pairs
// ---------------------------------------------------------------------------

struct Region {
  u64 base = 0;
  u64 last = 0;

  constexpr bool intersects(const Region& other) const {
    return base <= other.last && other.base <= last;
  }

  constexpr bool contains(u64 address) const {
    return address >= base && address <= last;
  }
};

// A merged, sorted set of disjoint physical regions.
class RegionSet {
 public:
  RegionSet() = default;

  explicit RegionSet(std::vector<Region> regions)
      : regions_(std::move(regions)) {
    normalize();
  }

  void add(u64 base, u64 last) {
    regions_.push_back(Region{base, last});
    normalize();
  }

  bool empty() const { return regions_.empty(); }
  const std::vector<Region>& regions() const { return regions_; }

  bool intersects(const Region& range) const {
    for (const Region& region : regions_) {
      if (region.intersects(range)) {
        return true;
      }
    }
    return false;
  }

  bool contains(u64 address) const {
    for (const Region& region : regions_) {
      if (region.contains(address)) {
        return true;
      }
      if (region.base > address) {
        break;
      }
    }
    return false;
  }

  // Offset (relative to range.base) of the first byte of range that is not
  // covered by this set; returns the full range length when fully covered.
  // Interval based - no page enumeration.
  u64 firstUncoveredOffset(const Region& range) const {
    u64 cursor = range.base;
    for (const Region& region : regions_) {
      if (region.base > range.last) {
        break;
      }
      if (region.base > cursor) {
        return cursor - range.base;
      }
      if (region.last < cursor) {
        continue;
      }
      if (region.last == range.last) {
        cursor = range.last + 1u;
        break;
      }
      cursor = region.last + 1u;
    }
    if (cursor <= range.last) {
      return cursor - range.base;
    }
    return range.last - range.base + 1u;
  }

 private:
  void normalize() {
    if (regions_.empty()) {
      return;
    }
    std::sort(regions_.begin(), regions_.end(),
              [](const Region& lhs, const Region& rhs) {
                return lhs.base < rhs.base;
              });
    std::vector<Region> merged;
    merged.push_back(regions_.front());
    for (std::size_t i = 1; i < regions_.size(); ++i) {
      Region& tail = merged.back();
      const Region& current = regions_[i];
      if (current.base <= tail.last) {
        tail.last = std::max(tail.last, current.last);
      } else {
        merged.push_back(current);
      }
    }
    regions_ = std::move(merged);
  }

  std::vector<Region> regions_;
};

// ---------------------------------------------------------------------------
// Wire-format 128-bit table entries
//
// Field semantics follow the VT-d Scalable Mode tables; concrete bit
// positions are centralized below with encode/decode and derived reserved
// masks so the verifier is self-contained and testable without hardware.
// ---------------------------------------------------------------------------

struct Raw128 {
  u64 lo = 0;
  u64 hi = 0;
};

struct RootEntry {
  bool present = false;
  u64 contextTableBase = 0;
  bool reservedNonZero = false;
  Raw128 raw{};
};

// Root entry: lo P(0) + CT base low bits 63:12; hi CT base high bits 11:0.
constexpr u64 kRootLoValidMask = 1ull | 0xFFFFFFFFFFFFF000ull;
constexpr u64 kRootHiValidMask = 0x0000000000000FFFull;

inline RootEntry decodeRootEntry(const Raw128& raw) {
  RootEntry entry;
  entry.raw = raw;
  entry.present = bitAt(raw.lo, 0);
  entry.contextTableBase =
      (raw.lo & 0xFFFFFFFFFFFFF000ull) | ((raw.hi & 0xFFFull) << 52);
  entry.reservedNonZero =
      (raw.lo & ~kRootLoValidMask) != 0u ||
      (raw.hi & ~kRootHiValidMask) != 0u;
  return entry;
}

inline Raw128 encodeRootEntry(const RootEntry& entry) {
  Raw128 raw{};
  raw.lo = (entry.present ? 1ull : 0ull) |
           (entry.contextTableBase & 0xFFFFFFFFFFFFF000ull);
  raw.hi = (entry.contextTableBase >> 52) & 0xFFFull;
  return raw;
}

struct ContextEntry {
  bool present = false;
  bool pasidDirEnable = false;     // PDE
  u8 directoryTranslationMode = 0;  // DTM bits 3:2
  u8 pasidBits = 0;                 // PSIZE bits 10:8 (1..20)
  u16 domainId = 0;                 // hi bits 27:12
  u64 pasidDirBase = 0;
  bool reservedNonZero = false;
  Raw128 raw{};
};

inline ContextEntry decodeContextEntry(const Raw128& raw) {
  ContextEntry entry;
  entry.raw = raw;
  entry.present = bitAt(raw.lo, 0);
  entry.directoryTranslationMode =
      static_cast<u8>(bitsOf(raw.lo, 3, 2));
  entry.pasidDirEnable = bitAt(raw.lo, 6);
  entry.pasidBits = static_cast<u8>(bitsOf(raw.lo, 10, 8));
  entry.pasidDirBase =
      (raw.lo & 0xFFFFFFFFFFFFF000ull) | ((raw.hi & 0xFFFull) << 52);
  entry.domainId = static_cast<u16>(bitsOf(raw.hi, 27, 12));
  constexpr u64 loValid =
      1ull | (0x3ull << 2) | (1ull << 6) | (0x7ull << 8) |
      0xFFFFFFFFFFFFF000ull;
  constexpr u64 hiValid = 0xFFFull | (0xFFFFull << 12);
  entry.reservedNonZero =
      (raw.lo & ~loValid) != 0u || (raw.hi & ~hiValid) != 0u;
  return entry;
}

inline Raw128 encodeContextEntry(const ContextEntry& entry) {
  Raw128 raw{};
  raw.lo = (entry.present ? 1ull : 0ull) |
           (static_cast<u64>(entry.directoryTranslationMode & 0x3u) << 2) |
           (entry.pasidDirEnable ? (1ull << 6) : 0ull) |
           (static_cast<u64>(entry.pasidBits & 0x7u) << 8) |
           (entry.pasidDirBase & 0xFFFFFFFFFFFFF000ull);
  raw.hi = ((entry.pasidDirBase >> 52) & 0xFFFull) |
           (static_cast<u64>(entry.domainId) << 12);
  return raw;
}

struct PasidDirEntry {
  bool present = false;
  u64 pasidTableBase = 0;
  bool reservedNonZero = false;
  Raw128 raw{};
};

inline PasidDirEntry decodePasidDirEntry(const Raw128& raw) {
  PasidDirEntry entry;
  entry.raw = raw;
  entry.present = bitAt(raw.lo, 0);
  entry.pasidTableBase =
      (raw.lo & 0xFFFFFFFFFFFFF000ull) | ((raw.hi & 0xFFFull) << 52);
  entry.reservedNonZero =
      (raw.lo & ~kRootLoValidMask) != 0u ||
      (raw.hi & ~kRootHiValidMask) != 0u;
  return entry;
}

inline Raw128 encodePasidDirEntry(const PasidDirEntry& entry) {
  Raw128 raw{};
  raw.lo = (entry.present ? 1ull : 0ull) |
           (entry.pasidTableBase & 0xFFFFFFFFFFFFF000ull);
  raw.hi = (entry.pasidTableBase >> 52) & 0xFFFull;
  return raw;
}

// PGTT encodings (VT-d PASID table entry).
constexpr u8 kPgttUnsupported0 = 0;
constexpr u8 kPgttFirstStage = 1;
constexpr u8 kPgttReserved2 = 2;
constexpr u8 kPgttNested = 3;
constexpr u8 kPgttPassthrough = 4;

struct PasidEntry {
  bool present = false;
  bool hostControl = false;  // HCTRL
  u8 pgtt = 0;               // hi bits 3:1
  bool pageWriteThrough = false;
  bool pageCacheDisable = false;
  u16 domainId = 0;  // hi bits 21:6
  u64 firstLevelRoot = 0;   // lo bits 63:12
  u64 secondLevelRoot = 0;  // hi bits 63:22
  bool reservedNonZero = false;
  Raw128 raw{};
};

// Modeled layout (symbolic, preserves 4 KB-aligned 52-bit root pointers):
//   lo: P(0), HCTRL(4), PGTT(3:1), PWT(5), PCD(6), FL_PGLTPTR(63:12)
//   hi: DID(11:0), SL_PGDIRPTR(63:12)
inline PasidEntry decodePasidEntry(const Raw128& raw) {
  PasidEntry entry;
  entry.raw = raw;
  entry.present = bitAt(raw.lo, 0);
  entry.pgtt = static_cast<u8>(bitsOf(raw.lo, 3, 1));
  entry.hostControl = bitAt(raw.lo, 4);
  entry.pageWriteThrough = bitAt(raw.lo, 5);
  entry.pageCacheDisable = bitAt(raw.lo, 6);
  entry.firstLevelRoot = raw.lo & 0xFFFFFFFFFFFFF000ull;
  entry.domainId = static_cast<u16>(bitsOf(raw.hi, 11, 0));
  entry.secondLevelRoot = raw.hi & 0xFFFFFFFFFFFFF000ull;
  constexpr u64 loValid = 1ull | (0x7ull << 1) | (1ull << 4) |
                          (1ull << 5) | (1ull << 6) |
                          0xFFFFFFFFFFFFF000ull;
  constexpr u64 hiValid = 0xFFFull | 0xFFFFFFFFFFFFF000ull;
  entry.reservedNonZero =
      (raw.lo & ~loValid) != 0u || (raw.hi & ~hiValid) != 0u;
  return entry;
}

inline Raw128 encodePasidEntry(const PasidEntry& entry) {
  Raw128 raw{};
  raw.lo = (entry.present ? 1ull : 0ull) |
           (static_cast<u64>(entry.pgtt & 0x7u) << 1) |
           (entry.hostControl ? (1ull << 4) : 0ull) |
           (entry.pageWriteThrough ? (1ull << 5) : 0ull) |
           (entry.pageCacheDisable ? (1ull << 6) : 0ull) |
           (entry.firstLevelRoot & 0xFFFFFFFFFFFFF000ull);
  raw.hi = (static_cast<u64>(entry.domainId) & 0xFFFull) |
           (entry.secondLevelRoot & 0xFFFFFFFFFFFFF000ull);
  return raw;
}

// ---------------------------------------------------------------------------
// Sparse hardware tables (absent key == not-present sparse entry)
// ---------------------------------------------------------------------------

using RootTable = std::map<u8, Raw128>;  // bus -> root entry
using ContextTablePool =
    std::map<u64, std::map<u16, Raw128>>;  // CT base -> devfn -> entry
using PasidDirPool =
    std::map<u64, std::map<u32, Raw128>>;  // dir base -> dir index -> entry
using PasidTablePool =
    std::map<u64, std::map<u32, Raw128>>;  // table base -> PASID -> entry

// ---------------------------------------------------------------------------
// Second-level page table (64-bit entries, symbolic sparse nodes)
// ---------------------------------------------------------------------------

constexpr u64 kSlPteRead = 1ull << 0;
constexpr u64 kSlPteWrite = 1ull << 1;
constexpr u64 kSlPteSuper = 1ull << 7;
constexpr u64 kSlPteExecute = 1ull << 63;

struct SlPte {
  bool present = false;
  bool read = false;
  bool write = false;
  bool execute = false;
  bool superpage = false;
  u64 address = 0;
  bool reservedNonZero = false;
  u64 raw = 0;
};

// Host physical address width modeled by the verifier.
constexpr unsigned kDefaultPhysAddrBits = 52;

inline SlPte decodeSlPte(u64 raw, unsigned physAddrBits =
                                      kDefaultPhysAddrBits) {
  SlPte pte;
  pte.raw = raw;
  pte.present = bitAt(raw, 0);
  pte.read = bitAt(raw, 0);
  pte.write = bitAt(raw, 1);
  pte.superpage = bitAt(raw, 7);
  pte.execute = bitAt(raw, 63);
  pte.address = raw & (maskLow(physAddrBits) & ~0xFFFull);
  constexpr u64 fixedValid =
      kSlPteRead | kSlPteWrite | (0x7ull << 2) | (1ull << 7) |
      (1ull << 8) | (0x1Full << 9) | kSlPteExecute;
  const u64 addrMask = maskLow(physAddrBits) & ~0xFFFull;
  pte.reservedNonZero = (raw & ~(fixedValid | addrMask)) != 0u;
  return pte;
}

inline u64 encodeSlPte(const SlPte& pte) {
  u64 raw = pte.address & ~0xFFFull;
  raw |= pte.read ? kSlPteRead : 0ull;
  raw |= pte.write ? kSlPteWrite : 0ull;
  raw |= pte.superpage ? kSlPteSuper : 0ull;
  raw |= pte.execute ? kSlPteExecute : 0ull;
  return raw;
}

using SlTable = std::map<u16, u64>;  // slot 0..511 -> raw PTE
using SlPool = std::map<u64, SlTable>;

// ---------------------------------------------------------------------------
// Interrupt Remapping Table Entry (128-bit, xAPIC/legacy format model)
// ---------------------------------------------------------------------------

constexpr u8 kIrteDeliveryFixed = 0;
constexpr u8 kIrteDeliverySmi = 2;
constexpr u8 kIrteDeliveryNmi = 4;
constexpr u8 kIrteDeliveryInit = 5;
constexpr u8 kIrteDeliveryExtInt = 7;

constexpr u8 kSvtDisabled = 0;
constexpr u8 kSvtExact = 1;

// Source Qualifier: 0 == exact match, higher values allow 2^SQ devices.
constexpr u8 kSqExact = 0;

struct Irte {
  bool present = false;
  u16 sourceId = 0;  // SID (full BDF)
  u8 sourceQualifier = 0;
  u8 sourceValidationType = 0;
  u8 vector = 0;
  u8 deliveryMode = 0;
  u8 triggerMode = 0;
  u8 polarity = 0;
  u32 destinationLapicId = 0;
  bool reservedNonZero = false;
  Raw128 raw{};
};

// lo layout: P(0), VECTOR(31:16), SID(47:32), SVT(49:48), SQ(52:50),
//            POL(59), TR(60), DLM(63:61)
// hi layout: DST(31:0)
inline Irte decodeIrte(const Raw128& raw) {
  Irte irte;
  irte.raw = raw;
  irte.present = bitAt(raw.lo, 0);
  irte.vector = static_cast<u8>(bitsOf(raw.lo, 31, 16));
  irte.sourceId = static_cast<u16>(bitsOf(raw.lo, 47, 32));
  irte.sourceValidationType =
      static_cast<u8>(bitsOf(raw.lo, 49, 48));
  irte.sourceQualifier = static_cast<u8>(bitsOf(raw.lo, 52, 50));
  irte.polarity = static_cast<u8>(bitsOf(raw.lo, 59, 59));
  irte.triggerMode = static_cast<u8>(bitsOf(raw.lo, 60, 60));
  irte.deliveryMode = static_cast<u8>(bitsOf(raw.lo, 63, 61));
  irte.destinationLapicId = static_cast<u32>(bitsOf(raw.hi, 31, 0));
  constexpr u64 loValid = 1ull | (0xFFFFull << 16) | (0xFFFFull << 32) |
                          (0x3ull << 48) | (0x7ull << 50) | (1ull << 59) |
                          (1ull << 60) | (0x7ull << 61);
  constexpr u64 hiValid = 0xFFFFFFFFull;
  irte.reservedNonZero =
      (raw.lo & ~loValid) != 0u || (raw.hi & ~hiValid) != 0u;
  return irte;
}

inline Raw128 encodeIrte(const Irte& irte) {
  Raw128 raw{};
  raw.lo = (irte.present ? 1ull : 0ull) |
           (static_cast<u64>(irte.vector) << 16) |
           (static_cast<u64>(irte.sourceId) << 32) |
           (static_cast<u64>(irte.sourceValidationType & 0x3u) << 48) |
           (static_cast<u64>(irte.sourceQualifier & 0x7u) << 50) |
           (static_cast<u64>(irte.polarity & 0x1u) << 59) |
           (static_cast<u64>(irte.triggerMode & 0x1u) << 60) |
           (static_cast<u64>(irte.deliveryMode & 0x7u) << 61);
  raw.hi = static_cast<u64>(irte.destinationLapicId) & 0xFFFFFFFFull;
  return raw;
}

using IrteArray = std::map<u32, Raw128>;  // IRTE index -> entry

// ---------------------------------------------------------------------------
// Violations
// ---------------------------------------------------------------------------

enum class ViolationKind {
  ReservedBitNonZero,
  MissingReferencedTable,
  PassthroughPgtt,
  InvalidPgtt,
  CrossVmSlRoot,
  DeviceNotAssigned,
  PasidDirectoryDisabled,
  MissingSlTable,
  SlReservedBitNonZero,
  SlMisalignedRoot,
  SlProtectedRegionOverlap,
  SlIllegalPhysicalRange,
  IrteReservedBitNonZero,
  IrteSourceValidationDisabled,
  IrteLooseSourceQualifier,
  IrteUnknownSourceId,
  IrteUnauthorizedHighPriorityTarget,
};

const char* violationName(ViolationKind kind) {
  switch (kind) {
    case ViolationKind::ReservedBitNonZero:
      return "ReservedBitNonZero";
    case ViolationKind::MissingReferencedTable:
      return "MissingReferencedTable";
    case ViolationKind::PassthroughPgtt:
      return "PassthroughPgtt";
    case ViolationKind::InvalidPgtt:
      return "InvalidPgtt";
    case ViolationKind::CrossVmSlRoot:
      return "CrossVmSlRoot";
    case ViolationKind::DeviceNotAssigned:
      return "DeviceNotAssigned";
    case ViolationKind::PasidDirectoryDisabled:
      return "PasidDirectoryDisabled";
    case ViolationKind::MissingSlTable:
      return "MissingSlTable";
    case ViolationKind::SlReservedBitNonZero:
      return "SlReservedBitNonZero";
    case ViolationKind::SlMisalignedRoot:
      return "SlMisalignedRoot";
    case ViolationKind::SlProtectedRegionOverlap:
      return "SlProtectedRegionOverlap";
    case ViolationKind::SlIllegalPhysicalRange:
      return "SlIllegalPhysicalRange";
    case ViolationKind::IrteReservedBitNonZero:
      return "IrteReservedBitNonZero";
    case ViolationKind::IrteSourceValidationDisabled:
      return "IrteSourceValidationDisabled";
    case ViolationKind::IrteLooseSourceQualifier:
      return "IrteLooseSourceQualifier";
    case ViolationKind::IrteUnknownSourceId:
      return "IrteUnknownSourceId";
    case ViolationKind::IrteUnauthorizedHighPriorityTarget:
      return "IrteUnauthorizedHighPriorityTarget";
  }
  return "Unknown";
}

struct Violation {
  ViolationKind kind;
  u16 bdf = 0;
  u32 pasid = kInvalidPasid;
  u64 iova = kInvalidIova;
  u32 irteIndex = kNoIrteIndex;
  u64 detail = 0;
  std::string message;
};

// ---------------------------------------------------------------------------
// Policy input
// ---------------------------------------------------------------------------

struct VmConfig {
  u16 vmId = 0;
  // Second-level page-table roots this VM is allowed to own.
  std::set<u64> allowedSlRoots;
};

struct PolicyInput {
  RootTable rootTable;
  ContextTablePool contextTables;
  PasidDirPool pasidDirectories;
  PasidTablePool pasidTables;
  SlPool slPool;
  IrteArray irteTable;

  // Device ownership: packed BDF -> VM id.
  std::map<u16, u16> deviceVm;
  // VM id -> VM configuration.
  std::map<u16, VmConfig> vms;

  RegionSet legalPhysical;
  RegionSet protectedPhysical;

  // LAPIC IDs allowed to receive NMI/SMI/ExtINT deliveries.
  std::set<u32> authorizedHighPriorityLapics;

  unsigned physAddrBits = kDefaultPhysAddrBits;
  bool fiveLevelSecondLevel = false;
};

struct VerifyStats {
  std::size_t devicesAudited = 0;
  std::size_t pasidEntriesAudited = 0;
  std::size_t slMappingsAudited = 0;
  std::size_t irteAudited = 0;
};

struct VerifyResult {
  std::vector<Violation> violations;
  VerifyStats stats;

  bool ok() const { return violations.empty(); }
};

// ---------------------------------------------------------------------------
// Verifier implementation
// ---------------------------------------------------------------------------

namespace detail {

class Verifier {
 public:
  explicit Verifier(const PolicyInput& input) : in_(input) {}

  VerifyResult run() {
    VerifyResult result;
    walkDeviceTree();
    auditIrteTable();
    result.violations = std::move(violations_);
    std::sort(result.violations.begin(), result.violations.end(),
              [](const Violation& lhs, const Violation& rhs) {
                if (lhs.bdf != rhs.bdf) return lhs.bdf < rhs.bdf;
                if (lhs.pasid != rhs.pasid)
                  return lhs.pasid < rhs.pasid;
                if (lhs.iova != rhs.iova) return lhs.iova < rhs.iova;
                if (lhs.irteIndex != rhs.irteIndex)
                  return lhs.irteIndex < rhs.irteIndex;
                if (lhs.kind != rhs.kind)
                  return static_cast<int>(lhs.kind) <
                         static_cast<int>(rhs.kind);
                return lhs.detail < rhs.detail;
              });
    result.stats = stats_;
    return result;
  }

 private:
  const PolicyInput& in_;
  VerifyStats stats_{};
  std::vector<Violation> violations_;
  std::set<std::tuple<int, u16, u32, u64, u32, u64>> seen_;
  std::set<u16> enabledDevices_;

  void emit(ViolationKind kind, u16 bdf, u32 pasid, u64 iova,
            u32 irteIndex, u64 detail, std::string message) {
    const auto key = std::make_tuple(static_cast<int>(kind), bdf, pasid,
                                     iova, irteIndex, detail);
    if (seen_.count(key) != 0u) {
      return;
    }
    seen_.insert(key);
    Violation violation;
    violation.kind = kind;
    violation.bdf = bdf;
    violation.pasid = pasid;
    violation.iova = iova;
    violation.irteIndex = irteIndex;
    violation.detail = detail;
    violation.message = std::move(message);
    violations_.push_back(std::move(violation));
  }

  void walkDeviceTree() {
    for (const auto& busEntry : in_.rootTable) {
      const u8 bus = busEntry.first;
      const RootEntry root = decodeRootEntry(busEntry.second);
      if (root.reservedNonZero) {
        emit(ViolationKind::ReservedBitNonZero, packBdf(bus, 0, 0),
             kInvalidPasid, kInvalidIova, kNoIrteIndex, 0u,
             "root entry reserved bits are non-zero");
      }
      if (!root.present) {
        continue;
      }
      const auto contextIt = in_.contextTables.find(root.contextTableBase);
      if (contextIt == in_.contextTables.end()) {
        emit(ViolationKind::MissingReferencedTable, packBdf(bus, 0, 0),
             kInvalidPasid, kInvalidIova, kNoIrteIndex,
             root.contextTableBase,
             "present root entry references a missing context table");
        continue;
      }
      for (const auto& devfnEntry : contextIt->second) {
        const u16 devfn = devfnEntry.first;
        const u8 dev = static_cast<u8>((devfn >> 3) & 0x1Fu);
        const u8 func = static_cast<u8>(devfn & 0x7u);
        const u16 bdf = packBdf(bus, dev, func);
        const ContextEntry context = decodeContextEntry(devfnEntry.second);
        if (context.reservedNonZero) {
          emit(ViolationKind::ReservedBitNonZero, bdf, kInvalidPasid,
               kInvalidIova, kNoIrteIndex, 0u,
               "context entry reserved bits are non-zero");
        }
        if (!context.present) {
          continue;
        }
        enabledDevices_.insert(bdf);
        ++stats_.devicesAudited;
        if (in_.deviceVm.count(bdf) == 0u) {
          emit(ViolationKind::DeviceNotAssigned, bdf, kInvalidPasid,
               kInvalidIova, kNoIrteIndex, 0u,
               "enabled device is not assigned to any VM");
        }
        if (!context.pasidDirEnable) {
          emit(ViolationKind::PasidDirectoryDisabled, bdf,
               kInvalidPasid, kInvalidIova, kNoIrteIndex, 0u,
               "scalable-mode context entry has PASID directory disabled");
          continue;
        }
        if (context.pasidBits == 0u || context.pasidBits > 20u) {
          emit(ViolationKind::ReservedBitNonZero, bdf, kInvalidPasid,
               kInvalidIova, kNoIrteIndex, context.pasidBits,
               "context entry PASID size is outside 1..20");
        }
        walkPasidHierarchy(bdf, context);
      }
    }
  }

  void walkPasidHierarchy(u16 bdf, const ContextEntry& context) {
    const auto dirIt = in_.pasidDirectories.find(context.pasidDirBase);
    if (dirIt == in_.pasidDirectories.end()) {
      emit(ViolationKind::MissingReferencedTable, bdf, kInvalidPasid,
           kInvalidIova, kNoIrteIndex, context.pasidDirBase,
           "context entry references a missing PASID directory");
      return;
    }
    for (const auto& dirEntry : dirIt->second) {
      const u32 dirIndex = dirEntry.first;
      const PasidDirEntry pasidDir = decodePasidDirEntry(dirEntry.second);
      if (pasidDir.reservedNonZero) {
        emit(ViolationKind::ReservedBitNonZero, bdf, kInvalidPasid,
             kInvalidIova, kNoIrteIndex, dirIndex,
             "PASID directory entry reserved bits are non-zero");
      }
      if (!pasidDir.present) {
        continue;
      }
      const auto tableIt = in_.pasidTables.find(pasidDir.pasidTableBase);
      if (tableIt == in_.pasidTables.end()) {
        emit(ViolationKind::MissingReferencedTable, bdf, kInvalidPasid,
             kInvalidIova, kNoIrteIndex, pasidDir.pasidTableBase,
             "PASID directory entry references a missing PASID table");
        continue;
      }
      for (const auto& pasidEntry : tableIt->second) {
        const u32 tableIndex = pasidEntry.first;
        const u32 pasid = (dirIndex << 10) | (tableIndex & 0x3FFu);
        auditPasidEntry(bdf, pasid,
                        decodePasidEntry(pasidEntry.second));
      }
    }
  }

  void auditPasidEntry(u16 bdf, u32 pasid, const PasidEntry& entry) {
    if (entry.reservedNonZero) {
      emit(ViolationKind::ReservedBitNonZero, bdf, pasid, kInvalidIova,
           kNoIrteIndex, 0u,
           "PASID table entry reserved bits are non-zero");
    }
    if (!entry.present) {
      return;
    }
    ++stats_.pasidEntriesAudited;

    if (entry.pgtt == kPgttPassthrough) {
      emit(ViolationKind::PassthroughPgtt, bdf, pasid, kInvalidIova,
           kNoIrteIndex, entry.pgtt,
           "PASID entry selects Pass-Through PGTT=0b100");
      return;
    }
    if (entry.pgtt == kPgttUnsupported0 ||
        entry.pgtt == kPgttReserved2 || entry.pgtt > kPgttPassthrough) {
      emit(ViolationKind::InvalidPgtt, bdf, pasid, kInvalidIova,
           kNoIrteIndex, entry.pgtt,
           "PASID entry uses a reserved/unsupported PGTT encoding");
      return;
    }
    if (entry.pgtt != kPgttNested) {
      return;  // First-stage only: no second-level policy surface.
    }

    const u64 slRoot = entry.secondLevelRoot;
    if (!isAligned(slRoot, 4096u)) {
      emit(ViolationKind::SlMisalignedRoot, bdf, pasid, kInvalidIova,
           kNoIrteIndex, slRoot,
           "second-level root is not 4 KB aligned");
      return;
    }
    const auto ownerIt = in_.deviceVm.find(bdf);
    if (ownerIt == in_.deviceVm.end()) {
      return;  // DeviceNotAssigned already reported.
    }
    const auto vmIt = in_.vms.find(ownerIt->second);
    if (vmIt == in_.vms.end() ||
        vmIt->second.allowedSlRoots.count(slRoot) == 0u) {
      emit(ViolationKind::CrossVmSlRoot, bdf, pasid, kInvalidIova,
           kNoIrteIndex, slRoot,
           "PASID entry points at a second-level root owned by another VM");
    }
    auditSecondLevel(slRoot, bdf, pasid);
  }

  // Symbolic second-level walk: visits present sparse entries only and
  // reasons about whole intervals, never individual 4 KB pages.
  void auditSecondLevel(u64 root, u16 bdf, u32 pasid) {
    const unsigned rootShift = in_.fiveLevelSecondLevel ? 48u : 39u;
    walkSlNode(root, rootShift, 0ull, bdf, pasid);
  }

  void walkSlNode(u64 tableBase, unsigned shift, u64 iovaPrefix, u16 bdf,
                  u32 pasid) {
    const auto tableIt = in_.slPool.find(tableBase);
    if (tableIt == in_.slPool.end()) {
      emit(ViolationKind::MissingSlTable, bdf, pasid, iovaPrefix,
           kNoIrteIndex, tableBase,
           "second-level PTE references a missing page-table node");
      return;
    }
    for (const auto& slotEntry : tableIt->second) {
      const u16 slot = slotEntry.first;
      if (slot > 511u) {
        emit(ViolationKind::ReservedBitNonZero, bdf, pasid, iovaPrefix,
             kNoIrteIndex, slot, "second-level slot index exceeds 511");
        continue;
      }
      const SlPte pte = decodeSlPte(slotEntry.second, in_.physAddrBits);
      const u64 iova = iovaPrefix | (static_cast<u64>(slot) << shift);
      if (pte.reservedNonZero) {
        emit(ViolationKind::SlReservedBitNonZero, bdf, pasid, iova,
             kNoIrteIndex, pte.raw,
             "second-level PTE reserved bits are non-zero");
      }
      if (!pte.present) {
        continue;
      }
      if (shift == 12u || pte.superpage) {
        const u64 size = 1ull << shift;
        if (pte.superpage && shift > 12u &&
            !isAligned(pte.address, size)) {
          emit(ViolationKind::SlReservedBitNonZero, bdf, pasid, iova,
               kNoIrteIndex, pte.address,
               "super-page physical address is not size aligned");
        }
        auditSlMapping(pte, iova, shift, bdf, pasid);
      } else {
        walkSlNode(pte.address, shift - 9u, iova, bdf, pasid);
      }
    }
  }

  void auditSlMapping(const SlPte& pte, u64 iova, unsigned shift, u16 bdf,
                      u32 pasid) {
    ++stats_.slMappingsAudited;
    const u64 size = 1ull << shift;
    const Region phys{pte.address, pte.address + size - 1u};

    // (a) Overlap with protected host physical regions.
    u64 protectedOffset = size;
    for (const Region& region : in_.protectedPhysical.regions()) {
      if (!region.intersects(phys)) {
        continue;
      }
      const u64 offset =
          region.base > phys.base ? region.base - phys.base : 0u;
      protectedOffset = std::min(protectedOffset, offset);
    }
    if (protectedOffset < size) {
      emit(ViolationKind::SlProtectedRegionOverlap, bdf, pasid,
           iova + protectedOffset, kNoIrteIndex, pte.raw,
           "DMA mapping overlaps a protected host physical region");
    }

    // (b) Physical range outside the machine's legal memory.
    const u64 illegalOffset =
        in_.legalPhysical.firstUncoveredOffset(phys);
    if (illegalOffset < size) {
      emit(ViolationKind::SlIllegalPhysicalRange, bdf, pasid,
           iova + illegalOffset, kNoIrteIndex,
           pte.address + illegalOffset,
           "DMA mapping reaches physical memory outside legal machine RAM");
    }
  }

  void auditIrteTable() {
    for (const auto& irteEntry : in_.irteTable) {
      const u32 index = irteEntry.first;
      const Irte irte = decodeIrte(irteEntry.second);
      if (!irte.present) {
        continue;
      }
      ++stats_.irteAudited;
      const u16 sourceBdf = irte.sourceId;

      if (irte.reservedNonZero) {
        emit(ViolationKind::IrteReservedBitNonZero, sourceBdf,
             kInvalidPasid, kInvalidIova, index, 0u,
             "IRTE reserved bits are non-zero");
      }

      if (irte.sourceValidationType != kSvtExact) {
        emit(ViolationKind::IrteSourceValidationDisabled, sourceBdf,
             kInvalidPasid, kInvalidIova, index,
             irte.sourceValidationType,
             "IRTE source validation (SVT) is not set to exact match");
      } else if (irte.sourceQualifier != kSqExact) {
        emit(ViolationKind::IrteLooseSourceQualifier, sourceBdf,
             kInvalidPasid, kInvalidIova, index, irte.sourceQualifier,
             "IRTE source qualifier mask is wider than a single device");
      }

      if (enabledDevices_.count(sourceBdf) == 0u ||
          in_.deviceVm.count(sourceBdf) == 0u) {
        emit(ViolationKind::IrteUnknownSourceId, sourceBdf,
             kInvalidPasid, kInvalidIova, index, sourceBdf,
             "IRTE SID does not match an enabled, assigned PCI device");
      }

      const bool highPriorityDelivery =
          irte.deliveryMode == kIrteDeliveryNmi ||
          irte.deliveryMode == kIrteDeliverySmi ||
          irte.deliveryMode == kIrteDeliveryExtInt;
      if (highPriorityDelivery &&
          in_.authorizedHighPriorityLapics.count(
              irte.destinationLapicId) == 0u) {
        emit(ViolationKind::IrteUnauthorizedHighPriorityTarget, sourceBdf,
             kInvalidPasid, kInvalidIova, index,
             irte.destinationLapicId,
             "NMI/SMI/ExtINT targets an unauthorized LAPIC ID");
      }
    }
  }

};

}  // namespace detail

// Verify the complete policy. The result is sorted by (BDF, PASID, IOVA)
// so the first reported DMA violation is the minimal offender.
inline VerifyResult verify(const PolicyInput& input) {
  detail::Verifier verifier(input);
  return verifier.run();
}

}  // namespace vtd

#endif  // VTD_POLICY_HPP
