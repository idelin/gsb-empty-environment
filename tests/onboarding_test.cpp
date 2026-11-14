#include <cstdio>
#include <string>

import netsec.onboarding;

using namespace netsec::onboarding;

namespace {

int g_checks = 0;
int g_failures = 0;

void Check(bool condition, const std::string& label) {
  ++g_checks;
  if (!condition) {
    ++g_failures;
    std::printf("  [FAIL] %s\n", label.c_str());
  }
}

struct Harness {
  static constexpr std::int64_t kStartMs = 1'700'000'000'000LL;

  FixedClock clock{EpochMs(kStartMs)};

  // Home A: legitimate home with an honest controller and signing key.
  InMemorySignatureEngine signer_a;
  InMemoryCertificateAuthority ca_a;
  InMemoryNonceRegistry nonces_a;
  const HomeId home_a{"home-alpha"};
  const ControllerId controller_a{"ctrl-alpha-1"};
  const DeviceTypeId sensor_type{"climate-sensor"};
  const DeviceTypeId actuator_type{"smart-lock"};
  const PublicKeyId key_a{"key-alpha"};
  const DeviceSerial serial{"sn-0001"};

  // Home B: the attacker's home with its own controller and signing key.
  InMemorySignatureEngine signer_b;
  InMemoryCertificateAuthority ca_b;
  InMemoryNonceRegistry nonces_b;
  const HomeId home_b{"home-bravo"};
  const ControllerId controller_b{"ctrl-bravo-9"};
  const PublicKeyId key_b{"key-bravo"};

  Harness() {
    signer_a.RegisterSigner(key_a, "legitimate-home-alpha-secret");
    signer_b.RegisterSigner(key_b, "attacker-home-bravo-secret");

    ca_a.IssueCertificate(CertA());
    ca_b.IssueCertificate(CertB());
  }

