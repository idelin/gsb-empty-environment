#include "netsec.hpp"

#include <algorithm>
#include <cstdint>
#include <iostream>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, const char* expression, int line) {
  if (!condition) {
    std::cerr << "FAIL line " << line << ": " << expression << '\n';
    ++failures;
  }
}

#define CHECK(expression) check((expression), #expression, __LINE__)

template <typename Fn>
void expect_der_error(netsec::DerErrorCode expected, Fn&& fn,
                      const char* label) {
  try {
    fn();
    std::cerr << "FAIL " << label << ": no DerError was thrown\n";
    ++failures;
  } catch (const netsec::DerError& error) {
    if (error.code() != expected) {
      std::cerr << "FAIL " << label << ": expected "
                << netsec::to_string(expected) << ", got "
                << netsec::to_string(error.code()) << " (" << error.what()
                << ")\n";
      ++failures;
    }
  }
}

std::uint8_t hex_nibble(char c) {
  if (c >= '0' && c <= '9') return static_cast<std::uint8_t>(c - '0');
  if (c >= 'a' && c <= 'f') return static_cast<std::uint8_t>(c - 'a' + 10);
  if (c >= 'A' && c <= 'F') return static_cast<std::uint8_t>(c - 'A' + 10);
  return 0xFF;
}

netsec::Bytes from_hex(std::string_view text) {
  netsec::Bytes out;
  int high = -1;
  for (char c : text) {
    const std::uint8_t nibble = hex_nibble(c);
    if (nibble == 0xFF) continue;
    if (high < 0) {
      high = nibble;
    } else {
      out.push_back(static_cast<std::uint8_t>((high << 4) | nibble));
      high = -1;
    }
  }
  return out;
}

netsec::Bytes to_bytes(netsec::ByteView view) {
  return netsec::Bytes(view.begin(), view.end());
}

void append(netsec::Bytes& target, netsec::ByteView source) {
  target.insert(target.end(), source.begin(), source.end());
}

netsec::Bytes encode_length(std::size_t length) {
  netsec::Bytes out;
  if (length < 0x80) {
    out.push_back(static_cast<std::uint8_t>(length));
    return out;
  }
  std::uint8_t bytes[sizeof(std::size_t)];
  std::size_t count = 0;
  while (length != 0) {
    bytes[count++] = static_cast<std::uint8_t>(length & 0xFF);
    length >>= 8;
  }
  out.push_back(static_cast<std::uint8_t>(0x80 | count));
  while (count != 0) {
    out.push_back(bytes[--count]);
  }
  return out;
}

netsec::Bytes tlv(std::uint8_t identifier, netsec::ByteView content) {
  netsec::Bytes out;
  out.push_back(identifier);
  const netsec::Bytes length = encode_length(content.size());
  out.insert(out.end(), length.begin(), length.end());
  append(out, content);
  return out;
}

struct MNode {
  std::uint8_t tag_class = 0;
  bool constructed = false;
  std::uint32_t number = 0;
  netsec::Bytes content;
  std::vector<MNode> children;

  netsec::Bytes encode() const {
    netsec::Bytes identifier;
    const std::uint8_t first = static_cast<std::uint8_t>(
        (tag_class << 6) | (constructed ? 0x20 : 0) |
        (number < 31 ? number : 31));
    identifier.push_back(first);
    if (number >= 31) {
      std::uint32_t value = number;
      std::uint8_t groups[5];
      std::size_t count = 0;
      do {
        groups[count++] = static_cast<std::uint8_t>(value & 0x7F);
        value >>= 7;
      } while (value != 0);
      while (count != 0) {
        std::uint8_t b = groups[--count];
        if (count != 0) b |= 0x80;
        identifier.push_back(b);
      }
    }
    netsec::Bytes body = content;
    if (constructed) {
      body.clear();
      for (const MNode& child : children) {
        const netsec::Bytes encoded = child.encode();
        body.insert(body.end(), encoded.begin(), encoded.end());
      }
    }
    const netsec::Bytes length = encode_length(body.size());
    identifier.insert(identifier.end(), length.begin(), length.end());
    identifier.insert(identifier.end(), body.begin(), body.end());
    return identifier;
  }
};

