#include "elf64_audit/elf64_audit.hpp"

#include <algorithm>
#include <fstream>
#include <limits>
#include <string>
#include <utility>
#include <vector>

namespace elf64_audit {
namespace {

constexpr std::uint8_t kElfMagic0 = 0x7fU;
constexpr std::uint8_t kElfMagic1 = static_cast<std::uint8_t>('E');
constexpr std::uint8_t kElfMagic2 = static_cast<std::uint8_t>('L');
constexpr std::uint8_t kElfMagic3 = static_cast<std::uint8_t>('F');

constexpr std::uint8_t kElfClass64 = 2U;
constexpr std::uint8_t kElfDataLittleEndian = 1U;
constexpr std::uint8_t kElfVersionCurrent = 1U;

constexpr std::uint16_t kEtExec = 2U;
constexpr std::uint16_t kEtDyn = 3U;
constexpr std::uint16_t kEmX86_64 = 62U;

constexpr std::uint32_t kPtNull = 0U;
constexpr std::uint32_t kPtLoad = 1U;
constexpr std::uint32_t kPtDynamic = 2U;
constexpr std::uint32_t kPtGnuStack = 0x6474e551U;
constexpr std::uint32_t kPtGnuRelro = 0x6474e552U;

constexpr std::uint32_t kPfX = 1U;
constexpr std::uint32_t kPfW = 2U;
constexpr std::uint32_t kPfR = 4U;

constexpr std::uint32_t kShtNull = 0U;
constexpr std::uint32_t kShtSymtab = 2U;
constexpr std::uint32_t kShtStrtab = 3U;
constexpr std::uint32_t kShtRela = 4U;
constexpr std::uint32_t kShtDynamic = 6U;
constexpr std::uint32_t kShtNobits = 8U;
constexpr std::uint32_t kShtDynsym = 11U;

constexpr std::uint64_t kShnUndef = 0U;

constexpr std::int64_t kDtNull = 0;
constexpr std::int64_t kDtNeeded = 1;
constexpr std::int64_t kDtPltGot = 3;
constexpr std::int64_t kDtHash = 4;
constexpr std::int64_t kDtStrtab = 5;
constexpr std::int64_t kDtSymtab = 6;
constexpr std::int64_t kDtRela = 7;
constexpr std::int64_t kDtRelaSz = 8;
constexpr std::int64_t kDtRelaEnt = 9;
constexpr std::int64_t kDtStrtabSz = 10;
constexpr std::int64_t kDtSyment = 11;
constexpr std::int64_t kDtSoname = 14;
constexpr std::int64_t kDtRpath = 15;
constexpr std::int64_t kDtPltRel = 20;
constexpr std::int64_t kDtJmprel = 23;
constexpr std::int64_t kDtBindNow = 24;
constexpr std::int64_t kDtInitArray = 25;
constexpr std::int64_t kDtFiniArray = 26;
constexpr std::int64_t kDtInitArraySz = 27;
constexpr std::int64_t kDtFiniArraySz = 28;
constexpr std::int64_t kDtRunpath = 29;
constexpr std::int64_t kDtFlags = 30;
constexpr std::int64_t kDtPreinitArray = 32;
constexpr std::int64_t kDtPreinitArraySz = 33;
constexpr std::int64_t kDtPltRelSz = 2;
constexpr std::int64_t kDtGnuHash = 0x6ffffef5LL;
constexpr std::int64_t kDtFlags1 = 0x6ffffffbLL;
constexpr std::int64_t kDtFilter = 0x7fffffffLL;
constexpr std::int64_t kDtAuxiliary = 0x7ffffffdLL;

constexpr std::uint64_t kDtRelaValue = 7U;
constexpr std::uint64_t kDfBindNow = 0x8U;
constexpr std::uint64_t kDf1Now = 0x1U;

constexpr std::uint32_t kRNone = 0U;
constexpr std::uint32_t kRGlobDat = 6U;
constexpr std::uint32_t kRJumpSlot = 7U;
constexpr std::uint32_t kRIrelative = 37U;
constexpr std::uint8_t kSttGnuIfunc = 10U;

constexpr std::uint64_t kElfHeaderSize = 64U;
constexpr std::uint64_t kProgramHeaderSize = 56U;
constexpr std::uint64_t kSectionHeaderSize = 64U;
constexpr std::uint64_t kDynamicEntrySize = 16U;
constexpr std::uint64_t kRelaEntrySize = 24U;
constexpr std::uint64_t kSymbolEntrySize = 24U;
constexpr std::uint64_t kNoValue = std::numeric_limits<std::uint64_t>::max();

std::string hex_value(std::uint64_t value) {
    static constexpr char digits[] = "0123456789abcdef";
    char buffer[19];
    buffer[0] = '0';
    buffer[1] = 'x';
    std::size_t pos = sizeof(buffer);
    if (value == 0U) {
        buffer[--pos] = '0';
    } else {
        while (value != 0U) {
            buffer[--pos] = digits[static_cast<std::size_t>(value & 0xfU)];
            value >>= 4U;
        }
    }
    return std::string(buffer + pos, sizeof(buffer) - pos);
}

bool checked_add(std::uint64_t left, std::uint64_t right, std::uint64_t& result) noexcept {
    if (left > std::numeric_limits<std::uint64_t>::max() - right) {
        return false;
    }
    result = left + right;
    return true;
}

bool checked_mul(std::uint64_t left, std::uint64_t right, std::uint64_t& result) noexcept {
    if (left != 0U && right > std::numeric_limits<std::uint64_t>::max() / left) {
        return false;
    }
    result = left * right;
    return true;
}

bool is_power_of_two(std::uint64_t value) noexcept {
    return value != 0U && (value & (value - 1U)) == 0U;
}

bool read_u8(const std::uint8_t* data, std::size_t size, std::uint64_t offset,
             std::uint8_t& value) noexcept {
    if (offset > static_cast<std::uint64_t>(size)) {
        return false;
    }
    const std::uint64_t remaining = static_cast<std::uint64_t>(size) - offset;
    if (remaining < 1U) {
        return false;
    }
    value = data[static_cast<std::size_t>(offset)];
    return true;
}

bool read_u16(const std::uint8_t* data, std::size_t size, std::uint64_t offset,
              std::uint16_t& value) noexcept {
    if (offset > static_cast<std::uint64_t>(size)) {
        return false;
    }
    const std::uint64_t remaining = static_cast<std::uint64_t>(size) - offset;
    if (remaining < 2U) {
        return false;
    }
    const std::size_t index = static_cast<std::size_t>(offset);
    value = static_cast<std::uint16_t>(static_cast<std::uint16_t>(data[index]) |
                                       static_cast<std::uint16_t>(
                                           static_cast<std::uint16_t>(data[index + 1U]) << 8U));
    return true;
}

bool read_u32(const std::uint8_t* data, std::size_t size, std::uint64_t offset,
              std::uint32_t& value) noexcept {
    if (offset > static_cast<std::uint64_t>(size)) {
        return false;
    }
    const std::uint64_t remaining = static_cast<std::uint64_t>(size) - offset;
    if (remaining < 4U) {
        return false;
    }
    const std::size_t index = static_cast<std::size_t>(offset);
    value = static_cast<std::uint32_t>(data[index]) |
            (static_cast<std::uint32_t>(data[index + 1U]) << 8U) |
            (static_cast<std::uint32_t>(data[index + 2U]) << 16U) |
            (static_cast<std::uint32_t>(data[index + 3U]) << 24U);
    return true;
}

bool read_u64(const std::uint8_t* data, std::size_t size, std::uint64_t offset,
              std::uint64_t& value) noexcept {
    if (offset > static_cast<std::uint64_t>(size)) {
        return false;
    }
    const std::uint64_t remaining = static_cast<std::uint64_t>(size) - offset;
    if (remaining < 8U) {
        return false;
    }
    const std::size_t index = static_cast<std::size_t>(offset);
    value = static_cast<std::uint64_t>(data[index]) |
            (static_cast<std::uint64_t>(data[index + 1U]) << 8U) |
            (static_cast<std::uint64_t>(data[index + 2U]) << 16U) |
            (static_cast<std::uint64_t>(data[index + 3U]) << 24U) |
            (static_cast<std::uint64_t>(data[index + 4U]) << 32U) |
            (static_cast<std::uint64_t>(data[index + 5U]) << 40U) |
            (static_cast<std::uint64_t>(data[index + 6U]) << 48U) |
            (static_cast<std::uint64_t>(data[index + 7U]) << 56U);
    return true;
}

struct ProgramHeader {
    std::uint32_t type = 0U;
    std::uint32_t flags = 0U;
    std::uint64_t offset = 0U;
    std::uint64_t vaddr = 0U;
    std::uint64_t filesz = 0U;
    std::uint64_t memsz = 0U;
    std::uint64_t align = 0U;
    std::uint64_t file_end = 0U;
    std::uint64_t mem_end = 0U;
};

struct SectionHeader {
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
    std::uint64_t mem_end = 0U;
    std::string name_text;
};

struct TargetRange {
    std::string name;
    std::uint64_t begin = 0U;
    std::uint64_t end = 0U;
};

class Auditor {
public:
    Auditor(const std::uint8_t* data, std::size_t size, AuditOptions options)
        : data_(data), size_(size), options_(options) {}

    AuditReport run() {
        if (!validate_options()) {
            return finish();
        }
        if (!parse_header()) {
            return finish();
        }
        if (!parse_program_headers()) {
            return finish();
        }
        if (!parse_section_headers()) {
            return finish();
        }
        if (!parse_dynamic()) {
            return finish();
        }
        if (!parse_relocations()) {
            return finish();
        }
        if (!parse_symbols()) {
            return finish();
        }

        check_relro();
        check_stack();
        report_.parsed = true;
        return finish();
    }

private:
    void add_finding(Severity severity, Code code, std::string message,
                     std::uint64_t file_offset = 0U,
                     std::uint64_t virtual_address = 0U) {
        Finding finding;
        finding.severity = severity;
        finding.code = code;
        finding.message = std::move(message);
        finding.file_offset = file_offset;
        finding.virtual_address = virtual_address;
        report_.findings.push_back(std::move(finding));
    }

