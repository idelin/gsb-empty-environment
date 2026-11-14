#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>
#include <unordered_map>
#include <utility>
#include <vector>

namespace netsec {

using TimePoint = std::chrono::system_clock::time_point;
using ClockFn = std::function<TimePoint()>;

[[nodiscard]] TimePoint now();

// Host names are compared case-insensitively and without a trailing dot.
[[nodiscard]] std::string canonical_host(std::string_view host);
[[nodiscard]] std::string canonical_domain(std::string_view domain);

// Recognises localhost, 127.0.0.0/8 and ::1 without performing DNS resolution.
[[nodiscard]] bool is_loopback_host(std::string_view host);

// Exact match or a single-label "*." wildcard match.
[[nodiscard]] bool host_matches_dns_name(std::string_view host,
                                         std::string_view dns_name);

// Removes separators (colon, space, dash) and lowercases hexadecimal data.
[[nodiscard]] std::string normalize_hex(std::string_view value);

// Accepts either a complete EHLO response or already-stripped extension lines.
[[nodiscard]] std::vector<std::string> parse_ehlo_capabilities(
    std::string_view ehlo_response);
[[nodiscard]] bool has_capability(
    const std::vector<std::string>& capabilities, std::string_view name);
[[nodiscard]] std::vector<std::string> auth_mechanisms(
    const std::vector<std::string>& capabilities);
[[nodiscard]] bool auth_mechanism_advertised(
    const std::vector<std::string>& capabilities, std::string_view mechanism);

enum class ChannelState {
  Plaintext,
  ImplicitTls,
};

enum class StartTlsResult {
  NotAttempted,
  NotAdvertised,
  Rejected,
  HandshakeFailed,
  Completed,
};

struct CertificateInfo {
  std::string subject;
  std::string issuer;
  std::string common_name;
  std::vector<std::string> dns_names;

  // SHA-256/SHA-512 hashes are hexadecimal. der_hex is the complete DER
  // certificate encoded as hexadecimal and exists for TLSA matching type 0.
  std::string der_sha256;
  std::string spki_sha256;
  std::string der_sha512;
  std::string spki_sha512;
  std::string der_hex;

  std::optional<TimePoint> not_before;
  std::optional<TimePoint> not_after;

  // Set on the leaf when the complete chain has been validated by the
  // platform trust store (or by the client's configured trust anchors).
  bool web_pki_trusted = false;
};

struct CertificateChain {
  // Leaf certificate first, then intermediates in issuing order.
  std::vector<CertificateInfo> certificates;
  bool trusted = false;

  [[nodiscard]] bool empty() const noexcept { return certificates.empty(); }
  [[nodiscard]] const CertificateInfo* leaf() const noexcept {
    return certificates.empty() ? nullptr : &certificates.front();
  }
  [[nodiscard]] bool web_pki_trusted() const noexcept {
    return trusted || (leaf() != nullptr && leaf()->web_pki_trusted);
  }
};

// Everything observed on one SMTP submission connection attempt. No password
// or message body belongs in this record.
struct ConnectionRecord {
  std::string host;
  std::uint16_t port = 0;

  // The policy domain; when empty, host is used.
  std::string policy_domain;

  ChannelState channel = ChannelState::Plaintext;
  std::vector<std::string> advertised_capabilities;
  StartTlsResult starttls = StartTlsResult::NotAttempted;
  std::string tls_version;
  std::string cipher_suite;
  CertificateChain certificate_chain;

