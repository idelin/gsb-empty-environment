#include "netsec.hpp"

#include <cstdlib>
#include <iostream>
#include <string>
#include <string_view>
#include <vector>

namespace {

int failures = 0;

#define CHECK(condition)                                                     \
    do {                                                                     \
        if (!(condition)) {                                                  \
            ++failures;                                                      \
            std::cerr << __FILE__ << ":" << __LINE__ << ": check failed: "  \
                      << #condition << '\n';                                 \
        }                                                                    \
    } while (false)

netsec::Seed seed_of(netsec::Byte value) {
    netsec::Seed seed{};
    seed.fill(value);
    return seed;
}

netsec::Bytes text(std::string_view value) {
    return netsec::Bytes(value.begin(), value.end());
}

netsec::Digest digest_from_hex(std::string_view hex) {
    const netsec::Bytes bytes = netsec::bytes_from_hex(hex);
    if (bytes.size() != netsec::kDigestSize) {
        std::abort();
    }
    netsec::Digest digest{};
    std::copy(bytes.begin(), bytes.end(), digest.begin());
    return digest;
}

netsec::Sha512Digest sha512_from_hex(std::string_view hex) {
    const netsec::Bytes bytes = netsec::bytes_from_hex(hex);
    if (bytes.size() != 64) {
        std::abort();
    }
    netsec::Sha512Digest digest{};
    std::copy(bytes.begin(), bytes.end(), digest.begin());
    return digest;
}

netsec::Signature signature_from_hex(std::string_view hex) {
    const netsec::Bytes bytes = netsec::bytes_from_hex(hex);
    if (bytes.size() != netsec::kSignatureSize) {
        std::abort();
    }
    netsec::Signature signature{};
    std::copy(bytes.begin(), bytes.end(), signature.begin());
    return signature;
}

struct Fixture {
    netsec::SigningKey root{seed_of(0x11)};
    netsec::SigningKey bl2{seed_of(0x22)};
    netsec::SigningKey bl31{seed_of(0x33)};
    netsec::SigningKey app{seed_of(0x44)};
    netsec::SigningKey attacker{seed_of(0x55)};

    netsec::TrustAnchor anchor{
        netsec::TrustAnchor::pinned(7, "BL1", root.public_key())};
    netsec::ChainPolicy policy{
        7, {"BL1", "BL2", "BL31", "APP"}, 16, true};
    std::vector<netsec::BootStage> stages;

