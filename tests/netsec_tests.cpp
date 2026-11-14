#include "netsec.hpp"

#include <algorithm>
#include <exception>
#include <iostream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace {

using namespace netsec;

[[noreturn]] void fail(const std::string& message) {
  throw std::runtime_error(message);
}

void require(bool condition, const std::string& message) {
  if (!condition) {
    fail(message);
  }
}

void requireUpdate(const UpdateResult& result, UpdateStatus expected,
                   std::string_view label) {
  if (result.status != expected) {
    fail(std::string(label) + ": expected " +
         std::string(updateStatusName(expected)) + ", got " +
         std::string(updateStatusName(result.status)) + " (" + result.detail +
         ")");
  }
}

void requireBoot(const BootDecision& decision, BootStatus expected,
                 std::string_view label) {
  if (decision.status != expected) {
    fail(std::string(label) + ": expected " +
         std::string(bootStatusName(expected)) + ", got " +
         std::string(bootStatusName(decision.status)) + " (" + decision.detail +
         ")");
  }
}

Bytes keyBytes(std::string_view text) {
  return Bytes(text.begin(), text.end());
}

VariableEntry entry(std::string id, std::string_view data = "") {
  return VariableEntry{std::move(id), keyBytes(data)};
}

bool hasEntry(const VariableState& state, std::string_view id) {
  return std::any_of(state.entries.begin(), state.entries.end(),
                     [id](const VariableEntry& value) {
                       return value.id == id;
                     });
}

UpdatePackage signedPackage(VariableId target, std::uint64_t version,
                            VariableEntries entries, std::string signer,
                            std::string_view secret,
                            UpdateMode mode = UpdateMode::normal,
                            bool factoryKeyInstall = false) {
  UpdatePackage package;
  package.target = target;
  package.version = version;
  package.entries = std::move(entries);
  package.signer_id = std::move(signer);
  package.mode = mode;
  package.factory_key_install = factoryKeyInstall;
  return sealUpdatePackage(std::move(package), keyBytes(secret));
}

struct Fixture {
  HmacSha256Verifier verifier;
  MemoryVariableStore store;
  Policy policy;
  SecureBootPolicy service;

  Fixture()
      : policy(makePolicy()),
        service(store, verifier, policy) {
    verifier.addKey("vendor-key", keyBytes("vendor-secret"));
    verifier.addKey("recovery-key", keyBytes("recovery-secret"));
    verifier.addKey("factory-key", keyBytes("factory-secret"));
    verifier.addKey("attacker-key", keyBytes("attacker-secret"));
  }

  static Policy makePolicy() {
    Policy value;
    value.update_signers = {"vendor-key"};
    value.recovery_signers = {"recovery-key"};
    value.factory_signers = {"factory-key"};
    value.factory_key_ids = {"factory-root"};
    value.recovery_boot_anchors = {"recovery-root"};
    value.allow_factory_boot_anchors = true;
    value.allow_recovery_updates = true;
    value.allow_factory_key_install = true;
    value.require_physical_presence = true;
    return value;
  }

  void provision() {
    requireUpdate(service.applyUpdate(signedPackage(
                     VariableId::pk, 1, {entry("platform-root")}, "vendor-key",
                     "vendor-secret")),
                 UpdateStatus::ok, "provision PK");
    requireUpdate(service.applyUpdate(signedPackage(
                     VariableId::kek, 1, {entry("vendor-kek")}, "vendor-key",
                     "vendor-secret")),
                 UpdateStatus::ok, "provision KEK");
    requireUpdate(service.applyUpdate(signedPackage(
                     VariableId::db, 1, {entry("vendor-root")}, "vendor-key",
                     "vendor-secret")),
                 UpdateStatus::ok, "provision db");
    requireUpdate(service.applyUpdate(signedPackage(
                     VariableId::dbx, 1, {entry("revoked-image-0")}, "vendor-key",
                     "vendor-secret")),
                 UpdateStatus::ok, "provision dbx");
  }

  VariableState state(VariableId id) const {
    const auto value = store.read(id);
    require(value.has_value(), "expected persisted state");
    return *value;
  }
};

