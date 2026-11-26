#include "pki/x509.hpp"

#include <algorithm>
#include <array>
#include <stdexcept>

namespace pki::x509 {

namespace {

[[noreturn]] void fail(const std::string& message) {
    throw std::runtime_error(message);
}

bool all_digits(const std::string& value, std::size_t begin, std::size_t count) {
    if (begin + count > value.size()) {
        return false;
    }
    for (std::size_t i = begin; i < begin + count; ++i) {
        if (value[i] < '0' || value[i] > '9') {
            return false;
        }
    }
    return true;
}

int two_digits(const std::string& value, std::size_t position) {
    return (value[position] - '0') * 10 + (value[position + 1] - '0');
}

std::uint64_t decode_context_uint(const der::Element& element) {
    der::Bytes integer_tlv = der::encode_tlv(0x02, element.content);
    der::Element wrapped = der::parse_one(integer_tlv);
    return der::decode_uint(wrapped);
}

AlgorithmIdentifier parse_algorithm(const der::Element& element) {
    der::expect_universal(element, 0x30);
    std::vector<der::Element> children = der::parse_children(element);
    if (children.empty() || children.size() > 2) {
        fail("invalid AlgorithmIdentifier");
    }

    AlgorithmIdentifier result;
    result.raw = element.raw.to_bytes();
    result.oid = der::decode_oid(children[0]);
    if (children.size() == 2) {
        result.has_parameters = true;
        result.parameters = children[1].raw.to_bytes();
    }
    return result;
}

X509Time parse_time(const der::Element& element) {
    if (element.tag.klass != der::TagClass::Universal) {
        fail("Time must be a universal UTCTime or GeneralizedTime tag");
    }
    const std::string value = der::decode_string(element);
    int year = 0;
    int month = 0;
    int day = 0;
    int hour = 0;
    int minute = 0;
    int second = 0;

    if (element.tag.number == 23) {
        if (value.size() != 13 || value[12] != 'Z' || !all_digits(value, 0, 12)) {
            fail("invalid UTCTime");
        }
        const int short_year = two_digits(value, 0);
        year = short_year < 50 ? 2000 + short_year : 1900 + short_year;
        month = two_digits(value, 2);
        day = two_digits(value, 4);
        hour = two_digits(value, 6);
        minute = two_digits(value, 8);
        second = two_digits(value, 10);
    } else if (element.tag.number == 24) {
        if (value.size() != 15 || value[14] != 'Z' || !all_digits(value, 0, 14)) {
            fail("invalid GeneralizedTime");
        }
        year = two_digits(value, 0) * 100 + two_digits(value, 2);
        month = two_digits(value, 4);
        day = two_digits(value, 6);
        hour = two_digits(value, 8);
        minute = two_digits(value, 10);
        second = two_digits(value, 12);
    } else {
        fail("invalid X.509 time tag");
    }

    if (month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || second > 59) {
        fail("X.509 time field out of range");
    }

    X509Time result;
    result.raw = value;
    result.unix_seconds = unix_seconds_from_date(year, month, day, hour, minute, second);
    return result;
}

DistinguishedName parse_name(const der::Element& element) {
    der::expect_universal(element, 0x30);
    DistinguishedName name;
    name.raw = element.raw.to_bytes();

    for (const der::Element& rdn : der::parse_children(element)) {
        der::expect_universal(rdn, 0x31);
        for (const der::Element& atv_element : der::parse_children(rdn)) {
            der::expect_universal(atv_element, 0x30);
            std::vector<der::Element> atv = der::parse_children(atv_element);
            if (atv.size() != 2) {
                fail("invalid AttributeTypeAndValue");
            }
            AttributeTypeAndValue attribute;
            attribute.oid = der::decode_oid(atv[0]);
            attribute.value_tag = atv[1].tag.first_byte;
            attribute.value = der::decode_string(atv[1]);
            attribute.raw = atv[1].raw.to_bytes();
            name.attributes.push_back(std::move(attribute));
        }
    }
    return name;
}

GeneralName parse_general_name(const der::Element& element) {
    if (element.tag.klass != der::TagClass::Context) {
        fail("GeneralName must use a context-specific tag");
    }
    if (element.tag.number > 8) {
        fail("unknown GeneralName choice");
    }

    GeneralName result;
    const auto number = static_cast<der::Byte>(element.tag.number);
    auto require_ia5 = [&element]() {
        for (der::Byte octet : element.content) {
            if (octet >= 0x80U) {
                fail("IA5String contains non-ASCII byte");
            }
        }
    };
    switch (number) {
        case 0:
            result.kind = GeneralNameKind::Other;
            if (!element.tag.constructed) fail("otherName must be constructed");
            break;
        case 1:
            result.kind = GeneralNameKind::Rfc822;
            if (element.tag.constructed) fail("rfc822Name must be primitive");
            require_ia5();
            result.text = der::decode_string(element);
            break;
        case 2:
            result.kind = GeneralNameKind::Dns;
            if (element.tag.constructed) fail("dNSName must be primitive");
            require_ia5();
            result.text = der::decode_string(element);
            break;
        case 3:
            result.kind = GeneralNameKind::X400Address;
            if (!element.tag.constructed) fail("x400Address must be constructed");
            break;
        case 4:
            result.kind = GeneralNameKind::Directory;
            if (!element.tag.constructed) fail("directoryName must be constructed");
            (void)der::parse_one(element.content);
            result.directory_name = element.content.to_bytes();
            break;
        case 5:
            result.kind = GeneralNameKind::EdiParty;
            if (!element.tag.constructed) fail("ediPartyName must be constructed");
            break;
        case 6:
            result.kind = GeneralNameKind::Uri;
            if (element.tag.constructed) fail("uniformResourceIdentifier must be primitive");
            require_ia5();
            result.text = der::decode_string(element);
            break;
        case 7:
            result.kind = GeneralNameKind::Ip;
            if (element.tag.constructed) fail("iPAddress must be primitive");
            result.ip = element.content.to_bytes();
            result.ip_address = result.ip;
            break;
        case 8:
            result.kind = GeneralNameKind::RegisteredId;
            if (element.tag.constructed) fail("registeredID must be primitive");
            result.text = der::decode_oid(element.content);
            break;
        default:
            fail("unreachable GeneralName choice");
    }
    return result;
}

std::vector<GeneralName> parse_general_subtrees(const der::Element& container) {
    std::vector<GeneralName> names;
    for (const der::Element& subtree_element : der::parse_children(container)) {
        der::expect_universal(subtree_element, 0x30);
        std::vector<der::Element> parts = der::parse_children(subtree_element);
        if (parts.empty()) {
            fail("empty GeneralSubtree");
        }

        GeneralName base = parse_general_name(parts[0]);
        bool maximum_present = false;
        std::uint64_t minimum = 0;
        std::uint64_t maximum = 0;

        for (std::size_t i = 1; i < parts.size(); ++i) {
            const der::Element& option = parts[i];
            if (option.tag.klass != der::TagClass::Context || option.tag.constructed) {
                fail("invalid GeneralSubtree bounds");
            }
            if (option.tag.number == 0) {
                minimum = decode_context_uint(option);
            } else if (option.tag.number == 1) {
                maximum_present = true;
                maximum = decode_context_uint(option);
            } else {
                fail("unknown GeneralSubtree field");
            }
        }

        if (minimum != 0 || maximum_present || maximum != 0) {
            fail("non-zero GeneralSubtree minimum or encoded maximum is not supported");
        }
        if (base.kind == GeneralNameKind::Ip) {
            if (base.ip.size() != 8 && base.ip.size() != 32) {
                fail("IP name constraint must contain address and mask");
            }
            const std::size_t half = base.ip.size() / 2;
            base.ip_address.assign(base.ip.begin(), base.ip.begin() + static_cast<std::ptrdiff_t>(half));
            base.ip_mask.assign(base.ip.begin() + static_cast<std::ptrdiff_t>(half), base.ip.end());
        }
        names.push_back(std::move(base));
    }
    if (names.empty()) {
        fail("GeneralSubtrees must contain at least one entry");
    }
    return names;
}

bool is_known_critical_extension(const std::string& oid) {
    static const std::array<const char*, 14> known = {{
        OID_BASIC_CONSTRAINTS, OID_KEY_USAGE, OID_SUBJECT_ALT_NAME, OID_ISSUER_ALT_NAME,
        OID_NAME_CONSTRAINTS, OID_CERTIFICATE_POLICIES, OID_POLICY_MAPPINGS,
        OID_POLICY_CONSTRAINTS, OID_INHIBIT_ANY_POLICY, OID_EXTENDED_KEY_USAGE,
        OID_SUBJECT_KEY_IDENTIFIER, OID_AUTHORITY_KEY_IDENTIFIER,
        OID_CRL_DISTRIBUTION_POINTS, OID_AUTHORITY_INFO_ACCESS,
    }};
    for (const char* item : known) {
        if (oid == item) return true;
    }
    return false;
}

void apply_extension(Certificate& certificate, const Extension& extension) {
    const der::Bytes& value = extension.value;

    if (extension.oid == OID_BASIC_CONSTRAINTS) {
        der::Element root = der::parse_one(value);
        der::expect_universal(root, 0x30);
        BasicConstraints constraints;
        for (const der::Element& item : der::parse_children(root)) {
            if (item.tag.number == 0x01 && item.tag.klass == der::TagClass::Universal) {
                constraints.ca = der::decode_boolean(item);
            } else if (item.tag.number == 0x02 && item.tag.klass == der::TagClass::Universal) {
                constraints.path_len_constraint = der::decode_uint(item);
            } else {
                fail("invalid BasicConstraints field");
            }
        }
        certificate.basic_constraints = constraints;
        return;
    }

    if (extension.oid == OID_KEY_USAGE) {
        der::Element root = der::parse_one(value);
        der::BitString bits = der::decode_bit_string(root);
        KeyUsage usage;
        usage.digital_signature = bits.bit(0);
        usage.non_repudiation = bits.bit(1);
        usage.key_encipherment = bits.bit(2);
        usage.data_encipherment = bits.bit(3);
        usage.key_agreement = bits.bit(4);
        usage.key_cert_sign = bits.bit(5);
        usage.crl_sign = bits.bit(6);
        usage.encipher_only = bits.bit(7);
        usage.decipher_only = bits.bit(8);
        certificate.key_usage = usage;
        return;
    }

    if (extension.oid == OID_SUBJECT_ALT_NAME) {
        der::Element root = der::parse_one(value);
        der::expect_universal(root, 0x30);
        for (const der::Element& item : der::parse_children(root)) {
            GeneralName name = parse_general_name(item);
            if (name.kind == GeneralNameKind::Ip && name.ip.size() != 4 && name.ip.size() != 16) {
                fail("subjectAltName iPAddress must be IPv4 or IPv6");
            }
            certificate.subject_alt_names.push_back(std::move(name));
        }
        return;
    }

    if (extension.oid == OID_NAME_CONSTRAINTS) {
        der::Element root = der::parse_one(value);
        der::expect_universal(root, 0x30);
        NameConstraints constraints;
        for (const der::Element& item : der::parse_children(root)) {
            if (item.tag.klass != der::TagClass::Context) {
                fail("invalid NameConstraints field");
            }
            if (item.tag.number == 0) {
                constraints.permitted = parse_general_subtrees(item);
            } else if (item.tag.number == 1) {
                constraints.excluded = parse_general_subtrees(item);
            } else {
                fail("unknown NameConstraints field");
            }
        }
        certificate.name_constraints = std::move(constraints);
        return;
    }

    if (extension.oid == OID_CERTIFICATE_POLICIES) {
        der::Element root = der::parse_one(value);
        der::expect_universal(root, 0x30);
        for (const der::Element& info_element : der::parse_children(root)) {
            der::expect_universal(info_element, 0x30);
            std::vector<der::Element> info = der::parse_children(info_element);
            if (info.empty() || info.size() > 2) {
                fail("invalid PolicyInformation");
            }
            certificate.certificate_policies.push_back(der::decode_oid(info[0]));
        }
        if (certificate.certificate_policies.empty()) {
            fail("CertificatePolicies must contain at least one policy");
        }
        return;
    }

    if (extension.oid == OID_POLICY_MAPPINGS) {
        der::Element root = der::parse_one(value);
        der::expect_universal(root, 0x30);
        for (const der::Element& mapping_element : der::parse_children(root)) {
            der::expect_universal(mapping_element, 0x30);
            std::vector<der::Element> pair = der::parse_children(mapping_element);
            if (pair.size() != 2) {
                fail("invalid PolicyMapping");
            }
            PolicyMapping mapping;
            mapping.issuer_domain_policy = der::decode_oid(pair[0]);
            mapping.subject_domain_policy = der::decode_oid(pair[1]);
            if (mapping.issuer_domain_policy == OID_ANY_POLICY ||
                mapping.subject_domain_policy == OID_ANY_POLICY) {
                fail("policy mappings must not map anyPolicy");
            }
            certificate.policy_mappings.push_back(std::move(mapping));
        }
        return;
    }

    if (extension.oid == OID_POLICY_CONSTRAINTS) {
        der::Element root = der::parse_one(value);
        der::expect_universal(root, 0x30);
        PolicyConstraints constraints;
        bool saw_require = false;
        bool saw_inhibit = false;
        for (const der::Element& item : der::parse_children(root)) {
            if (item.tag.klass != der::TagClass::Context || item.tag.constructed) {
                fail("invalid PolicyConstraints field");
            }
            if (item.tag.number == 0) {
                if (saw_require) fail("duplicate requireExplicitPolicy");
                saw_require = true;
                constraints.require_explicit_policy = decode_context_uint(item);
            } else if (item.tag.number == 1) {
                if (saw_inhibit) fail("duplicate inhibitPolicyMapping");
                saw_inhibit = true;
                constraints.inhibit_policy_mapping = decode_context_uint(item);
            } else {
                fail("unknown PolicyConstraints field");
            }
        }
        if (!constraints.require_explicit_policy.has_value() &&
            !constraints.inhibit_policy_mapping.has_value()) {
            fail("PolicyConstraints must contain requireExplicitPolicy or inhibitPolicyMapping");
        }
        certificate.policy_constraints = constraints;
        return;
    }

    if (extension.oid == OID_INHIBIT_ANY_POLICY) {
        der::Element root = der::parse_one(value);
        certificate.inhibit_any_policy = der::decode_uint(root);
        return;
    }

    if (extension.oid == OID_EXTENDED_KEY_USAGE) {
        der::Element root = der::parse_one(value);
        der::expect_universal(root, 0x30);
        for (const der::Element& oid_element : der::parse_children(root)) {
            certificate.extended_key_usages.push_back(der::decode_oid(oid_element));
        }
        return;
    }
}

SubjectPublicKeyInfo parse_spki(const der::Element& element) {
    der::expect_universal(element, 0x30);
    std::vector<der::Element> children = der::parse_children(element);
    if (children.size() != 2) {
        fail("invalid SubjectPublicKeyInfo");
    }
    SubjectPublicKeyInfo spki;
    spki.raw = element.raw.to_bytes();
    spki.algorithm = parse_algorithm(children[0]);
    der::BitString key = der::decode_bit_string(children[1]);
    if (key.unused_bits != 0) {
        fail("subjectPublicKey BIT STRING must have zero unused bits");
    }
    spki.public_key = key.bytes.to_bytes();
    return spki;
}

} // namespace

ParseResult ParseResult::success(Certificate certificate) {
    ParseResult result;
    result.ok = true;
    result.certificate = std::move(certificate);
    return result;
}

ParseResult ParseResult::failure(std::string error) {
    ParseResult result;
    result.ok = false;
    result.error = std::move(error);
    return result;
}

std::vector<std::string> DistinguishedName::common_names() const {
    std::vector<std::string> names;
    for (const AttributeTypeAndValue& attribute : attributes) {
        if (attribute.oid == OID_COMMON_NAME) {
            names.push_back(attribute.value);
        }
    }
    return names;
}

bool Certificate::is_self_issued() const {
    return !issuer_raw.empty() && issuer_raw == subject_raw;
}

const Extension* Certificate::find_extension(const std::string& oid) const {
    for (const Extension& extension : extensions) {
        if (extension.oid == oid) {
            return &extension;
        }
    }
    return nullptr;
}

std::int64_t unix_seconds_from_date(int year, int month, int day,
                                    int hour, int minute, int second) {
    std::int64_t adjusted_year = year;
    adjusted_year -= month <= 2 ? 1 : 0;
    const std::int64_t era = (adjusted_year >= 0 ? adjusted_year : adjusted_year - 399) / 400;
    const std::int64_t year_of_era = adjusted_year - era * 400;
    const std::int64_t day_of_year =
        (153 * (month + (month > 2 ? -3 : 9)) + 2) / 5 + day - 1;
    const std::int64_t day_of_era =
        year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    const std::int64_t days = era * 146097 + day_of_era - 719468;
    return days * 86400 + hour * 3600 + minute * 60 + second;
}

ParseResult parse_certificate(der::ByteView certificate_der) {
    try {
        Certificate certificate;
        certificate.der = certificate_der.to_bytes();

        der::Element root = der::parse_one(certificate_der);
        der::expect_universal(root, 0x30);
        std::vector<der::Element> top = der::parse_children(root);
        if (top.size() != 3) {
            fail("Certificate must contain tbsCertificate, signatureAlgorithm and signatureValue");
        }

        der::Element tbs_element = top[0];
        der::expect_universal(tbs_element, 0x30);
        certificate.tbs_der = tbs_element.raw.to_bytes();
        certificate.signature_algorithm = parse_algorithm(top[1]);
        certificate.signature_algorithm_raw = top[1].raw.to_bytes();

        der::BitString signature = der::decode_bit_string(top[2]);
        if (signature.unused_bits != 0) {
            fail("certificate signature BIT STRING must have zero unused bits");
        }
        certificate.signature = signature.bytes.to_bytes();

        std::vector<der::Element> tbs = der::parse_children(tbs_element);
        std::size_t index = 0;
        if (index < tbs.size() && tbs[index].tag.first_byte == 0xA0U) {
            std::vector<der::Element> version_children = der::parse_children(tbs[index]);
            if (version_children.size() != 1) {
                fail("invalid version wrapper");
            }
            const std::uint64_t encoded_version = der::decode_uint(version_children[0]);
            if (encoded_version > 2) {
                fail("unsupported X.509 version");
            }
            certificate.version = static_cast<std::uint32_t>(encoded_version + 1);
            ++index;
        }

        auto require_next = [&](const char* name) -> const der::Element& {
            if (index >= tbs.size()) {
                fail(std::string("missing TBS field: ") + name);
            }
            return tbs[index++];
        };

        certificate.serial_number = require_next("serialNumber").content.to_bytes();
        certificate.signature_algorithm = parse_algorithm(require_next("signature"));
        certificate.issuer = parse_name(require_next("issuer"));
        certificate.issuer_raw = certificate.issuer.raw;

        const der::Element& validity_element = require_next("validity");
        der::expect_universal(validity_element, 0x30);
        std::vector<der::Element> validity = der::parse_children(validity_element);
        if (validity.size() != 2) {
            fail("Validity must contain notBefore and notAfter");
        }
        certificate.not_before = parse_time(validity[0]);
        certificate.not_after = parse_time(validity[1]);

        certificate.subject = parse_name(require_next("subject"));
        certificate.subject_raw = certificate.subject.raw;
        certificate.subject_public_key_info = parse_spki(require_next("subjectPublicKeyInfo"));

        bool saw_issuer_uid = false;
        bool saw_subject_uid = false;
        bool saw_extensions = false;
        while (index < tbs.size()) {
            const der::Element& optional = tbs[index++];
            if (optional.tag.klass != der::TagClass::Context) {
                fail("invalid optional TBS field tag class");
            }
            if (optional.tag.number == 1 && !optional.tag.constructed) {
                if (saw_issuer_uid) fail("duplicate issuerUniqueID");
                saw_issuer_uid = true;
                if (certificate.version < 2) fail("issuerUniqueID requires v2 or v3");
            } else if (optional.tag.number == 2 && !optional.tag.constructed) {
                if (saw_subject_uid) fail("duplicate subjectUniqueID");
                saw_subject_uid = true;
                if (certificate.version < 2) fail("subjectUniqueID requires v2 or v3");
            } else if (optional.tag.number == 3 && optional.tag.constructed) {
                if (saw_extensions) fail("duplicate extensions");
                saw_extensions = true;
                if (certificate.version != 3) fail("extensions require X.509 v3");

                der::Element extensions_wrapper = der::parse_one(optional.content);
                der::expect_universal(extensions_wrapper, 0x30);
                for (const der::Element& extension_element : der::parse_children(extensions_wrapper)) {
                    der::expect_universal(extension_element, 0x30);
                    std::vector<der::Element> fields = der::parse_children(extension_element);
                    if (fields.size() < 2 || fields.size() > 3) {
                        fail("invalid Extension");
                    }

                    Extension extension;
                    extension.oid = der::decode_oid(fields[0]);
                    std::size_t value_index = 1;
                    if (fields.size() == 3) {
                        extension.critical = der::decode_boolean(fields[1]);
                        value_index = 2;
                    }
                    extension.value = der::decode_octet_string(fields[value_index]);
                    extension.raw = extension_element.raw.to_bytes();

                    for (const Extension& existing : certificate.extensions) {
                        if (existing.oid == extension.oid) {
                            fail("duplicate certificate extension");
                        }
                    }
                    if (extension.critical && !is_known_critical_extension(extension.oid)) {
                        certificate.unknown_critical_extensions.push_back(extension.oid);
                    }
                    certificate.extensions.push_back(std::move(extension));
                }
            } else {
                fail("unknown or non-canonical optional TBS field");
            }
        }

        if (certificate.signature_algorithm_raw != certificate.signature_algorithm.raw) {
            fail("Certificate signatureAlgorithm does not match TBS signature algorithm");
        }

        for (const Extension& extension : certificate.extensions) {
            try {
                apply_extension(certificate, extension);
            } catch (const std::exception& error) {
                fail("extension " + extension.oid + ": " + error.what());
            }
        }

        return ParseResult::success(std::move(certificate));
    } catch (const std::exception& exception) {
        return ParseResult::failure(exception.what());
    }
}

} // namespace pki::x509
