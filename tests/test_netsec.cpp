#include "netsec.hpp"

#include <algorithm>
#include <fstream>
#include <iostream>
#include <string>
#include <vector>

#ifndef NETSEC_FIXTURE_DIR
#define NETSEC_FIXTURE_DIR "tests/fixtures"
#endif

namespace {

int failures = 0;

#define CHECK(condition)                                                     \
    do {                                                                     \
        if (!(condition)) {                                                  \
            std::cerr << __FILE__ << ':' << __LINE__ << ": CHECK failed: "  \
                      << #condition << '\n';                                 \
            ++failures;                                                      \
        }                                                                    \
    } while (false)

#define CHECK_CODE(result, expected)                                         \
    do {                                                                     \
        const auto check_result = (result);                                  \
        if (check_result.code != (expected)) {                               \
            std::cerr << __FILE__ << ':' << __LINE__ << ": expected "        \
                      << netsec::to_string(expected) << ", got "            \
                      << netsec::to_string(check_result.code) << " ("        \
                      << check_result.message << ")\n";                     \
            ++failures;                                                      \
        }                                                                    \
    } while (false)

netsec::Bytes load(const std::string& name) {
    std::ifstream in(std::string(NETSEC_FIXTURE_DIR) + "/" + name,
                     std::ios::binary);
    return netsec::Bytes((std::istreambuf_iterator<char>(in)),
                         std::istreambuf_iterator<char>());
}

netsec::Digest digest_of(const std::string& fixture) {
    const netsec::Bytes bytes = load(fixture);
    return netsec::Digest::compute(netsec::HashAlgorithm::Sha256,
                                   netsec::ByteView(bytes));
}

netsec::VerificationPolicy base_policy(const netsec::ImportedTimestampToken& token) {
    netsec::VerificationPolicy policy;
    policy.expected_nonce = token.tst_info.nonce;
    policy.expected_serial_number = token.tst_info.serial_number;
    policy.expected_tsa = "CN=Test TSA,O=Test Org,C=CN";
    // Keep fixtures deterministic instead of depending on the wall clock.
    policy.evaluation_time = token.tst_info.gen_time + std::chrono::hours(1);
    return policy;
}

netsec::TrustStore root_store() {
    netsec::TrustStore trust;
    trust.add_anchor(load("ca.der"));
    return trust;
}

void test_digest_vectors() {
    using netsec::HashAlgorithm;
    struct Vector {
        HashAlgorithm algorithm;
        const char* message;
        const char* expected;
    };
    const Vector vectors[] = {
        {HashAlgorithm::Md5, "", "d41d8cd98f00b204e9800998ecf8427e"},
        {HashAlgorithm::Md5, "abc", "900150983cd24fb0d6963f7d28e17f72"},
        {HashAlgorithm::Sha1, "", "da39a3ee5e6b4b0d3255bfef95601890afd80709"},
        {HashAlgorithm::Sha1, "abc",
         "a9993e364706816aba3e25717850c26c9cd0d89d"},
        {HashAlgorithm::Sha224, "abc",
         "23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7"},
        {HashAlgorithm::Sha256, "",
         "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
        {HashAlgorithm::Sha256, "abc",
         "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
        {HashAlgorithm::Sha384, "abc",
         "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed"
         "8086072ba1e7cc2358baeca134c825a7"},
        {HashAlgorithm::Sha512, "abc",
         "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a"
         "2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"},
    };
    for (const Vector& vector : vectors) {
        const auto digest = netsec::Digest::compute(vector.algorithm,
                                                    std::string_view(vector.message));
        const std::string actual = netsec::hex_encode(digest.bytes);
        if (actual != vector.expected) {
            std::cerr << "digest vector " << netsec::to_string(vector.algorithm)
                      << "('" << vector.message << "') = " << actual
                      << ", expected " << vector.expected << '\n';
            ++failures;
        }
    }
}

void test_import_and_valid_binding() {
    const auto imported = netsec::import_timestamp_token(load("valid-token.der"));
    CHECK(imported.ok());
    if (!imported.ok()) return;

    const auto& token = imported.token;
    CHECK(token.tst_info.version == 1);
    CHECK(token.tst_info.policy_oid == "1.3.6.1.4.1.99999.1");
    CHECK(token.tst_info.message_imprint.algorithm == netsec::HashAlgorithm::Sha256);
    CHECK(netsec::hex_encode(token.tst_info.message_imprint.digest) ==
          "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9");
    CHECK(token.tst_info.serial_number == "2");
    CHECK(token.tst_info.nonce.has_value());
    CHECK(token.tst_info.gen_time_utc == "20261114074029Z");
    CHECK(token.tst_info.tsa_name.value_or("") == "CN=Test TSA,O=Test Org,C=CN");
    CHECK(token.certificates.size() == 2);
    CHECK(token.signer_certificate.has_value());
    if (token.signer_certificate) {
        CHECK(token.signer_certificate->subject == "CN=Test TSA,O=Test Org,C=CN");
        CHECK(token.signer_certificate->issuer == "CN=Test Root CA");
        CHECK(token.signer_certificate->has_time_stamping_eku);
    }

    netsec::TimestampVerifier verifier(base_policy(token), root_store());
    const auto result = verifier.verify(token, digest_of("artifact.bin"));
    CHECK_CODE(result, netsec::ErrorCode::Ok);
    CHECK(result.message_imprint_checked);
    CHECK(result.digest_algorithm_checked);
    CHECK(result.nonce_checked);
    CHECK(result.serial_checked);
    CHECK(result.issuer_checked == false);
    CHECK(result.time_checked);
    CHECK(result.signed_attributes_checked);
    CHECK(result.signature_checked);
    CHECK(result.chain_checked);

    // A trust store may be populated with either DER or PEM bytes.
    netsec::TrustStore pem_trust;
    pem_trust.trust_anchors.push_back(load("ca.pem"));
    netsec::TimestampVerifier pem_verifier(base_policy(token), pem_trust);
    CHECK_CODE(pem_verifier.verify(token, digest_of("artifact.bin")),
               netsec::ErrorCode::Ok);
    CHECK_CODE(token.verify(digest_of("artifact.bin"), base_policy(token),
                            root_store()),
               netsec::ErrorCode::Ok);
}

void test_digest_binding_failures() {
    const auto imported = netsec::import_timestamp_token(load("valid-token.der"));
    CHECK(imported.ok());
    if (!imported.ok()) return;
    netsec::TimestampVerifier verifier(base_policy(imported.token), root_store());

    // Exact algorithm is part of the binding.
    const auto artifact = load("artifact.bin");
    const auto sha1 = netsec::Digest::compute(netsec::HashAlgorithm::Sha1,
                                              netsec::ByteView(artifact));
    CHECK_CODE(verifier.verify(imported.token, sha1),
               netsec::ErrorCode::DigestAlgorithmMismatch);

    // An old token cannot be moved to a different artifact.
    CHECK_CODE(verifier.verify(imported.token, digest_of("other.bin")),
               netsec::ErrorCode::MessageImprintMismatch);
}

void test_token_reuse_is_rejected() {
    const auto other_token =
        netsec::import_timestamp_token(load("token-other.der"));
    CHECK(other_token.ok());
    if (!other_token.ok()) return;

    netsec::TimestampVerifier verifier(base_policy(other_token.token),
                                       root_store());
    CHECK_CODE(verifier.verify(other_token.token, digest_of("artifact.bin")),
               netsec::ErrorCode::MessageImprintMismatch);
    CHECK_CODE(verifier.verify(other_token.token, digest_of("other.bin")),
               netsec::ErrorCode::Ok);
}

void test_nonce_checks() {
    const auto imported = netsec::import_timestamp_token(load("valid-token.der"));
    CHECK(imported.ok());
    if (!imported.ok()) return;
    netsec::TimestampVerifier verifier(base_policy(imported.token), root_store());
    const auto artifact_digest = digest_of("artifact.bin");

    auto wrong = base_policy(imported.token);
    wrong.expected_nonce = "0x01";
    netsec::TimestampVerifier wrong_verifier(wrong, root_store());
    CHECK_CODE(wrong_verifier.verify(imported.token, artifact_digest),
               netsec::ErrorCode::NonceMismatch);

    auto missing_expected = base_policy(imported.token);
    missing_expected.expected_nonce.reset();
    missing_expected.require_expected_nonce = true;
    netsec::TimestampVerifier missing_verifier(missing_expected, root_store());
    CHECK_CODE(missing_verifier.verify(imported.token, artifact_digest),
               netsec::ErrorCode::NonceMismatch);

    const auto no_nonce =
        netsec::import_timestamp_token(load("token-no-nonce.der"));
    CHECK(no_nonce.ok());
    if (!no_nonce.ok()) return;
    auto no_nonce_policy = base_policy(no_nonce.token);
    no_nonce_policy.expected_nonce.reset();
    netsec::TimestampVerifier no_nonce_verifier(no_nonce_policy, root_store());
    CHECK_CODE(no_nonce_verifier.verify(no_nonce.token, artifact_digest),
               netsec::ErrorCode::MissingNonce);

    // Explicitly relaxing the presence requirement is possible, but never
    // implicitly.
    no_nonce_policy.require_nonce = false;
    netsec::TimestampVerifier relaxed(no_nonce_policy, root_store());
    CHECK_CODE(relaxed.verify(no_nonce.token, artifact_digest),
               netsec::ErrorCode::Ok);
}

void test_serial_issuer_policy_and_time() {
    const auto imported = netsec::import_timestamp_token(load("valid-token.der"));
    CHECK(imported.ok());
    if (!imported.ok()) return;
    const auto artifact_digest = digest_of("artifact.bin");

    auto serial = base_policy(imported.token);
    serial.expected_serial_number = "3";
    netsec::TimestampVerifier serial_verifier(serial, root_store());
    CHECK_CODE(serial_verifier.verify(imported.token, artifact_digest),
               netsec::ErrorCode::SerialMismatch);

    auto issuer = base_policy(imported.token);
    issuer.expected_issuer = "CN=Someone Else";
    netsec::TimestampVerifier issuer_verifier(issuer, root_store());
    CHECK_CODE(issuer_verifier.verify(imported.token, artifact_digest),
               netsec::ErrorCode::IssuerMismatch);

    auto tsa = base_policy(imported.token);
    tsa.expected_tsa = "CN=Someone Else";
    netsec::TimestampVerifier tsa_verifier(tsa, root_store());
    CHECK_CODE(tsa_verifier.verify(imported.token, artifact_digest),
               netsec::ErrorCode::TsaMismatch);

    auto policy = base_policy(imported.token);
    policy.expected_policy_oid = "1.2.3.4";
    netsec::TimestampVerifier policy_verifier(policy, root_store());
    CHECK_CODE(policy_verifier.verify(imported.token, artifact_digest),
               netsec::ErrorCode::PolicyMismatch);

    auto window = base_policy(imported.token);
    window.not_after = imported.token.tst_info.gen_time -
                       std::chrono::seconds(1);
    netsec::TimestampVerifier window_verifier(window, root_store());
    CHECK_CODE(window_verifier.verify(imported.token, artifact_digest),
               netsec::ErrorCode::TimeOutsidePolicy);

    auto future = base_policy(imported.token);
    future.evaluation_time = imported.token.tst_info.gen_time -
                             std::chrono::hours(1);
    netsec::TimestampVerifier future_verifier(future, root_store());
    CHECK_CODE(future_verifier.verify(imported.token, artifact_digest),
               netsec::ErrorCode::TimeOutsidePolicy);
}

void test_chain_and_revocation() {
    const auto imported = netsec::import_timestamp_token(load("valid-token.der"));
    CHECK(imported.ok());
    if (!imported.ok()) return;
    const auto artifact_digest = digest_of("artifact.bin");

    netsec::TimestampVerifier untrusted(base_policy(imported.token));
    CHECK_CODE(untrusted.verify(imported.token, artifact_digest),
               netsec::ErrorCode::ChainUntrusted);

    auto required = base_policy(imported.token);
    required.revocation = netsec::RevocationMode::Required;
    netsec::TimestampVerifier required_verifier(required, root_store());
    CHECK_CODE(required_verifier.verify(imported.token, artifact_digest),
               netsec::ErrorCode::RevocationUnknown);

    netsec::RevocationEvidence evidence;
    evidence.crls.push_back(load("tsa-revoked.crl.der"));
    CHECK_CODE(netsec::verify_timestamp_token(imported.token, artifact_digest,
                                              base_policy(imported.token),
                                              root_store(), evidence),
               netsec::ErrorCode::Revoked);

    // The same CRL supplied as PEM must behave identically.
    netsec::RevocationEvidence pem_evidence;
    pem_evidence.crls.push_back(load("tsa-revoked.crl.pem"));
    CHECK_CODE(netsec::verify_timestamp_token(imported.token, artifact_digest,
                                              base_policy(imported.token),
                                              root_store(), pem_evidence),
               netsec::ErrorCode::Revoked);

    netsec::RevocationEvidence serial_list;
    serial_list.revoked_serials.push_back("0xDE7F86F4FDB64C23");
    CHECK_CODE(netsec::verify_timestamp_token(imported.token, artifact_digest,
                                              base_policy(imported.token),
                                              root_store(), serial_list),
               netsec::ErrorCode::Revoked);

    netsec::RevocationEvidence ocsp_evidence;
    ocsp_evidence.ocsp_responses.push_back(
        load("ocsp-tsa-revoked.der"));
    CHECK_CODE(netsec::verify_timestamp_token(imported.token, artifact_digest,
                                              base_policy(imported.token),
                                              root_store(), ocsp_evidence),
               netsec::ErrorCode::Revoked);

    auto required_with_good = base_policy(imported.token);
    required_with_good.revocation = netsec::RevocationMode::Required;
    netsec::RevocationEvidence good_evidence;
    good_evidence.ocsp_responses.push_back(load("ocsp-tsa-good.der"));
    const auto good_result = netsec::verify_timestamp_token(
        imported.token, artifact_digest, required_with_good, root_store(),
        good_evidence);
    CHECK_CODE(good_result, netsec::ErrorCode::Ok);
    CHECK(good_result.revocation_checked);

    netsec::RevocationEvidence good_list;
    good_list.good_serials.push_back("DE7F86F4FDB64C23");
    const auto good_list_result = netsec::verify_timestamp_token(
        imported.token, artifact_digest, required_with_good, root_store(),
        good_list);
    CHECK_CODE(good_list_result, netsec::ErrorCode::Ok);

    netsec::TimestampVerifier callback_verifier(base_policy(imported.token),
                                                root_store());
    callback_verifier.set_revocation_checker(
        [](const netsec::CertificateInfo&, const netsec::CertificateInfo&) {
            return netsec::RevocationStatus::Revoked;
        });
    CHECK_CODE(callback_verifier.verify(imported.token, artifact_digest),
               netsec::ErrorCode::Revoked);

    // Revocation does not make the token valid for a different artifact.
    CHECK_CODE(netsec::verify_timestamp_token(imported.token, digest_of("other.bin"),
                                              base_policy(imported.token),
                                              root_store(), evidence),
               netsec::ErrorCode::MessageImprintMismatch);
}

void test_tampering() {
    const auto original = load("valid-token.der");
    const auto imported = netsec::import_timestamp_token(original);
    CHECK(imported.ok());
    if (!imported.ok()) return;
    netsec::TimestampVerifier verifier(base_policy(imported.token), root_store());
    const auto artifact_digest = digest_of("artifact.bin");

    netsec::Bytes signature_tampered = original;
    signature_tampered.back() ^= 0x01;
    CHECK_CODE(verifier.verify(signature_tampered, artifact_digest),
               netsec::ErrorCode::SignatureInvalid);

    // Replace messageImprint bytes inside TSTInfo; the CMS message-digest
    // signed attribute must catch this even before signature verification.
    netsec::Bytes imprint_tampered = original;
    const auto& original_digest = imported.token.tst_info.message_imprint.digest;
    auto it = std::search(imprint_tampered.begin(), imprint_tampered.end(),
                          original_digest.begin(), original_digest.end());
    CHECK(it != imprint_tampered.end());
    if (it != imprint_tampered.end()) {
        std::size_t index = static_cast<std::size_t>(
            std::distance(imprint_tampered.begin(), it));
        imprint_tampered[index] ^= 0x01;
        const auto reimported =
            netsec::import_timestamp_token(imprint_tampered);
        CHECK(reimported.ok());
        if (reimported.ok()) {
            netsec::Digest forged_digest;
            forged_digest.algorithm =
                reimported.token.tst_info.message_imprint.algorithm;
            forged_digest.bytes =
                reimported.token.tst_info.message_imprint.digest;
            const auto result = verifier.verify(reimported.token,
                                                forged_digest);
            CHECK(!result.ok());
            if (result.ok() ||
                (result.code != netsec::ErrorCode::ContentDigestMismatch &&
                 result.code != netsec::ErrorCode::SignatureInvalid)) {
                std::cerr << "imprint tamper code: "
                          << netsec::to_string(result.code) << " "
                          << result.message << '\n';
            }
            CHECK(result.code == netsec::ErrorCode::ContentDigestMismatch ||
                  result.code == netsec::ErrorCode::SignatureInvalid);
        }
    }
}

void test_import_encodings_and_helpers() {
    const auto der = load("valid-token.der");
    const std::string base64 = netsec::base64_encode(netsec::ByteView(der));
    auto imported = netsec::import_timestamp_token_base64(base64);
    CHECK(imported.ok());

    std::string pem = "-----BEGIN TIMESTAMP-TOKEN-----\n";
    for (std::size_t i = 0; i < base64.size(); i += 64) {
        pem += base64.substr(i, 64) + "\n";
    }
    pem += "-----END TIMESTAMP-TOKEN-----\n";
    imported = netsec::import_timestamp_token_pem(pem);
    CHECK(imported.ok());

    const auto cert = netsec::parse_certificate(netsec::ByteView(load("ca.der")));
    CHECK(cert.has_value());
    if (cert) {
        CHECK(cert->is_ca);
        CHECK(cert->subject == "CN=Test Root CA");
        CHECK(cert->self_signed);
    }

    std::string crl_error;
    CHECK(netsec::normalize_crl(netsec::ByteView(load("tsa-revoked.crl.der")),
                                &crl_error)
              .has_value());
}

void test_malformed_inputs_do_not_crash() {
    CHECK_CODE(netsec::import_timestamp_token(netsec::Bytes{}),
               netsec::ErrorCode::InvalidDer);
    const netsec::Bytes indefinite{0x30, 0x80, 0x00, 0x00};
    CHECK_CODE(netsec::import_timestamp_token(indefinite),
               netsec::ErrorCode::InvalidDer);

    const netsec::Bytes valid = load("valid-token.der");
    netsec::Bytes trailing = valid;
    trailing.push_back(0x00);
    CHECK_CODE(netsec::import_timestamp_token(trailing),
               netsec::ErrorCode::InvalidDer);

    const std::size_t cuts[] = {0, 1, 10, 50, 100, 200, 500, 1000,
                                valid.size() - 1};
    for (std::size_t cut : cuts) {
        netsec::Bytes truncated(valid.begin(), valid.begin() + cut);
        const auto result = netsec::import_timestamp_token(truncated);
        CHECK(!result.ok());
    }
}

void test_binding_component_seam() {
    const auto imported = netsec::import_timestamp_token(load("valid-token.der"));
    CHECK(imported.ok());
    if (!imported.ok()) return;
    netsec::TimestampVerifier verifier(base_policy(imported.token), root_store());
    netsec::DefaultTimestampTokenBinding binding(verifier);
    const netsec::ITimestampTokenBinding& interface = binding;
    CHECK_CODE(interface.verify(imported.token, digest_of("artifact.bin")),
               netsec::ErrorCode::Ok);
}

void test_ecdsa_token() {
    for (const char* fixture :
         {"token-ec.der", "token-secp384r1.der", "token-secp521r1.der"}) {
        const auto imported = netsec::import_timestamp_token(load(fixture));
        CHECK(imported.ok());
        if (!imported.ok()) {
            std::cerr << fixture << " import: "
                      << netsec::to_string(imported.code) << " "
                      << imported.message << '\n';
        }
        if (!imported.ok()) continue;
        auto policy = base_policy(imported.token);
        if (imported.token.signer_certificate) {
            policy.expected_tsa = imported.token.signer_certificate->subject;
        }
        netsec::TimestampVerifier verifier(policy, root_store());
        const auto result =
            verifier.verify(imported.token, digest_of("artifact.bin"));
        CHECK_CODE(result, netsec::ErrorCode::Ok);
        CHECK(result.signature_checked);
    }
}

void test_sha1_legacy_token() {
    const auto imported =
        netsec::import_timestamp_token(load("token-sha1.der"));
    CHECK(imported.ok());
    if (!imported.ok()) return;
    CHECK(imported.token.tst_info.message_imprint.algorithm ==
          netsec::HashAlgorithm::Sha1);
    const auto artifact = load("artifact.bin");
    const auto digest = netsec::Digest::compute(netsec::HashAlgorithm::Sha1,
                                                netsec::ByteView(artifact));
    netsec::TimestampVerifier verifier(base_policy(imported.token),
                                       root_store());
    CHECK_CODE(verifier.verify(imported.token, digest), netsec::ErrorCode::Ok);

    auto strict = base_policy(imported.token);
    strict.allow_weak_message_imprint = false;
    netsec::TimestampVerifier strict_verifier(strict, root_store());
    CHECK_CODE(strict_verifier.verify(imported.token, digest),
               netsec::ErrorCode::UnsupportedAlgorithm);
}

}  // namespace

int main() {
    test_digest_vectors();
    test_import_and_valid_binding();
    test_digest_binding_failures();
    test_token_reuse_is_rejected();
    test_nonce_checks();
    test_serial_issuer_policy_and_time();
    test_chain_and_revocation();
    test_tampering();
    test_import_encodings_and_helpers();
    test_malformed_inputs_do_not_crash();
    test_binding_component_seam();
    test_ecdsa_token();
    test_sha1_legacy_token();

    if (failures == 0) {
        std::cout << "all netsec tests passed\n";
        return 0;
    }
    std::cerr << failures << " test checks failed\n";
    return 1;
}
