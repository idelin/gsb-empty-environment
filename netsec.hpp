#pragma once

#include <cstdint>
#include <filesystem>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

enum class EntryType {
    RegularFile,
    Directory,
    HardLink,
    SymbolicLink,
};

using EntryKind = EntryType;

enum class LinkPolicy {
    Reject,
    AllowWithinArchive,
};

enum class ErrorCode {
    None,
    RootInvalid,
    InvalidPath,
    AbsolutePath,
    ParentTraversal,
    DuplicateEntry,
    EntryConflict,
    UnsupportedEntryType,
    UnsafePermissions,
    PermissionMismatch,
    HardLinkRejected,
    SymbolicLinkRejected,
    LinkTargetInvalid,
    LinkTargetOutsideArchive,
    LinkTargetMissingFromArchive,
    LinkTargetNotExtracted,
    LinkTargetWrongType,
    LinkCycle,
    SymlinkInPath,
    DestinationExists,
    DestinationNotDirectory,
    SizeMismatch,
    IoError,
};

struct Error {
    ErrorCode code = ErrorCode::None;
    std::string message;
    std::string path;

    [[nodiscard]] explicit operator bool() const noexcept {
        return code != ErrorCode::None;
    }
};

struct ArchiveEntry {
    std::string path;
    EntryType type = EntryType::RegularFile;
    std::string link_target;
    std::string data;
    std::uintmax_t declared_size = 0;
    std::optional<std::filesystem::perms> permissions;
};

using Entry = ArchiveEntry;

struct ExtractOptions {
    LinkPolicy hard_links = LinkPolicy::Reject;
    LinkPolicy symbolic_links = LinkPolicy::Reject;

    bool preserve_permissions = true;
    bool allow_group_or_world_writable = false;
    bool create_root = true;
    bool create_parent_directories = true;
    bool replace_existing_files = true;
    bool fsync_data = true;
    bool fsync_parent_directory = true;

    std::filesystem::perms default_file_permissions =
        std::filesystem::perms::owner_read |
        std::filesystem::perms::owner_write |
        std::filesystem::perms::group_read |
        std::filesystem::perms::others_read;
    std::filesystem::perms default_directory_permissions =
        std::filesystem::perms::owner_all |
        std::filesystem::perms::group_read |
        std::filesystem::perms::group_exec |
        std::filesystem::perms::others_read |
        std::filesystem::perms::others_exec;
};

using Options = ExtractOptions;

struct ExtractResult {
    Error error;
    std::vector<std::string> extracted;

    [[nodiscard]] explicit operator bool() const noexcept {
        return !error;
    }

    [[nodiscard]] bool ok() const noexcept {
        return !error;
    }
};

using Result = ExtractResult;

struct NormalizedPath {
    std::string path;
    Error error;

    [[nodiscard]] explicit operator bool() const noexcept {
        return !error;
    }

    [[nodiscard]] bool ok() const noexcept {
        return !error;
    }
};

// Validates and normalizes one archive-member path. Member paths are relative,
// use '/' as the separator, and may not contain ".." components.
[[nodiscard]] NormalizedPath normalize_archive_path(std::string_view path);

class ArchiveExtractor {
public:
    explicit ArchiveExtractor(ExtractOptions options = {});

    [[nodiscard]] ExtractResult extract(
        const std::filesystem::path& destination,
        const std::vector<ArchiveEntry>& entries) const;

    [[nodiscard]] const ExtractOptions& options() const noexcept {
        return options_;
    }

private:
    ExtractOptions options_;
};

[[nodiscard]] ExtractResult extract_archive(
    const std::filesystem::path& destination,
    const std::vector<ArchiveEntry>& entries,
    ExtractOptions options = {});

} // namespace netsec
