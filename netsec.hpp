#pragma once

#include <array>
#include <compare>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <optional>
#include <ostream>
#include <shared_mutex>
#include <string>
#include <string_view>
#include <unordered_map>

namespace netsec {

using Teid = std::uint32_t;
using TEID = Teid;

using UserId = std::uint64_t;
using SubscriberId = UserId;
using BearerId = std::uint64_t;
using ControlSessionId = std::uint64_t;
using CpSessionId = ControlSessionId;

enum class AddressFamily : std::uint8_t {
    Invalid,
    IPv4,
    IPv6,
};

// A small, dependency-free transport address value.  Integer construction uses
// host byte order, so 0x0a000001 is 10.0.0.1.
class IpAddress {
public:
    IpAddress() noexcept = default;
    IpAddress(std::string_view text);
    IpAddress(const char* text);
    explicit IpAddress(std::uint32_t ipv4_host_order) noexcept;

    static IpAddress parse(std::string_view text);
    static IpAddress from_string(std::string_view text);
    static IpAddress ipv4(std::uint32_t host_order) noexcept;

    [[nodiscard]] bool valid() const noexcept;
    [[nodiscard]] bool is_ipv4() const noexcept;
    [[nodiscard]] bool is_ipv6() const noexcept;
    [[nodiscard]] bool is_unspecified() const noexcept;
    [[nodiscard]] AddressFamily family() const noexcept;
    [[nodiscard]] std::string to_string() const;

    auto operator<=>(const IpAddress&) const = default;

private:
    AddressFamily family_{AddressFamily::Invalid};
    std::array<std::uint8_t, 16> bytes_{};
};

std::ostream& operator<<(std::ostream& out, const IpAddress& address);

enum class Direction {
    Uplink,
    Downlink,
    UL = Uplink,
    DL = Downlink,
    UPLINK = Uplink,
    DOWNLINK = Downlink,
};

inline constexpr Direction Uplink = Direction::Uplink;
inline constexpr Direction Downlink = Direction::Downlink;
inline constexpr Direction UL = Direction::UL;
inline constexpr Direction DL = Direction::DL;

enum class DropReason {
    None,
    InvalidTeid,
    UnknownTeid,
    ConflictTeid,
    AddressMismatch,
    InactiveSession,
    InvalidSession,
};

[[nodiscard]] std::string_view to_string(DropReason reason) noexcept;

struct TunnelEndpoint {
    Teid teid{};
    // peer_address is the primary spelling.  address is accepted as a
    // convenient equivalent for callers that already use that field name.
    IpAddress peer_address{};
    IpAddress address{};
};

// Primary session configuration.  The flat uplink_* / downlink_* members and
// the nested endpoint members are both accepted; when both are populated they
// must agree.
struct SessionConfig {
    UserId user_id{};
    BearerId bearer_id{};
    ControlSessionId control_session_id{};

    Teid uplink_teid{};
    IpAddress uplink_address{};
    Teid downlink_teid{};
    IpAddress downlink_address{};

    TunnelEndpoint uplink{};
    TunnelEndpoint downlink{};

    // Common alternative names used by control-plane session records.
    SubscriberId subscriber_id{};
    ControlSessionId session_id{};
    CpSessionId cp_session_id{};
    UserId ue_id{};
};

using SessionBinding = SessionConfig;
using TunnelBinding = SessionConfig;
using TunnelConfig = SessionConfig;
using TunnelSession = SessionConfig;
using Binding = SessionConfig;

class SessionHandle {
public:
    constexpr SessionHandle() noexcept = default;
    explicit constexpr SessionHandle(std::uint64_t value) noexcept
        : value_(value) {}

    [[nodiscard]] constexpr std::uint64_t value() const noexcept {
        return value_;
    }
    [[nodiscard]] constexpr bool valid() const noexcept { return value_ != 0; }
    [[nodiscard]] explicit constexpr operator bool() const noexcept {
        return valid();
    }

