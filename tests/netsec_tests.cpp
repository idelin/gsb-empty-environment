#include "netsec.hpp"

#include <iostream>
#include <limits>
#include <string>
#include <string_view>

namespace {

constexpr std::string_view kGcsId = "gcs-alpha";
constexpr std::string_view kOperatorId = "operator-7";
constexpr std::string_view kSessionId = "session-1";
constexpr std::string_view kGcsKey = "gcs-alpha-hmac-key-0123456789";
constexpr std::string_view kOperatorKey = "operator-7-confirm-key-0123456789";
constexpr std::string_view kAttackerKey = "attacker-key";

int failures = 0;

void check(bool condition, std::string_view name) {
    if (!condition) {
        ++failures;
        std::cerr << "FAIL: " << name << '\n';
    }
}

netsec::SafetyLimits mission_limits() {
    netsec::SafetyLimits limits;
    limits.geofence = {30.0, 32.0, 120.0, 122.0};
    limits.altitude = {20.0, 100.0};
    return limits;
}

netsec::Command waypoint_command(
    std::string message_id,
    std::uint64_t sequence,
    double latitude = 31.0,
    double longitude = 121.0,
    double altitude = 60.0,
    std::string session_id = std::string(kSessionId),
    std::string operator_id = std::string(kOperatorId)) {
    netsec::Command command;
    command.message_id = std::move(message_id);
    command.type = netsec::CommandType::Waypoint;
    command.source_id = std::string(kGcsId);
    command.session_id = std::move(session_id);
    command.operator_id = std::move(operator_id);
    command.sequence = sequence;
    command.issued_at_ms = 1000;
    command.operator_confirmed = true;
    command.waypoint = {latitude, longitude, altitude};
    return command;
}

netsec::Command mode_command(
    std::string message_id,
    std::uint64_t sequence,
    netsec::FlightMode mode) {
    netsec::Command command;
    command.message_id = std::move(message_id);
    command.type = netsec::CommandType::ModeSwitch;
    command.source_id = std::string(kGcsId);
    command.session_id = std::string(kSessionId);
    command.operator_id = std::string(kOperatorId);
    command.sequence = sequence;
    command.issued_at_ms = 1000;
    command.operator_confirmed = true;
    command.mode = mode;
    return command;
}

void authenticate(
    netsec::Command& command,
    std::string_view gcs_key = kGcsKey,
    std::string_view operator_key = kOperatorKey) {
    netsec::sign_command(command, gcs_key);
    netsec::confirm_command(command, operator_key);
}

struct Fixture {
    explicit Fixture(netsec::SafetyLimits limits = mission_limits())
        : authorizer(std::move(limits)),
          gate(authorizer, [this](const netsec::Command&) { ++executed; }) {
        check(authorizer.register_ground_station(
                  std::string(kGcsId), std::string(kGcsKey)),
              "register ground station");
        check(authorizer.register_operator(
                  std::string(kOperatorId),
                  std::string(kGcsId),
                  std::string(kOperatorKey)),
              "register operator");
        check(authorizer.open_session(
                  netsec::Session {
                      std::string(kSessionId),
                      std::string(kGcsId),
                      std::string(kOperatorId),
                      0,
                  },
                  1000),
              "open session");
    }

