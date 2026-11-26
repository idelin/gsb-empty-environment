#include "pki/der.hpp"
#include "pki/names.hpp"
#include "pki/validator.hpp"
#include "pki/x509.hpp"

#include <iostream>
#include <sstream>
#include <stdexcept>
#include <string>
#include <vector>

using namespace pki;
using pki::der::Bytes;

namespace {

int g_checks = 0;
int g_failures = 0;

void check(bool condition, const std::string& expression, const char* file, int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "CHECK failed: " << expression << " (" << file << ':' << line << ")\n";
    }
}

#define CHECK(expression) check(static_cast<bool>(expression), #expression, __FILE__, __LINE__)

#define CHECK_THROWS(expression)                                          \
    do {                                                                  \
        ++g_checks;                                                       \
        bool threw_ = false;                                              \
        try {                                                             \
            (void)(expression);                                           \
        } catch (const std::exception&) {                                 \
            threw_ = true;                                                \
        }                                                                 \
        if (!threw_) {                                                    \
            ++g_failures;                                                 \
            std::cerr << "CHECK_THROWS failed: " #expression " ("         \
                      << __FILE__ << ':' << __LINE__ << ")\n";            \
        }                                                                 \
    } while (0)

Bytes cat(std::vector<Bytes> parts) {
    Bytes result;
    for (const Bytes& part : parts) {
        result.insert(result.end(), part.begin(), part.end());
    }
    return result;
}

Bytes text_bytes(const std::string& value) {
    return Bytes(value.begin(), value.end());
}

Bytes uint_content(std::uint64_t value) {
    Bytes encoded = der::encode_integer(value);
    der::Element integer = der::parse_one(encoded);
    return integer.content.to_bytes();
}

Bytes make_name(const std::vector<std::pair<std::string, std::string>>& attributes) {
    Bytes rdn_sequence;
    for (const auto& [oid, value] : attributes) {
        Bytes atv = der::encode_sequence(cat({
            der::encode_oid(oid),
            der::encode_utf8(value),
        }));
        Bytes rdn = der::encode_set(atv);
        rdn_sequence.insert(rdn_sequence.end(), rdn.begin(), rdn.end());
    }
    return der::encode_sequence(rdn_sequence);
}

Bytes cn_name(const std::string& common_name) {
    return make_name({{pki::x509::OID_COMMON_NAME, common_name}});
}

Bytes algorithm_identifier() {
    return der::encode_sequence(cat({
        der::encode_oid("1.2.840.113549.1.1.11"),
        der::encode_null(),
    }));
}

Bytes subject_public_key_info() {
    Bytes key = der::encode_bit_string(Bytes{0x30, 0x03, 0x01, 0x00, 0x01}, 0);
    return der::encode_sequence(cat({algorithm_identifier(), key}));
}

Bytes extension(const std::string& oid, bool critical, const Bytes& value) {
    Bytes body = der::encode_oid(oid);
    if (critical) {
        Bytes boolean = der::encode_boolean(true);
        body.insert(body.end(), boolean.begin(), boolean.end());
    }
    Bytes octet_value = der::encode_octet_string(value);
    body.insert(body.end(), octet_value.begin(), octet_value.end());
    return der::encode_sequence(body);
}

Bytes dns_general_name(const std::string& name) {
    return der::encode_context(2, text_bytes(name), false);
}

Bytes email_general_name(const std::string& email) {
    return der::encode_context(1, text_bytes(email), false);
}

Bytes uri_general_name(const std::string& uri) {
    return der::encode_context(6, text_bytes(uri), false);
}

Bytes ip_general_name(const Bytes& address) {
    return der::encode_context(7, address, false);
}

Bytes directory_general_name(const Bytes& name_tlv) {
    return der::encode_context(4, name_tlv, true);
}

Bytes subtree(const Bytes& general_name) {
    return der::encode_sequence(general_name);
}

Bytes name_constraints_value(const std::vector<Bytes>& permitted_subtrees,
                             const std::vector<Bytes>& excluded_subtrees = {}) {
    Bytes body;
    if (!permitted_subtrees.empty()) {
        Bytes content = cat(permitted_subtrees);
        Bytes tagged = der::encode_context(0, content, true);
        body.insert(body.end(), tagged.begin(), tagged.end());
    }
    if (!excluded_subtrees.empty()) {
        Bytes content = cat(excluded_subtrees);
        Bytes tagged = der::encode_context(1, content, true);
        body.insert(body.end(), tagged.begin(), tagged.end());
    }
    return der::encode_sequence(body);
}

Bytes san_value(const std::vector<Bytes>& names) {
    return der::encode_sequence(cat(names));
}

Bytes basic_constraints_value(bool ca, std::optional<std::uint64_t> path_length = std::nullopt) {
    Bytes body;
    if (ca) {
        Bytes boolean = der::encode_boolean(true);
        body.insert(body.end(), boolean.begin(), boolean.end());
    }
    if (path_length.has_value()) {
        Bytes integer = der::encode_integer(*path_length);
        body.insert(body.end(), integer.begin(), integer.end());
    }
    return der::encode_sequence(body);
}