    auto operator<=>(const SessionHandle&) const = default;

private:
    std::uint64_t value_{};
};

struct Route {
    UserId user_id{};
    BearerId bearer_id{};
    ControlSessionId control_session_id{};
    SessionHandle session{};
    Direction direction{Direction::Uplink};
    Teid teid{};
    IpAddress peer_address{};
};

// Forwarding result.  It deliberately exposes the optional-like interface so
// callers can use either `if (result)` or `result->bearer_id`.
struct LookupResult {
    std::optional<Route> route{};
    DropReason reason{DropReason::UnknownTeid};

    [[nodiscard]] bool allowed() const noexcept { return route.has_value(); }
    [[nodiscard]] bool has_value() const noexcept { return route.has_value(); }
    [[nodiscard]] explicit operator bool() const noexcept {
        return route.has_value();
    }
    [[nodiscard]] const Route& value() const { return route.value(); }
    [[nodiscard]] const Route& operator*() const { return route.value(); }
    [[nodiscard]] const Route* operator->() const { return &route.value(); }
};

struct Packet {
    Direction direction{Direction::Uplink};
    Teid teid{};
    IpAddress source_address{};
    IpAddress destination_address{};
    IpAddress peer_address{};
    IpAddress address{};
};

class TunnelMap {
public:
    TunnelMap() = default;
    TunnelMap(const TunnelMap&) = delete;
    TunnelMap& operator=(const TunnelMap&) = delete;
    TunnelMap(TunnelMap&&) = delete;
    TunnelMap& operator=(TunnelMap&&) = delete;
    ~TunnelMap() = default;

    // Registers both directions atomically.  A TEID that is already used in the
    // same direction is quarantined: no packet with that ambiguous TEID is
    // forwarded until the owning session is released.
    [[nodiscard]] std::optional<SessionHandle> create_session(
        const SessionConfig& config);
    [[nodiscard]] std::optional<SessionHandle> establish_session(
        const SessionConfig& config) {
        return create_session(config);
    }
    [[nodiscard]] std::optional<SessionHandle> add_session(
        const SessionConfig& config) {
        return create_session(config);
    }
    [[nodiscard]] std::optional<SessionHandle> register_session(
        const SessionConfig& config) {
        return create_session(config);
    }
    [[nodiscard]] std::optional<SessionHandle> bind_session(
        const SessionConfig& config) {
        return create_session(config);
    }
    [[nodiscard]] std::optional<SessionHandle> add_tunnel(
        const SessionConfig& config) {
        return create_session(config);
    }
    [[nodiscard]] std::optional<SessionHandle> register_tunnel(
        const SessionConfig& config) {
        return create_session(config);
    }
    [[nodiscard]] std::optional<SessionHandle> bind(
        const SessionConfig& config) {
        return create_session(config);
    }

    [[nodiscard]] std::optional<SessionHandle> create_session(
        ControlSessionId control_session_id, UserId user_id, BearerId bearer_id,
        Teid uplink_teid, const IpAddress& uplink_address,
        Teid downlink_teid, const IpAddress& downlink_address);
    [[nodiscard]] std::optional<SessionHandle> establish_session(
        ControlSessionId control_session_id, UserId user_id, BearerId bearer_id,
        Teid uplink_teid, const IpAddress& uplink_address,
        Teid downlink_teid, const IpAddress& downlink_address) {
        return create_session(control_session_id, user_id, bearer_id,
                              uplink_teid, uplink_address, downlink_teid,
                              downlink_address);
    }
    [[nodiscard]] std::optional<SessionHandle> add_session(
        ControlSessionId control_session_id, UserId user_id, BearerId bearer_id,
        Teid uplink_teid, const IpAddress& uplink_address,
        Teid downlink_teid, const IpAddress& downlink_address) {
        return create_session(control_session_id, user_id, bearer_id,
                              uplink_teid, uplink_address, downlink_teid,
                              downlink_address);
    }
    [[nodiscard]] std::optional<SessionHandle> register_session(
        ControlSessionId control_session_id, UserId user_id, BearerId bearer_id,
        Teid uplink_teid, const IpAddress& uplink_address,
        Teid downlink_teid, const IpAddress& downlink_address) {
        return create_session(control_session_id, user_id, bearer_id,
                              uplink_teid, uplink_address, downlink_teid,
                              downlink_address);
    }

