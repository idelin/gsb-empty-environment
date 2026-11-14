#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <memory>
#include <optional>
#include <set>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

// Well-known Windows principals are represented by SIDs, never by localised
// account names.  Adapters must return SIDs from security descriptors.
namespace sids {
inline constexpr std::string_view everyone = "S-1-1-0";
inline constexpr std::string_view authenticated_users = "S-1-5-11";
inline constexpr std::string_view local_system = "S-1-5-18";
inline constexpr std::string_view local_service = "S-1-5-19";
inline constexpr std::string_view network_service = "S-1-5-20";
inline constexpr std::string_view builtin_users = "S-1-5-32-545";
inline constexpr std::string_view builtin_administrators = "S-1-5-32-544";
inline constexpr std::string_view creator_owner = "S-1-3-0";
inline constexpr std::string_view creator_group = "S-1-3-1";
inline constexpr std::string_view trusted_installer =
    "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464";
}  // namespace sids

inline constexpr std::string_view service_registry_root =
    "HKLM\\SYSTEM\\CurrentControlSet\\Services\\";

enum class ErrorCode : std::uint8_t {
  none = 0,
  malformed_configuration,
  invalid_signature,
  invalid_service_name,
  invalid_image_path,
  malformed_command_line,
  path_outside_trusted_directory,
  path_not_found,
  path_not_regular_file,
  path_reparse_point,
  path_component_unverified,
  path_user_writable,
  invalid_object_name,
  invalid_dependency,
  invalid_recovery_action,
  configuration_mismatch,
  service_name_mismatch,
  registry_key_missing,
  registry_unexpected_key,
  acl_missing,
  acl_untrusted_owner,
  acl_untrusted_writer,
  acl_unprotected,
  backend_failure,
  postcondition_failed,
  internal_error,
};

struct Diagnostic {
  ErrorCode code{ErrorCode::internal_error};
  std::string field;
  std::string message;
};

class ValidationResult {
 public:
  ValidationResult() = default;
  explicit ValidationResult(Diagnostic diagnostic);

  [[nodiscard]] bool ok() const noexcept { return diagnostics.empty(); }
  [[nodiscard]] bool failed() const noexcept { return !ok(); }

  void add(ErrorCode code, std::string field, std::string message);
  void merge(const ValidationResult& other);
  [[nodiscard]] std::string summary() const;

  std::vector<Diagnostic> diagnostics;
};

enum class RecoveryActionType : std::uint8_t {
  none = 0,
  restart_service = 1,
  restart_computer = 2,
  run_program = 3,
};

struct RecoveryAction {
  RecoveryActionType type{RecoveryActionType::none};
  std::uint32_t delay_milliseconds{0};
  std::string command;

  friend bool operator==(const RecoveryAction&, const RecoveryAction&) = default;
};

struct ServiceConfiguration {
  std::string service_name;
  std::string image_path;
  std::string object_name;
  std::vector<std::string> dependencies;
  std::vector<RecoveryAction> recovery_actions;

  friend bool operator==(const ServiceConfiguration&,
                         const ServiceConfiguration&) = default;
};

enum class SignatureAlgorithm : std::uint8_t {
  hmac_sha256 = 1,
};

struct SignedConfiguration {
  std::string key_id;
  SignatureAlgorithm algorithm{SignatureAlgorithm::hmac_sha256};
  std::vector<std::uint8_t> signature;
  ServiceConfiguration payload;
};

// The canonical message binds the key id, algorithm and every configuration
// field.  Length prefixes make the encoding unambiguous for arbitrary strings.
[[nodiscard]] std::vector<std::uint8_t> canonical_configuration_bytes(
    const ServiceConfiguration& configuration);
[[nodiscard]] std::vector<std::uint8_t> signed_configuration_message(
    const SignedConfiguration& signed_configuration);

[[nodiscard]] std::vector<std::uint8_t> hmac_sha256(
    std::span<const std::uint8_t> key, std::span<const std::uint8_t> data);
[[nodiscard]] std::string hex_encode(std::span<const std::uint8_t> bytes);

class SignatureVerifier {
 public:
  virtual ~SignatureVerifier() = default;
  [[nodiscard]] virtual bool verify(
      std::string_view key_id, SignatureAlgorithm algorithm,
      std::span<const std::uint8_t> message,
      std::span<const std::uint8_t> signature) const = 0;
};

