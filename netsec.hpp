#pragma once

// RFC 3161 timestamp-token import and artifact-binding verification.
//
// The component is intentionally self contained: it does not require an
// external TLS/crypto library at link time.  A caller normally:
//
//   1. imports a DER (or PEM/Base64) timestamp token,
//   2. computes the digest of the artifact it wants to bind, and
//   3. calls TimestampVerifier::verify().
//
// Verification checks the messageImprint (algorithm and bytes), nonce,
// TSTInfo serialNumber, issuer/TSA, genTime, the CMS signature and signed
// attributes, the X.509 path to a configured trust anchor, and revocation
// information supplied as CRLs/OCSP responses or by a callback.

#include <chrono>
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
using ByteView = std::span<const std::uint8_t>;
using Clock = std::chrono::system_clock;
using TimePoint = Clock::time_point;

// ---------------------------------------------------------------------------
// Digests
// ---------------------------------------------------------------------------

enum class HashAlgorithm {
    Unknown = 0,
    Md5,
    Sha1,
    Sha224,
    Sha256,
    Sha384,
    Sha512,
};

using DigestAlgorithm = HashAlgorithm;
using Hash = HashAlgorithm;

std::string_view to_string(HashAlgorithm algorithm) noexcept;
std::optional<HashAlgorithm> hash_algorithm_from_name(std::string_view name);
std::optional<HashAlgorithm> hash_algorithm_from_oid(std::string_view oid);
std::string_view hash_algorithm_oid(HashAlgorithm algorithm) noexcept;
std::size_t hash_output_size(HashAlgorithm algorithm) noexcept;

struct Digest {
    HashAlgorithm algorithm = HashAlgorithm::Unknown;
    Bytes bytes;

    bool empty() const noexcept { return bytes.empty(); }

    static Digest compute(HashAlgorithm algorithm, ByteView data);
    static Digest compute(HashAlgorithm algorithm, std::string_view data);
    static Digest compute_file(HashAlgorithm algorithm, const std::string& path,
                               std::string* error = nullptr);

    friend bool operator==(const Digest& lhs, const Digest& rhs) noexcept {
        return lhs.algorithm == rhs.algorithm && lhs.bytes == rhs.bytes;
    }
    friend bool operator!=(const Digest& lhs, const Digest& rhs) noexcept {
        return !(lhs == rhs);
    }
};

using TimestampDigest = Digest;

// ---------------------------------------------------------------------------
// Results and policy
// ---------------------------------------------------------------------------

enum class ErrorCode {
    Ok = 0,
    InvalidDer,
    UnsupportedAlgorithm,
    UnsupportedStructure,
    InvalidTokenStructure,
    InvalidContentType,
    ContentDigestMismatch,
    SignedAttributeInvalid,
    MessageImprintMismatch,
    DigestAlgorithmMismatch,
    MissingNonce,
    NonceMismatch,
    SerialMismatch,
    IssuerMismatch,
    TsaMismatch,
    PolicyMismatch,
    InvalidTime,
    TimeOutsidePolicy,
    CertificateInvalid,
    CertificateTimeInvalid,
    CertificatePurposeInvalid,
    ChainUntrusted,
    SignatureInvalid,
    Revoked,
    RevocationUnknown,
    IoError,
    InternalError,

    // Common alternate spellings kept as aliases for integration code.
    TokenMalformed = InvalidTokenStructure,
    DigestMismatch = MessageImprintMismatch,
    HashAlgorithmMismatch = DigestAlgorithmMismatch,
    NonceRequired = MissingNonce,
    InvalidSignature = SignatureInvalid,
    CertificateRevoked = Revoked,
    UntrustedChain = ChainUntrusted,
};

std::string_view to_string(ErrorCode code) noexcept;

struct VerificationResult {
    // Default construction fails closed; use VerificationResult::success()
    // when constructing a successful result explicitly.
    ErrorCode code = ErrorCode::InternalError;
    std::string message;