MNode copy_node(const netsec::DerNode& node) {
  MNode result;
  result.tag_class = node.tag.tag_class;
  result.constructed = node.tag.constructed;
  result.number = node.tag.number;
  if (!node.tag.constructed) {
    result.content = to_bytes(node.content);
  }
  for (const netsec::DerNode& child : node.children) {
    result.children.push_back(copy_node(child));
  }
  return result;
}

void replace_same_length(netsec::Bytes& bytes, std::string_view from,
                         std::string_view to) {
  if (from.size() != to.size()) return;
  for (std::size_t i = 0; i + from.size() <= bytes.size(); ++i) {
    if (std::equal(from.begin(), from.end(), bytes.begin() + i)) {
      std::copy(to.begin(), to.end(), bytes.begin() + i);
    }
  }
}

const char* kCertificateHex =
    "308203923082027aa00302010202090090b81eb5758e3680300d06092a864886f70d01010b0500303e31153013060355"
    "04030c0c676f6f642e6578616d706c6531183016060355040a0c0f537472696374204445522054657374310b30090603"
    "55040613025553301e170d3236313131343039303035355a170d3336313131313039303035355a303e31153013060355"
    "04030c0c676f6f642e6578616d706c6531183016060355040a0c0f537472696374204445522054657374310b30090603"
    "5504061302555330820122300d06092a864886f70d01010105000382010f003082010a0282010100c81fd79dae8847f0"
    "3d75285866afa168ed044e417910e63fa8df83033c312b475ef36a3e6e3cf34bdc12bd65af194376e0bbe0b7128ddc38"
    "10920631ce8e5c19c3b3378eb770755d8dd5aa51b4d3c78c59b6226ba01860812a53a8946fc427917d811bacead2cd15"
    "63a7f7758d4ec2b5f17ec007207688b1056d7a8140f8685a2c6ae23b5f8d5e966a44979a324c3c4adcbeead6b9d339ee"
    "09dcdd99693578847c671d658a506c006a9ebe7d6de639d3c5d648abae776d2300c65093bcf13ca02e24c219ce39f496"
    "e704655ce95c0d64c43fb5ae174470be758b465a31d21f65d38d98fd96392a0c6641cd03322d7b766dcaa334551235cd"
    "65eadb4e2ab78f3f0203010001a3819230818f302f0603551d1104283026820c676f6f642e6578616d706c6582107777"
    "772e676f6f642e6578616d706c658704c000020a300c0603551d130101ff04023000300e0603551d0f0101ff04040302"
    "05a0301d0603551d0e04160414db2eeda23d1e2a92cac82826f2f752a168463c3e301f0603551d23041830168014db2e"
    "eda23d1e2a92cac82826f2f752a168463c3e300d06092a864886f70d01010b050003820101001c6116e1dd8ff0049dc9"
    "e0f3a3a99060ebe2aa258b190ae3b5c95acb733c9fa374078c77e4ad422db8c6cf04b03cb667a9d889dfbcb392f26b99"
    "120a384c504df1f3c36fc1855d43b1fa2083e0f9294a35d0a45efa4b41dc98d5e5d3276fcb4a22263724db0f3ddde4bd"
    "546c84e24d6def4aac1cff53f9594e4ea4654cc15425df4ace5b91f05f01229f1e5105172e774a89a62967f04ea0a86a"
    "4892a18eab907f8590706ecdfa367632f59181779a96dbf1c4163a5b62cfa58ae3e9897a792c11aa8ae477fdd14d2e75"
    "47891d7bd1c69130f2dca62d0fd52d670f015a4c15854e8468c214f47bad8c9ebfe916863180405b90cbead3436314cb"
    "76870eb4dbe7";

