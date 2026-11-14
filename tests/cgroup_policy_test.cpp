#include "netsec.hpp"

#include <chrono>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <string>
#include <string_view>
#include <vector>

namespace {

using namespace netsec;

struct TestContext {
    int failures = 0;

    void expect(bool condition, std::string_view description) {
        if (!condition) {
            ++failures;
            std::cerr << "FAIL: " << description << '\n';
        }
    }
};

[[nodiscard]] bool contains_alert(
    const ValidationResult& result,
    AlertCode code) {
    for (const Alert& alert : result.alerts) {
        if (alert.code == code) {
            return true;
        }
    }
    return false;
}

[[nodiscard]] bool contains_alert(
    const ExitEventDecision& decision,
    AlertCode code) {
    for (const Alert& alert : decision.alerts) {
        if (alert.code == code) {
            return true;
        }
    }
    return false;
}

[[nodiscard]] CgroupPolicy make_policy() {
    CgroupPolicy policy;
    policy.hierarchy_root = "/sys/fs/cgroup";
    policy.delegated_root = "/sys/fs/cgroup/runtime/container-a";
    policy.cgroup_namespace_root = policy.delegated_root;
    policy.cgroup_namespace_id = 17;
    policy.mount_namespace_id = 23;
    policy.cgroup_owner_uid = 0;
    policy.cgroup_owner_gid = 0;
    policy.allowed_controllers = {"cpu", "cpuacct", "memory", "pids"};
    policy.trusted_release_agents = {"/usr/libexec/netsec/release-helper"};
    return policy;
}

[[nodiscard]] CgroupMount make_mount(
    const CgroupPolicy& policy,
    std::filesystem::path target,
    std::vector<std::string> options) {
    CgroupMount mount;
    mount.source = "cgroup";
    mount.target = std::move(target);
    mount.filesystem_type = "cgroup";
    mount.options = std::move(options);
    mount.propagation = MountPropagation::Private;
    mount.cgroup_namespace_id = policy.cgroup_namespace_id;
    mount.mount_namespace_id = policy.mount_namespace_id;
    mount.owner_uid = policy.cgroup_owner_uid;
    mount.owner_gid = policy.cgroup_owner_gid;
    return mount;
}

[[nodiscard]] CgroupSetting make_setting(
    const CgroupPolicy& policy,
    std::string key,
    std::string value) {
    CgroupSetting setting;
    setting.cgroup_path = policy.delegated_root;
    setting.key = std::move(key);
    setting.value = std::move(value);
    const std::size_t dot = setting.key.find('.');
    if (dot != std::string::npos) {
        setting.controller = setting.key.substr(0, dot);
    }
    setting.cgroup_namespace_id = policy.cgroup_namespace_id;
    setting.mount_namespace_id = policy.mount_namespace_id;
    setting.owner_uid = policy.cgroup_owner_uid;
    setting.owner_gid = policy.cgroup_owner_gid;
    return setting;
}

[[nodiscard]] CgroupConfig make_legal_config(const CgroupPolicy& policy) {
    CgroupConfig config;
    config.cgroup_namespace_id = policy.cgroup_namespace_id;
    config.mount_namespace_id = policy.mount_namespace_id;
    config.cgroup_owner_uid = policy.cgroup_owner_uid;
    config.cgroup_owner_gid = policy.cgroup_owner_gid;
    config.mounts.push_back(make_mount(
        policy,
        "/sys/fs/cgroup/cpu",
        {"ro", "nosuid", "nodev", "noexec", "cpu", "cpuacct"}));
    config.mounts.push_back(make_mount(
        policy,
        "/sys/fs/cgroup/memory",
        {"ro", "nosuid", "nodev", "noexec", "memory"}));
    config.mounts.push_back(make_mount(
        policy,
        "/sys/fs/cgroup/pids",
        {"ro", "nosuid", "nodev", "noexec", "pids"}));
    config.settings.push_back(make_setting(policy, "cpu.shares", "512"));
    config.settings.push_back(make_setting(policy, "cpuacct.usage", "0"));
    config.settings.push_back(make_setting(policy, "memory.limit_in_bytes", "536870912"));
    config.settings.push_back(make_setting(policy, "pids.max", "64"));
    return config;
}

[[nodiscard]] ControlWriteRequest make_write_request(
    const CgroupPolicy& policy,
    std::filesystem::path path,
    ControlOperation operation = ControlOperation::Write) {
    ControlWriteRequest request;
    request.path = std::move(path);
    request.delegated_cgroup = policy.delegated_root;
    request.operation = operation;
    request.actor_uid = 0;
    request.actor_gid = 0;
    request.owner_uid = policy.cgroup_owner_uid;
    request.owner_gid = policy.cgroup_owner_gid;
    request.cgroup_namespace_id = policy.cgroup_namespace_id;
    request.mount_namespace_id = policy.mount_namespace_id;
    request.from_container = true;
    return request;
}

void test_legal_configuration(TestContext& context, const CgroupPolicy& policy) {
    const CgroupConfig config = make_legal_config(policy);
    const ValidationResult result = validate_cgroup_config(config, policy);
    context.expect(result.allowed, "legal allowlisted controllers must be accepted");
    context.expect(result.alerts.empty(), "legal configuration must have no alerts");
    context.expect(
        config_fingerprint(config) == config_fingerprint(config),
        "configuration fingerprint must be deterministic");

    CgroupConfig reordered = config;
    std::swap(reordered.mounts[0], reordered.mounts[1]);
    std::swap(reordered.settings[0], reordered.settings[1]);
    context.expect(
        config_fingerprint(config) == config_fingerprint(reordered),
        "semantic configuration order must not change the fingerprint");
}

void test_release_agent_writes(TestContext& context, const CgroupPolicy& policy) {
    const auto release_request = make_write_request(
        policy, "/sys/fs/cgroup/release_agent");
    const AccessDecision release_decision =
        authorize_cgroup_control_write(release_request, policy);
    context.expect(!release_decision.allowed, "container release_agent write must be denied");
    context.expect(
        release_decision.code == AlertCode::ReleaseAgentWriteDenied,
        "release_agent denial must carry the specific alert code");

    const auto traversal_request = make_write_request(
        policy, "/sys/fs/cgroup/runtime/container-a/../../release_agent");
    const AccessDecision traversal_decision =
        authorize_cgroup_control_write(traversal_request, policy);
    context.expect(
        !traversal_decision.allowed,
        "path traversal must not bypass the release_agent denial");
    context.expect(
        traversal_decision.code == AlertCode::ReleaseAgentWriteDenied,
        "normalized release_agent traversal must still be identified");

    CgroupConfig config = make_legal_config(policy);
    CgroupSetting release_setting =
        make_setting(policy, "release_agent", "/tmp/container-payload");
    config.settings.push_back(release_setting);
    const ValidationResult result = validate_cgroup_config(config, policy);
    context.expect(!result.allowed, "release_agent configuration must be rejected");
    context.expect(
        contains_alert(result, AlertCode::ReleaseAgentWriteDenied),
        "release_agent configuration must produce a critical alert");
}

void test_notify_on_release_and_control_files(
    TestContext& context,
    const CgroupPolicy& policy) {
    const auto notify_request = make_write_request(
        policy, "/sys/fs/cgroup/runtime/container-a/notify_on_release");
    const AccessDecision notify_decision =
        authorize_cgroup_control_write(notify_request, policy);
    context.expect(!notify_decision.allowed, "notify_on_release write must be denied");
    context.expect(
        notify_decision.code == AlertCode::NotifyOnReleaseWriteDenied,
        "notify_on_release denial must carry the specific alert code");

    const auto procs_request = make_write_request(
        policy, "/sys/fs/cgroup/runtime/container-a/cgroup.procs");
    const AccessDecision procs_decision =
        authorize_cgroup_control_write(procs_request, policy);
    context.expect(!procs_decision.allowed, "cgroup.procs write must be denied");
    context.expect(
        procs_decision.code == AlertCode::ControlFileWriteDenied,
        "related cgroup control file denial must be reported");

    const auto read_request = make_write_request(
        policy,
        "/sys/fs/cgroup/runtime/container-a/notify_on_release",
        ControlOperation::Read);
    const AccessDecision read_decision =
        authorize_cgroup_control_write(read_request, policy);
    context.expect(read_decision.allowed, "reading a control file must not be blocked");

    CgroupConfig config = make_legal_config(policy);
    config.settings.push_back(make_setting(policy, "notify_on_release", "1"));
    const ValidationResult result = validate_cgroup_config(config, policy);
    context.expect(!result.allowed, "notify_on_release configuration must be rejected");
    context.expect(
        contains_alert(result, AlertCode::NotifyOnReleaseWriteDenied),
        "notify_on_release configuration must alert");
}

void test_mount_overrides(TestContext& context, const CgroupPolicy& policy) {
    CgroupConfig bind_config = make_legal_config(policy);
    CgroupMount bind_mount = make_mount(
        policy,
        "/sys/fs/cgroup/memory",
        {"ro", "cpu", "bind"});
    bind_mount.source = "/tmp/container-controlled";
    bind_config.mounts.push_back(bind_mount);
    const ValidationResult bind_result =
        validate_cgroup_config(bind_config, policy);
    context.expect(!bind_result.allowed, "bind mount over a cgroup target must be rejected");
    context.expect(
        contains_alert(bind_result, AlertCode::MountOverride),
        "bind mount override must raise MountOverride");

    CgroupConfig non_cgroup_config = make_legal_config(policy);
    CgroupMount tmpfs_mount = make_mount(
        policy,
        "/sys/fs/cgroup/memory",
        {"ro"});
    tmpfs_mount.filesystem_type = "tmpfs";
    non_cgroup_config.mounts.push_back(tmpfs_mount);
    const ValidationResult non_cgroup_result =
        validate_cgroup_config(non_cgroup_config, policy);
    context.expect(
        !non_cgroup_result.allowed,
        "a non-cgroup filesystem over the hierarchy must be rejected");
    context.expect(
        contains_alert(non_cgroup_result, AlertCode::MountOverride),
        "a non-cgroup hierarchy mount must raise MountOverride");

    CgroupConfig traversal_config = make_legal_config(policy);
    traversal_config.mounts[0].target =
        "/sys/fs/cgroup/escape/../cpu";
    const ValidationResult traversal_result =
        validate_cgroup_config(traversal_config, policy);
    context.expect(
        !traversal_result.allowed,
        "path traversal in a cgroup mount target must be rejected");
    context.expect(
        contains_alert(traversal_result, AlertCode::PathTraversalDenied),
        "mount target traversal must be reported");

    CgroupConfig shared_config = make_legal_config(policy);
    shared_config.mounts[0].propagation = MountPropagation::Shared;
    const ValidationResult shared_result =
        validate_cgroup_config(shared_config, policy);
    context.expect(!shared_result.allowed, "shared propagation must be rejected");
    context.expect(
        contains_alert(shared_result, AlertCode::UnsafePropagation),
        "shared propagation must raise UnsafePropagation");

    CgroupConfig writable_config = make_legal_config(policy);
    writable_config.mounts[0].options = {"rw", "nosuid", "nodev", "noexec", "cpu"};
    const ValidationResult writable_result =
        validate_cgroup_config(writable_config, policy);
    context.expect(
        !writable_result.allowed,
        "a writable cgroup hierarchy mount must be rejected");
    context.expect(
        contains_alert(writable_result, AlertCode::WritableCgroupMountDenied),
        "writable hierarchy mount must raise WritableCgroupMountDenied");
}

void test_controllers_and_ownership(
    TestContext& context,
    const CgroupPolicy& policy) {
    CgroupConfig disallowed_config = make_legal_config(policy);
    disallowed_config.mounts.push_back(make_mount(
        policy,
        "/sys/fs/cgroup/devices",
        {"ro", "devices"}));
    const ValidationResult disallowed_result =
        validate_cgroup_config(disallowed_config, policy);
    context.expect(
        !disallowed_result.allowed,
        "controllers outside the allowlist must be rejected");
    context.expect(
        contains_alert(disallowed_result, AlertCode::ControllerNotAllowed),
        "disallowed controller must raise ControllerNotAllowed");

    CgroupConfig all_config = make_legal_config(policy);
    all_config.mounts[0].options = {"ro", "all"};
    const ValidationResult all_result =
        validate_cgroup_config(all_config, policy);
    context.expect(!all_result.allowed, "the all controller shortcut must be rejected");

    CgroupConfig ownership_config = make_legal_config(policy);
    ownership_config.settings[0].owner_uid = 1000;
    const ValidationResult ownership_result =
        validate_cgroup_config(ownership_config, policy);
    context.expect(
        !ownership_result.allowed,
        "settings owned by the container must be rejected");
    context.expect(
        contains_alert(ownership_result, AlertCode::OwnershipMismatch),
        "ownership mismatch must be reported");

    CgroupConfig namespace_config = make_legal_config(policy);
    namespace_config.settings[0].cgroup_namespace_id = 99;
    const ValidationResult namespace_result =
        validate_cgroup_config(namespace_config, policy);
    context.expect(
        !namespace_result.allowed,
        "settings in a foreign cgroup namespace must be rejected");
    context.expect(
        contains_alert(namespace_result, AlertCode::CgroupNamespaceMismatch),
        "namespace mismatch must be reported");

    CgroupConfig escaped_setting_config = make_legal_config(policy);
    CgroupSetting escaped = make_setting(policy, "cpu.shares", "1024");
    escaped.cgroup_path = "/sys/fs/cgroup/runtime/container-b";
    escaped_setting_config.settings.push_back(escaped);
    const ValidationResult escaped_result =
        validate_cgroup_config(escaped_setting_config, policy);
    context.expect(
        !escaped_result.allowed,
        "settings outside the delegated root must be rejected");
    context.expect(
        contains_alert(escaped_result, AlertCode::PathOutsideDelegatedRoot),
        "delegated root escape must be reported");
}

void test_configuration_change_detection(
    TestContext& context,
    const CgroupPolicy& policy) {
    const CgroupConfig legal = make_legal_config(policy);
    CgroupConfigGuard guard(legal, policy);
    context.expect(guard.valid(), "a legal configuration must lock the guard");
    context.expect(
        guard.authorize_creation(legal),
        "unchanged configuration must be authorized for creation");

    CgroupConfig changed = legal;
    changed.settings[0].value = "2048";
    bool change_alert_observed = false;
    ValidationOptions options;
    options.alert_sink = [&change_alert_observed](const Alert& alert) {
        if (alert.code == AlertCode::ConfigChanged) {
            change_alert_observed = true;
        }
    };
    const ValidationResult change_result = guard.verify_unchanged(changed, options);
    context.expect(!change_result.allowed, "changed configuration must be rejected");
    context.expect(
        contains_alert(change_result, AlertCode::ConfigChanged),
        "changed configuration must raise ConfigChanged");
    context.expect(change_alert_observed, "changed configuration must invoke the alert sink");
    context.expect(
        !guard.authorize_creation(changed, options),
        "creation must be refused after a configuration change");

    CgroupConfig tampered = legal;
    tampered.mounts[0].options = {"rw", "cpu"};
    context.expect(
        !guard.authorize_creation(tampered),
        "post-validation mount tampering must refuse creation");
}

[[nodiscard]] std::filesystem::path make_unique_test_directory() {
    const auto suffix = std::chrono::high_resolution_clock::now()
                            .time_since_epoch()
                            .count();
    const std::filesystem::path directory =
        std::filesystem::temp_directory_path() /
        ("netsec-cgroup-test-" + std::to_string(suffix));
    std::filesystem::create_directories(directory);
    return directory;
}

void test_exit_event_never_executes_container_content(
    TestContext& context,
    const CgroupPolicy& policy) {
    const std::filesystem::path test_directory = make_unique_test_directory();
    const std::filesystem::path payload = test_directory / "container-payload";
    const std::filesystem::path execution_marker = test_directory / "host-executed";

    {
        std::ofstream payload_file(payload);
        payload_file << "#!/bin/sh\n"
                     << "printf executed > '" << execution_marker.string() << "'\n";
    }

    const CgroupConfig legal = make_legal_config(policy);
    CgroupConfigGuard guard(legal, policy);

    ExitEvent event;
    event.cgroup_path = policy.delegated_root;
    event.release_agent = payload.string();
    event.config_digest = guard.digest();
    event.notify_on_release = true;
    event.cgroup_namespace_id = policy.cgroup_namespace_id;
    event.mount_namespace_id = policy.mount_namespace_id;
    event.owner_uid = policy.cgroup_owner_uid;
    event.owner_gid = policy.cgroup_owner_gid;
    event.release_agent_writable_by_container = true;

    const ExitEventDecision decision = evaluate_exit_event(
        event, policy, guard.digest());
    context.expect(!decision.execute, "exit event must never request host execution");
    context.expect(!decision.accepted, "container-controlled release_agent must be rejected");
    context.expect(
        contains_alert(decision, AlertCode::UntrustedReleaseAgent),
        "container-controlled release_agent must raise an alert");

    if (decision.execute) {
        std::ofstream marker(execution_marker);
        marker << "executed";
    }
    context.expect(
        !std::filesystem::exists(execution_marker),
        "host must not execute container-controlled exit content");

    ExitEvent trusted_event = event;
    trusted_event.release_agent = "/usr/libexec/netsec/release-helper";
    trusted_event.release_agent_writable_by_container = false;
    const ExitEventDecision trusted_decision =
        evaluate_exit_event(trusted_event, policy, guard.digest());
    context.expect(
        !trusted_decision.execute,
        "even a trusted release agent must not be executed by the verifier");
    context.expect(trusted_decision.accepted, "a trusted host release agent may be reported");
    context.expect(
        trusted_decision.trusted_release_agent == "/usr/libexec/netsec/release-helper",
        "trusted release agent path must be returned for separate host handling");

    ExitEvent stale_event = event;
    stale_event.config_digest = "stale-digest";
    const ExitEventDecision stale_decision =
        evaluate_exit_event(stale_event, policy, guard.digest());
    context.expect(!stale_decision.accepted, "stale exit event must be rejected");
    context.expect(
        contains_alert(stale_decision, AlertCode::ConfigChanged),
        "stale exit event must raise ConfigChanged");

    std::filesystem::remove_all(test_directory);
}

}  // namespace

int main() {
    TestContext context;
    const CgroupPolicy policy = make_policy();

    test_legal_configuration(context, policy);
    test_release_agent_writes(context, policy);
    test_notify_on_release_and_control_files(context, policy);
    test_mount_overrides(context, policy);
    test_controllers_and_ownership(context, policy);
    test_configuration_change_detection(context, policy);
    test_exit_event_never_executes_container_content(context, policy);

    if (context.failures != 0) {
        std::cerr << context.failures << " test failure(s)\n";
        return 1;
    }
    std::cout << "all cgroup policy tests passed\n";
    return 0;
}