    Fixture() {
        netsec::StageManifest bl1;
        bl1.chain_id = 7;
        bl1.stage_index = 0;
        bl1.stage_id = "BL1";
        bl1.version = 1;
        bl1.next_stage_id = "BL2";
        bl1.next_authority = bl2.public_key();
        bl1.min_next_version = 2;
        stages.push_back(netsec::BootStage::create(bl1, text("BL1 image"),
                                                   root));

        netsec::StageManifest bl2_manifest;
        bl2_manifest.chain_id = 7;
        bl2_manifest.stage_index = 1;
        bl2_manifest.stage_id = "BL2";
        bl2_manifest.version = 2;
        bl2_manifest.next_stage_id = "BL31";
        bl2_manifest.next_authority = bl31.public_key();
        bl2_manifest.min_next_version = 3;
        stages.push_back(netsec::BootStage::create(
            bl2_manifest, text("BL2 image"), bl2));

        netsec::StageManifest bl31_manifest;
        bl31_manifest.chain_id = 7;
        bl31_manifest.stage_index = 2;
        bl31_manifest.stage_id = "BL31";
        bl31_manifest.version = 3;
        bl31_manifest.next_stage_id = "APP";
        bl31_manifest.next_authority = app.public_key();
        bl31_manifest.min_next_version = 4;
        stages.push_back(netsec::BootStage::create(
            bl31_manifest, text("BL31 image"), bl31));

        netsec::StageManifest app_manifest;
        app_manifest.chain_id = 7;
        app_manifest.stage_index = 3;
        app_manifest.stage_id = "APP";
        app_manifest.version = 4;
        stages.push_back(netsec::BootStage::create(
            app_manifest, text("application image"), app));
    }
};

void test_crypto_vectors() {
    constexpr std::string_view empty_sha256 =
        "e3b0c44298fc1c149afbf4c8996fb924"
        "27ae41e4649b934ca495991b7852b855";
    constexpr std::string_view empty_sha512 =
        "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce"
        "47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e";
    CHECK(netsec::crypto::sha256(std::span<const netsec::Byte>{}) ==
          digest_from_hex(empty_sha256));
    CHECK(netsec::crypto::sha512(std::span<const netsec::Byte>{}) ==
          sha512_from_hex(empty_sha512));

    // RFC 8032, Ed25519 test vector 1 (empty message).
    const netsec::SigningKey signing_key = netsec::SigningKey::from_hex(
        "9d61b19deffd5a60ba844af492ec2cc4"
        "4449c5697b326919703bac031cae7f60");
    const netsec::PublicKey expected_public = [&] {
        const netsec::Bytes bytes = netsec::bytes_from_hex(
            "d75a980182b10ab7d54bfed3c964073a"
            "0ee172f3daa62325af021a68f707511a");
        netsec::PublicKey key{};
        std::copy(bytes.begin(), bytes.end(), key.begin());
        return key;
    }();
    const netsec::Signature expected_signature = signature_from_hex(
        "e5564300c360ac729086e2cc806e828a"
        "84877f1eb8e5d974d873e06522490155"
        "5fb8821590a33bacc61e39701cf9b46b"
        "d25bf5f0595bbe24655141438e7a100b");
    CHECK(signing_key.public_key() == expected_public);
    CHECK(signing_key.sign(std::span<const netsec::Byte>{}) ==
          expected_signature);
    CHECK(netsec::crypto::ed25519_verify(
        std::span<const netsec::Byte>{}, expected_signature, expected_public));

    const netsec::Bytes one_byte = {0x72};
    CHECK(!netsec::crypto::ed25519_verify(one_byte, expected_signature,
                                         expected_public));
}

void test_legitimate_chain() {
    Fixture fixture;
    netsec::InMemoryRollbackStore store;
    netsec::BootChainVerifier verifier(fixture.anchor, fixture.policy, store);

    const netsec::VerificationReport report = verifier.verify(fixture.stages);
    CHECK(report.ok());
    CHECK(report.error == netsec::VerifyError::none);
    CHECK(report.proof.entries.size() == fixture.stages.size());
    CHECK(report.proof.final_measurement() != report.proof.genesis);

    for (std::size_t i = 0; i < fixture.stages.size(); ++i) {
        const auto& entry = report.proof.entries[i];
        CHECK(entry.index == i);
        CHECK(entry.manifest.stage_id == fixture.stages[i].manifest.stage_id);
        CHECK(entry.measurement != entry.previous_measurement);
    }

    const auto floors = store.snapshot();
    CHECK(floors.at("BL1") == 1);
    CHECK(floors.at("BL2") == 2);
    CHECK(floors.at("BL31") == 3);
    CHECK(floors.at("APP") == 4);

    std::string proof_detail;
    CHECK(netsec::verify_proof(report.proof, fixture.anchor, &proof_detail));
    CHECK(proof_detail.empty());
    CHECK(!report.proof.canonical_bytes().empty());
    const auto round_trip =
        netsec::AttestationProof::parse(report.proof.canonical_bytes());
    CHECK(round_trip.has_value());
    if (round_trip.has_value()) {
        CHECK(netsec::verify_proof(*round_trip, fixture.anchor));
        CHECK(round_trip->canonical_bytes() == report.proof.canonical_bytes());
    }

    const auto parsed_manifest =
        netsec::StageManifest::parse(fixture.stages[1].signed_bytes());
    CHECK(parsed_manifest.has_value());
    if (parsed_manifest.has_value()) {
        CHECK(parsed_manifest->canonical_bytes() ==
              fixture.stages[1].signed_bytes());
    }

    const netsec::Bytes proof_bytes = report.proof.canonical_bytes();
    for (const std::size_t cut :
         {std::size_t{0}, std::size_t{1}, std::size_t{4}, std::size_t{8},
          proof_bytes.size() / 2, proof_bytes.size() - 1}) {
        const std::span<const netsec::Byte> truncated(proof_bytes.data(), cut);
        CHECK(!netsec::AttestationProof::parse(truncated).has_value());
    }
    netsec::Bytes trailing = proof_bytes;
    trailing.push_back(0U);
    CHECK(!netsec::AttestationProof::parse(trailing).has_value());

    const netsec::VerificationReport second = verifier.verify(fixture.stages);
    CHECK(second.ok());
    CHECK(second.proof.canonical_bytes() == report.proof.canonical_bytes());
}

void test_intermediate_replacement_and_terminal_reuse() {
    Fixture fixture;
    auto replaced = fixture.stages;

    netsec::StageManifest malicious = replaced[1].manifest;
    replaced[1] = netsec::BootStage::create(malicious,
                                            text("malicious BL2 image"),
                                            fixture.attacker);
    // The attacker reuses the untouched, legitimate terminal signature.
    CHECK(netsec::crypto::ed25519_verify(
        replaced[3].signed_bytes(), replaced[3].signature,
        fixture.app.public_key()));

    netsec::InMemoryRollbackStore store;
    netsec::BootChainVerifier verifier(fixture.anchor, fixture.policy, store);
    const netsec::VerificationReport report = verifier.verify(replaced);
    CHECK(!report.ok());
    CHECK(report.error == netsec::VerifyError::signature_invalid);
    CHECK(report.failed_stage == 1);
    CHECK(store.snapshot().empty());
}

void test_missing_intermediate() {
    Fixture fixture;
    netsec::InMemoryRollbackStore store;
    netsec::BootChainVerifier verifier(fixture.anchor, fixture.policy, store);

    auto missing_under_policy = fixture.stages;
    missing_under_policy.erase(missing_under_policy.begin() + 2);
    const netsec::VerificationReport report =
        verifier.verify(missing_under_policy);
    CHECK(!report.ok());
    CHECK(report.error == netsec::VerifyError::missing_stage);

    netsec::ChainPolicy unpinned = fixture.policy;
    unpinned.stage_ids.clear();
    netsec::BootChainVerifier unpinned_verifier(fixture.anchor, unpinned,
                                                store);
    auto index_gap = fixture.stages;
    index_gap.erase(index_gap.begin() + 1);
    const netsec::VerificationReport gap_report =
        unpinned_verifier.verify(index_gap);
    CHECK(!gap_report.ok());
    CHECK(gap_report.error == netsec::VerifyError::stage_index_mismatch);
    CHECK(gap_report.failed_stage == 1);

    auto truncated = fixture.stages;
    truncated.pop_back();
    const netsec::VerificationReport truncation_report =
        unpinned_verifier.verify(truncated);
    CHECK(!truncation_report.ok());
    CHECK(truncation_report.error == netsec::VerifyError::missing_stage);
}

void test_version_rollback() {
    Fixture fixture;
    netsec::InMemoryRollbackStore store;
    store.seed("BL2", 3);
    netsec::BootChainVerifier verifier(fixture.anchor, fixture.policy, store);

    const netsec::VerificationReport report = verifier.verify(fixture.stages);
    CHECK(!report.ok());
    CHECK(report.error == netsec::VerifyError::version_rollback);
    CHECK(report.failed_stage == 1);
    CHECK(store.floor("BL1") == 0);
    CHECK(store.floor("BL2") == 3);
    CHECK(store.floor("BL31") == 0);
    CHECK(store.floor("APP") == 0);

    Fixture signed_policy_violation;
    signed_policy_violation.stages[1].manifest.version = 1;
    signed_policy_violation.stages[1].resign(
        signed_policy_violation.bl2);
    netsec::InMemoryRollbackStore clean_store;
    netsec::BootChainVerifier policy_verifier(
        signed_policy_violation.anchor, signed_policy_violation.policy,
        clean_store);
    const netsec::VerificationReport policy_report =
        policy_verifier.verify(signed_policy_violation.stages);
    CHECK(!policy_report.ok());
    CHECK(policy_report.error ==
          netsec::VerifyError::version_policy_violation);
    CHECK(policy_report.failed_stage == 1);
}

void test_payload_and_trust_anchor_failures() {
    Fixture fixture;
    netsec::InMemoryRollbackStore store;
    netsec::BootChainVerifier verifier(fixture.anchor, fixture.policy, store);

    auto tampered_payload = fixture.stages;
    tampered_payload[2].payload = text("malicious BL31 image");
    const netsec::VerificationReport payload_report =
        verifier.verify(tampered_payload);
    CHECK(!payload_report.ok());
    CHECK(payload_report.error == netsec::VerifyError::payload_mismatch);
    CHECK(payload_report.failed_stage == 2);
    CHECK(store.snapshot().empty());

    netsec::TrustAnchor wrong_anchor = netsec::TrustAnchor::pinned(
        7, "BL1", fixture.attacker.public_key());
    netsec::InMemoryRollbackStore wrong_store;
    netsec::BootChainVerifier wrong_verifier(wrong_anchor, fixture.policy,
                                             wrong_store);
    const netsec::VerificationReport wrong_report =
        wrong_verifier.verify(fixture.stages);
    CHECK(!wrong_report.ok());
    CHECK(wrong_report.error == netsec::VerifyError::signature_invalid);
    CHECK(wrong_report.failed_stage == 0);

    netsec::TrustAnchor empty_anchor;
    netsec::InMemoryRollbackStore empty_store;
    netsec::BootChainVerifier empty_verifier(empty_anchor, fixture.policy,
                                             empty_store);
    const netsec::VerificationReport empty_report =
        empty_verifier.verify(fixture.stages);
    CHECK(!empty_report.ok());
    CHECK(empty_report.error == netsec::VerifyError::untrusted_anchor);
}

void test_atomic_rollback_commit() {
    Fixture fixture;
    netsec::InMemoryRollbackStore store;
    store.seed("BL31", 5);
    netsec::BootChainVerifier verifier(fixture.anchor, fixture.policy, store);

    const netsec::VerificationReport report = verifier.verify(fixture.stages);
    CHECK(!report.ok());
    CHECK(report.error == netsec::VerifyError::version_rollback);
    CHECK(report.failed_stage == 2);
    CHECK(store.floor("BL1") == 0);
    CHECK(store.floor("BL2") == 0);
    CHECK(store.floor("BL31") == 5);
    CHECK(store.floor("APP") == 0);
}

void test_proof_verification_and_tampering() {
    Fixture fixture;
    netsec::InMemoryRollbackStore store;
    netsec::BootChainVerifier verifier(fixture.anchor, fixture.policy, store);
    const netsec::VerificationReport report = verifier.verify(fixture.stages);
    CHECK(report.ok());

    std::string detail;
    CHECK(netsec::verify_proof(report.proof, fixture.anchor, &detail));

    auto bad_measurement = report.proof;
    bad_measurement.entries[1].measurement[0] ^= 0x01U;
    CHECK(!netsec::verify_proof(bad_measurement, fixture.anchor, &detail));
    CHECK(!detail.empty());

    auto bad_manifest = report.proof;
    bad_manifest.entries[1].manifest.version = 99;
    CHECK(!netsec::verify_proof(bad_manifest, fixture.anchor, &detail));

    auto bad_signature = report.proof;
    bad_signature.entries[1].signature[0] ^= 0x01U;
    CHECK(!netsec::verify_proof(bad_signature, fixture.anchor, &detail));

    auto bad_root = report.proof;
    bad_root.root_key = fixture.attacker.public_key();
    CHECK(!netsec::verify_proof(bad_root, fixture.anchor, &detail));

    netsec::TrustAnchor other_anchor = netsec::TrustAnchor::pinned(
        7, "BL1", fixture.attacker.public_key());
    CHECK(!netsec::verify_proof(report.proof, other_anchor, &detail));
}

void test_policy_identity_pinning() {
    Fixture fixture;
    netsec::ChainPolicy wrong_policy = fixture.policy;
    wrong_policy.stage_ids[1] = "EVIL";
    netsec::InMemoryRollbackStore store;
    netsec::BootChainVerifier verifier(fixture.anchor, wrong_policy, store);
    const netsec::VerificationReport report = verifier.verify(fixture.stages);
    CHECK(!report.ok());
    CHECK(report.error == netsec::VerifyError::stage_id_mismatch);
    CHECK(report.failed_stage == 1);
}

}  // namespace

int main() {
    test_crypto_vectors();
    test_legitimate_chain();
    test_intermediate_replacement_and_terminal_reuse();
    test_missing_intermediate();
    test_version_rollback();
    test_payload_and_trust_anchor_failures();
    test_atomic_rollback_commit();
    test_proof_verification_and_tampering();
    test_policy_identity_pinning();

    if (failures != 0) {
        std::cerr << failures << " test check(s) failed\n";
        return 1;
    }
    std::cout << "all boot-chain tests passed\n";
    return 0;
}
