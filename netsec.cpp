#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <cstring>
#include <fstream>
#include <limits>
#include <map>
#include <optional>
#include <sstream>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace netsec {

namespace {

constexpr std::uint64_t kElfHeaderSize = 64;
constexpr std::uint64_t kProgramHeaderSize = 56;
constexpr std::uint64_t kSectionHeaderSize = 64;
constexpr std::uint64_t kDynamicEntrySize = 16;
constexpr std::uint64_t kSymbolSize = 24;
constexpr std::uint64_t kRelaSize = 24;
constexpr std::uint16_t kPnXnum = 0xffff;
constexpr std::size_t kMaxFindings = 256;

constexpr std::uint16_t kEtDyn = 3;
constexpr std::uint16_t kEmX86_64 = 62;

constexpr std::uint32_t kPtLoad = 1;
constexpr std::uint32_t kPtDynamic = 2;
constexpr std::uint32_t kPtGnuRelro = 0x6474e552;

constexpr std::uint32_t kPfX = 1;
constexpr std::uint32_t kPfW = 2;
constexpr std::uint32_t kPfR = 4;

constexpr std::uint64_t kDtNull = 0;
constexpr std::uint64_t kDtNeeded = 1;
constexpr std::uint64_t kDtPltrelsz = 2;
constexpr std::uint64_t kDtPltgot = 3;
constexpr std::uint64_t kDtHash = 4;
constexpr std::uint64_t kDtStrtab = 5;
constexpr std::uint64_t kDtSymtab = 6;
constexpr std::uint64_t kDtRela = 7;
constexpr std::uint64_t kDtRelasz = 8;
constexpr std::uint64_t kDtRelaent = 9;
constexpr std::uint64_t kDtStrsz = 10;
constexpr std::uint64_t kDtSyment = 11;
constexpr std::uint64_t kDtSoname = 14;
constexpr std::uint64_t kDtRpath = 15;
constexpr std::uint64_t kDtRel = 17;
constexpr std::uint64_t kDtRelsz = 18;
constexpr std::uint64_t kDtRelent = 19;
constexpr std::uint64_t kDtPltrel = 20;
constexpr std::uint64_t kDtTextrel = 22;
constexpr std::uint64_t kDtJmprel = 23;
constexpr std::uint64_t kDtBindNow = 24;
constexpr std::uint64_t kDtInitArray = 25;
constexpr std::uint64_t kDtFiniArray = 26;
constexpr std::uint64_t kDtInitArraysz = 27;
constexpr std::uint64_t kDtFiniArraysz = 28;
constexpr std::uint64_t kDtRunpath = 29;
constexpr std::uint64_t kDtFlags = 30;
constexpr std::uint64_t kDtPreinitArray = 32;
constexpr std::uint64_t kDtPreinitArraysz = 33;
constexpr std::uint64_t kDtSymtabShndx = 34;
constexpr std::uint64_t kDtRelrsz = 35;
constexpr std::uint64_t kDtRelr = 36;
constexpr std::uint64_t kDtRelrent = 37;
constexpr std::uint64_t kDtGnuHash = 0x6ffffef5;
constexpr std::uint64_t kDtVersym = 0x6ffffff0;
constexpr std::uint64_t kDtFlags1 = 0x6ffffffb;

constexpr std::uint64_t kDfBindNow = 0x8;
constexpr std::uint64_t kDf1Now = 0x1;

constexpr std::uint64_t kRNone = 0;
constexpr std::uint64_t kR64 = 1;
constexpr std::uint64_t kRPc32 = 2;
constexpr std::uint64_t kRGot32 = 3;
constexpr std::uint64_t kRPlt32 = 4;
constexpr std::uint64_t kRCopy = 5;
constexpr std::uint64_t kRGlobDat = 6;
constexpr std::uint64_t kRJumpSlot = 7;
constexpr std::uint64_t kRRelative = 8;
constexpr std::uint64_t kRGotPcRel = 9;
constexpr std::uint64_t kRIrelative = 37;
constexpr std::uint64_t kRRelative64 = 38;
constexpr std::uint64_t kRTlsdesc = 36;
constexpr std::uint64_t kRTlsdescCall = 35;

struct Ehdr {
    std::uint16_t type = 0;
    std::uint16_t machine = 0;
    std::uint32_t version = 0;
    std::uint64_t entry = 0;
    std::uint64_t phoff = 0;
    std::uint64_t shoff = 0;
    std::uint16_t ehsize = 0;
    std::uint16_t phentsize = 0;
    std::uint32_t phnum = 0;
    std::uint16_t shentsize = 0;
    std::uint16_t shnum = 0;
    std::uint16_t shstrndx = 0;
};

struct Phdr {
    std::uint32_t type = 0;
    std::uint32_t flags = 0;
    std::uint64_t offset = 0;
    std::uint64_t vaddr = 0;
    std::uint64_t filesz = 0;
    std::uint64_t memsz = 0;
    std::uint64_t align = 0;
    std::uint64_t file_offset = 0;
    std::uint32_t index = 0;

    [[nodiscard]] bool writable() const noexcept {
        return (flags & kPfW) != 0;
    }
    [[nodiscard]] bool executable() const noexcept {
        return (flags & kPfX) != 0;
    }
    [[nodiscard]] std::uint64_t vaddr_end() const noexcept {
        return vaddr + memsz;
    }
};

struct DynamicEntry {
    std::uint64_t tag = 0;
    std::uint64_t value = 0;
    std::uint64_t file_offset = 0;
};

struct Symbol {
    std::uint32_t name = 0;
    std::uint8_t info = 0;
    std::uint8_t other = 0;
    std::uint16_t shndx = 0;
    std::uint64_t value = 0;
    std::uint64_t size = 0;
    std::uint64_t file_offset = 0;

    [[nodiscard]] std::uint8_t type() const noexcept {
        return static_cast<std::uint8_t>(info & 0x0f);
    }
    [[nodiscard]] bool function() const noexcept {
        return type() == 2 || type() == 10;
    }
    [[nodiscard]] bool undefined() const noexcept {
        return shndx == 0;
    }
};

struct MetadataRange {
    std::uint64_t start = 0;
    std::uint64_t end = 0;
    std::string_view name;
};

struct RawReloc {
    std::uint64_t file_offset = 0;
    std::uint64_t r_offset = 0;
    std::uint64_t r_info = 0;
    std::int64_t addend = 0;
    bool from_plt = false;
    std::uint64_t index = 0;
};

struct RelaTable {
    std::uint64_t vaddr = 0;
    std::uint64_t size = 0;
    std::uint64_t file_offset = 0;
    std::uint64_t count = 0;
    bool plt = false;
    FileWitness source;
};

struct LoadPageRange {
    std::uint64_t start = 0;
    std::uint64_t end = 0;
};

struct FileSpan {
    std::uint64_t offset = 0;
    std::uint64_t available = 0;
};

bool add_overflow(std::uint64_t lhs, std::uint64_t rhs, std::uint64_t& out) {
    if (rhs > std::numeric_limits<std::uint64_t>::max() - lhs) {
        return true;
    }
    out = lhs + rhs;
    return false;
}

bool mul_overflow(std::uint64_t lhs, std::uint64_t rhs, std::uint64_t& out) {
    if (lhs != 0 && rhs > std::numeric_limits<std::uint64_t>::max() / lhs) {
        return true;
    }
    out = lhs * rhs;
    return false;
}

bool is_power_of_two(std::uint64_t value) {
    return value != 0 && (value & (value - 1)) == 0;
}

std::optional<std::uint64_t> align_down(std::uint64_t value,
                                        std::uint64_t alignment) {
    if (!is_power_of_two(alignment)) {
        return std::nullopt;
    }
    return value & ~(alignment - 1);
}

std::optional<std::uint64_t> align_up(std::uint64_t value,
                                      std::uint64_t alignment) {
    if (!is_power_of_two(alignment)) {
        return std::nullopt;
    }
    const std::uint64_t mask = alignment - 1;
    if (value > std::numeric_limits<std::uint64_t>::max() - mask) {
        return std::nullopt;
    }
    return (value + mask) & ~mask;
}

std::string hex_value(std::uint64_t value) {
    std::ostringstream stream;
    stream << "0x" << std::hex << value;
    return stream.str();
}

std::uint16_t read_u16(std::span<const std::uint8_t> data,
                       std::uint64_t offset) {
    return static_cast<std::uint16_t>(
               data[static_cast<std::size_t>(offset)]) |
           static_cast<std::uint16_t>(
               static_cast<std::uint16_t>(
                   data[static_cast<std::size_t>(offset + 1)])
               << 8);
}

std::uint32_t read_u32(std::span<const std::uint8_t> data,
                       std::uint64_t offset) {
    return static_cast<std::uint32_t>(
               data[static_cast<std::size_t>(offset)]) |
           (static_cast<std::uint32_t>(
                data[static_cast<std::size_t>(offset + 1)])
            << 8) |
           (static_cast<std::uint32_t>(
                data[static_cast<std::size_t>(offset + 2)])
            << 16) |
           (static_cast<std::uint32_t>(
                data[static_cast<std::size_t>(offset + 3)])
            << 24);
}

std::uint64_t read_u64(std::span<const std::uint8_t> data,
                       std::uint64_t offset) {
    std::uint64_t value = 0;
    for (unsigned index = 0; index < 8; ++index) {
        value |= static_cast<std::uint64_t>(
                     data[static_cast<std::size_t>(offset + index)])
                 << (index * 8);
    }
    return value;
}

std::int64_t read_i64(std::span<const std::uint8_t> data,
                      std::uint64_t offset) {
    return static_cast<std::int64_t>(read_u64(data, offset));
}

class Analyzer {
public:
    Analyzer(std::span<const std::uint8_t> image, PolicyOptions options)
        : image_(image), options_(options) {}

