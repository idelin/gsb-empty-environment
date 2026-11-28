#include <cstdio>
#include <string>
#include <vector>

#include "ext4_audit/ext4_audit.h"
#include "image_builder.h"

namespace {

using ext4_audit::AuditConfig;
using ext4_audit::AuditReport;
using ext4_audit::ViolationType;
using ext4_audit_test::ImageBuilder;

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& name) {
  ++g_checks;
  if (condition) {
    std::printf("  [ok] %s\n", name.c_str());
  } else {
    ++g_failures;
    std::printf("  [FAIL] %s\n", name.c_str());
  }
}

bool contains(const AuditReport& report, ViolationType type,
              uint32_t inode = 0) {
  for (const auto& violation : report.violations) {
    if (violation.type == type &&
        (inode == 0 || violation.inode == inode)) {
      return true;
    }
  }
  return false;
}

AuditReport audit(ImageBuilder& builder,
                  const AuditConfig& config = AuditConfig{}) {
  return ext4_audit::audit_image(builder.data(), builder.size(), config);
}

void test_clean_image() {
  std::printf("test_clean_image\n");
  ImageBuilder builder;
  const AuditReport report = audit(builder);
  check(report.ok(), "hand-built image produces zero violations");
  check(report.block_size == 1024, "block size parsed as 1024");
  check(report.total_blocks == ImageBuilder::kTotalBlocks,
        "total block count matches image");
  check(report.total_inodes == ImageBuilder::kTotalInodes,
        "total inode count parsed");
  check(report.scanned_inodes >= 7, "allocated inodes are scanned");
}

void test_bad_magic() {
  std::printf("test_bad_magic\n");
  ImageBuilder builder;
  builder.corrupt_magic();
  const AuditReport report = audit(builder);
  check(!report.ok() && contains(report, ViolationType::BadMagic),
        "corrupt super block magic rejected");
}

void test_truncated_image() {
  std::printf("test_truncated_image\n");
  ImageBuilder builder;
  builder.truncate(2048);
  const AuditReport report = audit(builder);
  check(contains(report, ViolationType::ImageTooSmall) ||
            contains(report, ViolationType::TruncatedImage),
        "truncated image rejected before out-of-bounds reads");
}

void test_unsupported_block_size() {
  std::printf("test_unsupported_block_size\n");
  ImageBuilder builder;
  builder.set_log_block_size(7);
  const AuditReport report = audit(builder);
  check(contains(report, ViolationType::UnsupportedBlockSize),
        "invalid log block size rejected");
}

void test_block_count_mismatch() {
  std::printf("test_block_count_mismatch\n");
  ImageBuilder builder;
  builder.set_total_blocks(ImageBuilder::kTotalBlocks + 32);
  const AuditReport report = audit(builder);
  check(contains(report, ViolationType::TruncatedImage),
        "declared block count beyond image length rejected");
}

void test_extent_loop() {
  std::printf("test_extent_loop\n");
  ImageBuilder builder;
  builder.corrupt_extent_loop();
  const AuditReport report = audit(builder);
  check(contains(report, ViolationType::ExtentLoop) ||
            contains(report, ViolationType::ExtentBadDepth),
        "self-referencing extent tree caught");
  check(contains(report, ViolationType::ExtentLoop, 7) ||
            contains(report, ViolationType::ExtentBadDepth, 7),
        "loop violation attributed to inode 7");
}

void test_cross_link() {
  std::printf("test_cross_link\n");
  ImageBuilder builder;
  builder.corrupt_cross_link();
  const AuditReport report = audit(builder);
  check(contains(report, ViolationType::CrossLinkedBlock, 8),
        "cross-linked data block detected");
  bool has_witness = false;
  for (const auto& violation : report.violations) {
    if (violation.type == ViolationType::CrossLinkedBlock &&
        !violation.witness_blocks.empty() &&
        violation.witness_blocks[0] == ImageBuilder::kFileABlock) {
      has_witness = true;
    }
  }
  check(has_witness, "cross-link reports shared physical block witness");
}

void test_hidden_block_device() {
  std::printf("test_hidden_block_device\n");
  ImageBuilder builder;
  builder.install_hidden_block_device();
  const AuditReport report = audit(builder);
  check(contains(report, ViolationType::UnsafeBlockDevice, 10),
        "hidden S_IFBLK device node rejected");

  AuditConfig allowed;
  allowed.device_major_minor_whitelist.insert(
      AuditConfig::make_device_key(8, 0));
  const AuditReport allowed_report = audit(builder, allowed);
  check(!contains(allowed_report, ViolationType::UnsafeBlockDevice),
        "whitelisted device major/minor is accepted");
}

void test_suid_file() {
  std::printf("test_suid_file\n");
  ImageBuilder builder;
  builder.install_suid_file();
  const AuditReport report = audit(builder);
  check(contains(report, ViolationType::UnsafeSetuid, 11),
        "non-whitelisted SUID executable rejected");

  AuditConfig allowed;
  allowed.suid_inode_whitelist.insert(11);
  const AuditReport allowed_report = audit(builder, allowed);
  check(!contains(allowed_report, ViolationType::UnsafeSetuid),
        "SUID whitelist suppresses the violation");
}

void test_out_of_range_extent() {
  std::printf("test_out_of_range_extent\n");
  ImageBuilder builder;
  builder.corrupt_physical_range();
  const AuditReport report = audit(builder);
  check(contains(report, ViolationType::ExtentPhysicalOutOfRange, 7),
        "physical extent beyond file system rejected");
}

void test_logical_overlap() {
  std::printf("test_logical_overlap\n");
  ImageBuilder builder;
  builder.corrupt_logical_overlap();
  const AuditReport report = audit(builder);
  check(contains(report, ViolationType::ExtentLogicOverlap, 7),
        "non-monotonic logical extents rejected");
}

void test_directory_entry_overrun() {
  std::printf("test_directory_entry_overrun\n");
  ImageBuilder builder;
  builder.corrupt_directory_entry_length();
  const AuditReport report = audit(builder);
  check(contains(report, ViolationType::DirectoryEntryOutOfRange, 2),
        "directory entry crossing block boundary rejected");
}

void test_sgid_file() {
  std::printf("test_sgid_file\n");
  ImageBuilder builder;
  builder.install_sgid_file();
  const AuditReport report = audit(builder);
  check(contains(report, ViolationType::UnsafeSetgid, 12),
        "non-whitelisted SGID executable rejected");

  AuditConfig allowed;
  allowed.sgid_inode_whitelist.insert(12);
  const AuditReport allowed_report = audit(builder, allowed);
  check(!contains(allowed_report, ViolationType::UnsafeSetgid),
        "SGID whitelist suppresses the violation");
}

void test_64bit_group_descriptor() {
  std::printf("test_64bit_group_descriptor\n");
  ImageBuilder builder;
  builder.enable_64bit();
  const AuditReport report = audit(builder);
  check(report.ok(),
        "64bit feature with wide descriptors parses cleanly");
}

}  // namespace

int main() {
  test_clean_image();
  test_bad_magic();
  test_truncated_image();
  test_unsupported_block_size();
  test_block_count_mismatch();
  test_extent_loop();
  test_cross_link();
  test_hidden_block_device();
  test_suid_file();
  test_out_of_range_extent();
  test_logical_overlap();
  test_directory_entry_overrun();
  test_sgid_file();
  test_64bit_group_descriptor();

  std::printf("\n%d checks, %d failures\n", g_checks, g_failures);
  return g_failures == 0 ? 0 : 1;
}
