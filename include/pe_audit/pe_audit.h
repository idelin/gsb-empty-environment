#ifndef PE_AUDIT_PE_AUDIT_H
#define PE_AUDIT_PE_AUDIT_H

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace pe_audit {

enum class Severity {
    Info,
    Warning,
    Error,
};

enum class IssueCode {
    FileReadError,
    FileTooSmall,
    InvalidDosHeader,
    InvalidPeSignature,
    InvalidCoffHeader,
    InvalidOptionalHeader,
    NotPe32Plus,
    InvalidDataDirectory,
    InvalidSectionTable,
    InvalidSectionLayout,
    OverlappingSections,
    ImportDirectoryInvalid,
    RelocDirectoryInvalid,
    RelocDirectoryUnaligned,
    RelocBlockTooSmall,
    RelocBlockUnaligned,
    RelocBlockOutOfBounds,
    RelocArithmeticOverflow,
    RelocTypeUnsupported,
    RelocTargetOutsideImage,
    RelocTargetOutsideSection,
    RelocTargetInHeaders,
    RelocTargetInImportDirectory,
    RelocWriteOverlap,
    DelayDirectoryInvalid,
    DelayDescriptorInvalid,
    DelayDescriptorUnterminated,
    DelayDescriptorReservedBits,
    DelayLegacyVaInvalid,
    DelayNameInvalid,
    DelayNameUnterminated,
    DelayNamePathInvalid,
    DelayNameNotAllowed,
    DelayIatInvalid,
    DelayIntInvalid,
    DelayBoundIatInvalid,
    DelayUnloadIatInvalid,
    LoadConfigInvalid,
    LoadConfigTruncated,
    GuardCfMissing,
    DelayIatProtectionMissing,
};

struct Finding {
    IssueCode code = IssueCode::FileTooSmall;
    Severity severity = Severity::Error;
    std::uint64_t rva = 0;
    std::string detail;
};

struct RelocationRecord {
    std::uint32_t block_rva = 0;
    std::uint16_t type = 0;
    std::uint16_t page_offset = 0;
    std::uint32_t target_rva = 0;
    std::uint8_t width = 0;
};

enum class DelayAddressMode {
    Rva,
    LegacyVa,
};

struct DelayImportRecord {
    std::uint32_t descriptor_rva = 0;
    DelayAddressMode address_mode = DelayAddressMode::Rva;
    std::string dll_name;
    std::uint32_t name_rva = 0;
    std::uint32_t iat_rva = 0;
    std::uint32_t int_rva = 0;
    std::uint32_t bound_iat_rva = 0;
    std::uint32_t unload_iat_rva = 0;
    bool iat_section_writable = false;
};

struct LoadConfigInfo {
    bool present = false;
    bool guard_flags_available = false;
    std::uint32_t structure_size = 0;
    std::uint32_t guard_flags = 0;
};

struct AuditOptions {
    // The built-in list is always used. These entries extend it and are compared
    // case-insensitively. Names belonging to the default list:
    // kernel32.dll, user32.dll, gdi32.dll, advapi32.dll, shell32.dll,
    // ole32.dll, oleaut32.dll, ws2_32.dll, crypt32.dll, bcrypt.dll, ntdll.dll.
    std::vector<std::string> additional_allowed_dlls;
};

const std::vector<std::string>& default_dll_allowlist();
const char* issue_code_name(IssueCode code) noexcept;
const char* severity_name(Severity severity) noexcept;

struct Report {
    std::vector<Finding> findings;
    std::vector<RelocationRecord> relocations;
    std::vector<DelayImportRecord> delay_imports;
    LoadConfigInfo load_config;
    bool guard_cf_present = false;
    bool delay_iat_protected = false;
    bool delay_iat_writable = false;

    bool ok() const noexcept;
    bool contains(IssueCode code) const noexcept;
};

Report audit_bytes(const std::uint8_t* data, std::size_t size,
                   const AuditOptions& options = AuditOptions{});
Report audit_bytes(const std::vector<std::uint8_t>& data,
                   const AuditOptions& options = AuditOptions{});
Report audit_file(const std::string& path,
                  const AuditOptions& options = AuditOptions{});

}  // namespace pe_audit

#endif