Bytes key_usage_value(der::Byte bits) {
    return der::encode_bit_string(Bytes{bits}, 0);
}

Bytes policies_value(const std::vector<std::string>& policies) {
    Bytes infos;
    for (const std::string& policy : policies) {
        Bytes info = der::encode_sequence(der::encode_oid(policy));
        infos.insert(infos.end(), info.begin(), info.end());
    }
    return der::encode_sequence(infos);
}

Bytes policy_mappings_value(const std::vector<std::pair<std::string, std::string>>& mappings) {
    Bytes entries;
    for (const auto& [issuer_policy, subject_policy] : mappings) {
        Bytes mapping = der::encode_sequence(cat({
            der::encode_oid(issuer_policy),
            der::encode_oid(subject_policy),
        }));
        entries.insert(entries.end(), mapping.begin(), mapping.end());
    }
    return der::encode_sequence(entries);
}

Bytes policy_constraints_value(std::optional<std::uint64_t> require_explicit,
                               std::optional<std::uint64_t> inhibit_mapping) {
    Bytes body;
    if (require_explicit.has_value()) {
        Bytes tagged = der::encode_context(0, uint_content(*require_explicit), false);
        body.insert(body.end(), tagged.begin(), tagged.end());
    }
    if (inhibit_mapping.has_value()) {
        Bytes tagged = der::encode_context(1, uint_content(*inhibit_mapping), false);
        body.insert(body.end(), tagged.begin(), tagged.end());
    }
    return der::encode_sequence(body);
}

class CertificateBuilder {
public:
    CertificateBuilder() {
        issuer_dn_ = cn_name("Issuer CA");
        subject_dn_ = cn_name("Subject");
    }

    CertificateBuilder& serial(std::uint64_t value) {
        serial_ = value;
        return *this;
    }
    CertificateBuilder& issuer(const Bytes& dn) {
        issuer_dn_ = dn;
        return *this;
    }
    CertificateBuilder& subject(const Bytes& dn) {
        subject_dn_ = dn;
        return *this;
    }
    CertificateBuilder& not_before(const std::string& value) {
        not_before_ = value;
        return *this;
    }
    CertificateBuilder& not_after(const std::string& value) {
        not_after_ = value;
        return *this;
    }
    CertificateBuilder& add_extension(const Bytes& extension_tlv) {
        extensions_.push_back(extension_tlv);
        return *this;
    }
    CertificateBuilder& basic_constraints(bool ca,
                                          std::optional<std::uint64_t> path_length = std::nullopt,
                                          bool critical = true) {
        extensions_.push_back(extension(x509::OID_BASIC_CONSTRAINTS, critical,
                                        basic_constraints_value(ca, path_length)));
        return *this;
    }
    CertificateBuilder& key_cert_sign(bool critical = true) {
        extensions_.push_back(extension(x509::OID_KEY_USAGE, critical, key_usage_value(0x04)));
        return *this;
    }

    [[nodiscard]] Bytes build() const {
        Bytes body;
        Bytes version = der::encode_explicit(0, der::encode_integer(2));
        body.insert(body.end(), version.begin(), version.end());

        Bytes serial = der::encode_integer(serial_);
        body.insert(body.end(), serial.begin(), serial.end());

        Bytes alg = algorithm_identifier();
        body.insert(body.end(), alg.begin(), alg.end());
        body.insert(body.end(), issuer_dn_.begin(), issuer_dn_.end());

        Bytes validity = der::encode_sequence(cat({
            der::encode_utc_time(not_before_),
            der::encode_utc_time(not_after_),
        }));
        body.insert(body.end(), validity.begin(), validity.end());
        body.insert(body.end(), subject_dn_.begin(), subject_dn_.end());

        Bytes spki = subject_public_key_info();
        body.insert(body.end(), spki.begin(), spki.end());

        if (!extensions_.empty()) {
            Bytes extension_body = cat(extensions_);
            Bytes extensions_sequence = der::encode_sequence(extension_body);
            Bytes wrapper = der::encode_explicit(3, extensions_sequence);
            body.insert(body.end(), wrapper.begin(), wrapper.end());
        }

        Bytes tbs = der::encode_sequence(body);
        Bytes signature = der::encode_bit_string(Bytes{0xDE, 0xAD, 0xBE, 0xEF}, 0);
        return der::encode_sequence(cat({tbs, alg, signature}));
    }

private:
    std::uint64_t serial_ = 1;
    Bytes issuer_dn_;
    Bytes subject_dn_;
    std::string not_before_ = "200101000000Z";
    std::string not_after_ = "350101000000Z";
    std::vector<Bytes> extensions_;
};

x509::Certificate must_parse(const Bytes& bytes) {
    x509::ParseResult result = x509::parse_certificate(bytes);
    if (!result.ok) {
        throw std::runtime_error("certificate parse failed: " + result.error);
    }
    return std::move(result.certificate);
}

class AllowAllVerifier final : public SignatureVerifier {
public:
    int calls = 0;
    bool verify(const SignatureInput&) override {
        ++calls;
        return true;
    }
};