    void add_error(Code code, std::string message, std::uint64_t file_offset = 0U,
                   std::uint64_t virtual_address = 0U) {
        add_finding(Severity::Error, code, std::move(message), file_offset,
                    virtual_address);
    }

    void add_warning(Code code, std::string message,
                     std::uint64_t file_offset = 0U,
                     std::uint64_t virtual_address = 0U) {
        add_finding(Severity::Warning, code, std::move(message), file_offset,
                    virtual_address);
    }

    AuditReport finish() {
        report_.admission_passed = report_.parsed && report_.error_count() == 0U;
        return std::move(report_);
    }

    bool validate_options() {
        if (!is_power_of_two(options_.page_size) || options_.page_size < 4096U) {
            add_error(Code::InvalidConfiguration,
                      "page_size must be a power of two and at least 4096");
            return false;
        }
        return true;
    }

    bool parse_header() {
        if (data_ == nullptr) {
            add_error(Code::TruncatedInput, "input buffer is null");
            return false;
        }
        if (size_ < static_cast<std::size_t>(kElfHeaderSize)) {
            add_error(Code::TruncatedInput, "ELF64 header is truncated");
            return false;
        }

        const bool has_magic =
            data_[0] == kElfMagic0 && data_[1] == kElfMagic1 &&
            data_[2] == kElfMagic2 && data_[3] == kElfMagic3;
        if (!has_magic) {
            add_error(Code::InvalidMagic, "ELF magic is missing");
            return false;
        }
        if (data_[4] != kElfClass64) {
            add_error(Code::UnsupportedClass, "only ELFCLASS64 is supported");
            return false;
        }
        if (data_[5] != kElfDataLittleEndian) {
            add_error(Code::UnsupportedEndianness,
                      "only little-endian ELF64 is supported");
            return false;
        }
        if (data_[6] != kElfVersionCurrent) {
            add_error(Code::UnsupportedVersion, "unsupported ELF ident version");
            return false;
        }

        std::uint16_t type = 0U;
        std::uint16_t machine = 0U;
        std::uint32_t version = 0U;
        std::uint16_t ehsize = 0U;
        std::uint16_t phentsize = 0U;
        std::uint16_t shentsize = 0U;
        std::uint16_t phnum_raw = 0U;
        std::uint16_t shnum_raw = 0U;
        std::uint16_t shstrndx_raw = 0U;

        if (!read_u16(data_, size_, 16U, type) ||
            !read_u16(data_, size_, 18U, machine) ||
            !read_u32(data_, size_, 20U, version) ||
            !read_u64(data_, size_, 32U, phoff_) ||
            !read_u64(data_, size_, 40U, shoff_) ||
            !read_u16(data_, size_, 52U, ehsize) ||
            !read_u16(data_, size_, 54U, phentsize) ||
            !read_u16(data_, size_, 56U, phnum_raw) ||
            !read_u16(data_, size_, 58U, shentsize) ||
            !read_u16(data_, size_, 60U, shnum_raw) ||
            !read_u16(data_, size_, 62U, shstrndx_raw)) {
            add_error(Code::TruncatedInput, "ELF64 header cannot be read");
            return false;
        }

        if (type != kEtExec && type != kEtDyn) {
            add_error(Code::UnsupportedType,
                      "only ET_EXEC and ET_DYN images are supported");
            return false;
        }
        if (machine != kEmX86_64) {
            add_error(Code::UnsupportedMachine,
                      "only EM_X86_64 images are supported");
            return false;
        }
        if (version != 1U) {
            add_error(Code::UnsupportedVersion, "unsupported ELF header version");
            return false;
        }
        if (ehsize != kElfHeaderSize) {
            add_error(Code::MalformedElfHeader,
                      "e_ehsize must be 64 for ELF64");
            return false;
        }

        phnum_ = static_cast<std::uint64_t>(phnum_raw);
        shnum_ = static_cast<std::uint64_t>(shnum_raw);
        shstrndx_ = static_cast<std::uint64_t>(shstrndx_raw);

        const bool extended_phnum = phnum_raw == 0xffffU;
        const bool extended_shnum = shnum_raw == 0U && shoff_ != 0U;
        const bool extended_shstrndx = shstrndx_raw == 0xffffU;
        const bool needs_section_zero =
            extended_phnum || extended_shnum || extended_shstrndx;

        if (phnum_raw != 0U && phentsize != kProgramHeaderSize) {
            add_error(Code::MalformedProgramHeaders,
                      "e_phentsize must be 56 for ELF64");
            return false;
        }
        if ((shoff_ != 0U || shnum_raw != 0U) && shentsize != kSectionHeaderSize) {
            add_error(Code::MalformedSectionHeaders,
                      "e_shentsize must be 64 for ELF64");
            return false;
        }

        if (needs_section_zero) {
            if (shoff_ == 0U || shentsize != kSectionHeaderSize) {
                add_error(Code::MalformedSectionHeaders,
                          "extended ELF numbering requires a section table");
                return false;
            }
            std::uint64_t section_zero_end = 0U;
            if (!checked_add(shoff_, kSectionHeaderSize, section_zero_end) ||
                section_zero_end > static_cast<std::uint64_t>(size_)) {
                add_error(Code::OutOfBounds,
                          "section header zero is outside the file", shoff_);
                return false;
            }

            std::uint32_t sh0_type = 0U;
            std::uint32_t sh0_link = 0U;
            std::uint32_t sh0_info = 0U;
            std::uint64_t sh0_size = 0U;
            if (!read_u32(data_, size_, shoff_ + 4U, sh0_type) ||
                !read_u32(data_, size_, shoff_ + 40U, sh0_link) ||
                !read_u32(data_, size_, shoff_ + 44U, sh0_info) ||
                !read_u64(data_, size_, shoff_ + 32U, sh0_size)) {
                add_error(Code::TruncatedInput,
                          "section header zero cannot be read", shoff_);
                return false;
            }
            if (sh0_type != kShtNull) {
                add_error(Code::MalformedSectionHeaders,
                          "section header zero must be SHT_NULL", shoff_);
                return false;
            }
            if (extended_phnum) {
                phnum_ = static_cast<std::uint64_t>(sh0_info);
            }
            if (extended_shnum) {
                shnum_ = sh0_size;
            }
            if (extended_shstrndx) {
                shstrndx_ = static_cast<std::uint64_t>(sh0_link);
            }
        }

        if (phnum_ != 0U) {
            if (phoff_ == 0U) {
                add_error(Code::MalformedProgramHeaders,
                          "e_phoff is zero while e_phnum is nonzero");
                return false;
            }
            if ((phoff_ & 7U) != 0U) {
                add_error(Code::MisalignedStructure,
                          "program header table is not 8-byte aligned", phoff_);
                return false;
            }
        }
        if (shnum_ != 0U) {
            if (shoff_ == 0U) {
                add_error(Code::MalformedSectionHeaders,
                          "e_shoff is zero while e_shnum is nonzero");
                return false;
            }
            if ((shoff_ & 7U) != 0U) {
                add_error(Code::MisalignedStructure,
                          "section header table is not 8-byte aligned", shoff_);
                return false;
            }
        }
        if (shnum_ == 0U && shstrndx_ != 0U) {
            add_error(Code::MalformedSectionHeaders,
                      "e_shstrndx is nonzero without a section table");
            return false;
        }
        if (shstrndx_ != 0U && shstrndx_ >= shnum_) {
            add_error(Code::MalformedSectionHeaders,
                      "e_shstrndx is outside the section table");
            return false;
        }

        return true;
    }

    bool read_program_header(std::uint64_t index, ProgramHeader& header) {
        std::uint64_t delta = 0U;
        std::uint64_t offset = 0U;
        if (!checked_mul(index, kProgramHeaderSize, delta) ||
            !checked_add(phoff_, delta, offset)) {
            add_error(Code::IntegerOverflow,
                      "program header offset overflow", phoff_);
            return false;
        }
        std::uint64_t entry_end = 0U;
        if (!checked_add(offset, kProgramHeaderSize, entry_end) ||
            entry_end > static_cast<std::uint64_t>(size_)) {
            add_error(Code::OutOfBounds,
                      "program header is outside the file", offset);
            return false;
        }

        if (!read_u32(data_, size_, offset, header.type) ||
            !read_u32(data_, size_, offset + 4U, header.flags) ||
            !read_u64(data_, size_, offset + 8U, header.offset) ||
            !read_u64(data_, size_, offset + 16U, header.vaddr) ||
            !read_u64(data_, size_, offset + 32U, header.filesz) ||
            !read_u64(data_, size_, offset + 40U, header.memsz) ||
            !read_u64(data_, size_, offset + 48U, header.align)) {
            add_error(Code::TruncatedInput,
                      "program header fields cannot be read", offset);
            return false;
        }
        return true;
    }

    bool validate_program_header(const ProgramHeader& header,
                                 std::uint64_t header_file_offset) {
        if (header.type == kPtNull) {
            return true;
        }
        if (header.offset > static_cast<std::uint64_t>(size_)) {
            add_error(Code::OutOfBounds,
                      "program segment file offset is outside the file",
                      header_file_offset, header.vaddr);
            return false;
        }
        if (!checked_add(header.offset, header.filesz, header_file_end_)) {
            add_error(Code::IntegerOverflow,
                      "program segment file range overflows", header_file_offset,
                      header.vaddr);
            return false;
        }
        if (header_file_end_ > static_cast<std::uint64_t>(size_)) {
            add_error(Code::OutOfBounds,
                      "program segment file range is outside the file",
                      header.offset, header.vaddr);
            return false;
        }
        if (!checked_add(header.vaddr, header.memsz, header_mem_end_)) {
            add_error(Code::IntegerOverflow,
                      "program segment memory range overflows",
                      header_file_offset, header.vaddr);
            return false;
        }
        if (header.align > 1U) {
            if (!is_power_of_two(header.align)) {
                add_error(Code::MisalignedStructure,
                          "program segment alignment is not a power of two",
                          header_file_offset, header.vaddr);
                return false;
            }
            if (header.type == kPtLoad &&
                (header.vaddr % header.align) != (header.offset % header.align)) {
                add_error(Code::MisalignedStructure,
                          "PT_LOAD virtual and file offsets are incongruent",
                          header_file_offset, header.vaddr);
                return false;
            }
        }
        if ((header.type == kPtLoad || header.type == kPtDynamic ||
             header.type == kPtGnuRelro) &&
            header.filesz > header.memsz) {
            add_error(Code::MalformedProgramHeaders,
                      "loadable segment p_filesz exceeds p_memsz",
                      header_file_offset,
                      header.vaddr);
            return false;
        }
        if (header.type == kPtDynamic &&
            (header.filesz == 0U || (header.filesz % kDynamicEntrySize) != 0U)) {
            add_error(Code::MalformedDynamicTable,
                      "PT_DYNAMIC size is not a nonzero multiple of 16",
                      header_file_offset, header.vaddr);
            return false;
        }
        return true;
    }

