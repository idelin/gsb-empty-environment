#include "netsec.hpp"

#include <cstdio>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <string>
#include <vector>

namespace {

using netsec::Bytes;

Bytes concat(const std::vector<Bytes> &values) {
  Bytes out;
  for (const Bytes &value : values) {
    out.insert(out.end(), value.begin(), value.end());
  }
  return out;
}

Bytes encode_length(std::size_t length) {
  Bytes out;
  if (length < 0x80U) {
    out.push_back(static_cast<std::uint8_t>(length));
    return out;
  }
  Bytes tmp;
  while (length != 0) {
    tmp.push_back(static_cast<std::uint8_t>(length & 0xFFU));
    length >>= 8U;
  }
  out.push_back(static_cast<std::uint8_t>(0x80U | tmp.size()));
  out.insert(out.end(), tmp.rbegin(), tmp.rend());
  return out;
}

Bytes tlv(std::uint8_t tag, const Bytes &content) {
  Bytes out;
  out.push_back(tag);
  Bytes length = encode_length(content.size());
  out.insert(out.end(), length.begin(), length.end());
  out.insert(out.end(), content.begin(), content.end());
  return out;
}

Bytes sequence(const std::vector<Bytes> &parts) {
  return tlv(0x30U, concat(parts));
}

Bytes null_value() { return Bytes{0x05U, 0x00U}; }

Bytes boolean_value(bool value) {
  return tlv(0x01U, Bytes{static_cast<std::uint8_t>(value ? 0xFFU : 0x00U)});
}

Bytes integer_value(std::uint64_t value) {
  Bytes content;
  do {
    content.push_back(static_cast<std::uint8_t>(value & 0xFFU));
    value >>= 8U;
  } while (value != 0);
  if ((content.back() & 0x80U) != 0) {
    content.push_back(0);
  }
  return tlv(0x02U, Bytes(content.rbegin(), content.rend()));
}

Bytes integer_bytes(Bytes magnitude) {
  std::size_t first = 0;
  while (first + 1 < magnitude.size() && magnitude[first] == 0) {
    ++first;
  }
  magnitude.erase(magnitude.begin(),
                  magnitude.begin() + static_cast<std::ptrdiff_t>(first));
  if (magnitude.empty()) {
    magnitude.push_back(0);
  }
  if ((magnitude.front() & 0x80U) != 0) {
    magnitude.insert(magnitude.begin(), 0);
  }
  return tlv(0x02U, magnitude);
}

Bytes oid_value(const std::string &dotted) {
  std::vector<std::uint64_t> arcs;
  std::size_t start = 0;
  while (start < dotted.size()) {
    std::size_t end = dotted.find('.', start);
    if (end == std::string::npos) {
      end = dotted.size();
    }
    arcs.push_back(static_cast<std::uint64_t>(
        std::stoull(dotted.substr(start, end - start))));
    start = end + 1;
  }
  Bytes content;
  auto emit = [&](std::uint64_t value) {
    Bytes tmp;
    do {
      tmp.push_back(static_cast<std::uint8_t>(value & 0x7FU));
      value >>= 7U;
    } while (value != 0);
    for (std::size_t i = tmp.size(); i > 0; --i) {
      std::uint8_t byte = tmp[i - 1];
      if (i != 1) {
        byte |= 0x80U;
      }
      content.push_back(byte);
    }
  };
  if (arcs.size() < 2) {
    std::abort();
  }
  emit(arcs[0] * 40 + arcs[1]);
  for (std::size_t i = 2; i < arcs.size(); ++i) {
    emit(arcs[i]);
  }
  return tlv(0x06U, content);
}

Bytes octet_value(const Bytes &value) { return tlv(0x04U, value); }

Bytes bit_string(const Bytes &value) {
  Bytes content{0};
  content.insert(content.end(), value.begin(), value.end());
  return tlv(0x03U, content);
}

Bytes utc_time(const std::string &text) {
  return tlv(0x17U, Bytes(text.begin(), text.end()));
}

Bytes context(std::uint32_t tag, const Bytes &content, bool constructed) {
  return tlv(static_cast<std::uint8_t>((constructed ? 0xA0U : 0x80U) | tag),
             content);
}

Bytes algorithm_identifier() {
  return sequence({oid_value("1.2.840.113549.1.1.11"), null_value()});
}

Bytes extension(const std::string &oid, bool critical, const Bytes &value) {
  std::vector<Bytes> parts{oid_value(oid)};
  if (critical) {
    parts.push_back(boolean_value(true));
  }
  parts.push_back(octet_value(value));
  return sequence(parts);
}

Bytes uri_general_name(const std::string &uri) {
  return context(6, Bytes(uri.begin(), uri.end()), false);
}

Bytes directory_general_name(const netsec::DistinguishedName &name) {
  return context(4, name.der, true);
}

Bytes idp(const std::string &uri, bool only_user, bool only_ca, bool indirect) {
  std::vector<Bytes> parts;
  parts.push_back(context(0, context(0, uri_general_name(uri), true), true));
  if (only_user) {
    parts.push_back(context(1, Bytes{0xFFU}, false));
  }
  if (only_ca) {
    parts.push_back(context(2, Bytes{0xFFU}, false));
  }
  if (indirect) {
    parts.push_back(context(4, Bytes{0xFFU}, false));
  }
  return sequence(parts);
}

Bytes aki_extension_value(const Bytes &key_identifier);
bool write_file(const std::filesystem::path &path, const Bytes &data);
Bytes read_file(const std::filesystem::path &path);

Bytes crldp_extension(
    const std::string &uri,
    const std::optional<netsec::DistinguishedName> &crl_issuer) {
  std::vector<Bytes> dp_parts;
  dp_parts.push_back(context(0, context(0, uri_general_name(uri), true), true));
  if (crl_issuer.has_value()) {
    dp_parts.push_back(context(2, directory_general_name(*crl_issuer), true));
  }
  return extension("2.5.29.31", false, sequence({sequence(dp_parts)}));
}

Bytes build_certificate(
    const netsec::CertificateInfo &issuer,
    const netsec::CertificateInfo &template_cert,
    const std::filesystem::path &key_path, const std::filesystem::path &tmp_dir,
    const std::string &uri,
    const std::optional<netsec::DistinguishedName> &crl_issuer) {
  if (template_cert.subject_public_key_info.empty()) {
    std::abort();
  }

  std::vector<Bytes> extensions;
  extensions.push_back(
      extension("2.5.29.19", true, sequence({boolean_value(false)})));
  extensions.push_back(
      extension("2.5.29.15", true, tlv(0x03U, Bytes{0x01U, 0xA0U})));
  extensions.push_back(extension(
      "2.5.29.14", false, octet_value(template_cert.subject_key_identifier)));
  extensions.push_back(extension(
      "2.5.29.35", false, aki_extension_value(issuer.subject_key_identifier)));
  extensions.push_back(crldp_extension(uri, crl_issuer));

  std::vector<Bytes> tbs_parts;
  tbs_parts.push_back(context(0, integer_value(2), true));
  tbs_parts.push_back(integer_bytes(template_cert.serial_number));
  tbs_parts.push_back(algorithm_identifier());
  tbs_parts.push_back(issuer.subject.der);
  tbs_parts.push_back(
      sequence({utc_time("261114000000Z"), utc_time("361111000000Z")}));
  tbs_parts.push_back(template_cert.subject.der);
  tbs_parts.push_back(template_cert.subject_public_key_info);
  tbs_parts.push_back(context(3, sequence(extensions), true));
  Bytes tbs = sequence(tbs_parts);

  std::filesystem::create_directories(tmp_dir);
  std::filesystem::path tbs_path = tmp_dir / "cert_tbs.der";
  std::filesystem::path signature_path = tmp_dir / "cert_signature.der";
  if (!write_file(tbs_path, tbs)) {
    std::abort();
  }
  std::string command = "openssl dgst -sha256 -sign \"" + key_path.string() +
                        "\" -out \"" + signature_path.string() + "\" \"" +
                        tbs_path.string() + "\"";
  if (std::system(command.c_str()) != 0) {
    std::abort();
  }
  Bytes signature = read_file(signature_path);
  return sequence({tbs, algorithm_identifier(), bit_string(signature)});
}

Bytes aki_extension_value(const Bytes &key_identifier) {
  return sequence({context(0, key_identifier, false)});
}

Bytes crl_number_extension(std::uint64_t number) {
  return extension("2.5.29.20", false, integer_value(number));
}

Bytes delta_indicator_extension(std::uint64_t base_number) {
  return extension("2.5.29.27", true, integer_value(base_number));
}

Bytes idp_extension(const std::string &uri, bool only_user, bool only_ca,
                    bool indirect) {
  return extension("2.5.29.28", true, idp(uri, only_user, only_ca, indirect));
}

Bytes serial_from_file(const std::filesystem::path &path) {
  std::ifstream input(path, std::ios::binary);
  return Bytes(std::istreambuf_iterator<char>(input),
               std::istreambuf_iterator<char>());
}

bool write_file(const std::filesystem::path &path, const Bytes &data) {
  std::ofstream output(path, std::ios::binary);
  output.write(reinterpret_cast<const char *>(data.data()),
               static_cast<std::streamsize>(data.size()));
  return static_cast<bool>(output);
}

Bytes read_file(const std::filesystem::path &path) {
  std::ifstream input(path, std::ios::binary);
  return Bytes(std::istreambuf_iterator<char>(input),
               std::istreambuf_iterator<char>());
}

struct Options {
  std::string uri = "http://ca1.example/crl/leaf.crl";
  bool only_user = true;
  bool only_ca = false;
  bool indirect = false;
  std::optional<std::uint64_t> crl_number = 10;
  std::optional<std::uint64_t> base_number;
  std::string this_update = "261114000000Z";
  std::string next_update = "271114000000Z";
  bool revoke_leaf = false;
  bool remove_leaf = false;
  int reason_code = 1;
  bool expired = false;
  bool unknown_critical = false;
  bool bad_signature = false;
  std::optional<netsec::DistinguishedName> entry_issuer;
};

Bytes build_crl(const netsec::CertificateInfo &signer,
                const netsec::CertificateInfo &leaf,
                const std::filesystem::path &key_path,
                const std::filesystem::path &tmp_dir, const Options &options) {
  std::vector<Bytes> crl_extensions;
  crl_extensions.push_back(extension(
      "2.5.29.35", false, aki_extension_value(signer.subject_key_identifier)));
  if (options.crl_number.has_value()) {
    crl_extensions.push_back(crl_number_extension(*options.crl_number));
  }
  if (options.base_number.has_value()) {
    crl_extensions.push_back(delta_indicator_extension(*options.base_number));
  }
  crl_extensions.push_back(idp_extension(options.uri, options.only_user,
                                         options.only_ca, options.indirect));
  if (options.unknown_critical) {
    crl_extensions.push_back(extension("1.2.3.4.5", true, null_value()));
  }

  std::vector<Bytes> tbs_parts;
  tbs_parts.push_back(integer_value(1)); // v2
  tbs_parts.push_back(algorithm_identifier());
  tbs_parts.push_back(signer.subject.der);
  tbs_parts.push_back(utc_time(options.this_update));
  tbs_parts.push_back(
      utc_time(options.expired ? "240101000000Z" : options.next_update));

  if (options.revoke_leaf || options.remove_leaf) {
    std::vector<Bytes> entry_parts;
    entry_parts.push_back(integer_bytes(leaf.serial_number));
    entry_parts.push_back(utc_time("261114010000Z"));
    std::vector<Bytes> entry_extensions;
    if (options.remove_leaf) {
      entry_extensions.push_back(
          extension("2.5.29.21", false, tlv(0x0AU, Bytes{8U})));
    } else {
      entry_extensions.push_back(extension(
          "2.5.29.21", false,
          tlv(0x0AU, Bytes{static_cast<std::uint8_t>(options.reason_code)})));
    }
    if (options.entry_issuer.has_value()) {
      entry_extensions.push_back(
          extension("2.5.29.29", true,
                    sequence({directory_general_name(*options.entry_issuer)})));
    }
    if (!entry_extensions.empty()) {
      entry_parts.push_back(sequence(entry_extensions));
    }
    tbs_parts.push_back(sequence({sequence(entry_parts)}));
  }

  tbs_parts.push_back(context(0, sequence(crl_extensions), true));
  Bytes tbs = sequence(tbs_parts);

  std::filesystem::create_directories(tmp_dir);
  std::filesystem::path tbs_path = tmp_dir / "tbs.der";
  std::filesystem::path signature_path = tmp_dir / "signature.der";
  if (!write_file(tbs_path, tbs)) {
    std::abort();
  }
  std::string command = "openssl dgst -sha256 -sign \"" + key_path.string() +
                        "\" -out \"" + signature_path.string() + "\" \"" +
                        tbs_path.string() + "\"";
  if (std::system(command.c_str()) != 0) {
    std::abort();
  }
  Bytes signature = read_file(signature_path);
  if (options.bad_signature && !signature.empty()) {
    signature[signature.size() / 2] ^= 0x5AU;
  }

  return sequence({tbs, algorithm_identifier(), bit_string(signature)});
}

} // namespace