VerificationOptions options_with(SignatureVerifier& verifier) {
    VerificationOptions options;
    options.signature_verifier = &verifier;
    options.verification_time = x509::unix_seconds_from_date(2026, 6, 1, 0, 0, 0);
    return options;
}

x509::Certificate parse_builder(CertificateBuilder builder) {
    return must_parse(builder.build());
}

x509::Certificate root_certificate(std::uint64_t serial = 1) {
    Bytes dn = cn_name("Root");
    return parse_builder(CertificateBuilder()
                             .serial(serial)
                             .issuer(dn)
                             .subject(dn)
                             .basic_constraints(true, 10)
                             .key_cert_sign());
}

x509::Certificate ca_certificate(const std::string& issuer_cn,
                                 const std::string& subject_cn,
                                 std::uint64_t serial = 2) {
    return parse_builder(CertificateBuilder()
                             .serial(serial)
                             .issuer(cn_name(issuer_cn))
                             .subject(cn_name(subject_cn))
                             .basic_constraints(true)
                             .key_cert_sign());
}

x509::Certificate dns_leaf(const std::string& issuer_cn,
                           const std::string& dns_name,
                           std::uint64_t serial = 3) {
    return parse_builder(CertificateBuilder()
                             .serial(serial)
                             .issuer(cn_name(issuer_cn))
                             .subject(cn_name(dns_name))
                             .add_extension(extension(
                                 x509::OID_SUBJECT_ALT_NAME, false,
                                 san_value({dns_general_name(dns_name)}))));
}

void test_strict_der_rejects_ber_ambiguities() {
    CHECK_THROWS(der::parse_one(Bytes{0x30, 0x80, 0x00, 0x00}));
    CHECK_THROWS(der::parse_one(Bytes{0x30, 0x81, 0x03, 0x01, 0x01, 0xFF}));
    CHECK_THROWS(der::parse_one(Bytes{0x30, 0x82, 0x00, 0x03, 0x01, 0x01, 0xFF}));
    CHECK_THROWS(der::decode_uint(der::parse_one(Bytes{0x02, 0x02, 0x00, 0x01})));
    CHECK_THROWS(der::decode_uint(der::parse_one(Bytes{0x02, 0x02, 0xFF, 0xFF})));
    CHECK_THROWS(der::decode_boolean(der::parse_one(Bytes{0x01, 0x01, 0x01})));
    CHECK_THROWS(der::decode_bit_string(der::parse_one(Bytes{0x03, 0x02, 0x07, 0x01})));
    CHECK_THROWS(der::parse_one(Bytes{0x30, 0x00, 0x00}));
    CHECK_THROWS(der::decode_oid(der::parse_one(Bytes{0x06, 0x02, 0x80, 0x01})));

    Bytes good_integer = der::encode_integer(0x1234ULL);
    der::Element integer = der::parse_one(good_integer);
    CHECK(der::decode_uint(integer) == 0x1234);
    Bytes good_boolean = der::encode_boolean(true);
    CHECK(der::decode_boolean(der::parse_one(good_boolean)));
    Bytes good_bits = der::encode_bit_string(Bytes{0x80}, 7);
    der::Element bit_string = der::parse_one(good_bits);
    CHECK(bit_string.tag.number == 3);
}

void test_name_matching_boundaries() {
    CHECK(names::dns_matches_constraint("www.example.com", "example.com"));
    CHECK(names::dns_matches_constraint("example.com", "example.com"));
    CHECK(names::dns_matches_constraint("a.b.example.com", "example.com"));
    CHECK(!names::dns_matches_constraint("badexample.com", "example.com"));
    CHECK(names::dns_matches_constraint("WWW.Example.COM", "EXAMPLE.com"));

    CHECK(names::dns_matches_constraint("www.example.com", ".example.com"));
    CHECK(names::dns_matches_constraint("a.b.example.com", ".example.com"));
    CHECK(!names::dns_matches_constraint("example.com", ".example.com"));
    CHECK(!names::dns_matches_constraint("evil.example.org", ".example.com"));

    CHECK(names::email_matches_constraint("root@EXAMPLE.com", "root@example.com"));
    CHECK(!names::email_matches_constraint("Root@example.com", "root@example.com"));
    CHECK(names::email_matches_constraint("anyone@example.com", "example.com"));
    CHECK(!names::email_matches_constraint("anyone@foo.example.com", "example.com"));
    CHECK(names::email_matches_constraint("anyone@foo.example.com", ".example.com"));
    CHECK(!names::email_matches_constraint("anyone@example.com", ".example.com"));

    names::IpCidr v4 = names::parse_ip_cidr("10.0.0.0/8");
    Bytes v4_address = names::parse_ip_address("10.1.2.3");
    CHECK(names::ip_matches_constraint(v4_address, v4.address, v4.mask));
    CHECK(!names::ip_matches_constraint(names::parse_ip_address("192.168.1.1"),
                                        v4.address, v4.mask));
    names::IpCidr v6 = names::parse_ip_cidr("2001:db8::/32");
    CHECK(names::ip_matches_constraint(names::parse_ip_address("2001:db8::1"),
                                       v6.address, v6.mask));
    CHECK(!names::ip_matches_constraint(names::parse_ip_address("2001:db9::1"),
                                        v6.address, v6.mask));
    CHECK_THROWS(names::parse_ip_cidr("10.0.0.0/33"));
    CHECK_THROWS(names::parse_ip_cidr("::1/129"));
}

