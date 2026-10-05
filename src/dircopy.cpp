#include "dircopy.h"

#include <cerrno>
#include <cstring>
#include <stdexcept>
#include <system_error>

#include <dirent.h>
#include <fcntl.h>
#include <unistd.h>

namespace dircopy {
namespace {

std::system_error sys_error(const std::string& what, int err) {
    return std::system_error(err, std::generic_category(), what);
}

EntryKind kind_of(mode_t m) {
    if (S_ISDIR(m)) return EntryKind::Directory;
    if (S_ISREG(m)) return EntryKind::Regular;
    if (S_ISLNK(m)) return EntryKind::Symlink;
    return EntryKind::Other;
}

std::string read_link(const std::string& path) {
    std::string buf(256, '\0');
    for (;;) {
        ssize_t n = ::readlink(path.c_str(), buf.data(), buf.size());
        if (n < 0) throw sys_error("readlink(" + path + ")", errno);
        if (static_cast<size_t>(n) < buf.size()) {
            buf.resize(static_cast<size_t>(n));
            return buf;
        }
        buf.resize(buf.size() * 2);
    }
}

void scan_dir(const std::string& abs, const std::string& rel, ScanResult& out) {
    DIR* d = ::opendir(abs.c_str());
    if (!d) throw sys_error("opendir(" + abs + ")", errno);
    struct CloseDir {
        DIR* d;
        ~CloseDir() { ::closedir(d); }
    } guard{d};

    while (dirent* de = ::readdir(d)) {
        const char* name = de->d_name;
        if (std::strcmp(name, ".") == 0 || std::strcmp(name, "..") == 0) continue;
        std::string child_abs = abs + "/" + name;
        std::string child_rel = rel.empty() ? name : rel + "/" + name;

        struct stat st;
        if (::lstat(child_abs.c_str(), &st) != 0)
            throw sys_error("lstat(" + child_abs + ")", errno);

        ScanEntry e;
        e.kind = kind_of(st.st_mode);
        e.id = FileId{st.st_dev, st.st_ino};
        e.mode = st.st_mode & 07777;
        if (e.kind == EntryKind::Symlink) e.link_target = read_link(child_abs);
        out.entries.emplace(child_rel, e);
        if (e.kind == EntryKind::Regular)
            out.first_regular_path.emplace(e.id, child_rel);
        if (e.kind == EntryKind::Directory) scan_dir(child_abs, child_rel, out);
    }
}

bool same_id(const struct stat& st, const FileId& id) {
    return st.st_dev == id.dev && st.st_ino == id.ino;
}

bool is_prefix_dir(const std::string& dir, const std::string& path) {
    return path.size() > dir.size() && path.compare(0, dir.size(), dir) == 0 &&
           path[dir.size()] == '/';
}

// Copies one regular file. Returns false (after removing any partial
// destination) and sets `reason` on failure.
bool copy_regular(const std::string& src, const std::string& dst,
                  const ScanEntry& scanned, std::uint64_t& bytes,
                  std::string& reason) {
    int sfd = ::open(src.c_str(), O_RDONLY | O_NOFOLLOW | O_CLOEXEC);
    if (sfd < 0) {
        reason = "cannot open source: " + std::string(std::strerror(errno));
        return false;
    }
    struct Fd {
        int fd;
        ~Fd() { if (fd >= 0) ::close(fd); }
    } sg{sfd};

    struct stat st;
    if (::fstat(sfd, &st) != 0) {
        reason = "cannot fstat source: " + std::string(std::strerror(errno));
        return false;
    }
    if (!S_ISREG(st.st_mode) || !same_id(st, scanned.id)) {
        reason = "identity changed while opening";
        return false;
    }

    int dfd = ::open(dst.c_str(), O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
    if (dfd < 0) {
        reason = "cannot create destination: " + std::string(std::strerror(errno));
        return false;
    }
    Fd dg{dfd};

    char buf[128 * 1024];
    for (;;) {
        ssize_t n = ::read(sfd, buf, sizeof buf);
        if (n < 0) {
            if (errno == EINTR) continue;
            reason = "read failed: " + std::string(std::strerror(errno));
            ::unlink(dst.c_str());
            return false;
        }
        if (n == 0) break;
        const char* p = buf;
        ssize_t left = n;
        while (left > 0) {
            ssize_t w = ::write(dfd, p, static_cast<size_t>(left));
            if (w < 0) {
                if (errno == EINTR) continue;
                reason = "write failed: " + std::string(std::strerror(errno));
                ::unlink(dst.c_str());
                return false;
            }
            p += w;
            left -= w;
        }
        bytes += static_cast<std::uint64_t>(n);
    }

    if (::fchmod(dfd, scanned.mode & 07777) != 0) {
        reason = "cannot set permissions: " + std::string(std::strerror(errno));
        ::unlink(dst.c_str());
        return false;
    }
#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__NetBSD__)
    struct timespec times[2] = {st.st_atimespec, st.st_mtimespec};
#else
    struct timespec times[2] = {st.st_atim, st.st_mtim};
#endif
    ::futimens(dfd, times);  // best effort
    return true;
}

void remove_tree(const std::string& root) {
    // Depth-first removal; everything below was created by us with
    // owner-writable/searchable directories, so no chmod tricks are needed.
    struct stat st;
    if (::lstat(root.c_str(), &st) != 0) return;
    if (S_ISDIR(st.st_mode)) {
        DIR* d = ::opendir(root.c_str());
        if (d) {
            while (dirent* de = ::readdir(d)) {
                if (std::strcmp(de->d_name, ".") == 0 ||
                    std::strcmp(de->d_name, "..") == 0)
                    continue;
                remove_tree(root + "/" + de->d_name);
            }
            ::closedir(d);
        }
        ::rmdir(root.c_str());
    } else {
        ::unlink(root.c_str());
    }
}

}  // namespace

ScanResult scan_tree(const std::string& root) {
    struct stat st;
    if (::lstat(root.c_str(), &st) != 0)
        throw sys_error("lstat(" + root + ")", errno);
    if (!S_ISDIR(st.st_mode))
        throw std::invalid_argument("scan root is not a directory: " + root);
    ScanResult out;
    scan_dir(root, "", out);
    return out;
}

bool copy_tree(const ScanResult& scan, const std::string& src_root,
               const std::string& dst_root, CopyReport& report,
               OnError on_error) {
    struct stat st;
    if (::lstat(dst_root.c_str(), &st) == 0)
        throw std::invalid_argument("destination already exists: " + dst_root);
    if (::mkdir(dst_root.c_str(), 0755) != 0)
        throw sys_error("mkdir(" + dst_root + ")", errno);

    std::map<FileId, std::string> dst_by_inode;  // inode -> first dst path
    std::vector<std::string> rejected_dirs;
    std::vector<std::pair<std::string, mode_t>> dir_modes;  // deferred chmod

    auto reject = [&](const std::string& rel, const std::string& reason) -> bool {
        report.rejected.push_back({rel, reason});
        if (on_error == OnError::Abort) {
            report.aborted = true;
            remove_tree(dst_root);
            return false;  // stop
        }
        return true;  // keep going
    };

    for (const auto& [rel, entry] : scan.entries) {
        const std::string src = src_root + "/" + rel;
        const std::string dst = dst_root + "/" + rel;

        // Reject the whole subtree under a rejected directory.
        bool parent_rejected = false;
        for (const auto& rd : rejected_dirs) {
            if (is_prefix_dir(rd, rel)) {
                if (!reject(rel, "parent directory '" + rd + "' was rejected"))
                    return false;
                parent_rejected = true;
                break;
            }
        }
        if (parent_rejected) continue;

        struct stat cur;
        if (::lstat(src.c_str(), &cur) != 0) {
            if (!reject(rel, std::string("source vanished: ") + std::strerror(errno)))
                return false;
            if (entry.kind == EntryKind::Directory) rejected_dirs.push_back(rel);
            continue;
        }
        if (kind_of(cur.st_mode) != entry.kind || !same_id(cur, entry.id)) {
            if (!reject(rel, "identity changed since scan")) return false;
            if (entry.kind == EntryKind::Directory) rejected_dirs.push_back(rel);
            continue;
        }

        switch (entry.kind) {
            case EntryKind::Directory:
                if (::mkdir(dst.c_str(), (entry.mode | 0700) & 07777) != 0) {
                    if (!reject(rel, std::string("cannot create directory: ") +
                                         std::strerror(errno)))
                        return false;
                    rejected_dirs.push_back(rel);
                    continue;
                }
                dir_modes.emplace_back(dst, entry.mode & 07777);
                ++report.dirs_created;
                break;

            case EntryKind::Symlink: {
                std::string target;
                try {
                    target = read_link(src);
                } catch (const std::system_error& e) {
                    if (!reject(rel, std::string("cannot read symlink: ") + e.code().message()))
                        return false;
                    continue;
                }
                if (target != entry.link_target) {
                    if (!reject(rel, "symlink target changed since scan")) return false;
                    continue;
                }
                if (::symlink(target.c_str(), dst.c_str()) != 0) {
                    if (!reject(rel, std::string("cannot create symlink: ") +
                                         std::strerror(errno)))
                        return false;
                    continue;
                }
                ++report.symlinks_copied;
                break;
            }

            case EntryKind::Regular: {
                auto it = dst_by_inode.find(entry.id);
                if (it != dst_by_inode.end()) {
                    // Inode seen inside the root during the scan and already
                    // copied: reproduce the sharing with a hard link.
                    if (::link(it->second.c_str(), dst.c_str()) != 0) {
                        if (!reject(rel, std::string("cannot create hard link: ") +
                                             std::strerror(errno)))
                            return false;
                        continue;
                    }
                    ++report.hardlinks_created;
                    break;
                }
                std::string reason;
                if (!copy_regular(src, dst, entry, report.bytes_copied, reason)) {
                    if (!reject(rel, reason)) return false;
                    continue;
                }
                dst_by_inode.emplace(entry.id, dst);
                ++report.files_copied;
                break;
            }

            case EntryKind::Other:
                if (!reject(rel, "unsupported file type (not file/dir/symlink)"))
                    return false;
                break;
        }
    }

    // Apply final directory permissions now that nothing more is written.
    for (const auto& [path, mode] : dir_modes) ::chmod(path.c_str(), mode);

    return report.rejected.empty();
}

bool copy_directory(const std::string& src_root, const std::string& dst_root,
                    CopyReport& report, OnError on_error) {
    ScanResult scan = scan_tree(src_root);
    return copy_tree(scan, src_root, dst_root, report, on_error);
}

std::string format_report(const CopyReport& r) {
    std::string out;
    for (const auto& rej : r.rejected) out += "REJECTED " + rej.path + ": " + rej.reason + "\n";
    out += "dirs=" + std::to_string(r.dirs_created) +
           " files=" + std::to_string(r.files_copied) +
           " hardlinks=" + std::to_string(r.hardlinks_created) +
           " symlinks=" + std::to_string(r.symlinks_copied) +
           " bytes=" + std::to_string(r.bytes_copied) +
           " rejected=" + std::to_string(r.rejected.size()) +
           (r.aborted ? " aborted\n" : "\n");
    return out;
}

}  // namespace dircopy
