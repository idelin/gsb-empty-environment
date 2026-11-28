#include "ext4_audit/ext4_audit.h"

#include <algorithm>
#include <fstream>
#include <queue>
#include <unordered_map>

namespace ext4_audit {

namespace {

using detail::Ext4Extent;
using detail::Ext4ExtentHeader;
using detail::Ext4ExtentIndex;
using detail::Ext4GroupDesc;
using detail::Ext4Inode;
using detail::Ext4SuperBlock;

constexpr std::size_t kSuperOffset = 1024;
constexpr uint16_t kSuperMagic = 0xEF53;
constexpr uint16_t kExtentMagic = 0xF30A;
constexpr uint32_t kRootInode = 2;
constexpr uint32_t kFeatureIncompat64Bit = 0x80;
constexpr uint32_t kInodeExtentsFlag = 0x80000;
constexpr uint16_t kExtentInitializedMask = 0x8000;
constexpr uint16_t kModeFormatMask = 0xF000;
constexpr uint16_t kModeSuid = 0x800;
constexpr uint16_t kModeSgid = 0x400;
constexpr uint16_t kTypeChar = 0x2000;
constexpr uint16_t kTypeDir = 0x4000;
constexpr uint16_t kTypeBlock = 0x6000;
constexpr uint16_t kTypeSymlink = 0xA000;
constexpr unsigned kMaxExtentDepth = 10;

struct InodeInfo {
  uint16_t mode = 0;
  uint32_t flags = 0;
  std::size_t byte_offset = 0;
  bool allocated = false;
  bool extents = false;
};

bool mul_overflow_u64(uint64_t a, uint64_t b, uint64_t& result) {
  if (a != 0 && b > (0xffffffffffffffffULL / a)) {
    return true;
  }
  result = a * b;
  return false;
}

class ImageView {
 public:
  ImageView(const uint8_t* data, std::size_t size)
      : data_(data), size_(size) {}

  bool has_bytes(std::size_t offset, std::size_t length) const {
    return length <= size_ && offset <= size_ - length;
  }

  const uint8_t* at(std::size_t offset, std::size_t length) const {
    return has_bytes(offset, length) ? data_ + offset : nullptr;
  }

  std::size_t size() const { return size_; }

  // Returns a pointer to a whole filesystem block, or nullptr when the block
  // number or resulting byte range is not fully inside the image.
  const uint8_t* block(uint64_t block_no, uint64_t fs_block_size) const {
    uint64_t byte_offset;
    if (mul_overflow_u64(block_no, fs_block_size, byte_offset)) {
      return nullptr;
    }
    if (byte_offset > size_ ||
        static_cast<uint64_t>(size_ - static_cast<std::size_t>(byte_offset))
            < fs_block_size) {
      return nullptr;
    }
    return data_ + static_cast<std::size_t>(byte_offset);
  }

 private:
  const uint8_t* data_;
  std::size_t size_;
};

class Auditor {
 public:
  Auditor(const uint8_t* data, std::size_t size, const AuditConfig& config)
      : image_(data, size), config_(config) {
    report_.image_size = static_cast<uint64_t>(size);
  }

  AuditReport run() {
    if (!parse_super_block()) {
      finalize();
      return std::move(report_);
    }
    if (!parse_group_descriptors()) {
      finalize();
      return std::move(report_);
    }
    scan_inodes();
    build_paths_and_policies();
    finalize();
    return std::move(report_);
  }

 private:
  ImageView image_;
  AuditConfig config_;
  AuditReport report_;

  uint64_t block_size_ = 0;
  uint64_t total_blocks_ = 0;
  uint32_t total_inodes_ = 0;
  uint32_t blocks_per_group_ = 0;
  uint32_t inodes_per_group_ = 0;
  uint32_t group_count_ = 0;
  uint32_t inode_size_ = 0;
  uint32_t inode_table_entry_size_ = 0;
  bool feature_64bit_ = false;
  std::size_t gdt_offset_ = 0;
  std::size_t gdt_entry_size_ = 0;
  std::vector<uint64_t> inode_tables_;
  std::vector<InodeInfo> inodes_;
  std::vector<uint32_t> block_owner_;
  std::vector<std::vector<uint64_t>> pending_cross_links_;

