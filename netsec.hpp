#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Bytes = std::vector<std::uint8_t>;
using Clock = std::chrono::system_clock;
using TimePoint = Clock::time_point;

enum class GeneralNameType {
  OtherName,
  Rfc822Name,
  DnsName,
  X400Address,
  DirectoryName,
  EdiPartyName,
  UniformResourceIdentifier,
  IpAddress,
  RegisteredId,
  Unknown,
};

struct GeneralName {
  GeneralNameType type = GeneralNameType::Unknown;
  std::uint32_t context_tag = 0;
  Bytes der;
  Bytes value;
  std::string text;
  std::string canonical;

  [[nodiscard]] bool valid() const noexcept { return !canonical.empty(); }
};

enum class DistributionPointNameType {
  FullName,
  NameRelativeToCrlIssuer,
};

struct DistributionPointName {
  DistributionPointNameType type = DistributionPointNameType::FullName;
  std::vector<GeneralName> full_names;
  Bytes relative_name_der;
  std::string relative_name_canonical;

  [[nodiscard]] bool empty() const noexcept;
  [[nodiscard]] bool
  intersects(const DistributionPointName &other) const noexcept;
};

// ReasonFlags uses the bit positions from RFC 5280.  The public mask stores
// reasonCode values (1..6, 9, 10) rather than raw BIT STRING positions.
struct ReasonFlags {
  bool present = false;
  std::uint16_t reason_mask = 0;

  [[nodiscard]] bool includes(int reason_code) const noexcept;
  [[nodiscard]] bool covers_all_reasons() const noexcept;
};

struct DistinguishedName {
  Bytes der;
  std::string canonical;
  std::string canonical_sorted;

  [[nodiscard]] bool empty() const noexcept { return der.empty(); }
  [[nodiscard]] bool equivalent(const DistinguishedName &other) const noexcept;
};

struct CrlScope {
  std::optional<DistributionPointName> distribution_point;
  bool only_contains_user_certs = false;
  bool only_contains_ca_certs = false;
  bool only_contains_attribute_certs = false;
  bool indirect_crl = false;
  ReasonFlags only_some_reasons;

  [[nodiscard]] bool restricts_certificate_type() const noexcept;
};

struct CrlDistributionPoint {
  std::optional<DistributionPointName> distribution_point;
  ReasonFlags reasons;
  std::vector<GeneralName> crl_issuers;
};

struct CrlEntry {
  Bytes serial_number;
  TimePoint revocation_date{};
  std::optional<int> reason_code;
  std::vector<GeneralName> certificate_issuer;
  std::optional<TimePoint> invalidity_date;

  [[nodiscard]] bool removes_from_crl() const noexcept {
    return reason_code.has_value() && *reason_code == 8;
  }
};

struct CrlInfo {
  Bytes der;
  Bytes tbs_cert_list;
  DistinguishedName issuer;
  TimePoint this_update{};
  std::optional<TimePoint> next_update;
  std::vector<CrlEntry> entries;
  std::optional<std::uint64_t> crl_number;
  // Present only for delta CRLs.  It is the cRLNumber of the complete
  // base CRL to which this delta applies.
  std::optional<std::uint64_t> base_crl_number;
  CrlScope scope;
  std::string signature_algorithm_oid;
  Bytes signature;
  Bytes authority_key_identifier;
  std::vector<GeneralName> issuer_alt_names;
  std::vector<std::string> critical_extension_oids;

  [[nodiscard]] bool is_delta() const noexcept {
    return base_crl_number.has_value();
  }
  [[nodiscard]] bool is_indirect() const noexcept { return scope.indirect_crl; }
};

struct CertificateInfo {
  Bytes der;
  Bytes tbs_certificate;
  DistinguishedName issuer;
  DistinguishedName subject;
  Bytes serial_number;
  TimePoint not_before{};
  TimePoint not_after{};
  Bytes validity_der;
  Bytes subject_public_key_info;
  bool basic_constraints_present = false;
  bool is_ca = false;
  std::optional<std::uint32_t> path_length_constraint;
  bool key_usage_present = false;
  std::uint16_t key_usage_bits = 0;
  Bytes subject_key_identifier;
  Bytes authority_key_identifier;
  std::vector<CrlDistributionPoint> crl_distribution_points;
  std::string signature_algorithm_oid;
  Bytes signature;
  std::vector<std::string> critical_extension_oids;

