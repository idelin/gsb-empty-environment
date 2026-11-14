#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <map>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

inline constexpr std::size_t kDigestSize = 32;
inline constexpr std::size_t kSignatureSize = 64;
inline constexpr std::size_t kPublicKeySize = 32;
inline constexpr std::size_t kSeedSize = 32;
inline constexpr std::size_t kNoStage = static_cast<std::size_t>(-1);

using Byte = std::uint8_t;
using Bytes = std::vector<Byte>;
using Digest = std::array<Byte, kDigestSize>;
using Sha512Digest = std::array<Byte, 64>;
using Signature = std::array<Byte, kSignatureSize>;
using PublicKey = std::array<Byte, kPublicKeySize>;
using Seed = std::array<Byte, kSeedSize>;

std::string to_hex(std::span<const Byte> bytes);
Bytes bytes_from_hex(std::string_view hex);

namespace crypto {

Digest sha256(std::span<const Byte> data);
Sha512Digest sha512(std::span<const Byte> data);

PublicKey ed25519_public_key(const Seed& seed);
bool ed25519_is_valid_public_key(const PublicKey& public_key);
Signature ed25519_sign(std::span<const Byte> message, const Seed& seed);
bool ed25519_verify(std::span<const Byte> message, const Signature& signature,
                    const PublicKey& public_key);

}  // namespace crypto

class SigningKey {
public:
    explicit SigningKey(Seed seed) noexcept;
    static SigningKey from_hex(std::string_view hex);

    [[nodiscard]] const Seed& seed() const noexcept { return seed_; }
    [[nodiscard]] PublicKey public_key() const;
    [[nodiscard]] Signature sign(std::span<const Byte> message) const;

private:
    Seed seed_{};
};

struct TrustAnchor {
    std::uint32_t chain_id = 0;
    std::string root_stage_id;
    PublicKey root_key{};

    [[nodiscard]] static TrustAnchor pinned(std::uint32_t chain_id,
                                            std::string root_stage_id,
                                            PublicKey root_key);
    [[nodiscard]] bool valid() const noexcept;
    [[nodiscard]] Digest fingerprint() const;
};

// A stage signs the manifest of the *next* stage.  The manifest therefore
// commits to the executable payload, the next signer, its minimum version and
// the expected identity of the next stage.  Every field is covered by the
// predecessor's signature.
struct StageManifest {
    std::uint32_t chain_id = 0;
    std::uint32_t stage_index = 0;
    std::string stage_id;
    std::uint64_t version = 0;
    std::string next_stage_id;
    PublicKey next_authority{};
    std::uint64_t min_next_version = 0;
    std::uint64_t payload_size = 0;
    Digest payload_digest{};

    [[nodiscard]] bool terminal() const noexcept {
        return next_stage_id.empty() &&
               next_authority == PublicKey{} &&
               min_next_version == 0;
    }
    [[nodiscard]] Bytes canonical_bytes() const;
    [[nodiscard]] Digest digest() const;
    [[nodiscard]] static std::optional<StageManifest> parse(
        std::span<const Byte> bytes, std::string* detail = nullptr);
};

struct BootStage {
    StageManifest manifest;
    Bytes payload;
    Signature signature{};

    // Computes payload_size/payload_digest and signs the resulting manifest
    // with the predecessor's key.
    [[nodiscard]] static BootStage create(StageManifest manifest,
                                          Bytes payload,
                                          const SigningKey& predecessor);
    void resign(const SigningKey& predecessor);
    [[nodiscard]] Bytes signed_bytes() const;
};

struct ChainPolicy {
    std::uint32_t chain_id = 0;
    // When non-empty, the exact ordered stage identities are pinned.
    std::vector<std::string> stage_ids;
    std::size_t max_stages = 64;
    bool require_stage_ids = true;
};

class RollbackStore {
public:
    virtual ~RollbackStore() = default;
    [[nodiscard]] virtual std::uint64_t floor(std::string_view stage_id) const = 0;
    virtual void commit(std::string_view stage_id, std::uint64_t version) = 0;
};

class InMemoryRollbackStore final : public RollbackStore {
public:
    [[nodiscard]] std::uint64_t floor(std::string_view stage_id) const override;
    void commit(std::string_view stage_id, std::uint64_t version) override;

    void seed(std::string_view stage_id, std::uint64_t version);
    [[nodiscard]] std::map<std::string, std::uint64_t, std::less<>> snapshot() const;

private:
    std::map<std::string, std::uint64_t, std::less<>> floors_;
};

enum class VerifyError {
    none = 0,
    invalid_argument,
    untrusted_anchor,
    chain_id_mismatch,
    empty_chain,
    chain_too_long,
    duplicate_stage,
    stage_index_mismatch,
    stage_id_mismatch,
    missing_stage,
    unexpected_stage,
    invalid_signer_key,
    signature_invalid,
    payload_mismatch,
    manifest_mismatch,
    version_rollback,
    version_policy_violation,
    terminal_policy_violation,
    proof_invalid,
};

[[nodiscard]] std::string_view to_string(VerifyError error) noexcept;

struct ProofEntry {
    std::uint32_t index = 0;
    StageManifest manifest;
    Bytes manifest_bytes;
    PublicKey signer{};
    Signature signature{};
    Digest manifest_digest{};
    Digest payload_digest{};
    Digest previous_measurement{};
    Digest measurement{};
};

// Self-contained, canonical transcript of a verified boot.  It can be logged,
// archived and re-verified later against the same TrustAnchor without access to
// the original BootStage objects.
struct AttestationProof {
    std::uint32_t chain_id = 0;
    std::string root_stage_id;
    PublicKey root_key{};
    Digest anchor_fingerprint{};
    Digest genesis{};
    std::vector<ProofEntry> entries;

    [[nodiscard]] Digest final_measurement() const;
    [[nodiscard]] Bytes canonical_bytes() const;
    [[nodiscard]] static std::optional<AttestationProof> parse(
        std::span<const Byte> bytes, std::string* detail = nullptr);
};

bool verify_proof(const AttestationProof& proof, const TrustAnchor& anchor,
                  std::string* detail = nullptr);

struct VerificationReport {
    VerifyError error = VerifyError::none;
    std::size_t failed_stage = kNoStage;
    std::string detail;
    AttestationProof proof;

    [[nodiscard]] bool ok() const noexcept { return error == VerifyError::none; }
    [[nodiscard]] explicit operator bool() const noexcept { return ok(); }
};

class BootChainVerifier {
public:
    BootChainVerifier(TrustAnchor anchor, ChainPolicy policy,
                      RollbackStore& rollback_store);

    // Verifies the complete chain before committing any anti-rollback state.
    // A failure leaves the RollbackStore untouched.
    [[nodiscard]] VerificationReport verify(std::span<const BootStage> stages) const;

    [[nodiscard]] const TrustAnchor& anchor() const noexcept { return anchor_; }
    [[nodiscard]] const ChainPolicy& policy() const noexcept { return policy_; }

private:
    TrustAnchor anchor_;
    ChainPolicy policy_;
    RollbackStore& rollback_;
};

}  // namespace netsec
