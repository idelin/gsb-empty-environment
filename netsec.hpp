#pragma once

#include <cstdint>
#include <functional>
#include <memory>
#include <set>
#include <string>
#include <string_view>

namespace netsec {

using TimestampMs = std::uint64_t;

enum class CommandType {
    Waypoint,
    ModeSwitch,
};

enum class FlightMode {
    Manual,
    Stabilize,
    AltHold,
    Hold,
    Loiter,
    Guided,
    ReturnToLaunch,
    Land,
};

enum class DenialReason {
    None,
    MalformedCommand,
    UnknownSource,
    InvalidSignature,
    UnknownSession,
    SessionExpired,
    SessionIdentityMismatch,
    ReplayDetected,
    OperatorConfirmationRequired,
    InvalidOperatorConfirmation,
    OutsideGeofence,
    OutsideAltitudeLimit,
    ModeNotPermitted,
};

[[nodiscard]] std::string_view to_string(CommandType type) noexcept;
[[nodiscard]] std::string_view to_string(FlightMode mode) noexcept;
[[nodiscard]] std::string_view to_string(DenialReason reason) noexcept;

struct Waypoint {
    double latitude_deg = 0.0;
    double longitude_deg = 0.0;
    double altitude_m = 0.0;
};

struct Command {
    std::string message_id;
    CommandType type = CommandType::Waypoint;
    std::string source_id;
    std::string session_id;
    std::string operator_id;
    std::uint64_t sequence = 0;
    TimestampMs issued_at_ms = 0;
    std::int32_t priority = 0;
    bool operator_confirmed = false;
    Waypoint waypoint;
    FlightMode mode = FlightMode::Hold;
    std::string signature;
    std::string operator_confirmation;
};

struct Geofence {
    double min_latitude_deg = -90.0;
    double max_latitude_deg = 90.0;
    double min_longitude_deg = -180.0;
    double max_longitude_deg = 180.0;
};

struct AltitudeLimits {
    double min_altitude_m = 0.0;
    double max_altitude_m = 120.0;
};

struct SafetyLimits {
    Geofence geofence;
    AltitudeLimits altitude;
    std::set<FlightMode> allowed_modes {
        FlightMode::Manual,
        FlightMode::Stabilize,
        FlightMode::AltHold,
        FlightMode::Hold,
        FlightMode::Loiter,
        FlightMode::Guided,
        FlightMode::ReturnToLaunch,
        FlightMode::Land,
    };
};

struct Session {
    std::string id;
    std::string ground_station_id;
    std::string operator_id;
    TimestampMs expires_at_ms = 0;
};

struct AuthorizationResult {
    DenialReason reason = DenialReason::None;
    std::string detail;

    [[nodiscard]] bool authorized() const noexcept {
        return reason == DenialReason::None;
    }

    [[nodiscard]] bool ok() const noexcept {
        return authorized();
    }

    explicit operator bool() const noexcept {
        return authorized();
    }
};

[[nodiscard]] TimestampMs unix_time_ms() noexcept;

[[nodiscard]] std::string hmac_sha256_hex(
    std::string_view key,
    std::string_view message);

void sign_command(Command& command, std::string_view ground_station_key);
void confirm_command(Command& command, std::string_view operator_key);

class SafetyValidator {
public:
    explicit SafetyValidator(SafetyLimits limits = {});

    [[nodiscard]] AuthorizationResult validate(const Command& command) const;
    [[nodiscard]] const SafetyLimits& limits() const noexcept;
    void set_limits(SafetyLimits limits) noexcept;

private:
    SafetyLimits limits_;
};

class CommandAuthorizer {
public:
    explicit CommandAuthorizer(SafetyLimits limits = {});

    CommandAuthorizer(const CommandAuthorizer&) = delete;
    CommandAuthorizer& operator=(const CommandAuthorizer&) = delete;

    [[nodiscard]] bool register_ground_station(
        std::string ground_station_id,
        std::string signing_key);

    [[nodiscard]] bool register_operator(
        std::string operator_id,
        std::string ground_station_id,
        std::string confirmation_key);

    [[nodiscard]] bool revoke_ground_station(std::string_view ground_station_id);
    [[nodiscard]] bool revoke_operator(std::string_view operator_id);

    [[nodiscard]] bool open_session(
        const Session& session,
        TimestampMs now_ms = unix_time_ms());

    [[nodiscard]] bool close_session(std::string_view session_id);

    [[nodiscard]] AuthorizationResult authorize(
        const Command& command,
        TimestampMs now_ms = unix_time_ms());

    [[nodiscard]] SafetyLimits safety_limits() const;
    void set_safety_limits(SafetyLimits limits);

private:
    struct Impl;
    std::shared_ptr<Impl> impl_;
};

class CommandGate {
public:
    using ExecuteHandler = std::function<void(const Command&)>;

    CommandGate(CommandAuthorizer& authorizer, ExecuteHandler handler);

    [[nodiscard]] AuthorizationResult dispatch(
        const Command& command,
        TimestampMs now_ms = unix_time_ms());

private:
    std::reference_wrapper<CommandAuthorizer> authorizer_;
    ExecuteHandler handler_;
};

}
