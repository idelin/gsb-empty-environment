#include "netsec.hpp"

#include <array>
#include <cmath>
#include <iostream>
#include <span>
#include <string>
#include <utility>
#include <vector>

namespace {

int failures = 0;

void expect(bool condition, const char* message) {
    if (!condition) {
        std::cerr << "FAIL: " << message << '\n';
        ++failures;
    }
}

void expect_near(double actual, double expected, double tolerance,
                 const char* message) {
    expect(std::abs(actual - expected) <= tolerance, message);
}

netsec::AuthenticationKey key(std::uint32_t id) {
    std::array<std::uint8_t, 32> material{};
    for (std::size_t i = 0; i < material.size(); ++i) {
        material[i] = static_cast<std::uint8_t>(id + i * 7U + 0x31U);
    }
    return netsec::make_authentication_key(
        id, std::span<const std::uint8_t>(material.data(), 16));
}

netsec::SignalSpec speed_signal() {
    netsec::SignalSpec signal;
    signal.name = "vehicle_speed";
    signal.conflict_group = "vehicle_speed";
    signal.start_bit = 0;
    signal.bit_length = 16;
    signal.byte_order = netsec::ByteOrder::Intel;
    signal.scale = 0.01;
    signal.minimum = 0.0;
    signal.maximum = 250.0;
    return signal;
}

netsec::SignalSpec steering_signal(std::string name) {
    netsec::SignalSpec signal;
    signal.name = std::move(name);
    signal.conflict_group = "steering_angle";
    signal.start_bit = 0;
    signal.bit_length = 16;
    signal.byte_order = netsec::ByteOrder::Intel;
    signal.is_signed = true;
    signal.scale = 0.1;
    signal.minimum = -540.0;
    signal.maximum = 540.0;
    return signal;
}

netsec::FrameSpec speed_frame() {
    netsec::FrameSpec frame;
    frame.identifier = 0x100;
    frame.sender_id = 0x20;
    frame.expected_period_us = 20000;
    frame.period_tolerance_us = 2000;
    frame.payload_size = 8;
    frame.critical = true;
    frame.authentication_key_id = 1;
    frame.signals = {speed_signal()};
    return frame;
}

netsec::FrameSpec steering_frame() {
    netsec::FrameSpec frame;
    frame.identifier = 0x200;
    frame.sender_id = 0x21;
    frame.expected_period_us = 10000;
    frame.period_tolerance_us = 1000;
    frame.payload_size = 8;
    frame.critical = true;
    frame.authentication_key_id = 2;
    frame.signals = {steering_signal("steering_angle")};
    return frame;
}

netsec::FrameSpec alternate_steering_frame() {
    netsec::FrameSpec frame;
    frame.identifier = 0x201;
    frame.sender_id = 0x22;
    frame.expected_period_us = 10000;
    frame.period_tolerance_us = 1000;
    frame.payload_size = 8;
    frame.critical = true;
    frame.authentication_key_id = 5;
    frame.signals = {steering_signal("steering_angle_alt")};
    return frame;
}

netsec::AuthorizationConfig config() {
    netsec::AuthorizationConfig result;
    result.frames = {
        speed_frame(),
        steering_frame(),
        alternate_steering_frame()
    };
    result.keys = {
        key(1), key(2), key(3), key(4), key(5)
    };
    result.sessions = {
        {netsec::SessionType::Diagnostic, 0x10, 3, 100000},
        {netsec::SessionType::Flashing, 0x20, 4, 50000}
    };
    return result;
}

void write_le16(netsec::Payload& payload, std::uint16_t raw) {
    payload[0] = static_cast<std::uint8_t>(raw);
    payload[1] = static_cast<std::uint8_t>(raw >> 8U);
}

netsec::BusFrame frame(
    std::uint32_t identifier, std::uint16_t sender, std::uint64_t sequence,
    std::int64_t timestamp_us, std::uint16_t raw,
    const netsec::AuthenticationKey& auth_key, bool authenticate = true) {
    netsec::BusFrame value;
    value.identifier = identifier;
    value.sender_id = sender;
    value.sequence = sequence;
    value.timestamp_us = timestamp_us;
    value.size = 8;
    write_le16(value.data, raw);
    value.has_authentication = authenticate;
    value.authentication_key_id = auth_key.id;
    if (authenticate) {
        value.authentication_tag =
            netsec::compute_frame_authentication_tag(auth_key, value);
    }
    return value;
}

netsec::SessionRequest session_request(
    netsec::SessionType type, std::uint16_t principal, std::uint64_t nonce,
    std::int64_t timestamp_us, const netsec::AuthenticationKey& auth_key) {
    netsec::SessionRequest request;
    request.type = type;
    request.principal_id = principal;
    request.nonce = nonce;
    request.timestamp_us = timestamp_us;
    request.authentication_key_id = auth_key.id;
    request.authentication_tag =
        netsec::compute_session_authentication_tag(auth_key, request);
    return request;
}

void test_valid_frames_and_trusted_snapshot() {
    constexpr std::int64_t base = 1000000;
    netsec::SignalAuthorizer authorizer(config());

    const auto speed = authorizer.authorize(
        frame(0x100, 0x20, 1, base, 8000, key(1)));
    expect(speed.authorized, "valid critical speed frame accepted");
    expect(speed.reason == netsec::DecisionReason::None,
           "valid frame has no rejection reason");
    expect(speed.signals.size() == 1, "speed exposes one authorized signal");
    expect_near(speed.signals[0].value, 80.0, 0.001,
                "speed physical value decoded");

    const auto steering = authorizer.authorize(
        frame(0x200, 0x21, 1, base, 1500, key(2)));
    expect(steering.authorized, "valid steering frame accepted");
    expect_near(steering.signals[0].value, 150.0, 0.001,
                "signed steering physical value decoded");

    const auto trusted =
        authorizer.trusted_signal("vehicle_speed", base + 10000);
    expect(trusted.has_value(), "trusted speed snapshot exists");
    expect(trusted->fresh, "trusted speed snapshot is fresh");
    expect_near(trusted->value, 80.0, 0.001,
                "snapshot carries the authorized speed");

    const auto stale =
        authorizer.trusted_signal("vehicle_speed", base + 100000);
    expect(stale.has_value(), "last trusted value remains available");
    expect(!stale->fresh, "old trusted value is marked stale");
}

void test_authentication_required_for_critical_frames() {
    constexpr std::int64_t base = 2000000;
    netsec::SignalAuthorizer authorizer(config());

    auto missing_auth = frame(0x100, 0x20, 1, base, 8000, key(1), false);
    auto result = authorizer.authorize(missing_auth);
    expect(!result.authorized, "critical frame without MAC is rejected");
    expect(result.reason == netsec::DecisionReason::MissingAuthentication,
           "missing MAC reason reported");
    expect(result.degraded && authorizer.degraded(),
           "missing critical MAC degrades authorization");

    auto wrong_key = frame(0x100, 0x20, 1, base, 8000, key(2));
    result = authorizer.authorize(wrong_key);
    expect(!result.authorized, "critical frame signed by wrong key is rejected");
    expect(result.reason == netsec::DecisionReason::AuthenticationFailed,
           "wrong-key MAC reason reported");

    auto tampered = frame(0x100, 0x20, 1, base, 8000, key(1));
    tampered.data[0] ^= 0x01U;
    result = authorizer.authorize(tampered);
    expect(!result.authorized, "payload tampering invalidates the MAC");
    expect(result.reason == netsec::DecisionReason::AuthenticationFailed,
           "tampered payload reason reported");

    result = authorizer.authorize(frame(0x100, 0x20, 1, base, 8000, key(1)));
    expect(result.authorized, "correctly authenticated frame is accepted");
    const auto trusted =
        authorizer.trusted_signal("vehicle_speed", base + 1000);
    expect(trusted.has_value() && trusted->fresh,
           "authenticated value is published");
}

void test_authentication_vectors_match_hmac_sha256() {
    constexpr std::int64_t base = 1000000;
    const netsec::AuthenticationTag expected_frame_tag = {
        0x9d, 0x97, 0x7b, 0xd3, 0x6d, 0x60, 0x15, 0x9b,
        0xd4, 0x74, 0x0f, 0xfe, 0x27, 0x88, 0x2b, 0xfc
    };
    const auto speed = frame(0x100, 0x20, 1, base, 8000, key(1));
    expect(speed.authentication_tag == expected_frame_tag,
           "frame MAC matches an independent HMAC-SHA-256 vector");

    const netsec::AuthenticationTag expected_session_tag = {
        0x8b, 0x80, 0x77, 0x2a, 0xec, 0x53, 0x1a, 0xe0,
        0x95, 0x2b, 0x2c, 0x8e, 0x71, 0x33, 0xf6, 0x8d
    };
    const auto request = session_request(
        netsec::SessionType::Diagnostic, 0x10, 1, 8000000, key(3));
    expect(request.authentication_tag == expected_session_tag,
           "session MAC matches an independent HMAC-SHA-256 vector");
}

void test_injected_sender_cannot_override_speed() {
    constexpr std::int64_t base = 3000000;
    netsec::SignalAuthorizer authorizer(config());

    auto result = authorizer.authorize(
        frame(0x100, 0x20, 1, base, 6000, key(1)));
    expect(result.authorized, "baseline speed accepted");

    result = authorizer.authorize(
        frame(0x100, 0x99, 2, base + 20000, 20000, key(1)));
    expect(!result.authorized, "injected sender is rejected");
    expect(result.reason == netsec::DecisionReason::SenderNotAllowed,
           "sender mismatch reason reported");
    const auto trusted =
        authorizer.trusted_signal("vehicle_speed", base + 20000);
    expect(trusted.has_value() && trusted->value == 60.0,
           "injected frame did not replace trusted speed");

    result = authorizer.authorize(
        frame(0x100, 0x20, 2, base + 20000, 6100, key(1)));
    expect(result.authorized, "legitimate speed after injection is accepted");
    expect(result.degraded, "previous injection leaves system degraded");
}

void test_period_anomalies_are_rejected() {
    constexpr std::int64_t base = 4000000;
    netsec::SignalAuthorizer authorizer(config());

    auto result = authorizer.authorize(
        frame(0x100, 0x20, 1, base, 6000, key(1)));
    expect(result.authorized, "period baseline accepted");
    result = authorizer.authorize(
        frame(0x100, 0x20, 2, base + 20000, 6100, key(1)));
    expect(result.authorized, "expected-period frame accepted");

    result = authorizer.authorize(
        frame(0x100, 0x20, 3, base + 25000, 6200, key(1)));
    expect(!result.authorized, "too-early frame rejected");
    expect(result.reason == netsec::DecisionReason::PeriodTooShort,
           "too-short period reason reported");

    result = authorizer.authorize(
        frame(0x100, 0x20, 3, base + 40000, 6200, key(1)));
    expect(result.authorized,
           "legitimate frame still accepted after an early injection");

    result = authorizer.authorize(
        frame(0x100, 0x20, 4, base + 82000, 6300, key(1)));
    expect(!result.authorized, "too-late frame rejected");
    expect(result.reason == netsec::DecisionReason::PeriodTooLong,
           "too-long period reason reported");

    result = authorizer.authorize(
        frame(0x100, 0x20, 5, base + 30000, 6400, key(1)));
    expect(!result.authorized, "timestamp regression rejected");
    expect(result.reason == netsec::DecisionReason::TimestampRegression,
           "timestamp regression reason reported");
    const auto trusted =
        authorizer.trusted_signal("vehicle_speed", base + 40000);
    expect(trusted.has_value() && trusted->value == 62.0,
           "period attacks do not replace the last trusted value");
}

void test_signal_range_enforced() {
    constexpr std::int64_t base = 5000000;
    netsec::SignalAuthorizer authorizer(config());

    auto result = authorizer.authorize(
        frame(0x100, 0x20, 1, base, 6000, key(1)));
    expect(result.authorized, "in-range baseline accepted");

    result = authorizer.authorize(
        frame(0x100, 0x20, 2, base + 20000, 30000, key(1)));
    expect(!result.authorized, "physically impossible speed rejected");
    expect(result.reason == netsec::DecisionReason::SignalOutOfRange,
           "out-of-range reason reported");
    const auto trusted =
        authorizer.trusted_signal("vehicle_speed", base + 20000);
    expect(trusted.has_value() && trusted->value == 60.0,
           "out-of-range speed did not overwrite trusted value");

    result = authorizer.authorize(
        frame(0x100, 0x20, 2, base + 20000, 7000, key(1)));
    expect(result.authorized,
           "same-sequence slot is accepted after a range reject");

    result = authorizer.authorize(
        frame(0x200, 0x21, 1, base, 6000, key(2)));
    expect(!result.authorized, "out-of-range steering angle rejected");
    expect(result.reason == netsec::DecisionReason::SignalOutOfRange,
           "signed steering range violation reason reported");
}

void test_conflicting_producers_are_rejected() {
    constexpr std::int64_t base = 6000000;
    netsec::SignalAuthorizer authorizer(config());

    auto result = authorizer.authorize(
        frame(0x200, 0x21, 1, base, 1000, key(2)));
    expect(result.authorized, "primary steering producer accepted");

    result = authorizer.authorize(
        frame(0x201, 0x22, 1, base + 5000, 2000, key(5)));
    expect(!result.authorized, "competing steering producer rejected");
    expect(result.reason == netsec::DecisionReason::ConflictingFrame,
           "conflicting producer reason reported");
    const auto trusted =
        authorizer.trusted_signal("steering_angle", base + 5000);
    expect(trusted.has_value() && trusted->value == 100.0,
           "conflicting steering value did not replace trusted value");
}

void test_replayed_authenticated_frame_is_rejected() {
    constexpr std::int64_t base = 7000000;
    netsec::SignalAuthorizer authorizer(config());

    auto original = frame(0x100, 0x20, 7, base, 6000, key(1));
    auto result = authorizer.authorize(original);
    expect(result.authorized, "original authenticated frame accepted");

    result = authorizer.authorize(original);
    expect(!result.authorized, "replayed sequence rejected");
    expect(result.reason == netsec::DecisionReason::SequenceReplay,
           "sequence replay reason reported");
}

void test_diagnostic_and_flashing_sessions_are_separate() {
    constexpr std::int64_t base = 8000000;
    netsec::SignalAuthorizer authorizer(config());

    auto diag_request = session_request(
        netsec::SessionType::Diagnostic, 0x10, 1, base, key(3));
    auto diag = authorizer.authorize_session(diag_request);
    expect(diag.authorized, "authenticated diagnostic session granted");
    expect(authorizer.authorize_service(
               diag.grant, netsec::ServiceClass::Diagnostic, base + 1000).authorized,
           "diagnostic grant authorizes diagnostic service");
    auto denied = authorizer.authorize_service(
        diag.grant, netsec::ServiceClass::Flashing, base + 1000);
    expect(!denied.authorized, "diagnostic grant cannot authorize flashing");
    expect(denied.reason == netsec::DecisionReason::SessionTypeMismatch,
           "capability mismatch reason reported");

    auto flash_with_diag_key = session_request(
        netsec::SessionType::Flashing, 0x20, 1, base, key(3));
    auto bad_flash = authorizer.authorize_session(flash_with_diag_key);
    expect(!bad_flash.authorized,
           "flashing session rejects a diagnostic key");
    expect(bad_flash.reason ==
               netsec::DecisionReason::SessionAuthenticationFailed,
           "flashing key mismatch reason reported");

    auto flash_request = session_request(
        netsec::SessionType::Flashing, 0x20, 1, base, key(4));
    auto flash = authorizer.authorize_session(flash_request);
    expect(flash.authorized, "authenticated flashing session granted");
    expect(authorizer.authorize_service(
               flash.grant, netsec::ServiceClass::Flashing, base + 1000).authorized,
           "flashing grant authorizes flashing service");
    denied = authorizer.authorize_service(
        flash.grant, netsec::ServiceClass::Diagnostic, base + 1000);
    expect(!denied.authorized, "flashing grant cannot authorize diagnostic");

    auto replay = session_request(
        netsec::SessionType::Diagnostic, 0x10, 1, base + 5000, key(3));
    auto replay_result = authorizer.authorize_session(replay);
    expect(!replay_result.authorized, "session nonce replay rejected");
    expect(replay_result.reason == netsec::DecisionReason::SessionReplay,
           "session replay reason reported");

    auto expired = authorizer.authorize_service(
        diag.grant, netsec::ServiceClass::Diagnostic,
        diag.grant.expires_at_us);
    expect(!expired.authorized, "expired session rejected");
    expect(expired.reason == netsec::DecisionReason::SessionExpired,
           "session expiration reason reported");

    auto forged = diag.grant;
    forged.type = netsec::SessionType::Flashing;
    auto forged_result = authorizer.authorize_service(
        forged, netsec::ServiceClass::Flashing, base + 1000);
    expect(!forged_result.authorized, "tampered grant is not trusted");

    expect(authorizer.end_session(diag.grant.grant_id),
           "session can be explicitly ended");
    expect(!authorizer.authorize_service(
               diag.grant, netsec::ServiceClass::Diagnostic, base + 1000).authorized,
           "ended session cannot authorize services");
}

void test_unknown_identifier_is_rejected() {
    constexpr std::int64_t base = 9000000;
    netsec::SignalAuthorizer authorizer(config());

    const auto result = authorizer.authorize(
        frame(0x300, 0x20, 1, base, 6000, key(1)));
    expect(!result.authorized, "unknown identifier rejected");
    expect(result.reason == netsec::DecisionReason::UnknownIdentifier,
           "unknown identifier reason reported");
    expect(authorizer.trusted_signals(base).empty(),
           "rejected identifier publishes no trusted signals");
}

}  // namespace

int main() {
    test_valid_frames_and_trusted_snapshot();
    test_authentication_required_for_critical_frames();
    test_authentication_vectors_match_hmac_sha256();
    test_injected_sender_cannot_override_speed();
    test_period_anomalies_are_rejected();
    test_signal_range_enforced();
    test_conflicting_producers_are_rejected();
    test_replayed_authenticated_frame_is_rejected();
    test_diagnostic_and_flashing_sessions_are_separate();
    test_unknown_identifier_is_rejected();

    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
