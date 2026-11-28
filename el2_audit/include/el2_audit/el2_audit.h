#ifndef EL2_AUDIT_EL2_AUDIT_H
#define EL2_AUDIT_EL2_AUDIT_H

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

// Embeddable, zero-dependency ARM64 EL2 hypervisor configuration and MMIO
// emulation boundary audit library. Constants follow the ARMv8-A Architecture
// Reference Manual (A-profile) system register layout.
namespace el2_audit {

inline constexpr std::uint32_t NO_VM = 0xFFFFFFFFU;

// HCR_EL2 bit positions (security relevant fields).
inline constexpr unsigned int HCR_VM_BIT = 0U;
inline constexpr unsigned int HCR_SWIO_BIT = 1U;
inline constexpr unsigned int HCR_FMO_BIT = 3U;
inline constexpr unsigned int HCR_IMO_BIT = 4U;
inline constexpr unsigned int HCR_AMO_BIT = 5U;
inline constexpr unsigned int HCR_TWI_BIT = 13U;
inline constexpr unsigned int HCR_TWE_BIT = 14U;
inline constexpr unsigned int HCR_TID0_BIT = 15U;
inline constexpr unsigned int HCR_TID1_BIT = 16U;
inline constexpr unsigned int HCR_TID2_BIT = 17U;
inline constexpr unsigned int HCR_TID3_BIT = 18U;
inline constexpr unsigned int HCR_TSC_BIT = 19U;
inline constexpr unsigned int HCR_TIDCP_BIT = 20U;
inline constexpr unsigned int HCR_TACR_BIT = 21U;
inline constexpr unsigned int HCR_TSW_BIT = 22U;
inline constexpr unsigned int HCR_TPCP_BIT = 23U;
inline constexpr unsigned int HCR_TPU_BIT = 24U;
inline constexpr unsigned int HCR_TTLB_BIT = 25U;
inline constexpr unsigned int HCR_TVM_BIT = 26U;
inline constexpr unsigned int HCR_TGE_BIT = 27U;
inline constexpr unsigned int HCR_TDZ_BIT = 28U;
inline constexpr unsigned int HCR_HCD_BIT = 29U;
inline constexpr unsigned int HCR_TRVM_BIT = 30U;

inline constexpr std::uint64_t HCR_VM = (1ULL << HCR_VM_BIT);
inline constexpr std::uint64_t HCR_SWIO = (1ULL << HCR_SWIO_BIT);
inline constexpr std::uint64_t HCR_FMO = (1ULL << HCR_FMO_BIT);
inline constexpr std::uint64_t HCR_IMO = (1ULL << HCR_IMO_BIT);
inline constexpr std::uint64_t HCR_AMO = (1ULL << HCR_AMO_BIT);
inline constexpr std::uint64_t HCR_TWI = (1ULL << HCR_TWI_BIT);
inline constexpr std::uint64_t HCR_TWE = (1ULL << HCR_TWE_BIT);
inline constexpr std::uint64_t HCR_TID0 = (1ULL << HCR_TID0_BIT);
inline constexpr std::uint64_t HCR_TID1 = (1ULL << HCR_TID1_BIT);
inline constexpr std::uint64_t HCR_TID2 = (1ULL << HCR_TID2_BIT);
inline constexpr std::uint64_t HCR_TID3 = (1ULL << HCR_TID3_BIT);
inline constexpr std::uint64_t HCR_TSC = (1ULL << HCR_TSC_BIT);
inline constexpr std::uint64_t HCR_TIDCP = (1ULL << HCR_TIDCP_BIT);
inline constexpr std::uint64_t HCR_TACR = (1ULL << HCR_TACR_BIT);
inline constexpr std::uint64_t HCR_TSW = (1ULL << HCR_TSW_BIT);
inline constexpr std::uint64_t HCR_TPCP = (1ULL << HCR_TPCP_BIT);
inline constexpr std::uint64_t HCR_TPU = (1ULL << HCR_TPU_BIT);
inline constexpr std::uint64_t HCR_TTLB = (1ULL << HCR_TTLB_BIT);
inline constexpr std::uint64_t HCR_TVM = (1ULL << HCR_TVM_BIT);
inline constexpr std::uint64_t HCR_TDZ = (1ULL << HCR_TDZ_BIT);
inline constexpr std::uint64_t HCR_TRVM = (1ULL << HCR_TRVM_BIT);

// Mandatory trap bits under the default hardening policy. The task requires
// VM plus TSC/TWE/TVM/TACR; the remaining bits close the standard guest-trap
// surface (cache/TLB maintenance, implementation-defined registers).
inline constexpr std::uint64_t DEFAULT_REQUIRED_HCR_TRAPS =
    HCR_VM | HCR_TSC | HCR_TWE | HCR_TVM | HCR_TACR | HCR_TWI | HCR_TID0 |
    HCR_TID1 | HCR_TID2 | HCR_TID3 | HCR_TIDCP | HCR_TSW | HCR_TPCP |
    HCR_TPU | HCR_TTLB | HCR_TDZ | HCR_TRVM;

// VTTBR_EL2 / ESR_EL2 layout constants.
inline constexpr std::uint64_t VTTBR_VMID_MASK = 0xFFFF000000000000ULL;
inline constexpr unsigned int VTTBR_VMID_SHIFT = 48U;

inline constexpr std::uint32_t ESR_EC_SHIFT = 26U;
inline constexpr std::uint32_t ESR_EC_MASK = 0x3FU;
inline constexpr std::uint32_t ESR_EC_DATA_ABORT_LOWER_EL = 0x24U;
inline constexpr std::uint32_t ESR_ISS_VALID_BIT = 24U;
inline constexpr std::uint32_t ESR_ISS_MASK = 0x01FFFFFFU;

inline constexpr std::uint32_t ESR_ISS_ISV_BIT = 24U;
inline constexpr std::uint32_t ESR_ISS_SAS_SHIFT = 22U;
inline constexpr std::uint32_t ESR_ISS_SAS_MASK = 0x3U;
inline constexpr std::uint32_t ESR_ISS_SSE_BIT = 21U;
inline constexpr std::uint32_t ESR_ISS_SRT_SHIFT = 16U;
inline constexpr std::uint32_t ESR_ISS_SRT_MASK = 0x1FU;
inline constexpr std::uint32_t ESR_ISS_WNR_BIT = 6U;
inline constexpr std::uint32_t ESR_ISS_DFSC_MASK = 0x3FU;

// Stage-2 translation fault DFSC values produced by an unmapped-IPA MMIO trap.
inline constexpr std::uint32_t DFSC_TRANSLATION_FAULT_LEVEL0 = 0x04U;
inline constexpr std::uint32_t DFSC_TRANSLATION_FAULT_LEVEL1 = 0x05U;
inline constexpr std::uint32_t DFSC_TRANSLATION_FAULT_LEVEL2 = 0x06U;
inline constexpr std::uint32_t DFSC_TRANSLATION_FAULT_LEVEL3 = 0x07U;

enum class Severity {
  Info,
  Warning,
  Error,
};

enum class FindingCode {
  None,
  HcrRequiredTrapMissing,
  VmidZero,
  VmidCollision,
  S2MappingInvalid,
  S2IpaOverlap,
  S2OverlapsEl2Memory,
  S2OverlapsOtherVmMemory,
  MmioWindowMappedInS2,
  MmioWindowsOverlap,
  MmioRegionInvalid,
  MmioRegisterInvalid,
  EsrNotDataAbort,
  EsrSyndromeInvalid,
  AccessVmUnknown,
  AccessOutsideAnyDevice,
  AccessCrossesDeviceWindow,
  AccessUnaligned,
  AccessCrossesRegister,
  AccessReservedWrite,
  AccessReadOnlyWrite,
};

const char* to_string(Severity severity);
const char* to_string(FindingCode code);

// Human-readable name of an HCR_EL2 bit position (e.g. bit 19 -> "TSC").
const char* hcr_bit_name(unsigned int bit);

struct Finding {
  FindingCode code = FindingCode::None;
  Severity severity = Severity::Info;
  std::uint32_t vm_id = NO_VM;
  std::string detail;
  std::uint64_t witness = 0U;

