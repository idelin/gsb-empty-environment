#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

std::string sha256Hex(std::string_view input);
std::string hmacSha256Hex(std::string_view key, std::string_view input);
bool constantTimeEqual(std::string_view lhs, std::string_view rhs);

struct PerformanceProfile {
  double nominal_cycles = 0.0;
  double tolerance_percent = 5.0;
  std::size_t minimum_samples = 1;
};

struct ChipDescriptor {
  std::string chip_id;
  std::string model;
  std::string physical_fingerprint;
};

struct ChipCertificate {
  std::string version = "CHIP-ATTESTATION/1";
  std::string serial;
  std::string chip_id;
  std::string model;
  std::string physical_fingerprint_commitment;
  std::string challenge_key_commitment;
  std::string issuer;
  std::uint64_t issued_at = 0;
  std::string signature;
};

struct PerformanceProbe {
  std::string output_hex;
  double cycles = 0.0;
  std::size_t samples = 0;
};

struct ChipObservation {
  ChipDescriptor descriptor;
  std::optional<ChipCertificate> certificate;
  std::string challenge_response_hex;
  PerformanceProbe performance;
};

class ChipEndpoint {
 public:
  virtual ~ChipEndpoint() = default;

  virtual ChipDescriptor identify() const = 0;
  virtual std::optional<ChipCertificate> certificate() const = 0;
  virtual std::string respondToChallenge(std::string_view challenge_hex) const = 0;
  virtual PerformanceProbe runPerformanceProbe(
      std::string_view challenge_hex) const = 0;
};

class ChipCertificateAuthority {
 public:
  ChipCertificateAuthority(std::string issuer, std::string signing_key);

  ChipCertificate issue(const ChipDescriptor& descriptor,
                        std::string_view challenge_key,
                        std::uint64_t issued_at = 0) const;
  bool verify(const ChipCertificate& certificate) const;

  const std::string& issuer() const;

 private:
  std::string issuer_;
  std::string signing_key_;
};

struct SupplyChainStep {
  std::string chip_id;
  std::uint64_t sequence = 0;
  std::string stage;
  std::string actor;
  std::string location;
  std::uint64_t timestamp_unix = 0;
  std::string metadata;
  std::string previous_digest;
  std::string digest;
};

class SupplyChainTraceBuilder {
 public:
  SupplyChainTraceBuilder(std::string signing_key, std::string chip_id);

  SupplyChainStep append(std::string stage, std::string actor,
                         std::string location, std::uint64_t timestamp_unix,
                         std::string metadata = {});

  const std::vector<SupplyChainStep>& steps() const;
  std::string headDigest() const;
  bool verify(std::string* error = nullptr) const;

 private:
  std::string signing_key_;
  std::string chip_id_;
  std::vector<SupplyChainStep> steps_;
};

bool verifySupplyChain(const std::vector<SupplyChainStep>& steps,
                       std::string_view signing_key,
                       std::string_view expected_chip_id,
                       std::string* error = nullptr);

struct ChipRegistration {
  ChipCertificate certificate;
  std::string challenge_key;
  PerformanceProfile performance_profile;
  std::vector<SupplyChainStep> supply_chain;
};

struct RegistrationResult {
  bool accepted = false;
  std::string message;
};

class ChipRegistry {
 public:
  explicit ChipRegistry(ChipCertificateAuthority authority);

  RegistrationResult registerChip(ChipRegistration registration);
  bool isRegistered(std::string_view chip_id) const;
  std::size_t size() const;

  bool verifySupplyChain(std::string_view chip_id,
                         std::string* error = nullptr) const;
  std::optional<std::string> supplyChainHead(std::string_view chip_id) const;
  std::optional<std::vector<SupplyChainStep>> supplyChain(
      std::string_view chip_id) const;
  std::optional<ChipCertificate> registeredCertificate(
      std::string_view chip_id) const;

 private:
  friend class ChipAuthenticator;

  struct StoredChip {
    ChipRegistration registration;
  };

  ChipCertificateAuthority authority_;
  std::vector<StoredChip> chips_;
};

enum class RejectionCode {
  None,
  UnregisteredChip,
  ModelMismatch,
  CertificateMissing,
  CertificateInvalid,
  CertificateBindingMismatch,
  PhysicalFingerprintMismatch,
  ChallengeRejected,
  PerformanceAnomaly,
  SupplyChainUntrusted,
  MalformedResponse,
  EndpointFailure,
};

std::string_view rejectionCodeName(RejectionCode code);

struct AuthenticationOptions {
  std::size_t challenge_bytes = 32;
  bool require_supply_chain = true;
  bool require_performance_fingerprint = true;
  bool require_probe_behavior = true;
};

struct AuthenticationResult {
  bool trusted = false;
  RejectionCode rejection = RejectionCode::None;
  std::string detail;
  std::string chip_id;
  std::string challenge_hex;
  std::string trace_head;
  std::uint64_t audit_sequence = 0;
};

struct AuditRecord {
  std::uint64_t sequence = 0;
  std::uint64_t timestamp_unix = 0;
  std::string chip_id;
  bool trusted = false;
  RejectionCode rejection = RejectionCode::None;
  std::string challenge_digest;
  std::string trace_head;
  std::string previous_digest;
  std::string digest;
};

class ChipAuthenticator {
 public:
  explicit ChipAuthenticator(const ChipRegistry& registry,
                             AuthenticationOptions options = {});

  AuthenticationResult authenticate(ChipEndpoint& endpoint);

  const std::vector<AuditRecord>& auditTrail() const;
  bool verifyAuditTrail(std::string* error = nullptr) const;

 private:
  AuthenticationResult authenticateObservation(
      ChipObservation observation, std::string challenge_hex);
  void appendAudit(AuthenticationResult& result, std::string_view challenge,
                   std::string_view trace_head);

  const ChipRegistry& registry_;
  AuthenticationOptions options_;
  std::vector<AuditRecord> audit_;
};

}  // namespace netsec