  [[nodiscard]] bool can_sign_crls() const noexcept;
  [[nodiscard]] bool valid_at(TimePoint at) const noexcept;
};

enum class CrlError {
  None = 0,
  EmptyInput,
  MalformedDer,
  MalformedCertificate,
  MalformedCrl,
  UnsupportedVersion,
  UnsupportedAlgorithm,
  InvalidSignature,
  Expired,
  NotYetValid,
  MissingNextUpdate,
  UnknownCriticalExtension,
  IssuerMismatch,
  UnauthorizedCrlIssuer,
  CrlSignerNotAuthorized,
  ScopeMismatch,
  NotApplicable,
  CrlNumberMissing,
  DeltaWithoutBase,
  DeltaGap,
  DeltaScopeMismatch,
  BaseCrlNumberMismatch,
  CertificateIssuerMismatch,
};

[[nodiscard]] std::string_view to_string(CrlError error) noexcept;

enum class RevocationStatus {
  Good,
  Revoked,
  Unknown,
};

[[nodiscard]] std::string_view to_string(RevocationStatus status) noexcept;

struct CrlValidationResult {
  // valid: the CRL evidence was parsed, authenticated, fresh enough and
  //        usable for this certificate.
  bool valid = false;
  // applicable: the CRL's issuer and issuing-distribution-point scope cover
  //             this certificate.
  bool applicable = false;
  bool revoked = false;
  RevocationStatus status = RevocationStatus::Unknown;
  CrlError error = CrlError::None;
  std::string detail;
  std::optional<std::size_t> matching_crl_index;
  std::optional<std::uint64_t> crl_number;
  std::optional<std::uint64_t> base_crl_number;
  std::optional<CrlEntry> revocation_entry;

  [[nodiscard]] bool ok() const noexcept {
    return valid && applicable && status != RevocationStatus::Unknown;
  }

  [[nodiscard]] bool has_revocation_evidence() const noexcept {
    return applicable && (status == RevocationStatus::Good ||
                          status == RevocationStatus::Revoked);
  }

  explicit operator bool() const noexcept { return ok(); }
};

struct ValidationContext {
  TimePoint now = Clock::now();
  std::chrono::seconds clock_skew{0};

  // Additional intermediates used to authenticate a delegated CRL signer.
  std::vector<Bytes> additional_certificates;

  // Certificates explicitly authorized to sign CRLs for the certificates
  // being checked.  These are trust anchors for CRL-signing delegation;
  // their CRL signatures are still verified cryptographically.
  std::vector<Bytes> trusted_crl_signer_certificates;

  // When true, a cRLIssuer name in the certificate's cRLDistributionPoints
  // extension is treated as an explicit delegation, provided the signer
  // certificate chains to the certificate's issuer and has cRLSign.
  bool allow_delegation_from_crl_distribution_point = true;

  // If a signer certificate carries keyUsage, cRLSign must be asserted.
  bool require_crl_signer_key_usage = true;

  // Unknown critical CRL extensions always cause rejection.  This option
  // applies the same rule to certificates used as CRL signers.
  bool reject_unknown_critical_certificate_extensions = true;

  bool require_next_update = false;
  bool reject_future_this_update = true;

  // Revocation checking normally does not reject already-expired end
  // entities; enable this when the service wants that additional check.
  bool reject_expired_certificate = false;
};

// ---------------------------------------------------------------------------
// Parsing and inspection API

[[nodiscard]] std::optional<CrlInfo>
parse_crl(std::span<const std::uint8_t> der);
[[nodiscard]] std::optional<CrlInfo> parse_crl(const Bytes &der);

[[nodiscard]] std::optional<CertificateInfo>
parse_certificate(std::span<const std::uint8_t> der);
[[nodiscard]] std::optional<CertificateInfo>
parse_certificate(const Bytes &der);

