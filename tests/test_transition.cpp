#include "netsec.hpp"

#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <string>
#include <system_error>

#include <sys/stat.h>
#include <unistd.h>

namespace {

int g_failures = 0;

#define CHECK(condition)                                                       \
  do {                                                                         \
    if (!(condition)) {                                                        \
      std::cerr << __FILE__ << ":" << __LINE__                                 \
                << ": check failed: " #condition "\n";                         \
      ++g_failures;                                                            \
    }                                                                          \
  } while (false)

#define EXPECT_ALLOWED(result)                                                 \
  do {                                                                         \
    if (!(result).allowed) {                                                   \
      std::cerr << __FILE__ << ":" << __LINE__ << ": expected allowed, got "   \
                << (result).reason << "\n";                                    \
      ++g_failures;                                                            \
    }                                                                          \
  } while (false)

#define EXPECT_DENIED(result, expected_code)                                   \
  do {                                                                         \
    if ((result).allowed || (result).code != (expected_code)) {                \
      std::cerr << __FILE__ << ":" << __LINE__ << ": expected denial "         \
                << static_cast<int>(expected_code)                             \
                << ", got allowed=" << (result).allowed                        \
                << " code=" << static_cast<int>((result).code)                 \
                << " reason=" << (result).reason << "\n";                      \
      ++g_failures;                                                            \
    }                                                                          \
  } while (false)

netsec::DomainPolicy source_domain() {
  netsec::DomainPolicy domain;
  domain.name = "service";
  domain.file_rules = {
      {"service-bin", netsec::FileAccess::Execute | netsec::FileAccess::Read},
      {"data", netsec::FileAccess::Read},
  };
  domain.network_access = netsec::NetworkAccess::Connect |
                          netsec::NetworkAccess::Send |
                          netsec::NetworkAccess::Receive;
  domain.capabilities = {"CAP_NET_ADMIN", "CAP_NET_RAW"};
  domain.environment_policy = netsec::EnvironmentPolicy::AllowList;
  domain.allowed_environment = {"HOME", "LANG", "PATH"};
  domain.namespace_policy = netsec::NamespacePolicy::AllowList;
  domain.allowed_namespaces = {"mnt", "net"};
  domain.default_inherited_descriptor_access = netsec::FileAccess::Read;
  return domain;
}

netsec::DomainPolicy target_domain() {
  netsec::DomainPolicy domain;
  domain.name = "worker";
  domain.file_rules = {
      {"data", netsec::FileAccess::Read},
      {"service-bin", netsec::FileAccess::Read},
  };
  domain.network_access =
      netsec::NetworkAccess::Connect | netsec::NetworkAccess::Send;
  domain.capabilities = {"CAP_NET_RAW"};
  domain.environment_policy = netsec::EnvironmentPolicy::AllowList;
  domain.allowed_environment = {"HOME", "PATH"};
  domain.namespace_policy = netsec::NamespacePolicy::AllowList;
  domain.allowed_namespaces = {"net"};
  domain.default_inherited_descriptor_access = netsec::FileAccess::Read;
  return domain;
}

netsec::ExecRequest base_request() {
  netsec::ExecutableInfo file;
  file.path = "/usr/libexec/service";
  file.label = "service-bin";
  file.hard_link_count = 1;

  netsec::ExecRequest request{"service", "worker", file};
  request.inherited_descriptors = {
      {3, netsec::FileAccess::Read, "/var/lib/data", "data", false},
  };
  request.environment = {{"HOME", "/home/user"}, {"PATH", "/usr/bin"}};
  request.namespaces = {"net"};
  return request;
}

void install_valid_policy(netsec::TransitionPolicy &policy) {
  policy.add_domain(source_domain())
      .add_domain(target_domain())
      .add_transition({"service", "service-bin", "worker", true});
}

void test_legitimate_transition() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);

  const netsec::TransitionResult result = policy.evaluate(base_request());
  EXPECT_ALLOWED(result);
  CHECK(result.code == netsec::DenyCode::None);
  CHECK(result.effective_capabilities.find("CAP_NET_RAW") !=
        result.effective_capabilities.end());
  CHECK(result.effective_capabilities.find("CAP_NET_ADMIN") ==
        result.effective_capabilities.end());
  CHECK(result.effective_network_access ==
        (netsec::NetworkAccess::Connect | netsec::NetworkAccess::Send));
  CHECK(result.policy_generation == policy.generation());

  // Same-domain exec does not cross a label boundary and is not a transition
  // entry point; the entry-point restrictions apply to real transitions.
  netsec::ExecRequest same = base_request();
  same.target_domain = "service";
  same.executable.path_writable = true;
  EXPECT_ALLOWED(policy.evaluate(same));
}