class RecordingVerifier final : public netsec::SignatureVerifier {
 public:
  bool verify(const netsec::SignatureRequest& request) override {
    called = true;
    algorithm = std::string(request.algorithm_oid);
    parameters = to_bytes(request.algorithm_parameters);
    tbs = to_bytes(request.tbs);
    tbs_pointer = request.tbs.data();
    public_key_info = to_bytes(request.public_key_info);
    signature = to_bytes(request.signature);
    return true;
  }

  bool called = false;
  std::string algorithm;
  netsec::Bytes parameters;
  netsec::Bytes tbs;
  const std::uint8_t* tbs_pointer = nullptr;
  netsec::Bytes public_key_info;
  netsec::Bytes signature;
};

netsec::CertificateIdentity validator_view(const netsec::Certificate& cert) {
  netsec::CertificateIdentity view;
  view.subject = cert.subject().canonical();
  for (const netsec::GeneralName& name : cert.subject_alt_names()) {
    view.subject_alt_names.push_back(std::string(netsec::to_string(name.kind)) +
                                     ":" + name.value);
  }
  view.basic_constraints_ca = cert.basic_constraints().ca;
  view.path_length = cert.basic_constraints().path_length;
  view.key_usage_bits = cert.key_usage().bits;
  return view;
}

void test_strict_der_primitive_rules() {
  using netsec::DerErrorCode;
  expect_der_error(
      DerErrorCode::indefinite_length,
      [] { netsec::StrictDer::parse(from_hex("30800000")); },
      "indefinite length");
  expect_der_error(
      DerErrorCode::non_minimal_length,
      [] { netsec::StrictDer::parse(from_hex("04810100")); },
      "long form for short length");
  expect_der_error(
      DerErrorCode::non_minimal_length,
      [] { netsec::StrictDer::parse(from_hex("3083000000")); },
      "leading zero length");
  expect_der_error(
      DerErrorCode::trailing_data,
      [] { netsec::StrictDer::parse(from_hex("050000")); },
      "trailing byte");
  expect_der_error(
      DerErrorCode::unexpected_end_of_contents,
      [] { netsec::StrictDer::parse(from_hex("0000")); },
      "end of contents");
  expect_der_error(
      DerErrorCode::non_canonical_tag,
      [] { netsec::StrictDer::parse(from_hex("3f1000")); },
      "high-tag form for SEQUENCE");
  expect_der_error(
      DerErrorCode::non_canonical_tag,
      [] { netsec::StrictDer::parse(from_hex("1000")); },
      "primitive SEQUENCE");
  expect_der_error(
      DerErrorCode::non_canonical_tag,
      [] { netsec::StrictDer::parse(from_hex("24020400")); },
      "constructed OCTET STRING");
  expect_der_error(
      DerErrorCode::non_canonical_tag,
      [] { netsec::StrictDer::parse(from_hex("02020001")); },
      "INTEGER leading zero");
  expect_der_error(
      DerErrorCode::non_canonical_tag,
      [] { netsec::StrictDer::parse(from_hex("06028000")); },
      "OID leading zero group");
  expect_der_error(
      DerErrorCode::unsorted_set,
      [] { netsec::StrictDer::parse(from_hex("3106020102020101")); },
      "unsorted SET OF");
}

