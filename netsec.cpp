#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <bit>
#include <chrono>
#include <cmath>
#include <cstring>
#include <mutex>
#include <unordered_map>
#include <unordered_set>
#include <utility>

namespace netsec {
namespace {

constexpr std::array<std::uint32_t, 64> kSha256Constants {
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U,
    0x3956c25bU, 0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U,
    0xd807aa98U, 0x12835b01U, 0x243185beU, 0x550c7dc3U,
    0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U, 0xc19bf174U,
    0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
    0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU,
    0x983e5152U, 0xa831c66dU, 0xb00327c8U, 0xbf597fc7U,
    0xc6e00bf3U, 0xd5a79147U, 0x06ca6351U, 0x14292967U,
    0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU, 0x53380d13U,
    0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
    0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U,
    0xd192e819U, 0xd6990624U, 0xf40e3585U, 0x106aa070U,
    0x19a4c116U, 0x1e376c08U, 0x2748774cU, 0x34b0bcb5U,
    0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU, 0x682e6ff3U,
    0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U,
};

constexpr std::array<std::uint32_t, 8> kSha256Initial {
    0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
    0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U,
};

std::uint32_t rotate_right(std::uint32_t value, unsigned int count) noexcept {
    return (value >> count) | (value << (32U - count));
}

void sha256_transform(
    const std::uint8_t* block,
    std::array<std::uint32_t, 8>& state) noexcept {
    std::array<std::uint32_t, 64> words {};

    for (std::size_t i = 0; i < 16; ++i) {
        const std::size_t offset = i * 4;
        words[i] =
            (static_cast<std::uint32_t>(block[offset]) << 24U) |
            (static_cast<std::uint32_t>(block[offset + 1]) << 16U) |
            (static_cast<std::uint32_t>(block[offset + 2]) << 8U) |
            static_cast<std::uint32_t>(block[offset + 3]);
    }

    for (std::size_t i = 16; i < 64; ++i) {
        const std::uint32_t s0 =
            rotate_right(words[i - 15], 7U) ^
            rotate_right(words[i - 15], 18U) ^
            (words[i - 15] >> 3U);
        const std::uint32_t s1 =
            rotate_right(words[i - 2], 17U) ^
            rotate_right(words[i - 2], 19U) ^
            (words[i - 2] >> 10U);
        words[i] = words[i - 16] + s0 + words[i - 7] + s1;
    }

    std::uint32_t a = state[0];
    std::uint32_t b = state[1];
    std::uint32_t c = state[2];
    std::uint32_t d = state[3];
    std::uint32_t e = state[4];
    std::uint32_t f = state[5];
    std::uint32_t g = state[6];
    std::uint32_t h = state[7];

    for (std::size_t i = 0; i < 64; ++i) {
        const std::uint32_t upper_e =
            rotate_right(e, 6U) ^ rotate_right(e, 11U) ^ rotate_right(e, 25U);
        const std::uint32_t choose = (e & f) ^ ((~e) & g);
        const std::uint32_t temp1 = h + upper_e + choose + kSha256Constants[i] + words[i];
        const std::uint32_t upper_a =
            rotate_right(a, 2U) ^ rotate_right(a, 13U) ^ rotate_right(a, 22U);
        const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
        const std::uint32_t temp2 = upper_a + majority;

        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
    state[5] += f;
    state[6] += g;
    state[7] += h;
}

std::array<std::uint8_t, 32> sha256(std::string_view message) noexcept {
    std::array<std::uint32_t, 8> state = kSha256Initial;
    const auto* bytes = reinterpret_cast<const std::uint8_t*>(message.data());
    const std::size_t full_blocks = message.size() / 64;

    for (std::size_t i = 0; i < full_blocks; ++i) {
        sha256_transform(bytes + (i * 64), state);
    }

    std::array<std::uint8_t, 128> padding {};
    const std::size_t remainder = message.size() % 64;
    if (remainder != 0) {
        std::memcpy(padding.data(), bytes + (full_blocks * 64), remainder);
    }

    padding[remainder] = 0x80U;
    const std::size_t padding_blocks = remainder < 56 ? 1 : 2;
    const std::size_t padding_size = padding_blocks * 64;
    const std::uint64_t bit_length =
        static_cast<std::uint64_t>(message.size()) * 8U;

    for (std::size_t i = 0; i < 8; ++i) {
        padding[padding_size - 1 - i] =
            static_cast<std::uint8_t>(bit_length >> (i * 8U));
    }

    for (std::size_t i = 0; i < padding_blocks; ++i) {
        sha256_transform(padding.data() + (i * 64), state);
    }

    std::array<std::uint8_t, 32> digest {};
    for (std::size_t i = 0; i < state.size(); ++i) {
        digest[(i * 4)] = static_cast<std::uint8_t>(state[i] >> 24U);
        digest[(i * 4) + 1] = static_cast<std::uint8_t>(state[i] >> 16U);
        digest[(i * 4) + 2] = static_cast<std::uint8_t>(state[i] >> 8U);
        digest[(i * 4) + 3] = static_cast<std::uint8_t>(state[i]);
    }
    return digest;
}

std::string to_hex(const std::array<std::uint8_t, 32>& digest) {
    constexpr char digits[] = "0123456789abcdef";
    std::string result;
    result.resize(digest.size() * 2);

    for (std::size_t i = 0; i < digest.size(); ++i) {
        result[(i * 2)] = digits[digest[i] >> 4U];
        result[(i * 2) + 1] = digits[digest[i] & 0x0fU];
    }
    return result;
}

bool constant_time_equal(std::string_view left, std::string_view right) noexcept {
    if (left.size() != right.size()) {
        return false;
    }

    unsigned char difference = 0;
    for (std::size_t i = 0; i < left.size(); ++i) {
        difference |= static_cast<unsigned char>(left[i] ^ right[i]);
    }
    return difference == 0;
}

void append_u64(std::string& output, std::uint64_t value) {
    for (int shift = 56; shift >= 0; shift -= 8) {
        output.push_back(static_cast<char>((value >> shift) & 0xffU));
    }
}

void append_string(std::string& output, std::string_view value) {
    append_u64(output, static_cast<std::uint64_t>(value.size()));
    if (!value.empty()) {
        output.append(value.data(), value.size());
    }
}

std::string command_signing_payload(const Command& command) {
    std::string payload;
    append_string(payload, "netsec.command.v1");
    append_string(payload, command.message_id);
    append_u64(payload, static_cast<std::uint64_t>(command.type));
    append_string(payload, command.source_id);
    append_string(payload, command.session_id);
    append_string(payload, command.operator_id);
    append_u64(payload, command.sequence);
    append_u64(payload, command.issued_at_ms);
    append_u64(
        payload,
        std::bit_cast<std::uint64_t>(command.waypoint.latitude_deg));
    append_u64(
        payload,
        std::bit_cast<std::uint64_t>(command.waypoint.longitude_deg));
    append_u64(
        payload,
        std::bit_cast<std::uint64_t>(command.waypoint.altitude_m));
    append_u64(payload, static_cast<std::uint64_t>(command.mode));
    append_u64(payload, command.operator_confirmed ? 1U : 0U);
    return payload;
}

std::string operator_confirmation_payload(const Command& command) {
    std::string payload;
    append_string(payload, "netsec.operator-confirmation.v1");
    append_string(payload, command.signature);
    append_string(payload, command_signing_payload(command));
    return payload;
}

bool is_known_type(CommandType type) noexcept {
    switch (type) {
    case CommandType::Waypoint:
    case CommandType::ModeSwitch:
        return true;
    }
    return false;
}

bool is_known_mode(FlightMode mode) noexcept {
    switch (mode) {
    case FlightMode::Manual:
    case FlightMode::Stabilize:
    case FlightMode::AltHold:
    case FlightMode::Hold:
    case FlightMode::Loiter:
    case FlightMode::Guided:
    case FlightMode::ReturnToLaunch:
    case FlightMode::Land:
        return true;
    }
    return false;
}

AuthorizationResult allow() {
    return {};
}

AuthorizationResult deny(DenialReason reason, std::string detail) {
    return AuthorizationResult {reason, std::move(detail)};
}

bool valid_limits(const SafetyLimits& limits) noexcept {
    const auto& fence = limits.geofence;
    const auto& altitude = limits.altitude;
    return
        std::isfinite(fence.min_latitude_deg) &&
        std::isfinite(fence.max_latitude_deg) &&
        std::isfinite(fence.min_longitude_deg) &&
        std::isfinite(fence.max_longitude_deg) &&
        std::isfinite(altitude.min_altitude_m) &&
        std::isfinite(altitude.max_altitude_m) &&
        fence.min_latitude_deg <= fence.max_latitude_deg &&
        fence.min_longitude_deg <= fence.max_longitude_deg &&
        altitude.min_altitude_m <= altitude.max_altitude_m;
}

}

std::string_view to_string(CommandType type) noexcept {
    switch (type) {
    case CommandType::Waypoint:
        return "waypoint";
    case CommandType::ModeSwitch:
        return "mode-switch";
    }
    return "unknown";
}

std::string_view to_string(FlightMode mode) noexcept {
    switch (mode) {
    case FlightMode::Manual:
        return "manual";
    case FlightMode::Stabilize:
        return "stabilize";
    case FlightMode::AltHold:
        return "alt-hold";
    case FlightMode::Hold:
        return "hold";
    case FlightMode::Loiter:
        return "loiter";
    case FlightMode::Guided:
        return "guided";
    case FlightMode::ReturnToLaunch:
        return "return-to-launch";
    case FlightMode::Land:
        return "land";
    }
    return "unknown";
}

std::string_view to_string(DenialReason reason) noexcept {
    switch (reason) {
    case DenialReason::None:
        return "none";
    case DenialReason::MalformedCommand:
        return "malformed-command";
    case DenialReason::UnknownSource:
        return "unknown-source";
    case DenialReason::InvalidSignature:
        return "invalid-signature";
    case DenialReason::UnknownSession:
        return "unknown-session";
    case DenialReason::SessionExpired:
        return "session-expired";
    case DenialReason::SessionIdentityMismatch:
        return "session-identity-mismatch";
    case DenialReason::ReplayDetected:
        return "replay-detected";
    case DenialReason::OperatorConfirmationRequired:
        return "operator-confirmation-required";
    case DenialReason::InvalidOperatorConfirmation:
        return "invalid-operator-confirmation";
    case DenialReason::OutsideGeofence:
        return "outside-geofence";
    case DenialReason::OutsideAltitudeLimit:
        return "outside-altitude-limit";
    case DenialReason::ModeNotPermitted:
        return "mode-not-permitted";
    }
    return "unknown";
}

TimestampMs unix_time_ms() noexcept {
    const auto now = std::chrono::system_clock::now().time_since_epoch();
    return static_cast<TimestampMs>(
        std::chrono::duration_cast<std::chrono::milliseconds>(now).count());
}

std::string hmac_sha256_hex(
    std::string_view key,
    std::string_view message) {
    std::array<std::uint8_t, 64> key_block {};

    if (key.size() > key_block.size()) {
        const auto hashed_key = sha256(key);
        std::copy(hashed_key.begin(), hashed_key.end(), key_block.begin());
    } else if (!key.empty()) {
        std::memcpy(key_block.data(), key.data(), key.size());
    }

    std::array<std::uint8_t, 64> inner_pad {};
    std::array<std::uint8_t, 64> outer_pad {};
    for (std::size_t i = 0; i < key_block.size(); ++i) {
        inner_pad[i] = static_cast<std::uint8_t>(key_block[i] ^ 0x36U);
        outer_pad[i] = static_cast<std::uint8_t>(key_block[i] ^ 0x5cU);
    }

    std::string inner_message;
    inner_message.reserve(inner_pad.size() + message.size());
    inner_message.append(
        reinterpret_cast<const char*>(inner_pad.data()),
        inner_pad.size());
    if (!message.empty()) {
        inner_message.append(message.data(), message.size());
    }

    const auto inner_digest = sha256(inner_message);
    std::string outer_message;
    outer_message.reserve(outer_pad.size() + inner_digest.size());
    outer_message.append(
        reinterpret_cast<const char*>(outer_pad.data()),
        outer_pad.size());
    outer_message.append(
        reinterpret_cast<const char*>(inner_digest.data()),
        inner_digest.size());

    return to_hex(sha256(outer_message));
}

void sign_command(Command& command, std::string_view ground_station_key) {
    command.signature =
        hmac_sha256_hex(ground_station_key, command_signing_payload(command));
}

void confirm_command(Command& command, std::string_view operator_key) {
    command.operator_confirmation =
        hmac_sha256_hex(operator_key, operator_confirmation_payload(command));
}

SafetyValidator::SafetyValidator(SafetyLimits limits)
    : limits_(std::move(limits)) {}

AuthorizationResult SafetyValidator::validate(const Command& command) const {
    if (!is_known_type(command.type)) {
        return deny(DenialReason::MalformedCommand, "unknown command type");
    }
    if (!valid_limits(limits_)) {
        return deny(DenialReason::MalformedCommand, "invalid safety limits");
    }

    if (command.type == CommandType::Waypoint) {
        const auto& waypoint = command.waypoint;
        if (!std::isfinite(waypoint.latitude_deg) ||
            !std::isfinite(waypoint.longitude_deg) ||
            !std::isfinite(waypoint.altitude_m)) {
            return deny(DenialReason::MalformedCommand, "non-finite waypoint");
        }

        const auto& fence = limits_.geofence;
        if (waypoint.latitude_deg < fence.min_latitude_deg ||
            waypoint.latitude_deg > fence.max_latitude_deg ||
            waypoint.longitude_deg < fence.min_longitude_deg ||
            waypoint.longitude_deg > fence.max_longitude_deg) {
            return deny(
                DenialReason::OutsideGeofence,
                "waypoint coordinates are outside the geofence");
        }

        const auto& altitude = limits_.altitude;
        if (waypoint.altitude_m < altitude.min_altitude_m ||
            waypoint.altitude_m > altitude.max_altitude_m) {
            return deny(
                DenialReason::OutsideAltitudeLimit,
                "waypoint altitude is outside the configured limits");
        }
    } else {
        if (!is_known_mode(command.mode)) {
            return deny(DenialReason::MalformedCommand, "unknown flight mode");
        }
        if (limits_.allowed_modes.find(command.mode) ==
            limits_.allowed_modes.end()) {
            return deny(
                DenialReason::ModeNotPermitted,
                "flight mode is not permitted by the safety policy");
        }
    }

    return allow();
}

const SafetyLimits& SafetyValidator::limits() const noexcept {
    return limits_;
}

void SafetyValidator::set_limits(SafetyLimits limits) noexcept {
    limits_ = std::move(limits);
}

struct CommandAuthorizer::Impl {
    struct StationRecord {
        std::string signing_key;
    };

