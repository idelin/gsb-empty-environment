#pragma once

#include <cstddef>
#include <string>
#include <sys/stat.h>
#include <unordered_map>
#include <vector>

namespace dircopy {

// Identity of a filesystem object: device + inode. Both are required;
// inode numbers are only unique within a single device.
struct FileId {
    dev_t dev = 0;
    ino_t ino = 0;
    bool operator==(const FileId& o) const { return dev == o.dev && ino == o.ino; }
    bool operator!=(const FileId& o) const { return !(*this == o); }
};

struct FileIdHash {
    std::size_t operator()(const FileId& id) const noexcept;
};

enum class EntryType { Directory, Regular, Symlink, Other };

struct ScannedEntry {
    EntryType type = EntryType::Other;
    std::string rel_path;      // path relative to the source root; "" is the root itself
    FileId id;
    mode_t mode = 0;           // permission bits
    off_t size = 0;            // regular files only
    timespec atime{};
    timespec mtime{};
    std::string link_target;   // symlinks only
};

struct ScanResult {
    // Deterministic order: parents always before their children.
    std::vector<ScannedEntry> entries;
    // Regular files only: FileId -> rel_path of the first occurrence inside
    // the root. Objects outside the root are never recorded here, so they
    // can never be pulled into the copy as hard-link members.
    std::unordered_map<FileId, std::string, FileIdHash> inode_to_first_rel;
};

// Recursively scan src_root (symlinks are never followed).
// Returns false and sets err on fatal failure (e.g. root missing/not a dir).
bool scan_tree(const std::string& src_root, ScanResult& out, std::string& err);

struct Rejection {
    std::string rel_path;
    std::string reason;
};

struct CopyReport {
    std::vector<Rejection> rejections;
    std::size_t dirs_created = 0;
    std::size_t files_copied = 0;
    std::size_t hardlinks_created = 0;
    std::size_t symlinks_created = 0;
    bool ok() const { return rejections.empty(); }
};

// Execute the copy described by `scan` from src_root to dst_root.
// dst_root must not exist yet; it is created. Every entry's identity
// (device + inode + type) is re-verified against the scan before use;
// entries whose identity changed are rejected and recorded in the report.
// Partially created files are removed on failure.
CopyReport execute_copy(const std::string& src_root, const std::string& dst_root,
                        const ScanResult& scan);

} // namespace dircopy
