#include "netsec.hpp"

#include <cerrno>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <functional>
#include <iostream>
#include <map>
#include <optional>
#include <string>
#include <sys/stat.h>
#include <unistd.h>
#include <utility>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, const char* expression, int line) {
  if (condition) {
    return;
  }
  std::cerr << "FAIL line " << line << ": " << expression << '\n';
  ++failures;
}

#define CHECK(expression) check((expression), #expression, __LINE__)

std::uint64_t inode_for(const std::string& path) {
  return static_cast<std::uint64_t>(std::hash<std::string>{}(path)) | 1U;
}

class FakeEnvironment final : public netsec::Environment {
 public:
  std::optional<std::string> read_file(
      std::string_view path) const override {
    const auto it = files_.find(std::string(path));
    if (it == files_.end()) {
      return std::nullopt;
    }
    return it->second;
  }

  netsec::FileFacts lstat(std::string_view path) const override {
    const auto it = entries_.find(std::string(path));
    if (it != entries_.end()) {
      return it->second;
    }
    netsec::FileFacts facts;
    facts.exists = true;
    facts.is_directory = true;
    facts.owner = 0;
    facts.group = 0;
    facts.mode = 0755;
    facts.device = 1;
    facts.inode = inode_for(std::string(path));
    return facts;
  }

  std::uint32_t effective_uid() const override { return 501; }

  void set_regular(const std::string& path, std::uint32_t owner,
                   std::uint32_t mode) {
    netsec::FileFacts facts;
    facts.exists = true;
    facts.is_regular = true;
    facts.owner = owner;
    facts.group = 0;
    facts.mode = mode;
    facts.device = 1;
    facts.inode = inode_for(path);
    entries_[path] = facts;
  }

  void set_directory(const std::string& path, std::uint32_t owner,
                     std::uint32_t mode) {
    netsec::FileFacts facts;
    facts.exists = true;
    facts.is_directory = true;
    facts.owner = owner;
    facts.group = 0;
    facts.mode = mode;
    facts.device = 1;
    facts.inode = inode_for(path);
    entries_[path] = facts;
  }

  void set_symlink(const std::string& path, std::uint32_t owner) {
    netsec::FileFacts facts;
    facts.exists = true;
    facts.is_symlink = true;
    facts.owner = owner;
    facts.group = 0;
    facts.mode = 0777;
    facts.device = 1;
    facts.inode = inode_for(path);
    entries_[path] = facts;
  }

  void set_text(const std::string& path, std::string text) {
    files_[path] = std::move(text);
  }

  void erase(const std::string& path) { entries_.erase(path); }

 private:
  std::map<std::string, netsec::FileFacts> entries_;
  std::map<std::string, std::string> files_;
};

struct RecordingLauncher final : netsec::ProcessLauncher {
  int calls = 0;
  netsec::ExecutionPlan last_plan;
  netsec::LaunchResult next;

  RecordingLauncher() {
    next.reason = netsec::PolicyReason::Allowed;
    next.launched = true;
    next.completed = true;
    next.exit_code = 0;
  }

  netsec::LaunchResult launch(const netsec::ExecutionPlan& plan,
                              int) override {
    ++calls;
    last_plan = plan;
    return next;
  }
};

struct Fixture {
  FakeEnvironment environment;
  std::string config_path = "/etc/netsec/core.conf";
  std::string handler = "/usr/libexec/core-dumper";
  std::string dump_directory = "/var/lib/netsec/cores";
  std::string pattern;

  Fixture() : pattern("|" + handler + " %P %u %g %s %t %d") {
    environment.set_regular(config_path, 501, 0600);
    environment.set_regular(handler, 0, 0755);
    environment.set_directory(dump_directory, 501, 0700);
    write_config();
  }

  void write_config(const std::string& extra = {}) {
    std::string text;
    text += "core_pattern=" + pattern + "\n";
    text += "handler=" + handler + "\n";
    text += "dump_directory=" + dump_directory + "\n";
    text += "max_core_bytes=1048576\n";
    text += "max_pid=99999\n";
    text += "max_signal=64\n";
    text += "max_dump_mode=2\n";
    text += "max_arguments=32\n";
    text += "max_argument_bytes=4096\n";
    text += extra;
    environment.set_text(config_path, std::move(text));
  }