    bool parse_program_headers() {
        if (phnum_ == 0U) {
            add_error(Code::NoLoadSegment, "ELF image has no program headers");
            return false;
        }

        std::uint64_t table_size = 0U;
        std::uint64_t table_end = 0U;
        if (!checked_mul(phnum_, kProgramHeaderSize, table_size) ||
            !checked_add(phoff_, table_size, table_end)) {
            add_error(Code::IntegerOverflow,
                      "program header table size overflows", phoff_);
            return false;
        }
        if (table_end > static_cast<std::uint64_t>(size_)) {
            add_error(Code::OutOfBounds,
                      "program header table is outside the file", phoff_);
            return false;
        }

        phdrs_.reserve(static_cast<std::size_t>(phnum_));
        for (std::uint64_t i = 0U; i < phnum_; ++i) {
            ProgramHeader header;
            if (!read_program_header(i, header)) {
                return false;
            }
            std::uint64_t entry_offset = 0U;
            std::uint64_t delta = 0U;
            if (!checked_mul(i, kProgramHeaderSize, delta) ||
                !checked_add(phoff_, delta, entry_offset)) {
                add_error(Code::IntegerOverflow,
                          "program header offset overflow", phoff_);
                return false;
            }
            if (!validate_program_header(header, entry_offset)) {
                return false;
            }
            header.file_end = header_file_end_;
            header.mem_end = header_mem_end_;
            phdrs_.push_back(header);
        }

        std::size_t load_count = 0U;
        std::size_t dynamic_count = 0U;
        std::size_t relro_count = 0U;
        for (std::size_t i = 0U; i < phdrs_.size(); ++i) {
            const ProgramHeader& header = phdrs_[i];
            if (header.type == kPtLoad) {
                ++load_count;
                load_indices_.push_back(i);
            } else if (header.type == kPtDynamic) {
                ++dynamic_count;
                dynamic_ = header;
                has_dynamic_ = true;
            } else if (header.type == kPtGnuRelro) {
                ++relro_count;
                relro_ = header;
                has_relro_ = true;
                report_.has_pt_gnu_relro = true;
            } else if (header.type == kPtGnuStack) {
                report_.has_gnu_stack = true;
                if ((header.flags & kPfX) != 0U) {
                    report_.executable_stack = true;
                }
            }
        }

        if (load_count == 0U) {
            add_error(Code::NoLoadSegment, "ELF image has no PT_LOAD segment");
            return false;
        }
        if (dynamic_count > 1U) {
            add_error(Code::MalformedProgramHeaders,
                      "multiple PT_DYNAMIC segments are ambiguous");
            return false;
        }
        if (relro_count > 1U) {
            add_error(Code::MalformedProgramHeaders,
                      "multiple PT_GNU_RELRO segments are ambiguous");
            return false;
        }

        if (!check_load_overlaps()) {
            return false;
        }

        if (has_dynamic_) {
            std::uint64_t mapped_offset = 0U;
            if (!map_virtual_range(dynamic_.vaddr, dynamic_.filesz, mapped_offset,
                                   "PT_DYNAMIC")) {
                return false;
            }
            if (mapped_offset != dynamic_.offset) {
                add_error(Code::UnsafeLoadMapping,
                          "PT_DYNAMIC file offset disagrees with its PT_LOAD "
                          "mapping",
                          dynamic_.offset, dynamic_.vaddr);
                return false;
            }
        }

        return true;
    }

    bool check_load_overlaps() {
        for (std::size_t i = 0U; i < load_indices_.size(); ++i) {
            const ProgramHeader& left = phdrs_[load_indices_[i]];
            if (left.memsz == 0U) {
                continue;
            }
            for (std::size_t j = i + 1U; j < load_indices_.size(); ++j) {
                const ProgramHeader& right = phdrs_[load_indices_[j]];
                if (right.memsz == 0U) {
                    continue;
                }
                const bool raw_overlap =
                    left.vaddr < right.mem_end && right.vaddr < left.mem_end;
                std::uint64_t left_page_begin = 0U;
                std::uint64_t right_page_begin = 0U;
                std::uint64_t left_page_end = 0U;
                std::uint64_t right_page_end = 0U;
                align_down(left.vaddr, options_.page_size, left_page_begin);
                align_down(right.vaddr, options_.page_size, right_page_begin);
                if (!align_up(left.mem_end, options_.page_size, left_page_end) ||
                    !align_up(right.mem_end, options_.page_size,
                              right_page_end)) {
                    add_error(Code::IntegerOverflow,
                              "PT_LOAD page-aligned range overflows",
                              left.offset, left.vaddr);
                    return false;
                }
                const bool page_overlap =
                    left_page_begin < right_page_end &&
                    right_page_begin < left_page_end;
                if ((raw_overlap || page_overlap) &&
                    left.flags != right.flags) {
                    add_error(Code::OverlappingLoadSegments,
                              "PT_LOAD segments overlap with incompatible "
                              "permissions",
                              left.offset, left.vaddr);
                    return false;
                }
            }
        }
        return true;
    }

    bool read_section_header(std::uint64_t index, SectionHeader& section) {
        std::uint64_t delta = 0U;
        std::uint64_t offset = 0U;
        if (!checked_mul(index, kSectionHeaderSize, delta) ||
            !checked_add(shoff_, delta, offset)) {
            add_error(Code::IntegerOverflow,
                      "section header offset overflow", shoff_);
            return false;
        }
        std::uint64_t entry_end = 0U;
        if (!checked_add(offset, kSectionHeaderSize, entry_end) ||
            entry_end > static_cast<std::uint64_t>(size_)) {
            add_error(Code::OutOfBounds,
                      "section header is outside the file", offset);
            return false;
        }
        if (!read_u32(data_, size_, offset, section.name) ||
            !read_u32(data_, size_, offset + 4U, section.type) ||
            !read_u64(data_, size_, offset + 8U, section.flags) ||
            !read_u64(data_, size_, offset + 16U, section.addr) ||
            !read_u64(data_, size_, offset + 24U, section.offset) ||
            !read_u64(data_, size_, offset + 32U, section.size) ||
            !read_u32(data_, size_, offset + 40U, section.link) ||
            !read_u32(data_, size_, offset + 44U, section.info) ||
            !read_u64(data_, size_, offset + 48U, section.addralign) ||
            !read_u64(data_, size_, offset + 56U, section.entsize)) {
            add_error(Code::TruncatedInput,
                      "section header fields cannot be read", offset);
            return false;
        }
        return true;
    }

    bool validate_section_header(const SectionHeader& section,
                                 std::uint64_t file_offset) {
        if (!checked_add(section.addr, section.size, section_mem_end_)) {
            add_error(Code::IntegerOverflow,
                      "section memory range overflows", file_offset,
                      section.addr);
            return false;
        }
        if (section.offset > static_cast<std::uint64_t>(size_)) {
            add_error(Code::OutOfBounds,
                      "section file offset is outside the file", file_offset,
                      section.addr);
            return false;
        }
        if (section.type != kShtNull && section.type != kShtNobits) {
            std::uint64_t section_file_end = 0U;
            if (!checked_add(section.offset, section.size, section_file_end)) {
                add_error(Code::IntegerOverflow,
                          "section file range overflows", file_offset,
                          section.addr);
                return false;
            }
            if (section_file_end > static_cast<std::uint64_t>(size_)) {
                add_error(Code::OutOfBounds,
                          "section file range is outside the file",
                          section.offset, section.addr);
                return false;
            }
        }
        if (section.addralign > 1U && !is_power_of_two(section.addralign)) {
            add_error(Code::MisalignedStructure,
                      "section alignment is not a power of two", file_offset,
                      section.addr);
            return false;
        }

        if (section.type == kShtRela) {
            if (section.entsize != kRelaEntrySize ||
                (section.size % kRelaEntrySize) != 0U) {
                add_error(Code::MalformedRelocationTable,
                          "SHT_RELA entry size must be 24", file_offset,
                          section.addr);
                return false;
            }
        } else if (section.type == kShtSymtab ||
                   section.type == kShtDynsym) {
            if (section.entsize != kSymbolEntrySize ||
                (section.size % kSymbolEntrySize) != 0U) {
                add_error(Code::MalformedSymbolTable,
                          "symbol table entry size must be 24", file_offset,
                          section.addr);
                return false;
            }
        } else if (section.type == kShtDynamic) {
            if (section.entsize != kDynamicEntrySize ||
                (section.size % kDynamicEntrySize) != 0U) {
                add_error(Code::MalformedDynamicTable,
                          ".dynamic entry size must be 16", file_offset,
                          section.addr);
                return false;
            }
        }
        return true;
    }