    bool message_imprint_checked = false;
    bool digest_algorithm_checked = false;
    bool nonce_checked = false;
    bool serial_checked = false;
    bool issuer_checked = false;
    bool time_checked = false;
    bool signed_attributes_checked = false;
    bool signature_checked = false;
    bool chain_checked = false;
    bool revocation_checked = false;

    bool ok() const noexcept { return code == ErrorCode::Ok; }
    explicit operator bool() const noexcept { return ok(); }

    static VerificationResult success();
    static VerificationResult failure(ErrorCode code, std::string message);
};

using TokenBindingResult = VerificationResult;

enum class RevocationMode {
    // Check supplied CRLs/OCSP responses and a callback when present.  If no
    // evidence is supplied the token is still accepted, but the result records
    // that revocation was not checked.
    IfProvided,
    // Require positive Good/NotRevoked evidence for every certificate in the
    // path.  Missing evidence fails with RevocationUnknown.
    Required,
    Disabled,
};

struct VerificationPolicy {
    // The messageImprint algorithm must be exactly this algorithm.  When
    // unset, the algorithm carried by the token is used and must match the
    // Digest supplied by the caller.
    std::optional<HashAlgorithm> required_digest_algorithm;

    // Replay protection.  A timestamp without a nonce is rejected by default
    // because it can be replayed against any artifact with the same digest.
    bool require_nonce = true;
    std::optional<std::string> expected_nonce;
    bool require_expected_nonce = false;

    // Optional exact checks for TSTInfo fields.
    std::optional<std::string> expected_serial_number;
    std::optional<std::string> expected_issuer;
    std::optional<std::string> expected_tsa;
    std::optional<std::string> expected_policy_oid;

    // Time window for genTime.  evaluation_time defaults to Clock::now().
    std::optional<TimePoint> not_before;
    std::optional<TimePoint> not_after;
    std::optional<TimePoint> evaluation_time;
    std::chrono::seconds clock_skew{0};
    bool reject_future_timestamps = true;

    // Signature/path validation.
    bool verify_signature = true;
    bool verify_signed_attributes = true;
    bool verify_chain = true;
    bool require_trust_anchor = true;
    bool verify_certificate_time = true;
    bool verify_tsa_purpose = true;
    bool require_signer_certificate = true;

    // Legacy token compatibility.  Applications processing only modern
    // tokens should set this to false to reject MD5/SHA-1 messageImprints.
    bool allow_weak_message_imprint = true;

    RevocationMode revocation = RevocationMode::IfProvided;
};

using TokenBindingOptions = VerificationPolicy;

struct TrustStore {
    // DER encoded trust anchors and optional intermediate certificates.
    std::vector<Bytes> trust_anchors;
    std::vector<Bytes> intermediates;

    // Trusting an anchor shipped inside an untrusted token defeats path
    // validation.  It is available only for isolated test harnesses.
    bool allow_embedded_trust_anchor = false;

    void add_anchor(Bytes der) { trust_anchors.push_back(std::move(der)); }
    bool add_anchor(std::string_view pem) { return add_anchor_pem(pem); }
    void add_anchor_der(Bytes der) { add_anchor(std::move(der)); }
    void add_intermediate(Bytes der) { intermediates.push_back(std::move(der)); }
    bool add_intermediate(std::string_view pem) {
        return add_intermediate_pem(pem);
    }
    void add_intermediate_der(Bytes der) { add_intermediate(std::move(der)); }

    bool add_anchor_pem(std::string_view pem);
    bool add_intermediate_pem(std::string_view pem);
    bool add_anchor_file(const std::string& path);
    bool add_intermediate_file(const std::string& path);
};

enum class RevocationStatus {
    NotChecked,
    Good,
    Revoked,
    Unknown,
    NotRevoked = Good,
};

std::string_view to_string(RevocationStatus status) noexcept;

