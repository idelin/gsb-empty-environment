#ifndef EXT4_AUDIT_TEST_IMAGE_BUILDER_H
#define EXT4_AUDIT_TEST_IMAGE_BUILDER_H

#include <cassert>
#include <cstddef>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

#include "ext4_audit/ext4_audit.h"

namespace ext4_audit_test {

using ext4_audit::detail::Ext4Extent;
using ext4_audit::detail::Ext4ExtentIndex;
using ext4_audit::detail::Ext4ExtentHeader;
using ext4_audit::detail::Ext4GroupDesc;
using ext4_audit::detail::Ext4Inode;
using ext4_audit::detail::Ext4SuperBlock;
using ext4_audit::detail::wr16;
using ext4_audit::detail::wr32;

// Hand-built minimal ext4 file system used by the tests. Everything is emitted
// byte-by-byte so the helper itself needs no kernel, mount privileges or any
// third-party library.
class ImageBuilder {
 public:
  static constexpr uint32_t kBlockSize = 1024;
  static constexpr uint32_t kTotalBlocks = 64;
  static constexpr uint32_t kTotalInodes = 16;
  static constexpr uint32_t kInodesPerGroup = 16;
  static constexpr uint32_t kBlocksPerGroup = 64;
  static constexpr uint32_t kInodeSize = 128;

  // Well-known block numbers in the fixed tiny layout.
  static constexpr uint32_t kBlockBitmapBlock = 3;
  static constexpr uint32_t kInodeBitmapBlock = 4;
  static constexpr uint32_t kInodeTableBlock = 5;
  static constexpr uint32_t kRootDirBlock = 7;
  static constexpr uint32_t kLostFoundBlock = 8;
  static constexpr uint32_t kFileABlock = 9;
  static constexpr uint32_t kFileBBlock = 10;
  static constexpr uint32_t kDirBlock = 11;
  static constexpr uint32_t kDirDataBlock = 12;
  static constexpr uint32_t kFileCBlock = 13;
  static constexpr uint32_t kFileDBlock = 14;

  ImageBuilder() : data_(kTotalBlocks * kBlockSize, 0) { build(); }

  std::vector<uint8_t> copy() const { return data_; }
  uint8_t* data() { return data_.data(); }
  std::size_t size() const { return data_.size(); }

  std::size_t super_field(std::size_t offset) const {
    return 1024 + offset;
  }

  std::size_t inode_offset(uint32_t inode_no) const {
    assert(inode_no >= 1 && inode_no <= kTotalInodes);
    return static_cast<std::size_t>(kInodeTableBlock) * kBlockSize +
           static_cast<std::size_t>(inode_no - 1) * kInodeSize;
  }

  Ext4Inode* inode(uint32_t inode_no) {
    return reinterpret_cast<Ext4Inode*>(data_.data() +
                                        inode_offset(inode_no));
  }

  Ext4SuperBlock* super() {
    return reinterpret_cast<Ext4SuperBlock*>(data_.data() + 1024);
  }

  Ext4GroupDesc* group_desc() {
    // With 1K blocks s_first_data_block is 1, so the GDT starts in block 2.
    return reinterpret_cast<Ext4GroupDesc*>(data_.data() + 2 * kBlockSize);
  }

  uint8_t* block(uint32_t block_no) {
    return data_.data() +
           static_cast<std::size_t>(block_no) * kBlockSize;
  }

  // Corruption mutators used by individual tests.
  void corrupt_magic() { super()->s_magic = 0x1234; }

  void truncate(std::size_t new_size) { data_.resize(new_size); }

  void set_log_block_size(uint32_t value) {
    super()->s_log_block_size = value;
  }

  void set_total_blocks(uint32_t value) {
    super()->s_blocks_count_lo = value;
  }

  // Makes inode 7's only extent point past the end of the file system.
  void corrupt_physical_range() {
    Ext4Inode* node = inode(7);
    Ext4Extent* extent =
        reinterpret_cast<Ext4Extent*>(node->i_block +
                                      sizeof(Ext4ExtentHeader));
    extent->ee_start_lo = kTotalBlocks + 100;
  }

  // Forces two leaf extents with decreasing logical blocks.
  void corrupt_logical_overlap() {
    Ext4Inode* node = inode(7);
    Ext4ExtentHeader* header =
        reinterpret_cast<Ext4ExtentHeader*>(node->i_block);
    header->eh_entries = 2;
    Ext4Extent* first =
        reinterpret_cast<Ext4Extent*>(node->i_block +
                                      sizeof(Ext4ExtentHeader));
    Ext4Extent* second = first + 1;
    *first = make_extent(10, kFileABlock, 1);
    *second = make_extent(5, kFileABlock + 1, 1);
  }

