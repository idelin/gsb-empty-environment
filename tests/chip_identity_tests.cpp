#include "netsec.hpp"

#include <cmath>
#include <exception>
#include <functional>
#include <iostream>
#include <optional>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace {

int g_failures = 0;

void check(bool condition, std::string_view test, std::string_view detail) {
  if (!condition) {
    ++g_failures;
    std::cerr << "FAIL [" << test << "] " << detail << '\n';
  }
}

void appendField(std::string& output, std::string_view value) {
  output.append(std::to_string(value.size()));
  output.push_back(':');
  output.append(value);
  output.push_back('|');
}

std::string expectedChallengeResponse(const std::string& key,
                                      const netsec::ChipCertificate& certificate,
                                      std::string_view challenge) {
  std::string message = "CHALLENGE-RESPONSE/1|";
  appendField(message, certificate.chip_id);
  appendField(message, certificate.model);
  appendField(message, certificate.physical_fingerprint_commitment);
  appendField(message, challenge);
  return netsec::hmacSha256Hex(key, message);
}

std::string expectedProbeOutput(const std::string& key,
                                const netsec::ChipCertificate& certificate,
                                std::string_view challenge) {
  std::string message = "PERFORMANCE-PROBE/1|";
  appendField(message, certificate.chip_id);
  appendField(message, certificate.model);
  appendField(message, challenge);
  return netsec::hmacSha256Hex(key, message);
}

class SimulatedChip : public netsec::ChipEndpoint {
 public:
  SimulatedChip(netsec::ChipDescriptor descriptor,
                std::optional<netsec::ChipCertificate> certificate,
                std::string challenge_key)
      : descriptor_(std::move(descriptor)),
        certificate_(std::move(certificate)),
        challenge_key_(std::move(challenge_key)) {}

  netsec::ChipDescriptor identify() const override { return descriptor_; }

  std::optional<netsec::ChipCertificate> certificate() const override {
    return certificate_;
  }

  std::string respondToChallenge(
      std::string_view challenge_hex) const override {
    if (challenge_override_.has_value()) {
      return *challenge_override_;
    }
    if (!certificate_.has_value()) {
      return {};
    }
    return expectedChallengeResponse(
        response_key_.empty() ? challenge_key_ : response_key_, *certificate_,
        challenge_hex);
  }

  netsec::PerformanceProbe runPerformanceProbe(
      std::string_view challenge_hex) const override {
    netsec::PerformanceProbe probe;
    probe.cycles = cycles_;
    probe.samples = samples_;
    if (certificate_.has_value()) {
      probe.output_hex = expectedProbeOutput(
          probe_key_.empty() ? challenge_key_ : probe_key_, *certificate_,
          challenge_hex);
    }
    if (probe_override_.has_value()) {
      probe.output_hex = *probe_override_;
    }
    return probe;
  }

  void setCycles(double cycles) { cycles_ = cycles; }
  void setSamples(std::size_t samples) { samples_ = samples; }
  void setResponseKey(std::string key) { response_key_ = std::move(key); }
  void setProbeKey(std::string key) { probe_key_ = std::move(key); }
  void setChallengeOverride(std::string value) {
    challenge_override_ = std::move(value);
  }
  void setProbeOverride(std::string value) {
    probe_override_ = std::move(value);
  }
  void clearCertificate() { certificate_.reset(); }

 private:
  netsec::ChipDescriptor descriptor_;
  std::optional<netsec::ChipCertificate> certificate_;
  std::string challenge_key_;
  std::string response_key_;
  std::string probe_key_;
  std::optional<std::string> challenge_override_;
  std::optional<std::string> probe_override_;
  double cycles_ = 1200.0;
  std::size_t samples_ = 3;
};

struct Fixture {
  netsec::ChipCertificateAuthority authority{"vendor-root-2026",
                                              "authority-signing-key"};
  netsec::ChipDescriptor descriptor{"CHIP-0001", "ACME-SEC-42",
                                    "puf-silicon-0001-7f3a"};
  std::string challenge_key{"chip-provisioned-secret-0001"};
  netsec::ChipCertificate certificate =
      authority.issue(descriptor, challenge_key, 1789000000);
  std::vector<netsec::SupplyChainStep> trace;
  netsec::ChipRegistry registry{authority};

