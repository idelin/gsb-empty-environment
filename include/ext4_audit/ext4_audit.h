#ifndef EXT4_AUDIT_EXT4_AUDIT_H
#define EXT4_AUDIT_EXT4_AUDIT_H

#include <cstddef>
#include <cstdint>
#include <string>
#include <unordered_set>
#include <vector>

namespace ext4_audit {

// Kinds of structural or policy violations that the offline auditor detects.
enum class ViolationType {
  ImageTooSmall,
  BadMagic,
  UnsupportedBlockSize,
  BlockCountMismatch,
  TruncatedImage,
  InvalidGroupDescriptor,
  InvalidInodeTable,
  InvalidInode,
  RootInodeInvalid,
  LegacyMappingUnsupported,
  ExtentBadMagic,
  ExtentBadDepth,
  ExtentLoop,
  ExtentLogicOverlap,
  ExtentPhysicalOutOfRange,
  ExtentDuplicatePhysicalBlock,
  DirectoryEntryOutOfRange,
  CrossLinkedBlock,
  UnsafeSetuid,
  UnsafeSetgid,
  UnsafeBlockDevice,
  UnsafeCharDevice
};

// Human readable name for a violation type.
const char* violation_type_name(ViolationType type);

// A single audit finding. Inode numbers are 1-based, matching ext4 semantics.
struct Violation {
  ViolationType type;
  uint32_t inode = 0;
  std::string path;
  std::string detail;
  // Physical block witnesses (for block-level findings such as cross-links).
  std::vector<uint64_t> witness_blocks;
};

// Policy knobs for the admission audit. The default configuration rejects all
// SUID/SGID executables and all block/character device nodes.
struct AuditConfig {
  // Inode numbers whose SUID bit is allowed.
  std::unordered_set<uint32_t> suid_inode_whitelist;
  // Inode numbers whose SGID bit is allowed.
  std::unordered_set<uint32_t> sgid_inode_whitelist;
  // Allowed device nodes as (major, minor) pairs. Empty means deny all.
  std::unordered_set<uint64_t> device_major_minor_whitelist;

  static uint64_t make_device_key(uint32_t major, uint32_t minor) {
    return (static_cast<uint64_t>(major) << 32) |
           static_cast<uint64_t>(minor);
  }
};

struct AuditReport {
  std::vector<Violation> violations;
  uint64_t image_size = 0;
  uint32_t block_size = 0;
  uint64_t total_blocks = 0;
  uint32_t total_inodes = 0;
  uint32_t block_groups = 0;
  uint32_t scanned_inodes = 0;

