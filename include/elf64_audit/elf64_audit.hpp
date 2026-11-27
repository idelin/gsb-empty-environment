#ifndef ELF64_AUDIT_ELF64_AUDIT_HPP
#define ELF64_AUDIT_ELF64_AUDIT_HPP

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace elf64_audit {

enum class Severity {
    Info,
    Warning,
    Error
};

enum class Code {
    FileIoError,
    TruncatedInput,
    InvalidMagic,
    UnsupportedClass,
    UnsupportedEndianness,
    UnsupportedVersion,
    UnsupportedType,
    UnsupportedMachine,
    MalformedElfHeader,
    MalformedProgramHeaders,
    MalformedSectionHeaders,
    OutOfBounds,
    IntegerOverflow,
    MisalignedStructure,
    InvalidConfiguration,
    NoLoadSegment,
    OverlappingLoadSegments,
    MissingDynamicSegment,
    MalformedDynamicTable,
    MalformedStringTable,
    MalformedRelocationTable,
    MalformedSymbolTable,
    UnsafeLoadMapping,
    MissingRelro,
    RelroCoverageIncomplete,
    RelroMetadataUnavailable,
    BindNowMissing,
    ExecutableStack,
    MissingGnuStack,
    EmptyRuntimePath,
    NonAbsoluteRuntimePath,
    UnboundedOriginPath,
    WritableIfuncResolver,
    UnmappedIfuncResolver,
    WritableIrelativeResolver,
    UnmappedIrelativeResolver
};

enum class RelroStatus {
    None,
    Partial,
    Full
};

struct Finding {
    Severity severity = Severity::Error;
    Code code = Code::MalformedElfHeader;
    std::string message;
    std::uint64_t file_offset = 0;
    std::uint64_t virtual_address = 0;
};

struct DynamicEntry {
    std::int64_t tag = 0;
    std::uint64_t value = 0;
};

struct RuntimePath {
    std::int64_t tag = 0;
    std::string value;
    std::vector<std::string> segments;
    bool safe = true;
};

struct AuditOptions {
    std::uint64_t page_size = 4096;
};

struct AuditReport {
    bool parsed = false;
    bool admission_passed = false;
    RelroStatus relro = RelroStatus::None;
    bool has_pt_gnu_relro = false;
    bool immediate_binding = false;
    bool has_rpath = false;
    bool has_runpath = false;
    bool executable_stack = false;
    bool has_gnu_stack = false;
    bool unsafe_ifunc = false;
    std::vector<DynamicEntry> dynamic_entries;
    std::vector<RuntimePath> runtime_paths;
    std::vector<Finding> findings;

    bool has_finding(Code code) const noexcept;
    std::size_t error_count() const noexcept;
    std::size_t warning_count() const noexcept;
};

AuditReport audit_elf64(
    const void* data,
    std::size_t size,
    const AuditOptions& options = AuditOptions{});

AuditReport audit_elf64_file(
    const std::string& path,
    const AuditOptions& options = AuditOptions{});

const char* to_string(Code code) noexcept;
const char* to_string(Severity severity) noexcept;
const char* to_string(RelroStatus status) noexcept;

}  // namespace elf64_audit

#endif
