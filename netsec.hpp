#pragma once

#include <chrono>
#include <compare>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <initializer_list>
#include <mutex>
#include <optional>
#include <set>
#include <span>
#include <string>
#include <string_view>
#include <unordered_map>
#include <vector>

namespace netsec {

using SenderId = std::string;
using FunctionCode = std::uint8_t;

namespace function {
inline constexpr FunctionCode light_on = 0x01;
inline constexpr FunctionCode light_off = 0x02;
inline constexpr FunctionCode light_toggle = 0x03;
inline constexpr FunctionCode dim = 0x04;
inline constexpr FunctionCode valve_open = 0x10;
inline constexpr FunctionCode valve_close = 0x11;
inline constexpr FunctionCode valve_set_position = 0x12;
}  // namespace function

// KNX-style three-level group address: zone(5 bits), device type(3 bits),
// function(8 bits). The function component identifies the controlled function,
// so the command function must match this field.
struct GroupAddress {
    std::uint8_t zone{};
    std::uint8_t device_type{};
    std::uint8_t function{};

    [[nodiscard]] constexpr bool valid() const noexcept {
        return zone <= 31U && device_type <= 7U;
    }

    [[nodiscard]] constexpr std::uint16_t raw() const noexcept {
        return static_cast<std::uint16_t>(
            (static_cast<std::uint16_t>(zone) << 11U) |
            (static_cast<std::uint16_t>(device_type) << 8U) |
            static_cast<std::uint16_t>(function));
    }

    [[nodiscard]] static constexpr GroupAddress from_raw(std::uint16_t raw) noexcept {
        return GroupAddress{
            static_cast<std::uint8_t>((raw >> 11U) & 0x1FU),
            static_cast<std::uint8_t>((raw >> 8U) & 0x07U),
            static_cast<std::uint8_t>(raw & 0xFFU)};
    }

    auto operator<=>(const GroupAddress&) const = default;
};

enum class GroupKind {
    zone,
    broadcast,
    central,
};

struct MemberPolicy {
    std::set<FunctionCode> functions;

    // A membership alone is never enough for a broadcast or central group.
    // These flags are set only by the explicit grant methods.
    bool broadcast_authorized{false};
    bool central_authorized{false};

    auto operator<=>(const MemberPolicy&) const = default;
};

struct GroupPolicy {
    GroupAddress address{};
    GroupKind kind{GroupKind::zone};
    std::unordered_map<SenderId, MemberPolicy> members;
};

struct DeviceRecord {
    SenderId id;
    std::uint8_t zone{};
    std::uint8_t device_type{};

    auto operator<=>(const DeviceRecord&) const = default;
};

class GroupRegistry {
public:
    // Registration is intentionally fail-closed: duplicates are rejected and
    // do not silently replace an existing policy.
    bool register_device(DeviceRecord device);
    bool register_device(SenderId id, std::uint8_t zone, std::uint8_t device_type);
    bool register_group(GroupPolicy policy);
    bool register_group(GroupAddress address, GroupKind kind = GroupKind::zone);

    // Adds ordinary membership. For a zone group the sender must have the
    // same zone and device type as the group address.
    bool add_member(const GroupAddress& group,
                    const SenderId& sender,
                    std::set<FunctionCode> functions = {});

    // Explicit authorization for the two special group classes. These calls
    // also create the membership entry.
    bool grant_broadcast(const GroupAddress& group,
                         const SenderId& sender,
                         std::set<FunctionCode> functions);
    bool grant_central(const GroupAddress& group,
                       const SenderId& sender,
                       std::set<FunctionCode> functions);

    [[nodiscard]] bool has_device(const SenderId& sender) const;
    [[nodiscard]] bool has_group(const GroupAddress& group) const;
    [[nodiscard]] std::optional<DeviceRecord> device(const SenderId& sender) const;
    [[nodiscard]] std::optional<GroupPolicy> group(const GroupAddress& group) const;

private:
    friend class GroupAuthorizer;

    [[nodiscard]] const DeviceRecord* find_device(const SenderId& sender) const noexcept;
    [[nodiscard]] const GroupPolicy* find_group(const GroupAddress& group) const noexcept;

