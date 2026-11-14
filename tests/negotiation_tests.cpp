#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <iostream>
#include <memory>
#include <optional>
#include <sstream>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace {

using namespace netsec;

constexpr std::int64_t kNow = 1'800'000'000;
constexpr std::string_view kPassword =
    "S3cr3t!P@ssw0rd-NOT-ON-WIRE-42";

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const char* expression, const char* file, int line) {
  ++g_checks;
  if (!condition) {
    ++g_failures;
    std::cerr << file << ':' << line << ": CHECK failed: " << expression
              << '\n';
  }
}

void check_status(Status actual, Status expected, const char* file, int line,
                  const char* label) {
  ++g_checks;
  if (actual != expected) {
    ++g_failures;
    std::cerr << file << ':' << line << ": " << label << " expected "
              << to_string(expected) << " but got " << to_string(actual)
              << '\n';
  }
}

#define CHECK(expression) check((expression), #expression, __FILE__, __LINE__)
#define CHECK_STATUS(actual, expected) \
  check_status((actual), (expected), __FILE__, __LINE__, #actual)

Bytes make_bytes(std::string_view text) {
  Bytes out;
  out.reserve(text.size());
  for (char ch : text) {
    out.push_back(static_cast<std::uint8_t>(ch));
  }
  return out;
}

Bytes make_certificate(std::string_view tag) {
  Bytes out{0x30U, 0x82U, 0x01U, 0x00U, 0x02U, 0x01U, 0x01U};
  const Bytes body = make_bytes(tag);
  out.insert(out.end(), body.begin(), body.end());
  return out;
}

const Bytes& good_certificate() {
  static const Bytes value = make_certificate("server-certificate-alpha");
  return value;
}

const Bytes& replacement_certificate() {
  static const Bytes value = make_certificate("attacker-certificate-beta");
  return value;
}

Bytes make_exporter(std::uint8_t seed) {
  Bytes out(32);
  for (std::size_t i = 0; i < out.size(); ++i) {
    out[i] = static_cast<std::uint8_t>(seed + i * 7U);
  }
  return out;
}

std::string make_nonce(std::uint32_t serial) {
  Bytes value(16);
  for (std::size_t i = 0; i < value.size(); ++i) {
    value[i] = static_cast<std::uint8_t>(
        (serial >> ((i % 4U) * 8U)) ^ (0xA5U + i * 13U));
  }
  value[0] = static_cast<std::uint8_t>(value[0] ^ serial);
  return to_hex(value);
}

struct CounterRandom {
  std::uint8_t next{1};

  Bytes operator()(std::size_t length) {
    Bytes out(length);
    for (std::uint8_t& byte : out) {
      byte = next++;
    }
    return out;
  }
};

AuthenticatorOptions deterministic_options() {
  auto counter = std::make_shared<CounterRandom>();
  AuthenticatorOptions options;
  options.random = [counter](std::size_t length) {
    return (*counter)(length);
  };
  options.now_unix = [] { return kNow; };
  return options;
}

ServerPolicy strong_policy() {
  ServerPolicy policy;
  policy.server_id = "rdp.example.test";
  policy.policy_revision = "2026-11-strong";
  policy.minimum_version = {10, 6};
  policy.maximum_version = {10, 12};
  policy.require_nla = true;
  policy.reject_downgrade = true;
  policy.allow_weak = false;
  policy.allowed_methods = {AuthMethod::RdpStandard, AuthMethod::Tls,
                            AuthMethod::CredSsp};
  policy.isolated_networks = {{"10.20.0.0/16", true}};
  policy.pinned_certificate_sha256 = {
      fingerprint_certificate(good_certificate())};
  policy.binding_lifetime = std::chrono::seconds(60);
  policy.ticket_lifetime = std::chrono::seconds(600);
  return policy;
}

ServerPolicy weak_policy() {
  ServerPolicy policy = strong_policy();
  policy.policy_revision = "2026-11-isolated";
  policy.require_nla = false;
  policy.reject_downgrade = false;
  policy.allow_weak = true;
  return policy;
}

ClientHello make_hello(const ServerPolicy& policy,
                       const Bytes& certificate,
                       std::vector<AuthMethod> methods,
                       ProtocolVersion version = {10, 12},
                       std::string nonce = make_nonce(1),
                       bool requires_nla = true) {
  ClientHello hello;
  hello.server_id = policy.server_id;
  hello.expected_certificate_sha256 =
      fingerprint_certificate(certificate);
  hello.offered_methods = std::move(methods);
  hello.version = version;
  hello.client_nonce = std::move(nonce);
  hello.requires_nla = requires_nla;
  return hello;
}

TransportContext make_transport(std::string peer, const Bytes& certificate,
                                std::uint8_t exporter_seed = 1) {
  TransportContext transport;
  transport.peer_address = std::move(peer);
  transport.presented_certificate = certificate;
  transport.transport_exporter = make_exporter(exporter_seed);
  return transport;
}

ClientExpectations make_expectations(const ServerPolicy& policy,
                                     const ClientHello& hello,
                                     std::uint8_t exporter_seed,
                                     AuthMethod minimum_method,
                                     bool requires_nla) {
  ClientExpectations expectations;
  expectations.server_id = hello.server_id;
  expectations.policy_digest = policy_digest(policy);
  expectations.observed_certificate_sha256 =
      hello.expected_certificate_sha256;
  expectations.offered_methods = hello.offered_methods;
  expectations.offered_version = hello.version;
  expectations.client_nonce = hello.client_nonce;
  expectations.minimum_method = minimum_method;
  expectations.requires_nla = requires_nla;
  expectations.transport_exporter = make_exporter(exporter_seed);
  return expectations;
}

bool contains_text(std::span<const std::uint8_t> haystack,
                   std::string_view needle) {
  const auto* first = reinterpret_cast<const std::uint8_t*>(needle.data());
  return std::search(haystack.begin(), haystack.end(), first,
                     first + needle.size()) != haystack.end();
}

bool audit_contains(const std::vector<AuditEvent>& events,
                    AuditEventType type, Status status) {
  return std::any_of(events.begin(), events.end(),
                     [&](const AuditEvent& event) {
                       return event.type == type && event.status == status;
                     });
}

std::string join_audit(const std::vector<AuditEvent>& events) {
  std::ostringstream stream;
  for (const AuditEvent& event : events) {
    stream << format_audit_event(event) << '\n';
  }
  return stream.str();
}

bool same_binding(const NegotiationBinding& lhs,
                  const NegotiationBinding& rhs) {
  return lhs.server_id == rhs.server_id &&
         lhs.policy_digest == rhs.policy_digest &&
         lhs.certificate_sha256 == rhs.certificate_sha256 &&
         lhs.offered_methods == rhs.offered_methods &&
         lhs.requested_version == rhs.requested_version &&
         lhs.negotiated_version == rhs.negotiated_version &&
         lhs.method == rhs.method && lhs.peer_address == rhs.peer_address &&
         lhs.client_nonce == rhs.client_nonce &&
         lhs.server_nonce == rhs.server_nonce &&
         lhs.session_id == rhs.session_id && lhs.ticket == rhs.ticket &&
         lhs.issued_at == rhs.issued_at && lhs.expires_at == rhs.expires_at &&
         lhs.mac == rhs.mac;
}

void test_legitimate_strong_auth_and_wire_capture() {
  const ServerPolicy policy = strong_policy();
  Authenticator authenticator(policy, deterministic_options());
  CHECK_STATUS(authenticator.policy_status(), Status::Ok);

  const std::uint8_t exporter_seed = 11;
  const ClientHello hello =
      make_hello(policy, good_certificate(),
                 {AuthMethod::RdpStandard, AuthMethod::Tls,
                  AuthMethod::CredSsp},
                 {10, 12}, make_nonce(10), true);
  const TransportContext transport =
      make_transport("10.20.9.15", good_certificate(), exporter_seed);

  const NegotiationResult result = authenticator.negotiate(hello, transport);
  CHECK_STATUS(result.status, Status::Ok);
  CHECK(result.binding.has_value());
  if (!result.binding.has_value()) {
    return;
  }
  const NegotiationBinding& binding = *result.binding;
  CHECK(binding.method == AuthMethod::CredSsp);
  CHECK(binding.certificate_sha256 ==
        fingerprint_certificate(good_certificate()));
  CHECK(binding.policy_digest == policy_digest(policy));
  CHECK(binding.negotiated_version == (ProtocolVersion{10, 12}));
  CHECK(binding.session_id.size() == 32U);
  CHECK(binding.ticket.size() == 64U);

  const ClientExpectations expectations =
      make_expectations(policy, hello, exporter_seed, AuthMethod::CredSsp,
                        true);
  CHECK_STATUS(verify_binding(binding, expectations, kNow), Status::Ok);
  CHECK(authenticator.verify_ticket(
      binding.ticket, binding.session_id, binding.certificate_sha256));
  const auto session =
      authenticator.resume_session(binding.ticket, binding.session_id);
  CHECK(session.has_value());
  if (session.has_value()) {
    CHECK(session->method == AuthMethod::CredSsp);
    CHECK(session->session_id == binding.session_id);
  }

  const CredentialPayload credentials{"CORP", "alice", std::string(kPassword)};
  const CredentialSealResult sealed =
      seal_credentials(binding, transport.transport_exporter, credentials, 1,
                       kNow);
  CHECK_STATUS(sealed.status, Status::Ok);
  CHECK(sealed.envelope.has_value());
  if (!sealed.envelope.has_value()) {
    return;
  }

  Bytes captured = serialize_binding(binding);
  const Bytes envelope_wire = serialize_credentials(*sealed.envelope);
  captured.insert(captured.end(), envelope_wire.begin(), envelope_wire.end());
  CHECK(!contains_text(captured, kPassword));
  CHECK(!contains_text(captured, credentials.username));

  const CredentialOpenResult opened = authenticator.open_credentials(
      binding, *sealed.envelope, transport.transport_exporter);
  CHECK_STATUS(opened.status, Status::Ok);
  CHECK(opened.credentials.has_value());
  if (opened.credentials.has_value()) {
    CHECK(opened.credentials->domain == credentials.domain);
    CHECK(opened.credentials->username == credentials.username);
    CHECK(opened.credentials->password == credentials.password);
  }

  const auto events = authenticator.drain_audit_log();
  CHECK(audit_contains(events, AuditEventType::NegotiationAccepted, Status::Ok));
  CHECK(audit_contains(events, AuditEventType::CredentialsAccepted, Status::Ok));
  CHECK(!contains_text(make_bytes(join_audit(events)), kPassword));
}

void test_nla_downgrade_is_rejected() {
  const ServerPolicy policy = strong_policy();
  Authenticator authenticator(policy, deterministic_options());

  for (const AuthMethod weak :
       {AuthMethod::RdpStandard, AuthMethod::Tls}) {
    const ClientHello hello =
        make_hello(policy, good_certificate(), {weak}, {10, 12},
                   make_nonce(20U + static_cast<std::uint32_t>(weak)), true);
    const NegotiationResult result = authenticator.negotiate(
        hello, make_transport("10.20.9.15", good_certificate(), 21));
    CHECK_STATUS(result.status, Status::RejectedNlaRequired);
    CHECK(!result.binding.has_value());
  }

  const auto events = authenticator.drain_audit_log();
  CHECK(audit_contains(events, AuditEventType::NegotiationRejected,
                       Status::RejectedNlaRequired));
}

void test_downgrade_tampering_is_detected() {
  const ServerPolicy policy = strong_policy();
  Authenticator authenticator(policy, deterministic_options());
  const std::uint8_t exporter_seed = 31;
  const ClientHello hello =
      make_hello(policy, good_certificate(),
                 {AuthMethod::RdpStandard, AuthMethod::CredSsp});
  const TransportContext transport =
      make_transport("10.20.9.15", good_certificate(), exporter_seed);
  const NegotiationResult result = authenticator.negotiate(hello, transport);
  CHECK_STATUS(result.status, Status::Ok);
  if (!result.binding.has_value()) {
    return;
  }

  ClientExpectations expectations =
      make_expectations(policy, hello, exporter_seed, AuthMethod::CredSsp,
                        true);

  NegotiationBinding method_tampered = *result.binding;
  method_tampered.method = AuthMethod::RdpStandard;
  CHECK_STATUS(verify_binding(method_tampered, expectations, kNow),
               Status::RejectedNlaRequired);

  expectations.requires_nla = false;
  expectations.minimum_method = AuthMethod::RdpStandard;
  CHECK_STATUS(verify_binding(method_tampered, expectations, kNow),
               Status::RejectedBinding);

  NegotiationBinding offer_tampered = *result.binding;
  offer_tampered.offered_methods = {AuthMethod::RdpStandard};
  expectations.offered_methods = {AuthMethod::RdpStandard};
  expectations.minimum_method = AuthMethod::RdpStandard;
  CHECK_STATUS(verify_binding(offer_tampered, expectations, kNow),
               Status::RejectedBinding);

  NegotiationBinding mac_tampered = *result.binding;
  mac_tampered.mac[0] =
      mac_tampered.mac[0] == '0' ? '1' : '0';
  expectations = make_expectations(policy, hello, exporter_seed,
                                   AuthMethod::CredSsp, true);
  CHECK_STATUS(verify_binding(mac_tampered, expectations, kNow),
               Status::RejectedBinding);

  const CredentialSealResult refused = seal_credentials(
      mac_tampered, make_exporter(exporter_seed),
      CredentialPayload{"CORP", "alice", std::string(kPassword)}, 1, kNow);
  CHECK_STATUS(refused.status, Status::RejectedBinding);
  CHECK(!refused.envelope.has_value());
}

void test_certificate_substitution_is_rejected() {
  const ServerPolicy policy = strong_policy();
  Authenticator authenticator(policy, deterministic_options());
  const ClientHello hello =
      make_hello(policy, replacement_certificate(), {AuthMethod::CredSsp});

  const NegotiationResult result = authenticator.negotiate(
      hello, make_transport("10.20.9.15", replacement_certificate(), 41));
  CHECK_STATUS(result.status, Status::RejectedCertificate);
  CHECK(!result.binding.has_value());

  const ClientHello good_hello =
      make_hello(policy, good_certificate(), {AuthMethod::CredSsp},
                 {10, 12}, make_nonce(42));
  const TransportContext good_transport =
      make_transport("10.20.9.15", good_certificate(), 42);
  const NegotiationResult good = authenticator.negotiate(good_hello,
                                                         good_transport);
  CHECK_STATUS(good.status, Status::Ok);
  if (!good.binding.has_value()) {
    return;
  }

  ClientExpectations expectations = make_expectations(
      policy, good_hello, 42, AuthMethod::CredSsp, true);
  expectations.observed_certificate_sha256 =
      fingerprint_certificate(replacement_certificate());
  CHECK_STATUS(verify_binding(*good.binding, expectations, kNow),
               Status::RejectedCertificate);

  NegotiationBinding replaced = *good.binding;
  replaced.certificate_sha256 =
      fingerprint_certificate(replacement_certificate());
  expectations = make_expectations(policy, good_hello, 42,
                                   AuthMethod::CredSsp, true);
  CHECK_STATUS(verify_binding(replaced, expectations, kNow),
               Status::RejectedCertificate);

  const auto events = authenticator.drain_audit_log();
  CHECK(audit_contains(events, AuditEventType::NegotiationRejected,
                       Status::RejectedCertificate));
}

void test_protocol_version_rollback_is_rejected() {
  const ServerPolicy policy = strong_policy();
  Authenticator authenticator(policy, deterministic_options());

  for (const ProtocolVersion version :
       {ProtocolVersion{10, 5}, ProtocolVersion{11, 0}}) {
    const ClientHello hello =
        make_hello(policy, good_certificate(), {AuthMethod::CredSsp}, version,
                   make_nonce(50U + version.minor));
    const NegotiationResult result = authenticator.negotiate(
        hello, make_transport("10.20.9.15", good_certificate(), 51));
    CHECK_STATUS(result.status, Status::RejectedProtocolVersion);
  }

  const std::uint8_t exporter_seed = 52;
  const ClientHello hello =
      make_hello(policy, good_certificate(), {AuthMethod::CredSsp},
                 {10, 12}, make_nonce(52));
  const TransportContext transport =
      make_transport("10.20.9.15", good_certificate(), exporter_seed);
  const NegotiationResult result = authenticator.negotiate(hello, transport);
  CHECK_STATUS(result.status, Status::Ok);
  if (!result.binding.has_value()) {
    return;
  }
  const ClientExpectations expectations =
      make_expectations(policy, hello, exporter_seed, AuthMethod::CredSsp,
                        true);
  NegotiationBinding rolled_back = *result.binding;
  rolled_back.negotiated_version = {10, 5};
  CHECK_STATUS(verify_binding(rolled_back, expectations, kNow),
               Status::RejectedProtocolVersion);
}

void test_weak_auth_requires_explicit_isolated_policy() {
  const ServerPolicy policy = weak_policy();
  Authenticator authenticator(policy, deterministic_options());
  const std::uint8_t exporter_seed = 61;

  const ClientHello allowed_hello =
      make_hello(policy, good_certificate(), {AuthMethod::RdpStandard},
                 {10, 12}, make_nonce(61), false);
  const TransportContext allowed_transport =
      make_transport("10.20.44.9", good_certificate(), exporter_seed);
  const NegotiationResult allowed =
      authenticator.negotiate(allowed_hello, allowed_transport);
  CHECK_STATUS(allowed.status, Status::Ok);
  CHECK(allowed.binding.has_value());
  if (!allowed.binding.has_value()) {
    return;
  }
  CHECK(allowed.binding->method == AuthMethod::RdpStandard);
  CHECK(allowed.binding->ticket.empty());

  const ClientExpectations expectations = make_expectations(
      policy, allowed_hello, exporter_seed, AuthMethod::RdpStandard, false);
  CHECK_STATUS(verify_binding(*allowed.binding, expectations, kNow), Status::Ok);

  const CredentialPayload credentials{"LAB", "weak-user",
                                      std::string(kPassword)};
  const CredentialSealResult sealed = seal_credentials(
      *allowed.binding, allowed_transport.transport_exporter, credentials, 1,
      kNow);
  CHECK_STATUS(sealed.status, Status::Ok);
  if (sealed.envelope.has_value()) {
    Bytes captured = serialize_binding(*allowed.binding);
    const Bytes wire = serialize_credentials(*sealed.envelope);
    captured.insert(captured.end(), wire.begin(), wire.end());
    CHECK(!contains_text(captured, kPassword));

    const CredentialOpenResult opened = authenticator.open_credentials(
        *allowed.binding, *sealed.envelope,
        allowed_transport.transport_exporter);
    CHECK_STATUS(opened.status, Status::Ok);
    CHECK(opened.credentials.has_value());
    if (opened.credentials.has_value()) {
      CHECK(opened.credentials->password == std::string(kPassword));
    }
  }
  CHECK(!authenticator.resume_session({}, allowed.binding->session_id)
             .has_value());

  const ClientHello outside_hello =
      make_hello(policy, good_certificate(), {AuthMethod::RdpStandard},
                 {10, 12}, make_nonce(62), false);
  const NegotiationResult outside = authenticator.negotiate(
      outside_hello, make_transport("192.168.4.10", good_certificate(), 62));
  CHECK_STATUS(outside.status, Status::RejectedWeakNetwork);

  const ClientHello nla_hello =
      make_hello(policy, good_certificate(), {AuthMethod::RdpStandard},
                 {10, 12}, make_nonce(63), true);
  CHECK_STATUS(authenticator
                   .negotiate(nla_hello,
                              make_transport("10.20.44.9", good_certificate(),
                                             63))
                   .status,
               Status::RejectedNlaRequired);

  ServerPolicy disabled = policy;
  disabled.allow_weak = false;
  Authenticator disabled_authenticator(disabled, deterministic_options());
  const ClientHello disabled_hello =
      make_hello(disabled, good_certificate(), {AuthMethod::RdpStandard},
                 {10, 12}, make_nonce(64), false);
  CHECK_STATUS(disabled_authenticator
                   .negotiate(disabled_hello,
                              make_transport("10.20.44.9", good_certificate(),
                                             64))
                   .status,
               Status::RejectedWeakAuthDisabled);

  ServerPolicy non_isolated = policy;
  non_isolated.isolated_networks = {{"10.20.0.0/16", false}};
  Authenticator non_isolated_authenticator(non_isolated,
                                           deterministic_options());
  const ClientHello non_isolated_hello =
      make_hello(non_isolated, good_certificate(), {AuthMethod::RdpStandard},
                 {10, 12}, make_nonce(65), false);
  CHECK_STATUS(
      non_isolated_authenticator
          .negotiate(non_isolated_hello,
                     make_transport("10.20.44.9", good_certificate(), 65))
          .status,
      Status::RejectedWeakNetwork);

  const auto events = authenticator.drain_audit_log();
  CHECK(audit_contains(events, AuditEventType::WeakAuthAccepted, Status::Ok));
  CHECK(audit_contains(events, AuditEventType::WeakAuthRejected,
                       Status::RejectedWeakNetwork));
  CHECK(!contains_text(make_bytes(join_audit(events)), kPassword));
}

void test_strong_capable_client_is_not_silently_downgraded() {
  ServerPolicy policy = weak_policy();
  policy.reject_downgrade = true;
  policy.isolated_networks = {{"10.20.0.0/16", true}};
  Authenticator authenticator(policy, deterministic_options());

  const ClientHello hello =
      make_hello(policy, good_certificate(),
                 {AuthMethod::RdpStandard, AuthMethod::CredSsp},
                 {10, 12}, make_nonce(70), false);
  const NegotiationResult result = authenticator.negotiate(
      hello, make_transport("10.20.44.9", good_certificate(), 70));
  CHECK_STATUS(result.status, Status::Ok);
  CHECK(result.binding.has_value());
  if (result.binding.has_value()) {
    CHECK(result.binding->method == AuthMethod::CredSsp);
    CHECK(!result.binding->ticket.empty());
  }
}

void test_client_detects_stripped_strong_offer() {
  ServerPolicy policy = weak_policy();
  policy.reject_downgrade = false;
  Authenticator authenticator(policy, deterministic_options());

  const ClientHello original =
      make_hello(policy, good_certificate(),
                 {AuthMethod::RdpStandard, AuthMethod::CredSsp},
                 {10, 12}, make_nonce(75), false);
  ClientHello intercepted = original;
  intercepted.offered_methods = {AuthMethod::RdpStandard};

  const std::uint8_t exporter_seed = 75;
  const NegotiationResult result = authenticator.negotiate(
      intercepted,
      make_transport("10.20.44.9", good_certificate(), exporter_seed));
  CHECK_STATUS(result.status, Status::Ok);
  if (!result.binding.has_value()) {
    return;
  }
  CHECK(result.binding->method == AuthMethod::RdpStandard);

  const ClientExpectations expectations = make_expectations(
      policy, original, exporter_seed, AuthMethod::CredSsp, false);
  CHECK_STATUS(verify_binding(*result.binding, expectations, kNow),
               Status::RejectedDowngrade);
}

void test_tickets_are_bound_to_sessions() {
  const ServerPolicy policy = strong_policy();
  Authenticator authenticator(policy, deterministic_options());
  const std::uint8_t exporter_seed = 81;
  const ClientHello hello =
      make_hello(policy, good_certificate(), {AuthMethod::CredSsp},
                 {10, 12}, make_nonce(81));
  const TransportContext transport =
      make_transport("10.20.9.15", good_certificate(), exporter_seed);
  const NegotiationResult first = authenticator.negotiate(hello, transport);
  CHECK_STATUS(first.status, Status::Ok);
  if (!first.binding.has_value()) {
    return;
  }

  CHECK(authenticator.verify_ticket(
      first.binding->ticket, first.binding->session_id,
      first.binding->certificate_sha256));
  CHECK(!authenticator.verify_ticket(
      first.binding->ticket, "00000000000000000000000000000000",
      first.binding->certificate_sha256));
  CHECK(!authenticator.verify_ticket(
      first.binding->ticket, first.binding->session_id,
      fingerprint_certificate(replacement_certificate())));
  CHECK(!authenticator
             .resume_session(first.binding->ticket,
                             "00000000000000000000000000000000")
             .has_value());

  const ClientHello second_hello =
      make_hello(policy, good_certificate(), {AuthMethod::CredSsp},
                 {10, 12}, make_nonce(82));
  const NegotiationResult second = authenticator.negotiate(
      second_hello, make_transport("10.20.9.16", good_certificate(), 82));
  CHECK_STATUS(second.status, Status::Ok);
  if (second.binding.has_value()) {
    CHECK(first.binding->session_id != second.binding->session_id);
    CHECK(first.binding->ticket != second.binding->ticket);
    CHECK(!authenticator
               .resume_session(first.binding->ticket,
                               second.binding->session_id)
               .has_value());
  }

  const NegotiationResult replay = authenticator.negotiate(hello, transport);
  CHECK_STATUS(replay.status, Status::RejectedReplay);
  const auto events = authenticator.drain_audit_log();
  CHECK(audit_contains(events, AuditEventType::ReplayRejected,
                       Status::RejectedReplay));
  CHECK(audit_contains(events, AuditEventType::TicketRejected,
                       Status::RejectedTicket));
}

void test_credential_envelope_tampering_and_replay() {
  const ServerPolicy policy = strong_policy();
  Authenticator authenticator(policy, deterministic_options());
  const std::uint8_t exporter_seed = 91;
  const ClientHello hello =
      make_hello(policy, good_certificate(), {AuthMethod::CredSsp},
                 {10, 12}, make_nonce(91));
  const TransportContext transport =
      make_transport("10.20.9.15", good_certificate(), exporter_seed);
  const NegotiationResult result = authenticator.negotiate(hello, transport);
  CHECK_STATUS(result.status, Status::Ok);
  if (!result.binding.has_value()) {
    return;
  }
  const CredentialPayload credentials{"CORP", "bob", std::string(kPassword)};
  const CredentialSealResult sealed = seal_credentials(
      *result.binding, transport.transport_exporter, credentials, 7, kNow);
  CHECK_STATUS(sealed.status, Status::Ok);
  if (!sealed.envelope.has_value()) {
    return;
  }

  SealedCredentials bad_ciphertext = *sealed.envelope;
  bad_ciphertext.ciphertext[0] =
      bad_ciphertext.ciphertext[0] == 'a' ? 'b' : 'a';
  CHECK_STATUS(authenticator
                   .open_credentials(*result.binding, bad_ciphertext,
                                     transport.transport_exporter)
                   .status,
               Status::RejectedCredentials);

  SealedCredentials bad_tag = *sealed.envelope;
  bad_tag.tag[0] = bad_tag.tag[0] == 'a' ? 'b' : 'a';
  CHECK_STATUS(authenticator
                   .open_credentials(*result.binding, bad_tag,
                                     transport.transport_exporter)
                   .status,
               Status::RejectedCredentials);

  SealedCredentials wrong_session = *sealed.envelope;
  wrong_session.session_id = "ffffffffffffffffffffffffffffffff";
  CHECK_STATUS(authenticator
                   .open_credentials(*result.binding, wrong_session,
                                     transport.transport_exporter)
                   .status,
               Status::RejectedSession);

  const CredentialOpenResult opened = authenticator.open_credentials(
      *result.binding, *sealed.envelope, transport.transport_exporter);
  CHECK_STATUS(opened.status, Status::Ok);
  if (opened.credentials.has_value()) {
    CHECK(opened.credentials->password == credentials.password);
  }
  CHECK_STATUS(authenticator
                   .open_credentials(*result.binding, *sealed.envelope,
                                     transport.transport_exporter)
                   .status,
               Status::RejectedReplay);

  const auto events = authenticator.drain_audit_log();
  CHECK(audit_contains(events, AuditEventType::CredentialsRejected,
                       Status::RejectedCredentials));
  CHECK(audit_contains(events, AuditEventType::CredentialsAccepted, Status::Ok));
  CHECK(!contains_text(make_bytes(join_audit(events)), kPassword));
}

void test_binding_is_bound_to_policy_and_serialization_roundtrips() {
  ServerPolicy policy = strong_policy();
  const std::string original_digest = policy_digest(policy);

  policy.require_nla = false;
  CHECK(policy_digest(policy) != original_digest);
  policy = strong_policy();
  policy.allowed_methods = {AuthMethod::RdpStandard, AuthMethod::CredSsp};
  CHECK(policy_digest(policy) != original_digest);
  policy = strong_policy();
  policy.pinned_certificate_sha256 = {
      fingerprint_certificate(replacement_certificate())};
  CHECK(policy_digest(policy) != original_digest);
  policy = strong_policy();
  policy.policy_revision = "rotated-policy";
  CHECK(policy_digest(policy) != original_digest);

  Authenticator authenticator(strong_policy(), deterministic_options());
  const ClientHello hello =
      make_hello(strong_policy(), good_certificate(), {AuthMethod::CredSsp},
                 {10, 12}, make_nonce(101));
  const NegotiationResult result = authenticator.negotiate(
      hello, make_transport("10.20.9.15", good_certificate(), 101));
  CHECK_STATUS(result.status, Status::Ok);
  if (!result.binding.has_value()) {
    return;
  }

  const Bytes wire = serialize_binding(*result.binding);
  const auto parsed = parse_binding(wire);
  CHECK(parsed.has_value());
  if (parsed.has_value()) {
    CHECK(same_binding(*parsed, *result.binding));
  }
  CHECK(!parse_binding(std::span<const std::uint8_t>(wire).first(10U))
             .has_value());

  const CredentialSealResult sealed = seal_credentials(
      *result.binding, make_exporter(101),
      CredentialPayload{"CORP", "carol", std::string(kPassword)}, 1, kNow);
  CHECK_STATUS(sealed.status, Status::Ok);
  if (sealed.envelope.has_value()) {
    const auto parsed_envelope =
        parse_credentials(serialize_credentials(*sealed.envelope));
    CHECK(parsed_envelope.has_value());
    if (parsed_envelope.has_value()) {
      CHECK(parsed_envelope->session_id == sealed.envelope->session_id);
      CHECK(parsed_envelope->nonce == sealed.envelope->nonce);
      CHECK(parsed_envelope->ciphertext == sealed.envelope->ciphertext);
      CHECK(parsed_envelope->tag == sealed.envelope->tag);
      CHECK(parsed_envelope->sequence == sealed.envelope->sequence);
    }
  }

  ClientExpectations expectations = make_expectations(
      strong_policy(), hello, 101, AuthMethod::CredSsp, true);
  expectations.policy_digest = policy_digest(strong_policy());
  CHECK_STATUS(verify_binding(*result.binding, expectations, kNow),
               Status::Ok);
  expectations.policy_digest =
      "0000000000000000000000000000000000000000000000000000000000000000";
  CHECK_STATUS(verify_binding(*result.binding, expectations, kNow),
               Status::RejectedPolicy);
}

void test_invalid_policy_fails_closed() {
  ServerPolicy policy = strong_policy();
  policy.pinned_certificate_sha256.clear();
  Authenticator authenticator(policy, deterministic_options());
  CHECK_STATUS(authenticator.policy_status(), Status::RejectedPolicy);

  const ClientHello hello =
      make_hello(policy, good_certificate(), {AuthMethod::CredSsp},
                 {10, 12}, make_nonce(111));
  const NegotiationResult result = authenticator.negotiate(
      hello, make_transport("10.20.9.15", good_certificate(), 111));
  CHECK_STATUS(result.status, Status::RejectedPolicy);
  CHECK(!result.binding.has_value());

  ServerPolicy no_nla_support = strong_policy();
  no_nla_support.allowed_methods = {AuthMethod::Tls, AuthMethod::RdpStandard};
  Authenticator second(no_nla_support, deterministic_options());
  CHECK_STATUS(second.policy_status(), Status::RejectedPolicy);
}

void test_cidr_and_hex_helpers() {
  CHECK(cidr_contains("10.20.0.0/16", "10.20.44.9"));
  CHECK(cidr_contains("10.20.0.0/16", "10.20.44.9:3389"));
  CHECK(!cidr_contains("10.20.0.0/16", "10.21.44.9"));
  CHECK(cidr_contains("192.168.1.0/24", "192.168.1.200"));
  CHECK(!cidr_contains("192.168.1.0/24", "192.168.2.1"));
  CHECK(!cidr_contains("not-a-cidr", "10.20.44.9"));

  const Bytes bytes = make_bytes("wire-bytes");
  const auto decoded = from_hex(to_hex(bytes));
  CHECK(decoded.has_value());
  if (decoded.has_value()) {
    CHECK(*decoded == bytes);
  }
  CHECK(!from_hex("abc").has_value());
  CHECK(!from_hex("zz").has_value());
}

void test_sha256_known_answers() {
  CHECK(sha256_hex(make_bytes("")) ==
        "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855");
  CHECK(sha256_hex(make_bytes("abc")) ==
        "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
  CHECK(sha256_hex(make_bytes(
            "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq")) ==
        "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1");
}

struct TestCase {
  const char* name;
  void (*function)();
};

}  // namespace

int main() {
  const std::array<TestCase, 13> tests{{
      {"legitimate strong auth and wire capture",
       test_legitimate_strong_auth_and_wire_capture},
      {"NLA downgrade rejected", test_nla_downgrade_is_rejected},
      {"downgrade tampering detected", test_downgrade_tampering_is_detected},
      {"certificate substitution rejected",
       test_certificate_substitution_is_rejected},
      {"protocol version rollback rejected",
       test_protocol_version_rollback_is_rejected},
      {"weak auth requires explicit isolated policy",
       test_weak_auth_requires_explicit_isolated_policy},
      {"strong-capable client is not downgraded",
       test_strong_capable_client_is_not_silently_downgraded},
      {"client detects stripped strong offer",
       test_client_detects_stripped_strong_offer},
      {"tickets are bound to sessions", test_tickets_are_bound_to_sessions},
      {"credential envelope tampering and replay",
       test_credential_envelope_tampering_and_replay},
      {"binding and serialization round trips",
       test_binding_is_bound_to_policy_and_serialization_roundtrips},
      {"invalid policy fails closed", test_invalid_policy_fails_closed},
      {"SHA-256 known answers", test_sha256_known_answers},
  }};

  for (const TestCase& test : tests) {
    std::cerr << "[ RUN      ] " << test.name << '\n';
    test.function();
    std::cerr << "[ COMPLETE ] " << test.name << '\n';
  }
  test_cidr_and_hex_helpers();

  std::cerr << g_checks << " checks, " << g_failures << " failure(s)\n";
  return g_failures == 0 ? 0 : 1;
}