void test_permissive_label() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);

  // The conventional "unconfined" label is denied as a transition entry even
  // if the target rules themselves do not add permissions.
  policy.add_transition({"service", "unconfined", "worker", true});
  netsec::ExecRequest request = base_request();
  request.executable.label = "unconfined";
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::PermissiveFileLabel);

  netsec::ExecRequest explicit_flag = base_request();
  explicit_flag.executable.label_permissive = true;
  EXPECT_DENIED(policy.evaluate(explicit_flag),
                netsec::DenyCode::PermissiveFileLabel);

  // Policy updates that mark another label as permissive take effect
  // immediately, and can be reversed.
  policy.mark_permissive_label("service-bin");
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::PermissiveFileLabel);
  CHECK(policy.unmark_permissive_label("service-bin"));
  EXPECT_ALLOWED(policy.evaluate(base_request()));
}

void test_entry_point_restrictions() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);

  netsec::ExecRequest request = base_request();
  request.executable.path_writable = true;
  EXPECT_DENIED(policy.evaluate(request), netsec::DenyCode::WritableEntryPoint);

  request = base_request();
  request.executable.file_writable = true;
  EXPECT_DENIED(policy.evaluate(request), netsec::DenyCode::WritableEntryPoint);

  request = base_request();
  request.executable.hard_link_count = 2;
  EXPECT_DENIED(policy.evaluate(request), netsec::DenyCode::MultipleHardLinks);

  request = base_request();
  request.executable.hard_link_count = 0;
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::InvalidFileMetadata);

  request = base_request();
  request.executable.symlink = true;
  EXPECT_DENIED(policy.evaluate(request), netsec::DenyCode::SymlinkEntryPoint);

  request = base_request();
  request.executable.exists = false;
  EXPECT_DENIED(policy.evaluate(request), netsec::DenyCode::ExecutableMissing);

  request = base_request();
  request.executable.regular_file = false;
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::ExecutableNotRegular);

  request = base_request();
  request.executable.path.clear();
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::EmptyExecutablePath);

  request = base_request();
  request.executable.label.clear();
  EXPECT_DENIED(policy.evaluate(request), netsec::DenyCode::EmptyFileLabel);
}

void test_transition_rules() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);

  netsec::ExecRequest unknown_target = base_request();
  unknown_target.target_domain = "missing";
  EXPECT_DENIED(policy.evaluate(unknown_target),
                netsec::DenyCode::TargetDomainUnknown);

  netsec::ExecRequest unknown_source = base_request();
  unknown_source.current_domain = "missing";
  EXPECT_DENIED(policy.evaluate(unknown_source),
                netsec::DenyCode::SourceDomainUnknown);

  policy.add_transition({"service", "service-bin", "worker", false});
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::TransitionRuleDisabled);

  policy.add_transition({"service", "service-bin", "worker", true});
  CHECK(policy.remove_transition("service", "service-bin", "worker"));
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::TransitionRuleMissing);

  CHECK(!policy.remove_transition("service", "service-bin", "worker"));
}

void test_target_may_not_widen_rules() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);

  netsec::DomainPolicy widened = target_domain();
  widened.capabilities.insert("CAP_SYS_ADMIN");
  policy.add_domain(widened);
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::TargetWidensCapabilities);

  widened = target_domain();
  widened.network_access |= netsec::NetworkAccess::Raw;
  policy.add_domain(widened);
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::TargetWidensNetworkRules);

  // Unknown permission bits fail closed rather than being masked away.
  widened = target_domain();
  widened.network_access = static_cast<netsec::NetworkAccess>(1u << 20);
  policy.add_domain(widened);
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::TargetWidensNetworkRules);

  widened = target_domain();
  widened.add_file_rule("secret", netsec::FileAccess::Read);
  policy.add_domain(widened);
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::TargetWidensFileRules);

  // Restoring a subset target makes the same request legal again.
  policy.add_domain(target_domain());
  EXPECT_ALLOWED(policy.evaluate(base_request()));
}