void test_certificate_parser_reads_requested_extensions() {
    Bytes nc_value = name_constraints_value(
        {subtree(dns_general_name("example.com"))},
        {subtree(ip_general_name(cat({names::parse_ip_cidr("10.0.0.0/8").address,
                                      names::parse_ip_cidr("10.0.0.0/8").mask})))});

    CertificateBuilder builder;
    Bytes cert_der = builder.serial(42)
                         .subject(cn_name("leaf.example.com"))
                         .add_extension(extension(x509::OID_SUBJECT_ALT_NAME, false,
                                                 san_value({
                                                     dns_general_name("leaf.example.com"),
                                                     email_general_name("leaf@example.com"),
                                                     uri_general_name("https://leaf.example.com/path"),
                                                     ip_general_name(names::parse_ip_address("10.1.2.3")),
                                                     ip_general_name(names::parse_ip_address("2001:db8::1")),
                                                     directory_general_name(cn_name("Directory Name")),
                                                 })))
                         .basic_constraints(false)
                         .add_extension(extension(x509::OID_NAME_CONSTRAINTS, true, nc_value))
                         .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                                 policies_value({"1.2.3.4", x509::OID_ANY_POLICY})))
                         .add_extension(extension(x509::OID_POLICY_MAPPINGS, false,
                                                 policy_mappings_value({{"1.2.3.4", "1.2.3.5"}})))
                         .add_extension(extension(x509::OID_POLICY_CONSTRAINTS, true,
                                                 policy_constraints_value(0U, 1U)))
                         .add_extension(extension(x509::OID_INHIBIT_ANY_POLICY, true,
                                                 der::encode_integer(0U)))
                         .build();

    x509::ParseResult parse_result = x509::parse_certificate(cert_der);
    CHECK(parse_result.ok);
    x509::Certificate certificate = std::move(parse_result.certificate);
    CHECK(certificate.version == 3);
    CHECK(certificate.serial_number.size() == 1);
    CHECK(certificate.serial_number[0] == 42);
    CHECK(certificate.subject.common_names().size() == 1);
    CHECK(certificate.subject.common_names()[0] == "leaf.example.com");
    CHECK(certificate.subject_alt_names.size() == 6);
    CHECK(certificate.subject_alt_names[0].kind == x509::GeneralNameKind::Dns);
    CHECK(certificate.subject_alt_names[3].ip.size() == 4);
    CHECK(certificate.subject_alt_names[4].ip.size() == 16);
    CHECK(certificate.basic_constraints.has_value());
    CHECK(!certificate.basic_constraints->ca);
    CHECK(certificate.name_constraints.has_value());
    CHECK(certificate.name_constraints->permitted.size() == 1);
    CHECK(certificate.name_constraints->excluded.size() == 1);
    CHECK(certificate.name_constraints->excluded[0].ip_mask.size() == 4);
    CHECK(certificate.certificate_policies.size() == 2);
    CHECK(certificate.policy_mappings.size() == 1);
    CHECK(certificate.policy_constraints.has_value());
    CHECK(certificate.policy_constraints->require_explicit_policy.value_or(99) == 0);
    CHECK(certificate.policy_constraints->inhibit_policy_mapping.value_or(99) == 1);
    CHECK(certificate.inhibit_any_policy.value_or(99) == 0);
}

void test_basic_chain_and_bad_signature() {
    Bytes root_der = CertificateBuilder()
                         .serial(1)
                         .subject(cn_name("Root"))
                         .issuer(cn_name("Root"))
                         .basic_constraints(true, 10)
                         .key_cert_sign()
                         .build();
    Bytes intermediate_der = CertificateBuilder()
                                 .serial(2)
                                 .issuer(cn_name("Root"))
                                 .subject(cn_name("Intermediate"))
                                 .basic_constraints(true, 1)
                                 .key_cert_sign()
                                 .build();
    Bytes leaf_der = CertificateBuilder()
                         .serial(3)
                         .issuer(cn_name("Intermediate"))
                         .subject(cn_name("leaf.example.com"))
                         .add_extension(extension(
                             x509::OID_SUBJECT_ALT_NAME, false,
                             san_value({dns_general_name("leaf.example.com")})))
                         .build();

    x509::Certificate root = must_parse(root_der);
    x509::Certificate intermediate = must_parse(intermediate_der);
    x509::Certificate leaf = must_parse(leaf_der);

    AllowAllVerifier verifier;
    VerificationOptions options = options_with(verifier);
    VerifyResult result = verify_path({leaf, intermediate}, root, options);
    CHECK(result.ok());
    CHECK(verifier.calls == 2);

    VerifyResult raw_result = verify_der_path({leaf.der, intermediate.der}, root.der, options);
    CHECK(raw_result.ok());

    class RejectOnce final : public SignatureVerifier {
    public:
        bool verify(const SignatureInput& input) override {
            return input.subject.serial_number[0] != 2;
        }
    } reject_once;
    options.signature_verifier = &reject_once;
    VerifyResult bad = verify_path({leaf, intermediate}, root, options);
    CHECK(bad.code == ErrorCode::BadSignature);
    CHECK(bad.certificate_index == 1);
}

