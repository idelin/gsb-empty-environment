#include "el2_audit/el2_audit.h"

#include <cstdlib>
#include <functional>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

using el2_audit::AuditPolicy;
using el2_audit::Auditor;
using el2_audit::Finding;
using el2_audit::FindingCode;
using el2_audit::MmioAccess;
using el2_audit::MmioRegion;
using el2_audit::MmioRegister;
using el2_audit::MmioRegisterKind;
using el2_audit::ProtectedRange;
using el2_audit::S2Mapping;
using el2_audit::VmConfig;

struct TestCase {
  std::string name;
  std::function<void()> body;
};

std::vector<TestCase>& registry() {
  static std::vector<TestCase> cases;
  return cases;
}

struct Registrar {
  Registrar(const std::string& name, std::function<void()> body) {
    registry().push_back(TestCase{name, std::move(body)});
  }
};

void check_true(bool condition, const std::string& message) {
  if (!condition) {
    throw std::runtime_error(message);
  }
}

void check_contains_code(const std::vector<Finding>& findings,
                         FindingCode expected, std::uint32_t vm_id) {
  for (const Finding& finding : findings) {
    if (finding.code == expected && finding.vm_id == vm_id) {
      return;
    }
  }
  throw std::runtime_error(std::string("missing finding ") +
                           el2_audit::to_string(expected) + " for vm " +
                           std::to_string(vm_id));
}

void check_absent_code(const std::vector<Finding>& findings,
                       FindingCode forbidden) {
  for (const Finding& finding : findings) {
    if (finding.code == forbidden) {
      throw std::runtime_error(std::string("unexpected finding ") +
                               el2_audit::to_string(forbidden));
    }
  }
}

// Build an ESR_EL2 for a lower-EL data abort with a valid ISS syndrome.
std::uint64_t make_mmio_esr(std::uint32_t access_size, bool is_write,
                            std::uint32_t source_register = 0U) {
  std::uint32_t sas = 0U;
  switch (access_size) {
    case 1U:
      sas = 0U;
      break;
    case 2U:
      sas = 1U;
      break;
    case 4U:
      sas = 2U;
      break;
    case 8U:
      sas = 3U;
      break;
    default:
      throw std::runtime_error("unsupported MMIO access size");
  }
  std::uint32_t iss = (1U << el2_audit::ESR_ISS_ISV_BIT) |
                      (sas << el2_audit::ESR_ISS_SAS_SHIFT) |
                      ((source_register & el2_audit::ESR_ISS_SRT_MASK)
                       << el2_audit::ESR_ISS_SRT_SHIFT) |
                      el2_audit::DFSC_TRANSLATION_FAULT_LEVEL2;
  if (is_write) {
    iss |= (1U << el2_audit::ESR_ISS_WNR_BIT);
  }
  return (static_cast<std::uint64_t>(
              el2_audit::ESR_EC_DATA_ABORT_LOWER_EL)
          << el2_audit::ESR_EC_SHIFT) |
         static_cast<std::uint64_t>(iss);
}

std::uint64_t vttbr_with_vmid(std::uint16_t vmid) {
  return static_cast<std::uint64_t>(vmid)
         << el2_audit::VTTBR_VMID_SHIFT;
}

// Default-hardened HCR: every policy-required trap bit set.
std::uint64_t hardened_hcr() {
  return AuditPolicy{}.required_hcr_traps;
}

MmioRegion make_gicd_window() {
  MmioRegion region(0x8000000ULL, 0x1000ULL, "vgic-distributor");
  region.registers.push_back(
      MmioRegister(0x000ULL, 4U, MmioRegisterKind::Rw, "GICD_CTLR"));
  region.registers.push_back(
      MmioRegister(0x004ULL, 4U, MmioRegisterKind::ReadOnly, "GICD_TYPER"));
  region.registers.push_back(
      MmioRegister(0x008ULL, 4U, MmioRegisterKind::Rw, "GICD_IIDR_slot"));
  region.registers.push_back(
      MmioRegister(0x010ULL, 8U, MmioRegisterKind::Rw, "GICD_IGROUPR_pair"));
  region.registers.push_back(
      MmioRegister(0x080ULL, 4U, MmioRegisterKind::Reserved,
                   "GICD_RESERVED_080"));
  return region;
}