struct RevocationEvidence {
    std::vector<Bytes> crls;             // DER or PEM CRLs
    std::vector<Bytes> ocsp_responses;   // DER OCSPResponse values
    // Optional caller-maintained serial lists (decimal or hex strings).
    std::vector<std::string> revoked_serials;
    std::vector<std::string> good_serials;
};

// A caller-supplied OCSP/CRL/remote-status adapter.  The callback receives
// the certificate being checked and its issuer (which may be the anchor).
using RevocationChecker =
    std::function<RevocationStatus(const struct CertificateInfo& certificate,
                                   const struct CertificateInfo& issuer)>;

// ---------------------------------------------------------------------------
// RFC 3161 / CMS parsed values
// ---------------------------------------------------------------------------

struct MessageImprint {
    HashAlgorithm algorithm = HashAlgorithm::Unknown;
    std::string algorithm_oid;
    Bytes digest;

    HashAlgorithm hash_algorithm() const noexcept { return algorithm; }
    const Bytes& hashed_message() const noexcept { return digest; }
};

struct Accuracy {
    std::optional<std::uint32_t> seconds;
    std::optional<std::uint32_t> millis;
    std::optional<std::uint32_t> micros;
};

struct TimestampInfo {
    std::int32_t version = 1;
    std::string policy_oid;
    MessageImprint message_imprint;
    std::string serial_number;       // canonical decimal representation
    bool serial_fits_uint64 = false;
    std::uint64_t serial_uint64 = 0;
    TimePoint gen_time{};
    std::string gen_time_utc;        // normalized YYYYMMDDHHMMSSZ
    Accuracy accuracy;
    bool ordering = false;
    std::optional<std::string> nonce;  // canonical decimal representation
    bool nonce_fits_uint64 = false;
    std::uint64_t nonce_uint64 = 0;
    std::optional<std::string> tsa_name;
    Bytes tsa_name_der;
    Bytes raw_der;

    bool has_nonce() const noexcept { return nonce.has_value(); }
    std::optional<std::uint64_t> nonce_value() const noexcept {
        if (!nonce_fits_uint64) return std::nullopt;
        return nonce_uint64;
    }
    std::optional<std::uint64_t> serial_value() const noexcept {
        if (!serial_fits_uint64) return std::nullopt;
        return serial_uint64;
    }
};

struct CertificateInfo {
    Bytes der;
    std::string subject;
    std::string issuer;
    std::string serial_number;
    bool serial_fits_uint64 = false;
    std::uint64_t serial_uint64 = 0;
    TimePoint not_before{};
    TimePoint not_after{};
    std::string not_before_utc;
    std::string not_after_utc;
    bool is_ca = false;
    bool has_basic_constraints = false;
    bool basic_constraints_critical = false;
    bool self_signed = false;
    std::string subject_key_id;      // lowercase hex
    std::string authority_key_id;    // lowercase hex
    bool has_time_stamping_eku = false;
    bool has_extended_key_usage = false;
    bool extended_key_usage_critical = false;
    bool has_key_usage = false;
    bool key_usage_critical = false;
    bool key_usage_digital_signature = false;
    bool key_usage_non_repudiation = false;
    bool key_usage_key_cert_sign = false;
    Bytes subject_der;
    Bytes issuer_der;
    Bytes spki_der;

    bool is_time_stamping_certificate() const noexcept {
        return has_time_stamping_eku;
    }
    bool is_ca_certificate() const noexcept { return is_ca; }
};

using Certificate = CertificateInfo;
using X509Certificate = CertificateInfo;
using TSTInfo = TimestampInfo;
using TstInfo = TimestampInfo;

enum class SignerIdentifierType {
    Unknown,
    IssuerAndSerialNumber,
    SubjectKeyIdentifier,
};

namespace detail {
struct ParsedTimestampToken;
}

struct ImportResult;

struct ImportedTimestampToken {
    Bytes raw_der;
    TimestampInfo tst_info;
    std::vector<CertificateInfo> certificates;
    std::optional<CertificateInfo> signer_certificate;
    std::string signer_issuer;
    std::string signer_serial;
    SignerIdentifierType signer_identifier_type = SignerIdentifierType::Unknown;
    std::vector<std::string> cms_digest_algorithms;
    std::string signer_digest_algorithm_oid;
    std::string signature_algorithm_oid;