    struct OperatorRecord {
        std::string ground_station_id;
        std::string confirmation_key;
    };

    struct SessionRecord {
        Session session;
        bool has_sequence = false;
        std::uint64_t last_sequence = 0;
        std::unordered_set<std::string> message_ids;
    };

    explicit Impl(SafetyLimits limits)
        : safety(std::move(limits)) {}

    mutable std::mutex mutex;
    SafetyValidator safety;
    std::unordered_map<std::string, StationRecord> stations;
    std::unordered_map<std::string, OperatorRecord> operators;
    std::unordered_map<std::string, SessionRecord> sessions;
    std::unordered_set<std::string> closed_sessions;
};

CommandAuthorizer::CommandAuthorizer(SafetyLimits limits)
    : impl_(std::make_shared<Impl>(std::move(limits))) {}

bool CommandAuthorizer::register_ground_station(
    std::string ground_station_id,
    std::string signing_key) {
    if (ground_station_id.empty() || signing_key.empty()) {
        return false;
    }

    const std::lock_guard lock(impl_->mutex);
    impl_->stations[std::move(ground_station_id)] =
        Impl::StationRecord {std::move(signing_key)};
    return true;
}

bool CommandAuthorizer::register_operator(
    std::string operator_id,
    std::string ground_station_id,
    std::string confirmation_key) {
    if (operator_id.empty() ||
        ground_station_id.empty() ||
        confirmation_key.empty()) {
        return false;
    }

    const std::lock_guard lock(impl_->mutex);
    if (impl_->stations.find(ground_station_id) == impl_->stations.end()) {
        return false;
    }

    impl_->operators[std::move(operator_id)] = Impl::OperatorRecord {
        std::move(ground_station_id),
        std::move(confirmation_key),
    };
    return true;
}

bool CommandAuthorizer::revoke_ground_station(
    std::string_view ground_station_id) {
    const std::lock_guard lock(impl_->mutex);
    return impl_->stations.erase(std::string(ground_station_id)) != 0;
}

bool CommandAuthorizer::revoke_operator(std::string_view operator_id) {
    const std::lock_guard lock(impl_->mutex);
    return impl_->operators.erase(std::string(operator_id)) != 0;
}

bool CommandAuthorizer::open_session(
    const Session& session,
    TimestampMs now_ms) {
    if (session.id.empty() ||
        session.ground_station_id.empty() ||
        session.operator_id.empty()) {
        return false;
    }
    if (session.expires_at_ms != 0 && now_ms >= session.expires_at_ms) {
        return false;
    }

    const std::lock_guard lock(impl_->mutex);
    if (impl_->sessions.find(session.id) != impl_->sessions.end() ||
        impl_->closed_sessions.find(session.id) != impl_->closed_sessions.end()) {
        return false;
    }
    if (impl_->stations.find(session.ground_station_id) ==
        impl_->stations.end()) {
        return false;
    }

    const auto operator_it = impl_->operators.find(session.operator_id);
    if (operator_it == impl_->operators.end() ||
        operator_it->second.ground_station_id != session.ground_station_id) {
        return false;
    }

    Impl::SessionRecord record;
    record.session = session;
    impl_->sessions.emplace(session.id, std::move(record));
    return true;
}

bool CommandAuthorizer::close_session(std::string_view session_id) {
    const std::lock_guard lock(impl_->mutex);
    const std::string id(session_id);
    const auto session_it = impl_->sessions.find(id);
    if (session_it == impl_->sessions.end()) {
        return false;
    }

    impl_->sessions.erase(session_it);
    impl_->closed_sessions.insert(id);
    return true;
}

AuthorizationResult CommandAuthorizer::authorize(
    const Command& command,
    TimestampMs now_ms) {
    if (command.message_id.empty() ||
        command.source_id.empty() ||
        command.session_id.empty() ||
        command.operator_id.empty()) {
        return deny(
            DenialReason::MalformedCommand,
            "command identity fields must not be empty");
    }
    if (!is_known_type(command.type)) {
        return deny(DenialReason::MalformedCommand, "unknown command type");
    }

    const std::lock_guard lock(impl_->mutex);

    const auto station_it = impl_->stations.find(command.source_id);
    if (station_it == impl_->stations.end()) {
        return deny(DenialReason::UnknownSource, "source is not trusted");
    }

    const auto expected_signature = hmac_sha256_hex(
        station_it->second.signing_key,
        command_signing_payload(command));
    if (!constant_time_equal(expected_signature, command.signature)) {
        return deny(
            DenialReason::InvalidSignature,
            "ground station signature is missing or invalid");
    }

    const auto session_it = impl_->sessions.find(command.session_id);
    if (session_it == impl_->sessions.end()) {
        return deny(
            DenialReason::UnknownSession,
            "session is not active or has already been closed");
    }

    auto& session_record = session_it->second;
    const auto& session = session_record.session;
    if (session.expires_at_ms != 0 && now_ms >= session.expires_at_ms) {
        return deny(DenialReason::SessionExpired, "session has expired");
    }
    if (session.ground_station_id != command.source_id ||
        session.operator_id != command.operator_id) {
        return deny(
            DenialReason::SessionIdentityMismatch,
            "command identities do not match the session");
    }

    const auto operator_it = impl_->operators.find(command.operator_id);
    if (operator_it == impl_->operators.end() ||
        operator_it->second.ground_station_id != command.source_id) {
        return deny(
            DenialReason::SessionIdentityMismatch,
            "operator is not trusted for this ground station");
    }

    if (session_record.message_ids.find(command.message_id) !=
        session_record.message_ids.end()) {
        return deny(
            DenialReason::ReplayDetected,
            "message identifier has already been consumed");
    }
    if (session_record.has_sequence &&
        command.sequence <= session_record.last_sequence) {
        return deny(
            DenialReason::ReplayDetected,
            "sequence is not strictly increasing");
    }

    if (!command.operator_confirmed) {
        return deny(
            DenialReason::OperatorConfirmationRequired,
            "waypoint and mode changes require operator confirmation");
    }

    const auto expected_confirmation = hmac_sha256_hex(
        operator_it->second.confirmation_key,
        operator_confirmation_payload(command));
    if (!constant_time_equal(
            expected_confirmation,
            command.operator_confirmation)) {
        return deny(
            DenialReason::InvalidOperatorConfirmation,
            "operator confirmation is missing or invalid");
    }

    session_record.has_sequence = true;
    session_record.last_sequence = command.sequence;
    session_record.message_ids.insert(command.message_id);

    const auto safety_result = impl_->safety.validate(command);
    if (!safety_result.authorized()) {
        return safety_result;
    }

    return allow();
}

SafetyLimits CommandAuthorizer::safety_limits() const {
    const std::lock_guard lock(impl_->mutex);
    return impl_->safety.limits();
}

void CommandAuthorizer::set_safety_limits(SafetyLimits limits) {
    const std::lock_guard lock(impl_->mutex);
    impl_->safety.set_limits(std::move(limits));
}

CommandGate::CommandGate(
    CommandAuthorizer& authorizer,
    ExecuteHandler handler)
    : authorizer_(authorizer),
      handler_(std::move(handler)) {}

AuthorizationResult CommandGate::dispatch(
    const Command& command,
    TimestampMs now_ms) {
    const auto result = authorizer_.get().authorize(command, now_ms);
    if (result.authorized() && handler_) {
        handler_(command);
    }
    return result;
}

}