void test_name_constraints_exclusion_priority_and_bypass() {
    x509::Certificate root = root_certificate();
    Bytes nc = name_constraints_value(
        {subtree(dns_general_name("example.com"))},
        {subtree(dns_general_name("evil.example.com"))});
    x509::Certificate ca = parse_builder(
        CertificateBuilder()
            .serial(2)
            .issuer(cn_name("Root"))
            .subject(cn_name("Constrained CA"))
            .basic_constraints(true, 0)
            .key_cert_sign()
            .add_extension(extension(x509::OID_NAME_CONSTRAINTS, true, nc)));

    AllowAllVerifier verifier;
    VerificationOptions options = options_with(verifier);

    VerifyResult good =
        verify_path({dns_leaf("Constrained CA", "www.example.com"), ca}, root, options);
    CHECK(good.ok());

    VerifyResult excluded =
        verify_path({dns_leaf("Constrained CA", "evil.example.com", 4), ca}, root, options);
    CHECK(excluded.code == ErrorCode::ExcludedName);

    VerifyResult suffix_attack =
        verify_path({dns_leaf("Constrained CA", "evil-example.com", 5), ca}, root, options);
    CHECK(suffix_attack.code == ErrorCode::PermittedNameViolation);

    VerifyResult outside =
        verify_path({dns_leaf("Constrained CA", "www.other.org", 6), ca}, root, options);
    CHECK(outside.code == ErrorCode::PermittedNameViolation);
}

void test_permitted_intersection_and_excluded_union() {
    Bytes root_nc = name_constraints_value({subtree(dns_general_name("example.com"))},
                                           {subtree(dns_general_name("root-bad.example.com"))});
    x509::Certificate root = parse_builder(
        CertificateBuilder()
            .issuer(cn_name("Root"))
            .subject(cn_name("Root"))
            .basic_constraints(true, 10)
            .add_extension(extension(x509::OID_NAME_CONSTRAINTS, true, root_nc)));

    Bytes ca_nc = name_constraints_value({subtree(dns_general_name("www.example.com"))},
                                        {subtree(dns_general_name("ca-bad.example.com"))});
    x509::Certificate ca = parse_builder(
        CertificateBuilder()
            .issuer(cn_name("Root"))
            .subject(cn_name("Web CA"))
            .basic_constraints(true, 0)
            .add_extension(extension(x509::OID_NAME_CONSTRAINTS, true, ca_nc)));

    AllowAllVerifier verifier;
    VerificationOptions options = options_with(verifier);

    CHECK(verify_path({dns_leaf("Web CA", "www.example.com"), ca}, root, options).ok());
    CHECK(verify_path({dns_leaf("Web CA", "api.example.com", 4), ca}, root, options).code ==
          ErrorCode::PermittedNameViolation);
    CHECK(verify_path({dns_leaf("Web CA", "root-bad.example.com", 5), ca}, root, options).code ==
          ErrorCode::ExcludedName);
    CHECK(verify_path({dns_leaf("Web CA", "ca-bad.example.com", 6), ca}, root, options).code ==
          ErrorCode::ExcludedName);
}

