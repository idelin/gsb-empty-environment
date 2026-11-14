#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstring>
#include <limits>
#include <utility>

namespace netsec {
namespace {

constexpr std::uint32_t kSha256RoundConstants[64] = {
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU,
    0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U, 0xd807aa98U, 0x12835b01U,
    0x243185beU, 0x550c7dc3U, 0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U,
    0xc19bf174U, 0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
    0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU, 0x983e5152U,
    0xa831c66dU, 0xb00327c8U, 0xbf597fc7U, 0xc6e00bf3U, 0xd5a79147U,
    0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU,
    0x53380d13U, 0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
    0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U, 0xd192e819U,
    0xd6990624U, 0xf40e3585U, 0x106aa070U, 0x19a4c116U, 0x1e376c08U,
    0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU,
    0x682e6ff3U, 0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U};

constexpr std::uint32_t rotateRight(std::uint32_t value,
                                    std::uint32_t amount) {
  return (value >> amount) | (value << (32U - amount));
}

void sha256Block(const std::uint8_t* block, std::array<std::uint32_t, 8>& state) {
  std::array<std::uint32_t, 64> words{};
  for (std::size_t i = 0; i < 16; ++i) {
    words[i] = (static_cast<std::uint32_t>(block[i * 4]) << 24U) |
               (static_cast<std::uint32_t>(block[i * 4 + 1]) << 16U) |
               (static_cast<std::uint32_t>(block[i * 4 + 2]) << 8U) |
               static_cast<std::uint32_t>(block[i * 4 + 3]);
  }
  for (std::size_t i = 16; i < 64; ++i) {
    const auto s0 = rotateRight(words[i - 15], 7U) ^
                    rotateRight(words[i - 15], 18U) ^ (words[i - 15] >> 3U);
    const auto s1 = rotateRight(words[i - 2], 17U) ^
                    rotateRight(words[i - 2], 19U) ^ (words[i - 2] >> 10U);
    words[i] = words[i - 16] + s0 + words[i - 7] + s1;
  }

  auto a = state[0];
  auto b = state[1];
  auto c = state[2];
  auto d = state[3];
  auto e = state[4];
  auto f = state[5];
  auto g = state[6];
  auto h = state[7];

  for (std::size_t i = 0; i < 64; ++i) {
    const auto sum1 = rotateRight(e, 6U) ^ rotateRight(e, 11U) ^
                      rotateRight(e, 25U);
    const auto choose = (e & f) ^ (~e & g);
    const auto temp1 = h + sum1 + choose + kSha256RoundConstants[i] + words[i];
    const auto sum0 = rotateRight(a, 2U) ^ rotateRight(a, 13U) ^
                      rotateRight(a, 22U);
    const auto majority = (a & b) ^ (a & c) ^ (b & c);
    const auto temp2 = sum0 + majority;

    h = g;
    g = f;
    f = e;
    e = d + temp1;
    d = c;
    c = b;
    b = a;
    a = temp1 + temp2;
  }

  state[0] += a;
  state[1] += b;
  state[2] += c;
  state[3] += d;
  state[4] += e;
  state[5] += f;
  state[6] += g;
  state[7] += h;
}

void appendU8(Bytes& out, std::uint8_t value) { out.push_back(value); }

void appendU32(Bytes& out, std::uint32_t value) {
  out.push_back(static_cast<std::uint8_t>(value & 0xffU));
  out.push_back(static_cast<std::uint8_t>((value >> 8U) & 0xffU));
  out.push_back(static_cast<std::uint8_t>((value >> 16U) & 0xffU));
  out.push_back(static_cast<std::uint8_t>((value >> 24U) & 0xffU));
}

void appendU64(Bytes& out, std::uint64_t value) {
  for (unsigned shift = 0; shift < 64; shift += 8) {
    out.push_back(static_cast<std::uint8_t>((value >> shift) & 0xffU));
  }
}

void appendBytes(Bytes& out, std::span<const std::uint8_t> bytes) {
  if (bytes.size() > std::numeric_limits<std::uint32_t>::max()) {
    return;
  }
  appendU32(out, static_cast<std::uint32_t>(bytes.size()));
  out.insert(out.end(), bytes.begin(), bytes.end());
}

void appendString(Bytes& out, std::string_view text) {
  appendBytes(out, std::span<const std::uint8_t>(
                       reinterpret_cast<const std::uint8_t*>(text.data()),
                       text.size()));
}

VariableEntries normalizedEntries(const VariableEntries& entries) {
  auto normalized = entries;
  std::stable_sort(normalized.begin(), normalized.end(),
                   [](const VariableEntry& left, const VariableEntry& right) {
                     return left.id < right.id;
                   });
  return normalized;
}

Bytes encodeEntries(const VariableEntries& entries) {
  Bytes encoded;
  const auto normalized = normalizedEntries(entries);
  appendU32(encoded, static_cast<std::uint32_t>(normalized.size()));
  for (const auto& entry : normalized) {
    appendString(encoded, entry.id);
    appendBytes(encoded, entry.data);
  }
  return encoded;
}

Bytes encodePayload(VariableId id, std::uint64_t version,
                    const VariableEntries& entries) {
  Bytes encoded;
  appendString(encoded, "netsec.secureboot.variable.v1");
  appendU8(encoded, static_cast<std::uint8_t>(id));
  appendU64(encoded, version);
  const auto entryBytes = encodeEntries(entries);
  encoded.insert(encoded.end(), entryBytes.begin(), entryBytes.end());
  return encoded;
}

bool constantTimeEqual(std::span<const std::uint8_t> left,
                       std::span<const std::uint8_t> right) {
  if (left.size() != right.size()) {
    return false;
  }
  std::uint8_t difference = 0;
  for (std::size_t i = 0; i < left.size(); ++i) {
    difference = static_cast<std::uint8_t>(difference | (left[i] ^ right[i]));
  }
  return difference == 0;
}

bool hasDuplicateIds(const VariableEntries& entries) {
  auto ids = std::vector<std::string>{};
  ids.reserve(entries.size());
  for (const auto& entry : entries) {
    ids.push_back(entry.id);
  }
  std::sort(ids.begin(), ids.end());
  return std::adjacent_find(ids.begin(), ids.end()) != ids.end();
}

bool containsEntryId(const VariableEntries& entries, std::string_view id) {
  return std::any_of(entries.begin(), entries.end(),
                     [id](const VariableEntry& entry) {
                       return entry.id == id;
                     });
}

bool containsEntry(const VariableEntries& entries, const VariableEntry& target) {
  return std::any_of(entries.begin(), entries.end(),
                     [&target](const VariableEntry& entry) {
                       return entry.id == target.id && entry.data == target.data;
                     });
}

bool containsAllEntries(const VariableEntries& superset,
                        const VariableEntries& subset) {
  return std::all_of(subset.begin(), subset.end(),
                     [&superset](const VariableEntry& entry) {
                       return containsEntry(superset, entry);
                     });
}

bool containsString(const std::vector<std::string>& values,
                    std::string_view target) {
  return std::any_of(values.begin(), values.end(),
                     [target](const std::string& value) {
                       return value == target;
                     });
}

bool validVariableId(VariableId id) {
  switch (id) {
    case VariableId::db:
    case VariableId::dbx:
    case VariableId::kek:
    case VariableId::pk:
      return true;
  }
  return false;
}

bool validUpdateMode(UpdateMode mode) {
  switch (mode) {
    case UpdateMode::normal:
    case UpdateMode::recovery:
      return true;
  }
  return false;
}

bool validState(const VariableState& state) {
  return state.version != 0 && !hasDuplicateIds(state.entries) &&
         state.content_hash ==
             variablePayloadHash(state.id, state.version, state.entries);
}

UpdateResult failure(UpdateStatus status, std::string detail) {
  return UpdateResult{status, std::move(detail)};
}

}  // namespace

Digest sha256(std::span<const std::uint8_t> data) {
  std::array<std::uint32_t, 8> state = {
      0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
      0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U};

  const auto fullBlocks = data.size() / 64U;
  for (std::size_t block = 0; block < fullBlocks; ++block) {
    sha256Block(data.data() + block * 64U, state);
  }

  std::array<std::uint8_t, 128> tail{};
  const auto remainder = data.size() % 64U;
  if (remainder != 0) {
    std::memcpy(tail.data(), data.data() + fullBlocks * 64U, remainder);
  }
  tail[remainder] = 0x80U;

  const std::size_t paddingLength = remainder < 56U ? 64U : 128U;
  const auto bitLength = static_cast<std::uint64_t>(data.size()) * 8U;
  for (unsigned i = 0; i < 8; ++i) {
    tail[paddingLength - 1U - i] =
        static_cast<std::uint8_t>((bitLength >> (8U * i)) & 0xffU);
  }
  sha256Block(tail.data(), state);
  if (paddingLength == 128U) {
    sha256Block(tail.data() + 64U, state);
  }

  Digest result{};
  for (std::size_t word = 0; word < state.size(); ++word) {
    result[word * 4] = static_cast<std::uint8_t>((state[word] >> 24U) & 0xffU);
    result[word * 4 + 1] =
        static_cast<std::uint8_t>((state[word] >> 16U) & 0xffU);
    result[word * 4 + 2] =
        static_cast<std::uint8_t>((state[word] >> 8U) & 0xffU);
    result[word * 4 + 3] = static_cast<std::uint8_t>(state[word] & 0xffU);
  }
  return result;
}

Digest hmacSha256(std::span<const std::uint8_t> key,
                  std::span<const std::uint8_t> message) {
  constexpr std::size_t blockSize = 64;
  std::array<std::uint8_t, blockSize> normalizedKey{};
  if (key.size() > blockSize) {
    const auto hashedKey = sha256(key);
    std::copy(hashedKey.begin(), hashedKey.end(), normalizedKey.begin());
  } else {
    std::copy(key.begin(), key.end(), normalizedKey.begin());
  }

  std::array<std::uint8_t, blockSize> innerPad{};
  std::array<std::uint8_t, blockSize> outerPad{};
  for (std::size_t i = 0; i < blockSize; ++i) {
    innerPad[i] = static_cast<std::uint8_t>(normalizedKey[i] ^ 0x36U);
    outerPad[i] = static_cast<std::uint8_t>(normalizedKey[i] ^ 0x5cU);
  }

  Bytes inner;
  inner.reserve(blockSize + message.size());
  inner.insert(inner.end(), innerPad.begin(), innerPad.end());
  inner.insert(inner.end(), message.begin(), message.end());
  const auto innerHash = sha256(inner);

  Bytes outer;
  outer.reserve(blockSize + innerHash.size());
  outer.insert(outer.end(), outerPad.begin(), outerPad.end());
  outer.insert(outer.end(), innerHash.begin(), innerHash.end());
  return sha256(outer);
}

std::string toHex(std::span<const std::uint8_t> bytes) {
  static constexpr char digits[] = "0123456789abcdef";
  std::string result;
  result.reserve(bytes.size() * 2U);
  for (const auto byte : bytes) {
    result.push_back(digits[(byte >> 4U) & 0x0fU]);
    result.push_back(digits[byte & 0x0fU]);
  }
  return result;
}

std::string_view variableName(VariableId id) {
  switch (id) {
    case VariableId::db:
      return "db";
    case VariableId::dbx:
      return "dbx";
    case VariableId::kek:
      return "KEK";
    case VariableId::pk:
      return "PK";
  }
  return "unknown";
}

std::optional<VariableId> parseVariable(std::string_view name) {
  if (name == "db" || name == "DB") {
    return VariableId::db;
  }
  if (name == "dbx" || name == "DBX") {
    return VariableId::dbx;
  }
  if (name == "kek" || name == "KEK") {
    return VariableId::kek;
  }
  if (name == "pk" || name == "PK") {
    return VariableId::pk;
  }
  return std::nullopt;
}

Digest variablePayloadHash(VariableId id, std::uint64_t version,
                           const VariableEntries& entries) {
  const auto encoded = encodePayload(id, version, entries);
  return sha256(encoded);
}

VariableState makeVariableState(VariableId id, std::uint64_t version,
                                VariableEntries entries) {
  auto normalized = normalizedEntries(entries);
  const auto hash = variablePayloadHash(id, version, normalized);
  return VariableState{id, version, std::move(normalized), hash};
}

std::optional<VariableState> MemoryVariableStore::read(VariableId id) const {
  const auto found = records_.find(id);
  if (found == records_.end()) {
    return std::nullopt;
  }
  return found->second;
}

bool MemoryVariableStore::write(const VariableState& state) {
  records_[state.id] = state;
  return true;
}

std::string_view updateModeName(UpdateMode mode) {
  switch (mode) {
    case UpdateMode::normal:
      return "normal";
    case UpdateMode::recovery:
      return "recovery";
  }
  return "unknown";
}

CallbackSignatureVerifier::CallbackSignatureVerifier(Callback callback)
    : callback_(std::move(callback)) {}

bool CallbackSignatureVerifier::verify(
    std::string_view signer_id, std::span<const std::uint8_t> message,
    std::span<const std::uint8_t> signature) const {
  return callback_ && callback_(signer_id, message, signature);
}

void HmacSha256Verifier::addKey(std::string signer_id, Bytes secret) {
  keys_.insert_or_assign(std::move(signer_id), std::move(secret));
}

bool HmacSha256Verifier::verify(
    std::string_view signer_id, std::span<const std::uint8_t> message,
    std::span<const std::uint8_t> signature) const {
  const auto found = keys_.find(signer_id);
  if (found == keys_.end()) {
    return false;
  }
  const auto expected = hmacSha256(found->second, message);
  return constantTimeEqual(expected, signature);
}

Bytes updateSignatureTranscript(const UpdatePackage& package) {
  Bytes transcript;
  appendString(transcript, "netsec.secureboot.update.v1");
  appendU8(transcript, static_cast<std::uint8_t>(package.target));
  appendU64(transcript, package.version);
  transcript.insert(transcript.end(), package.payload_hash.begin(),
                    package.payload_hash.end());
  appendU8(transcript, static_cast<std::uint8_t>(package.mode));
  appendU8(transcript, package.factory_key_install ? 1U : 0U);
  appendString(transcript, package.signer_id);
  const auto entries = encodeEntries(package.entries);
  transcript.insert(transcript.end(), entries.begin(), entries.end());
  return transcript;
}

Bytes signUpdateTranscript(const UpdatePackage& package,
                           std::span<const std::uint8_t> secret) {
  const auto transcript = updateSignatureTranscript(package);
  const auto mac = hmacSha256(secret, transcript);
  return Bytes(mac.begin(), mac.end());
}

UpdatePackage sealUpdatePackage(UpdatePackage package,
                                std::span<const std::uint8_t> secret) {
  package.payload_hash =
      variablePayloadHash(package.target, package.version, package.entries);
  package.signature = signUpdateTranscript(package, secret);
  return package;
}

std::string_view updateStatusName(UpdateStatus status) {
  switch (status) {
    case UpdateStatus::ok:
      return "ok";
    case UpdateStatus::invalid_package:
      return "invalid_package";
    case UpdateStatus::hash_mismatch:
      return "hash_mismatch";
    case UpdateStatus::invalid_signature:
      return "invalid_signature";
    case UpdateStatus::unauthorized_signer:
      return "unauthorized_signer";
    case UpdateStatus::revoked_signer:
      return "revoked_signer";
    case UpdateStatus::recovery_not_allowed:
      return "recovery_not_allowed";
    case UpdateStatus::physical_presence_required:
      return "physical_presence_required";
    case UpdateStatus::factory_policy_violation:
      return "factory_policy_violation";
    case UpdateStatus::stale_version:
      return "stale_version";
    case UpdateStatus::empty_payload:
      return "empty_payload";
    case UpdateStatus::dbx_shrink:
      return "dbx_shrink";
    case UpdateStatus::corrupted_state:
      return "corrupted_state";
    case UpdateStatus::storage_write_failed:
      return "storage_write_failed";
    case UpdateStatus::readback_mismatch:
      return "readback_mismatch";
    case UpdateStatus::rollback_failed:
      return "rollback_failed";
  }
  return "unknown";
}

std::string_view bootStatusName(BootStatus status) {
  switch (status) {
    case BootStatus::allowed:
      return "allowed";
    case BootStatus::revoked:
      return "revoked";
    case BootStatus::untrusted_signer:
      return "untrusted_signer";
    case BootStatus::missing_trust_database:
      return "missing_trust_database";
    case BootStatus::missing_revocation_database:
      return "missing_revocation_database";
    case BootStatus::corrupted_trust_database:
      return "corrupted_trust_database";
    case BootStatus::corrupted_revocation_database:
      return "corrupted_revocation_database";
  }
  return "unknown";
}

SecureBootPolicy::SecureBootPolicy(IVariableStore& store,
                                   const ISignatureVerifier& verifier,
                                   Policy policy)
    : store_(store), verifier_(verifier), policy_(std::move(policy)) {}

UpdateResult SecureBootPolicy::applyUpdate(const UpdatePackage& package,
                                           const UpdateContext& context) {
  if (package.version == 0 || package.signer_id.empty() ||
      package.signature.empty()) {
    return failure(UpdateStatus::invalid_package,
                   "version, signer id, and signature are required");
  }
  if (!validVariableId(package.target) || !validUpdateMode(package.mode) ||
      !validUpdateMode(context.mode)) {
    return failure(UpdateStatus::invalid_package,
                   "target or update mode is invalid");
  }
  if (context.mode != package.mode) {
    return failure(UpdateStatus::invalid_package,
                   "runtime mode does not match the signed package mode");
  }
  // Clearing a boot variable is never a valid update, even when signed.
  if (package.entries.empty()) {
    return failure(UpdateStatus::empty_payload,
                   "clearing a boot variable is not permitted");
  }
  if (hasDuplicateIds(package.entries)) {
    return failure(UpdateStatus::invalid_package,
                   "duplicate entry ids are not permitted");
  }
  for (const auto& entry : package.entries) {
    if (entry.id.empty()) {
      return failure(UpdateStatus::invalid_package,
                     "entry ids must not be empty");
    }
  }

  const auto computedHash =
      variablePayloadHash(package.target, package.version, package.entries);
  if (!constantTimeEqual(computedHash, package.payload_hash)) {
    return failure(UpdateStatus::hash_mismatch,
                   "declared payload hash does not match the package contents");
  }

  const auto transcript = updateSignatureTranscript(package);
  if (!verifier_.verify(package.signer_id, transcript, package.signature)) {
    return failure(UpdateStatus::invalid_signature,
                   "package signature does not cover this target/version/hash");
  }

  if (package.factory_key_install) {
    if (package.mode != UpdateMode::recovery) {
      return failure(UpdateStatus::factory_policy_violation,
                     "factory key installation requires recovery mode");
    }
    if (!policy_.allow_factory_key_install) {
      return failure(UpdateStatus::factory_policy_violation,
                     "factory key installation is disabled by policy");
    }
    if (package.target != VariableId::pk && package.target != VariableId::kek) {
      return failure(UpdateStatus::factory_policy_violation,
                     "factory keys may only be installed into PK or KEK");
    }
    if (policy_.factory_key_ids.empty()) {
      return failure(UpdateStatus::factory_policy_violation,
                     "no factory keys are configured");
    }
    if (policy_.require_physical_presence &&
        !context.physical_presence_confirmed) {
      return failure(UpdateStatus::physical_presence_required,
                     "factory key installation requires physical presence");
    }
    if (!containsString(policy_.factory_signers, package.signer_id)) {
      return failure(UpdateStatus::unauthorized_signer,
                     "signer is not a configured factory authority");
    }
    for (const auto& factoryKey : policy_.factory_key_ids) {
      if (!containsEntryId(package.entries, factoryKey)) {
        return failure(UpdateStatus::factory_policy_violation,
                       "factory package is missing an authorized factory key");
      }
    }
  } else if (package.mode == UpdateMode::recovery) {
    if (!policy_.allow_recovery_updates) {
      return failure(UpdateStatus::recovery_not_allowed,
                     "recovery updates are disabled by policy");
    }
    if (policy_.require_physical_presence &&
        !context.physical_presence_confirmed) {
      return failure(UpdateStatus::physical_presence_required,
                     "recovery updates require physical presence");
    }
    if (!containsString(policy_.recovery_signers, package.signer_id)) {
      return failure(UpdateStatus::unauthorized_signer,
                     "signer is not a configured recovery authority");
    }
  } else if (!containsString(policy_.update_signers, package.signer_id)) {
    return failure(UpdateStatus::unauthorized_signer,
                   "signer is not a configured update authority");
  }

  const auto currentRecord = store_.read(package.target);
  if (currentRecord &&
      (currentRecord->id != package.target || !validState(*currentRecord))) {
    return failure(UpdateStatus::corrupted_state,
                   "existing variable state failed its integrity check");
  }

  const auto revocationRecord = store_.read(VariableId::dbx);
  if (revocationRecord &&
      (revocationRecord->id != VariableId::dbx ||
       !validState(*revocationRecord))) {
    return failure(UpdateStatus::corrupted_state,
                   "dbx failed its integrity check");
  }
  if (revocationRecord &&
      containsEntryId(revocationRecord->entries, package.signer_id)) {
    return failure(UpdateStatus::revoked_signer,
                   "the update signer is present in dbx");
  }

  if (currentRecord) {
    if (package.version <= currentRecord->version) {
      return failure(UpdateStatus::stale_version,
                     "update version must be strictly greater than the stored "
                     "version");
    }
    if (package.target == VariableId::dbx &&
        !containsAllEntries(package.entries, currentRecord->entries)) {
      return failure(UpdateStatus::dbx_shrink,
                     "dbx update removes an existing revocation entry");
    }
    if (package.target == VariableId::db && policy_.enforce_db_no_shrink &&
        !containsAllEntries(package.entries, currentRecord->entries)) {
      return failure(UpdateStatus::dbx_shrink,
                     "db update removes an existing trusted entry");
    }
  }

  const auto nextState =
      makeVariableState(package.target, package.version, package.entries);
  if (!store_.write(nextState)) {
    bool restored = false;
    if (currentRecord) {
      const auto observed = store_.read(package.target);
      if (!observed || !(*observed == *currentRecord)) {
        restored = store_.write(*currentRecord);
      } else {
        restored = true;
      }
      if (restored) {
        const auto restoredReadback = store_.read(package.target);
        restored = restoredReadback && *restoredReadback == *currentRecord;
      }
    } else {
      restored = !store_.read(package.target).has_value();
    }
    if (!restored) {
      return failure(UpdateStatus::rollback_failed,
                     "write failed after mutating storage and the previous "
                     "state could not be restored");
    }
    return failure(UpdateStatus::storage_write_failed,
                   "variable store rejected the write");
  }

  const auto readback = store_.read(package.target);
  if (!readback || !(*readback == nextState) || !validState(*readback)) {
    bool restored = false;
    if (currentRecord) {
      restored = store_.write(*currentRecord);
      if (restored) {
        const auto restoredReadback = store_.read(package.target);
        restored = restoredReadback && *restoredReadback == *currentRecord;
      }
    } else {
      restored = !store_.read(package.target).has_value();
    }
    if (!restored) {
      return failure(UpdateStatus::rollback_failed,
                     "read-back failed and the previous state could not be "
                     "restored");
    }
    return failure(UpdateStatus::readback_mismatch,
                   "written state did not match the read-back value");
  }

  return UpdateResult{UpdateStatus::ok, "update committed and verified"};
}

BootDecision SecureBootPolicy::evaluateBoot(const BootComponent& component,
                                            BootMode mode) const {
  const auto trust = store_.read(VariableId::db);
  if (!trust || trust->id != VariableId::db) {
    return BootDecision{BootStatus::missing_trust_database,
                        "db is absent; refusing to boot"};
  }
  if (!validState(*trust)) {
    return BootDecision{BootStatus::corrupted_trust_database,
                        "db integrity check failed; refusing to boot"};
  }

  const auto revocation = store_.read(VariableId::dbx);
  if (!revocation || revocation->id != VariableId::dbx) {
    return BootDecision{BootStatus::missing_revocation_database,
                        "dbx is absent; refusing to boot"};
  }
  if (!validState(*revocation)) {
    return BootDecision{BootStatus::corrupted_revocation_database,
                        "dbx integrity check failed; refusing to boot"};
  }

  if (!component.image_id.empty() &&
      containsEntryId(revocation->entries, component.image_id)) {
    return BootDecision{BootStatus::revoked,
                        "image digest is present in dbx"};
  }
  for (const auto& signer : component.signer_chain) {
    if (containsEntryId(revocation->entries, signer)) {
      return BootDecision{BootStatus::revoked,
                          "signer chain contains a dbx-revoked identity"};
    }
  }

  std::vector<std::string> trustAnchors;
  trustAnchors.reserve(trust->entries.size() +
                       policy_.recovery_boot_anchors.size() +
                       policy_.factory_key_ids.size());
  for (const auto& entry : trust->entries) {
    trustAnchors.push_back(entry.id);
  }
  if (mode == BootMode::recovery) {
    trustAnchors.insert(trustAnchors.end(),
                        policy_.recovery_boot_anchors.begin(),
                        policy_.recovery_boot_anchors.end());
    if (policy_.allow_factory_boot_anchors) {
      const auto pk = store_.read(VariableId::pk);
      const auto kek = store_.read(VariableId::kek);
      const bool pkValid =
          pk && pk->id == VariableId::pk && validState(*pk);
      const bool kekValid =
          kek && kek->id == VariableId::kek && validState(*kek);
      for (const auto& factoryKey : policy_.factory_key_ids) {
        const bool installedInPk =
            pkValid && containsEntryId(pk->entries, factoryKey);
        const bool installedInKek =
            kekValid && containsEntryId(kek->entries, factoryKey);
        if (installedInPk || installedInKek) {
          trustAnchors.push_back(factoryKey);
        }
      }
    }
  }

  if (!component.image_id.empty() &&
      containsString(trustAnchors, component.image_id)) {
    return BootDecision{BootStatus::allowed,
                        "image digest is trusted by db"};
  }
  for (const auto& signer : component.signer_chain) {
    if (containsString(trustAnchors, signer)) {
      return BootDecision{BootStatus::allowed,
                          "a signer chain element is trusted"};
    }
  }
  return BootDecision{BootStatus::untrusted_signer,
                      "no signer chain element is trusted"};
}

}  // namespace netsec
