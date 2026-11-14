#include "netsec.hpp"

#include <algorithm>
#include <atomic>
#include <cerrno>
#include <chrono>
#include <cstring>
#include <sstream>
#include <string>
#include <unordered_map>
#include <utility>
#include <vector>

#include <fcntl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

namespace netsec {
namespace {

constexpr std::size_t kMaxComponentLength = 255;
constexpr std::size_t kMaxTempAttempts = 128;

std::atomic<std::uint64_t> g_temp_counter{0};

Error make_error(ErrorCode code, std::string message, std::string path = {}) {
    return Error{code, std::move(message), std::move(path)};
}

struct ParsedPath {
    std::vector<std::string> components;
    std::string normalized;
};

struct PathParse {
    ParsedPath value;
    Error error;

    [[nodiscard]] explicit operator bool() const noexcept {
        return !error;
    }
};

bool is_absolute_archive_path(std::string_view value) {
    if (value.empty()) {
        return false;
    }
    if (value.front() == '/') {
        return true;
    }
#if defined(_WIN32)
    if (value.size() >= 2 && value[1] == ':') {
        return true;
    }
#endif
    return false;
}

std::string join_components(const std::vector<std::string>& components,
                            std::size_t count) {
    std::string result;
    for (std::size_t i = 0; i < count; ++i) {
        if (i != 0) {
            result.push_back('/');
        }
        result.append(components[i]);
    }
    return result;
}

PathParse parse_member_path(std::string_view raw, bool allow_dot_components) {
    if (raw.empty()) {
        return {{}, make_error(ErrorCode::InvalidPath,
                               "archive path must not be empty")};
    }
    if (raw.find('\0') != std::string_view::npos) {
        return {{}, make_error(ErrorCode::InvalidPath,
                               "archive path contains a NUL byte")};
    }
    if (is_absolute_archive_path(raw)) {
        return {{}, make_error(ErrorCode::AbsolutePath,
                               "archive path must be relative",
                               std::string(raw))};
    }
    if (raw.find('\\') != std::string_view::npos) {
        return {{}, make_error(ErrorCode::InvalidPath,
                               "backslashes are not valid archive separators",
                               std::string(raw))};
    }

    ParsedPath parsed;
    std::size_t begin = 0;
    while (begin <= raw.size()) {
        const std::size_t end = raw.find('/', begin);
        const std::size_t length =
            end == std::string_view::npos ? raw.size() - begin : end - begin;
        std::string_view component = raw.substr(begin, length);

        if (component.empty()) {
            return {{}, make_error(ErrorCode::InvalidPath,
                                   "archive path contains an empty component",
                                   std::string(raw))};
        }
        if (component.size() > kMaxComponentLength) {
            return {{}, make_error(ErrorCode::InvalidPath,
                                   "archive path component is too long",
                                   std::string(raw))};
        }
        if (component == "..") {
            return {{}, make_error(ErrorCode::ParentTraversal,
                                   "archive path contains a parent traversal",
                                   std::string(raw))};
        }
        if (component == ".") {
            if (!allow_dot_components) {
                return {{}, make_error(ErrorCode::InvalidPath,
                                       "archive path contains a dot component",
                                       std::string(raw))};
            }
        } else {
            parsed.components.emplace_back(component);
        }

        if (end == std::string_view::npos) {
            break;
        }
        begin = end + 1;
    }

    if (parsed.components.empty()) {
        return {{}, make_error(ErrorCode::InvalidPath,
                               "archive path normalizes to an empty path",
                               std::string(raw))};
    }

    parsed.normalized = join_components(parsed.components,
                                        parsed.components.size());
    return {std::move(parsed), {}};
}

PathParse resolve_symbolic_target(std::string_view entry_path,
                                  std::string_view target) {
    if (target.empty()) {
        return {{}, make_error(ErrorCode::LinkTargetInvalid,
                               "symbolic link target must not be empty",
                               std::string(entry_path))};
    }
    if (target.find('\0') != std::string_view::npos) {
        return {{}, make_error(ErrorCode::LinkTargetInvalid,
                               "symbolic link target contains a NUL byte",
                               std::string(entry_path))};
    }
    if (is_absolute_archive_path(target)) {
        return {{}, make_error(ErrorCode::LinkTargetOutsideArchive,
                               "absolute symbolic link targets are not allowed",
                               std::string(target))};
    }
    if (target.find('\\') != std::string_view::npos) {
        return {{}, make_error(ErrorCode::LinkTargetInvalid,
                               "backslashes are not valid archive separators",
                               std::string(target))};
    }

    PathParse entry = parse_member_path(entry_path, true);
    if (!entry) {
        return {{}, entry.error};
    }
    if (entry.value.components.empty()) {
        return {{}, make_error(ErrorCode::LinkTargetInvalid,
                               "symbolic link entry path is empty",
                               std::string(entry_path))};
    }

    std::vector<std::string> resolved(entry.value.components.begin(),
                                      entry.value.components.end() - 1);
    std::size_t begin = 0;
    while (begin <= target.size()) {
        const std::size_t end = target.find('/', begin);
        const std::size_t length =
            end == std::string_view::npos ? target.size() - begin : end - begin;
        std::string_view component = target.substr(begin, length);

        if (component.empty()) {
            return {{}, make_error(ErrorCode::LinkTargetInvalid,
                                   "symbolic link target contains an empty component",
                                   std::string(target))};
        }
        if (component.size() > kMaxComponentLength) {
            return {{}, make_error(ErrorCode::LinkTargetInvalid,
                                   "symbolic link target component is too long",
                                   std::string(target))};
        }
        if (component == ".") {
            // No change.
        } else if (component == "..") {
            if (resolved.empty()) {
                return {{}, make_error(
                                   ErrorCode::LinkTargetOutsideArchive,
                                   "symbolic link target escapes the archive root",
                                   std::string(target))};
            }
            resolved.pop_back();
        } else {
            resolved.emplace_back(component);
        }

        if (end == std::string_view::npos) {
            break;
        }
        begin = end + 1;
    }

    if (resolved.empty()) {
        return {{}, make_error(ErrorCode::LinkTargetOutsideArchive,
                               "symbolic link target resolves outside the archive",
                               std::string(target))};
    }

    ParsedPath result;
    result.components = std::move(resolved);
    result.normalized = join_components(result.components,
                                        result.components.size());
    return {std::move(result), {}};
}

std::string relative_link_text(const ParsedPath& link_path,
                               const ParsedPath& target_path) {
    const std::vector<std::string> parent_components(
        link_path.components.begin(), link_path.components.end() - 1);

    std::size_t common = 0;
    while (common < parent_components.size() &&
           common < target_path.components.size() &&
           parent_components[common] == target_path.components[common]) {
        ++common;
    }

    if (common == parent_components.size() &&
        common == target_path.components.size()) {
        return ".";
    }

    std::string text;
    for (std::size_t i = common; i < parent_components.size(); ++i) {
        text.append("../");
    }
    for (std::size_t i = common; i < target_path.components.size(); ++i) {
        text.append(target_path.components[i]);
        if (i + 1 != target_path.components.size()) {
            text.push_back('/');
        }
    }
    return text;
}

Error validate_permission_bits(std::filesystem::perms permissions,
                               std::string_view path,
                               const ExtractOptions& options) {
    using P = std::filesystem::perms;
    const P allowed = P::all;

    if ((permissions & allowed) != permissions) {
        return make_error(ErrorCode::UnsafePermissions,
                          "entry contains undefined permission bits",
                          std::string(path));
    }
    if ((permissions & P::set_uid) != P::none ||
        (permissions & P::set_gid) != P::none ||
        (permissions & P::sticky_bit) != P::none) {
        return make_error(ErrorCode::UnsafePermissions,
                          "setuid, setgid and sticky permission bits are not allowed",
                          std::string(path));
    }
    if (!options.allow_group_or_world_writable &&
        ((permissions & P::group_write) != P::none ||
         (permissions & P::others_write) != P::none)) {
        return make_error(ErrorCode::UnsafePermissions,
                          "group/other write permission bits are not allowed",
                          std::string(path));
    }
    return {};
}

Error validate_permissions(const ArchiveEntry& entry,
                           const ExtractOptions& options) {
    if (!entry.permissions.has_value()) {
        return {};
    }
    return validate_permission_bits(*entry.permissions, entry.path, options);
}

mode_t to_mode(std::filesystem::perms permissions) {
    return static_cast<mode_t>(permissions) & 0777;
}

std::filesystem::perms file_default_permissions(const ExtractOptions& options) {
    return options.default_file_permissions;
}

std::filesystem::perms directory_default_permissions(
    const ExtractOptions& options) {
    return options.default_directory_permissions;
}

class FileDescriptor {
public:
    FileDescriptor() = default;
    explicit FileDescriptor(int fd) : fd_(fd) {}
    FileDescriptor(const FileDescriptor&) = delete;
    FileDescriptor& operator=(const FileDescriptor&) = delete;