    PolicyResult run() {
        if (!is_power_of_two(options_.page_size)) {
            add(FindingCode::invalid_relro,
                "policy page size must be a non-zero power of two",
                FileWitness{0, 0, 0});
            return finish();
        }
        if (!parse_header()) {
            return finish();
        }
        if (!parse_program_headers()) {
            return finish();
        }
        analyze_loads();
        if (!parse_dynamic_segment()) {
            return finish();
        }
        validate_dynamic_values();
        validate_string_table();
        locate_relocations();
        validate_hash_and_symbols();
        analyze_relro();
        analyze_relocations();
        analyze_constructor_arrays();
        return finish();
    }

private:
    PolicyResult finish() {
        bool reject = false;
        for (const Finding& finding : result_.findings) {
            if (finding.severity == Severity::error) {
                reject = true;
                break;
            }
        }
        result_.decision =
            reject ? PolicyDecision::reject : PolicyDecision::allow;
        result_.allowed.value = !reject;
        result_.rejected.value = reject;
        return result_;
    }

    void add(FindingCode code, std::string message, FileWitness source,
             Severity severity = Severity::error,
             std::optional<FileWitness> relocation = std::nullopt,
             std::optional<FileWitness> target = std::nullopt) {
        if (result_.findings.size() >= kMaxFindings) {
            return;
        }
        Finding finding;
        finding.code = code;
        finding.severity = severity;
        finding.message = std::move(message);
        finding.source = source;
        finding.relocation = relocation;
        finding.target = target;
        finding.file_offset = source.file_offset;
        finding.file_size = source.size;
        finding.virtual_address = source.virtual_address;
        if (target) {
            finding.target_file_offset = target->file_offset;
        }
        result_.findings.push_back(std::move(finding));
    }

    [[nodiscard]] bool file_range_ok(std::uint64_t offset,
                                     std::uint64_t size) const {
        if (offset > image_.size()) {
            return false;
        }
        return size <= static_cast<std::uint64_t>(image_.size()) - offset;
    }

    [[nodiscard]] bool is_zero(std::uint64_t offset,
                               std::uint64_t size) const {
        if (!file_range_ok(offset, size)) {
            return false;
        }
        for (std::uint64_t index = 0; index < size; ++index) {
            if (image_[static_cast<std::size_t>(offset + index)] != 0) {
                return false;
            }
        }
        return true;
    }

    bool parse_header() {
        if (image_.size() < kElfHeaderSize) {
            add(FindingCode::truncated_table,
                "file is shorter than an ELF64 header",
                FileWitness{0, static_cast<std::uint64_t>(image_.size()), 0});
            return false;
        }
        if (image_[0] != 0x7f || image_[1] != 'E' || image_[2] != 'L' ||
            image_[3] != 'F') {
            add(FindingCode::malformed_header, "missing ELF magic",
                FileWitness{0, 4, 0});
            return false;
        }
        if (image_[4] != 2) {
            add(FindingCode::unsupported_elf,
                "only ELFCLASS64 is supported", FileWitness{4, 1, 0});
            return false;
        }
        if (image_[5] != 1) {
            add(FindingCode::unsupported_elf,
                "only little-endian ELF is supported", FileWitness{5, 1, 0});
            return false;
        }
        if (image_[6] != 1) {
            add(FindingCode::unsupported_elf,
                "unsupported ELF identification version",
                FileWitness{6, 1, 0});
            return false;
        }

        eh_.type = read_u16(image_, 16);
        eh_.machine = read_u16(image_, 18);
        eh_.version = read_u32(image_, 20);
        eh_.entry = read_u64(image_, 24);
        eh_.phoff = read_u64(image_, 32);
        eh_.shoff = read_u64(image_, 40);
        eh_.ehsize = read_u16(image_, 52);
        eh_.phentsize = read_u16(image_, 54);
        eh_.phnum = read_u16(image_, 56);
        eh_.shentsize = read_u16(image_, 58);
        eh_.shnum = read_u16(image_, 60);
        eh_.shstrndx = read_u16(image_, 62);

        if (eh_.type != kEtDyn) {
            add(FindingCode::unsupported_elf,
                "only ET_DYN shared objects/position-independent images are "
                "accepted",
                FileWitness{16, 2, 0});
            return false;
        }
        if (eh_.machine != kEmX86_64) {
            add(FindingCode::unsupported_elf,
                "only EM_X86_64 images are accepted",
                FileWitness{18, 2, 0});
            return false;
        }
        if (eh_.version != 1) {
            add(FindingCode::unsupported_elf, "unsupported ELF version",
                FileWitness{20, 4, 0});
            return false;
        }
        if (eh_.ehsize != kElfHeaderSize) {
            add(FindingCode::malformed_header,
                "e_ehsize is not the ELF64 header size",
                FileWitness{52, 2, 0});
            return false;
        }
        if (eh_.phentsize != kProgramHeaderSize) {
            add(FindingCode::malformed_program_headers,
                "e_phentsize is not 56", FileWitness{54, 2, 0});
            return false;
        }
        if ((eh_.phoff & 7U) != 0) {
            add(FindingCode::malformed_program_headers,
                "e_phoff is not 8-byte aligned", FileWitness{32, 8, 0});
            return false;
        }

        if (eh_.phnum == kPnXnum) {
            if (eh_.shoff == 0 || eh_.shentsize != kSectionHeaderSize) {
                add(FindingCode::malformed_program_headers,
                    "PN_XNUM requires a valid section-header zero entry",
                    FileWitness{56, 2, 0});
                return false;
            }
            if (!file_range_ok(eh_.shoff, kSectionHeaderSize)) {
                add(FindingCode::truncated_table,
                    "extended program-header count refers to a truncated "
                    "section header",
                    FileWitness{eh_.shoff, kSectionHeaderSize, 0});
                return false;
            }
            const std::uint64_t section_count =
                read_u64(image_, eh_.shoff + 32);
            if (section_count == 0) {
                add(FindingCode::malformed_program_headers,
                    "extended section count is zero",
                    FileWitness{eh_.shoff + 32, 8, 0});
                return false;
            }
            std::uint64_t section_table_size = 0;
            if (mul_overflow(section_count, kSectionHeaderSize,
                             section_table_size) ||
                !file_range_ok(eh_.shoff, section_table_size)) {
                add(FindingCode::truncated_table,
                    "extended section-header table is truncated",
                    FileWitness{eh_.shoff, section_table_size, 0});
                return false;
            }
            eh_.phnum = read_u32(image_, eh_.shoff + 44);
        }

        if (eh_.phnum == 0) {
            add(FindingCode::malformed_program_headers,
                "program-header table is empty", FileWitness{56, 2, 0});
            return false;
        }
        std::uint64_t table_size = 0;
        if (mul_overflow(eh_.phnum, kProgramHeaderSize, table_size) ||
            !file_range_ok(eh_.phoff, table_size)) {
            add(FindingCode::truncated_table,
                "program-header table is truncated or overflows",
                FileWitness{eh_.phoff, table_size, 0});
            return false;
        }
        return true;
    }

    bool parse_program_headers() {
        phdrs_.reserve(eh_.phnum);
        for (std::uint32_t index = 0; index < eh_.phnum; ++index) {
            const std::uint64_t offset =
                eh_.phoff + static_cast<std::uint64_t>(index) *
                                kProgramHeaderSize;
            Phdr phdr;
            phdr.type = read_u32(image_, offset);
            phdr.flags = read_u32(image_, offset + 4);
            phdr.offset = read_u64(image_, offset + 8);
            phdr.vaddr = read_u64(image_, offset + 16);
            phdr.filesz = read_u64(image_, offset + 32);
            phdr.memsz = read_u64(image_, offset + 40);
            phdr.align = read_u64(image_, offset + 48);
            phdr.file_offset = offset;
            phdr.index = index;

            std::uint64_t file_end = 0;
            std::uint64_t memory_end = 0;
            if (add_overflow(phdr.offset, phdr.filesz, file_end)) {
                add(FindingCode::integer_overflow,
                    "segment file range overflows",
                    FileWitness{offset, 56, 0});
                return false;
            }
            if (add_overflow(phdr.vaddr, phdr.memsz, memory_end)) {
                add(FindingCode::integer_overflow,
                    "segment virtual range overflows",
                    FileWitness{offset, 56, phdr.vaddr});
                return false;
            }
            if (phdr.filesz != 0 &&
                !file_range_ok(phdr.offset, phdr.filesz)) {
                add(FindingCode::truncated_table,
                    "segment file range extends past end of file (type " +
                        hex_value(phdr.type) + ")",
                    FileWitness{offset, 56, phdr.vaddr});
                return false;
            }
            if ((phdr.flags & ~(kPfR | kPfW | kPfX)) != 0) {
                add(FindingCode::malformed_program_headers,
                    "segment contains unknown permission bits",
                    FileWitness{offset + 4, 4, phdr.vaddr});
            }
            if (phdr.type == kPtLoad) {
                if (phdr.filesz > phdr.memsz) {
                    add(FindingCode::malformed_program_headers,
                        "PT_LOAD p_filesz exceeds p_memsz",
                        FileWitness{offset + 32, 16, phdr.vaddr});
                }
                if (phdr.align != 0 && !is_power_of_two(phdr.align)) {
                    add(FindingCode::malformed_program_headers,
                        "PT_LOAD p_align is not a power of two",
                        FileWitness{offset + 48, 8, phdr.vaddr});
                } else if (phdr.align > 1 &&
                           (phdr.vaddr % phdr.align) !=
                               (phdr.offset % phdr.align)) {
                    add(FindingCode::malformed_program_headers,
                        "PT_LOAD offset and address are not congruent modulo "
                        "p_align",
                        FileWitness{offset, 56, phdr.vaddr});
                }
            } else if (phdr.type == kPtDynamic) {
                if (dynamic_index_) {
                    add(FindingCode::malformed_dynamic_segment,
                        "multiple PT_DYNAMIC segments are ambiguous",
                        FileWitness{offset, 56, phdr.vaddr});
                } else {
                    dynamic_index_ = phdrs_.size();
                }
                if (phdr.filesz == 0 ||
                    (phdr.filesz % kDynamicEntrySize) != 0) {
                    add(FindingCode::malformed_dynamic_segment,
                        "PT_DYNAMIC p_filesz is empty or not a multiple of 16",
                        FileWitness{offset + 32, 8, phdr.vaddr});
                }
            } else if (phdr.type == kPtGnuRelro) {
                if (relro_index_) {
                    add(FindingCode::invalid_relro,
                        "multiple PT_GNU_RELRO segments are ambiguous",
                        FileWitness{offset, 56, phdr.vaddr});
                } else {
                    relro_index_ = phdrs_.size();
                }
                if (phdr.memsz == 0) {
                    add(FindingCode::invalid_relro,
                        "PT_GNU_RELRO has an empty memory range",
                        FileWitness{offset + 40, 8, phdr.vaddr});
                }
                if (phdr.filesz > phdr.memsz) {
                    add(FindingCode::invalid_relro,
                        "PT_GNU_RELRO p_filesz exceeds p_memsz",
                        FileWitness{offset + 32, 16, phdr.vaddr});
                }
            }
            phdrs_.push_back(phdr);
        }
        return true;
    }

