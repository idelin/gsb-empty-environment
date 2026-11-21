#include "rdpauth/negotiator.hpp"

#include <chrono>
#include <optional>
#include <random>
#include <string_view>
#include <vector>

namespace rdp::auth {
namespace {

void append_u32(std::vector<std::uint8_t>& out, std::uint32_t value) {
    for (int i = 3; i >= 0; --i) {
        out.push_back(static_cast<std::uint8_t>(value >> (i * 8)));
    }
}

void append_u64(std::vector<std::uint8_t>& out, std::uint64_t value) {
    for (int i = 7; i >= 0; --i) {
        out.push_back(static_cast<std::uint8_t>(value >> (i * 8)));
    }
}

std::optional<std::uint32_t> parse_ipv4(std::string_view ip) {
    std::uint32_t result = 0;
    int octets = 0;
    std::size_t pos = 0;
    while (pos <= ip.size() && octets < 4) {
        const std::size_t dot = ip.find('.', pos);
        const std::string_view part =
            ip.substr(pos, dot == std::string_view::npos ? dot : dot - pos);
        if (part.empty() || part.size() > 3) {
            return std::nullopt;
        }
        std::uint32_t value = 0;
        for (char c : part) {
            if (c < '0' || c > '9') {
                return std::nullopt;
            }
            value = value * 10 + static_cast<std::uint32_t>(c - '0');
        }
        if (value > 255) {
            return std::nullopt;
        }
        result = (result << 8) | value;
        ++octets;
        if (dot == std::string_view::npos) {
            break;
        }
        pos = dot + 1;
    }
    if (octets != 4) {
        return std::nullopt;
    }
    return result;
}

bool ip_in_cidr(std::uint32_t ip, std::string_view cidr) {
    const std::size_t slash = cidr.find('/');
    if (slash == std::string_view::npos) {
        return false;
    }
    const auto network = parse_ipv4(cidr.substr(0, slash));
    if (!network) {
        return false;
    }
    int prefix = 0;
    for (char c : cidr.substr(slash + 1)) {
        if (c < '0' || c > '9') {
            return false;
        }
        prefix = prefix * 10 + (c - '0');
    }
    if (prefix < 0 || prefix > 32) {
        return false;
    }
    const std::uint32_t mask = prefix == 0 ? 0 : (0xFFFFFFFFu << (32 - prefix));
    return (ip & mask) == (*network & mask);
}

std::uint64_t now_unix() {
    return static_cast<std::uint64_t>(
        std::chrono::duration_cast<std::chrono::seconds>(
            std::chrono::system_clock::now().time_since_epoch())
            .count());
}

} // namespace

Negotiator::Negotiator(ServerAuthPolicy policy, Hash256 server_key, AuditSink* sink)
    : policy_(std::move(policy)), server_key_(server_key), sink_(sink) {
    std::random_device rd;
    session_salt_ = (static_cast<std::uint64_t>(rd()) << 32) ^ rd();
}

Hash256 Negotiator::compute_binding_token(std::uint64_t session_id, AuthProtocol protocol,
                                          std::uint32_t version) const {
    std::vector<std::uint8_t> msg;
    const std::string label = "rdp-auth/session-binding/v1";
    msg.insert(msg.end(), label.begin(), label.end());
    msg.insert(msg.end(), policy_.config_id.begin(), policy_.config_id.end());
    msg.insert(msg.end(), policy_.config_hash.begin(), policy_.config_hash.end());
    msg.insert(msg.end(), policy_.server_cert_fingerprint.begin(),
               policy_.server_cert_fingerprint.end());
    msg.push_back(static_cast<std::uint8_t>(protocol));
    append_u32(msg, version);
    append_u64(msg, session_id);
    return hmac_sha256(server_key_, msg);
}

bool Negotiator::client_in_isolated_network(const std::string& ip) const {
    const auto addr = parse_ipv4(ip);
    if (!addr) {
        return false;
    }
    for (const std::string& cidr : policy_.isolated_networks) {
        if (ip_in_cidr(*addr, cidr)) {
            return true;
        }
    }
    return false;
}

void Negotiator::audit(std::string category, const NegotiationRequest& request,
                       std::string detail) {
    if (!sink_) {
        return;
    }
    AuditEvent event;
    event.category = std::move(category);
    event.client_ip = request.client_ip;
    event.protocol = request.requested_protocol;
    event.detail = std::move(detail);
    event.unix_time = now_unix();
    sink_->record(event);
}

NegotiationDecision Negotiator::reject(RejectReason reason, const NegotiationRequest& request) {
    audit("negotiation_rejected", request, to_string(reason));
    NegotiationDecision decision;
    decision.accepted = false;
    decision.reason = reason;
    return decision;
}

NegotiationDecision Negotiator::negotiate(const NegotiationRequest& request) {
    // Version rollback protection.
    if (request.client_protocol_version < policy_.min_protocol_version) {
        return reject(RejectReason::protocol_version_rollback, request);
    }

    // Certificate substitution protection: the certificate the client was
    // shown must be the configured server certificate.
    if (!constant_time_equal(request.presented_cert_fingerprint,
                             policy_.server_cert_fingerprint)) {
        return reject(RejectReason::certificate_mismatch, request);
    }

    // Downgrade protection: NLA policy admits CredSSP only. Weak auth never
    // overrides a mandatory NLA policy.
    if (request.requested_protocol < AuthProtocol::credssp && policy_.require_nla) {
        return reject(RejectReason::nla_required_downgrade, request);
    }

    // Weak (legacy) auth: explicit policy opt-in AND isolated network only.
    if (request.requested_protocol == AuthProtocol::legacy_rdp) {
        if (!policy_.allow_weak_auth) {
            return reject(RejectReason::weak_auth_not_allowed, request);
        }
        if (!client_in_isolated_network(request.client_ip)) {
            return reject(RejectReason::client_not_in_isolated_network, request);
        }
        audit("weak_auth_permitted", request,
              "legacy RDP security permitted in isolated network");
    }

    NegotiationDecision decision;
    decision.accepted = true;
    decision.reason = RejectReason::none;
    decision.binding.session_id = session_salt_ + (++session_counter_);
    decision.binding.protocol = request.requested_protocol;
    decision.binding.protocol_version = request.client_protocol_version;
    decision.binding.token = compute_binding_token(decision.binding.session_id,
                                                   decision.binding.protocol,
                                                   decision.binding.protocol_version);
    return decision;
}

bool Negotiator::verify_binding(const SessionBinding& binding) const {
    const Hash256 expected =
        compute_binding_token(binding.session_id, binding.protocol, binding.protocol_version);
    return constant_time_equal(expected, binding.token);
}

CredSspTicket Negotiator::issue_credssp_ticket(const SessionBinding& binding,
                                               std::uint64_t ticket_id,
                                               std::uint64_t expires_at) const {
    CredSspTicket ticket;
    ticket.ticket_id = ticket_id;
    ticket.session_id = binding.session_id;
    ticket.expires_at = expires_at;

    std::vector<std::uint8_t> msg;
    const std::string label = "rdp-auth/credssp-ticket/v1";
    msg.insert(msg.end(), label.begin(), label.end());
    append_u64(msg, ticket.ticket_id);
    append_u64(msg, ticket.session_id);
    append_u64(msg, ticket.expires_at);
    msg.insert(msg.end(), binding.token.begin(), binding.token.end());
    ticket.mac = hmac_sha256(server_key_, msg);
    return ticket;
}

bool Negotiator::validate_credssp_ticket(const SessionBinding& binding,
                                         const CredSspTicket& ticket,
                                         std::uint64_t now) const {
    if (!verify_binding(binding)) {
        return false;
    }
    if (ticket.session_id != binding.session_id) {
        return false;
    }
    if (now >= ticket.expires_at) {
        return false;
    }
    const CredSspTicket expected =
        issue_credssp_ticket(binding, ticket.ticket_id, ticket.expires_at);
    return constant_time_equal(expected.mac, ticket.mac);
}

std::optional<SecureChannel>
Negotiator::establish_channel(const NegotiationDecision& decision) const {
    if (!decision.accepted || !verify_binding(decision.binding)) {
        return std::nullopt;
    }
    std::vector<std::uint8_t> seed;
    const std::string label = "rdp-auth/session-key/v1";
    seed.insert(seed.end(), label.begin(), label.end());
    seed.insert(seed.end(), decision.binding.token.begin(), decision.binding.token.end());
    const Hash256 session_key = hmac_sha256(server_key_, seed);
    return SecureChannel{decision.binding, session_key};
}

} // namespace rdp::auth
