#include "copy_engine.hpp"

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <unistd.h>

#if defined(__APPLE__)
#define DIRCOPY_ST_ATIME(st) ((st).st_atimespec)
#define DIRCOPY_ST_MTIME(st) ((st).st_mtimespec)
#else
#define DIRCOPY_ST_ATIME(st) ((st).st_atim)
#define DIRCOPY_ST_MTIME(st) ((st).st_mtim)
#endif

namespace dircopy {

std::size_t FileIdHash::operator()(const FileId& id) const noexcept {
    std::size_t h1 = std::hash<unsigned long long>{}(static_cast<unsigned long long>(id.dev));
    std::size_t h2 = std::hash<unsigned long long>{}(static_cast<unsigned long long>(id.ino));
    return h1 ^ (h2 + 0x9e3779b97f4a7c15ULL + (h1 << 6) + (h1 >> 2));
}

namespace {

std::string join_path(const std::string& dir, const std::string& name) {
    if (dir.empty() || dir == "/") return dir + name;
    return dir + "/" + name;
}

std::string errno_string(int e) { return std::strerror(e); }

EntryType type_from_mode(mode_t m) {
    if (S_ISDIR(m)) return EntryType::Directory;
    if (S_ISREG(m)) return EntryType::Regular;
    if (S_ISLNK(m)) return EntryType::Symlink;
    return EntryType::Other;
}

bool scan_dir(const std::string& src_root, const std::string& prefix, ScanResult& out,
              std::string& err) {
    const std::string abs = prefix.empty() ? src_root : join_path(src_root, prefix);
    DIR* dp = ::opendir(abs.c_str());
    if (!dp) {
        err = "cannot open directory '" + abs + "': " + errno_string(errno);
        return false;
    }
    std::vector<std::string> names;
    while (dirent* de = ::readdir(dp)) {
        std::string name = de->d_name;
        if (name != "." && name != "..") names.push_back(std::move(name));
    }
    ::closedir(dp);
    std::sort(names.begin(), names.end());

    for (const std::string& name : names) {
        const std::string rel = prefix.empty() ? name : prefix + "/" + name;
        const std::string child_abs = join_path(src_root, rel);
        struct stat st;
        if (::lstat(child_abs.c_str(), &st) != 0) {
            err = "cannot stat '" + child_abs + "': " + errno_string(errno);
            return false;
        }
        ScannedEntry e;
        e.type = type_from_mode(st.st_mode);
        e.rel_path = rel;
        e.id = FileId{st.st_dev, st.st_ino};
        e.mode = st.st_mode & 07777;
        e.size = st.st_size;
        e.atime = DIRCOPY_ST_ATIME(st);
        e.mtime = DIRCOPY_ST_MTIME(st);
        if (e.type == EntryType::Symlink) {
            char buf[PATH_MAX];
            ssize_t n = ::readlink(child_abs.c_str(), buf, sizeof(buf) - 1);
            if (n < 0) {
                err = "cannot read symlink '" + child_abs + "': " + errno_string(errno);
                return false;
            }
            e.link_target.assign(buf, static_cast<std::size_t>(n));
        }
        if (e.type == EntryType::Regular) {
            // Record the first in-root occurrence of this (dev, inode). Hard
            // links whose other members live outside the root are irrelevant:
            // only paths discovered by this walk can become link targets.
            out.inode_to_first_rel.emplace(e.id, rel);
        }
        out.entries.push_back(e);
        if (e.type == EntryType::Directory) {
            if (!scan_dir(src_root, rel, out, err)) return false;
        }
    }
    return true;
}

// Verify that the current filesystem object at abs still has the identity
// recorded during the scan. Returns false (with reason set) if it vanished
// or changed identity since the scan.
bool verify_identity(const std::string& abs, const ScannedEntry& e, struct stat& st,
                     std::string& reason) {
    if (::lstat(abs.c_str(), &st) != 0) {
        reason = "source vanished after scan: " + errno_string(errno);
        return false;
    }
    if (type_from_mode(st.st_mode) != e.type || st.st_dev != e.id.dev ||
        st.st_ino != e.id.ino) {
        reason = "source identity changed after scan (dev/inode/type mismatch)";
        return false;
    }
    return true;
}

// Copy one regular file. On any failure the partial destination is removed.
bool copy_regular(const std::string& src_abs, const std::string& dst_abs,
                  const ScannedEntry& e, std::string& reason) {
    int src_fd = ::open(src_abs.c_str(), O_RDONLY | O_NOFOLLOW | O_CLOEXEC);
    if (src_fd < 0) {
        reason = "cannot open source: " + errno_string(errno);
        return false;
    }
    struct stat st;
    if (::fstat(src_fd, &st) != 0 || !S_ISREG(st.st_mode) || st.st_dev != e.id.dev ||
        st.st_ino != e.id.ino) {
        ::close(src_fd);
        reason = "source identity changed while opening";
        return false;
    }

    // Create a unique temp file next to the final destination.
    std::string tmp_abs;
    int dst_fd = -1;
    for (int attempt = 0; attempt < 100; ++attempt) {
        tmp_abs = dst_abs + ".dircopy-tmp." + std::to_string(::getpid()) + "." +
                  std::to_string(attempt);
        dst_fd = ::open(tmp_abs.c_str(), O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
        if (dst_fd >= 0) break;
        if (errno != EEXIST) break;
    }
    if (dst_fd < 0) {
        reason = "cannot create temp destination: " + errno_string(errno);
        ::close(src_fd);
        return false;
    }

    auto fail = [&](const std::string& msg) {
        ::close(src_fd);
        ::close(dst_fd);
        ::unlink(tmp_abs.c_str()); // partial-failure cleanup
        reason = msg;
        return false;
    };

    char buf[128 * 1024];
    for (;;) {
        ssize_t n = ::read(src_fd, buf, sizeof(buf));
        if (n < 0) {
            if (errno == EINTR) continue;
            return fail("read error: " + errno_string(errno));
        }
        if (n == 0) break;
        ssize_t off = 0;
        while (off < n) {
            ssize_t w = ::write(dst_fd, buf + off, static_cast<std::size_t>(n - off));
            if (w < 0) {
                if (errno == EINTR) continue;
                return fail("write error: " + errno_string(errno));
            }
            off += w;
        }
    }
    if (::fchmod(dst_fd, e.mode) != 0) return fail("chmod failed: " + errno_string(errno));
    timespec ts[2] = {e.atime, e.mtime};
    if (::futimens(dst_fd, ts) != 0) return fail("futimens failed: " + errno_string(errno));
    if (::close(dst_fd) != 0) {
        dst_fd = -1;
        ::close(src_fd);
        ::unlink(tmp_abs.c_str());
        reason = "close failed: " + errno_string(errno);
        return false;
    }
    ::close(src_fd);
    if (::rename(tmp_abs.c_str(), dst_abs.c_str()) != 0) {
        const std::string msg = "rename into place failed: " + errno_string(errno);
        ::unlink(tmp_abs.c_str());
        reason = msg;
        return false;
    }
    return true;
}

} // namespace

bool scan_tree(const std::string& src_root, ScanResult& out, std::string& err) {
    struct stat st;
    if (::lstat(src_root.c_str(), &st) != 0) {
        err = "cannot stat source root '" + src_root + "': " + errno_string(errno);
        return false;
    }
    if (!S_ISDIR(st.st_mode)) {
        err = "source root '" + src_root + "' is not a directory";
        return false;
    }
    ScannedEntry root;
    root.type = EntryType::Directory;
    root.rel_path = "";
    root.id = FileId{st.st_dev, st.st_ino};
    root.mode = st.st_mode & 07777;
    root.atime = DIRCOPY_ST_ATIME(st);
    root.mtime = DIRCOPY_ST_MTIME(st);
    out.entries.push_back(root);
    return scan_dir(src_root, "", out, err);
}

CopyReport execute_copy(const std::string& src_root, const std::string& dst_root,
                        const ScanResult& scan) {
    CopyReport report;
    // FileId -> absolute destination path of the first successfully copied
    // member of the hard-link group.
    std::unordered_map<FileId, std::string, FileIdHash> copied_inodes;
    std::vector<const ScannedEntry*> dirs; // for the final mode/mtime pass

    for (const ScannedEntry& e : scan.entries) {
        const std::string src_abs =
            e.rel_path.empty() ? src_root : join_path(src_root, e.rel_path);
        const std::string dst_abs =
            e.rel_path.empty() ? dst_root : join_path(dst_root, e.rel_path);

        struct stat st;
        std::string reason;
        if (!verify_identity(src_abs, e, st, reason)) {
            report.rejections.push_back({e.rel_path, reason});
            continue;
        }

        switch (e.type) {
        case EntryType::Directory:
            if (::mkdir(dst_abs.c_str(), e.mode | 0700) != 0) {
                report.rejections.push_back(
                    {e.rel_path, "mkdir failed: " + errno_string(errno)});
                continue;
            }
            dirs.push_back(&e);
            ++report.dirs_created;
            break;

        case EntryType::Symlink: {
            char buf[PATH_MAX];
            ssize_t n = ::readlink(src_abs.c_str(), buf, sizeof(buf) - 1);
            if (n < 0) {
                report.rejections.push_back(
                    {e.rel_path, "cannot re-read symlink: " + errno_string(errno)});
                continue;
            }
            if (e.link_target.compare(0, std::string::npos, buf,
                                      static_cast<std::size_t>(n)) != 0) {
                report.rejections.push_back(
                    {e.rel_path, "symlink target changed after scan"});
                continue;
            }
            if (::symlink(e.link_target.c_str(), dst_abs.c_str()) != 0) {
                report.rejections.push_back(
                    {e.rel_path, "symlink creation failed: " + errno_string(errno)});
                continue;
            }
            ++report.symlinks_created;
            break;
        }

        case EntryType::Regular: {
            auto it = copied_inodes.find(e.id);
            if (it != copied_inodes.end()) {
                // Same (dev, inode) already copied inside this run: recreate
                // the sharing relationship with a hard link in the target.
                if (::link(it->second.c_str(), dst_abs.c_str()) != 0) {
                    report.rejections.push_back(
                        {e.rel_path, "hard link creation failed: " + errno_string(errno)});
                    continue;
                }
                ++report.hardlinks_created;
                break;
            }
            if (!copy_regular(src_abs, dst_abs, e, reason)) {
                report.rejections.push_back({e.rel_path, reason});
                continue;
            }
            copied_inodes.emplace(e.id, dst_abs);
            ++report.files_copied;
            break;
        }

        case EntryType::Other:
            report.rejections.push_back(
                {e.rel_path, "unsupported file type (not dir/file/symlink)"});
            break;
        }
    }

    // Restore directory modes/mtimes deepest-first so read-only directories
    // do not block populating their children.
    std::sort(dirs.begin(), dirs.end(), [](const ScannedEntry* a, const ScannedEntry* b) {
        return a->rel_path.size() > b->rel_path.size();
    });
    for (const ScannedEntry* e : dirs) {
        const std::string dst_abs =
            e->rel_path.empty() ? dst_root : join_path(dst_root, e->rel_path);
        ::chmod(dst_abs.c_str(), e->mode);
        timespec ts[2] = {e->atime, e->mtime};
        ::utimensat(AT_FDCWD, dst_abs.c_str(), ts, 0);
    }
    return report;
}

} // namespace dircopy
