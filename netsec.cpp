#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <optional>
#include <string>
#include <string_view>
#include <utility>

namespace netsec {
namespace {

constexpr std::array<std::uint32_t, 64> kSha256Constants{
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU,
    0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U, 0xd807aa98U, 0x12835b01U,
    0x243185beU, 0x550c7dc3U, 0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U,
    0xc19bf174U, 0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
    0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU, 0x983e5152U,
    0xa831c66dU, 0xb00327c8U, 0xbf597fc7U, 0xc6e00bf3U, 0xd5a79147U,
    0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU,
    0x53380d13U, 0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
    0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U, 0xd192e819U,
    0xd6990624U, 0xf40e3585U, 0x106aa070U, 0x19a4c116U, 0x1e376c08U,
    0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU,
    0x682e6ff3U, 0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U,
};

constexpr std::uint32_t rotate_right(std::uint32_t value, unsigned int count) {
    return (value >> count) | (value << (32U - count));
}

class Sha256 {
public:
    void update(std::string_view input) {
        for (const char character : input) {
            buffer_[buffer_length_++] = static_cast<std::uint8_t>(
                static_cast<unsigned char>(character));
            bit_length_ += 8U;
            if (buffer_length_ == buffer_.size()) {
                transform();
                buffer_length_ = 0;
            }
        }
    }

    [[nodiscard]] std::string finish() {
        const std::uint64_t original_bit_length = bit_length_;
        buffer_[buffer_length_++] = 0x80U;

        if (buffer_length_ > 56U) {
            while (buffer_length_ < buffer_.size()) {
                buffer_[buffer_length_++] = 0U;
            }
            transform();
            buffer_length_ = 0;
        }

        while (buffer_length_ < 56U) {
            buffer_[buffer_length_++] = 0U;
        }

        for (int shift = 56; shift >= 0; shift -= 8) {
            buffer_[buffer_length_++] = static_cast<std::uint8_t>(
                (original_bit_length >> static_cast<unsigned int>(shift)) & 0xffU);
        }
        transform();

        std::string output;
        output.reserve(64);
        constexpr char hex_digits[] = "0123456789abcdef";
        for (const std::uint32_t state : state_) {
            for (int shift = 28; shift >= 0; shift -= 4) {
                output.push_back(hex_digits[(state >> static_cast<unsigned int>(shift)) & 0x0fU]);
            }
        }
        return output;
    }

private:
    void transform() {
        std::array<std::uint32_t, 64> words{};
        for (std::size_t index = 0; index < 16U; ++index) {
            const std::size_t offset = index * 4U;
            words[index] =
                (static_cast<std::uint32_t>(buffer_[offset]) << 24U) |
                (static_cast<std::uint32_t>(buffer_[offset + 1U]) << 16U) |
                (static_cast<std::uint32_t>(buffer_[offset + 2U]) << 8U) |
                static_cast<std::uint32_t>(buffer_[offset + 3U]);
        }
        for (std::size_t index = 16U; index < words.size(); ++index) {
            const std::uint32_t sigma0 =
                rotate_right(words[index - 15U], 7U) ^
                rotate_right(words[index - 15U], 18U) ^
                (words[index - 15U] >> 3U);
            const std::uint32_t sigma1 =
                rotate_right(words[index - 2U], 17U) ^
                rotate_right(words[index - 2U], 19U) ^
                (words[index - 2U] >> 10U);
            words[index] =
                words[index - 16U] + sigma0 + words[index - 7U] + sigma1;
        }

        std::uint32_t first = state_[0];
        std::uint32_t second = state_[1];
        std::uint32_t third = state_[2];
        std::uint32_t fourth = state_[3];
        std::uint32_t fifth = state_[4];
        std::uint32_t sixth = state_[5];
        std::uint32_t seventh = state_[6];
        std::uint32_t eighth = state_[7];

        for (std::size_t index = 0; index < words.size(); ++index) {
            const std::uint32_t choice =
                (fifth & sixth) ^ ((~fifth) & seventh);
            const std::uint32_t majority =
                (first & second) ^ (first & third) ^ (second & third);
            const std::uint32_t sum0 =
                rotate_right(first, 2U) ^ rotate_right(first, 13U) ^ rotate_right(first, 22U);
            const std::uint32_t sum1 =
                rotate_right(fifth, 6U) ^ rotate_right(fifth, 11U) ^ rotate_right(fifth, 25U);
            const std::uint32_t temporary1 =
                eighth + sum1 + choice + kSha256Constants[index] + words[index];
            const std::uint32_t temporary2 = sum0 + majority;

            eighth = seventh;
            seventh = sixth;
            sixth = fifth;
            fifth = fourth + temporary1;
            fourth = third;
            third = second;
            second = first;
            first = temporary1 + temporary2;
        }

        state_[0] += first;
        state_[1] += second;
        state_[2] += third;
        state_[3] += fourth;
        state_[4] += fifth;
        state_[5] += sixth;
        state_[6] += seventh;
        state_[7] += eighth;
    }