  Fixture() {
    netsec::SupplyChainTraceBuilder builder(challenge_key, descriptor.chip_id);
    builder.append("fab", "foundry-a", "Hsinchu", 1788000000, "lot-17");
    builder.append("assembly", "packager-b", "Penang", 1788100000, "reel-04");
    builder.append("distribution", "distributor-c", "Shenzhen", 1788200000,
                   "invoice-771");
    trace = builder.steps();
    netsec::ChipRegistration registration;
    registration.certificate = certificate;
    registration.challenge_key = challenge_key;
    registration.performance_profile =
        netsec::PerformanceProfile{1200.0, 5.0, 3};
    registration.supply_chain = trace;
    const netsec::RegistrationResult result =
        registry.registerChip(std::move(registration));
    if (!result.accepted) {
      throw std::runtime_error("fixture registration failed: " + result.message);
    }
  }
};

void testCryptoPrimitives() {
  const std::string test = "crypto";
  check(netsec::sha256Hex("abc") ==
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
        test, "SHA-256 known answer failed");
  check(netsec::sha256Hex("") ==
            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        test, "empty SHA-256 known answer failed");
  check(netsec::hmacSha256Hex("key", "The quick brown fox jumps over the lazy dog") ==
            "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8",
        test, "HMAC-SHA-256 known answer failed");
  check(!netsec::constantTimeEqual("abcd", "abce"), test,
        "constant-time compare accepted different values");
}

void testLegitimateChip(Fixture& fixture) {
  const std::string test = "legitimate";
  SimulatedChip chip(fixture.descriptor, fixture.certificate,
                     fixture.challenge_key);
  netsec::ChipAuthenticator authenticator(fixture.registry);
  const netsec::AuthenticationResult result = authenticator.authenticate(chip);

  check(result.trusted, test, result.detail);
  check(result.rejection == netsec::RejectionCode::None, test,
        "legitimate chip returned a rejection code");
  check(result.chip_id == fixture.descriptor.chip_id, test,
        "trusted result has the wrong chip id");
  check(result.trace_head == fixture.trace.back().digest, test,
        "trusted result did not expose the supply-chain head");
  check(authenticator.verifyAuditTrail(), test, "audit trail failed verification");
  check(fixture.registry.verifySupplyChain(fixture.descriptor.chip_id), test,
        "registered supply chain failed verification");
  check(fixture.registry.supplyChainHead(fixture.descriptor.chip_id) ==
            fixture.trace.back().digest,
        test, "supply-chain head lookup failed");
  const auto trace = fixture.registry.supplyChain(fixture.descriptor.chip_id);
  check(trace.has_value() && trace->size() == fixture.trace.size(), test,
        "full supply-chain trace lookup failed");
  const auto certificate =
      fixture.registry.registeredCertificate(fixture.descriptor.chip_id);
  check(certificate.has_value() &&
            certificate->serial == fixture.certificate.serial,
        test, "registered certificate lookup failed");
}

void testUnknownFakeChip(Fixture& fixture) {
  const std::string test = "fake-chip";
  netsec::ChipDescriptor fake_descriptor{"CHIP-FAKE-9000", "ACME-SEC-42",
                                         "refurbished-puf-9000"};
  const std::string fake_key{"attacker-secret"};
  const netsec::ChipCertificate fake_certificate =
      fixture.authority.issue(fake_descriptor, fake_key, 1789000000);
  SimulatedChip fake(fake_descriptor, fake_certificate, fake_key);
  netsec::ChipAuthenticator authenticator(fixture.registry);
  const netsec::AuthenticationResult result = authenticator.authenticate(fake);

  check(!result.trusted, test, "unregistered fake chip was trusted");
  check(result.rejection == netsec::RejectionCode::UnregisteredChip, test,
        "fake chip did not fail as unregistered");
  check(!fixture.registry.isRegistered(fake_descriptor.chip_id), test,
        "fake chip unexpectedly appears in the registry");
}

