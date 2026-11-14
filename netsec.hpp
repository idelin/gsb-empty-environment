#pragma once

#include <cstddef>
#include <cstdint>
#include <filesystem>
#include <map>
#include <mutex>
#include <optional>
#include <stdexcept>
#include <string>
#include <string_view>
#include <sys/types.h>
#include <vector>

namespace netsec {

// Failures are deliberately coarse enough for a service manager to log without
// exposing attacker-controlled file contents.
enum class EnvironmentLoadError {
    open_failed,
    symlink,
    not_regular_file,
    wrong_owner,
    unsafe_permissions,
    file_too_large,
    line_too_long,
    invalid_syntax,
    invalid_variable_name,
    duplicate_variable,
    variable_not_allowed,
    forbidden_variable,
    changed_during_read,
    read_failed,
};

class EnvironmentException : public std::runtime_error {
public:
    EnvironmentException(EnvironmentLoadError code, std::string message);

    [[nodiscard]] EnvironmentLoadError code() const noexcept;

private:
    EnvironmentLoadError code_;
};

struct EnvironmentPolicy {
    // The UID that is allowed to own this environment file. In a privileged
    // service this should normally be 0. When unset, the caller's effective
    // UID is used.
    std::optional<uid_t> trusted_owner{};

    // Exact variable names that this service accepts. This is an allowlist,
    // not a denylist. Dangerous names remain forbidden even when listed here.
    std::vector<std::string> allowed_variables{};

    std::size_t max_line_bytes = 4096;
    std::size_t max_file_bytes = 64 * 1024;
    bool allow_comments = true;
};

struct EnvironmentSnapshot {
    std::map<std::string, std::string, std::less<>> variables;

    std::uint64_t device = 0;
    std::uint64_t inode = 0;
    std::uint64_t size = 0;
    std::uint64_t owner = 0;
    std::uint32_t mode = 0;

    // Lower-case hexadecimal SHA-256 of the exact bytes that were parsed.
    std::string sha256;
    std::uint64_t generation = 0;
};

// Importable, all-or-nothing environment file loader for a C++20 service
// manager. load() reopens and revalidates the path every time; a failed load
// never installs a partial snapshot and never changes the current generation.
class EnvironmentFileLoader {
public:
    explicit EnvironmentFileLoader(std::filesystem::path path,
                                   EnvironmentPolicy policy = {});

    [[nodiscard]] EnvironmentSnapshot load();
    [[nodiscard]] std::optional<EnvironmentSnapshot> current() const;
    [[nodiscard]] const std::filesystem::path& path() const noexcept;

private:
    std::filesystem::path path_;
    EnvironmentPolicy policy_;
    std::optional<EnvironmentSnapshot> current_;
    mutable std::mutex mutex_;
};

// Dynamic-linker, search-path and interpreter/launcher variables are never
// accepted, regardless of EnvironmentPolicy::allowed_variables.
[[nodiscard]] bool is_forbidden_environment_variable(
    std::string_view name) noexcept;

} // namespace netsec
