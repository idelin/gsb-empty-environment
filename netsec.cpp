#include "netsec.hpp"

#include <algorithm>
#include <cstring>
#include <exception>
#include <map>
#include <stdexcept>
#include <utility>

namespace netsec {
namespace {

constexpr std::size_t kMaxEncodedString = 0xffffU;
constexpr std::size_t kProofEntryLimit = 4096;

struct Reader {
    std::span<const Byte> data;
    std::size_t position = 0;

    [[nodiscard]] std::size_t left() const {
        return data.size() - position;
    }

    bool read_u16(std::uint16_t& value) {
        if (left() < 2) {
            return false;
        }
        value = static_cast<std::uint16_t>(
            (static_cast<std::uint16_t>(data[position]) << 8) |
            static_cast<std::uint16_t>(data[position + 1]));
        position += 2;
        return true;
    }

    bool read_u32(std::uint32_t& value) {
        if (left() < 4) {
            return false;
        }
        value = (static_cast<std::uint32_t>(data[position]) << 24) |
                (static_cast<std::uint32_t>(data[position + 1]) << 16) |
                (static_cast<std::uint32_t>(data[position + 2]) << 8) |
                static_cast<std::uint32_t>(data[position + 3]);
        position += 4;
        return true;
    }

    bool read_u64(std::uint64_t& value) {
        if (left() < 8) {
            return false;
        }
        value = 0;
        for (std::size_t i = 0; i < 8; ++i) {
            value = (value << 8) | data[position + i];
        }
        position += 8;
        return true;
    }

    bool read_bytes(Byte* out, std::size_t size) {
        if (left() < size) {
            return false;
        }
        std::memcpy(out, data.data() + position, size);
        position += size;
        return true;
    }

    bool read_bytes(Bytes& out, std::size_t size) {
        if (left() < size) {
            return false;
        }
        out.assign(data.begin() + static_cast<std::ptrdiff_t>(position),
                   data.begin() +
                       static_cast<std::ptrdiff_t>(position + size));
        position += size;
        return true;
    }

    bool read_string(std::string& out) {
        std::uint16_t size = 0;
        if (!read_u16(size) || left() < size) {
            return false;
        }
        out.assign(reinterpret_cast<const char*>(data.data() + position), size);
        position += size;
        return true;
    }

