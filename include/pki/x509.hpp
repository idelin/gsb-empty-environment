#pragma once

#include "pki/der.hpp"

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <vector>

namespace pki::x509 {

inline constexpr const char* OID_COMMON_NAME = "2.5.4.3";
inline constexpr const char* OID_COUNTRY = "2.5.4.6";
inline constexpr const char* OID_ORGANIZATION = "2.5.4.10";
inline constexpr const char* OID_ORGANIZATIONAL_UNIT = "2.5.4.11";

inline constexpr const char* OID_BASIC_CONSTRAINTS = "2.5.29.19";
inline constexpr const char* OID_KEY_USAGE = "2.5.29.15";
inline constexpr const char* OID_SUBJECT_ALT_NAME = "2.5.29.17";
inline constexpr const char* OID_ISSUER_ALT_NAME = "2.5.29.18";
inline constexpr const char* OID_NAME_CONSTRAINTS = "2.5.29.30";
inline constexpr const char* OID_CERTIFICATE_POLICIES = "2.5.29.32";
inline constexpr const char* OID_POLICY_MAPPINGS = "2.5.29.33";
inline constexpr const char* OID_POLICY_CONSTRAINTS = "2.5.29.36";
inline constexpr const char* OID_INHIBIT_ANY_POLICY = "2.5.29.54";
inline constexpr const char* OID_EXTENDED_KEY_USAGE = "2.5.29.37";
inline constexpr const char* OID_SUBJECT_KEY_IDENTIFIER = "2.5.29.14";
inline constexpr const char* OID_AUTHORITY_KEY_IDENTIFIER = "2.5.29.35";
inline constexpr const char* OID_CRL_DISTRIBUTION_POINTS = "2.5.29.31";
inline constexpr const char* OID_AUTHORITY_INFO_ACCESS = "1.3.6.1.5.5.7.1.1";
inline constexpr const char* OID_ANY_POLICY = "2.5.29.32.0";

struct AlgorithmIdentifier {
    std::string oid;
    der::Bytes parameters;
    bool has_parameters = false;
    der::Bytes raw;
};

struct X509Time {
    std::int64_t unix_seconds = 0;
    std::string raw;
};

struct AttributeTypeAndValue {
    std::string oid;
    der::Byte value_tag{};
    std::string value;
    der::Bytes raw;
};

struct DistinguishedName {
    der::Bytes raw;
    std::vector<AttributeTypeAndValue> attributes;

    [[nodiscard]] std::vector<std::string> common_names() const;
};

enum class GeneralNameKind {
    Other,
    Rfc822,
    Dns,
    X400Address,
    Directory,
    EdiParty,
    Uri,
    Ip,
    RegisteredId,
};

struct GeneralName {
    GeneralNameKind kind = GeneralNameKind::Other;
    std::string text;
    der::Bytes ip;
    der::Bytes ip_address;
    der::Bytes ip_mask;
    der::Bytes directory_name;
};

struct KeyUsage {
    bool digital_signature = false;
    bool non_repudiation = false;
    bool key_encipherment = false;
    bool data_encipherment = false;
    bool key_agreement = false;
    bool key_cert_sign = false;
    bool crl_sign = false;
    bool encipher_only = false;
    bool decipher_only = false;
};

struct BasicConstraints {
    bool ca = false;
    std::optional<std::uint64_t> path_len_constraint;
};

struct NameConstraints {
    std::vector<GeneralName> permitted;
    std::vector<GeneralName> excluded;
};

struct PolicyMapping {
    std::string issuer_domain_policy;
    std::string subject_domain_policy;
};

struct PolicyConstraints {
    std::optional<std::uint64_t> require_explicit_policy;
    std::optional<std::uint64_t> inhibit_policy_mapping;
};

struct Extension {
    std::string oid;
    bool critical = false;
    der::Bytes value;
    der::Bytes raw;
};

struct SubjectPublicKeyInfo {
    AlgorithmIdentifier algorithm;
    der::Bytes public_key;
    der::Bytes raw;
};

struct Certificate {
    der::Bytes der;
    der::Bytes tbs_der;
    der::Bytes signature;
    der::Bytes signature_algorithm_raw;

    std::uint32_t version = 1;
    der::Bytes serial_number;
    AlgorithmIdentifier signature_algorithm;

    der::Bytes issuer_raw;
    der::Bytes subject_raw;
    DistinguishedName issuer;
    DistinguishedName subject;

    X509Time not_before;
    X509Time not_after;

    SubjectPublicKeyInfo subject_public_key_info;

    std::vector<Extension> extensions;
    std::vector<std::string> unknown_critical_extensions;

    std::optional<BasicConstraints> basic_constraints;
    std::optional<KeyUsage> key_usage;
    std::vector<GeneralName> subject_alt_names;
    std::optional<NameConstraints> name_constraints;
    std::vector<std::string> certificate_policies;
    std::vector<PolicyMapping> policy_mappings;
    std::optional<PolicyConstraints> policy_constraints;
    std::optional<std::uint64_t> inhibit_any_policy;
    std::vector<std::string> extended_key_usages;

    [[nodiscard]] bool is_self_issued() const;
    [[nodiscard]] const Extension* find_extension(const std::string& oid) const;
};

struct ParseResult {
    bool ok = false;
    std::string error;
    Certificate certificate;

    static ParseResult success(Certificate certificate);
    static ParseResult failure(std::string error);
};

[[nodiscard]] ParseResult parse_certificate(der::ByteView certificate_der);
[[nodiscard]] std::int64_t unix_seconds_from_date(int year, int month, int day,
                                                  int hour, int minute, int second);

} // namespace pki::x509