  void add(ViolationType type, uint32_t inode, std::string detail,
           std::vector<uint64_t> witnesses = {}) {
    Violation violation;
    violation.type = type;
    violation.inode = inode;
    violation.detail = std::move(detail);
    violation.witness_blocks = std::move(witnesses);
    report_.violations.push_back(std::move(violation));
  }

  bool parse_super_block() {
    const uint8_t* raw =
        image_.at(kSuperOffset, sizeof(Ext4SuperBlock));
    if (raw == nullptr) {
      add(ViolationType::ImageTooSmall, 0,
          "image is smaller than the 1024 byte ext4 super block");
      return false;
    }
    const Ext4SuperBlock* sb =
        reinterpret_cast<const Ext4SuperBlock*>(raw);

    if (sb->s_magic != kSuperMagic) {
      add(ViolationType::BadMagic, 0,
          "ext4 magic 0xEF53 not present at byte offset 1024");
      return false;
    }

    const uint32_t log_block_size = sb->s_log_block_size;
    if (log_block_size > 2) {
      add(ViolationType::UnsupportedBlockSize, 0,
          "only 1K/2K/4K block sizes are supported");
      return false;
    }
    block_size_ = 1024ULL << log_block_size;
    report_.block_size = static_cast<uint32_t>(block_size_);

    feature_64bit_ =
        (sb->s_feature_incompat & kFeatureIncompat64Bit) != 0;
    total_inodes_ = sb->s_inodes_count;
    blocks_per_group_ = sb->s_blocks_per_group;
    inodes_per_group_ = sb->s_inodes_per_group;
    inode_size_ = sb->s_inode_size;

    if (total_inodes_ == 0 || blocks_per_group_ == 0 ||
        inodes_per_group_ == 0 || inode_size_ < sizeof(Ext4Inode)) {
      add(ViolationType::InvalidInodeTable, 0,
          "super block declares invalid inode or block group geometry");
      return false;
    }

    const uint64_t blocks_hi =
        feature_64bit_ ? static_cast<uint64_t>(sb->s_blocks_count_hi)
                       : 0ULL;
    if (!feature_64bit_ && sb->s_blocks_count_hi != 0) {
      add(ViolationType::BlockCountMismatch, 0,
          "high block count set without the 64bit feature flag");
    }
    total_blocks_ =
        (blocks_hi << 32) | static_cast<uint64_t>(sb->s_blocks_count_lo);
    if (total_blocks_ == 0) {
      add(ViolationType::BlockCountMismatch, 0,
          "super block declares zero total blocks");
      return false;
    }

    uint64_t byte_length;
    if (mul_overflow_u64(total_blocks_, block_size_, byte_length)) {
      add(ViolationType::BlockCountMismatch, 0,
          "total blocks * block size overflows 64 bits");
      return false;
    }
    if (byte_length > image_.size()) {
      add(ViolationType::TruncatedImage, 0,
          "declared file system extends past the end of the image");
      return false;
    }
    if (byte_length < image_.size()) {
      add(ViolationType::BlockCountMismatch, 0,
          "image contains trailing bytes not covered by the file system");
    }

    group_count_ =
        static_cast<uint32_t>((total_blocks_ + blocks_per_group_ - 1) /
                              blocks_per_group_);
    const uint64_t inode_groups =
        (static_cast<uint64_t>(total_inodes_) + inodes_per_group_ - 1) /
        inodes_per_group_;
    if (inode_groups != group_count_) {
      add(ViolationType::InvalidGroupDescriptor, 0,
          "inode count and block count imply different group counts");
      return false;
    }

    gdt_entry_size_ =
        feature_64bit_
            ? static_cast<std::size_t>(
                  std::max<uint16_t>(sb->s_desc_size, 64))
            : sizeof(Ext4GroupDesc) / 2;
    if (feature_64bit_ && sb->s_desc_size < sizeof(Ext4GroupDesc)) {
      add(ViolationType::InvalidGroupDescriptor, 0,
          "64bit file system requires a descriptor size of at least 64");
      return false;
    }

    const uint64_t first_data_block = sb->s_first_data_block;
    uint64_t gdt_start;
    if (mul_overflow_u64(first_data_block + 1, block_size_, gdt_start)) {
      add(ViolationType::InvalidGroupDescriptor, 0,
          "group descriptor table offset overflows");
      return false;
    }
    uint64_t gdt_bytes;
    if (mul_overflow_u64(static_cast<uint64_t>(group_count_),
                         static_cast<uint64_t>(gdt_entry_size_),
                         gdt_bytes)) {
      add(ViolationType::InvalidGroupDescriptor, 0,
          "group descriptor table size overflows");
      return false;
    }
    if (gdt_start > image_.size() ||
        !image_.has_bytes(static_cast<std::size_t>(gdt_start),
                          static_cast<std::size_t>(gdt_bytes))) {
      add(ViolationType::InvalidGroupDescriptor, 0,
          "group descriptor table lies outside the image");
      return false;
    }
    gdt_offset_ = static_cast<std::size_t>(gdt_start);

    inode_table_entry_size_ = inode_size_;
    report_.total_blocks = total_blocks_;
    report_.total_inodes = total_inodes_;
    report_.block_groups = group_count_;
    return true;
  }

