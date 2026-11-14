#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Bytes = std::vector<std::uint8_t>;
using ByteView = std::span<const std::uint8_t>;

// The component deliberately exposes a small, strict error vocabulary.  A
// caller can log the offset without having to understand the decoder state.
enum class DerErrorCode : std::uint8_t {
  empty_input,
  truncated,
  trailing_data,
  unexpected_end_of_contents,
  invalid_tag,
  non_canonical_tag,
  indefinite_length,
  non_minimal_length,
  length_overflow,
  value_too_large,
  nesting_too_deep,
  too_many_nodes,
  invalid_boolean,
  invalid_integer,
  invalid_bit_string,
  invalid_null,
  invalid_oid,
  invalid_string,
  invalid_time,
  unsorted_set,
  invalid_certificate,
  duplicate_extension,
  invalid_extension,
  invalid_public_key,
  signature_mismatch,
};

const char* to_string(DerErrorCode code) noexcept;

class DerError : public std::runtime_error {
 public:
  DerError(DerErrorCode code, std::size_t offset, std::string message);

  DerErrorCode code() const noexcept { return code_; }
  std::size_t offset() const noexcept { return offset_; }

 private:
  DerErrorCode code_;
  std::size_t offset_;
};

struct DerTag {
  static constexpr std::uint8_t universal = 0;
  static constexpr std::uint8_t application = 1;
  static constexpr std::uint8_t context_specific = 2;
  static constexpr std::uint8_t private_tag = 3;

  std::uint8_t tag_class = universal;
  bool constructed = false;
  std::uint32_t number = 0;

  bool operator==(const DerTag&) const = default;
};

struct DerNode {
  DerTag tag;
  ByteView encoded;  // complete original TLV, never re-encoded
  ByteView content;
  std::vector<DerNode> children;

  bool is_universal(std::uint32_t number) const noexcept;
  bool is_context(std::uint32_t number, bool constructed) const noexcept;
};

struct DerLimits {
  std::size_t max_depth = 64;
  std::size_t max_nodes = 100'000;
  std::size_t max_value_size = 16U * 1024U * 1024U;
};

// Strict DER parser.  It accepts exactly one complete TLV and enforces the
// canonical rules that matter to certificate processing: definite minimal
// lengths, canonical identifiers, minimal INTEGER/BOOLEAN/BIT STRING/OID
// encodings and sorted SET OF values.  Returned views point into `input`.
class StrictDer {
 public:
  static DerNode parse(ByteView input, const DerLimits& limits = {});
};

struct NameAttribute {
  std::string oid;
  ByteView value_der;
  std::string text;
};

struct Rdn {
  std::vector<NameAttribute> attributes;
};

struct Name {
  std::vector<Rdn> rdns;
  ByteView der;

  bool empty() const noexcept { return rdns.empty(); }
  std::string text() const;       // human-readable, best effort
  std::string canonical() const;  // unambiguous exact identity
};

enum class GeneralNameKind : std::uint8_t {
  other_name,
  rfc822_name,
  dns_name,
  x400_address,
  directory_name,
  edi_party_name,
  uri,
  ip_address,
  registered_id,
};

const char* to_string(GeneralNameKind kind) noexcept;

struct GeneralName {
  std::uint32_t tag = 0;
  GeneralNameKind kind = GeneralNameKind::other_name;
  std::string value;
  ByteView der;
};

struct BasicConstraints {
  bool present = false;
  bool ca = false;
  std::optional<std::uint64_t> path_length;
};

struct KeyUsage {
  bool present = false;
  std::uint16_t bits = 0;

  bool has(unsigned bit) const noexcept {
    return bit < 16 && (bits & (std::uint16_t{1} << bit)) != 0;
  }
};

struct Extension {
  std::string oid;
  bool critical = false;
  ByteView value;   // extnValue payload (the inner DER object)
  ByteView der;     // complete Extension TLV from the certificate
  std::optional<DerNode> decoded;
};