void test_indefinite_certificate_variants(const netsec::Bytes& valid_der,
                                          const netsec::Certificate& cert) {
  using netsec::DerErrorCode;
  netsec::Bytes outer_indefinite{0x30, 0x80};
  append(outer_indefinite, cert.root().content);
  outer_indefinite.push_back(0x00);
  outer_indefinite.push_back(0x00);
  expect_der_error(
      DerErrorCode::indefinite_length,
      [&] { netsec::Certificate::parse(outer_indefinite); },
      "indefinite outer certificate");

  netsec::Bytes nested_content;
  nested_content.push_back(0x30);
  nested_content.push_back(0x80);
  append(nested_content, cert.tbs_node().content);
  nested_content.push_back(0x00);
  nested_content.push_back(0x00);
  append(nested_content, cert.root().children[1].encoded);
  append(nested_content, cert.root().children[2].encoded);
  const netsec::Bytes nested_indefinite = tlv(0x30, nested_content);
  expect_der_error(
      DerErrorCode::indefinite_length,
      [&] { netsec::Certificate::parse(nested_indefinite); },
      "indefinite TBS inside certificate");

  netsec::Bytes leading_zero_length;
  leading_zero_length.push_back(0x30);
  leading_zero_length.push_back(0x83);
  leading_zero_length.push_back(0x00);
  leading_zero_length.push_back(
      static_cast<std::uint8_t>((cert.root().content.size() >> 8) & 0xFF));
  leading_zero_length.push_back(
      static_cast<std::uint8_t>(cert.root().content.size() & 0xFF));
  append(leading_zero_length, cert.root().content);
  expect_der_error(
      DerErrorCode::non_minimal_length,
      [&] { netsec::Certificate::parse(leading_zero_length); },
      "certificate with leading zero length");

  netsec::Bytes trailing = valid_der;
  trailing.push_back(0x00);
  trailing.push_back(0x00);
  expect_der_error(
      DerErrorCode::trailing_data,
      [&] { netsec::Certificate::parse(trailing); },
      "certificate trailing data");

  netsec::Bytes ambiguous = outer_indefinite;
  append(ambiguous, valid_der);
  expect_der_error(
      DerErrorCode::indefinite_length,
      [&] { netsec::Certificate::parse(ambiguous); },
      "two interpretations / appended certificate");
}

void test_certificate_field_canonicality(const netsec::Certificate& cert) {
  using netsec::DerErrorCode;

  MNode root = copy_node(cert.root());
  MNode& version_integer = root.children[0].children[0].children[0];
  version_integer.content = {0x00, 0x02};
  const netsec::Bytes bad_version = root.encode();
  expect_der_error(
      DerErrorCode::non_canonical_tag,
      [&] { netsec::Certificate::parse(bad_version); },
      "version INTEGER with redundant leading zero");

  MNode duplicate_root = copy_node(cert.root());
  MNode& extensions_wrapper = duplicate_root.children[0].children.back();
  MNode& extensions = extensions_wrapper.children[0];
  MNode& san = extensions.children[0];
  CHECK(san.children.back().number == 4);
  MNode duplicate = san;
  replace_same_length(duplicate.children.back().content, "good.example",
                      "evil.example");
  extensions.children.push_back(std::move(duplicate));
  const netsec::Bytes duplicate_extension = duplicate_root.encode();
  expect_der_error(
      DerErrorCode::duplicate_extension,
      [&] { netsec::Certificate::parse(duplicate_extension); },
      "duplicate SAN extension with two identities");
}