    std::shared_ptr<const detail::ParsedTimestampToken> parsed;

    bool valid() const noexcept { return static_cast<bool>(parsed); }

    static ImportResult from_der(ByteView der);
    static ImportResult parse(ByteView der);
    static ImportResult parse(std::string_view encoded);
    static ImportResult from_file(const std::string& path);

    VerificationResult verify(
        const Digest& artifact_digest,
        const VerificationPolicy& policy = {},
        const TrustStore& trust = {},
        const RevocationEvidence& revocation = {}) const;
    VerificationResult bind(
        const Digest& artifact_digest,
        const VerificationPolicy& policy = {},
        const TrustStore& trust = {},
        const RevocationEvidence& revocation = {}) const {
        return verify(artifact_digest, policy, trust, revocation);
    }
};

struct ImportResult {
    // Default construction fails closed; successful imports overwrite this.
    ErrorCode code = ErrorCode::InvalidTokenStructure;
    std::string message;
    ImportedTimestampToken token;

    bool ok() const noexcept { return code == ErrorCode::Ok; }
    explicit operator bool() const noexcept { return ok(); }
};

using TimestampToken = ImportedTimestampToken;
using TimestampImportResult = ImportResult;

// ---------------------------------------------------------------------------
// Importer and verifier
// ---------------------------------------------------------------------------

class TimestampTokenImporter {
public:
    static ImportResult from_der(ByteView der);
    static ImportResult from_der(const Bytes& der) {
        return from_der(ByteView(der));
    }
    static ImportResult from_bytes(ByteView der) { return from_der(der); }
    static ImportResult from_bytes(const Bytes& der) { return from_der(der); }
    static ImportResult parse_der(ByteView der) { return from_der(der); }
    static ImportResult import_der(ByteView der) { return from_der(der); }
    static ImportResult importDER(ByteView der) { return from_der(der); }
    static ImportResult from_base64(std::string_view base64);
    static ImportResult import_base64(std::string_view base64) {
        return from_base64(base64);
    }
    static ImportResult from_pem(std::string_view pem);
    static ImportResult import_pem(std::string_view pem) { return from_pem(pem); }
    static ImportResult importPEM(std::string_view pem) { return from_pem(pem); }
    static ImportResult from_file(const std::string& path);

    // Accepts DER, PEM, or Base64 text and detects the encoding.
    static ImportResult import(ByteView input);
    static ImportResult import(std::string_view input);
};

ImportResult import_timestamp_token(ByteView der);
ImportResult import_timestamp_token(const Bytes& der);
ImportResult import_timestamp_token(std::string_view encoded);
ImportResult import_timestamp_token(const std::string& encoded);
ImportResult import_timestamp_token_base64(std::string_view base64);
ImportResult import_timestamp_token_pem(std::string_view pem);
ImportResult import_timestamp_token_file(const std::string& path);
inline ImportResult import_token(ByteView der) {
    return import_timestamp_token(der);
}
inline ImportResult parse_timestamp_token(ByteView der) {
    return import_timestamp_token(der);
}

class TimestampVerifier {
public:
    TimestampVerifier();
    explicit TimestampVerifier(VerificationPolicy policy);
    TimestampVerifier(VerificationPolicy policy, TrustStore trust);
    explicit TimestampVerifier(TrustStore trust);
    TimestampVerifier(TrustStore trust, VerificationPolicy policy);

    void set_policy(VerificationPolicy policy);
    const VerificationPolicy& policy() const noexcept { return policy_; }

    TrustStore& trust_store() noexcept { return trust_; }
    const TrustStore& trust_store() const noexcept { return trust_; }

    RevocationEvidence& revocation_evidence() noexcept { return revocation_; }
    const RevocationEvidence& revocation_evidence() const noexcept { return revocation_; }