  std::optional<netsec::CoreDumpPolicy> load(
      const std::string& extra = {}, std::string* error = nullptr) {
    write_config(extra);
    netsec::ConfigLoadOptions options;
    options.trusted_uid = 501;
    return netsec::CoreDumpPolicy::load(config_path, environment, options,
                                        error);
  }

  netsec::KernelRuntimeState runtime(
      const netsec::CoreDumpPolicy& policy) const {
    netsec::KernelRuntimeState state;
    state.core_pattern = policy.config().pattern.text;
    state.suid_dumpable = 0;
    state.fs_suid_dumpable = 0;
    return state;
  }
};

netsec::CoreDumpIdentity base_identity() {
  netsec::CoreDumpIdentity identity;
  identity.pid = 4242;
  identity.uid = 501;
  identity.gid = 20;
  identity.signal = 6;
  identity.dumpable = true;
  identity.in_initial_pid_namespace = true;
  identity.in_initial_mount_namespace = true;
  identity.in_initial_user_namespace = true;
  identity.pid_namespace_inode = 1;
  identity.user_namespace_inode = 1;
  identity.core_bytes = 128;
  identity.timestamp = 1700000000;
  identity.dump_mode = 0;
  return identity;
}

void expect_reason(const netsec::PolicyDecision& decision,
                   netsec::PolicyReason reason, const char* label) {
  if (decision.reason != reason) {
    std::cerr << "FAIL " << label << ": expected "
              << netsec::to_string(reason) << ", got "
              << netsec::to_string(decision.reason) << " ("
              << decision.detail << ")\n";
    ++failures;
  }
  if (decision.plan.has_value()) {
    std::cerr << "FAIL " << label << ": denied request produced a plan\n";
    ++failures;
  }
}

void expect_allowed(const netsec::PolicyDecision& decision,
                    const char* label) {
  if (!decision.allowed()) {
    std::cerr << "FAIL " << label << ": expected allow, got "
              << netsec::to_string(decision.reason) << " ("
              << decision.detail << ")\n";
    ++failures;
  }
}

