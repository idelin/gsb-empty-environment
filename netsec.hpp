#pragma once

#include <cstdint>
#include <map>
#include <optional>
#include <set>
#include <shared_mutex>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

// Bit sets are deliberately small, fixed-width values.  "All" covers every
// permission understood by this component; it never means "whatever the host
// happens to support".
enum class FileAccess : std::uint32_t {
  None = 0u,
  Execute = 1u << 0,
  Read = 1u << 1,
  Write = 1u << 2,
  Append = 1u << 3,
  Create = 1u << 4,
  Delete = 1u << 5,
  Rename = 1u << 6,
  Link = 1u << 7,
  Lock = 1u << 8,
  GetAttributes = 1u << 9,
  SetAttributes = 1u << 10,
  All = (1u << 11) - 1u,
};

constexpr FileAccess operator|(FileAccess lhs, FileAccess rhs) noexcept {
  return static_cast<FileAccess>(static_cast<std::uint32_t>(lhs) |
                                 static_cast<std::uint32_t>(rhs));
}

constexpr FileAccess operator&(FileAccess lhs, FileAccess rhs) noexcept {
  return static_cast<FileAccess>(static_cast<std::uint32_t>(lhs) &
                                 static_cast<std::uint32_t>(rhs));
}

constexpr FileAccess operator~(FileAccess value) noexcept {
  return static_cast<FileAccess>(~static_cast<std::uint32_t>(value) &
                                 static_cast<std::uint32_t>(FileAccess::All));
}

constexpr FileAccess &operator|=(FileAccess &lhs, FileAccess rhs) noexcept {
  lhs = lhs | rhs;
  return lhs;
}

constexpr FileAccess &operator&=(FileAccess &lhs, FileAccess rhs) noexcept {
  lhs = lhs & rhs;
  return lhs;
}

// requested is contained in allowed when it contains no bit that allowed lacks.
constexpr bool contains(FileAccess allowed, FileAccess requested) noexcept {
  return (static_cast<std::uint32_t>(requested) &
          ~static_cast<std::uint32_t>(allowed)) == 0u;
}

enum class NetworkAccess : std::uint32_t {
  None = 0u,
  Connect = 1u << 0,
  Accept = 1u << 1,
  Bind = 1u << 2,
  Listen = 1u << 3,
  Send = 1u << 4,
  Receive = 1u << 5,
  Create = 1u << 6,
  Raw = 1u << 7,
  GetOption = 1u << 8,
  SetOption = 1u << 9,
  All = (1u << 10) - 1u,
};

constexpr NetworkAccess operator|(NetworkAccess lhs,
                                  NetworkAccess rhs) noexcept {
  return static_cast<NetworkAccess>(static_cast<std::uint32_t>(lhs) |
                                    static_cast<std::uint32_t>(rhs));
}

constexpr NetworkAccess operator&(NetworkAccess lhs,
                                  NetworkAccess rhs) noexcept {
  return static_cast<NetworkAccess>(static_cast<std::uint32_t>(lhs) &
                                    static_cast<std::uint32_t>(rhs));
}

constexpr NetworkAccess operator~(NetworkAccess value) noexcept {
  return static_cast<NetworkAccess>(
      ~static_cast<std::uint32_t>(value) &
      static_cast<std::uint32_t>(NetworkAccess::All));
}

constexpr NetworkAccess &operator|=(NetworkAccess &lhs,
                                    NetworkAccess rhs) noexcept {
  lhs = lhs | rhs;
  return lhs;
}

constexpr NetworkAccess &operator&=(NetworkAccess &lhs,
                                    NetworkAccess rhs) noexcept {
  lhs = lhs & rhs;
  return lhs;
}

constexpr bool contains(NetworkAccess allowed,
                        NetworkAccess requested) noexcept {
  return (static_cast<std::uint32_t>(requested) &
          ~static_cast<std::uint32_t>(allowed)) == 0u;
}

enum class EnvironmentPolicy {
  // All names are inherited except explicitly forbidden names and the
  // loader/shell variables that are rejected unconditionally on exec.
  InheritAll,
  // Only names in allowed_environment may cross the transition.
  AllowList,
  // No inherited environment is accepted.
  DenyAll,
};

enum class NamespacePolicy {
  // The target re-evaluates required namespaces, but does not restrict the
  // inherited namespace set.
  AllowAll,
  // Every inherited namespace must be in allowed_namespaces.
  AllowList,
};

struct FileRule {
  std::string label;
  FileAccess access = FileAccess::None;
};