void testLegitimateUpdateAndBootRevocation() {
  Fixture fixture;
  fixture.provision();

  const BootComponent trusted{"image-a", {"vendor-root"}};
  const BootComponent revoked{"revoked-image", {"revoked-old-root"}};

  requireBoot(fixture.service.evaluateBoot(trusted), BootStatus::allowed,
              "trusted image before update");
  requireBoot(fixture.service.evaluateBoot(revoked),
              BootStatus::untrusted_signer, "not yet revoked");

  requireUpdate(
      fixture.service.applyUpdate(signedPackage(
          VariableId::dbx, 2,
          {entry("revoked-image-0"), entry("revoked-old-root")}, "vendor-key",
          "vendor-secret")),
      UpdateStatus::ok, "legitimate dbx update");

  const auto dbx = fixture.state(VariableId::dbx);
  require(dbx.version == 2, "dbx version advanced");
  require(hasEntry(dbx, "revoked-old-root"), "new revocation persisted");

  requireBoot(fixture.service.evaluateBoot(revoked), BootStatus::revoked,
              "revoked component in normal boot");
  requireBoot(fixture.service.evaluateBoot(revoked, BootMode::recovery),
              BootStatus::revoked, "revocation applies in recovery boot");
  requireBoot(fixture.service.evaluateBoot(trusted), BootStatus::allowed,
              "trusted component still bootable");
}

void testDbxRollbackAndSemanticShrinkRejected() {
  Fixture fixture;
  fixture.provision();
  requireUpdate(fixture.service.applyUpdate(signedPackage(
                   VariableId::dbx, 2,
                   {entry("revoked-image-0"), entry("revoked-old-root")},
                   "vendor-key", "vendor-secret")),
               UpdateStatus::ok, "seed dbx v2");

  requireUpdate(
      fixture.service.applyUpdate(signedPackage(
          VariableId::dbx, 1, {entry("revoked-image-0")}, "vendor-key",
          "vendor-secret")),
      UpdateStatus::stale_version, "old dbx version");

  // Advancing the counter must not make an old/short revocation list valid.
  requireUpdate(
      fixture.service.applyUpdate(signedPackage(
          VariableId::dbx, 3, {entry("revoked-image-0")}, "vendor-key",
          "vendor-secret")),
      UpdateStatus::dbx_shrink, "dbx missing a prior revocation");

  const auto dbx = fixture.state(VariableId::dbx);
  require(dbx.version == 2, "dbx version unchanged after rejected updates");
  require(hasEntry(dbx, "revoked-old-root"),
          "prior revocation survived rejected updates");
  requireBoot(
      fixture.service.evaluateBoot({"image", {"revoked-old-root"}}),
      BootStatus::revoked, "revoked component remains rejected");
}

void testEmptyListRejected() {
  Fixture fixture;
  fixture.provision();

  // The package is correctly signed; policy still refuses to clear dbx.
  requireUpdate(fixture.service.applyUpdate(signedPackage(
                   VariableId::dbx, 2, {}, "vendor-key", "vendor-secret")),
               UpdateStatus::empty_payload, "empty dbx package");

  const auto dbx = fixture.state(VariableId::dbx);
  require(dbx.version == 1, "dbx unchanged after empty-list attempt");
  require(!dbx.entries.empty(), "dbx was not cleared");

  requireUpdate(fixture.service.applyUpdate(signedPackage(
                   VariableId::db, 2, {}, "vendor-key", "vendor-secret")),
               UpdateStatus::empty_payload, "empty db package");
  requireUpdate(fixture.service.applyUpdate(signedPackage(
                   VariableId::kek, 2, {}, "vendor-key", "vendor-secret")),
               UpdateStatus::empty_payload, "empty KEK package");
  requireUpdate(fixture.service.applyUpdate(signedPackage(
                   VariableId::pk, 2, {}, "vendor-key", "vendor-secret")),
               UpdateStatus::empty_payload, "empty PK package");
}

