#include "pe_audit/pe_audit.h"

#include <cstdio>
#include <cstdlib>
#include <cstdint>
#include <cstring>
#include <fstream>
#include <iostream>
#include <string>
#include <utility>
#include <vector>

namespace {

constexpr std::uint32_t kImageBaseLow = 0x00400000;
constexpr std::uint32_t kRdataRva = 0x2000;
constexpr std::uint32_t kDataRva = 0x3000;
constexpr std::uint32_t kRelocRva = 0x4000;
constexpr std::uint32_t kDelayRva = 0x2000;
constexpr std::uint32_t kLoadConfigRva = 0x2200;
constexpr std::uint32_t kNameRva = 0x2100;
constexpr std::uint32_t kIatRva = 0x3010;
constexpr std::uint32_t kIntRva = 0x3020;
constexpr std::uint32_t kBoundIatRva = 0x3030;
constexpr std::uint32_t kGuardBits = 0x1100;

class PeBuilder {
public:
    explicit PeBuilder(std::uint64_t image_base = 0x140000000ULL,
                       std::uint32_t guard_flags = kGuardBits) {
        bytes_.resize(0xE00, 0);
        put_u16(0, 0x5A4D);
        put_u32(0x3C, 0x80);
        put_u32(0x80, 0x00004550);
        put_u16(0x84, 0x8664);
        put_u16(0x86, 4);
        put_u16(0x94, 0x00F0);
        put_u16(0x98, 0x020B);
        put_u64(0x98 + 24, image_base);
        put_u32(0x98 + 32, 0x1000);
        put_u32(0x98 + 36, 0x200);
        put_u32(0x98 + 56, 0x5000);
        put_u32(0x98 + 60, 0x400);
        put_u32(0x98 + 108, 16);

        add_section(0x188, ".text", 0x1000, 0x200, 0x400, 0x60000020);
        add_section(0x1B0, ".rdata", 0x2000, 0x400, 0x600, 0x40000040);
        add_section(0x1D8, ".data", 0x3000, 0x200, 0xA00, 0xC0000040);
        add_section(0x200, ".reloc", 0x4000, 0x200, 0xC00, 0x42000040);

        set_directory(13, kDelayRva, 0x40);
        set_directory(10, kLoadConfigRva, 0x148);
        put_u32(file_offset(kLoadConfigRva), 0x148);
        put_u32(file_offset(kLoadConfigRva + 144), guard_flags);

        put_u32(file_offset(kDelayRva), 1);
        put_u32(file_offset(kDelayRva + 4), kNameRva);
        put_u32(file_offset(kDelayRva + 12), kIatRva);
        put_u32(file_offset(kDelayRva + 16), kIntRva);
        put_u32(file_offset(kDelayRva + 20), kBoundIatRva);
        write_string(kNameRva, "KERNEL32.dll");
        set_relocations({0xA050});
    }

    std::vector<std::uint8_t>& bytes() { return bytes_; }
    const std::vector<std::uint8_t>& bytes() const { return bytes_; }

    std::uint64_t file_offset(std::uint32_t rva) const {
        if (rva >= kRelocRva) {
            return 0xC00 + static_cast<std::uint64_t>(rva - kRelocRva);
        }
        if (rva >= kDataRva) {
            return 0xA00 + static_cast<std::uint64_t>(rva - kDataRva);
        }
        if (rva >= kRdataRva) {
            return 0x600 + static_cast<std::uint64_t>(rva - kRdataRva);
        }
        return rva;
    }

    void put_u16(std::uint64_t offset, std::uint16_t value) {
        bytes_[offset] = static_cast<std::uint8_t>(value & 0xFFU);
        bytes_[offset + 1] =
            static_cast<std::uint8_t>((value >> 8) & 0xFFU);
    }

    void put_u32(std::uint64_t offset, std::uint32_t value) {
        for (std::uint32_t index = 0; index < 4; ++index) {
            bytes_[offset + index] = static_cast<std::uint8_t>(
                (value >> (index * 8U)) & 0xFFU);
        }
    }

    void put_u64(std::uint64_t offset, std::uint64_t value) {
        for (std::uint32_t index = 0; index < 8; ++index) {
            bytes_[offset + index] = static_cast<std::uint8_t>(
                (value >> (index * 8U)) & 0xFFU);
        }
    }

    void set_directory(std::uint32_t index, std::uint32_t rva,
                       std::uint32_t size) {
        const std::uint64_t entry = 0x108 + static_cast<std::uint64_t>(index) * 8;
        put_u32(entry, rva);
        put_u32(entry + 4, size);
    }