  [[nodiscard]] bool tls_active() const noexcept {
    return channel == ChannelState::ImplicitTls ||
           starttls == StartTlsResult::Completed;
  }
  [[nodiscard]] bool starttls_advertised() const {
    return has_capability(advertised_capabilities, "STARTTLS");
  }
  [[nodiscard]] std::string domain() const {
    return canonical_domain(policy_domain.empty() ? host : policy_domain);
  }
  [[nodiscard]] bool has_auth_capability() const {
    return has_capability(advertised_capabilities, "AUTH");
  }
  [[nodiscard]] bool advertises_auth_mechanism(
      std::string_view mechanism) const {
    return auth_mechanism_advertised(advertised_capabilities, mechanism);
  }
};

enum class MtaStsMode {
  None,
  Testing,
  Enforce,
};

struct MtaStsPolicy {
  std::string domain;
  MtaStsMode mode = MtaStsMode::None;
  std::vector<std::string> mx_patterns;
  std::optional<TimePoint> fetched_at;
  std::optional<TimePoint> valid_from;
  // Required. A cache entry without an expiry is treated as expired.
  std::optional<TimePoint> expires_at;
};

// RFC 6698 TLSA fields. usage: PKIX-TA=0, PKIX-EE=1, DANE-TA=2, DANE-EE=3.
// selector: full certificate=0, SPKI=1. matching_type: full=0, SHA-256=1,
// SHA-512=2.
struct TlsaRecord {
  std::uint8_t usage = 3;
  std::uint8_t selector = 1;
  std::uint8_t matching_type = 1;
  std::string association_data;
};

struct DanePolicy {
  std::string domain;
  bool has_tlsa = false;
  std::vector<TlsaRecord> tlsa_records;
  std::optional<TimePoint> fetched_at;
  std::optional<TimePoint> valid_from;
  // Required. A cache entry without an expiry is treated as expired.
  std::optional<TimePoint> expires_at;
};

enum class CacheState {
  Missing,
  Valid,
  NotYetValid,
  Expired,
};

template <typename T>
struct CacheResult {
  CacheState state = CacheState::Missing;
  std::optional<T> value;

  [[nodiscard]] bool present() const noexcept {
    return state != CacheState::Missing;
  }
  [[nodiscard]] bool usable() const noexcept {
    return state == CacheState::Valid && value.has_value();
  }
};

class PolicyCache {
 public:
  void store(const MtaStsPolicy& policy);
  void store(const DanePolicy& policy);
  void remove(std::string_view domain);

  [[nodiscard]] CacheResult<MtaStsPolicy> mta_sts(std::string_view domain,
                                                  TimePoint at) const;
  [[nodiscard]] CacheResult<DanePolicy> dane(std::string_view domain,
                                             TimePoint at) const;

  [[nodiscard]] std::size_t size() const;
  void clear();

 private:
  mutable std::mutex mutex_;
  std::unordered_map<std::string, MtaStsPolicy> mta_sts_;
  std::unordered_map<std::string, DanePolicy> dane_;
};

enum class EncryptionMode {
  Opportunistic,
  Required,
};

struct SubmissionPolicyConfig {
  EncryptionMode encryption = EncryptionMode::Required;

  // Require a platform-trusted chain for TLS, unless a DANE-EE/DANE-TA record
  // supplies the trust anchor and MTA-STS is not enforcing PKIX.
  bool require_web_pki = true;
  bool allow_dane_without_web_pki = true;

  // This exception must be requested explicitly. It never permits plaintext
  // message content unless allow_plaintext_content_on_loopback is also true.
  bool allow_plaintext_auth_on_loopback = false;
  bool allow_plaintext_content_on_loopback = false;

  // A retry must stay on the exact host and port of the first attempt.
  bool pin_retry_endpoint = true;

  // Empty means any port is accepted on the first attempt.
  std::vector<std::uint16_t> allowed_ports;

  // Empty means any AUTH mechanism advertised by the server is accepted.
  std::vector<std::string> allowed_auth_mechanisms;
};

enum class Decision {
  Allow,
  RequireStartTls,
  Deny,
};

enum class Reason {
  Ok,
  PlaintextLoopbackAllowed,
  StartTlsRequired,
  StartTlsNotAdvertised,
  StartTlsRejected,
  TlsHandshakeFailed,
  TlsStateInconsistent,
  PolicyExpired,
  PolicyNotYetValid,
  MtaStsHostMismatch,
  CertificateMissing,
  CertificateUntrusted,
  CertificateNotYetValid,
  CertificateExpired,
  CertificateHostnameMismatch,
  DaneMismatch,
  PortNotAllowed,
  RetryPortChanged,
  RetryHostChanged,
  EncryptionDowngrade,
  SessionAborted,
  InvalidConnection,
  AuthNotConnected,
  AuthConnectionDenied,
  AuthCapabilityMissing,
  AuthMechanismNotAllowed,
  AuthRequiresTls,
  ContentRequiresTls,
};

[[nodiscard]] const char* to_string(Decision decision);
[[nodiscard]] const char* to_string(Reason reason);

struct PolicyVerdict {
  Decision decision = Decision::Deny;
  Reason reason = Reason::InvalidConnection;
  std::string detail;
  bool tls_active = false;
  bool loopback = false;
  bool tls_required = false;
  bool plaintext_auth_permitted = false;
  bool plaintext_content_permitted = false;