void test_dns_leading_dot_and_ip_email_constraints() {
    x509::Certificate root = root_certificate();

    Bytes dns_nc = name_constraints_value({subtree(dns_general_name(".example.com"))});
    x509::Certificate dns_ca = parse_builder(
        CertificateBuilder()
            .serial(2)
            .issuer(cn_name("Root"))
            .subject(cn_name("DNS CA"))
            .basic_constraints(true, 0)
            .add_extension(extension(x509::OID_NAME_CONSTRAINTS, true, dns_nc)));
    AllowAllVerifier verifier;
    VerificationOptions options = options_with(verifier);
    CHECK(verify_path({dns_leaf("DNS CA", "www.example.com"), dns_ca}, root, options).ok());
    CHECK(verify_path({dns_leaf("DNS CA", "example.com", 4), dns_ca}, root, options).code ==
          ErrorCode::PermittedNameViolation);

    names::IpCidr cidr = names::parse_ip_cidr("10.0.0.0/8");
    Bytes ip_constraint = cat({cidr.address, cidr.mask});
    Bytes ip_nc = name_constraints_value({subtree(ip_general_name(ip_constraint))});
    x509::Certificate ip_ca = parse_builder(
        CertificateBuilder()
            .serial(3)
            .issuer(cn_name("Root"))
            .subject(cn_name("IP CA"))
            .basic_constraints(true, 0)
            .add_extension(extension(x509::OID_NAME_CONSTRAINTS, true, ip_nc)));

    auto ip_leaf = [&](const std::string& address, std::uint64_t serial) {
        return parse_builder(
            CertificateBuilder()
                .serial(serial)
                .issuer(cn_name("IP CA"))
                .subject(cn_name("IP Leaf"))
                .add_extension(extension(
                    x509::OID_SUBJECT_ALT_NAME, false,
                    san_value({ip_general_name(names::parse_ip_address(address))}))));
    };
    CHECK(verify_path({ip_leaf("10.20.30.40", 10), ip_ca}, root, options).ok());
    CHECK(verify_path({ip_leaf("192.168.1.1", 11), ip_ca}, root, options).code ==
          ErrorCode::PermittedNameViolation);

    Bytes email_nc = name_constraints_value({subtree(email_general_name("example.com"))});
    x509::Certificate email_ca = parse_builder(
        CertificateBuilder()
            .serial(4)
            .issuer(cn_name("Root"))
            .subject(cn_name("Email CA"))
            .basic_constraints(true, 0)
            .add_extension(extension(x509::OID_NAME_CONSTRAINTS, true, email_nc)));

    auto email_leaf = [&](const std::string& email, std::uint64_t serial) {
        return parse_builder(
            CertificateBuilder()
                .serial(serial)
                .issuer(cn_name("Email CA"))
                .subject(cn_name("Email Leaf"))
                .add_extension(extension(x509::OID_SUBJECT_ALT_NAME, false,
                                        san_value({email_general_name(email)}))));
    };
    CHECK(verify_path({email_leaf("Any@EXAMPLE.com", 12), email_ca}, root, options).ok());
    CHECK(verify_path({email_leaf("a@foo.example.com", 13), email_ca}, root, options).code ==
          ErrorCode::PermittedNameViolation);
}

void test_unknown_critical_name_chaining_and_validity() {
    x509::Certificate root = root_certificate();
    AllowAllVerifier verifier;
    VerificationOptions options = options_with(verifier);

    x509::Certificate unknown_leaf = parse_builder(
        CertificateBuilder()
            .issuer(cn_name("Root"))
            .subject(cn_name("Unknown Critical"))
            .add_extension(extension("1.2.3.4.999", true, der::encode_null())));
    CHECK(verify_path({unknown_leaf}, root, options).code ==
          ErrorCode::UnrecognizedCriticalExtension);

    x509::Certificate wrong_issuer = parse_builder(
        CertificateBuilder().issuer(cn_name("Other Root")).subject(cn_name("Leaf")));
    CHECK(verify_path({wrong_issuer}, root, options).code == ErrorCode::NameChaining);

    x509::Certificate expired = parse_builder(
        CertificateBuilder().issuer(cn_name("Root")).not_after("200101000000Z"));
    CHECK(verify_path({expired}, root, options).code == ErrorCode::Expired);

    x509::Certificate not_yet = parse_builder(
        CertificateBuilder().issuer(cn_name("Root")).not_before("300101000000Z"));
    CHECK(verify_path({not_yet}, root, options).code == ErrorCode::NotYetValid);
}

void test_pathlen_and_self_issued_handling() {
    AllowAllVerifier verifier;
    VerificationOptions options = options_with(verifier);

    x509::Certificate zero_root = parse_builder(
        CertificateBuilder()
            .issuer(cn_name("Root"))
            .subject(cn_name("Root"))
            .basic_constraints(true, 0)
            .key_cert_sign());
    x509::Certificate ca = ca_certificate("Root", "CA");
    x509::Certificate leaf = dns_leaf("CA", "leaf.example.com");
    CHECK(verify_path({leaf, ca}, zero_root, options).code ==
          ErrorCode::PathLengthExceeded);

    x509::Certificate one_root = parse_builder(
        CertificateBuilder()
            .issuer(cn_name("Root"))
            .subject(cn_name("Root"))
            .basic_constraints(true, 1)
            .key_cert_sign());
    Bytes root_dn = cn_name("Root");
    x509::Certificate self_issued = parse_builder(
        CertificateBuilder()
            .serial(20)
            .issuer(root_dn)
            .subject(root_dn)
            .basic_constraints(true)
            .key_cert_sign());
    x509::Certificate normal_ca = ca_certificate("Root", "Normal CA", 21);
    x509::Certificate normal_leaf = dns_leaf("Normal CA", "leaf.example.com", 22);
    VerifyResult self_issued_path =
        verify_path({normal_leaf, normal_ca, self_issued}, one_root, options);
    CHECK(self_issued_path.ok());

    Bytes restrictive_nc = name_constraints_value(
        {subtree(dns_general_name("allowed.example"))});
    x509::Certificate constrained_root = parse_builder(
        CertificateBuilder()
            .issuer(cn_name("Root"))
            .subject(cn_name("Root"))
            .basic_constraints(true, 10)
            .add_extension(extension(x509::OID_NAME_CONSTRAINTS, true, restrictive_nc)));
    x509::Certificate exempt_self_issued = parse_builder(
        CertificateBuilder()
            .serial(30)
            .issuer(root_dn)
            .subject(root_dn)
            .basic_constraints(true)
            .add_extension(extension(
                x509::OID_SUBJECT_ALT_NAME, false,
                san_value({dns_general_name("not-checked.evil")}))));
    x509::Certificate allowed_leaf = dns_leaf("Root", "www.allowed.example", 31);
    CHECK(verify_path({allowed_leaf, exempt_self_issued}, constrained_root, options).ok());
}

