#include "netsec.hpp"

#include <algorithm>
#include <cstring>
#include <map>
#include <set>
#include <utility>

namespace netsec {
namespace {

constexpr std::uint64_t kRoundConstants[80] = {
    0x428a2f98d728ae22ULL, 0x7137449123ef65cdULL,
    0xb5c0fbcfec4d3b2fULL, 0xe9b5dba58189dbbcULL,
    0x3956c25bf348b538ULL, 0x59f111f1b605d019ULL,
    0x923f82a4af194f9bULL, 0xab1c5ed5da6d8118ULL,
    0xd807aa98a3030242ULL, 0x12835b0145706fbeULL,
    0x243185be4ee4b28cULL, 0x550c7dc3d5ffb4e2ULL,
    0x72be5d74f27b896fULL, 0x80deb1fe3b1696b1ULL,
    0x9bdc06a725c71235ULL, 0xc19bf174cf692694ULL,
    0xe49b69c19ef14ad2ULL, 0xefbe4786384f25e3ULL,
    0x0fc19dc68b8cd5b5ULL, 0x240ca1cc77ac9c65ULL,
    0x2de92c6f592b0275ULL, 0x4a7484aa6ea6e483ULL,
    0x5cb0a9dcbd41fbd4ULL, 0x76f988da831153b5ULL,
    0x983e5152ee66dfabULL, 0xa831c66d2db43210ULL,
    0xb00327c898fb213fULL, 0xbf597fc7beef0ee4ULL,
    0xc6e00bf33da88fc2ULL, 0xd5a79147930aa725ULL,
    0x06ca6351e003826fULL, 0x142929670a0e6e70ULL,
    0x27b70a8546d22ffcULL, 0x2e1b21385c26c926ULL,
    0x4d2c6dfc5ac42aedULL, 0x53380d139d95b3dfULL,
    0x650a73548baf63deULL, 0x766a0abb3c77b2a8ULL,
    0x81c2c92e47edaee6ULL, 0x92722c851482353bULL,
    0xa2bfe8a14cf10364ULL, 0xa81a664bbc423001ULL,
    0xc24b8b70d0f89791ULL, 0xc76c51a30654be30ULL,
    0xd192e819d6ef5218ULL, 0xd69906245565a910ULL,
    0xf40e35855771202aULL, 0x106aa07032bbd1b8ULL,
    0x19a4c116b8d2d0c8ULL, 0x1e376c085141ab53ULL,
    0x2748774cdf8eeb99ULL, 0x34b0bcb5e19b48a8ULL,
    0x391c0cb3c5c95a63ULL, 0x4ed8aa4ae3418acbULL,
    0x5b9cca4f7763e373ULL, 0x682e6ff3d6b2b8a3ULL,
    0x748f82ee5defb2fcULL, 0x78a5636f43172f60ULL,
    0x84c87814a1f0ab72ULL, 0x8cc702081a6439ecULL,
    0x90befffa23631e28ULL, 0xa4506cebde82bde9ULL,
    0xbef9a3f7b2c67915ULL, 0xc67178f2e372532bULL,
    0xca273eceea26619cULL, 0xd186b8c721c0c207ULL,
    0xeada7dd6cde0eb1eULL, 0xf57d4f7fee6ed178ULL,
    0x06f067aa72176fbaULL, 0x0a637dc5a2c898a6ULL,
    0x113f9804bef90daeULL, 0x1b710b35131c471bULL,
    0x28db77f523047d84ULL, 0x32caab7b40c72493ULL,
    0x3c9ebe0a15c9bebcULL, 0x431d67c49c100d4cULL,
    0x4cc5d4becb3e42b6ULL, 0x597f299cfc657e2aULL,
    0x5fcb6fab3ad6faecULL, 0x6c44198c4a475817ULL
};

constexpr std::uint64_t rotr(std::uint64_t value, std::uint64_t bits) noexcept {
    return (value >> bits) | (value << (64U - bits));
}

std::uint64_t load_be64(const std::uint8_t* data) noexcept {
    std::uint64_t value = 0;
    for (std::size_t i = 0; i < 8; ++i) {
        value = (value << 8U) | static_cast<std::uint64_t>(data[i]);
    }
    return value;
}

VerificationResult failure(VerificationError error,
                           std::optional<std::uint8_t> block_index = std::nullopt,
                           std::string detail = {}) {
    VerificationResult result;
    result.error = error;
    result.block_index = block_index;
    result.detail = std::move(detail);
    return result;
}

VerificationResult success() {
    return {};
}

bool check_unique_ascending_indices(const std::vector<MeasurementBlock>& blocks,
                                    VerificationResult* result) {
    std::set<std::uint8_t> seen;
    for (std::size_t position = 0; position < blocks.size(); ++position) {
        const std::uint8_t index = blocks[position].index;
        if (!seen.insert(index).second) {
            *result = failure(VerificationError::MeasurementBlockRepeatedIndex,
                              index, "measurement block index is duplicated");
            return false;
        }
        if (position != 0 && blocks[position - 1].index >= index) {
            *result = failure(VerificationError::MeasurementBlockIndexNotAscending,
                              index, "measurement block indices are not strictly ascending");
            return false;
        }
    }
    return true;
}

bool check_expected_measurements(
    const std::vector<MeasurementBlock>& actual,
    const std::vector<ExpectedMeasurementBlock>& expected,
    VerificationResult* result) {
    std::set<std::uint8_t> expected_indices;
    std::map<std::uint8_t, const Bytes*> expected_values;

    for (const ExpectedMeasurementBlock& block : expected) {
        if (!expected_indices.insert(block.index).second) {
            *result = failure(VerificationError::InvalidExpectedMeasurementSet,
                              block.index, "expected measurement index is duplicated");
            return false;
        }
        expected_values.emplace(block.index, &block.value);
    }

    std::set<std::uint8_t> actual_indices;
    for (const MeasurementBlock& block : actual) {
        actual_indices.insert(block.index);
    }

    if (actual.size() != expected.size()) {
        for (const ExpectedMeasurementBlock& block : expected) {
            if (actual_indices.count(block.index) == 0) {
                *result = failure(VerificationError::MissingMeasurementBlock,
                                  block.index, "an expected measurement block is missing");
                return false;
            }
        }
        for (const MeasurementBlock& block : actual) {
            if (expected_indices.count(block.index) == 0) {
                *result = failure(VerificationError::UnexpectedMeasurementBlock,
                                  block.index, "the response contains an unexpected measurement block");
                return false;
            }
        }
        *result = failure(VerificationError::MeasurementCountMismatch,
                          std::nullopt, "measurement block count does not match");
        return false;
    }

    for (const ExpectedMeasurementBlock& expected_block : expected) {
        if (actual_indices.count(expected_block.index) == 0) {
            *result = failure(VerificationError::MissingMeasurementBlock,
                              expected_block.index,
                              "an expected measurement block is missing");
            return false;
        }
    }
    for (const MeasurementBlock& actual_block : actual) {
        if (expected_indices.count(actual_block.index) == 0) {
            *result = failure(VerificationError::UnexpectedMeasurementBlock,
                              actual_block.index,
                              "the response contains an unexpected measurement block");
            return false;
        }
    }

    for (const MeasurementBlock& actual_block : actual) {
        const auto expected_it = expected_values.find(actual_block.index);
        if (expected_it == expected_values.end()) {
            *result = failure(VerificationError::UnexpectedMeasurementBlock,
                              actual_block.index,
                              "the response contains an unexpected measurement block");
            return false;
        }
        if (actual_block.value != *expected_it->second) {
            *result = failure(VerificationError::MeasurementValueMismatch,
                              actual_block.index,
                              "measurement value does not match the expected value");
            return false;
        }
    }

    return true;
}

}  // namespace

Sha384::Sha384() noexcept = default;

void Sha384::compress(const std::uint8_t block[128]) noexcept {
    std::uint64_t words[80]{};
    for (std::size_t i = 0; i < 16; ++i) {
        words[i] = load_be64(block + 8 * i);
    }
    for (std::size_t i = 16; i < 80; ++i) {
        const std::uint64_t s0 =
            rotr(words[i - 15], 1) ^ rotr(words[i - 15], 8) ^
            (words[i - 15] >> 7);
        const std::uint64_t s1 =
            rotr(words[i - 2], 19) ^ rotr(words[i - 2], 61) ^
            (words[i - 2] >> 6);
        words[i] = words[i - 16] + s0 + words[i - 7] + s1;
    }

    std::uint64_t a = state_[0];
    std::uint64_t b = state_[1];
    std::uint64_t c = state_[2];
    std::uint64_t d = state_[3];
    std::uint64_t e = state_[4];
    std::uint64_t f = state_[5];
    std::uint64_t g = state_[6];
    std::uint64_t h = state_[7];

    for (std::size_t i = 0; i < 80; ++i) {
        const std::uint64_t sigma1 =
            rotr(e, 14) ^ rotr(e, 18) ^ rotr(e, 41);
        const std::uint64_t choose = (e & f) ^ (~e & g);
        const std::uint64_t temp1 =
            h + sigma1 + choose + kRoundConstants[i] + words[i];
        const std::uint64_t sigma0 =
            rotr(a, 28) ^ rotr(a, 34) ^ rotr(a, 39);
        const std::uint64_t majority = (a & b) ^ (a & c) ^ (b & c);
        const std::uint64_t temp2 = sigma0 + majority;

        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    state_[0] += a;
    state_[1] += b;
    state_[2] += c;
    state_[3] += d;
    state_[4] += e;
    state_[5] += f;
    state_[6] += g;
    state_[7] += h;
}

void Sha384::update(const void* data, std::size_t size) noexcept {
    if (size == 0) {
        return;
    }

    const std::uint8_t* input = static_cast<const std::uint8_t*>(data);
    total_size_ += static_cast<std::uint64_t>(size);

    if (buffer_size_ != 0) {
        const std::size_t needed = 128 - buffer_size_;
        const std::size_t take = std::min(needed, size);
        std::memcpy(buffer_.data() + buffer_size_, input, take);
        buffer_size_ += take;
        input += take;
        size -= take;

        if (buffer_size_ == 128) {
            compress(buffer_.data());
            buffer_size_ = 0;
        }
    }

    while (size >= 128) {
        compress(input);
        input += 128;
        size -= 128;
    }

    if (size != 0) {
        std::memcpy(buffer_.data(), input, size);
        buffer_size_ = size;
    }
}

void Sha384::update(const Bytes& bytes) noexcept {
    update(bytes.data(), bytes.size());
}

Sha384Digest Sha384::finalize() noexcept {
    const std::uint64_t bit_length = total_size_ * 8U;
    const std::uint8_t one_bit = 0x80;

    update(&one_bit, 1);
    while (buffer_size_ != 112) {
        std::uint8_t zeros[128]{};
        if (buffer_size_ < 112) {
            update(zeros, 112 - buffer_size_);
        } else {
            update(zeros, 128 - buffer_size_);
        }
    }

    std::uint8_t length_bytes[16]{};
    for (std::size_t i = 0; i < 8; ++i) {
        length_bytes[15 - i] = static_cast<std::uint8_t>(bit_length >> (8 * i));
    }
    update(length_bytes, sizeof(length_bytes));

    Sha384Digest digest{};
    for (std::size_t i = 0; i < 6; ++i) {
        for (std::size_t byte = 0; byte < 8; ++byte) {
            digest[8 * i + byte] = static_cast<std::uint8_t>(
                state_[i] >> (56U - 8U * byte));
        }
    }
    return digest;
}

Sha384Digest sha384(const void* data, std::size_t size) noexcept {
    Sha384 hasher;
    hasher.update(data, size);
    return hasher.finalize();
}

Sha384Digest sha384(const Bytes& bytes) noexcept {
    Sha384 hasher;
    hasher.update(bytes);
    return hasher.finalize();
}

std::size_t expected_signature_length(SignatureAlgorithm algorithm) noexcept {
    switch (algorithm) {
    case SignatureAlgorithm::EcdsaP256Sha384:
        return 64;
    case SignatureAlgorithm::EcdsaP384Sha384:
        return 96;
    case SignatureAlgorithm::EcdsaP521Sha384:
        return 132;
    case SignatureAlgorithm::Rsa2048Sha384:
        return 256;
    case SignatureAlgorithm::Rsa3072Sha384:
        return 384;
    case SignatureAlgorithm::Rsa4096Sha384:
        return 512;
    case SignatureAlgorithm::Ed25519Sha512:
        return 64;
    }
    return 0;
}

const char* signature_algorithm_name(SignatureAlgorithm algorithm) noexcept {
    switch (algorithm) {
    case SignatureAlgorithm::EcdsaP256Sha384:
        return "ECDSA-P256-SHA384";
    case SignatureAlgorithm::EcdsaP384Sha384:
        return "ECDSA-P384-SHA384";
    case SignatureAlgorithm::EcdsaP521Sha384:
        return "ECDSA-P521-SHA384";
    case SignatureAlgorithm::Rsa2048Sha384:
        return "RSA-2048-SHA384";
    case SignatureAlgorithm::Rsa3072Sha384:
        return "RSA-3072-SHA384";
    case SignatureAlgorithm::Rsa4096Sha384:
        return "RSA-4096-SHA384";
    case SignatureAlgorithm::Ed25519Sha512:
        return "Ed25519-SHA512";
    }
    return "unknown";
}

std::string to_hex(const std::uint8_t* data, std::size_t size) {
    static constexpr char kHex[] = "0123456789abcdef";
    std::string output;
    output.reserve(size * 2);
    for (std::size_t i = 0; i < size; ++i) {
        output.push_back(kHex[data[i] >> 4U]);
        output.push_back(kHex[data[i] & 0x0fU]);
    }
    return output;
}

std::string to_hex(const Sha384Digest& digest) {
    return to_hex(digest.data(), digest.size());
}

std::string to_hex(const Nonce& nonce) {
    return to_hex(nonce.data(), nonce.size());
}

std::optional<Bytes> bytes_from_hex(std::string_view text) {
    if (text.size() % 2 != 0) {
        return std::nullopt;
    }

    Bytes output;
    output.reserve(text.size() / 2);
    for (std::size_t i = 0; i < text.size(); i += 2) {
        auto nibble = [](char value) -> std::optional<std::uint8_t> {
            if (value >= '0' && value <= '9') {
                return static_cast<std::uint8_t>(value - '0');
            }
            if (value >= 'a' && value <= 'f') {
                return static_cast<std::uint8_t>(value - 'a' + 10);
            }
            if (value >= 'A' && value <= 'F') {
                return static_cast<std::uint8_t>(value - 'A' + 10);
            }
            return std::nullopt;
        };

        const auto high = nibble(text[i]);
        const auto low = nibble(text[i + 1]);
        if (!high || !low) {
            return std::nullopt;
        }
        output.push_back(static_cast<std::uint8_t>((*high << 4U) | *low));
    }
    return output;
}

std::optional<Sha384Digest> sha384_digest_from_hex(std::string_view text) {
    const auto bytes = bytes_from_hex(text);
    if (!bytes || bytes->size() != kSha384DigestSize) {
        return std::nullopt;
    }
    Sha384Digest digest{};
    std::copy(bytes->begin(), bytes->end(), digest.begin());
    return digest;
}

std::optional<Nonce> nonce_from_hex(std::string_view text) {
    const auto bytes = bytes_from_hex(text);
    if (!bytes || bytes->size() != kNonceSize) {
        return std::nullopt;
    }
    Nonce nonce{};
    std::copy(bytes->begin(), bytes->end(), nonce.begin());
    return nonce;
}

CertificateChainRecord CertificateChainRecord::from_root_hash(
    Bytes record, Sha384Digest root_hash) {
    CertificateChainRecord chain;
    chain.record = std::move(record);
    chain.root_certificate_hash = root_hash;
    chain.has_root_certificate_hash = true;
    return chain;
}

CertificateChainRecord CertificateChainRecord::from_root_certificate(
    Bytes record, Bytes root_der) {
    CertificateChainRecord chain;
    chain.record = std::move(record);
    chain.root_certificate_der = std::move(root_der);
    return chain;
}

Nonce generate_request_nonce(
    const std::function<Bytes(std::size_t)>& random_source) {
    const Bytes generated = random_source(kNonceSize);
    Nonce nonce{};
    if (generated.size() == kNonceSize) {
        std::copy(generated.begin(), generated.end(), nonce.begin());
    }
    return nonce;
}

Sha384Digest compute_transcript_digest(const SessionTranscript& session,
                                       const MeasurementRequest& request,
                                       const MeasurementResponse& response) noexcept {
    return compute_transcript_digest(
        session.vca, session.certificate_chain.record, request.bytes,
        response.bytes_without_signature);
}

Sha384Digest compute_transcript_digest(
    const Bytes& vca,
    const Bytes& certificate_chain_record,
    const Bytes& request_bytes,
    const Bytes& response_bytes_without_signature) noexcept {
    Sha384 hasher;
    hasher.update(vca);
    hasher.update(certificate_chain_record);
    hasher.update(request_bytes);
    hasher.update(response_bytes_without_signature);
    return hasher.finalize();
}

bool constant_time_equal(const std::uint8_t* left,
                         const std::uint8_t* right,
                         std::size_t size) noexcept {
    std::uint8_t difference = 0;
    for (std::size_t i = 0; i < size; ++i) {
        difference |= static_cast<std::uint8_t>(left[i] ^ right[i]);
    }
    return difference == 0;
}

bool is_trusted_root(const Sha384Digest& root_hash,
                     const std::vector<Sha384Digest>& trusted_root_hashes) noexcept {
    for (const Sha384Digest& trusted : trusted_root_hashes) {
        if (constant_time_equal(root_hash.data(), trusted.data(),
                                root_hash.size())) {
            return true;
        }
    }
    return false;
}

std::optional<Sha384Digest> certificate_chain_root_hash(
    const CertificateChainRecord& chain) noexcept {
    if (!chain.root_certificate_der.empty()) {
        return sha384(chain.root_certificate_der);
    }
    if (chain.has_root_certificate_hash) {
        return chain.root_certificate_hash;
    }
    return std::nullopt;
}

const char* verification_error_name(VerificationError error) noexcept {
    switch (error) {
    case VerificationError::None:
        return "None";
    case VerificationError::MissingRequestBytes:
        return "MissingRequestBytes";
    case VerificationError::MissingResponseBytes:
        return "MissingResponseBytes";
    case VerificationError::RequestNonceNotInTranscript:
        return "RequestNonceNotInTranscript";
    case VerificationError::ResponseNonceNotInTranscript:
        return "ResponseNonceNotInTranscript";
    case VerificationError::ResponseNonceEqualsRequestNonce:
        return "ResponseNonceEqualsRequestNonce";
    case VerificationError::MissingSignature:
        return "MissingSignature";
    case VerificationError::UnsupportedSignatureAlgorithm:
        return "UnsupportedSignatureAlgorithm";
    case VerificationError::SignatureLengthMismatch:
        return "SignatureLengthMismatch";
    case VerificationError::MissingRootCertificateHash:
        return "MissingRootCertificateHash";
    case VerificationError::UntrustedCertificateRoot:
        return "UntrustedCertificateRoot";
    case VerificationError::TranscriptDigestMismatch:
        return "TranscriptDigestMismatch";
    case VerificationError::MissingSignatureVerifier:
        return "MissingSignatureVerifier";
    case VerificationError::SignatureVerificationFailed:
        return "SignatureVerificationFailed";
    case VerificationError::MeasurementBlockRepeatedIndex:
        return "MeasurementBlockRepeatedIndex";
    case VerificationError::MeasurementBlockIndexNotAscending:
        return "MeasurementBlockIndexNotAscending";
    case VerificationError::InvalidExpectedMeasurementSet:
        return "InvalidExpectedMeasurementSet";
    case VerificationError::MeasurementCountMismatch:
        return "MeasurementCountMismatch";
    case VerificationError::MissingMeasurementBlock:
        return "MissingMeasurementBlock";
    case VerificationError::UnexpectedMeasurementBlock:
        return "UnexpectedMeasurementBlock";
    case VerificationError::MeasurementIndexMismatch:
        return "MeasurementIndexMismatch";
    case VerificationError::MeasurementValueMismatch:
        return "MeasurementValueMismatch";
    }
    return "UnknownVerificationError";
}

const char* verification_error_message(VerificationError error) noexcept {
    switch (error) {
    case VerificationError::None:
        return "verification succeeded";
    case VerificationError::MissingRequestBytes:
        return "GET_MEASUREMENTS request bytes are missing";
    case VerificationError::MissingResponseBytes:
        return "MEASUREMENTS response bytes outside the signature are missing";
    case VerificationError::RequestNonceNotInTranscript:
        return "request bytes do not contain the request nonce";
    case VerificationError::ResponseNonceNotInTranscript:
        return "response bytes do not contain the response nonce";
    case VerificationError::ResponseNonceEqualsRequestNonce:
        return "response nonce must differ from the request nonce";
    case VerificationError::MissingSignature:
        return "signature was requested but the response does not carry one";
    case VerificationError::UnsupportedSignatureAlgorithm:
        return "signature algorithm is not supported";
    case VerificationError::SignatureLengthMismatch:
        return "signature length does not match the selected algorithm";
    case VerificationError::MissingRootCertificateHash:
        return "certificate-chain root hash is missing";
    case VerificationError::UntrustedCertificateRoot:
        return "certificate-chain root is not in the trusted set";
    case VerificationError::TranscriptDigestMismatch:
        return "transcript digest does not match the signed digest";
    case VerificationError::MissingSignatureVerifier:
        return "no signature verifier was supplied; refusing an opaque signature";
    case VerificationError::SignatureVerificationFailed:
        return "signature verification failed";
    case VerificationError::MeasurementBlockRepeatedIndex:
        return "measurement block index is duplicated";
    case VerificationError::MeasurementBlockIndexNotAscending:
        return "measurement block indices are not strictly ascending";
    case VerificationError::InvalidExpectedMeasurementSet:
        return "expected measurement set contains a duplicate index";
    case VerificationError::MeasurementCountMismatch:
        return "measurement block count does not match the expected count";
    case VerificationError::MissingMeasurementBlock:
        return "an expected measurement block is missing";
    case VerificationError::UnexpectedMeasurementBlock:
        return "the response contains an unexpected measurement block";
    case VerificationError::MeasurementIndexMismatch:
        return "measurement block indices do not match the expected set";
    case VerificationError::MeasurementValueMismatch:
        return "measurement value does not match the expected value";
    }
    return "unknown verification error";
}

VerificationResult verify_measurements_response(
    const SessionTranscript& session,
    const MeasurementRequest& request,
    const MeasurementResponse& response,
    const VerificationPolicy& policy) {
    if (request.bytes.empty()) {
        return failure(VerificationError::MissingRequestBytes);
    }
    if (response.bytes_without_signature.empty()) {
        return failure(VerificationError::MissingResponseBytes);
    }

    if (constant_time_equal(request.nonce.data(), response.nonce.data(),
                            kNonceSize)) {
        return failure(VerificationError::ResponseNonceEqualsRequestNonce);
    }

    if (std::search(request.bytes.begin(), request.bytes.end(),
                    request.nonce.begin(), request.nonce.end()) ==
        request.bytes.end()) {
        return failure(VerificationError::RequestNonceNotInTranscript);
    }
    if (std::search(response.bytes_without_signature.begin(),
                    response.bytes_without_signature.end(),
                    response.nonce.begin(), response.nonce.end()) ==
        response.bytes_without_signature.end()) {
        return failure(VerificationError::ResponseNonceNotInTranscript);
    }

    if (request.signature_requested && !response.signature_present) {
        return failure(VerificationError::MissingSignature);
    }

    if (response.signature_present) {
        const std::size_t required_length =
            expected_signature_length(response.signature_algorithm);
        if (required_length == 0) {
            return failure(VerificationError::UnsupportedSignatureAlgorithm);
        }
        if (response.signature.size() != required_length) {
            return failure(
                VerificationError::SignatureLengthMismatch, std::nullopt,
                "expected " + std::to_string(required_length) +
                    " bytes, received " +
                    std::to_string(response.signature.size()));
        }
    }

    const std::optional<Sha384Digest> root_hash =
        certificate_chain_root_hash(session.certificate_chain);
    if (!root_hash.has_value()) {
        return failure(VerificationError::MissingRootCertificateHash);
    }
    if (!is_trusted_root(*root_hash, policy.trusted_root_hashes)) {
        return failure(VerificationError::UntrustedCertificateRoot);
    }

    const Sha384Digest transcript_digest =
        compute_transcript_digest(session, request, response);

    if (response.signed_transcript_digest.has_value() &&
        !constant_time_equal(transcript_digest.data(),
                             response.signed_transcript_digest->data(),
                             kSha384DigestSize)) {
        return failure(VerificationError::TranscriptDigestMismatch);
    }

    if (response.signature_present) {
        if (!policy.signature_verifier) {
            return failure(VerificationError::MissingSignatureVerifier);
        }
        if (!policy.signature_verifier(transcript_digest,
                                       response.signature_algorithm,
                                       response.signature,
                                       session.certificate_chain)) {
            return failure(VerificationError::SignatureVerificationFailed);
        }
    }

    VerificationResult index_result;
    if (!check_unique_ascending_indices(response.measurements,
                                        &index_result)) {
        return index_result;
    }

    if (policy.compare_expected_measurements) {
        VerificationResult expected_result;
        if (!check_expected_measurements(response.measurements,
                                         policy.expected_measurements,
                                         &expected_result)) {
            return expected_result;
        }
    }

    return success();
}

MeasurementResponseVerifier::MeasurementResponseVerifier(
    VerificationPolicy policy)
    : policy_(std::move(policy)) {}

VerificationResult MeasurementResponseVerifier::verify(
    const SessionTranscript& session,
    const MeasurementRequest& request,
    const MeasurementResponse& response) const {
    return verify_measurements_response(session, request, response, policy_);
}

}  // namespace netsec