// A dependency-free verifier used by the reference tests and offline
// deployments.  Production agents may inject a platform key-store backed
// asymmetric verifier through the SignatureVerifier interface.
class HmacSha256Verifier final : public SignatureVerifier {
 public:
  HmacSha256Verifier() = default;
  explicit HmacSha256Verifier(
      std::map<std::string, std::vector<std::uint8_t>> keys);

  bool add_key(std::string key_id, std::vector<std::uint8_t> key);
  [[nodiscard]] bool verify(
      std::string_view key_id, SignatureAlgorithm algorithm,
      std::span<const std::uint8_t> message,
      std::span<const std::uint8_t> signature) const override;

 private:
  std::map<std::string, std::vector<std::uint8_t>> keys_;
};

class HmacSha256Signer {
 public:
  HmacSha256Signer(std::string key_id, std::vector<std::uint8_t> key);
  [[nodiscard]] SignedConfiguration sign(ServiceConfiguration payload) const;

 private:
  std::string key_id_;
  std::vector<std::uint8_t> key_;
};

namespace registry_rights {
inline constexpr std::uint32_t set_value = 0x00000002u;
inline constexpr std::uint32_t create_sub_key = 0x00000004u;
inline constexpr std::uint32_t create_link = 0x00000020u;
inline constexpr std::uint32_t delete_key = 0x00010000u;
inline constexpr std::uint32_t write_dac = 0x00040000u;
inline constexpr std::uint32_t write_owner = 0x00080000u;
inline constexpr std::uint32_t generic_write = 0x40000000u;
inline constexpr std::uint32_t generic_all = 0x10000000u;
inline constexpr std::uint32_t maximum_allowed = 0x02000000u;
inline constexpr std::uint32_t write_mask =
    set_value | create_sub_key | create_link | delete_key | write_dac |
    write_owner | generic_write | generic_all | maximum_allowed;
}  // namespace registry_rights

struct AccessControlEntry {
  enum class Kind : std::uint8_t { allow = 0, deny = 1 };

  Kind kind{Kind::allow};
  std::string principal;
  std::uint32_t rights{0};
  bool inherited{false};
};

struct SecurityDescriptor {
  std::string owner;
  bool dacl_present{true};
  bool dacl_protected{false};
  std::vector<AccessControlEntry> entries;
};

struct SecurityPolicy {
  std::set<std::string> privileged_owners;
  std::set<std::string> privileged_writers;
  std::vector<std::string> trusted_directories;
  std::uint32_t dangerous_write_rights{registry_rights::write_mask};
  bool require_protected_dacl{true};
  // File paths are checked together with every ancestor up to the volume
  // root, so an inherited file DACL is acceptable when the whole chain is
  // privileged-only.  Registry keys default to a protected DACL.
  bool require_protected_file_dacl{false};
  bool case_insensitive_paths{true};
  std::size_t max_recovery_actions{16};
  std::size_t max_dependencies{128};

  [[nodiscard]] static SecurityPolicy windows_service_defaults();
};

enum class SecurityObject : std::uint8_t {
  registry_key = 0,
  file_system = 1,
};

[[nodiscard]] ValidationResult validate_security_descriptor(
    const SecurityDescriptor& descriptor, const SecurityPolicy& policy,
    std::string_view location,
    SecurityObject object = SecurityObject::registry_key);

struct NormalizedPath {
  bool valid{false};
  std::string value;
  std::string error;
};

[[nodiscard]] NormalizedPath normalize_windows_path(std::string_view path);
[[nodiscard]] bool path_is_within_root(std::string_view path,
                                       std::string_view root,
                                       bool case_insensitive = true);

struct ParsedCommandLine {
  bool valid{false};
  std::vector<std::string> argv;
  std::string error;
};

[[nodiscard]] ParsedCommandLine parse_windows_command_line(
    std::string_view command_line);

struct RegistryKeyAcl {
  std::string path;
  SecurityDescriptor security;
};

struct ObservedServiceConfiguration {
  std::optional<std::string> image_path;
  std::optional<std::string> object_name;
  std::optional<std::vector<std::string>> dependencies;
  std::optional<std::vector<RecoveryAction>> recovery_actions;
};

