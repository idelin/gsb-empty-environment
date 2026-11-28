#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <optional>
#include <string>
#include <vector>

namespace iommu_guard {

using PhysicalAddress = std::uint64_t;
using SecurityDomainId = std::uint32_t;
using DomainId = std::uint16_t;

// Half-open physical or guest-physical interval [begin, end).
struct AddressRange {
    std::uint64_t begin = 0;
    std::uint64_t end = 0;

    bool valid() const noexcept { return begin < end; }
    std::uint64_t size() const noexcept { return end - begin; }
};

bool operator<(const AddressRange& lhs, const AddressRange& rhs) noexcept;
std::optional<AddressRange> intersect_ranges(const AddressRange& lhs,
                                              const AddressRange& rhs) noexcept;
std::vector<AddressRange> merge_ranges(std::vector<AddressRange> ranges);

struct PciBdf {
    std::uint16_t segment = 0;
    std::uint8_t bus = 0;
    std::uint8_t device = 0;
    std::uint8_t function = 0;

    bool valid() const noexcept {
        return device <= 31U && function <= 7U;
    }
};

bool operator==(const PciBdf& lhs, const PciBdf& rhs) noexcept;
bool operator!=(const PciBdf& lhs, const PciBdf& rhs) noexcept;
bool operator<(const PciBdf& lhs, const PciBdf& rhs) noexcept;
std::string format_bdf(const PciBdf& bdf);

enum class IssueCode : std::uint16_t {
    kInvalidArgument = 0,
    kDmarHeaderTooShort,
    kDmarBadSignature,
    kDmarBadTableLength,
    kDmarChecksumMismatch,
    kDmarInvalidHaw,
    kDmarTruncatedStructure,
    kDmarInvalidStructureLength,
    kDmarUnknownStructure,
    kDmarDuplicateIncludePciAll,
    kDmarIncludePciAllSegmentConflict,
    kDmarInvalidDeviceScope,
    kDmarInvalidPciPath,
    kDmarMissingDrhd,
    kDmarBadRegisterBase,
    kDmarBadRmrrAlignment,
    kDmarBadRmrrLimit,
    kDmarRmrrLimitOverflow,
    kDmarBadRhsa,
    kPciMultipleOwners,
    kPciNoOwner,
    kPciBridgeMetadataMissing,
    kPciBridgeScopeConflict,
    kPhantomAliasConflict,
    kVtdMemoryReadFailed,
    kVtdRootEntryNotPresent,
    kVtdRootEntryReserved,
    kVtdRootEntryOutOfRange,
    kVtdContextEntryNotPresent,
    kVtdContextEntryReserved,
    kVtdContextEntryOutOfRange,
    kVtdTranslationTypePassThrough,
    kVtdTranslationTypeUnsupported,
    kVtdAddressWidthUnsupported,
    kVtdScalableLayoutInvalid,
    kVtdPasidDirectoryNotPresent,
    kVtdPasidTableEntryNotPresent,
    kVtdPgttPassThrough,
    kVtdPgttUnsupported,
    kVtdPageTableReserved,
    kVtdPagePermissionsInvalid,
    kVtdSuperpageMisaligned,
    kVtdHpaOutOfRange,
    kVtdGpaOutOfRange,
    kVtdMappingLimitExceeded,
    kMappingInProtectedMemory,
    kMappingInUnauthorizedMmio,
    kRmrrOverlapsProtectedMemory,
    kRmrrIdentityMappingMissing,
    kDmaRemappingDisabled,
    kDidSharedAcrossSecurityDomains
};

const char* to_string(IssueCode code) noexcept;

struct Issue {
    IssueCode code = IssueCode::kInvalidArgument;
    std::string message;
    std::uint64_t table_offset = 0;
    std::optional<PciBdf> bdf;
    std::optional<std::uint32_t> pasid;
    std::optional<DomainId> did;
    std::optional<AddressRange> gpa;
    std::optional<AddressRange> hpa;
    // Physical addresses of the root/context/page-walk entries that led to the
    // finding. This lets a caller re-read and independently attest the entry.
    std::vector<PhysicalAddress> entry_phys;
};

enum class DmarScopeKind : std::uint8_t {
    kPciEndpoint = 1,
    kPciSubHierarchy = 2,
    kIoApic = 3,
    kHpet = 4
};

struct DmarPciPath {
    std::uint8_t device = 0;
    std::uint8_t function = 0;
};

struct DmarDeviceScope {
    DmarScopeKind kind = DmarScopeKind::kPciEndpoint;
    std::uint8_t enumeration_id = 0;
    std::uint8_t start_bus = 0;
    std::vector<DmarPciPath> path;
};

struct Drhd {
    std::uint16_t segment = 0;
    bool include_pci_all = false;
    PhysicalAddress register_base = 0;
    std::uint32_t table_offset = 0;
    std::vector<DmarDeviceScope> scopes;
};

struct Rmrr {
    std::uint16_t segment = 0;
    // ACPI RMRR uses an inclusive limit. end() is the safe half-open value.
    std::uint64_t base = 0;
    std::uint64_t limit = 0;
    std::uint32_t table_offset = 0;
    std::vector<DmarDeviceScope> scopes;