  bool parse_group_descriptors() {
    inode_tables_.resize(group_count_);
    for (uint32_t group = 0; group < group_count_; ++group) {
      const std::size_t offset =
          gdt_offset_ + static_cast<std::size_t>(group) * gdt_entry_size_;
      const uint8_t* raw =
          image_.at(offset, sizeof(Ext4GroupDesc) / 2);
      if (raw == nullptr) {
        add(ViolationType::InvalidGroupDescriptor, 0,
            "group descriptor is truncated");
        return false;
      }
      const Ext4GroupDesc* gd =
          reinterpret_cast<const Ext4GroupDesc*>(raw);
      uint64_t inode_table =
          static_cast<uint64_t>(gd->bg_inode_table_lo);
      if (feature_64bit_) {
        const uint8_t* wide =
            image_.at(offset, sizeof(Ext4GroupDesc));
        if (wide == nullptr) {
          add(ViolationType::InvalidGroupDescriptor, 0,
              "64bit group descriptor is truncated");
          return false;
        }
        const Ext4GroupDesc* wide_gd =
            reinterpret_cast<const Ext4GroupDesc*>(wide);
        inode_table |=
            static_cast<uint64_t>(wide_gd->bg_inode_table_hi) << 32;
      } else if (gd->bg_inode_table_hi != 0 ||
                 gd->bg_block_bitmap_hi != 0 ||
                 gd->bg_inode_bitmap_hi != 0) {
        add(ViolationType::InvalidGroupDescriptor, 0,
            "group descriptor uses 64bit fields without feature flag");
        return false;
      }

      if (inode_table >= total_blocks_) {
        add(ViolationType::InvalidInodeTable, 0,
            "group inode table starts outside the file system");
        return false;
      }
      const uint64_t base_inode =
          static_cast<uint64_t>(group) * inodes_per_group_;
      const uint64_t inode_count_in_group =
          std::min<uint64_t>(inodes_per_group_,
                             static_cast<uint64_t>(total_inodes_) -
                                 base_inode);
      uint64_t table_bytes;
      if (mul_overflow_u64(
              inode_count_in_group,
              static_cast<uint64_t>(inode_table_entry_size_),
              table_bytes)) {
        add(ViolationType::InvalidInodeTable, 0,
            "inode table size overflows");
        return false;
      }
      if (!image_.has_bytes(
              static_cast<std::size_t>(inode_table * block_size_),
              static_cast<std::size_t>(table_bytes))) {
        add(ViolationType::InvalidInodeTable, 0,
            "inode table extends outside the image");
        return false;
      }
      inode_tables_[group] = inode_table;
    }
    return true;
  }

  const uint8_t* inode_bytes(uint32_t inode_no, std::size_t& byte_offset) {
    const uint32_t index = inode_no - 1;
    const uint32_t group = index / inodes_per_group_;
    const uint32_t index_in_group = index % inodes_per_group_;
    const uint64_t offset =
        static_cast<uint64_t>(index_in_group) * inode_table_entry_size_;
    byte_offset = static_cast<std::size_t>(
        inode_tables_[group] * block_size_ + offset);
    return image_.at(byte_offset, sizeof(Ext4Inode));
  }

