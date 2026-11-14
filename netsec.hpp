#pragma once

#include <array>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

// ---------------------------------------------------------------------------
// Address and endpoint primitives
// ---------------------------------------------------------------------------

enum class IpFamily : std::uint8_t {
    v4 = 4,
    v6 = 6,
};

struct IpAddress {
    IpFamily family{IpFamily::v4};
    std::array<std::uint8_t, 16> bytes{};

    static IpAddress from_v4(std::uint8_t a, std::uint8_t b, std::uint8_t c,
                             std::uint8_t d) noexcept;
    static IpAddress from_v6(const std::array<std::uint8_t, 16>& raw) noexcept;

    // Accepts dotted-quad IPv4, IPv6 with or without brackets. Zone suffixes
    // (for example "fe80::1%en0") are rejected: they are ambiguous in a policy
    // decision and link-local addresses are never valid data targets.
    static std::optional<IpAddress> parse(std::string_view text);

    [[nodiscard]] std::string to_string() const;
    [[nodiscard]] bool is_v4() const noexcept;
    [[nodiscard]] std::optional<std::array<std::uint8_t, 4>> as_v4() const noexcept;

    friend bool operator==(const IpAddress&, const IpAddress&) = default;
};

struct Subnet {
    IpAddress network;
    std::uint8_t prefix_length{0};

    static std::optional<Subnet> parse(std::string_view text);

    [[nodiscard]] bool contains(const IpAddress& address) const noexcept;
    [[nodiscard]] std::string to_string() const;
};

struct Endpoint {
    IpAddress address;
    std::uint16_t port{0};

    // Accepts "a.b.c.d:port" and "[ipv6]:port".
    static std::optional<Endpoint> parse(std::string_view text);

    [[nodiscard]] std::string to_string() const;

    friend bool operator==(const Endpoint&, const Endpoint&) = default;
};

enum class AddressScope : std::uint8_t {
    global,
    unspecified,
    loopback,
    link_local,
    private_network,
    shared_address,
    multicast,
    reserved,
};

// Classifies an address for data-connection purposes. Documentation ranges
// (198.51.100.0/24, 203.0.113.0/24, 2001:db8::/32) are deliberately treated as
// global so that test and lab deployments can use them; they are not internal
// destinations.
[[nodiscard]] AddressScope classify_address(const IpAddress& address) noexcept;
[[nodiscard]] bool is_forbidden_data_target(const IpAddress& address) noexcept;
[[nodiscard]] const char* to_string(AddressScope scope) noexcept;

struct PortRange {
    std::uint16_t min{1024};
    std::uint16_t max{65535};

    [[nodiscard]] bool contains(std::uint16_t port) const noexcept {
        return port >= min && port <= max;
    }
};

// ---------------------------------------------------------------------------
// Policy configuration
// ---------------------------------------------------------------------------

struct PolicyConfig {
    // Every data endpoint, whether advertised by PASV or requested with PORT,
    // must use a port in this range. The default excludes well-known service
    // ports, which also removes the most useful FTP-bounce jump targets.
    PortRange allowed_data_ports{1024, 65535};

    // The client-owned subnet used for the declared-address check. A control
    // connection from 198.51.100.10 with /24 permits only 198.51.100.0/24.
    std::uint8_t ipv4_client_prefix{24};
    std::uint8_t ipv6_client_prefix{64};

    // A data endpoint must be the exact client endpoint, not merely another
    // host on the same subnet. This is the "only back to the client" invariant.
    bool require_exact_client_address{true};

    // Session and grant lifetimes. Both are checked against a monotonic clock;
    // an expired session or grant is denied (fail closed).
    std::chrono::milliseconds session_idle_timeout{30000};
    std::chrono::milliseconds grant_ttl{10000};

    // Bounds used to keep forged sessions/grants from exhausting memory.
    std::size_t max_sessions{4096};
    std::size_t max_pending_grants_per_session{8};

    // A PROXY protocol source is accepted only when the TCP peer that supplied
    // it is one of these trusted proxy networks. Empty means "trust no proxy
    // header", which is the safe default for direct client connections.
    std::vector<Subnet> trusted_proxies;
};

// ---------------------------------------------------------------------------
// Sessions, data commands, decisions
// ---------------------------------------------------------------------------

struct CommandConnection {
    std::uint64_t session_id{0};

    // Remote peer of the command TCP connection as observed by the gateway.
    Endpoint transport_peer;

    // Local gateway endpoint of the command connection. Kept for audit and
    // diagnostics; it never authorizes a data target.
    Endpoint local_endpoint;

    // Original client address asserted by a trusted PROXY protocol header.
    // When set, this becomes the authoritative "actual source" of the client.
    std::optional<Endpoint> proxied_client;
};

enum class DataMode : std::uint8_t {
    pasv,   // PASV: declared address/port, client connects to the gateway
    epsv,   // EPSV: only a port is declared; address is the control peer
    active, // PORT/EPRT: gateway connects to the client-declared endpoint
    proxy,  // data endpoint supplied through a proxy/ALG negotiation
};

struct DataRequest {
    DataMode mode{DataMode::pasv};

    // PASV returns an address in its 227 reply; PORT/EPRT carry one in the
    // command. EPSV has no address and may leave this empty.
    std::optional<IpAddress> declared_address;

    std::uint16_t port{0};
};

