module;

#include <algorithm>
#include <array>
#include <cstdint>
#include <map>
#include <mutex>
#include <optional>
#include <sstream>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

module netsec.onboarding;

namespace netsec::onboarding {

namespace {

constexpr char kHex[] = "0123456789abcdef";

std::string ToHex(const std::uint8_t* data, std::size_t len) {
  std::string out;
  out.reserve(len * 2);
  for (std::size_t i = 0; i < len; ++i) {
    out.push_back(kHex[data[i] >> 4]);
    out.push_back(kHex[data[i] & 0x0f]);
  }
  return out;
}

int HexNibble(char c) {
  if (c >= '0' && c <= '9') return c - '0';
  if (c >= 'a' && c <= 'f') return c - 'a' + 10;
  if (c >= 'A' && c <= 'F') return c - 'A' + 10;
  return -1;
}

std::optional<std::vector<std::uint8_t>> FromHex(const std::string& s) {
  if (s.size() % 2 != 0) return std::nullopt;
  std::vector<std::uint8_t> bytes;
  bytes.reserve(s.size() / 2);
  for (std::size_t i = 0; i < s.size(); i += 2) {
    int hi = HexNibble(s[i]);
    int lo = HexNibble(s[i + 1]);
    if (hi < 0 || lo < 0) return std::nullopt;
    bytes.push_back(static_cast<std::uint8_t>((hi << 4) | lo));
  }
  return bytes;
}

// Length-prefixed field: "<decimal-length>:<value>". The length prefix keeps
// the encoding unambiguous even if a value contains separator characters.
std::string LenPrefixed(std::string_view v) {
  std::string field;
  field += std::to_string(v.size());
  field.push_back(':');
  field.append(v.data(), v.size());
  return field;
}

// Test-only keyed hash: FNV-1a (128 bit) over a domain-separated
// secret||message input. This stands in for Ed25519/ECDSA in tests; replace it
// with an asymmetric primitive through the Signer/Verifier ports in production.
std::string KeyedHashTest(const std::string& secret,
                          const std::string& message) {
  constexpr __uint128_t kFnvOffset =
      ((__uint128_t)0x6c62272e07bb0142ULL << 64) | 0x62b821756295c58dULL;
  constexpr __uint128_t kFnvPrime =
      ((__uint128_t)0x0000000001000000ULL << 64) | 0x000000000000013bULL;

  __uint128_t hash = kFnvOffset;
  auto absorb = [&](char byte) {
    hash ^= static_cast<unsigned char>(byte);
    hash *= kFnvPrime;
  };
  const std::string domain = "ONBOARD-SIG-v1|";
  for (char c : domain) absorb(c);
  for (char c : secret) absorb(c);
  absorb('|');
  for (char c : message) absorb(c);

  std::array<std::uint8_t, 16> digest{};
  for (int i = 15; i >= 0; --i) {
    digest[static_cast<std::size_t>(i)] =
        static_cast<std::uint8_t>(hash & 0xff);
    hash >>= 8;
  }
  return ToHex(digest.data(), digest.size());
}

// MAC comparison without early exit on the first mismatching byte.
bool SignatureEqual(const std::string& a, const std::string& b) {
  if (a.size() != b.size()) return false;
  unsigned char diff = 0;
  for (std::size_t i = 0; i < a.size(); ++i) {
    diff |= static_cast<unsigned char>(a[i] ^ b[i]);
  }
  return diff == 0;
}

std::vector<std::string> Split(const std::string& s, char sep) {
  std::vector<std::string> parts;
  std::size_t start = 0;
  while (true) {
    std::size_t pos = s.find(sep, start);
    if (pos == std::string::npos) {
      parts.push_back(s.substr(start));
      break;
    }
    parts.push_back(s.substr(start, pos - start));
    start = pos + 1;
  }
  return parts;
}

bool ParseInt64(const std::string& s, std::int64_t& out) {
  if (s.empty()) return false;
  std::size_t i = 0;
  bool negative = false;
  if (s[0] == '-' || s[0] == '+') {
    negative = s[0] == '-';
    i = 1;
    if (s.size() == 1) return false;
  }
  std::int64_t value = 0;
  for (; i < s.size(); ++i) {
    if (s[i] < '0' || s[i] > '9') return false;
    value = value * 10 + (s[i] - '0');
  }
  out = negative ? -value : value;
  return true;
}

bool RestrictedToken(const std::string& s) {
  if (s.empty()) return false;
  return std::all_of(s.begin(), s.end(), [](char c) {
    return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
           (c >= '0' && c <= '9') || c == '-' || c == '_';
  });
}

}  // namespace

// ---------------------------------------------------------------------------
// StrongAlias out-of-line members
// ---------------------------------------------------------------------------

template <typename Tag>
StrongAlias<Tag>::StrongAlias() = default;

template <typename Tag>
StrongAlias<Tag>::StrongAlias(std::string v) : value(std::move(v)) {}

template <typename Tag>
StrongAlias<Tag>::StrongAlias(const StrongAlias&) = default;

template <typename Tag>
StrongAlias<Tag>::StrongAlias(StrongAlias&&) noexcept = default;

template <typename Tag>
StrongAlias<Tag>& StrongAlias<Tag>::operator=(const StrongAlias&) = default;

template <typename Tag>
StrongAlias<Tag>& StrongAlias<Tag>::operator=(StrongAlias&&) noexcept = default;

template <typename Tag>
StrongAlias<Tag>::~StrongAlias() = default;

template <typename Tag>
bool StrongAlias<Tag>::empty() const noexcept {
  return value.empty();
}

template struct StrongAlias<HomeIdTag>;
template struct StrongAlias<ControllerIdTag>;
template struct StrongAlias<DeviceTypeIdTag>;
template struct StrongAlias<DeviceSerialTag>;
template struct StrongAlias<PublicKeyTag>;

// ---------------------------------------------------------------------------
// Credential serialization
// ---------------------------------------------------------------------------

std::string CanonicalMessage(const OnboardingCredential& c) {
  std::string msg;
  msg += LenPrefixed(kQrMagic);
  msg += LenPrefixed(c.home_id.value);
  msg += LenPrefixed(c.controller_id.value);
  msg += LenPrefixed(c.device_type.value);
  msg += LenPrefixed(c.signer_key_id.value);
  msg += LenPrefixed(c.nonce);
  msg += LenPrefixed(std::to_string(c.issued_at.ms));
  msg += LenPrefixed(std::to_string(c.window_ms));
  return msg;
}

std::string EncodeQr(const OnboardingCredential& c) {
  std::ostringstream qr;
  qr << kQrMagic << '|' << c.home_id.value << '|' << c.controller_id.value
     << '|' << c.device_type.value << '|' << c.signer_key_id.value << '|'
     << c.nonce << '|' << c.issued_at.ms << '|' << c.window_ms << '|'
     << c.signature_hex;
  return qr.str();
}

std::optional<OnboardingCredential> DecodeQr(const std::string& qr_payload) {
  const std::vector<std::string> p = Split(qr_payload, '|');
  if (p.size() != 9) return std::nullopt;
  if (p[0] != kQrMagic) return std::nullopt;

  std::int64_t issued_at = 0;
  std::int64_t window_ms = 0;
  if (!ParseInt64(p[6], issued_at) || !ParseInt64(p[7], window_ms)) {
    return std::nullopt;
  }
  if (window_ms <= 0) return std::nullopt;

  // Identity fields and nonce use a restricted charset; the signature is
  // validated as hex. This blocks delimiter/encoding injection via codes.
  for (std::size_t i = 1; i <= 5; ++i) {
    if (!RestrictedToken(p[i])) return std::nullopt;
  }
  if (!FromHex(p[8]).has_value()) return std::nullopt;

  OnboardingCredential c;
  c.home_id = HomeId(p[1]);
  c.controller_id = ControllerId(p[2]);
  c.device_type = DeviceTypeId(p[3]);
  c.signer_key_id = PublicKeyId(p[4]);
  c.nonce = p[5];
  c.issued_at = EpochMs(issued_at);
  c.window_ms = window_ms;
  c.signature_hex = p[8];
  return c;
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

CredentialFactory::CredentialFactory(HomeId home, ControllerId controller,
                                     DeviceTypeId type, PublicKeyId signer_key,
                                     Signer sign, NonceIssuer issue_nonce,
                                     Clock clock)
    : home_(std::move(home)),
      controller_(std::move(controller)),
      device_type_(std::move(type)),
      signer_key_(std::move(signer_key)),
      sign_(std::move(sign)),
      issue_nonce_(std::move(issue_nonce)),
      clock_(std::move(clock)) {}

OnboardingCredential CredentialFactory::Issue(std::string nonce,
                                              std::int64_t window_ms) {
  OnboardingCredential c;
  c.home_id = home_;
  c.controller_id = controller_;
  c.device_type = device_type_;
  c.signer_key_id = signer_key_;
  c.nonce = std::move(nonce);
  c.issued_at = clock_();
  c.window_ms = window_ms;
  c.signature_hex = sign_(signer_key_, CanonicalMessage(c));
  issue_nonce_(c.nonce);
  return c;
}

std::string CredentialFactory::IssueQr(std::string nonce,
                                       std::int64_t window_ms) {
  return EncodeQr(Issue(std::move(nonce), window_ms));
}

// ---------------------------------------------------------------------------
// Onboarding decision logic
// ---------------------------------------------------------------------------

std::string ToString(RejectReason reason) {
  switch (reason) {
    case RejectReason::kMalformedQr:
      return "malformed-qr";
    case RejectReason::kPhysicalConfirmationMissing:
      return "physical-confirmation-missing";
    case RejectReason::kWindowExpired:
      return "window-expired";
    case RejectReason::kSignatureInvalid:
      return "signature-invalid";
    case RejectReason::kCertificateUntrusted:
      return "certificate-untrusted";
    case RejectReason::kHomeMismatch:
      return "home-mismatch";
    case RejectReason::kControllerMismatch:
      return "controller-mismatch";
    case RejectReason::kDeviceTypeMismatch:
      return "device-type-mismatch";
    case RejectReason::kNonceReused:
      return "nonce-reused";
  }
  return "unknown";
}

OnboardingService::OnboardingService(HomeId expected_home,
                                     CertificateVerifier verify_cert,
                                     SignatureVerifier verify_signature,
                                     NonceConsumer consume_nonce, Clock clock)
    : expected_home_(std::move(expected_home)),
      verify_cert_(std::move(verify_cert)),
      verify_signature_(std::move(verify_signature)),
      consume_nonce_(std::move(consume_nonce)),
      clock_(std::move(clock)) {}

EnrollmentResult OnboardingService::Enroll(const EnrollmentRequest& req) {
  auto reject = [](RejectReason r) {
    EnrollmentResult result;
    result.accepted = false;
    result.reason = r;
    return result;
  };

  // 1. Physical presence: a human must confirm the device being onboarded.
  if (!req.physical_confirmation) {
    return reject(RejectReason::kPhysicalConfirmationMissing);
  }

  // 2. Structural parsing of the QR credential.
  auto credential = DecodeQr(req.qr_payload);
  if (!credential.has_value()) {
    return reject(RejectReason::kMalformedQr);
  }

  // 3. Short provisioning window.
  const EpochMs now = clock_();
  if (now.ms > credential->issued_at.ms + credential->window_ms) {
    return reject(RejectReason::kWindowExpired);
  }

  // 4. Signature over the bound home/controller/type/nonce statement. A
  //    swapped or tampered QR cannot carry a valid signature for the anchor.
  if (!verify_signature_(credential->signer_key_id,
                         CanonicalMessage(*credential),
                         credential->signature_hex)) {
    return reject(RejectReason::kSignatureInvalid);
  }

  // 5. Controller certificate: trusted chain, identity/home binding and
  //    validity interval are all checked before any state is mutated.
  const CertStatus cert_status =
      verify_cert_(req.presented_certificate, now);
  if (cert_status != CertStatus::kTrusted) {
    return reject(RejectReason::kCertificateUntrusted);
  }

  // 6. Home binding: the credential, the device's authorized home and the
  //    presented controller certificate must name the same home.
  if (credential->home_id != expected_home_ ||
      req.presented_certificate.home_id != expected_home_) {
    return reject(RejectReason::kHomeMismatch);
  }

  // 7. Controller identity binding.
  if (req.presented_certificate.controller_id !=
      credential->controller_id) {
    return reject(RejectReason::kControllerMismatch);
  }

  // 8. Authorized device type binding.
  if (credential->device_type != req.device_type) {
    return reject(RejectReason::kDeviceTypeMismatch);
  }

  // 9. Atomic single-use nonce consumption happens last, so rejected attempts
  //    never burn a valid nonce; a replayed credential is refused here.
  if (!consume_nonce_(credential->nonce)) {
    return reject(RejectReason::kNonceReused);
  }

  EnrollmentResult result;
  result.accepted = true;
  result.authorized_home = credential->home_id;
  result.authorized_controller = credential->controller_id;
  return result;
}

// ---------------------------------------------------------------------------
// In-memory test doubles
// ---------------------------------------------------------------------------

struct InMemorySignatureEngine::State {
  std::map<std::string, std::string> secrets;
};

InMemorySignatureEngine::InMemorySignatureEngine() : state_(new State()) {}
InMemorySignatureEngine::~InMemorySignatureEngine() { delete state_; }

void InMemorySignatureEngine::RegisterSigner(const PublicKeyId& signer,
                                             std::string secret) {
  state_->secrets[signer.value] = std::move(secret);
}

std::string InMemorySignatureEngine::Sign(const PublicKeyId& signer,
                                          const std::string& message) {
  auto it = state_->secrets.find(signer.value);
  if (it == state_->secrets.end()) return {};
  return KeyedHashTest(it->second, message);
}

bool InMemorySignatureEngine::Verify(const PublicKeyId& signer,
                                     const std::string& message,
                                     const std::string& signature_hex) const {
  auto it = state_->secrets.find(signer.value);
  if (it == state_->secrets.end()) return false;
  return SignatureEqual(KeyedHashTest(it->second, message), signature_hex);
}

Signer InMemorySignatureEngine::AsSigner() {
  return [this](const PublicKeyId& k, const std::string& m) {
    return this->Sign(k, m);
  };
}

SignatureVerifier InMemorySignatureEngine::AsVerifier() {
  return [this](const PublicKeyId& k, const std::string& m,
                const std::string& sig) { return this->Verify(k, m, sig); };
}

struct InMemoryCertificateAuthority::State {
  std::map<std::string, ControllerCertificate> issued;
};

InMemoryCertificateAuthority::InMemoryCertificateAuthority()
    : state_(new State()) {}
InMemoryCertificateAuthority::~InMemoryCertificateAuthority() { delete state_; }

void InMemoryCertificateAuthority::IssueCertificate(
    const ControllerCertificate& cert) {
  state_->issued[cert.controller_id.value] = cert;
}

CertStatus InMemoryCertificateAuthority::Verify(
    const ControllerCertificate& cert, EpochMs now) const {
  auto it = state_->issued.find(cert.controller_id.value);
  if (it == state_->issued.end()) return CertStatus::kUnknownAuthority;

  const ControllerCertificate& trusted = it->second;
  if (cert.public_key_id != trusted.public_key_id) {
    return CertStatus::kIdentityMismatch;
  }
  if (cert.home_id != trusted.home_id) {
    return CertStatus::kHomeMismatch;
  }
  if (now.ms < trusted.not_before.ms) {
    return CertStatus::kNotYetValid;
  }
  if (now.ms > trusted.not_after.ms) {
    return CertStatus::kExpired;
  }
  return CertStatus::kTrusted;
}

CertificateVerifier InMemoryCertificateAuthority::AsVerifier() {
  return [this](const ControllerCertificate& c, EpochMs now) {
    return this->Verify(c, now);
  };
}

struct InMemoryNonceRegistry::State {
  std::mutex mutex;
  std::vector<std::string> issued;
  std::vector<std::string> consumed;
};

InMemoryNonceRegistry::InMemoryNonceRegistry() : state_(new State()) {}
InMemoryNonceRegistry::~InMemoryNonceRegistry() { delete state_; }

void InMemoryNonceRegistry::Issue(const std::string& nonce) {
  std::lock_guard<std::mutex> lock(state_->mutex);
  if (std::find(state_->issued.begin(), state_->issued.end(), nonce) ==
      state_->issued.end()) {
    state_->issued.push_back(nonce);
  }
}

bool InMemoryNonceRegistry::Consume(const std::string& nonce) {
  std::lock_guard<std::mutex> lock(state_->mutex);
  const bool was_issued =
      std::find(state_->issued.begin(), state_->issued.end(), nonce) !=
      state_->issued.end();
  const bool already_consumed =
      std::find(state_->consumed.begin(), state_->consumed.end(), nonce) !=
      state_->consumed.end();
  if (!was_issued || already_consumed) {
    return false;
  }
  state_->consumed.push_back(nonce);
  return true;
}

NonceIssuer InMemoryNonceRegistry::AsIssuer() {
  return [this](const std::string& n) { this->Issue(n); };
}

NonceConsumer InMemoryNonceRegistry::AsConsumer() {
  return [this](const std::string& n) { return this->Consume(n); };
}

FixedClock::FixedClock(EpochMs initial) : now_(initial) {}
FixedClock::~FixedClock() = default;
EpochMs FixedClock::Now() const { return now_; }
void FixedClock::Set(EpochMs t) { now_ = t; }
void FixedClock::Advance(std::int64_t delta_ms) {
  now_ = EpochMs(now_.ms + delta_ms);
}

Clock FixedClock::AsClock() {
  return [this]() { return this->Now(); };
}

}  // namespace netsec::onboarding