// Diagnostic text for the most recent parse_crl()/parse_certificate() failure
// on the calling thread.
[[nodiscard]] std::string last_parse_error();

// ---------------------------------------------------------------------------
// Validation API

// Validates one complete CRL against a certificate.  A delta CRL supplied on
// its own is rejected with DeltaWithoutBase.  Use the vector overload below to
// supply a base and its delta chain.
[[nodiscard]] CrlValidationResult
validate_crl(const CertificateInfo &certificate,
             const CertificateInfo &issuer_certificate, const CrlInfo &crl,
             const ValidationContext &context = {});

[[nodiscard]] CrlValidationResult
validate_crl(const Bytes &certificate_der, const Bytes &issuer_certificate_der,
             const Bytes &crl_der, const ValidationContext &context = {});

// Checks revocation status using all supplied CRLs.  Base and delta CRLs are
// assembled by issuer/scope and by cRLNumber.  If the supplied delta chain has
// a gap, the result is Unknown (fail closed), never Good.
[[nodiscard]] CrlValidationResult
check_revocation(const CertificateInfo &certificate,
                 const CertificateInfo &issuer_certificate,
                 const std::vector<CrlInfo> &crls,
                 const ValidationContext &context = {});

[[nodiscard]] CrlValidationResult check_revocation(
    const Bytes &certificate_der, const Bytes &issuer_certificate_der,
    const std::vector<Bytes> &crl_ders, const ValidationContext &context = {});

[[nodiscard]] CrlValidationResult
check_revocation(const Bytes &certificate_der,
                 const Bytes &issuer_certificate_der, const Bytes &crl_der,
                 const ValidationContext &context = {});

// Convenience entry point for a service that wants certificate validity,
// issuer signature, critical-extension checks and revocation all in one call.
[[nodiscard]] CrlValidationResult validate_certificate(
    const Bytes &certificate_der, const Bytes &issuer_certificate_der,
    const std::vector<Bytes> &crl_ders, const ValidationContext &context = {});

[[nodiscard]] CrlValidationResult
validate_certificate(const Bytes &certificate_der,
                     const Bytes &issuer_certificate_der, const Bytes &crl_der,
                     const ValidationContext &context = {});

// Small stateful facade for services that import CRLs once and then evaluate
// many certificates.
class CrlScopeComponent {
public:
  explicit CrlScopeComponent(ValidationContext context = {});

  // Returns false for malformed CRLs.  Freshness, signatures and scope are
  // checked when the CRLs are used against a certificate because those
  // checks depend on the selected issuer/signer and validation time.
  bool import_crl(const Bytes &crl_der);
  bool import_crl(std::span<const std::uint8_t> crl_der);
  bool importCrl(const Bytes &crl_der) { return import_crl(crl_der); }

  void clear();

  [[nodiscard]] const std::vector<CrlInfo> &crls() const noexcept {
    return crls_;
  }
  [[nodiscard]] const std::vector<std::string> &import_errors() const noexcept {
    return import_errors_;
  }

  [[nodiscard]] CrlValidationResult
  check(const Bytes &certificate_der, const Bytes &issuer_certificate_der,
        const ValidationContext *override_context = nullptr) const;

  [[nodiscard]] CrlValidationResult
  validate(const Bytes &certificate_der, const Bytes &issuer_certificate_der,
           const ValidationContext *override_context = nullptr) const {
    return check(certificate_der, issuer_certificate_der, override_context);
  }

  [[nodiscard]] const ValidationContext &context() const noexcept {
    return context_;
  }
  ValidationContext &context() noexcept { return context_; }

private:
  ValidationContext context_;
  std::vector<CrlInfo> crls_;
  std::vector<std::string> import_errors_;
};

// Conventional aliases used by certificate-validation front ends.
using CRLInfo = CrlInfo;
using CRL = CrlInfo;
using Crl = CrlInfo;
using CRLScope = CrlScope;
using CRLValidationResult = CrlValidationResult;
using RevocationResult = CrlValidationResult;
using CrlScopeImporter = CrlScopeComponent;
using CrlStore = CrlScopeComponent;
using CrlValidator = CrlScopeComponent;
using CrlValidationStatus = RevocationStatus;
using CrlScopeResult = CrlValidationResult;
using CrlValidationError = CrlError;