    void set_import_directory(std::uint32_t rva, std::uint32_t size) {
        set_directory(1, rva, size);
    }

    void set_guard_flags(std::uint32_t flags) {
        put_u32(file_offset(kLoadConfigRva + 144), flags);
    }

    void set_delay_attributes(std::uint32_t attributes) {
        put_u32(file_offset(kDelayRva), attributes);
    }

    void set_delay_field(std::uint32_t field_index, std::uint32_t value) {
        put_u32(file_offset(kDelayRva) +
                    static_cast<std::uint64_t>(field_index) * 4,
                value);
    }

    void write_string(std::uint32_t rva, const std::string& value) {
        const std::uint64_t offset = file_offset(rva);
        for (std::size_t index = 0; index < value.size(); ++index) {
            bytes_[offset + index] =
                static_cast<std::uint8_t>(value[index]);
        }
        bytes_[offset + value.size()] = 0;
    }

    void set_relocations(std::vector<std::uint16_t> entries,
                         std::uint32_t block_rva = kDataRva) {
        if (entries.size() % 2 != 0) {
            entries.push_back(0);
        }
        const std::uint32_t block_size =
            8 + static_cast<std::uint32_t>(entries.size()) * 2;
        const std::uint64_t offset = file_offset(kRelocRva);
        put_u32(offset, block_rva);
        put_u32(offset + 4, block_size);
        for (std::size_t index = 0; index < entries.size(); ++index) {
            put_u16(offset + 8 + index * 2, entries[index]);
        }
        set_directory(15, kRelocRva, block_size);
    }

    void set_block_header(std::uint32_t block_rva, std::uint32_t block_size,
                          std::uint32_t directory_size) {
        const std::uint64_t offset = file_offset(kRelocRva);
        put_u32(offset, block_rva);
        put_u32(offset + 4, block_size);
        set_directory(15, kRelocRva, directory_size);
    }

private:
    void add_section(std::uint64_t header_offset, const char* name,
                     std::uint32_t virtual_address,
                     std::uint32_t virtual_size, std::uint32_t raw_pointer,
                     std::uint32_t characteristics) {
        const std::size_t name_length = std::strlen(name);
        for (std::size_t index = 0; index < name_length && index < 8; ++index) {
            bytes_[header_offset + index] =
                static_cast<std::uint8_t>(name[index]);
        }
        put_u32(header_offset + 8, virtual_size);
        put_u32(header_offset + 12, virtual_address);
        put_u32(header_offset + 16, virtual_size);
        put_u32(header_offset + 20, raw_pointer);
        put_u32(header_offset + 36, characteristics);
    }

    std::vector<std::uint8_t> bytes_;
};

bool expect(const pe_audit::Report& report, pe_audit::IssueCode code) {
    return report.contains(code);
}

bool test_file_api() {
    const char* temporary_directory = std::getenv("TMPDIR");
    if (temporary_directory == nullptr) {
        return true;
    }
    std::string path(temporary_directory);
    if (!path.empty() && path.back() != '/') {
        path.push_back('/');
    }
    path += "pe_audit_valid_sample.bin";

    const PeBuilder builder;
    std::ofstream output(path, std::ios::binary | std::ios::trunc);
    if (!output) {
        return false;
    }
    output.write(
        reinterpret_cast<const char*>(builder.bytes().data()),
        static_cast<std::streamsize>(builder.bytes().size()));
    output.close();
    if (!output) {
        return false;
    }

    const pe_audit::Report report = pe_audit::audit_file(path);
    const int removed = std::remove(path.c_str());
    return removed == 0 && report.ok() && report.delay_imports.size() == 1;
}

bool test_valid_pe() {
    const PeBuilder builder;
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    if (!report.ok()) {
        for (const pe_audit::Finding& finding : report.findings) {
            std::cerr << "unexpected: "
                      << pe_audit::issue_code_name(finding.code) << " "
                      << finding.detail << '\n';
        }
        return false;
    }
    if (report.relocations.size() != 1 ||
        report.relocations[0].target_rva != 0x3050 ||
        report.relocations[0].width != 8) {
        return false;
    }
    if (report.delay_imports.size() != 1 ||
        report.delay_imports[0].dll_name != "KERNEL32.dll" ||
        report.delay_imports[0].address_mode !=
            pe_audit::DelayAddressMode::Rva) {
        return false;
    }
    return report.load_config.guard_flags == kGuardBits &&
           report.guard_cf_present && report.delay_iat_protected &&
           report.delay_iat_writable;
}

bool test_legacy_va_descriptor() {
    PeBuilder builder(kImageBaseLow);
    builder.set_delay_attributes(0);
    builder.set_delay_field(1, kImageBaseLow + kNameRva);
    builder.set_delay_field(3, kImageBaseLow + kIatRva);
    builder.set_delay_field(4, kImageBaseLow + kIntRva);
    builder.set_delay_field(5, kImageBaseLow + kBoundIatRva);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return report.ok() && report.delay_imports.size() == 1 &&
           report.delay_imports[0].address_mode ==
               pe_audit::DelayAddressMode::LegacyVa &&
           report.delay_imports[0].iat_rva == kIatRva;
}

bool test_legacy_va_below_image_base_rejected() {
    PeBuilder builder(kImageBaseLow);
    builder.set_delay_attributes(0);
    builder.set_delay_field(3, kIatRva);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() && expect(report, pe_audit::IssueCode::DelayIatInvalid);
}

bool test_reloc_target_outside_section() {
    PeBuilder builder;
    builder.set_relocations({0xAFF8});
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::RelocTargetOutsideSection);
}