void test_pattern_rejects_user_controlled_fragments() {
  std::string error;
  CHECK(netsec::parse_core_pattern("|/usr/bin/dumper %P %u %g %s %t %d",
                                   &error)
            .has_value());
  CHECK(!netsec::parse_core_pattern("|/usr/bin/dumper %e", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("|/usr/bin/dumper %E", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("|/usr/bin/dumper %h", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("|/usr/bin/dumper %c", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("|/usr/bin/dumper %p", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("|/usr/bin/dumper %x", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("|/usr/bin/dumper --x;reboot", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("|/usr/bin/dumper $(reboot)", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("|/usr/bin/dumper  %P", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("|/usr/bin/dumper %P ", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("/usr/bin/dumper %P", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("|relative/dumper %P", &error)
             .has_value());
  CHECK(!netsec::parse_core_pattern("|/usr/bin/../bin/dumper %P", &error)
             .has_value());

  const auto parsed =
      netsec::parse_core_pattern("|/usr/bin/dumper %P %u %g %s %t %d %%",
                                 &error);
  CHECK(parsed.has_value());
  if (parsed.has_value()) {
    netsec::CorePatternValues values;
    values.pid = 10;
    values.uid = 20;
    values.gid = 30;
    values.signal = 11;
    values.timestamp = 40;
    values.dump_mode = 1;
    const std::vector<std::string> rendered = parsed->render(values, &error);
    const std::vector<std::string> expected = {
        "/usr/bin/dumper", "10", "20", "30", "11", "40", "1", "%"};
    CHECK(rendered == expected);
  }
}

void test_legitimate_dump_and_fixed_arguments() {
  Fixture fixture;
  const auto policy = fixture.load();
  CHECK(policy.has_value());
  if (!policy.has_value()) {
    return;
  }

  const netsec::CoreDumpIdentity identity = base_identity();
  const netsec::KernelRuntimeState runtime = fixture.runtime(*policy);
  const netsec::PolicyDecision decision = policy->evaluate(identity, runtime);
  expect_allowed(decision, "legitimate dump");

  fixture.environment.set_text("/proc/sys/kernel/core_pattern",
                               policy->config().pattern.text + "\n");
  fixture.environment.set_text("/proc/sys/kernel/suid_dumpable", "0\n");
  fixture.environment.set_text("/proc/sys/fs/suid_dumpable", "0\n");
  expect_allowed(policy->evaluate(base_identity()),
                 "runtime read from environment");
  if (!decision.allowed()) {
    return;
  }

  const netsec::ExecutionPlan& plan = *decision.plan;
  CHECK(plan.executable == fixture.handler);
  CHECK(plan.argv.size() == 7);
  CHECK(plan.argv.at(0) == fixture.handler);
  CHECK(plan.argv.at(1) == "4242");
  CHECK(plan.argv.at(2) == "501");
  CHECK(plan.argv.at(3) == "20");
  CHECK(plan.argv.at(4) == "6");
  CHECK(plan.argv.at(5) == "1700000000");
  CHECK(plan.argv.at(6) == "0");
  CHECK(plan.working_directory == "/");
  for (const std::string& argument : plan.argv) {
    CHECK(argument.find("%e") == std::string::npos);
    CHECK(argument.find("%E") == std::string::npos);
    CHECK(argument.find(';') == std::string::npos);
    CHECK(argument.find("$(") == std::string::npos);
  }

  RecordingLauncher launcher;
  netsec::CoreDumpExecutor executor(*policy, launcher);
  const netsec::LaunchResult result =
      executor.execute(identity, runtime, -1);
  CHECK(result.ok());
  CHECK(launcher.calls == 1);
  CHECK(launcher.last_plan.argv == plan.argv);
}

void test_core_pattern_replacement_denied() {
  Fixture fixture;
  const auto policy = fixture.load();
  CHECK(policy.has_value());
  if (!policy.has_value()) {
    return;
  }

  netsec::KernelRuntimeState runtime = fixture.runtime(*policy);
  runtime.core_pattern = "|/tmp/attacker %P %u";
  RecordingLauncher launcher;
  netsec::CoreDumpExecutor executor(*policy, launcher);
  const netsec::LaunchResult result =
      executor.execute(base_identity(), runtime, -1);
  CHECK(result.reason == netsec::PolicyReason::CorePatternMismatch);
  CHECK(!result.launched);
  CHECK(launcher.calls == 0);

  runtime.core_pattern = std::nullopt;
  expect_reason(policy->evaluate(base_identity(), runtime),
                netsec::PolicyReason::RuntimeUnavailable,
                "missing core_pattern");

  runtime.core_pattern = policy->config().pattern.text + " %e";
  expect_reason(policy->evaluate(base_identity(), runtime),
                netsec::PolicyReason::CorePatternMismatch,
                "replaced core_pattern expansion");
}

void test_dump_directory_preemption_denied() {
  Fixture fixture;
  const auto policy = fixture.load();
  CHECK(policy.has_value());
  if (!policy.has_value()) {
    return;
  }
  const netsec::CoreDumpIdentity identity = base_identity();
  const netsec::KernelRuntimeState runtime = fixture.runtime(*policy);

  fixture.environment.set_directory(fixture.dump_directory, 501, 0777);
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::UnsafeDumpDirectory,
                "world-writable dump directory");

  fixture.environment.set_directory(fixture.dump_directory, 1000, 0700);
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::UnsafeDumpDirectory,
                "dump directory owned by attacker");

  fixture.environment.set_symlink(fixture.dump_directory, 501);
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::UnsafeDumpDirectory,
                "symlinked dump directory");

  fixture.environment.set_symlink("/var/lib/netsec", 0);
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::UnsafeDumpDirectory,
                "symlinked dump directory parent");

  fixture.environment.set_directory("/var/lib/netsec", 0, 0755);
  fixture.environment.set_directory(fixture.dump_directory, 501, 0700);
  RecordingLauncher launcher;
  netsec::CoreDumpExecutor executor(*policy, launcher);
  CHECK(executor.execute(identity, runtime, -1).ok());
  CHECK(launcher.calls == 1);

  fixture.environment.set_symlink(fixture.handler, 0);
  const netsec::LaunchResult denied = executor.execute(identity, runtime, -1);
  CHECK(denied.reason == netsec::PolicyReason::UnsafeHandler);
  CHECK(launcher.calls == 1);
}

void test_suid_and_privileged_crashes_denied() {
  Fixture fixture;
  const auto policy = fixture.load();
  CHECK(policy.has_value());
  if (!policy.has_value()) {
    return;
  }

  netsec::CoreDumpIdentity identity = base_identity();
  netsec::KernelRuntimeState runtime = fixture.runtime(*policy);

  identity.setuid = true;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::SuidProcessDenied,
                "setuid crash");

  identity = base_identity();
  identity.setgid = true;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::SuidProcessDenied,
                "setgid crash");

  identity = base_identity();
  identity.file_capabilities = true;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::SuidProcessDenied,
                "file capability crash");

  identity = base_identity();
  identity.uid = 0;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::PrivilegedProcessDenied,
                "root crash");

  identity = base_identity();
  identity.euid = 0;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::PrivilegedProcessDenied,
                "effective root crash");

  identity = base_identity();
  identity.dumpable = false;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::DumpableDenied,
                "non-dumpable crash");

  identity = base_identity();
  runtime = fixture.runtime(*policy);
  runtime.fs_suid_dumpable = 2;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::SuidDumpableUnsafe,
                "fs.suid_dumpable=2");

  runtime = fixture.runtime(*policy);
  runtime.suid_dumpable = 1;
  runtime.fs_suid_dumpable = 1;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::SuidDumpableUnsafe,
                "suid_dumpable=1");

  runtime = fixture.runtime(*policy);
  runtime.suid_dumpable = std::nullopt;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::RuntimeUnavailable,
                "missing suid_dumpable");
}