void test_permissive_target_domain() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);

  netsec::DomainPolicy permissive = target_domain();
  permissive.name = "permissive-worker";
  permissive.permissive = true;
  policy.add_domain(permissive);
  policy.add_transition({"service", "service-bin", "permissive-worker", true});

  netsec::ExecRequest request = base_request();
  request.target_domain = "permissive-worker";
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::PermissiveTargetDomain);

  const std::vector<netsec::PolicyIssue> issues = policy.validate();
  CHECK(!issues.empty());
  bool found = false;
  for (const netsec::PolicyIssue &issue : issues) {
    if (issue.code == netsec::DenyCode::PermissiveTargetDomain) {
      found = true;
    }
  }
  CHECK(found);
}

void test_capability_floor() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);

  netsec::ExecRequest request = base_request();
  request.current_capabilities = std::set<std::string>{"CAP_SYS_ADMIN"};
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::CurrentCapabilitiesExceedSource);

  request = base_request();
  request.current_capabilities =
      std::set<std::string>{"CAP_NET_ADMIN", "CAP_NET_RAW"};
  const netsec::TransitionResult result = policy.evaluate(request);
  EXPECT_ALLOWED(result);
  CHECK(result.effective_capabilities.find("CAP_NET_ADMIN") ==
        result.effective_capabilities.end());
  CHECK(result.effective_capabilities.find("CAP_NET_RAW") !=
        result.effective_capabilities.end());

  // Even the source's full ceiling is reduced to the target's declaration.
  netsec::DomainPolicy no_caps = target_domain();
  no_caps.capabilities.clear();
  policy.add_domain(no_caps);
  const netsec::TransitionResult empty = policy.evaluate(base_request());
  EXPECT_ALLOWED(empty);
  CHECK(empty.effective_capabilities.empty());
}

void test_inherited_descriptors() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);

  netsec::ExecRequest request = base_request();
  request.inherited_descriptors[0].access =
      netsec::FileAccess::Read | netsec::FileAccess::Write;
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::DescriptorNotPermitted);

  // Close-on-exec descriptors are not inherited and are not re-evaluated.
  request = base_request();
  request.inherited_descriptors[0].access =
      netsec::FileAccess::Read | netsec::FileAccess::Write;
  request.inherited_descriptors[0].close_on_exec = true;
  EXPECT_ALLOWED(policy.evaluate(request));

  netsec::DomainPolicy descriptor_policy = target_domain();
  descriptor_policy.descriptor_rules = {
      {5, "", "", netsec::FileAccess::Read | netsec::FileAccess::Write, false},
      {7, "", "", netsec::FileAccess::None, true},
  };
  policy.add_domain(descriptor_policy);

  request = base_request();
  request.inherited_descriptors = {
      {5, netsec::FileAccess::Write, "/var/lib/data", "data", false},
  };
  EXPECT_ALLOWED(policy.evaluate(request));

  request.inherited_descriptors = {
      {6, netsec::FileAccess::Write, "/var/lib/data", "data", false},
  };
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::DescriptorNotPermitted);

  request.inherited_descriptors = {
      {7, netsec::FileAccess::Read, "/var/lib/data", "data", false},
  };
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::DescriptorNotPermitted);
}

void test_environment_reevaluation() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);

  netsec::ExecRequest request = base_request();
  request.environment["UNKNOWN"] = "value";
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::EnvironmentNotPermitted);

  request = base_request();
  request.environment["LD_PRELOAD"] = "/tmp/evil.so";
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::EnvironmentNotPermitted);

  netsec::DomainPolicy inherit_all = target_domain();
  inherit_all.environment_policy = netsec::EnvironmentPolicy::InheritAll;
  policy.add_domain(inherit_all);
  EXPECT_ALLOWED(policy.evaluate(base_request()));
  request = base_request();
  request.environment["LD_PRELOAD"] = "/tmp/evil.so";
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::EnvironmentNotPermitted);

  netsec::DomainPolicy required = target_domain();
  required.required_environment = {"SERVICE_TOKEN"};
  required.allowed_environment.insert("SERVICE_TOKEN");
  policy.add_domain(required);
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::RequiredEnvironmentMissing);
  request = base_request();
  request.environment["SERVICE_TOKEN"] = "present";
  EXPECT_ALLOWED(policy.evaluate(request));

  netsec::DomainPolicy deny_all = target_domain();
  deny_all.environment_policy = netsec::EnvironmentPolicy::DenyAll;
  policy.add_domain(deny_all);
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::EnvironmentNotPermitted);
  request = base_request();
  request.environment.clear();
  EXPECT_ALLOWED(policy.evaluate(request));
}