bool test_reloc_write_overlap() {
    PeBuilder builder;
    builder.set_relocations({0xA050, 0xA054});
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::RelocWriteOverlap);
}

bool test_zero_sized_block_rejected() {
    PeBuilder builder;
    builder.set_block_header(kDataRva, 0, 12);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::RelocBlockTooSmall);
}

bool test_misaligned_block_rejected() {
    PeBuilder builder;
    builder.set_block_header(kDataRva, 13, 16);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::RelocBlockUnaligned);
}

bool test_misaligned_directory_rejected() {
    PeBuilder builder;
    builder.set_directory(15, kRelocRva, 13);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::RelocDirectoryUnaligned);
}

bool test_block_out_of_bounds_rejected() {
    PeBuilder builder;
    builder.set_block_header(kDataRva, 20, 12);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::RelocBlockOutOfBounds);
}

bool test_high_risk_relocation_type_rejected() {
    PeBuilder builder;
    builder.set_relocations({0x4050});
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::RelocTypeUnsupported);
}

bool test_reloc_into_headers_rejected() {
    PeBuilder builder;
    builder.set_relocations({0xA100}, 0);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::RelocTargetInHeaders);
}

bool test_reloc_into_import_directory_rejected() {
    PeBuilder builder;
    builder.set_import_directory(0x2300, 0x10);
    builder.set_relocations({0xA304}, 0x2000);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report,
                  pe_audit::IssueCode::RelocTargetInImportDirectory);
}

bool test_reloc_into_delay_import_directory_rejected() {
    PeBuilder builder;
    builder.set_relocations({0xA004}, 0x2000);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report,
                  pe_audit::IssueCode::RelocTargetInImportDirectory);
}

bool test_reloc_at_section_end_is_valid() {
    PeBuilder builder;
    builder.set_relocations({0xA1F8});
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return report.ok() && report.relocations.size() == 1 &&
           report.relocations[0].target_rva == 0x31F8;
}

bool test_unprotected_writable_delay_iat_rejected() {
    PeBuilder builder(0x140000000ULL, 0x0100);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report,
                  pe_audit::IssueCode::DelayIatProtectionMissing) &&
           report.delay_iat_writable && !report.delay_iat_protected;
}

bool test_read_only_delay_iat_needs_no_protect_bit() {
    PeBuilder builder(0x140000000ULL, 0x0100);
    builder.put_u32(0x1D8 + 36, 0x40000040);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return report.ok() && !report.delay_iat_writable &&
           report.guard_cf_present;
}

bool test_missing_guard_cf_rejected() {
    PeBuilder builder(0x140000000ULL, 0x1000);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::GuardCfMissing) &&
           !report.guard_cf_present;
}

bool test_missing_load_config_rejected() {
    PeBuilder builder;
    builder.set_directory(10, 0, 0);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::GuardCfMissing) &&
           expect(report,
                  pe_audit::IssueCode::DelayIatProtectionMissing);
}

bool test_relative_dll_path_rejected() {
    PeBuilder builder;
    builder.write_string(kNameRva, "subdir\\KERNEL32.dll");
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::DelayNamePathInvalid);
}

bool test_unlisted_dll_rejected() {
    PeBuilder builder;
    builder.write_string(kNameRva, "evil.dll");
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::DelayNameNotAllowed);
}

