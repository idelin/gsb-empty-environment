#pragma once

#include <chrono>
#include <compare>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Bytes = std::vector<std::uint8_t>;
using RandomSource = std::function<Bytes(std::size_t)>;
using ClockSource = std::function<std::int64_t()>;

struct ProtocolVersion {
  std::uint16_t major{0};
  std::uint16_t minor{0};

  auto operator<=>(const ProtocolVersion&) const = default;
  std::string to_string() const;
};

enum class AuthMethod : std::uint16_t {
  None = 0,
  RdpStandard = 1,
  Tls = 2,
  CredSsp = 3,
};

bool is_network_level_auth(AuthMethod method) noexcept;
bool is_weak_auth(AuthMethod method) noexcept;
int auth_strength(AuthMethod method) noexcept;
std::string_view to_string(AuthMethod method) noexcept;

enum class Status : std::uint8_t {
  Ok = 0,
  RejectedMalformed,
  RejectedPolicy,
  RejectedServerIdentity,
  RejectedCertificate,
  RejectedProtocolVersion,
  RejectedNoCommonMethod,
  RejectedNlaRequired,
  RejectedDowngrade,
  RejectedWeakAuthDisabled,
  RejectedWeakNetwork,
  RejectedReplay,
  RejectedExpired,
  RejectedBinding,
  RejectedSession,
  RejectedTicket,
  RejectedCredentials,
  RejectedTransport,
};

std::string_view to_string(Status status) noexcept;

std::string to_hex(std::span<const std::uint8_t> bytes);
std::optional<Bytes> from_hex(std::string_view hex);
std::string sha256_hex(std::span<const std::uint8_t> data);
std::string fingerprint_certificate(std::span<const std::uint8_t> der);

struct NetworkRule {
  std::string cidr;
  bool isolated{false};
};

bool cidr_contains(std::string_view cidr, std::string_view ipv4_address) noexcept;

struct ServerPolicy {
  std::string server_id{"rdp.local"};
  std::string policy_revision{"1"};
  ProtocolVersion minimum_version{10, 0};
  ProtocolVersion maximum_version{10, 12};
  bool require_nla{true};
  bool reject_downgrade{true};
  bool allow_weak{false};
  std::vector<AuthMethod> allowed_methods{AuthMethod::CredSsp};
  std::vector<NetworkRule> isolated_networks;
  std::vector<std::string> pinned_certificate_sha256;
  std::chrono::seconds binding_lifetime{60};
  std::chrono::seconds ticket_lifetime{600};
};

std::string policy_digest(const ServerPolicy& policy);

struct ClientHello {
  std::string server_id;
  std::string expected_certificate_sha256;
  std::vector<AuthMethod> offered_methods;
  ProtocolVersion version{10, 12};
  std::string client_nonce;
  bool requires_nla{true};
};

struct TransportContext {
  std::string peer_address;
  Bytes presented_certificate;
  Bytes transport_exporter;
};

struct NegotiationBinding {
  std::string server_id;
  std::string policy_digest;
  std::string certificate_sha256;
  std::vector<AuthMethod> offered_methods;
  ProtocolVersion requested_version;
  ProtocolVersion negotiated_version;
  AuthMethod method{AuthMethod::None};
  std::string peer_address;
  std::string client_nonce;
  std::string server_nonce;
  std::string session_id;
  std::string ticket;
  std::int64_t issued_at{0};
  std::int64_t expires_at{0};
  std::string mac;
};

struct NegotiationResult {
  Status status{Status::RejectedMalformed};
  std::optional<NegotiationBinding> binding;
  std::string detail;

  explicit operator bool() const noexcept {
    return status == Status::Ok && binding.has_value();
  }
};

struct ClientExpectations {
  std::string server_id;
  std::string policy_digest;
  std::string observed_certificate_sha256;
  std::vector<AuthMethod> offered_methods;
  ProtocolVersion offered_version;
  std::string client_nonce;
  AuthMethod minimum_method{AuthMethod::CredSsp};
  bool requires_nla{true};
  Bytes transport_exporter;
};

Status verify_binding(const NegotiationBinding& binding,
                      const ClientExpectations& expectations,
                      std::int64_t now_unix = 0);

struct SessionInfo {
  std::string session_id;
  AuthMethod method{AuthMethod::None};
  ProtocolVersion version;
  std::string certificate_sha256;
  std::int64_t expires_at{0};
};

struct CredentialPayload {
  std::string domain;
  std::string username;
  std::string password;
};

struct SealedCredentials {
  std::string session_id;
  std::string nonce;
  std::string ciphertext;
  std::string tag;
  std::uint64_t sequence{0};
};

struct CredentialSealResult {
  Status status{Status::RejectedBinding};
  std::optional<SealedCredentials> envelope;
  std::string detail;

  explicit operator bool() const noexcept {
    return status == Status::Ok && envelope.has_value();
  }
};

struct CredentialOpenResult {
  Status status{Status::RejectedCredentials};
  std::optional<CredentialPayload> credentials;
  std::string detail;

  explicit operator bool() const noexcept {
    return status == Status::Ok && credentials.has_value();
  }
};

CredentialSealResult seal_credentials(const NegotiationBinding& binding,
                                      std::span<const std::uint8_t> transport_exporter,
                                      const CredentialPayload& credentials,
                                      std::uint64_t sequence = 0,
                                      std::int64_t now_unix = 0);

Bytes serialize_binding(const NegotiationBinding& binding);
std::optional<NegotiationBinding> parse_binding(std::span<const std::uint8_t> wire);
Bytes serialize_credentials(const SealedCredentials& envelope);
std::optional<SealedCredentials> parse_credentials(std::span<const std::uint8_t> wire);

enum class AuditEventType : std::uint8_t {
  NegotiationAccepted = 0,
  NegotiationRejected,
  WeakAuthAccepted,
  WeakAuthRejected,
  CredentialsAccepted,
  CredentialsRejected,
  TicketAccepted,
  TicketRejected,
  SessionResumed,
  ReplayRejected,
};

std::string_view to_string(AuditEventType type) noexcept;

struct AuditEvent {
  AuditEventType type{AuditEventType::NegotiationRejected};
  Status status{Status::Ok};
  AuthMethod method{AuthMethod::None};
  std::string peer_address;
  std::string session_id;
  std::string detail;
  std::int64_t timestamp{0};
};

std::string format_audit_event(const AuditEvent& event);

struct AuthenticatorOptions {
  RandomSource random;
  ClockSource now_unix;
};

class Authenticator {
 public:
  explicit Authenticator(ServerPolicy policy, AuthenticatorOptions options = {});
  ~Authenticator();

  Authenticator(Authenticator&&) noexcept;
  Authenticator& operator=(Authenticator&&) noexcept;
  Authenticator(const Authenticator&) = delete;
  Authenticator& operator=(const Authenticator&) = delete;

  const ServerPolicy& policy() const noexcept;
  Status policy_status() const noexcept;

  NegotiationResult negotiate(const ClientHello& hello, const TransportContext& transport);

  bool verify_ticket(std::string_view ticket, std::string_view session_id,
                     std::string_view certificate_sha256) const;
  std::optional<SessionInfo> resume_session(std::string_view ticket,
                                             std::string_view session_id);

  CredentialOpenResult open_credentials(const NegotiationBinding& binding,
                                        const SealedCredentials& envelope,
                                        std::span<const std::uint8_t> transport_exporter);

  std::vector<AuditEvent> drain_audit_log();

 private:
  struct Impl;
  std::unique_ptr<Impl> impl_;
};

}  // namespace netsec