int main() {
  namespace fs = std::filesystem;
  fs::path fixture = "tests/fixtures";
  fs::path tmp = std::getenv("TMPDIR") == nullptr
                     ? fs::path("tests/fixtures/tmp")
                     : fs::path(std::getenv("TMPDIR")) / "netsec-fixtures";

  auto ca1 = netsec::parse_certificate(serial_from_file(fixture / "ca1.der"));
  auto ca2 = netsec::parse_certificate(serial_from_file(fixture / "ca2.der"));
  auto leaf = netsec::parse_certificate(serial_from_file(fixture / "leaf.der"));
  auto delegate =
      netsec::parse_certificate(serial_from_file(fixture / "delegate.der"));
  if (!ca1.has_value() || !ca2.has_value() || !leaf.has_value() ||
      !delegate.has_value()) {
    std::cerr << "failed to parse fixture certificates\n";
    return 1;
  }

  Bytes delegated_leaf_der =
      build_certificate(*ca1, *leaf, fixture / "ca1.key", tmp,
                        "http://ca1.example/crl/leaf.crl", delegate->subject);
  if (!write_file(fixture / "leaf_delegated.der", delegated_leaf_der)) {
    std::cerr << "failed to write delegated leaf certificate\n";
    return 1;
  }
  auto delegated_leaf = netsec::parse_certificate(delegated_leaf_der);
  if (!delegated_leaf.has_value()) {
    std::cerr << "delegated leaf certificate did not parse\n";
    return 1;
  }

  struct Fixture {
    std::string name;
    Options options;
    const netsec::CertificateInfo *signer;
    std::string key;
  };

  std::vector<Fixture> fixtures;
  {
    Options options;
    options.crl_number = 10;
    options.revoke_leaf = true;
    fixtures.push_back(
        {"crl_ca1_scope_revoke.der", options, &*ca1, "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 11;
    options.revoke_leaf = true;
    fixtures.push_back(
        {"crl_other_ca.der", options, &*ca2, "tests/fixtures/ca2.key"});
  }
  {
    Options options;
    options.crl_number = 12;
    options.uri = "http://ca1.example/crl/other.crl";
    options.revoke_leaf = true;
    fixtures.push_back({"crl_ca1_scope_mismatch.der", options, &*ca1,
                        "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 13;
    options.only_user = false;
    options.only_ca = true;
    options.revoke_leaf = true;
    fixtures.push_back(
        {"crl_ca1_ca_only.der", options, &*ca1, "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 14;
    fixtures.push_back({"crl_ca1_good_same_scope.der", options, &*ca1,
                        "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 5;
    fixtures.push_back(
        {"crl_ca1_base5.der", options, &*ca1, "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 6;
    options.base_number = 5;
    options.revoke_leaf = true;
    fixtures.push_back(
        {"crl_ca1_delta6.der", options, &*ca1, "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 7;
    options.base_number = 5;
    fixtures.push_back(
        {"crl_ca1_delta7_gap.der", options, &*ca1, "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 6;
    options.base_number = 5;
    options.only_user = false;
    options.only_ca = true;
    options.revoke_leaf = true;
    fixtures.push_back({"crl_ca1_delta6_scope_mismatch.der", options, &*ca1,
                        "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 30;
    options.expired = true;
    fixtures.push_back(
        {"crl_ca1_expired.der", options, &*ca1, "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 40;
    options.unknown_critical = true;
    fixtures.push_back({"crl_ca1_unknown_critical.der", options, &*ca1,
                        "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 50;
    options.bad_signature = true;
    fixtures.push_back({"crl_ca1_bad_signature.der", options, &*ca1,
                        "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 60;
    options.indirect = true;
    options.revoke_leaf = true;
    options.entry_issuer = ca1->subject;
    fixtures.push_back({"crl_delegate_indirect.der", options, &*delegate,
                        "tests/fixtures/delegate.key"});
  }
  {
    Options options;
    options.crl_number = 70;
    options.revoke_leaf = true;
    options.reason_code = 6; // certificateHold
    fixtures.push_back(
        {"crl_ca1_hold_base70.der", options, &*ca1, "tests/fixtures/ca1.key"});
  }
  {
    Options options;
    options.crl_number = 71;
    options.base_number = 70;
    options.remove_leaf = true;
    fixtures.push_back({"crl_ca1_remove_delta71.der", options, &*ca1,
                        "tests/fixtures/ca1.key"});
  }

  for (const Fixture &item : fixtures) {
    const netsec::CertificateInfo &revoke_target =
        item.name == "crl_delegate_indirect.der" ? *delegated_leaf : *leaf;
    Bytes der =
        build_crl(*item.signer, revoke_target, item.key, tmp, item.options);
    if (!write_file(fixture / item.name, der)) {
      std::cerr << "failed to write " << item.name << "\n";
      return 1;
    }
    auto parsed = netsec::parse_crl(der);
    if (!parsed.has_value() && !item.options.unknown_critical) {
      std::cerr << "generated CRL did not parse: " << item.name << "\n";
      return 1;
    }
  }
  return 0;
}