  // Builds a depth-1 index that points at itself, producing a loop.
  void corrupt_extent_loop() {
    Ext4Inode* node = inode(7);
    Ext4ExtentHeader* root =
        reinterpret_cast<Ext4ExtentHeader*>(node->i_block);
    root->eh_depth = 1;
    root->eh_entries = 1;
    Ext4ExtentIndex* index =
        reinterpret_cast<Ext4ExtentIndex*>(node->i_block +
                                           sizeof(Ext4ExtentHeader));
    index->ei_block = 0;
    index->ei_leaf_lo = kFileABlock;
    index->ei_leaf_hi = 0;

    uint8_t* child = block(kFileABlock);
    Ext4ExtentHeader* child_header =
        reinterpret_cast<Ext4ExtentHeader*>(child);
    child_header->eh_magic = 0xF30A;
    child_header->eh_entries = 1;
    child_header->eh_max = 4;
    child_header->eh_depth = 1;  // Should be 0, proving depth enforcement.
    Ext4ExtentIndex* child_index =
        reinterpret_cast<Ext4ExtentIndex*>(
            child + sizeof(Ext4ExtentHeader));
    child_index->ei_block = 0;
    child_index->ei_leaf_lo = kFileABlock;  // Self reference.
    child_index->ei_leaf_hi = 0;
  }

  // Points file B at file A's data block to create a cross-link.
  void corrupt_cross_link() {
    Ext4Inode* node = inode(8);
    Ext4Extent* extent =
        reinterpret_cast<Ext4Extent*>(node->i_block +
                                      sizeof(Ext4ExtentHeader));
    extent->ee_start_lo = kFileABlock;
  }

  // Turns inode 10 into a hidden block device with major 8 (host disks).
  void install_hidden_block_device() {
    Ext4Inode* node = inode(10);
    std::memset(node, 0, sizeof(Ext4Inode));
    node->i_mode = 0x61B6;  // S_IFBLK | 0666.
    node->i_links_count = 1;
    node->i_flags = 0;
    wr32(node->i_block, 0x00000800);  // Old format: major 8, minor 0.
    add_dir_entry(kRootDirBlock, "sda", 10, 0x6000);
  }

  void install_suid_file() {
    Ext4Inode* node = inode(11);
    node->i_mode = 0x89ED;  // S_IFREG | SUID | 0755.
    node->i_links_count = 1;
    node->i_flags = 0x80000;
    node->i_size_lo = kBlockSize;
    Ext4ExtentHeader* header =
        reinterpret_cast<Ext4ExtentHeader*>(node->i_block);
    *header = make_extent_header(1, 0);
    Ext4Extent* extent =
        reinterpret_cast<Ext4Extent*>(node->i_block +
                                      sizeof(Ext4ExtentHeader));
    *extent = make_extent(0, kFileCBlock, 1);
    add_dir_entry(kRootDirBlock, "suided", 11, 0x8000);
  }

  void install_sgid_file() {
    Ext4Inode* node = inode(12);
    node->i_mode = 0x8C94;  // S_IFREG | S_ISGID | 0754.
    node->i_links_count = 1;
    node->i_flags = 0x80000;
    node->i_size_lo = kBlockSize;
    Ext4ExtentHeader* header =
        reinterpret_cast<Ext4ExtentHeader*>(node->i_block);
    *header = make_extent_header(1, 0);
    Ext4Extent* extent =
        reinterpret_cast<Ext4Extent*>(node->i_block +
                                      sizeof(Ext4ExtentHeader));
    *extent = make_extent(0, kFileDBlock, 1);
    add_dir_entry(kRootDirBlock, "sgided", 12, 0x8000);
  }

  // Switches the file system to the 64bit feature and widens the GDT. The
  // tiny image keeps all metadata below block 2^32, so only descriptor width
  // changes.
  void enable_64bit() {
    super()->s_feature_incompat = 0x40 | 0x80;
    super()->s_desc_size = sizeof(Ext4GroupDesc);
    Ext4GroupDesc* gd = group_desc();
    gd->bg_inode_table_hi = 0;
    gd->bg_block_bitmap_hi = 0;
    gd->bg_inode_bitmap_hi = 0;
  }

  // Rewrites the last directory record so it overruns its block.
  void corrupt_directory_entry_length() {
    uint8_t* dir = block(kRootDirBlock);
    std::size_t offset = 0;
    while (offset < kBlockSize) {
      const uint16_t rec_len =
          ext4_audit::detail::rd16(dir + offset + 4);
      if (rec_len == 0 || offset + rec_len >= kBlockSize) {
        break;
      }
      offset += rec_len;
    }
    wr16(dir + offset + 4, static_cast<uint16_t>(kBlockSize + 8));
  }

