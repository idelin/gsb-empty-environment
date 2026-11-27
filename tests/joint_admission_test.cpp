#include "ja/joint_admission.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <string>
#include <vector>

namespace {

int failures = 0;

#define CHECK(condition)                                                     \
  do {                                                                       \
    if (!(condition)) {                                                      \
      std::cerr << __FILE__ << ":" << __LINE__ << ": check failed: "         \
                << #condition << "\n";                                       \
      ++failures;                                                            \
    }                                                                        \
  } while (false)

ja::Signature sign_manifest(const ja::VisibilityManifest& manifest) {
  std::vector<std::uint8_t> material = ja::canonical_manifest(manifest);
  material.push_back(0xa5);
  const ja::Digest digest = ja::sha256(material);
  return ja::Signature(digest.begin(), digest.end());
}

bool verify_manifest(const ja::VisibilityManifest& manifest,
                     const ja::Signature& signature) {
  return signature == sign_manifest(manifest);
}

ja::SignedManifest signed_manifest(ja::VisibilityManifest manifest,
                                   ja::Obligation signature_obligation = 1) {
  ja::SignedManifest result;
  result.manifest = std::move(manifest);
  result.signature = sign_manifest(result.manifest);
  result.signature_obligation = signature_obligation;
  return result;
}

ja::Field field(ja::FieldId id,
                ja::Obligation obligation,
                ja::Span span,
                ja::SharedId shared = 0,
                ja::SemanticId interpretation = 0) {
  ja::Field result;
  result.id = id;
  result.obligation = obligation;
  result.span = span;
  result.shared_object = shared;
  result.interpretation = interpretation;
  return result;
}

ja::Layout fixed_layout(ja::Obligation obligation,
                        std::uint64_t length,
                        std::vector<ja::Field> fields,
                        ja::CaseId case_id = ja::kDefaultCase) {
  ja::Layout layout;
  layout.obligation = obligation;
  layout.min_length = length;
  layout.max_length = length;
  layout.arms.push_back(ja::Arm{case_id, obligation + 1, std::move(fields)});
  return layout;
}

ja::Layout variable_layout(ja::Obligation obligation,
                           std::uint64_t minimum,
                           std::uint64_t maximum,
                           std::vector<ja::Field> fields) {
  ja::Layout layout;
  layout.obligation = obligation;
  layout.min_length = minimum;
  layout.max_length = maximum;
  layout.arms.push_back(
      ja::Arm{ja::kDefaultCase, obligation + 1, std::move(fields)});
  return layout;
}

ja::PermissionRule rule(ja::Obligation obligation,
                        ja::View view,
                        ja::Span span) {
  return ja::PermissionRule{obligation, view, span};
}

std::vector<std::uint8_t> make_bytes(std::uint64_t size,
                                     std::uint64_t seed) {
  std::vector<std::uint8_t> result(static_cast<std::size_t>(size));
  std::uint64_t value = seed * 6364136223846793005ull + 1;
  for (std::uint8_t& byte : result) {
    value = value * 2862933555777941757ull + 3037000493ull;
    byte = static_cast<std::uint8_t>(value >> 24);
  }
  return result;
}

bool same_bytes(ja::ByteView view, const std::vector<std::uint8_t>& expected) {
  if (view.size != expected.size()) {
    return false;
  }
  return std::equal(expected.begin(), expected.end(), view.data,
                    view.data + expected.size());
}

std::vector<std::uint8_t> copy_view(ja::ByteView view) {
  return std::vector<std::uint8_t>(view.data, view.data + view.size);
}

void test_sha256() {
  const std::string text = "abc";
  const ja::Digest digest = ja::sha256(
      reinterpret_cast<const std::uint8_t*>(text.data()), text.size());
  const ja::Digest expected = {
      0xba, 0x78, 0x16, 0xbf, 0x8f, 0x01, 0xcf, 0xea, 0x41, 0x41,
      0x40, 0xde, 0x5d, 0xae, 0x22, 0x23, 0xb0, 0x03, 0x61, 0xa3,
      0x96, 0x17, 0x7a, 0x9c, 0xb4, 0x10, 0xff, 0x61, 0xf2, 0x00,
      0x15, 0xad};
  CHECK(digest == expected);
}

void test_shared_object_visibility() {
  ja::Layout old_layout =
      fixed_layout(10, 8,
                   {field(1, 101, ja::Span::fixed(0, 2), 7, 9),
                    field(2, 102, ja::Span::fixed(2, 2), 7, 9)});
  ja::Layout new_layout =
      fixed_layout(20, 8,
                   {field(11, 201, ja::Span::fixed(2, 2), 7, 9),
                    field(12, 202, ja::Span::fixed(4, 2), 7, 9)});
  old_layout.shared_objects.push_back(
      ja::SharedObject{7, 1001, ja::Span::fixed(0, 8), 9});
  new_layout.shared_objects = old_layout.shared_objects;

  ja::VisibilityManifest manifest;
  manifest.generation = 3;
  manifest.grants.push_back(rule(301, ja::View::Old, ja::Span::fixed(0, 4)));
  manifest.grants.push_back(rule(302, ja::View::New, ja::Span::fixed(2, 4)));
  const ja::SignedManifest signed_manifest_value = signed_manifest(manifest);

  ja::JointAdmission admission(verify_manifest);
  ja::AdmissionOutcome outcome =
      admission.admit(old_layout, new_layout, signed_manifest_value);
  CHECK(outcome.handle.has_value());
  if (!outcome.handle.has_value()) {
    return;
  }

  const ja::ReadOnlyHandle& handle = *outcome.handle;
  CHECK(handle.valid());
  CHECK(handle.old_layout_digest() == ja::layout_digest(old_layout));
  CHECK(handle.new_layout_digest() == ja::layout_digest(new_layout));
  CHECK(handle.policy_generation() == 3);

  // Exhaust every byte at every message position, but only 8 * 256 states.
  for (std::uint64_t position = 0; position < 8; ++position) {
    for (std::uint32_t value = 0; value < 256; ++value) {
      std::vector<std::uint8_t> bytes =
          make_bytes(8, position * 257u + value);
      bytes[static_cast<std::size_t>(position)] =
          static_cast<std::uint8_t>(value);
      ja::Message message{bytes.data(), bytes.size(), 0};

      const auto old_left = handle.read(ja::View::Old, 1, message);
      const auto old_right = handle.read(ja::View::Old, 2, message);
      const auto new_left = handle.read(ja::View::New, 11, message);
      const auto new_right = handle.read(ja::View::New, 12, message);
      CHECK(old_left.has_value());
      CHECK(old_right.has_value());
      CHECK(new_left.has_value());
      CHECK(new_right.has_value());
      if (old_left) {
        CHECK(old_left->data == bytes.data());
        CHECK(old_left->size == 2);
      }
      if (old_right) {
        CHECK(old_right->data == bytes.data() + 2);
      }
      if (new_left) {
        CHECK(new_left->data == bytes.data() + 2);
      }
      if (new_right) {
        CHECK(new_right->data == bytes.data() + 4);
      }
      CHECK(!handle.read(ja::View::Old, 11, message).has_value());
      CHECK(!handle.read(ja::View::New, 1, message).has_value());
    }
  }

  // The two entries have genuinely different visibility.
  CHECK(!handle.read(ja::View::Old, 1,
                     ja::Message{nullptr, 8, 0})
             .has_value());

  ja::VisibilityManifest denied = manifest;
  denied.grants.pop_back();
  denied.grants.push_back(rule(302, ja::View::New, ja::Span::fixed(2, 2)));
  const ja::AdmissionOutcome rejected =
      admission.admit(old_layout, new_layout, signed_manifest(denied));
  CHECK(!rejected.handle.has_value());
  CHECK(rejected.failure.layout == 202);
  CHECK(rejected.failure.permission == 0);
}

void test_variable_fragments_and_large_lengths() {
  ja::Layout old_layout = variable_layout(
      30, 8, ja::kMaxMessageLength,
      {field(1, 301, ja::Span::to_end(4))});
  ja::Layout new_layout = variable_layout(
      31, 8, ja::kMaxMessageLength,
      {field(11, 311, ja::Span::fixed(4, 4)),
       field(12, 312, ja::Span::to_end(8))});

  ja::Compatibility compatibility;
  compatibility.obligation = 320;
  compatibility.old_field = 1;
  compatibility.new_fragments.push_back(ja::NewFragment{321, 11, 0, std::nullopt});
  compatibility.new_fragments.push_back(ja::NewFragment{322, 12, 0, std::nullopt});
  old_layout.compatibility.push_back(compatibility);

  ja::VisibilityManifest manifest;
  manifest.generation = 77;
  manifest.grants.push_back(
      rule(401, ja::View::Old, ja::Span::to_end(0)));
  manifest.grants.push_back(
      rule(402, ja::View::New, ja::Span::to_end(0)));
  const ja::SignedManifest signed_manifest_value = signed_manifest(manifest);

  ja::AdmissionProof proof;
  proof.policy_generation = 77;
  proof.lengths.push_back(ja::LengthClass{501, ja::View::Old,
                                          ja::kDefaultCase, 8,
                                          ja::kMaxMessageLength});
  proof.lengths.push_back(ja::LengthClass{502, ja::View::New,
                                          ja::kDefaultCase, 8,
                                          ja::kMaxMessageLength});
  CHECK(!ja::JointAdmission::verify(old_layout, new_layout,
                                    signed_manifest_value, proof,
                                    verify_manifest)
             .has_value());

  ja::JointAdmission admission(verify_manifest);
  ja::AdmissionOutcome outcome =
      admission.admit(old_layout, new_layout, signed_manifest_value, &proof);
  CHECK(outcome.handle.has_value());
  if (!outcome.handle.has_value()) {
    return;
  }

  const auto old_large =
      outcome.handle->interval(ja::View::Old, 1, ja::kMaxMessageLength, 0);
  const auto new_lo =
      outcome.handle->interval(ja::View::New, 11, ja::kMaxMessageLength, 0);
  const auto new_hi =
      outcome.handle->interval(ja::View::New, 12, ja::kMaxMessageLength, 0);
  CHECK((old_large == ja::Interval{4, ja::kMaxMessageLength}));
  CHECK((new_lo == ja::Interval{4, 8}));
  CHECK((new_hi == ja::Interval{8, ja::kMaxMessageLength}));

  // Exhaustive small-space comparison: old field equals concatenated new
  // fragments for every accepted length in a small window.
  for (std::uint64_t length = 8; length <= 12; ++length) {
    std::vector<std::uint8_t> bytes = make_bytes(length, length);
    ja::Message message{bytes.data(), bytes.size(), 0};
    const auto old_field = outcome.handle->read(ja::View::Old, 1, message);
    const auto fragment_lo =
        outcome.handle->read(ja::View::New, 11, message);
    const auto fragment_hi =
        outcome.handle->read(ja::View::New, 12, message);
    CHECK(old_field.has_value());
    CHECK(fragment_lo.has_value());
    CHECK(fragment_hi.has_value());
    if (old_field && fragment_lo && fragment_hi) {
      std::vector<std::uint8_t> combined = copy_view(*fragment_lo);
      const std::vector<std::uint8_t> tail = copy_view(*fragment_hi);
      combined.insert(combined.end(), tail.begin(), tail.end());
      CHECK(same_bytes(*old_field, combined));
    }
  }

  ja::AdmissionProof missing_class = proof;
  missing_class.lengths.pop_back();
  CHECK(ja::JointAdmission::verify(old_layout, new_layout,
                                   signed_manifest_value, missing_class,
                                   verify_manifest)
            .has_value());

  ja::AdmissionProof overlapping_lengths = proof;
  overlapping_lengths.lengths.push_back(
      ja::LengthClass{503, ja::View::New, ja::kDefaultCase, 8, 9});
  CHECK(ja::JointAdmission::verify(old_layout, new_layout,
                                   signed_manifest_value,
                                   overlapping_lengths, verify_manifest)
            .has_value());

  ja::AdmissionProof wrong_generation = proof;
  wrong_generation.policy_generation = 76;
  CHECK(ja::JointAdmission::verify(old_layout, new_layout,
                                   signed_manifest_value, wrong_generation,
                                   verify_manifest)
            .has_value());
}

ja::Layout renamed_layout(ja::Obligation obligation,
                          std::uint64_t tag_first,
                          std::uint64_t tag_second,
                          ja::FieldId first_field,
                          ja::FieldId second_field,
                          ja::Obligation first_obligation,
                          ja::Obligation second_obligation) {
  ja::Layout layout;
  layout.obligation = obligation;
  layout.min_length = 4;
  layout.max_length = 4;
  ja::Discriminant discriminant;
  discriminant.obligation = obligation + 1;
  discriminant.span = ja::Span::fixed(0, 1);
  discriminant.width = 1;
  discriminant.domain_first = std::min(tag_first, tag_second);
  discriminant.domain_last = std::max(tag_first, tag_second);
  discriminant.bindings.push_back(
      ja::TagBinding{obligation + 2, tag_first, tag_first, 10});
  discriminant.bindings.push_back(
      ja::TagBinding{obligation + 3, tag_second, tag_second, 20});
  layout.discriminant = discriminant;
  layout.arms.push_back(ja::Arm{
      10, obligation + 4,
      {field(first_field, first_obligation, ja::Span::fixed(1, 2))}});
  layout.arms.push_back(ja::Arm{
      20, obligation + 5,
      {field(second_field, second_obligation, ja::Span::fixed(1, 2))}});
  return layout;
}

void test_discriminant_rename() {
  ja::Layout old_layout =
      renamed_layout(50, 1, 2, 1, 2, 501, 502);
  ja::Layout new_layout =
      renamed_layout(60, 101, 102, 11, 12, 601, 602);
  old_layout.compatibility.push_back(
      ja::Compatibility{510, 1, {ja::NewFragment{511, 11, 0, std::nullopt}}});
  old_layout.compatibility.push_back(
      ja::Compatibility{520, 2, {ja::NewFragment{521, 12, 0, std::nullopt}}});

  ja::VisibilityManifest manifest;
  manifest.generation = 9;
  manifest.grants.push_back(rule(700, ja::View::Old, ja::Span::fixed(0, 4)));
  manifest.grants.push_back(rule(701, ja::View::New, ja::Span::fixed(0, 4)));
  const ja::SignedManifest signed_manifest_value = signed_manifest(manifest);

  ja::AdmissionProof proof;
  proof.policy_generation = 9;
  proof.tags = {
      ja::TagClass{801, ja::View::Old, 1, 1, 10},
      ja::TagClass{802, ja::View::Old, 2, 2, 20},
      ja::TagClass{803, ja::View::New, 101, 101, 10},
      ja::TagClass{804, ja::View::New, 102, 102, 20},
  };
  for (const ja::View view : {ja::View::Old, ja::View::New}) {
    proof.lengths.push_back(ja::LengthClass{900, view, 10, 4, 4});
    proof.lengths.push_back(ja::LengthClass{901, view, 20, 4, 4});
  }

  CHECK(!ja::JointAdmission::verify(old_layout, new_layout,
                                    signed_manifest_value, proof,
                                    verify_manifest)
             .has_value());

  ja::JointAdmission admission(verify_manifest);
  ja::AdmissionOutcome outcome =
      admission.admit(old_layout, new_layout, signed_manifest_value, &proof);
  CHECK(outcome.handle.has_value());
  if (!outcome.handle.has_value()) {
    return;
  }

  for (std::uint32_t value = 0; value < 256; ++value) {
    std::vector<std::uint8_t> bytes = {0, static_cast<std::uint8_t>(value),
                                       0x33, 0x44};
    ja::Message old_message{bytes.data(), bytes.size(), 1};
    ja::Message new_message{bytes.data(), bytes.size(), 101};
    const auto old_read = outcome.handle->read(ja::View::Old, 1, old_message);
    const auto new_read =
        outcome.handle->read(ja::View::New, 11, new_message);
    CHECK(old_read.has_value());
    CHECK(new_read.has_value());
    if (old_read && new_read) {
      CHECK(same_bytes(*old_read, copy_view(*new_read)));
    }
    CHECK(!outcome.handle
               ->read(ja::View::Old, 1,
                      ja::Message{bytes.data(), bytes.size(), 2})
               .has_value());
    CHECK(!outcome.handle
               ->read(ja::View::New, 11,
                      ja::Message{bytes.data(), bytes.size(), 102})
               .has_value());
  }

  ja::AdmissionProof contradictory = proof;
  contradictory.tags[0] = ja::TagClass{801, ja::View::Old, 1, 2, 10};
  contradictory.tags.erase(contradictory.tags.begin() + 1);
  CHECK(ja::JointAdmission::verify(old_layout, new_layout,
                                   signed_manifest_value, contradictory,
                                   verify_manifest)
            .has_value());

  ja::AdmissionProof missing_tag = proof;
  missing_tag.tags.erase(missing_tag.tags.begin() + 1);
  CHECK(ja::JointAdmission::verify(old_layout, new_layout,
                                   signed_manifest_value, missing_tag,
                                   verify_manifest)
            .has_value());
}

void test_same_raw_tag_must_have_same_meaning() {
  ja::Layout old_layout =
      renamed_layout(70, 1, 2, 1, 2, 701, 702);
  ja::Layout new_layout =
      renamed_layout(80, 2, 102, 11, 12, 801, 802);
  // The helper creates bindings for exactly 2 and 102, but its domain is
  // [2, 102]; make the middle interval explicit.
  auto& bindings = new_layout.discriminant->bindings;
  bindings = {
      ja::TagBinding{803, 2, 101, 10},
      ja::TagBinding{804, 102, 102, 20},
  };

  ja::VisibilityManifest manifest;
  manifest.generation = 10;
  manifest.grants.push_back(rule(710, ja::View::Old, ja::Span::fixed(0, 4)));
  manifest.grants.push_back(rule(711, ja::View::New, ja::Span::fixed(0, 4)));
  const ja::SignedManifest signed_manifest_value = signed_manifest(manifest);

  ja::AdmissionProof proof;
  proof.policy_generation = 10;
  proof.tags = {
      ja::TagClass{811, ja::View::Old, 1, 1, 10},
      ja::TagClass{812, ja::View::Old, 2, 2, 20},
      ja::TagClass{813, ja::View::New, 2, 101, 10},
      ja::TagClass{814, ja::View::New, 102, 102, 20},
  };
  for (const ja::View view : {ja::View::Old, ja::View::New}) {
    proof.lengths.push_back(ja::LengthClass{910, view, 10, 4, 4});
    proof.lengths.push_back(ja::LengthClass{911, view, 20, 4, 4});
  }

  const auto failure = ja::JointAdmission::verify(
      old_layout, new_layout, signed_manifest_value, proof, verify_manifest);
  CHECK(failure.has_value());
  CHECK(failure->layout == 812);
}

void test_revocation_and_signature() {
  ja::Layout old_layout = fixed_layout(
      90, 4, {field(1, 901, ja::Span::fixed(0, 4))});
  ja::Layout new_layout = fixed_layout(
      91, 4, {field(11, 911, ja::Span::fixed(0, 4))});
  old_layout.compatibility.push_back(
      ja::Compatibility{920, 1, {ja::NewFragment{921, 11, 0, std::nullopt}}});

  ja::VisibilityManifest manifest;
  manifest.generation = 12;
  manifest.grants.push_back(rule(930, ja::View::Old, ja::Span::fixed(0, 4)));
  manifest.grants.push_back(rule(931, ja::View::New, ja::Span::fixed(0, 4)));
  ja::VisibilityManifest revoked = manifest;
  revoked.denials.push_back(
      rule(940, ja::View::New, ja::Span::fixed(1, 2)));

  ja::JointAdmission admission(verify_manifest);
  const ja::AdmissionOutcome denied =
      admission.admit(old_layout, new_layout, signed_manifest(revoked));
  CHECK(!denied.handle.has_value());
  CHECK(denied.failure.layout == 911);
  CHECK(denied.failure.permission == 940);

  ja::SignedManifest bad_signature = signed_manifest(manifest, 950);
  bad_signature.signature[0] ^= 0xff;
  const ja::AdmissionOutcome unsigned_result =
      admission.admit(old_layout, new_layout, bad_signature);
  CHECK(!unsigned_result.handle.has_value());
  CHECK(unsigned_result.failure.layout == 90);
  CHECK(unsigned_result.failure.permission == 950);

  const ja::AdmissionOutcome accepted =
      admission.admit(old_layout, new_layout, signed_manifest(manifest));
  CHECK(accepted.handle.has_value());
}

void test_alias_cannot_expose_hidden_bytes() {
  ja::Layout old_layout = fixed_layout(
      100, 6, {field(1, 1001, ja::Span::fixed(0, 4))});
  ja::Layout new_layout = fixed_layout(
      110, 6,
      {field(11, 1011, ja::Span::fixed(0, 2)),
       field(12, 1012, ja::Span::fixed(2, 4))});
  old_layout.compatibility.push_back(
      ja::Compatibility{1020,
                        1,
                        {ja::NewFragment{1021, 11, 0, std::nullopt},
                         ja::NewFragment{1022, 12, 0, std::nullopt}}});

  ja::VisibilityManifest manifest;
  manifest.generation = 13;
  manifest.grants.push_back(rule(1030, ja::View::Old, ja::Span::fixed(0, 4)));
  manifest.grants.push_back(rule(1031, ja::View::New, ja::Span::fixed(0, 6)));

  ja::JointAdmission admission(verify_manifest);
  const ja::AdmissionOutcome outcome =
      admission.admit(old_layout, new_layout, signed_manifest(manifest));
  CHECK(!outcome.handle.has_value());
  CHECK(outcome.failure.layout == 1020);
  CHECK(outcome.failure.permission == 0);
}

void test_overlapping_interpretations() {
  ja::Layout layout =
      fixed_layout(120, 4,
                   {field(1, 1201, ja::Span::fixed(0, 4), 7, 31),
                    field(2, 1202, ja::Span::fixed(2, 2), 7, 32)});
  layout.shared_objects.push_back(
      ja::SharedObject{7, 1200, ja::Span::fixed(0, 4), 31});

  ja::VisibilityManifest manifest;
  manifest.generation = 14;
  manifest.grants.push_back(rule(1210, ja::View::Old, ja::Span::fixed(0, 4)));
  manifest.grants.push_back(rule(1211, ja::View::New, ja::Span::fixed(0, 4)));

  ja::JointAdmission admission(verify_manifest);
  const ja::AdmissionOutcome outcome =
      admission.admit(layout, layout, signed_manifest(manifest));
  CHECK(!outcome.handle.has_value());
  CHECK(outcome.failure.layout == 1202);
  CHECK(outcome.failure.permission == 0);
}

void test_overlapping_proof_classes() {
  ja::Layout old_layout =
      renamed_layout(130, 1, 2, 1, 2, 1301, 1302);
  ja::Layout new_layout =
      renamed_layout(140, 101, 102, 11, 12, 1401, 1402);

  ja::VisibilityManifest manifest;
  manifest.generation = 15;
  manifest.grants.push_back(rule(1310, ja::View::Old, ja::Span::fixed(0, 4)));
  manifest.grants.push_back(rule(1311, ja::View::New, ja::Span::fixed(0, 4)));
  const ja::SignedManifest signed_manifest_value = signed_manifest(manifest);

  ja::AdmissionProof proof;
  proof.policy_generation = 15;
  proof.tags = {
      ja::TagClass{1320, ja::View::Old, 1, 1, 10},
      ja::TagClass{1321, ja::View::Old, 1, 2, 20},
      ja::TagClass{1322, ja::View::New, 101, 101, 10},
      ja::TagClass{1323, ja::View::New, 102, 102, 20},
  };
  for (const ja::View view : {ja::View::Old, ja::View::New}) {
    proof.lengths.push_back(ja::LengthClass{1330, view, 10, 4, 4});
    proof.lengths.push_back(ja::LengthClass{1331, view, 20, 4, 4});
  }

  CHECK(ja::JointAdmission::verify(old_layout, new_layout,
                                   signed_manifest_value, proof,
                                   verify_manifest)
            .has_value());
}

void test_runtime_rejects_nonconforming_messages() {
  ja::Layout layout =
      fixed_layout(150, 4,
                   {field(1, 1501, ja::Span::fixed(0, 4))});
  ja::VisibilityManifest manifest;
  manifest.generation = 16;
  manifest.grants.push_back(rule(1510, ja::View::Old, ja::Span::fixed(0, 4)));
  manifest.grants.push_back(rule(1511, ja::View::New, ja::Span::fixed(0, 4)));

  ja::JointAdmission admission(verify_manifest);
  ja::AdmissionOutcome outcome =
      admission.admit(layout, layout, signed_manifest(manifest));
  CHECK(outcome.handle.has_value());
  if (!outcome.handle.has_value()) {
    return;
  }
  std::vector<std::uint8_t> bytes = {1, 2, 3, 4};
  CHECK(!outcome.handle->read(ja::View::Old, 1,
                              ja::Message{bytes.data(), 3, 0})
             .has_value());
  CHECK(!outcome.handle->read(ja::View::Old, 1,
                              ja::Message{nullptr, 4, 0})
             .has_value());
  CHECK(!outcome.handle->read(ja::View::Old, 2,
                              ja::Message{bytes.data(), 4, 0})
             .has_value());
}

}  // namespace

int main() {
  test_sha256();
  test_shared_object_visibility();
  test_variable_fragments_and_large_lengths();
  test_discriminant_rename();
  test_same_raw_tag_must_have_same_meaning();
  test_revocation_and_signature();
  test_alias_cannot_expose_hidden_bytes();
  test_overlapping_interpretations();
  test_overlapping_proof_classes();
  test_runtime_rejects_nonconforming_messages();

  if (failures != 0) {
    std::cerr << failures << " test check(s) failed\n";
    return EXIT_FAILURE;
  }
  std::cout << "all joint admission tests passed\n";
  return EXIT_SUCCESS;
}