void testSignatureSubstitutionRejected() {
  Fixture fixture;
  fixture.provision();

  auto legitimate = signedPackage(
      VariableId::dbx, 2,
      {entry("revoked-image-0"), entry("revoked-new-root")}, "vendor-key",
      "vendor-secret");
  const auto other = signedPackage(
      VariableId::db, 2, {entry("vendor-root")}, "vendor-key", "vendor-secret");

  // Signature lifted from a different valid package.
  auto swappedSignature = legitimate;
  swappedSignature.signature = other.signature;
  requireUpdate(fixture.service.applyUpdate(swappedSignature),
                UpdateStatus::invalid_signature, "signature replacement");

  // Change the target and recompute the payload hash, but keep the old
  // signature: the signed transcript is still bound to the old target.
  auto swappedTarget = legitimate;
  swappedTarget.target = VariableId::db;
  swappedTarget.payload_hash =
      variablePayloadHash(swappedTarget.target, swappedTarget.version,
                          swappedTarget.entries);
  requireUpdate(fixture.service.applyUpdate(swappedTarget),
                UpdateStatus::invalid_signature, "target substitution");

  // Same for the version field.
  auto swappedVersion = legitimate;
  swappedVersion.version = 3;
  swappedVersion.payload_hash =
      variablePayloadHash(swappedVersion.target, swappedVersion.version,
                          swappedVersion.entries);
  requireUpdate(fixture.service.applyUpdate(swappedVersion),
                UpdateStatus::invalid_signature, "version substitution");

  // Replace the payload without replacing the hash or signature.
  auto swappedPayload = legitimate;
  swappedPayload.entries = {entry("revoked-image-0")};
  requireUpdate(fixture.service.applyUpdate(swappedPayload),
                UpdateStatus::hash_mismatch, "payload substitution");

  const auto dbx = fixture.state(VariableId::dbx);
  require(dbx.version == 1, "dbx unchanged after signature attacks");
  require(!hasEntry(dbx, "revoked-new-root"),
          "attacker-controlled revocation state was not written");
}

void testAllVariablesRequireStrictVersionIncrease() {
  Fixture fixture;
  fixture.provision();

  for (const auto id : {VariableId::db, VariableId::dbx, VariableId::kek,
                        VariableId::pk}) {
    const auto current = fixture.state(id);
    requireUpdate(fixture.service.applyUpdate(signedPackage(
                     id, current.version, current.entries, "vendor-key",
                     "vendor-secret")),
                 UpdateStatus::stale_version,
                 std::string(variableName(id)) + " equal version");
  }
}

class FaultyStore final : public IVariableStore {
 public:
  std::optional<VariableState> read(VariableId id) const override {
    return inner_.read(id);
  }

  bool write(const VariableState& state) override {
    if (failNextWrite_) {
      failNextWrite_ = false;
      return false;
    }
    if (corruptNextWrite_) {
      corruptNextWrite_ = false;
      auto corrupted = state;
      corrupted.version += 1;
      corrupted.content_hash = variablePayloadHash(
          corrupted.id, corrupted.version, corrupted.entries);
      return inner_.write(corrupted);
    }
    return inner_.write(state);
  }

  void failNextWrite() { failNextWrite_ = true; }
  void corruptNextWrite() { corruptNextWrite_ = true; }

 private:
  MemoryVariableStore inner_;
  bool failNextWrite_ = false;
  bool corruptNextWrite_ = false;
};

void testStorageFailureAndReadbackMismatch() {
  HmacSha256Verifier verifier;
  verifier.addKey("vendor-key", keyBytes("vendor-secret"));
  FaultyStore store;
  Policy policy = Fixture::makePolicy();
  SecureBootPolicy service(store, verifier, policy);

  requireUpdate(service.applyUpdate(signedPackage(
                   VariableId::db, 1, {entry("vendor-root")}, "vendor-key",
                   "vendor-secret")),
               UpdateStatus::ok, "fault store db");
  requireUpdate(service.applyUpdate(signedPackage(
                   VariableId::dbx, 1, {entry("revoked-old-root")}, "vendor-key",
                   "vendor-secret")),
               UpdateStatus::ok, "fault store dbx");

  store.failNextWrite();
  requireUpdate(service.applyUpdate(signedPackage(
                   VariableId::dbx, 2,
                   {entry("revoked-old-root"), entry("revoked-new-root")},
                   "vendor-key", "vendor-secret")),
               UpdateStatus::storage_write_failed, "storage write rejection");
  require(store.read(VariableId::dbx)->version == 1,
          "failed write did not advance dbx");

  store.corruptNextWrite();
  requireUpdate(service.applyUpdate(signedPackage(
                   VariableId::dbx, 2,
                   {entry("revoked-old-root"), entry("revoked-new-root")},
                   "vendor-key", "vendor-secret")),
               UpdateStatus::readback_mismatch,
               "silently corrupted write detected by read-back");

  require(store.read(VariableId::dbx)->version == 1,
          "previous dbx state restored after read-back mismatch");
  requireBoot(service.evaluateBoot({"image", {"revoked-old-root"}}),
              BootStatus::revoked, "revocation survives failed writes");
}

