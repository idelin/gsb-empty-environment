#include <cstdio>

#include "ext4_audit/ext4_audit.h"

int main(int argc, char** argv) {
  if (argc != 2) {
    std::fprintf(stderr, "usage: %s <ext4-image>\n",
                 argc > 0 ? argv[0] : "ext4_audit_cli");
    return 2;
  }

  const ext4_audit::AuditReport report =
      ext4_audit::audit_file(argv[1]);
  std::printf("image=%s block_size=%u total_blocks=%llu inodes=%u "
              "groups=%u scanned=%u violations=%zu\n",
              argv[1], report.block_size,
              static_cast<unsigned long long>(report.total_blocks),
              report.total_inodes, report.block_groups,
              report.scanned_inodes, report.violations.size());

  for (const auto& violation : report.violations) {
    std::printf("  [%s] inode=%u path=%s",
                ext4_audit::violation_type_name(violation.type),
                violation.inode,
                violation.path.empty() ? "-" : violation.path.c_str());
    if (!violation.witness_blocks.empty()) {
      std::printf(" blocks=");
      for (uint64_t block : violation.witness_blocks) {
        std::printf("%llu ", static_cast<unsigned long long>(block));
      }
    }
    std::printf(" detail=%s\n", violation.detail.c_str());
  }
  return report.ok() ? 0 : 1;
}
