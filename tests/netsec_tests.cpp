#include "netsec.hpp"

#include <algorithm>
#include <cstdint>
#include <iostream>
#include <map>
#include <memory>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace {

using netsec::AccessControlEntry;
using netsec::ConfigurationIntegrity;
using netsec::ErrorCode;
using netsec::FileSystemComponent;
using netsec::FileSystemInspector;
using netsec::HmacSha256Signer;
using netsec::HmacSha256Verifier;
using netsec::NormalizedPath;
using netsec::PathSnapshot;
using netsec::RecoveryAction;
using netsec::RecoveryActionType;
using netsec::RegistryKeyAcl;
using netsec::RegistrySnapshot;
using netsec::SecurityDescriptor;
using netsec::SecurityPolicy;
using netsec::ServiceConfiguration;
using netsec::ServiceRegistry;
using netsec::SignedConfiguration;
using netsec::ValidationResult;

int failures = 0;

void report_failure(const char* file, int line, std::string_view message) {
  ++failures;
  std::cerr << file << ":" << line << ": " << message << '\n';
}

#define CHECK(expression)                                              \
  do {                                                                 \
    if (!(expression)) {                                               \
      report_failure(__FILE__, __LINE__, "CHECK failed: " #expression); \
    }                                                                  \
  } while (false)

void check_ok(const ValidationResult& result, std::string_view label) {
  if (!result.ok()) {
    report_failure(__FILE__, __LINE__,
                   std::string(label) + " failed: " + result.summary());
  }
}

void check_failed_with(const ValidationResult& result, ErrorCode code,
                       std::string_view label) {
  if (result.ok()) {
    report_failure(__FILE__, __LINE__,
                   std::string(label) + " unexpectedly succeeded");
    return;
  }
  const bool found = std::any_of(
      result.diagnostics.begin(), result.diagnostics.end(),
      [code](const netsec::Diagnostic& diagnostic) {
        return diagnostic.code == code;
      });
  if (!found) {
    report_failure(__FILE__, __LINE__,
                   std::string(label) + " did not report expected code: " +
                       result.summary());
  }
}

std::string lower_ascii(std::string_view value) {
  std::string result(value);
  for (char& character : result) {
    if (character >= 'A' && character <= 'Z') {
      character = static_cast<char>(character - 'A' + 'a');
    }
  }
  return result;
}

std::string normalized_key(std::string_view path) {
  const NormalizedPath normalized = netsec::normalize_windows_path(path);
  return lower_ascii(normalized.valid ? normalized.value : std::string(path));
}

std::vector<std::uint8_t> byte_string(std::string_view value) {
  return std::vector<std::uint8_t>(value.begin(), value.end());
}

const std::vector<std::uint8_t> signing_key{
    0x4e, 0x45, 0x54, 0x53, 0x45, 0x43, 0x2d, 0x53,
    0x49, 0x47, 0x4e, 0x49, 0x4e, 0x47, 0x2d, 0x4b,
    0x45, 0x59, 0x2d, 0x32, 0x30, 0x32, 0x36, 0x2d,
    0x30, 0x31, 0x2d, 0x30, 0x31, 0x2d, 0x41, 0x42};

SecurityDescriptor privileged_descriptor(const SecurityPolicy& policy) {
  SecurityDescriptor descriptor;
  descriptor.owner = std::string(netsec::sids::local_system);
  descriptor.dacl_present = true;
  descriptor.dacl_protected = true;
  for (const std::string& principal : policy.privileged_writers) {
    descriptor.entries.push_back(AccessControlEntry{
        AccessControlEntry::Kind::allow, principal, 0xffffffffu, false});
  }
  descriptor.entries.push_back(AccessControlEntry{
      AccessControlEntry::Kind::allow,
      std::string(netsec::sids::builtin_users), 0x00020019u, true});
  return descriptor;
}

std::vector<std::string> windows_ancestors(std::string_view path) {
  const NormalizedPath normalized = netsec::normalize_windows_path(path);
  std::vector<std::string> ancestors;
  if (!normalized.valid) {
    return ancestors;
  }

  std::string current = normalized.value;
  while (true) {
    ancestors.push_back(current);
    if (current.size() == 3 && current[1] == ':' && current[2] == '\\') {
      break;
    }
    const std::size_t separator = current.rfind('\\');
    if (separator == std::string::npos || separator == 0) {
      break;
    }
    current = current.substr(0, separator);
    if (current.size() == 2 && current[1] == ':') {
      current.push_back('\\');
    }
  }
  return ancestors;
}

struct RegistryKeyState {
  std::string path;
  SecurityDescriptor security;
};

class FakeRegistry final : public ServiceRegistry {
 public:
  explicit FakeRegistry(const SecurityPolicy&) {}

  [[nodiscard]] RegistrySnapshot snapshot(
      std::string_view service_key) const override {
    RegistrySnapshot snapshot;
    snapshot.key_path = std::string(service_key);
    snapshot.exists = exists_;
    snapshot.observed = observed_;
    if (exists_) {
      for (const auto& entry : keys_) {
        snapshot.keys.push_back(RegistryKeyAcl{entry.second.path,
                                               entry.second.security});
      }
    }
    return snapshot;
  }

  [[nodiscard]] ValidationResult write_configuration(
      std::string_view service_key, const ServiceConfiguration& configuration,
      const SecurityPolicy& policy) override {
    ValidationResult result;
    ++write_count;
    if (!exists_) {
      exists_ = true;
      root_path_ = std::string(service_key);
      create_key(root_path_, privileged_descriptor(policy));
      create_key(root_path_ + "\\Parameters", privileged_descriptor(policy));
    }
    observed_.image_path = configuration.image_path;
    observed_.object_name = configuration.object_name;
    observed_.dependencies = configuration.dependencies;
    observed_.recovery_actions = configuration.recovery_actions;
    return result;
  }

  [[nodiscard]] ValidationResult enforce_acl(
      std::string_view service_key, const SecurityPolicy& policy) override {
    ValidationResult result;
    ++harden_count;
    if (!exists_ || lower_ascii(root_path_) !=
                        lower_ascii(std::string(service_key))) {
      result.add(ErrorCode::backend_failure, std::string(service_key),
                 "cannot harden a missing service key");
      return result;
    }
    for (auto& entry : keys_) {
      if (entry.second.path == root_path_ ||
          entry.second.path.rfind(root_path_ + "\\", 0) == 0) {
        entry.second.security = privileged_descriptor(policy);
      }
    }
    return result;
  }

  void create_key(std::string path, SecurityDescriptor security) {
    const std::string key = normalized_key(path);
    keys_[key] = RegistryKeyState{std::move(path), std::move(security)};
  }

  void add_user_write_ace(std::string_view path) {
    auto found = keys_.find(normalized_key(path));
    if (found == keys_.end()) {
      return;
    }
    found->second.security.entries.push_back(AccessControlEntry{
        AccessControlEntry::Kind::allow,
        std::string(netsec::sids::everyone),
        netsec::registry_rights::set_value, false});
  }

  void add_unprivileged_subkey(std::string path) {
    SecurityDescriptor descriptor;
    descriptor.owner = "S-1-5-21-111111111-222222222-333333333-1001";
    descriptor.dacl_present = true;
    descriptor.dacl_protected = true;
    descriptor.entries.push_back(AccessControlEntry{
        AccessControlEntry::Kind::allow, descriptor.owner,
        netsec::registry_rights::write_mask, false});
    create_key(std::move(path), std::move(descriptor));
  }

  [[nodiscard]] const netsec::ObservedServiceConfiguration& observed()
      const {
    return observed_;
  }

  netsec::ObservedServiceConfiguration& mutable_observed() { return observed_; }

  bool exists() const { return exists_; }
  std::string root_path() const { return root_path_; }

  int write_count{0};
  int harden_count{0};

 private:
  bool exists_{false};
  std::string root_path_;
  netsec::ObservedServiceConfiguration observed_;
  std::map<std::string, RegistryKeyState> keys_;
};

class FakeFiles final : public FileSystemInspector {
 public:
  explicit FakeFiles(SecurityPolicy policy) : policy_(std::move(policy)) {}

  [[nodiscard]] PathSnapshot inspect(std::string_view path) const override {
    const auto found = entries_.find(normalized_key(path));
    if (found == entries_.end()) {
      PathSnapshot missing;
      missing.exists = false;
      return missing;
    }
    return found->second;
  }

  void add_secure(std::string_view path) {
    const NormalizedPath normalized = netsec::normalize_windows_path(path);
    if (!normalized.valid) {
      return;
    }
    PathSnapshot snapshot;
    snapshot.exists = true;
    snapshot.is_regular_file = true;
    snapshot.canonical_path = normalized.value;

    const std::vector<std::string> ancestors = windows_ancestors(path);
    for (std::size_t index = 0; index < ancestors.size(); ++index) {
      FileSystemComponent component;
      component.path = ancestors[index];
      component.is_directory = index != 0;
      component.is_reparse_point = false;
      component.security = privileged_descriptor(policy_);
      snapshot.components.push_back(std::move(component));
    }
    entries_[normalized_key(path)] = std::move(snapshot);
  }

  void add_user_writable_file(std::string_view path) {
    add_secure(path);
    relax_component(path, path);
  }

  void relax_component(std::string_view path, std::string_view component_path) {
    auto found = entries_.find(normalized_key(path));
    if (found == entries_.end()) {
      return;
    }
    for (FileSystemComponent& component : found->second.components) {
      if (lower_ascii(component.path) ==
          lower_ascii(std::string(component_path))) {
        component.security.entries.push_back(AccessControlEntry{
            AccessControlEntry::Kind::allow,
            std::string(netsec::sids::everyone),
            netsec::registry_rights::write_mask, false});
      }
    }
  }

  void make_reparse(std::string_view path, bool component_flagged) {
    add_secure(path);
    auto found = entries_.find(normalized_key(path));
    if (found == entries_.end()) {
      return;
    }
    found->second.traverses_reparse_point = true;
    if (component_flagged && !found->second.components.empty()) {
      found->second.components.front().is_reparse_point = true;
    }
  }

  void redirect_canonical(std::string_view path, std::string_view canonical) {
    add_secure(path);
    auto found = entries_.find(normalized_key(path));
    if (found != entries_.end()) {
      const NormalizedPath normalized = netsec::normalize_windows_path(canonical);
      found->second.canonical_path = normalized.valid ? normalized.value
                                                      : std::string(canonical);
    }
  }

 private:
  SecurityPolicy policy_;
  std::map<std::string, PathSnapshot> entries_;
};

ServiceConfiguration version_one_configuration() {
  ServiceConfiguration configuration;
  configuration.service_name = "ContosoAgent";
  configuration.image_path =
      "\"C:\\Program Files\\Contoso\\bin\\contoso-agent.exe\" --service";
  configuration.object_name = "LocalSystem";
  configuration.dependencies = {"Tcpip", "EventLog"};
  configuration.recovery_actions = {
      RecoveryAction{RecoveryActionType::restart_service, 0, ""},
      RecoveryAction{RecoveryActionType::restart_service, 5000, ""},
      RecoveryAction{RecoveryActionType::run_program, 0,
                     "\"C:\\Program Files\\Contoso\\bin\\contoso-recover.exe\" "
                     "--once"}};
  return configuration;
}

ServiceConfiguration version_two_configuration() {
  ServiceConfiguration configuration;
  configuration.service_name = "ContosoAgent";
  configuration.image_path =
      "\"C:\\Program Files\\Contoso\\v2\\contoso-agent.exe\" --service "
      "--revision=2";
  configuration.object_name = "NT AUTHORITY\\NetworkService";
  configuration.dependencies = {"Tcpip", "EventLog", "RpcSs"};
  configuration.recovery_actions = {
      RecoveryAction{RecoveryActionType::restart_service, 0, ""},
      RecoveryAction{RecoveryActionType::restart_service, 10000, ""},
      RecoveryAction{RecoveryActionType::run_program, 0,
                     "\"C:\\Program Files\\Contoso\\v2\\contoso-recover.exe\""}};
  return configuration;
}

struct Fixture {
  SecurityPolicy policy = SecurityPolicy::windows_service_defaults();
  std::shared_ptr<FakeRegistry> registry =
      std::make_shared<FakeRegistry>(policy);
  std::shared_ptr<FakeFiles> files = std::make_shared<FakeFiles>(policy);
  std::shared_ptr<HmacSha256Verifier> verifier =
      std::make_shared<HmacSha256Verifier>();
  HmacSha256Signer signer{"release-key", signing_key};
  ConfigurationIntegrity integrity{verifier, registry, files, policy};

  Fixture() {
    verifier->add_key("release-key", signing_key);
    files->add_secure("C:\\Program Files\\Contoso\\bin\\contoso-agent.exe");
    files->add_secure("C:\\Program Files\\Contoso\\bin\\contoso-recover.exe");
    files->add_secure("C:\\Program Files\\Contoso\\v2\\contoso-agent.exe");
    files->add_secure("C:\\Program Files\\Contoso\\v2\\contoso-recover.exe");
  }

  SignedConfiguration sign(ServiceConfiguration configuration) {
    return signer.sign(std::move(configuration));
  }
};

void test_hmac_vector() {
  const std::vector<std::uint8_t> key = byte_string("key");
  const std::vector<std::uint8_t> data =
      byte_string("The quick brown fox jumps over the lazy dog");
  const auto digest = netsec::hmac_sha256(key, data);
  CHECK(netsec::hex_encode(digest) ==
        "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8");
}

void test_install_launch_and_declared_program() {
  Fixture fixture;
  const ServiceConfiguration configuration = version_one_configuration();
  const SignedConfiguration signed_configuration =
      fixture.sign(configuration);

  check_ok(fixture.integrity.validate_configuration(signed_configuration),
           "validate configuration");
  check_ok(fixture.integrity.install(signed_configuration), "install");
  CHECK(fixture.registry->exists());
  CHECK(fixture.registry->write_count == 1);
  CHECK(fixture.registry->observed().image_path.has_value());
  CHECK(*fixture.registry->observed().image_path == configuration.image_path);
  CHECK(*fixture.registry->observed().object_name == configuration.object_name);

  const ConfigurationIntegrity::LaunchDecision decision =
      fixture.integrity.prepare_launch(signed_configuration);
  check_ok(decision.status, "prepare launch");
  CHECK(decision.plan.has_value());
  if (decision.plan.has_value()) {
    CHECK(decision.plan->executable ==
          "C:\\Program Files\\Contoso\\bin\\contoso-agent.exe");
    CHECK(decision.plan->argv.size() == 2);
    CHECK(decision.plan->argv.front() == decision.plan->executable);
    CHECK(decision.plan->argv[1] == "--service");
  }
}

void test_acl_relaxation_blocks_launch_and_update() {
  Fixture fixture;
  const ServiceConfiguration configuration = version_one_configuration();
  const SignedConfiguration signed_configuration =
      fixture.sign(configuration);
  check_ok(fixture.integrity.install(signed_configuration), "initial install");

  fixture.registry->add_user_write_ace(fixture.registry->root_path());
  fixture.registry->add_user_write_ace(fixture.registry->root_path() +
                                       "\\Parameters");

  check_failed_with(fixture.integrity.audit(signed_configuration),
                    ErrorCode::acl_untrusted_writer, "audit relaxed ACL");
  const ConfigurationIntegrity::LaunchDecision blocked =
      fixture.integrity.prepare_launch(signed_configuration);
  CHECK(blocked.status.failed());
  CHECK(!blocked.plan.has_value());

  const SignedConfiguration replacement =
      fixture.sign(version_two_configuration());
  const int writes_before_update = fixture.registry->write_count;
  check_failed_with(fixture.integrity.update(replacement),
                    ErrorCode::acl_untrusted_writer,
                    "update with relaxed ACL");
  CHECK(fixture.registry->write_count == writes_before_update);

  check_ok(fixture.integrity.repair(signed_configuration),
           "repair after ACL relaxation");
  check_ok(fixture.integrity.audit(signed_configuration),
           "audit after repair");
  const ConfigurationIntegrity::LaunchDecision repaired =
      fixture.integrity.prepare_launch(signed_configuration);
  check_ok(repaired.status, "launch after repair");
  CHECK(repaired.plan.has_value());
}

void test_path_substitution_is_rejected() {
  Fixture fixture;
  const ServiceConfiguration configuration = version_one_configuration();
  const SignedConfiguration signed_configuration =
      fixture.sign(configuration);
  check_ok(fixture.integrity.install(signed_configuration), "initial install");

  fixture.registry->mutable_observed().image_path =
      "\"C:\\Users\\Public\\evil.exe\" --service";
  check_failed_with(fixture.integrity.audit(signed_configuration),
                    ErrorCode::configuration_mismatch,
                    "registry path substitution");
  const auto blocked = fixture.integrity.prepare_launch(signed_configuration);
  CHECK(blocked.status.failed());
  CHECK(!blocked.plan.has_value());

  ServiceConfiguration user_written = configuration;
  user_written.image_path =
      "\"C:\\Program Files\\Evil\\evil.exe\" --service";
  fixture.files->add_user_writable_file(
      "C:\\Program Files\\Evil\\evil.exe");
  const SignedConfiguration signed_bad_file = fixture.sign(user_written);
  const int writes_before = fixture.registry->write_count;
  check_failed_with(fixture.integrity.install(signed_bad_file),
                    ErrorCode::path_user_writable,
                    "install with user-writable executable");
  CHECK(fixture.registry->write_count == writes_before);

  ServiceConfiguration outside = configuration;
  outside.image_path =
      "\"C:\\Program Files\\Contoso\\..\\..\\Users\\Public\\evil.exe\" "
      "--service";
  check_failed_with(fixture.integrity.validate_configuration(fixture.sign(outside)),
                    ErrorCode::path_outside_trusted_directory,
                    "path traversal outside trusted directory");

  ServiceConfiguration bad_recovery = configuration;
  bad_recovery.recovery_actions.back().command =
      "\"C:\\Users\\Public\\evil.exe\"";
  check_failed_with(
      fixture.integrity.validate_configuration(fixture.sign(bad_recovery)),
      ErrorCode::path_outside_trusted_directory,
      "recovery action outside trusted directory");
}

void test_account_substitution_is_rejected() {
  Fixture fixture;
  const ServiceConfiguration configuration = version_one_configuration();
  const SignedConfiguration signed_configuration =
      fixture.sign(configuration);
  check_ok(fixture.integrity.install(signed_configuration), "initial install");

  fixture.registry->mutable_observed().object_name = ".\\evil";
  check_failed_with(fixture.integrity.audit(signed_configuration),
                    ErrorCode::configuration_mismatch,
                    "registry account substitution");
  const auto blocked = fixture.integrity.prepare_launch(signed_configuration);
  CHECK(blocked.status.failed());
  CHECK(!blocked.plan.has_value());

  ServiceConfiguration malformed = configuration;
  malformed.object_name = "%USERNAME%";
  check_failed_with(fixture.integrity.validate_configuration(fixture.sign(malformed)),
                    ErrorCode::invalid_object_name,
                    "environment account substitution");
}

void test_legitimate_update_executes_new_declared_program() {
  Fixture fixture;
  const SignedConfiguration first = fixture.sign(version_one_configuration());
  const SignedConfiguration second = fixture.sign(version_two_configuration());
  check_ok(fixture.integrity.install(first), "install revision one");

  check_ok(fixture.integrity.update(second), "legitimate signed update");
  check_ok(fixture.integrity.audit(second), "audit revision two");
  CHECK(fixture.registry->observed().image_path.has_value());
  CHECK(*fixture.registry->observed().image_path ==
        version_two_configuration().image_path);
  CHECK(*fixture.registry->observed().object_name ==
        "NT AUTHORITY\\NetworkService");

  const auto decision = fixture.integrity.prepare_launch(second);
  check_ok(decision.status, "prepare revision two launch");
  CHECK(decision.plan.has_value());
  if (decision.plan.has_value()) {
    CHECK(decision.plan->executable ==
          "C:\\Program Files\\Contoso\\v2\\contoso-agent.exe");
    CHECK(decision.plan->argv.size() == 3);
    CHECK(decision.plan->argv[2] == "--revision=2");
  }

  const auto blocked = fixture.integrity.prepare_launch(first);
  CHECK(blocked.status.failed());
  CHECK(!blocked.plan.has_value());
}

void test_repair_restores_values_acl_and_declared_program() {
  Fixture fixture;
  const ServiceConfiguration configuration = version_one_configuration();
  const SignedConfiguration signed_configuration =
      fixture.sign(configuration);
  check_ok(fixture.integrity.install(signed_configuration), "initial install");

  fixture.registry->mutable_observed().image_path =
      "\"C:\\Users\\Public\\evil.exe\" --service";
  fixture.registry->mutable_observed().object_name = ".\\evil";
  fixture.registry->mutable_observed().dependencies = {"EvilDependency"};
  fixture.registry->mutable_observed().recovery_actions = {
      RecoveryAction{RecoveryActionType::run_program, 0,
                     "\"C:\\Users\\Public\\evil.exe\""}};
  fixture.registry->add_user_write_ace(fixture.registry->root_path());
  fixture.registry->add_unprivileged_subkey(fixture.registry->root_path() +
                                            "\\Parameters\\Evil");

  check_failed_with(fixture.integrity.audit(signed_configuration),
                    ErrorCode::configuration_mismatch,
                    "audit tampered configuration");
  check_ok(fixture.integrity.repair(signed_configuration), "repair");
  check_ok(fixture.integrity.audit(signed_configuration),
           "audit after full repair");
  CHECK(fixture.registry->observed().image_path.has_value());
  CHECK(*fixture.registry->observed().image_path == configuration.image_path);
  CHECK(*fixture.registry->observed().object_name == configuration.object_name);
  CHECK(fixture.registry->observed().dependencies.has_value());
  CHECK(*fixture.registry->observed().dependencies == configuration.dependencies);
  CHECK(fixture.registry->observed().recovery_actions.has_value());
  CHECK(*fixture.registry->observed().recovery_actions ==
        configuration.recovery_actions);

  const auto decision = fixture.integrity.prepare_launch(signed_configuration);
  check_ok(decision.status, "launch after full repair");
  CHECK(decision.plan.has_value());
  if (decision.plan.has_value()) {
    CHECK(decision.plan->executable ==
          "C:\\Program Files\\Contoso\\bin\\contoso-agent.exe");
  }
}

void test_file_acl_reparse_and_canonical_checks() {
  {
    Fixture fixture;
    fixture.files->add_secure(
        "C:\\Program Files\\Contoso\\bin\\contoso-agent.exe");
    fixture.files->relax_component(
        "C:\\Program Files\\Contoso\\bin\\contoso-agent.exe",
        "C:\\Program Files\\Contoso\\bin");
    const int writes_before = fixture.registry->write_count;
    check_failed_with(
        fixture.integrity.install(fixture.sign(version_one_configuration())),
        ErrorCode::path_user_writable, "user-writable executable directory");
    CHECK(fixture.registry->write_count == writes_before);
  }
  {
    Fixture fixture;
    fixture.files->make_reparse(
        "C:\\Program Files\\Contoso\\bin\\contoso-agent.exe", true);
    check_failed_with(
        fixture.integrity.install(fixture.sign(version_one_configuration())),
        ErrorCode::path_reparse_point, "reparse-point executable");
  }
  {
    Fixture fixture;
    fixture.files->redirect_canonical(
        "C:\\Program Files\\Contoso\\bin\\contoso-agent.exe",
        "C:\\Users\\Public\\evil.exe");
    check_failed_with(
        fixture.integrity.install(fixture.sign(version_one_configuration())),
        ErrorCode::path_reparse_point, "redirected canonical executable");
  }
  {
    Fixture fixture;
    fixture.files->add_secure(
        "C:\\Program Files\\Contoso\\bin\\contoso-agent.exe");
    auto missing = version_one_configuration();
    missing.image_path =
        "\"C:\\Program Files\\Contoso\\bin\\missing.exe\" --service";
    check_failed_with(fixture.integrity.install(fixture.sign(missing)),
                      ErrorCode::path_not_found, "missing executable");
  }
}

void test_signature_and_structural_validation() {
  Fixture fixture;
  const ServiceConfiguration configuration = version_one_configuration();
  SignedConfiguration signed_configuration = fixture.sign(configuration);

  signed_configuration.signature[0] ^= 0x80u;
  check_failed_with(fixture.integrity.validate_configuration(signed_configuration),
                    ErrorCode::invalid_signature, "tampered signature");
  signed_configuration = fixture.sign(configuration);
  signed_configuration.payload.image_path =
      "\"C:\\Users\\Public\\evil.exe\" --service";
  check_failed_with(fixture.integrity.validate_configuration(signed_configuration),
                    ErrorCode::invalid_signature, "tampered signed payload");

  HmacSha256Signer unknown{"unknown-key", signing_key};
  check_failed_with(
      fixture.integrity.validate_configuration(unknown.sign(configuration)),
      ErrorCode::invalid_signature, "unknown signing key");

  ServiceConfiguration dependency_injection = configuration;
  dependency_injection.dependencies = {"Tcpip\\..\\Evil"};
  check_failed_with(
      fixture.integrity.validate_configuration(
          fixture.sign(dependency_injection)),
      ErrorCode::invalid_dependency, "dependency injection");

  ServiceConfiguration unquoted = configuration;
  unquoted.image_path =
      "C:\\Program Files\\Contoso\\bin\\contoso-agent.exe --service";
  check_failed_with(fixture.integrity.validate_configuration(fixture.sign(unquoted)),
                    ErrorCode::invalid_image_path, "unquoted executable path");

  ServiceConfiguration environment = configuration;
  environment.image_path = "\"%SystemRoot%\\System32\\evil.exe\"";
  check_failed_with(
      fixture.integrity.validate_configuration(fixture.sign(environment)),
      ErrorCode::invalid_image_path, "environment-expanded image path");

  ServiceConfiguration alternate_data_stream = configuration;
  alternate_data_stream.image_path =
      "\"C:\\Program Files\\Contoso\\bin\\contoso-agent.exe:evil\"";
  check_failed_with(
      fixture.integrity.validate_configuration(
          fixture.sign(alternate_data_stream)),
      ErrorCode::invalid_image_path, "alternate data stream path");

  ServiceConfiguration recovery_override = configuration;
  recovery_override.recovery_actions.front().type =
      RecoveryActionType::restart_service;
  recovery_override.recovery_actions.front().command = "\"C:\\Users\\evil.exe\"";
  check_failed_with(
      fixture.integrity.validate_configuration(
          fixture.sign(recovery_override)),
      ErrorCode::invalid_recovery_action, "recovery action command override");
}

void test_acl_policy_subkey_and_owner_checks() {
  SecurityPolicy policy = SecurityPolicy::windows_service_defaults();
  SecurityDescriptor untrusted_owner;
  untrusted_owner.owner = "S-1-5-21-111111111-222222222-333333333-1001";
  untrusted_owner.dacl_present = true;
  untrusted_owner.dacl_protected = true;
  check_failed_with(
      netsec::validate_security_descriptor(untrusted_owner, policy,
                                          "registry:test"),
      ErrorCode::acl_untrusted_owner, "untrusted owner");

  SecurityDescriptor unprotected = privileged_descriptor(policy);
  unprotected.dacl_protected = false;
  check_failed_with(
      netsec::validate_security_descriptor(unprotected, policy,
                                          "registry:test"),
      ErrorCode::acl_unprotected, "inherited DACL");

  SecurityDescriptor null_dacl = privileged_descriptor(policy);
  null_dacl.dacl_present = false;
  check_failed_with(
      netsec::validate_security_descriptor(null_dacl, policy,
                                          "registry:test"),
      ErrorCode::acl_missing, "null DACL");

  SecurityDescriptor deny_untrusted = privileged_descriptor(policy);
  deny_untrusted.entries.push_back(AccessControlEntry{
      AccessControlEntry::Kind::deny, std::string(netsec::sids::everyone),
      netsec::registry_rights::write_mask, false});
  check_ok(netsec::validate_security_descriptor(deny_untrusted, policy,
                                                "registry:test"),
           "deny ACE does not grant user writes");
}

void test_install_from_scratch_creates_secure_subkeys() {
  Fixture fixture;
  const auto signed_configuration = fixture.sign(version_one_configuration());
  check_ok(fixture.integrity.install(signed_configuration), "fresh install");

  const RegistrySnapshot snapshot =
      fixture.registry->snapshot(fixture.registry->root_path());
  CHECK(snapshot.exists);
  CHECK(snapshot.keys.size() >= 2);
  const bool has_parameters = std::any_of(
      snapshot.keys.begin(), snapshot.keys.end(),
      [&fixture](const RegistryKeyAcl& key) {
        return lower_ascii(key.path) ==
               lower_ascii(fixture.registry->root_path() + "\\Parameters");
      });
  CHECK(has_parameters);
}

}  // namespace

int main() {
  test_hmac_vector();
  test_install_launch_and_declared_program();
  test_acl_relaxation_blocks_launch_and_update();
  test_path_substitution_is_rejected();
  test_account_substitution_is_rejected();
  test_legitimate_update_executes_new_declared_program();
  test_repair_restores_values_acl_and_declared_program();
  test_file_acl_reparse_and_canonical_checks();
  test_signature_and_structural_validation();
  test_acl_policy_subkey_and_owner_checks();
  test_install_from_scratch_creates_secure_subkeys();

  if (failures != 0) {
    std::cerr << failures << " test assertion(s) failed\n";
    return 1;
  }
  std::cout << "all configuration-integrity scenarios passed\n";
  return 0;
}