void test_namespace_reevaluation() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);

  netsec::ExecRequest request = base_request();
  request.namespaces.insert("user");
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::NamespaceNotPermitted);

  netsec::DomainPolicy required = target_domain();
  required.required_namespaces = {"mnt"};
  policy.add_domain(required);
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::RequiredNamespaceMissing);

  request = base_request();
  request.namespaces.insert("mnt");
  // The target's allowed set is {"net"}, so adding the required "mnt" also
  // trips the allow-list check.  A policy that needs both must allow both.
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::NamespaceNotPermitted);

  netsec::DomainPolicy both = target_domain();
  both.allowed_namespaces.insert("mnt");
  both.required_namespaces = {"mnt"};
  policy.add_domain(both);
  EXPECT_ALLOWED(policy.evaluate(request));
}

void test_rule_updates_are_atomic_and_rechecked() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);
  EXPECT_ALLOWED(policy.evaluate(base_request()));

  const std::uint64_t before = policy.generation();
  netsec::DomainPolicy widened = target_domain();
  widened.network_access |= netsec::NetworkAccess::Raw;
  policy.update_domain(widened);
  CHECK(policy.generation() > before);
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::TargetWidensNetworkRules);

  policy.update_domain(target_domain());
  EXPECT_ALLOWED(policy.evaluate(base_request()));

  netsec::TransitionRule disabled{"service", "service-bin", "worker", false};
  CHECK(policy.update_transition(disabled));
  EXPECT_DENIED(policy.evaluate(base_request()),
                netsec::DenyCode::TransitionRuleDisabled);

  CHECK(policy.update_transition({"service", "service-bin", "worker", true}));
  EXPECT_ALLOWED(policy.evaluate(base_request()));

  // A new transition to an undeclared target is rejected rather than
  // implicitly importing any existing domain.
  policy.add_transition({"service", "service-bin", "declared-later", true});
  netsec::ExecRequest request = base_request();
  request.target_domain = "declared-later";
  EXPECT_DENIED(policy.evaluate(request),
                netsec::DenyCode::TargetDomainUnknown);

  const std::vector<netsec::PolicyIssue> issues = policy.validate();
  bool found_unknown = false;
  for (const netsec::PolicyIssue &issue : issues) {
    if (issue.code == netsec::DenyCode::TargetDomainUnknown) {
      found_unknown = true;
    }
  }
  CHECK(found_unknown);

  // Batch updates are installed under one lock and one generation bump.
  const netsec::TransitionResult before_batch = policy.evaluate(base_request());
  EXPECT_ALLOWED(before_batch);
  CHECK(policy.is_current(before_batch));

  netsec::DomainPolicy broken = target_domain();
  broken.name = "broken";
  broken.capabilities.insert("CAP_SYS_ADMIN");

  netsec::PolicyUpdate update;
  update.upsert_domains = {target_domain(), broken};
  update.upsert_transitions = {{"service", "service-bin", "broken", true}};
  update.remove_transitions = {
      {"service", "service-bin", "declared-later", true}};
  policy.apply_update(update);

  CHECK(!policy.is_current(before_batch));
  netsec::ExecRequest broken_request = base_request();
  broken_request.target_domain = "broken";
  EXPECT_DENIED(policy.evaluate(broken_request),
                netsec::DenyCode::TargetWidensCapabilities);
}

class TempDirectory {
public:
  TempDirectory() {
    const char *tmp = std::getenv("TMPDIR");
    const std::filesystem::path root = (tmp != nullptr && *tmp != '\0')
                                           ? std::filesystem::path(tmp)
                                           : std::filesystem::current_path();
    path_ = root / ("netsec-transition-test-" + std::to_string(::getpid()));
    std::error_code error;
    std::filesystem::remove_all(path_, error);
    std::filesystem::create_directories(path_, error);
    CHECK(!error);
    ::chmod(path_.c_str(), 0700);
  }

  ~TempDirectory() {
    ::chmod(path_.c_str(), 0700);
    std::error_code error;
    std::filesystem::remove_all(path_, error);
  }

  TempDirectory(const TempDirectory &) = delete;
  TempDirectory &operator=(const TempDirectory &) = delete;

  [[nodiscard]] const std::filesystem::path &path() const { return path_; }

private:
  std::filesystem::path path_;
};

