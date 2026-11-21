#pragma once

#include <array>
#include <cstdint>
#include <string>
#include <vector>

namespace rdp::auth {

// Highest RDP protocol version this component negotiates.
inline constexpr std::uint32_t kProtocolVersionCurrent = 0x0008000D;

// Authentication protocols in ascending order of strength.
enum class AuthProtocol : std::uint8_t {
    legacy_rdp = 0, // Standard RDP Security (weak, legacy)
    tls = 1,        // Enhanced RDP Security (TLS only)
    credssp = 2,    // Network Level Authentication (CredSSP)
};

enum class RejectReason : std::uint8_t {
    none = 0,
    nla_required_downgrade,         // NLA mandated by policy, client asked for less
    weak_auth_not_allowed,          // weak auth not explicitly enabled by policy
    client_not_in_isolated_network, // weak auth only allowed from isolated networks
    protocol_version_rollback,      // client offered a version below the policy floor
    certificate_mismatch,           // presented certificate is not the configured one
};

using Hash256 = std::array<std::uint8_t, 32>;

struct ServerAuthPolicy {
    bool require_nla = true;       // mandate CredSSP; reject any downgrade
    bool allow_weak_auth = false;  // explicit opt-in for legacy auth, isolated nets only
    std::vector<std::string> isolated_networks; // CIDR ranges, e.g. "10.0.0.0/8"
    std::uint32_t min_protocol_version = kProtocolVersionCurrent;
    Hash256 server_cert_fingerprint{}; // SHA-256 of the configured server certificate
    std::string config_id;             // identity of the server configuration
    Hash256 config_hash{};             // SHA-256 of the full server configuration
};

struct NegotiationRequest {
    AuthProtocol requested_protocol = AuthProtocol::credssp;
    std::uint32_t client_protocol_version = kProtocolVersionCurrent;
    Hash256 presented_cert_fingerprint{}; // certificate the client was actually shown
    std::string client_ip;
};

// Binds an accepted negotiation to the server config, certificate and
// protocol version via an HMAC token keyed by the server secret.
struct SessionBinding {
    std::uint64_t session_id = 0;
    AuthProtocol protocol = AuthProtocol::credssp;
    std::uint32_t protocol_version = 0;
    Hash256 token{};
};

struct NegotiationDecision {
    bool accepted = false;
    RejectReason reason = RejectReason::none;
    SessionBinding binding{};
};

// CredSSP service ticket, cryptographically bound to one session.
struct CredSspTicket {
    std::uint64_t ticket_id = 0;
    std::uint64_t session_id = 0;
    std::uint64_t expires_at = 0;
    Hash256 mac{};
};

struct AuditEvent {
    std::string category; // e.g. "weak_auth_permitted", "negotiation_rejected"
    std::string client_ip;
    AuthProtocol protocol = AuthProtocol::legacy_rdp;
    std::string detail;
    std::uint64_t unix_time = 0;
};

const char* to_string(RejectReason reason) noexcept;
const char* to_string(AuthProtocol protocol) noexcept;

} // namespace rdp::auth
