// Emits byte-level fixtures to disk so the CLI host can be demonstrated
// against a benign image and every synthesized attack variant.
#include <cstdio>
#include <fstream>
#include <string>
#include <vector>

#include "elf_builder.h"

namespace {

bool write_file(const std::string& path, const std::vector<std::uint8_t>& bytes) {
  std::ofstream stream(path, std::ios::binary | std::ios::trunc);
  if (!stream) {
    return false;
  }
  stream.write(reinterpret_cast<const char*>(bytes.data()),
               static_cast<std::streamsize>(bytes.size()));
  return static_cast<bool>(stream);
}

}  // namespace

int main(int argc, char** argv) {
  if (argc != 2) {
    std::fprintf(stderr, "usage: %s <output-dir>\n", argv[0]);
    return 2;
  }
  const std::string dir = argv[1];
  struct Case {
    const char* name;
    elfsec_test::BuilderConfig config;
  };
  std::vector<Case> cases;
  cases.push_back(Case{"full_relro.elf", {}});
  {
    elfsec_test::BuilderConfig cfg;
    cfg.bind_now = false;
    cases.push_back(Case{"partial_relro.elf", cfg});
  }
  {
    elfsec_test::BuilderConfig cfg;
    cfg.relro_page_aligned = false;
    cases.push_back(Case{"relro_misaligned.elf", cfg});
  }
  {
    elfsec_test::BuilderConfig cfg;
    cfg.overlapping_load = true;
    cases.push_back(Case{"overlapping_load.elf", cfg});
  }
  {
    elfsec_test::BuilderConfig cfg;
    cfg.hidden_irelative = true;
    cases.push_back(Case{"hidden_irelative.elf", cfg});
  }
  for (const Case& test_case : cases) {
    const std::string path = dir + "/" + test_case.name;
    if (!write_file(path, elfsec_test::Elf64Builder(test_case.config).build())) {
      std::fprintf(stderr, "failed writing %s\n", path.c_str());
      return 1;
    }
    std::printf("wrote %s\n", path.c_str());
  }
  return 0;
}