    bool read_magic(std::string_view magic) {
        if (left() < magic.size()) {
            return false;
        }
        if (std::memcmp(data.data() + position, magic.data(), magic.size()) !=
            0) {
            return false;
        }
        position += magic.size();
        return true;
    }
};

void append_u16_be(Bytes& out, std::uint16_t value) {
    out.push_back(static_cast<Byte>(value >> 8));
    out.push_back(static_cast<Byte>(value));
}

void append_u32_be(Bytes& out, std::uint32_t value) {
    for (int shift = 24; shift >= 0; shift -= 8) {
        out.push_back(static_cast<Byte>((value >> shift) & 0xffU));
    }
}

void append_u64_be(Bytes& out, std::uint64_t value) {
    for (int shift = 56; shift >= 0; shift -= 8) {
        out.push_back(static_cast<Byte>((value >> shift) & 0xffU));
    }
}

void append_string(Bytes& out, std::string_view value) {
    if (value.size() > kMaxEncodedString) {
        throw std::length_error("stage identity is too long");
    }
    append_u16_be(out, static_cast<std::uint16_t>(value.size()));
    out.insert(out.end(), value.begin(), value.end());
}

void append_bytes(Bytes& out, std::span<const Byte> value) {
    out.insert(out.end(), value.begin(), value.end());
}

void append_tag(Bytes& out, std::string_view tag) {
    append_u16_be(out, static_cast<std::uint16_t>(tag.size()));
    out.insert(out.end(), tag.begin(), tag.end());
}

Digest digest_of(std::string_view domain, const Bytes& body) {
    Bytes material;
    material.reserve(domain.size() + body.size() + 2);
    append_tag(material, domain);
    material.insert(material.end(), body.begin(), body.end());
    return crypto::sha256(material);
}

Digest anchor_fingerprint(const TrustAnchor& anchor) {
    Bytes body;
    append_u32_be(body, anchor.chain_id);
    append_string(body, anchor.root_stage_id);
    append_bytes(body, anchor.root_key);
    return digest_of("netsec/trust-anchor/v1", body);
}

Digest chain_genesis(const TrustAnchor& anchor) {
    Bytes body;
    append_u32_be(body, anchor.chain_id);
    append_bytes(body, anchor.fingerprint());
    return digest_of("netsec/genesis/v1", body);
}

Digest stage_measurement(const Digest& previous, std::uint32_t index,
                         std::uint64_t version, std::span<const Byte> manifest,
                         const Signature& signature) {
    Bytes body;
    body.reserve(32 + 4 + 8 + manifest.size() + signature.size());
    append_bytes(body, previous);
    append_u32_be(body, index);
    append_u64_be(body, version);
    append_bytes(body, manifest);
    append_bytes(body, signature);
    return digest_of("netsec/stage-measurement/v1", body);
}

bool has_duplicate_stage(const std::vector<std::string>& stage_ids) {
    std::vector<std::string_view> seen;
    seen.reserve(stage_ids.size());
    for (const auto& id : stage_ids) {
        for (const auto view : seen) {
            if (view == id) {
                return true;
            }
        }
        seen.push_back(id);
    }
    return false;
}

VerificationReport fail(VerifyError error, std::size_t stage,
                        std::string detail) {
    VerificationReport report;
    report.error = error;
    report.failed_stage = stage;
    report.detail = std::move(detail);
    return report;
}

bool key_is_zero(const PublicKey& key) {
    for (const Byte byte : key) {
        if (byte != 0) {
            return false;
        }
    }
    return true;
}

bool proof_fail(std::string* detail, std::string message) {
    if (detail != nullptr) {
        *detail = std::move(message);
    }
    return false;
}

void set_detail(std::string* detail, std::string message) {
    if (detail != nullptr) {
        *detail = std::move(message);
    }
}

}  // namespace

Bytes StageManifest::canonical_bytes() const {
    Bytes out;
    out.reserve(4 + 2 + 4 + 4 + 8 + 8 + 8 + 2 + stage_id.size() +
                2 + next_stage_id.size() + 32 + 32);
    out.insert(out.end(), {'N', 'B', 'S', 'G'});
    append_u16_be(out, 1);
    append_u32_be(out, chain_id);
    append_u32_be(out, stage_index);
    append_u64_be(out, version);
    append_u64_be(out, min_next_version);
    append_u64_be(out, payload_size);
    append_string(out, stage_id);
    append_string(out, next_stage_id);
    append_bytes(out, payload_digest);
    append_bytes(out, next_authority);
    return out;
}

Digest StageManifest::digest() const {
    return crypto::sha256(canonical_bytes());
}

std::optional<StageManifest> StageManifest::parse(std::span<const Byte> bytes,
                                                  std::string* detail) {
    Reader reader{bytes};
    std::uint16_t format = 0;
    if (!reader.read_magic("NBSG") || !reader.read_u16(format)) {
        set_detail(detail, "manifest magic or format is missing");
        return std::nullopt;
    }
    if (format != 1) {
        set_detail(detail, "unsupported manifest format");
        return std::nullopt;
    }

    StageManifest manifest;
    if (!reader.read_u32(manifest.chain_id) ||
        !reader.read_u32(manifest.stage_index) ||
        !reader.read_u64(manifest.version) ||
        !reader.read_u64(manifest.min_next_version) ||
        !reader.read_u64(manifest.payload_size) ||
        !reader.read_string(manifest.stage_id) ||
        !reader.read_string(manifest.next_stage_id) ||
        !reader.read_bytes(manifest.payload_digest.data(), kDigestSize) ||
        !reader.read_bytes(manifest.next_authority.data(), kPublicKeySize)) {
        set_detail(detail, "manifest is truncated");
        return std::nullopt;
    }
    if (reader.left() != 0) {
        set_detail(detail, "manifest has trailing bytes");
        return std::nullopt;
    }
    return manifest;
}

BootStage BootStage::create(StageManifest manifest, Bytes payload,
                            const SigningKey& predecessor) {
    manifest.payload_size = payload.size();
    manifest.payload_digest = crypto::sha256(payload);
    BootStage stage;
    stage.manifest = std::move(manifest);
    stage.payload = std::move(payload);
    stage.signature = predecessor.sign(stage.manifest.canonical_bytes());
    return stage;
}

void BootStage::resign(const SigningKey& predecessor) {
    manifest.payload_size = payload.size();
    manifest.payload_digest = crypto::sha256(payload);
    signature = predecessor.sign(manifest.canonical_bytes());
}

Bytes BootStage::signed_bytes() const {
    return manifest.canonical_bytes();
}

TrustAnchor TrustAnchor::pinned(std::uint32_t chain_id,
                                std::string root_stage_id,
                                PublicKey root_key) {
    TrustAnchor anchor;
    anchor.chain_id = chain_id;
    anchor.root_stage_id = std::move(root_stage_id);
    anchor.root_key = root_key;
    return anchor;
}

bool TrustAnchor::valid() const noexcept {
    if (root_stage_id.empty() || key_is_zero(root_key)) {
        return false;
    }
    return crypto::ed25519_is_valid_public_key(root_key);
}

Digest TrustAnchor::fingerprint() const {
    return anchor_fingerprint(*this);
}

std::uint64_t InMemoryRollbackStore::floor(std::string_view stage_id) const {
    const auto it = floors_.find(stage_id);
    return it == floors_.end() ? 0U : it->second;
}

void InMemoryRollbackStore::commit(std::string_view stage_id,
                                   std::uint64_t version) {
    auto [it, inserted] = floors_.try_emplace(std::string(stage_id), version);
    if (!inserted && it->second < version) {
        it->second = version;
    }
}

void InMemoryRollbackStore::seed(std::string_view stage_id,
                                 std::uint64_t version) {
    commit(stage_id, version);
}

std::map<std::string, std::uint64_t, std::less<>>
InMemoryRollbackStore::snapshot() const {
    return floors_;
}

std::string_view to_string(VerifyError error) noexcept {
    switch (error) {
        case VerifyError::none:
            return "none";
        case VerifyError::invalid_argument:
            return "invalid_argument";
        case VerifyError::untrusted_anchor:
            return "untrusted_anchor";
        case VerifyError::chain_id_mismatch:
            return "chain_id_mismatch";
        case VerifyError::empty_chain:
            return "empty_chain";
        case VerifyError::chain_too_long:
            return "chain_too_long";
        case VerifyError::duplicate_stage:
            return "duplicate_stage";
        case VerifyError::stage_index_mismatch:
            return "stage_index_mismatch";
        case VerifyError::stage_id_mismatch:
            return "stage_id_mismatch";
        case VerifyError::missing_stage:
            return "missing_stage";
        case VerifyError::unexpected_stage:
            return "unexpected_stage";
        case VerifyError::invalid_signer_key:
            return "invalid_signer_key";
        case VerifyError::signature_invalid:
            return "signature_invalid";
        case VerifyError::payload_mismatch:
            return "payload_mismatch";
        case VerifyError::manifest_mismatch:
            return "manifest_mismatch";
        case VerifyError::version_rollback:
            return "version_rollback";
        case VerifyError::version_policy_violation:
            return "version_policy_violation";
        case VerifyError::terminal_policy_violation:
            return "terminal_policy_violation";
        case VerifyError::proof_invalid:
            return "proof_invalid";
    }
    return "unknown";
}

BootChainVerifier::BootChainVerifier(TrustAnchor anchor, ChainPolicy policy,
                                     RollbackStore& rollback_store)
    : anchor_(std::move(anchor)),
      policy_(std::move(policy)),
      rollback_(rollback_store) {}

VerificationReport BootChainVerifier::verify(
    std::span<const BootStage> stages) const {
    try {
        if (!anchor_.valid()) {
            return fail(VerifyError::untrusted_anchor, kNoStage,
                        "trust anchor is not a pinned Ed25519 key");
        }
        if (policy_.chain_id != anchor_.chain_id) {
            return fail(VerifyError::chain_id_mismatch, kNoStage,
                        "policy and trust anchor name different chains");
        }
        if (policy_.max_stages == 0) {
            return fail(VerifyError::invalid_argument, kNoStage,
                        "maximum stage count must be non-zero");
        }
        if (stages.empty()) {
            return fail(VerifyError::empty_chain, 0,
                        "no boot stages were supplied");
        }
        if (stages.size() > policy_.max_stages) {
            return fail(VerifyError::chain_too_long, policy_.max_stages,
                        "chain exceeds the configured stage limit");
        }

        std::vector<std::string> supplied_ids;
        supplied_ids.reserve(stages.size());
        for (const BootStage& stage : stages) {
            if (stage.manifest.stage_id.empty()) {
                return fail(VerifyError::invalid_argument, supplied_ids.size(),
                            "stage identity must not be empty");
            }
            supplied_ids.push_back(stage.manifest.stage_id);
        }
        if (has_duplicate_stage(supplied_ids)) {
            return fail(VerifyError::duplicate_stage, kNoStage,
                        "a stage identity occurs more than once");
        }

        if (policy_.require_stage_ids && !policy_.stage_ids.empty()) {
            if (policy_.stage_ids.size() > stages.size()) {
                return fail(VerifyError::missing_stage, stages.size(),
                            "the boot chain stopped before a required stage");
            }
            if (policy_.stage_ids.size() < stages.size()) {
                return fail(VerifyError::unexpected_stage,
                            policy_.stage_ids.size(),
                            "the chain contains a stage outside the policy");
            }
        }

        const Digest genesis = chain_genesis(anchor_);
        Digest previous_measurement = genesis;
        const BootStage* previous_stage = nullptr;

        AttestationProof proof;
        proof.chain_id = anchor_.chain_id;
        proof.root_stage_id = anchor_.root_stage_id;
        proof.root_key = anchor_.root_key;
        proof.anchor_fingerprint = anchor_.fingerprint();
        proof.genesis = genesis;
        proof.entries.reserve(stages.size());

        std::map<std::string, std::uint64_t, std::less<>> pending_floors;

        for (std::size_t i = 0; i < stages.size(); ++i) {
            const BootStage& stage = stages[i];
            const StageManifest& manifest = stage.manifest;

            if (manifest.chain_id != anchor_.chain_id) {
                return fail(VerifyError::chain_id_mismatch, i,
                            "stage belongs to a different chain");
            }
            if (manifest.stage_index != i) {
                return fail(VerifyError::stage_index_mismatch, i,
                            "stage index is not contiguous");
            }
            if (i == 0 && manifest.stage_id != anchor_.root_stage_id) {
                return fail(VerifyError::stage_id_mismatch, i,
                            "first stage does not match the pinned root stage");
            }
            if (policy_.require_stage_ids && !policy_.stage_ids.empty() &&
                manifest.stage_id != policy_.stage_ids[i]) {
                return fail(VerifyError::stage_id_mismatch, i,
                            "stage identity does not match the pinned policy");
            }
            if (previous_stage != nullptr) {
                if (previous_stage->manifest.next_stage_id.empty()) {
                    return fail(VerifyError::unexpected_stage, i,
                                "a terminal stage is followed by another stage");
                }
                if (previous_stage->manifest.next_stage_id !=
                    manifest.stage_id) {
                    return fail(VerifyError::stage_id_mismatch, i,
                                "stage is not the identity announced by its "
                                "predecessor");
                }
            }

            const PublicKey& signer =
                i == 0 ? anchor_.root_key
                       : previous_stage->manifest.next_authority;
            if (key_is_zero(signer) ||
                !crypto::ed25519_is_valid_public_key(signer)) {
                return fail(VerifyError::invalid_signer_key, i,
                            "the predecessor did not delegate a valid signing "
                            "key");
            }

            const std::uint64_t floor = rollback_.floor(manifest.stage_id);
            if (manifest.version < floor) {
                return fail(VerifyError::version_rollback, i,
                            "stage version is below the anti-rollback floor");
            }
            if (previous_stage != nullptr &&
                manifest.version <
                    previous_stage->manifest.min_next_version) {
                return fail(VerifyError::version_policy_violation, i,
                            "stage version is below the minimum signed by its "
                            "predecessor");
            }

            const Bytes manifest_bytes = manifest.canonical_bytes();
            if (!crypto::ed25519_verify(manifest_bytes, stage.signature,
                                        signer)) {
                return fail(VerifyError::signature_invalid, i,
                            "stage signature is not valid under the delegated "
                            "authority");
            }
            if (manifest.payload_size != stage.payload.size()) {
                return fail(VerifyError::payload_mismatch, i,
                            "payload length does not match the signed manifest");
            }
            if (crypto::sha256(stage.payload) != manifest.payload_digest) {
                return fail(VerifyError::payload_mismatch, i,
                            "payload digest does not match the signed manifest");
            }

            const Digest measurement =
                stage_measurement(previous_measurement, manifest.stage_index,
                                  manifest.version, manifest_bytes,
                                  stage.signature);

            ProofEntry entry;
            entry.index = manifest.stage_index;
            entry.manifest = manifest;
            entry.manifest_bytes = manifest_bytes;
            entry.signer = signer;
            entry.signature = stage.signature;
            entry.manifest_digest = crypto::sha256(manifest_bytes);
            entry.payload_digest = manifest.payload_digest;
            entry.previous_measurement = previous_measurement;
            entry.measurement = measurement;
            proof.entries.push_back(std::move(entry));

            pending_floors[manifest.stage_id] =
                std::max(pending_floors[manifest.stage_id], manifest.version);
            previous_measurement = measurement;
            previous_stage = &stage;
        }

        if (previous_stage != nullptr &&
            !previous_stage->manifest.next_stage_id.empty()) {
            return fail(VerifyError::missing_stage, stages.size(),
                        "the last stage announces a successor that was not "
                        "supplied");
        }
        if (previous_stage != nullptr && !previous_stage->manifest.terminal()) {
            return fail(VerifyError::terminal_policy_violation,
                        stages.size() - 1,
                        "last stage has inconsistent terminal delegation data");
        }

        // Commit anti-rollback state only after every stage and the complete
        // measurement transcript have been validated.
        for (const auto& [stage_id, version] : pending_floors) {
            rollback_.commit(stage_id, version);
        }

        VerificationReport report;
        report.error = VerifyError::none;
        report.proof = std::move(proof);
        return report;
    } catch (const std::exception& ex) {
        return fail(VerifyError::invalid_argument, kNoStage, ex.what());
    }
}

Digest AttestationProof::final_measurement() const {
    if (entries.empty()) {
        return Digest{};
    }
    return entries.back().measurement;
}

Bytes AttestationProof::canonical_bytes() const {
    Bytes out;
    out.insert(out.end(), {'N', 'B', 'S', 'P'});
    append_u16_be(out, 1);
    append_u32_be(out, chain_id);
    append_string(out, root_stage_id);
    append_bytes(out, root_key);
    append_bytes(out, anchor_fingerprint);
    append_bytes(out, genesis);
    append_u32_be(out, static_cast<std::uint32_t>(entries.size()));
    for (const ProofEntry& entry : entries) {
        append_u32_be(out, entry.index);
        append_bytes(out, entry.signer);
        append_bytes(out, entry.signature);
        append_bytes(out, entry.manifest_digest);
        append_bytes(out, entry.payload_digest);
        append_bytes(out, entry.previous_measurement);
        append_bytes(out, entry.measurement);
        append_u32_be(out,
                      static_cast<std::uint32_t>(entry.manifest_bytes.size()));
        append_bytes(out, entry.manifest_bytes);
    }
    return out;
}

std::optional<AttestationProof> AttestationProof::parse(
    std::span<const Byte> bytes, std::string* detail) {
    Reader reader{bytes};
    std::uint16_t format = 0;
    if (!reader.read_magic("NBSP") || !reader.read_u16(format)) {
        set_detail(detail, "proof magic or format is missing");
        return std::nullopt;
    }
    if (format != 1) {
        set_detail(detail, "unsupported proof format");
        return std::nullopt;
    }

    AttestationProof proof;
    if (!reader.read_u32(proof.chain_id) ||
        !reader.read_string(proof.root_stage_id) ||
        !reader.read_bytes(proof.root_key.data(), kPublicKeySize) ||
        !reader.read_bytes(proof.anchor_fingerprint.data(), kDigestSize) ||
        !reader.read_bytes(proof.genesis.data(), kDigestSize)) {
        set_detail(detail, "proof header is truncated");
        return std::nullopt;
    }

    std::uint32_t count = 0;
    if (!reader.read_u32(count)) {
        set_detail(detail, "proof entry count is missing");
        return std::nullopt;
    }
    if (count > kProofEntryLimit) {
        set_detail(detail, "proof exceeds the entry limit");
        return std::nullopt;
    }
    constexpr std::size_t kMinimumEntrySize =
        4 + kPublicKeySize + kSignatureSize + kDigestSize + kDigestSize +
        kDigestSize + kDigestSize + 4;
    if (count > reader.left() / kMinimumEntrySize) {
        set_detail(detail, "proof entry count exceeds the payload size");
        return std::nullopt;
    }

    proof.entries.reserve(count);
    for (std::uint32_t i = 0; i < count; ++i) {
        ProofEntry entry;
        std::uint32_t manifest_size = 0;
        if (!reader.read_u32(entry.index) ||
            !reader.read_bytes(entry.signer.data(), kPublicKeySize) ||
            !reader.read_bytes(entry.signature.data(), kSignatureSize) ||
            !reader.read_bytes(entry.manifest_digest.data(), kDigestSize) ||
            !reader.read_bytes(entry.payload_digest.data(), kDigestSize) ||
            !reader.read_bytes(entry.previous_measurement.data(), kDigestSize) ||
            !reader.read_bytes(entry.measurement.data(), kDigestSize) ||
            !reader.read_u32(manifest_size)) {
            set_detail(detail, "proof entry is truncated");
            return std::nullopt;
        }
        if (!reader.read_bytes(entry.manifest_bytes, manifest_size)) {
            set_detail(detail, "proof manifest bytes are truncated");
            return std::nullopt;
        }
        auto manifest =
            StageManifest::parse(entry.manifest_bytes, detail);
        if (!manifest.has_value()) {
            return std::nullopt;
        }
        entry.manifest = *manifest;
        proof.entries.push_back(std::move(entry));
    }

    if (reader.left() != 0) {
        set_detail(detail, "proof has trailing bytes");
        return std::nullopt;
    }
    if (detail != nullptr) {
        detail->clear();
    }
    return proof;
}

bool verify_proof(const AttestationProof& proof, const TrustAnchor& anchor,
                  std::string* detail) {
    try {
        if (!anchor.valid()) {
            return proof_fail(detail, "trust anchor is not a pinned key");
        }
        if (proof.entries.empty()) {
            return proof_fail(detail, "proof contains no stages");
        }
        if (proof.entries.size() > kProofEntryLimit) {
            return proof_fail(detail, "proof exceeds the entry limit");
        }
        if (proof.chain_id != anchor.chain_id) {
            return proof_fail(detail, "proof chain id does not match anchor");
        }
        if (proof.root_stage_id != anchor.root_stage_id) {
            return proof_fail(detail, "proof root stage does not match anchor");
        }
        if (proof.root_key != anchor.root_key) {
            return proof_fail(detail, "proof root key does not match anchor");
        }

        const Digest expected_anchor_fingerprint = anchor.fingerprint();
        if (proof.anchor_fingerprint != expected_anchor_fingerprint) {
            return proof_fail(detail, "proof anchor fingerprint is invalid");
        }
        const Digest expected_genesis = chain_genesis(anchor);
        if (proof.genesis != expected_genesis) {
            return proof_fail(detail, "proof genesis value is invalid");
        }

        Digest previous_measurement = expected_genesis;
        std::vector<std::string> stage_ids;
        stage_ids.reserve(proof.entries.size());
        const StageManifest* previous_manifest = nullptr;

        for (std::size_t i = 0; i < proof.entries.size(); ++i) {
            const ProofEntry& entry = proof.entries[i];
            const StageManifest& manifest = entry.manifest;

            if (entry.index != i || manifest.stage_index != entry.index) {
                return proof_fail(detail, "proof stage indices are not contiguous");
            }
            if (manifest.chain_id != anchor.chain_id) {
                return proof_fail(detail, "proof entry chain id is invalid");
            }
            if (manifest.stage_id.empty()) {
                return proof_fail(detail, "proof entry has an empty stage id");
            }
            stage_ids.push_back(manifest.stage_id);
            if (i == 0 && manifest.stage_id != anchor.root_stage_id) {
                return proof_fail(detail, "proof does not start at root stage");
            }

            const Bytes canonical = manifest.canonical_bytes();
            if (canonical != entry.manifest_bytes) {
                return proof_fail(detail, "proof manifest encoding was altered");
            }
            if (crypto::sha256(canonical) != entry.manifest_digest) {
                return proof_fail(detail, "proof manifest digest is invalid");
            }
            if (entry.payload_digest != manifest.payload_digest) {
                return proof_fail(detail, "proof payload digest is inconsistent");
            }

            const PublicKey& expected_signer =
                i == 0 ? anchor.root_key : previous_manifest->next_authority;
            if (entry.signer != expected_signer) {
                return proof_fail(detail,
                                  "proof signer is not the delegated authority");
            }
            if (key_is_zero(entry.signer) ||
                !crypto::ed25519_is_valid_public_key(entry.signer)) {
                return proof_fail(detail, "proof contains an invalid signer key");
            }
            if (!crypto::ed25519_verify(canonical, entry.signature,
                                        entry.signer)) {
                return proof_fail(detail, "proof stage signature is invalid");
            }

            if (previous_manifest != nullptr) {
                if (previous_manifest->next_stage_id.empty()) {
                    return proof_fail(detail,
                                      "proof continues after a terminal stage");
                }
                if (previous_manifest->next_stage_id != manifest.stage_id) {
                    return proof_fail(
                        detail,
                        "proof stage is not the expected successor identity");
                }
                if (manifest.version < previous_manifest->min_next_version) {
                    return proof_fail(
                        detail,
                        "proof stage violates the predecessor version policy");
                }
            }

            if (entry.previous_measurement != previous_measurement) {
                return proof_fail(detail, "proof measurement chain is broken");
            }
            const Digest expected_measurement = stage_measurement(
                previous_measurement, manifest.stage_index, manifest.version,
                canonical, entry.signature);
            if (entry.measurement != expected_measurement) {
                return proof_fail(detail, "proof measurement is invalid");
            }

            previous_measurement = expected_measurement;
            previous_manifest = &manifest;
        }

        if (has_duplicate_stage(stage_ids)) {
            return proof_fail(detail, "proof contains duplicate stage identities");
        }
        if (previous_manifest == nullptr ||
            !previous_manifest->next_stage_id.empty()) {
            return proof_fail(detail, "proof is truncated before a terminal stage");
        }
        if (!previous_manifest->terminal()) {
            return proof_fail(detail,
                              "proof terminal stage has invalid delegation data");
        }
        if (proof.final_measurement() != previous_measurement) {
            return proof_fail(detail, "proof final measurement is inconsistent");
        }
        if (detail != nullptr) {
            detail->clear();
        }
        return true;
    } catch (const std::exception& ex) {
        return proof_fail(detail, ex.what());
    }
}

}  // namespace netsec