MmioRegion make_pci_window() {
  MmioRegion region(0x9000000ULL, 0x100ULL, "vpci-config-space");
  region.registers.push_back(
      MmioRegister(0x00ULL, 4U, MmioRegisterKind::Rw, "VENDOR_DEVICE_ID"));
  region.registers.push_back(
      MmioRegister(0x04ULL, 4U, MmioRegisterKind::ReadOnly, "STATUS_COMMAND"));
  return region;
}

VmConfig make_clean_vm(std::uint32_t id, std::uint16_t vmid) {
  VmConfig vm;
  vm.id = id;
  vm.name = std::string("vm") + std::to_string(id);
  vm.hcr_el2 = hardened_hcr();
  vm.vttbr_el2 = vttbr_with_vmid(vmid);
  vm.vtcr_el2 = 0U;
  vm.s2_mappings.push_back(S2Mapping{0x40000000ULL,
                                     0x80000000ULL + id * 0x40000000ULL,
                                     0x200000ULL, true, true, false});
  vm.mmio_regions.push_back(make_gicd_window());
  vm.mmio_regions.push_back(make_pci_window());
  return vm;
}

Auditor make_clean_two_vm_auditor() {
  Auditor auditor;
  auditor.add_protected_range(
      ProtectedRange(0x08000000ULL, 0x00200000ULL, "el2-private-dram"));
  auditor.add_vm(make_clean_vm(1U, 0x1001U));
  auditor.add_vm(make_clean_vm(2U, 0x1002U));
  return auditor;
}

}  // namespace

