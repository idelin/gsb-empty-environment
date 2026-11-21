#include "rdpauth/negotiator.hpp"

#include <algorithm>
#include <cstdio>
#include <string>
#include <vector>

namespace {

using namespace rdp::auth;

int g_passes = 0;
int g_failures = 0;

#define CHECK(cond)                                                                            \
    do {                                                                                       \
        if (cond) {                                                                            \
            ++g_passes;                                                                        \
        } else {                                                                               \
            ++g_failures;                                                                      \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);                        \
        }                                                                                      \
    } while (0)

class InMemoryAuditSink : public AuditSink {
public:
    void record(const AuditEvent& event) override { events.push_back(event); }
    bool has_category(const std::string& category) const {
        return std::any_of(events.begin(), events.end(),
                           [&](const AuditEvent& e) { return e.category == category; });
    }
    std::vector<AuditEvent> events;
};

const Hash256 kServerKey = hash_of("server-secret-key-material");
const Hash256 kServerCertFp = hash_of("server-certificate-der-bytes");
const Hash256 kAttackerCertFp = hash_of("attacker-certificate-der-bytes");
const Hash256 kConfigHash = hash_of("server-configuration-v1");
const std::string kPassword = "S3cret!Passw0rd#2026";

ServerAuthPolicy make_strict_policy() {
    ServerAuthPolicy policy;
    policy.require_nla = true;
    policy.allow_weak_auth = false;
    policy.min_protocol_version = kProtocolVersionCurrent;
    policy.server_cert_fingerprint = kServerCertFp;
    policy.config_id = "rdp-server-config-v1";
    policy.config_hash = kConfigHash;
    return policy;
}

NegotiationRequest make_request(AuthProtocol protocol) {
    NegotiationRequest request;
    request.requested_protocol = protocol;
    request.client_protocol_version = kProtocolVersionCurrent;
    request.presented_cert_fingerprint = kServerCertFp;
    request.client_ip = "203.0.113.9";
    return request;
}

bool wire_contains(const std::vector<std::uint8_t>& wire, std::string_view needle) {
    return std::search(wire.begin(), wire.end(), needle.begin(), needle.end()) != wire.end();
}

// 1. Type downgrade: NLA required, client forces legacy/TLS -> rejected.
void test_type_downgrade_rejected() {
    InMemoryAuditSink sink;
    Negotiator negotiator(make_strict_policy(), kServerKey, &sink);

    const NegotiationDecision legacy = negotiator.negotiate(make_request(AuthProtocol::legacy_rdp));
    CHECK(!legacy.accepted);
    CHECK(legacy.reason == RejectReason::nla_required_downgrade);
    CHECK(!negotiator.establish_channel(legacy).has_value());

    const NegotiationDecision tls = negotiator.negotiate(make_request(AuthProtocol::tls));
    CHECK(!tls.accepted);
    CHECK(tls.reason == RejectReason::nla_required_downgrade);
    CHECK(!negotiator.establish_channel(tls).has_value());

    CHECK(sink.has_category("negotiation_rejected"));
}

// 2. Certificate substitution: MITM shows attacker certificate -> rejected.
void test_certificate_substitution_rejected() {
    InMemoryAuditSink sink;
    Negotiator negotiator(make_strict_policy(), kServerKey, &sink);

    NegotiationRequest request = make_request(AuthProtocol::credssp);
    request.presented_cert_fingerprint = kAttackerCertFp;
    const NegotiationDecision decision = negotiator.negotiate(request);
    CHECK(!decision.accepted);
    CHECK(decision.reason == RejectReason::certificate_mismatch);
    CHECK(!negotiator.establish_channel(decision).has_value());
}

// 3. Version rollback: client offers an older protocol version -> rejected.
void test_version_rollback_rejected() {
    InMemoryAuditSink sink;
    Negotiator negotiator(make_strict_policy(), kServerKey, &sink);

    NegotiationRequest request = make_request(AuthProtocol::credssp);
    request.client_protocol_version = kProtocolVersionCurrent - 1;
    const NegotiationDecision decision = negotiator.negotiate(request);
    CHECK(!decision.accepted);
    CHECK(decision.reason == RejectReason::protocol_version_rollback);
    CHECK(!negotiator.establish_channel(decision).has_value());
}

// 4. Legitimate strong auth: CredSSP accepted, binding and ticket verified,
//    password never on the wire in cleartext.
void test_legitimate_strong_auth() {
    InMemoryAuditSink sink;
    Negotiator negotiator(make_strict_policy(), kServerKey, &sink);

    const NegotiationDecision decision =
        negotiator.negotiate(make_request(AuthProtocol::credssp));
    CHECK(decision.accepted);
    CHECK(decision.reason == RejectReason::none);
    CHECK(decision.binding.protocol == AuthProtocol::credssp);
    CHECK(negotiator.verify_binding(decision.binding));

    // Binding is tied to session, protocol and version: any tamper breaks it.
    SessionBinding tampered = decision.binding;
    tampered.session_id += 1;
    CHECK(!negotiator.verify_binding(tampered));
    tampered = decision.binding;
    tampered.protocol_version -= 1;
    CHECK(!negotiator.verify_binding(tampered));
    tampered = decision.binding;
    tampered.token[0] ^= 0xFF;
    CHECK(!negotiator.verify_binding(tampered));

    // Binding is tied to server config and certificate.
    ServerAuthPolicy other_config = make_strict_policy();
    other_config.config_hash = hash_of("server-configuration-v2");
    Negotiator other_config_negotiator(other_config, kServerKey, &sink);
    CHECK(!other_config_negotiator.verify_binding(decision.binding));

    ServerAuthPolicy other_cert = make_strict_policy();
    other_cert.server_cert_fingerprint = kAttackerCertFp;
    Negotiator other_cert_negotiator(other_cert, kServerKey, &sink);
    CHECK(!other_cert_negotiator.verify_binding(decision.binding));

    // Channel transmits credentials encrypted only.
    auto channel = negotiator.establish_channel(decision);
    CHECK(channel.has_value());
    channel->send_credentials("alice", kPassword);
    CHECK(!channel->wire_log().empty());
    CHECK(!wire_contains(channel->wire_log(), kPassword));
    CHECK(!wire_contains(channel->wire_log(), "alice"));

    // CredSSP ticket is bound to the issuing session.
    const std::uint64_t now = 1'800'000'000;
    const CredSspTicket ticket =
        negotiator.issue_credssp_ticket(decision.binding, 42, now + 600);
    CHECK(negotiator.validate_credssp_ticket(decision.binding, ticket, now));

    // Same ticket presented for a different session must fail.
    const NegotiationDecision other =
        negotiator.negotiate(make_request(AuthProtocol::credssp));
    CHECK(other.accepted);
    CHECK(!negotiator.validate_credssp_ticket(other.binding, ticket, now));

    // Expired or tampered tickets must fail.
    CHECK(!negotiator.validate_credssp_ticket(decision.binding, ticket, now + 601));
    CredSspTicket forged = ticket;
    forged.mac[0] ^= 0xFF;
    CHECK(!negotiator.validate_credssp_ticket(decision.binding, forged, now));
}

