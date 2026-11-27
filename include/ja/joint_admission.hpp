#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <utility>
#include <vector>

namespace ja {

using Obligation = std::uint32_t;
using FieldId = std::uint32_t;
using CaseId = std::uint32_t;
using SharedId = std::uint32_t;
using SemanticId = std::uint32_t;
using Generation = std::uint64_t;

inline constexpr std::uint64_t kMaxMessageLength = std::uint64_t{1} << 40;
inline constexpr CaseId kDefaultCase = 1;

enum class View : std::uint8_t {
  Old = 0,
  New = 1,
};

// value(L) = constant + per_length * L
struct Linear {
  std::int64_t constant = 0;
  std::int64_t per_length = 0;

  static constexpr Linear fixed(std::uint64_t value) {
    return Linear{static_cast<std::int64_t>(value), 0};
  }
  static constexpr Linear length() { return Linear{0, 1}; }
};

// A half-open byte interval [begin(L), end(L)).  When
// clamp_end_to_length is true, the effective end is min(end(L), L).
struct Span {
  Linear begin;
  Linear end;
  bool clamp_end_to_length = false;

  static Span fixed(std::uint64_t offset, std::uint64_t size) {
    return Span{Linear::fixed(offset),
                Linear::fixed(offset + size),
                false};
  }

  // [offset, L)
  static Span to_end(std::uint64_t offset) {
    return Span{Linear::fixed(offset), Linear::length(), false};
  }

  // [offset, min(offset + max_size, L))
  static Span bounded_tail(std::uint64_t offset, std::uint64_t max_size) {
    return Span{Linear::fixed(offset),
                Linear::fixed(offset + max_size),
                true};
  }
};

struct Field {
  FieldId id = 0;
  Obligation obligation = 0;
  Span span;

  // Non-zero identifies the shared subobject whose bytes this field aliases.
  SharedId shared_object = 0;

  // Non-zero makes an explicit semantic claim about the field bytes.  Two
  // overlapping non-zero claims in the same view/case must agree.
  SemanticId interpretation = 0;
};

struct SharedObject {
  SharedId id = 0;
  Obligation obligation = 0;
  Span span;
  SemanticId interpretation = 0;
};

struct TagBinding {
  Obligation obligation = 0;
  std::uint64_t first = 0;
  std::uint64_t last = 0;
  CaseId case_id = kDefaultCase;
};

struct Discriminant {
  Obligation obligation = 0;
  Span span = Span::fixed(0, 1);
  std::uint8_t width = 1;
  std::uint64_t domain_first = 0;
  std::uint64_t domain_last = 255;
  std::vector<TagBinding> bindings;
};

struct Arm {
  CaseId case_id = kDefaultCase;
  Obligation obligation = 0;
  std::vector<Field> fields;
};

struct NewFragment {
  Obligation obligation = 0;
  FieldId new_field = 0;

  // Offset from the new field's dynamic begin.  A missing size means "to
  // the end of the new field".
  std::uint64_t offset = 0;
  std::optional<std::uint64_t> size;
};

struct Compatibility {
  Obligation obligation = 0;
  FieldId old_field = 0;
  std::vector<NewFragment> new_fragments;
};

struct Layout {
  Obligation obligation = 0;
  std::uint64_t min_length = 0;
  std::uint64_t max_length = 0;
  std::optional<Discriminant> discriminant;
  std::vector<SharedObject> shared_objects;
  std::vector<Field> common_fields;
  std::vector<Arm> arms;
  std::vector<Compatibility> compatibility;
};

struct PermissionRule {
  Obligation obligation = 0;
  View view = View::Old;
  Span span;
};

struct VisibilityManifest {
  Generation generation = 0;
  std::vector<PermissionRule> grants;
  std::vector<PermissionRule> denials;
};

using Signature = std::vector<std::uint8_t>;

struct SignedManifest {
  VisibilityManifest manifest;
  Signature signature;
  Obligation signature_obligation = 0;
};

// A witness that partitions the discriminant and length domains into finite
// categories.  It is deliberately only a partition: the verifier derives all
// interval and compatibility facts itself.
struct TagClass {
  Obligation obligation = 0;
  View view = View::Old;
  std::uint64_t first = 0;
  std::uint64_t last = 0;
  CaseId case_id = kDefaultCase;
};

struct LengthClass {
  Obligation obligation = 0;
  View view = View::Old;
  CaseId case_id = kDefaultCase;
  std::uint64_t first = 0;
  std::uint64_t last = 0;
};

struct AdmissionProof {
  Generation policy_generation = 0;
  std::vector<TagClass> tags;
  std::vector<LengthClass> lengths;
};

// The only failure detail exposed by this component.
struct Failure {
  Obligation layout = 0;
  Obligation permission = 0;