struct RegistrySnapshot {
  std::string key_path;
  bool exists{false};
  ObservedServiceConfiguration observed;
  // Includes the service key and every subkey recursively.  An adapter must
  // not omit a subkey from this list.
  std::vector<RegistryKeyAcl> keys;
};

class ServiceRegistry {
 public:
  virtual ~ServiceRegistry() = default;

  [[nodiscard]] virtual RegistrySnapshot snapshot(
      std::string_view service_key) const = 0;

  // Writes only the declared values.  Newly created keys must be created with
  // an owner and DACL satisfying policy; existing DACLs must not be weakened.
  [[nodiscard]] virtual ValidationResult write_configuration(
      std::string_view service_key,
      const ServiceConfiguration& configuration,
      const SecurityPolicy& policy) = 0;

  // Recursively resets ownership and DACLs to the privileged set, protecting
  // the DACL so that parent inheritance cannot reintroduce user write access.
  [[nodiscard]] virtual ValidationResult enforce_acl(
      std::string_view service_key, const SecurityPolicy& policy) = 0;
};

struct FileSystemComponent {
  std::string path;
  bool is_directory{false};
  bool is_reparse_point{false};
  SecurityDescriptor security;
};

struct PathSnapshot {
  bool exists{false};
  bool is_regular_file{false};
  bool traverses_reparse_point{false};
  std::string canonical_path;
  // The target file first, followed by every existing ancestor up to the
  // volume root.  The integrity engine rejects an incomplete list.
  std::vector<FileSystemComponent> components;
};

class FileSystemInspector {
 public:
  virtual ~FileSystemInspector() = default;
  [[nodiscard]] virtual PathSnapshot inspect(
      std::string_view path) const = 0;
};

[[nodiscard]] std::string service_registry_key(std::string_view service_name);

class ConfigurationIntegrity {
 public:
  struct LaunchPlan {
    std::string service_name;
    std::string executable;
    std::vector<std::string> argv;
  };

  struct LaunchDecision {
    ValidationResult status;
    std::optional<LaunchPlan> plan;
  };

  ConfigurationIntegrity(
      std::shared_ptr<const SignatureVerifier> verifier,
      std::shared_ptr<ServiceRegistry> registry,
      std::shared_ptr<const FileSystemInspector> files,
      SecurityPolicy policy = SecurityPolicy::windows_service_defaults());

  [[nodiscard]] const SecurityPolicy& policy() const noexcept { return policy_; }

  // Signature, field syntax, trusted location and file ACL checks.
  [[nodiscard]] ValidationResult validate_configuration(
      const SignedConfiguration& signed_configuration) const;

  // Full runtime audit: signed payload, registry values and every ACL.
  [[nodiscard]] ValidationResult audit(
      const SignedConfiguration& signed_configuration) const;

  // Installation is allowed to create or repair the key ACL, but the final
  // state is audited before success is returned.
  [[nodiscard]] ValidationResult install(
      const SignedConfiguration& signed_configuration) const;

  // An update is accepted only when the existing key tree is already
  // privileged-only.  A relaxed ACL must go through repair().
  [[nodiscard]] ValidationResult update(
      const SignedConfiguration& signed_configuration) const;

  // Restores the signed values and re-secures the entire key tree.
  [[nodiscard]] ValidationResult repair(
      const SignedConfiguration& signed_configuration) const;

  // Audits immediately before returning a launch plan.  The returned argv is
  // built from the signed image path, never from untrusted registry content.
  [[nodiscard]] LaunchDecision prepare_launch(
      const SignedConfiguration& signed_configuration) const;

 private:
  [[nodiscard]] ValidationResult validate_program_path(
      std::string_view command_line, std::string_view field) const;
  [[nodiscard]] ValidationResult validate_registry_acl(
      const RegistrySnapshot& snapshot, std::string_view expected_key) const;
  [[nodiscard]] ValidationResult write_then_audit(
      const SignedConfiguration& signed_configuration, bool require_existing,
      bool preaudit_acl) const;

  std::shared_ptr<const SignatureVerifier> verifier_;
  std::shared_ptr<ServiceRegistry> registry_;
  std::shared_ptr<const FileSystemInspector> files_;
  SecurityPolicy policy_;
};

}  // namespace netsec