    void set_revocation_checker(RevocationChecker checker) {
        revocation_checker_ = std::move(checker);
    }

    void add_trust_anchor(Bytes der) { trust_.add_anchor(std::move(der)); }
    bool add_trust_anchor(std::string_view pem) {
        return trust_.add_anchor_pem(pem);
    }
    void add_trusted_root(Bytes der) { trust_.add_anchor(std::move(der)); }
    void add_root_certificate(Bytes der) { trust_.add_anchor(std::move(der)); }
    bool add_trust_anchor_pem(std::string_view pem) { return trust_.add_anchor_pem(pem); }
    bool add_trust_anchor_file(const std::string& path) { return trust_.add_anchor_file(path); }
    void add_intermediate(Bytes der) { trust_.add_intermediate(std::move(der)); }
    bool add_intermediate_pem(std::string_view pem) {
        return trust_.add_intermediate_pem(pem);
    }
    void add_crl(Bytes der) { revocation_.crls.push_back(std::move(der)); }
    bool add_crl_pem(std::string_view pem);
    bool add_crl_file(const std::string& path);
    void add_ocsp_response(Bytes der) {
        revocation_.ocsp_responses.push_back(std::move(der));
    }
    bool add_ocsp_response_file(const std::string& path);

    VerificationResult verify(const ImportedTimestampToken& token,
                              const Digest& artifact_digest) const;
    VerificationResult verify(ByteView token_der, const Digest& artifact_digest) const;
    VerificationResult verify(const Bytes& token_der, const Digest& artifact_digest) const {
        return verify(ByteView(token_der), artifact_digest);
    }

    VerificationResult verify_artifact(const ImportedTimestampToken& token,
                                       ByteView artifact,
                                       HashAlgorithm algorithm) const;
    VerificationResult verify(const ImportedTimestampToken& token,
                              ByteView artifact,
                              HashAlgorithm algorithm) const {
        return verify_artifact(token, artifact, algorithm);
    }
    VerificationResult verify(ByteView token_der, ByteView artifact,
                              HashAlgorithm algorithm) const;
    VerificationResult verify_file(const ImportedTimestampToken& token,
                                   const std::string& artifact_path,
                                   HashAlgorithm algorithm) const;
    VerificationResult verify_file(ByteView token_der,
                                   const std::string& artifact_path,
                                   HashAlgorithm algorithm) const;
    VerificationResult verify(const ImportedTimestampToken& token,
                              const std::string& artifact_path,
                              HashAlgorithm algorithm) const {
        return verify_file(token, artifact_path, algorithm);
    }
    VerificationResult verify(ByteView token_der,
                              const std::string& artifact_path,
                              HashAlgorithm algorithm) const {
        return verify_file(token_der, artifact_path, algorithm);
    }

    // Binding-oriented aliases for timestamp-verification components.
    VerificationResult bind(const ImportedTimestampToken& token,
                            const Digest& artifact_digest) const {
        return verify(token, artifact_digest);
    }
    VerificationResult bind(ByteView token_der, const Digest& artifact_digest) const {
        return verify(token_der, artifact_digest);
    }
    VerificationResult bind_artifact(const ImportedTimestampToken& token,
                                     ByteView artifact,
                                     HashAlgorithm algorithm) const {
        return verify_artifact(token, artifact, algorithm);
    }
    VerificationResult check(const ImportedTimestampToken& token,
                             const Digest& artifact_digest) const {
        return verify(token, artifact_digest);
    }
    VerificationResult check(ByteView token_der,
                             const Digest& artifact_digest) const {
        return verify(token_der, artifact_digest);
    }
    VerificationResult bind_file(const ImportedTimestampToken& token,
                                 const std::string& artifact_path,
                                 HashAlgorithm algorithm) const {
        return verify_file(token, artifact_path, algorithm);
    }

private:
    VerificationPolicy policy_;
    TrustStore trust_;
    RevocationEvidence revocation_;
    RevocationChecker revocation_checker_;
};