void test_identity_namespace_and_size_limits() {
  Fixture fixture;
  const auto policy = fixture.load();
  CHECK(policy.has_value());
  if (!policy.has_value()) {
    return;
  }
  const netsec::KernelRuntimeState runtime = fixture.runtime(*policy);

  netsec::CoreDumpIdentity identity = base_identity();
  identity.pid = 0;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::InvalidRequest, "zero pid");

  identity = base_identity();
  identity.pid = 100000;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::InvalidRequest, "pid above limit");

  identity = base_identity();
  identity.signal = 0;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::InvalidRequest, "zero signal");

  identity = base_identity();
  identity.dump_mode = 3;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::InvalidRequest, "invalid dump mode");

  identity = base_identity();
  identity.in_initial_pid_namespace = false;
  identity.pid_namespace_inode = 42;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::NamespaceDenied,
                "non-initial pid namespace");

  identity = base_identity();
  identity.core_bytes = 1048577;
  expect_reason(policy->evaluate(identity, runtime),
                netsec::PolicyReason::CoreTooLarge,
                "declared core above limit");

  const auto allowing = fixture.load("allow_non_initial_namespaces=true\n"
                                     "allowed_pid_namespaces=42\n");
  CHECK(allowing.has_value());
  if (allowing.has_value()) {
    const netsec::KernelRuntimeState allowed_runtime =
        fixture.runtime(*allowing);
    identity = base_identity();
    identity.in_initial_pid_namespace = false;
    identity.pid_namespace_inode = 42;
    expect_allowed(allowing->evaluate(identity, allowed_runtime),
                   "allowlisted pid namespace");
    identity.pid_namespace_inode = 43;
    expect_reason(allowing->evaluate(identity, allowed_runtime),
                  netsec::PolicyReason::NamespaceDenied,
                  "pid namespace not allowlisted");
  }

  const auto suid_one = fixture.load("allow_suid_dumpable_one=true\n");
  CHECK(suid_one.has_value());
  if (suid_one.has_value()) {
    netsec::KernelRuntimeState state = fixture.runtime(*suid_one);
    state.suid_dumpable = 1;
    state.fs_suid_dumpable = 1;
    expect_allowed(suid_one->evaluate(base_identity(), state),
                   "restricted suid dumpable mode");
  }

  const auto identity_limited =
      fixture.load("allowed_uids=501\nallowed_gids=20\n");
  CHECK(identity_limited.has_value());
  if (identity_limited.has_value()) {
    const netsec::KernelRuntimeState state = fixture.runtime(*identity_limited);
    expect_allowed(identity_limited->evaluate(base_identity(), state),
                   "allowlisted identity");
    netsec::CoreDumpIdentity wrong_uid = base_identity();
    wrong_uid.uid = 502;
    expect_reason(identity_limited->evaluate(wrong_uid, state),
                  netsec::PolicyReason::IdentityDenied,
                  "uid not allowlisted");
    netsec::CoreDumpIdentity wrong_gid = base_identity();
    wrong_gid.gid = 21;
    expect_reason(identity_limited->evaluate(wrong_gid, state),
                  netsec::PolicyReason::IdentityDenied,
                  "gid not allowlisted");
  }
}