enum class DenyReason : std::uint8_t {
    none = 0,
    invalid_input,
    session_unknown,
    session_closed,
    session_expired,
    session_conflict,
    resource_limit,
    proxy_untrusted,
    proxy_invalid,
    address_required,
    address_mismatch,
    address_outside_client_subnet,
    forbidden_address,
    port_not_allowed,
    grant_unknown,
    grant_expired,
    grant_consumed,
    peer_mismatch,
};

[[nodiscard]] const char* to_string(DenyReason reason) noexcept;

struct Decision {
    bool allowed{false};
    DenyReason reason{DenyReason::invalid_input};
    std::string detail;

    static Decision allow(std::string detail = {});
    static Decision deny(DenyReason reason, std::string detail = {});

    explicit operator bool() const noexcept { return allowed; }
};

struct DataConnectionGrant {
    std::uint64_t grant_id{0};
    std::uint64_t session_id{0};
    DataMode mode{DataMode::pasv};

    // The only peer allowed to complete this grant. Port comparison is enabled
    // for active/proxy modes (the client named the port) and disabled for
    // passive modes (the client uses an ephemeral source port).
    Endpoint expected_peer;
    bool compare_peer_port{false};

    // Authorized destination. It is always the client endpoint (or its declared
    // address when exact matching is deliberately disabled).
    Endpoint target;

    std::chrono::steady_clock::time_point expires_at{};
};

struct AuthorizationResult {
    Decision decision;
    std::optional<DataConnectionGrant> grant;

    explicit operator bool() const noexcept { return decision.allowed; }
};

// ---------------------------------------------------------------------------
// The policy component
// ---------------------------------------------------------------------------

class DataConnectionPolicy {
public:
    using TimePoint = std::chrono::steady_clock::time_point;
    using NowFunction = std::function<TimePoint()>;

    explicit DataConnectionPolicy(PolicyConfig config = {},
                                  NowFunction now = {});

    DataConnectionPolicy(const DataConnectionPolicy&) = delete;
    DataConnectionPolicy& operator=(const DataConnectionPolicy&) = delete;

    // Registers the command connection. A duplicate session id is rejected so
    // that one command connection cannot be rebound to another client.
    Decision open_session(const CommandConnection& command);

    // Removes the session and every outstanding grant belonging to it.
    Decision close_session(std::uint64_t session_id);

    [[nodiscard]] bool has_session(std::uint64_t session_id) const;

    // Applies the source-subnet, forbidden-address, port, and session-binding
    // rules, then issues a single-use grant for one data connection.
    AuthorizationResult authorize(std::uint64_t session_id,
                                  const DataRequest& request);

    // Verifies the peer of an established data connection against a grant.
    // A mismatching peer is rejected without consuming the grant.
    Decision confirm_peer(const DataConnectionGrant& grant,
                          const Endpoint& peer);
    Decision confirm_peer(std::uint64_t grant_id, const Endpoint& peer);

    // Removes expired sessions and grants. Safe to call periodically.
    void purge_expired();

    [[nodiscard]] Subnet source_subnet_for(const IpAddress& address) const;
    [[nodiscard]] const PolicyConfig& config() const noexcept { return config_; }

private:
    struct SessionRecord {
        CommandConnection command;
        Endpoint client;
        Subnet client_subnet;
        TimePoint last_activity{};
        bool closed{false};
    };

    struct GrantRecord {
        DataConnectionGrant grant;
        bool consumed{false};
    };

    TimePoint now() const {
        return now_ ? now_() : std::chrono::steady_clock::now();
    }
    bool session_expired(const SessionRecord& record, TimePoint at) const;
    bool grant_expired(const GrantRecord& record, TimePoint at) const;
    std::size_t pending_grants_for(std::uint64_t session_id, TimePoint at);
    void erase_grant(std::uint64_t grant_id);
    void erase_session(std::uint64_t session_id);

    PolicyConfig config_;
    NowFunction now_;
    mutable std::mutex mutex_;
    std::map<std::uint64_t, SessionRecord> sessions_;
    std::map<std::uint64_t, GrantRecord> grants_;
    std::uint64_t next_grant_id_{1};
};

// ---------------------------------------------------------------------------
// FTP and PROXY protocol parsers
// ---------------------------------------------------------------------------

// h1,h2,h3,h4,p1,p2 (PORT)
[[nodiscard]] std::optional<Endpoint> parse_ftp_port_argument(
    std::string_view argument);

// |1|a.b.c.d|port| or |2|ipv6|port| (EPRT)
[[nodiscard]] std::optional<Endpoint> parse_eprt_argument(
    std::string_view argument);

// Extracts the endpoint from a "227 ... (h1,h2,h3,h4,p1,p2)" reply.
[[nodiscard]] std::optional<Endpoint> parse_pasv_response(std::string_view line);

// Extracts the port from a "229 ... (|||port|)" reply.
[[nodiscard]] std::optional<std::uint16_t> parse_epsv_response(
    std::string_view line);

struct ProxyInfo {
    Endpoint source;
    Endpoint destination;
};

// Parses the text PROXY protocol v1 header ("PROXY TCP4 ...\r\n"). Returns
// nullopt for malformed headers and for the LOCAL/UNKNOWN command.
[[nodiscard]] std::optional<ProxyInfo> parse_proxy_v1(std::string_view header);

// Formatting helpers for gateway replies.
[[nodiscard]] std::optional<std::string> format_ftp_port_argument(
    const Endpoint& endpoint);
[[nodiscard]] std::string format_epsv_argument(std::uint16_t port);

}  // namespace netsec