    bool parse_section_headers() {
        if (shnum_ == 0U) {
            return true;
        }

        std::uint64_t table_size = 0U;
        std::uint64_t table_end = 0U;
        if (!checked_mul(shnum_, kSectionHeaderSize, table_size) ||
            !checked_add(shoff_, table_size, table_end)) {
            add_error(Code::IntegerOverflow,
                      "section header table size overflows", shoff_);
            return false;
        }
        if (table_end > static_cast<std::uint64_t>(size_)) {
            add_error(Code::OutOfBounds,
                      "section header table is outside the file", shoff_);
            return false;
        }

        sections_.reserve(static_cast<std::size_t>(shnum_));
        for (std::uint64_t i = 0U; i < shnum_; ++i) {
            SectionHeader section;
            if (!read_section_header(i, section)) {
                return false;
            }
            std::uint64_t delta = 0U;
            std::uint64_t file_offset = 0U;
            if (!checked_mul(i, kSectionHeaderSize, delta) ||
                !checked_add(shoff_, delta, file_offset)) {
                add_error(Code::IntegerOverflow,
                          "section header offset overflow", shoff_);
                return false;
            }
            if (!validate_section_header(section, file_offset)) {
                return false;
            }
            section.mem_end = section_mem_end_;
            sections_.push_back(std::move(section));
        }

        if (!sections_.empty() && sections_[0].type != kShtNull) {
            add_error(Code::MalformedSectionHeaders,
                      "section zero must be SHT_NULL", shoff_);
            return false;
        }

        const std::uint8_t* shstr_data = nullptr;
        std::size_t shstr_size = 0U;
        if (shstrndx_ != 0U) {
            const SectionHeader& shstr = sections_[static_cast<std::size_t>(shstrndx_)];
            if (shstr.type != kShtStrtab) {
                add_error(Code::MalformedStringTable,
                          "e_shstrndx does not reference SHT_STRTAB",
                          shstr.offset, shstr.addr);
                return false;
            }
            if (shstr.size > static_cast<std::uint64_t>(
                                 std::numeric_limits<std::size_t>::max())) {
                add_error(Code::IntegerOverflow,
                          "section string table is too large", shstr.offset);
                return false;
            }
            shstr_data = data_ + static_cast<std::size_t>(shstr.offset);
            shstr_size = static_cast<std::size_t>(shstr.size);
        }

        for (SectionHeader& section : sections_) {
            if (shstr_data == nullptr) {
                if (section.name != 0U) {
                    add_error(Code::MalformedStringTable,
                              "section name is set without a section string "
                              "table");
                    return false;
                }
                continue;
            }
            if (static_cast<std::uint64_t>(section.name) >=
                static_cast<std::uint64_t>(shstr_size)) {
                add_error(Code::MalformedStringTable,
                          "section name offset is outside .shstrtab");
                return false;
            }
            std::size_t pos = static_cast<std::size_t>(section.name);
            const std::size_t start = pos;
            while (pos < shstr_size && shstr_data[pos] != 0U) {
                ++pos;
            }
            if (pos == shstr_size) {
                add_error(Code::MalformedStringTable,
                          "section name is not NUL-terminated");
                return false;
            }
            section.name_text.assign(
                reinterpret_cast<const char*>(shstr_data + start), pos - start);
        }

        if (has_dynamic_) {
            const SectionHeader* dynamic_section = nullptr;
            for (const SectionHeader& section : sections_) {
                if (section.name_text != ".dynamic") {
                    continue;
                }
                if (dynamic_section != nullptr) {
                    add_error(Code::MalformedSectionHeaders,
                              "multiple .dynamic sections are ambiguous");
                    return false;
                }
                dynamic_section = &section;
            }
            if (dynamic_section != nullptr) {
                if (dynamic_section->type != kShtDynamic) {
                    add_error(Code::MalformedDynamicTable,
                              ".dynamic section does not have SHT_DYNAMIC",
                              dynamic_section->offset, dynamic_section->addr);
                    return false;
                }
                if (dynamic_section->addr != dynamic_.vaddr ||
                    dynamic_section->size != dynamic_.filesz) {
                    add_error(Code::MalformedDynamicTable,
                              ".dynamic section and PT_DYNAMIC disagree",
                              dynamic_section->offset, dynamic_section->addr);
                    return false;
                }
            }
        }

        return true;
    }

    bool map_virtual_range(std::uint64_t address, std::uint64_t length,
                           std::uint64_t& file_offset,
                           const std::string& description) {
        std::uint64_t end = 0U;
        if (!checked_add(address, length, end)) {
            add_error(Code::IntegerOverflow,
                      description + " virtual range overflows", 0U, address);
            return false;
        }

        std::uint64_t found_offset = 0U;
        bool found = false;
        bool ambiguous = false;
        for (const std::size_t index : load_indices_) {
            const ProgramHeader& load = phdrs_[index];
            const std::uint64_t load_file_end = load.vaddr + load.filesz;
            if (address < load.vaddr || end > load_file_end) {
                continue;
            }
            const std::uint64_t candidate =
                load.offset + (address - load.vaddr);
            if (!found) {
                found = true;
                found_offset = candidate;
            } else if (candidate != found_offset) {
                ambiguous = true;
            }
        }

        if (ambiguous) {
            add_error(Code::UnsafeLoadMapping,
                      description + " maps ambiguously through PT_LOAD",
                      0U, address);
            return false;
        }
        if (!found) {
            add_error(Code::UnsafeLoadMapping,
                      description + " is not backed by a PT_LOAD file range",
                      0U, address);
            return false;
        }
        file_offset = found_offset;
        return true;
    }

    bool range_inside_writable_load(std::uint64_t address,
                                    std::uint64_t length) const {
        std::uint64_t end = 0U;
        if (!checked_add(address, length, end)) {
            return false;
        }
        for (const std::size_t index : load_indices_) {
            const ProgramHeader& load = phdrs_[index];
            if ((load.flags & kPfW) != 0U && address >= load.vaddr &&
                end <= load.mem_end) {
                return true;
            }
        }
        return false;
    }

    bool load_file_end_for_vaddr(std::uint64_t address,
                                 std::uint64_t& file_end) const {
        bool found = false;
        for (const std::size_t index : load_indices_) {
            const ProgramHeader& load = phdrs_[index];
            const std::uint64_t virtual_file_end = load.vaddr + load.filesz;
            if (address < load.vaddr || address >= virtual_file_end) {
                continue;
            }
            if (!found || load.file_end < file_end) {
                file_end = load.file_end;
            }
            found = true;
        }
        return found;
    }

    bool range_inside_readonly_exec_load(std::uint64_t address,
                                         std::uint64_t length,
                                         bool& mapped,
                                         bool& writable) const {
        std::uint64_t end = 0U;
        if (!checked_add(address, length, end)) {
            return false;
        }
        bool readonly_exec = false;
        mapped = false;
        writable = false;
        for (const std::size_t index : load_indices_) {
            const ProgramHeader& load = phdrs_[index];
            if (address < load.vaddr || end > load.mem_end) {
                continue;
            }
            mapped = true;
            if ((load.flags & kPfW) != 0U) {
                writable = true;
            }
            if ((load.flags & kPfW) == 0U &&
                (load.flags & kPfR) != 0U &&
                (load.flags & kPfX) != 0U) {
                readonly_exec = true;
            }
        }
        return readonly_exec;
    }

    bool parse_dynamic() {
        if (!has_dynamic_) {
            add_error(Code::MissingDynamicSegment,
                      "ELF image has no PT_DYNAMIC segment");
            return false;
        }

        std::uint64_t count = dynamic_.filesz / kDynamicEntrySize;
        bool terminated = false;
        for (std::uint64_t i = 0U; i < count; ++i) {
            std::uint64_t entry_offset = 0U;
            std::uint64_t delta = 0U;
            if (!checked_mul(i, kDynamicEntrySize, delta) ||
                !checked_add(dynamic_.offset, delta, entry_offset)) {
                add_error(Code::IntegerOverflow,
                          "dynamic entry offset overflows", dynamic_.offset,
                          dynamic_.vaddr);
                return false;
            }
            std::uint64_t raw_tag = 0U;
            DynamicEntry entry;
            if (!read_u64(data_, size_, entry_offset, raw_tag) ||
                !read_u64(data_, size_, entry_offset + 8U, entry.value)) {
                add_error(Code::TruncatedInput,
                          "dynamic entry cannot be read", entry_offset);
                return false;
            }
            entry.tag = static_cast<std::int64_t>(raw_tag);
            report_.dynamic_entries.push_back(entry);
            if (entry.tag == kDtNull) {
                terminated = true;
                break;
            }
        }
        if (!terminated) {
            add_error(Code::MalformedDynamicTable,
                      "PT_DYNAMIC has no DT_NULL terminator",
                      dynamic_.offset, dynamic_.vaddr);
            return false;
        }

        if (!collect_dynamic_metadata()) {
            return false;
        }
        if (!parse_dynamic_strings()) {
            return false;
        }
        return true;
    }

