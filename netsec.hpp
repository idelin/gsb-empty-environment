#pragma once

#include <array>
#include <cstdint>
#include <functional>
#include <map>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Bytes = std::vector<std::uint8_t>;
using Digest = std::array<std::uint8_t, 32>;

// SHA-256 and HMAC-SHA256 are provided so the component is self-contained and
// can be tested without a platform crypto dependency. Production deployments
// should inject an asymmetric verifier through ISignatureVerifier (for example
// an Ed25519/RSA verifier backed by the TPM or a secure element).
Digest sha256(std::span<const std::uint8_t> data);
Digest hmacSha256(std::span<const std::uint8_t> key,
                  std::span<const std::uint8_t> message);
std::string toHex(std::span<const std::uint8_t> bytes);

enum class VariableId : std::uint8_t {
  db = 0,
  dbx = 1,
  kek = 2,
  pk = 3,

  // UEFI spelling aliases.
  DB = db,
  DBX = dbx,
  KEK = kek,
  PK = pk,
};

std::string_view variableName(VariableId id);
std::optional<VariableId> parseVariable(std::string_view name);

struct VariableEntry {
  // Stable identity of the key, certificate, image digest, or signer chain
  // element. For the supplied tests this is usually a hex digest.
  std::string id;
  // Encoded payload (DER certificate, signature, hash, GUID, ...).
  Bytes data;

  friend bool operator==(const VariableEntry&, const VariableEntry&) = default;
};

using VariableEntries = std::vector<VariableEntry>;

struct VariableState {
  VariableId id = VariableId::db;
  std::uint64_t version = 0;
  VariableEntries entries;
  // SHA-256 over the canonical (id, version, entries) encoding. A state whose
  // digest does not match is rejected before it can influence a boot decision.
  Digest content_hash{};

  friend bool operator==(const VariableState&, const VariableState&) = default;
};

// Normalizes entry order (stable sort by id) and computes content_hash.
// Update packages are checked for duplicate ids before reaching this helper.
VariableState makeVariableState(VariableId id, std::uint64_t version,
                                VariableEntries entries);
Digest variablePayloadHash(VariableId id, std::uint64_t version,
                           const VariableEntries& entries);

// Persistence boundary. Implementations must make write() atomic at the
// record level: either the complete state is visible or the old state remains.
class IVariableStore {
 public:
  virtual ~IVariableStore() = default;
  virtual std::optional<VariableState> read(VariableId id) const = 0;
  virtual bool write(const VariableState& state) = 0;
};

// Small in-memory reference store. It is process-local; production firmware
// adapters should persist the same records in authenticated NVRAM.
class MemoryVariableStore final : public IVariableStore {
 public:
  std::optional<VariableState> read(VariableId id) const override;
  bool write(const VariableState& state) override;

 private:
  std::map<VariableId, VariableState> records_;
};

enum class UpdateMode : std::uint8_t {
  normal = 0,
  recovery = 1,

  Normal = normal,
  Recovery = recovery,
};

std::string_view updateModeName(UpdateMode mode);

// All fields that describe the update are covered by the package signature.
// The service recomputes payload_hash from entries, compares it with the
// declared value, then verifies the signature over the canonical transcript.
struct UpdatePackage {
  VariableId target = VariableId::db;
  std::uint64_t version = 0;
  Digest payload_hash{};
  VariableEntries entries;
  std::string signer_id;
  Bytes signature;
  UpdateMode mode = UpdateMode::normal;
  bool factory_key_install = false;
};

class ISignatureVerifier {
 public:
  virtual ~ISignatureVerifier() = default;
  virtual bool verify(std::string_view signer_id,
                      std::span<const std::uint8_t> message,
                      std::span<const std::uint8_t> signature) const = 0;
};

class CallbackSignatureVerifier final : public ISignatureVerifier {
 public:
  using Callback = std::function<bool(std::string_view,
                                      std::span<const std::uint8_t>,
                                      std::span<const std::uint8_t>)>;

  explicit CallbackSignatureVerifier(Callback callback);
  bool verify(std::string_view signer_id,
              std::span<const std::uint8_t> message,
              std::span<const std::uint8_t> signature) const override;

 private:
  Callback callback_;
};

class HmacSha256Verifier final : public ISignatureVerifier {
 public:
  void addKey(std::string signer_id, Bytes secret);
  bool verify(std::string_view signer_id,
              std::span<const std::uint8_t> message,
              std::span<const std::uint8_t> signature) const override;