void test_symlink_and_config_trust_failures() {
  Fixture fixture;
  std::string error;

  fixture.environment.set_symlink(fixture.config_path, 501);
  CHECK(!fixture.load({}, &error).has_value());

  fixture.environment.set_regular(fixture.config_path, 501, 0666);
  CHECK(!fixture.load({}, &error).has_value());

  fixture.environment.set_regular(fixture.config_path, 1000, 0600);
  CHECK(!fixture.load({}, &error).has_value());

  fixture.environment.set_regular(fixture.config_path, 501, 0600);
  fixture.environment.set_symlink(fixture.handler, 0);
  netsec::ConfigLoadOptions options;
  options.trusted_uid = 501;
  CHECK(!netsec::CoreDumpPolicy::load(fixture.config_path,
                                      fixture.environment, options, &error)
             .has_value());

  fixture.environment.set_regular(fixture.handler, 0, 0755);
  fixture.environment.set_symlink(fixture.dump_directory, 501);
  CHECK(!netsec::CoreDumpPolicy::load(fixture.config_path,
                                      fixture.environment, options, &error)
             .has_value());

  fixture.environment.set_directory(fixture.dump_directory, 501, 0700);
  fixture.environment.set_regular(fixture.handler, 0, 0775);
  CHECK(!netsec::CoreDumpPolicy::load(fixture.config_path,
                                      fixture.environment, options, &error)
             .has_value());

  fixture.environment.set_regular(fixture.handler, 0, 0755);
  CHECK(netsec::CoreDumpPolicy::load(fixture.config_path,
                                     fixture.environment, options, &error)
            .has_value());

  CHECK(!fixture.load("unknown_option=true\n", &error).has_value());
}

void test_failure_is_not_retried() {
  Fixture fixture;
  const auto policy = fixture.load();
  CHECK(policy.has_value());
  if (!policy.has_value()) {
    return;
  }
  RecordingLauncher launcher;
  launcher.next.reason = netsec::PolicyReason::LauncherFailed;
  launcher.next.launched = true;
  launcher.next.completed = true;
  launcher.next.exit_code = 9;
  netsec::CoreDumpExecutor executor(*policy, launcher);

  const netsec::LaunchResult result = executor.execute(
      base_identity(), fixture.runtime(*policy), -1);
  CHECK(result.reason == netsec::PolicyReason::LauncherFailed);
  CHECK(launcher.calls == 1);

  netsec::KernelRuntimeState bad = fixture.runtime(*policy);
  bad.core_pattern = "|/tmp/attacker";
  launcher.calls = 0;
  const netsec::LaunchResult denied =
      executor.execute(base_identity(), bad, -1);
  CHECK(denied.reason == netsec::PolicyReason::CorePatternMismatch);
  CHECK(launcher.calls == 0);
}