    bool collect_dynamic_metadata() {
        bool has_strtab = false;
        bool has_strtab_size = false;
        bool has_symtab = false;
        bool has_syment = false;
        bool has_hash = false;
        bool has_gnu_hash = false;
        bool has_pltgot = false;
        bool has_rela = false;
        bool has_rela_size = false;
        bool has_jmprel = false;
        bool has_pltrel_size = false;
        bool has_pltrel = false;

        for (const DynamicEntry& entry : report_.dynamic_entries) {
            switch (entry.tag) {
                case kDtStrtab:
                    if (has_strtab && dynstr_address_ != entry.value) {
                        add_error(Code::MalformedStringTable,
                                  "conflicting DT_STRTAB values");
                        return false;
                    }
                    has_strtab = true;
                    dynstr_address_ = entry.value;
                    break;
                case kDtStrtabSz:
                    if (has_strtab_size && dynstr_size_ != entry.value) {
                        add_error(Code::MalformedStringTable,
                                  "conflicting DT_STRSZ values");
                        return false;
                    }
                    has_strtab_size = true;
                    dynstr_size_ = entry.value;
                    break;
                case kDtSymtab:
                    if (has_symtab && dynsym_address_ != entry.value) {
                        add_error(Code::MalformedSymbolTable,
                                  "conflicting DT_SYMTAB values");
                        return false;
                    }
                    has_symtab = true;
                    dynsym_address_ = entry.value;
                    break;
                case kDtSyment:
                    if (entry.value != kSymbolEntrySize) {
                        add_error(Code::MalformedSymbolTable,
                                  "DT_SYMENT must be 24");
                        return false;
                    }
                    has_syment = true;
                    break;
                case kDtHash:
                    has_hash = true;
                    hash_address_ = entry.value;
                    break;
                case kDtGnuHash:
                    has_gnu_hash = true;
                    gnu_hash_address_ = entry.value;
                    break;
                case kDtPltGot:
                    if (entry.value != 0U) {
                        has_pltgot = true;
                        pltgot_address_ = entry.value;
                    }
                    break;
                case kDtRela:
                    has_rela = true;
                    rela_address_ = entry.value;
                    break;
                case kDtRelaSz:
                    has_rela_size = true;
                    rela_size_ = entry.value;
                    break;
                case kDtRelaEnt:
                    if (entry.value != kRelaEntrySize) {
                        add_error(Code::MalformedRelocationTable,
                                  "DT_RELAENT must be 24");
                        return false;
                    }
                    break;
                case kDtJmprel:
                    has_jmprel = true;
                    jmprel_address_ = entry.value;
                    break;
                case kDtPltRelSz:
                    has_pltrel_size = true;
                    pltrel_size_ = entry.value;
                    break;
                case kDtPltRel:
                    has_pltrel = true;
                    pltrel_value_ = entry.value;
                    break;
                case kDtBindNow:
                    report_.immediate_binding = true;
                    break;
                case kDtFlags:
                    if ((entry.value & kDfBindNow) != 0U) {
                        report_.immediate_binding = true;
                    }
                    break;
                case kDtFlags1:
                    if ((entry.value & kDf1Now) != 0U) {
                        report_.immediate_binding = true;
                    }
                    break;
                default:
                    break;
            }
        }

        if (has_rela != has_rela_size) {
            add_error(Code::MalformedRelocationTable,
                      "DT_RELA and DT_RELASZ must appear together");
            return false;
        }
        if (has_jmprel != has_pltrel_size) {
            add_error(Code::MalformedRelocationTable,
                      "DT_JMPREL and DT_PLTRELSZ must appear together");
            return false;
        }
        if (has_jmprel && !has_pltrel) {
            add_error(Code::MalformedRelocationTable,
                      "DT_JMPREL requires DT_PLTREL");
            return false;
        }
        if (has_pltrel && pltrel_value_ != kDtRelaValue) {
            add_error(Code::MalformedRelocationTable,
                      "x86-64 DT_PLTREL must identify DT_RELA");
            return false;
        }
        if (has_rela && rela_size_ != 0U && (rela_size_ % kRelaEntrySize) != 0U) {
            add_error(Code::MalformedRelocationTable,
                      "DT_RELASZ is not a multiple of 24");
            return false;
        }
        if (has_jmprel && (pltrel_size_ % kRelaEntrySize) != 0U) {
            add_error(Code::MalformedRelocationTable,
                      "DT_PLTRELSZ is not a multiple of 24");
            return false;
        }
        if (has_symtab != has_syment) {
            add_error(Code::MalformedSymbolTable,
                      "DT_SYMTAB and DT_SYMENT must appear together");
            return false;
        }
        if (has_hash && hash_address_ == 0U) {
            add_error(Code::MalformedSymbolTable, "DT_HASH address is zero");
            return false;
        }
        if (has_gnu_hash && gnu_hash_address_ == 0U) {
            add_error(Code::MalformedSymbolTable, "DT_GNU_HASH address is zero");
            return false;
        }

        if (has_strtab != has_strtab_size) {
            add_error(Code::MalformedStringTable,
                      "DT_STRTAB and DT_STRSZ must appear together");
            return false;
        }
        if (has_strtab) {
            if (dynstr_size_ == 0U) {
                add_error(Code::MalformedStringTable,
                          "DT_STRSZ must be nonzero");
                return false;
            }
            if (!map_virtual_range(dynstr_address_, dynstr_size_,
                                   dynstr_file_offset_, "DT_STRTAB")) {
                return false;
            }
            if (dynstr_file_offset_ > static_cast<std::uint64_t>(size_) ||
                dynstr_size_ > static_cast<std::uint64_t>(size_) -
                                   dynstr_file_offset_) {
                add_error(Code::OutOfBounds,
                          "DT_STRTAB is outside the file",
                          dynstr_file_offset_, dynstr_address_);
                return false;
            }
            const std::uint64_t last =
                dynstr_file_offset_ + dynstr_size_ - 1U;
            if (data_[static_cast<std::size_t>(last)] != 0U) {
                add_error(Code::MalformedStringTable,
                          "DT_STRTAB is not NUL-terminated",
                          dynstr_file_offset_, dynstr_address_);
                return false;
            }
            has_dynstr_ = true;
        }

        if (has_symtab) {
            std::uint64_t dummy = 0U;
            if (!map_virtual_range(dynsym_address_, kSymbolEntrySize, dummy,
                                   "DT_SYMTAB")) {
                return false;
            }
            dynsym_file_offset_ = dummy;
        }
        if (has_pltgot && !range_inside_writable_load(pltgot_address_, 8U)) {
            add_error(Code::UnsafeLoadMapping,
                      "DT_PLTGOT is not inside a writable PT_LOAD",
                      0U, pltgot_address_);
            return false;
        }

        return true;
    }

    bool read_dynstr(std::uint64_t offset, std::string& value,
                     const std::string& description) {
        if (!has_dynstr_) {
            add_error(Code::MalformedStringTable,
                      description + " requires DT_STRTAB");
            return false;
        }
        if (offset >= dynstr_size_) {
            add_error(Code::MalformedStringTable,
                      description + " string offset is outside DT_STRTAB");
            return false;
        }
        const std::uint64_t file_offset = dynstr_file_offset_ + offset;
        const std::size_t start = static_cast<std::size_t>(file_offset);
        const std::uint64_t max_length = dynstr_size_ - offset;
        std::size_t length = 0U;
        while (static_cast<std::uint64_t>(length) < max_length &&
               data_[start + length] != 0U) {
            ++length;
        }
        if (static_cast<std::uint64_t>(length) == max_length) {
            add_error(Code::MalformedStringTable,
                      description + " string is not NUL-terminated",
                      file_offset);
            return false;
        }
        value.assign(reinterpret_cast<const char*>(data_ + start), length);
        return true;
    }

    bool parse_dynamic_strings() {
        for (const DynamicEntry& entry : report_.dynamic_entries) {
            const bool is_path =
                entry.tag == kDtRpath || entry.tag == kDtRunpath;
            const bool is_string =
                entry.tag == kDtNeeded || entry.tag == kDtSoname ||
                entry.tag == kDtFilter || entry.tag == kDtAuxiliary ||
                is_path;
            if (!is_string) {
                continue;
            }

            std::string value;
            const char* tag_name = entry.tag == kDtRpath
                                       ? "DT_RPATH"
                                       : (entry.tag == kDtRunpath
                                              ? "DT_RUNPATH"
                                              : "dynamic string");
            if (!read_dynstr(entry.value, value, tag_name)) {
                return false;
            }
            if (is_path) {
                if (entry.tag == kDtRpath) {
                    report_.has_rpath = true;
                } else {
                    report_.has_runpath = true;
                }
                analyze_runtime_path(entry.tag, value);
            }
        }
        return true;
    }

    static std::size_t origin_prefix_length(const std::string& segment) {
        static const std::string literal = "$ORIGIN";
        static const std::string braced = "${ORIGIN}";
        if (segment.compare(0U, literal.size(), literal) == 0 &&
            (segment.size() == literal.size() ||
             segment[literal.size()] == '/')) {
            return literal.size();
        }
        if (segment.compare(0U, braced.size(), braced) == 0 &&
            (segment.size() == braced.size() ||
             segment[braced.size()] == '/')) {
            return braced.size();
        }
        return 0U;
    }

    static bool origin_path_escapes(const std::string& segment,
                                    std::size_t position) {
        std::int64_t depth = 0;
        while (position < segment.size()) {
            if (segment[position] == '/') {
                ++position;
                continue;
            }
            const std::size_t end = segment.find('/', position);
            const std::size_t component_end =
                end == std::string::npos ? segment.size() : end;
            const std::string component =
                segment.substr(position, component_end - position);
            if (component == "..") {
                --depth;
                if (depth < 0) {
                    return true;
                }
            } else if (!component.empty() && component != ".") {
                ++depth;
            }
            position = component_end;
        }
        return false;
    }

    void analyze_runtime_path(std::int64_t tag, const std::string& value) {
        RuntimePath path;
        path.tag = tag;
        path.value = value;

        std::size_t start = 0U;
        while (start <= value.size()) {
            const std::size_t separator = value.find(':', start);
            const std::size_t end =
                separator == std::string::npos ? value.size() : separator;
            const std::string segment = value.substr(start, end - start);
            path.segments.push_back(segment);

            const std::string tag_name =
                tag == kDtRpath ? "DT_RPATH" : "DT_RUNPATH";
            if (segment.empty()) {
                path.safe = false;
                add_error(Code::EmptyRuntimePath,
                          tag_name +
                              " contains an empty search segment (current "
                              "directory)");
            } else if (segment.front() == '/') {
                // Absolute path, no current-directory fallback.
            } else {
                const std::size_t prefix = origin_prefix_length(segment);
                if (prefix == 0U) {
                    path.safe = false;
                    add_error(Code::NonAbsoluteRuntimePath,
                              tag_name + " contains a non-absolute segment: " +
                                  segment);
                } else if (origin_path_escapes(segment, prefix)) {
                    path.safe = false;
                    add_error(Code::UnboundedOriginPath,
                              tag_name +
                                  " permits $ORIGIN traversal outside the "
                                  "origin directory: " +
                                  segment);
                }
            }

            if (separator == std::string::npos) {
                break;
            }
            start = end + 1U;
        }
        report_.runtime_paths.push_back(std::move(path));
    }