void test_legitimate_certificate() {
  const netsec::Bytes der = from_hex(kCertificateHex);
  const netsec::Certificate cert = netsec::Certificate::parse(der);

  CHECK(cert.version() == 2);
  CHECK(cert.serial_number() == "90b81eb5758e3680");
  CHECK(cert.subject().text() == "CN=good.example,O=Strict DER Test,C=US");
  CHECK(cert.issuer().text() == "CN=good.example,O=Strict DER Test,C=US");
  CHECK(cert.subject().der.data() == cert.tbs_node().children[5].encoded.data());

  CHECK(cert.subject_alt_names().size() == 3);
  CHECK(cert.subject_alt_names()[0].kind == netsec::GeneralNameKind::dns_name);
  CHECK(cert.subject_alt_names()[0].value == "good.example");
  CHECK(cert.subject_alt_names()[1].value == "www.good.example");
  CHECK(cert.subject_alt_names()[2].kind ==
        netsec::GeneralNameKind::ip_address);
  CHECK(cert.subject_alt_names()[2].value == "192.0.2.10");

  CHECK(cert.basic_constraints().present);
  CHECK(!cert.basic_constraints().ca);
  CHECK(!cert.basic_constraints().path_length.has_value());
  CHECK(cert.key_usage().present);
  CHECK(cert.key_usage().bits == 5);
  CHECK(cert.key_usage().has(0));
  CHECK(cert.key_usage().has(2));
  CHECK(!cert.key_usage().has(1));
  CHECK(!cert.has_unsupported_critical_extensions());

  const netsec::Extension* san_extension = cert.find_extension("2.5.29.17");
  const netsec::Extension* bc_extension = cert.find_extension("2.5.29.19");
  const netsec::Extension* ku_extension = cert.find_extension("2.5.29.15");
  CHECK(san_extension != nullptr && san_extension->decoded.has_value());
  CHECK(bc_extension != nullptr && bc_extension->decoded.has_value());
  CHECK(ku_extension != nullptr && ku_extension->decoded.has_value());
  CHECK(san_extension->decoded->encoded.data() == san_extension->value.data());
  CHECK(bc_extension->decoded->encoded.data() == bc_extension->value.data());
  CHECK(ku_extension->decoded->encoded.data() == ku_extension->value.data());

  CHECK(cert.signature_algorithm_oid() == "1.2.840.113549.1.1.11");
  CHECK(cert.tbs_signature_algorithm_oid() == cert.signature_algorithm_oid());
  CHECK(cert.verify_signature());
  CHECK(cert.verify_signature_with_key(cert.subject_public_key_info()));

  // Both the business snapshot and a validator-side view are constructed from
  // the same certificate object, which owns exactly one parsed DER tree.
  const netsec::CertificateIdentity business = cert.identity();
  const netsec::CertificateIdentity validator = validator_view(cert);
  CHECK(business.canonical() == validator.canonical());
  CHECK(business.subject == cert.subject().canonical());
  CHECK(business.subject_alt_names.size() == 3);

  RecordingVerifier recorder;
  CHECK(cert.verify_signature(recorder));
  CHECK(recorder.called);
  CHECK(recorder.tbs == to_bytes(cert.tbs_der()));
  CHECK(recorder.tbs == to_bytes(cert.root().children[0].encoded));
  CHECK(recorder.public_key_info ==
        to_bytes(cert.tbs_node().children[6].encoded));
  CHECK(recorder.signature == to_bytes(cert.signature()));
  CHECK(recorder.algorithm == cert.signature_algorithm_oid());
  CHECK(recorder.tbs_pointer == cert.tbs_der().data());

  // A byte change in the TBS must invalidate the signature even though the
  // modified certificate is still valid DER and still parses.
  netsec::Bytes tampered = der;
  std::size_t occurrences = 0;
  for (std::size_t i = 0; i + 15 <= tampered.size(); ++i) {
    if (std::equal(reinterpret_cast<const unsigned char*>("Strict DER Test"),
                   reinterpret_cast<const unsigned char*>("Strict DER Test") +
                       15,
                   tampered.begin() + i)) {
      ++occurrences;
      if (occurrences == 2) {
        tampered[i] = 'X';
        break;
      }
    }
  }
  CHECK(occurrences == 2);
  const netsec::Certificate changed = netsec::Certificate::parse(tampered);
  CHECK(changed.subject().text() != cert.subject().text());
  CHECK(changed.identity().canonical() != cert.identity().canonical());
  CHECK(!changed.verify_signature());
}

}  // namespace

int main() {
  test_strict_der_primitive_rules();

  const netsec::Bytes der = from_hex(kCertificateHex);
  const netsec::Certificate cert = netsec::Certificate::parse(der);
  test_indefinite_certificate_variants(der, cert);
  test_certificate_field_canonicality(cert);
  test_legitimate_certificate();

  if (failures != 0) {
    std::cerr << failures << " test(s) failed\n";
    return 1;
  }
  std::cout << "all tests passed\n";
  return 0;
}