class PipeInput {
 public:
  explicit PipeInput(const std::string& contents) {
    int fds[2] = {-1, -1};
    if (pipe(fds) != 0) {
      return;
    }
    fd_ = fds[0];
    std::uint64_t offset = 0;
    while (offset < contents.size()) {
      const ssize_t written =
          write(fds[1], contents.data() + offset, contents.size() - offset);
      if (written <= 0) {
        break;
      }
      offset += static_cast<std::uint64_t>(written);
    }
    close(fds[1]);
  }

  ~PipeInput() {
    if (fd_ >= 0) {
      close(fd_);
    }
  }

  PipeInput(const PipeInput&) = delete;
  PipeInput& operator=(const PipeInput&) = delete;

  int fd() const { return fd_; }

 private:
  int fd_ = -1;
};

std::string choose_cat_handler(const netsec::SystemEnvironment& environment) {
  const netsec::FileFacts direct = environment.lstat("/usr/bin/cat");
  if (direct.exists && direct.is_regular && !direct.is_symlink) {
    return "/usr/bin/cat";
  }
  return "/bin/cat";
}

struct RealFixture {
  std::string root;
  std::string dump_directory;
  std::string config_path;
  std::string handler;
  std::uint32_t uid = 0;

  RealFixture() {
    const char* temp = std::getenv("TMPDIR");
    std::string base = temp != nullptr && *temp != '\0'
                           ? temp
                           : std::filesystem::current_path().string();
    if (!base.empty() && base.back() == '/') {
      base.pop_back();
    }
    uid = static_cast<std::uint32_t>(geteuid());
    netsec::SystemEnvironment environment;
    if (!netsec::validate_trusted_directory(environment, base, uid).ok()) {
      base = std::filesystem::current_path().string();
    }
    std::string templ = base + "/netsec-core-test-XXXXXX";
    std::vector<char> buffer(templ.begin(), templ.end());
    buffer.push_back('\0');
    char* created = mkdtemp(buffer.data());
    if (created == nullptr) {
      return;
    }
    root = created;
    dump_directory = root + "/cores";
    config_path = root + "/core.conf";
    mkdir(dump_directory.c_str(), 0700);
    chmod(dump_directory.c_str(), 0700);

    handler = choose_cat_handler(environment);
    const std::string pattern = "|" + handler;
    std::ofstream config(config_path, std::ios::trunc);
    config << "core_pattern=" << pattern << "\n";
    config << "handler=" << handler << "\n";
    config << "dump_directory=" << dump_directory << "\n";
    config << "max_core_bytes=4\n";
    config << "max_pid=99999\n";
    config << "max_signal=64\n";
    config << "allow_root_processes=true\n";
    config.close();
    chmod(config_path.c_str(), 0600);
  }

  ~RealFixture() {
    if (!root.empty()) {
      std::error_code error;
      std::filesystem::remove_all(root, error);
    }
  }
};

