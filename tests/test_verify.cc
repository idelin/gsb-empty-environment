#include "elf_builder.h"

#include "elfsec/verify.h"

#include <cstdio>
#include <string>
#include <vector>

using elfsec::Machine;
using elfsec::VerifyOptions;
using elfsec::VerifyResult;
using elfsec::ViolationCode;
using elfsec::verify_elf64;
using elfsec::violation_name;
using elfsec_test::BuilderConfig;
using elfsec_test::Elf64Builder;

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& label) {
  ++g_checks;
  if (condition) {
    std::printf("  [PASS] %s\n", label.c_str());
    return;
  }
  ++g_failures;
  std::printf("  [FAIL] %s\n", label.c_str());
}

VerifyOptions strict_options() {
  VerifyOptions options;
  options.ifunc_whitelist = {"trusted_ifunc"};
  return options;
}

void print_violations(const VerifyResult& result) {
  for (const auto& violation : result.violations) {
    std::printf("         -> %s: %s\n", violation_name(violation.code),
                violation.message.c_str());
  }
}

void test_benign_full_relro() {
  std::printf("test_benign_full_relro\n");
  BuilderConfig config;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(result.ok, "synthetic full-RELRO object passes with zero violations");
  check(result.bind_now, "immediate binding flag detected");
  check(result.relro_present, "PT_GNU_RELRO detected");
  check(result.machine == Machine::kX86_64, "machine identified as x86-64");
  check(result.rela_entry_count == 3, "three DT_RELA entries parsed");
  check(result.jmprel_entry_count == 1, "JUMP_SLOT table parsed");
}

void test_partial_relro() {
  std::printf("test_partial_relro\n");
  BuilderConfig config;
  config.bind_now = false;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(!result.ok, "partial RELRO object is rejected");
  check(result.has(ViolationCode::kNoImmediateBinding),
        "missing immediate binding reported");
}

void test_missing_relro() {
  std::printf("test_missing_relro\n");
  BuilderConfig config;
  config.emit_relro = false;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(result.has(ViolationCode::kRelroSegmentMissing),
        "missing PT_GNU_RELRO reported");
  check(result.has(ViolationCode::kRelroGapDynamic),
        "uncovered .dynamic reported without RELRO region");
}

void test_relro_misaligned() {
  std::printf("test_relro_misaligned\n");
  BuilderConfig config;
  config.relro_page_aligned = false;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(result.has(ViolationCode::kRelroNotPageAligned),
        "non-page-aligned PT_GNU_RELRO reported");
}

void test_relocation_outside_relro() {
  std::printf("test_relocation_outside_relro\n");
  BuilderConfig config;
  config.relocation_outside_relro = true;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(result.has(ViolationCode::kRelroGapRelocationTarget),
        "relocation target outside RELRO reported");
}

void test_executable_stack() {
  std::printf("test_executable_stack\n");
  BuilderConfig config;
  config.executable_stack = true;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(result.has(ViolationCode::kExecutableStack),
        "executable PT_GNU_STACK reported");
}

void test_writable_executable_load() {
  std::printf("test_writable_executable_load\n");
  BuilderConfig config;
  config.wx_load = true;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(result.has(ViolationCode::kWritableExecutableSegment),
        "simultaneous PF_W|PF_X PT_LOAD reported");
}

void test_overlapping_load() {
  std::printf("test_overlapping_load\n");
  BuilderConfig config;
  config.overlapping_load = true;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(result.has(ViolationCode::kLoadVirtualPageOverlap),
        "overlapping PT_LOAD virtual ranges reported");
  check(result.first_fatal() != nullptr,
        "overlap is treated as a fatal structural violation");
}

void test_filesz_gt_memsz() {
  std::printf("test_filesz_gt_memsz\n");
  BuilderConfig config;
  config.filesz_gt_memsz = true;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(result.has(ViolationCode::kLoadFileszGreaterMemsz),
        "p_filesz > p_memsz reported");
}

void test_rpath_runpath() {
  std::printf("test_rpath_runpath\n");
  BuilderConfig rpath_config;
  rpath_config.rpath_relative = true;
  VerifyResult rpath_result =
      verify_elf64(Elf64Builder(rpath_config).build(), strict_options());
  print_violations(rpath_result);
  check(rpath_result.has(ViolationCode::kRpathRelativeOrEmpty),
        "relative DT_RPATH entry reported");

  BuilderConfig runpath_config;
  runpath_config.runpath_relative = true;
  VerifyResult runpath_result =
      verify_elf64(Elf64Builder(runpath_config).build(), strict_options());
  print_violations(runpath_result);
  check(runpath_result.has(ViolationCode::kRunpathRelativeOrEmpty),
        "relative DT_RUNPATH entry reported");
}

