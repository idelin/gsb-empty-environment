#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <map>
#include <optional>
#include <shared_mutex>
#include <string>
#include <string_view>

namespace netsec {

// Length of the session identifier in raw (unencoded) bytes. 256 bits make
// guessing the identifier computationally infeasible.
inline constexpr std::size_t kSessionIdBytes = 32;

// Length of the base64url-encoded session identifier (no padding).
inline constexpr std::size_t kSessionIdEncodedLen = (kSessionIdBytes * 8 + 5) / 6;

// How a change of the request source address (the peer address seen on the
// RTSP control connection) is handled. The policy is always explicit; there
// is no implicit "trust whatever address shows up" behaviour.
enum class NatPolicy : std::uint8_t {
    // Reject any request whose source IP does not match the bound address.
    RejectChange = 0,
    // Require the same source IP, but tolerate a different UDP/TCP port
    // (typical UDP NAT rebinding while the public IP stays constant).
    SameIpAllowPortChange,
    // Require the same /24 (IPv4) or /64 (IPv6) network; a different IP
    // inside the same network may rebind to any port.
    SameNetworkAllowRebind,
    // Allow rebinding to any address, but only when the request carries the
    // same authenticated principal that was bound at SETUP time.
    AuthenticatedRebind,
};

enum class TransportMode : std::uint8_t {
    UnicastUdp,
    UnicastTcp, // RTP/AVP/TCP interleaved over the control connection
    Other,
};

// Transport parameters carried by the RTSP Transport header. Only the
// parameters that decide where media is sent are modelled.
struct TransportParams {
    TransportMode mode = TransportMode::UnicastUdp;
    bool unicast = true;
    // IP address from the Transport header "destination"/"client_ip" field.
    // Empty means "use the control-connection peer address".
    std::string mediaIp;
    std::uint16_t clientPortA = 0; // client_port=A[-B], A only
    std::uint16_t clientPortB = 0; // 0 when no second port is present
    bool hasClientPort = false;
    std::uint8_t ttl = 0; // unicast only; 0 when absent
    std::uint8_t interleavedA = 0; // interleaved=A[-B], A only
    std::uint8_t interleavedB = 0;
    bool hasInterleaved = false;

    bool operator==(const TransportParams&) const = default;

    // The host that media packets must be sent to for this transport.
    std::string_view mediaTargetIp(std::string_view peerIp) const {
        return mediaIp.empty() ? peerIp : std::string_view(mediaIp);
    }
};

// Parsed view of where an RTSP request came from.
struct RequestSource {
    std::string peerIp;          // IP seen on the control connection
    std::uint16_t peerPort = 0;  // port seen on the control connection
    // Authenticated principal (e.g. digest username / token subject). Empty
    // means the request is anonymous / unauthenticated.
    std::string principal;
    bool authenticated = false;
};

enum class RejectReason : std::uint8_t {
    None = 0,
    UnknownSession,
    OneTimeSessionReused,
    SourceAddressMismatch,
    PrincipalMismatch,
    UnauthenticatedRebind,
    TransportMismatch,
    InvalidTransportHeader,
    InvalidState,
    InvalidArgument,
};

enum class SessionState : std::uint8_t {
    Init = 0, // bound by SETUP, not yet PLAYing
    Ready,    // paused / play stopped
    Playing,
    TornDown,
};

struct VerifyResult {
    bool ok = false;
    RejectReason reason = RejectReason::None;
    // Monotonic per-session generation, advanced on every accepted state
    // change so stale/duplicated requests cannot reuse old parameters.
    std::uint64_t generation = 0;
};

// Where a media server must actually send packets. Returned only for an
// authorized, Playing session.
struct MediaTarget {
    std::string ip;
    std::uint16_t port = 0;
    std::uint64_t generation = 0;
};

struct SessionInfo {
    std::string id;
    RequestSource source;
    TransportParams transport;
    SessionState state = SessionState::Init;
    std::uint64_t generation = 0;
};

// Parses the value of an RTSP Transport header, e.g.
// "RTP/AVP;unicast;client_port=5000-5001;destination=198.51.100.7".
// Returns std::nullopt when the profile is not RTP/AVP based.
std::optional<TransportParams> parseTransportHeader(std::string_view header);

// Thread-safe registry of RTSP sessions and their security bindings.
//
// Security model:
//  * setup() binds a freshly generated, unpredictable, one-time session id to
//    the request source, principal and transport parameters.
//  * play() re-verifies source (under the explicit NAT policy), principal and
//    every transport field against the stored binding before media starts.
//  * mediaTarget() only hands out a destination for an authorized, Playing
//    session, so a copied session id from another host can never redirect the
//    stream to an attacker address.
class SessionManager {
public:
    explicit SessionManager(NatPolicy policy = NatPolicy::RejectChange);

    // Generates a new one-time session id and stores the binding. The id is
    // cryptographically random and is never issued twice.
    VerifyResult setup(const RequestSource& source,
                       const TransportParams& transport,
                       std::string& issuedSessionIdOut);

    // Convenience overload that parses the Transport header itself.
    VerifyResult setup(const RequestSource& source,
                       std::string_view transportHeader,
                       std::string& issuedSessionIdOut);

    // Verifies a PLAY request against the stored binding and moves the
    // session into the Playing state when accepted.
    VerifyResult play(std::string_view sessionId,
                      const RequestSource& source,
                      const TransportParams& transport);

    VerifyResult play(std::string_view sessionId,
                      const RequestSource& source,
                      std::string_view transportHeader);

    // PAUSE: keeps the binding but stops media delivery.
    VerifyResult pause(std::string_view sessionId, const RequestSource& source);

    // TEARDOWN: removes the session. Its id stays tombstoned forever and can
    // never be re-bound.
    VerifyResult teardown(std::string_view sessionId,
                          const RequestSource& source);

    // Returns the media destination only when the session exists, is Playing,
    // and the caller's source still satisfies the policy. The destination is
    // the address stored at SETUP time, never an attacker-supplied one.
    std::optional<MediaTarget> mediaTarget(std::string_view sessionId,
                                           const RequestSource& source) const;

    std::optional<SessionInfo> lookup(std::string_view sessionId) const;
    bool hasSession(std::string_view sessionId) const;

    NatPolicy policy() const noexcept;
    void setPolicy(NatPolicy policy) noexcept;
    std::size_t activeCount() const;

    // Fills `out` with base64url-encoded CSPRNG bytes; never blocks on a
    // weak fallback. Exposed for testing the id generator.
    static bool generateRandomId(std::string& out);

private:
    struct Session {
        SessionInfo info;
    };

    bool authorizeSourceLocked(const RequestSource& bound,
                               const RequestSource& current) const;

    mutable std::shared_mutex mutex_;
    NatPolicy policy_;
    std::map<std::string, Session, std::less<>> sessions_;
    // Every id ever issued (including torn-down sessions); guarantees that a
    // random id is never handed out twice and that a replayed id is rejected.
    std::map<std::string, std::nullptr_t, std::less<>> tombstones_;
};

// Human-readable description of a rejection reason (diagnostics/logging).
std::string_view explainReject(RejectReason reason) noexcept;

} // namespace netsec