    bool scan_rela_table(std::uint64_t file_offset, std::uint64_t table_size,
                         const std::string& description) {
        if ((table_size % kRelaEntrySize) != 0U) {
            add_error(Code::MalformedRelocationTable,
                      description + " size is not a multiple of 24",
                      file_offset);
            return false;
        }
        std::uint64_t table_end = 0U;
        if (!checked_add(file_offset, table_size, table_end) ||
            table_end > static_cast<std::uint64_t>(size_)) {
            add_error(Code::OutOfBounds,
                      description + " is outside the file", file_offset);
            return false;
        }

        const std::uint64_t count = table_size / kRelaEntrySize;
        for (std::uint64_t i = 0U; i < count; ++i) {
            std::uint64_t entry_offset = 0U;
            std::uint64_t delta = 0U;
            if (!checked_mul(i, kRelaEntrySize, delta) ||
                !checked_add(file_offset, delta, entry_offset)) {
                add_error(Code::IntegerOverflow,
                          description + " offset overflows", file_offset);
                return false;
            }
            std::uint64_t relocation_offset = 0U;
            std::uint64_t relocation_info = 0U;
            std::uint64_t raw_addend = 0U;
            if (!read_u64(data_, size_, entry_offset, relocation_offset) ||
                !read_u64(data_, size_, entry_offset + 8U, relocation_info) ||
                !read_u64(data_, size_, entry_offset + 16U, raw_addend)) {
                add_error(Code::TruncatedInput,
                          description + " entry cannot be read", entry_offset);
                return false;
            }

            bool destination_mapped = false;
            for (const std::size_t load_index : load_indices_) {
                const ProgramHeader& load = phdrs_[load_index];
                if (relocation_offset >= load.vaddr &&
                    relocation_offset < load.mem_end) {
                    destination_mapped = true;
                    break;
                }
            }
            if (!destination_mapped) {
                add_error(Code::MalformedRelocationTable,
                          description +
                              " relocation destination is outside PT_LOAD",
                          entry_offset, relocation_offset);
                return false;
            }

            const std::uint32_t relocation_type =
                static_cast<std::uint32_t>(relocation_info & 0xffffffffU);
            if (relocation_type == kRNone) {
                continue;
            }
            if (relocation_type == kRGlobDat ||
                relocation_type == kRJumpSlot ||
                relocation_type == kRIrelative) {
                bool already_recorded = false;
                for (const std::uint64_t recorded :
                     got_relocation_offsets_) {
                    if (recorded == relocation_offset) {
                        already_recorded = true;
                        break;
                    }
                }
                if (!already_recorded) {
                    got_relocation_offsets_.push_back(relocation_offset);
                }
            }
            if (relocation_type == kRIrelative) {
                if (raw_addend >
                    static_cast<std::uint64_t>(
                        std::numeric_limits<std::int64_t>::max())) {
                    report_.unsafe_ifunc = true;
                    add_error(Code::WritableIrelativeResolver,
                              "R_X86_64_IRELATIVE has a negative resolver "
                              "addend",
                              entry_offset);
                    continue;
                }
                check_resolver(raw_addend, true, entry_offset);
            }
        }
        return true;
    }

    void check_resolver(std::uint64_t address, bool irelative,
                        std::uint64_t file_offset) {
        for (const std::pair<std::uint64_t, bool>& checked :
             checked_resolvers_) {
            if (checked.first == address && checked.second == irelative) {
                return;
            }
        }
        checked_resolvers_.push_back(std::make_pair(address, irelative));

        bool mapped = false;
        bool writable = false;
        const bool readonly_exec =
            range_inside_readonly_exec_load(address, 1U, mapped, writable);
        if (readonly_exec && !writable) {
            return;
        }

        report_.unsafe_ifunc = true;
        const std::string source =
            irelative ? "R_X86_64_IRELATIVE" : "STT_GNU_IFUNC";
        if (!mapped) {
            add_error(irelative ? Code::UnmappedIrelativeResolver
                                : Code::UnmappedIfuncResolver,
                      source +
                          " resolver does not map to a read-only executable "
                          "PT_LOAD: " +
                          hex_value(address),
                      file_offset, address);
            return;
        }
        add_error(irelative ? Code::WritableIrelativeResolver
                            : Code::WritableIfuncResolver,
                  source +
                      " resolver maps to a writable or non-executable "
                      "PT_LOAD: " +
                      hex_value(address),
                  file_offset, address);
    }

    bool scan_dynamic_rela(std::uint64_t address, std::uint64_t table_size,
                           const std::string& description) {
        if (table_size == 0U) {
            return true;
        }
        std::uint64_t file_offset = 0U;
        if (!map_virtual_range(address, table_size, file_offset, description)) {
            return false;
        }
        return scan_rela_table(file_offset, table_size, description);
    }

    bool parse_relocations() {
        bool has_rela = false;
        bool has_jmprel = false;
        std::uint64_t rela_address = 0U;
        std::uint64_t rela_size = 0U;
        std::uint64_t jmprel_address = 0U;
        std::uint64_t pltrel_size = 0U;

        for (const DynamicEntry& entry : report_.dynamic_entries) {
            if (entry.tag == kDtRela) {
                has_rela = true;
                rela_address = entry.value;
            } else if (entry.tag == kDtRelaSz) {
                rela_size = entry.value;
            } else if (entry.tag == kDtJmprel) {
                has_jmprel = true;
                jmprel_address = entry.value;
            } else if (entry.tag == kDtPltRelSz) {
                pltrel_size = entry.value;
            }
        }

        if (has_rela &&
            !scan_dynamic_rela(rela_address, rela_size, "DT_RELA")) {
            return false;
        }
        if (has_jmprel &&
            !scan_dynamic_rela(jmprel_address, pltrel_size, "DT_JMPREL")) {
            return false;
        }

        for (const SectionHeader& section : sections_) {
            if (section.type != kShtRela) {
                continue;
            }
            if (section.size == 0U) {
                continue;
            }
            std::string description = ".rela section";
            if (!section.name_text.empty()) {
                description = section.name_text;
            }
            if (!scan_rela_table(section.offset, section.size, description)) {
                return false;
            }
        }
        return true;
    }

    bool read_symbol(std::uint64_t table_file_offset, std::uint64_t index,
                     std::uint8_t& info, std::uint16_t& shndx,
                     std::uint64_t& value) {
        std::uint64_t delta = 0U;
        std::uint64_t offset = 0U;
        if (!checked_mul(index, kSymbolEntrySize, delta) ||
            !checked_add(table_file_offset, delta, offset)) {
            add_error(Code::IntegerOverflow, "symbol table offset overflows",
                      table_file_offset);
            return false;
        }
        std::uint8_t other = 0U;
        std::uint64_t size = 0U;
        if (!read_u8(data_, size_, offset + 4U, info) ||
            !read_u8(data_, size_, offset + 5U, other) ||
            !read_u16(data_, size_, offset + 6U, shndx) ||
            !read_u64(data_, size_, offset + 8U, value) ||
            !read_u64(data_, size_, offset + 16U, size)) {
            add_error(Code::TruncatedInput,
                      "symbol table entry cannot be read", offset);
            return false;
        }
        (void)other;
        (void)size;
        return true;
    }

    bool inspect_symbols(std::uint64_t table_file_offset,
                         std::uint64_t count) {
        for (std::uint64_t i = 0U; i < count; ++i) {
            std::uint8_t info = 0U;
            std::uint16_t shndx = 0U;
            std::uint64_t value = 0U;
            if (!read_symbol(table_file_offset, i, info, shndx, value)) {
                return false;
            }
            const std::uint8_t symbol_type =
                static_cast<std::uint8_t>(info & 0x0fU);
            if (symbol_type != kSttGnuIfunc) {
                continue;
            }
            if (shndx == kShnUndef && value == 0U) {
                continue;
            }
            std::uint64_t entry_offset = 0U;
            std::uint64_t delta = 0U;
            if (checked_mul(i, kSymbolEntrySize, delta) &&
                checked_add(table_file_offset, delta, entry_offset)) {
                check_resolver(value, false, entry_offset);
            }
        }
        return true;
    }

    bool try_hash_symbol_count(std::uint64_t hash_address,
                               std::uint64_t& count) {
        std::uint64_t hash_file_offset = 0U;
        if (!map_virtual_range(hash_address, 8U, hash_file_offset, "DT_HASH")) {
            return false;
        }
        std::uint32_t bucket_count = 0U;
        std::uint32_t chain_count = 0U;
        if (!read_u32(data_, size_, hash_file_offset, bucket_count) ||
            !read_u32(data_, size_, hash_file_offset + 4U, chain_count)) {
            add_error(Code::TruncatedInput,
                      "DT_HASH header cannot be read", hash_file_offset);
            return false;
        }
        (void)bucket_count;
        count = static_cast<std::uint64_t>(chain_count);
        return true;
    }