struct InheritedDescriptorRule {
  std::optional<int> fd;
  std::string path;
  std::string label;
  FileAccess access = FileAccess::None;
  bool deny = false;
};

struct InheritedDescriptor {
  int fd = -1;
  FileAccess access = FileAccess::None;
  std::string path;
  std::string label;
  // A close-on-exec descriptor is not inherited and therefore is not
  // evaluated after the transition.
  bool close_on_exec = false;
};

struct DomainPolicy {
  std::string name;
  // A permissive target is never a legal transition destination.  This flag
  // is explicit so callers can import policies that use names other than the
  // conventional "unconfined"/"permissive" labels.
  bool permissive = false;
  std::vector<FileRule> file_rules;
  NetworkAccess network_access = NetworkAccess::None;
  std::set<std::string> capabilities;

  EnvironmentPolicy environment_policy = EnvironmentPolicy::InheritAll;
  std::set<std::string> allowed_environment;
  std::set<std::string> required_environment;
  std::set<std::string> forbidden_environment;

  NamespacePolicy namespace_policy = NamespacePolicy::AllowAll;
  std::set<std::string> allowed_namespaces;
  std::set<std::string> required_namespaces;

  FileAccess default_inherited_descriptor_access = FileAccess::None;
  std::vector<InheritedDescriptorRule> descriptor_rules;

  [[nodiscard]] std::optional<FileAccess>
  file_access(std::string_view label) const;
  [[nodiscard]] bool allows_file(std::string_view label,
                                 FileAccess access) const;
  [[nodiscard]] bool allows_capability(std::string_view capability) const;

  DomainPolicy &add_file_rule(std::string label, FileAccess access);
};

struct ExecutableInfo {
  std::string path;
  std::string label;
  std::uint64_t hard_link_count = 1;
  // The executable itself, or any directory component of its path, can be
  // modified by the invoking principal.
  bool path_writable = false;
  bool file_writable = false;
  bool label_permissive = false;
  bool symlink = false;
  bool regular_file = true;
  bool exists = true;

  [[nodiscard]] bool writable() const noexcept {
    return path_writable || file_writable;
  }
  [[nodiscard]] bool has_multiple_hard_links() const noexcept {
    return hard_link_count > 1;
  }
};

struct ExecRequest {
  std::string current_domain;
  std::string target_domain;
  ExecutableInfo executable;
  std::vector<InheritedDescriptor> inherited_descriptors;
  std::map<std::string, std::string> environment;
  std::set<std::string> namespaces;
  // If omitted, the source domain's capability ceiling is used as the current
  // effective set.  The result is always the intersection with the target
  // domain, so exec cannot add a capability.
  std::optional<std::set<std::string>> current_capabilities;

  ExecRequest() = default;
  ExecRequest(std::string current, std::string target, ExecutableInfo file)
      : current_domain(std::move(current)), target_domain(std::move(target)),
        executable(std::move(file)) {}
};

struct TransitionRule {
  std::string source_domain;
  std::string executable_label;
  std::string target_domain;
  bool enabled = true;
};

enum class DenyCode : std::uint8_t {
  None = 0,
  SourceDomainUnknown,
  TargetDomainUnknown,
  EmptyExecutablePath,
  EmptyFileLabel,
  ExecutableMissing,
  ExecutableNotRegular,
  InvalidFileMetadata,
  SymlinkEntryPoint,
  WritableEntryPoint,
  MultipleHardLinks,
  PermissiveFileLabel,
  PermissiveTargetDomain,
  TransitionRuleMissing,
  TransitionRuleDisabled,
  TargetWidensFileRules,
  TargetWidensNetworkRules,
  TargetWidensCapabilities,
  CurrentCapabilitiesExceedSource,
  DescriptorNotPermitted,
  EnvironmentNotPermitted,
  RequiredEnvironmentMissing,
  NamespaceNotPermitted,
  RequiredNamespaceMissing,
};

[[nodiscard]] std::string_view to_string(DenyCode code) noexcept;

struct TransitionResult {
  bool allowed = false;
  DenyCode code = DenyCode::None;
  std::string reason;
  std::string source_domain;
  std::string target_domain;
  std::string file_label;
  std::set<std::string> effective_capabilities;
  NetworkAccess effective_network_access = NetworkAccess::None;
  std::uint64_t policy_generation = 0;

  [[nodiscard]] explicit operator bool() const noexcept { return allowed; }
  [[nodiscard]] bool denied() const noexcept { return !allowed; }
};