    std::array<std::uint32_t, 8> state_{
        0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
        0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U,
    };
    std::array<std::uint8_t, 64> buffer_{};
    std::size_t buffer_length_ = 0;
    std::uint64_t bit_length_ = 0;
};

[[nodiscard]] std::string trim_ascii(std::string_view input) {
    std::size_t start = 0;
    while (start < input.size() &&
           std::isspace(static_cast<unsigned char>(input[start])) != 0) {
        ++start;
    }
    std::size_t end = input.size();
    while (end > start &&
           std::isspace(static_cast<unsigned char>(input[end - 1U])) != 0) {
        --end;
    }
    return std::string(input.substr(start, end - start));
}

[[nodiscard]] std::string lower_ascii(std::string_view input) {
    std::string output;
    output.reserve(input.size());
    for (const char character : input) {
        output.push_back(static_cast<char>(
            std::tolower(static_cast<unsigned char>(character))));
    }
    return output;
}

[[nodiscard]] bool has_control_characters(std::string_view input) {
    for (const char character : input) {
        const auto value = static_cast<unsigned char>(character);
        if (value == 0U || value < 0x20U || value == 0x7fU) {
            return true;
        }
    }
    return false;
}

[[nodiscard]] bool has_parent_segment(const std::filesystem::path& input) {
    for (const auto& component : input) {
        if (component == "..") {
            return true;
        }
    }
    return false;
}

[[nodiscard]] std::optional<std::filesystem::path> normalize_absolute_path(
    const std::filesystem::path& input) {
    if (input.empty()) {
        return std::nullopt;
    }
    const std::string raw = input.generic_string();
    if (has_control_characters(raw) || !input.is_absolute()) {
        return std::nullopt;
    }
    std::filesystem::path normalized = input.lexically_normal();
    std::string text = normalized.generic_string();
    while (text.size() > 1U && text.back() == '/') {
        text.pop_back();
    }
    if (text.empty() || text.front() != '/') {
        return std::nullopt;
    }
    return std::filesystem::path(text);
}

[[nodiscard]] std::string path_text(const std::filesystem::path& input) {
    const auto normalized = normalize_absolute_path(input);
    return (normalized ? *normalized : input).generic_string();
}

[[nodiscard]] bool is_within(
    const std::filesystem::path& child,
    const std::filesystem::path& root) {
    auto child_part = child.begin();
    for (auto root_part = root.begin(); root_part != root.end(); ++root_part) {
        if (child_part == child.end() || *child_part != *root_part) {
            return false;
        }
        ++child_part;
    }
    return true;
}

[[nodiscard]] bool contains_value(
    const std::vector<std::string>& values,
    std::string_view wanted) {
    return std::find(values.begin(), values.end(), wanted) != values.end();
}

void add_alert(
    ValidationResult& result,
    const ValidationOptions& options,
    AlertCode code,
    AlertSeverity severity,
    std::filesystem::path path,
    std::string message) {
    Alert alert{code, severity, std::move(path), std::move(message)};
    result.alerts.push_back(alert);
    if (options.alert_sink) {
        try {
            options.alert_sink(alert);
        } catch (...) {
        }
    }
}

void add_alert(
    std::vector<Alert>& alerts,
    const ValidationOptions& options,
    AlertCode code,
    AlertSeverity severity,
    std::filesystem::path path,
    std::string message) {
    Alert alert{code, severity, std::move(path), std::move(message)};
    alerts.push_back(alert);
    if (options.alert_sink) {
        try {
            options.alert_sink(alert);
        } catch (...) {
        }
    }
}

void append_field(std::string& output, std::string_view name, std::string_view value) {
    output.append(name);
    output.push_back('=');
    output.append(std::to_string(value.size()));
    output.push_back(':');
    output.append(value);
    output.push_back('\n');
}

struct ParsedMountOptions {
    std::vector<std::string> controllers;
    std::optional<MountPropagation> propagation;
    bool read_only = false;
    bool writable = false;
    bool bind = false;
    bool remount = false;
    bool valid = true;
    std::string error;
};

[[nodiscard]] bool is_plain_mount_flag(std::string_view option) {
    static constexpr std::string_view flags[] = {
        "async", "atime", "auto", "defaults", "dev", "diratime", "dirsync",
        "exec", "group", "iversion", "lazytime", "loud", "mand", "noatime",
        "noauto", "nodev", "nodiratime", "noexec", "nofail", "noiversion",
        "nolazytime", "nomand", "norelatime", "nostrictatime", "nosuid",
        "nosymfollow", "nouser", "owner", "private", "rprivate", "relatime",
        "recursive", "ro", "rw", "seclabel", "shared", "rshared", "silent",
        "slave", "rslave", "suid", "sync", "user", "users",
    };
    for (const std::string_view flag : flags) {
        if (flag == option) {
            return true;
        }
    }
    return false;
}

[[nodiscard]] bool is_value_mount_flag(std::string_view option) {
    return option.starts_with("uid=") ||
           option.starts_with("gid=") ||
           option.starts_with("mode=") ||
           option.starts_with("context=") ||
           option.starts_with("rootcontext=") ||
           option.starts_with("fscontext=") ||
           option.starts_with("defcontext=");
}

[[nodiscard]] ParsedMountOptions parse_mount_options(
    const std::vector<std::string>& raw_options) {
    ParsedMountOptions parsed;
    for (const std::string& raw_option : raw_options) {
        const std::string option = lower_ascii(trim_ascii(raw_option));
        if (option.empty()) {
            continue;
        }

        if (option == "ro") {
            parsed.read_only = true;
            continue;
        }
        if (option == "rw") {
            parsed.writable = true;
            continue;
        }
        if (option == "bind" || option == "rbind") {
            parsed.bind = true;
            continue;
        }
        if (option == "remount" || option == "move") {
            parsed.remount = true;
            continue;
        }
        if (option == "shared" || option == "rshared") {
            parsed.propagation = MountPropagation::Shared;
            continue;
        }
        if (option == "slave" || option == "rslave") {
            parsed.propagation = MountPropagation::Slave;
            continue;
        }
        if (option == "private" || option == "rprivate") {
            parsed.propagation = MountPropagation::Private;
            continue;
        }
        if (option == "unbindable" || option == "runbindable") {
            parsed.propagation = MountPropagation::Unbindable;
            continue;
        }
        if (option == "all" || option == "none") {
            parsed.controllers.push_back(option);
            continue;
        }
        if (option.starts_with("name=")) {
            parsed.controllers.push_back(option);
            continue;
        }
        if (is_plain_mount_flag(option)) {
            continue;
        }
        if (is_value_mount_flag(option)) {
            continue;
        }
        if (option.find('=') != std::string::npos) {
            parsed.valid = false;
            parsed.error = "unsupported cgroup mount option: " + option;
            return parsed;
        }
        parsed.controllers.push_back(option);
    }

    std::sort(parsed.controllers.begin(), parsed.controllers.end());
    parsed.controllers.erase(
        std::unique(parsed.controllers.begin(), parsed.controllers.end()),
        parsed.controllers.end());
    return parsed;
}

[[nodiscard]] bool is_relevant_cgroup_mount(
    const CgroupMount& mount,
    const std::optional<std::filesystem::path>& hierarchy_root) {
    const std::string filesystem_type = lower_ascii(trim_ascii(mount.filesystem_type));
    const std::string source = lower_ascii(trim_ascii(mount.source));
    if (filesystem_type == "cgroup" || filesystem_type == "cgroup2" || source == "cgroup") {
        return true;
    }
    if (!hierarchy_root) {
        return false;
    }
    const auto target = normalize_absolute_path(mount.target);
    return target && is_within(*target, *hierarchy_root);
}

[[nodiscard]] MountPropagation effective_propagation(
    const CgroupMount& mount,
    const ParsedMountOptions& parsed_options) {
    if (parsed_options.propagation) {
        if (mount.propagation != MountPropagation::Private &&
            mount.propagation != MountPropagation::Unknown &&
            mount.propagation != *parsed_options.propagation) {
            return MountPropagation::Shared;
        }
        return *parsed_options.propagation;
    }
    return mount.propagation;
}

[[nodiscard]] std::string mount_fingerprint_block(const CgroupMount& mount) {
    std::vector<std::string> options;
    options.reserve(mount.options.size());
    for (const std::string& option : mount.options) {
        options.push_back(lower_ascii(trim_ascii(option)));
    }
    std::sort(options.begin(), options.end());

    std::string block;
    append_field(block, "source", lower_ascii(trim_ascii(mount.source)));
    append_field(block, "target", path_text(mount.target));
    append_field(block, "type", lower_ascii(trim_ascii(mount.filesystem_type)));
    for (const std::string& option : options) {
        append_field(block, "option", option);
    }
    append_field(block, "propagation", std::to_string(static_cast<int>(mount.propagation)));
    append_field(block, "cgroup_namespace", std::to_string(mount.cgroup_namespace_id));
    append_field(block, "mount_namespace", std::to_string(mount.mount_namespace_id));
    append_field(block, "uid", std::to_string(mount.owner_uid));
    append_field(block, "gid", std::to_string(mount.owner_gid));
    append_field(block, "symlink", mount.target_is_symlink ? "1" : "0");
    return block;
}

[[nodiscard]] std::string setting_fingerprint_block(const CgroupSetting& setting) {
    std::string block;
    append_field(block, "path", path_text(setting.path));
    append_field(block, "cgroup", path_text(setting.cgroup_path));
    append_field(block, "key", setting.key);
    append_field(block, "value", setting.value);
    append_field(block, "controller", lower_ascii(setting.controller));
    append_field(block, "cgroup_namespace", std::to_string(setting.cgroup_namespace_id));
    append_field(block, "mount_namespace", std::to_string(setting.mount_namespace_id));
    append_field(block, "uid", std::to_string(setting.owner_uid));
    append_field(block, "gid", std::to_string(setting.owner_gid));
    append_field(block, "symlink", setting.path_is_symlink ? "1" : "0");
    return block;
}

[[nodiscard]] bool matches_trusted_release_agent(
    const std::filesystem::path& candidate,
    const CgroupPolicy& policy) {
    for (const auto& trusted : policy.trusted_release_agents) {
        const auto normalized = normalize_absolute_path(trusted);
        if (normalized && *normalized == candidate) {
            return true;
        }
    }
    return false;
}

}  // namespace

std::string_view to_string(AlertCode code) noexcept {
    switch (code) {
        case AlertCode::None: return "none";
        case AlertCode::InvalidPolicy: return "invalid_policy";
        case AlertCode::InvalidPath: return "invalid_path";
        case AlertCode::PathTraversalDenied: return "path_traversal_denied";
        case AlertCode::PathOutsideDelegatedRoot: return "path_outside_delegated_root";
        case AlertCode::SymlinkDenied: return "symlink_denied";
        case AlertCode::ReleaseAgentWriteDenied: return "release_agent_write_denied";
        case AlertCode::NotifyOnReleaseWriteDenied: return "notify_on_release_write_denied";
        case AlertCode::ControlFileWriteDenied: return "control_file_write_denied";
        case AlertCode::WritableCgroupMountDenied: return "writable_cgroup_mount_denied";
        case AlertCode::UnsafeMountSource: return "unsafe_mount_source";
        case AlertCode::UnsupportedFilesystem: return "unsupported_filesystem";
        case AlertCode::ControllerNotAllowed: return "controller_not_allowed";
        case AlertCode::MissingController: return "missing_controller";
        case AlertCode::MountOverride: return "mount_override";
        case AlertCode::MountNamespaceMismatch: return "mount_namespace_mismatch";
        case AlertCode::CgroupNamespaceMismatch: return "cgroup_namespace_mismatch";
        case AlertCode::OwnershipMismatch: return "ownership_mismatch";
        case AlertCode::UnsafePropagation: return "unsafe_propagation";
        case AlertCode::ConfigChanged: return "config_changed";
        case AlertCode::UntrustedReleaseAgent: return "untrusted_release_agent";
        case AlertCode::ExitEventRejected: return "exit_event_rejected";
    }
    return "unknown";
}

std::string_view to_string(AlertSeverity severity) noexcept {
    switch (severity) {
        case AlertSeverity::Warning: return "warning";
        case AlertSeverity::Error: return "error";
        case AlertSeverity::Critical: return "critical";
    }
    return "unknown";
}

bool is_sensitive_cgroup_control_file(std::string_view file_name) noexcept {
    const std::string normalized = lower_ascii(trim_ascii(file_name));
    static constexpr std::array<std::string_view, 14> names{
        "release_agent",
        "notify_on_release",
        "cgroup.procs",
        "tasks",
        "cgroup.threads",
        "cgroup.clone_children",
        "cgroup.event_control",
        "cgroup.sane_behavior",
        "cgroup.subtree_control",
        "cgroup.controllers",
        "cgroup.freeze",
        "cgroup.kill",
        "cgroup.max.depth",
        "cgroup.max.descendants",
    };
    return std::find(names.begin(), names.end(), normalized) != names.end();
}

bool is_write_operation(ControlOperation operation) noexcept {
    return operation != ControlOperation::Read;
}

std::string config_fingerprint(const CgroupConfig& config) {
    std::vector<std::string> mount_blocks;
    mount_blocks.reserve(config.mounts.size());
    for (const CgroupMount& mount : config.mounts) {
        mount_blocks.push_back(mount_fingerprint_block(mount));
    }
    std::sort(mount_blocks.begin(), mount_blocks.end());

    std::vector<std::string> setting_blocks;
    setting_blocks.reserve(config.settings.size());
    for (const CgroupSetting& setting : config.settings) {
        setting_blocks.push_back(setting_fingerprint_block(setting));
    }
    std::sort(setting_blocks.begin(), setting_blocks.end());

    std::string canonical;
    append_field(canonical, "schema", "netsec-cgroup-v1-config-1");
    append_field(canonical, "cgroup_namespace", std::to_string(config.cgroup_namespace_id));
    append_field(canonical, "mount_namespace", std::to_string(config.mount_namespace_id));
    append_field(canonical, "uid", std::to_string(config.cgroup_owner_uid));
    append_field(canonical, "gid", std::to_string(config.cgroup_owner_gid));
    for (const std::string& block : mount_blocks) {
        append_field(canonical, "mount", block);
    }
    for (const std::string& block : setting_blocks) {
        append_field(canonical, "setting", block);
    }

    Sha256 hash;
    hash.update(canonical);
    return hash.finish();
}

ValidationResult validate_cgroup_config(
    const CgroupConfig& config,
    const CgroupPolicy& policy,
    const ValidationOptions& options) {
    ValidationResult result;
    result.digest = config_fingerprint(config);

    const auto hierarchy_root = normalize_absolute_path(policy.hierarchy_root);
    const auto delegated_root = normalize_absolute_path(policy.delegated_root);
    const auto namespace_root = policy.cgroup_namespace_root.empty()
        ? delegated_root
        : normalize_absolute_path(policy.cgroup_namespace_root);

    if (!hierarchy_root) {
        add_alert(result, options, AlertCode::InvalidPolicy, AlertSeverity::Critical,
                  policy.hierarchy_root, "cgroup hierarchy root must be an absolute path");
    }
    if (!delegated_root) {
        add_alert(result, options, AlertCode::InvalidPolicy, AlertSeverity::Critical,
                  policy.delegated_root, "delegated cgroup root must be an absolute path");
    }
    if (!namespace_root) {
        add_alert(result, options, AlertCode::InvalidPolicy, AlertSeverity::Critical,
                  policy.cgroup_namespace_root, "cgroup namespace root is invalid");
    }

    if (hierarchy_root && delegated_root && !is_within(*delegated_root, *hierarchy_root)) {
        add_alert(result, options, AlertCode::InvalidPolicy, AlertSeverity::Critical,
                  *delegated_root, "delegated cgroup root escapes the hierarchy root");
    }
    if (delegated_root && namespace_root && !is_within(*delegated_root, *namespace_root)) {
        add_alert(result, options, AlertCode::InvalidPolicy, AlertSeverity::Critical,
                  *delegated_root, "delegated cgroup root is outside its namespace root");
    }

    std::vector<std::string> allowed_controllers;
    for (const std::string& raw_controller : policy.allowed_controllers) {
        const std::string controller = lower_ascii(trim_ascii(raw_controller));
        if (controller.empty() || controller == "all" || controller == "none" ||
            controller.find('/') != std::string::npos ||
            controller.find(',') != std::string::npos ||
            has_control_characters(controller)) {
            add_alert(result, options, AlertCode::InvalidPolicy, AlertSeverity::Critical,
                      policy.delegated_root, "invalid allowed controller: " + controller);
            continue;
        }
        allowed_controllers.push_back(controller);
    }
    std::sort(allowed_controllers.begin(), allowed_controllers.end());
    allowed_controllers.erase(
        std::unique(allowed_controllers.begin(), allowed_controllers.end()),
        allowed_controllers.end());
    if (allowed_controllers.empty()) {
        add_alert(result, options, AlertCode::InvalidPolicy, AlertSeverity::Critical,
                  policy.delegated_root, "no cgroup controller is allowlisted");
    }

    for (const auto& trusted : policy.trusted_release_agents) {
        const auto normalized = normalize_absolute_path(trusted);
        if (!normalized || has_parent_segment(trusted)) {
            add_alert(result, options, AlertCode::InvalidPolicy, AlertSeverity::Critical,
                      trusted, "trusted release agent must be a clean absolute path");
        }
    }

    if (!hierarchy_root || !delegated_root || !namespace_root ||
        allowed_controllers.empty()) {
        result.allowed = false;
        return result;
    }

    std::vector<std::filesystem::path> mount_targets;
    std::vector<std::pair<std::string, std::filesystem::path>> mounted_controllers;

    for (const CgroupMount& mount : config.mounts) {
        if (!is_relevant_cgroup_mount(mount, hierarchy_root)) {
            continue;
        }

        const auto target = normalize_absolute_path(mount.target);
        if (!target) {
            add_alert(result, options, AlertCode::InvalidPath, AlertSeverity::Critical,
                      mount.target, "cgroup mount target must be a clean absolute path");
            continue;
        }
        mount_targets.push_back(*target);
        if (has_parent_segment(mount.target)) {
            add_alert(result, options, AlertCode::PathTraversalDenied,
                      AlertSeverity::Critical, *target,
                      "cgroup mount target contains path traversal");
        }

        if (options.reject_symlink_paths && mount.target_is_symlink) {
            add_alert(result, options, AlertCode::SymlinkDenied, AlertSeverity::Critical,
                      *target, "cgroup mount target is a symlink");
        }
        if (!is_within(*target, *hierarchy_root)) {
            add_alert(result, options, AlertCode::MountOverride, AlertSeverity::Critical,
                      *target, "cgroup mount target escapes the managed hierarchy");
        }

        const std::string filesystem_type =
            lower_ascii(trim_ascii(mount.filesystem_type));
        if (filesystem_type != "cgroup") {
            if (filesystem_type != "cgroup2") {
                add_alert(result, options, AlertCode::MountOverride,
                          AlertSeverity::Critical, *target,
                          "non-cgroup filesystem covers the managed cgroup hierarchy");
            }
            add_alert(result, options, AlertCode::UnsupportedFilesystem, AlertSeverity::Critical,
                      *target, "only cgroups v1 filesystems are supported");
            continue;
        }

        const std::string source = lower_ascii(trim_ascii(mount.source));
        if (source != "cgroup" && source != "none") {
            add_alert(result, options, AlertCode::UnsafeMountSource, AlertSeverity::Critical,
                      *target, "cgroup mount source must be cgroup or none");
        }

        const ParsedMountOptions parsed = parse_mount_options(mount.options);
        if (!parsed.valid) {
            add_alert(result, options, AlertCode::InvalidPath, AlertSeverity::Critical,
                      *target, parsed.error);
            continue;
        }
        if (parsed.bind) {
            add_alert(result, options, AlertCode::MountOverride, AlertSeverity::Critical,
                      *target, "bind mounts cannot cover the cgroup hierarchy");
        }
        if (parsed.remount) {
            add_alert(result, options, AlertCode::MountOverride, AlertSeverity::Critical,
                      *target, "remounting an existing cgroup hierarchy is not allowed");
        }
        if (parsed.read_only && parsed.writable) {
            add_alert(result, options, AlertCode::WritableCgroupMountDenied,
                      AlertSeverity::Critical, *target,
                      "cgroup mount has conflicting ro and rw options");
        } else if (options.require_read_only_cgroup_mounts && !parsed.read_only) {
            add_alert(result, options, AlertCode::WritableCgroupMountDenied,
                      AlertSeverity::Critical, *target,
                      "cgroup hierarchy mounts must be read-only inside the container");
        }

        const MountPropagation propagation = effective_propagation(mount, parsed);
        if (options.require_private_mount_propagation &&
            propagation != MountPropagation::Private &&
            propagation != MountPropagation::Unbindable) {
            add_alert(result, options, AlertCode::UnsafePropagation, AlertSeverity::Critical,
                      *target, "cgroup mount propagation must be private");
        }

        bool has_controller = false;
        for (const std::string& controller : parsed.controllers) {
            if (controller == "none") {
                continue;
            }
            has_controller = true;
            if (controller == "all" ||
                !contains_value(allowed_controllers, controller)) {
                add_alert(result, options, AlertCode::ControllerNotAllowed,
                          AlertSeverity::Critical, *target,
                          "controller is not allowed: " + controller);
            }
            mounted_controllers.emplace_back(controller, *target);
        }
        if (!has_controller) {
            add_alert(result, options, AlertCode::MissingController, AlertSeverity::Critical,
                      *target, "cgroup mount does not expose an allowlisted controller");
        }

        if (mount.owner_uid != policy.cgroup_owner_uid ||
            mount.owner_gid != policy.cgroup_owner_gid) {
            add_alert(result, options, AlertCode::OwnershipMismatch, AlertSeverity::Critical,
                      *target, "cgroup mount ownership does not match the delegated owner");
        }
        if (mount.cgroup_namespace_id != policy.cgroup_namespace_id) {
            add_alert(result, options, AlertCode::CgroupNamespaceMismatch,
                      AlertSeverity::Critical, *target,
                      "cgroup mount belongs to a different cgroup namespace");
        }
        if (mount.mount_namespace_id != policy.mount_namespace_id) {
            add_alert(result, options, AlertCode::MountNamespaceMismatch,
                      AlertSeverity::Critical, *target,
                      "cgroup mount belongs to a different mount namespace");
        }
    }

    for (std::size_t first_index = 0; first_index < mount_targets.size(); ++first_index) {
        for (std::size_t second_index = first_index + 1U;
             second_index < mount_targets.size(); ++second_index) {
            const auto& first_target = mount_targets[first_index];
            const auto& second_target = mount_targets[second_index];
            if (is_within(first_target, second_target) ||
                is_within(second_target, first_target)) {
                add_alert(result, options, AlertCode::MountOverride, AlertSeverity::Critical,
                          first_target, "overlapping cgroup mounts can override one another");
            }
        }
    }

    for (std::size_t first_index = 0; first_index < mounted_controllers.size(); ++first_index) {
        for (std::size_t second_index = first_index + 1U;
             second_index < mounted_controllers.size(); ++second_index) {
            if (mounted_controllers[first_index].first ==
                mounted_controllers[second_index].first) {
                add_alert(result, options, AlertCode::MountOverride, AlertSeverity::Critical,
                          mounted_controllers[second_index].second,
                          "controller is exposed by more than one hierarchy mount: " +
                              mounted_controllers[first_index].first);
            }
        }
    }

    for (const CgroupSetting& setting : config.settings) {
        std::string key = trim_ascii(setting.key);
        std::optional<std::filesystem::path> file_path;
        std::optional<std::filesystem::path> cgroup_path;

        if (!setting.cgroup_path.empty()) {
            cgroup_path = normalize_absolute_path(setting.cgroup_path);
            if (!cgroup_path || has_parent_segment(setting.cgroup_path)) {
                add_alert(result, options, AlertCode::PathTraversalDenied,
                          AlertSeverity::Critical, setting.cgroup_path,
                          "cgroup setting path must be a clean absolute path");
                continue;
            }
        }

        if (!setting.path.empty()) {
            file_path = normalize_absolute_path(setting.path);
            if (!file_path || has_parent_segment(setting.path)) {
                add_alert(result, options, AlertCode::PathTraversalDenied,
                          AlertSeverity::Critical, setting.path,
                          "setting file path must be a clean absolute path");
                continue;
            }
            if (key.empty()) {
                key = file_path->filename().string();
            } else if (key.find('/') != std::string::npos) {
                const auto key_path = normalize_absolute_path(std::filesystem::path(key));
                if (!key_path || has_parent_segment(std::filesystem::path(key)) ||
                    key_path->filename() != file_path->filename()) {
                    add_alert(result, options, AlertCode::InvalidPath, AlertSeverity::Critical,
                              setting.path, "setting key and file path do not agree");
                    continue;
                }
                key = file_path->filename().string();
            }
        } else {
            if (key.empty()) {
                add_alert(result, options, AlertCode::InvalidPath, AlertSeverity::Critical,
                          setting.cgroup_path, "cgroup setting has no file name");
                continue;
            }
            if (key.find('/') != std::string::npos) {
                const auto key_path = normalize_absolute_path(std::filesystem::path(key));
                if (!key_path || has_parent_segment(std::filesystem::path(key))) {
                    add_alert(result, options, AlertCode::PathTraversalDenied,
                              AlertSeverity::Critical, setting.cgroup_path,
                              "setting key path is invalid");
                    continue;
                }
                file_path = *key_path;
                key = file_path->filename().string();
            } else {
                const std::filesystem::path base =
                    cgroup_path ? *cgroup_path : *delegated_root;
                file_path = normalize_absolute_path(base / key);
                if (!file_path) {
                    add_alert(result, options, AlertCode::InvalidPath,
                              AlertSeverity::Critical, base / key,
                              "cgroup setting file path is invalid");
                    continue;
                }
            }
        }

        if (!file_path) {
            add_alert(result, options, AlertCode::InvalidPath, AlertSeverity::Critical,
                      setting.cgroup_path, "cgroup setting file path is missing");
            continue;
        }
        if (cgroup_path && !is_within(*file_path, *cgroup_path)) {
            add_alert(result, options, AlertCode::PathOutsideDelegatedRoot,
                      AlertSeverity::Critical, *file_path,
                      "cgroup setting escapes its declared cgroup directory");
        }
        if (!is_within(*file_path, *delegated_root)) {
            add_alert(result, options, AlertCode::PathOutsideDelegatedRoot,
                      AlertSeverity::Critical, *file_path,
                      "cgroup setting escapes the delegated root");
        }
        if (!is_within(*file_path, *namespace_root)) {
            add_alert(result, options, AlertCode::CgroupNamespaceMismatch,
                      AlertSeverity::Critical, *file_path,
                      "cgroup setting is outside the cgroup namespace root");
        }
        if (options.reject_symlink_paths && setting.path_is_symlink) {
            add_alert(result, options, AlertCode::SymlinkDenied, AlertSeverity::Critical,
                      *file_path, "cgroup setting path is a symlink");
        }

        const std::string normalized_key = lower_ascii(key);
        if (normalized_key == "release_agent") {
            add_alert(result, options, AlertCode::ReleaseAgentWriteDenied,
                      AlertSeverity::Critical, *file_path,
                      "release_agent must not be writable through container configuration");
            continue;
        }
        if (normalized_key == "notify_on_release") {
            add_alert(result, options, AlertCode::NotifyOnReleaseWriteDenied,
                      AlertSeverity::Critical, *file_path,
                      "notify_on_release must not be writable through container configuration");
            continue;
        }
        if (is_sensitive_cgroup_control_file(normalized_key)) {
            add_alert(result, options, AlertCode::ControlFileWriteDenied,
                      AlertSeverity::Critical, *file_path,
                      "sensitive cgroup control file cannot be configured by a container");
            continue;
        }

        const std::size_t dot = normalized_key.find('.');
        const std::string prefix = dot == std::string::npos
            ? std::string{}
            : normalized_key.substr(0, dot);
        if (prefix.empty()) {
            add_alert(result, options, AlertCode::ControllerNotAllowed,
                      AlertSeverity::Critical, *file_path,
                      "cgroup setting has no controller prefix");
        } else if (!contains_value(allowed_controllers, prefix)) {
            add_alert(result, options, AlertCode::ControllerNotAllowed,
                      AlertSeverity::Critical, *file_path,
                      "cgroup setting uses a controller outside the allowlist: " + prefix);
        }
        if (!setting.controller.empty()) {
            const std::string declared_controller =
                lower_ascii(trim_ascii(setting.controller));
            if (!contains_value(allowed_controllers, declared_controller) ||
                (!prefix.empty() && declared_controller != prefix)) {
                add_alert(result, options, AlertCode::ControllerNotAllowed,
                          AlertSeverity::Critical, *file_path,
                          "declared controller does not match the setting");
            }
        }

        if (setting.owner_uid != policy.cgroup_owner_uid ||
            setting.owner_gid != policy.cgroup_owner_gid) {
            add_alert(result, options, AlertCode::OwnershipMismatch, AlertSeverity::Critical,
                      *file_path, "cgroup setting ownership does not match the delegated owner");
        }
        if (setting.cgroup_namespace_id != policy.cgroup_namespace_id) {
            add_alert(result, options, AlertCode::CgroupNamespaceMismatch,
                      AlertSeverity::Critical, *file_path,
                      "cgroup setting belongs to a different cgroup namespace");
        }
        if (setting.mount_namespace_id != policy.mount_namespace_id) {
            add_alert(result, options, AlertCode::MountNamespaceMismatch,
                      AlertSeverity::Critical, *file_path,
                      "cgroup setting belongs to a different mount namespace");
        }
    }

    result.allowed = result.alerts.empty();
    return result;
}

CgroupConfigGuard::CgroupConfigGuard(
    const CgroupConfig& config,
    const CgroupPolicy& policy,
    const ValidationOptions& options)
    : snapshot_(config),
      policy_(policy),
      options_(options),
      initial_(validate_cgroup_config(config, policy, options)),
      locked_(initial_.allowed) {}

bool CgroupConfigGuard::valid() const noexcept {
    return locked_;
}

const std::string& CgroupConfigGuard::digest() const noexcept {
    return initial_.digest;
}

const ValidationResult& CgroupConfigGuard::initial_validation() const noexcept {
    return initial_;
}

ValidationResult CgroupConfigGuard::verify_unchanged(
    const CgroupConfig& current) const {
    return verify_unchanged(current, options_);
}

ValidationResult CgroupConfigGuard::verify_unchanged(
    const CgroupConfig& current,
    const ValidationOptions& options) const {
    ValidationResult result;
    result.digest = config_fingerprint(current);

    if (!locked_) {
        add_alert(result, options, AlertCode::InvalidPolicy, AlertSeverity::Critical,
                  policy_.delegated_root,
                  "configuration guard was not locked by a valid configuration");
        return result;
    }

    const std::string snapshot_digest = config_fingerprint(snapshot_);
    if (result.digest != snapshot_digest) {
        add_alert(result, options, AlertCode::ConfigChanged, AlertSeverity::Critical,
                  policy_.delegated_root,
                  "cgroup configuration changed after validation; refusing creation");
        return result;
    }

    ValidationResult revalidated =
        validate_cgroup_config(current, policy_, options);
    if (!revalidated.allowed) {
        result.alerts.insert(
            result.alerts.end(), revalidated.alerts.begin(), revalidated.alerts.end());
        return result;
    }

    result.allowed = true;
    return result;
}

bool CgroupConfigGuard::authorize_creation(const CgroupConfig& current) const {
    return authorize_creation(current, options_);
}

bool CgroupConfigGuard::authorize_creation(
    const CgroupConfig& current,
    const ValidationOptions& options) const {
    return verify_unchanged(current, options).allowed;
}

AccessDecision authorize_cgroup_control_write(
    const ControlWriteRequest& request,
    const CgroupPolicy& policy,
    const ValidationOptions& options) {
    AccessDecision decision;
    const auto reject = [&](AlertCode code, std::string message) {
        decision.allowed = false;
        decision.code = code;
        decision.message = message;
        Alert alert{code, AlertSeverity::Critical, request.path, message};
        if (options.alert_sink) {
            try {
                options.alert_sink(alert);
            } catch (...) {
            }
        }
        return decision;
    };

    const auto normalized_path = normalize_absolute_path(request.path);
    const std::filesystem::path effective_path =
        normalized_path ? *normalized_path : request.path;
    const std::string file_name = lower_ascii(effective_path.filename().string());

    if (is_write_operation(request.operation) && request.from_container &&
        is_sensitive_cgroup_control_file(file_name)) {
        if (file_name == "release_agent") {
            decision.code = AlertCode::ReleaseAgentWriteDenied;
            decision.message =
                "container write to release_agent is forbidden";
        } else if (file_name == "notify_on_release") {
            decision.code = AlertCode::NotifyOnReleaseWriteDenied;
            decision.message =
                "container write to notify_on_release is forbidden";
        } else {
            decision.code = AlertCode::ControlFileWriteDenied;
            decision.message =
                "container write to a sensitive cgroup control file is forbidden";
        }

        Alert alert{decision.code, AlertSeverity::Critical, effective_path, decision.message};
        if (options.alert_sink) {
            try {
                options.alert_sink(alert);
            } catch (...) {
            }
        }
        return decision;
    }

    if (!normalized_path) {
        return reject(
            AlertCode::InvalidPath,
            "cgroup control path must be a clean absolute path");
    }
    if (options.reject_symlink_paths && request.path_is_symlink) {
        return reject(AlertCode::SymlinkDenied, "cgroup control path is a symlink");
    }
    if (has_parent_segment(request.path)) {
        return reject(
            AlertCode::PathTraversalDenied,
            "cgroup control path contains traversal");
    }

    const auto delegated_root = normalize_absolute_path(policy.delegated_root);
    if (!delegated_root) {
        return reject(AlertCode::InvalidPolicy, "delegated cgroup root is invalid");
    }
    const auto namespace_root = policy.cgroup_namespace_root.empty()
        ? delegated_root
        : normalize_absolute_path(policy.cgroup_namespace_root);
    if (!namespace_root) {
        return reject(AlertCode::InvalidPolicy, "cgroup namespace root is invalid");
    }
    if (!is_within(*normalized_path, *namespace_root)) {
        return reject(
            AlertCode::CgroupNamespaceMismatch,
            "cgroup control file is outside the cgroup namespace root");
    }

    std::optional<std::filesystem::path> delegated_cgroup = delegated_root;
    if (!request.delegated_cgroup.empty()) {
        delegated_cgroup = normalize_absolute_path(request.delegated_cgroup);
        if (!delegated_cgroup || !is_within(*delegated_cgroup, *delegated_root) ||
            has_parent_segment(request.delegated_cgroup)) {
            return reject(
                AlertCode::PathOutsideDelegatedRoot,
                "requested cgroup is outside the delegated root");
        }
    }
    if (!is_within(*normalized_path, *delegated_cgroup)) {
        return reject(
            AlertCode::PathOutsideDelegatedRoot,
            "cgroup control file is outside the delegated cgroup");
    }

    if (request.owner_uid != policy.cgroup_owner_uid ||
        request.owner_gid != policy.cgroup_owner_gid) {
        return reject(
            AlertCode::OwnershipMismatch,
            "cgroup ownership does not match the delegated owner");
    }
    if (request.cgroup_namespace_id != policy.cgroup_namespace_id) {
        return reject(
            AlertCode::CgroupNamespaceMismatch,
            "write request belongs to a different cgroup namespace");
    }
    if (request.mount_namespace_id != policy.mount_namespace_id) {
        return reject(
            AlertCode::MountNamespaceMismatch,
            "write request belongs to a different mount namespace");
    }

    if (!request.from_container && file_name == "release_agent") {
        if (request.actor_uid != 0U ||
            !matches_trusted_release_agent(*normalized_path, policy)) {
            return reject(
                AlertCode::UntrustedReleaseAgent,
                "host release_agent must be root-owned and explicitly trusted");
        }
    }

    decision.allowed = true;
    decision.code = AlertCode::None;
    decision.message = "cgroup control operation allowed";
    return decision;
}

ExitEventDecision evaluate_exit_event(
    const ExitEvent& event,
    const CgroupPolicy& policy,
    std::string_view expected_config_digest,
    const ValidationOptions& options) {
    ExitEventDecision decision;

    if (!expected_config_digest.empty() &&
        (event.config_digest.empty() || event.config_digest != expected_config_digest)) {
        add_alert(decision.alerts, options, AlertCode::ConfigChanged,
                  AlertSeverity::Critical, event.cgroup_path,
                  "exit event was produced by a different cgroup configuration");
    }

    const auto delegated_root = normalize_absolute_path(policy.delegated_root);
    const auto cgroup_path = normalize_absolute_path(event.cgroup_path);
    if (!delegated_root || !cgroup_path || has_parent_segment(event.cgroup_path) ||
        !is_within(*cgroup_path, *delegated_root)) {
        add_alert(decision.alerts, options, AlertCode::ExitEventRejected,
                  AlertSeverity::Critical, event.cgroup_path,
                  "exit event cgroup is outside the delegated root");
    }

    const auto namespace_root = policy.cgroup_namespace_root.empty()
        ? delegated_root
        : normalize_absolute_path(policy.cgroup_namespace_root);
    if (cgroup_path && namespace_root && !is_within(*cgroup_path, *namespace_root)) {
        add_alert(decision.alerts, options, AlertCode::CgroupNamespaceMismatch,
                  AlertSeverity::Critical, event.cgroup_path,
                  "exit event cgroup is outside the cgroup namespace root");
    }

    if (event.owner_uid != policy.cgroup_owner_uid ||
        event.owner_gid != policy.cgroup_owner_gid) {
        add_alert(decision.alerts, options, AlertCode::OwnershipMismatch,
                  AlertSeverity::Critical, event.cgroup_path,
                  "exit event cgroup ownership does not match the delegated owner");
    }
    if (event.cgroup_namespace_id != policy.cgroup_namespace_id) {
        add_alert(decision.alerts, options, AlertCode::CgroupNamespaceMismatch,
                  AlertSeverity::Critical, event.cgroup_path,
                  "exit event belongs to a different cgroup namespace");
    }
    if (event.mount_namespace_id != policy.mount_namespace_id) {
        add_alert(decision.alerts, options, AlertCode::MountNamespaceMismatch,
                  AlertSeverity::Critical, event.cgroup_path,
                  "exit event belongs to a different mount namespace");
    }

    if (!event.release_agent.empty()) {
        const auto release_agent = normalize_absolute_path(event.release_agent);
        const auto hierarchy_root = normalize_absolute_path(policy.hierarchy_root);
        bool trusted = false;
        if (release_agent && !event.release_agent_writable_by_container &&
            !event.release_agent_is_symlink &&
            !has_parent_segment(event.release_agent) &&
            !is_within(*release_agent, *delegated_root) &&
            (!hierarchy_root || !is_within(*release_agent, *hierarchy_root)) &&
            matches_trusted_release_agent(*release_agent, policy)) {
            trusted = true;
            decision.trusted_release_agent = release_agent->generic_string();
        }
        if (!trusted) {
            add_alert(decision.alerts, options, AlertCode::UntrustedReleaseAgent,
                      AlertSeverity::Critical, event.release_agent,
                      "release_agent is container-controlled or not explicitly trusted; "
                      "host execution is refused");
        }
    }

    decision.execute = false;
    decision.accepted = decision.alerts.empty();
    return decision;
}

}  // namespace netsec
