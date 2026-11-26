// Minimal audit-host entry point: reads a file into memory and prints every
// verifier finding. Exits non-zero when the image is not admissible.
#include <cstdio>
#include <fstream>
#include <iostream>
#include <string>
#include <vector>

#include "elfsec/verify.h"

namespace {

std::vector<std::uint8_t> read_file(const std::string& path) {
  std::ifstream stream(path, std::ios::binary);
  if (!stream) {
    return {};
  }
  stream.seekg(0, std::ios::end);
  const std::streamoff length = stream.tellg();
  if (length <= 0) {
    return {};
  }
  stream.seekg(0, std::ios::beg);
  std::vector<std::uint8_t> bytes(static_cast<std::size_t>(length));
  stream.read(reinterpret_cast<char*>(bytes.data()),
              static_cast<std::streamsize>(bytes.size()));
  if (!stream) {
    return {};
  }
  return bytes;
}

}  // namespace

int main(int argc, char** argv) {
  if (argc != 2) {
    std::fprintf(stderr, "usage: %s <elf64-file>\n",
                 argc > 0 ? argv[0] : "elfsec");
    return 2;
  }
  std::vector<std::uint8_t> bytes = read_file(argv[1]);
  if (bytes.empty()) {
    std::fprintf(stderr, "error: cannot read non-empty file: %s\n", argv[1]);
    return 2;
  }
  elfsec::VerifyOptions options;
  options.ifunc_whitelist = {"trusted_ifunc"};
  elfsec::VerifyResult result = elfsec::verify_elf64(bytes, options);

  std::printf("machine: %d\n", static_cast<int>(result.machine));
  std::printf("shared_object: %s\n", result.is_shared_object ? "yes" : "no");
  std::printf("bind_now: %s\n", result.bind_now ? "yes" : "no");
  std::printf("relro: %s\n", result.relro_present ? "yes" : "no");
  std::printf("loads: %zu rela: %zu jmprel: %zu\n",
              result.load_segment_count, result.rela_entry_count,
              result.jmprel_entry_count);
  if (result.violations.empty()) {
    std::printf("verdict: ADMIT (no violations)\n");
    return 0;
  }
  for (const elfsec::Violation& violation : result.violations) {
    std::printf("violation: %s: %s\n",
                elfsec::violation_name(violation.code),
                violation.message.c_str());
  }
  std::printf("verdict: REJECT (%zu violation(s))\n",
              result.violations.size());
  return 1;
}
