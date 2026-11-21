#pragma once

#include "rdpauth/channel.hpp"
#include "rdpauth/crypto.hpp"
#include "rdpauth/types.hpp"

#include <cstdint>
#include <optional>
#include <string>

namespace rdp::auth {

// Sink for security-relevant audit records (weak auth usage, rejections).
class AuditSink {
public:
    virtual ~AuditSink() = default;
    virtual void record(const AuditEvent& event) = 0;
};

// Server-side authentication negotiation engine.
//
// Security invariants enforced by negotiate():
//  - When policy.require_nla is set, any request below CredSSP is rejected
//    (downgrade protection), even if weak auth is otherwise allowed.
//  - Client protocol versions below policy.min_protocol_version are rejected
//    (version rollback protection).
//  - The certificate the client was shown must match the configured server
//    certificate fingerprint (certificate substitution protection).
//  - Weak (legacy) auth is only accepted when policy.allow_weak_auth is
//    explicitly set AND the client sits inside a configured isolated
//    network; every such acceptance is audited.
//  - Every accepted negotiation yields a SessionBinding whose HMAC token
//    binds the server config, certificate, protocol and version.
class Negotiator {
public:
    Negotiator(ServerAuthPolicy policy, Hash256 server_key, AuditSink* sink = nullptr);

    NegotiationDecision negotiate(const NegotiationRequest& request);
    bool verify_binding(const SessionBinding& binding) const;

    // CredSSP tickets are bound to the issuing session binding.
    CredSspTicket issue_credssp_ticket(const SessionBinding& binding,
                                       std::uint64_t ticket_id,
                                       std::uint64_t expires_at) const;
    bool validate_credssp_ticket(const SessionBinding& binding,
                                 const CredSspTicket& ticket,
                                 std::uint64_t now) const;

    // Only an accepted decision with a valid binding yields a channel.
    std::optional<SecureChannel> establish_channel(const NegotiationDecision& decision) const;

    const ServerAuthPolicy& policy() const noexcept { return policy_; }

private:
    Hash256 compute_binding_token(std::uint64_t session_id, AuthProtocol protocol,
                                  std::uint32_t version) const;
    bool client_in_isolated_network(const std::string& ip) const;
    NegotiationDecision reject(RejectReason reason, const NegotiationRequest& request);
    void audit(std::string category, const NegotiationRequest& request, std::string detail);

    ServerAuthPolicy policy_;
    Hash256 server_key_;
    AuditSink* sink_;
    std::uint64_t session_salt_;
    std::uint64_t session_counter_ = 0;
};

} // namespace rdp::auth