  void scan_inodes() {
    inodes_.resize(static_cast<std::size_t>(total_inodes_) + 1);
    block_owner_.assign(static_cast<std::size_t>(total_blocks_), 0);
    for (uint32_t inode_no = 1; inode_no <= total_inodes_; ++inode_no) {
      std::size_t byte_offset;
      const uint8_t* raw = inode_bytes(inode_no, byte_offset);
      if (raw == nullptr) {
        add(ViolationType::InvalidInode, inode_no,
            "inode record lies outside the image");
        continue;
      }
      const Ext4Inode* inode =
          reinterpret_cast<const Ext4Inode*>(raw);
      if (inode->i_mode == 0) {
        continue;  // Unused inode table slot.
      }
      InodeInfo& info = inodes_[inode_no];
      info.allocated = true;
      info.mode = inode->i_mode;
      info.flags = inode->i_flags;
      info.byte_offset = byte_offset;
      info.extents = (inode->i_flags & kInodeExtentsFlag) != 0;
      ++report_.scanned_inodes;

      if (info.extents) {
        validate_extent_tree(inode_no, inode->i_block);
      } else if (legacy_block_pointers_present(inode)) {
        add(ViolationType::LegacyMappingUnsupported, inode_no,
            "non-extent block mapping cannot be validated safely");
      }
    }

    if (!inodes_[kRootInode].allocated ||
        (inodes_[kRootInode].mode & kModeFormatMask) != kTypeDir) {
      add(ViolationType::RootInodeInvalid, kRootInode,
          "root inode 2 is missing or is not a directory");
    }
  }

  static bool legacy_block_pointers_present(const Ext4Inode* inode) {
    const uint16_t type = inode->i_mode & kModeFormatMask;
    if (type == kTypeSymlink) {
      // Fast symlinks store their target inline in i_block.
      const uint32_t size = inode->i_size_lo;
      return size >= sizeof(inode->i_block);
    }
    for (uint8_t byte : inode->i_block) {
      if (byte != 0) {
        return true;
      }
    }
    return false;
  }

  void validate_extent_tree(uint32_t inode_no, const uint8_t* root) {
    const Ext4ExtentHeader* header =
        reinterpret_cast<const Ext4ExtentHeader*>(root);
    if (header->eh_magic != kExtentMagic) {
      add(ViolationType::ExtentBadMagic, inode_no,
          "inode extent root missing magic 0xF30A");
      return;
    }
    std::unordered_set<uint64_t> visited_nodes;
    visited_nodes.insert(0);  // The inline root is identified as node 0.
    validate_extent_node(inode_no, root, header->eh_depth,
                         visited_nodes);
  }

