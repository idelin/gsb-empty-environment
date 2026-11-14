#include "netsec.hpp"

#include <chrono>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <string>
#include <sys/stat.h>
#include <unistd.h>

namespace fs = std::filesystem;

namespace {

int failures = 0;

void check(bool condition, const std::string& message) {
    if (!condition) {
        std::cerr << "FAIL: " << message << '\n';
        ++failures;
    }
}

class TestDirectory {
public:
    TestDirectory() {
        const auto suffix =
            std::to_string(::getpid()) + "-" +
            std::to_string(std::chrono::steady_clock::now()
                               .time_since_epoch()
                               .count());
        path_ = fs::temp_directory_path() / ("netsec-test-" + suffix);
        fs::create_directories(path_);
        ::chmod(path_.c_str(), 0700);
    }

    ~TestDirectory() {
        std::error_code ignored;
        fs::remove_all(path_, ignored);
    }

    [[nodiscard]] const fs::path& path() const { return path_; }

private:
    fs::path path_;
};

void writeRule(const fs::path& directory, const std::string& name,
               const std::string& content, mode_t mode = 0444) {
    const auto path = directory / name;
    ::chmod(path.c_str(), 0600);
    std::error_code ignored;
    fs::remove(path, ignored);
    std::ofstream stream(path, std::ios::binary | std::ios::trunc);
    stream << content;
    stream.close();
    ::chmod(path.c_str(), mode);
}

netsec::RuleFilePolicy testPolicy() {
    netsec::RuleFilePolicy policy;
    policy.trusted_uid = static_cast<netsec::OwnerId>(::getuid());
    policy.require_secure_directories = true;
    return policy;
}

netsec::CommandPolicy helperPolicy() {
    netsec::CommandPolicy command;
    command.name = "device-helper";
    command.executable = "/opt/trusted/device-helper";
    command.argument_template = {
        "--action",
        "{ACTION}",
        "--kernel",
        "{KERNEL}",
        "--serial",
        "{ENV:SERIAL}",
        "--vendor",
        "{ATTR:idVendor}",
    };
    command.environment_template = {
        {"DEVICE_ACTION", "{ACTION}"},
        {"DEVICE_KERNEL", "{KERNEL}"},
        {"DEVICE_SERIAL", "{ENV:SERIAL}"},
    };
    return command;
}

netsec::DeviceEvent legalEvent() {
    netsec::DeviceEvent event;
    event.action = "add";
    event.subsystem = "usb";
    event.kernel = "disk1";
    event.devpath = "/devices/usb/disk1";
    event.devname = "/dev/disk1";
    event.seqnum = 17;
    event.event_id = 41;
    event.attributes["idVendor"] = "0x1234";
    event.environment["SERIAL"] = "SN42";
    return event;
}

bool hasError(const netsec::LoadResult& result, netsec::RuleErrorCode code) {
    for (const auto& error : result.errors) {
        if (error.code == code) {
            return true;
        }
    }
    return false;
}

void writableRuleIsRejected() {
    TestDirectory directory;
    writeRule(directory.path(), "writable.rules",
              "ACTION==\"add\", RUN+=\"device-helper\"\n", 0644);

    netsec::RuleFileLoader loader(directory.path(), {helperPolicy()},
                                  testPolicy());
    const auto result = loader.load(1);
    check(!result.ok, "writable rule file must be rejected");
    check(hasError(result, netsec::RuleErrorCode::insecure_permissions),
          "writable rule should report insecure permissions");
    check(result.snapshot == nullptr,
          "rejected writable rule must not produce a snapshot");
}

void writableRuleDirectoryIsRejected() {
    TestDirectory directory;
    const auto open_directory = directory.path() / "open-rules";
    fs::create_directories(open_directory);
    ::chmod(open_directory.c_str(), 0777);
    writeRule(open_directory, "trusted-looking.rules",
              "ACTION==\"add\", RUN+=\"device-helper\"\n");

    netsec::RuleFileLoader loader(open_directory, {helperPolicy()},
                                  testPolicy());
    const auto result = loader.load(1);
    check(!result.ok, "group/world-writable rule directory must be rejected");
    check(hasError(result, netsec::RuleErrorCode::insecure_permissions),
          "writable rule directory should report insecure permissions");
    check(result.snapshot == nullptr,
          "writable rule directory must not publish a snapshot");
}

void symlinkedRuleIsRejected() {
    TestDirectory directory;
    writeRule(directory.path(), "target.conf",
              "ACTION==\"add\", RUN+=\"device-helper\"\n");
    fs::create_symlink("target.conf", directory.path() / "link.rules");

    netsec::RuleFileLoader loader(directory.path(), {helperPolicy()},
                                  testPolicy());
    const auto result = loader.load(1);
    check(!result.ok, "symlinked rule file must be rejected");
    check(hasError(result, netsec::RuleErrorCode::insecure_path),
          "symlink should be reported as an insecure path");
}

void stickySystemParentIsAccepted() {
    TestDirectory directory;
    const auto sticky_parent = directory.path() / "sticky-parent";
    const auto rules = sticky_parent / "rules.d";
    fs::create_directories(rules);
    ::chmod(sticky_parent.c_str(), 01777);
    ::chmod(rules.c_str(), 0700);
    writeRule(rules, "trusted.rules",
              "ACTION==\"add\", KERNEL==\"disk*\", RUN+=\"device-helper\"\n");

    netsec::RuleFileLoader loader(rules, {helperPolicy()}, testPolicy());
    const auto result = loader.load(1);
    check(result.ok,
          "a root/trusted-owned sticky ancestor such as /tmp is safe");
    check(result.rule_count == 1, "rule below sticky ancestor should load");
}

void commandInjectionIsRejected() {
    TestDirectory directory;
    writeRule(directory.path(), "injection.rules",
              "ACTION==\"add\", RUN+=\"/opt/trusted/device-helper "
              "--action add;reboot\"\n");
    writeRule(directory.path(), "substitution.rules",
              "ACTION==\"add\", RUN+=\"/opt/trusted/device-helper "
              "--action $(id)\"\n");

    netsec::RuleFileLoader loader(directory.path(), {helperPolicy()},
                                  testPolicy());
    const auto result = loader.load(1);
    check(!result.ok, "command injection rule must be rejected");
    check(hasError(result, netsec::RuleErrorCode::command_not_allowed),
          "injected command should not resolve to a command policy");
    check(result.snapshot == nullptr,
          "command injection must not publish a snapshot");
}

void environmentExpansionIsRejected() {
    TestDirectory directory;
    writeRule(directory.path(), "bad-env.rules",
              "ACTION==\"add\", ENV{LD_PRELOAD}=\"evil.so\", "
              "RUN+=\"device-helper\"\n");

    netsec::RuleFileLoader loader(directory.path(), {helperPolicy()},
                                  testPolicy());
    const auto result = loader.load(1);
    check(!result.ok, "rule environment assignment must be rejected");
    check(hasError(result, netsec::RuleErrorCode::environment_not_allowed),
          "rule should not control process environment");

    auto bad_command = helperPolicy();
    bad_command.environment_template.push_back(
        {"EVIL_VALUE", "{ENV:LD_PRELOAD}"});
    netsec::RuleFileLoader policy_loader(directory.path(), {bad_command},
                                         testPolicy());
    const auto policy_result = policy_loader.load(1);
    check(!policy_result.ok,
          "environment placeholder outside the controlled set must be rejected");
    check(hasError(policy_result,
                   netsec::RuleErrorCode::environment_not_allowed),
          "uncontrolled environment source should be reported");

    auto reserved_name = helperPolicy();
    reserved_name.environment_template.push_back({"LD_PRELOAD", "fixed.so"});
    netsec::RuleFileLoader reserved_loader(directory.path(), {reserved_name},
                                           testPolicy());
    const auto reserved_result = reserved_loader.load(1);
    check(!reserved_result.ok,
          "reserved process environment variables must be rejected");
}

void legitimateRuleRunsOnlyTrustedCommand() {
    TestDirectory directory;
    writeRule(directory.path(), "trusted.rules",
              "ACTION==\"add\", SUBSYSTEM==\"usb\", KERNEL==\"disk[0-9]\", "
              "ENV{SERIAL}==\"SN*\", RUN+=\"device-helper\"\n");

    netsec::RuleEngine engine(directory.path(), {helperPolicy()}, testPolicy());
    const auto loaded = engine.load();
    check(loaded.ok, "legitimate rule must load");
    check(loaded.generation == 1, "first successful load should be generation 1");
    check(loaded.rule_count == 1, "legitimate rule count");

    auto event = legalEvent();
    event.environment["LD_PRELOAD"] = "/tmp/evil.dylib";
    const auto evaluation = engine.evaluate(event);
    check(evaluation.ok, "legitimate event should evaluate");
    check(evaluation.generation == 1, "event must be bound to generation 1");
    check(evaluation.commands.size() == 1,
          "exactly one trusted command should be selected");
    if (evaluation.commands.size() == 1) {
        const auto& command = evaluation.commands.front();
        check(command.generation == 1, "command generation binding");
        check(command.executable == "/opt/trusted/device-helper",
              "only the fixed policy executable may run");
        check(command.argv ==
                  std::vector<std::string>{
                      "/opt/trusted/device-helper", "--action", "add",
                      "--kernel", "disk1", "--serial", "SN42",
                      "--vendor", "0x1234"},
              "argv must come from the host policy template");
        check(command.environment.count("DEVICE_ACTION") == 1,
              "controlled environment value");
        check(command.environment.at("DEVICE_ACTION") == "add",
              "environment value expansion");
        check(command.environment.count("LD_PRELOAD") == 0,
              "daemon/event environment must not leak into the child");
        check(command.environment.count("PATH") == 0,
              "child environment must be clean");
    }
}

void forgedEventsProduceNoCommands() {
    TestDirectory directory;
    writeRule(directory.path(), "trusted.rules",
              "ACTION==\"add\", SUBSYSTEM==\"usb\", KERNEL==\"disk*\", "
              "ENV{SERIAL}==\"SN*\", RUN+=\"device-helper\"\n");
    netsec::RuleEngine engine(directory.path(), {helperPolicy()}, testPolicy());
    check(engine.load().ok, "setup rule must load");

    auto bad_action = legalEvent();
    bad_action.action = "add;reboot";
    const auto action_result = engine.evaluate(bad_action);
    check(!action_result.ok, "forged ACTION must reject the event");
    check(action_result.commands.empty(), "forged ACTION must run nothing");

    auto bad_path = legalEvent();
    bad_path.devname = "/dev/disk1;rm -rf /";
    const auto path_result = engine.evaluate(bad_path);
    check(!path_result.ok, "forged device path must reject the event");
    check(path_result.commands.empty(), "forged device path must run nothing");

    auto bad_env = legalEvent();
    bad_env.environment["SERIAL"] = "$(id)";
    const auto env_result = engine.evaluate(bad_env);
    check(!env_result.ok, "forged controlled environment value must reject");
    check(env_result.commands.empty(),
          "forged environment must run nothing");

    auto bad_attr = legalEvent();
    bad_attr.attributes["idVendor"] = "0x1234;reboot";
    const auto attr_result = engine.evaluate(bad_attr);
    check(!attr_result.ok, "forged attribute value must reject");
    check(attr_result.commands.empty(), "forged attribute must run nothing");
}

void hotReloadIsAtomicAndFailurePreservesOldRules() {
    TestDirectory directory;
    const auto rule_path = directory.path() / "hot.rules";
    writeRule(directory.path(), "hot.rules",
              "ACTION==\"add\", KERNEL==\"disk*\", RUN+=\"device-helper\"\n");

    netsec::RuleEngine engine(directory.path(), {helperPolicy()}, testPolicy());
    const auto first = engine.load();
    check(first.ok && first.generation == 1, "initial generation");
    const auto first_snapshot = engine.snapshot();
    check(first_snapshot != nullptr, "initial snapshot must be immutable");

    writeRule(directory.path(), "hot.rules",
              "ACTION==\"add\", KERNEL==\"nvme*\", RUN+=\"device-helper\"\n");
    const auto second = engine.reload();
    check(second.ok && second.generation == 2, "successful hot reload");
    check(engine.activeGeneration() == 2, "active generation switched");

    auto disk = legalEvent();
    const auto current_disk = engine.evaluate(disk);
    check(current_disk.commands.empty(),
          "new generation must not use the old disk match");
    const auto bound_disk = engine.evaluate(disk, *first_snapshot);
    check(bound_disk.commands.size() == 1 &&
              bound_disk.commands.front().generation == 1,
          "an event bound to an old snapshot keeps generation 1");

    auto nvme = legalEvent();
    nvme.kernel = "nvme0";
    const auto current_nvme = engine.evaluate(nvme);
    check(current_nvme.commands.size() == 1 &&
              current_nvme.commands.front().generation == 2,
          "new generation selects the new rule");

    writeRule(directory.path(), "hot.rules",
              "THIS_KEYWORD_IS_NOT_ALLOWED==\"x\"\n");
    const auto failed = engine.reload();
    check(!failed.ok, "malformed reload must fail");
    check(engine.activeGeneration() == 2,
          "failed reload must preserve the old generation");
    check(engine.ruleCount() == 1, "failed reload must preserve old rules");
    const auto after_failure = engine.evaluate(nvme);
    check(after_failure.commands.size() == 1 &&
              after_failure.commands.front().generation == 2,
          "old trusted command must remain active after a failed reload");

    writeRule(directory.path(), "hot.rules",
              "ACTION==\"add\", KERNEL==\"disk*\", RUN+=\"device-helper\"\n");
    const auto third = engine.reload();
    check(third.ok && third.generation == 3,
          "a later successful reload advances generation");
    const auto recovered = engine.evaluate(disk);
    check(recovered.commands.size() == 1 &&
              recovered.commands.front().generation == 3,
          "recovered generation uses the restored rule");
}

}  // namespace

int main() {
    writableRuleIsRejected();
    writableRuleDirectoryIsRejected();
    symlinkedRuleIsRejected();
    stickySystemParentIsAccepted();
    commandInjectionIsRejected();
    environmentExpansionIsRejected();
    legitimateRuleRunsOnlyTrustedCommand();
    forgedEventsProduceNoCommands();
    hotReloadIsAtomicAndFailurePreservesOldRules();

    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "all netsec rule-loader tests passed\n";
    return 0;
}