void testRecoveryAndFactoryKeyPolicy() {
  Fixture fixture;
  fixture.provision();

  const VariableEntries dbxV2 = {entry("revoked-image-0"),
                                 entry("revoked-recovery-root")};

  requireUpdate(
      fixture.service.applyUpdate(
          signedPackage(VariableId::dbx, 2, dbxV2, "recovery-key",
                        "recovery-secret", UpdateMode::recovery),
          UpdateContext{UpdateMode::recovery, false}),
      UpdateStatus::physical_presence_required,
      "recovery without physical presence");

  requireUpdate(
      fixture.service.applyUpdate(
          signedPackage(VariableId::dbx, 2, dbxV2, "recovery-key",
                        "recovery-secret", UpdateMode::recovery),
          UpdateContext{UpdateMode::recovery, true}),
      UpdateStatus::ok, "explicit recovery update");

  requireUpdate(
      fixture.service.applyUpdate(
          signedPackage(VariableId::dbx, 1, {entry("revoked-image-0")},
                        "recovery-key", "recovery-secret",
                        UpdateMode::recovery),
          UpdateContext{UpdateMode::recovery, true}),
      UpdateStatus::stale_version, "recovery cannot roll back dbx");

  requireUpdate(
      fixture.service.applyUpdate(
          signedPackage(VariableId::dbx, 3, {entry("revoked-image-0")},
                        "recovery-key", "recovery-secret",
                        UpdateMode::recovery),
          UpdateContext{UpdateMode::recovery, true}),
      UpdateStatus::dbx_shrink, "recovery cannot drop revocations");

  requireBoot(
      fixture.service.evaluateBoot({"image", {"revoked-recovery-root"}},
                                   BootMode::recovery),
      BootStatus::revoked, "recovery boot still honors dbx");
  requireBoot(fixture.service.evaluateBoot({"image", {"factory-root"}},
                                           BootMode::recovery),
              BootStatus::untrusted_signer,
              "factory root is not trusted before installation");

  // A recovery authority cannot masquerade as an ordinary update authority.
  requireUpdate(fixture.service.applyUpdate(signedPackage(
                   VariableId::db, 2, {entry("vendor-root")}, "recovery-key",
                   "recovery-secret")),
               UpdateStatus::unauthorized_signer,
               "recovery key outside recovery mode");
  requireUpdate(
      fixture.service.applyUpdate(
          signedPackage(VariableId::db, 2, {entry("vendor-root")},
                        "vendor-key", "vendor-secret", UpdateMode::normal),
          UpdateContext{UpdateMode::recovery, true}),
      UpdateStatus::invalid_package,
      "runtime recovery flag cannot change a normally signed package");

  // Factory keys may only be installed into PK/KEK, never a revocation list.
  requireUpdate(
      fixture.service.applyUpdate(
          signedPackage(VariableId::dbx, 3,
                        {entry("revoked-image-0"), entry("revoked-recovery-root")},
                        "factory-key", "factory-secret", UpdateMode::recovery,
                        true),
          UpdateContext{UpdateMode::recovery, true}),
      UpdateStatus::factory_policy_violation,
      "factory install into dbx");

  requireUpdate(
      fixture.service.applyUpdate(
          signedPackage(VariableId::pk, 2,
                        {entry("platform-root"), entry("factory-root")},
                        "factory-key", "factory-secret", UpdateMode::recovery,
                        true),
          UpdateContext{UpdateMode::recovery, false}),
      UpdateStatus::physical_presence_required,
      "factory install without physical presence");

  requireUpdate(
      fixture.service.applyUpdate(
          signedPackage(VariableId::pk, 2, {entry("platform-root")},
                        "factory-key", "factory-secret", UpdateMode::recovery,
                        true),
          UpdateContext{UpdateMode::recovery, true}),
      UpdateStatus::factory_policy_violation,
      "factory package missing configured key");

  requireUpdate(
      fixture.service.applyUpdate(
          signedPackage(VariableId::pk, 2,
                        {entry("platform-root"), entry("factory-root")},
                        "factory-key", "factory-secret", UpdateMode::recovery,
                        true),
          UpdateContext{UpdateMode::recovery, true}),
      UpdateStatus::ok, "explicit factory key installation");

  const auto pk = fixture.state(VariableId::pk);
  require(pk.version == 2 && hasEntry(pk, "factory-root"),
          "factory key was installed");

  // Factory boot anchors are usable only in recovery and only when the policy
  // explicitly enables them. Revocation still wins over this anchor.
  requireBoot(fixture.service.evaluateBoot({"image", {"factory-root"}}),
              BootStatus::untrusted_signer,
              "factory root is not a normal boot anchor");
  requireBoot(fixture.service.evaluateBoot({"image", {"factory-root"}},
                                           BootMode::recovery),
              BootStatus::allowed, "factory root as explicit recovery anchor");
  requireBoot(fixture.service.evaluateBoot({"image", {"vendor-root"}},
                                           BootMode::recovery),
              BootStatus::allowed, "db anchor remains usable in recovery");
}