 private:
  std::vector<uint8_t> data_;

  static Ext4ExtentHeader make_extent_header(uint16_t entries,
                                             uint16_t depth) {
    Ext4ExtentHeader header;
    header.eh_magic = 0xF30A;
    header.eh_entries = entries;
    header.eh_max = 4;
    header.eh_depth = depth;
    header.eh_generation = 0;
    return header;
  }

  static Ext4Extent make_extent(uint32_t logical, uint32_t physical,
                                uint16_t length) {
    Ext4Extent extent;
    extent.ee_block = logical;
    extent.ee_len = length;
    extent.ee_start_hi = 0;
    extent.ee_start_lo = physical;
    return extent;
  }

  void build() {
    write_super_block();
    write_group_descriptor();
    write_bitmaps();
    write_inodes();
    write_directories();
  }

  void write_super_block() {
    Ext4SuperBlock* sb = super();
    sb->s_inodes_count = kTotalInodes;
    sb->s_blocks_count_lo = kTotalBlocks;
    sb->s_free_blocks_count_lo = kTotalBlocks - 15;
    sb->s_free_inodes_count = 4;  // Inodes 1-12 are allocated.
    sb->s_first_data_block = 1;
    sb->s_log_block_size = 0;  // 1024 byte blocks.
    sb->s_log_cluster_size = 0;
    sb->s_blocks_per_group = kBlocksPerGroup;
    sb->s_clusters_per_group = kBlocksPerGroup;
    sb->s_inodes_per_group = kInodesPerGroup;
    sb->s_magic = 0xEF53;
    sb->s_state = 1;
    sb->s_max_mnt_count = 0xFFFF;
    sb->s_rev_level = 1;
    sb->s_first_ino = 11;
    sb->s_inode_size = static_cast<uint16_t>(kInodeSize);
    sb->s_feature_incompat = 0x40;  // extents, no 64bit.
    sb->s_desc_size = 0;
  }

  void write_group_descriptor() {
    Ext4GroupDesc* gd = group_desc();
    gd->bg_block_bitmap_lo = kBlockBitmapBlock;
    gd->bg_inode_bitmap_lo = kInodeBitmapBlock;
    gd->bg_inode_table_lo = kInodeTableBlock;
    gd->bg_free_blocks_count_lo = kTotalBlocks - 15;
    gd->bg_free_inodes_count_lo = 4;
    gd->bg_used_dirs_count_lo = 3;
  }

  void write_bitmaps() {
    uint8_t* block_bitmap = block(kBlockBitmapBlock);
    uint8_t* inode_bitmap = block(kInodeBitmapBlock);
    for (uint32_t used = 1; used <= 14; ++used) {
      const uint32_t bit = used - 1;
      block_bitmap[bit / 8] = static_cast<uint8_t>(
          block_bitmap[bit / 8] | (1u << (bit % 8)));
      inode_bitmap[bit / 8] = static_cast<uint8_t>(
          inode_bitmap[bit / 8] | (1u << (bit % 8)));
    }
  }

  void write_inodes() {
    write_directory_inode(2, 3, kRootDirBlock);
    write_directory_inode(3, 2, kLostFoundBlock);
    write_file_inode(7, kFileABlock);
    write_file_inode(8, kFileBBlock);
    write_directory_inode(9, 2, kDirDataBlock);
    write_file_inode(11, kFileCBlock);
    write_file_inode(12, kFileDBlock);

    // A zero-length file still carries a valid empty extent root.
    Ext4Inode* empty = inode(6);
    empty->i_mode = 0x81A4;
    empty->i_links_count = 1;
    empty->i_flags = 0x80000;
    Ext4ExtentHeader* header =
        reinterpret_cast<Ext4ExtentHeader*>(empty->i_block);
    *header = make_extent_header(0, 0);
  }

  void write_directory_inode(uint32_t inode_no, uint16_t links,
                             uint32_t data_block) {
    Ext4Inode* node = inode(inode_no);
    node->i_mode = 0x41ED;  // S_IFDIR | 0755.
    node->i_links_count = links;
    node->i_flags = 0x80000;
    node->i_size_lo = kBlockSize;
    node->i_blocks_lo = 2;
    Ext4ExtentHeader* header =
        reinterpret_cast<Ext4ExtentHeader*>(node->i_block);
    *header = make_extent_header(1, 0);
    Ext4Extent* extent =
        reinterpret_cast<Ext4Extent*>(node->i_block +
                                      sizeof(Ext4ExtentHeader));
    *extent = make_extent(0, data_block, 1);
  }

