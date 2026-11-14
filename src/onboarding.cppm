module;

#include <cstdint>
#include <functional>
#include <optional>
#include <string>

export module netsec.onboarding;

export namespace netsec::onboarding {

// ---------------------------------------------------------------------------
// Strong, non-interchangeable identifiers. Each alias is a distinct type so a
// home id can never be accidentally passed where a controller id is required.
// Members are defined out-of-line in the implementation unit so every symbol
// is attached to the module rather than emitted inline into importers.
// ---------------------------------------------------------------------------

template <typename Tag>
struct StrongAlias {
  std::string value;

  StrongAlias();
  explicit StrongAlias(std::string v);
  StrongAlias(const StrongAlias&);
  StrongAlias(StrongAlias&&) noexcept;
  StrongAlias& operator=(const StrongAlias&);
  StrongAlias& operator=(StrongAlias&&) noexcept;
  ~StrongAlias();

  [[nodiscard]] bool empty() const noexcept;
  friend bool operator==(const StrongAlias& lhs,
                         const StrongAlias& rhs) noexcept {
    return lhs.value == rhs.value;
  }
};

struct HomeIdTag {};
struct ControllerIdTag {};
struct DeviceTypeIdTag {};
struct DeviceSerialTag {};
struct PublicKeyTag {};

using HomeId = StrongAlias<HomeIdTag>;
using ControllerId = StrongAlias<ControllerIdTag>;
using DeviceTypeId = StrongAlias<DeviceTypeIdTag>;
using DeviceSerial = StrongAlias<DeviceSerialTag>;
using PublicKeyId = StrongAlias<PublicKeyTag>;

// Unix epoch milliseconds, wrapped so raw integers cannot be confused with it.
struct EpochMs {
  std::int64_t ms{0};

  EpochMs() = default;
  explicit constexpr EpochMs(std::int64_t milliseconds) : ms(milliseconds) {}

  friend bool operator==(const EpochMs&, const EpochMs&) = default;
};

// Default provisioning window: short-lived by design (two minutes).
inline constexpr std::int64_t kDefaultWindowMs = 2 * 60 * 1000;

// ---------------------------------------------------------------------------
// Certificate model. A controller certificate binds a controller identity and
// its public key to a specific home and carries a bounded validity interval.
// ---------------------------------------------------------------------------

struct ControllerCertificate {
  ControllerId controller_id;
  HomeId home_id;
  PublicKeyId public_key_id;
  EpochMs not_before;
  EpochMs not_after;
};

// Why a presented certificate cannot be trusted for this enrollment.
enum class CertStatus {
  kTrusted,
  kUnknownAuthority,
  kIdentityMismatch,
  kHomeMismatch,
  kNotYetValid,
  kExpired,
};

// ---------------------------------------------------------------------------
// Dependency ports. These are type-erased callables rather than virtual base
// classes, which keeps the component free of vtables/RTTI while still allowing
// the provisioning service to inject real PKI/crypto/storage implementations.
// Production wires them to X.509 + Ed25519/ECDSA and a durable nonce store.
// ---------------------------------------------------------------------------

// Verifies a presented controller certificate against the trust anchor.
using CertificateVerifier =
    std::function<CertStatus(const ControllerCertificate&, EpochMs)>;

// Verifies an asymmetric signature over a message for the given signer. The
// device only holds public material, never the provisioning signing secret.
using SignatureVerifier =
    std::function<bool(const PublicKeyId&, const std::string& message,
                       const std::string& signature_hex)>;

// Issuing-side signing capability used by the provisioning service.
using Signer =
    std::function<std::string(const PublicKeyId&, const std::string&)>;

// Atomically registers / consumes a one-time nonce. Consume returns true only
// for the first call of a previously issued nonce; replays return false.
using NonceIssuer = std::function<void(const std::string&)>;
using NonceConsumer = std::function<bool(const std::string&)>;

// Monotonic UTC clock.
using Clock = std::function<EpochMs()>;

// ---------------------------------------------------------------------------
// Onboarding credential (the signed payload carried by the QR code).
// It binds home, controller identity, device type and a single-use nonce into
// one tamper-evident statement with a short validity window.
// ---------------------------------------------------------------------------

struct OnboardingCredential {
  HomeId home_id;
  ControllerId controller_id;
  DeviceTypeId device_type;
  PublicKeyId signer_key_id;
  std::string nonce;
  EpochMs issued_at;
  std::int64_t window_ms{kDefaultWindowMs};
  std::string signature_hex;
};

// Header tag so a swapped/foreign QR code is rejected before parsing work.
inline constexpr const char* kQrMagic = "ONBQ1";

// Deterministic wire format shared by signer and verifier. The signature is
// never part of the signed message; fields are length-prefixed to remove
// delimiter-injection ambiguity.
[[nodiscard]] std::string CanonicalMessage(const OnboardingCredential& c);

// Encodes a credential into the QR payload:
// ONBQ1|<base fields>|<window>|<signature-hex>
[[nodiscard]] std::string EncodeQr(const OnboardingCredential& c);

// Parses a QR payload. Returns std::nullopt on any structural violation; the
// signature is still verified separately by the onboarding service.
[[nodiscard]] std::optional<OnboardingCredential> DecodeQr(
    const std::string& qr_payload);

// ---------------------------------------------------------------------------
// Enrollment decision model.
// ---------------------------------------------------------------------------

enum class RejectReason {
  kMalformedQr,
  kPhysicalConfirmationMissing,
  kWindowExpired,
  kSignatureInvalid,       // swapped QR / tampered credential
  kCertificateUntrusted,   // controller certificate failed PKI checks
  kHomeMismatch,           // credential home != controller/certificate home
  kControllerMismatch,     // credential controller != presented controller
  kDeviceTypeMismatch,     // joining device is not the authorized type
  kNonceReused,            // one-time nonce was already consumed
};

[[nodiscard]] std::string ToString(RejectReason reason);

struct EnrollmentRequest {
  std::string qr_payload;
  DeviceSerial device_serial;
  DeviceTypeId device_type;
  bool physical_confirmation{false};  // button-press / LED confirmation
  ControllerCertificate presented_certificate;
};

struct EnrollmentResult {
  bool accepted{false};
  std::optional<RejectReason> reason;
  std::optional<HomeId> authorized_home;
  std::optional<ControllerId> authorized_controller;
};

// ---------------------------------------------------------------------------
// Credential factory: provisioning-service side. Issues a signed, single-use
// credential for one home/controller/device-type tuple.
// ---------------------------------------------------------------------------

class CredentialFactory {
 public:
  CredentialFactory(HomeId home, ControllerId controller, DeviceTypeId type,
                    PublicKeyId signer_key, Signer sign,
                    NonceIssuer issue_nonce, Clock clock);