  [[nodiscard]] bool allowed() const noexcept {
    return decision == Decision::Allow;
  }
  [[nodiscard]] bool requires_starttls() const noexcept {
    return decision == Decision::RequireStartTls;
  }
};

struct AuthVerdict {
  bool allowed = false;
  Reason reason = Reason::AuthNotConnected;
  std::string detail;
};

struct ContentVerdict {
  bool allowed = false;
  Reason reason = Reason::ContentRequiresTls;
  std::string detail;
};

// Stateless policy evaluation. It does not remember retries.
[[nodiscard]] PolicyVerdict evaluate_connection(
    const ConnectionRecord& record, const SubmissionPolicyConfig& config,
    const PolicyCache& cache, TimePoint at);

// Stateful per-client session. It pins the first host/port, remembers a fatal
// connection failure, and prevents a later plaintext downgrade on the same
// endpoint.
class SubmissionSession {
 public:
  explicit SubmissionSession(SubmissionPolicyConfig config = {});
  SubmissionSession(std::shared_ptr<PolicyCache> cache,
                    SubmissionPolicyConfig config = {},
                    ClockFn clock = {});

  [[nodiscard]] PolicyVerdict inspect(const ConnectionRecord& record);
  [[nodiscard]] PolicyVerdict inspect(const ConnectionRecord& record,
                                      TimePoint at);

  [[nodiscard]] AuthVerdict authorize_auth(
      std::string_view mechanism) const;
  [[nodiscard]] ContentVerdict authorize_content() const;

  [[nodiscard]] const std::optional<ConnectionRecord>& connection() const {
    return connection_;
  }
  [[nodiscard]] const std::optional<PolicyVerdict>& last_verdict() const {
    return verdict_;
  }
  [[nodiscard]] std::optional<std::pair<std::string, std::uint16_t>>
  pinned_endpoint() const;
  [[nodiscard]] bool aborted() const noexcept { return aborted_; }

 private:
  [[nodiscard]] PolicyVerdict evaluate_locked(const ConnectionRecord& record,
                                              TimePoint at) const;

  std::shared_ptr<PolicyCache> cache_;
  SubmissionPolicyConfig config_;
  ClockFn clock_;

  std::optional<ConnectionRecord> connection_;
  std::optional<PolicyVerdict> verdict_;
  std::optional<std::pair<std::string, std::uint16_t>> pinned_endpoint_;
  bool secure_established_ = false;
  bool aborted_ = false;
};

enum class WireSecurity {
  Plaintext,
  Tls,
};

struct WireEvent {
  WireSecurity security = WireSecurity::Plaintext;
  std::string label;
  std::string payload;
};

// A small audit log for tests and client diagnostics. Credentials/body are
// never retained by the policy engine; a caller can use this log to prove they
// were written only on the encrypted channel.
class WireTranscript {
 public:
  void append(WireSecurity security, std::string label, std::string payload);
  void append_plaintext(std::string label, std::string payload);
  void append_tls(std::string label, std::string payload);

  [[nodiscard]] bool plaintext_contains(std::string_view secret) const;
  [[nodiscard]] std::size_t plaintext_matches(std::string_view secret) const;
  [[nodiscard]] const std::vector<WireEvent>& events() const noexcept {
    return events_;
  }
  void clear() { events_.clear(); }

 private:
  std::vector<WireEvent> events_;
};

}  // namespace netsec
