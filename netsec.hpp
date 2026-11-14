#pragma once

#include <cstdint>
#include <filesystem>
#include <functional>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

enum class AlertSeverity {
    Warning,
    Error,
    Critical,
};

enum class AlertCode {
    None,
    InvalidPolicy,
    InvalidPath,
    PathTraversalDenied,
    PathOutsideDelegatedRoot,
    SymlinkDenied,
    ReleaseAgentWriteDenied,
    NotifyOnReleaseWriteDenied,
    ControlFileWriteDenied,
    WritableCgroupMountDenied,
    UnsafeMountSource,
    UnsupportedFilesystem,
    ControllerNotAllowed,
    MissingController,
    MountOverride,
    MountNamespaceMismatch,
    CgroupNamespaceMismatch,
    OwnershipMismatch,
    UnsafePropagation,
    ConfigChanged,
    UntrustedReleaseAgent,
    ExitEventRejected,
};

struct Alert {
    AlertCode code = AlertCode::None;
    AlertSeverity severity = AlertSeverity::Error;
    std::filesystem::path path;
    std::string message;
};

using AlertSink = std::function<void(const Alert&)>;

[[nodiscard]] std::string_view to_string(AlertCode code) noexcept;
[[nodiscard]] std::string_view to_string(AlertSeverity severity) noexcept;

struct ValidationOptions {
    // A writable cgroup v1 mount exposes the hierarchy root and its
    // release_agent. Containers therefore get read-only hierarchy mounts.
    bool require_read_only_cgroup_mounts = true;
    bool require_private_mount_propagation = true;
    bool reject_symlink_paths = true;
    AlertSink alert_sink;
};

struct CgroupPolicy {
    std::filesystem::path hierarchy_root{"/sys/fs/cgroup"};
    std::filesystem::path delegated_root;
    std::filesystem::path cgroup_namespace_root;

    std::uint32_t cgroup_namespace_id = 0;
    std::uint32_t mount_namespace_id = 0;
    std::uint32_t cgroup_owner_uid = 0;
    std::uint32_t cgroup_owner_gid = 0;

    std::vector<std::string> allowed_controllers;
    std::vector<std::filesystem::path> trusted_release_agents;
};

enum class MountPropagation {
    Private,
    Shared,
    Slave,
    Unbindable,
    Unknown,
};

struct CgroupMount {
    std::string source{"cgroup"};
    std::filesystem::path target;
    std::string filesystem_type{"cgroup"};
    // cgroup v1 controller names and ordinary mount flags, for example:
    // {"ro", "nosuid", "nodev", "noexec", "cpu", "cpuacct"}.
    std::vector<std::string> options;
    MountPropagation propagation = MountPropagation::Private;

    std::uint32_t cgroup_namespace_id = 0;
    std::uint32_t mount_namespace_id = 0;
    std::uint32_t owner_uid = 0;
    std::uint32_t owner_gid = 0;

    bool target_is_symlink = false;
};

struct CgroupSetting {
    // If path is empty, the component forms it as cgroup_path/key. If key
    // itself is a path, its final component is used as the control file name.
    std::filesystem::path path;
    std::filesystem::path cgroup_path;
    std::string key;
    std::string value;
    std::string controller;

    std::uint32_t cgroup_namespace_id = 0;
    std::uint32_t mount_namespace_id = 0;
    std::uint32_t owner_uid = 0;
    std::uint32_t owner_gid = 0;

    bool path_is_symlink = false;
};

struct CgroupConfig {
    std::vector<CgroupMount> mounts;
    std::vector<CgroupSetting> settings;

    std::uint32_t cgroup_namespace_id = 0;
    std::uint32_t mount_namespace_id = 0;
    std::uint32_t cgroup_owner_uid = 0;
    std::uint32_t cgroup_owner_gid = 0;
};

struct ValidationResult {
    bool allowed = false;
    std::string digest;
    std::vector<Alert> alerts;

    [[nodiscard]] bool has_errors() const noexcept {
        return !alerts.empty();
    }
};