  void validate_extent_node(
      uint32_t inode_no, const uint8_t* node,
      uint16_t expected_depth,
      std::unordered_set<uint64_t>& visited_nodes) {
    const Ext4ExtentHeader* header =
        reinterpret_cast<const Ext4ExtentHeader*>(node);
    if (header->eh_magic != kExtentMagic) {
      add(ViolationType::ExtentBadMagic, inode_no,
          "extent node missing magic 0xF30A");
      return;
    }
    if (header->eh_depth != expected_depth) {
      add(ViolationType::ExtentBadDepth, inode_no,
          "extent child depth does not decrease by exactly one");
      return;
    }
    if (expected_depth > kMaxExtentDepth) {
      add(ViolationType::ExtentBadDepth, inode_no,
          "extent tree exceeds the supported depth");
      return;
    }
    const std::size_t usable = block_size_;
    const uint32_t entries = header->eh_entries;
    if (static_cast<uint64_t>(entries) * 12ULL + 12ULL > usable) {
      add(ViolationType::ExtentBadMagic, inode_no,
          "extent entry count exceeds node boundaries");
      return;
    }

    bool have_logical = false;
    uint64_t next_logical = 0;
    const uint8_t* entry_base = node + sizeof(Ext4ExtentHeader);
    for (uint32_t i = 0; i < entries; ++i) {
      const uint8_t* entry =
          entry_base + static_cast<std::size_t>(i) * 12;
      if (header->eh_depth == 0) {
        const Ext4Extent* extent =
            reinterpret_cast<const Ext4Extent*>(entry);
        const uint64_t logical = extent->ee_block;
        uint16_t raw_len = extent->ee_len;
        // The top bit marks an uninitialized extent; real lengths never set it.
        const uint16_t length =
            (raw_len & kExtentInitializedMask) != 0
                ? static_cast<uint16_t>(
                      raw_len & static_cast<uint16_t>(~kExtentInitializedMask))
                : raw_len;
        if (length == 0) {
          add(ViolationType::ExtentBadMagic, inode_no,
              "extent with zero length");
          continue;
        }
        if (have_logical && logical < next_logical) {
          add(ViolationType::ExtentLogicOverlap, inode_no,
              "extent logical ranges are out of order or overlap");
        }
        have_logical = true;
        next_logical = logical + static_cast<uint64_t>(length);

        const uint64_t physical =
            (static_cast<uint64_t>(extent->ee_start_hi) << 32) |
            static_cast<uint64_t>(extent->ee_start_lo);
        const uint64_t last =
            physical + static_cast<uint64_t>(length) - 1;
        if (physical == 0 || physical >= total_blocks_ ||
            last >= total_blocks_ || last < physical) {
          add(ViolationType::ExtentPhysicalOutOfRange, inode_no,
              "extent physical block falls outside data area",
              {physical});
          continue;
        }
        for (uint64_t block = physical; block <= last; ++block) {
          claim_block(inode_no, block);
        }
      } else {
        const Ext4ExtentIndex* index =
            reinterpret_cast<const Ext4ExtentIndex*>(entry);
        const uint64_t logical = index->ei_block;
        if (have_logical && logical < next_logical) {
          add(ViolationType::ExtentLogicOverlap, inode_no,
              "extent index logical keys are out of order");
        }
        have_logical = true;
        next_logical = logical;

        const uint64_t child =
            (static_cast<uint64_t>(index->ei_leaf_hi) << 32) |
            static_cast<uint64_t>(index->ei_leaf_lo);
        if (child == 0 || child >= total_blocks_) {
          add(ViolationType::ExtentPhysicalOutOfRange, inode_no,
              "extent index points outside the file system",
              {child});
          continue;
        }
        if (!visited_nodes.insert(child).second) {
          add(ViolationType::ExtentLoop, inode_no,
              "extent tree references the same node twice",
              {child});
          continue;
        }
        const uint8_t* child_node =
            image_.block(child, block_size_);
        if (child_node == nullptr) {
          add(ViolationType::ExtentPhysicalOutOfRange, inode_no,
              "extent index block is truncated",
              {child});
          continue;
        }
        validate_extent_node(inode_no, child_node,
                             static_cast<uint16_t>(expected_depth - 1),
                             visited_nodes);
      }
    }
  }

  void claim_block(uint32_t inode_no, uint64_t block) {
    const std::size_t slot = static_cast<std::size_t>(block);
    const uint32_t current_owner = block_owner_[slot];
    if (current_owner == 0) {
      block_owner_[slot] = inode_no;
      return;
    }
    if (current_owner == inode_no) {
      add(ViolationType::ExtentDuplicatePhysicalBlock, inode_no,
          "inode maps the same physical block more than once",
          {block});
      return;
    }
    add(ViolationType::CrossLinkedBlock, inode_no,
        "physical block is shared by two different inodes",
        {block});
    pending_cross_links_.push_back(
        std::vector<uint64_t>{block, static_cast<uint64_t>(current_owner)});
  }

  bool collect_leaf_blocks(uint32_t inode_no,
                           std::vector<uint64_t>& leaves) {
    std::size_t byte_offset;
    const uint8_t* raw = inode_bytes(inode_no, byte_offset);
    if (raw == nullptr) {
      return false;
    }
    const Ext4Inode* inode =
        reinterpret_cast<const Ext4Inode*>(raw);
    if ((inode->i_flags & kInodeExtentsFlag) == 0) {
      return false;
    }
    const Ext4ExtentHeader* header =
        reinterpret_cast<const Ext4ExtentHeader*>(inode->i_block);
    if (header->eh_magic != kExtentMagic) {
      add(ViolationType::ExtentBadMagic, inode_no,
          "directory extent root missing magic 0xF30A");
      return false;
    }
    std::unordered_set<uint64_t> visited;
    return collect_leaf_blocks_rec(inode_no, inode->i_block,
                                   header->eh_depth, visited, leaves);
  }

