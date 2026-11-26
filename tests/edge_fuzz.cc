#include "elfsec/verify.h"

#include <cstdio>
#include <vector>

int main() {
  std::vector<std::uint8_t> junk(5000);
  for (std::size_t i = 0; i < junk.size(); ++i) {
    junk[i] = static_cast<std::uint8_t>((i * 73 + 11) & 0xff);
  }
  elfsec::VerifyResult random_result = elfsec::verify_elf64(junk);
  std::printf("junk ok=%d violations=%zu first=%s\n",
              static_cast<int>(random_result.ok),
              random_result.violations.size(),
              random_result.violations.empty()
                  ? "-"
                  : elfsec::violation_name(
                        random_result.violations.front().code));

  std::vector<std::uint8_t> base(64, 0);
  base[0] = 0x7f;
  base[1] = 'E';
  base[2] = 'L';
  base[3] = 'F';
  base[4] = 2;
  base[5] = 1;
  base[6] = 1;
  for (std::size_t n = 0; n <= 64; ++n) {
    elfsec::verify_elf64(base.data(), n);
  }
  std::printf("truncation sweep complete\n");
  return 0;
}
