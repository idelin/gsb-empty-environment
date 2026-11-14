#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

inline constexpr std::uint64_t kDefaultMaxCoreBytes =
    64ull * 1024ull * 1024ull;
inline constexpr std::uint32_t kUnsetId = 0xFFFFFFFFu;

enum class PolicyReason : std::uint8_t {
  Allowed,
  InvalidRequest,
  InvalidConfiguration,
  RuntimeUnavailable,
  CorePatternMismatch,
  UnsafeConfig,
  UnsafeHandler,
  UnsafeDumpDirectory,
  SuidDumpableUnsafe,
  SuidProcessDenied,
  PrivilegedProcessDenied,
  IdentityDenied,
  NamespaceDenied,
  DumpableDenied,
  CoreTooLarge,
  ArgumentLimitExceeded,
  SpawnFailed,
  LauncherFailed,
  SizeLimitExceeded,
  Timeout,
};

std::string_view to_string(PolicyReason reason) noexcept;

struct FileFacts {
  bool exists = false;
  bool is_symlink = false;
  bool is_regular = false;
  bool is_directory = false;
  std::uint32_t owner = 0;
  std::uint32_t group = 0;
  std::uint32_t mode = 0;
  std::uint64_t device = 0;
  std::uint64_t inode = 0;
};

class Environment {
 public:
  virtual ~Environment() = default;

  virtual std::optional<std::string> read_file(
      std::string_view path) const = 0;
  virtual FileFacts lstat(std::string_view path) const = 0;
  virtual std::uint32_t effective_uid() const = 0;
};

class SystemEnvironment final : public Environment {
 public:
  std::optional<std::string> read_file(
      std::string_view path) const override;
  FileFacts lstat(std::string_view path) const override;
  std::uint32_t effective_uid() const override;
};

struct FileValidation {
  PolicyReason reason = PolicyReason::InvalidConfiguration;
  std::string detail;
  FileFacts facts;

  bool ok() const noexcept { return reason == PolicyReason::Allowed; }
  explicit operator bool() const noexcept { return ok(); }
};

FileValidation validate_trusted_directory(
    const Environment& environment, std::string_view path,
    std::uint32_t trusted_uid);
FileValidation validate_trusted_executable(
    const Environment& environment, std::string_view path,
    std::uint32_t trusted_uid);
FileValidation validate_read_only_file(
    const Environment& environment, std::string_view path,
    std::uint32_t trusted_uid);

struct CorePatternValues {
  std::uint32_t pid = 0;
  std::uint32_t uid = 0;
  std::uint32_t gid = 0;
  std::uint32_t signal = 0;
  std::uint64_t timestamp = 0;
  std::uint32_t dump_mode = 0;
};

struct CorePattern {
  std::string text;
  std::string handler;
  std::vector<std::string> tokens;

  static std::optional<CorePattern> parse(
      std::string_view pattern, std::string* error = nullptr);
  std::vector<std::string> render(
      const CorePatternValues& values,
      std::string* error = nullptr) const;
};

std::optional<CorePattern> parse_core_pattern(
    std::string_view pattern, std::string* error = nullptr);
bool is_safe_core_pattern(
    std::string_view pattern, std::string* error = nullptr);

struct CoreDumpIdentity {
  std::uint32_t pid = 0;
  std::uint32_t uid = 0;
  std::uint32_t gid = 0;
  std::uint32_t signal = 0;
  bool setuid = false;
  bool setgid = false;
  bool dumpable = true;
  bool in_initial_pid_namespace = true;
  bool in_initial_mount_namespace = true;
  bool in_initial_user_namespace = true;
  std::uint64_t pid_namespace_inode = 1;
  std::uint64_t user_namespace_inode = 1;
  std::uint64_t core_bytes = 0;
  std::uint64_t timestamp = 0;
  std::uint32_t dump_mode = 0;
  std::uint32_t euid = kUnsetId;
  std::uint32_t egid = kUnsetId;
  bool file_capabilities = false;
};

struct CoreDumpLimits {
  std::uint64_t max_core_bytes = kDefaultMaxCoreBytes;
  std::uint32_t max_pid = 4'194'304;
  std::uint32_t max_signal = 64;
  std::uint32_t max_dump_mode = 2;
  std::size_t max_arguments = 32;
  std::size_t max_argument_bytes = 4096;
  bool allow_root_processes = false;
  bool allow_setuid_processes = false;
  bool allow_non_initial_namespaces = false;
  bool allow_suid_dumpable_one = false;
  std::vector<std::uint32_t> allowed_uids;
  std::vector<std::uint32_t> allowed_gids;
  std::vector<std::uint64_t> allowed_pid_namespaces;
  std::vector<std::uint64_t> allowed_user_namespaces;
};

