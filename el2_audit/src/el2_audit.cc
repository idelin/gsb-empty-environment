#include "el2_audit/el2_audit.h"

#include <algorithm>
#include <array>
#include <iomanip>
#include <limits>
#include <sstream>
#include <unordered_map>

namespace el2_audit {
namespace {

constexpr std::uint64_t U64_MAX = std::numeric_limits<std::uint64_t>::max();

// A [base, base + size) byte range is canonical when size is non-zero and the
// inclusive last byte base + size - 1 does not wrap the 64-bit address space.
bool range_is_canonical(std::uint64_t base, std::uint64_t size) {
  return size != 0U && base <= (U64_MAX - (size - 1U));
}

// True when canonical [base, base + size) extends beyond the given limit
// (exclusive). The short-circuit plus difference form never computes
// base + size, so it cannot wrap the 64-bit address space.
bool range_ends_after(std::uint64_t base, std::uint64_t size,
                      std::uint64_t limit) {
  if (size == 0U) {
    return false;
  }
  if (base >= limit) {
    return true;
  }
  return (limit - base) < size;
}

// Offset-space overlap used when both values are relative to one window.
bool range_overlap_present(std::uint64_t base, std::uint64_t size,
                           std::uint64_t point) {
  return point >= base && (point - base) < size;
}

std::string hex(std::uint64_t value) {
  std::ostringstream stream;
  stream << "0x" << std::hex << std::uppercase << value;
  return stream.str();
}

std::string size_name(std::uint64_t size) {
  std::ostringstream stream;
  stream << size;
  return stream.str();
}

bool is_power_of_two(std::uint64_t value) {
  return value != 0U && (value & (value - 1U)) == 0U;
}

const char* register_kind_name(MmioRegisterKind kind) {
  switch (kind) {
    case MmioRegisterKind::Rw:
      return "RW";
    case MmioRegisterKind::ReadOnly:
      return "RO";
    case MmioRegisterKind::WriteOnly:
      return "WO";
    case MmioRegisterKind::Reserved:
      return "RESERVED";
  }
  return "UNKNOWN";
}

}  // namespace

const char* to_string(Severity severity) {
  switch (severity) {
    case Severity::Info:
      return "INFO";
    case Severity::Warning:
      return "WARNING";
    case Severity::Error:
      return "ERROR";
  }
  return "UNKNOWN";
}

const char* to_string(FindingCode code) {
  switch (code) {
    case FindingCode::None:
      return "None";
    case FindingCode::HcrRequiredTrapMissing:
      return "HcrRequiredTrapMissing";
    case FindingCode::VmidZero:
      return "VmidZero";
    case FindingCode::VmidCollision:
      return "VmidCollision";
    case FindingCode::S2MappingInvalid:
      return "S2MappingInvalid";
    case FindingCode::S2IpaOverlap:
      return "S2IpaOverlap";
    case FindingCode::S2OverlapsEl2Memory:
      return "S2OverlapsEl2Memory";
    case FindingCode::S2OverlapsOtherVmMemory:
      return "S2OverlapsOtherVmMemory";
    case FindingCode::MmioWindowMappedInS2:
      return "MmioWindowMappedInS2";
    case FindingCode::MmioWindowsOverlap:
      return "MmioWindowsOverlap";
    case FindingCode::MmioRegionInvalid:
      return "MmioRegionInvalid";
    case FindingCode::MmioRegisterInvalid:
      return "MmioRegisterInvalid";
    case FindingCode::EsrNotDataAbort:
      return "EsrNotDataAbort";
    case FindingCode::EsrSyndromeInvalid:
      return "EsrSyndromeInvalid";
    case FindingCode::AccessVmUnknown:
      return "AccessVmUnknown";
    case FindingCode::AccessOutsideAnyDevice:
      return "AccessOutsideAnyDevice";
    case FindingCode::AccessCrossesDeviceWindow:
      return "AccessCrossesDeviceWindow";
    case FindingCode::AccessUnaligned:
      return "AccessUnaligned";
    case FindingCode::AccessCrossesRegister:
      return "AccessCrossesRegister";
    case FindingCode::AccessReservedWrite:
      return "AccessReservedWrite";
    case FindingCode::AccessReadOnlyWrite:
      return "AccessReadOnlyWrite";
  }
  return "Unknown";
}

const char* hcr_bit_name(unsigned int bit) {
  switch (bit) {
    case HCR_VM_BIT:
      return "VM";
    case HCR_SWIO_BIT:
      return "SWIO";
    case HCR_FMO_BIT:
      return "FMO";
    case HCR_IMO_BIT:
      return "IMO";
    case HCR_AMO_BIT:
      return "AMO";
    case HCR_TWI_BIT:
      return "TWI";
    case HCR_TWE_BIT:
      return "TWE";
    case HCR_TID0_BIT:
      return "TID0";
    case HCR_TID1_BIT:
      return "TID1";
    case HCR_TID2_BIT:
      return "TID2";
    case HCR_TID3_BIT:
      return "TID3";
    case HCR_TSC_BIT:
      return "TSC";
    case HCR_TIDCP_BIT:
      return "TIDCP";
    case HCR_TACR_BIT:
      return "TACR";
    case HCR_TSW_BIT:
      return "TSW";
    case HCR_TPCP_BIT:
      return "TPCP";
    case HCR_TPU_BIT:
      return "TPU";
    case HCR_TTLB_BIT:
      return "TTLB";
    case HCR_TVM_BIT:
      return "TVM";
    case HCR_TGE_BIT:
      return "TGE";
    case HCR_TDZ_BIT:
      return "TDZ";
    case HCR_HCD_BIT:
      return "HCD";
    case HCR_TRVM_BIT:
      return "TRVM";
    default:
      return "UNKNOWN";
  }
}

std::uint16_t extract_vmid(std::uint64_t vttbr_el2) {
  return static_cast<std::uint16_t>(
      (vttbr_el2 & VTTBR_VMID_MASK) >> VTTBR_VMID_SHIFT);
}

DecodedEsr decode_data_abort_esr(std::uint64_t esr_el2) {
  DecodedEsr decoded;
  decoded.ec = static_cast<std::uint32_t>(
      (esr_el2 >> ESR_EC_SHIFT) & ESR_EC_MASK);
  decoded.iss = static_cast<std::uint32_t>(esr_el2 & ESR_ISS_MASK);

  const bool syndrome_valid =
      (decoded.iss & (1U << ESR_ISS_ISV_BIT)) != 0U;
  decoded.valid_syndrome =
      decoded.ec == ESR_EC_DATA_ABORT_LOWER_EL && syndrome_valid;
  if (!decoded.valid_syndrome) {
    return decoded;
  }

  const std::uint32_t sas =
      (decoded.iss >> ESR_ISS_SAS_SHIFT) & ESR_ISS_SAS_MASK;
  static const std::array<std::uint32_t, 4> kSizesBySas = {1U, 2U, 4U, 8U};
  decoded.access_size = kSizesBySas[sas];
  decoded.is_write = (decoded.iss & (1U << ESR_ISS_WNR_BIT)) != 0U;
  decoded.source_register =
      (decoded.iss >> ESR_ISS_SRT_SHIFT) & ESR_ISS_SRT_MASK;
  return decoded;
}

bool ranges_overlap(std::uint64_t base_a, std::uint64_t size_a,
                    std::uint64_t base_b, std::uint64_t size_b) {
  if (size_a == 0U || size_b == 0U) {
    return false;
  }
  // Difference-based test: no addition can wrap the 64-bit address space.
  if (base_a <= base_b) {
    return (base_b - base_a) < size_a;
  }
  return (base_a - base_b) < size_b;
}

std::string to_text(const Finding& finding) {
  std::ostringstream out;
  out << "[" << to_string(finding.severity) << "]["
      << to_string(finding.code) << "]";
  if (finding.vm_id != NO_VM) {
    out << " vm=" << finding.vm_id;
  }
  if (!finding.detail.empty()) {
    out << " " << finding.detail;
  }
  if (finding.witness != 0U) {
    out << " witness=" << hex(finding.witness);
  }
  return out.str();
}

Auditor::Auditor(AuditPolicy policy) : policy_(policy) {}

void Auditor::set_policy(const AuditPolicy& policy) { policy_ = policy; }

const AuditPolicy& Auditor::policy() const { return policy_; }

void Auditor::add_vm(const VmConfig& vm) { vms_.push_back(vm); }

void Auditor::add_protected_range(const ProtectedRange& range) {
  protected_ranges_.push_back(range);
}

const std::vector<VmConfig>& Auditor::vms() const { return vms_; }

const std::vector<ProtectedRange>& Auditor::protected_ranges() const {
  return protected_ranges_;
}

const VmConfig* Auditor::find_vm(std::uint32_t vm_id) const {
  for (const VmConfig& vm : vms_) {
    if (vm.id == vm_id) {
      return &vm;
    }
  }
  return nullptr;
}

std::vector<Finding> Auditor::audit_static_configuration() const {
  std::vector<Finding> findings;

  const auto push = [&findings](FindingCode code, std::uint32_t vm_id,
                                std::string detail, std::uint64_t witness) {
    findings.push_back(
        Finding{code, Severity::Error, vm_id, std::move(detail), witness});
  };

  // ---- (1a) HCR_EL2 mandatory trap bits, VM included --------------------
  for (const VmConfig& vm : vms_) {
    const std::uint64_t missing =
        policy_.required_hcr_traps & (~vm.hcr_el2);
    for (unsigned int bit = 0U; bit < 64U; ++bit) {
      if ((missing & (1ULL << bit)) == 0U) {
        continue;
      }
      std::ostringstream detail;
      detail << "HCR_EL2 missing required trap bit " << hcr_bit_name(bit)
             << " (bit " << bit << ")";
      push(FindingCode::HcrRequiredTrapMissing, vm.id, detail.str(),
           1ULL << bit);
    }
  }

  // ---- (1b) VMID: non-zero and unique across isolation domains ----------
  std::unordered_map<std::uint16_t, std::uint32_t> vmid_owner;
  for (const VmConfig& vm : vms_) {
    const std::uint16_t vmid = extract_vmid(vm.vttbr_el2);
    if (vmid == 0U) {
      std::ostringstream detail;
      detail << "VTTBR_EL2 carries VMID 0; non-zero VMID required for "
             << "isolated TLB tagging";
      push(FindingCode::VmidZero, vm.id, detail.str(), vm.vttbr_el2);
      continue;
    }
    const auto seen = vmid_owner.find(vmid);
    if (seen != vmid_owner.end()) {
      std::ostringstream detail;
      detail << "VMID " << static_cast<std::uint32_t>(vmid)
             << " collision with vm " << seen->second
             << "; TLB entries can alias across isolation domains";
      push(FindingCode::VmidCollision, vm.id, detail.str(),
           static_cast<std::uint64_t>(vmid) << VTTBR_VMID_SHIFT);
    } else {
      vmid_owner.emplace(vmid, vm.id);
    }
  }

  // ---- Descriptor table sanity: MMIO windows and registers -------------
  for (const VmConfig& vm : vms_) {
    for (const MmioRegion& region : vm.mmio_regions) {
      if (!range_is_canonical(region.base, region.size)) {
        std::ostringstream detail;
        detail << "MMIO region '" << region.name << "' has invalid window "
               << hex(region.base) << " size " << region.size;
        push(FindingCode::MmioRegionInvalid, vm.id, detail.str(), region.base);
      }
      for (const MmioRegister& reg : region.registers) {
        const bool reg_in_window =
            range_is_canonical(reg.offset, reg.size) &&
            !range_ends_after(reg.offset, reg.size, region.size) &&
            is_power_of_two(reg.size);
        if (!reg_in_window) {
          std::ostringstream detail;
          detail << "MMIO register '" << reg.name << "' offset "
                 << hex(reg.offset) << " size " << reg.size
                 << " is invalid for region '" << region.name << "'";
          push(FindingCode::MmioRegisterInvalid, vm.id, detail.str(),
               reg.offset);
        }
      }
    }
    for (std::size_t i = 0U; i < vm.mmio_regions.size(); ++i) {
      for (std::size_t j = i + 1U; j < vm.mmio_regions.size(); ++j) {
        const MmioRegion& first = vm.mmio_regions[i];
        const MmioRegion& second = vm.mmio_regions[j];
        if (ranges_overlap(first.base, first.size, second.base,
                           second.size)) {
          std::ostringstream detail;
          detail << "MMIO windows '" << first.name << "' and '"
                 << second.name << "' overlap in IPA";
          push(FindingCode::MmioWindowsOverlap, vm.id, detail.str(),
               first.base);
        }
      }
    }
  }

  // ---- (2a) Stage-2 descriptor sanity and intra-VM IPA overlap ----------
  for (const VmConfig& vm : vms_) {
    for (std::size_t i = 0U; i < vm.s2_mappings.size(); ++i) {
      const S2Mapping& mapping = vm.s2_mappings[i];
      const bool canonical_ipa =
          range_is_canonical(mapping.ipa, mapping.size);
      const bool canonical_pa =
          range_is_canonical(mapping.pa, mapping.size);
      const bool has_permission =
          mapping.readable || mapping.writable || mapping.executable;
      if (!canonical_ipa || !canonical_pa || !has_permission) {
        std::ostringstream detail;
        detail << "Stage-2 mapping[" << i << "] IPA " << hex(mapping.ipa)
               << " PA " << hex(mapping.pa) << " size " << mapping.size
               << " is invalid or grants no permission";
        push(FindingCode::S2MappingInvalid, vm.id, detail.str(), mapping.ipa);
      }
    }
    for (std::size_t i = 0U; i < vm.s2_mappings.size(); ++i) {
      for (std::size_t j = i + 1U; j < vm.s2_mappings.size(); ++j) {
        const S2Mapping& left = vm.s2_mappings[i];
        const S2Mapping& right = vm.s2_mappings[j];
        if (ranges_overlap(left.ipa, left.size, right.ipa, right.size)) {
          std::ostringstream detail;
          detail << "Stage-2 IPA overlap between mapping[" << i << "] and ["
                 << j << "] at " << hex(right.ipa);
          push(FindingCode::S2IpaOverlap, vm.id, detail.str(), right.ipa);
        }
      }
    }
  }

  // ---- (2b) No mapping of hypervisor-owned physical memory --------------
  for (const VmConfig& vm : vms_) {
    for (const S2Mapping& mapping : vm.s2_mappings) {
      if (!range_is_canonical(mapping.pa, mapping.size)) {
        continue;
      }
      for (const ProtectedRange& protected_range : protected_ranges_) {
        if (!range_is_canonical(protected_range.base,
                                protected_range.size)) {
          continue;
        }
        if (ranges_overlap(mapping.pa, mapping.size, protected_range.base,
                           protected_range.size)) {
          std::ostringstream detail;
          detail << "Stage-2 mapping PA " << hex(mapping.pa) << " size "
                 << mapping.size << " overlaps protected EL2 range '"
                 << protected_range.name << "'";
          push(FindingCode::S2OverlapsEl2Memory, vm.id, detail.str(),
               mapping.pa);
        }
      }
    }
  }

  // ---- (2c) No mapping of physical memory owned by another VM -----------
  for (std::size_t i = 0U; i < vms_.size(); ++i) {
    for (std::size_t j = i + 1U; j < vms_.size(); ++j) {
      const VmConfig& left = vms_[i];
      const VmConfig& right = vms_[j];
      for (const S2Mapping& left_map : left.s2_mappings) {
        for (const S2Mapping& right_map : right.s2_mappings) {
          if (!range_is_canonical(left_map.pa, left_map.size) ||
              !range_is_canonical(right_map.pa, right_map.size)) {
            continue;
          }
          if (ranges_overlap(left_map.pa, left_map.size, right_map.pa,
                             right_map.size)) {
            std::ostringstream detail_left;
            detail_left << "Stage-2 PA " << hex(left_map.pa)
                        << " overlaps physical memory owned by vm "
                        << right.id;
            push(FindingCode::S2OverlapsOtherVmMemory, left.id,
                 detail_left.str(), left_map.pa);
            std::ostringstream detail_right;
            detail_right << "Stage-2 PA " << hex(right_map.pa)
                         << " overlaps physical memory owned by vm "
                         << left.id;
            push(FindingCode::S2OverlapsOtherVmMemory, right.id,
                 detail_right.str(), right_map.pa);
          }
        }
      }
    }
  }

  // ---- (2d) Emulated MMIO windows must stay unmapped in Stage-2 ---------
  for (const VmConfig& vm : vms_) {
    for (const MmioRegion& region : vm.mmio_regions) {
      if (!range_is_canonical(region.base, region.size)) {
        continue;
      }
      for (const S2Mapping& mapping : vm.s2_mappings) {
        if (!range_is_canonical(mapping.ipa, mapping.size)) {
          continue;
        }
        if (ranges_overlap(region.base, region.size, mapping.ipa,
                           mapping.size)) {
          std::ostringstream detail;
          detail << "Emulated MMIO window '" << region.name << "' IPA "
                 << hex(region.base) << " is backed by a Stage-2 mapping; "
                 << "guest bypasses EL2 and reaches physical hardware";
          push(FindingCode::MmioWindowMappedInS2, vm.id, detail.str(),
               region.base);
        }
      }
    }
  }

  return findings;
}

Finding Auditor::audit_mmio_access(const MmioAccess& access) const {
  const auto violation = [&access](FindingCode code, std::string detail,
                                   std::uint64_t witness) {
    return Finding{code, Severity::Error, access.vm_id, std::move(detail),
                   witness};
  };

  // ESR must be a lower-EL data abort with a valid instruction syndrome so
  // that width, direction and source register are architecturally defined.
  const DecodedEsr decoded = decode_data_abort_esr(access.esr_el2);
  if (decoded.ec != ESR_EC_DATA_ABORT_LOWER_EL) {
    return violation(FindingCode::EsrNotDataAbort,
                     "ESR_EL2 EC is not Data Abort from lower EL",
                     access.esr_el2);
  }
  if (!decoded.valid_syndrome) {
    return violation(FindingCode::EsrSyndromeInvalid,
                     "ESR_EL2 ISS.ISV is clear; cannot decode MMIO access",
                     access.esr_el2);
  }

  const VmConfig* vm = find_vm(access.vm_id);
  if (vm == nullptr) {
    return violation(FindingCode::AccessVmUnknown,
                     "MMIO access targets an unknown VM id", access.ipa);
  }

  const std::uint64_t size = decoded.access_size;

  // Reject IPA + size wraparound before any window arithmetic.
  if (!range_is_canonical(access.ipa, size)) {
    return violation(FindingCode::AccessCrossesDeviceWindow,
                     "IPA + access size wraps the 64-bit address space",
                     access.ipa);
  }

  const MmioRegion* region = nullptr;
  for (const MmioRegion& candidate : vm->mmio_regions) {
    if (range_is_canonical(candidate.base, candidate.size) &&
        access.ipa >= candidate.base &&
        access.ipa < candidate.base + candidate.size) {
      // Candidate means the start IPA is inside the window; whether the full
      // access stays inside is enforced below as a hard boundary check.
      region = &candidate;
      break;
    }
  }
  if (region == nullptr) {
    std::ostringstream detail;
    detail << "IPA " << hex(access.ipa) << " size " << size_name(size)
           << " does not land inside any emulated MMIO device window";
    return violation(FindingCode::AccessOutsideAnyDevice, detail.str(),
                     access.ipa);
  }

  // Hard boundary check: [offset, offset + size) must stay inside the
  // half-open device window [0, region->size). The window descriptor is
  // validated as canonical, so candidate.base + size below cannot wrap.
  const std::uint64_t in_window_offset = access.ipa - region->base;
  const bool access_wider_than_window = size > region->size;
  // Half-open window: legal iff offset + size <= region->size. The standard
  // unsigned-wrap test (sum < offset) catches an overflowing end address.
  const std::uint64_t window_end_offset = in_window_offset + size;
  const bool end_would_wrap = window_end_offset < in_window_offset;
  const bool crosses_window =
      access_wider_than_window || end_would_wrap ||
      window_end_offset >= region->size;
  if (crosses_window) {
    std::ostringstream detail;
    detail << "Access " << hex(access.ipa) << " size " << size_name(size)
           << " crosses device '" << region->name
           << "' window of size " << region->size;
    return violation(FindingCode::AccessCrossesDeviceWindow, detail.str(),
                     access.ipa);
  }

  // Hard alignment check: every MMIO access must be naturally aligned.
  if ((access.ipa & (size - 1U)) != 0U) {
    std::ostringstream detail;
    detail << "Unaligned " << size_name(size)
           << (decoded.is_write ? "-byte write" : "-byte read") << " at IPA "
           << hex(access.ipa) << " in device '" << region->name << "'";
    return violation(FindingCode::AccessUnaligned, detail.str(), access.ipa);
  }

  const std::uint64_t offset = access.ipa - region->base;
  const MmioRegister* target = nullptr;
  for (const MmioRegister& reg : region->registers) {
    if (offset == reg.offset) {
      target = &reg;
      break;
    }
  }

  // Accesses not matching a declared register start either straddle two
  // registers or hit an undeclared (reserved) slot.
  if (target == nullptr) {
    const MmioRegister* surrounding = nullptr;
    for (const MmioRegister& reg : region->registers) {
      if (range_overlap_present(reg.offset, reg.size, offset)) {
        surrounding = &reg;
        break;
      }
    }
    if (surrounding != nullptr) {
      std::ostringstream detail;
      detail << "Access at offset " << hex(offset) << " size "
             << size_name(size) << " starts inside register '"
             << surrounding->name << "' and crosses its "
             << surrounding->size << "-byte boundary in device '"
             << region->name << "'";
      return violation(FindingCode::AccessCrossesRegister, detail.str(),
                       access.ipa);
    }
    if (decoded.is_write && policy_.treat_undeclared_as_reserved) {
      std::ostringstream detail;
      detail << "Write to undeclared/reserved offset " << hex(offset)
             << " in device '" << region->name << "'";
      return violation(FindingCode::AccessReservedWrite, detail.str(),
                       access.ipa);
    }
    return Finding{};
  }

  // Exact-start access must still fit the declared register width.
  const std::uint64_t register_offset = offset - target->offset;
  const std::uint64_t register_end = register_offset + size;
  const bool register_end_wraps = register_end < register_offset;
  if (register_end_wraps ||
      register_end > target->size) {
    std::ostringstream detail;
    detail << size_name(size) << "-byte access at register '" << target->name
           << "' crosses its " << target->size << "-byte boundary in device '"
           << region->name << "'";
    return violation(FindingCode::AccessCrossesRegister, detail.str(),
                     access.ipa);
  }

  if (!decoded.is_write) {
    return Finding{};
  }

  if (target->kind == MmioRegisterKind::Reserved) {
    std::ostringstream detail;
    detail << "Write to " << register_kind_name(target->kind)
           << " register '" << target->name
           << "' at IPA " << hex(access.ipa) << " in device '"
           << region->name << "'";
    return violation(FindingCode::AccessReservedWrite, detail.str(),
                     access.ipa);
  }
  if (target->kind == MmioRegisterKind::ReadOnly) {
    std::ostringstream detail;
    detail << "Write to read-only register '" << target->name
           << "' at IPA " << hex(access.ipa) << " in device '"
           << region->name << "'";
    return violation(FindingCode::AccessReadOnlyWrite, detail.str(),
                     access.ipa);
  }

  return Finding{};
}

std::vector<Finding> Auditor::audit_mmio_sequence(
    const std::vector<MmioAccess>& accesses) const {
  std::vector<Finding> findings;
  for (const MmioAccess& access : accesses) {
    Finding finding = audit_mmio_access(access);
    if (!finding.ok()) {
      findings.push_back(std::move(finding));
    }
  }
  return findings;
}

std::vector<Finding> Auditor::audit_all(
    const std::vector<MmioAccess>& accesses) const {
  std::vector<Finding> findings = audit_static_configuration();
  std::vector<Finding> access_findings = audit_mmio_sequence(accesses);
  findings.insert(findings.end(),
                  std::make_move_iterator(access_findings.begin()),
                  std::make_move_iterator(access_findings.end()));
  return findings;
}

}  // namespace el2_audit