  bool collect_leaf_blocks_rec(
      uint32_t inode_no, const uint8_t* node, uint16_t depth,
      std::unordered_set<uint64_t>& visited,
      std::vector<uint64_t>& leaves) {
    const Ext4ExtentHeader* header =
        reinterpret_cast<const Ext4ExtentHeader*>(node);
    if (header->eh_magic != kExtentMagic || depth > kMaxExtentDepth) {
      add(ViolationType::ExtentBadMagic, inode_no,
          "directory extent node is malformed");
      return false;
    }
    const uint8_t* entries = node + sizeof(Ext4ExtentHeader);
    for (uint32_t i = 0; i < header->eh_entries; ++i) {
      const uint8_t* entry = entries + static_cast<std::size_t>(i) * 12;
      if (depth == 0) {
        const Ext4Extent* extent =
            reinterpret_cast<const Ext4Extent*>(entry);
        const uint16_t raw_len = extent->ee_len;
        const uint16_t length =
            (raw_len & kExtentInitializedMask) != 0
                ? static_cast<uint16_t>(
                      raw_len &
                      static_cast<uint16_t>(~kExtentInitializedMask))
                : raw_len;
        const uint64_t physical =
            (static_cast<uint64_t>(extent->ee_start_hi) << 32) |
            static_cast<uint64_t>(extent->ee_start_lo);
        for (uint16_t n = 0; n < length; ++n) {
          leaves.push_back(physical + static_cast<uint64_t>(n));
        }
      } else {
        const Ext4ExtentIndex* index =
            reinterpret_cast<const Ext4ExtentIndex*>(entry);
        const uint64_t child =
            (static_cast<uint64_t>(index->ei_leaf_hi) << 32) |
            static_cast<uint64_t>(index->ei_leaf_lo);
        if (child == 0 || child >= total_blocks_ ||
            !visited.insert(child).second) {
          add(ViolationType::ExtentLoop, inode_no,
              "directory extent index is invalid",
              {child});
          continue;
        }
        const uint8_t* child_node =
            image_.block(child, block_size_);
        if (child_node == nullptr ||
            !collect_leaf_blocks_rec(inode_no, child_node,
                                     static_cast<uint16_t>(depth - 1),
                                     visited, leaves)) {
          return false;
        }
      }
    }
    return true;
  }

  void build_paths_and_policies() {
    std::unordered_map<uint32_t, std::string> paths;
    std::unordered_set<uint32_t> reached;
    paths.emplace(kRootInode, std::string("/"));

    if (inodes_[kRootInode].allocated &&
        (inodes_[kRootInode].mode & kModeFormatMask) == kTypeDir) {
      std::queue<uint32_t> queue;
      queue.push(kRootInode);
      reached.insert(kRootInode);
      while (!queue.empty()) {
        const uint32_t dir_inode = queue.front();
        queue.pop();
        walk_directory(dir_inode, paths, reached, queue);
      }
    }

    for (uint32_t inode_no = 1; inode_no <= total_inodes_; ++inode_no) {
      if (!inodes_[inode_no].allocated) {
        continue;
      }
      const auto found = paths.find(inode_no);
      if (found == paths.end()) {
        paths.emplace(inode_no,
                      "<orphan inode " +
                          std::to_string(inode_no) + ">");
      }
    }

    apply_policies(paths);
    decorate_violations(paths);
  }