bool test_unlisted_dll_still_gets_iat_risk_check() {
    PeBuilder builder(0x140000000ULL, 0x0100);
    builder.write_string(kNameRva, "evil.dll");
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::DelayNameNotAllowed) &&
           expect(report,
                  pe_audit::IssueCode::DelayIatProtectionMissing);
}

bool test_allowlist_extension() {
    PeBuilder builder;
    builder.write_string(kNameRva, "evil.dll");
    pe_audit::AuditOptions options;
    options.additional_allowed_dlls.push_back("evil.dll");
    const pe_audit::Report report =
        pe_audit::audit_bytes(builder.bytes(), options);
    return report.ok();
}

bool test_unterminated_dll_name_rejected() {
    PeBuilder builder;
    builder.set_delay_field(1, 0x23F0);
    for (std::uint32_t index = 0; index < 16; ++index) {
        builder.bytes()[builder.file_offset(0x23F0) + index] = 'A';
    }
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::DelayNameUnterminated);
}

bool test_invalid_bound_iat_rejected() {
    PeBuilder builder;
    builder.set_delay_field(5, 0x6000);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::DelayBoundIatInvalid);
}

bool test_directory_integer_wrap_rejected() {
    PeBuilder builder;
    builder.set_directory(15, 0xFFFFFFF8U, 0x20);
    const pe_audit::Report report = pe_audit::audit_bytes(builder.bytes());
    return !report.ok() &&
           expect(report, pe_audit::IssueCode::RelocDirectoryInvalid);
}

bool test_mutations_terminate() {
    const std::vector<std::uint8_t> seed = PeBuilder().bytes();
    std::uint64_t state = 0x9E3779B97F4A7C15ULL;
    for (std::uint32_t iteration = 0; iteration < 2000; ++iteration) {
        state ^= state << 7;
        state ^= state >> 9;
        state ^= state << 8;
        std::vector<std::uint8_t> sample = seed;
        const std::size_t index =
            static_cast<std::size_t>(state % sample.size());
        sample[index] = static_cast<std::uint8_t>(
            sample[index] ^ static_cast<std::uint8_t>(state >> 24));
        const pe_audit::Report report = pe_audit::audit_bytes(sample);
        (void)report;
    }
    return true;
}

}  // namespace

int main() {
    const std::vector<std::pair<const char*, bool (*)()>> tests = {
        {"file_api", test_file_api},
        {"valid_pe", test_valid_pe},
        {"legacy_va_descriptor", test_legacy_va_descriptor},
        {"legacy_va_below_image_base",
         test_legacy_va_below_image_base_rejected},
        {"reloc_target_outside_section", test_reloc_target_outside_section},
        {"reloc_write_overlap", test_reloc_write_overlap},
        {"zero_sized_block", test_zero_sized_block_rejected},
        {"misaligned_block", test_misaligned_block_rejected},
        {"misaligned_directory", test_misaligned_directory_rejected},
        {"block_out_of_bounds", test_block_out_of_bounds_rejected},
        {"high_risk_relocation_type",
         test_high_risk_relocation_type_rejected},
        {"reloc_into_headers", test_reloc_into_headers_rejected},
        {"reloc_into_import_directory",
         test_reloc_into_import_directory_rejected},
        {"reloc_into_delay_import_directory",
         test_reloc_into_delay_import_directory_rejected},
        {"reloc_at_section_end", test_reloc_at_section_end_is_valid},
        {"unprotected_writable_delay_iat",
         test_unprotected_writable_delay_iat_rejected},
        {"read_only_delay_iat", test_read_only_delay_iat_needs_no_protect_bit},
        {"missing_guard_cf", test_missing_guard_cf_rejected},
        {"missing_load_config", test_missing_load_config_rejected},
        {"relative_dll_path", test_relative_dll_path_rejected},
        {"unlisted_dll", test_unlisted_dll_rejected},
        {"unlisted_dll_iat_risk",
         test_unlisted_dll_still_gets_iat_risk_check},
        {"allowlist_extension", test_allowlist_extension},
        {"unterminated_dll_name", test_unterminated_dll_name_rejected},
        {"invalid_bound_iat", test_invalid_bound_iat_rejected},
        {"directory_integer_wrap", test_directory_integer_wrap_rejected},
        {"mutations_terminate", test_mutations_terminate},
    };

    std::uint32_t failures = 0;
    for (const auto& test : tests) {
        if (!test.second()) {
            std::cerr << "FAILED: " << test.first << '\n';
            ++failures;
        }
    }
    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    std::cout << tests.size() << " tests passed\n";
    return 0;
}