// 5. Explicit weak policy: legacy auth only inside isolated networks, audited.
void test_explicit_weak_policy() {
    InMemoryAuditSink sink;
    ServerAuthPolicy policy = make_strict_policy();
    policy.require_nla = false;
    policy.allow_weak_auth = true;
    policy.isolated_networks = {"10.0.0.0/8"};
    Negotiator negotiator(policy, kServerKey, &sink);

    // Isolated network client: permitted and audited.
    NegotiationRequest isolated = make_request(AuthProtocol::legacy_rdp);
    isolated.client_ip = "10.20.30.40";
    const NegotiationDecision accepted = negotiator.negotiate(isolated);
    CHECK(accepted.accepted);
    CHECK(sink.has_category("weak_auth_permitted"));

    // Even on the weak path, credentials stay off the plaintext channel.
    auto channel = negotiator.establish_channel(accepted);
    CHECK(channel.has_value());
    channel->send_credentials("bob", kPassword);
    CHECK(!wire_contains(channel->wire_log(), kPassword));

    // Non-isolated client: rejected.
    NegotiationRequest outside = make_request(AuthProtocol::legacy_rdp);
    outside.client_ip = "192.168.1.5";
    const NegotiationDecision rejected = negotiator.negotiate(outside);
    CHECK(!rejected.accepted);
    CHECK(rejected.reason == RejectReason::client_not_in_isolated_network);

    // Weak auth without explicit opt-in: rejected.
    ServerAuthPolicy no_opt_in = policy;
    no_opt_in.allow_weak_auth = false;
    Negotiator strict_negotiator(no_opt_in, kServerKey, &sink);
    const NegotiationDecision denied = strict_negotiator.negotiate(isolated);
    CHECK(!denied.accepted);
    CHECK(denied.reason == RejectReason::weak_auth_not_allowed);

    // Weak allowance never overrides a mandatory NLA policy.
    ServerAuthPolicy nla_first = policy;
    nla_first.require_nla = true;
    Negotiator nla_negotiator(nla_first, kServerKey, &sink);
    const NegotiationDecision downgraded = nla_negotiator.negotiate(isolated);
    CHECK(!downgraded.accepted);
    CHECK(downgraded.reason == RejectReason::nla_required_downgrade);
}

// Cross-scenario: the password must never appear on any wire log.
void test_password_never_on_plaintext_channel() {
    InMemoryAuditSink sink;
    Negotiator strong(make_strict_policy(), kServerKey, &sink);

    ServerAuthPolicy weak_policy = make_strict_policy();
    weak_policy.require_nla = false;
    weak_policy.allow_weak_auth = true;
    weak_policy.isolated_networks = {"172.16.0.0/12"};
    Negotiator weak(weak_policy, kServerKey, &sink);

    std::vector<std::uint8_t> all_wire_bytes;

    const NegotiationDecision strong_decision =
        strong.negotiate(make_request(AuthProtocol::credssp));
    auto strong_channel = strong.establish_channel(strong_decision);
    CHECK(strong_channel.has_value());
    strong_channel->send_credentials("carol", kPassword);
    all_wire_bytes.insert(all_wire_bytes.end(), strong_channel->wire_log().begin(),
                          strong_channel->wire_log().end());

    NegotiationRequest weak_request = make_request(AuthProtocol::legacy_rdp);
    weak_request.client_ip = "172.16.5.5";
    const NegotiationDecision weak_decision = weak.negotiate(weak_request);
    auto weak_channel = weak.establish_channel(weak_decision);
    CHECK(weak_channel.has_value());
    weak_channel->send_credentials("carol", kPassword);
    all_wire_bytes.insert(all_wire_bytes.end(), weak_channel->wire_log().begin(),
                          weak_channel->wire_log().end());

    CHECK(!all_wire_bytes.empty());
    CHECK(!wire_contains(all_wire_bytes, kPassword));
}

} // namespace

int main() {
    test_type_downgrade_rejected();
    test_certificate_substitution_rejected();
    test_version_rollback_rejected();
    test_legitimate_strong_auth();
    test_explicit_weak_policy();
    test_password_never_on_plaintext_channel();

    std::printf("%d checks passed, %d failed\n", g_passes, g_failures);
    return g_failures == 0 ? 0 : 1;
}
