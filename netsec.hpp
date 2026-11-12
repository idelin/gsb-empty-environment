#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <optional>
#include <string>
#include <string_view>
#include <type_traits>
#include <vector>

namespace netsec {

inline constexpr std::size_t kNonceSize = 32;
inline constexpr std::size_t kSha384DigestSize = 48;

using Bytes = std::vector<std::uint8_t>;
using Nonce = std::array<std::uint8_t, kNonceSize>;
using Sha384Digest = std::array<std::uint8_t, kSha384DigestSize>;

enum class SignatureAlgorithm : std::uint8_t {
    EcdsaP256Sha384 = 0,
    EcdsaP384Sha384 = 1,
    EcdsaP521Sha384 = 2,
    Rsa2048Sha384 = 3,
    Rsa3072Sha384 = 4,
    Rsa4096Sha384 = 5,
    Ed25519Sha512 = 6,

    ECDSA_P256_SHA384 = EcdsaP256Sha384,
    ECDSA_P384_SHA384 = EcdsaP384Sha384,
    ECDSA_P521_SHA384 = EcdsaP521Sha384,
    RSA_2048_SHA384 = Rsa2048Sha384,
    RSA_3072_SHA384 = Rsa3072Sha384,
    RSA_4096_SHA384 = Rsa4096Sha384,
    ED25519_SHA512 = Ed25519Sha512
};

// Returns 0 for an unsupported algorithm.
std::size_t expected_signature_length(SignatureAlgorithm algorithm) noexcept;
const char* signature_algorithm_name(SignatureAlgorithm algorithm) noexcept;

// Incremental SHA-384. Used for the transcript digest and root-of-chain hashing.
class Sha384 {
public:
    Sha384() noexcept;

    void update(const void* data, std::size_t size) noexcept;
    void update(const Bytes& bytes) noexcept;
    Sha384Digest finalize() noexcept;

private:
    void compress(const std::uint8_t block[128]) noexcept;

    std::array<std::uint64_t, 8> state_{
        0xcbbb9d5dc1059ed8ULL, 0x629a292a367cd507ULL,
        0x9159015a3070dd17ULL, 0x152fecd8f70e5939ULL,
        0x67332667ffc00b31ULL, 0x8eb44a8768581511ULL,
        0xdb0c2e0d64f98fa7ULL, 0x47b5481dbefa4fa4ULL};
    std::array<std::uint8_t, 128> buffer_{};
    std::size_t buffer_size_ = 0;
    std::uint64_t total_size_ = 0;
};

Sha384Digest sha384(const void* data, std::size_t size) noexcept;
Sha384Digest sha384(const Bytes& bytes) noexcept;

// Hex helpers are deliberately small and dependency-free so firmware can log
// digests and test vectors without pulling in a formatting library.
std::string to_hex(const std::uint8_t* data, std::size_t size);
std::string to_hex(const Sha384Digest& digest);
std::string to_hex(const Nonce& nonce);
std::optional<Bytes> bytes_from_hex(std::string_view text);
std::optional<Sha384Digest> sha384_digest_from_hex(std::string_view text);
std::optional<Nonce> nonce_from_hex(std::string_view text);

struct CertificateChainRecord {
    // Complete CHAIN record bytes, exactly as received. These bytes are part of
    // the transcript digest.
    Bytes record;

    // SHA-384 of the root certificate. The caller can provide this directly,
    // or provide root_certificate_der and let the verifier hash it.
    Sha384Digest root_certificate_hash{};
    bool has_root_certificate_hash = false;
    Bytes root_certificate_der;

    static CertificateChainRecord from_root_hash(Bytes record,
                                                  Sha384Digest root_hash);
    static CertificateChainRecord from_root_certificate(Bytes record,
                                                        Bytes root_der);
};

struct MeasurementBlock {
    std::uint8_t index = 0;
    Bytes value;
};

struct ExpectedMeasurementBlock {
    std::uint8_t index = 0;
    Bytes value;
};

using ExpectedMeasurement = ExpectedMeasurementBlock;

// VCA and the certificate-chain record are connection/authentication state.
// They precede GET_MEASUREMENTS in the SPDM transcript.
struct SessionTranscript {
    Bytes vca;
    CertificateChainRecord certificate_chain;
};

struct MeasurementRequest {
    // The request nonce must be produced by the caller's random source.
    Nonce nonce{};

    // The complete GET_MEASUREMENTS request bytes (including the nonce).
    Bytes bytes;

    // SPDM GET_MEASUREMENTS "signature requested" attribute.
    bool signature_requested = true;
};

using MeasurementsRequest = MeasurementRequest;
using GetMeasurementsRequest = MeasurementRequest;

struct MeasurementResponse {
    // Nonce carried by the MEASUREMENTS response.
    Nonce nonce{};

    // All response bytes covered by the transcript signature, excluding the
    // signature field itself. This must be the exact wire representation.
    Bytes bytes_without_signature;

    bool signature_present = false;
    SignatureAlgorithm signature_algorithm = SignatureAlgorithm::EcdsaP256Sha384;
    Bytes signature;

