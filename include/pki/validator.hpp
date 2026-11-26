#pragma once

#include "pki/x509.hpp"

#include <cstdint>
#include <functional>
#include <memory>
#include <string>
#include <vector>

namespace pki {

enum class ErrorCode {
    Ok,
    InvalidInput,
    InvalidCertificateEncoding,
    UnknownCriticalExtension,
    BadSignature,
    NotYetValid,
    Expired,
    NameChaining,
    NotACa,
    KeyUsage,
    PathLengthExceeded,
    ExcludedName,
    PermittedNameViolation,
    UnrecognizedCriticalExtension,
    PolicyMappingDenied,
    ExplicitPolicyRequired,
    NoValidPolicy,
    DuplicateCertificate,
    MissingTrustAnchor,
};

struct SignatureInput {
    const x509::Certificate& issuer;
    const x509::Certificate& subject;
    der::ByteView tbs;
    der::ByteView signature;
    std::string algorithm_oid;
};

class SignatureVerifier {
public:
    virtual ~SignatureVerifier() = default;
    [[nodiscard]] virtual bool verify(const SignatureInput& input) = 0;
};

class FunctionSignatureVerifier final : public SignatureVerifier {
public:
    using Function = std::function<bool(const SignatureInput&)>;

    explicit FunctionSignatureVerifier(Function function) : function_(std::move(function)) {}

    [[nodiscard]] bool verify(const SignatureInput& input) override {
        return function_ && function_(input);
    }

private:
    Function function_;
};

struct VerificationOptions {
    SignatureVerifier* signature_verifier = nullptr;

    // Seconds since the Unix epoch. A negative value means use system clock.
    std::int64_t verification_time = -1;
    bool check_validity = true;
    bool check_basic_constraints = true;
    bool check_key_usage = true;
    bool check_names = true;

    // RFC 5280 initial policy inputs.
    bool initial_explicit_policy = false;
    bool initial_policy_mapping_inhibit = false;
    bool initial_any_policy_inhibit = false;

    // Empty means the RFC special anyPolicy/user-initial-policy-set.
    std::vector<std::string> user_initial_policies;
};

struct PolicyNode {
    std::size_t depth = 0;
    std::string valid_policy;
    std::string qualifier_set;
    std::vector<std::string> expected_policy_set;
    std::vector<std::size_t> parent_indices;
};

struct VerifyResult {
    ErrorCode code = ErrorCode::Ok;
    std::string message;
    std::size_t certificate_index = 0;
    std::vector<std::string> valid_policies;
    std::vector<PolicyNode> policy_tree;

    [[nodiscard]] bool ok() const noexcept { return code == ErrorCode::Ok; }
    static VerifyResult failure(ErrorCode code, std::string message, std::size_t index = 0);
};

// leaf_first contains the end entity and intermediates, but not the trust anchor.
[[nodiscard]] VerifyResult verify_path(
    const std::vector<x509::Certificate>& leaf_first,
    const x509::Certificate& trust_anchor,
    const VerificationOptions& options);

// Raw-DER convenience entry. leaf_first excludes the trust anchor.
[[nodiscard]] VerifyResult verify_der_path(
    const std::vector<der::Bytes>& leaf_first_der,
    const der::Bytes& trust_anchor_der,
    const VerificationOptions& options);

// chain is leaf-first and chain.back() is treated as the trust anchor.
[[nodiscard]] VerifyResult verify_chain_including_anchor(
    const std::vector<x509::Certificate>& leaf_first_including_anchor,
    const VerificationOptions& options);

[[nodiscard]] std::int64_t current_unix_time();
[[nodiscard]] const char* error_code_name(ErrorCode code);

} // namespace pki