  bool ok() const { return violations.empty(); }
};

// Audits an ext4 image already resident in memory. The buffer must remain
// valid for the duration of the call.
AuditReport audit_image(const uint8_t* data, std::size_t size,
                        const AuditConfig& config = AuditConfig{});

// Reads a file from disk and audits it. A read failure is reported through a
// single ImageTooSmall/IO failure violation rather than exceptions.
AuditReport audit_file(const std::string& path,
                       const AuditConfig& config = AuditConfig{});

namespace detail {

#if defined(__GNUC__) || defined(__clang__)
#define EXT4_AUDIT_PACKED __attribute__((packed))
#else
#define EXT4_AUDIT_PACKED
#endif

// Portable little-endian accessors. All disk access goes through these so the
// auditor works on any host endianness without unaligned loads.
inline uint16_t rd16(const uint8_t* p) {
  return static_cast<uint16_t>(static_cast<uint16_t>(p[0]) |
                               (static_cast<uint16_t>(p[1]) << 8));
}

inline uint32_t rd32(const uint8_t* p) {
  return static_cast<uint32_t>(p[0]) |
         (static_cast<uint32_t>(p[1]) << 8) |
         (static_cast<uint32_t>(p[2]) << 16) |
         (static_cast<uint32_t>(p[3]) << 24);
}

inline uint64_t rd64(const uint8_t* p) {
  return static_cast<uint64_t>(rd32(p)) |
         (static_cast<uint64_t>(rd32(p + 4)) << 32);
}

inline void wr16(uint8_t* p, uint16_t v) {
  p[0] = static_cast<uint8_t>(v & 0xffu);
  p[1] = static_cast<uint8_t>((v >> 8) & 0xffu);
}

inline void wr32(uint8_t* p, uint32_t v) {
  p[0] = static_cast<uint8_t>(v & 0xffu);
  p[1] = static_cast<uint8_t>((v >> 8) & 0xffu);
  p[2] = static_cast<uint8_t>((v >> 16) & 0xffu);
  p[3] = static_cast<uint8_t>((v >> 24) & 0xffu);
}

inline void wr64(uint8_t* p, uint64_t v) {
  wr32(p, static_cast<uint32_t>(v & 0xffffffffULL));
  wr32(p + 4, static_cast<uint32_t>(v >> 32));
}

// On-disk ext4 super block (1024 bytes, stored at byte offset 1024).
struct Ext4SuperBlock {
  uint32_t s_inodes_count;
  uint32_t s_blocks_count_lo;
  uint32_t s_r_blocks_count_lo;
  uint32_t s_free_blocks_count_lo;
  uint32_t s_free_inodes_count;
  uint32_t s_first_data_block;
  uint32_t s_log_block_size;
  uint32_t s_log_cluster_size;
  uint32_t s_blocks_per_group;
  uint32_t s_clusters_per_group;
  uint32_t s_inodes_per_group;
  uint32_t s_mtime;
  uint32_t s_wtime;
  uint16_t s_mnt_count;
  uint16_t s_max_mnt_count;
  uint16_t s_magic;
  uint16_t s_state;
  uint16_t s_errors;
  uint16_t s_minor_rev_level;
  uint32_t s_lastcheck;
  uint32_t s_checkinterval;
  uint32_t s_creator_os;
  uint32_t s_rev_level;
  uint16_t s_def_resuid;
  uint16_t s_def_resgid;
  uint32_t s_first_ino;
  uint16_t s_inode_size;
  uint16_t s_block_group_nr;
  uint32_t s_feature_compat;
  uint32_t s_feature_incompat;
  uint32_t s_feature_ro_compat;
  uint8_t s_uuid[16];
  uint8_t s_volume_name[16];
  uint8_t s_last_mounted[64];
  uint32_t s_algorithm_usage_bitmap;
  uint8_t s_prealloc_blocks;
  uint8_t s_prealloc_dir_blocks;
  uint16_t s_reserved_gdt_blocks;
  uint8_t s_journal_uuid[16];
  uint32_t s_journal_inum;
  uint32_t s_journal_dev;
  uint32_t s_last_orphan;
  uint32_t s_hash_seed[4];
  uint8_t s_def_hash_version;
  uint8_t s_jnl_backup_type;
  uint16_t s_desc_size;
  uint32_t s_default_mount_opts;
  uint32_t s_first_meta_bg;
  uint32_t s_mkfs_time;
  uint32_t s_jnl_blocks[17];
  uint32_t s_blocks_count_hi;
  uint32_t s_r_blocks_count_hi;
  uint32_t s_free_blocks_count_hi;
  uint16_t s_min_extra_isize;
  uint16_t s_want_extra_isize;
  uint32_t s_flags;
  uint16_t s_raid_stride;
  uint16_t s_mmp_update_interval;
  uint64_t s_mmp_block;
  uint32_t s_raid_stripe_width;
  uint8_t s_log_groups_per_flex;
  uint8_t s_checksum_type;
  uint16_t s_reserved_pad;
  uint64_t s_kbytes_written;
  uint8_t s_unknown[636];
  uint32_t s_checksum;
} EXT4_AUDIT_PACKED;

// On-disk block group descriptor (64 bytes when the 64bit feature is set).
struct Ext4GroupDesc {
  uint32_t bg_block_bitmap_lo;
  uint32_t bg_inode_bitmap_lo;
  uint32_t bg_inode_table_lo;
  uint16_t bg_free_blocks_count_lo;
  uint16_t bg_free_inodes_count_lo;
  uint16_t bg_used_dirs_count_lo;
  uint16_t bg_flags;
  uint32_t bg_exclude_bitmap_lo;
  uint16_t bg_block_bitmap_csum_lo;
  uint16_t bg_inode_bitmap_csum_lo;
  uint16_t bg_itable_unused_lo;
  uint16_t bg_checksum;
  uint32_t bg_block_bitmap_hi;
  uint32_t bg_inode_bitmap_hi;
  uint32_t bg_inode_table_hi;
  uint16_t bg_free_blocks_count_hi;
  uint16_t bg_free_inodes_count_hi;
  uint16_t bg_used_dirs_count_hi;
  uint16_t bg_itable_unused_hi;
  uint32_t bg_exclude_bitmap_hi;
  uint16_t bg_block_bitmap_csum_hi;
  uint16_t bg_inode_bitmap_csum_hi;
  uint32_t bg_reserved;
} EXT4_AUDIT_PACKED;

// On-disk inode (128 byte base layout).
struct Ext4Inode {
  uint16_t i_mode;
  uint16_t i_uid;
  uint32_t i_size_lo;
  uint32_t i_atime;
  uint32_t i_ctime;
  uint32_t i_mtime;
  uint32_t i_dtime;
  uint16_t i_gid;
  uint16_t i_links_count;
  uint32_t i_blocks_lo;
  uint32_t i_flags;
  uint32_t i_osd1;
  uint8_t i_block[60];
  uint32_t i_generation;
  uint32_t i_file_acl_lo;
  uint32_t i_size_high;
  uint32_t i_obso_faddr;
  uint16_t i_osd2[2];
  uint16_t i_extra_isize;
  uint16_t i_checksum_hi;
  uint32_t i_ctime_extra;
} EXT4_AUDIT_PACKED;

struct Ext4ExtentHeader {
  uint16_t eh_magic;
  uint16_t eh_entries;
  uint16_t eh_max;
  uint16_t eh_depth;
  uint32_t eh_generation;
} EXT4_AUDIT_PACKED;

struct Ext4ExtentIndex {
  uint32_t ei_block;
  uint32_t ei_leaf_lo;
  uint16_t ei_leaf_hi;
  uint16_t ei_unused;
} EXT4_AUDIT_PACKED;

struct Ext4Extent {
  uint32_t ee_block;
  uint16_t ee_len;
  uint16_t ee_start_hi;
  uint32_t ee_start_lo;
} EXT4_AUDIT_PACKED;

static_assert(sizeof(Ext4SuperBlock) == 1024, "super block layout");
static_assert(sizeof(Ext4GroupDesc) == 64, "group descriptor layout");
static_assert(sizeof(Ext4Inode) == 128, "inode layout");
static_assert(sizeof(Ext4ExtentHeader) == 12, "extent header layout");
static_assert(sizeof(Ext4ExtentIndex) == 12, "extent index layout");
static_assert(sizeof(Ext4Extent) == 12, "extent layout");

}  // namespace detail
}  // namespace ext4_audit

#endif  // EXT4_AUDIT_EXT4_AUDIT_H