    std::unordered_map<SenderId, DeviceRecord> devices_;
    std::unordered_map<std::uint16_t, GroupPolicy> groups_;
};

struct IncomingCommand {
    // Untrusted source field from the bus telegram.
    SenderId claimed_source;
    GroupAddress target{};
    FunctionCode function{};
    std::uint64_t sequence{};
    std::string payload;
};

struct TrustedCommand {
    IncomingCommand command;
    // Identity established by the gateway's transport authentication layer.
    // It must never be copied from the telegram's claimed source field.
    SenderId authenticated_source;
};

enum class AuthorizationCode {
    allow,
    invalid_command,
    invalid_address,
    function_mismatch,
    source_mismatch,
    unknown_sender,
    unknown_group,
    cross_zone,
    sender_not_member,
    function_not_permitted,
    broadcast_not_authorized,
    central_not_authorized,
    duplicate,
    rate_limited,
};

struct AuthorizationResult {
    AuthorizationCode code{AuthorizationCode::invalid_command};
    GroupAddress target{};

    [[nodiscard]] constexpr bool allowed() const noexcept {
        return code == AuthorizationCode::allow;
    }

    [[nodiscard]] constexpr explicit operator bool() const noexcept {
        return allowed();
    }

    [[nodiscard]] std::string_view reason() const noexcept;
};

struct RoutingDecision {
    // Only these groups may receive the command. A denied target is never
    // placed in this list.
    std::vector<GroupAddress> authorized_targets;
    std::vector<AuthorizationResult> results;
};

struct RateLimitConfig {
    std::size_t burst{8};
    double refill_per_second{4.0};
    std::chrono::milliseconds duplicate_window{750};
    std::chrono::seconds replay_window{30};
    std::size_t max_replay_entries{16384};
};

// Thread-safe authorization component for a bus gateway. All registration and
// authorization calls may be made concurrently.
class GroupAuthorizer {
public:
    using Clock = std::chrono::steady_clock;
    using TimeSource = std::function<Clock::time_point()>;

    GroupAuthorizer();
    explicit GroupAuthorizer(GroupRegistry registry,
                             RateLimitConfig config = {},
                             TimeSource time_source = {});
    explicit GroupAuthorizer(RateLimitConfig config, TimeSource time_source = {});

    GroupAuthorizer(const GroupAuthorizer&) = delete;
    GroupAuthorizer& operator=(const GroupAuthorizer&) = delete;
    GroupAuthorizer(GroupAuthorizer&&) = delete;
    GroupAuthorizer& operator=(GroupAuthorizer&&) = delete;

    bool register_device(DeviceRecord device);
    bool register_device(SenderId id, std::uint8_t zone, std::uint8_t device_type);
    bool register_group(GroupPolicy policy);
    bool register_group(GroupAddress address, GroupKind kind = GroupKind::zone);
    bool add_member(const GroupAddress& group,
                    const SenderId& sender,
                    std::set<FunctionCode> functions = {});
    bool grant_broadcast(const GroupAddress& group,
                         const SenderId& sender,
                         std::set<FunctionCode> functions);
    bool grant_central(const GroupAddress& group,
                       const SenderId& sender,
                       std::set<FunctionCode> functions);

    [[nodiscard]] bool has_device(const SenderId& sender) const;
    [[nodiscard]] bool has_group(const GroupAddress& group) const;
    [[nodiscard]] std::optional<DeviceRecord> device(const SenderId& sender) const;
    [[nodiscard]] std::optional<GroupPolicy> group(const GroupAddress& group) const;

    // The authenticated_source must come from the gateway's authentication
    // layer, not from the telegram. Mismatching claimed/authenticated sources
    // are rejected before any group lookup.
    [[nodiscard]] AuthorizationResult authorize(
        const IncomingCommand& command,
        const SenderId& authenticated_source);

    [[nodiscard]] AuthorizationResult authorize(const TrustedCommand& command);

    // Routes one command to a set of requested groups and returns only the
    // groups for which a positive authorization decision was made.
    [[nodiscard]] RoutingDecision authorize_many(
        const IncomingCommand& command,
        const SenderId& authenticated_source,
        std::span<const GroupAddress> requested_targets);

private:
    struct Bucket {
        double tokens{};
        Clock::time_point last{};
    };

    [[nodiscard]] AuthorizationResult authorize_locked(
        const IncomingCommand& command,
        const SenderId& authenticated_source,
        Clock::time_point now);
    [[nodiscard]] bool is_duplicate_locked(const IncomingCommand& command,
                                           Clock::time_point now) const;
    void record_command_locked(const IncomingCommand& command,
                               Clock::time_point now);
    void prune_replay_locked(Clock::time_point now);

    [[nodiscard]] Clock::time_point now() const;

    mutable std::mutex mutex_;
    GroupRegistry registry_;
    RateLimitConfig config_;
    TimeSource time_source_;
    std::unordered_map<std::string, Bucket> buckets_;
    std::unordered_map<std::string, Clock::time_point> sequence_replay_;
    std::unordered_map<std::string, Clock::time_point> content_replay_;
    std::size_t cleanup_tick_{0};
};

}  // namespace netsec