    // Releasing through a handle is generation-safe: an old handle cannot
    // remove a later session that reused the same control-session id or TEID.
    bool release_session(const SessionHandle& handle);
    bool release_session(ControlSessionId control_session_id);
    bool remove_session(const SessionHandle& handle) {
        return release_session(handle);
    }
    bool remove_session(ControlSessionId control_session_id) {
        return release_session(control_session_id);
    }
    bool delete_session(const SessionHandle& handle) {
        return release_session(handle);
    }
    bool tear_down_session(const SessionHandle& handle) {
        return release_session(handle);
    }
    bool unbind_session(const SessionHandle& handle) {
        return release_session(handle);
    }
    bool unbind_session(ControlSessionId control_session_id) {
        return release_session(control_session_id);
    }
    bool remove_tunnel(const SessionHandle& handle) {
        return release_session(handle);
    }
    bool remove_tunnel(ControlSessionId control_session_id) {
        return release_session(control_session_id);
    }
    bool unbind(const SessionHandle& handle) { return release_session(handle); }
    bool unbind(ControlSessionId control_session_id) {
        return release_session(control_session_id);
    }

    [[nodiscard]] LookupResult validate(Direction direction, Teid teid,
                                        const IpAddress& peer_address) const;
    [[nodiscard]] LookupResult validate(Teid teid,
                                        const IpAddress& peer_address,
                                        Direction direction) const {
        return validate(direction, teid, peer_address);
    }
    [[nodiscard]] LookupResult validate_uplink(
        Teid teid, const IpAddress& source_address) const {
        return validate(Direction::Uplink, teid, source_address);
    }
    [[nodiscard]] LookupResult validate_downlink(
        Teid teid, const IpAddress& destination_address) const {
        return validate(Direction::Downlink, teid, destination_address);
    }
    [[nodiscard]] LookupResult forward(Direction direction, Teid teid,
                                       const IpAddress& peer_address) const {
        return validate(direction, teid, peer_address);
    }
    [[nodiscard]] LookupResult route(Direction direction, Teid teid,
                                     const IpAddress& peer_address) const {
        return validate(direction, teid, peer_address);
    }
    [[nodiscard]] LookupResult lookup(Direction direction, Teid teid,
                                      const IpAddress& peer_address) const {
        return validate(direction, teid, peer_address);
    }
    [[nodiscard]] LookupResult forward(Teid teid, const IpAddress& peer_address,
                                       Direction direction) const {
        return validate(direction, teid, peer_address);
    }
    [[nodiscard]] LookupResult forward_uplink(
        Teid teid, const IpAddress& source_address) const {
        return validate(Direction::Uplink, teid, source_address);
    }
    [[nodiscard]] LookupResult forward_downlink(
        Teid teid, const IpAddress& destination_address) const {
        return validate(Direction::Downlink, teid, destination_address);
    }
    [[nodiscard]] LookupResult lookup_uplink(
        Teid teid, const IpAddress& source_address) const {
        return validate(Direction::Uplink, teid, source_address);
    }
    [[nodiscard]] LookupResult lookup_downlink(
        Teid teid, const IpAddress& destination_address) const {
        return validate(Direction::Downlink, teid, destination_address);
    }
    [[nodiscard]] LookupResult route_uplink(
        Teid teid, const IpAddress& source_address) const {
        return validate(Direction::Uplink, teid, source_address);
    }
    [[nodiscard]] LookupResult route_downlink(
        Teid teid, const IpAddress& destination_address) const {
        return validate(Direction::Downlink, teid, destination_address);
    }
    [[nodiscard]] LookupResult forward(const Packet& packet) const;
    [[nodiscard]] LookupResult forward_packet(const Packet& packet) const {
        return forward(packet);
    }