// Identity is copied out as strings, so it remains valid independently of the
// Certificate object.  `canonical()` is length-delimited and cannot be made
// ambiguous by separators or embedded NULs.
struct CertificateIdentity {
  std::string subject;
  std::vector<std::string> subject_alt_names;
  bool basic_constraints_ca = false;
  std::optional<std::uint64_t> path_length;
  std::uint16_t key_usage_bits = 0;

  std::string canonical() const;
};

struct SignatureRequest {
  std::string_view algorithm_oid;
  ByteView algorithm_parameters;
  ByteView tbs;                  // exact TBSCertificate bytes from input
  ByteView signature;            // signature octets (unused bits already checked)
  ByteView public_key_info;      // exact SubjectPublicKeyInfo used to verify
};

class SignatureVerifier {
 public:
  virtual ~SignatureVerifier() = default;
  virtual bool verify(const SignatureRequest& request) = 0;
};

// Self-contained RSA PKCS#1 v1.5 verifier for SHA-1/SHA-256/SHA-384/SHA-512.
// It parses the SubjectPublicKeyInfo with StrictDer and hashes
// SignatureRequest::tbs directly; it never re-encodes the certificate.
class RsaPkcs1Verifier final : public SignatureVerifier {
 public:
  bool verify(const SignatureRequest& request) override;
};

class Certificate {
 public:
  static Certificate parse(ByteView der, const DerLimits& limits = {});

  const Bytes& encoded() const noexcept { return *encoded_; }
  const DerNode& root() const noexcept { return root_; }
  const DerNode& tbs_node() const noexcept { return tbs_; }

  ByteView tbs_der() const noexcept;
  ByteView signature() const noexcept;
  ByteView subject_public_key_info() const noexcept {
    return subject_public_key_info_;
  }

  std::uint64_t version() const noexcept { return version_; }
  const std::string& serial_number() const noexcept { return serial_number_; }
  const std::string& not_before() const noexcept { return not_before_; }
  const std::string& not_after() const noexcept { return not_after_; }

  const Name& issuer() const noexcept { return issuer_; }
  const Name& subject() const noexcept { return subject_; }
  const std::vector<GeneralName>& subject_alt_names() const noexcept {
    return subject_alt_names_;
  }
  const BasicConstraints& basic_constraints() const noexcept {
    return basic_constraints_;
  }
  const KeyUsage& key_usage() const noexcept { return key_usage_; }
  const std::vector<Extension>& extensions() const noexcept { return extensions_; }

  std::string_view signature_algorithm_oid() const noexcept {
    return signature_algorithm_oid_;
  }
  std::string_view tbs_signature_algorithm_oid() const noexcept {
    return tbs_signature_algorithm_oid_;
  }

  const Extension* find_extension(std::string_view oid) const noexcept;
  bool has_unsupported_critical_extensions() const;

  // Snapshot of the fields the business layer is allowed to consume.  All of
  // them are derived from the one canonical tree built during parse().
  CertificateIdentity identity() const;

  // Calls the verifier with the exact original TBS and SPKI slices.  The
  // built-in RSA verifier exposes this same contract.
  bool verify_signature(SignatureVerifier& verifier) const;
  bool verify_signature() const;

  // Path validation verifies with the issuer's SPKI.  This overload has the
  // same raw-TBS contract, so the caller never re-encodes the certificate.
  bool verify_signature_with_key(SignatureVerifier& verifier,
                                 ByteView verification_key_spki) const;
  bool verify_signature_with_key(ByteView verification_key_spki) const;

 private:
  std::shared_ptr<const Bytes> encoded_;
  DerNode root_;
  DerNode tbs_;
  ByteView signature_{};
  ByteView subject_public_key_info_{};

  std::uint64_t version_ = 0;
  std::string serial_number_;
  std::string not_before_;
  std::string not_after_;
  Name issuer_;
  Name subject_;
  std::vector<GeneralName> subject_alt_names_;
  BasicConstraints basic_constraints_;
  KeyUsage key_usage_;
  std::vector<Extension> extensions_;

  ByteView signature_parameters_{};
  ByteView tbs_signature_parameters_{};
  std::string signature_algorithm_oid_;
  std::string tbs_signature_algorithm_oid_;

  friend class CertificateParser;
};

}  // namespace netsec