 private:
  std::map<std::string, Bytes, std::less<>> keys_;
};

// Canonical message binding target, version, payload hash, mode, factory
// flag, entries, and signer identity. Exposed for external signing tools and
// tests; the service verifies this exact transcript.
Bytes updateSignatureTranscript(const UpdatePackage& package);

// Convenience helpers for the self-contained HMAC path. A real update signing
// service must keep the secret (or private key) outside the firmware process.
Bytes signUpdateTranscript(const UpdatePackage& package,
                           std::span<const std::uint8_t> secret);
UpdatePackage sealUpdatePackage(UpdatePackage package,
                                std::span<const std::uint8_t> secret);

struct Policy {
  // Authorities allowed to sign ordinary firmware updates.
  std::vector<std::string> update_signers;
  // Authorities allowed only while the caller explicitly enters recovery mode.
  std::vector<std::string> recovery_signers;
  // Authorities allowed to perform an explicit factory-key installation.
  std::vector<std::string> factory_signers;
  // Keys that must be present in a factory-key installation package.
  std::vector<std::string> factory_key_ids;

  // Recovery-mode boot may add these anchors to db, but only after dbx has
  // been checked. They never override revocation.
  std::vector<std::string> recovery_boot_anchors;
  // Factory keys become usable as recovery boot anchors only when this switch
  // is enabled. This is deliberately off by default.
  bool allow_factory_boot_anchors = false;

  // Recovery/factory operations require an explicit, out-of-band signal.
  bool require_physical_presence = true;
  bool allow_recovery_updates = true;
  bool allow_factory_key_install = false;

  // db can legitimately rotate keys, so no-shrink is opt-in for db. dbx is
  // always append-only because removing a revocation would re-trust an image.
  bool enforce_db_no_shrink = false;
};

struct UpdateContext {
  UpdateMode mode = UpdateMode::normal;
  bool physical_presence_confirmed = false;
};

enum class UpdateStatus : std::uint8_t {
  ok = 0,
  invalid_package,
  hash_mismatch,
  invalid_signature,
  unauthorized_signer,
  revoked_signer,
  recovery_not_allowed,
  physical_presence_required,
  factory_policy_violation,
  stale_version,
  empty_payload,
  dbx_shrink,
  corrupted_state,
  storage_write_failed,
  readback_mismatch,
  rollback_failed,
};

std::string_view updateStatusName(UpdateStatus status);

struct UpdateResult {
  UpdateStatus status = UpdateStatus::ok;
  std::string detail;

  [[nodiscard]] bool ok() const { return status == UpdateStatus::ok; }
};

enum class BootMode : std::uint8_t {
  normal = 0,
  recovery = 1,

  Normal = normal,
  Recovery = recovery,
};

struct BootComponent {
  // Digest/GUID of the image itself.
  std::string image_id;
  // Leaf-to-root signer chain. Every element is checked against dbx, so a
  // revoked intermediate/root cannot be hidden behind a trusted leaf.
  std::vector<std::string> signer_chain;
};

enum class BootStatus : std::uint8_t {
  allowed = 0,
  revoked,
  untrusted_signer,
  missing_trust_database,
  missing_revocation_database,
  corrupted_trust_database,
  corrupted_revocation_database,
};

std::string_view bootStatusName(BootStatus status);

struct BootDecision {
  BootStatus status = BootStatus::untrusted_signer;
  std::string detail;

  [[nodiscard]] bool allowed() const { return status == BootStatus::allowed; }
};

class SecureBootPolicy {
 public:
  SecureBootPolicy(IVariableStore& store, const ISignatureVerifier& verifier,
                   Policy policy);

  // Verifies, authorizes, applies, and read-back verifies one variable update.
  // No state is written until the signature and all anti-rollback checks pass.
  UpdateResult applyUpdate(const UpdatePackage& package,
                           const UpdateContext& context = {});

  // Evaluates a component against the persisted db/dbx. Revocation is checked
  // first and is never waived in recovery mode.
  BootDecision evaluateBoot(const BootComponent& component,
                            BootMode mode = BootMode::normal) const;

  const Policy& policy() const { return policy_; }

 private:
  IVariableStore& store_;
  const ISignatureVerifier& verifier_;
  Policy policy_;
};

// Convenient aliases for embedders.
using BootPolicyService = SecureBootPolicy;
using FirmwareUpdatePolicy = SecureBootPolicy;

}  // namespace netsec