  ControllerCertificate CertA() const {
    return ControllerCertificate{
        controller_a, home_a, key_a, EpochMs(kStartMs - 60'000),
        EpochMs(kStartMs + 365 * 24 * 3600 * 1000LL)};
  }

  ControllerCertificate CertB() const {
    return ControllerCertificate{
        controller_b, home_b, key_b, EpochMs(kStartMs - 60'000),
        EpochMs(kStartMs + 365 * 24 * 3600 * 1000LL)};
  }

  // A home-A device verifies against home A's trust anchor and clock.
  OnboardingService ServiceA() {
    return OnboardingService(home_a, ca_a.AsVerifier(), signer_a.AsVerifier(),
                             nonces_a.AsConsumer(), clock.AsClock());
  }

  CredentialFactory FactoryA() {
    return CredentialFactory(home_a, controller_a, sensor_type, key_a,
                             signer_a.AsSigner(), nonces_a.AsIssuer(),
                             clock.AsClock());
  }

  CredentialFactory FactoryB() {
    return CredentialFactory(home_b, controller_b, sensor_type, key_b,
                             signer_b.AsSigner(), nonces_b.AsIssuer(),
                             clock.AsClock());
  }

  EnrollmentRequest RequestFor(const std::string& qr,
                               const DeviceTypeId& type,
                               const ControllerCertificate& cert,
                               bool confirmed = true) {
    EnrollmentRequest req;
    req.qr_payload = qr;
    req.device_serial = serial;
    req.device_type = type;
    req.physical_confirmation = confirmed;
    req.presented_certificate = cert;
    return req;
  }
};

void ExpectReject(const EnrollmentResult& result, RejectReason expected,
                  const std::string& label) {
  Check(!result.accepted, label + " -> rejected");
  Check(result.reason.has_value() && *result.reason == expected,
        label + " -> reason=" +
            (result.reason ? ToString(*result.reason) : std::string("none")));
  Check(!result.authorized_home.has_value(), label + " -> no home granted");
}

}  // namespace

int main() {
  Harness h;

  // -------------------------------------------------------------------------
  // 1. Legitimate onboarding: a fresh credential for home A joins home A.
  // -------------------------------------------------------------------------
  {
    const std::string qr = h.FactoryA().IssueQr("nonce-legit-001");

    OnboardingService service = h.ServiceA();
    const EnrollmentResult result =
        service.Enroll(h.RequestFor(qr, h.sensor_type, h.CertA()));

    Check(result.accepted, "legit -> accepted");
    Check(result.authorized_home.has_value() &&
              *result.authorized_home == h.home_a,
          "legit -> bound to home-alpha");
    Check(result.authorized_controller.has_value() &&
              *result.authorized_controller == h.controller_a,
          "legit -> bound to ctrl-alpha-1");
  }

  // -------------------------------------------------------------------------
  // 2. QR code replacement: an attacker swaps in a code signed by the
  //    attacker's key. Home A's verifier does not trust that signature.
  // -------------------------------------------------------------------------
  {
    const std::string malicious_qr =
        h.FactoryB().IssueQr("nonce-attack-qr-002");

    OnboardingService service = h.ServiceA();
    const EnrollmentResult result =
        service.Enroll(h.RequestFor(malicious_qr, h.sensor_type, h.CertA()));
    ExpectReject(result, RejectReason::kSignatureInvalid,
                 "replaced-qr attacker signature");
  }

  // -------------------------------------------------------------------------
  // 3. QR replacement via tampering: a home-A code with one field altered
  //    invalidates the signature even though the signing key is trusted.
  // -------------------------------------------------------------------------
  {
    std::string tampered_qr = h.FactoryA().IssueQr("nonce-tamper-003");
    const std::string original = "ctrl-alpha-1";
    const std::string replacement = "ctrl-evil-00";
    const std::size_t pos = tampered_qr.find(original);
    Check(pos != std::string::npos, "tamper -> field located");
    tampered_qr.replace(pos, original.size(), replacement);

    OnboardingService service = h.ServiceA();
    const EnrollmentResult result =
        service.Enroll(h.RequestFor(tampered_qr, h.sensor_type, h.CertA()));
    ExpectReject(result, RejectReason::kSignatureInvalid,
                 "tampered-qr field modification");
  }

  // -------------------------------------------------------------------------
  // 4. Cross-home onboarding: a valid home-B credential presented to a
  //    home-A device is never admitted to home A.
  // -------------------------------------------------------------------------
  {
    const std::string qr_b = h.FactoryB().IssueQr("nonce-crosshome-004");

    // 4a. Presented with a home-B controller certificate: home A's trust
    //     anchor rejects the foreign signature before any home grant.
    OnboardingService service = h.ServiceA();
    const EnrollmentResult foreign_cert =
        service.Enroll(h.RequestFor(qr_b, h.sensor_type, h.CertB()));
    Check(!foreign_cert.accepted, "cross-home(b-cert) -> rejected");
    Check(foreign_cert.reason == RejectReason::kSignatureInvalid ||
              foreign_cert.reason == RejectReason::kCertificateUntrusted ||
              foreign_cert.reason == RejectReason::kHomeMismatch,
          "cross-home(b-cert) -> blocked by trust/home binding");

    // 4b. Rejected before the one-time nonce is consumed.
    OnboardingService service2 = h.ServiceA();
    const EnrollmentResult local_cert =
        service2.Enroll(h.RequestFor(qr_b, h.sensor_type, h.CertA()));
    Check(!local_cert.accepted &&
              local_cert.reason != RejectReason::kNonceReused,
          "cross-home(a-cert) -> rejected without burning nonce");
  }

  // -------------------------------------------------------------------------
  // 5. Credential replay: the same one-time nonce cannot be enrolled twice.
  // -------------------------------------------------------------------------
  {
    const std::string qr = h.FactoryA().IssueQr("nonce-replay-005");

    OnboardingService service = h.ServiceA();
    const EnrollmentResult first =
        service.Enroll(h.RequestFor(qr, h.sensor_type, h.CertA()));
    Check(first.accepted, "replay -> first use accepted");

    const EnrollmentResult second =
        service.Enroll(h.RequestFor(qr, h.sensor_type, h.CertA()));
    ExpectReject(second, RejectReason::kNonceReused,
                 "replay -> second use refused");
  }

  // -------------------------------------------------------------------------
  // 6. Expired provisioning window: advancing past the window rejects the
  //    code without consuming the nonce.
  // -------------------------------------------------------------------------
  {
    const std::string qr =
        h.FactoryA().IssueQr("nonce-expired-006", 60'000);

    h.clock.Advance(60'001);
    OnboardingService service = h.ServiceA();
    const EnrollmentResult expired =
        service.Enroll(h.RequestFor(qr, h.sensor_type, h.CertA()));
    ExpectReject(expired, RejectReason::kWindowExpired,
                 "timeout -> expired window rejected");

    // Roll the clock back inside the window: the nonce was never burned.
    h.clock.Set(EpochMs(Harness::kStartMs + 59'000));
    OnboardingService service2 = h.ServiceA();
    const EnrollmentResult recovered =
        service2.Enroll(h.RequestFor(qr, h.sensor_type, h.CertA()));
    Check(recovered.accepted, "timeout -> nonce preserved for valid retry");
    h.clock.Set(EpochMs(Harness::kStartMs));
  }

  // -------------------------------------------------------------------------
  // 7. Physical confirmation and device-type binding guard rails.
  // -------------------------------------------------------------------------
  {
    const std::string qr = h.FactoryA().IssueQr("nonce-guard-007");

    OnboardingService service = h.ServiceA();
    const EnrollmentResult no_confirm = service.Enroll(
        h.RequestFor(qr, h.sensor_type, h.CertA(), /*confirmed=*/false));
    ExpectReject(no_confirm, RejectReason::kPhysicalConfirmationMissing,
                 "physical -> confirmation required");

    const EnrollmentResult wrong_type =
        service.Enroll(h.RequestFor(qr, h.actuator_type, h.CertA()));
    // The prior rejection did not consume the nonce, so the request reaches
    // the device-type check rather than failing as a replay.
    ExpectReject(wrong_type, RejectReason::kDeviceTypeMismatch,
                 "device-type -> mismatch rejected");
  }

  // -------------------------------------------------------------------------
  // 8. Malformed / foreign QR payloads are rejected structurally.
  // -------------------------------------------------------------------------
  {
    OnboardingService service = h.ServiceA();
    const EnrollmentResult garbage = service.Enroll(
        h.RequestFor("https://evil.example/qr", h.sensor_type, h.CertA()));
    ExpectReject(garbage, RejectReason::kMalformedQr,
                 "malformed -> foreign payload rejected");
  }

  // -------------------------------------------------------------------------
  // 9. Expired controller certificate is refused even with a valid credential.
  // -------------------------------------------------------------------------
  {
    const std::string qr = h.FactoryA().IssueQr("nonce-cert-009");
    // Register a distinct controller whose trusted certificate already lapsed.
    const ControllerId stale_controller{"ctrl-stale"};
    const ControllerCertificate stale_cert{
        stale_controller, h.home_a, h.key_a,
        EpochMs(Harness::kStartMs - 10'000),
        EpochMs(Harness::kStartMs - 1)};
    h.ca_a.IssueCertificate(stale_cert);

    OnboardingService service = h.ServiceA();
    const EnrollmentResult result =
        service.Enroll(h.RequestFor(qr, h.sensor_type, stale_cert));
    ExpectReject(result, RejectReason::kCertificateUntrusted,
                 "certificate -> expired controller cert rejected");
  }

  std::printf("\n%d checks, %d failures\n", g_checks, g_failures);
  return g_failures == 0 ? 0 : 1;
}