struct PolicyIssue {
  std::string source_domain;
  std::string executable_label;
  std::string target_domain;
  DenyCode code = DenyCode::None;
  std::string detail;
};

struct PolicyUpdate {
  std::vector<DomainPolicy> upsert_domains;
  std::vector<std::string> remove_domains;
  std::vector<TransitionRule> upsert_transitions;
  std::vector<TransitionRule> remove_transitions;
  std::set<std::string> add_permissive_labels;
  std::set<std::string> remove_permissive_labels;
};

struct InspectOptions {
  // Including all ancestors is the strict mode: if a writable ancestor
  // exists, the pathname itself can be replaced.
  bool check_all_path_components = true;
  bool check_access_permission = true;
};

class TransitionPolicy {
public:
  TransitionPolicy();

  // Domain setters are upserts and are atomic with respect to evaluation.
  TransitionPolicy &add_domain(DomainPolicy domain);
  TransitionPolicy &upsert_domain(DomainPolicy domain);
  TransitionPolicy &update_domain(DomainPolicy domain);
  bool remove_domain(std::string_view name);

  // Apply a group of changes under one lock and one generation bump.  This
  // prevents a concurrent exec from observing a half-installed policy.
  void apply_update(const PolicyUpdate &update);

  // A transition key is (source_domain, executable_label, target_domain).
  // Re-adding the same key replaces the previous rule.
  TransitionPolicy &add_transition(TransitionRule rule);
  TransitionPolicy &upsert_transition(TransitionRule rule);
  bool update_transition(const TransitionRule &rule);
  bool remove_transition(std::string_view source_domain,
                         std::string_view executable_label,
                         std::string_view target_domain);

  TransitionPolicy &mark_permissive_label(std::string label);
  bool unmark_permissive_label(std::string_view label);
  [[nodiscard]] bool is_permissive_label(std::string_view label) const;
  void clear_permissive_labels();

  [[nodiscard]] std::optional<DomainPolicy>
  find_domain(std::string_view name) const;
  [[nodiscard]] std::optional<TransitionRule>
  find_transition(std::string_view source_domain,
                  std::string_view executable_label,
                  std::string_view target_domain) const;

  // All checks are performed against the snapshot that produced this
  // generation.  See also the generation in TransitionResult.
  [[nodiscard]] std::uint64_t generation() const;
  [[nodiscard]] bool is_current(const TransitionResult &result) const;

  [[nodiscard]] TransitionResult evaluate(const ExecRequest &request) const;
  [[nodiscard]] TransitionResult
  evaluate_exec(const ExecRequest &request) const {
    return evaluate(request);
  }
  [[nodiscard]] TransitionResult authorize(const ExecRequest &request) const {
    return evaluate(request);
  }
  [[nodiscard]] TransitionResult
  authorize_exec(const ExecRequest &request) const {
    return evaluate(request);
  }
  [[nodiscard]] TransitionResult check_exec(const ExecRequest &request) const {
    return evaluate(request);
  }

  [[nodiscard]] std::vector<PolicyIssue> validate() const;

private:
  struct Snapshot {
    std::map<std::string, DomainPolicy> domains;
    std::vector<TransitionRule> transitions;
    std::set<std::string> permissive_labels;
    std::uint64_t generation = 0;
  };

  [[nodiscard]] Snapshot snapshot() const;
  static TransitionResult evaluate_snapshot(const Snapshot &snapshot,
                                            const ExecRequest &request);

  mutable std::shared_mutex mutex_;
  std::map<std::string, DomainPolicy> domains_;
  std::vector<TransitionRule> transitions_;
  std::set<std::string> permissive_labels_;
  std::uint64_t generation_ = 0;
};

using Policy = TransitionPolicy;
using ExecPolicy = TransitionPolicy;
using ExecContext = ExecRequest;
using ExecDecision = TransitionResult;
using PolicyDecision = TransitionResult;

[[nodiscard]] TransitionResult evaluate_exec(const TransitionPolicy &policy,
                                             const ExecRequest &request);
[[nodiscard]] TransitionResult authorize_exec(const TransitionPolicy &policy,
                                              const ExecRequest &request);

// Inspect a pathname before constructing ExecRequest.  Any failure to inspect
// is represented as an unsafe entry point (fail closed).  The caller must
// still supply the policy label; labels are not inferred from path names.
[[nodiscard]] ExecutableInfo
inspect_executable(std::string path, std::string label,
                   const InspectOptions &options = {});

} // namespace netsec