void test_policy_mapping_counter_and_escalation() {
    const std::string issuer_policy = "1.2.3.100";
    const std::string subject_policy = "1.2.3.200";
    x509::Certificate root = root_certificate();
    AllowAllVerifier verifier;
    VerificationOptions options = options_with(verifier);
    options.user_initial_policies = {issuer_policy};

    for (std::uint64_t inhibit_mapping : {0ULL, 1ULL}) {
        x509::Certificate policy_ca = parse_builder(
            CertificateBuilder()
                .serial(40)
                .issuer(cn_name("Root"))
                .subject(cn_name("Policy CA"))
                .basic_constraints(true)
                .key_cert_sign()
                .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                        policies_value({issuer_policy})))
                .add_extension(extension(
                    x509::OID_POLICY_CONSTRAINTS, true,
                    policy_constraints_value(std::nullopt, inhibit_mapping))));
        x509::Certificate cross_ca = parse_builder(
            CertificateBuilder()
                .serial(41)
                .issuer(cn_name("Policy CA"))
                .subject(cn_name("Cross CA"))
                .basic_constraints(true)
                .key_cert_sign()
                .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                        policies_value({issuer_policy})))
                .add_extension(extension(
                    x509::OID_POLICY_MAPPINGS, false,
                    policy_mappings_value({{issuer_policy, subject_policy}}))));
        x509::Certificate mapped_leaf = parse_builder(
            CertificateBuilder()
                .serial(42)
                .issuer(cn_name("Cross CA"))
                .subject(cn_name("Mapped Leaf"))
                .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                        policies_value({subject_policy}))));

        VerifyResult result =
            verify_path({mapped_leaf, cross_ca, policy_ca}, root, options);
        if (inhibit_mapping == 1) {
            CHECK(result.ok());
        } else {
            CHECK(!result.ok());
            CHECK(result.code == ErrorCode::NoValidPolicy);
        }
    }

    VerificationOptions initial_inhibit = options_with(verifier);
    initial_inhibit.initial_policy_mapping_inhibit = true;
    initial_inhibit.user_initial_policies = {issuer_policy};
    x509::Certificate simple_ca = parse_builder(
        CertificateBuilder()
            .issuer(cn_name("Root"))
            .subject(cn_name("Simple Cross"))
            .basic_constraints(true)
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({issuer_policy})))
            .add_extension(extension(
                x509::OID_POLICY_MAPPINGS, false,
                policy_mappings_value({{issuer_policy, subject_policy}}))));
    x509::Certificate simple_leaf = parse_builder(
        CertificateBuilder()
            .issuer(cn_name("Simple Cross"))
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({subject_policy}))));
    CHECK(verify_path({simple_leaf, simple_ca}, root, initial_inhibit).code ==
          ErrorCode::NoValidPolicy);
}

