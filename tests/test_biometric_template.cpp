#include "netsec.hpp"

#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <iostream>
#include <limits>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace bio = netsec::biometric;

namespace {

int failures = 0;

void check(bool condition, std::string_view expression, int line)
{
    if (!condition) {
        std::cerr << "FAIL line " << line << ": " << expression << '\n';
        ++failures;
    }
}

#define CHECK(expression) check((expression), #expression, __LINE__)

[[nodiscard]] std::span<const std::byte> bytes(std::string_view text)
{
    return {reinterpret_cast<const std::byte*>(text.data()), text.size()};
}

[[nodiscard]] std::string text(std::span<const std::byte> data)
{
    return {reinterpret_cast<const char*>(data.data()), data.size()};
}

[[nodiscard]] bio::EncryptionKey test_key(std::string id = "kms-key-2026-11")
{
    return bio::EncryptionKey::from_hex(
        std::move(id),
        "00112233445566778899aabbccddeeff"
        "ffeeddccbbaa99887766554433221100");
}

[[nodiscard]] bio::TemplateRegistration registration(
    std::string user = "alice",
    std::string device = "phone-7",
    std::string session = "enroll-1001",
    std::uint64_t generation = 7,
    std::uint64_t issued = 1'000)
{
    return bio::TemplateRegistration{
        std::move(user),
        std::move(device),
        std::move(session),
        bio::Algorithm::fingerprint_v2,
        generation,
        issued};
}

[[nodiscard]] bio::TemplateExpectation expectation(
    std::string user = "alice",
    std::string device = "phone-7",
    std::string session = "enroll-1001",
    std::optional<std::uint64_t> generation = 7)
{
    return bio::TemplateExpectation{
        std::move(user),
        std::move(device),
        std::move(session),
        generation};
}

[[nodiscard]] bio::ValidationPolicy policy()
{
    bio::ValidationPolicy value;
    value.now_unix = 1'100;
    value.maximum_age_seconds = 3'600;
    value.minimum_generation = 7;
    value.minimum_algorithm = bio::Algorithm::fingerprint_v2;
    return value;
}

void test_legitimate_round_trip()
{
    const auto key = test_key();
    const auto sealed = bio::seal_template(
        bytes("alice-fingerprint-template-v2"),
        registration(),
        key);
    CHECK(sealed.status == bio::Status::ok);
    CHECK(static_cast<bool>(sealed));

    const auto imported = bio::import_template(sealed.envelope.encoded());
    CHECK(imported.status == bio::Status::ok);
    CHECK(imported.envelope.size() == sealed.envelope.size());

    auto opened = bio::open_template(imported.envelope, expectation(), policy(), key);
    CHECK(opened.status == bio::Status::ok);
    CHECK(static_cast<bool>(opened));
    CHECK(opened.plaintext.is_locked());
    CHECK(text(opened.plaintext.bytes()) == "alice-fingerprint-template-v2");
    CHECK(opened.metadata.user_id == "alice");
    CHECK(opened.metadata.device_id == "phone-7");
    CHECK(opened.metadata.enrollment_session_id == "enroll-1001");
    CHECK(opened.metadata.algorithm == bio::Algorithm::fingerprint_v2);
    CHECK(opened.metadata.generation == 7);
    CHECK(opened.metadata.key_id == key.id());
}

void test_template_substitution_is_rejected()
{
    const auto key = test_key();
    const auto attacker = bio::seal_template(
        bytes("mallory-fingerprint-template"),
        registration("mallory"),
        key);
    CHECK(attacker.status == bio::Status::ok);

    // The row lookup still uses alice's identifier, but the stored record now
    // contains an otherwise valid envelope registered to mallory.
    const auto opened = bio::open_template(
        attacker.envelope,
        expectation("alice"),
        policy(),
        key);
    CHECK(opened.status == bio::Status::user_mismatch);
    CHECK(!opened);
    CHECK(opened.plaintext.empty());
    CHECK(opened.metadata.user_id.empty());
}

void test_tampering_and_wrong_key_fail_integrity()
{
    const auto key = test_key();
    const auto sealed = bio::seal_template(
        bytes("alice-fingerprint-template-v2"),
        registration(),
        key);
    CHECK(sealed.status == bio::Status::ok);

    auto tampered_bytes = std::vector<std::byte>(
        sealed.envelope.encoded().begin(),
        sealed.envelope.encoded().end());
    // The final 32 bytes are the MAC tag.
    tampered_bytes[tampered_bytes.size() - 33] ^= std::byte{1};

    const auto tampered = bio::import_template(tampered_bytes);
    CHECK(tampered.status == bio::Status::ok);
    const auto opened_tampered = bio::open_template(
        tampered.envelope,
        expectation(),
        policy(),
        key);
    CHECK(opened_tampered.status == bio::Status::integrity_failure);
    CHECK(opened_tampered.plaintext.empty());

    const auto wrong_key = bio::EncryptionKey::from_hex(
        "kms-key-2026-11",
        "11111111111111111111111111111111"
        "11111111111111111111111111111111");
    const auto opened_wrong_key = bio::open_template(
        sealed.envelope,
        expectation(),
        policy(),
        wrong_key);
    CHECK(opened_wrong_key.status == bio::Status::integrity_failure);

    const auto other_key_id = bio::EncryptionKey::from_hex(
        "rotated-key",
        "00112233445566778899aabbccddeeff"
        "ffeeddccbbaa99887766554433221100");
    const auto opened_other_id = bio::open_template(
        sealed.envelope,
        expectation(),
        policy(),
        other_key_id);
    CHECK(opened_other_id.status == bio::Status::key_mismatch);
}

void test_cross_user_device_and_session_are_rejected()
{
    const auto key = test_key();
    const auto sealed = bio::seal_template(
        bytes("alice-fingerprint-template-v2"),
        registration(),
        key);
    CHECK(sealed.status == bio::Status::ok);

    CHECK(bio::open_template(
              sealed.envelope,
              expectation("bob"),
              policy(),
              key)
              .status
          == bio::Status::user_mismatch);
    CHECK(bio::open_template(
              sealed.envelope,
              expectation("alice", "phone-8"),
              policy(),
              key)
              .status
          == bio::Status::device_mismatch);
    CHECK(bio::open_template(
              sealed.envelope,
              expectation("alice", "phone-7", "enroll-9999"),
              policy(),
              key)
              .status
          == bio::Status::enrollment_session_mismatch);
}

void test_old_and_expired_templates_are_rejected()
{
    const auto key = test_key();
    const auto sealed = bio::seal_template(
        bytes("alice-fingerprint-template-v2"),
        registration("alice", "phone-7", "enroll-1001", 7, 1'000),
        key);
    CHECK(sealed.status == bio::Status::ok);

    auto expectation_newer = expectation("alice", "phone-7", "enroll-1001", 8);
    CHECK(bio::open_template(
              sealed.envelope,
              expectation_newer,
              policy(),
              key)
              .status
          == bio::Status::stale_generation);

    auto policy_newer = policy();
    policy_newer.minimum_generation = 8;
    CHECK(bio::open_template(
              sealed.envelope,
              expectation("alice", "phone-7", "enroll-1001", std::nullopt),
              policy_newer,
              key)
              .status
          == bio::Status::stale_generation);

    auto expired_policy = policy();
    expired_policy.now_unix = 1'000 + 3'601;
    CHECK(bio::open_template(
              sealed.envelope,
              expectation(),
              expired_policy,
              key)
              .status
          == bio::Status::template_expired);

    auto future_policy = policy();
    future_policy.now_unix = 900;
    CHECK(bio::open_template(
              sealed.envelope,
              expectation(),
              future_policy,
              key)
              .status
          == bio::Status::template_not_yet_valid);
}

void test_algorithm_downgrade_is_rejected()
{
    const auto key = test_key();
    auto old_registration = registration();
    old_registration.algorithm = bio::Algorithm::fingerprint_v1;
    const auto old_template = bio::seal_template(
        bytes("legacy-template"),
        old_registration,
        key);
    CHECK(old_template.status == bio::Status::ok);
    CHECK(bio::open_template(
              old_template.envelope,
              expectation(),
              policy(),
              key)
              .status
          == bio::Status::algorithm_downgrade);

    const auto current_template = bio::seal_template(
        bytes("current-template"),
        registration(),
        key);
    CHECK(current_template.status == bio::Status::ok);
    auto stronger_policy = policy();
    stronger_policy.expected_algorithm = bio::Algorithm::fingerprint_v3;
    CHECK(bio::open_template(
              current_template.envelope,
              expectation(),
              stronger_policy,
              key)
              .status
          == bio::Status::algorithm_downgrade);

    // An attacker cannot turn a v2 envelope into a v1 envelope without also
    // invalidating the authenticated header.
    auto downgraded_bytes = std::vector<std::byte>(
        current_template.envelope.encoded().begin(),
        current_template.envelope.encoded().end());
    downgraded_bytes[10] = std::byte{0};
    downgraded_bytes[11] = std::byte{1};
    const auto downgraded = bio::import_template(downgraded_bytes);
    CHECK(downgraded.status == bio::Status::ok);
    CHECK(bio::open_template(
              downgraded.envelope,
              expectation(),
              policy(),
              key)
              .status
          == bio::Status::integrity_failure);
}

void test_structural_import_validation()
{
    const auto key = test_key();
    const auto sealed = bio::seal_template(
        bytes("alice-fingerprint-template-v2"),
        registration(),
        key);
    CHECK(sealed.status == bio::Status::ok);

    auto truncated = std::vector<std::byte>(
        sealed.envelope.encoded().begin(),
        sealed.envelope.encoded().end() - 1);
    CHECK(bio::import_template(truncated).status == bio::Status::malformed_template);

    auto trailing = std::vector<std::byte>(
        sealed.envelope.encoded().begin(),
        sealed.envelope.encoded().end());
    trailing.push_back(std::byte{0});
    CHECK(bio::import_template(trailing).status == bio::Status::malformed_template);

    const std::array<std::byte, 5> junk{};
    CHECK(bio::import_template(junk).status == bio::Status::malformed_template);
}

void test_match_and_liveness_joint_decision()
{
    const bio::MatchPolicy policy{0.90, 0.80};

    const auto accepted = bio::decide_match(
        0.97,
        bio::LivenessResult{true, 0.93},
        policy);
    CHECK(accepted.accepted);
    CHECK(accepted.reason == bio::MatchDecisionReason::accepted);

    // A perfect fingerprint score must not resurrect a spoof/replay attempt.
    const auto spoof = bio::decide_match(
        1.0,
        bio::LivenessResult{false, 1.0},
        policy);
    CHECK(!spoof.accepted);
    CHECK(spoof.reason == bio::MatchDecisionReason::liveness_failed);

    const auto weak_liveness = bio::decide_match(
        0.99,
        bio::LivenessResult{true, 0.20},
        policy);
    CHECK(!weak_liveness.accepted);
    CHECK(weak_liveness.reason == bio::MatchDecisionReason::liveness_score_below_threshold);

    const auto weak_match = bio::decide_match(
        0.40,
        bio::LivenessResult{true, 0.99},
        policy);
    CHECK(!weak_match.accepted);
    CHECK(weak_match.reason == bio::MatchDecisionReason::match_score_below_threshold);

    const auto non_finite = bio::decide_match(
        std::numeric_limits<double>::quiet_NaN(),
        bio::LivenessResult{true, 0.99},
        policy);
    CHECK(!non_finite.accepted);
    CHECK(non_finite.reason == bio::MatchDecisionReason::invalid_evidence);

    const auto bool_overload = bio::decide_match(0.95, true, policy);
    CHECK(bool_overload.accepted);
}

void test_invalid_inputs_fail_closed()
{
    const auto key = test_key();
    auto unknown_algorithm = registration();
    unknown_algorithm.algorithm = bio::Algorithm::unknown;
    CHECK(bio::seal_template(bytes("x"), unknown_algorithm, key).status
          == bio::Status::invalid_argument);

    auto empty_user = registration("");
    CHECK(bio::seal_template(bytes("x"), empty_user, key).status
          == bio::Status::invalid_argument);

    const auto sealed = bio::seal_template(bytes("x"), registration(), key);
    CHECK(sealed.status == bio::Status::ok);
    CHECK(bio::open_template(
              sealed.envelope,
              expectation(""),
              policy(),
              key)
              .status
          == bio::Status::invalid_argument);
}

} // namespace

int main()
{
    test_legitimate_round_trip();
    test_template_substitution_is_rejected();
    test_tampering_and_wrong_key_fail_integrity();
    test_cross_user_device_and_session_are_rejected();
    test_old_and_expired_templates_are_rejected();
    test_algorithm_downgrade_is_rejected();
    test_structural_import_validation();
    test_match_and_liveness_joint_decision();
    test_invalid_inputs_fail_closed();

    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "all biometric template tests passed\n";
    return 0;
}