// Camel-case adapters for services that expose these names.
inline std::optional<CrlInfo> parseCrl(const Bytes &der) {
  return parse_crl(der);
}

inline std::optional<CertificateInfo> parseCertificate(const Bytes &der) {
  return parse_certificate(der);
}

inline CrlValidationResult
checkRevocation(const Bytes &certificate_der,
                const Bytes &issuer_certificate_der, const Bytes &crl_der,
                const ValidationContext &context = {}) {
  return check_revocation(certificate_der, issuer_certificate_der, crl_der,
                          context);
}

inline CrlValidationResult checkRevocation(
    const Bytes &certificate_der, const Bytes &issuer_certificate_der,
    const std::vector<Bytes> &crl_ders, const ValidationContext &context = {}) {
  return check_revocation(certificate_der, issuer_certificate_der, crl_ders,
                          context);
}

inline CrlValidationResult validateCrl(const Bytes &certificate_der,
                                       const Bytes &issuer_certificate_der,
                                       const Bytes &crl_der,
                                       const ValidationContext &context = {}) {
  return validate_crl(certificate_der, issuer_certificate_der, crl_der,
                      context);
}

inline CrlValidationResult
checkCrlScope(const Bytes &certificate_der, const Bytes &issuer_certificate_der,
              const Bytes &crl_der, const ValidationContext &context = {}) {
  return validate_crl(certificate_der, issuer_certificate_der, crl_der,
                      context);
}

inline CrlValidationResult
check_crl_scope(const Bytes &certificate_der,
                const Bytes &issuer_certificate_der, const Bytes &crl_der,
                const ValidationContext &context = {}) {
  return validate_crl(certificate_der, issuer_certificate_der, crl_der,
                      context);
}

inline bool is_revoked(const Bytes &certificate_der,
                       const Bytes &issuer_certificate_der,
                       const Bytes &crl_der,
                       const ValidationContext &context = {}) {
  return check_revocation(certificate_der, issuer_certificate_der, crl_der,
                          context)
             .status == RevocationStatus::Revoked;
}

inline CrlValidationResult validateCertificate(
    const Bytes &certificate_der, const Bytes &issuer_certificate_der,
    const std::vector<Bytes> &crl_ders, const ValidationContext &context = {}) {
  return validate_certificate(certificate_der, issuer_certificate_der, crl_ders,
                              context);
}

inline bool isRevoked(const Bytes &certificate_der,
                      const Bytes &issuer_certificate_der, const Bytes &crl_der,
                      const ValidationContext &context = {}) {
  return check_revocation(certificate_der, issuer_certificate_der, crl_der,
                          context)
             .status == RevocationStatus::Revoked;
}

namespace oid {
inline constexpr std::string_view common_name = "2.5.4.3";
inline constexpr std::string_view authority_key_identifier = "2.5.29.35";
inline constexpr std::string_view subject_key_identifier = "2.5.29.14";
inline constexpr std::string_view key_usage = "2.5.29.15";
inline constexpr std::string_view subject_alt_name = "2.5.29.17";
inline constexpr std::string_view issuer_alt_name = "2.5.29.18";
inline constexpr std::string_view basic_constraints = "2.5.29.19";
inline constexpr std::string_view crl_number = "2.5.29.20";
inline constexpr std::string_view reason_code = "2.5.29.21";
inline constexpr std::string_view invalidity_date = "2.5.29.24";
inline constexpr std::string_view delta_crl_indicator = "2.5.29.27";
inline constexpr std::string_view issuing_distribution_point = "2.5.29.28";
inline constexpr std::string_view certificate_issuer = "2.5.29.29";
inline constexpr std::string_view c_rl_distribution_points = "2.5.29.31";
inline constexpr std::string_view freshest_crl = "2.5.29.46";
inline constexpr std::string_view authority_info_access = "1.3.6.1.5.5.7.1.1";
} // namespace oid

} // namespace netsec