// Validates the complete mount and setting configuration before a container
// is created. Any finding makes allowed == false.
[[nodiscard]] ValidationResult validate_cgroup_config(
    const CgroupConfig& config,
    const CgroupPolicy& policy,
    const ValidationOptions& options = {});

[[nodiscard]] inline ValidationResult validate_config(
    const CgroupConfig& config,
    const CgroupPolicy& policy,
    const ValidationOptions& options = {}) {
    return validate_cgroup_config(config, policy, options);
}

[[nodiscard]] std::string config_fingerprint(const CgroupConfig& config);

// Keeps the validated configuration as a creation-time snapshot. Mutating the
// original CgroupConfig object after locking cannot silently change creation.
class CgroupConfigGuard {
public:
    CgroupConfigGuard() = default;
    CgroupConfigGuard(
        const CgroupConfig& config,
        const CgroupPolicy& policy,
        const ValidationOptions& options = {});

    [[nodiscard]] bool valid() const noexcept;
    [[nodiscard]] const std::string& digest() const noexcept;
    [[nodiscard]] const ValidationResult& initial_validation() const noexcept;

    [[nodiscard]] ValidationResult verify_unchanged(const CgroupConfig& current) const;
    [[nodiscard]] ValidationResult verify_unchanged(
        const CgroupConfig& current,
        const ValidationOptions& options) const;

    [[nodiscard]] bool authorize_creation(const CgroupConfig& current) const;
    [[nodiscard]] bool authorize_creation(
        const CgroupConfig& current,
        const ValidationOptions& options) const;

private:
    CgroupConfig snapshot_;
    CgroupPolicy policy_;
    ValidationOptions options_;
    ValidationResult initial_;
    bool locked_ = false;
};

enum class ControlOperation {
    Read,
    Write,
    Truncate,
    Chmod,
    Chown,
    Create,
    Unlink,
    Rename,
};

struct ControlWriteRequest {
    std::filesystem::path path;
    std::filesystem::path delegated_cgroup;
    ControlOperation operation = ControlOperation::Write;

    std::uint32_t actor_uid = 0;
    std::uint32_t actor_gid = 0;
    std::uint32_t owner_uid = 0;
    std::uint32_t owner_gid = 0;
    std::uint32_t cgroup_namespace_id = 0;
    std::uint32_t mount_namespace_id = 0;

    bool path_is_symlink = false;
    bool from_container = true;
};

struct AccessDecision {
    bool allowed = false;
    AlertCode code = AlertCode::None;
    std::string message;
};

[[nodiscard]] bool is_sensitive_cgroup_control_file(std::string_view file_name) noexcept;
[[nodiscard]] bool is_write_operation(ControlOperation operation) noexcept;

// Release-agent writes and the related cgroup control files are denied for
// container callers even when the caller has uid 0 in its namespace.
[[nodiscard]] AccessDecision authorize_cgroup_control_write(
    const ControlWriteRequest& request,
    const CgroupPolicy& policy,
    const ValidationOptions& options = {});

struct ExitEvent {
    std::filesystem::path cgroup_path;
    std::string release_agent;
    std::string config_digest;
    bool notify_on_release = false;

    std::uint32_t cgroup_namespace_id = 0;
    std::uint32_t mount_namespace_id = 0;
    std::uint32_t owner_uid = 0;
    std::uint32_t owner_gid = 0;

    bool release_agent_writable_by_container = false;
    bool release_agent_is_symlink = false;
};

struct ExitEventDecision {
    bool accepted = false;
    // Always false. This module is a verifier, not a process launcher.
    bool execute = false;
    std::string trusted_release_agent;
    std::vector<Alert> alerts;
};

// Evaluates a cgroups v1 release event. It never calls fork/exec/system and
// never turns release_agent content into a host command. Even a trusted agent
// is only returned for a host-owned, separately validated runtime action.
[[nodiscard]] ExitEventDecision evaluate_exit_event(
    const ExitEvent& event,
    const CgroupPolicy& policy,
    std::string_view expected_config_digest = {},
    const ValidationOptions& options = {});

}  // namespace netsec