void test_unbound_version() {
  std::printf("test_unbound_version\n");
  BuilderConfig config;
  config.unbound_version = true;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(result.has(ViolationCode::kSymbolVersionUnbound),
        "symbol version absent from DT_VERNEED reported");
}

void test_irelative_attacks() {
  std::printf("test_irelative_attacks\n");
  BuilderConfig hidden_config;
  hidden_config.hidden_irelative = true;
  VerifyResult hidden =
      verify_elf64(Elf64Builder(hidden_config).build(), strict_options());
  print_violations(hidden);
  check(hidden.has(ViolationCode::kUnwhitelistedIfuncResolver),
        "IRELATIVE resolver outside mapped memory reported");

  BuilderConfig evil_config;
  evil_config.ifunc_unwhitelisted = true;
  VerifyResult evil =
      verify_elf64(Elf64Builder(evil_config).build(), strict_options());
  print_violations(evil);
  check(evil.has(ViolationCode::kUnwhitelistedIfuncResolver),
        "non-whitelisted IFUNC symbol reported");
}

void test_malformed_buffers() {
  std::printf("test_malformed_buffers\n");
  VerifyOptions options = strict_options();
  VerifyResult empty = verify_elf64(std::vector<std::uint8_t>{}, options);
  check(empty.has(ViolationCode::kBufferTooSmall), "empty buffer rejected");

  std::vector<std::uint8_t> truncated(32, 0);
  truncated[0] = 0x7f;
  truncated[1] = 'E';
  truncated[2] = 'L';
  truncated[3] = 'F';
  VerifyResult truncated_result = verify_elf64(truncated, options);
  check(truncated_result.has(ViolationCode::kBufferTooSmall),
        "truncated ELF header rejected");

  std::vector<std::uint8_t> bad_magic(64, 0);
  VerifyResult magic_result = verify_elf64(bad_magic, options);
  check(magic_result.has(ViolationCode::kBadMagic), "bad magic rejected");
}

void test_section_header_spoof_ignored() {
  std::printf("test_section_header_spoof_ignored\n");
  BuilderConfig config;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  // Forge a section header table offset that points outside the buffer. The
  // verifier must ignore section headers entirely and still admit the image.
  auto write_u32 = [](std::vector<std::uint8_t>& buf, std::size_t off,
                      std::uint32_t value) {
    for (int i = 0; i < 4; ++i) {
      buf[off + static_cast<std::size_t>(i)] =
          static_cast<std::uint8_t>((value >> (8 * i)) & 0xff);
    }
  };
  auto write_u64 = [](std::vector<std::uint8_t>& buf, std::size_t off,
                      std::uint64_t value) {
    for (int i = 0; i < 8; ++i) {
      buf[off + static_cast<std::size_t>(i)] =
          static_cast<std::uint8_t>((value >> (8 * i)) & 0xff);
    }
  };
  write_u64(image, 40, 0xffffffffffff0000ULL);  // e_shoff out of buffer
  write_u32(image, 60, 0xffff);                  // e_shnum huge
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(result.ok, "forged section headers do not affect the verdict");
}

void test_program_header_table_oob() {
  std::printf("test_program_header_table_oob\n");
  BuilderConfig config;
  std::vector<std::uint8_t> image = Elf64Builder(config).build();
  auto write_u64 = [](std::vector<std::uint8_t>& buf, std::size_t off,
                      std::uint64_t value) {
    for (int i = 0; i < 8; ++i) {
      buf[off + static_cast<std::size_t>(i)] =
          static_cast<std::uint8_t>((value >> (8 * i)) & 0xff);
    }
  };
  write_u64(image, 32, 0x7000);  // e_phoff beyond the image
  VerifyResult result = verify_elf64(image, strict_options());
  print_violations(result);
  check(result.has(ViolationCode::kProgramHeaderTableOutOfBounds),
        "out-of-bounds program header table reported");
}

}  // namespace

int main() {
  test_benign_full_relro();
  test_partial_relro();
  test_missing_relro();
  test_relro_misaligned();
  test_relocation_outside_relro();
  test_executable_stack();
  test_writable_executable_load();
  test_overlapping_load();
  test_filesz_gt_memsz();
  test_rpath_runpath();
  test_unbound_version();
  test_irelative_attacks();
  test_malformed_buffers();
  test_section_header_spoof_ignored();
  test_program_header_table_oob();

  std::printf("\n%d checks, %d failures\n", g_checks, g_failures);
  return g_failures == 0 ? 0 : 1;
}
