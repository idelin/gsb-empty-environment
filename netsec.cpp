#include "netsec.hpp"

#include <algorithm>
#include <cctype>

namespace netsec {
namespace {

std::string trim_copy(std::string_view text) {
  std::size_t first = 0;
  while (first < text.size() &&
         std::isspace(static_cast<unsigned char>(text[first])) != 0) {
    ++first;
  }
  std::size_t last = text.size();
  while (last > first &&
         std::isspace(static_cast<unsigned char>(text[last - 1])) != 0) {
    --last;
  }
  return std::string(text.substr(first, last - first));
}

std::string ascii_lower_copy(std::string_view text) {
  std::string result;
  result.reserve(text.size());
  for (char character : text) {
    result.push_back(static_cast<char>(
        std::tolower(static_cast<unsigned char>(character))));
  }
  return result;
}

std::string ascii_upper_copy(std::string_view text) {
  std::string result;
  result.reserve(text.size());
  for (char character : text) {
    result.push_back(static_cast<char>(
        std::toupper(static_cast<unsigned char>(character))));
  }
  return result;
}

bool ascii_iequals(std::string_view left, std::string_view right) {
  return ascii_lower_copy(left) == ascii_lower_copy(right);
}

std::vector<std::string> split_ascii_whitespace(std::string_view text) {
  std::vector<std::string> tokens;
  std::size_t cursor = 0;
  while (cursor < text.size()) {
    while (cursor < text.size() &&
           std::isspace(static_cast<unsigned char>(text[cursor])) != 0) {
      ++cursor;
    }
    std::size_t start = cursor;
    while (cursor < text.size() &&
           std::isspace(static_cast<unsigned char>(text[cursor])) == 0) {
      ++cursor;
    }
    if (cursor > start) {
      tokens.emplace_back(text.substr(start, cursor - start));
    }
  }
  return tokens;
}

std::string capability_name(std::string_view line) {
  std::string trimmed = trim_copy(line);
  std::size_t end = 0;
  while (end < trimmed.size() &&
         std::isspace(static_cast<unsigned char>(trimmed[end])) == 0 &&
         trimmed[end] != '=') {
    ++end;
  }
  return ascii_upper_copy(std::string_view(trimmed).substr(0, end));
}

bool looks_like_ehlo_greeting(std::string_view line) {
  static constexpr std::string_view known_extensions[] = {
      "STARTTLS", "AUTH",       "SIZE",       "PIPELINING",
      "8BITMIME", "SMTPUTF8",   "CHUNKING",   "BDAT",
      "DSN",      "ENHANCEDSTATUSCODES", "ETRN", "VRFY",
      "EXPN",     "HELP",       "TURN",       "ATRN",
      "BINARYMIME", "DELIVERBY", "REQUIRETLS", "LIMITS",
  };
  std::string trimmed = trim_copy(line);
  if (trimmed.empty() || trimmed.find(' ') != std::string::npos ||
      trimmed.find('\t') != std::string::npos) {
    return false;
  }
  for (std::string_view extension : known_extensions) {
    if (ascii_iequals(trimmed, extension)) {
      return false;
    }
  }
  return true;
}

bool contains_port(const std::vector<std::uint16_t>& ports,
                   std::uint16_t port) {
  return std::find(ports.begin(), ports.end(), port) != ports.end();
}

bool contains_mechanism(const std::vector<std::string>& mechanisms,
                        std::string_view mechanism) {
  for (const std::string& allowed : mechanisms) {
    if (ascii_iequals(allowed, mechanism)) {
      return true;
    }
  }
  return false;
}

bool policy_is_expired(const std::optional<TimePoint>& expires_at,
                       TimePoint at) {
  return !expires_at.has_value() || at >= *expires_at;
}

bool policy_is_not_yet_valid(const std::optional<TimePoint>& valid_from,
                             TimePoint at) {
  return valid_from.has_value() && at < *valid_from;
}

bool certificate_matches_tlsa(const CertificateInfo& certificate,
                              const TlsaRecord& tlsa) {
  std::string expected = normalize_hex(tlsa.association_data);
  if (expected.empty()) {
    return false;
  }

  std::string actual;
  if (tlsa.matching_type == 0) {
    actual = normalize_hex(certificate.der_hex);
  } else if (tlsa.selector == 0 && tlsa.matching_type == 1) {
    actual = normalize_hex(certificate.der_sha256);
  } else if (tlsa.selector == 0 && tlsa.matching_type == 2) {
    actual = normalize_hex(certificate.der_sha512);
  } else if (tlsa.selector == 1 && tlsa.matching_type == 1) {
    actual = normalize_hex(certificate.spki_sha256);
  } else if (tlsa.selector == 1 && tlsa.matching_type == 2) {
    actual = normalize_hex(certificate.spki_sha512);
  } else {
    return false;
  }
  return !actual.empty() && actual == expected;
}

bool identity_matches(const CertificateInfo& certificate,
                      std::string_view host) {
  bool has_subject_alt_name = false;
  for (const std::string& name : certificate.dns_names) {
    if (!name.empty()) {
      has_subject_alt_name = true;
      if (host_matches_dns_name(host, name)) {
        return true;
      }
    }
  }
  return !has_subject_alt_name && !certificate.common_name.empty() &&
         host_matches_dns_name(host, certificate.common_name);
}

PolicyVerdict deny(Reason reason, std::string detail, bool tls_required,
                   bool tls_active, bool loopback) {
  PolicyVerdict verdict;
  verdict.decision = Decision::Deny;
  verdict.reason = reason;
  verdict.detail = std::move(detail);
  verdict.tls_required = tls_required;
  verdict.tls_active = tls_active;
  verdict.loopback = loopback;
  return verdict;
}

}  // namespace

TimePoint now() { return std::chrono::system_clock::now(); }

std::string canonical_host(std::string_view host) {
  std::string trimmed = trim_copy(host);
  if (trimmed.size() >= 2 && trimmed.front() == '[' &&
      trimmed.back() == ']') {
    trimmed = trimmed.substr(1, trimmed.size() - 2);
  }
  std::string result = ascii_lower_copy(trimmed);
  while (!result.empty() && result.back() == '.') {
    result.pop_back();
  }
  return result;
}

std::string canonical_domain(std::string_view domain) {
  return canonical_host(domain);
}

bool is_loopback_host(std::string_view host) {
  std::string name = canonical_host(host);
  if (name.empty()) {
    return false;
  }
  if (name == "localhost" || name.ends_with(".localhost")) {
    return true;
  }
  if (name == "::1" || name == "0:0:0:0:0:0:0:1") {
    return true;
  }
  if (name.starts_with("::1%")) {
    return true;
  }

  std::vector<std::string> parts;
  std::size_t start = 0;
  while (start <= name.size()) {
    std::size_t dot = name.find('.', start);
    if (dot == std::string::npos) {
      parts.push_back(name.substr(start));
      break;
    }
    parts.push_back(name.substr(start, dot - start));
    start = dot + 1;
  }
  if (parts.size() != 4) {
    return false;
  }
  for (const std::string& part : parts) {
    if (part.empty() || part.size() > 3 ||
        !std::all_of(part.begin(), part.end(), [](char character) {
          return std::isdigit(static_cast<unsigned char>(character)) != 0;
        })) {
      return false;
    }
    int octet = 0;
    for (char digit : part) {
      octet = octet * 10 + (digit - '0');
    }
    if (octet > 255) {
      return false;
    }
  }
  return parts.front() == "127";
}

bool host_matches_dns_name(std::string_view host, std::string_view dns_name) {
  std::string host_name = canonical_host(host);
  std::string pattern = canonical_host(dns_name);
  if (host_name.empty() || pattern.empty()) {
    return false;
  }
  if (host_name == pattern) {
    return true;
  }
  if (!pattern.starts_with("*.")) {
    return false;
  }
  std::string suffix = pattern.substr(1);
  if (host_name.size() <= suffix.size() ||
      !host_name.ends_with(suffix)) {
    return false;
  }
  std::string prefix =
      host_name.substr(0, host_name.size() - suffix.size());
  return !prefix.empty() && prefix.find('.') == std::string::npos;
}

std::string normalize_hex(std::string_view value) {
  std::string result;
  result.reserve(value.size());
  for (char character : value) {
    if (character == ':' || character == '-' ||
        std::isspace(static_cast<unsigned char>(character)) != 0) {
      continue;
    }
    result.push_back(static_cast<char>(
        std::tolower(static_cast<unsigned char>(character))));
  }
  if (result.starts_with("0x")) {
    result = result.substr(2);
  }
  return result;
}

std::vector<std::string> parse_ehlo_capabilities(
    std::string_view ehlo_response) {
  std::vector<std::string> capabilities;
  bool first_content_line = true;
  std::size_t start = 0;

  while (start <= ehlo_response.size()) {
    std::size_t newline = ehlo_response.find('\n', start);
    std::string line =
        trim_copy(ehlo_response.substr(start, newline == std::string::npos
                                                 ? std::string::npos
                                                 : newline - start));
    start = newline == std::string::npos ? ehlo_response.size() + 1
                                         : newline + 1;
    if (line.empty()) {
      continue;
    }
    if (line.size() >= 4 && std::isdigit(static_cast<unsigned char>(line[0]))
                         && std::isdigit(static_cast<unsigned char>(line[1]))
                         && std::isdigit(static_cast<unsigned char>(line[2]))
                         && (line[3] == '-' || line[3] == ' ')) {
      line = trim_copy(std::string_view(line).substr(4));
    }
    if (line.empty()) {
      continue;
    }
    if (first_content_line) {
      first_content_line = false;
      if (looks_like_ehlo_greeting(line)) {
        continue;
      }
    }
    capabilities.push_back(std::move(line));
  }
  return capabilities;
}

bool has_capability(const std::vector<std::string>& capabilities,
                    std::string_view name) {
  for (const std::string& line : capabilities) {
    if (ascii_iequals(capability_name(line), name)) {
      return true;
    }
  }
  return false;
}

std::vector<std::string> auth_mechanisms(
    const std::vector<std::string>& capabilities) {
  std::vector<std::string> mechanisms;
  for (const std::string& line : capabilities) {
    if (capability_name(line) != "AUTH") {
      continue;
    }
    std::string trimmed = trim_copy(line);
    std::size_t cursor = 4;
    if (cursor < trimmed.size() && trimmed[cursor] == '=') {
      ++cursor;
    }
    std::size_t before = mechanisms.size();
    std::vector<std::string> tokens =
        split_ascii_whitespace(std::string_view(trimmed).substr(cursor));
    for (std::string token : tokens) {
      std::size_t start = 0;
      while (start <= token.size()) {
        std::size_t comma = token.find(',', start);
        std::string piece =
            token.substr(start, comma == std::string::npos
                                    ? std::string::npos
                                    : comma - start);
        if (!piece.empty()) {
          mechanisms.push_back(ascii_upper_copy(piece));
        }
        if (comma == std::string::npos) {
          break;
        }
        start = comma + 1;
      }
    }
    if (mechanisms.size() == before && cursor < trimmed.size()) {
      std::string remainder =
          trim_copy(std::string_view(trimmed).substr(cursor));
      if (!remainder.empty()) {
        mechanisms.push_back(ascii_upper_copy(remainder));
      }
    }
  }
  return mechanisms;
}

bool auth_mechanism_advertised(const std::vector<std::string>& capabilities,
                               std::string_view mechanism) {
  if (mechanism.empty()) {
    return has_capability(capabilities, "AUTH");
  }
  if (!has_capability(capabilities, "AUTH")) {
    return false;
  }
  std::vector<std::string> mechanisms = auth_mechanisms(capabilities);
  if (mechanisms.empty()) {
    return true;
  }
  return contains_mechanism(mechanisms, mechanism);
}

void PolicyCache::store(const MtaStsPolicy& policy) {
  std::string domain = canonical_domain(policy.domain);
  if (domain.empty()) {
    return;
  }
  MtaStsPolicy copied = policy;
  copied.domain = domain;
  std::lock_guard<std::mutex> lock(mutex_);
  mta_sts_[domain] = std::move(copied);
}

void PolicyCache::store(const DanePolicy& policy) {
  std::string domain = canonical_domain(policy.domain);
  if (domain.empty()) {
    return;
  }
  DanePolicy copied = policy;
  copied.domain = domain;
  std::lock_guard<std::mutex> lock(mutex_);
  dane_[domain] = std::move(copied);
}

void PolicyCache::remove(std::string_view domain) {
  std::string key = canonical_domain(domain);
  std::lock_guard<std::mutex> lock(mutex_);
  mta_sts_.erase(key);
  dane_.erase(key);
}

CacheResult<MtaStsPolicy> PolicyCache::mta_sts(std::string_view domain,
                                               TimePoint at) const {
  CacheResult<MtaStsPolicy> result;
  std::string key = canonical_domain(domain);
  MtaStsPolicy copied;
  {
    std::lock_guard<std::mutex> lock(mutex_);
    auto found = mta_sts_.find(key);
    if (found == mta_sts_.end()) {
      return result;
    }
    copied = found->second;
  }
  result.value = copied;
  if (policy_is_expired(copied.expires_at, at)) {
    result.state = CacheState::Expired;
  } else if (policy_is_not_yet_valid(copied.valid_from, at)) {
    result.state = CacheState::NotYetValid;
  } else {
    result.state = CacheState::Valid;
  }
  return result;
}

CacheResult<DanePolicy> PolicyCache::dane(std::string_view domain,
                                          TimePoint at) const {
  CacheResult<DanePolicy> result;
  std::string key = canonical_domain(domain);
  DanePolicy copied;
  {
    std::lock_guard<std::mutex> lock(mutex_);
    auto found = dane_.find(key);
    if (found == dane_.end()) {
      return result;
    }
    copied = found->second;
  }
  result.value = copied;
  if (policy_is_expired(copied.expires_at, at)) {
    result.state = CacheState::Expired;
  } else if (policy_is_not_yet_valid(copied.valid_from, at)) {
    result.state = CacheState::NotYetValid;
  } else {
    result.state = CacheState::Valid;
  }
  return result;
}

std::size_t PolicyCache::size() const {
  std::lock_guard<std::mutex> lock(mutex_);
  return mta_sts_.size() + dane_.size();
}

void PolicyCache::clear() {
  std::lock_guard<std::mutex> lock(mutex_);
  mta_sts_.clear();
  dane_.clear();
}

PolicyVerdict evaluate_connection(const ConnectionRecord& record,
                                  const SubmissionPolicyConfig& config,
                                  const PolicyCache& cache, TimePoint at) {
  bool loopback = is_loopback_host(record.host);
  bool tls_active = record.tls_active();
  bool tls_required = config.encryption == EncryptionMode::Required;

  std::string host = canonical_host(record.host);
  std::string domain = record.domain();
  if (host.empty() || record.port == 0 || domain.empty()) {
    return deny(Reason::InvalidConnection,
                "host, policy domain and non-zero port are required",
                tls_required, tls_active, loopback);
  }
  if (!config.allowed_ports.empty() &&
      !contains_port(config.allowed_ports, record.port)) {
    return deny(Reason::PortNotAllowed,
                "connection port is outside the configured submission ports",
                tls_required, tls_active, loopback);
  }
  if (record.channel == ChannelState::ImplicitTls &&
      record.starttls != StartTlsResult::NotAttempted &&
      record.starttls != StartTlsResult::Completed) {
    return deny(Reason::TlsStateInconsistent,
                "implicit TLS conflicts with the recorded STARTTLS result",
                tls_required, tls_active, loopback);
  }

  CacheResult<MtaStsPolicy> mta = cache.mta_sts(domain, at);
  CacheResult<DanePolicy> dane = cache.dane(domain, at);
  bool mta_sts_enforced = false;

  if (mta.present() && mta.value.has_value()) {
    if (mta.state == CacheState::Expired &&
        mta.value->mode == MtaStsMode::Enforce) {
      return deny(Reason::PolicyExpired,
                  "the cached MTA-STS enforce policy for " + domain +
                      " has expired; refusing to downgrade",
                  tls_required, tls_active, loopback);
    }
    if (mta.state == CacheState::NotYetValid &&
        mta.value->mode == MtaStsMode::Enforce) {
      return deny(Reason::PolicyNotYetValid,
                  "the cached MTA-STS enforce policy for " + domain +
                      " is not valid yet",
                  tls_required, tls_active, loopback);
    }
    if (mta.state == CacheState::Valid &&
        mta.value->mode == MtaStsMode::Enforce) {
      mta_sts_enforced = true;
      tls_required = true;
      if (!mta.value->mx_patterns.empty()) {
        bool host_matches_mx = false;
        for (const std::string& pattern : mta.value->mx_patterns) {
          if (host_matches_dns_name(host, pattern)) {
            host_matches_mx = true;
            break;
          }
        }
        if (!host_matches_mx) {
          return deny(Reason::MtaStsHostMismatch,
                      "server host is not one of the MTA-STS policy MX hosts",
                      tls_required, tls_active, loopback);
        }
      }
    }
  }

  if (dane.present() && dane.value.has_value()) {
    if (dane.state == CacheState::Expired && dane.value->has_tlsa) {
      return deny(Reason::PolicyExpired,
                  "the cached DANE policy for " + domain +
                      " has expired; refusing to ignore TLSA records",
                  tls_required, tls_active, loopback);
    }
    if (dane.state == CacheState::NotYetValid && dane.value->has_tlsa) {
      return deny(Reason::PolicyNotYetValid,
                  "the cached DANE policy for " + domain +
                      " is not valid yet",
                  tls_required, tls_active, loopback);
    }
    if (dane.state == CacheState::Valid && dane.value->has_tlsa) {
      tls_required = true;
    }
  }

  if (!tls_active) {
    bool loopback_auth =
        loopback && config.allow_plaintext_auth_on_loopback;
    bool loopback_content =
        loopback && config.allow_plaintext_content_on_loopback;

    if (tls_required) {
      if (record.starttls == StartTlsResult::HandshakeFailed) {
        return deny(Reason::TlsHandshakeFailed,
                    "STARTTLS handshake failed; plaintext fallback is forbidden",
                    tls_required, tls_active, loopback);
      }
      if (record.starttls == StartTlsResult::Rejected) {
        return deny(Reason::StartTlsRejected,
                    "STARTTLS command was rejected; plaintext fallback is "
                    "forbidden",
                    tls_required, tls_active, loopback);
      }
      if (record.starttls == StartTlsResult::NotAttempted &&
          record.starttls_advertised()) {
        PolicyVerdict verdict;
        verdict.decision = Decision::RequireStartTls;
        verdict.reason = Reason::StartTlsRequired;
        verdict.detail =
            "STARTTLS is advertised; complete it and verify the server "
            "certificate before AUTH or content";
        verdict.tls_required = true;
        verdict.loopback = loopback;
        return verdict;
      }
      if (loopback_auth || loopback_content) {
        PolicyVerdict verdict;
        verdict.decision = Decision::Allow;
        verdict.reason = Reason::PlaintextLoopbackAllowed;
        verdict.detail =
            "explicit plaintext loopback exception. Authentication and "
            "content permissions are reported separately.";
        verdict.tls_active = false;
        verdict.loopback = true;
        verdict.tls_required = true;
        verdict.plaintext_auth_permitted = loopback_auth;
        verdict.plaintext_content_permitted = loopback_content;
        return verdict;
      }
      if (record.starttls == StartTlsResult::NotAdvertised ||
          !record.starttls_advertised()) {
        return deny(Reason::StartTlsNotAdvertised,
                    "server did not advertise STARTTLS while policy requires "
                    "encryption; aborting before AUTH or content",
                    tls_required, tls_active, loopback);
      }
      return deny(Reason::TlsStateInconsistent,
                  "inconsistent STARTTLS state while encryption is required",
                  tls_required, tls_active, loopback);
    }

    PolicyVerdict verdict;
    verdict.decision = Decision::Allow;
    verdict.reason = Reason::Ok;
    verdict.detail =
        "policy allows an opportunistic plaintext connection; AUTH still "
        "requires TLS or an explicit loopback exception";
    verdict.tls_required = false;
    verdict.tls_active = false;
    verdict.loopback = loopback;
    verdict.plaintext_auth_permitted = loopback_auth;
    verdict.plaintext_content_permitted = true;
    return verdict;
  }

  if (record.certificate_chain.empty()) {
    return deny(Reason::CertificateMissing,
                "TLS completed without a recorded certificate chain",
                tls_required, tls_active, loopback);
  }
  const CertificateInfo* leaf = record.certificate_chain.leaf();
  if (leaf == nullptr) {
    return deny(Reason::CertificateMissing,
                "TLS completed without a leaf certificate", tls_required,
                tls_active, loopback);
  }
  if (!identity_matches(*leaf, host)) {
    return deny(Reason::CertificateHostnameMismatch,
                "certificate identity does not match server host " + host,
                tls_required, tls_active, loopback);
  }
  if (leaf->not_before.has_value() && at < *leaf->not_before) {
    return deny(Reason::CertificateNotYetValid,
                "leaf certificate is not valid yet", tls_required, tls_active,
                loopback);
  }
  if (leaf->not_after.has_value() && at >= *leaf->not_after) {
    return deny(Reason::CertificateExpired,
                "leaf certificate has expired", tls_required, tls_active,
                loopback);
  }

  bool dane_record_required = dane.usable() && dane.value->has_tlsa;
  bool dane_matched = false;
  bool dane_provides_anchor = false;
  bool dane_requires_pkix = false;
  if (dane_record_required) {
    for (const TlsaRecord& tlsa : dane.value->tlsa_records) {
      switch (tlsa.usage) {
        case 3:
          if (certificate_matches_tlsa(*leaf, tlsa)) {
            dane_matched = true;
            dane_provides_anchor = true;
          }
          break;
        case 2:
          for (const CertificateInfo& certificate :
               record.certificate_chain.certificates) {
            if (certificate_matches_tlsa(certificate, tlsa)) {
              dane_matched = true;
              dane_provides_anchor = true;
              break;
            }
          }
          break;
        case 1:
          if (certificate_matches_tlsa(*leaf, tlsa)) {
            dane_matched = true;
            dane_requires_pkix = true;
          }
          break;
        case 0:
          for (const CertificateInfo& certificate :
               record.certificate_chain.certificates) {
            if (certificate_matches_tlsa(certificate, tlsa)) {
              dane_matched = true;
              dane_requires_pkix = true;
              break;
            }
          }
          break;
        default:
          break;
      }
    }
    if (!dane_matched) {
      return deny(Reason::DaneMismatch,
                  "presented certificate chain matches none of the cached "
                  "DANE TLSA records",
                  tls_required, tls_active, loopback);
    }
  }

  bool web_pki_required =
      config.require_web_pki || mta_sts_enforced || dane_requires_pkix;
  if (dane_provides_anchor && config.allow_dane_without_web_pki &&
      !mta_sts_enforced && !dane_requires_pkix) {
    web_pki_required = false;
  }
  if (web_pki_required && !record.certificate_chain.web_pki_trusted()) {
    return deny(Reason::CertificateUntrusted,
                "certificate chain is not trusted; replacement or interception "
                "certificate rejected",
                tls_required, tls_active, loopback);
  }

  PolicyVerdict verdict;
  verdict.decision = Decision::Allow;
  verdict.reason = Reason::Ok;
  verdict.detail =
      "TLS is active, server identity was verified, and the certificate "
      "chain satisfies policy";
  verdict.tls_active = true;
  verdict.loopback = loopback;
  verdict.tls_required = tls_required;
  return verdict;
}

SubmissionSession::SubmissionSession(SubmissionPolicyConfig config)
    : SubmissionSession(std::make_shared<PolicyCache>(), std::move(config),
                        ClockFn{}) {}

SubmissionSession::SubmissionSession(std::shared_ptr<PolicyCache> cache,
                                     SubmissionPolicyConfig config,
                                     ClockFn clock)
    : cache_(cache != nullptr ? std::move(cache)
                              : std::make_shared<PolicyCache>()),
      config_(std::move(config)),
      clock_(std::move(clock)) {
  if (!clock_) {
    clock_ = [] { return netsec::now(); };
  }
}

PolicyVerdict SubmissionSession::inspect(const ConnectionRecord& record) {
  return inspect(record, clock_());
}

PolicyVerdict SubmissionSession::inspect(const ConnectionRecord& record,
                                         TimePoint at) {
  std::string host = canonical_host(record.host);
  if (host.empty() || record.port == 0) {
    PolicyVerdict verdict = deny(Reason::InvalidConnection,
                                 "host and non-zero port are required",
                                 false, record.tls_active(),
                                 is_loopback_host(record.host));
    connection_ = record;
    verdict_ = verdict;
    aborted_ = true;
    return verdict;
  }

  if (!pinned_endpoint_.has_value()) {
    pinned_endpoint_ = std::make_pair(host, record.port);
  } else if (config_.pin_retry_endpoint) {
    if (host != pinned_endpoint_->first) {
      PolicyVerdict verdict =
          deny(Reason::RetryHostChanged,
               "retry changed the submission host; refusing to bypass policy",
               false, record.tls_active(), is_loopback_host(record.host));
      connection_ = record;
      verdict_ = verdict;
      aborted_ = true;
      return verdict;
    }
    if (record.port != pinned_endpoint_->second) {
      PolicyVerdict verdict =
          deny(Reason::RetryPortChanged,
               "retry changed the submission port; refusing to bypass policy",
               false, record.tls_active(), is_loopback_host(record.host));
      connection_ = record;
      verdict_ = verdict;
      aborted_ = true;
      return verdict;
    }
  }

  if (aborted_) {
    PolicyVerdict verdict =
        deny(Reason::SessionAborted,
             "the submission session was already aborted by a previous "
             "connection failure",
             false, record.tls_active(), is_loopback_host(record.host));
    connection_ = record;
    verdict_ = verdict;
    return verdict;
  }

  if (secure_established_ && !record.tls_active()) {
    PolicyVerdict verdict =
        deny(Reason::EncryptionDowngrade,
             "an earlier attempt on this endpoint was encrypted; plaintext "
             "retry is forbidden",
             true, false, is_loopback_host(record.host));
    connection_ = record;
    verdict_ = verdict;
    aborted_ = true;
    return verdict;
  }

  PolicyVerdict verdict = evaluate_locked(record, at);
  if (verdict.allowed() && record.tls_active()) {
    secure_established_ = true;
  }
  if (verdict.decision == Decision::Deny) {
    aborted_ = true;
  }
  connection_ = record;
  verdict_ = verdict;
  return verdict;
}

PolicyVerdict SubmissionSession::evaluate_locked(const ConnectionRecord& record,
                                                 TimePoint at) const {
  return evaluate_connection(record, config_, *cache_, at);
}

AuthVerdict SubmissionSession::authorize_auth(
    std::string_view mechanism) const {
  AuthVerdict result;
  if (!connection_.has_value() || !verdict_.has_value()) {
    result.reason = Reason::AuthNotConnected;
    result.detail = "inspect a connection before authorizing AUTH";
    return result;
  }
  if (!verdict_->allowed()) {
    result.reason = Reason::AuthConnectionDenied;
    result.detail =
        std::string("connection policy denied AUTH: ") +
        to_string(verdict_->reason);
    return result;
  }
  if (!connection_->has_auth_capability()) {
    result.reason = Reason::AuthCapabilityMissing;
    result.detail = "server did not advertise the AUTH capability";
    return result;
  }

  std::string normalized_mechanism = ascii_upper_copy(mechanism);
  if (!normalized_mechanism.empty()) {
    if (!config_.allowed_auth_mechanisms.empty() &&
        !contains_mechanism(config_.allowed_auth_mechanisms,
                            normalized_mechanism)) {
      result.reason = Reason::AuthMechanismNotAllowed;
      result.detail =
          "AUTH mechanism is not in the client's configured allowlist";
      return result;
    }
    std::vector<std::string> advertised =
        auth_mechanisms(connection_->advertised_capabilities);
    if (!advertised.empty() &&
        !contains_mechanism(advertised, normalized_mechanism)) {
      result.reason = Reason::AuthMechanismNotAllowed;
      result.detail = "AUTH mechanism was not advertised by the server";
      return result;
    }
  }

  if (connection_->tls_active()) {
    result.allowed = true;
    result.reason = Reason::Ok;
    result.detail = "AUTH is permitted on the encrypted channel";
    return result;
  }
  if (verdict_->loopback && config_.allow_plaintext_auth_on_loopback) {
    result.allowed = true;
    result.reason = Reason::PlaintextLoopbackAllowed;
    result.detail =
        "AUTH is permitted by the explicit plaintext loopback exception";
    return result;
  }

  result.reason = Reason::AuthRequiresTls;
  result.detail =
      "AUTH is forbidden because the channel is not encrypted and the "
      "server is not an explicitly allowed local loopback";
  return result;
}

ContentVerdict SubmissionSession::authorize_content() const {
  ContentVerdict result;
  if (!connection_.has_value() || !verdict_.has_value()) {
    result.reason = Reason::ContentRequiresTls;
    result.detail = "inspect a connection before authorizing message content";
    return result;
  }
  if (!verdict_->allowed()) {
    result.reason = Reason::ContentRequiresTls;
    result.detail =
        std::string("connection policy denied content: ") +
        to_string(verdict_->reason);
    return result;
  }
  if (connection_->tls_active()) {
    result.allowed = true;
    result.reason = Reason::Ok;
    result.detail = "content is permitted on the encrypted channel";
    return result;
  }
  if (verdict_->plaintext_content_permitted) {
    result.allowed = true;
    result.reason = Reason::PlaintextLoopbackAllowed;
    result.detail =
        "content is permitted by the explicit plaintext loopback exception";
    return result;
  }
  result.reason = Reason::ContentRequiresTls;
  result.detail =
      "message content is forbidden on a plaintext connection";
  return result;
}

std::optional<std::pair<std::string, std::uint16_t>>
SubmissionSession::pinned_endpoint() const {
  return pinned_endpoint_;
}

const char* to_string(Decision decision) {
  switch (decision) {
    case Decision::Allow:
      return "allow";
    case Decision::RequireStartTls:
      return "require-starttls";
    case Decision::Deny:
      return "deny";
  }
  return "unknown";
}

const char* to_string(Reason reason) {
  switch (reason) {
    case Reason::Ok:
      return "ok";
    case Reason::PlaintextLoopbackAllowed:
      return "plaintext-loopback-allowed";
    case Reason::StartTlsRequired:
      return "starttls-required";
    case Reason::StartTlsNotAdvertised:
      return "starttls-not-advertised";
    case Reason::StartTlsRejected:
      return "starttls-rejected";
    case Reason::TlsHandshakeFailed:
      return "tls-handshake-failed";
    case Reason::TlsStateInconsistent:
      return "tls-state-inconsistent";
    case Reason::PolicyExpired:
      return "policy-expired";
    case Reason::PolicyNotYetValid:
      return "policy-not-yet-valid";
    case Reason::MtaStsHostMismatch:
      return "mta-sts-host-mismatch";
    case Reason::CertificateMissing:
      return "certificate-missing";
    case Reason::CertificateUntrusted:
      return "certificate-untrusted";
    case Reason::CertificateNotYetValid:
      return "certificate-not-yet-valid";
    case Reason::CertificateExpired:
      return "certificate-expired";
    case Reason::CertificateHostnameMismatch:
      return "certificate-hostname-mismatch";
    case Reason::DaneMismatch:
      return "dane-mismatch";
    case Reason::PortNotAllowed:
      return "port-not-allowed";
    case Reason::RetryPortChanged:
      return "retry-port-changed";
    case Reason::RetryHostChanged:
      return "retry-host-changed";
    case Reason::EncryptionDowngrade:
      return "encryption-downgrade";
    case Reason::SessionAborted:
      return "session-aborted";
    case Reason::InvalidConnection:
      return "invalid-connection";
    case Reason::AuthNotConnected:
      return "auth-not-connected";
    case Reason::AuthConnectionDenied:
      return "auth-connection-denied";
    case Reason::AuthCapabilityMissing:
      return "auth-capability-missing";
    case Reason::AuthMechanismNotAllowed:
      return "auth-mechanism-not-allowed";
    case Reason::AuthRequiresTls:
      return "auth-requires-tls";
    case Reason::ContentRequiresTls:
      return "content-requires-tls";
  }
  return "unknown";
}

void WireTranscript::append(WireSecurity security, std::string label,
                            std::string payload) {
  events_.push_back(
      WireEvent{security, std::move(label), std::move(payload)});
}

void WireTranscript::append_plaintext(std::string label, std::string payload) {
  append(WireSecurity::Plaintext, std::move(label), std::move(payload));
}

void WireTranscript::append_tls(std::string label, std::string payload) {
  append(WireSecurity::Tls, std::move(label), std::move(payload));
}

bool WireTranscript::plaintext_contains(std::string_view secret) const {
  return plaintext_matches(secret) != 0;
}

std::size_t WireTranscript::plaintext_matches(
    std::string_view secret) const {
  if (secret.empty()) {
    return 0;
  }
  std::size_t matches = 0;
  for (const WireEvent& event : events_) {
    if (event.security == WireSecurity::Plaintext &&
        event.payload.find(secret) != std::string::npos) {
      ++matches;
    }
  }
  return matches;
}

}  // namespace netsec