    bool try_gnu_hash_symbol_count(std::uint64_t hash_address,
                                   std::uint64_t& count) {
        std::uint64_t hash_file_offset = 0U;
        if (!map_virtual_range(hash_address, 16U, hash_file_offset,
                               "DT_GNU_HASH")) {
            return false;
        }
        std::uint32_t bucket_count = 0U;
        std::uint32_t symbol_offset = 0U;
        std::uint32_t bloom_count = 0U;
        std::uint32_t bloom_shift = 0U;
        if (!read_u32(data_, size_, hash_file_offset, bucket_count) ||
            !read_u32(data_, size_, hash_file_offset + 4U, symbol_offset) ||
            !read_u32(data_, size_, hash_file_offset + 8U, bloom_count) ||
            !read_u32(data_, size_, hash_file_offset + 12U, bloom_shift)) {
            add_error(Code::TruncatedInput,
                      "DT_GNU_HASH header cannot be read", hash_file_offset);
            return false;
        }
        (void)bloom_shift;

        std::uint64_t hash_load_file_end = 0U;
        if (!load_file_end_for_vaddr(hash_address, hash_load_file_end)) {
            add_error(Code::UnsafeLoadMapping,
                      "DT_GNU_HASH is not inside a file-backed PT_LOAD", 0U,
                      hash_address);
            return false;
        }

        std::uint64_t cursor = hash_file_offset + 16U;
        std::uint64_t bloom_bytes = 0U;
        if (!checked_mul(static_cast<std::uint64_t>(bloom_count), 8U,
                         bloom_bytes) ||
            !checked_add(cursor, bloom_bytes, cursor)) {
            add_error(Code::IntegerOverflow,
                      "DT_GNU_HASH bloom filter overflows", hash_file_offset);
            return false;
        }
        std::uint64_t bucket_bytes = 0U;
        if (!checked_mul(static_cast<std::uint64_t>(bucket_count), 4U,
                         bucket_bytes) ||
            !checked_add(cursor, bucket_bytes, cursor)) {
            add_error(Code::IntegerOverflow,
                      "DT_GNU_HASH bucket table overflows", hash_file_offset);
            return false;
        }
        if (cursor > hash_load_file_end) {
            add_error(Code::OutOfBounds,
                      "DT_GNU_HASH fixed tables are outside its PT_LOAD",
                      hash_file_offset);
            return false;
        }

        const std::uint64_t buckets_file_offset =
            hash_file_offset + 16U + bloom_bytes;
        const std::uint64_t chains_file_offset = cursor;
        const std::uint64_t max_chain_entries =
            (hash_load_file_end - chains_file_offset) / 4U;
        std::uint64_t maximum_symbol = static_cast<std::uint64_t>(symbol_offset);
        if (maximum_symbol != 0U) {
            --maximum_symbol;
        }

        for (std::uint32_t i = 0U; i < bucket_count; ++i) {
            std::uint32_t symbol = 0U;
            if (!read_u32(data_, size_,
                          buckets_file_offset +
                              static_cast<std::uint64_t>(i) * 4U,
                          symbol)) {
                add_error(Code::TruncatedInput,
                          "DT_GNU_HASH bucket cannot be read",
                          buckets_file_offset);
                return false;
            }
            if (symbol == 0U) {
                continue;
            }
            if (symbol < symbol_offset) {
                add_error(Code::MalformedSymbolTable,
                          "DT_GNU_HASH bucket precedes symoffset",
                          hash_file_offset);
                return false;
            }
            std::uint64_t chain_index =
                static_cast<std::uint64_t>(symbol - symbol_offset);
            std::uint64_t walks = 0U;
            for (;;) {
                if (chain_index >= max_chain_entries ||
                    walks >= max_chain_entries) {
                    add_error(Code::MalformedSymbolTable,
                              "DT_GNU_HASH chain is unterminated or outside "
                              "the file",
                              chains_file_offset);
                    return false;
                }
                std::uint32_t chain_value = 0U;
                if (!read_u32(data_, size_,
                              chains_file_offset + chain_index * 4U,
                              chain_value)) {
                    add_error(Code::TruncatedInput,
                              "DT_GNU_HASH chain cannot be read",
                              chains_file_offset);
                    return false;
                }
                ++walks;
                const std::uint64_t current_symbol =
                    static_cast<std::uint64_t>(symbol_offset) + chain_index;
                if (current_symbol > maximum_symbol) {
                    maximum_symbol = current_symbol;
                }
                if ((chain_value & 1U) != 0U) {
                    break;
                }
                ++chain_index;
            }
        }

        count = maximum_symbol + 1U;
        return true;
    }

    bool parse_symbols() {
        for (const SectionHeader& section : sections_) {
            if (section.type != kShtSymtab && section.type != kShtDynsym) {
                continue;
            }
            if (section.size == 0U) {
                continue;
            }
            const std::uint64_t count = section.size / kSymbolEntrySize;
            if (!inspect_symbols(section.offset, count)) {
                return false;
            }
        }

        if (dynsym_file_offset_ == kNoValue) {
            return true;
        }

        std::uint64_t count = 0U;
        bool count_found = false;
        for (const SectionHeader& section : sections_) {
            if (section.type == kShtDynsym &&
                section.addr == dynsym_address_ &&
                section.size >= kSymbolEntrySize) {
                const std::uint64_t section_count =
                    section.size / kSymbolEntrySize;
                if (section_count > count) {
                    count = section_count;
                }
                count_found = true;
            }
        }
        if (hash_address_ != kNoValue) {
            std::uint64_t hash_count = 0U;
            if (!try_hash_symbol_count(hash_address_, hash_count)) {
                return false;
            }
            if (hash_count > count) {
                count = hash_count;
            }
            count_found = true;
        }
        if (gnu_hash_address_ != kNoValue) {
            std::uint64_t gnu_hash_count = 0U;
            if (!try_gnu_hash_symbol_count(gnu_hash_address_,
                                           gnu_hash_count)) {
                return false;
            }
            if (gnu_hash_count > count) {
                count = gnu_hash_count;
            }
            count_found = true;
        }
        if (!count_found) {
            add_error(Code::MalformedSymbolTable,
                      "dynamic symbol count cannot be determined without a "
                      "dynamic symbol section or hash table");
            return false;
        }

        std::uint64_t table_size = 0U;
        if (!checked_mul(count, kSymbolEntrySize, table_size)) {
            add_error(Code::IntegerOverflow,
                      "dynamic symbol table size overflows");
            return false;
        }
        std::uint64_t ignored = 0U;
        if (!map_virtual_range(dynsym_address_, table_size, ignored,
                               "DT_SYMTAB")) {
            return false;
        }
        return inspect_symbols(dynsym_file_offset_, count);
    }

    bool align_down(std::uint64_t value, std::uint64_t alignment,
                    std::uint64_t& result) const {
        result = value & ~(alignment - 1U);
        return true;
    }

    bool align_up(std::uint64_t value, std::uint64_t alignment,
                  std::uint64_t& result) const {
        if (value > std::numeric_limits<std::uint64_t>::max() -
                        (alignment - 1U)) {
            return false;
        }
        result = (value + alignment - 1U) & ~(alignment - 1U);
        return true;
    }

    bool has_section_named(const std::string& name) const {
        for (const SectionHeader& section : sections_) {
            if (section.name_text == name) {
                return true;
            }
        }
        return false;
    }

    void add_target(std::vector<TargetRange>& targets, const std::string& name,
                    std::uint64_t begin, std::uint64_t end) {
        if (end < begin) {
            return;
        }
        targets.push_back(TargetRange{name, begin, end});
    }

    void collect_relro_targets(std::vector<TargetRange>& targets) {
        add_target(targets, "PT_DYNAMIC", dynamic_.vaddr, dynamic_.mem_end);

        for (const SectionHeader& section : sections_) {
            if (section.name_text == ".got" ||
                section.name_text == ".got.plt" ||
                section.name_text == ".init_array" ||
                section.name_text == ".fini_array") {
                add_target(targets, section.name_text, section.addr,
                           section.mem_end);
            }
        }

        for (const DynamicEntry& entry : report_.dynamic_entries) {
            if (entry.tag != kDtInitArray && entry.tag != kDtFiniArray &&
                entry.tag != kDtPreinitArray) {
                continue;
            }
            std::int64_t size_tag = 0;
            const char* name = "";
            if (entry.tag == kDtInitArray) {
                size_tag = kDtInitArraySz;
                name = "DT_INIT_ARRAY";
            } else if (entry.tag == kDtFiniArray) {
                size_tag = kDtFiniArraySz;
                name = "DT_FINI_ARRAY";
            } else {
                size_tag = kDtPreinitArraySz;
                name = "DT_PREINIT_ARRAY";
            }
            std::uint64_t size = kNoValue;
            for (const DynamicEntry& candidate : report_.dynamic_entries) {
                if (candidate.tag == size_tag) {
                    size = candidate.value;
                    break;
                }
            }
            if (size == kNoValue) {
                add_error(Code::MalformedDynamicTable,
                          std::string(name) + " size tag is missing");
                continue;
            }
            std::uint64_t end = 0U;
            if (!checked_add(entry.value, size, end)) {
                add_error(Code::IntegerOverflow,
                          std::string(name) + " range overflows", 0U,
                          entry.value);
                continue;
            }
            if (size != 0U) {
                add_target(targets, name, entry.value, end);
            }
        }

        for (const std::uint64_t relocation_offset :
             got_relocation_offsets_) {
            std::uint64_t end = 0U;
            if (!checked_add(relocation_offset, 8U, end)) {
                add_error(Code::IntegerOverflow,
                          "GOT relocation target range overflows", 0U,
                          relocation_offset);
                continue;
            }
            add_target(targets, "GOT relocation target", relocation_offset,
                       end);
        }

        if (pltgot_address_ != kNoValue) {
            std::uint64_t end = 0U;
            if (checked_add(pltgot_address_, 8U, end)) {
                add_target(targets, "DT_PLTGOT", pltgot_address_, end);
            }
        }
    }

    bool has_dynamic_relocations() const {
        for (const DynamicEntry& entry : report_.dynamic_entries) {
            if ((entry.tag == kDtRelaSz || entry.tag == kDtPltRelSz) &&
                entry.value != 0U) {
                return true;
            }
        }
        return false;
    }

    void check_relro() {
        if (!has_relro_) {
            report_.relro = RelroStatus::None;
            add_error(Code::MissingRelro,
                      "ELF image has no PT_GNU_RELRO segment");
            return;
        }

        bool coverage_complete = true;
        std::uint64_t relro_page_begin = 0U;
        std::uint64_t relro_end = 0U;
        std::uint64_t relro_page_end = 0U;
        align_down(relro_.vaddr, options_.page_size, relro_page_begin);

        if (!checked_add(relro_.vaddr, relro_.memsz, relro_end)) {
            add_error(Code::IntegerOverflow,
                      "PT_GNU_RELRO memory range overflows", relro_.offset,
                      relro_.vaddr);
            coverage_complete = false;
        } else if (!align_up(relro_end, options_.page_size, relro_page_end)) {
            add_error(Code::IntegerOverflow,
                      "PT_GNU_RELRO page-aligned range overflows",
                      relro_.offset, relro_.vaddr);
            coverage_complete = false;
        }

        if (relro_.memsz == 0U) {
            add_error(Code::RelroCoverageIncomplete,
                      "PT_GNU_RELRO has zero size", relro_.offset,
                      relro_.vaddr);
            coverage_complete = false;
        }
        if (!range_inside_writable_load(relro_.vaddr, relro_.memsz)) {
            add_error(Code::UnsafeLoadMapping,
                      "PT_GNU_RELRO is not fully contained in a writable "
                      "PT_LOAD",
                      relro_.offset, relro_.vaddr);
            coverage_complete = false;
        }

        std::vector<TargetRange> targets;
        collect_relro_targets(targets);
        for (const TargetRange& target : targets) {
            if (target.begin < relro_page_begin ||
                target.end > relro_page_end) {
                coverage_complete = false;
                add_error(Code::RelroCoverageIncomplete,
                          target.name +
                              " is not fully covered by the page-aligned "
                              "PT_GNU_RELRO interval",
                          0U, target.begin);
            }
        }

        if ((has_dynamic_relocations() || pltgot_address_ != kNoValue) &&
            !has_section_named(".got") &&
            !has_section_named(".got.plt")) {
            coverage_complete = false;
            add_error(Code::RelroMetadataUnavailable,
                      "GOT section metadata is unavailable; full GOT coverage "
                      "cannot be proven");
        }

        if (!report_.immediate_binding) {
            add_error(Code::BindNowMissing,
                      "neither DT_BIND_NOW, DF_BIND_NOW, nor DF_1_NOW requests "
                      "immediate binding");
        }

        if (coverage_complete && report_.immediate_binding) {
            report_.relro = RelroStatus::Full;
        } else {
            report_.relro = RelroStatus::Partial;
        }
    }

