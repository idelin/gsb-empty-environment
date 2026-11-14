#include "netsec.hpp"

#include <chrono>
#include <cstdint>
#include <fstream>
#include <iostream>
#include <iterator>
#include <string>
#include <vector>

namespace {

using netsec::Bytes;

Bytes read_file(const std::string &path) {
  std::ifstream input(path, std::ios::binary);
  if (!input) {
    std::cerr << "cannot open " << path << "\n";
    std::exit(1);
  }
  return Bytes(std::istreambuf_iterator<char>(input),
               std::istreambuf_iterator<char>());
}

int failures = 0;

void expect(bool condition, const std::string &message) {
  if (!condition) {
    std::cerr << "FAIL: " << message << "\n";
    ++failures;
  }
}

netsec::ValidationContext context_at_2026_11_20() {
  netsec::ValidationContext context;
  auto day = std::chrono::sys_days{std::chrono::year{2026} /
                                   std::chrono::November / 20};
  context.now =
      netsec::TimePoint(std::chrono::duration_cast<netsec::Clock::duration>(
          day.time_since_epoch()));
  return context;
}

void expect_error(const netsec::CrlValidationResult &result,
                  netsec::CrlError error, const std::string &message) {
  expect(result.error == error,
         message + " (error=" + std::string(netsec::to_string(result.error)) +
             ", detail=" + result.detail + ")");
}

} // namespace