void test_real_executor_enforces_size_and_allows_legitimate_dump() {
  RealFixture fixture;
  CHECK(!fixture.root.empty());
  if (fixture.root.empty()) {
    return;
  }

  netsec::SystemEnvironment environment;
  netsec::ConfigLoadOptions options;
  options.trusted_uid = fixture.uid;
  std::string error;
  auto policy = netsec::CoreDumpPolicy::load(fixture.config_path, environment,
                                             options, &error);
  CHECK(policy.has_value());
  if (!policy.has_value()) {
    std::cerr << "real fixture load error: " << error << '\n';
    return;
  }

  netsec::KernelRuntimeState runtime;
  runtime.core_pattern = policy->config().pattern.text;
  runtime.suid_dumpable = 0;
  runtime.fs_suid_dumpable = 0;

  netsec::CoreDumpIdentity identity = base_identity();
  identity.uid = fixture.uid;
  identity.gid = static_cast<std::uint32_t>(getegid());
  identity.pid = static_cast<std::uint32_t>(getpid());
  identity.core_bytes = 4;

  PipeInput too_large("12345678");
  CHECK(too_large.fd() >= 0);
  netsec::PosixProcessLauncher launcher;
  netsec::CoreDumpExecutor executor(*policy, launcher);
  const netsec::LaunchResult oversized =
      executor.execute(identity, runtime, too_large.fd());
  CHECK(oversized.reason == netsec::PolicyReason::SizeLimitExceeded);
  CHECK(!oversized.ok());

  std::ofstream config(fixture.config_path, std::ios::trunc);
  config << "core_pattern=" << policy->config().pattern.text << "\n";
  config << "handler=" << fixture.handler << "\n";
  config << "dump_directory=" << fixture.dump_directory << "\n";
  config << "max_core_bytes=1024\n";
  config << "max_pid=99999\n";
  config << "max_signal=64\n";
  config << "allow_root_processes=true\n";
  config.close();
  chmod(fixture.config_path.c_str(), 0600);

  auto larger_policy = netsec::CoreDumpPolicy::load(
      fixture.config_path, environment, options, &error);
  CHECK(larger_policy.has_value());
  if (!larger_policy.has_value()) {
    return;
  }
  netsec::CoreDumpExecutor larger_executor(*larger_policy, launcher);
  PipeInput legitimate("hello");
  CHECK(legitimate.fd() >= 0);
  const netsec::LaunchResult result = larger_executor.execute(
      identity, runtime, legitimate.fd());
  CHECK(result.ok());
  CHECK(result.bytes_written == 5);
}

void test_real_environment_rejects_symlinks() {
  RealFixture fixture;
  CHECK(!fixture.root.empty());
  if (fixture.root.empty()) {
    return;
  }

  netsec::SystemEnvironment environment;
  const std::uint32_t uid = static_cast<std::uint32_t>(geteuid());
  std::string link_directory = fixture.root + "/link-directory";
  CHECK(::symlink(fixture.dump_directory.c_str(), link_directory.c_str()) == 0);
  const netsec::FileValidation directory =
      netsec::validate_trusted_directory(environment, link_directory, uid);
  CHECK(directory.reason == netsec::PolicyReason::UnsafeDumpDirectory);

  std::string link_config = fixture.root + "/link-config";
  CHECK(::symlink(fixture.config_path.c_str(), link_config.c_str()) == 0);
  netsec::ConfigLoadOptions options;
  options.trusted_uid = uid;
  std::string error;
  CHECK(!netsec::CoreDumpPolicy::load(link_config, environment, options,
                                      &error)
             .has_value());
}

void test_real_directory_swap_after_authorization() {
  RealFixture fixture;
  CHECK(!fixture.root.empty());
  if (fixture.root.empty()) {
    return;
  }

  std::ofstream config(fixture.config_path, std::ios::trunc);
  config << "core_pattern=|" << fixture.handler << "\n";
  config << "handler=" << fixture.handler << "\n";
  config << "dump_directory=" << fixture.dump_directory << "\n";
  config << "max_core_bytes=1024\n";
  config << "allow_root_processes=true\n";
  config.close();
  chmod(fixture.config_path.c_str(), 0600);

  netsec::SystemEnvironment environment;
  netsec::ConfigLoadOptions options;
  options.trusted_uid = static_cast<std::uint32_t>(geteuid());
  std::string error;
  auto policy = netsec::CoreDumpPolicy::load(fixture.config_path, environment,
                                             options, &error);
  CHECK(policy.has_value());
  if (!policy.has_value()) {
    return;
  }

  netsec::KernelRuntimeState runtime;
  runtime.core_pattern = policy->config().pattern.text;
  runtime.suid_dumpable = 0;
  runtime.fs_suid_dumpable = 0;
  netsec::CoreDumpIdentity identity = base_identity();
  identity.uid = static_cast<std::uint32_t>(geteuid());
  identity.gid = static_cast<std::uint32_t>(getegid());
  identity.pid = static_cast<std::uint32_t>(getpid());

  const netsec::PolicyDecision decision = policy->evaluate(identity, runtime);
  expect_allowed(decision, "directory swap precondition");
  if (!decision.allowed()) {
    return;
  }

  const std::string displaced = fixture.dump_directory + ".displaced";
  CHECK(::rename(fixture.dump_directory.c_str(), displaced.c_str()) == 0);
  CHECK(::mkdir(fixture.dump_directory.c_str(), 0700) == 0);
  CHECK(::chmod(fixture.dump_directory.c_str(), 0700) == 0);

  PipeInput input("tiny");
  netsec::PosixProcessLauncher launcher;
  const netsec::LaunchResult result = launcher.launch(*decision.plan, input.fd());
  CHECK(result.reason == netsec::PolicyReason::UnsafeDumpDirectory);
  CHECK(!result.launched);
}