    void check_stack() {
        if (!report_.has_gnu_stack) {
            add_error(Code::MissingGnuStack,
                      "ELF image has no PT_GNU_STACK program header; the "
                      "stack execution policy is not explicit");
            return;
        }
        if (report_.executable_stack) {
            add_error(Code::ExecutableStack,
                      "PT_GNU_STACK has the executable permission bit set");
        }
    }

    const std::uint8_t* data_;
    std::size_t size_;
    AuditOptions options_;
    AuditReport report_;

    std::uint64_t phoff_ = 0U;
    std::uint64_t shoff_ = 0U;
    std::uint64_t phnum_ = 0U;
    std::uint64_t shnum_ = 0U;
    std::uint64_t shstrndx_ = 0U;
    std::uint64_t header_file_end_ = 0U;
    std::uint64_t header_mem_end_ = 0U;
    std::uint64_t section_mem_end_ = 0U;

    std::vector<ProgramHeader> phdrs_;
    std::vector<SectionHeader> sections_;
    std::vector<std::size_t> load_indices_;

    bool has_dynamic_ = false;
    ProgramHeader dynamic_;
    bool has_relro_ = false;
    ProgramHeader relro_;

    bool has_dynstr_ = false;
    std::uint64_t dynstr_address_ = 0U;
    std::uint64_t dynstr_size_ = 0U;
    std::uint64_t dynstr_file_offset_ = 0U;

    std::uint64_t dynsym_address_ = 0U;
    std::uint64_t dynsym_file_offset_ = kNoValue;
    std::uint64_t hash_address_ = kNoValue;
    std::uint64_t gnu_hash_address_ = kNoValue;
    std::uint64_t pltgot_address_ = kNoValue;
    std::uint64_t rela_address_ = 0U;
    std::uint64_t rela_size_ = 0U;
    std::uint64_t jmprel_address_ = 0U;
    std::uint64_t pltrel_size_ = 0U;
    std::uint64_t pltrel_value_ = 0U;
    std::vector<std::pair<std::uint64_t, bool>> checked_resolvers_;
    std::vector<std::uint64_t> got_relocation_offsets_;
};

}  // namespace

bool AuditReport::has_finding(Code code) const noexcept {
    for (const Finding& finding : findings) {
        if (finding.code == code) {
            return true;
        }
    }
    return false;
}

std::size_t AuditReport::error_count() const noexcept {
    std::size_t count = 0U;
    for (const Finding& finding : findings) {
        if (finding.severity == Severity::Error) {
            ++count;
        }
    }
    return count;
}

std::size_t AuditReport::warning_count() const noexcept {
    std::size_t count = 0U;
    for (const Finding& finding : findings) {
        if (finding.severity == Severity::Warning) {
            ++count;
        }
    }
    return count;
}

AuditReport audit_elf64(const void* data, std::size_t size,
                       const AuditOptions& options) {
    if (data == nullptr) {
        Auditor auditor(nullptr, size, options);
        return auditor.run();
    }
    const auto* bytes = static_cast<const std::uint8_t*>(data);
    Auditor auditor(bytes, size, options);
    return auditor.run();
}

AuditReport audit_elf64_file(const std::string& path,
                            const AuditOptions& options) {
    std::ifstream stream(path, std::ios::binary | std::ios::ate);
    if (!stream) {
        Auditor auditor(nullptr, 0U, options);
        AuditReport report = auditor.run();
        report.findings.clear();
        report.parsed = false;
        report.admission_passed = false;
        Finding finding;
        finding.severity = Severity::Error;
        finding.code = Code::FileIoError;
        finding.message = "cannot open ELF file: " + path;
        report.findings.push_back(std::move(finding));
        return report;
    }

    const std::streampos end_position = stream.tellg();
    const std::streamoff length = end_position - std::streampos(0);
    if (length < std::streamoff(0)) {
        stream.close();
        Auditor auditor(nullptr, 0U, options);
        AuditReport report = auditor.run();
        report.findings.clear();
        report.parsed = false;
        report.admission_passed = false;
        Finding finding;
        finding.severity = Severity::Error;
        finding.code = Code::FileIoError;
        finding.message = "cannot determine ELF file size: " + path;
        report.findings.push_back(std::move(finding));
        return report;
    }

    const std::uint64_t file_size = static_cast<std::uint64_t>(length);
    if (file_size >
        static_cast<std::uint64_t>(std::numeric_limits<std::size_t>::max())) {
        Auditor auditor(nullptr, 0U, options);
        AuditReport report = auditor.run();
        report.findings.clear();
        report.parsed = false;
        report.admission_passed = false;
        Finding finding;
        finding.severity = Severity::Error;
        finding.code = Code::FileIoError;
        finding.message = "ELF file is too large to map into memory: " + path;
        report.findings.push_back(std::move(finding));
        return report;
    }
    if (file_size >
        static_cast<std::uint64_t>(
            std::numeric_limits<std::streamsize>::max())) {
        Auditor auditor(nullptr, 0U, options);
        AuditReport report = auditor.run();
        report.findings.clear();
        report.parsed = false;
        report.admission_passed = false;
        Finding finding;
        finding.severity = Severity::Error;
        finding.code = Code::FileIoError;
        finding.message = "ELF file exceeds streamsize limits: " + path;
        report.findings.push_back(std::move(finding));
        return report;
    }

    std::vector<std::uint8_t> bytes(static_cast<std::size_t>(file_size));
    stream.seekg(0, std::ios::beg);
    if (!bytes.empty()) {
        stream.read(reinterpret_cast<char*>(bytes.data()),
                    static_cast<std::streamsize>(bytes.size()));
    }
    if (!stream && !bytes.empty()) {
        Auditor auditor(nullptr, 0U, options);
        AuditReport report = auditor.run();
        report.findings.clear();
        report.parsed = false;
        report.admission_passed = false;
        Finding finding;
        finding.severity = Severity::Error;
        finding.code = Code::FileIoError;
        finding.message = "cannot read ELF file: " + path;
        report.findings.push_back(std::move(finding));
        return report;
    }

    return audit_elf64(bytes.data(), bytes.size(), options);
}

const char* to_string(Code code) noexcept {
    switch (code) {
        case Code::FileIoError:
            return "FileIoError";
        case Code::TruncatedInput:
            return "TruncatedInput";
        case Code::InvalidMagic:
            return "InvalidMagic";
        case Code::UnsupportedClass:
            return "UnsupportedClass";
        case Code::UnsupportedEndianness:
            return "UnsupportedEndianness";
        case Code::UnsupportedVersion:
            return "UnsupportedVersion";
        case Code::UnsupportedType:
            return "UnsupportedType";
        case Code::UnsupportedMachine:
            return "UnsupportedMachine";
        case Code::MalformedElfHeader:
            return "MalformedElfHeader";
        case Code::MalformedProgramHeaders:
            return "MalformedProgramHeaders";
        case Code::MalformedSectionHeaders:
            return "MalformedSectionHeaders";
        case Code::OutOfBounds:
            return "OutOfBounds";
        case Code::IntegerOverflow:
            return "IntegerOverflow";
        case Code::MisalignedStructure:
            return "MisalignedStructure";
        case Code::InvalidConfiguration:
            return "InvalidConfiguration";
        case Code::NoLoadSegment:
            return "NoLoadSegment";
        case Code::OverlappingLoadSegments:
            return "OverlappingLoadSegments";
        case Code::MissingDynamicSegment:
            return "MissingDynamicSegment";
        case Code::MalformedDynamicTable:
            return "MalformedDynamicTable";
        case Code::MalformedStringTable:
            return "MalformedStringTable";
        case Code::MalformedRelocationTable:
            return "MalformedRelocationTable";
        case Code::MalformedSymbolTable:
            return "MalformedSymbolTable";
        case Code::UnsafeLoadMapping:
            return "UnsafeLoadMapping";
        case Code::MissingRelro:
            return "MissingRelro";
        case Code::RelroCoverageIncomplete:
            return "RelroCoverageIncomplete";
        case Code::RelroMetadataUnavailable:
            return "RelroMetadataUnavailable";
        case Code::BindNowMissing:
            return "BindNowMissing";
        case Code::ExecutableStack:
            return "ExecutableStack";
        case Code::MissingGnuStack:
            return "MissingGnuStack";
        case Code::EmptyRuntimePath:
            return "EmptyRuntimePath";
        case Code::NonAbsoluteRuntimePath:
            return "NonAbsoluteRuntimePath";
        case Code::UnboundedOriginPath:
            return "UnboundedOriginPath";
        case Code::WritableIfuncResolver:
            return "WritableIfuncResolver";
        case Code::UnmappedIfuncResolver:
            return "UnmappedIfuncResolver";
        case Code::WritableIrelativeResolver:
            return "WritableIrelativeResolver";
        case Code::UnmappedIrelativeResolver:
            return "UnmappedIrelativeResolver";
    }
    return "Unknown";
}

const char* to_string(Severity severity) noexcept {
    switch (severity) {
        case Severity::Info:
            return "info";
        case Severity::Warning:
            return "warning";
        case Severity::Error:
            return "error";
    }
    return "unknown";
}

const char* to_string(RelroStatus status) noexcept {
    switch (status) {
        case RelroStatus::None:
            return "none";
        case RelroStatus::Partial:
            return "partial";
        case RelroStatus::Full:
            return "full";
    }
    return "unknown";
}

}  // namespace elf64_audit
