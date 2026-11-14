#include "netsec.hpp"

#include <chrono>
#include <iostream>
#include <memory>
#include <string>
#include <vector>

namespace {

using namespace netsec;

int failures = 0;

#define CHECK(condition)                                                     \
  do {                                                                       \
    if (!(condition)) {                                                      \
      std::cerr << __FILE__ << ":" << __LINE__ << ": check failed: "         \
                << #condition << "\n";                                       \
      ++failures;                                                            \
    }                                                                        \
  } while (false)

TimePoint at_seconds(std::int64_t base_offset, std::int64_t offset = 0) {
  return TimePoint(std::chrono::seconds(1'700'000'000 + base_offset +
                                        offset));
}

CertificateInfo good_leaf(std::string host = "mail.example.com",
                          std::string fingerprint = "aabbccdd") {
  CertificateInfo certificate;
  certificate.subject = "CN=" + host;
  certificate.issuer = "CN=Example Test CA";
  certificate.common_name = host;
  certificate.dns_names = {std::move(host)};
  certificate.der_sha256 = std::move(fingerprint);
  certificate.spki_sha256 = "11223344";
  certificate.not_before = at_seconds(0);
  certificate.not_after = at_seconds(10'000);
  certificate.web_pki_trusted = true;
  return certificate;
}

CertificateChain good_chain(std::string host = "mail.example.com",
                            std::string fingerprint = "aabbccdd") {
  CertificateChain chain;
  chain.certificates.push_back(good_leaf(std::move(host),
                                         std::move(fingerprint)));
  chain.trusted = true;
  return chain;
}

ConnectionRecord encrypted_record(std::string host = "mail.example.com",
                                  std::uint16_t port = 587) {
  ConnectionRecord record;
  record.host = std::move(host);
  record.port = port;
  record.channel = ChannelState::Plaintext;
  record.advertised_capabilities = {"SIZE 1000000", "STARTTLS",
                                    "AUTH PLAIN LOGIN", "8BITMIME"};
  record.starttls = StartTlsResult::Completed;
  record.tls_version = "TLSv1.3";
  record.cipher_suite = "TLS_AES_256_GCM_SHA384";
  record.certificate_chain = good_chain("mail.example.com");
  return record;
}

void test_ehlo_and_host_helpers() {
  std::string ehlo =
      "250-mail.example.com\r\n"
      "250-STARTTLS\r\n"
      "250-AUTH PLAIN LOGIN\r\n"
      "250-SIZE 1000000\r\n"
      "250 8BITMIME\r\n";
  std::vector<std::string> capabilities = parse_ehlo_capabilities(ehlo);
  CHECK(has_capability(capabilities, "starttls"));
  CHECK(has_capability(capabilities, "AUTH"));
  CHECK(has_capability(capabilities, "SIZE"));
  std::vector<std::string> mechanisms = auth_mechanisms(capabilities);
  CHECK(mechanisms.size() == 2);
  CHECK(auth_mechanism_advertised(capabilities, "plain"));
  CHECK(auth_mechanism_advertised(capabilities, "LOGIN"));
  CHECK(!auth_mechanism_advertised(capabilities, "CRAM-MD5"));

  std::vector<std::string> stripped =
      parse_ehlo_capabilities("STARTTLS\r\nAUTH PLAIN\r\nSIZE 1000");
  CHECK(has_capability(stripped, "STARTTLS"));
  CHECK(has_capability(stripped, "AUTH"));

  CHECK(host_matches_dns_name("mail.example.com", "MAIL.EXAMPLE.COM."));
  CHECK(host_matches_dns_name("mx1.example.com", "*.example.com"));
  CHECK(!host_matches_dns_name("deep.mx1.example.com", "*.example.com"));
  CHECK(!host_matches_dns_name("example.com", "*.example.com"));
  CHECK(is_loopback_host("localhost"));
  CHECK(is_loopback_host("127.0.0.1"));
  CHECK(is_loopback_host("[::1]"));
  CHECK(!is_loopback_host("mail.example.com"));
  CHECK(normalize_hex("AA:bb-CC dd") == "aabbccdd");
}

void test_capability_stripping_aborts_before_secrets() {
  auto cache = std::make_shared<PolicyCache>();
  SubmissionSession session(cache, SubmissionPolicyConfig{});

  ConnectionRecord record;
  record.host = "mail.example.com";
  record.port = 587;
  record.advertised_capabilities = {"SIZE 1000000", "AUTH PLAIN LOGIN"};
  record.starttls = StartTlsResult::NotAdvertised;
  record.channel = ChannelState::Plaintext;

  PolicyVerdict verdict = session.inspect(record, at_seconds(1));
  CHECK(verdict.decision == Decision::Deny);
  CHECK(verdict.reason == Reason::StartTlsNotAdvertised);
  CHECK(!verdict.tls_active);

  const std::string password = "S3cret-Password!";
  const std::string body = "Confidential quarterly numbers";
  CHECK(!session.authorize_auth("PLAIN").allowed);
  CHECK(!session.authorize_content().allowed);

  WireTranscript transcript;
  transcript.append_plaintext("EHLO", "EHLO mail.example.com");
  if (session.authorize_auth("PLAIN").allowed) {
    transcript.append_plaintext("AUTH", "AUTH PLAIN " + password);
  }
  if (session.authorize_content().allowed) {
    transcript.append_plaintext("DATA", body);
  }
  CHECK(!transcript.plaintext_contains(password));
  CHECK(!transcript.plaintext_contains(body));
}

void test_certificate_replacement_is_rejected() {
  auto cache = std::make_shared<PolicyCache>();
  SubmissionPolicyConfig config;

  {
    SubmissionSession session(cache, config);
    ConnectionRecord record = encrypted_record();
    record.certificate_chain = good_chain("mail.attacker.example");
    PolicyVerdict verdict = session.inspect(record, at_seconds(2));
    CHECK(verdict.decision == Decision::Deny);
    CHECK(verdict.reason == Reason::CertificateHostnameMismatch);
    CHECK(!session.authorize_auth("PLAIN").allowed);
    CHECK(!session.authorize_content().allowed);
  }

  {
    SubmissionSession session(cache, config);
    ConnectionRecord record = encrypted_record();
    CertificateInfo leaf = good_leaf();
    leaf.common_name = "mail.example.com";
    leaf.dns_names = {"mail.attacker.example"};
    record.certificate_chain.certificates = {leaf};
    PolicyVerdict verdict = session.inspect(record, at_seconds(2));
    CHECK(verdict.decision == Decision::Deny);
    CHECK(verdict.reason == Reason::CertificateHostnameMismatch);
  }

  {
    SubmissionSession session(cache, config);
    ConnectionRecord record = encrypted_record();
    CertificateInfo leaf = good_leaf();
    leaf.web_pki_trusted = false;
    record.certificate_chain.certificates = {leaf};
    record.certificate_chain.trusted = false;
    PolicyVerdict verdict = session.inspect(record, at_seconds(2));
    CHECK(verdict.decision == Decision::Deny);
    CHECK(verdict.reason == Reason::CertificateUntrusted);
  }

  {
    SubmissionSession session(cache, config);
    ConnectionRecord record = encrypted_record();
    PolicyVerdict verdict = session.inspect(record, at_seconds(2));
    CHECK(verdict.allowed());
    CHECK(verdict.reason == Reason::Ok);
  }
}

void test_dane_fingerprint_and_expiry() {
  SubmissionPolicyConfig config;

  DanePolicy dane;
  dane.domain = "example.com";
  dane.has_tlsa = true;
  dane.tlsa_records.push_back(
      TlsaRecord{3, 1, 1, "aa:bb:cc:dd"});
  dane.valid_from = at_seconds(0);
  dane.expires_at = at_seconds(100);

  {
    auto cache = std::make_shared<PolicyCache>();
    cache->store(dane);
    SubmissionSession session(cache, config);
    ConnectionRecord record = encrypted_record();
    record.policy_domain = "example.com";
    record.certificate_chain = good_chain("mail.example.com", "deadbeef");
    PolicyVerdict verdict = session.inspect(record, at_seconds(50));
    CHECK(verdict.decision == Decision::Deny);
    CHECK(verdict.reason == Reason::DaneMismatch);
  }

  {
    auto cache = std::make_shared<PolicyCache>();
    cache->store(dane);
    SubmissionSession session(cache, config);
    ConnectionRecord record = encrypted_record();
    record.policy_domain = "example.com";
    CertificateInfo leaf = good_leaf("mail.example.com", "aabbccdd");
    leaf.spki_sha256 = "aabbccdd";
    leaf.web_pki_trusted = false;
    record.certificate_chain.certificates = {leaf};
    record.certificate_chain.trusted = false;
    PolicyVerdict verdict = session.inspect(record, at_seconds(50));
    CHECK(verdict.allowed());
    CHECK(verdict.reason == Reason::Ok);
  }

  {
    auto cache = std::make_shared<PolicyCache>();
    cache->store(dane);
    SubmissionSession session(cache, config);
    ConnectionRecord record = encrypted_record();
    record.policy_domain = "example.com";
    PolicyVerdict verdict = session.inspect(record, at_seconds(150));
    CHECK(verdict.decision == Decision::Deny);
    CHECK(verdict.reason == Reason::PolicyExpired);
  }
}

void test_mta_sts_expiry_and_cache_binding() {
  SubmissionPolicyConfig config;
  MtaStsPolicy policy;
  policy.domain = "example.com";
  policy.mode = MtaStsMode::Enforce;
  policy.mx_patterns = {"mail.example.com"};
  policy.valid_from = at_seconds(0);
  policy.expires_at = at_seconds(100);

  {
    auto cache = std::make_shared<PolicyCache>();
    cache->store(policy);
    SubmissionSession session(cache, config);
    ConnectionRecord record = encrypted_record();
    record.policy_domain = "EXAMPLE.COM.";
    PolicyVerdict verdict = session.inspect(record, at_seconds(50));
    CHECK(verdict.allowed());
    CHECK(verdict.tls_required);
  }

  {
    auto cache = std::make_shared<PolicyCache>();
    cache->store(policy);
    SubmissionSession session(cache, config);
    ConnectionRecord record = encrypted_record();
    record.policy_domain = "example.com";
    PolicyVerdict verdict = session.inspect(record, at_seconds(100));
    CHECK(verdict.decision == Decision::Deny);
    CHECK(verdict.reason == Reason::PolicyExpired);
  }

  {
    auto cache = std::make_shared<PolicyCache>();
    MtaStsPolicy future = policy;
    future.valid_from = at_seconds(200);
    cache->store(future);
    SubmissionSession session(cache, config);
    ConnectionRecord record = encrypted_record();
    record.policy_domain = "example.com";
    PolicyVerdict verdict = session.inspect(record, at_seconds(50));
    CHECK(verdict.decision == Decision::Deny);
    CHECK(verdict.reason == Reason::PolicyNotYetValid);
  }
}

void test_retry_cannot_switch_port() {
  auto cache = std::make_shared<PolicyCache>();
  SubmissionSession session(cache, SubmissionPolicyConfig{});

  ConnectionRecord first;
  first.host = "mail.example.com";
  first.port = 587;
  first.advertised_capabilities = {"AUTH PLAIN LOGIN"};
  first.starttls = StartTlsResult::NotAdvertised;
  PolicyVerdict first_verdict = session.inspect(first, at_seconds(3));
  CHECK(first_verdict.decision == Decision::Deny);
  CHECK(first_verdict.reason == Reason::StartTlsNotAdvertised);

  ConnectionRecord fallback = encrypted_record("mail.example.com", 25);
  PolicyVerdict fallback_verdict = session.inspect(fallback, at_seconds(4));
  CHECK(fallback_verdict.decision == Decision::Deny);
  CHECK(fallback_verdict.reason == Reason::RetryPortChanged);
  CHECK(!session.authorize_auth("PLAIN").allowed);
  CHECK(!session.authorize_content().allowed);

  CHECK(session.aborted());
  CHECK(session.pinned_endpoint().has_value());
  CHECK(session.pinned_endpoint()->second == 587);
}

void test_same_port_retry_after_handshake_failure_is_aborted() {
  auto cache = std::make_shared<PolicyCache>();
  SubmissionSession session(cache, SubmissionPolicyConfig{});

  ConnectionRecord failed = encrypted_record();
  failed.starttls = StartTlsResult::HandshakeFailed;
  PolicyVerdict failed_verdict = session.inspect(failed, at_seconds(5));
  CHECK(failed_verdict.decision == Decision::Deny);
  CHECK(failed_verdict.reason == Reason::TlsHandshakeFailed);

  ConnectionRecord retry = encrypted_record();
  PolicyVerdict retry_verdict = session.inspect(retry, at_seconds(6));
  CHECK(retry_verdict.decision == Decision::Deny);
  CHECK(retry_verdict.reason == Reason::SessionAborted);
}

void test_legitimate_encrypted_submission() {
  auto cache = std::make_shared<PolicyCache>();
  SubmissionSession session(cache, SubmissionPolicyConfig{});

  ConnectionRecord preflight = encrypted_record();
  preflight.starttls = StartTlsResult::NotAttempted;
  preflight.certificate_chain = {};
  PolicyVerdict preflight_verdict =
      session.inspect(preflight, at_seconds(7));
  CHECK(preflight_verdict.requires_starttls());
  CHECK(!session.authorize_auth("PLAIN").allowed);
  CHECK(!session.authorize_content().allowed);

  ConnectionRecord encrypted = encrypted_record();
  PolicyVerdict verdict = session.inspect(encrypted, at_seconds(8));
  CHECK(verdict.allowed());
  CHECK(verdict.tls_active);

  AuthVerdict auth = session.authorize_auth("PLAIN");
  ContentVerdict content = session.authorize_content();
  CHECK(auth.allowed);
  CHECK(content.allowed);

  const std::string password = "correct horse battery staple";
  const std::string body = "Please find the confidential report attached.";
  WireTranscript transcript;
  transcript.append_plaintext("EHLO", "EHLO mail.example.com");
  transcript.append_plaintext("STARTTLS", "STARTTLS");
  if (auth.allowed) {
    transcript.append_tls("AUTH", "AUTH PLAIN " + password);
  }
  if (content.allowed) {
    transcript.append_tls("DATA", body);
  }
  CHECK(!transcript.plaintext_contains(password));
  CHECK(!transcript.plaintext_contains(body));
  CHECK(transcript.plaintext_matches(password) == 0);
  CHECK(transcript.plaintext_matches(body) == 0);
}

void test_encryption_downgrade_on_same_endpoint_is_rejected() {
  auto cache = std::make_shared<PolicyCache>();
  SubmissionPolicyConfig config;
  config.encryption = EncryptionMode::Opportunistic;
  SubmissionSession session(cache, config);

  ConnectionRecord implicit_tls = encrypted_record();
  implicit_tls.channel = ChannelState::ImplicitTls;
  implicit_tls.starttls = StartTlsResult::NotAttempted;
  PolicyVerdict first = session.inspect(implicit_tls, at_seconds(9));
  CHECK(first.allowed());

  ConnectionRecord plaintext = encrypted_record();
  plaintext.starttls = StartTlsResult::NotAttempted;
  plaintext.certificate_chain = {};
  PolicyVerdict downgrade = session.inspect(plaintext, at_seconds(10));
  CHECK(downgrade.decision == Decision::Deny);
  CHECK(downgrade.reason == Reason::EncryptionDowngrade);
}

void test_loopback_exception_is_explicit() {
  ConnectionRecord loopback;
  loopback.host = "127.0.0.1";
  loopback.port = 2525;
  loopback.advertised_capabilities = {"AUTH PLAIN"};
  loopback.starttls = StartTlsResult::NotAdvertised;

  {
    auto cache = std::make_shared<PolicyCache>();
    SubmissionSession session(cache, SubmissionPolicyConfig{});
    PolicyVerdict verdict = session.inspect(loopback, at_seconds(11));
    CHECK(verdict.decision == Decision::Deny);
    CHECK(verdict.reason == Reason::StartTlsNotAdvertised);
    CHECK(!session.authorize_auth("PLAIN").allowed);
  }

  {
    auto cache = std::make_shared<PolicyCache>();
    SubmissionPolicyConfig config;
    config.allow_plaintext_auth_on_loopback = true;
    SubmissionSession session(cache, config);
    PolicyVerdict verdict = session.inspect(loopback, at_seconds(11));
    CHECK(verdict.allowed());
    CHECK(verdict.reason == Reason::PlaintextLoopbackAllowed);
    CHECK(verdict.plaintext_auth_permitted);
    CHECK(!verdict.plaintext_content_permitted);
    CHECK(session.authorize_auth("PLAIN").allowed);
    CHECK(!session.authorize_content().allowed);
  }

  {
    auto cache = std::make_shared<PolicyCache>();
    SubmissionSession session(cache, SubmissionPolicyConfig{});
    ConnectionRecord secure = encrypted_record("localhost", 587);
    secure.certificate_chain = good_chain("localhost");
    PolicyVerdict verdict = session.inspect(secure, at_seconds(12));
    CHECK(verdict.allowed());

    const std::string password = "loopback-password";
    const std::string body = "loopback body";
    WireTranscript transcript;
    transcript.append_plaintext("EHLO", "EHLO localhost");
    transcript.append_plaintext("STARTTLS", "STARTTLS");
    transcript.append_tls("AUTH", "AUTH PLAIN " + password);
    transcript.append_tls("DATA", body);
    CHECK(!transcript.plaintext_contains(password));
    CHECK(!transcript.plaintext_contains(body));
  }
}

void test_auth_still_requires_tls_in_opportunistic_mode() {
  auto cache = std::make_shared<PolicyCache>();
  SubmissionPolicyConfig config;
  config.encryption = EncryptionMode::Opportunistic;
  SubmissionSession session(cache, config);

  ConnectionRecord record;
  record.host = "mail.example.com";
  record.port = 587;
  record.advertised_capabilities = {"AUTH PLAIN"};
  record.starttls = StartTlsResult::NotAdvertised;
  PolicyVerdict verdict = session.inspect(record, at_seconds(14));
  CHECK(verdict.allowed());
  CHECK(!session.authorize_auth("PLAIN").allowed);
  CHECK(session.authorize_content().allowed);
}

void test_loopback_exception_does_not_bypass_starttls_or_failure() {
  SubmissionPolicyConfig config;
  config.allow_plaintext_auth_on_loopback = true;

  {
    auto cache = std::make_shared<PolicyCache>();
    SubmissionSession session(cache, config);
    ConnectionRecord record;
    record.host = "localhost";
    record.port = 587;
    record.advertised_capabilities = {"STARTTLS", "AUTH PLAIN"};
    record.starttls = StartTlsResult::NotAttempted;
    PolicyVerdict verdict = session.inspect(record, at_seconds(15));
    CHECK(verdict.requires_starttls());
    CHECK(!session.authorize_auth("PLAIN").allowed);
  }

  {
    auto cache = std::make_shared<PolicyCache>();
    SubmissionSession session(cache, config);
    ConnectionRecord record;
    record.host = "127.0.0.1";
    record.port = 2525;
    record.advertised_capabilities = {"AUTH PLAIN"};
    record.starttls = StartTlsResult::HandshakeFailed;
    PolicyVerdict verdict = session.inspect(record, at_seconds(16));
    CHECK(verdict.decision == Decision::Deny);
    CHECK(verdict.reason == Reason::TlsHandshakeFailed);
    CHECK(!session.authorize_auth("PLAIN").allowed);
  }
}

void test_policy_cache_per_domain_and_expiry() {
  PolicyCache cache;
  MtaStsPolicy policy;
  policy.domain = "Example.COM.";
  policy.mode = MtaStsMode::Enforce;
  policy.expires_at = at_seconds(100);
  cache.store(policy);

  CacheResult<MtaStsPolicy> valid =
      cache.mta_sts("example.com", at_seconds(50));
  CHECK(valid.usable());
  CHECK(valid.value->domain == "example.com");

  CacheResult<MtaStsPolicy> expired =
      cache.mta_sts("example.com", at_seconds(100));
  CHECK(expired.state == CacheState::Expired);

  CacheResult<MtaStsPolicy> missing =
      cache.mta_sts("other.example", at_seconds(50));
  CHECK(missing.state == CacheState::Missing);

  cache.remove("EXAMPLE.COM");
  CHECK(cache.mta_sts("example.com", at_seconds(50)).state ==
        CacheState::Missing);
}

void test_allowed_port_allowlist() {
  SubmissionPolicyConfig config;
  config.allowed_ports = {587, 465};
  auto cache = std::make_shared<PolicyCache>();
  SubmissionSession session(cache, config);

  ConnectionRecord record = encrypted_record("mail.example.com", 25);
  PolicyVerdict verdict = session.inspect(record, at_seconds(13));
  CHECK(verdict.decision == Decision::Deny);
  CHECK(verdict.reason == Reason::PortNotAllowed);
}

struct TestCase {
  const char* name;
  void (*function)();
};

}  // namespace

int main() {
  const std::vector<TestCase> tests = {
      {"ehlo_and_host_helpers", test_ehlo_and_host_helpers},
      {"capability_stripping_aborts_before_secrets",
       test_capability_stripping_aborts_before_secrets},
      {"certificate_replacement_is_rejected",
       test_certificate_replacement_is_rejected},
      {"dane_fingerprint_and_expiry", test_dane_fingerprint_and_expiry},
      {"mta_sts_expiry_and_cache_binding",
       test_mta_sts_expiry_and_cache_binding},
      {"retry_cannot_switch_port", test_retry_cannot_switch_port},
      {"same_port_retry_after_handshake_failure_is_aborted",
       test_same_port_retry_after_handshake_failure_is_aborted},
      {"legitimate_encrypted_submission",
       test_legitimate_encrypted_submission},
      {"encryption_downgrade_on_same_endpoint_is_rejected",
       test_encryption_downgrade_on_same_endpoint_is_rejected},
      {"loopback_exception_is_explicit", test_loopback_exception_is_explicit},
      {"auth_still_requires_tls_in_opportunistic_mode",
       test_auth_still_requires_tls_in_opportunistic_mode},
      {"loopback_exception_does_not_bypass_starttls_or_failure",
       test_loopback_exception_does_not_bypass_starttls_or_failure},
      {"policy_cache_per_domain_and_expiry",
       test_policy_cache_per_domain_and_expiry},
      {"allowed_port_allowlist", test_allowed_port_allowlist},
  };

  for (const TestCase& test : tests) {
    int before = failures;
    test.function();
    std::cout << (failures == before ? "[PASS] " : "[FAIL] ") << test.name
              << "\n";
  }

  if (failures != 0) {
    std::cerr << failures << " check(s) failed\n";
    return 1;
  }
  std::cout << "All submission policy tests passed\n";
  return 0;
}