  // Issues a credential with a caller-provided nonce (e.g. derived from a
  // secure RNG). The nonce is registered so it can be consumed exactly once.
  [[nodiscard]] OnboardingCredential Issue(std::string nonce,
                                           std::int64_t window_ms =
                                               kDefaultWindowMs);

  [[nodiscard]] std::string IssueQr(
      std::string nonce, std::int64_t window_ms = kDefaultWindowMs);

 private:
  HomeId home_;
  ControllerId controller_;
  DeviceTypeId device_type_;
  PublicKeyId signer_key_;
  Signer sign_;
  NonceIssuer issue_nonce_;
  Clock clock_;
};

// ---------------------------------------------------------------------------
// Onboarding service: device side. Pure verification logic over injected
// callables; thread-safe as long as the injected ports are.
// ---------------------------------------------------------------------------

class OnboardingService {
 public:
  OnboardingService(HomeId expected_home, CertificateVerifier verify_cert,
                    SignatureVerifier verify_signature,
                    NonceConsumer consume_nonce, Clock clock);

  [[nodiscard]] EnrollmentResult Enroll(const EnrollmentRequest& req);

 private:
  HomeId expected_home_;
  CertificateVerifier verify_cert_;
  SignatureVerifier verify_signature_;
  NonceConsumer consume_nonce_;
  Clock clock_;
};

// ---------------------------------------------------------------------------
// In-memory test doubles (NOT for production).
//
// InMemorySignatureEngine is a symmetric MAC-like stand-in: real devices must
// use an asymmetric trust anchor so the signing key never leaves the home
// controller / provisioning service.
// InMemoryCertificateAuthority models a trusted PKI database of issued
// certificates and enforces identity/home binding plus validity windows.
// All classes are vtable-free; expose their behaviour through the port
// callables via the As*() helpers.
// ---------------------------------------------------------------------------

class InMemorySignatureEngine {
 public:
  InMemorySignatureEngine();
  ~InMemorySignatureEngine();

  // Registers the shared secret associated with a signer's public-key id.
  void RegisterSigner(const PublicKeyId& signer, std::string secret);

  [[nodiscard]] std::string Sign(const PublicKeyId& signer,
                                 const std::string& message);
  [[nodiscard]] bool Verify(const PublicKeyId& signer,
                            const std::string& message,
                            const std::string& signature_hex) const;

  [[nodiscard]] Signer AsSigner();
  [[nodiscard]] SignatureVerifier AsVerifier();

 private:
  struct State;
  State* state_;
};

class InMemoryCertificateAuthority {
 public:
  InMemoryCertificateAuthority();
  ~InMemoryCertificateAuthority();

  void IssueCertificate(const ControllerCertificate& cert);
  [[nodiscard]] CertStatus Verify(const ControllerCertificate& cert,
                                  EpochMs now) const;

  [[nodiscard]] CertificateVerifier AsVerifier();

 private:
  struct State;
  State* state_;
};

// Thread-safe one-time nonce registry backed by in-memory storage.
class InMemoryNonceRegistry {
 public:
  InMemoryNonceRegistry();
  ~InMemoryNonceRegistry();

  void Issue(const std::string& nonce);
  [[nodiscard]] bool Consume(const std::string& nonce);

  [[nodiscard]] NonceIssuer AsIssuer();
  [[nodiscard]] NonceConsumer AsConsumer();

 private:
  struct State;
  State* state_;
};

// Manually driven clock so tests can advance time deterministically.
class FixedClock {
 public:
  explicit FixedClock(EpochMs initial);
  ~FixedClock();

  [[nodiscard]] EpochMs Now() const;
  void Set(EpochMs t);
  void Advance(std::int64_t delta_ms);

  [[nodiscard]] Clock AsClock();

 private:
  EpochMs now_;
};

}  // namespace netsec::onboarding
