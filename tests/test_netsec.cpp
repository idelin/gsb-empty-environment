#include "netsec.hpp"

#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

namespace {

int failures = 0;

#define CHECK(condition)                                                     \
    do {                                                                     \
        if (!(condition)) {                                                  \
            std::cerr << __FILE__ << ":" << __LINE__                         \
                      << ": check failed: " #condition << "\n";             \
            ++failures;                                                      \
        }                                                                    \
    } while (false)

netsec::Bytes append(netsec::Bytes bytes,
                     std::initializer_list<std::uint8_t> suffix) {
    bytes.insert(bytes.end(), suffix.begin(), suffix.end());
    return bytes;
}

netsec::Bytes append_nonce(netsec::Bytes bytes, const netsec::Nonce& nonce) {
    bytes.insert(bytes.end(), nonce.begin(), nonce.end());
    return bytes;
}

netsec::Bytes test_signature(const netsec::Sha384Digest& digest) {
    static const netsec::Bytes key = {
        0x9a, 0x30, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66,
        0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee
    };

    netsec::Bytes input = key;
    input.insert(input.end(), digest.begin(), digest.end());
    const netsec::Sha384Digest material = netsec::sha384(input);

    netsec::Bytes signature(64);
    for (std::size_t i = 0; i < signature.size(); ++i) {
        signature[i] = static_cast<std::uint8_t>(
            material[i % material.size()] ^ static_cast<std::uint8_t>(i));
    }
    return signature;
}

bool test_verify(const netsec::Sha384Digest& digest,
                 netsec::SignatureAlgorithm algorithm,
                 const netsec::Bytes& signature,
                 const netsec::CertificateChainRecord&) {
    if (algorithm != netsec::SignatureAlgorithm::EcdsaP256Sha384) {
        return false;
    }
    const netsec::Bytes expected = test_signature(digest);
    return netsec::constant_time_equal(signature.data(), expected.data(),
                                       expected.size());
}

struct Fixture {
    netsec::SessionTranscript session;
    netsec::MeasurementRequest request;
    netsec::MeasurementResponse response;
    netsec::VerificationPolicy policy;
};

netsec::Bytes make_request_bytes(const netsec::Nonce& nonce,
                                 std::uint8_t marker = 0x31) {
    netsec::Bytes bytes{0x10, 0x01, marker};
    bytes = append_nonce(std::move(bytes), nonce);
    return append(std::move(bytes), {0x00, 0x01, 0x00});
}

netsec::Bytes make_response_bytes(
    const netsec::Nonce& nonce,
    const std::vector<netsec::MeasurementBlock>& measurements,
    std::uint8_t marker = 0x41) {
    netsec::Bytes bytes{0x20, 0x02, marker,
                        static_cast<std::uint8_t>(measurements.size())};
    bytes = append_nonce(std::move(bytes), nonce);
    for (const netsec::MeasurementBlock& block : measurements) {
        bytes.push_back(block.index);
        bytes.push_back(static_cast<std::uint8_t>(block.value.size()));
        bytes.insert(bytes.end(), block.value.begin(), block.value.end());
    }
    return append(std::move(bytes), {0x00, 0x00});
}

void reseal(Fixture& fixture) {
    const netsec::Sha384Digest digest = netsec::compute_transcript_digest(
        fixture.session, fixture.request, fixture.response);
    fixture.response.signed_transcript_digest = digest;
    fixture.response.signature_present = true;
    fixture.response.signature = test_signature(digest);
}

Fixture make_fixture() {
    Fixture fixture;

    fixture.session.vca = {0x56, 0x43, 0x41, 0x00, 0x01, 0x02};
    netsec::Bytes root_der = {0x30, 0x82, 0x01, 0x00, 0x52, 0x4f, 0x4f, 0x54};
    fixture.session.certificate_chain =
        netsec::CertificateChainRecord::from_root_certificate(
            {0x43, 0x48, 0x41, 0x49, 0x4e, 0x01, 0x02, 0x03},
            root_der);

    for (std::size_t i = 0; i < fixture.request.nonce.size(); ++i) {
        fixture.request.nonce[i] = static_cast<std::uint8_t>(i);
    }
    fixture.request.bytes = make_request_bytes(fixture.request.nonce);
    fixture.request.signature_requested = true;

    for (std::size_t i = 0; i < fixture.response.nonce.size(); ++i) {
        fixture.response.nonce[i] = static_cast<std::uint8_t>(0x80 + i);
    }
    fixture.response.measurements = {
        {0, {0xaa, 0xbb}},
        {1, {0xcc}},
        {2, {0xdd, 0xee, 0xff}}};
    fixture.response.bytes_without_signature =
        make_response_bytes(fixture.response.nonce,
                            fixture.response.measurements);
    fixture.response.signature_algorithm =
        netsec::SignatureAlgorithm::EcdsaP256Sha384;

    fixture.policy.trusted_root_hashes.push_back(netsec::sha384(root_der));
    fixture.policy.expected_measurements = {
        {0, {0xaa, 0xbb}},
        {1, {0xcc}},
        {2, {0xdd, 0xee, 0xff}}};
    fixture.policy.signature_verifier = test_verify;

    reseal(fixture);
    return fixture;
}

void expect_error(const netsec::VerificationResult& result,
                  netsec::VerificationError expected,
                  const char* test_name) {
    if (result.error != expected) {
        std::cerr << test_name << ": expected "
                  << netsec::verification_error_name(expected)
                  << ", got "
                  << netsec::verification_error_name(result.error)
                  << " (" << result.detail << ")\n";
        ++failures;
    }
}

void test_sha384_vectors() {
    const auto empty = netsec::sha384(nullptr, 0);
    CHECK(netsec::to_hex(empty) ==
          "38b060a751ac96384cd9327eb1b1e36a21fdb71114be0743"
          "4c0cc7bf63f6e1da274edebfe76f65fbd51ad2f14898b95b");

    const auto abc = netsec::sha384(netsec::Bytes{'a', 'b', 'c'});
    CHECK(netsec::to_hex(abc) ==
          "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded163"
          "1a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7");

    netsec::Sha384 hasher;
    const netsec::Bytes prefix{'a'};
    const netsec::Bytes suffix{'b', 'c'};
    hasher.update(prefix);
    hasher.update(suffix);
    CHECK(hasher.finalize() == abc);

    std::string input;
    for (int i = 0; i < 20; ++i) {
        input += "0123456789abcdef";
    }
    const netsec::Bytes long_message(input.begin(), input.end());
    CHECK(long_message.size() == 320);
    CHECK(netsec::to_hex(netsec::sha384(long_message)) ==
          "2a427a4c589f8200c876512bad269a85e79d26dfed00cd37"
          "c03132169fc834247f38b0b0ec338e554f9ed2debbad5d63");
}

void test_nonce_and_hex_helpers() {
    netsec::Nonce generated = netsec::generate_request_nonce(
        [](std::uint8_t* output, std::size_t size) {
            for (std::size_t i = 0; i < size; ++i) {
                output[i] = static_cast<std::uint8_t>(0xf0 + i);
            }
        });
    CHECK(generated[0] == 0xf0);
    CHECK(generated[31] == 0x0f);

    netsec::Nonce generated_from_bytes = netsec::generate_request_nonce(
        [](std::size_t size) {
            return netsec::Bytes(size, 0x5a);
        });
    CHECK(generated_from_bytes[0] == 0x5a);
    CHECK(generated_from_bytes[31] == 0x5a);

    const std::string nonce_hex = netsec::to_hex(generated);
    const auto parsed_nonce = netsec::nonce_from_hex(nonce_hex);
    CHECK(parsed_nonce.has_value());
    CHECK(*parsed_nonce == generated);

    CHECK(!netsec::bytes_from_hex("xyz").has_value());
    CHECK(!netsec::nonce_from_hex("0011").has_value());
}

void test_valid_response() {
    Fixture fixture = make_fixture();
    const auto result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    CHECK(result.ok());
}

void test_replayed_response_is_rejected() {
    Fixture fixture = make_fixture();
    const netsec::MeasurementResponse replayed = fixture.response;

    for (std::size_t i = 0; i < fixture.request.nonce.size(); ++i) {
        fixture.request.nonce[i] = static_cast<std::uint8_t>(0x40 + i);
    }
    fixture.request.bytes = make_request_bytes(fixture.request.nonce, 0x32);

    const auto result = netsec::verify_measurements_response(
        fixture.session, fixture.request, replayed, fixture.policy);
    expect_error(result, netsec::VerificationError::TranscriptDigestMismatch,
                 "replayed response");

    // Even without a response-carried digest, the signature is checked against
    // the transcript digest of the new request.
    netsec::MeasurementResponse replayed_without_digest = replayed;
    replayed_without_digest.signed_transcript_digest.reset();
    const auto signature_result = netsec::verify_measurements_response(
        fixture.session, fixture.request, replayed_without_digest,
        fixture.policy);
    expect_error(signature_result,
                 netsec::VerificationError::SignatureVerificationFailed,
                 "replayed signature");
}

void test_changed_response_nonce_is_rejected() {
    Fixture fixture = make_fixture();

    // Equal response nonce is an explicit protocol violation.
    fixture.response.nonce = fixture.request.nonce;
    auto result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result,
                 netsec::VerificationError::ResponseNonceEqualsRequestNonce,
                 "equal nonce");