    void analyze_loads() {
        bool have_load = false;
        for (const Phdr& phdr : phdrs_) {
            if (phdr.type != kPtLoad) {
                continue;
            }
            have_load = true;
            LoadSegmentInfo info;
            info.file_offset = phdr.offset;
            info.virtual_address = phdr.vaddr;
            info.file_size = phdr.filesz;
            info.memory_size = phdr.memsz;
            info.alignment = phdr.align;
            info.flags = phdr.flags;
            info.read = (phdr.flags & kPfR) != 0;
            info.write = phdr.writable();
            info.execute = phdr.executable();
            result_.summary.loads.push_back(info);

            if (phdr.memsz != 0 && !load_page_range(phdr)) {
                add(FindingCode::integer_overflow,
                    "page-rounded PT_LOAD range overflows",
                    FileWitness{phdr.file_offset, 56, phdr.vaddr});
            }
            if (phdr.writable() && phdr.executable()) {
                add(FindingCode::rwx_load_segment,
                    "PT_LOAD is both writable and executable",
                    FileWitness{phdr.file_offset, 56, phdr.vaddr});
            }
        }
        if (!have_load) {
            add(FindingCode::malformed_program_headers,
                "ET_DYN image has no PT_LOAD segment",
                FileWitness{eh_.phoff, 0, 0});
        }

        const auto loads = load_segments();
        for (std::size_t left = 0; left < loads.size(); ++left) {
            const Phdr& first = *loads[left];
            if (first.memsz == 0) {
                continue;
            }
            const auto first_pages = load_page_range(first);
            for (std::size_t right = left + 1; right < loads.size(); ++right) {
                const Phdr& second = *loads[right];
                if (second.memsz == 0) {
                    continue;
                }
                const std::uint64_t first_end = first.vaddr_end();
                const std::uint64_t second_end = second.vaddr_end();
                if (first.vaddr < second_end && second.vaddr < first_end) {
                    add(FindingCode::overlapping_load_segments,
                        "PT_LOAD virtual ranges overlap, making address "
                        "ownership and permissions ambiguous",
                        FileWitness{second.file_offset, 56, second.vaddr});
                    continue;
                }
                const auto second_pages = load_page_range(second);
                if (first_pages && second_pages &&
                    first_pages->start < second_pages->end &&
                    second_pages->start < first_pages->end &&
                    first.flags != second.flags) {
                    add(FindingCode::ambiguous_load_permissions,
                        "PT_LOAD segments share a mapped page with different "
                        "permissions",
                        FileWitness{second.file_offset, 56, second.vaddr});
                }
            }
        }
    }

    [[nodiscard]] std::vector<const Phdr*> load_segments() const {
        std::vector<const Phdr*> loads;
        for (const Phdr& phdr : phdrs_) {
            if (phdr.type == kPtLoad) {
                loads.push_back(&phdr);
            }
        }
        return loads;
    }

    [[nodiscard]] const Phdr* containing_load(std::uint64_t address,
                                              std::uint64_t size) const {
        std::uint64_t end = 0;
        if (add_overflow(address, size, end)) {
            return nullptr;
        }
        const Phdr* found = nullptr;
        for (const Phdr& phdr : phdrs_) {
            if (phdr.type != kPtLoad) {
                continue;
            }
            const std::uint64_t load_end = phdr.vaddr_end();
            if (address >= phdr.vaddr && end <= load_end) {
                if (found != nullptr) {
                    return nullptr;
                }
                found = &phdr;
            }
        }
        return found;
    }

    [[nodiscard]] std::optional<FileSpan> file_span(
        std::uint64_t address) const {
        const Phdr* load = containing_load(address, 0);
        if (load == nullptr || address < load->vaddr) {
            return std::nullopt;
        }
        const std::uint64_t delta = address - load->vaddr;
        if (delta >= load->filesz) {
            return std::nullopt;
        }
        return FileSpan{load->offset + delta, load->filesz - delta};
    }

    [[nodiscard]] std::optional<FileWitness> file_witness(
        std::uint64_t address, std::uint64_t size) const {
        std::uint64_t end = 0;
        if (add_overflow(address, size, end)) {
            return std::nullopt;
        }
        const Phdr* load = containing_load(address, size);
        if (load == nullptr || address < load->vaddr) {
            return std::nullopt;
        }
        const std::uint64_t delta = address - load->vaddr;
        if (delta > load->filesz || size > load->filesz - delta) {
            return std::nullopt;
        }
        return FileWitness{load->offset + delta, size, address};
    }

    [[nodiscard]] std::optional<LoadPageRange> load_page_range(
        const Phdr& load) const {
        if (!is_power_of_two(options_.page_size)) {
            return std::nullopt;
        }
        const auto start = align_down(load.vaddr, options_.page_size);
        const auto end = align_up(load.vaddr_end(), options_.page_size);
        if (!start || !end) {
            return std::nullopt;
        }
        return LoadPageRange{*start, *end};
    }

    [[nodiscard]] std::optional<FileWitness> register_table(
        std::uint64_t address, std::uint64_t size, std::string_view name,
        FileWitness source, FindingCode malformed_code) {
        std::uint64_t end = 0;
        if (add_overflow(address, size, end)) {
            add(FindingCode::integer_overflow,
                std::string(name) + " range overflows", source);
            return std::nullopt;
        }
        const Phdr* load = containing_load(address, size);
        if (load == nullptr) {
            add(malformed_code,
                std::string(name) +
                    " is not wholly contained in one PT_LOAD",
                source);
            return std::nullopt;
        }
        if (address < load->vaddr) {
            add(malformed_code,
                std::string(name) + " underflows its PT_LOAD", source);
            return std::nullopt;
        }
        const std::uint64_t delta = address - load->vaddr;
        if (delta > load->filesz || size > load->filesz - delta) {
            add(FindingCode::truncated_table,
                std::string(name) + " exceeds p_filesz of its PT_LOAD",
                source);
            return std::nullopt;
        }
        FileWitness witness{load->offset + delta, size, address};
        if (!file_range_ok(witness.file_offset, witness.size)) {
            add(FindingCode::truncated_table,
                std::string(name) + " extends past end of file", source);
            return std::nullopt;
        }
        for (const MetadataRange& existing : metadata_) {
            if (address < existing.end && existing.start < end) {
                add(malformed_code,
                    std::string(name) + " overlaps " +
                        std::string(existing.name) +
                        "; table ownership is ambiguous",
                    source);
                break;
            }
        }
        metadata_.push_back(MetadataRange{address, end, name});
        return witness;
    }