  bool ok() const { return code == FindingCode::None; }
};

struct AuditPolicy {
  // HCR_EL2 bits that must be set for every audited VM.
  std::uint64_t required_hcr_traps = DEFAULT_REQUIRED_HCR_TRAPS;
  // When true, accesses that do not decode to a declared MMIO register are
  // treated as reserved (writes hard-blocked, reads still alignment checked).
  bool treat_undeclared_as_reserved = true;
};

// A physical range that no guest Stage-2 mapping is allowed to map.
struct ProtectedRange {
  std::uint64_t base = 0U;
  std::uint64_t size = 0U;
  std::string name;

  ProtectedRange() = default;
  ProtectedRange(std::uint64_t base_addr, std::uint64_t length,
                 std::string range_name)
      : base(base_addr), size(length), name(std::move(range_name)) {}
};

// One resolved leaf of a sparse Stage-2 table. The library consumes the page
// walker's leaf view (block/page descriptors), so 4K/16K/64K granules and any
// supported block size are handled uniformly.
struct S2Mapping {
  std::uint64_t ipa = 0U;
  std::uint64_t pa = 0U;
  std::uint64_t size = 0U;
  bool readable = false;
  bool writable = false;
  bool executable = false;
};

enum class MmioRegisterKind {
  Rw,
  ReadOnly,
  WriteOnly,
  Reserved,
};

struct MmioRegister {
  std::uint64_t offset = 0U;  // Offset relative to the device window base.
  std::uint64_t size = 4U;
  MmioRegisterKind kind = MmioRegisterKind::Rw;
  std::string name;

