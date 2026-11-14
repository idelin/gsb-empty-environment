#pragma once

#include <cstddef>
#include <cstdint>
#include <initializer_list>
#include <set>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

struct ByteRange {
    std::uint64_t offset = 0;
    std::uint64_t length = 0;

    constexpr std::uint64_t end() const noexcept { return offset + length; }
    constexpr bool contains(std::uint64_t position) const noexcept {
        return position >= offset && position < end();
    }
    friend constexpr bool operator==(const ByteRange&, const ByteRange&) = default;
};

using ByteSpan = ByteRange;
using SignedByteRange = ByteRange;

enum class Decision {
    Valid,
    InvalidSignatureScope,
    UnauthenticatedIncrementalUpdate,
    MalformedDocument,
    NoSignatureRange,
};

enum class RevisionKind {
    Original,
    Incremental,
};

enum class XrefEntryType {
    Free = 0,
    InUse = 1,
    Compressed = 2,
};

enum class ChangeKind {
    Text,
    Page,
    Annotation,
    FormValue,
    EmbeddedFile,
    DocumentStructure,
    Signature,
    Metadata,
    Other,
};

struct RevisionInfo {
    std::size_t index = 0;
    RevisionKind kind = RevisionKind::Original;
    std::uint64_t begin = 0;
    std::uint64_t end = 0;
    bool covered_by_signature = false;
    double covered_fraction = 0.0;
    bool linearized = false;
    std::size_t xref_sections = 0;
    std::size_t object_versions = 0;
};

struct ObjectRevisionInfo {
    std::uint32_t object_number = 0;
    std::uint16_t generation = 0;
    std::size_t revision = 0;
    XrefEntryType xref_type = XrefEntryType::InUse;
    std::uint64_t file_offset = 0;
    std::uint64_t byte_length = 0;
    bool compressed = false;
    std::uint32_t object_stream = 0;
    std::uint32_t index_in_object_stream = 0;
    bool listed_in_xref = true;
    bool deleted = false;
    bool reused_object_number = false;
    bool covered_by_signature = false;
    std::string type;
    std::string subtype;
    std::vector<std::string> impacts;
};

struct Change {
    ChangeKind kind = ChangeKind::Other;
    std::size_t revision = 0;
    std::uint32_t object_number = 0;
    std::string category;
    std::string description;
    std::string before;
    std::string after;
    std::string subtype;
    std::string annotation_name;
    bool hidden = false;
    bool covered_by_signature = false;
    bool allowed = false;
};

struct Policy {
    bool allow_annotation_increments = false;
    bool allow_hidden_annotations = false;
    bool allow_attachment_annotations = false;
    bool allow_countersignatures = false;
    bool allow_form_value_changes = false;
    bool allow_text_changes = false;
    bool allow_page_changes = false;
    bool allow_document_structure_changes = false;
    bool allow_embedded_file_changes = false;
    bool allow_unknown_incremental_objects = false;
    bool derive_from_document = true;
    bool require_original_revision_coverage = true;
    bool ignore_trailing_whitespace = true;

    std::set<std::string> allowed_annotation_subtypes;
    std::set<std::string> allowed_annotation_names;
    std::set<std::uint32_t> allowed_object_numbers;

    static Policy pure_annotations(std::initializer_list<std::string> subtypes = {});
    static Policy countersigning();
};

struct Assessment {
    Decision decision = Decision::MalformedDocument;
    bool signature_covers_original = false;
    bool signature_covers_entire_document = false;
    bool has_incremental_updates = false;
    std::uint64_t file_size = 0;
    std::uint64_t original_revision_size = 0;
    std::vector<ByteRange> signed_ranges;
    std::vector<ByteRange> allowed_holes;
    std::vector<RevisionInfo> revisions;
    std::vector<ObjectRevisionInfo> objects;
    std::vector<Change> changes;
    std::vector<std::string> violations;
    std::string reason;
    std::string visible_difference_summary;

    bool valid() const noexcept { return decision == Decision::Valid; }
    bool ok() const noexcept { return valid(); }
    explicit operator bool() const noexcept { return valid(); }
    std::string to_json() const;
};

using RevisionAssessment = Assessment;
using SignatureAssessment = Assessment;
using IncrementalUpdatePolicy = Policy;

Assessment assess(std::string_view pdf,
                  std::span<const ByteRange> signed_ranges,
                  const Policy& policy = {});
Assessment assess(std::string_view pdf,
                  std::initializer_list<ByteRange> signed_ranges,
                  const Policy& policy = {});
Assessment assess(std::string_view pdf,
                  const std::vector<ByteRange>& signed_ranges,
                  const Policy& policy = {});
Assessment assess(std::string_view pdf,
                  const ByteRange& signed_range,
                  const Policy& policy = {});
Assessment assess(std::string_view pdf, const Policy& policy = {});
Assessment assess_original_revision_only(std::string_view pdf, const Policy& policy = {});

std::vector<ByteRange> extract_signature_byte_ranges(std::string_view pdf);

std::string to_string(Decision decision);
std::string to_string(RevisionKind kind);
std::string to_string(XrefEntryType type);
std::string to_string(ChangeKind kind);

class RevisionOverrideDetector {
public:
    explicit RevisionOverrideDetector(Policy policy = {});

    Assessment assess(std::string_view pdf, std::span<const ByteRange> signed_ranges) const;
    Assessment assess(std::string_view pdf, const Policy& policy) const;
    Assessment assess(std::string_view pdf) const;

    const Policy& policy() const noexcept { return policy_; }

private:
    Policy policy_;
};

inline Assessment detect_revision_override(std::string_view pdf,
                                           std::span<const ByteRange> signed_ranges,
                                           const Policy& policy = {}) {
    return assess(pdf, signed_ranges, policy);
}

inline Assessment check_revision_override(std::string_view pdf,
                                          std::span<const ByteRange> signed_ranges,
                                          const Policy& policy = {}) {
    return assess(pdf, signed_ranges, policy);
}

inline Assessment assess_pdf(std::string_view pdf,
                             std::span<const ByteRange> signed_ranges,
                             const Policy& policy = {}) {
    return assess(pdf, signed_ranges, policy);
}

inline Assessment analyze_revision_override(std::string_view pdf,
                                            std::span<const ByteRange> signed_ranges,
                                            const Policy& policy = {}) {
    return assess(pdf, signed_ranges, policy);
}

inline Assessment verify_signature_scope(std::string_view pdf,
                                         std::span<const ByteRange> signed_ranges,
                                         const Policy& policy = {}) {
    return assess(pdf, signed_ranges, policy);
}

}  // namespace netsec