    AddressRange range() const noexcept {
        constexpr std::uint64_t kMax = ~std::uint64_t{0};
        return AddressRange{base, limit == kMax ? kMax : limit + 1U};
    }
};

struct Atsr {
    std::uint16_t segment = 0;
    std::uint8_t flags = 0;
    std::uint32_t table_offset = 0;
    std::vector<DmarDeviceScope> scopes;
};

struct Rhsa {
    PhysicalAddress base_address = 0;
    std::uint32_t proximity_domain = 0;
    std::uint32_t table_offset = 0;
};

struct DmarTable {
    std::uint32_t length = 0;
    std::uint8_t revision = 0;
    std::uint8_t checksum = 0;
    std::uint8_t host_address_width = 0;
    std::uint8_t flags = 0;
    std::vector<Drhd> drhds;
    std::vector<Rmrr> rmrrs;
    std::vector<Atsr> atsrs;
    std::vector<Rhsa> rhsas;

    // Highest legal byte address for HAW+1 address bits. A DMAR HAW of 39
    // therefore covers 40-bit physical addresses.
    std::uint64_t physical_address_mask() const noexcept;
};

struct DmarParseResult {
    bool ok = false;
    DmarTable table;
    std::vector<Issue> issues;
};

DmarParseResult parse_dmar(const std::uint8_t* data, std::size_t size);
DmarParseResult parse_dmar(const std::vector<std::uint8_t>& data);

struct PciDeviceInfo {
    PciBdf bdf;
    bool is_bridge = false;
    // Default is an invalid bus range; callers fill this from PCI config space.
    std::uint8_t secondary_bus = 0xFF;
    std::uint8_t subordinate_bus = 0;
    std::optional<PciBdf> upstream_bridge;
    // Zero disables phantom functions. A mask such as 0x7 aliases all eight
    // function numbers of this device into the same requester ID context.
    std::uint8_t phantom_alias_mask = 0;
    SecurityDomainId security_domain = 0;
    bool dma_remapping_enabled = true;
    // PASIDs configured for this device. Empty means RID_PASID/legacy only.
    std::vector<std::uint32_t> pasids;
};

struct PciTopology {
    std::vector<PciDeviceInfo> devices;
};

struct PciOwnership {
    PciBdf bdf;
    std::vector<std::size_t> drhd_indices;
    std::optional<std::size_t> unique_owner;
};

PciOwnership resolve_pci_ownership(const DmarTable& dmar,
                                   const PciTopology& topology,
                                   const PciBdf& bdf);

struct PciTopologyAudit {
    bool ok = false;
    std::vector<PciOwnership> ownership;
    std::vector<Issue> issues;
};

PciTopologyAudit audit_pci_topology(const DmarTable& dmar,
                                    const PciTopology& topology);

namespace vtd {

// Root entry: bit 0 present, bits 63:12 context-table pointer.
inline constexpr std::uint64_t kRootEntryPresent = 1ULL << 0U;

// Legacy context entry: P[0], FPD[1], TT[3:2], SLPTP[63:12],
// AW[66:64], DID[87:72]. Scalable context entry is 32 bytes with
// PDTS[11:9], PASID-directory pointer[63:12], RID_PASID[83:64].
inline constexpr std::uint64_t kContextEntryPresent = 1ULL << 0U;
inline constexpr std::uint64_t kContextEntryFaultDisable = 1ULL << 1U;
inline constexpr std::uint64_t kScalableContextPdsShift = 9U;
inline constexpr std::uint64_t kScalableContextPdsMask = 7ULL << 9U;
inline constexpr std::uint64_t kScalableContextEntrySize = 32U;
inline constexpr std::uint64_t kContextTranslationTypeMask = 3ULL << 2U;
inline constexpr std::uint64_t kContextTranslationTypeShift = 2U;
inline constexpr std::uint8_t kTranslationSecondLevel = 0U;
inline constexpr std::uint8_t kTranslationSecondLevelAll = 1U;
inline constexpr std::uint8_t kTranslationPassThrough = 2U;
inline constexpr std::uint8_t kTranslationReserved = 3U;
inline constexpr std::uint64_t kContextAddressWidthMask = 7ULL;
inline constexpr std::uint64_t kContextDomainIdShift = 8U;
inline constexpr std::uint64_t kContextRidPasidShift = 0U;
inline constexpr std::uint64_t kContextRidPasidMask = 0xFFFFFULL;
inline constexpr std::uint8_t kAddressWidth39 = 1U;
inline constexpr std::uint8_t kAddressWidth48 = 2U;

// Scalable PASID directory entry: P[0], PASID-table pointer[63:12].
inline constexpr std::uint64_t kPasidDirectoryPresent = 1ULL << 0U;
inline constexpr std::uint64_t kPasidDirectoryShift = 6U;
inline constexpr std::uint64_t kPasidDirectoryIndexMask = 0x3FFFULL;

// Scalable PASID table entry: P[0], FPD[1], AW[4:2], PGTT[8:6],
// SSPTP[63:12], DID[79:64].
inline constexpr std::uint64_t kPasidTablePresent = 1ULL << 0U;
inline constexpr std::uint64_t kPasidTableFaultDisable = 1ULL << 1U;
inline constexpr std::uint64_t kPasidAddressWidthShift = 2U;
inline constexpr std::uint64_t kPasidAddressWidthMask = 7ULL << 2U;
inline constexpr std::uint64_t kPasidPgttMask = 7ULL << 6U;
inline constexpr std::uint64_t kPasidPgttShift = 6U;
inline constexpr std::uint64_t kPasidTableEntrySize = 64U;
inline constexpr std::uint64_t kPasidTableIndexMask = 0x3FULL;
inline constexpr std::uint64_t kPasidDomainIdShift = 0U;
inline constexpr std::uint8_t kPgttFirstLevel = 1U;
inline constexpr std::uint8_t kPgttSecondLevel = 2U;
inline constexpr std::uint8_t kPgttNested = 3U;
inline constexpr std::uint8_t kPgttPassThrough = 4U;

// Second-level page-table entry, EPT-like:
// R[0], W[1], X[2], PS[7], A[8], D[9], SNP[11], HPA[HAW:12].
inline constexpr std::uint64_t kSlptRead = 1ULL << 0U;
inline constexpr std::uint64_t kSlptWrite = 1ULL << 1U;
inline constexpr std::uint64_t kSlptExecute = 1ULL << 2U;
inline constexpr std::uint64_t kSlptPageSize = 1ULL << 7U;
inline constexpr std::uint64_t kSlptAccessed = 1ULL << 8U;
inline constexpr std::uint64_t kSlptDirty = 1ULL << 9U;
inline constexpr std::uint64_t kSlptSnoop = 1ULL << 11U;
inline constexpr std::uint64_t kPage4K = 4ULL * 1024ULL;
inline constexpr std::uint64_t kPage2M = 2ULL * 1024ULL * 1024ULL;
inline constexpr std::uint64_t kPage1G = 1ULL * 1024ULL * 1024ULL * 1024ULL;

}  // namespace vtd

struct TranslationRoot {
    PhysicalAddress root_table_phys = 0;
    // DMAR HAW value (address bits are HAW+1 wide).
    std::uint8_t host_address_width = 39;
    bool scalable_mode = false;
};

using MemoryRead = std::function<bool(PhysicalAddress, void*, std::size_t)>;

struct SlptMapping {
    AddressRange gpa;
    PhysicalAddress hpa_begin = 0;
    std::uint64_t page_size = 0;
    bool read = false;
    bool write = false;
    bool execute = false;
    bool snoop = false;
    PhysicalAddress leaf_entry_phys = 0;
    std::vector<PhysicalAddress> walk_entry_phys;
};

struct WalkOptions {
    std::size_t max_mappings = 1000000U;
};

struct TranslationWalk {
    PciBdf bdf;
    std::optional<std::uint32_t> pasid;
    std::optional<PhysicalAddress> root_entry_phys;
    std::optional<PhysicalAddress> context_entry_phys;
    std::optional<PhysicalAddress> pasid_directory_entry_phys;
    std::optional<PhysicalAddress> pasid_table_entry_phys;
    std::optional<DomainId> did;
    std::optional<std::uint8_t> address_width_bits;
    bool translation_active = false;
    bool pass_through = false;
    std::vector<SlptMapping> mappings;
    std::vector<Issue> issues;
};

TranslationWalk walk_device_translation(
    const TranslationRoot& root, const PciBdf& bdf,
    std::optional<std::uint32_t> pasid, const MemoryRead& read,
    const WalkOptions& options = {});

struct ProtectedRange {
    AddressRange range;
    std::string label;
};

struct SensitiveMmioRange {
    AddressRange range;
    std::string label;
    // An empty allow-list means no security domain may DMA to this range.
    std::vector<SecurityDomainId> allowed_security_domains;
};

struct IsolationPolicy {
    std::vector<ProtectedRange> protected_memory;
    std::vector<SensitiveMmioRange> sensitive_mmio;
};

struct DeviceAssignment {
    PciBdf bdf;
    SecurityDomainId security_domain = 0;
    bool dma_remapping_enabled = true;
    // Required for scalable mode. Ignored by legacy mode.
    std::optional<std::uint32_t> pasid;
};

std::vector<DeviceAssignment> assignments_from_topology(
    const PciTopology& topology, bool scalable_mode);

struct DeviceAuditResult {
    DeviceAssignment assignment;
    PciOwnership ownership;
    TranslationWalk walk;
    std::vector<AddressRange> rmrr_regions;
    std::vector<AddressRange> gpa_conflicts;
    std::vector<AddressRange> hpa_conflicts;
};

struct AuditReport {
    bool ok = false;
    std::vector<DeviceAuditResult> devices;
    std::vector<Issue> issues;
};

AuditReport audit_isolation(
    const DmarTable& dmar, const PciTopology& topology,
    const TranslationRoot& translation_root, const MemoryRead& read,
    const IsolationPolicy& policy,
    const std::vector<DeviceAssignment>& assignments,
    const WalkOptions& options = {});

}  // namespace iommu_guard