  MmioRegister() = default;
  MmioRegister(std::uint64_t reg_offset, std::uint64_t reg_size,
               MmioRegisterKind reg_kind, std::string reg_name)
      : offset(reg_offset),
        size(reg_size),
        kind(reg_kind),
        name(std::move(reg_name)) {}
};

struct MmioRegion {
  std::uint64_t base = 0U;  // IPA base of the device register window.
  std::uint64_t size = 0U;
  std::string name;
  std::vector<MmioRegister> registers;

  MmioRegion() = default;
  MmioRegion(std::uint64_t window_base, std::uint64_t window_size,
             std::string region_name)
      : base(window_base),
        size(window_size),
        name(std::move(region_name)) {}
};

struct VmConfig {
  std::uint32_t id = 0U;
  std::string name;
  std::uint64_t hcr_el2 = 0U;
  std::uint64_t vttbr_el2 = 0U;
  std::uint64_t vtcr_el2 = 0U;
  std::vector<S2Mapping> s2_mappings;  // Flattened sparse Stage-2 leaf view.
  std::vector<MmioRegion> mmio_regions;
};

struct DecodedEsr {
  std::uint32_t ec = 0U;
  std::uint32_t iss = 0U;
  std::uint32_t access_size = 0U;  // 1, 2, 4 or 8 bytes; 0 if undecodable.
  bool is_write = false;
  std::uint32_t source_register = 0U;  // ISS.SRT (guest Xt index).
  bool valid_syndrome = false;
};

struct MmioAccess {
  std::uint32_t vm_id = 0U;
  std::uint64_t esr_el2 = 0U;
  std::uint64_t ipa = 0U;  // HPFAR_EL2/FAR_EL2 resolved faulting IPA.
};

// Pure helpers, usable without an Auditor instance.
std::uint16_t extract_vmid(std::uint64_t vttbr_el2);
DecodedEsr decode_data_abort_esr(std::uint64_t esr_el2);
bool ranges_overlap(std::uint64_t base_a, std::uint64_t size_a,
                    std::uint64_t base_b, std::uint64_t size_b);
std::string to_text(const Finding& finding);

class Auditor {
 public:
  Auditor() = default;
  explicit Auditor(AuditPolicy policy);

  void set_policy(const AuditPolicy& policy);
  const AuditPolicy& policy() const;

  void add_vm(const VmConfig& vm);
  void add_protected_range(const ProtectedRange& range);

  const std::vector<VmConfig>& vms() const;
  const std::vector<ProtectedRange>& protected_ranges() const;

  // Checks (1) and (2): HCR traps, VMID uniqueness and Stage-2 isolation.
  std::vector<Finding> audit_static_configuration() const;

  // Check (3) for one ESR-driven MMIO access. A None finding means allowed.
  Finding audit_mmio_access(const MmioAccess& access) const;

  // Check (3) over a full ESR access sequence; every violation is returned.
  std::vector<Finding> audit_mmio_sequence(
      const std::vector<MmioAccess>& accesses) const;

  // Full report: static findings plus all access-sequence findings.
  std::vector<Finding> audit_all(
      const std::vector<MmioAccess>& accesses) const;

 private:
  const VmConfig* find_vm(std::uint32_t vm_id) const;

  AuditPolicy policy_;
  std::vector<VmConfig> vms_;
  std::vector<ProtectedRange> protected_ranges_;
};

}  // namespace el2_audit

#endif  // EL2_AUDIT_EL2_AUDIT_H