    // A response nonce changed in the signed transcript invalidates the
    // transcript digest / signature.
    fixture = make_fixture();
    fixture.response.nonce[0] ^= 0x01;
    fixture.response.bytes_without_signature[4] ^= 0x01;
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result, netsec::VerificationError::TranscriptDigestMismatch,
                 "changed response nonce");

    // Metadata and transcript bytes must describe the same nonce.
    fixture = make_fixture();
    fixture.response.nonce[0] ^= 0x01;
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result,
                 netsec::VerificationError::ResponseNonceNotInTranscript,
                 "response nonce absent from transcript");

    fixture = make_fixture();
    fixture.request.nonce[0] ^= 0x01;
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result,
                 netsec::VerificationError::RequestNonceNotInTranscript,
                 "request nonce absent from transcript");
}

void test_transcript_record_change_is_rejected() {
    Fixture fixture = make_fixture();
    fixture.session.vca[0] ^= 0x01;
    auto result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result, netsec::VerificationError::TranscriptDigestMismatch,
                 "changed VCA");

    fixture = make_fixture();
    fixture.session.certificate_chain.record[1] ^= 0x80;
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result, netsec::VerificationError::TranscriptDigestMismatch,
                 "changed certificate chain record");

    fixture = make_fixture();
    fixture.request.bytes[2] ^= 0x10;
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result, netsec::VerificationError::TranscriptDigestMismatch,
                 "changed request record");

    fixture = make_fixture();
    fixture.response.bytes_without_signature[2] ^= 0x04;
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result, netsec::VerificationError::TranscriptDigestMismatch,
                 "changed response record");
}