using TimestampTokenBinder = TimestampVerifier;
using TimestampTokenBinding = TimestampVerifier;
using TokenBinder = TimestampVerifier;
using TokenBinding = TimestampVerifier;
using TimestampTokenVerifier = TimestampVerifier;
using TimestampBinding = TimestampVerifier;
using TokenVerifier = TimestampVerifier;
using TokenImporter = TimestampTokenImporter;

// Abstract seam for a larger timestamp verification component.  The binder
// owns all policy/trust configuration; the caller only supplies an imported
// token and the digest of the exact artifact being verified.
class ITimestampTokenBinding {
public:
    virtual ~ITimestampTokenBinding() = default;
    virtual VerificationResult verify(const ImportedTimestampToken& token,
                                      const Digest& artifact_digest) const = 0;
};

class DefaultTimestampTokenBinding final : public ITimestampTokenBinding {
public:
    explicit DefaultTimestampTokenBinding(TimestampVerifier verifier)
        : verifier_(std::move(verifier)) {}

    VerificationResult verify(const ImportedTimestampToken& token,
                              const Digest& artifact_digest) const override {
        return verifier_.verify(token, artifact_digest);
    }

private:
    TimestampVerifier verifier_;
};

// One-shot helpers.  They are equivalent to constructing TimestampVerifier
// with the supplied policy/trust/revocation inputs.
VerificationResult verify_timestamp_token(const ImportedTimestampToken& token,
                                          const Digest& artifact_digest,
                                          const VerificationPolicy& policy = {},
                                          const TrustStore& trust = {},
                                          const RevocationEvidence& revocation = {});
VerificationResult verify_timestamp_token(ByteView token_der,
                                          const Digest& artifact_digest,
                                          const VerificationPolicy& policy = {},
                                          const TrustStore& trust = {},
                                          const RevocationEvidence& revocation = {});
VerificationResult verify_timestamp_token(const Bytes& token_der,
                                          const Digest& artifact_digest,
                                          const VerificationPolicy& policy = {},
                                          const TrustStore& trust = {},
                                          const RevocationEvidence& revocation = {});
VerificationResult verify_timestamp_token(
    ByteView token_der, ByteView artifact, HashAlgorithm algorithm,
    const VerificationPolicy& policy = {}, const TrustStore& trust = {},
    const RevocationEvidence& revocation = {});
VerificationResult verify_token_binding(const Bytes& token_der,
                                        const Digest& artifact_digest,
                                        const VerificationPolicy& policy = {},
                                        const TrustStore& trust = {},
                                        const RevocationEvidence& revocation = {});
VerificationResult verify_timestamp_token_binding(
    const Bytes& token_der, const Digest& artifact_digest,
    const VerificationPolicy& policy = {}, const TrustStore& trust = {},
    const RevocationEvidence& revocation = {});

// ---------------------------------------------------------------------------
// Encoding helpers (useful to callers and to tests)
// ---------------------------------------------------------------------------

std::string base64_encode(ByteView data);
std::optional<Bytes> base64_decode(std::string_view text);
std::vector<Bytes> parse_pem_blocks(std::string_view pem, std::string_view label);
std::optional<Bytes> pem_to_der(std::string_view pem, std::string_view label);
std::string hex_encode(ByteView data, bool upper = false);
std::optional<Bytes> hex_decode(std::string_view text);

// Parses an X.509 certificate for inspection/tests.  Errors are reported by
// an empty certificate DER.
std::optional<CertificateInfo> parse_certificate(ByteView der,
                                                 std::string* error = nullptr);

// Parses a DER CRL to the subset needed by revocation checking.  The returned
// bytes are the exact DER input on success.
std::optional<Bytes> normalize_crl(ByteView der, std::string* error = nullptr);

// Exposed for integration tests and for components that need to know whether
// the token contains enough information for offline verification.
bool token_has_nonce(const ImportedTimestampToken& token) noexcept;

}  // namespace netsec