void testClonedIdentifier(Fixture& fixture) {
  const std::string test = "cloned-id";
  netsec::ChipDescriptor clone_descriptor = fixture.descriptor;
  clone_descriptor.physical_fingerprint = "cloned-puf-different-silicon";
  SimulatedChip clone(clone_descriptor, fixture.certificate,
                      fixture.challenge_key);
  netsec::ChipAuthenticator authenticator(fixture.registry);
  const netsec::AuthenticationResult result = authenticator.authenticate(clone);

  check(!result.trusted, test, "identifier clone was trusted");
  check(result.rejection == netsec::RejectionCode::PhysicalFingerprintMismatch,
        test, "identifier clone did not fail physical binding");

  SimulatedChip keyless_clone(fixture.descriptor, fixture.certificate,
                              fixture.challenge_key);
  keyless_clone.setResponseKey("wrong-clone-key");
  const netsec::AuthenticationResult keyless_result =
      authenticator.authenticate(keyless_clone);
  check(!keyless_result.trusted, test,
        "clone without the chip secret was trusted");
  check(keyless_result.rejection == netsec::RejectionCode::ChallengeRejected,
        test, "clone without the chip secret passed challenge-response");
}

void testPerformanceAnomaly(Fixture& fixture) {
  const std::string test = "performance";
  SimulatedChip slow(fixture.descriptor, fixture.certificate,
                     fixture.challenge_key);
  slow.setCycles(1900.0);
  netsec::ChipAuthenticator authenticator(fixture.registry);
  const netsec::AuthenticationResult result = authenticator.authenticate(slow);

  check(!result.trusted, test, "performance-anomalous chip was trusted");
  check(result.rejection == netsec::RejectionCode::PerformanceAnomaly, test,
        "performance anomaly was not classified correctly");
}

void testModelAndCertificateFailures(Fixture& fixture) {
  const std::string test = "model-certificate";
  netsec::ChipDescriptor wrong_model = fixture.descriptor;
  wrong_model.model = "CLONED-MODEL-X";
  SimulatedChip model_swap(wrong_model, fixture.certificate,
                           fixture.challenge_key);
  netsec::ChipAuthenticator authenticator(fixture.registry);
  const netsec::AuthenticationResult model_result =
      authenticator.authenticate(model_swap);
  check(!model_result.trusted, test, "wrong model was trusted");
  check(model_result.rejection == netsec::RejectionCode::ModelMismatch, test,
        "wrong model was not classified as model mismatch");

  SimulatedChip no_certificate(fixture.descriptor, std::nullopt,
                               fixture.challenge_key);
  const netsec::AuthenticationResult certificate_result =
      authenticator.authenticate(no_certificate);
  check(!certificate_result.trusted, test, "chip without certificate was trusted");
  check(certificate_result.rejection == netsec::RejectionCode::CertificateMissing,
        test, "missing certificate was not classified correctly");

  SimulatedChip bad_response(fixture.descriptor, fixture.certificate,
                             fixture.challenge_key);
  bad_response.setChallengeOverride(std::string(64U, '0'));
  const netsec::AuthenticationResult response_result =
      authenticator.authenticate(bad_response);
  check(!response_result.trusted, test, "invalid challenge response was trusted");
  check(response_result.rejection == netsec::RejectionCode::ChallengeRejected,
        test, "invalid challenge response was not classified correctly");

  const netsec::ChipCertificate replacement_certificate =
      fixture.authority.issue(fixture.descriptor, fixture.challenge_key,
                              1789000999);
  SimulatedChip replaced_certificate(fixture.descriptor,
                                     replacement_certificate,
                                     fixture.challenge_key);
  const netsec::AuthenticationResult replacement_result =
      authenticator.authenticate(replaced_certificate);
  check(!replacement_result.trusted, test,
        "unregistered replacement certificate was trusted");
  check(replacement_result.rejection ==
            netsec::RejectionCode::CertificateBindingMismatch,
        test, "replacement certificate was not rejected as a binding mismatch");
}