    netsec::CommandAuthorizer authorizer;
    int executed = 0;
    netsec::CommandGate gate;
};

void test_hmac_known_vector() {
    check(
        netsec::hmac_sha256_hex(
            "key",
            "The quick brown fox jumps over the lazy dog") ==
            "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8",
        "HMAC-SHA256 known vector");
    check(
        netsec::hmac_sha256_hex(std::string(80, 'k'), "long-key-message") ==
            "3200ddfc6e73af13606332d45a679e80469b2b3d9f064e592c310d53e3e21431",
        "HMAC-SHA256 long-key vector");
}

void test_valid_waypoint_and_sequence() {
    Fixture fixture;

    auto first = waypoint_command("wp-100", 100);
    authenticate(first);
    const auto first_result = fixture.gate.dispatch(first, 1000);
    check(first_result.authorized(), "valid waypoint accepted");
    check(fixture.executed == 1, "valid waypoint executed once");

    auto second = waypoint_command("wp-101", 101, 31.1, 121.1, 70);
    authenticate(second);
    const auto second_result = fixture.gate.dispatch(second, 1000);
    check(second_result.authorized(), "next sequence accepted");
    check(fixture.executed == 2, "next sequence executed once");
}

void test_unsigned_high_priority_injection() {
    Fixture fixture;

    auto injected = waypoint_command("attack-wp", 1, 31.5, 121.5, 60);
    injected.priority = 32767;
    injected.signature.clear();
    injected.operator_confirmation.clear();

    const auto result = fixture.gate.dispatch(injected, 1000);
    check(
        result.reason == netsec::DenialReason::InvalidSignature,
        "unsigned high-priority injection rejected");

    auto injected_land = mode_command("attack-land", 2, netsec::FlightMode::Land);
    injected_land.priority = 32767;
    injected_land.signature = "00";
    injected_land.operator_confirmation.clear();
    const auto land_result = fixture.gate.dispatch(injected_land, 1000);
    check(
        land_result.reason == netsec::DenialReason::InvalidSignature,
        "unsigned high-priority landing rejected");
    check(fixture.executed == 0, "unsigned injection not executed");
}

void test_tampering_after_signature() {
    Fixture fixture;

    auto tampered = waypoint_command("wp-tampered", 1);
    authenticate(tampered);
    tampered.waypoint.latitude_deg = 40.0;

    const auto result = fixture.gate.dispatch(tampered, 1000);
    check(
        result.reason == netsec::DenialReason::InvalidSignature,
        "waypoint tampering rejected");
    check(fixture.executed == 0, "tampered waypoint not executed");
}

void test_source_spoofing() {
    Fixture fixture;

    auto signed_by_attacker = waypoint_command("spoof-1", 1);
    authenticate(signed_by_attacker, kAttackerKey, kOperatorKey);
    const auto wrong_key = fixture.gate.dispatch(signed_by_attacker, 1000);
    check(
        wrong_key.reason == netsec::DenialReason::InvalidSignature,
        "spoofed source with attacker key rejected");

    auto unknown_source = waypoint_command("spoof-2", 2);
    unknown_source.source_id = "gcs-attacker";
    authenticate(unknown_source, kAttackerKey, kOperatorKey);
    const auto unknown = fixture.gate.dispatch(unknown_source, 1000);
    check(
        unknown.reason == netsec::DenialReason::UnknownSource,
        "unregistered source rejected");
    check(fixture.executed == 0, "spoofed sources not executed");
}

void test_replay_and_sequence_rollback() {
    Fixture fixture;

    auto command = waypoint_command("replay-wp", 5);
    authenticate(command);
    check(fixture.gate.dispatch(command, 1000).authorized(), "replay baseline accepted");
    check(fixture.executed == 1, "replay baseline executed");

    const auto replay = fixture.gate.dispatch(command, 1000);
    check(
        replay.reason == netsec::DenialReason::ReplayDetected,
        "exact replay rejected");

    auto rollback = waypoint_command("rollback-wp", 4);
    authenticate(rollback);
    const auto rollback_result = fixture.gate.dispatch(rollback, 1000);
    check(
        rollback_result.reason == netsec::DenialReason::ReplayDetected,
        "sequence rollback rejected");

    auto reused_id = waypoint_command("replay-wp", 6);
    authenticate(reused_id);
    const auto reused_id_result = fixture.gate.dispatch(reused_id, 1000);
    check(
        reused_id_result.reason == netsec::DenialReason::ReplayDetected,
        "reused message identifier rejected");
    check(fixture.executed == 1, "replayed commands not executed");
}

void test_operator_confirmation() {
    Fixture fixture;

    auto missing = waypoint_command("no-confirm", 1);
    missing.operator_confirmed = false;
    authenticate(missing);
    const auto missing_result = fixture.gate.dispatch(missing, 1000);
    check(
        missing_result.reason ==
            netsec::DenialReason::OperatorConfirmationRequired,
        "operator confirmation flag required");

    auto wrong = waypoint_command("wrong-confirm", 2);
    authenticate(wrong, kGcsKey, kAttackerKey);
    const auto wrong_result = fixture.gate.dispatch(wrong, 1000);
    check(
        wrong_result.reason ==
            netsec::DenialReason::InvalidOperatorConfirmation,
        "wrong operator confirmation rejected");
    check(fixture.executed == 0, "unconfirmed commands not executed");
}

void test_session_binding_and_expiry() {
    Fixture fixture;

    auto unknown_session = waypoint_command("unknown-session", 1, 31.0, 121.0, 60, "missing");
    authenticate(unknown_session);
    const auto unknown_result = fixture.gate.dispatch(unknown_session, 1000);
    check(
        unknown_result.reason == netsec::DenialReason::UnknownSession,
        "unknown session rejected");

    auto wrong_operator = waypoint_command(
        "wrong-session-operator",
        2,
        31.0,
        121.0,
        60,
        std::string(kSessionId),
        "operator-8");
    authenticate(wrong_operator);
    const auto mismatch_result = fixture.gate.dispatch(wrong_operator, 1000);
    check(
        mismatch_result.reason ==
            netsec::DenialReason::SessionIdentityMismatch,
        "session operator mismatch rejected");
    check(fixture.executed == 0, "mismatched sessions not executed");

    netsec::CommandAuthorizer expiring_authorizer(mission_limits());
    check(expiring_authorizer.register_ground_station(
              std::string(kGcsId), std::string(kGcsKey)),
          "expiry register ground station");
    check(expiring_authorizer.register_operator(
              std::string(kOperatorId),
              std::string(kGcsId),
              std::string(kOperatorKey)),
          "expiry register operator");
    check(expiring_authorizer.open_session(
              netsec::Session {
                  "short-session",
                  std::string(kGcsId),
                  std::string(kOperatorId),
                  5000,
              },
              1000),
          "open expiring session");

    auto expired = waypoint_command("expired-wp", 1, 31.0, 121.0, 60, "short-session");
    authenticate(expired);
    const auto expired_result = expiring_authorizer.authorize(expired, 5000);
    check(
        expired_result.reason == netsec::DenialReason::SessionExpired,
        "expired session rejected");
}

void test_geofence_and_altitude() {
    Fixture fixture;

    auto outside_latitude = waypoint_command("fence-1", 1, 40.0, 121.0, 60);
    authenticate(outside_latitude);
    const auto fence_result = fixture.gate.dispatch(outside_latitude, 1000);
    check(
        fence_result.reason == netsec::DenialReason::OutsideGeofence,
        "outside geofence rejected");
    const auto fence_replay = fixture.gate.dispatch(outside_latitude, 1000);
    check(
        fence_replay.reason == netsec::DenialReason::ReplayDetected,
        "rejected authenticated fence command consumes sequence");

    auto outside_longitude = waypoint_command("fence-2", 2, 31.0, 130.0, 60);
    authenticate(outside_longitude);
    const auto longitude_result = fixture.gate.dispatch(outside_longitude, 1000);
    check(
        longitude_result.reason == netsec::DenialReason::OutsideGeofence,
        "outside longitude fence rejected");

    auto too_high = waypoint_command("altitude-1", 3, 31.0, 121.0, 101.0);
    authenticate(too_high);
    const auto altitude_result = fixture.gate.dispatch(too_high, 1000);
    check(
        altitude_result.reason == netsec::DenialReason::OutsideAltitudeLimit,
        "outside altitude limit rejected");

    auto non_finite = waypoint_command(
        "nan-wp",
        4,
        std::numeric_limits<double>::quiet_NaN(),
        121.0,
        60.0);
    authenticate(non_finite);
    const auto nan_result = fixture.gate.dispatch(non_finite, 1000);
    check(
        nan_result.reason == netsec::DenialReason::MalformedCommand,
        "non-finite waypoint rejected");
    check(fixture.executed == 0, "unsafe waypoints not executed");
}

void test_mode_switch_authorization() {
    Fixture fixture;

    auto land = mode_command("mode-land", 1, netsec::FlightMode::Land);
    authenticate(land);
    check(fixture.gate.dispatch(land, 1000).authorized(), "confirmed land accepted");
    check(fixture.executed == 1, "confirmed land executed");

    auto unconfirmed = mode_command("mode-rtl", 2, netsec::FlightMode::ReturnToLaunch);
    unconfirmed.operator_confirmed = false;
    authenticate(unconfirmed);
    const auto unconfirmed_result = fixture.gate.dispatch(unconfirmed, 1000);
    check(
        unconfirmed_result.reason ==
            netsec::DenialReason::OperatorConfirmationRequired,
        "unconfirmed mode switch rejected");

    auto invalid_mode = mode_command(
        "mode-invalid",
        3,
        static_cast<netsec::FlightMode>(99));
    authenticate(invalid_mode);
    const auto invalid_result = fixture.gate.dispatch(invalid_mode, 1000);
    check(
        invalid_result.reason == netsec::DenialReason::MalformedCommand,
        "invalid flight mode rejected");
    check(fixture.executed == 1, "unauthorized modes not executed");
}

void test_mode_allowlist_and_independent_safety_validator() {
    auto limits = mission_limits();
    limits.allowed_modes.erase(netsec::FlightMode::Land);
    Fixture fixture(limits);

    auto land = mode_command("mode-land-denied", 1, netsec::FlightMode::Land);
    authenticate(land);
    const auto result = fixture.gate.dispatch(land, 1000);
    check(
        result.reason == netsec::DenialReason::ModeNotPermitted,
        "mode allowlist enforced");

    netsec::SafetyValidator validator(limits);
    auto outside = waypoint_command("safety-only", 2, 40.0, 121.0, 60);
    const auto safety_result = validator.validate(outside);
    check(
        safety_result.reason == netsec::DenialReason::OutsideGeofence,
        "safety validator independently rejects fence violation");
}

void test_priority_is_not_authority() {
    Fixture fixture;

    auto high_priority = waypoint_command("priority-wp", 1);
    high_priority.priority = std::numeric_limits<std::int32_t>::max();
    authenticate(high_priority);

    const auto result = fixture.gate.dispatch(high_priority, 1000);
    check(result.authorized(), "priority does not alter authenticated command");
    check(fixture.executed == 1, "authenticated command executed once");
}

}

int main() {
    test_hmac_known_vector();
    test_valid_waypoint_and_sequence();
    test_unsigned_high_priority_injection();
    test_tampering_after_signature();
    test_source_spoofing();
    test_replay_and_sequence_rollback();
    test_operator_confirmation();
    test_session_binding_and_expiry();
    test_geofence_and_altitude();
    test_mode_switch_authorization();
    test_mode_allowlist_and_independent_safety_validator();
    test_priority_is_not_authority();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }

    std::cout << "all authorization tests passed\n";
    return 0;
}