void test_filesystem_inspection() {
  TempDirectory temp;
  const std::filesystem::path safe = temp.path() / "safe.bin";
  {
    std::ofstream stream(safe);
    stream << "#!/bin/sh\nexit 0\n";
  }
  ::chmod(safe.c_str(), 0555);

  // The directory is still writable, so an attacker can replace the entry.
  netsec::InspectOptions immediate;
  immediate.check_all_path_components = false;
  const netsec::ExecutableInfo writable_dir =
      netsec::inspect_executable(safe.string(), "safe", immediate);
  CHECK(writable_dir.exists);
  CHECK(writable_dir.regular_file);
  CHECK(writable_dir.path_writable);
  CHECK(!writable_dir.file_writable);

  // Make the file and its immediate directory read-only for the caller.
  ::chmod(temp.path().c_str(), 0555);
  const netsec::ExecutableInfo safe_info =
      netsec::inspect_executable(safe.string(), "safe", immediate);
  CHECK(safe_info.exists);
  CHECK(safe_info.regular_file);
  CHECK(!safe_info.path_writable);
  CHECK(!safe_info.file_writable);
  CHECK(safe_info.hard_link_count == 1);

  // Re-open the directory to create hard links and a symlink for inspection.
  ::chmod(temp.path().c_str(), 0700);
  const std::filesystem::path hard_link = temp.path() / "hard.bin";
  std::error_code error;
  std::filesystem::create_hard_link(safe, hard_link, error);
  CHECK(!error);
  const netsec::ExecutableInfo linked =
      netsec::inspect_executable(hard_link.string(), "safe", immediate);
  CHECK(linked.hard_link_count == 2);

  const std::filesystem::path symlink = temp.path() / "link.bin";
  std::filesystem::create_symlink(safe, symlink, error);
  CHECK(!error);
  const netsec::ExecutableInfo linked_path =
      netsec::inspect_executable(symlink.string(), "safe", immediate);
  CHECK(linked_path.symlink);

  // A symlinked directory inside a writable directory is still a writable
  // path component even when its target is root-owned.
  const std::filesystem::path directory_link = temp.path() / "trusted";
  std::filesystem::create_directory_symlink("/usr/bin", directory_link, error);
  CHECK(!error);
  const std::filesystem::path through_link = directory_link / "env";
  if (std::filesystem::exists(through_link)) {
    const netsec::ExecutableInfo linked_directory =
        netsec::inspect_executable(through_link.string(), "system");
    CHECK(linked_directory.path_writable);
  }

  const netsec::ExecutableInfo missing = netsec::inspect_executable(
      (temp.path() / "missing.bin").string(), "safe", immediate);
  CHECK(!missing.exists);
  CHECK(missing.path_writable);

  // Root-owned system paths are not writable by an unprivileged caller.  This
  // guards against treating a mode bit such as S_IWUSR as "writable" even
  // when the caller is not the owner.
  if (::geteuid() != 0) {
    for (const char *candidate : {"/usr/bin/env", "/bin/env"}) {
      if (!std::filesystem::exists(candidate)) {
        continue;
      }
      const netsec::ExecutableInfo system =
          netsec::inspect_executable(candidate, "system");
      CHECK(system.exists);
      CHECK(system.regular_file);
      CHECK(!system.path_writable);
      CHECK(!system.file_writable);
      break;
    }
  }
}

void test_validate_reports_widening() {
  netsec::TransitionPolicy policy;
  install_valid_policy(policy);

  netsec::DomainPolicy widened = target_domain();
  widened.capabilities.insert("CAP_SYS_ADMIN");
  policy.add_domain(widened);

  const std::vector<netsec::PolicyIssue> issues = policy.validate();
  bool found = false;
  for (const netsec::PolicyIssue &issue : issues) {
    if (issue.code == netsec::DenyCode::TargetWidensCapabilities) {
      found = true;
    }
  }
  CHECK(found);
}

} // namespace

int main() {
  test_legitimate_transition();
  test_permissive_label();
  test_entry_point_restrictions();
  test_transition_rules();
  test_target_may_not_widen_rules();
  test_permissive_target_domain();
  test_capability_floor();
  test_inherited_descriptors();
  test_environment_reevaluation();
  test_namespace_reevaluation();
  test_rule_updates_are_atomic_and_rechecked();
  test_filesystem_inspection();
  test_validate_reports_widening();

  if (g_failures != 0) {
    std::cerr << g_failures << " test check(s) failed\n";
    return 1;
  }
  std::cout << "all transition policy tests passed\n";
  return 0;
}