  friend bool operator==(const Failure& lhs, const Failure& rhs) {
    return lhs.layout == rhs.layout && lhs.permission == rhs.permission;
  }
  friend bool operator!=(const Failure& lhs, const Failure& rhs) {
    return !(lhs == rhs);
  }
};

struct Interval {
  std::uint64_t begin = 0;
  std::uint64_t end = 0;

  friend bool operator==(const Interval& lhs, const Interval& rhs) {
    return lhs.begin == rhs.begin && lhs.end == rhs.end;
  }
  friend bool operator!=(const Interval& lhs, const Interval& rhs) {
    return !(lhs == rhs);
  }
};

struct ByteView {
  const std::uint8_t* data = nullptr;
  std::uint64_t size = 0;
};

struct Message {
  const std::uint8_t* bytes = nullptr;
  std::uint64_t size = 0;
  std::uint64_t discriminant = 0;
};

using Digest = std::array<std::uint8_t, 32>;
using ManifestVerifier =
    std::function<bool(const VisibilityManifest&, const Signature&)>;

class ReadOnlyHandle {
 public:
  ReadOnlyHandle(const ReadOnlyHandle&) = default;
  ReadOnlyHandle& operator=(const ReadOnlyHandle&) = default;
  ReadOnlyHandle(ReadOnlyHandle&&) noexcept = default;
  ReadOnlyHandle& operator=(ReadOnlyHandle&&) noexcept = default;

  bool valid() const noexcept;
  const Digest& old_layout_digest() const noexcept;
  const Digest& new_layout_digest() const noexcept;
  Generation policy_generation() const noexcept;

  // Resolve a field without touching message bytes.
  std::optional<Interval> interval(View view,
                                   FieldId field,
                                   std::uint64_t message_length,
                                   std::uint64_t discriminant) const;

  // The returned bytes are const and borrow the caller's buffer.  Nullopt
  // means the field is absent, the message does not conform to the frozen
  // layout, or the resolved interval is outside the buffer.
  std::optional<ByteView> read(View view,
                               FieldId field,
                               const Message& message) const;

 private:
  struct Impl;
  explicit ReadOnlyHandle(std::shared_ptr<const Impl> impl);

  std::shared_ptr<const Impl> impl_;

  friend class JointAdmission;
};

struct AdmissionOutcome {
  std::optional<ReadOnlyHandle> handle;
  Failure failure{};

  explicit operator bool() const noexcept { return handle.has_value(); }
};

class JointAdmission {
 public:
  explicit JointAdmission(ManifestVerifier verifier);

  // Verify a host-supplied compressed proof and all admission obligations.
  // No handle is produced by this entry point.
  static std::optional<Failure> verify(
      const Layout& old_layout,
      const Layout& new_layout,
      const SignedManifest& signed_manifest,
      const AdmissionProof& proof,
      const ManifestVerifier& verifier);

  // If proof is null, the component synthesizes the canonical partition.
  AdmissionOutcome admit(const Layout& old_layout,
                         const Layout& new_layout,
                         const SignedManifest& signed_manifest,
                         const AdmissionProof* proof = nullptr) const;

 private:
  ManifestVerifier verifier_;
};

std::vector<std::uint8_t> canonical_layout(const Layout& layout);
std::vector<std::uint8_t> canonical_manifest(
    const VisibilityManifest& manifest);

Digest layout_digest(const Layout& layout);
Digest sha256(const std::uint8_t* data, std::size_t size);
Digest sha256(const std::vector<std::uint8_t>& data);

}  // namespace ja
