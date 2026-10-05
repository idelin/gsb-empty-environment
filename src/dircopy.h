#ifndef DIRCOPY_H
#define DIRCOPY_H

#include <cstdint>
#include <map>
#include <string>
#include <vector>
#include <sys/stat.h>

namespace dircopy {

// Identity of a filesystem object: device + inode pair.
struct FileId {
    dev_t dev = 0;
    ino_t ino = 0;
    bool operator<(const FileId& o) const {
        return dev != o.dev ? dev < o.dev : ino < o.ino;
    }
    bool operator==(const FileId& o) const { return dev == o.dev && ino == o.ino; }
};

enum class EntryKind { Directory, Regular, Symlink, Other };

struct ScanEntry {
    EntryKind kind = EntryKind::Other;
    FileId id;                 // identity captured at scan time (lstat)
    mode_t mode = 0;           // permission bits (regular files & dirs)
    std::string link_target;   // symlink target at scan time
};

// Result of scanning a source tree. Paths are relative to the scan root,
// using '/' separators; the root itself is not an entry.
struct ScanResult {
    std::map<std::string, ScanEntry> entries;          // relpath -> entry
    std::map<FileId, std::string> first_regular_path;  // inode -> first relpath
};

// Scans `root` recursively without following symlinks. Every regular-file
// inode encountered *inside* root is recorded exactly once in
// first_regular_path; hardlinked files outside root are never visited and
// therefore never enter the inode set.
// Throws std::system_error if root cannot be statted/listed.
ScanResult scan_tree(const std::string& root);

struct Rejection {
    std::string path;    // relative path of the rejected item
    std::string reason;  // human-readable cause
};

struct CopyReport {
    std::vector<Rejection> rejected;
    std::uint64_t dirs_created = 0;
    std::uint64_t files_copied = 0;
    std::uint64_t hardlinks_created = 0;
    std::uint64_t symlinks_copied = 0;
    std::uint64_t bytes_copied = 0;
    bool aborted = false;  // true if the copy stopped early (OnError::Abort)
};

enum class OnError {
    Skip,   // reject the affected item, keep going, keep the partial tree
    Abort,  // reject, stop immediately, remove the whole destination tree
};

// Copies the tree at `src_root` into `dst_root` (which must not exist yet;
// it is created). `scan` must be a prior scan of `src_root`.
//
// For every scanned entry the current source object is re-validated against
// the recorded (dev, ino) identity. Objects whose identity changed since the
// scan are rejected and recorded in `report`. Regular files whose inode was
// already copied get a hard link in the destination, so sharing relationships
// are reproduced inside the destination tree. Inodes that were not seen
// during the scan (e.g. a hard link to an object outside the root swapped in
// after the scan) are rejected, never silently adopted.
//
// Returns true when nothing was rejected. On OnError::Abort the partially
// created destination tree is removed before returning.
bool copy_tree(const ScanResult& scan, const std::string& src_root,
               const std::string& dst_root, CopyReport& report,
               OnError on_error = OnError::Skip);

// Convenience: scan + copy in one call.
bool copy_directory(const std::string& src_root, const std::string& dst_root,
                    CopyReport& report, OnError on_error = OnError::Skip);

// Formats a report as text lines for humans / log files.
std::string format_report(const CopyReport& report);

}  // namespace dircopy

#endif
