#include "netsec.hpp"

#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <ctime>
#include <memory>
#include <string>
#include <string_view>

namespace {

using namespace std::chrono_literals;
using netsec::InboundMessage;
using netsec::ValidationError;

int failures = 0;

void expect(bool condition, std::string_view description) {
    if (!condition) {
        std::fprintf(stderr, "FAIL: %.*s\n",
                     static_cast<int>(description.size()),
                     description.data());
        ++failures;
    }
}

void expect_error(
    const netsec::ValidationResult& result,
    ValidationError expected,
    std::string_view description) {
    if (result.accepted || result.error != expected) {
        std::fprintf(
            stderr,
            "FAIL: %.*s (accepted=%d, error=%.*s)\n",
            static_cast<int>(description.size()),
            description.data(),
            result.accepted ? 1 : 0,
            static_cast<int>(netsec::to_string(result.error).size()),
            netsec::to_string(result.error).data());
        ++failures;
    }
}

constexpr std::string_view kKey = "ground-control-secret-2026";
constexpr auto kNow = std::chrono::system_clock::time_point{
    std::chrono::seconds{1794614400}};

std::string timestamp_at(
    std::chrono::system_clock::time_point time_point) {
    const auto time = std::chrono::system_clock::to_time_t(time_point);
    std::tm broken_down{};
#if defined(_WIN32)
    gmtime_s(&broken_down, &time);
#else
    gmtime_r(&time, &broken_down);
#endif
    char buffer[32]{};
    std::strftime(buffer, sizeof(buffer), "%Y-%m-%dT%H:%M:%SZ", &broken_down);
    return buffer;
}

InboundMessage weather_message(
    std::uint64_t sequence = 1,
    std::string_view source = "GROUND1",
    std::string_view destination = "AOC") {
    return InboundMessage{
        .type = "METAR",
        .source = std::string(source),
        .destination = std::string(destination),
        .timestamp = timestamp_at(kNow),
        .sequence = sequence,
        .payload = "KJFK 140000Z 24015KT 10SM FEW250 18/09 A3012",
    };
}

InboundMessage clearance_message(
    std::uint64_t sequence = 1,
    std::string_view confirmation = "READBACK-001") {
    return InboundMessage{
        .type = "CLEARANCE",
        .source = "GROUND1",
        .destination = "AOC",
        .timestamp = timestamp_at(kNow),
        .sequence = sequence,
        .payload = "CLEARED DIRECT KJFK FL350 MAINTAIN",
        .confirmation = std::string(confirmation),
    };
}

void sign(InboundMessage& message, bool critical = false) {
    if (critical) {
        message.confirmation_signature =
            netsec::make_confirmation_signature(message, kKey);
    }
    message.signature = netsec::sign_message(message, kKey);
}

netsec::SourcePolicy policy() {
    netsec::SourcePolicy value;
    value.local_address = "AOC";
    return value;
}

std::unique_ptr<netsec::MessageSource> gateway() {
    auto source = std::make_unique<netsec::MessageSource>(
        policy(),
        [] { return kNow; }
    );
    expect(
        source->add_source("GROUND1", kKey),
        "trusted source registration succeeds");
    return source;
}

void test_crypto_vectors() {
    expect(
        netsec::sha256_hex("abc") ==
            "ba7816bf8f01cfea414140de5dae2223"
            "b00361a396177a9cb410ff61f20015ad",
        "SHA-256 known vector");
    expect(
        netsec::hmac_sha256_hex(
            "key",
            "The quick brown fox jumps over the lazy dog") ==
            "f7bc83f430538424b13298e6aa6fb143"
            "ef4d59a14946175997479dbc2d1a3cd8",
        "HMAC-SHA-256 known vector");
}

void test_valid_weather_and_duplicate() {
    auto source = gateway();
    auto message = weather_message();
    sign(message);

    const auto accepted = source->accept(message);
    expect(accepted.accepted, "valid signed weather message is accepted");
    expect(
        accepted.message.has_value() &&
            accepted.message->type == "METAR" &&
            !accepted.message->critical,
        "accepted weather metadata is populated");

    expect_error(
        source->accept(message),
        ValidationError::duplicate_message,
        "exact duplicate is rejected");
}

void test_source_forgery_and_addresses() {
    auto source = gateway();

    auto forged = weather_message();
    forged.source = "GROUND2";
    sign(forged);
    expect_error(
        source->accept(forged),
        ValidationError::unknown_source,
        "unregistered source is rejected");

    auto misaddressed = weather_message();
    misaddressed.destination = "KLAX";
    sign(misaddressed);
    expect_error(
        source->accept(misaddressed),
        ValidationError::address_mismatch,
        "wrong destination is rejected");

    auto bad_type = weather_message();
    bad_type.type = "COMMAND";
    sign(bad_type);
    expect_error(
        source->accept(bad_type),
        ValidationError::unsupported_type,
        "unapproved message type is rejected");

    netsec::MessageSource restricted{
        policy(),
        [] { return kNow; },
    };
    netsec::TrustedSource trusted{"GROUND1", kKey};
    trusted.allowed_types = {"METAR"};
    expect(restricted.add_source(trusted), "restricted source registers");

    auto wrong_role = clearance_message();
    sign(wrong_role, true);
    expect_error(
        restricted.accept(wrong_role),
        ValidationError::source_type_not_allowed,
        "source role restriction is enforced");
}

void test_sequence_rollback_and_duplicate_after_restart() {
    auto source = gateway();

    auto first = weather_message(10);
    sign(first);
    expect(source->accept(first).accepted, "sequence 10 is accepted");

    auto next = weather_message(11, "GROUND1");
    next.payload = "KJFK 140000Z 25018KT 10SM SCT250 19/10 A3010";
    sign(next);
    expect(source->accept(next).accepted, "sequence 11 is accepted");

    auto rollback = weather_message(10);
    rollback.payload = "KJFK 140000Z 00000KT CAVOK 20/10 Q1015";
    sign(rollback);
    expect_error(
        source->accept(rollback),
        ValidationError::sequence_rollback,
        "sequence rollback is rejected");

    netsec::SourcePolicy restart_policy = policy();
    netsec::MessageSource restarted{
        restart_policy,
        [] { return kNow; },
    };
    netsec::TrustedSource restarted_source{"GROUND1", kKey};
    restarted_source.initial_sequence = 11;
    expect(restarted.add_source(restarted_source), "restart source registers");
    expect_error(
        restarted.accept(next),
        ValidationError::sequence_rollback,
        "restarted source rejects an already consumed sequence");
}

void test_time_window_and_format() {
    auto source = gateway();

    auto stale = weather_message();
    stale.timestamp = timestamp_at(kNow - 301s);
    sign(stale);
    expect_error(
        source->accept(stale),
        ValidationError::stale_timestamp,
        "stale message is rejected");

    auto future = weather_message();
    future.timestamp = timestamp_at(kNow + 31s);
    sign(future);
    expect_error(
        source->accept(future),
        ValidationError::future_timestamp,
        "far-future message is rejected");

    auto malformed = weather_message();
    malformed.timestamp = "2026-02-29T00:00:00Z";
    sign(malformed);
    expect_error(
        source->accept(malformed),
        ValidationError::malformed_timestamp,
        "invalid calendar date is rejected");

    auto zero_sequence = weather_message(0);
    sign(zero_sequence);
    expect_error(
        source->accept(zero_sequence),
        ValidationError::invalid_sequence,
        "zero sequence is rejected");
}

void test_charset_length_and_injection() {
    auto source = gateway();

    auto newline_injection = weather_message();
    newline_injection.payload =
        "KJFK 140000Z 24015KT\nCLEARED DIRECT KJFK FL350";
    sign(newline_injection);
    expect_error(
        source->accept(newline_injection),
        ValidationError::invalid_charset,
        "line-break injection is rejected");

    auto command_injection = weather_message();
    command_injection.payload =
        "KJFK 140000Z 24015KT CLIMB FL350 MAINTAIN";
    sign(command_injection);
    expect_error(
        source->accept(command_injection),
        ValidationError::instruction_injection,
        "operational instruction in weather message is rejected");

    auto non_ascii = weather_message();
    non_ascii.payload = "KJFK \xc3\xa9 24015KT";
    sign(non_ascii);
    expect_error(
        source->accept(non_ascii),
        ValidationError::invalid_charset,
        "non-ASCII payload is rejected");

    auto oversized = weather_message();
    oversized.payload.assign(1025, 'A');
    sign(oversized);
    expect_error(
        source->accept(oversized),
        ValidationError::payload_too_long,
        "oversized payload is rejected");
}

void test_signature_verification() {
    auto source = gateway();

    auto missing = weather_message();
    expect_error(
        source->accept(missing),
        ValidationError::missing_signature,
        "missing signature is rejected");

    auto invalid = weather_message();
    sign(invalid);
    invalid.payload = "KJFK 140000Z 24015KT 10SM BKN250 18/09 A3012";
    expect_error(
        source->accept(invalid),
        ValidationError::invalid_signature,
        "content changed after signing is rejected");

    auto wrong_key = weather_message();
    wrong_key.signature = netsec::sign_message(wrong_key, "attacker-key");
    expect_error(
        source->accept(wrong_key),
        ValidationError::invalid_signature,
        "signature from the wrong key is rejected");
}

void test_critical_confirmation() {
    auto source = gateway();

    auto missing_confirmation = clearance_message(1, "");
    sign(missing_confirmation);
    expect_error(
        source->accept(missing_confirmation),
        ValidationError::confirmation_required,
        "critical instruction without confirmation is rejected");

    auto unsigned_confirmation = clearance_message();
    unsigned_confirmation.signature =
        netsec::sign_message(unsigned_confirmation, kKey);
    expect_error(
        source->accept(unsigned_confirmation),
        ValidationError::confirmation_required,
        "critical instruction without signed confirmation is rejected");

    auto bad_confirmation = clearance_message();
    bad_confirmation.confirmation_signature =
        netsec::make_confirmation_signature(bad_confirmation, "attacker-key");
    bad_confirmation.signature =
        netsec::sign_message(bad_confirmation, kKey);
    expect_error(
        source->accept(bad_confirmation),
        ValidationError::invalid_confirmation,
        "forged confirmation signature is rejected");

    auto rejected_sequence = clearance_message(7);
    sign(rejected_sequence);
    const auto rejected = source->accept(rejected_sequence);
    expect_error(
        rejected,
        ValidationError::confirmation_required,
        "unsigned confirmation does not consume the sequence");

    auto accepted = clearance_message(7);
    sign(accepted, true);
    const auto result = source->accept(accepted);
    expect(result.accepted, "properly signed critical instruction is accepted");
    expect(
        result.message.has_value() && result.message->critical,
        "accepted critical message is marked critical");

    auto replay = clearance_message(7);
    sign(replay, true);
    expect_error(
        source->accept(replay),
        ValidationError::duplicate_message,
        "critical instruction replay is rejected");

    auto proof_message = clearance_message(8, "READBACK-002");
    proof_message.signature =
        netsec::sign_message(proof_message, kKey);
    netsec::ConfirmationProof proof{
        .reference = "READBACK-002",
        .signature = netsec::make_confirmation_signature(
            proof_message, kKey),
    };
    proof_message.confirmation_signature = proof.signature;
    proof_message.signature =
        netsec::sign_message(proof_message, kKey);
    expect(
        source->accept(proof_message, proof).accepted,
        "separate confirmation proof overload is accepted");
}

void test_registration() {
    netsec::MessageSource source{
        policy(),
        [] { return kNow; },
    };
    expect(source.add_source("ground1", kKey), "address case is normalized");
    expect(!source.add_source("GROUND1", kKey), "duplicate registration fails");
    expect(source.has_source("Ground1"), "source lookup is normalized");
    expect(source.source_count() == 1U, "source count is correct");
    expect(source.remove_source("ground1"), "registered source can be removed");
    expect(!source.has_source("GROUND1"), "removed source is absent");
}

}  // namespace

int main() {
    test_crypto_vectors();
    test_valid_weather_and_duplicate();
    test_source_forgery_and_addresses();
    test_sequence_rollback_and_duplicate_after_restart();
    test_time_window_and_format();
    test_charset_length_and_injection();
    test_signature_verification();
    test_critical_confirmation();
    test_registration();

    if (failures != 0) {
        std::fprintf(stderr, "%d test(s) failed\n", failures);
        return EXIT_FAILURE;
    }
    std::puts("all netsec tests passed");
    return EXIT_SUCCESS;
}