    bool parse_dynamic_segment() {
        if (!dynamic_index_) {
            return true;
        }
        const Phdr& dynamic = phdrs_[*dynamic_index_];
        result_.summary.has_dynamic_segment = true;
        if (dynamic.filesz == 0 ||
            (dynamic.filesz % kDynamicEntrySize) != 0) {
            return true;
        }

        const auto witness =
            register_table(dynamic.vaddr, dynamic.filesz, "PT_DYNAMIC",
                           FileWitness{dynamic.file_offset, 56, dynamic.vaddr},
                           FindingCode::malformed_dynamic_segment);
        if (!witness) {
            return false;
        }
        const Phdr* containing =
            containing_load(dynamic.vaddr, dynamic.filesz);
        if (containing == nullptr) {
            return false;
        }
        const std::uint64_t expected =
            containing->offset + (dynamic.vaddr - containing->vaddr);
        if (expected != dynamic.offset) {
            add(FindingCode::malformed_dynamic_segment,
                "PT_DYNAMIC p_offset disagrees with the containing PT_LOAD "
                "mapping",
                FileWitness{dynamic.file_offset + 8, 8, dynamic.vaddr});
            return false;
        }

        bool saw_null = false;
        bool reported_after_null = false;
        const std::uint64_t count = dynamic.filesz / kDynamicEntrySize;
        for (std::uint64_t index = 0; index < count; ++index) {
            const std::uint64_t offset =
                dynamic.offset + index * kDynamicEntrySize;
            const std::uint64_t tag = read_u64(image_, offset);
            const std::uint64_t value = read_u64(image_, offset + 8);
            if (tag == kDtNull) {
                saw_null = true;
                for (std::uint64_t remaining = index + 1; remaining < count;
                     ++remaining) {
                    const std::uint64_t remaining_offset =
                        dynamic.offset + remaining * kDynamicEntrySize;
                    if (!is_zero(remaining_offset, kDynamicEntrySize) &&
                        !reported_after_null) {
                        add(FindingCode::dynamic_after_null,
                            "non-null dynamic entry appears after DT_NULL",
                            FileWitness{remaining_offset, kDynamicEntrySize,
                                        0});
                        reported_after_null = true;
                    }
                }
                break;
            }
            dynamic_entries_.push_back(DynamicEntry{tag, value, offset});
            const bool multiple_allowed =
                tag == kDtNeeded || tag == kDtRpath || tag == kDtRunpath ||
                tag == 0x6ffffefc /* DT_AUDIT */ ||
                tag == 0x6ffffefb /* DT_DEPAUDIT */ ||
                tag == 0x7ffffffd /* DT_AUXILIARY */ ||
                tag == 0x7fffffff /* DT_FILTER */;
            const auto existing = dynamic_values_.find(tag);
            if (existing == dynamic_values_.end()) {
                dynamic_values_.emplace(tag, value);
                dynamic_offsets_.emplace(tag, offset);
            } else if (!multiple_allowed && existing->second != value) {
                add(FindingCode::duplicate_dynamic_tag,
                    "dynamic tag " + hex_value(tag) +
                        " appears with conflicting values",
                    FileWitness{offset, kDynamicEntrySize, 0});
            }
        }
        if (!saw_null) {
            add(FindingCode::malformed_dynamic_segment,
                "PT_DYNAMIC has no DT_NULL terminator",
                FileWitness{dynamic.offset, dynamic.filesz, dynamic.vaddr});
        }
        if (dynamic_offsets_.contains(kDtBindNow)) {
            result_.summary.bind_now = true;
        }
        if (const auto flags = dynamic_values_.find(kDtFlags);
            flags != dynamic_values_.end() &&
            (flags->second & kDfBindNow) != 0) {
            result_.summary.bind_now = true;
        }
        if (const auto flags1 = dynamic_values_.find(kDtFlags1);
            flags1 != dynamic_values_.end() &&
            (flags1->second & kDf1Now) != 0) {
            result_.summary.bind_now = true;
        }
        return true;
    }

    [[nodiscard]] std::optional<std::uint64_t> dynamic_value(
        std::uint64_t tag) const {
        const auto found = dynamic_values_.find(tag);
        if (found == dynamic_values_.end()) {
            return std::nullopt;
        }
        return found->second;
    }

    [[nodiscard]] FileWitness dynamic_source(std::uint64_t tag) const {
        const auto found = dynamic_offsets_.find(tag);
        if (found == dynamic_offsets_.end()) {
            return FileWitness{};
        }
        return FileWitness{found->second, kDynamicEntrySize, 0};
    }

    void validate_dynamic_values() {
        if (!dynamic_index_) {
            return;
        }
        for (const DynamicEntry& entry : dynamic_entries_) {
            if (entry.tag == kDtRel || entry.tag == kDtRelsz ||
                entry.tag == kDtRelent || entry.tag == kDtRelr ||
                entry.tag == kDtRelrsz ||
                entry.tag == kDtRelrent || entry.tag == 0x6000000f ||
                entry.tag == 0x60000010 || entry.tag == 0x60000011 ||
                entry.tag == 0x60000012) {
                add(FindingCode::unsupported_dynamic_feature,
                    "packed/Android relocation tables are not RELA and cannot "
                    "be proven safe",
                    FileWitness{entry.file_offset, kDynamicEntrySize, 0});
            }
            if (entry.tag == kDtTextrel) {
                add(FindingCode::text_relocation,
                    "DT_TEXTREL declares a text relocation",
                    FileWitness{entry.file_offset, kDynamicEntrySize, 0});
            }
            if (entry.tag == kDtFlags && (entry.value & 0x4) != 0) {
                add(FindingCode::text_relocation,
                    "DF_TEXTREL declares a text relocation",
                    FileWitness{entry.file_offset, kDynamicEntrySize, 0});
            }
        }

        const auto strtab = dynamic_value(kDtStrtab);
        const auto strsz = dynamic_value(kDtStrsz);
        if (strtab.has_value() != strsz.has_value()) {
            add(FindingCode::malformed_string_table,
                "DT_STRTAB and DT_STRSZ must appear together",
                strtab ? dynamic_source(kDtStrtab) : dynamic_source(kDtStrsz));
        }
        if (strtab && strsz) {
            const auto witness =
                register_table(*strtab, *strsz, "dynamic string table",
                               dynamic_source(kDtStrtab),
                               FindingCode::malformed_string_table);
            if (witness) {
                strtab_file_ = witness->file_offset;
                strsz_ = *strsz;
            }
        }

        const auto syment = dynamic_value(kDtSyment);
        if (syment && *syment != kSymbolSize) {
            add(FindingCode::malformed_symbol_table,
                "DT_SYMENT is not 24 for ELF64",
                dynamic_source(kDtSyment));
        }
        const auto symtab = dynamic_value(kDtSymtab);
        if (symtab && !syment) {
            add(FindingCode::malformed_symbol_table,
                "DT_SYMTAB is present without DT_SYMENT",
                dynamic_source(kDtSymtab));
        }
        if (!symtab && syment) {
            add(FindingCode::malformed_symbol_table,
                "DT_SYMENT is present without DT_SYMTAB",
                dynamic_source(kDtSyment));
        }

        const auto rela = dynamic_value(kDtRela);
        const auto relasz = dynamic_value(kDtRelasz);
        const auto relaent = dynamic_value(kDtRelaent);
        if (rela.has_value() != relasz.has_value()) {
            add(FindingCode::malformed_relocation_table,
                "DT_RELA and DT_RELASZ must appear together",
                rela ? dynamic_source(kDtRela) : dynamic_source(kDtRelasz));
        }
        if ((rela || relasz) && !relaent) {
            add(FindingCode::malformed_relocation_table,
                "DT_RELA/DT_RELASZ require DT_RELAENT",
                dynamic_source(kDtRela));
        }
        if (relaent && *relaent != kRelaSize) {
            add(FindingCode::malformed_relocation_table,
                "DT_RELAENT is not 24 for ELF64",
                dynamic_source(kDtRelaent));
        }
        if (relasz && (*relasz % kRelaSize) != 0) {
            add(FindingCode::malformed_relocation_table,
                "DT_RELASZ is not a multiple of 24",
                dynamic_source(kDtRelasz));
        }
        if (rela && relasz && *rela == 0 && *relasz != 0) {
            add(FindingCode::malformed_relocation_table,
                "DT_RELA address is zero for a non-empty table",
                dynamic_source(kDtRela));
        }

        const auto jmprel = dynamic_value(kDtJmprel);
        const auto pltrelsz = dynamic_value(kDtPltrelsz);
        const auto pltrel = dynamic_value(kDtPltrel);
        if (jmprel.has_value() != pltrelsz.has_value()) {
            add(FindingCode::malformed_relocation_table,
                "DT_JMPREL and DT_PLTRELSZ must appear together",
                jmprel ? dynamic_source(kDtJmprel)
                       : dynamic_source(kDtPltrelsz));
        }
        if (jmprel && !pltrel) {
            add(FindingCode::malformed_relocation_table,
                "DT_JMPREL requires DT_PLTREL",
                dynamic_source(kDtJmprel));
        }
        if (pltrel && *pltrel != kDtRela) {
            add(FindingCode::unsupported_dynamic_feature,
                "DT_PLTREL is not DT_RELA (7); x86-64 PLT relocation layout is "
                "not analyzable",
                dynamic_source(kDtPltrel));
        }
        if (pltrelsz && (*pltrelsz % kRelaSize) != 0) {
            add(FindingCode::malformed_relocation_table,
                "DT_PLTRELSZ is not a multiple of 24",
                dynamic_source(kDtPltrelsz));
        }

        if (const auto symtabShndx = dynamic_value(kDtSymtabShndx)) {
            symtab_shndx_address_ = *symtabShndx;
        }
        if (const auto versym = dynamic_value(kDtVersym)) {
            versym_address_ = *versym;
        }

        const std::array<std::uint64_t, 8> string_tags{
            kDtNeeded, kDtSoname,   kDtRpath,   kDtRunpath,
            0x6ffffefc, 0x6ffffefb, 0x7ffffffd, 0x7fffffff};
        for (const DynamicEntry& entry : dynamic_entries_) {
            if (std::find(string_tags.begin(), string_tags.end(),
                          entry.tag) != string_tags.end()) {
                validate_string_index(entry.value,
                                      "dynamic string reference");
            }
        }
        if (const auto soname = dynamic_value(kDtSoname)) {
            if (*soname < strsz_ && strtab_file_ &&
                image_[static_cast<std::size_t>(*strtab_file_ + *soname)] !=
                    0) {
                const char* text = reinterpret_cast<const char*>(
                    image_.data() +
                    static_cast<std::size_t>(*strtab_file_ + *soname));
                const std::size_t available =
                    static_cast<std::size_t>(strsz_ - *soname);
                const void* terminator = std::memchr(text, '\0', available);
                if (terminator != nullptr) {
                    const std::size_t length = static_cast<std::size_t>(
                        static_cast<const char*>(terminator) - text);
                    result_.summary.soname = std::string(
                        text, length);
                }
            }
        }

        std::uint64_t pltgot = 0;
        if (const auto value = dynamic_value(kDtPltgot)) {
            pltgot = *value;
        }
        if (pltgot != 0) {
            const Phdr* load = containing_load(pltgot, 8);
            if (load == nullptr || !load->writable()) {
                add(FindingCode::malformed_dynamic_segment,
                    "DT_PLTGOT does not point into a writable PT_LOAD",
                    dynamic_source(kDtPltgot));
            }
        }
    }

