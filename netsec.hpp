#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

enum class PolicyDecision {
    allow,
    reject,
};

enum class Severity {
    error,
    warning,
};

enum class FindingCode {
    malformed_header,
    unsupported_elf,
    malformed_program_headers,
    malformed_dynamic_segment,
    malformed_string_table,
    malformed_symbol_table,
    malformed_hash_table,
    malformed_relocation_table,
    truncated_table,
    integer_overflow,
    overlapping_load_segments,
    ambiguous_load_permissions,
    rwx_load_segment,
    text_relocation,
    unknown_relocation_type,
    unexpected_relocation_symbol,
    invalid_relocation_target,
    relocation_crosses_region,
    relocation_overlaps_dynamic_metadata,
    lazy_binding,
    writable_got_relocation,
    writable_function_pointer_relocation,
    writable_constructor_array,
    invalid_relro,
    relro_overflow,
    duplicate_dynamic_tag,
    unsupported_dynamic_feature,
    dynamic_after_null,
};

std::string_view to_string(FindingCode code) noexcept;
std::string_view to_string(PolicyDecision decision) noexcept;
std::string_view to_string(Severity severity) noexcept;

struct FileWitness {
    std::uint64_t file_offset = 0;
    std::uint64_t size = 0;
    std::uint64_t virtual_address = 0;
};

using Witness = FileWitness;
using OffsetWitness = FileWitness;

struct Finding {
    FindingCode code = FindingCode::malformed_header;
    Severity severity = Severity::error;
    std::string message;

    // The table entry, program header, or relocation record that exposed the
    // problem. This is always an exact offset into the inspected file.
    FileWitness source;

    // Present when the finding is caused by a relocation. The target witness
    // is file-backed when the destination lies in p_filesz and absent when it
    // lies only in the zero-filled p_memsz portion of a load.
    std::optional<FileWitness> relocation;
    std::optional<FileWitness> target;

    // Direct mirrors of source/target for callers that do not need the richer
    // witness structure.
    std::uint64_t file_offset = 0;
    std::uint64_t file_size = 0;
    std::uint64_t virtual_address = 0;
    std::optional<std::uint64_t> target_file_offset;
};

using Issue = Finding;
using Diagnostic = Finding;

struct LoadSegmentInfo {
    std::uint64_t file_offset = 0;
    std::uint64_t virtual_address = 0;
    std::uint64_t file_size = 0;
    std::uint64_t memory_size = 0;
    std::uint64_t alignment = 0;
    std::uint32_t flags = 0;
    bool read = false;
    bool write = false;
    bool execute = false;
};

struct ElfSummary {
    std::optional<std::string> soname;
    std::vector<LoadSegmentInfo> loads;

    std::uint64_t relocation_count = 0;
    std::uint64_t jump_slot_count = 0;
    std::uint64_t glob_dat_count = 0;
    std::uint64_t copy_count = 0;
    std::uint64_t symbol_count = 0;

    bool has_dynamic_segment = false;
    bool bind_now = false;
    bool has_relro = false;
    std::uint64_t relro_start = 0;
    std::uint64_t relro_end = 0;       // Exclusive, as declared by PT_GNU_RELRO.
    std::uint64_t relro_page_start = 0; // Inclusive page-rounded mprotect range.
    std::uint64_t relro_page_end = 0;   // Exclusive page-rounded mprotect range.
};

// Both `if (report.allowed)` and `if (report.allowed())` are intentional: the
// former keeps the result easy to embed in policy code, while the latter makes
// the gate explicit at call sites.
struct DecisionFlag {
    bool value = false;

    constexpr operator bool() const noexcept { return value; }
    constexpr bool operator()() const noexcept { return value; }
};

struct PolicyResult {
    PolicyDecision decision = PolicyDecision::reject;
    DecisionFlag allowed;
    DecisionFlag rejected{true};
    std::vector<Finding> findings;
    ElfSummary summary;

    [[nodiscard]] bool is_allowed() const noexcept { return allowed.value; }
    [[nodiscard]] bool is_rejected() const noexcept { return rejected.value; }
    [[nodiscard]] const std::vector<Finding>& issues() const noexcept {
        return findings;
    }

    [[nodiscard]] std::string format() const;
};

struct PolicyOptions {
    // The loader rounds RELRO to its page size. The policy models the pages
    // that will actually be made read-only, not just the ELF virtual range.
    std::uint64_t page_size = 4096;
};

// Parses the image as data. No mapping, dlopen, dynamic loader, initializer,
// or relocation is performed by this library.
[[nodiscard]] PolicyResult analyze(std::span<const std::uint8_t> image,
                                   PolicyOptions options = {});
[[nodiscard]] inline PolicyResult analyze(const std::uint8_t* image,
                                          std::size_t size,
                                          PolicyOptions options = {}) {
    return analyze(std::span<const std::uint8_t>(image, size), options);
}
[[nodiscard]] PolicyResult analyze(std::span<const std::byte> image,
                                   PolicyOptions options = {});
[[nodiscard]] PolicyResult analyze_file(const std::string& path,
                                        PolicyOptions options = {});

// Convenient aliases for callers that model the check as a pre-load gate.
using PolicyReport = PolicyResult;
[[nodiscard]] PolicyResult check_shared_object(
    std::span<const std::uint8_t> image, PolicyOptions options = {});

[[nodiscard]] inline PolicyResult check(std::span<const std::uint8_t> image,
                                        PolicyOptions options = {}) {
    return analyze(image, options);
}

[[nodiscard]] inline PolicyResult analyze_elf(
    std::span<const std::uint8_t> image, PolicyOptions options = {}) {
    return analyze(image, options);
}

using Report = PolicyResult;
using AnalysisResult = PolicyResult;
using Result = PolicyResult;

[[nodiscard]] inline PolicyResult check_file(const std::string& path,
                                             PolicyOptions options = {}) {
    return analyze_file(path, options);
}

class ElfSecurityPolicy {
public:
    explicit ElfSecurityPolicy(PolicyOptions options = {}) : options_(options) {}

    [[nodiscard]] PolicyResult evaluate(std::span<const std::uint8_t> image) const {
        return netsec::analyze(image, options_);
    }

    [[nodiscard]] PolicyResult analyze(std::span<const std::uint8_t> image) const {
        return evaluate(image);
    }

    [[nodiscard]] PolicyResult check(std::span<const std::uint8_t> image) const {
        return evaluate(image);
    }

    [[nodiscard]] PolicyResult evaluate_file(const std::string& path) const {
        return analyze_file(path, options_);
    }

    [[nodiscard]] PolicyResult check_file(const std::string& path) const {
        return evaluate_file(path);
    }

    [[nodiscard]] PolicyOptions options() const noexcept { return options_; }

private:
    PolicyOptions options_;
};

} // namespace netsec