int main() {
  const std::string fixture = "tests/fixtures/";
  Bytes ca1 = read_file(fixture + "ca1.der");
  Bytes ca2 = read_file(fixture + "ca2.der");
  Bytes leaf = read_file(fixture + "leaf.der");
  netsec::ValidationContext context = context_at_2026_11_20();

  auto parsed_leaf = netsec::parse_certificate(leaf);
  auto parsed_ca1 = netsec::parse_certificate(ca1);
  auto parsed_crl =
      netsec::parse_crl(read_file(fixture + "crl_ca1_scope_revoke.der"));
  expect(parsed_leaf.has_value(), "leaf certificate parses");
  expect(parsed_ca1.has_value(), "issuer certificate parses");
  expect(parsed_crl.has_value(), "scoped CRL parses");
  if (parsed_leaf && parsed_ca1 && parsed_crl) {
    expect(parsed_crl->scope.only_contains_user_certs,
           "onlyContainsUserCerts is parsed");
    expect(parsed_crl->scope.only_contains_ca_certs == false,
           "onlyContainsCACerts default is false");
    expect(parsed_crl->scope.indirect_crl == false,
           "indirectCRL default is false");
    expect(parsed_crl->scope.distribution_point.has_value(),
           "issuingDistributionPoint name is parsed");
    expect(parsed_crl->crl_number == 10, "cRLNumber is parsed");
    expect(parsed_leaf->crl_distribution_points.size() == 1,
           "leaf CRL distribution point is parsed");
    if (parsed_crl->scope.distribution_point &&
        !parsed_leaf->crl_distribution_points.empty() &&
        parsed_leaf->crl_distribution_points.front().distribution_point) {
      expect(
          parsed_crl->scope.distribution_point->intersects(
              *parsed_leaf->crl_distribution_points.front().distribution_point),
          "CRL IDP and certificate CRLDP names match");
    }
  }

  auto parsed_delta =
      netsec::parse_crl(read_file(fixture + "crl_ca1_delta6.der"));
  expect(parsed_delta.has_value(), "delta CRL parses");
  if (parsed_delta) {
    expect(parsed_delta->is_delta(), "delta CRL is identified");
    expect(parsed_delta->crl_number == 6, "delta cRLNumber is parsed");
    expect(parsed_delta->base_crl_number == 5,
           "deltaCRLIndicator base number is parsed");
  }

  auto parsed_ca_only =
      netsec::parse_crl(read_file(fixture + "crl_ca1_ca_only.der"));
  expect(parsed_ca_only.has_value(), "CA-only CRL parses");
  if (parsed_ca_only) {
    expect(parsed_ca_only->scope.only_contains_ca_certs,
           "onlyContainsCACerts is parsed");
    expect(!parsed_ca_only->scope.only_contains_user_certs,
           "onlyContainsUserCerts stays false for CA-only CRL");
  }

  auto parsed_indirect =
      netsec::parse_crl(read_file(fixture + "crl_delegate_indirect.der"));
  expect(parsed_indirect.has_value(), "indirect CRL parses");
  if (parsed_indirect) {
    expect(parsed_indirect->scope.indirect_crl, "indirectCRL flag is parsed");
    expect(!parsed_indirect->entries.empty() &&
               !parsed_indirect->entries.front().certificate_issuer.empty(),
           "certificateIssuer entry extension is parsed");
  }

  auto valid_revoked = netsec::check_revocation(
      leaf, ca1, read_file(fixture + "crl_ca1_scope_revoke.der"), context);
  expect(valid_revoked.status == netsec::RevocationStatus::Revoked,
         "same-scope CRL revokes the certificate");
  expect(valid_revoked.revoked, "revoked flag is set");
  expect(valid_revoked.error == netsec::CrlError::None,
         "valid revocation has no error");
  expect(valid_revoked.revocation_entry.has_value(),
         "revocation entry is exposed");

  auto full_validation = netsec::validate_certificate(
      leaf, ca1, read_file(fixture + "crl_ca1_scope_revoke.der"), context);
  expect(full_validation.status == netsec::RevocationStatus::Revoked,
         "full certificate validation reaches the same revocation conclusion");

  auto foreign_ca = netsec::check_revocation(
      leaf, ca1, read_file(fixture + "crl_other_ca.der"), context);
  expect(foreign_ca.status == netsec::RevocationStatus::Unknown,
         "another CA's CRL never proves revocation for this certificate");
  expect(foreign_ca.revoked == false,
         "another CA's CRL does not report this certificate as revoked");
  expect(foreign_ca.has_revocation_evidence() == false,
         "another CA's CRL is not revocation evidence");
  expect(foreign_ca.error == netsec::CrlError::UnauthorizedCrlIssuer ||
             foreign_ca.error == netsec::CrlError::IssuerMismatch,
         "another CA's CRL is rejected as unauthorized");

  auto wrong_scope = netsec::check_revocation(
      leaf, ca1, read_file(fixture + "crl_ca1_scope_mismatch.der"), context);
  expect(wrong_scope.status == netsec::RevocationStatus::Unknown,
         "wrong distribution point is not used as revocation evidence");
  expect(wrong_scope.revoked == false,
         "wrong distribution point does not revoke the certificate");
  expect_error(wrong_scope, netsec::CrlError::ScopeMismatch,
               "wrong distribution point is a scope mismatch");

  auto ca_only = netsec::check_revocation(
      leaf, ca1, read_file(fixture + "crl_ca1_ca_only.der"), context);
  expect(ca_only.status == netsec::RevocationStatus::Unknown,
         "CA-only CRL does not cover an end-entity certificate");
  expect_error(ca_only, netsec::CrlError::ScopeMismatch,
               "onlyContainsCACerts mismatch is rejected");

  auto good = netsec::check_revocation(
      leaf, ca1, read_file(fixture + "crl_ca1_good_same_scope.der"), context);
  expect(good.status == netsec::RevocationStatus::Good,
         "same-scope CRL without an entry proves good status");
  expect(good.ok(), "good result is usable evidence");

  std::vector<Bytes> mixed_evidence{
      read_file(fixture + "crl_other_ca.der"),
      read_file(fixture + "crl_ca1_scope_revoke.der")};
  auto mixed = netsec::check_revocation(leaf, ca1, mixed_evidence, context);
  expect(mixed.status == netsec::RevocationStatus::Revoked,
         "a foreign CRL cannot mask valid same-scope revocation evidence");

  auto expired = netsec::check_revocation(
      leaf, ca1, read_file(fixture + "crl_ca1_expired.der"), context);
  expect(expired.status == netsec::RevocationStatus::Unknown,
         "expired CRL is rejected");
  expect_error(expired, netsec::CrlError::Expired,
               "expired CRL reports expiry");

  auto unknown_critical = netsec::check_revocation(
      leaf, ca1, read_file(fixture + "crl_ca1_unknown_critical.der"), context);
  expect(unknown_critical.status == netsec::RevocationStatus::Unknown,
         "unknown critical extension CRL is rejected");
  expect_error(unknown_critical, netsec::CrlError::UnknownCriticalExtension,
               "unknown critical extension fails closed");

  auto bad_signature = netsec::check_revocation(
      leaf, ca1, read_file(fixture + "crl_ca1_bad_signature.der"), context);
  expect(bad_signature.status == netsec::RevocationStatus::Unknown,
         "invalid signature is rejected");
  expect_error(bad_signature, netsec::CrlError::InvalidSignature,
               "invalid signature is reported");

  auto delta_alone = netsec::check_revocation(
      leaf, ca1, read_file(fixture + "crl_ca1_delta6.der"), context);
  expect(delta_alone.status == netsec::RevocationStatus::Unknown,
         "delta CRL without a base fails closed");
  expect_error(delta_alone, netsec::CrlError::DeltaWithoutBase,
               "missing base is reported");

  std::vector<Bytes> base_and_delta{read_file(fixture + "crl_ca1_base5.der"),
                                    read_file(fixture + "crl_ca1_delta6.der")};
  auto chained = netsec::check_revocation(leaf, ca1, base_and_delta, context);
  expect(chained.status == netsec::RevocationStatus::Revoked,
         "base plus contiguous delta revokes the certificate");
  expect(chained.base_crl_number == 5, "base CRL number is exposed");
  expect(chained.crl_number == 6, "latest delta CRL number is exposed");

  std::vector<Bytes> reversed_chain{base_and_delta[1], base_and_delta[0]};
  auto chained_reversed =
      netsec::check_revocation(leaf, ca1, reversed_chain, context);
  expect(chained_reversed.status == netsec::RevocationStatus::Revoked,
         "base and delta ordering does not depend on input order");

  std::vector<Bytes> gapped_chain{
      read_file(fixture + "crl_ca1_base5.der"),
      read_file(fixture + "crl_ca1_delta7_gap.der")};
  auto gap = netsec::check_revocation(leaf, ca1, gapped_chain, context);
  expect(gap.status == netsec::RevocationStatus::Unknown,
         "a delta number gap is not accepted as good or revoked evidence");
  expect(gap.revoked == false, "gap does not claim the certificate revoked");
  expect_error(gap, netsec::CrlError::DeltaGap, "delta gap is reported");

  std::vector<Bytes> mismatched_delta{
      read_file(fixture + "crl_ca1_base5.der"),
      read_file(fixture + "crl_ca1_delta6_scope_mismatch.der")};
  auto delta_scope_mismatch =
      netsec::check_revocation(leaf, ca1, mismatched_delta, context);
  expect(delta_scope_mismatch.status == netsec::RevocationStatus::Unknown,
         "a delta with a different scope cannot extend the base");
  expect_error(delta_scope_mismatch, netsec::CrlError::DeltaScopeMismatch,
               "delta scope mismatch is reported");

  auto base_only = netsec::check_revocation(
      leaf, ca1, read_file(fixture + "crl_ca1_base5.der"), context);
  expect(base_only.status == netsec::RevocationStatus::Good,
         "a complete base CRL can stand alone");

  auto held = netsec::check_revocation(
      leaf, ca1, read_file(fixture + "crl_ca1_hold_base70.der"), context);
  expect(held.status == netsec::RevocationStatus::Revoked,
         "certificateHold is a revocation entry");
  expect(held.revocation_entry.has_value() &&
             held.revocation_entry->reason_code == 6,
         "certificateHold reason is preserved");

  std::vector<Bytes> remove_chain{
      read_file(fixture + "crl_ca1_hold_base70.der"),
      read_file(fixture + "crl_ca1_remove_delta71.der")};
  auto removed = netsec::check_revocation(leaf, ca1, remove_chain, context);
  expect(removed.status == netsec::RevocationStatus::Good,
         "removeFromCRL in a delta updates the base status");

  Bytes delegated_leaf = read_file(fixture + "leaf_delegated.der");
  Bytes delegate = read_file(fixture + "delegate.der");
  Bytes indirect_crl = read_file(fixture + "crl_delegate_indirect.der");

  netsec::ValidationContext trusted_context = context;
  trusted_context.trusted_crl_signer_certificates.push_back(delegate);
  auto indirect_revoked = netsec::check_revocation(
      delegated_leaf, ca1, indirect_crl, trusted_context);
  expect(indirect_revoked.status == netsec::RevocationStatus::Revoked,
         "explicitly delegated indirect CRL revokes the certificate");

  netsec::ValidationContext chained_context = context;
  chained_context.additional_certificates.push_back(delegate);
  auto chained_delegation = netsec::check_revocation(
      delegated_leaf, ca1, indirect_crl, chained_context);
  expect(chained_delegation.status == netsec::RevocationStatus::Revoked,
         "cRLIssuer delegation chain authenticates the indirect CRL");

  auto unknown_delegate =
      netsec::check_revocation(delegated_leaf, ca1, indirect_crl, context);
  expect(unknown_delegate.status == netsec::RevocationStatus::Unknown,
         "indirect CRL without a supplied delegated signer is rejected");
  expect(unknown_delegate.error == netsec::CrlError::UnauthorizedCrlIssuer,
         "missing delegation is reported");

  auto indirect_scope_mismatch =
      netsec::check_revocation(leaf, ca1, indirect_crl, trusted_context);
  expect(
      indirect_scope_mismatch.status == netsec::RevocationStatus::Unknown,
      "indirect CRL does not cover a certificate without matching cRLIssuer");
  expect_error(indirect_scope_mismatch, netsec::CrlError::ScopeMismatch,
               "indirect CRL scope mismatch is reported");

  netsec::CrlScopeComponent component(context);
  expect(component.import_crl(read_file(fixture + "crl_ca1_base5.der")),
         "component imports a complete CRL");
  expect(component.import_crl(read_file(fixture + "crl_ca1_delta6.der")),
         "component imports a delta CRL");
  auto component_result = component.check(leaf, ca1);
  expect(component_result.status == netsec::RevocationStatus::Revoked,
         "component assembles imported base and delta CRLs");

  if (failures == 0) {
    std::cout << "all netsec tests passed\n";
  }
  return failures == 0 ? 0 : 1;
}