    void validate_string_index(std::uint64_t index,
                               std::string_view context) {
        if (!strtab_file_) {
            add(FindingCode::malformed_string_table,
                std::string(context) +
                    " needs a valid DT_STRTAB/DT_STRSZ",
                dynamic_source(kDtStrtab));
            return;
        }
        if (index >= strsz_) {
            add(FindingCode::malformed_string_table,
                std::string(context) +
                    " string index is outside DT_STRSZ",
                dynamic_source(kDtStrtab));
            return;
        }
        const auto* begin = reinterpret_cast<const char*>(
            image_.data() +
            static_cast<std::size_t>(*strtab_file_ + index));
        const std::size_t remaining =
            static_cast<std::size_t>(strsz_ - index);
        if (std::memchr(begin, '\0', remaining) == nullptr) {
            add(FindingCode::malformed_string_table,
                std::string(context) + " string is not NUL-terminated",
                FileWitness{*strtab_file_ + index, strsz_ - index, 0});
        }
    }

    void validate_string_table() {
        if (strtab_file_ && strsz_ == 0) {
            add(FindingCode::malformed_string_table, "DT_STRSZ is zero",
                dynamic_source(kDtStrsz));
        }
    }

    void locate_relocations() {
        if (const auto rela = dynamic_value(kDtRela)) {
            if (const auto size = dynamic_value(kDtRelasz)) {
                locate_rela_table(*rela, *size, false, kDtRela);
            }
        }
        if (const auto jmprel = dynamic_value(kDtJmprel)) {
            if (const auto size = dynamic_value(kDtPltrelsz)) {
                locate_rela_table(*jmprel, *size, true, kDtJmprel);
            }
        }
        for (std::size_t left = 0; left < rela_tables_.size(); ++left) {
            for (std::size_t right = left + 1; right < rela_tables_.size();
                 ++right) {
                const RelaTable& first = rela_tables_[left];
                const RelaTable& second = rela_tables_[right];
                const std::uint64_t first_end = first.vaddr + first.size;
                const std::uint64_t second_end = second.vaddr + second.size;
                if (first.vaddr < second_end &&
                    second.vaddr < first_end) {
                    add(FindingCode::malformed_relocation_table,
                        "DT_RELA and DT_JMPREL overlap; relocation "
                        "application order is ambiguous",
                        second.source);
                }
            }
        }
    }

    void locate_rela_table(std::uint64_t address, std::uint64_t size,
                           bool plt, std::uint64_t source_tag) {
        if (size == 0) {
            return;
        }
        const FileWitness source = dynamic_source(source_tag);
        if ((address & 7U) != 0) {
            add(FindingCode::malformed_relocation_table,
                "RELA table is not 8-byte aligned", source);
            return;
        }
        const auto witness =
            register_table(address, size,
                           plt ? "DT_JMPREL RELA table" : "DT_RELA table",
                           source,
                           FindingCode::malformed_relocation_table);
        if (!witness) {
            return;
        }
        const std::uint64_t count = size / kRelaSize;
        RelaTable table;
        table.vaddr = address;
        table.size = size;
        table.file_offset = witness->file_offset;
        table.count = count;
        table.plt = plt;
        table.source = source;
        rela_tables_.push_back(table);

        for (std::uint64_t index = 0; index < count; ++index) {
            const std::uint64_t offset =
                witness->file_offset + index * kRelaSize;
            RawReloc reloc;
            reloc.file_offset = offset;
            reloc.r_offset = read_u64(image_, offset);
            reloc.r_info = read_u64(image_, offset + 8);
            reloc.addend = read_i64(image_, offset + 16);
            reloc.from_plt = plt;
            reloc.index = index;
            const std::uint64_t symbol = reloc.r_info >> 32;
            max_reloc_symbol_ = std::max(max_reloc_symbol_, symbol);
            raw_relocations_.push_back(reloc);
        }
    }

    void validate_hash_and_symbols() {
        const auto symtab = dynamic_value(kDtSymtab);
        const auto hash = dynamic_value(kDtHash);
        const auto gnuHash = dynamic_value(kDtGnuHash);

        std::optional<std::uint64_t> sysv_count;
        std::optional<std::uint64_t> gnu_count;
        if (hash) {
            sysv_count = validate_sysv_hash(*hash);
        }
        if (gnuHash) {
            gnu_count = validate_gnu_hash(*gnuHash);
        }
        if (sysv_count && gnu_count && *sysv_count != *gnu_count) {
            add(FindingCode::malformed_hash_table,
                "DT_HASH and DT_GNU_HASH imply different symbol counts",
                dynamic_source(kDtHash));
        }

        std::optional<std::uint64_t> count;
        if (sysv_count) {
            count = sysv_count;
        }
        if (gnu_count) {
            count = std::max(count.value_or(0), *gnu_count);
        }
        if (max_reloc_symbol_ != 0) {
            if (max_reloc_symbol_ ==
                std::numeric_limits<std::uint64_t>::max()) {
                add(FindingCode::integer_overflow,
                    "relocation symbol index cannot be incremented",
                    dynamic_source(kDtRela));
                return;
            }
            const std::uint64_t required_symbols = max_reloc_symbol_ + 1;
            if (count && required_symbols > *count) {
                add(FindingCode::malformed_symbol_table,
                    "relocation symbol index exceeds the hash-declared symbol "
                    "count",
                    dynamic_source(kDtSymtab));
            } else if (!count) {
                count = required_symbols;
            }
        }

        if (!symtab) {
            if (count && *count > 0) {
                add(FindingCode::malformed_symbol_table,
                    "hash or relocation table implies symbols but DT_SYMTAB "
                    "is absent",
                    dynamic_source(kDtSymtab));
            }
            if (max_reloc_symbol_ != 0) {
                add(FindingCode::malformed_symbol_table,
                    "relocation references a missing dynamic symbol table",
                    dynamic_source(kDtRela));
            }
            if (symtab_shndx_address_ || versym_address_) {
                add(FindingCode::malformed_symbol_table,
                    "symbol auxiliary tables are present without DT_SYMTAB",
                    dynamic_source(kDtSymtabShndx));
            }
            return;
        }

        if (!hash && !gnuHash) {
            add(FindingCode::malformed_hash_table,
                "DT_SYMTAB has no DT_HASH/DT_GNU_HASH; the symbol table extent "
                "cannot be validated",
                dynamic_source(kDtSymtab));
            return;
        }
        if (!count || *count == 0) {
            count = 1;
        }
        if (*count >
            (std::numeric_limits<std::uint64_t>::max() / kSymbolSize)) {
            add(FindingCode::integer_overflow,
                "symbol table size overflows", dynamic_source(kDtSymtab));
            return;
        }
        const std::uint64_t symbol_bytes = *count * kSymbolSize;
        result_.summary.symbol_count = *count;
        const auto witness =
            register_table(*symtab, symbol_bytes, "dynamic symbol table",
                           dynamic_source(kDtSymtab),
                           FindingCode::malformed_symbol_table);
        if (!witness) {
            return;
        }
        symbol_count_ = *count;
        symbols_.reserve(static_cast<std::size_t>(*count));
        for (std::uint64_t index = 0; index < *count; ++index) {
            const std::uint64_t offset =
                witness->file_offset + index * kSymbolSize;
            Symbol symbol;
            symbol.name = read_u32(image_, offset);
            symbol.info = image_[static_cast<std::size_t>(offset + 4)];
            symbol.other = image_[static_cast<std::size_t>(offset + 5)];
            symbol.shndx = read_u16(image_, offset + 6);
            symbol.value = read_u64(image_, offset + 8);
            symbol.size = read_u64(image_, offset + 16);
            symbol.file_offset = offset;
            symbols_.push_back(symbol);
            if (symbol.name != 0) {
                validate_string_index(symbol.name, "symbol st_name");
            }
        }

        if (symtab_shndx_address_) {
            std::uint64_t bytes = 0;
            if (mul_overflow(*count, 4, bytes)) {
                add(FindingCode::integer_overflow,
                    "DT_SYMTAB_SHNDX size overflows",
                    dynamic_source(kDtSymtabShndx));
            } else {
                (void)register_table(*symtab_shndx_address_, bytes,
                                     "DT_SYMTAB_SHNDX table",
                                     dynamic_source(kDtSymtabShndx),
                                     FindingCode::malformed_symbol_table);
            }
        }
        if (versym_address_) {
            std::uint64_t bytes = 0;
            std::uint64_t count_twice = 0;
            if (add_overflow(*count, *count, count_twice)) {
                add(FindingCode::integer_overflow,
                    "DT_VERSYM size overflows",
                    dynamic_source(kDtVersym));
            } else {
                bytes = count_twice;
                (void)register_table(*versym_address_, bytes,
                                     "DT_VERSYM table",
                                     dynamic_source(kDtVersym),
                                     FindingCode::malformed_symbol_table);
            }
        }
    }