void test_duplicate_and_unsorted_indices_are_rejected() {
    Fixture fixture = make_fixture();
    fixture.response.measurements = {
        {0, {0xaa, 0xbb}},
        {0, {0xcc}},
        {2, {0xdd, 0xee, 0xff}}};
    auto result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result,
                 netsec::VerificationError::MeasurementBlockRepeatedIndex,
                 "duplicate index");
    CHECK(result.block_index.has_value());
    CHECK(*result.block_index == 0);

    fixture = make_fixture();
    fixture.response.measurements = {
        {0, {0xaa, 0xbb}},
        {2, {0xcc}},
        {1, {0xdd, 0xee, 0xff}}};
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result,
                 netsec::VerificationError::MeasurementBlockIndexNotAscending,
                 "unsorted indices");
}

void test_signature_presence_and_length() {
    Fixture fixture = make_fixture();
    fixture.response.signature_present = false;
    fixture.response.signature.clear();
    auto result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result, netsec::VerificationError::MissingSignature,
                 "missing signature");

    fixture = make_fixture();
    fixture.response.signature.pop_back();
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result, netsec::VerificationError::SignatureLengthMismatch,
                 "short signature");

    fixture = make_fixture();
    fixture.response.signature_algorithm =
        static_cast<netsec::SignatureAlgorithm>(0xff);
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result,
                 netsec::VerificationError::UnsupportedSignatureAlgorithm,
                 "unknown algorithm");
}