    // Optional response-carried/expected digest. When present it must equal
    // SHA-384(VCA || certificate-chain record || request || response-without-
    // signature). A real SPDM signature check still receives the computed
    // digest through signature_verifier below.
    std::optional<Sha384Digest> signed_transcript_digest;

    std::vector<MeasurementBlock> measurements;
};

using MeasurementsResponse = MeasurementResponse;
using GetMeasurementsResponse = MeasurementResponse;

// The verifier callback performs algorithm-specific signature verification
// using the leaf certificate in certificate_chain.record. It receives the
// computed transcript digest, never an attacker-supplied digest.
using SignatureVerifier = std::function<bool(
    const Sha384Digest& transcript_digest,
    SignatureAlgorithm algorithm,
    const Bytes& signature,
    const CertificateChainRecord& certificate_chain)>;

struct VerificationPolicy {
    // SHA-384 hashes of acceptable root certificates.
    std::vector<Sha384Digest> trusted_root_hashes;

    // Expected measurement blocks. Comparison is by index and value.
    std::vector<ExpectedMeasurementBlock> expected_measurements;

    // Set false only when a caller intentionally does not have an expected
    // measurement set (the structural checks still run).
    bool compare_expected_measurements = true;

    // Required whenever a signed response must be accepted. The package
    // deliberately fails closed instead of treating an opaque signature as
    // verified.
    SignatureVerifier signature_verifier;
};

using VerificationOptions = VerificationPolicy;

enum class VerificationError : std::uint8_t {
    None = 0,
    MissingRequestBytes,
    MissingResponseBytes,
    RequestNonceNotInTranscript,
    ResponseNonceNotInTranscript,
    ResponseNonceEqualsRequestNonce,
    MissingSignature,
    UnsupportedSignatureAlgorithm,
    SignatureLengthMismatch,
    MissingRootCertificateHash,
    UntrustedCertificateRoot,
    TranscriptDigestMismatch,
    MissingSignatureVerifier,
    SignatureVerificationFailed,
    MeasurementBlockRepeatedIndex,
    MeasurementBlockIndexNotAscending,
    InvalidExpectedMeasurementSet,
    MeasurementCountMismatch,
    MissingMeasurementBlock,
    UnexpectedMeasurementBlock,
    MeasurementIndexMismatch,
    MeasurementValueMismatch
};

const char* verification_error_name(VerificationError error) noexcept;
const char* verification_error_message(VerificationError error) noexcept;

struct VerificationResult {
    VerificationError error = VerificationError::None;
    std::optional<std::uint8_t> block_index;
    std::string detail;

    bool ok() const noexcept { return error == VerificationError::None; }
    explicit operator bool() const noexcept { return ok(); }
};

// Computes SHA-384(vca || certificate_chain_record || request_bytes ||
// response_bytes_without_signature). The order is fixed and no implicit
// separators are inserted: callers must pass exact wire bytes.
Sha384Digest compute_transcript_digest(const SessionTranscript& session,
                                       const MeasurementRequest& request,
                                       const MeasurementResponse& response) noexcept;

Sha384Digest compute_transcript_digest(const Bytes& vca,
                                       const Bytes& certificate_chain_record,
                                       const Bytes& request_bytes,
                                       const Bytes& response_bytes_without_signature) noexcept;

bool is_trusted_root(const Sha384Digest& root_hash,
                     const std::vector<Sha384Digest>& trusted_root_hashes) noexcept;

// Derives the root hash from root_certificate_der when present; otherwise
// returns the caller-supplied hash. The optional is empty when neither source
// is available.
std::optional<Sha384Digest> certificate_chain_root_hash(
    const CertificateChainRecord& chain) noexcept;

bool constant_time_equal(const std::uint8_t* left,
                         const std::uint8_t* right,
                         std::size_t size) noexcept;

// Generates a request nonce using a caller-provided random source. The source
// must fill the supplied buffer; entropy/quality is the caller's
// responsibility. Both a buffer-filling callable and a byte-returning callable
// are supported.
template <typename RandomSource,
          typename = std::enable_if_t<std::is_invocable_v<
              RandomSource&, std::uint8_t*, std::size_t>>>
Nonce generate_request_nonce(RandomSource&& random_source) {
    Nonce nonce{};
    random_source(nonce.data(), nonce.size());
    return nonce;
}

Nonce generate_request_nonce(
    const std::function<Bytes(std::size_t)>& random_source);

// Validates a GET_MEASUREMENTS response. The policy is copied by the class but
// the class itself is stateless after construction, so it can be reused for
// concurrent connections.
VerificationResult verify_measurements_response(
    const SessionTranscript& session,
    const MeasurementRequest& request,
    const MeasurementResponse& response,
    const VerificationPolicy& policy);

class MeasurementResponseVerifier {
public:
    explicit MeasurementResponseVerifier(VerificationPolicy policy);

    VerificationResult verify(const SessionTranscript& session,
                              const MeasurementRequest& request,
                              const MeasurementResponse& response) const;

private:
    VerificationPolicy policy_;
};

}  // namespace netsec