    std::optional<std::uint64_t> validate_sysv_hash(std::uint64_t address) {
        const auto span = file_span(address);
        if (!span || span->available < 8) {
            add(FindingCode::truncated_table, "DT_HASH header is truncated",
                dynamic_source(kDtHash));
            return std::nullopt;
        }
        const std::uint64_t buckets = read_u32(image_, span->offset);
        const std::uint64_t chains = read_u32(image_, span->offset + 4);
        std::uint64_t words = 0;
        if (add_overflow(buckets, chains, words) ||
            add_overflow(words, 2, words)) {
            add(FindingCode::integer_overflow, "DT_HASH size overflows",
                dynamic_source(kDtHash));
            return std::nullopt;
        }
        std::uint64_t bytes = 0;
        if (mul_overflow(words, 4, bytes)) {
            add(FindingCode::integer_overflow, "DT_HASH size overflows",
                dynamic_source(kDtHash));
            return std::nullopt;
        }
        const auto witness =
            register_table(address, bytes, "DT_HASH table",
                           dynamic_source(kDtHash),
                           FindingCode::malformed_hash_table);
        if (!witness) {
            return std::nullopt;
        }
        if (chains == 0) {
            add(FindingCode::malformed_hash_table,
                "DT_HASH has no symbol chains", dynamic_source(kDtHash));
        }
        for (std::uint64_t index = 0; index < buckets; ++index) {
            const std::uint64_t bucket =
                read_u32(image_, witness->file_offset + 8 + index * 4);
            if (bucket >= chains) {
                add(FindingCode::malformed_hash_table,
                    "DT_HASH bucket points outside the symbol chain array",
                    FileWitness{witness->file_offset + 8 + index * 4, 4, 0});
            }
        }
        return chains;
    }

    std::optional<std::uint64_t> validate_gnu_hash(std::uint64_t address) {
        const auto span = file_span(address);
        if (!span || span->available < 16) {
            add(FindingCode::truncated_table,
                "DT_GNU_HASH header is truncated", dynamic_source(kDtGnuHash));
            return std::nullopt;
        }
        const std::uint64_t buckets = read_u32(image_, span->offset);
        const std::uint64_t symoffset = read_u32(image_, span->offset + 4);
        const std::uint64_t bloom_size =
            read_u32(image_, span->offset + 8);
        std::uint64_t bloom_bytes = 0;
        if (mul_overflow(bloom_size, 8, bloom_bytes) ||
            add_overflow(16, bloom_bytes, bloom_bytes)) {
            add(FindingCode::integer_overflow,
                "DT_GNU_HASH bloom filter size overflows",
                dynamic_source(kDtGnuHash));
            return std::nullopt;
        }
        std::uint64_t bucket_bytes = 0;
        std::uint64_t chain_start = 0;
        if (mul_overflow(buckets, 4, bucket_bytes) ||
            add_overflow(bloom_bytes, bucket_bytes, chain_start)) {
            add(FindingCode::integer_overflow,
                "DT_GNU_HASH bucket table size overflows",
                dynamic_source(kDtGnuHash));
            return std::nullopt;
        }
        if (chain_start > span->available) {
            add(FindingCode::truncated_table,
                "DT_GNU_HASH bloom/bucket tables are truncated",
                dynamic_source(kDtGnuHash));
            return std::nullopt;
        }
        const std::uint64_t bucket_offset = span->offset + bloom_bytes;
        const std::uint64_t chain_offset = span->offset + chain_start;
        const std::uint64_t max_chain_words =
            (span->available - chain_start) / 4;
        std::uint64_t highest = 0;
        bool any_chain = false;
        std::uint64_t walked_words = 0;
        for (std::uint64_t index = 0; index < buckets; ++index) {
            const std::uint64_t bucket =
                read_u32(image_, bucket_offset + index * 4);
            if (bucket == 0) {
                continue;
            }
            if (bucket < symoffset) {
                add(FindingCode::malformed_hash_table,
                    "DT_GNU_HASH bucket points before symoffset",
                    FileWitness{bucket_offset + index * 4, 4, 0});
                continue;
            }
            std::uint64_t chain_index = bucket - symoffset;
            while (true) {
                if (chain_index >= max_chain_words) {
                    add(FindingCode::truncated_table,
                        "DT_GNU_HASH chain is not terminated inside its "
                        "containing PT_LOAD",
                        FileWitness{chain_offset + chain_index * 4, 4, 0});
                    break;
                }
                any_chain = true;
                if (++walked_words > max_chain_words) {
                    add(FindingCode::malformed_hash_table,
                        "DT_GNU_HASH buckets traverse overlapping or "
                        "unbounded chains",
                        FileWitness{chain_offset + chain_index * 4, 4, 0});
                    break;
                }
                highest = std::max(highest, chain_index);
                const std::uint64_t hash_value =
                    read_u32(image_, chain_offset + chain_index * 4);
                if ((hash_value & 1U) != 0) {
                    break;
                }
                ++chain_index;
            }
        }
        std::uint64_t count = symoffset;
        if (any_chain) {
            std::uint64_t count_after_symoffset = 0;
            if (add_overflow(symoffset, 1, count_after_symoffset) ||
                add_overflow(count_after_symoffset, highest, count)) {
                add(FindingCode::integer_overflow,
                    "DT_GNU_HASH symbol count overflows",
                    dynamic_source(kDtGnuHash));
                return std::nullopt;
            }
        }
        std::uint64_t used = chain_start;
        if (any_chain) {
            std::uint64_t chain_bytes = 0;
            if (mul_overflow(highest + 1, 4, chain_bytes) ||
                add_overflow(chain_start, chain_bytes, used)) {
                add(FindingCode::integer_overflow,
                    "DT_GNU_HASH chain size overflows",
                    dynamic_source(kDtGnuHash));
                return std::nullopt;
            }
        }
        (void)register_table(address, used, "DT_GNU_HASH table",
                             dynamic_source(kDtGnuHash),
                             FindingCode::malformed_hash_table);
        return count;
    }

    [[nodiscard]] const Symbol* symbol_at(std::uint64_t index) const {
        if (index >= symbols_.size()) {
            return nullptr;
        }
        return &symbols_[static_cast<std::size_t>(index)];
    }

    [[nodiscard]] bool address_in_executable(std::uint64_t address) const {
        if (address == 0) {
            return false;
        }
        for (const Phdr& phdr : phdrs_) {
            if (phdr.type == kPtLoad && phdr.executable() &&
                address >= phdr.vaddr && address < phdr.vaddr_end()) {
                return true;
            }
        }
        return false;
    }

    [[nodiscard]] bool metadata_intersects(std::uint64_t start,
                                           std::uint64_t end,
                                           std::string_view& name) const {
        for (const MetadataRange& range : metadata_) {
            if (start < range.end && range.start < end) {
                name = range.name;
                return true;
            }
        }
        return false;
    }

    [[nodiscard]] std::optional<std::uint64_t> relocation_width(
        std::uint64_t type, const Symbol* symbol) {
        switch (type) {
        case kRNone:
        case kRTlsdescCall:
            return 0;
        case kR64:
            return 8;
        case kRPc32:
        case kRGot32:
        case kRPlt32:
            return 4;
        case kRCopy:
            if (symbol == nullptr || symbol->size == 0) {
                return std::nullopt;
            }
            return symbol->size;
        case kRGlobDat:
        case kRJumpSlot:
        case kRRelative:
            return 8;
        case kRGotPcRel:
        case 10: // R_X86_64_32
        case 11: // R_X86_64_32S
        case 26: // R_X86_64_GOTPC32
        case 32: // R_X86_64_SIZE32
        case 34: // R_X86_64_GOTPC32_TLSDESC
        case 41: // R_X86_64_GOTPCRELX
        case 42: // R_X86_64_REX_GOTPCRELX
            return 4;
        case 12: // R_X86_64_16
        case 13: // R_X86_64_PC16
            return 2;
        case 14: // R_X86_64_8
        case 15: // R_X86_64_PC8
            return 1;
        case 16: // R_X86_64_DTPMOD64
        case 17: // R_X86_64_DTPOFF64
        case 18: // R_X86_64_TPOFF64
        case 24: // R_X86_64_PC64
        case 25: // R_X86_64_GOTOFF64
        case 27: // R_X86_64_GOT64
        case 28: // R_X86_64_GOTPCREL64
        case 29: // R_X86_64_GOTPC64
        case 30: // R_X86_64_GOTPLT64
        case 31: // R_X86_64_PLTOFF64
        case 33: // R_X86_64_SIZE64
        case kRIrelative:
        case kRRelative64:
            return 8;
        case 19: // R_X86_64_TLSGD
        case 20: // R_X86_64_TLSLD
            return 8;
        case 21: // R_X86_64_DTPOFF32
        case 22: // R_X86_64_GOTTPOFF
        case 23: // R_X86_64_TPOFF32
            return 4;
        case kRTlsdesc:
            return 16;
        default:
            return std::nullopt;
        }
    }

    [[nodiscard]] bool type_uses_symbol(std::uint64_t type) const {
        return type != kRNone && type != kRRelative &&
               type != kRIrelative && type != kRRelative64;
    }

    [[nodiscard]] bool type_is_absolute_pointer(std::uint64_t type) const {
        switch (type) {
        case kR64:
        case kRGlobDat:
        case kRJumpSlot:
        case kRRelative:
        case kRIrelative:
        case kRRelative64:
            return true;
        default:
            return false;
        }
    }