  void walk_directory(
      uint32_t dir_inode,
      std::unordered_map<uint32_t, std::string>& paths,
      std::unordered_set<uint32_t>& reached,
      std::queue<uint32_t>& queue) {
    std::vector<uint64_t> leaves;
    if (!collect_leaf_blocks(dir_inode, leaves)) {
      return;
    }
    const std::string& parent_path = paths[dir_inode];
    const uint64_t max_entries = block_size_ / 8;
    for (uint64_t block_no : leaves) {
      const uint8_t* block = image_.block(block_no, block_size_);
      if (block == nullptr) {
        add(ViolationType::DirectoryEntryOutOfRange, dir_inode,
            "directory data block is outside the image",
            {block_no});
        continue;
      }
      uint64_t offset = 0;
      uint64_t entries_seen = 0;
      while (offset < block_size_) {
        if (++entries_seen > max_entries) {
          add(ViolationType::DirectoryEntryOutOfRange, dir_inode,
              "directory entry chain does not terminate",
              {block_no});
          break;
        }
        const uint8_t* entry = block + offset;
        const uint32_t child_inode = detail::rd32(entry);
        const uint16_t rec_len = detail::rd16(entry + 4);
        const uint8_t name_len = entry[6];
        if (rec_len < 8 || rec_len % 4 != 0 ||
            offset + rec_len > block_size_ ||
            static_cast<uint32_t>(name_len) + 8 > rec_len) {
          add(ViolationType::DirectoryEntryOutOfRange, dir_inode,
              "directory entry crosses the block boundary",
              {block_no});
          break;
        }
        if (child_inode != 0) {
          if (child_inode > total_inodes_ ||
              !inodes_[child_inode].allocated) {
            add(ViolationType::InvalidInode, child_inode,
                "directory entry references a non-allocated inode",
                {block_no});
          } else {
            const std::string name(
                reinterpret_cast<const char*>(entry + 8), name_len);
            if (name != "." && name != "..") {
              std::string child_path =
                  parent_path == "/" ? "/" + name
                                     : parent_path + "/" + name;
              if (paths.find(child_inode) == paths.end()) {
                paths.emplace(child_inode, child_path);
              }
              const uint16_t type =
                  inodes_[child_inode].mode & kModeFormatMask;
              if (type == kTypeDir &&
                  reached.insert(child_inode).second) {
                queue.push(child_inode);
              }
            }
          }
        }
        offset += rec_len;
      }
    }
  }

  void apply_policies(
      const std::unordered_map<uint32_t, std::string>& paths) {
    for (uint32_t inode_no = 1; inode_no <= total_inodes_; ++inode_no) {
      const InodeInfo& info = inodes_[inode_no];
      if (!info.allocated) {
        continue;
      }
      const auto path_it = paths.find(inode_no);
      const std::string& path =
          path_it == paths.end() ? std::string{} : path_it->second;
      const uint16_t type = info.mode & kModeFormatMask;

      if ((info.mode & kModeSuid) != 0 &&
          config_.suid_inode_whitelist.count(inode_no) == 0) {
        add(ViolationType::UnsafeSetuid, inode_no,
            "non-whitelisted SUID bit on " + path);
      }
      if ((info.mode & kModeSgid) != 0 &&
          config_.sgid_inode_whitelist.count(inode_no) == 0) {
        add(ViolationType::UnsafeSetgid, inode_no,
            "non-whitelisted SGID bit on " + path);
      }

      if (type == kTypeBlock || type == kTypeChar) {
        const std::pair<uint32_t, uint32_t> device =
            read_device_number(inode_no);
        const uint64_t key = AuditConfig::make_device_key(
            device.first, device.second);
        if (config_.device_major_minor_whitelist.count(key) == 0) {
          add(type == kTypeBlock
                  ? ViolationType::UnsafeBlockDevice
                  : ViolationType::UnsafeCharDevice,
              inode_no,
              "dangerous device node " + path + " (" +
                  std::to_string(device.first) + "," +
                  std::to_string(device.second) + ")");
        }
      }
    }
  }

  std::pair<uint32_t, uint32_t> read_device_number(uint32_t inode_no) {
    std::size_t byte_offset;
    const uint8_t* raw = inode_bytes(inode_no, byte_offset);
    if (raw == nullptr) {
      return {0, 0};
    }
    const Ext4Inode* inode =
        reinterpret_cast<const Ext4Inode*>(raw);
    const uint32_t value = detail::rd32(inode->i_block);
    if (value < 0x10000u) {
      return {static_cast<uint32_t>((value >> 8) & 0xfffu),
              static_cast<uint32_t>(value & 0xffu)};
    }
    return {static_cast<uint32_t>(value & 0xfffffu),
            static_cast<uint32_t>((value >> 20) & 0xfffffu)};
  }

  void decorate_violations(
      const std::unordered_map<uint32_t, std::string>& paths) {
    for (Violation& violation : report_.violations) {
      const auto found = paths.find(violation.inode);
      if (found != paths.end()) {
        violation.path = found->second;
      }
    }
    for (const auto& link : pending_cross_links_) {
      const uint32_t other_owner =
          static_cast<uint32_t>(link[1]);
      const auto found = paths.find(other_owner);
      if (found != paths.end()) {
        for (Violation& violation : report_.violations) {
          if (violation.type == ViolationType::CrossLinkedBlock &&
              !violation.witness_blocks.empty() &&
              violation.witness_blocks[0] == link[0]) {
            violation.detail +=
                "; also owned by inode " +
                std::to_string(other_owner) + " " + found->second;
          }
        }
      }
    }
  }

