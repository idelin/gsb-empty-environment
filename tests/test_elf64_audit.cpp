#include "elf64_audit/elf64_audit.hpp"

#include <cstddef>
#include <cstdint>
#include <iostream>
#include <string>
#include <utility>
#include <vector>

namespace {

using Bytes = std::vector<std::uint8_t>;

constexpr std::uint64_t kRxFileOffset = 0x0000U;
constexpr std::uint64_t kRxVirtualAddress = 0x0000U;
constexpr std::uint64_t kRxSize = 0x1000U;
constexpr std::uint64_t kRwFileOffset = 0x1000U;
constexpr std::uint64_t kRwVirtualAddress = 0x2000U;
constexpr std::uint64_t kRwSize = 0x2000U;
constexpr std::uint64_t kDynamicFileOffset = 0x1100U;
constexpr std::uint64_t kDynamicVirtualAddress = 0x2100U;
constexpr std::uint64_t kDynsymFileOffset = 0x1300U;
constexpr std::uint64_t kDynsymVirtualAddress = 0x2300U;
constexpr std::uint64_t kDynstrFileOffset = 0x1330U;
constexpr std::uint64_t kDynstrVirtualAddress = 0x2330U;
constexpr std::uint64_t kRelaFileOffset = 0x1400U;
constexpr std::uint64_t kRelaVirtualAddress = 0x2400U;
constexpr std::uint64_t kGotFileOffset = 0x2000U;
constexpr std::uint64_t kGotVirtualAddress = 0x3000U;
constexpr std::uint64_t kGotPltFileOffset = 0x2020U;
constexpr std::uint64_t kGotPltVirtualAddress = 0x3020U;
constexpr std::uint64_t kInitArrayFileOffset = 0x2040U;
constexpr std::uint64_t kInitArrayVirtualAddress = 0x3040U;
constexpr std::uint64_t kFiniArrayFileOffset = 0x2048U;
constexpr std::uint64_t kFiniArrayVirtualAddress = 0x3048U;
constexpr std::uint64_t kShstrtabFileOffset = 0x2f00U;
constexpr std::uint64_t kSectionTableFileOffset = 0x3000U;
constexpr std::uint64_t kFullRelroSize = 0x0f50U;
constexpr std::uint64_t kResolverAddress = 0x0100U;
constexpr std::uint64_t kRelaSize = 24U;
constexpr std::uint64_t kDynamicEntrySize = 16U;
constexpr std::uint64_t kProgramHeaderSize = 56U;
constexpr std::uint64_t kSectionHeaderSize = 64U;
constexpr std::uint64_t kSectionCount = 11U;
constexpr std::uint32_t kPtLoad = 1U;
constexpr std::uint32_t kPtDynamic = 2U;
constexpr std::uint32_t kPtGnuStack = 0x6474e551U;
constexpr std::uint32_t kPtGnuRelro = 0x6474e552U;
constexpr std::uint32_t kPfX = 1U;
constexpr std::uint32_t kPfW = 2U;
constexpr std::uint32_t kPfR = 4U;

void put_u8(Bytes& bytes, std::uint64_t offset, std::uint8_t value) {
    bytes[static_cast<std::size_t>(offset)] = value;
}

void put_u16(Bytes& bytes, std::uint64_t offset, std::uint16_t value) {
    const std::size_t index = static_cast<std::size_t>(offset);
    bytes[index] = static_cast<std::uint8_t>(value & 0xffU);
    bytes[index + 1U] = static_cast<std::uint8_t>((value >> 8U) & 0xffU);
}

void put_u32(Bytes& bytes, std::uint64_t offset, std::uint32_t value) {
    const std::size_t index = static_cast<std::size_t>(offset);
    for (std::uint32_t i = 0U; i < 4U; ++i) {
        bytes[index + static_cast<std::size_t>(i)] =
            static_cast<std::uint8_t>((value >> (i * 8U)) & 0xffU);
    }
}

void put_u64(Bytes& bytes, std::uint64_t offset, std::uint64_t value) {
    const std::size_t index = static_cast<std::size_t>(offset);
    for (std::uint32_t i = 0U; i < 8U; ++i) {
        bytes[index + static_cast<std::size_t>(i)] =
            static_cast<std::uint8_t>((value >> (i * 8U)) & 0xffU);
    }
}

void put_bytes(Bytes& bytes, std::uint64_t offset, const std::string& value) {
    const std::size_t index = static_cast<std::size_t>(offset);
    for (std::size_t i = 0U; i < value.size(); ++i) {
        bytes[index + i] = static_cast<std::uint8_t>(value[i]);
    }
}

void put_program_header(Bytes& bytes, std::uint64_t index, std::uint32_t type,
                        std::uint32_t flags, std::uint64_t offset,
                        std::uint64_t vaddr, std::uint64_t filesz,
                        std::uint64_t memsz, std::uint64_t align) {
    const std::uint64_t base =
        64U + index * kProgramHeaderSize;
    put_u32(bytes, base, type);
    put_u32(bytes, base + 4U, flags);
    put_u64(bytes, base + 8U, offset);
    put_u64(bytes, base + 16U, vaddr);
    put_u64(bytes, base + 24U, vaddr);
    put_u64(bytes, base + 32U, filesz);
    put_u64(bytes, base + 40U, memsz);
    put_u64(bytes, base + 48U, align);
}

struct SectionSpec {
    std::uint32_t name = 0U;
    std::uint32_t type = 0U;
    std::uint64_t flags = 0U;
    std::uint64_t addr = 0U;
    std::uint64_t offset = 0U;
    std::uint64_t size = 0U;
    std::uint32_t link = 0U;
    std::uint32_t info = 0U;
    std::uint64_t addralign = 0U;
    std::uint64_t entsize = 0U;
};

void put_section_header(Bytes& bytes, std::uint64_t index,
                        const SectionSpec& section) {
    const std::uint64_t base =
        kSectionTableFileOffset + index * kSectionHeaderSize;
    put_u32(bytes, base, section.name);
    put_u32(bytes, base + 4U, section.type);
    put_u64(bytes, base + 8U, section.flags);
    put_u64(bytes, base + 16U, section.addr);
    put_u64(bytes, base + 24U, section.offset);
    put_u64(bytes, base + 32U, section.size);
    put_u32(bytes, base + 40U, section.link);
    put_u32(bytes, base + 44U, section.info);
    put_u64(bytes, base + 48U, section.addralign);
    put_u64(bytes, base + 56U, section.entsize);
}

struct DynamicSpec {
    std::int64_t tag = 0;
    std::uint64_t value = 0U;
};

enum class BindMode {
    None,
    Tag,
    Flags,
    Flags1
};

enum class PathTag {
    None,
    Rpath,
    Runpath
};

struct SampleOptions {
    std::uint64_t relro_size = kFullRelroSize;
    BindMode bind_mode = BindMode::Tag;
    PathTag path_tag = PathTag::None;
    std::string path;
    std::uint64_t ifunc_value = kResolverAddress;
    std::uint64_t irelative_addend = kResolverAddress;
    bool executable_stack = false;
};

std::uint32_t add_shstr_string(std::string& table, const std::string& value) {
    const std::uint32_t offset = static_cast<std::uint32_t>(table.size());
    table += value;
    table.push_back('\0');
    return offset;
}

Bytes make_sample(const SampleOptions& options) {
    std::string shstr;
    shstr.push_back('\0');
    const std::uint32_t text_name = add_shstr_string(shstr, ".text");
    const std::uint32_t dynamic_name = add_shstr_string(shstr, ".dynamic");
    const std::uint32_t got_name = add_shstr_string(shstr, ".got");
    const std::uint32_t got_plt_name = add_shstr_string(shstr, ".got.plt");
    const std::uint32_t init_array_name = add_shstr_string(shstr, ".init_array");
    const std::uint32_t fini_array_name = add_shstr_string(shstr, ".fini_array");
    const std::uint32_t dynsym_name = add_shstr_string(shstr, ".dynsym");
    const std::uint32_t dynstr_name = add_shstr_string(shstr, ".dynstr");
    const std::uint32_t rela_name = add_shstr_string(shstr, ".rela.dyn");
    const std::uint32_t shstrtab_name = add_shstr_string(shstr, ".shstrtab");

    std::string dynstr;
    dynstr.push_back('\0');
    const std::uint64_t ifunc_name =
        static_cast<std::uint64_t>(dynstr.size());
    dynstr += "__ifunc";
    dynstr.push_back('\0');
    std::uint64_t path_offset = 0U;
    if (options.path_tag != PathTag::None) {
        path_offset = static_cast<std::uint64_t>(dynstr.size());
        dynstr += options.path;
        dynstr.push_back('\0');
    }

    const std::uint64_t dynamic_size =
        (14U + (options.path_tag == PathTag::None ? 0U : 1U)) *
        kDynamicEntrySize;
    const std::uint64_t file_size =
        kSectionTableFileOffset + kSectionCount * kSectionHeaderSize;
    Bytes bytes(static_cast<std::size_t>(file_size), 0U);

    bytes[0] = 0x7fU;
    bytes[1] = static_cast<std::uint8_t>('E');
    bytes[2] = static_cast<std::uint8_t>('L');
    bytes[3] = static_cast<std::uint8_t>('F');
    bytes[4] = 2U;
    bytes[5] = 1U;
    bytes[6] = 1U;
    put_u16(bytes, 16U, 3U);
    put_u16(bytes, 18U, 62U);
    put_u32(bytes, 20U, 1U);
    put_u64(bytes, 24U, kResolverAddress);
    put_u64(bytes, 32U, 64U);
    put_u64(bytes, 40U, kSectionTableFileOffset);
    put_u16(bytes, 52U, 64U);
    put_u16(bytes, 54U, 56U);
    put_u16(bytes, 56U, 5U);
    put_u16(bytes, 58U, 64U);
    put_u16(bytes, 60U, static_cast<std::uint16_t>(kSectionCount));
    put_u16(bytes, 62U, 10U);

    put_program_header(bytes, 0U, kPtLoad, kPfR | kPfX, kRxFileOffset,
                       kRxVirtualAddress, kRxSize, kRxSize, 0x1000U);
    put_program_header(bytes, 1U, kPtLoad, kPfR | kPfW, kRwFileOffset,
                       kRwVirtualAddress, kRwSize, kRwSize, 0x1000U);
    put_program_header(bytes, 2U, kPtDynamic, kPfR | kPfW,
                       kDynamicFileOffset, kDynamicVirtualAddress, dynamic_size,
                       dynamic_size, 8U);
    put_program_header(bytes, 3U, kPtGnuRelro, kPfR, kDynamicFileOffset,
                       kDynamicVirtualAddress, options.relro_size,
                       options.relro_size, 1U);
    const std::uint32_t stack_flags =
        kPfR | kPfW | (options.executable_stack ? kPfX : 0U);
    put_program_header(bytes, 4U, kPtGnuStack, stack_flags, 0U, 0U, 0U, 0U,
                       16U);

    std::vector<DynamicSpec> dynamic;
    dynamic.push_back(DynamicSpec{5, kDynstrVirtualAddress});
    dynamic.push_back(DynamicSpec{10, static_cast<std::uint64_t>(dynstr.size())});
    dynamic.push_back(DynamicSpec{6, kDynsymVirtualAddress});
    dynamic.push_back(DynamicSpec{11, 24U});
    dynamic.push_back(DynamicSpec{3, kGotPltVirtualAddress});
    dynamic.push_back(DynamicSpec{7, kRelaVirtualAddress});
    dynamic.push_back(DynamicSpec{8, kRelaSize});
    dynamic.push_back(DynamicSpec{9, kRelaSize});
    dynamic.push_back(DynamicSpec{25, kInitArrayVirtualAddress});
    dynamic.push_back(DynamicSpec{27, 8U});
    dynamic.push_back(DynamicSpec{26, kFiniArrayVirtualAddress});
    dynamic.push_back(DynamicSpec{28, 8U});
    switch (options.bind_mode) {
        case BindMode::None:
            break;
        case BindMode::Tag:
            dynamic.push_back(DynamicSpec{24, 0U});
            break;
        case BindMode::Flags:
            dynamic.push_back(DynamicSpec{30, 8U});
            break;
        case BindMode::Flags1:
            dynamic.push_back(DynamicSpec{0x6ffffffbLL, 1U});
            break;
    }
    if (options.path_tag == PathTag::Rpath) {
        dynamic.push_back(DynamicSpec{15, path_offset});
    } else if (options.path_tag == PathTag::Runpath) {
        dynamic.push_back(DynamicSpec{29, path_offset});
    }
    dynamic.push_back(DynamicSpec{0, 0U});

    for (std::size_t i = 0U; i < dynamic.size(); ++i) {
        const std::uint64_t base =
            kDynamicFileOffset +
            static_cast<std::uint64_t>(i) * kDynamicEntrySize;
        put_u64(bytes, base, static_cast<std::uint64_t>(dynamic[i].tag));
        put_u64(bytes, base + 8U, dynamic[i].value);
    }

    put_bytes(bytes, kDynstrFileOffset, dynstr);

    const std::uint64_t dynsym_entry =
        kDynsymFileOffset + 24U;
    put_u32(bytes, dynsym_entry, static_cast<std::uint32_t>(ifunc_name));
    put_u8(bytes, dynsym_entry + 4U, 0x1aU);
    put_u16(bytes, dynsym_entry + 6U, 1U);
    put_u64(bytes, dynsym_entry + 8U, options.ifunc_value);
    put_u64(bytes, dynsym_entry + 16U, 0x10U);

    put_u64(bytes, kRelaFileOffset, kGotVirtualAddress);
    put_u64(bytes, kRelaFileOffset + 8U, 37U);
    put_u64(bytes, kRelaFileOffset + 16U, options.irelative_addend);

    put_bytes(bytes, kShstrtabFileOffset, shstr);
    put_bytes(bytes, kInitArrayFileOffset, std::string(8U, '\0'));
    put_bytes(bytes, kFiniArrayFileOffset, std::string(8U, '\0'));

    std::vector<SectionSpec> sections(kSectionCount);
    sections[1].name = text_name;
    sections[1].type = 1U;
    sections[1].flags = 2U | 4U;
    sections[1].addr = kResolverAddress;
    sections[1].offset = kResolverAddress;
    sections[1].size = 0x40U;
    sections[1].addralign = 16U;

    sections[2].name = dynamic_name;
    sections[2].type = 6U;
    sections[2].flags = 2U | 1U;
    sections[2].addr = kDynamicVirtualAddress;
    sections[2].offset = kDynamicFileOffset;
    sections[2].size = dynamic_size;
    sections[2].link = 8U;
    sections[2].addralign = 8U;
    sections[2].entsize = kDynamicEntrySize;

    sections[3].name = got_name;
    sections[3].type = 1U;
    sections[3].flags = 2U | 1U;
    sections[3].addr = kGotVirtualAddress;
    sections[3].offset = kGotFileOffset;
    sections[3].size = 0x20U;
    sections[3].addralign = 8U;

    sections[4].name = got_plt_name;
    sections[4].type = 1U;
    sections[4].flags = 2U | 1U;
    sections[4].addr = kGotPltVirtualAddress;
    sections[4].offset = kGotPltFileOffset;
    sections[4].size = 0x20U;
    sections[4].addralign = 8U;

    sections[5].name = init_array_name;
    sections[5].type = 14U;
    sections[5].flags = 2U | 1U;
    sections[5].addr = kInitArrayVirtualAddress;
    sections[5].offset = kInitArrayFileOffset;
    sections[5].size = 8U;
    sections[5].addralign = 8U;
    sections[5].entsize = 8U;

    sections[6].name = fini_array_name;
    sections[6].type = 15U;
    sections[6].flags = 2U | 1U;
    sections[6].addr = kFiniArrayVirtualAddress;
    sections[6].offset = kFiniArrayFileOffset;
    sections[6].size = 8U;
    sections[6].addralign = 8U;
    sections[6].entsize = 8U;

    sections[7].name = dynsym_name;
    sections[7].type = 11U;
    sections[7].flags = 2U;
    sections[7].addr = kDynsymVirtualAddress;
    sections[7].offset = kDynsymFileOffset;
    sections[7].size = 48U;
    sections[7].link = 8U;
    sections[7].info = 1U;
    sections[7].addralign = 8U;
    sections[7].entsize = 24U;

    sections[8].name = dynstr_name;
    sections[8].type = 3U;
    sections[8].flags = 2U;
    sections[8].addr = kDynstrVirtualAddress;
    sections[8].offset = kDynstrFileOffset;
    sections[8].size = static_cast<std::uint64_t>(dynstr.size());
    sections[8].addralign = 1U;

    sections[9].name = rela_name;
    sections[9].type = 4U;
    sections[9].flags = 2U;
    sections[9].addr = kRelaVirtualAddress;
    sections[9].offset = kRelaFileOffset;
    sections[9].size = kRelaSize;
    sections[9].link = 7U;
    sections[9].info = 1U;
    sections[9].addralign = 8U;
    sections[9].entsize = kRelaSize;

    sections[10].name = shstrtab_name;
    sections[10].type = 3U;
    sections[10].addr = kShstrtabFileOffset;
    sections[10].offset = kShstrtabFileOffset;
    sections[10].size = static_cast<std::uint64_t>(shstr.size());
    sections[10].addralign = 1U;

    for (std::size_t i = 0U; i < sections.size(); ++i) {
        put_section_header(bytes, static_cast<std::uint64_t>(i), sections[i]);
    }
    return bytes;
}

int failures = 0;

void check(bool condition, const std::string& name) {
    if (!condition) {
        std::cerr << "FAIL: " << name << '\n';
        ++failures;
    }
}

constexpr std::uint64_t kFirstProgramHeader = 64U;

void test_valid_full_relro() {
    const Bytes image = make_sample(SampleOptions{});
    const elf64_audit::AuditReport report =
        elf64_audit::audit_elf64(image.data(), image.size());

    check(report.parsed, "valid image parses");
    check(report.admission_passed, "valid Full RELRO image is admitted");
    check(report.relro == elf64_audit::RelroStatus::Full,
          "valid image reports Full RELRO");
    check(report.immediate_binding, "DT_BIND_NOW is recognized");
    check(!report.executable_stack, "non-executable stack is recognized");
    check(!report.unsafe_ifunc, "RX IFUNC and IRELATIVE resolvers are safe");
    check(report.error_count() == 0U, "valid image has no errors");
}

void test_bind_now_encodings() {
    for (const BindMode mode :
         {BindMode::Tag, BindMode::Flags, BindMode::Flags1}) {
        SampleOptions options;
        options.bind_mode = mode;
        const Bytes image = make_sample(options);
        const elf64_audit::AuditReport report =
            elf64_audit::audit_elf64(image.data(), image.size());
        check(report.admission_passed, "bind-now encoding is admitted");
        check(report.relro == elf64_audit::RelroStatus::Full,
              "bind-now encoding yields Full RELRO");
    }
}

void test_pseudo_relro() {
    SampleOptions options;
    options.relro_size = 0x0100U;
    const Bytes image = make_sample(options);
    const elf64_audit::AuditReport report =
        elf64_audit::audit_elf64(image.data(), image.size());

    check(report.parsed, "pseudo RELRO image parses");
    check(!report.admission_passed, "pseudo RELRO image is rejected");
    check(report.relro == elf64_audit::RelroStatus::Partial,
          "pseudo RELRO is classified as Partial");
    check(report.has_finding(elf64_audit::Code::RelroCoverageIncomplete),
          "pseudo RELRO reports incomplete coverage");
}

void test_missing_bind_now() {
    SampleOptions options;
    options.bind_mode = BindMode::None;
    const Bytes image = make_sample(options);
    const elf64_audit::AuditReport report =
        elf64_audit::audit_elf64(image.data(), image.size());

    check(!report.admission_passed, "lazy binding image is rejected");
    check(report.relro == elf64_audit::RelroStatus::Partial,
          "RELRO without immediate binding is Partial");
    check(report.has_finding(elf64_audit::Code::BindNowMissing),
          "missing bind-now is reported");
}

void test_empty_rpath() {
    SampleOptions options;
    options.path_tag = PathTag::Rpath;
    options.path = "";
    const Bytes image = make_sample(options);
    const elf64_audit::AuditReport report =
        elf64_audit::audit_elf64(image.data(), image.size());

    check(!report.admission_passed, "empty RPATH image is rejected");
    check(report.has_rpath, "RPATH presence is reported");
    check(report.has_finding(elf64_audit::Code::EmptyRuntimePath),
          "empty RPATH segment is reported");
}

void test_relative_and_origin_paths() {
    {
        SampleOptions options;
        options.path_tag = PathTag::Runpath;
        options.path = "plugins";
        const Bytes image = make_sample(options);
        const elf64_audit::AuditReport report =
            elf64_audit::audit_elf64(image.data(), image.size());
        check(report.has_finding(
                  elf64_audit::Code::NonAbsoluteRuntimePath),
              "relative RUNPATH is rejected");
    }
    {
        SampleOptions options;
        options.path_tag = PathTag::Runpath;
        options.path = "$ORIGIN/../../tmp";
        const Bytes image = make_sample(options);
        const elf64_audit::AuditReport report =
            elf64_audit::audit_elf64(image.data(), image.size());
        check(report.has_finding(elf64_audit::Code::UnboundedOriginPath),
              "$ORIGIN escape is rejected");
    }
    {
        SampleOptions options;
        options.path_tag = PathTag::Runpath;
        options.path = "$ORIGIN/plugins";
        const Bytes image = make_sample(options);
        const elf64_audit::AuditReport report =
            elf64_audit::audit_elf64(image.data(), image.size());
        check(report.admission_passed, "bounded $ORIGIN path is admitted");
    }
}

void test_executable_stack() {
    SampleOptions options;
    options.executable_stack = true;
    const Bytes image = make_sample(options);
    const elf64_audit::AuditReport report =
        elf64_audit::audit_elf64(image.data(), image.size());

    check(report.executable_stack, "executable stack flag is reported");
    check(report.has_finding(elf64_audit::Code::ExecutableStack),
          "executable stack is rejected");
}

void test_missing_stack_header() {
    Bytes image = make_sample(SampleOptions{});
    put_u32(image, kFirstProgramHeader + 4U * kProgramHeaderSize, 0U);
    const elf64_audit::AuditReport report =
        elf64_audit::audit_elf64(image.data(), image.size());

    check(!report.admission_passed, "missing stack header is rejected");
    check(report.has_finding(elf64_audit::Code::MissingGnuStack),
          "missing stack header is reported");
}

void test_writable_ifunc_and_irelative() {
    {
        SampleOptions options;
        options.ifunc_value = kGotVirtualAddress;
        const Bytes image = make_sample(options);
        const elf64_audit::AuditReport report =
            elf64_audit::audit_elf64(image.data(), image.size());
        check(report.has_finding(
                  elf64_audit::Code::WritableIfuncResolver),
              "writable STT_GNU_IFUNC resolver is rejected");
        check(report.unsafe_ifunc, "unsafe IFUNC state is reported");
    }
    {
        SampleOptions options;
        options.irelative_addend = kGotVirtualAddress;
        const Bytes image = make_sample(options);
        const elf64_audit::AuditReport report =
            elf64_audit::audit_elf64(image.data(), image.size());
        check(report.has_finding(
                  elf64_audit::Code::WritableIrelativeResolver),
              "writable R_X86_64_IRELATIVE resolver is rejected");
    }
}

void test_malformed_ranges() {
    {
        Bytes image = make_sample(SampleOptions{});
        put_u64(image, kFirstProgramHeader + 8U, 0xfffffffffffffff0ULL);
        const elf64_audit::AuditReport report =
            elf64_audit::audit_elf64(image.data(), image.size());
        check(!report.parsed, "out-of-bounds segment is rejected");
    }
    {
        Bytes image = make_sample(SampleOptions{});
        put_u64(image, kFirstProgramHeader + 16U,
                0xfffffffffffffff0ULL);
        put_u64(image, kFirstProgramHeader + 40U, 0x100U);
        const elf64_audit::AuditReport report =
            elf64_audit::audit_elf64(image.data(), image.size());
        check(!report.parsed, "wrapping virtual range is rejected");
    }
    {
        const Bytes image = make_sample(SampleOptions{});
        const elf64_audit::AuditReport report =
            elf64_audit::audit_elf64(image.data(), 32U);
        check(!report.parsed, "truncated header is rejected");
    }
}

void test_overlapping_load_permissions() {
    Bytes image = make_sample(SampleOptions{});
    put_u64(image, kFirstProgramHeader + 40U, 0x2000U);

    const std::uint64_t second_load = kFirstProgramHeader + kProgramHeaderSize;
    put_u64(image, second_load + 16U, 0x1000U);
    put_u64(image, second_load + 24U, 0x1000U);

    const elf64_audit::AuditReport report =
        elf64_audit::audit_elf64(image.data(), image.size());
    check(!report.parsed, "conflicting overlapping PT_LOAD is rejected");
    check(report.has_finding(elf64_audit::Code::OverlappingLoadSegments),
          "overlapping permission conflict is reported");

    Bytes page_image = make_sample(SampleOptions{});
    put_u64(page_image, kFirstProgramHeader + 32U, 0x100U);
    put_u64(page_image, kFirstProgramHeader + 40U, 0x100U);
    const std::uint64_t page_second_load =
        kFirstProgramHeader + kProgramHeaderSize;
    put_u64(page_image, page_second_load + 8U, 0x200U);
    put_u64(page_image, page_second_load + 16U, 0x200U);
    put_u64(page_image, page_second_load + 24U, 0x200U);
    put_u64(page_image, page_second_load + 48U, 1U);
    const elf64_audit::AuditReport page_report =
        elf64_audit::audit_elf64(page_image.data(), page_image.size());
    check(!page_report.parsed,
          "same-page incompatible PT_LOAD permissions are rejected");
}

void test_mutation_robustness() {
    const Bytes base = make_sample(SampleOptions{});
    std::uint64_t state = 0x7d3a1c9e5b2f4086ULL;
    for (std::size_t round = 0U; round < 1000U; ++round) {
        Bytes image = base;
        state = state * 6364136223846793005ULL + 1442695040888963407ULL;
        const std::size_t mutation_count =
            1U + static_cast<std::size_t>(state % 8U);
        for (std::size_t i = 0U; i < mutation_count; ++i) {
            state = state * 6364136223846793005ULL + 1442695040888963407ULL;
            const std::size_t position =
                static_cast<std::size_t>(state % image.size());
            state = state * 6364136223846793005ULL + 1442695040888963407ULL;
            image[position] ^= static_cast<std::uint8_t>(state >> 24U);
        }
        const elf64_audit::AuditReport report =
            elf64_audit::audit_elf64(image.data(), image.size());
        check(report.parsed || report.error_count() > 0U,
              "unparsed mutated image carries an error");
        check(!report.admission_passed || report.parsed,
              "mutated image admission implies successful parsing");
    }
}

}  // namespace

int main() {
    test_valid_full_relro();
    test_bind_now_encodings();
    test_pseudo_relro();
    test_missing_bind_now();
    test_empty_rpath();
    test_relative_and_origin_paths();
    test_executable_stack();
    test_missing_stack_header();
    test_writable_ifunc_and_irelative();
    test_malformed_ranges();
    test_overlapping_load_permissions();
    test_mutation_robustness();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return 1;
    }
    std::cout << "all ELF64 audit tests passed\n";
    return 0;
}
