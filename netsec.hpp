#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

inline constexpr std::size_t kMaxPayloadBytes = 64;
inline constexpr std::size_t kAuthenticationTagBytes = 16;
inline constexpr std::uint32_t kMaxCanIdentifier = 0x1FFFFFFF;

using Payload = std::array<std::uint8_t, kMaxPayloadBytes>;
using AuthenticationTag = std::array<std::uint8_t, kAuthenticationTagBytes>;

// A 128-bit truncated HMAC-SHA-256 tag. Key material lives with the caller and
// is copied into the authorizer; keys must not be accepted from bus traffic.
struct AuthenticationKey {
    std::uint32_t id{};
    std::array<std::uint8_t, 32> material{};
    std::uint8_t size{};
};

AuthenticationKey make_authentication_key(
    std::uint32_t id, std::span<const std::uint8_t> material);

enum class ByteOrder : std::uint8_t {
    Intel,     // least-significant bit first, start_bit is the signal LSB
    Motorola   // big-endian DBC numbering, start_bit is the signal MSB
};

// A physical signal decoded from a frame payload. Bounds are inclusive and are
// checked after scale and offset are applied.
struct SignalSpec {
    std::string name;
    std::string conflict_group;
    std::uint16_t start_bit{};
    std::uint8_t bit_length{};
    ByteOrder byte_order{ByteOrder::Intel};
    bool is_signed{};
    double scale{1.0};
    double offset{};
    double minimum{};
    double maximum{};
};

// A frame is identified by its CAN identifier and is only accepted from the
// configured sender. Critical frames must carry a valid authentication tag.
struct FrameSpec {
    std::uint32_t identifier{};
    std::uint16_t sender_id{};
    std::int64_t expected_period_us{};
    std::int64_t period_tolerance_us{};
    std::uint8_t payload_size{};
    bool critical{true};
    std::uint32_t authentication_key_id{};
    // Zero derives a window from the frame period.
    std::int64_t conflict_window_us{};
    std::vector<SignalSpec> signals;
};

struct BusFrame {
    std::uint32_t identifier{};
    std::uint16_t sender_id{};
    // Consumers should increment this for every transmitted frame on a given
    // identifier. Replayed authenticated frames are rejected.
    std::uint64_t sequence{};
    std::int64_t timestamp_us{};
    std::uint8_t size{};
    Payload data{};
    bool has_authentication{};
    std::uint32_t authentication_key_id{};
    AuthenticationTag authentication_tag{};
};

enum class SessionType : std::uint8_t {
    Diagnostic = 1,
    Flashing = 2
};

enum class ServiceClass : std::uint8_t {
    Diagnostic = 1,
    Flashing = 2
};

struct SessionSpec {
    SessionType type{SessionType::Diagnostic};
    std::uint16_t principal_id{};
    std::uint32_t authentication_key_id{};
    std::int64_t lifetime_us{};
};

struct SessionRequest {
    SessionType type{SessionType::Diagnostic};
    std::uint16_t principal_id{};
    // Must increase for each successful request by the same principal and type.
    std::uint64_t nonce{};
    std::int64_t timestamp_us{};
    std::uint32_t authentication_key_id{};
    AuthenticationTag authentication_tag{};
};

struct SessionGrant {
    std::uint64_t grant_id{};
    SessionType type{SessionType::Diagnostic};
    std::uint16_t principal_id{};
    std::int64_t issued_at_us{};
    std::int64_t expires_at_us{};
};

struct AuthorizationConfig {
    std::vector<FrameSpec> frames;
    std::vector<AuthenticationKey> keys;
    std::vector<SessionSpec> sessions;
};

enum class DecisionReason : std::uint8_t {
    None = 0,
    UnknownIdentifier,
    SenderNotAllowed,
    PayloadSizeMismatch,
    InvalidSignalLayout,
    MissingAuthentication,
    AuthenticationFailed,
    SequenceReplay,
    TimestampRegression,
    PeriodTooShort,
    PeriodTooLong,
    SignalOutOfRange,
    SignalDecodeError,
    ConflictingFrame,
    SessionNotProvisioned,
    SessionAuthenticationFailed,
    SessionReplay,
    SessionUnknown,
    SessionExpired,
    SessionTypeMismatch
};

std::string_view to_string(DecisionReason reason) noexcept;

struct AuthorizedSignal {
    std::string name;
    std::string conflict_group;
    std::uint64_t raw_value{};
    double value{};
    bool critical{};
};

struct AuthorizationDecision {
    bool authorized{};
    bool degraded{};
    DecisionReason reason{DecisionReason::None};
    std::vector<AuthorizedSignal> signals;

    explicit operator bool() const noexcept { return authorized; }
};

struct TrustedSignal {
    std::string name;
    double value{};
    std::uint64_t raw_value{};
    std::int64_t timestamp_us{};
    std::uint32_t identifier{};
    std::uint16_t sender_id{};
    bool critical{};
    bool fresh{};
};

struct SessionDecision {
    bool authorized{};
    DecisionReason reason{DecisionReason::SessionNotProvisioned};
    SessionGrant grant;

    explicit operator bool() const noexcept { return authorized; }
};

struct ServiceDecision {
    bool authorized{};
    DecisionReason reason{DecisionReason::SessionUnknown};

    explicit operator bool() const noexcept { return authorized; }
};

// Computes the tag that a trusted producer places in BusFrame. The MAC covers
// the key id, CAN identifier, sender, sequence, timestamp, payload length and
// payload, so changing any routing-relevant field invalidates the frame.
AuthenticationTag compute_frame_authentication_tag(
    const AuthenticationKey& key, const BusFrame& frame) noexcept;

// Computes the tag for a diagnostic or flashing session request. Session type
// and principal are part of the MAC, preventing a grant for one capability
// from being replayed into the other.
AuthenticationTag compute_session_authentication_tag(
    const AuthenticationKey& key, const SessionRequest& request) noexcept;

bool constant_time_equal(
    const AuthenticationTag& lhs, const AuthenticationTag& rhs) noexcept;

// Thread-safe, importable authorization component for a vehicle gateway.
//
// The authorizer keeps the last trusted value of every configured signal.
// Rejected frames are never published. Any rejection sets the degraded state;
// callers should use the last trusted snapshot until an external recovery
// policy clears it.
class SignalAuthorizer {
public:
    explicit SignalAuthorizer(AuthorizationConfig config);
    ~SignalAuthorizer();

    SignalAuthorizer(SignalAuthorizer&&) noexcept;
    SignalAuthorizer& operator=(SignalAuthorizer&&) noexcept;
    SignalAuthorizer(const SignalAuthorizer&) = delete;
    SignalAuthorizer& operator=(const SignalAuthorizer&) = delete;

    AuthorizationDecision authorize(const BusFrame& frame);

    // Returns values that were accepted by authorize(). A value is fresh while
    // it is within its frame's period plus configured tolerance/grace window.
    std::optional<TrustedSignal> trusted_signal(
        std::string_view name, std::int64_t now_us) const;
    std::vector<TrustedSignal> trusted_signals(std::int64_t now_us) const;

    bool degraded() const noexcept;
    void clear_degradation() noexcept;

    SessionDecision authorize_session(const SessionRequest& request);
    ServiceDecision authorize_service(
        const SessionGrant& grant, ServiceClass service, std::int64_t now_us) const;
    bool end_session(std::uint64_t grant_id) noexcept;

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

}  // namespace netsec