  void finalize() {
    std::sort(report_.violations.begin(), report_.violations.end(),
              [](const Violation& a, const Violation& b) {
                if (a.inode != b.inode) {
                  return a.inode < b.inode;
                }
                return static_cast<int>(a.type) <
                       static_cast<int>(b.type);
              });
  }
};

}  // namespace

const char* violation_type_name(ViolationType type) {
  switch (type) {
    case ViolationType::ImageTooSmall:
      return "ImageTooSmall";
    case ViolationType::BadMagic:
      return "BadMagic";
    case ViolationType::UnsupportedBlockSize:
      return "UnsupportedBlockSize";
    case ViolationType::BlockCountMismatch:
      return "BlockCountMismatch";
    case ViolationType::TruncatedImage:
      return "TruncatedImage";
    case ViolationType::InvalidGroupDescriptor:
      return "InvalidGroupDescriptor";
    case ViolationType::InvalidInodeTable:
      return "InvalidInodeTable";
    case ViolationType::InvalidInode:
      return "InvalidInode";
    case ViolationType::RootInodeInvalid:
      return "RootInodeInvalid";
    case ViolationType::LegacyMappingUnsupported:
      return "LegacyMappingUnsupported";
    case ViolationType::ExtentBadMagic:
      return "ExtentBadMagic";
    case ViolationType::ExtentBadDepth:
      return "ExtentBadDepth";
    case ViolationType::ExtentLoop:
      return "ExtentLoop";
    case ViolationType::ExtentLogicOverlap:
      return "ExtentLogicOverlap";
    case ViolationType::ExtentPhysicalOutOfRange:
      return "ExtentPhysicalOutOfRange";
    case ViolationType::ExtentDuplicatePhysicalBlock:
      return "ExtentDuplicatePhysicalBlock";
    case ViolationType::DirectoryEntryOutOfRange:
      return "DirectoryEntryOutOfRange";
    case ViolationType::CrossLinkedBlock:
      return "CrossLinkedBlock";
    case ViolationType::UnsafeSetuid:
      return "UnsafeSetuid";
    case ViolationType::UnsafeSetgid:
      return "UnsafeSetgid";
    case ViolationType::UnsafeBlockDevice:
      return "UnsafeBlockDevice";
    case ViolationType::UnsafeCharDevice:
      return "UnsafeCharDevice";
  }
  return "Unknown";
}

AuditReport audit_image(const uint8_t* data, std::size_t size,
                        const AuditConfig& config) {
  if (data == nullptr || size == 0) {
    AuditReport report;
    report.image_size = size;
    Violation violation;
    violation.type = ViolationType::ImageTooSmall;
    violation.detail = "empty or null image buffer";
    report.violations.push_back(std::move(violation));
    return report;
  }
  Auditor auditor(data, size, config);
  return auditor.run();
}

AuditReport audit_file(const std::string& path, const AuditConfig& config) {
  std::ifstream stream(path, std::ios::binary | std::ios::ate);
  if (!stream) {
    AuditReport report;
    Violation violation;
    violation.type = ViolationType::ImageTooSmall;
    violation.detail = "unable to open image file: " + path;
    report.violations.push_back(std::move(violation));
    return report;
  }
  const std::streamsize length = stream.tellg();
  if (length <= 0) {
    AuditReport report;
    Violation violation;
    violation.type = ViolationType::ImageTooSmall;
    violation.detail = "image file is empty: " + path;
    report.violations.push_back(std::move(violation));
    return report;
  }
  stream.seekg(0, std::ios::beg);
  std::vector<uint8_t> buffer(static_cast<std::size_t>(length));
  if (!stream.read(reinterpret_cast<char*>(buffer.data()), length)) {
    AuditReport report;
    Violation violation;
    violation.type = ViolationType::ImageTooSmall;
    violation.detail = "failed to read full image file: " + path;
    report.violations.push_back(std::move(violation));
    return report;
  }
  return audit_image(buffer.data(), buffer.size(), config);
}

}  // namespace ext4_audit