    template <class Sink>
    bool dispatch(const Packet& packet, Sink&& sink) const {
        const auto result = forward(packet);
        if (!result) {
            return false;
        }
        std::invoke(std::forward<Sink>(sink), *result);
        return true;
    }

    [[nodiscard]] std::optional<Route> lookup_session(
        const SessionHandle& handle) const;
    [[nodiscard]] std::optional<SessionHandle> find_session(
        ControlSessionId control_session_id) const;
    [[nodiscard]] bool is_active(const SessionHandle& handle) const;
    [[nodiscard]] bool is_teid_active(Direction direction, Teid teid) const;
    [[nodiscard]] bool is_teid_conflicted(Direction direction,
                                          Teid teid) const;
    [[nodiscard]] std::size_t session_count() const;
    void clear();

private:
    enum class SlotState : std::uint8_t {
        Free,
        Active,
        Conflicted,
    };

    struct Slot {
        SlotState state{SlotState::Free};
        SessionHandle owner{};
    };

    struct SessionRecord {
        SessionHandle handle{};
        UserId user_id{};
        BearerId bearer_id{};
        ControlSessionId control_session_id{};
        TunnelEndpoint uplink{};
        TunnelEndpoint downlink{};
    };

    struct ResolvedConfig {
        UserId user_id{};
        BearerId bearer_id{};
        ControlSessionId control_session_id{};
        TunnelEndpoint uplink{};
        TunnelEndpoint downlink{};
        bool valid{};
    };

    [[nodiscard]] static ResolvedConfig resolve_config(
        const SessionConfig& config);
    [[nodiscard]] static const TunnelEndpoint& endpoint_for(
        const SessionRecord& session, Direction direction);
    [[nodiscard]] static DropReason validate_direction(Direction direction);
    [[nodiscard]] static Slot* find_slot(
        std::unordered_map<Teid, Slot>& slots, Teid teid);
    [[nodiscard]] static const Slot* find_slot(
        const std::unordered_map<Teid, Slot>& slots, Teid teid);
    static void mark_conflict(std::unordered_map<Teid, Slot>& slots, Teid teid);
    static void release_slot(std::unordered_map<Teid, Slot>& slots, Teid teid,
                             const SessionHandle& owner);

    mutable std::shared_mutex mutex_;
    std::unordered_map<Teid, Slot> uplink_slots_;
    std::unordered_map<Teid, Slot> downlink_slots_;
    std::unordered_map<std::uint64_t, SessionRecord> sessions_;
    std::unordered_map<ControlSessionId, SessionHandle> sessions_by_control_;
    std::uint64_t next_handle_{1};
};

using TunnelMapping = TunnelMap;
using TunnelMappingTable = TunnelMap;
using TunnelRegistry = TunnelMap;
using UserPlaneTunnelMap = TunnelMap;
using GatewayTunnelMap = TunnelMap;
using TeidMap = TunnelMap;
using TEIDMap = TunnelMap;
using TeidRegistry = TunnelMap;
using TunnelTable = TunnelMap;
using TunnelMapper = TunnelMap;
using TunnelManager = TunnelMap;

}  // namespace netsec

template <>
struct std::hash<netsec::IpAddress> {
    std::size_t operator()(const netsec::IpAddress& address) const noexcept {
        return std::hash<std::string>{}(address.to_string());
    }
};

template <>
struct std::hash<netsec::SessionHandle> {
    std::size_t operator()(const netsec::SessionHandle& handle) const noexcept {
        return std::hash<std::uint64_t>{}(handle.value());
    }
};