void test_self_issued_policy_counters() {
    const std::string policy_a = "1.2.3.10";
    x509::Certificate root = root_certificate();
    AllowAllVerifier verifier;
    VerificationOptions options = options_with(verifier);

    x509::Certificate policy_ca = parse_builder(
        CertificateBuilder()
            .serial(50)
            .issuer(cn_name("Root"))
            .subject(cn_name("Policy CA"))
            .basic_constraints(true)
            .key_cert_sign()
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({policy_a})))
            .add_extension(extension(x509::OID_POLICY_CONSTRAINTS, true,
                                    policy_constraints_value(1U, 1U))));

    Bytes policy_dn = cn_name("Policy CA");
    x509::Certificate self_issued = parse_builder(
        CertificateBuilder()
            .serial(51)
            .issuer(policy_dn)
            .subject(policy_dn)
            .basic_constraints(true)
            .key_cert_sign()
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({policy_a}))));

    x509::Certificate mapping_ca = parse_builder(
        CertificateBuilder()
            .serial(52)
            .issuer(policy_dn)
            .subject(cn_name("Mapping CA"))
            .basic_constraints(true)
            .key_cert_sign()
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({policy_a})))
            .add_extension(extension(
                x509::OID_POLICY_MAPPINGS, false,
                policy_mappings_value({{policy_a, "1.2.3.99"}}))));
    x509::Certificate mapped_leaf = parse_builder(
        CertificateBuilder()
            .serial(53)
            .issuer(cn_name("Mapping CA"))
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({"1.2.3.99"}))));

    VerificationOptions mapping_options = options;
    mapping_options.user_initial_policies = {policy_a};
    VerifyResult mapping_result = verify_path(
        {mapped_leaf, mapping_ca, self_issued, policy_ca}, root, mapping_options);
    CHECK(mapping_result.ok());

    x509::Certificate policy_ca_explicit = parse_builder(
        CertificateBuilder()
            .serial(54)
            .issuer(cn_name("Root"))
            .subject(cn_name("Explicit CA"))
            .basic_constraints(true)
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({policy_a})))
            .add_extension(extension(x509::OID_POLICY_CONSTRAINTS, true,
                                    policy_constraints_value(1U, std::nullopt))));
    Bytes explicit_dn = cn_name("Explicit CA");
    x509::Certificate self_issued_no_policy = parse_builder(
        CertificateBuilder()
            .serial(55)
            .issuer(explicit_dn)
            .subject(explicit_dn)
            .basic_constraints(true));
    x509::Certificate explicit_middle = parse_builder(
        CertificateBuilder()
            .serial(56)
            .issuer(explicit_dn)
            .subject(cn_name("Explicit Middle"))
            .basic_constraints(true)
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({policy_a}))));
    x509::Certificate explicit_leaf = parse_builder(
        CertificateBuilder()
            .serial(57)
            .issuer(cn_name("Explicit Middle"))
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({policy_a}))));
    VerifyResult explicit_result = verify_path(
        {explicit_leaf, explicit_middle, self_issued_no_policy, policy_ca_explicit},
        root, options);
    CHECK(explicit_result.code == ErrorCode::ExplicitPolicyRequired);
    CHECK(explicit_result.certificate_index == 1);

    VerificationOptions explicit_initial = options;
    explicit_initial.initial_explicit_policy = true;
    x509::Certificate inhibit_ca = parse_builder(
        CertificateBuilder()
            .serial(58)
            .issuer(cn_name("Root"))
            .subject(cn_name("Any Inhibit CA"))
            .basic_constraints(true)
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({policy_a})))
            .add_extension(extension(x509::OID_INHIBIT_ANY_POLICY, true,
                                    der::encode_integer(0U))));
    Bytes inhibit_dn = cn_name("Any Inhibit CA");
    x509::Certificate self_issued_any = parse_builder(
        CertificateBuilder()
            .serial(59)
            .issuer(inhibit_dn)
            .subject(inhibit_dn)
            .basic_constraints(true)
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({x509::OID_ANY_POLICY}))));
    x509::Certificate explicit_after_si = parse_builder(
        CertificateBuilder()
            .serial(60)
            .issuer(inhibit_dn)
            .subject(cn_name("After SI"))
            .basic_constraints(true)
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({policy_a}))));
    x509::Certificate explicit_leaf_after_si = parse_builder(
        CertificateBuilder()
            .serial(61)
            .issuer(cn_name("After SI"))
            .add_extension(extension(x509::OID_CERTIFICATE_POLICIES, false,
                                    policies_value({policy_a}))));
    VerifyResult any_result = verify_path(
        {explicit_leaf_after_si, explicit_after_si, self_issued_any, inhibit_ca},
        root, explicit_initial);
    CHECK(any_result.ok());
}

void test_final_self_issued_name_check_is_not_exempt() {
    Bytes dn = cn_name("Self Final");
    Bytes nc = name_constraints_value({}, {subtree(dns_general_name("evil.example"))});
    x509::Certificate anchor = parse_builder(
        CertificateBuilder()
            .issuer(dn)
            .subject(dn)
            .basic_constraints(true)
            .add_extension(extension(x509::OID_NAME_CONSTRAINTS, true, nc)));
    x509::Certificate self_final = parse_builder(
        CertificateBuilder()
            .issuer(dn)
            .subject(dn)
            .add_extension(extension(
                x509::OID_SUBJECT_ALT_NAME, false,
                san_value({dns_general_name("blocked.evil.example")}))));
    AllowAllVerifier verifier;
    VerificationOptions options = options_with(verifier);
    CHECK(verify_path({self_final}, anchor, options).code == ErrorCode::ExcludedName);
}

void test_key_usage_parsing() {
    x509::Certificate certificate = parse_builder(
        CertificateBuilder().add_extension(
            extension(x509::OID_KEY_USAGE, true, key_usage_value(0x84))));
    CHECK(certificate.key_usage.has_value());
    CHECK(certificate.key_usage->digital_signature);
    CHECK(certificate.key_usage->key_cert_sign);
    CHECK(!certificate.key_usage->crl_sign);
}

} // namespace

int main() {
    try {
        test_strict_der_rejects_ber_ambiguities();
        test_name_matching_boundaries();
        test_certificate_parser_reads_requested_extensions();
        test_basic_chain_and_bad_signature();
        test_name_constraints_exclusion_priority_and_bypass();
        test_permitted_intersection_and_excluded_union();
        test_dns_leading_dot_and_ip_email_constraints();
        test_unknown_critical_name_chaining_and_validity();
        test_pathlen_and_self_issued_handling();
        test_policy_mapping_counter_and_escalation();
        test_self_issued_policy_counters();
        test_final_self_issued_name_check_is_not_exempt();
        test_key_usage_parsing();
    } catch (const std::exception& error) {
        ++g_failures;
        std::cerr << "test threw: " << error.what() << '\n';
    }

    if (g_failures != 0) {
        std::cerr << g_failures << " failure(s) after " << g_checks << " checks\n";
        return 1;
    }
    std::cout << "all " << g_checks << " checks passed\n";
    return 0;
}