void testTamperedCertificateAndBehavior(Fixture& fixture) {
  const std::string test = "tamper";
  netsec::ChipCertificate tampered_certificate = fixture.certificate;
  tampered_certificate.model = "TAMPERED-MODEL";
  netsec::ChipDescriptor tampered_descriptor = fixture.descriptor;
  tampered_descriptor.model = "TAMPERED-MODEL";
  SimulatedChip tampered_chip(tampered_descriptor, tampered_certificate,
                              fixture.challenge_key);
  netsec::ChipAuthenticator authenticator(fixture.registry);
  const netsec::AuthenticationResult certificate_result =
      authenticator.authenticate(tampered_chip);
  check(!certificate_result.trusted, test, "tampered certificate was trusted");
  check(certificate_result.rejection == netsec::RejectionCode::CertificateInvalid,
        test, "tampered certificate was not rejected");

  SimulatedChip bad_behavior(fixture.descriptor, fixture.certificate,
                             fixture.challenge_key);
  bad_behavior.setProbeOverride(std::string(64U, 'a'));
  const netsec::AuthenticationResult behavior_result =
      authenticator.authenticate(bad_behavior);
  check(!behavior_result.trusted, test, "altered probe behavior was trusted");
  check(behavior_result.rejection == netsec::RejectionCode::ChallengeRejected,
        test, "altered probe behavior was not rejected");
}

void testSupplyChainRejectsTampering(Fixture& fixture) {
  const std::string test = "supply-chain";
  std::vector<netsec::SupplyChainStep> tampered = fixture.trace;
  tampered[1].actor = "unauthorized-broker";
  std::string error;
  check(!netsec::verifySupplyChain(tampered, fixture.challenge_key,
                                   fixture.descriptor.chip_id, &error),
        test, "tampered supply-chain record passed verification");

  netsec::ChipDescriptor second_descriptor{
      "CHIP-0002", "ACME-SEC-42", "puf-silicon-0002"};
  const std::string second_key{"chip-provisioned-secret-0002"};
  netsec::SupplyChainTraceBuilder second_builder(second_key,
                                                 second_descriptor.chip_id);
  second_builder.append("fab", "foundry-a", "Hsinchu", 1788000000, "lot-18");
  second_builder.append("assembly", "packager-b", "Penang", 1788100000,
                        "reel-05");
  second_builder.append("distribution", "distributor-c", "Shenzhen",
                        1788200000, "invoice-772");
  auto second_trace = second_builder.steps();
  second_trace[1].actor = "unauthorized-broker";

  netsec::ChipRegistration registration;
  registration.certificate =
      fixture.authority.issue(second_descriptor, second_key, 1789000100);
  registration.challenge_key = second_key;
  registration.performance_profile =
      netsec::PerformanceProfile{1200.0, 5.0, 3};
  registration.supply_chain = std::move(second_trace);
  const netsec::RegistrationResult result =
      fixture.registry.registerChip(std::move(registration));
  check(!result.accepted, test,
        "registry accepted a chip with a tampered supply chain");
  check(!fixture.registry.isRegistered(second_descriptor.chip_id), test,
        "tampered chip was inserted into the registry");
}

}  // namespace

int main() {
  try {
    testCryptoPrimitives();
    Fixture fixture;
    testLegitimateChip(fixture);
    testUnknownFakeChip(fixture);
    testClonedIdentifier(fixture);
    testPerformanceAnomaly(fixture);
    testModelAndCertificateFailures(fixture);
    testTamperedCertificateAndBehavior(fixture);
    testSupplyChainRejectsTampering(fixture);
  } catch (const std::exception& exception) {
    std::cerr << "FATAL: " << exception.what() << '\n';
    return 1;
  }

  if (g_failures != 0) {
    std::cerr << g_failures << " assertion(s) failed\n";
    return 1;
  }
  std::cout << "all chip identity tests passed\n";
  return 0;
}