    [[nodiscard]] bool relocation_points_to_executable(
        const RawReloc& reloc, std::uint64_t type,
        const Symbol* symbol) const {
        if (!type_is_absolute_pointer(type)) {
            return false;
        }
        if (type == kRRelative || type == kRRelative64) {
            return address_in_executable(
                static_cast<std::uint64_t>(reloc.addend));
        }
        if (type == kRIrelative) {
            return true;
        }
        if (symbol == nullptr) {
            return false;
        }
        if (symbol->function()) {
            return true;
        }
        if (symbol->undefined()) {
            return false;
        }
        std::uint64_t value = 0;
        if (add_overflow(symbol->value,
                         static_cast<std::uint64_t>(reloc.addend), value)) {
            return false;
        }
        return address_in_executable(value);
    }

    void analyze_relocations() {
        std::optional<FileWitness> first_jump_slot;
        for (const RawReloc& reloc : raw_relocations_) {
            ++result_.summary.relocation_count;
            const std::uint64_t symbol_index = reloc.r_info >> 32;
            const std::uint64_t type = reloc.r_info & 0xffffffffU;
            const Symbol* symbol = symbol_at(symbol_index);
            const FileWitness relocation_witness{reloc.file_offset, kRelaSize,
                                                  reloc.r_offset};

            if (type_uses_symbol(type)) {
                if (symbol_index == 0) {
                    add(FindingCode::unexpected_relocation_symbol,
                        "relocation type " + hex_value(type) +
                            " requires a non-zero symbol",
                        relocation_witness, Severity::error,
                        relocation_witness, std::nullopt);
                    continue;
                }
                if (symbol == nullptr) {
                    add(FindingCode::malformed_symbol_table,
                        "relocation symbol index " +
                            hex_value(symbol_index) +
                            " is outside the validated symbol table",
                        relocation_witness, Severity::error,
                        relocation_witness, std::nullopt);
                    continue;
                }
            } else if (symbol_index != 0) {
                add(FindingCode::unexpected_relocation_symbol,
                    "relocation type " + hex_value(type) +
                        " has a non-zero symbol index",
                    relocation_witness, Severity::error,
                    relocation_witness, std::nullopt);
            }

            const auto width = relocation_width(type, symbol);
            if (!width) {
                add(FindingCode::unknown_relocation_type,
                    "unsupported x86-64 relocation type " +
                        hex_value(type) + " cannot be proven safe",
                    relocation_witness, Severity::error,
                    relocation_witness, std::nullopt);
                continue;
            }

            if (type == kRJumpSlot) {
                ++result_.summary.jump_slot_count;
                if (!first_jump_slot) {
                    first_jump_slot = relocation_witness;
                }
            } else if (type == kRGlobDat) {
                ++result_.summary.glob_dat_count;
            } else if (type == kRCopy) {
                ++result_.summary.copy_count;
            }

            if (*width == 0) {
                continue;
            }
            std::uint64_t target_end = 0;
            if (add_overflow(reloc.r_offset, *width, target_end)) {
                add(FindingCode::integer_overflow,
                    "relocation target range overflows",
                    relocation_witness, Severity::error,
                    relocation_witness, std::nullopt);
                continue;
            }
            const Phdr* target_load =
                containing_load(reloc.r_offset, *width);
            std::optional<FileWitness> target_witness =
                file_witness(reloc.r_offset, *width);
            if (target_load == nullptr) {
                add(FindingCode::invalid_relocation_target,
                    "relocation target does not fit in exactly one PT_LOAD",
                    relocation_witness, Severity::error,
                    relocation_witness, target_witness);
                continue;
            }
            if (target_load->executable()) {
                add(FindingCode::text_relocation,
                    "relocation writes to an executable PT_LOAD",
                    relocation_witness, Severity::error,
                    relocation_witness, target_witness);
            }
            if (!target_load->writable()) {
                add(FindingCode::invalid_relocation_target,
                    "relocation target is not in a writable PT_LOAD",
                    relocation_witness, Severity::error,
                    relocation_witness, target_witness);
                continue;
            }

            std::string_view metadata_name;
            if (metadata_intersects(reloc.r_offset, target_end,
                                    metadata_name)) {
                add(FindingCode::relocation_overlaps_dynamic_metadata,
                    "relocation target overlaps dynamic metadata (" +
                        std::string(metadata_name) + ")",
                    relocation_witness, Severity::error,
                    relocation_witness, target_witness);
            }

            bool fully_relro = false;
            bool overlaps_relro = false;
            if (result_.summary.has_relro) {
                overlaps_relro =
                    reloc.r_offset < result_.summary.relro_page_end &&
                    result_.summary.relro_page_start < target_end;
                fully_relro =
                    reloc.r_offset >= result_.summary.relro_page_start &&
                    target_end <= result_.summary.relro_page_end;
                if (overlaps_relro && !fully_relro) {
                    add(FindingCode::relocation_crosses_region,
                        "relocation target crosses the effective "
                        "page-rounded RELRO boundary",
                        relocation_witness, Severity::error,
                        relocation_witness, target_witness);
                }
            }
            const bool writable_after_relocation = !fully_relro;
            const bool got_relocation =
                type == kRGlobDat || type == kRJumpSlot || type == kRCopy;
            const bool function_pointer =
                relocation_points_to_executable(reloc, type, symbol);

            if (got_relocation && writable_after_relocation) {
                add(FindingCode::writable_got_relocation,
                    "GOT/COPY relocation target remains writable after "
                    "relocation and RELRO",
                    relocation_witness, Severity::error,
                    relocation_witness, target_witness);
            } else if (function_pointer && writable_after_relocation) {
                add(FindingCode::writable_function_pointer_relocation,
                    "relocation stores a function/code entry into a target "
                    "that remains writable after RELRO",
                    relocation_witness, Severity::error,
                    relocation_witness, target_witness);
            }
        }

        if (result_.summary.jump_slot_count != 0 &&
            !result_.summary.bind_now) {
            add(FindingCode::lazy_binding,
                "JUMP_SLOT relocations exist but neither DT_BIND_NOW, "
                "DF_BIND_NOW, nor DF_1_NOW is set",
                first_jump_slot.value_or(FileWitness{}));
        }
    }

    void analyze_relro() {
        if (!relro_index_) {
            return;
        }
        const Phdr& relro = phdrs_[*relro_index_];
        result_.summary.has_relro = true;
        result_.summary.relro_start = relro.vaddr;
        result_.summary.relro_end = relro.vaddr_end();
        const FileWitness source{relro.file_offset, 56, relro.vaddr};

        if (!is_power_of_two(options_.page_size)) {
            add(FindingCode::invalid_relro,
                "policy page size is not a power of two", source);
            return;
        }
        std::uint64_t declared_end = 0;
        if (add_overflow(relro.vaddr, relro.memsz, declared_end)) {
            add(FindingCode::integer_overflow,
                "PT_GNU_RELRO virtual range overflows", source);
            return;
        }
        const Phdr* load = containing_load(relro.vaddr, relro.memsz);
        if (load == nullptr) {
            add(FindingCode::invalid_relro,
                "PT_GNU_RELRO is not wholly contained in one PT_LOAD", source);
            return;
        }
        if (!load->writable()) {
            add(FindingCode::invalid_relro,
                "PT_GNU_RELRO is not inside a writable PT_LOAD and therefore "
                "does not close a post-relocation write window",
                source);
        }
        if (relro.vaddr < load->vaddr) {
            add(FindingCode::invalid_relro,
                "PT_GNU_RELRO underflows its containing PT_LOAD", source);
            return;
        }
        const std::uint64_t delta = relro.vaddr - load->vaddr;
        if (delta > load->filesz || relro.filesz > load->filesz - delta) {
            add(FindingCode::invalid_relro,
                "PT_GNU_RELRO file range is outside the containing PT_LOAD",
                source);
        } else if (relro.filesz != 0) {
            const std::uint64_t expected = load->offset + delta;
            if (expected != relro.offset) {
                add(FindingCode::invalid_relro,
                    "PT_GNU_RELRO p_offset disagrees with its containing "
                    "PT_LOAD mapping",
                    FileWitness{relro.file_offset + 8, 8, relro.vaddr});
            }
        }

        const auto page_start = align_down(relro.vaddr, options_.page_size);
        const auto page_end = align_up(declared_end, options_.page_size);
        if (!page_start || !page_end) {
            add(FindingCode::integer_overflow,
                "page-rounded PT_GNU_RELRO range overflows", source);
            return;
        }
        const auto load_pages = load_page_range(*load);
        if (!load_pages) {
            add(FindingCode::integer_overflow,
                "page-rounded containing PT_LOAD range overflows", source);
            return;
        }
        result_.summary.relro_page_start = *page_start;
        result_.summary.relro_page_end = *page_end;
        if (*page_start < load_pages->start || *page_end > load_pages->end) {
            add(FindingCode::relro_overflow,
                "page-rounded PT_GNU_RELRO extends outside the mapped pages of "
                "its containing PT_LOAD",
                source);
        }
    }