void test_real_handler_swap_after_authorization() {
  RealFixture fixture;
  CHECK(!fixture.root.empty());
  if (fixture.root.empty()) {
    return;
  }

  netsec::SystemEnvironment environment;
  const std::string source_handler = choose_cat_handler(environment);
  const std::string handler = fixture.root + "/trusted-dumper";
  const std::string replacement = fixture.root + "/trusted-dumper.new";
  std::error_code copy_error;
  CHECK(std::filesystem::copy_file(source_handler, handler, copy_error));
  CHECK(::chmod(handler.c_str(), 0755) == 0);

  std::ofstream config(fixture.config_path, std::ios::trunc);
  config << "core_pattern=|" << handler << "\n";
  config << "handler=" << handler << "\n";
  config << "dump_directory=" << fixture.dump_directory << "\n";
  config << "max_core_bytes=1024\n";
  config << "allow_root_processes=true\n";
  config.close();
  chmod(fixture.config_path.c_str(), 0600);

  netsec::ConfigLoadOptions options;
  options.trusted_uid = static_cast<std::uint32_t>(geteuid());
  std::string error;
  auto policy = netsec::CoreDumpPolicy::load(fixture.config_path, environment,
                                             options, &error);
  CHECK(policy.has_value());
  if (!policy.has_value()) {
    return;
  }

  netsec::KernelRuntimeState runtime;
  runtime.core_pattern = policy->config().pattern.text;
  runtime.suid_dumpable = 0;
  runtime.fs_suid_dumpable = 0;
  netsec::CoreDumpIdentity identity = base_identity();
  identity.uid = static_cast<std::uint32_t>(geteuid());
  identity.gid = static_cast<std::uint32_t>(getegid());
  identity.pid = static_cast<std::uint32_t>(getpid());

  const netsec::PolicyDecision decision = policy->evaluate(identity, runtime);
  expect_allowed(decision, "handler swap precondition");
  if (!decision.allowed()) {
    return;
  }

  copy_error.clear();
  CHECK(std::filesystem::copy_file(source_handler, replacement, copy_error));
  CHECK(::chmod(replacement.c_str(), 0755) == 0);
  CHECK(::rename(replacement.c_str(), handler.c_str()) == 0);

  PipeInput input("tiny");
  netsec::PosixProcessLauncher launcher;
  const netsec::LaunchResult result = launcher.launch(*decision.plan, input.fd());
  CHECK(result.reason == netsec::PolicyReason::UnsafeHandler);
  CHECK(!result.launched);
}

}

int main() {
  test_pattern_rejects_user_controlled_fragments();
  test_legitimate_dump_and_fixed_arguments();
  test_core_pattern_replacement_denied();
  test_dump_directory_preemption_denied();
  test_suid_and_privileged_crashes_denied();
  test_identity_namespace_and_size_limits();
  test_symlink_and_config_trust_failures();
  test_failure_is_not_retried();
  test_real_executor_enforces_size_and_allows_legitimate_dump();
  test_real_environment_rejects_symlinks();
  test_real_directory_swap_after_authorization();
  test_real_handler_swap_after_authorization();

  if (failures != 0) {
    std::cerr << failures << " test checks failed\n";
    return 1;
  }
  std::cout << "all core dump policy tests passed\n";
  return 0;
}