struct PolicyConfig {
  CorePattern pattern;
  std::string dump_directory;
  std::uint32_t trusted_uid = 0;
  CoreDumpLimits limits;
};

struct ConfigLoadOptions {
  std::uint32_t trusted_uid = 0;
  std::size_t max_config_bytes = 64 * 1024;
};

std::optional<PolicyConfig> load_policy_config(
    std::string_view path, const Environment& environment,
    const ConfigLoadOptions& options = {}, std::string* error = nullptr);

struct KernelRuntimeState {
  std::optional<std::string> core_pattern;
  std::optional<int> suid_dumpable;
  std::optional<int> fs_suid_dumpable;
};

KernelRuntimeState read_kernel_runtime(const Environment& environment);

struct ExecutionPlan {
  std::string executable;
  std::vector<std::string> argv;
  std::vector<std::string> env;
  std::string working_directory;
  std::string dump_directory;
  std::uint64_t handler_device = 0;
  std::uint64_t handler_inode = 0;
  std::uint32_t handler_owner = 0;
  std::uint32_t handler_mode = 0;
  std::uint64_t dump_directory_device = 0;
  std::uint64_t dump_directory_inode = 0;
  std::uint32_t dump_directory_owner = 0;
  std::uint32_t dump_directory_mode = 0;
  std::uint64_t max_core_bytes = 0;
};

struct PolicyDecision {
  PolicyReason reason = PolicyReason::InvalidRequest;
  std::string detail;
  std::optional<ExecutionPlan> plan;

  bool allowed() const noexcept {
    return reason == PolicyReason::Allowed && plan.has_value();
  }
  explicit operator bool() const noexcept { return allowed(); }

  static PolicyDecision deny(PolicyReason reason, std::string detail);
  static PolicyDecision permit(ExecutionPlan plan);
};

PolicyDecision evaluate_core_dump(
    const PolicyConfig& config, const CoreDumpIdentity& identity,
    const KernelRuntimeState& runtime, const Environment& environment,
    std::string* error = nullptr);

class CoreDumpPolicy {
 public:
  static std::optional<CoreDumpPolicy> create(
      PolicyConfig config, const Environment& environment,
      std::string* error = nullptr);
  static std::optional<CoreDumpPolicy> load(
      std::string_view config_path, const Environment& environment,
      const ConfigLoadOptions& options = {},
      std::string* error = nullptr);

  CoreDumpPolicy(const CoreDumpPolicy&) = default;
  CoreDumpPolicy& operator=(const CoreDumpPolicy&) = default;
  CoreDumpPolicy(CoreDumpPolicy&&) noexcept = default;
  CoreDumpPolicy& operator=(CoreDumpPolicy&&) noexcept = default;

  const PolicyConfig& config() const noexcept { return config_; }
  const Environment& environment() const noexcept { return *environment_; }

  PolicyDecision evaluate(
      const CoreDumpIdentity& identity,
      const KernelRuntimeState& runtime) const;
  PolicyDecision evaluate(const CoreDumpIdentity& identity) const;

 private:
  CoreDumpPolicy(PolicyConfig config, const Environment& environment);

  PolicyConfig config_;
  const Environment* environment_;
};

struct LaunchResult {
  PolicyReason reason = PolicyReason::SpawnFailed;
  std::string detail;
  bool launched = false;
  bool completed = false;
  int exit_code = -1;
  std::uint64_t bytes_written = 0;

  bool ok() const noexcept {
    return reason == PolicyReason::Allowed && completed && exit_code == 0;
  }
  explicit operator bool() const noexcept { return ok(); }
};

class ProcessLauncher {
 public:
  virtual ~ProcessLauncher() = default;
  virtual LaunchResult launch(const ExecutionPlan& plan, int core_fd) = 0;
};

struct PosixLauncherOptions {
  std::uint64_t timeout_ms = 5000;
  std::uint32_t max_fd = 4096;
};

class PosixProcessLauncher final : public ProcessLauncher {
 public:
  explicit PosixProcessLauncher(PosixLauncherOptions options = {});
  LaunchResult launch(const ExecutionPlan& plan, int core_fd) override;

 private:
  PosixLauncherOptions options_;
};

class CoreDumpExecutor {
 public:
  CoreDumpExecutor(const CoreDumpPolicy& policy, ProcessLauncher& launcher);

  LaunchResult execute(
      const CoreDumpIdentity& identity,
      const KernelRuntimeState& runtime, int core_fd);
  LaunchResult execute(const CoreDumpIdentity& identity, int core_fd);

 private:
  CoreDumpPolicy policy_;
  ProcessLauncher& launcher_;
};

}