    void analyze_constructor_arrays() {
        const std::array<std::pair<std::uint64_t, std::uint64_t>, 3> specs{{
            {kDtInitArray, kDtInitArraysz},
            {kDtFiniArray, kDtFiniArraysz},
            {kDtPreinitArray, kDtPreinitArraysz},
        }};
        for (const auto& [address_tag, size_tag] : specs) {
            const auto address = dynamic_value(address_tag);
            const auto size = dynamic_value(size_tag);
            if (address.has_value() != size.has_value()) {
                add(FindingCode::malformed_dynamic_segment,
                    "constructor/finalizer array address and size tags must "
                    "appear together",
                    address ? dynamic_source(address_tag)
                            : dynamic_source(size_tag));
                continue;
            }
            if (!address || !size || *size == 0) {
                continue;
            }
            const char* name = "DT_INIT_ARRAY";
            if (address_tag == kDtFiniArray) {
                name = "DT_FINI_ARRAY";
            } else if (address_tag == kDtPreinitArray) {
                name = "DT_PREINIT_ARRAY";
            }
            if ((*size % 8) != 0) {
                add(FindingCode::malformed_dynamic_segment,
                    std::string(name) + " size is not a multiple of 8",
                    dynamic_source(size_tag));
                continue;
            }
            const auto table = register_table(
                *address, *size, name, dynamic_source(address_tag),
                FindingCode::malformed_dynamic_segment);
            if (!table) {
                continue;
            }
            const Phdr* load = containing_load(*address, *size);
            if (load == nullptr) {
                add(FindingCode::writable_constructor_array,
                    std::string(name) + " is not inside one PT_LOAD",
                    *table);
                continue;
            }
            const std::uint64_t end = *address + *size;
            const bool in_relro =
                result_.summary.has_relro &&
                *address >= result_.summary.relro_page_start &&
                end <= result_.summary.relro_page_end;
            if (load->writable() && !in_relro) {
                add(FindingCode::writable_constructor_array,
                    std::string(name) +
                        " is in writable memory outside the effective RELRO "
                        "page range",
                    *table);
            }
        }
    }

    std::span<const std::uint8_t> image_;
    PolicyOptions options_;
    PolicyResult result_;
    Ehdr eh_;
    std::vector<Phdr> phdrs_;
    std::optional<std::size_t> dynamic_index_;
    std::optional<std::size_t> relro_index_;
    std::vector<DynamicEntry> dynamic_entries_;
    std::map<std::uint64_t, std::uint64_t> dynamic_values_;
    std::map<std::uint64_t, std::uint64_t> dynamic_offsets_;
    std::vector<MetadataRange> metadata_;
    std::optional<std::uint64_t> strtab_file_;
    std::uint64_t strsz_ = 0;
    std::vector<RelaTable> rela_tables_;
    std::vector<RawReloc> raw_relocations_;
    std::uint64_t max_reloc_symbol_ = 0;
    std::vector<Symbol> symbols_;
    std::uint64_t symbol_count_ = 0;
    std::optional<std::uint64_t> symtab_shndx_address_;
    std::optional<std::uint64_t> versym_address_;
};

} // namespace

std::string_view to_string(FindingCode code) noexcept {
    switch (code) {
    case FindingCode::malformed_header:
        return "malformed-header";
    case FindingCode::unsupported_elf:
        return "unsupported-elf";
    case FindingCode::malformed_program_headers:
        return "malformed-program-headers";
    case FindingCode::malformed_dynamic_segment:
        return "malformed-dynamic-segment";
    case FindingCode::malformed_string_table:
        return "malformed-string-table";
    case FindingCode::malformed_symbol_table:
        return "malformed-symbol-table";
    case FindingCode::malformed_hash_table:
        return "malformed-hash-table";
    case FindingCode::malformed_relocation_table:
        return "malformed-relocation-table";
    case FindingCode::truncated_table:
        return "truncated-table";
    case FindingCode::integer_overflow:
        return "integer-overflow";
    case FindingCode::overlapping_load_segments:
        return "overlapping-load-segments";
    case FindingCode::ambiguous_load_permissions:
        return "ambiguous-load-permissions";
    case FindingCode::rwx_load_segment:
        return "rwx-load-segment";
    case FindingCode::text_relocation:
        return "text-relocation";
    case FindingCode::unknown_relocation_type:
        return "unknown-relocation-type";
    case FindingCode::unexpected_relocation_symbol:
        return "unexpected-relocation-symbol";
    case FindingCode::invalid_relocation_target:
        return "invalid-relocation-target";
    case FindingCode::relocation_crosses_region:
        return "relocation-crosses-region";
    case FindingCode::relocation_overlaps_dynamic_metadata:
        return "relocation-overlaps-dynamic-metadata";
    case FindingCode::lazy_binding:
        return "lazy-binding";
    case FindingCode::writable_got_relocation:
        return "writable-got-relocation";
    case FindingCode::writable_function_pointer_relocation:
        return "writable-function-pointer-relocation";
    case FindingCode::writable_constructor_array:
        return "writable-constructor-array";
    case FindingCode::invalid_relro:
        return "invalid-relro";
    case FindingCode::relro_overflow:
        return "relro-overflow";
    case FindingCode::duplicate_dynamic_tag:
        return "duplicate-dynamic-tag";
    case FindingCode::unsupported_dynamic_feature:
        return "unsupported-dynamic-feature";
    case FindingCode::dynamic_after_null:
        return "dynamic-after-null";
    }
    return "unknown";
}

std::string_view to_string(PolicyDecision decision) noexcept {
    return decision == PolicyDecision::allow ? "allow" : "reject";
}

std::string_view to_string(Severity severity) noexcept {
    return severity == Severity::error ? "error" : "warning";
}

PolicyResult analyze(std::span<const std::uint8_t> image,
                     PolicyOptions options) {
    Analyzer analyzer(image, options);
    return analyzer.run();
}

PolicyResult analyze(std::span<const std::byte> image,
                     PolicyOptions options) {
    return analyze(
        std::span<const std::uint8_t>(
            reinterpret_cast<const std::uint8_t*>(image.data()),
            image.size()),
        options);
}

PolicyResult analyze_file(const std::string& path, PolicyOptions options) {
    std::ifstream input(path, std::ios::binary);
    if (!input) {
        PolicyResult result;
        result.decision = PolicyDecision::reject;
        result.allowed.value = false;
        result.rejected.value = true;
        result.findings.push_back(Finding{
            FindingCode::truncated_table,
            Severity::error,
            "cannot open file for pre-load inspection",
            FileWitness{0, 0, 0},
            std::nullopt,
            std::nullopt,
            0,
            0,
            0,
            std::nullopt,
        });
        return result;
    }
    input.seekg(0, std::ios::end);
    const std::streamoff length = input.tellg();
    if (length < 0) {
        PolicyResult result;
        result.decision = PolicyDecision::reject;
        result.allowed.value = false;
        result.rejected.value = true;
        result.findings.push_back(Finding{
            FindingCode::truncated_table,
            Severity::error,
            "cannot determine file length",
            FileWitness{0, 0, 0},
            std::nullopt,
            std::nullopt,
            0,
            0,
            0,
            std::nullopt,
        });
        return result;
    }
    input.seekg(0, std::ios::beg);
    std::vector<std::uint8_t> image(static_cast<std::size_t>(length));
    if (!image.empty() &&
        !input.read(reinterpret_cast<char*>(image.data()),
                    static_cast<std::streamsize>(image.size()))) {
        PolicyResult result;
        result.decision = PolicyDecision::reject;
        result.allowed.value = false;
        result.rejected.value = true;
        result.findings.push_back(Finding{
            FindingCode::truncated_table,
            Severity::error,
            "short read while loading file for pre-load inspection",
            FileWitness{0, static_cast<std::uint64_t>(image.size()), 0},
            std::nullopt,
            std::nullopt,
            0,
            static_cast<std::uint64_t>(image.size()),
            0,
            std::nullopt,
        });
        return result;
    }
    return analyze(std::span<const std::uint8_t>(image.data(), image.size()),
                   options);
}

PolicyResult check_shared_object(std::span<const std::uint8_t> image,
                                 PolicyOptions options) {
    return analyze(image, options);
}

std::string PolicyResult::format() const {
    std::ostringstream output;
    output << "decision: " << to_string(decision) << '\n';
    output << "loads: " << summary.loads.size() << '\n';
    output << "symbols: " << summary.symbol_count << '\n';
    output << "relocations: " << summary.relocation_count << '\n';
    output << "jump_slots: " << summary.jump_slot_count << '\n';
    output << "glob_dat: " << summary.glob_dat_count << '\n';
    output << "copy: " << summary.copy_count << '\n';
    output << "bind_now: " << (summary.bind_now ? "yes" : "no") << '\n';
    output << "relro: " << (summary.has_relro ? "yes" : "no") << '\n';
    if (summary.has_relro) {
        output << "relro_declared: " << hex_value(summary.relro_start)
               << "-" << hex_value(summary.relro_end) << '\n';
        output << "relro_pages: " << hex_value(summary.relro_page_start)
               << "-" << hex_value(summary.relro_page_end) << '\n';
    }
    if (summary.soname) {
        output << "soname: " << *summary.soname << '\n';
    }
    for (const Finding& finding : findings) {
        output << "finding: " << to_string(finding.code)
               << " severity=" << to_string(finding.severity) << '\n';
        output << "  message: " << finding.message << '\n';
        output << "  file_offset: " << hex_value(finding.source.file_offset)
               << " size: " << finding.source.size << '\n';
        if (finding.source.virtual_address != 0) {
            output << "  virtual_address: "
                   << hex_value(finding.source.virtual_address) << '\n';
        }
        if (finding.relocation) {
            output << "  relocation_file_offset: "
                   << hex_value(finding.relocation->file_offset) << '\n';
        }
        if (finding.target) {
            output << "  target_file_offset: "
                   << hex_value(finding.target->file_offset) << '\n';
            output << "  target_virtual_address: "
                   << hex_value(finding.target->virtual_address) << '\n';
        }
    }
    return output.str();
}

} // namespace netsec