  void write_file_inode(uint32_t inode_no, uint32_t data_block) {
    Ext4Inode* node = inode(inode_no);
    node->i_mode = 0x81A4;  // S_IFREG | 0644.
    node->i_links_count = 1;
    node->i_flags = 0x80000;
    node->i_size_lo = kBlockSize;
    node->i_blocks_lo = 2;
    Ext4ExtentHeader* header =
        reinterpret_cast<Ext4ExtentHeader*>(node->i_block);
    *header = make_extent_header(1, 0);
    Ext4Extent* extent =
        reinterpret_cast<Ext4Extent*>(node->i_block +
                                      sizeof(Ext4ExtentHeader));
    *extent = make_extent(0, data_block, 1);
  }

  void write_directories() {
    write_root_directory();
    write_lost_found();
    write_nested_directory();
  }

  void write_root_directory() {
    uint8_t* dir = block(kRootDirBlock);
    std::size_t offset = 0;
    offset = add_dir_entry_at(dir, offset, 2, ".", 0x4000, 12);
    offset = add_dir_entry_at(dir, offset, 2, "..", 0x4000, 12);
    offset = add_dir_entry_at(dir, offset, 3, "lost+found", 0x4000, 20);
    offset = add_dir_entry_at(dir, offset, 9, "subdir", 0x4000, 16);
    offset = add_dir_entry_at(dir, offset, 7, "file_a", 0x8000, 16);
    offset = add_dir_entry_at(dir, offset, 8, "file_b", 0x8000, 16);
    wr16(dir + offset + 4,
         static_cast<uint16_t>(kBlockSize - offset));
  }

  void write_lost_found() {
    uint8_t* dir = block(kLostFoundBlock);
    std::size_t offset = 0;
    offset = add_dir_entry_at(dir, offset, 3, ".", 0x4000, 12);
    offset = add_dir_entry_at(dir, offset, 2, "..", 0x4000, 12);
    wr16(dir + offset + 4,
         static_cast<uint16_t>(kBlockSize - offset));
  }

  void write_nested_directory() {
    uint8_t* dir = block(kDirDataBlock);
    std::size_t offset = 0;
    offset = add_dir_entry_at(dir, offset, 9, ".", 0x4000, 12);
    offset = add_dir_entry_at(dir, offset, 2, "..", 0x4000, 12);
    wr16(dir + offset + 4,
         static_cast<uint16_t>(kBlockSize - offset));
  }

  static std::size_t add_dir_entry_at(uint8_t* dir, std::size_t offset,
                                      uint32_t child, const char* name,
                                      uint16_t file_type,
                                      uint16_t rec_len) {
    const auto name_len = static_cast<uint8_t>(std::strlen(name));
    wr32(dir + offset, child);
    wr16(dir + offset + 4, rec_len);
    dir[offset + 6] = name_len;
    dir[offset + 7] = static_cast<uint8_t>(file_type >> 12);
    std::memcpy(dir + offset + 8, name, name_len);
    return offset + rec_len;
  }

 protected:
  void add_dir_entry(uint32_t directory_block, const char* name,
                     uint32_t child, uint16_t file_type) {
    uint8_t* dir = block(directory_block);
    std::size_t offset = 0;
    while (offset < kBlockSize) {
      const uint16_t rec_len =
          ext4_audit::detail::rd16(dir + offset + 4);
      if (rec_len == 0) {
        break;
      }
      const uint32_t current = ext4_audit::detail::rd32(dir + offset);
      const uint16_t next = static_cast<uint16_t>(
          8 + ((std::strlen(
                    reinterpret_cast<const char*>(dir + offset + 8)) +
                3) &
               static_cast<std::size_t>(~3u)));
      if (current != 0 && rec_len > next) {
        wr16(dir + offset + 4, next);
        offset += next;
        break;
      }
      offset += rec_len;
      if (offset >= kBlockSize) {
        return;
      }
    }
    const uint16_t rec_len = static_cast<uint16_t>(
        8 + ((std::strlen(name) + 3) & static_cast<std::size_t>(~3u)));
    add_dir_entry_at(dir, offset, child, name, file_type, rec_len);
    if (offset + rec_len < kBlockSize) {
      wr16(dir + offset + rec_len + 4,
           static_cast<uint16_t>(kBlockSize - offset - rec_len));
    }
  }
};

}  // namespace ext4_audit_test

#endif  // EXT4_AUDIT_TEST_IMAGE_BUILDER_H