void testRevokedUpdateSignerRejected() {
  Fixture fixture;
  fixture.provision();
  requireUpdate(fixture.service.applyUpdate(signedPackage(
                   VariableId::dbx, 2,
                   {entry("revoked-image-0"), entry("vendor-key")},
                   "vendor-key", "vendor-secret")),
               UpdateStatus::ok, "revoke update authority");

  // The same signed package path must now be refused before any write.
  requireUpdate(fixture.service.applyUpdate(signedPackage(
                   VariableId::db, 2, {entry("vendor-root")}, "vendor-key",
                   "vendor-secret")),
               UpdateStatus::revoked_signer, "revoked signer update");
  require(fixture.state(VariableId::db).version == 1,
          "revoked signer did not update db");
}

void testCorruptedStateFailsClosed() {
  Fixture fixture;
  fixture.provision();

  auto dbx = fixture.store.read(VariableId::dbx).value();
  dbx.version += 5;  // tamper without recomputing content_hash
  require(fixture.store.write(dbx), "write tampered record for test");

  requireBoot(fixture.service.evaluateBoot({"image", {"vendor-root"}}),
              BootStatus::corrupted_revocation_database,
              "corrupt dbx fails closed");
  requireUpdate(fixture.service.applyUpdate(signedPackage(
                   VariableId::dbx, 2,
                   {entry("revoked-image-0"), entry("revoked-new-root")},
                   "vendor-key", "vendor-secret")),
               UpdateStatus::corrupted_state, "corrupt state blocks update");
}

void testSha256KnownVector() {
  const auto digest = sha256(std::span<const std::uint8_t>(
      reinterpret_cast<const std::uint8_t*>("abc"), 3));
  require(toHex(digest) ==
              "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
          "SHA-256 known vector");

  const auto mac = hmacSha256(
      keyBytes("key"),
      keyBytes("The quick brown fox jumps over the lazy dog"));
  require(toHex(mac) ==
              "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8",
          "HMAC-SHA256 known vector");
}

}  // namespace

int main() {
  try {
    testSha256KnownVector();
    testLegitimateUpdateAndBootRevocation();
    testDbxRollbackAndSemanticShrinkRejected();
    testEmptyListRejected();
    testSignatureSubstitutionRejected();
    testAllVariablesRequireStrictVersionIncrease();
    testStorageFailureAndReadbackMismatch();
    testRecoveryAndFactoryKeyPolicy();
    testRevokedUpdateSignerRejected();
    testCorruptedStateFailsClosed();
    std::cout << "all secure boot policy tests passed\n";
    return 0;
  } catch (const std::exception& error) {
    std::cerr << "test failure: " << error.what() << '\n';
    return 1;
  }
}