namespace {

Registrar vmid_collision_test(
    "vmid_collision_blocks_tlb_alias", [] {
      Auditor auditor = make_clean_two_vm_auditor();
      VmConfig colliding = make_clean_vm(3U, 0x1001U);  // Reuses VM 1 VMID.
      auditor.add_vm(colliding);

      const std::vector<Finding> findings =
          auditor.audit_static_configuration();
      check_contains_code(findings, FindingCode::VmidCollision, 3U);

      check_true(el2_audit::extract_vmid(vttbr_with_vmid(0x1001U)) == 0x1001U,
                 "VMID extraction must read VTTBR_EL2[63:48]");
    });

Registrar hcr_vm_missing_test(
    "hcr_vm_and_trap_bits_missing_are_reported", [] {
      Auditor auditor = make_clean_two_vm_auditor();
      VmConfig weak = make_clean_vm(4U, 0x1004U);
      weak.hcr_el2 =
          hardened_hcr() & ~(el2_audit::HCR_VM | el2_audit::HCR_TSC |
                             el2_audit::HCR_TWE | el2_audit::HCR_TVM |
                             el2_audit::HCR_TACR);
      auditor.add_vm(weak);

      const std::vector<Finding> findings =
          auditor.audit_static_configuration();
      check_contains_code(findings, FindingCode::HcrRequiredTrapMissing, 4U);

      bool saw_vm = false;
      bool saw_tsc = false;
      for (const Finding& finding : findings) {
        if (finding.vm_id != 4U ||
            finding.code != FindingCode::HcrRequiredTrapMissing) {
          continue;
        }
        saw_vm = saw_vm || finding.witness == el2_audit::HCR_VM;
        saw_tsc = saw_tsc || finding.witness == el2_audit::HCR_TSC;
      }
      check_true(saw_vm, "missing HCR_EL2.VM must be a separate finding");
      check_true(saw_tsc, "missing HCR_EL2.TSC must be a separate finding");

      VmConfig zero_vmid = make_clean_vm(5U, 0U);
      Auditor single;
      single.add_vm(zero_vmid);
      check_contains_code(single.audit_static_configuration(),
                          FindingCode::VmidZero, 5U);
    });

Registrar mmio_passthrough_and_memory_test(
    "mmio_passthrough_and_physical_isolation_are_blocked", [] {
      Auditor auditor = make_clean_two_vm_auditor();

      // Leak 1: emulated GICD window gets a backing Stage-2 mapping.
      VmConfig leak = make_clean_vm(6U, 0x1006U);
      leak.s2_mappings.push_back(S2Mapping{0x8000000ULL, 0xE0000000ULL,
                                           0x1000ULL, true, true, false});
      auditor.add_vm(leak);
      std::vector<Finding> findings = auditor.audit_static_configuration();
      check_contains_code(findings, FindingCode::MmioWindowMappedInS2, 6U);

      // Leak 2: map the hypervisor's own protected EL2 DRAM.
      VmConfig el2_leak = make_clean_vm(7U, 0x1007U);
      el2_leak.s2_mappings.push_back(
          S2Mapping{0x50000000ULL, 0x08010000ULL, 0x10000ULL, true, true,
                    false});
      auditor.add_vm(el2_leak);
      findings = auditor.audit_static_configuration();
      check_contains_code(findings, FindingCode::S2OverlapsEl2Memory, 7U);

      // Leak 3: a second isolation domain maps a PA already owned by VM 1's
      // RAM (VM 1 spans PA [0x80000000, 0x80200000)). Use a dedicated
      // two-VM auditor so the pair-wise verdict is fully deterministic.
      Auditor cross_auditor;
      VmConfig owner = make_clean_vm(1U, 0x2001U);
      // make_clean_vm(1) places RAM at PA 0xC0000000; target inside it.
      const std::uint64_t owner_pa = owner.s2_mappings[0].pa;
      VmConfig intruder = make_clean_vm(8U, 0x2008U);
      intruder.s2_mappings.clear();
      intruder.s2_mappings.push_back(
          S2Mapping{0x60000000ULL, owner_pa + 0x1000ULL, 0x1000ULL, true,
                    true, false});
      cross_auditor.add_vm(owner);
      cross_auditor.add_vm(intruder);
      const std::vector<Finding> cross_findings =
          cross_auditor.audit_static_configuration();
      check_contains_code(cross_findings,
                          FindingCode::S2OverlapsOtherVmMemory, 8U);
      check_contains_code(cross_findings,
                          FindingCode::S2OverlapsOtherVmMemory, 1U);
    });

Registrar cross_boundary_eight_byte_write_test(
    "cross_boundary_unaligned_and_illegal_writes_are_hard_blocked", [] {
      Auditor auditor = make_clean_two_vm_auditor();

      const std::uint64_t gicd = 0x8000000ULL;
      const std::uint64_t pci = 0x9000000ULL;

      // Legal: aligned 8-byte write fully inside the 8-byte GICD register.
      Finding allowed = auditor.audit_mmio_access(
          MmioAccess{1U, make_mmio_esr(8U, true), gicd + 0x10ULL});
      check_true(allowed.ok(), "aligned in-register 8-byte write must pass");

      // The headline attack: 8-byte write at offset 4 is not naturally
      // aligned (4 mod 8 != 0), so the hard alignment trap fires first.
      Finding straddle = auditor.audit_mmio_access(
          MmioAccess{1U, make_mmio_esr(8U, true), gicd + 0x04ULL});
      check_true(straddle.code == FindingCode::AccessUnaligned,
                 "non-naturally-aligned 8-byte write must be blocked");

      // An aligned but oversized 4-byte-window access still straddles two
      // registers and corrupts adjacent device state.
      Finding aligned_straddle = auditor.audit_mmio_access(
          MmioAccess{1U, make_mmio_esr(8U, true), gicd + 0x00ULL});
      check_true(aligned_straddle.code ==
                     FindingCode::AccessCrossesRegister,
                 "aligned 8-byte write across 4-byte registers is blocked");

      // 8-byte write whose IPA + size leaves the 0x1000-byte device window.
      Finding window_cross = auditor.audit_mmio_access(
          MmioAccess{1U, make_mmio_esr(8U, true), gicd + 0xFF8ULL});
      check_true(
          window_cross.code == FindingCode::AccessCrossesDeviceWindow,
          "access crossing the device register window must be blocked");

      // 4-byte write at an unaligned IPA.
      Finding unaligned = auditor.audit_mmio_access(
          MmioAccess{1U, make_mmio_esr(4U, true), gicd + 0x02ULL});
      check_true(unaligned.code == FindingCode::AccessUnaligned,
                 "non naturally-aligned MMIO write must be blocked");

      // Read-only and reserved register writes (aligned to their slots).
      Finding ro_write = auditor.audit_mmio_access(
          MmioAccess{1U, make_mmio_esr(4U, true), gicd + 0x04ULL});
      check_true(ro_write.code == FindingCode::AccessReadOnlyWrite,
                 "read-only register write must be blocked");
      Finding reserved_write = auditor.audit_mmio_access(
          MmioAccess{1U, make_mmio_esr(4U, true), gicd + 0x80ULL});
      check_true(reserved_write.code == FindingCode::AccessReservedWrite,
                 "reserved register write must be blocked");

      // IPA that belongs to no declared device at all.
      Finding outside = auditor.audit_mmio_access(
          MmioAccess{1U, make_mmio_esr(4U, true), 0x7777000ULL});
      check_true(outside.code == FindingCode::AccessOutsideAnyDevice,
                 "access outside every device window must be blocked");

      // PCI window end crossing and malformed ESR witnesses.
      Finding pci_cross = auditor.audit_mmio_access(
          MmioAccess{1U, make_mmio_esr(8U, true), pci + 0xFCULL});
      check_true(pci_cross.code ==
                     FindingCode::AccessCrossesDeviceWindow,
                 "PCI config-space window crossing must be blocked");
      Finding bad_ec = auditor.audit_mmio_access(
          MmioAccess{1U, 0ULL, gicd});
      check_true(bad_ec.code == FindingCode::EsrNotDataAbort,
                 "non-data-abort ESR must be rejected");
      Finding bad_syndrome = auditor.audit_mmio_access(
          MmioAccess{1U,
                     static_cast<std::uint64_t>(
                         el2_audit::ESR_EC_DATA_ABORT_LOWER_EL)
                         << el2_audit::ESR_EC_SHIFT,
                     gicd});
      check_true(bad_syndrome.code == FindingCode::EsrSyndromeInvalid,
                 "data abort without valid syndrome must be rejected");
      Finding unknown_vm = auditor.audit_mmio_access(
          MmioAccess{99U, make_mmio_esr(4U, true), gicd});
      check_true(unknown_vm.code == FindingCode::AccessVmUnknown,
                 "unknown VM id must be rejected");

      // A clean baseline sequence produces no access violations.
      std::vector<MmioAccess> clean_sequence = {
          MmioAccess{1U, make_mmio_esr(4U, false), gicd},
          MmioAccess{1U, make_mmio_esr(4U, true), gicd},
          MmioAccess{1U, make_mmio_esr(8U, true), gicd + 0x10ULL},
      };
      check_true(auditor.audit_mmio_sequence(clean_sequence).empty(),
                 "legal MMIO sequence must yield no findings");
      check_absent_code(auditor.audit_mmio_sequence(clean_sequence),
                        FindingCode::AccessUnaligned);
    });

Registrar helper_semantics_test("pure_helpers_and_witness_text", [] {
  check_true(el2_audit::ranges_overlap(0x1000ULL, 0x1000ULL, 0x1800ULL,
                                       0x100ULL),
             "overlapping ranges must be detected");
  check_true(!el2_audit::ranges_overlap(0x1000ULL, 0x800ULL, 0x1800ULL,
                                        0x100ULL),
             "adjacent ranges must not overlap");
  check_true(!el2_audit::ranges_overlap(0x1000ULL, 0x1000ULL, 0x2000ULL,
                                        0x1000ULL),
             "back-to-back ranges must not overlap");

  const std::uint64_t esr = make_mmio_esr(8U, true, 7U);
  el2_audit::DecodedEsr decoded = el2_audit::decode_data_abort_esr(esr);
  check_true(decoded.valid_syndrome && decoded.access_size == 8U &&
                 decoded.is_write && decoded.source_register == 7U,
             "ESR decoder must recover width, direction and SRT");

  Finding sample{FindingCode::VmidCollision, el2_audit::Severity::Error, 3U,
                 "demo", 0x1001000000000000ULL};
  const std::string rendered = el2_audit::to_text(sample);
  check_true(rendered.find("VmidCollision") != std::string::npos &&
                 rendered.find("vm=3") != std::string::npos,
             "rendered finding must carry code and VM id");
});

}  // namespace

int main() {
  std::size_t failures = 0U;
  for (const TestCase& test : registry()) {
    try {
      test.body();
      std::cout << "[ PASS ] " << test.name << '\n';
    } catch (const std::exception& error) {
      ++failures;
      std::cout << "[ FAIL ] " << test.name << ": " << error.what() << '\n';
    }
  }
  std::cout << registry().size() << " test(s), " << failures
            << " failure(s)\n";
  return failures == 0U ? EXIT_SUCCESS : EXIT_FAILURE;
}