    FileDescriptor(FileDescriptor&& other) noexcept : fd_(other.fd_) {
        other.fd_ = -1;
    }
    FileDescriptor& operator=(FileDescriptor&& other) noexcept {
        if (this != &other) {
            reset();
            fd_ = other.fd_;
            other.fd_ = -1;
        }
        return *this;
    }
    ~FileDescriptor() { reset(); }

    [[nodiscard]] int get() const noexcept { return fd_; }
    [[nodiscard]] explicit operator bool() const noexcept { return fd_ >= 0; }

    void reset(int fd = -1) noexcept {
        if (fd_ >= 0) {
            ::close(fd_);
        }
        fd_ = fd;
    }

private:
    int fd_ = -1;
};

struct PlanEntry {
    ArchiveEntry entry;
    ParsedPath path;
    std::optional<ParsedPath> target_path;
};

struct Plan {
    std::vector<PlanEntry> entries;
    std::unordered_map<std::string, std::size_t> index;
};

struct PlanResult {
    Plan value;
    Error error;

    [[nodiscard]] explicit operator bool() const noexcept {
        return !error;
    }
};

PlanResult build_plan(const std::vector<ArchiveEntry>& entries,
                      const ExtractOptions& options) {
    Plan plan;
    plan.entries.reserve(entries.size());

    Error default_file_error = validate_permission_bits(
        options.default_file_permissions, "<default file permissions>", options);
    if (default_file_error) {
        return {{}, std::move(default_file_error)};
    }
    Error default_directory_error = validate_permission_bits(
        options.default_directory_permissions,
        "<default directory permissions>", options);
    if (default_directory_error) {
        return {{}, std::move(default_directory_error)};
    }

    for (std::size_t i = 0; i < entries.size(); ++i) {
        const ArchiveEntry& entry = entries[i];
        PathParse parsed = parse_member_path(entry.path, true);
        if (!parsed) {
            return {{}, parsed.error};
        }

        if (plan.index.contains(parsed.value.normalized)) {
            return {{}, make_error(ErrorCode::DuplicateEntry,
                                   "archive contains duplicate entry paths",
                                   parsed.value.normalized)};
        }

        Error permissions_error = validate_permissions(entry, options);
        if (permissions_error) {
            return {{}, std::move(permissions_error)};
        }

        PlanEntry planned;
        planned.entry = entry;
        planned.path = std::move(parsed.value);

        switch (entry.type) {
        case EntryType::RegularFile:
        case EntryType::Directory:
            if (entry.declared_size != 0 &&
                entry.declared_size != entry.data.size()) {
                return {{}, make_error(ErrorCode::SizeMismatch,
                                       "declared size does not match entry data",
                                       planned.path.normalized)};
            }
            break;
        case EntryType::HardLink: {
            if (options.hard_links == LinkPolicy::Reject) {
                return {{}, make_error(ErrorCode::HardLinkRejected,
                                       "hard link entries are rejected by policy",
                                       planned.path.normalized)};
            }
            PathParse target = parse_member_path(entry.link_target, true);
            if (!target) {
                Error error = target.error;
                if (error.code == ErrorCode::AbsolutePath ||
                    error.code == ErrorCode::ParentTraversal) {
                    error.code = ErrorCode::LinkTargetOutsideArchive;
                }
                error.path = planned.path.normalized;
                return {{}, std::move(error)};
            }
            if (target.value.normalized == planned.path.normalized) {
                return {{}, make_error(ErrorCode::LinkCycle,
                                       "hard link points to itself",
                                       planned.path.normalized)};
            }
            planned.target_path = std::move(target.value);
            break;
        }
        case EntryType::SymbolicLink: {
            if (options.symbolic_links == LinkPolicy::Reject) {
                return {{}, make_error(
                                   ErrorCode::SymbolicLinkRejected,
                                   "symbolic link entries are rejected by policy",
                                   planned.path.normalized)};
            }
            PathParse target =
                resolve_symbolic_target(planned.path.normalized,
                                        entry.link_target);
            if (!target) {
                Error error = target.error;
                error.path = planned.path.normalized;
                return {{}, std::move(error)};
            }
            if (target.value.normalized == planned.path.normalized) {
                return {{}, make_error(ErrorCode::LinkCycle,
                                       "symbolic link points to itself",
                                       planned.path.normalized)};
            }
            planned.target_path = std::move(target.value);
            break;
        }
        default:
            return {{}, make_error(ErrorCode::UnsupportedEntryType,
                                   "unsupported archive entry type",
                                   planned.path.normalized)};
        }

        plan.index.emplace(planned.path.normalized, i);
        plan.entries.push_back(std::move(planned));
    }

    for (std::size_t i = 0; i < plan.entries.size(); ++i) {
        const PlanEntry& planned = plan.entries[i];

        if (planned.entry.type == EntryType::HardLink ||
            planned.entry.type == EntryType::SymbolicLink) {
            const std::string& target = planned.target_path->normalized;
            auto target_it = plan.index.find(target);
            if (target_it == plan.index.end()) {
                return {{}, make_error(
                                   ErrorCode::LinkTargetMissingFromArchive,
                                   "link target is not an archive member",
                                   planned.path.normalized)};
            }
            if (target_it->second >= i) {
                return {{}, make_error(
                                   ErrorCode::LinkTargetNotExtracted,
                                   "link target must occur before the link entry",
                                   planned.path.normalized)};
            }
            const PlanEntry& target_entry = plan.entries[target_it->second];
            if (planned.entry.type == EntryType::HardLink &&
                target_entry.entry.type != EntryType::RegularFile) {
                return {{}, make_error(
                                   ErrorCode::LinkTargetWrongType,
                                   "hard link target must be a regular file entry",
                                   planned.path.normalized)};
            }
            if (planned.entry.type == EntryType::SymbolicLink &&
                target_entry.entry.type != EntryType::RegularFile &&
                target_entry.entry.type != EntryType::Directory) {
                return {{}, make_error(
                                   ErrorCode::LinkTargetWrongType,
                                   "symbolic link target must be a file or directory entry",
                                   planned.path.normalized)};
            }
        }

        // No entry may be extracted through another archive link. This also
        // prevents children of file entries and link-based parent traversal.
        for (std::size_t depth = 1; depth < planned.path.components.size();
             ++depth) {
            const std::string prefix =
                join_components(planned.path.components, depth);
            auto parent_it = plan.index.find(prefix);
            if (parent_it == plan.index.end()) {
                continue;
            }
            const PlanEntry& parent = plan.entries[parent_it->second];
            if (parent.entry.type != EntryType::Directory) {
                return {{}, make_error(
                                   ErrorCode::EntryConflict,
                                   "entry path traverses a non-directory archive member",
                                   planned.path.normalized)};
            }
        }
    }

    return {std::move(plan), {}};
}

FileDescriptor open_root_directory(const std::filesystem::path& destination,
                                   const ExtractOptions& options,
                                   Error& error) {
    std::filesystem::path root = destination;
    try {
        if (root.empty()) {
            error = make_error(ErrorCode::RootInvalid,
                               "destination root must not be empty");
            return {};
        }

        const bool exists = std::filesystem::exists(root);
        if (!exists) {
            if (!options.create_root) {
                error = make_error(ErrorCode::RootInvalid,
                                   "destination root does not exist",
                                   root.string());
                return {};
            }
            std::filesystem::create_directories(root);
        }

        if (!std::filesystem::is_directory(root)) {
            error = make_error(ErrorCode::RootInvalid,
                               "destination root is not a directory",
                               root.string());
            return {};
        }

        root = std::filesystem::canonical(root);
        const std::string root_string = root.string();
        const int fd = ::open(root_string.c_str(),
                              O_RDONLY | O_DIRECTORY | O_CLOEXEC);
        if (fd < 0) {
            error = make_error(ErrorCode::IoError,
                               std::string("cannot open destination root: ") +
                                   std::strerror(errno),
                               root_string);
            return {};
        }
        return FileDescriptor(fd);
    } catch (const std::filesystem::filesystem_error& ex) {
        error = make_error(ErrorCode::IoError,
                           std::string("cannot prepare destination root: ") +
                               ex.what(),
                           root.string());
        return {};
    }
}

FileDescriptor open_or_create_directory(int parent_fd,
                                        const std::string& component,
                                        bool create,
                                        std::filesystem::perms mode,
                                        bool* created,
                                        Error& error) {
    if (created != nullptr) {
        *created = false;
    }

    const int first = ::openat(parent_fd, component.c_str(),
                               O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (first >= 0) {
        return FileDescriptor(first);
    }

    if (errno == ENOENT && create) {
        const mode_t native_mode = to_mode(mode);
        if (::mkdirat(parent_fd, component.c_str(), native_mode) != 0 &&
            errno != EEXIST) {
            error = make_error(ErrorCode::IoError,
                               std::string("cannot create parent directory: ") +
                                   std::strerror(errno),
                               component);
            return {};
        }
        const int fd = ::openat(parent_fd, component.c_str(),
                                O_RDONLY | O_DIRECTORY | O_NOFOLLOW |
                                    O_CLOEXEC);
        if (fd < 0) {
            const ErrorCode code =
                (errno == ELOOP || errno == ENOTDIR)
                    ? ErrorCode::SymlinkInPath
                    : ErrorCode::IoError;
            error = make_error(code,
                               std::string("cannot open parent directory: ") +
                                   std::strerror(errno),
                               component);
            return {};
        }
        if (created != nullptr) {
            *created = true;
        }
        if (::fchmod(fd, native_mode) != 0) {
            ::close(fd);
            error = make_error(ErrorCode::IoError,
                               std::string("cannot set parent directory permissions: ") +
                                   std::strerror(errno),
                               component);
            return {};
        }
        return FileDescriptor(fd);
    }

    const ErrorCode code =
        (errno == ELOOP || errno == ENOTDIR)
            ? ErrorCode::SymlinkInPath
            : ErrorCode::IoError;
    error = make_error(code,
                       std::string("cannot open parent directory: ") +
                           std::strerror(errno),
                       component);
    return {};
}

struct ParentHandle {
    FileDescriptor descriptor;
    std::string leaf;
};

struct ParentResult {
    ParentHandle value;
    Error error;

    [[nodiscard]] explicit operator bool() const noexcept {
        return !error;
    }
};

ParentResult open_parent(int root_fd, const ParsedPath& path,
                         const ExtractOptions& options) {
    // FileDescriptor owns only descriptors it opened. Duplicate the root so
    // repeated calls cannot close the caller's descriptor.
    FileDescriptor current(::dup(root_fd));
    if (!current) {
        return {{}, make_error(ErrorCode::IoError,
                               std::string("cannot duplicate root descriptor: ") +
                                   std::strerror(errno))};
    }

    if (path.components.empty()) {
        return {{}, make_error(ErrorCode::InvalidPath,
                               "archive entry path is empty")};
    }

    for (std::size_t i = 0; i + 1 < path.components.size(); ++i) {
        bool created = false;
        Error error;
        FileDescriptor next =
            open_or_create_directory(current.get(), path.components[i],
                                     options.create_parent_directories,
                                     directory_default_permissions(options),
                                     &created, error);
        if (!next) {
            return {{}, std::move(error)};
        }
        current = std::move(next);
    }

    ParentHandle handle;
    handle.descriptor = std::move(current);
    handle.leaf = path.components.back();
    return {std::move(handle), {}};
}

struct LeafStatus {
    bool exists = false;
    bool is_directory = false;
    bool is_regular = false;
    bool is_symlink = false;
    mode_t mode = 0;
};

struct LeafResult {
    LeafStatus value;
    Error error;

    [[nodiscard]] explicit operator bool() const noexcept {
        return !error;
    }
};

LeafResult inspect_leaf(int parent_fd, const std::string& leaf) {
    struct stat st {};
    if (::fstatat(parent_fd, leaf.c_str(), &st, AT_SYMLINK_NOFOLLOW) == 0) {
        LeafStatus status;
        status.exists = true;
        status.is_directory = S_ISDIR(st.st_mode);
        status.is_regular = S_ISREG(st.st_mode);
        status.is_symlink = S_ISLNK(st.st_mode);
        status.mode = st.st_mode;
        return {status, {}};
    }
    if (errno == ENOENT) {
        return {LeafStatus{}, {}};
    }
    return {{}, make_error(ErrorCode::IoError,
                           std::string("cannot inspect destination entry: ") +
                               std::strerror(errno),
                           leaf)};
}

std::string make_temp_name() {
    const auto now = std::chrono::steady_clock::now().time_since_epoch();
    const std::uint64_t ticks = static_cast<std::uint64_t>(
        std::chrono::duration_cast<std::chrono::nanoseconds>(now).count());
    const std::uint64_t counter =
        g_temp_counter.fetch_add(1, std::memory_order_relaxed);
    std::ostringstream stream;
    stream << ".netsec-tmp-" << static_cast<std::uint64_t>(::getpid()) << '-'
           << counter << '-' << ticks;
    return stream.str();
}

struct TempName {
    std::string name;
    bool active = false;
};

void remove_temp(int parent_fd, TempName& temp_name) {
    if (temp_name.active) {
        ::unlinkat(parent_fd, temp_name.name.c_str(), 0);
        temp_name.active = false;
    }
}

struct TempFile {
    FileDescriptor descriptor;
    std::string name;
};

struct TempFileResult {
    TempFile value;
    Error error;

    [[nodiscard]] explicit operator bool() const noexcept {
        return !error;
    }
};

TempFileResult create_temp_file(int parent_fd, TempName& temp_name) {
    for (std::size_t attempt = 0; attempt < kMaxTempAttempts; ++attempt) {
        std::string name = make_temp_name();
        const int fd = ::openat(parent_fd, name.c_str(),
                                O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW |
                                    O_CLOEXEC,
                                S_IRUSR | S_IWUSR);
        if (fd >= 0) {
            temp_name.name = std::move(name);
            temp_name.active = true;
            return {{FileDescriptor(fd), temp_name.name}, {}};
        }
        if (errno != EEXIST) {
            return {{}, make_error(ErrorCode::IoError,
                                   std::string("cannot create temporary file: ") +
                                       std::strerror(errno))};
        }
    }
    return {{}, make_error(ErrorCode::IoError,
                           "cannot allocate a unique temporary file name")};
}

Error write_all(int fd, const std::string& data) {
    const char* cursor = data.data();
    std::size_t remaining = data.size();
    while (remaining != 0) {
        const ssize_t written = ::write(fd, cursor, remaining);
        if (written < 0) {
            if (errno == EINTR) {
                continue;
            }
            return make_error(ErrorCode::IoError,
                              std::string("cannot write temporary file: ") +
                                  std::strerror(errno));
        }
        if (written == 0) {
            return make_error(ErrorCode::IoError,
                              "write to temporary file made no progress");
        }
        cursor += written;
        remaining -= static_cast<std::size_t>(written);
    }
    return {};
}

Error sync_parent(int parent_fd) {
    if (::fsync(parent_fd) != 0) {
        return make_error(ErrorCode::IoError,
                          std::string("cannot fsync destination directory: ") +
                              std::strerror(errno));
    }
    return {};
}

Error finish_temp_file(int parent_fd, TempFile& temp,
                       const std::string& leaf,
                       std::filesystem::perms permissions,
                       const ExtractOptions& options, TempName& temp_name) {
    if (::fchmod(temp.descriptor.get(), to_mode(permissions)) != 0) {
        remove_temp(parent_fd, temp_name);
        return make_error(ErrorCode::IoError,
                          std::string("cannot set file permissions: ") +
                              std::strerror(errno),
                          leaf);
    }
    if (options.fsync_data && ::fsync(temp.descriptor.get()) != 0) {
        remove_temp(parent_fd, temp_name);
        return make_error(ErrorCode::IoError,
                          std::string("cannot fsync temporary file: ") +
                              std::strerror(errno),
                          leaf);
    }
    temp.descriptor.reset();

    if (::renameat(parent_fd, temp.name.c_str(), parent_fd, leaf.c_str()) !=
        0) {
        const ErrorCode code = (errno == EISDIR || errno == ENOTDIR)
                                   ? ErrorCode::DestinationNotDirectory
                                   : ErrorCode::IoError;
        remove_temp(parent_fd, temp_name);
        return make_error(code,
                          std::string("cannot atomically replace destination: ") +
                              std::strerror(errno),
                          leaf);
    }
    temp_name.active = false;

    if (options.fsync_parent_directory) {
        Error sync_error = sync_parent(parent_fd);
        if (sync_error) {
            sync_error.path = leaf;
            return sync_error;
        }
    }
    return {};
}

std::filesystem::perms effective_file_permissions(
    const ArchiveEntry& entry, const ExtractOptions& options) {
    if (!options.preserve_permissions || !entry.permissions.has_value()) {
        return file_default_permissions(options);
    }
    return *entry.permissions;
}

std::filesystem::perms effective_directory_permissions(
    const ArchiveEntry& entry, const ExtractOptions& options) {
    if (!options.preserve_permissions || !entry.permissions.has_value()) {
        return directory_default_permissions(options);
    }
    return *entry.permissions;
}

Error extract_regular_file(int root_fd, const PlanEntry& planned,
                           const ExtractOptions& options) {
    ParentResult parent = open_parent(root_fd, planned.path, options);
    if (!parent) {
        return parent.error;
    }

    LeafResult leaf = inspect_leaf(parent.value.descriptor.get(),
                                   parent.value.leaf);
    if (!leaf) {
        return leaf.error;
    }
    if (leaf.value.is_directory) {
        return make_error(ErrorCode::DestinationNotDirectory,
                          "cannot replace a directory with a regular file",
                          planned.path.normalized);
    }
    if (leaf.value.exists && !options.replace_existing_files) {
        return make_error(ErrorCode::DestinationExists,
                          "destination entry already exists",
                          planned.path.normalized);
    }

    if (planned.entry.declared_size != 0 &&
        planned.entry.declared_size != planned.entry.data.size()) {
        return make_error(ErrorCode::SizeMismatch,
                          "declared size does not match entry data",
                          planned.path.normalized);
    }

    TempName temp_name;
    TempFileResult temp =
        create_temp_file(parent.value.descriptor.get(), temp_name);
    if (!temp) {
        return temp.error;
    }

    Error write_error = write_all(temp.value.descriptor.get(),
                                  planned.entry.data);
    if (write_error) {
        remove_temp(parent.value.descriptor.get(), temp_name);
        write_error.path = planned.path.normalized;
        return write_error;
    }

    Error finish_error =
        finish_temp_file(parent.value.descriptor.get(), temp.value,
                         parent.value.leaf,
                         effective_file_permissions(planned.entry, options),
                         options, temp_name);
    if (finish_error) {
        finish_error.path = planned.path.normalized;
        return finish_error;
    }
    return {};
}

Error extract_directory(int root_fd, const PlanEntry& planned,
                        const ExtractOptions& options) {
    ParentResult parent = open_parent(root_fd, planned.path, options);
    if (!parent) {
        return parent.error;
    }

    LeafResult leaf = inspect_leaf(parent.value.descriptor.get(),
                                   parent.value.leaf);
    if (!leaf) {
        return leaf.error;
    }
    if (leaf.value.exists && !leaf.value.is_directory) {
        return make_error(ErrorCode::EntryConflict,
                          "cannot replace a non-directory with a directory",
                          planned.path.normalized);
    }

    std::filesystem::perms mode =
        effective_directory_permissions(planned.entry, options);
    const std::filesystem::perms working_mode =
        mode | std::filesystem::perms::owner_all;

    Error open_error;
    FileDescriptor directory = open_or_create_directory(
        parent.value.descriptor.get(), parent.value.leaf, true, working_mode,
        nullptr, open_error);
    if (!directory) {
        open_error.path = planned.path.normalized;
        return open_error;
    }

    // Keep owner write/search access until all children have been installed.
    // The declared final mode is restored after the extraction pass.
    if (::fchmod(directory.get(), to_mode(working_mode)) != 0) {
        return make_error(ErrorCode::IoError,
                          std::string("cannot set working directory permissions: ") +
                              std::strerror(errno),
                          planned.path.normalized);
    }
    return {};
}

Error apply_directory_permissions(int root_fd, const PlanEntry& planned,
                                  const ExtractOptions& options) {
    ParentResult parent = open_parent(root_fd, planned.path, options);
    if (!parent) {
        return parent.error;
    }

    const int fd = ::openat(parent.value.descriptor.get(),
                            parent.value.leaf.c_str(),
                            O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (fd < 0) {
        return make_error(ErrorCode::SymlinkInPath,
                          std::string("cannot reopen extracted directory: ") +
                              std::strerror(errno),
                          planned.path.normalized);
    }
    FileDescriptor directory(fd);

    const std::filesystem::perms mode =
        effective_directory_permissions(planned.entry, options);
    if (::fchmod(directory.get(), to_mode(mode)) != 0) {
        return make_error(ErrorCode::IoError,
                          std::string("cannot set final directory permissions: ") +
                              std::strerror(errno),
                          planned.path.normalized);
    }
    return {};
}

Error extract_hard_link(int root_fd, const PlanEntry& planned,
                        const ExtractOptions& options,
                        const Plan& plan) {
    const std::string& target = planned.target_path->normalized;
    auto target_it = plan.index.find(target);
    if (target_it == plan.index.end()) {
        return make_error(ErrorCode::LinkTargetMissingFromArchive,
                          "hard link target is not an archive member",
                          planned.path.normalized);
    }
    PathParse target_parsed = parse_member_path(target, true);
    if (!target_parsed) {
        return target_parsed.error;
    }

    ParentResult source_parent =
        open_parent(root_fd, target_parsed.value, options);
    if (!source_parent) {
        return source_parent.error;
    }
    LeafResult source = inspect_leaf(source_parent.value.descriptor.get(),
                                     source_parent.value.leaf);
    if (!source) {
        return source.error;
    }
    if (!source.value.exists) {
        return make_error(ErrorCode::LinkTargetNotExtracted,
                          "hard link target does not exist on disk",
                          planned.path.normalized);
    }
    if (!source.value.is_regular) {
        return make_error(ErrorCode::LinkTargetWrongType,
                          "hard link target is not a regular file on disk",
                          planned.path.normalized);
    }
    if (planned.entry.permissions.has_value() &&
        to_mode(*planned.entry.permissions) != (source.value.mode & 0777)) {
        return make_error(ErrorCode::PermissionMismatch,
                          "hard link permissions differ from target permissions",
                          planned.path.normalized);
    }

    ParentResult destination =
        open_parent(root_fd, planned.path, options);
    if (!destination) {
        return destination.error;
    }
    LeafResult leaf = inspect_leaf(destination.value.descriptor.get(),
                                   destination.value.leaf);
    if (!leaf) {
        return leaf.error;
    }
    if (leaf.value.is_directory) {
        return make_error(ErrorCode::DestinationNotDirectory,
                          "cannot replace a directory with a hard link",
                          planned.path.normalized);
    }
    if (leaf.value.exists && !options.replace_existing_files) {
        return make_error(ErrorCode::DestinationExists,
                          "destination entry already exists",
                          planned.path.normalized);
    }

    TempName temp_name;
    for (std::size_t attempt = 0; attempt < kMaxTempAttempts; ++attempt) {
        const std::string name = make_temp_name();
        if (::linkat(source_parent.value.descriptor.get(),
                     source_parent.value.leaf.c_str(),
                     destination.value.descriptor.get(), name.c_str(), 0) ==
            0) {
            temp_name.name = name;
            temp_name.active = true;
            break;
        }
        if (errno != EEXIST) {
            return make_error(ErrorCode::IoError,
                              std::string("cannot create hard link: ") +
                                  std::strerror(errno),
                              planned.path.normalized);
        }
    }
    if (!temp_name.active) {
        return make_error(ErrorCode::IoError,
                          "cannot allocate a unique hard link name",
                          planned.path.normalized);
    }

    if (::renameat(destination.value.descriptor.get(), temp_name.name.c_str(),
                   destination.value.descriptor.get(),
                   destination.value.leaf.c_str()) != 0) {
        const ErrorCode code = (errno == EISDIR || errno == ENOTDIR)
                                   ? ErrorCode::DestinationNotDirectory
                                   : ErrorCode::IoError;
        remove_temp(destination.value.descriptor.get(), temp_name);
        return make_error(code,
                          std::string("cannot atomically install hard link: ") +
                              std::strerror(errno),
                          planned.path.normalized);
    }
    temp_name.active = false;

    if (options.fsync_parent_directory) {
        Error sync_error =
            sync_parent(destination.value.descriptor.get());
        if (sync_error) {
            sync_error.path = planned.path.normalized;
            return sync_error;
        }
    }

    return {};
}

Error extract_symbolic_link(int root_fd, const PlanEntry& planned,
                            const ExtractOptions& options,
                            const Plan& plan) {
    const std::string& target = planned.target_path->normalized;
    auto target_it = plan.index.find(target);
    if (target_it == plan.index.end()) {
        return make_error(ErrorCode::LinkTargetMissingFromArchive,
                          "symbolic link target is not an archive member",
                          planned.path.normalized);
    }

    PathParse target_parsed = parse_member_path(target, true);
    if (!target_parsed) {
        return target_parsed.error;
    }
    ParentResult target_parent =
        open_parent(root_fd, target_parsed.value, options);
    if (!target_parent) {
        return target_parent.error;
    }
    LeafResult target_leaf = inspect_leaf(target_parent.value.descriptor.get(),
                                          target_parent.value.leaf);
    if (!target_leaf) {
        return target_leaf.error;
    }
    if (!target_leaf.value.exists ||
        !(target_leaf.value.is_regular || target_leaf.value.is_directory)) {
        return make_error(ErrorCode::LinkTargetNotExtracted,
                          "symbolic link target does not exist as an archive file or directory",
                          planned.path.normalized);
    }

    ParentResult destination =
        open_parent(root_fd, planned.path, options);
    if (!destination) {
        return destination.error;
    }
    LeafResult leaf = inspect_leaf(destination.value.descriptor.get(),
                                   destination.value.leaf);
    if (!leaf) {
        return leaf.error;
    }
    if (leaf.value.is_directory) {
        return make_error(ErrorCode::DestinationNotDirectory,
                          "cannot replace a directory with a symbolic link",
                          planned.path.normalized);
    }
    if (leaf.value.exists && !options.replace_existing_files) {
        return make_error(ErrorCode::DestinationExists,
                          "destination entry already exists",
                          planned.path.normalized);
    }

    TempName temp_name;
    const std::string link_text =
        relative_link_text(planned.path, *planned.target_path);

    for (std::size_t attempt = 0; attempt < kMaxTempAttempts; ++attempt) {
        const std::string name = make_temp_name();
        if (::symlinkat(link_text.c_str(), destination.value.descriptor.get(),
                        name.c_str()) == 0) {
            temp_name.name = name;
            temp_name.active = true;
            break;
        }
        if (errno != EEXIST) {
            return make_error(ErrorCode::IoError,
                              std::string("cannot create symbolic link: ") +
                                  std::strerror(errno),
                              planned.path.normalized);
        }
    }
    if (!temp_name.active) {
        return make_error(ErrorCode::IoError,
                          "cannot allocate a unique symbolic link name",
                          planned.path.normalized);
    }

    if (::renameat(destination.value.descriptor.get(), temp_name.name.c_str(),
                   destination.value.descriptor.get(),
                   destination.value.leaf.c_str()) != 0) {
        const ErrorCode code = (errno == EISDIR || errno == ENOTDIR)
                                   ? ErrorCode::DestinationNotDirectory
                                   : ErrorCode::IoError;
        remove_temp(destination.value.descriptor.get(), temp_name);
        return make_error(
            code,
            std::string("cannot atomically install symbolic link: ") +
                std::strerror(errno),
            planned.path.normalized);
    }
    temp_name.active = false;

    if (options.fsync_parent_directory) {
        Error sync_error =
            sync_parent(destination.value.descriptor.get());
        if (sync_error) {
            sync_error.path = planned.path.normalized;
            return sync_error;
        }
    }
    return {};
}

} // namespace

NormalizedPath normalize_archive_path(std::string_view path) {
    PathParse parsed = parse_member_path(path, true);
    if (!parsed) {
        return {{}, std::move(parsed.error)};
    }
    return {std::move(parsed.value.normalized), {}};
}

ArchiveExtractor::ArchiveExtractor(ExtractOptions options)
    : options_(std::move(options)) {}

ExtractResult ArchiveExtractor::extract(
    const std::filesystem::path& destination,
    const std::vector<ArchiveEntry>& entries) const {
    ExtractResult result;

    PlanResult plan_result = build_plan(entries, options_);
    if (!plan_result) {
        result.error = std::move(plan_result.error);
        return result;
    }

    Error root_error;
    FileDescriptor root =
        open_root_directory(destination, options_, root_error);
    if (!root) {
        result.error = std::move(root_error);
        return result;
    }

    const Plan& plan = plan_result.value;
    for (std::size_t i = 0; i < plan.entries.size(); ++i) {
        const PlanEntry& planned = plan.entries[i];
        Error error;

        switch (planned.entry.type) {
        case EntryType::RegularFile:
            error = extract_regular_file(root.get(), planned, options_);
            break;
        case EntryType::Directory:
            error = extract_directory(root.get(), planned, options_);
            break;
        case EntryType::HardLink:
            error = extract_hard_link(root.get(), planned, options_, plan);
            break;
        case EntryType::SymbolicLink:
            error = extract_symbolic_link(root.get(), planned, options_, plan);
            break;
        default:
            error = make_error(ErrorCode::UnsupportedEntryType,
                               "unsupported archive entry type",
                               planned.path.normalized);
            break;
        }

        if (error) {
            if (error.path.empty()) {
                error.path = planned.path.normalized;
            }
            result.error = std::move(error);
            return result;
        }
        result.extracted.push_back(planned.path.normalized);
    }

    std::vector<std::size_t> directories;
    directories.reserve(plan.entries.size());
    for (std::size_t i = 0; i < plan.entries.size(); ++i) {
        if (plan.entries[i].entry.type == EntryType::Directory) {
            directories.push_back(i);
        }
    }
    std::sort(directories.begin(), directories.end(),
              [&plan](std::size_t left, std::size_t right) {
                  const auto& left_path = plan.entries[left].path.components;
                  const auto& right_path = plan.entries[right].path.components;
                  return left_path.size() > right_path.size();
              });
    for (std::size_t index : directories) {
        Error error = apply_directory_permissions(
            root.get(), plan.entries[index], options_);
        if (error) {
            result.error = std::move(error);
            return result;
        }
    }

    return result;
}

ExtractResult extract_archive(const std::filesystem::path& destination,
                              const std::vector<ArchiveEntry>& entries,
                              ExtractOptions options) {
    return ArchiveExtractor(std::move(options)).extract(destination, entries);
}

} // namespace netsec