void test_trusted_roots() {
    Fixture fixture = make_fixture();
    fixture.policy.trusted_root_hashes.clear();
    auto result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result, netsec::VerificationError::UntrustedCertificateRoot,
                 "untrusted root");

    fixture = make_fixture();
    fixture.session.certificate_chain.has_root_certificate_hash = false;
    fixture.session.certificate_chain.root_certificate_der.clear();
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result,
                 netsec::VerificationError::MissingRootCertificateHash,
                 "missing root hash");

    fixture = make_fixture();
    const netsec::Sha384Digest root_hash =
        *netsec::certificate_chain_root_hash(fixture.session.certificate_chain);
    CHECK(fixture.policy.trusted_root_hashes.front() == root_hash);
    CHECK(netsec::is_trusted_root(root_hash, fixture.policy.trusted_root_hashes));
}

void test_measurement_expectations() {
    Fixture fixture = make_fixture();
    fixture.response.measurements = {
        {0, {0xaa, 0xbb}},
        {2, {0xdd, 0xee, 0xff}}};
    auto result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    CHECK(result.error == netsec::VerificationError::MissingMeasurementBlock ||
          result.error == netsec::VerificationError::MeasurementCountMismatch);

    fixture = make_fixture();
    fixture.response.measurements.push_back({3, {0x11}});
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    CHECK(result.error == netsec::VerificationError::UnexpectedMeasurementBlock ||
          result.error == netsec::VerificationError::MeasurementCountMismatch);

    fixture = make_fixture();
    fixture.response.measurements[1].value[0] = 0x00;
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result, netsec::VerificationError::MeasurementValueMismatch,
                 "value mismatch");

    fixture = make_fixture();
    fixture.policy.expected_measurements = {
        {0, {0xaa, 0xbb}},
        {0, {0xcc}},
        {2, {0xdd, 0xee, 0xff}}};
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result,
                 netsec::VerificationError::InvalidExpectedMeasurementSet,
                 "duplicate expected index");
}

void test_unsigned_request_path() {
    Fixture fixture = make_fixture();
    fixture.request.signature_requested = false;
    fixture.response.signature_present = false;
    fixture.response.signature.clear();
    fixture.response.signed_transcript_digest.reset();

    const auto result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    CHECK(result.ok());
}

void test_signature_verification_paths() {
    Fixture fixture = make_fixture();
    fixture.policy.signature_verifier = {};
    auto result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result, netsec::VerificationError::MissingSignatureVerifier,
                 "missing verifier");

    fixture = make_fixture();
    fixture.policy.signature_verifier =
        [](const netsec::Sha384Digest&, netsec::SignatureAlgorithm,
           const netsec::Bytes&, const netsec::CertificateChainRecord&) {
            return false;
        };
    result = netsec::verify_measurements_response(
        fixture.session, fixture.request, fixture.response, fixture.policy);
    expect_error(result, netsec::VerificationError::SignatureVerificationFailed,
                 "verifier rejects");
}

void test_verifier_class_reuse() {
    Fixture fixture = make_fixture();
    netsec::MeasurementResponseVerifier verifier(fixture.policy);
    CHECK(verifier.verify(fixture.session, fixture.request, fixture.response).ok());
    CHECK(verifier.verify(fixture.session, fixture.request, fixture.response).ok());
}

}  // namespace

int main() {
    test_sha384_vectors();
    test_nonce_and_hex_helpers();
    test_valid_response();
    test_replayed_response_is_rejected();
    test_changed_response_nonce_is_rejected();
    test_transcript_record_change_is_rejected();
    test_duplicate_and_unsorted_indices_are_rejected();
    test_signature_presence_and_length();
    test_trusted_roots();
    test_measurement_expectations();
    test_unsigned_request_path();
    test_signature_verification_paths();
    test_verifier_class_reuse();

    if (failures != 0) {
        std::cerr << failures << " test check(s) failed\n";
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
