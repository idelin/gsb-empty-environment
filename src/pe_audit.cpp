#include "pe_audit/pe_audit.h"

#include <algorithm>
#include <array>
#include <cstring>
#include <fstream>
#include <limits>
#include <string>
#include <utility>
#include <vector>

namespace pe_audit {
namespace {

constexpr std::uint16_t kDosSignature = 0x5A4D;
constexpr std::uint32_t kPeSignature = 0x00004550;
constexpr std::uint16_t kPe32PlusMagic = 0x020B;
constexpr std::uint32_t kSectionHeaderSize = 40;
constexpr std::uint32_t kDelayDescriptorSize = 32;
constexpr std::uint32_t kGuardFlagsOffset = 144;
constexpr std::uint32_t kGuardFlagsSize = 4;
constexpr std::uint32_t kGuardCfInstrumented = 0x00000100;
constexpr std::uint32_t kGuardProtectDelayLoadIat = 0x00001000;
constexpr std::uint32_t kScnMemWrite = 0x80000000;
constexpr std::size_t kMaxDllNameLength = 255;
constexpr std::size_t kMaxDllNameScan = 512;

constexpr std::uint32_t kDirImport = 1;
constexpr std::uint32_t kDirLoadConfig = 10;
constexpr std::uint32_t kDirDelayImport = 13;
constexpr std::uint32_t kDirBaseReloc = 15;

struct Section {
    std::uint32_t virtual_address = 0;
    std::uint32_t virtual_size = 0;
    std::uint32_t raw_size = 0;
    std::uint32_t raw_pointer = 0;
    std::uint32_t characteristics = 0;
    std::uint64_t mapped_end = 0;
};

struct DataDirectory {
    std::uint32_t rva = 0;
    std::uint32_t size = 0;
};

class ByteView {
public:
    ByteView(const std::uint8_t* data, std::size_t size)
        : data_(data), size_(static_cast<std::uint64_t>(size)) {}

    bool contains(std::uint64_t offset, std::uint64_t length) const noexcept {
        return offset <= size_ && length <= size_ - offset;
    }

    bool read_u16(std::uint64_t offset, std::uint16_t& value) const noexcept {
        if (!contains(offset, 2)) {
            return false;
        }
        const std::uint64_t absolute = offset;
        value = static_cast<std::uint16_t>(data_[absolute]) |
                static_cast<std::uint16_t>(
                    static_cast<std::uint16_t>(data_[absolute + 1]) << 8);
        return true;
    }

    bool read_u32(std::uint64_t offset, std::uint32_t& value) const noexcept {
        if (!contains(offset, 4)) {
            return false;
        }
        const std::uint64_t absolute = offset;
        value = static_cast<std::uint32_t>(data_[absolute]) |
                (static_cast<std::uint32_t>(data_[absolute + 1]) << 8) |
                (static_cast<std::uint32_t>(data_[absolute + 2]) << 16) |
                (static_cast<std::uint32_t>(data_[absolute + 3]) << 24);
        return true;
    }

    bool read_u64(std::uint64_t offset, std::uint64_t& value) const noexcept {
        std::uint32_t low = 0;
        std::uint32_t high = 0;
        if (!read_u32(offset, low) || !read_u32(offset + 4, high)) {
            return false;
        }
        value = static_cast<std::uint64_t>(low) |
                (static_cast<std::uint64_t>(high) << 32);
        return true;
    }

    std::uint8_t at(std::uint64_t offset) const noexcept {
        return data_[offset];
    }

private:
    const std::uint8_t* data_;
    std::uint64_t size_;
};

std::string hex_value(std::uint64_t value) {
    static constexpr char kDigits[] = "0123456789abcdef";
    std::array<char, 18> buffer{};
    std::size_t pos = buffer.size();
    do {
        --pos;
        buffer[pos] = kDigits[value & 0xFu];
        value >>= 4;
    } while (value != 0);
    --pos;
    buffer[pos] = 'x';
    --pos;
    buffer[pos] = '0';
    return std::string(buffer.data() + pos, buffer.size() - pos);
}

bool ascii_equal_ignore_case(const std::string& left, const std::string& right) {
    if (left.size() != right.size()) {
        return false;
    }
    for (std::size_t index = 0; index < left.size(); ++index) {
        unsigned char a = static_cast<unsigned char>(left[index]);
        unsigned char b = static_cast<unsigned char>(right[index]);
        if (a >= 'A' && a <= 'Z') {
            a = static_cast<unsigned char>(a - 'A' + 'a');
        }
        if (b >= 'A' && b <= 'Z') {
            b = static_cast<unsigned char>(b - 'A' + 'a');
        }
        if (a != b) {
            return false;
        }
    }
    return true;
}

bool ends_with_ascii_ignore_case(const std::string& value,
                                 const std::string& suffix) {
    if (value.size() < suffix.size()) {
        return false;
    }
    return ascii_equal_ignore_case(
        value.substr(value.size() - suffix.size()), suffix);
}

bool ranges_overlap(std::uint64_t left_start, std::uint64_t left_size,
                    std::uint64_t right_start, std::uint64_t right_size) {
    if (left_size == 0 || right_size == 0) {
        return false;
    }
    if (left_start > std::numeric_limits<std::uint64_t>::max() - left_size ||
        right_start > std::numeric_limits<std::uint64_t>::max() - right_size) {
        return true;
    }
    const std::uint64_t left_end = left_start + left_size;
    const std::uint64_t right_end = right_start + right_size;
    return left_start < right_end && right_start < left_end;
}

class Parser {
public:
    Parser(const std::uint8_t* data, std::size_t size,
           const AuditOptions& options, Report& report)
        : view_(data, size), options_(options), report_(report) {}

    void run() {
        if (!parse_headers()) {
            return;
        }
        parse_import_directory();
        parse_load_config();
        parse_delay_imports();
        parse_relocations();
        evaluate_delay_import_guards();
    }

private:
    void add(Severity severity, IssueCode code, std::uint64_t rva,
             std::string detail) {
        Finding finding;
        finding.severity = severity;
        finding.code = code;
        finding.rva = rva;
        finding.detail = std::move(detail);
        report_.findings.push_back(std::move(finding));
    }

    void add_error(IssueCode code, std::uint64_t rva, std::string detail) {
        add(Severity::Error, code, rva, std::move(detail));
    }

    bool parse_headers() {
        std::uint16_t dos_magic = 0;
        std::uint32_t pe_offset = 0;
        if (!view_.read_u16(0, dos_magic) || dos_magic != kDosSignature) {
            add_error(IssueCode::InvalidDosHeader, 0, "missing MZ signature");
            return false;
        }
        if (!view_.read_u32(0x3C, pe_offset)) {
            add_error(IssueCode::InvalidDosHeader, 0,
                      "truncated DOS header / e_lfanew");
            return false;
        }
        if (pe_offset > std::numeric_limits<std::uint32_t>::max() - 24U ||
            !view_.contains(pe_offset, 24)) {
            add_error(IssueCode::InvalidPeSignature, pe_offset,
                      "PE header lies outside the file");
            return false;
        }

        std::uint32_t signature = 0;
        if (!view_.read_u32(pe_offset, signature) ||
            signature != kPeSignature) {
            add_error(IssueCode::InvalidPeSignature, pe_offset,
                      "missing PE signature");
            return false;
        }

        const std::uint64_t coff = static_cast<std::uint64_t>(pe_offset) + 4;
        std::uint16_t machine = 0;
        std::uint16_t section_count = 0;
        std::uint16_t optional_size = 0;
        if (!view_.read_u16(coff, machine) ||
            !view_.read_u16(coff + 2, section_count) ||
            !view_.read_u16(coff + 16, optional_size)) {
            add_error(IssueCode::InvalidCoffHeader, coff,
                      "truncated COFF header");
            return false;
        }
        if (machine == 0 || section_count == 0) {
            add_error(IssueCode::InvalidCoffHeader, coff,
                      "missing machine type or sections");
            return false;
        }
        if (optional_size < 112) {
            add_error(IssueCode::InvalidOptionalHeader, coff + 16,
                      "optional header is too short for PE32+");
            return false;
        }
        const std::uint64_t optional = coff + 20;
        if (!view_.contains(optional, optional_size)) {
            add_error(IssueCode::InvalidOptionalHeader, optional,
                      "optional header exceeds the file");
            return false;
        }

        std::uint16_t magic = 0;
        if (!view_.read_u16(optional, magic)) {
            add_error(IssueCode::InvalidOptionalHeader, optional,
                      "cannot read optional header magic");
            return false;
        }
        if (magic != kPe32PlusMagic) {
            add_error(IssueCode::NotPe32Plus, optional,
                      "only PE32+ images are accepted");
            return false;
        }

        if (!view_.read_u64(optional + 24, image_base_) ||
            !view_.read_u32(optional + 32, section_alignment_) ||
            !view_.read_u32(optional + 56, size_of_image_) ||
            !view_.read_u32(optional + 60, size_of_headers_) ||
            !view_.read_u32(optional + 108, directory_count_)) {
            add_error(IssueCode::InvalidOptionalHeader, optional,
                      "truncated PE32+ optional header");
            return false;
        }

        const std::uint64_t directories_end =
            112 + static_cast<std::uint64_t>(directory_count_) * 8;
        if (directories_end > optional_size) {
            add_error(IssueCode::InvalidDataDirectory, optional + 112,
                      "NumberOfRvaAndSizes exceeds optional header size");
            return false;
        }
        if (section_alignment_ == 0 || size_of_image_ == 0 ||
            size_of_headers_ == 0) {
            add_error(IssueCode::InvalidOptionalHeader, optional,
                      "zero section alignment, image size, or header size");
            return false;
        }

        const std::uint64_t section_table_offset =
            optional + static_cast<std::uint64_t>(optional_size);
        const std::uint64_t section_table_size =
            static_cast<std::uint64_t>(section_count) * kSectionHeaderSize;
        if (!view_.contains(section_table_offset, section_table_size) ||
            section_table_offset + section_table_size > size_of_headers_) {
            add_error(IssueCode::InvalidSectionTable, section_table_offset,
                      "section table is truncated or outside SizeOfHeaders");
            return false;
        }
        if (!view_.contains(0, size_of_headers_)) {
            add_error(IssueCode::InvalidSectionTable, 0,
                      "SizeOfHeaders exceeds the file");
            return false;
        }
        if (size_of_headers_ > size_of_image_) {
            add_error(IssueCode::InvalidSectionTable, 0,
                      "SizeOfHeaders exceeds SizeOfImage");
            return false;
        }

        const std::uint32_t dirs_to_read =
            std::min<std::uint32_t>(directory_count_, 16);
        for (std::uint32_t index = 0; index < dirs_to_read; ++index) {
            const std::uint64_t entry =
                optional + 112 + static_cast<std::uint64_t>(index) * 8;
            if (!view_.read_u32(entry, directories_[index].rva) ||
                !view_.read_u32(entry + 4, directories_[index].size)) {
                add_error(IssueCode::InvalidDataDirectory, entry,
                          "truncated data directory");
                return false;
            }
        }
        dirs_available_ = dirs_to_read;

        for (std::uint32_t index = 0; index < section_count; ++index) {
            const std::uint64_t entry =
                section_table_offset +
                static_cast<std::uint64_t>(index) * kSectionHeaderSize;
            Section section;
            if (!view_.read_u32(entry + 8, section.virtual_size) ||
                !view_.read_u32(entry + 12, section.virtual_address) ||
                !view_.read_u32(entry + 16, section.raw_size) ||
                !view_.read_u32(entry + 20, section.raw_pointer) ||
                !view_.read_u32(entry + 36, section.characteristics)) {
                add_error(IssueCode::InvalidSectionTable, entry,
                          "truncated section header");
                return false;
            }

            const std::uint64_t mapped_size =
                std::max<std::uint64_t>(section.virtual_size,
                                        section.raw_size);
            if (mapped_size == 0) {
                add_error(IssueCode::InvalidSectionLayout,
                          section.virtual_address,
                          "section has no mapped extent");
                return false;
            }
            section.mapped_end =
                static_cast<std::uint64_t>(section.virtual_address) +
                mapped_size;
            if (section.virtual_address < size_of_headers_ ||
                section.mapped_end > size_of_image_) {
                add_error(IssueCode::InvalidSectionLayout,
                          section.virtual_address,
                          "section maps headers or exceeds SizeOfImage");
                return false;
            }
            if (section.raw_size != 0) {
                if (section.raw_pointer < size_of_headers_ ||
                    !view_.contains(section.raw_pointer,
                                    section.raw_size)) {
                    add_error(IssueCode::InvalidSectionLayout,
                              section.virtual_address,
                              "section raw data exceeds the file");
                    return false;
                }
            }
            sections_.push_back(section);
        }

        std::vector<std::uint32_t> order(sections_.size());
        for (std::size_t index = 0; index < order.size(); ++index) {
            order[index] = static_cast<std::uint32_t>(index);
        }
        std::sort(order.begin(), order.end(),
                  [this](std::uint32_t left, std::uint32_t right) {
                      return sections_[left].virtual_address <
                             sections_[right].virtual_address;
                  });
        for (std::size_t index = 1; index < order.size(); ++index) {
            const Section& previous = sections_[order[index - 1]];
            const Section& current = sections_[order[index]];
            if (static_cast<std::uint64_t>(current.virtual_address) <
                previous.mapped_end) {
                add_error(IssueCode::OverlappingSections,
                          current.virtual_address,
                          "virtual section ranges overlap");
                return false;
            }
        }
        return true;
    }

    bool directory_value(std::uint32_t index, DataDirectory& directory) const {
        if (index >= dirs_available_) {
            directory = DataDirectory{};
            return false;
        }
        directory = directories_[index];
        return directory.rva != 0 || directory.size != 0;
    }

    bool validate_directory(std::uint32_t index, IssueCode invalid_code,
                            DataDirectory& directory) {
        if (!directory_value(index, directory)) {
            return false;
        }
        if (directory.rva == 0 || directory.size == 0) {
            add_error(invalid_code, directory.rva,
                      "data directory has only one of RVA/size");
            return false;
        }
        const std::uint64_t end =
            static_cast<std::uint64_t>(directory.rva) + directory.size;
        if (end > size_of_image_) {
            add_error(invalid_code, directory.rva,
                      "data directory exceeds SizeOfImage");
            return false;
        }
        if (directory.rva < size_of_headers_) {
            add_error(invalid_code, directory.rva,
                      "data directory lies in PE headers");
            return false;
        }
        const Section* section = section_for_rva(directory.rva);
        if (section == nullptr || end > section->mapped_end) {
            add_error(invalid_code, directory.rva,
                      "data directory is not mapped by one section");
            return false;
        }
        std::uint64_t file_offset = 0;
        if (!file_offset_for_rva(directory.rva, directory.size, file_offset)) {
            add_error(invalid_code, directory.rva,
                      "data directory is not fully file-backed");
            return false;
        }
        return true;
    }

    const Section* section_for_rva(std::uint32_t rva) const {
        for (const Section& section : sections_) {
            if (rva >= section.virtual_address &&
                static_cast<std::uint64_t>(rva) < section.mapped_end) {
                return &section;
            }
        }
        return nullptr;
    }

    const Section* section_for_range(std::uint32_t rva,
                                     std::uint32_t size) const {
        for (const Section& section : sections_) {
            if (rva < section.virtual_address) {
                continue;
            }
            const std::uint64_t delta =
                static_cast<std::uint64_t>(rva) - section.virtual_address;
            if (delta < section.mapped_end - section.virtual_address &&
                static_cast<std::uint64_t>(size) <=
                    section.mapped_end - rva) {
                return &section;
            }
        }
        return nullptr;
    }

    bool file_offset_for_rva(std::uint32_t rva, std::uint32_t size,
                             std::uint64_t& offset) const {
        const Section* section = section_for_range(rva, size);
        if (section == nullptr) {
            return false;
        }
        const std::uint64_t delta =
            static_cast<std::uint64_t>(rva) - section->virtual_address;
        if (delta > section->raw_size ||
            static_cast<std::uint64_t>(size) >
                static_cast<std::uint64_t>(section->raw_size) - delta) {
            return false;
        }
        const std::uint64_t file_offset =
            static_cast<std::uint64_t>(section->raw_pointer) + delta;
        if (!view_.contains(file_offset, size)) {
            return false;
        }
        offset = file_offset;
        return true;
    }

    void parse_import_directory() {
        DataDirectory import_directory;
        if (!validate_directory(kDirImport, IssueCode::ImportDirectoryInvalid,
                                import_directory)) {
            return;
        }
        import_directory_ = import_directory;
        has_import_directory_ = true;
    }

    void parse_load_config() {
        DataDirectory directory;
        if (!validate_directory(kDirLoadConfig, IssueCode::LoadConfigInvalid,
                                directory)) {
            return;
        }
        if (directory.size < 4) {
            add_error(IssueCode::LoadConfigTruncated, directory.rva,
                      "load config directory is shorter than Size field");
            return;
        }

        std::uint64_t offset = 0;
        if (!file_offset_for_rva(directory.rva, directory.size, offset)) {
            return;
        }
        std::uint32_t structure_size = 0;
        if (!view_.read_u32(offset, structure_size)) {
            add_error(IssueCode::LoadConfigTruncated, directory.rva,
                      "cannot read load config Size");
            return;
        }
        if (structure_size < 4 || structure_size > directory.size) {
            add_error(IssueCode::LoadConfigInvalid, directory.rva,
                      "load config Size is inconsistent with its directory");
            return;
        }

        report_.load_config.present = true;
        report_.load_config.structure_size = structure_size;
        if (structure_size >= kGuardFlagsOffset + kGuardFlagsSize) {
            std::uint32_t guard_flags = 0;
            if (!view_.read_u32(offset + kGuardFlagsOffset, guard_flags)) {
                add_error(IssueCode::LoadConfigTruncated,
                          static_cast<std::uint64_t>(directory.rva) +
                              kGuardFlagsOffset,
                          "GuardFlags lies outside the file");
                return;
            }
            report_.load_config.guard_flags_available = true;
            report_.load_config.guard_flags = guard_flags;
        }
    }

    bool convert_descriptor_value(std::uint32_t raw, bool rva_mode,
                                  bool required, IssueCode error_code,
                                  std::uint32_t& converted,
                                  const char* field_name) {
        if (raw == 0) {
            converted = 0;
            if (required) {
                add_error(error_code, 0,
                          std::string(field_name) + " is required but zero");
                return false;
            }
            return true;
        }

        std::uint64_t value = raw;
        if (!rva_mode) {
            if (image_base_ > std::numeric_limits<std::uint32_t>::max() ||
                static_cast<std::uint64_t>(raw) < image_base_) {
                add_error(error_code, raw,
                          std::string(field_name) +
                              " legacy VA is below ImageBase");
                return false;
            }
            value = static_cast<std::uint64_t>(raw) - image_base_;
        }
        if (value == 0 ||
            value > std::numeric_limits<std::uint32_t>::max()) {
            add_error(error_code, raw,
                      std::string(field_name) +
                          " does not map to a valid 32-bit RVA");
            return false;
        }
        converted = static_cast<std::uint32_t>(value);
        return true;
    }

    bool read_name_string(std::uint32_t rva, std::string& name) {
        const Section* section = section_for_rva(rva);
        if (section == nullptr || rva < size_of_headers_) {
            add_error(IssueCode::DelayNameInvalid, rva,
                      "szName is not in a mapped section");
            return false;
        }
        const std::uint64_t delta =
            static_cast<std::uint64_t>(rva) - section->virtual_address;
        if (delta >= section->raw_size) {
            add_error(IssueCode::DelayNameInvalid, rva,
                      "szName has no file-backed bytes");
            return false;
        }
        const std::uint64_t available =
            static_cast<std::uint64_t>(section->raw_size) - delta;
        const std::uint64_t scan_limit =
            std::min<std::uint64_t>(available, kMaxDllNameScan);
        const std::uint64_t file_offset =
            static_cast<std::uint64_t>(section->raw_pointer) + delta;
        name.clear();
        for (std::uint64_t index = 0; index < scan_limit; ++index) {
            const unsigned char value =
                static_cast<unsigned char>(view_.at(file_offset + index));
            if (value == 0) {
                return true;
            }
            name.push_back(static_cast<char>(value));
        }
        add_error(IssueCode::DelayNameUnterminated, rva,
                  available > kMaxDllNameScan
                      ? "szName has no NUL within 512 bytes"
                      : "szName is not NUL-terminated in its section");
        return false;
    }

    bool is_safe_dll_name(const std::string& name) const {
        if (name.empty() || name.size() > kMaxDllNameLength) {
            return false;
        }
        if (name.front() == '.' || !ends_with_ascii_ignore_case(name, ".dll")) {
            return false;
        }
        if (name.find("..") != std::string::npos) {
            return false;
        }
        for (const char character : name) {
            const unsigned char value = static_cast<unsigned char>(character);
            if (value < 0x21 || value > 0x7E || value == '/' ||
                value == '\\' || value == ':') {
                return false;
            }
        }
        return true;
    }

    bool is_allowed_dll(const std::string& name) const {
        for (const std::string& allowed : default_dll_allowlist()) {
            if (ascii_equal_ignore_case(name, allowed)) {
                return true;
            }
        }
        for (const std::string& allowed : options_.additional_allowed_dlls) {
            if (ascii_equal_ignore_case(name, allowed)) {
                return true;
            }
        }
        return false;
    }

    bool validate_mapped_pointer(std::uint32_t rva, IssueCode code,
                                 const char* field_name) {
        if (rva == 0) {
            add_error(code, 0, std::string(field_name) + " is zero");
            return false;
        }
        if (rva < size_of_headers_) {
            add_error(code, rva,
                      std::string(field_name) + " points into PE headers");
            return false;
        }
        if (rva > std::numeric_limits<std::uint32_t>::max() - 8U ||
            section_for_range(rva, 8) == nullptr) {
            add_error(code, rva,
                      std::string(field_name) +
                          " does not provide a mapped pointer-sized slot");
            return false;
        }
        return true;
    }

    void parse_delay_imports() {
        DataDirectory directory;
        if (!validate_directory(kDirDelayImport, IssueCode::DelayDirectoryInvalid,
                                directory)) {
            return;
        }
        delay_import_directory_ = directory;
        has_delay_import_directory_ = true;
        if (directory.size < kDelayDescriptorSize ||
            directory.size % kDelayDescriptorSize != 0) {
            add_error(IssueCode::DelayDirectoryInvalid, directory.rva,
                      "delay import directory size is not a non-zero multiple "
                      "of 32");
            return;
        }

        std::uint64_t directory_offset = 0;
        if (!file_offset_for_rva(directory.rva, directory.size,
                                directory_offset)) {
            return;
        }

        const std::uint32_t descriptor_count =
            directory.size / kDelayDescriptorSize;
        bool terminated = false;
        for (std::uint32_t index = 0; index < descriptor_count; ++index) {
            const std::uint64_t relative =
                static_cast<std::uint64_t>(index) * kDelayDescriptorSize;
            const std::uint64_t descriptor_rva =
                static_cast<std::uint64_t>(directory.rva) + relative;
            const std::uint64_t descriptor_offset =
                directory_offset + relative;

            std::array<std::uint32_t, 8> words{};
            bool readable = true;
            for (std::size_t word = 0; word < words.size(); ++word) {
                if (!view_.read_u32(descriptor_offset +
                                        static_cast<std::uint64_t>(word) * 4,
                                    words[word])) {
                    readable = false;
                    break;
                }
            }
            if (!readable) {
                add_error(IssueCode::DelayDescriptorInvalid, descriptor_rva,
                          "truncated ImgDelayDescr");
                return;
            }

            bool all_zero = true;
            for (const std::uint32_t word : words) {
                all_zero = all_zero && word == 0;
            }
            if (all_zero) {
                terminated = true;
                break;
            }

            const std::uint32_t attributes = words[0];
            if ((attributes & ~1U) != 0) {
                add_error(IssueCode::DelayDescriptorReservedBits,
                          descriptor_rva,
                          "ImgDelayDescr grAttrs has reserved bits set");
            }
            const bool rva_mode = (attributes & 1U) != 0;
            const DelayAddressMode mode =
                rva_mode ? DelayAddressMode::Rva : DelayAddressMode::LegacyVa;

            std::uint32_t name_rva = 0;
            std::uint32_t iat_rva = 0;
            std::uint32_t int_rva = 0;
            std::uint32_t bound_iat_rva = 0;
            std::uint32_t unload_iat_rva = 0;
            const bool name_converted = convert_descriptor_value(
                words[1], rva_mode, true, IssueCode::DelayNameInvalid,
                name_rva, "szName");
            const bool iat_converted = convert_descriptor_value(
                words[3], rva_mode, true, IssueCode::DelayIatInvalid,
                iat_rva, "pIAT");
            const bool int_converted = convert_descriptor_value(
                words[4], rva_mode, true, IssueCode::DelayIntInvalid,
                int_rva, "pINT");
            const bool bound_converted = convert_descriptor_value(
                words[5], rva_mode, false, IssueCode::DelayBoundIatInvalid,
                bound_iat_rva, "pBoundIAT");
            const bool unload_converted = convert_descriptor_value(
                words[6], rva_mode, false, IssueCode::DelayUnloadIatInvalid,
                unload_iat_rva, "pUnloadIAT");
            if (!iat_converted) {
                continue;
            }

            const bool iat_ok = validate_mapped_pointer(
                iat_rva, IssueCode::DelayIatInvalid, "pIAT");
            if (int_converted) {
                (void)validate_mapped_pointer(
                    int_rva, IssueCode::DelayIntInvalid, "pINT");
            }
            if (bound_converted && bound_iat_rva != 0) {
                (void)validate_mapped_pointer(
                    bound_iat_rva, IssueCode::DelayBoundIatInvalid,
                    "pBoundIAT");
            }
            if (unload_converted && unload_iat_rva != 0) {
                (void)validate_mapped_pointer(
                    unload_iat_rva, IssueCode::DelayUnloadIatInvalid,
                    "pUnloadIAT");
            }

            std::string dll_name;
            const bool name_read =
                name_converted && name_rva != 0 &&
                read_name_string(name_rva, dll_name);
            if (name_read && !is_safe_dll_name(dll_name)) {
                add_error(IssueCode::DelayNamePathInvalid, name_rva,
                          "DLL name is not a canonical bare .dll name");
            } else if (name_read && !is_allowed_dll(dll_name)) {
                add_error(IssueCode::DelayNameNotAllowed, name_rva,
                          "DLL name is not in the configured allowlist");
            }
            if (!iat_ok) {
                continue;
            }

            const Section* iat_section = section_for_rva(iat_rva);
            if (iat_section == nullptr) {
                add_error(IssueCode::DelayIatInvalid, iat_rva,
                          "pIAT is not mapped");
                continue;
            }

            DelayImportRecord record;
            record.descriptor_rva =
                static_cast<std::uint32_t>(descriptor_rva);
            record.address_mode = mode;
            record.dll_name = dll_name;
            record.name_rva = name_rva;
            record.iat_rva = iat_rva;
            record.int_rva = int_rva;
            record.bound_iat_rva = bound_iat_rva;
            record.unload_iat_rva = unload_iat_rva;
            record.iat_section_writable =
                (iat_section->characteristics & kScnMemWrite) != 0;
            report_.delay_imports.push_back(std::move(record));
        }

        if (!terminated) {
            add_error(IssueCode::DelayDescriptorUnterminated, directory.rva,
                      "delay import descriptor array has no zero terminator");
        }
    }

    static std::uint32_t relocation_width(std::uint16_t type) {
        switch (type) {
        case 0:
            return 0;
        case 1:
        case 2:
            return 2;
        case 3:
            return 4;
        case 10:
            return 8;
        default:
            return 0;
        }
    }

    bool relocation_is_supported(std::uint16_t type) const {
        switch (type) {
        case 0:
        case 1:
        case 2:
        case 3:
        case 10:
            return true;
        default:
            return false;
        }
    }

    void parse_relocations() {
        DataDirectory directory;
        if (!validate_directory(kDirBaseReloc,
                                IssueCode::RelocDirectoryInvalid, directory)) {
            return;
        }
        if (directory.rva % 4 != 0 || directory.size % 4 != 0) {
            add_error(IssueCode::RelocDirectoryUnaligned, directory.rva,
                      "relocation directory RVA/size is not 4-byte aligned");
            return;
        }
        if (directory.size < 8) {
            add_error(IssueCode::RelocDirectoryInvalid, directory.rva,
                      "relocation directory is shorter than one block header");
            return;
        }

        std::uint64_t directory_offset = 0;
        if (!file_offset_for_rva(directory.rva, directory.size,
                                directory_offset)) {
            return;
        }

        const std::uint64_t directory_end =
            static_cast<std::uint64_t>(directory.rva) + directory.size;
        std::uint64_t cursor = directory.rva;
        while (cursor < directory_end) {
            const std::uint64_t remaining = directory_end - cursor;
            if (remaining < 8) {
                add_error(IssueCode::RelocBlockOutOfBounds, cursor,
                          "trailing bytes cannot form an "
                          "IMAGE_BASE_RELOCATION header");
                break;
            }
            const std::uint64_t block_offset =
                directory_offset + (cursor - directory.rva);
            std::uint32_t block_rva = 0;
            std::uint32_t block_size = 0;
            if (!view_.read_u32(block_offset, block_rva) ||
                !view_.read_u32(block_offset + 4, block_size)) {
                add_error(IssueCode::RelocBlockOutOfBounds, cursor,
                          "truncated IMAGE_BASE_RELOCATION header");
                break;
            }
            if (block_size < 8) {
                add_error(IssueCode::RelocBlockTooSmall, cursor,
                          "SizeOfBlock is smaller than 8");
                break;
            }
            if (block_size % 4 != 0) {
                add_error(IssueCode::RelocBlockUnaligned, cursor,
                          "SizeOfBlock is not 4-byte aligned");
                break;
            }
            if (static_cast<std::uint64_t>(block_size) > remaining) {
                add_error(IssueCode::RelocBlockOutOfBounds, cursor,
                          "SizeOfBlock crosses the relocation directory");
                break;
            }
            if (static_cast<std::uint64_t>(cursor) + block_size >
                directory_end) {
                add_error(IssueCode::RelocArithmeticOverflow, cursor,
                          "block end wraps the relocation directory");
                break;
            }

            const std::uint32_t entry_count = (block_size - 8) / 2;
            for (std::uint32_t entry_index = 0; entry_index < entry_count;
                 ++entry_index) {
                const std::uint64_t relative =
                    8 + static_cast<std::uint64_t>(entry_index) * 2;
                std::uint16_t entry = 0;
                if (!view_.read_u16(block_offset + relative, entry)) {
                    add_error(IssueCode::RelocBlockOutOfBounds, cursor,
                              "truncated relocation entry");
                    break;
                }
                const std::uint16_t type =
                    static_cast<std::uint16_t>(entry >> 12);
                const std::uint16_t page_offset =
                    static_cast<std::uint16_t>(entry & 0x0FFFU);
                if (!relocation_is_supported(type)) {
                    const std::uint64_t target =
                        static_cast<std::uint64_t>(block_rva) + page_offset;
                    add_error(IssueCode::RelocTypeUnsupported, target,
                              "unsupported or high-risk relocation type " +
                                  hex_value(type));
                    continue;
                }
                const std::uint32_t width = relocation_width(type);
                if (width == 0) {
                    continue;
                }

                const std::uint64_t target64 =
                    static_cast<std::uint64_t>(block_rva) + page_offset;
                if (target64 > std::numeric_limits<std::uint32_t>::max() ||
                    target64 + width >
                        std::numeric_limits<std::uint32_t>::max()) {
                    add_error(IssueCode::RelocArithmeticOverflow, target64,
                              "target RVA plus write width overflows 32 bits");
                    continue;
                }
                const std::uint32_t target =
                    static_cast<std::uint32_t>(target64);
                const std::uint64_t target_end =
                    static_cast<std::uint64_t>(target) + width;

                if (target < size_of_headers_) {
                    add_error(IssueCode::RelocTargetInHeaders, target,
                              "relocation writes into PE headers");
                }
                if (target_end > size_of_image_) {
                    add_error(IssueCode::RelocTargetOutsideImage, target,
                              "relocation write exceeds SizeOfImage");
                }
                if (section_for_range(target, width) == nullptr) {
                    add_error(IssueCode::RelocTargetOutsideSection, target,
                              "relocation write is not fully inside one "
                              "mapped section");
                }
                if (has_import_directory_ &&
                    ranges_overlap(target, width,
                                   import_directory_.rva,
                                   import_directory_.size)) {
                    add_error(IssueCode::RelocTargetInImportDirectory, target,
                              "relocation writes into the import directory");
                }
                if (has_delay_import_directory_ &&
                    ranges_overlap(target, width,
                                   delay_import_directory_.rva,
                                   delay_import_directory_.size)) {
                    add_error(IssueCode::RelocTargetInImportDirectory, target,
                              "relocation writes into the delay import "
                              "directory");
                }

                RelocationRecord record;
                record.block_rva = block_rva;
                record.type = type;
                record.page_offset = page_offset;
                record.target_rva = target;
                record.width = static_cast<std::uint8_t>(width);
                report_.relocations.push_back(record);
            }
            cursor += block_size;
        }

        std::sort(report_.relocations.begin(), report_.relocations.end(),
                  [](const RelocationRecord& left,
                     const RelocationRecord& right) {
                      if (left.target_rva != right.target_rva) {
                          return left.target_rva < right.target_rva;
                      }
                      return left.width < right.width;
                  });

        std::uint64_t previous_end = 0;
        std::uint32_t previous_target = 0;
        bool have_previous = false;
        for (const RelocationRecord& record : report_.relocations) {
            const std::uint64_t end =
                static_cast<std::uint64_t>(record.target_rva) + record.width;
            if (have_previous &&
                static_cast<std::uint64_t>(record.target_rva) < previous_end) {
                add_error(IssueCode::RelocWriteOverlap, record.target_rva,
                          "relocation write overlaps prior write at " +
                              hex_value(previous_target));
            }
            if (!have_previous || end > previous_end) {
                previous_end = end;
                previous_target = record.target_rva;
                have_previous = true;
            }
        }
    }

    void evaluate_delay_import_guards() {
        const std::uint32_t flags =
            report_.load_config.guard_flags_available
                ? report_.load_config.guard_flags
                : 0;
        report_.guard_cf_present =
            (flags & kGuardCfInstrumented) != 0;
        report_.delay_iat_protected =
            (flags & kGuardProtectDelayLoadIat) != 0;

        if (report_.delay_imports.empty()) {
            return;
        }

        for (const DelayImportRecord& import : report_.delay_imports) {
            if (import.iat_section_writable) {
                report_.delay_iat_writable = true;
                break;
            }
        }

        if (!report_.guard_cf_present) {
            add_error(IssueCode::GuardCfMissing, 0,
                      "delay imports exist but load config lacks "
                      "IMAGE_GUARD_CF_INSTRUMENTED");
        }
        if (report_.delay_iat_writable && !report_.delay_iat_protected) {
            add_error(IssueCode::DelayIatProtectionMissing, 0,
                      "writable Delay-Load IAT is not protected by "
                      "IMAGE_GUARD_PROTECT_DELAYLOAD_IAT");
        }
    }

    ByteView view_;
    const AuditOptions& options_;
    Report& report_;
    std::uint64_t image_base_ = 0;
    std::uint32_t section_alignment_ = 0;
    std::uint32_t size_of_image_ = 0;
    std::uint32_t size_of_headers_ = 0;
    std::uint32_t directory_count_ = 0;
    std::uint32_t dirs_available_ = 0;
    std::array<DataDirectory, 16> directories_{};
    std::vector<Section> sections_;
    DataDirectory import_directory_{};
    bool has_import_directory_ = false;
    DataDirectory delay_import_directory_{};
    bool has_delay_import_directory_ = false;
};

}  // namespace

const std::vector<std::string>& default_dll_allowlist() {
    static const std::vector<std::string> kAllowlist = {
        "kernel32.dll",   "user32.dll",   "gdi32.dll",
        "advapi32.dll",   "shell32.dll",  "ole32.dll",
        "oleaut32.dll",   "comctl32.dll", "comdlg32.dll",
        "ws2_32.dll",     "crypt32.dll",  "bcrypt.dll",
        "ncrypt.dll",     "secur32.dll",  "shlwapi.dll",
        "version.dll",    "winmm.dll",    "wininet.dll",
        "winhttp.dll",    "urlmon.dll",   "dwmapi.dll",
        "uxtheme.dll",    "psapi.dll",    "dbghelp.dll",
        "iphlpapi.dll",   "dnsapi.dll",   "mswsock.dll",
        "ntdll.dll",      "setupapi.dll", "cfgmgr32.dll",
        "powrprof.dll",   "propsys.dll",  "wtsapi32.dll",
        "userenv.dll",    "d3d9.dll",     "d3d11.dll",
        "d3d12.dll",      "dxgi.dll",     "opengl32.dll",
        "imm32.dll",      "msvcrt.dll",   "vcruntime140.dll",
    };
    return kAllowlist;
}

const char* issue_code_name(IssueCode code) noexcept {
    switch (code) {
    case IssueCode::FileReadError:
        return "FileReadError";
    case IssueCode::FileTooSmall:
        return "FileTooSmall";
    case IssueCode::InvalidDosHeader:
        return "InvalidDosHeader";
    case IssueCode::InvalidPeSignature:
        return "InvalidPeSignature";
    case IssueCode::InvalidCoffHeader:
        return "InvalidCoffHeader";
    case IssueCode::InvalidOptionalHeader:
        return "InvalidOptionalHeader";
    case IssueCode::NotPe32Plus:
        return "NotPe32Plus";
    case IssueCode::InvalidDataDirectory:
        return "InvalidDataDirectory";
    case IssueCode::InvalidSectionTable:
        return "InvalidSectionTable";
    case IssueCode::InvalidSectionLayout:
        return "InvalidSectionLayout";
    case IssueCode::OverlappingSections:
        return "OverlappingSections";
    case IssueCode::ImportDirectoryInvalid:
        return "ImportDirectoryInvalid";
    case IssueCode::RelocDirectoryInvalid:
        return "RelocDirectoryInvalid";
    case IssueCode::RelocDirectoryUnaligned:
        return "RelocDirectoryUnaligned";
    case IssueCode::RelocBlockTooSmall:
        return "RelocBlockTooSmall";
    case IssueCode::RelocBlockUnaligned:
        return "RelocBlockUnaligned";
    case IssueCode::RelocBlockOutOfBounds:
        return "RelocBlockOutOfBounds";
    case IssueCode::RelocArithmeticOverflow:
        return "RelocArithmeticOverflow";
    case IssueCode::RelocTypeUnsupported:
        return "RelocTypeUnsupported";
    case IssueCode::RelocTargetOutsideImage:
        return "RelocTargetOutsideImage";
    case IssueCode::RelocTargetOutsideSection:
        return "RelocTargetOutsideSection";
    case IssueCode::RelocTargetInHeaders:
        return "RelocTargetInHeaders";
    case IssueCode::RelocTargetInImportDirectory:
        return "RelocTargetInImportDirectory";
    case IssueCode::RelocWriteOverlap:
        return "RelocWriteOverlap";
    case IssueCode::DelayDirectoryInvalid:
        return "DelayDirectoryInvalid";
    case IssueCode::DelayDescriptorInvalid:
        return "DelayDescriptorInvalid";
    case IssueCode::DelayDescriptorUnterminated:
        return "DelayDescriptorUnterminated";
    case IssueCode::DelayDescriptorReservedBits:
        return "DelayDescriptorReservedBits";
    case IssueCode::DelayLegacyVaInvalid:
        return "DelayLegacyVaInvalid";
    case IssueCode::DelayNameInvalid:
        return "DelayNameInvalid";
    case IssueCode::DelayNameUnterminated:
        return "DelayNameUnterminated";
    case IssueCode::DelayNamePathInvalid:
        return "DelayNamePathInvalid";
    case IssueCode::DelayNameNotAllowed:
        return "DelayNameNotAllowed";
    case IssueCode::DelayIatInvalid:
        return "DelayIatInvalid";
    case IssueCode::DelayIntInvalid:
        return "DelayIntInvalid";
    case IssueCode::DelayBoundIatInvalid:
        return "DelayBoundIatInvalid";
    case IssueCode::DelayUnloadIatInvalid:
        return "DelayUnloadIatInvalid";
    case IssueCode::LoadConfigInvalid:
        return "LoadConfigInvalid";
    case IssueCode::LoadConfigTruncated:
        return "LoadConfigTruncated";
    case IssueCode::GuardCfMissing:
        return "GuardCfMissing";
    case IssueCode::DelayIatProtectionMissing:
        return "DelayIatProtectionMissing";
    }
    return "Unknown";
}

const char* severity_name(Severity severity) noexcept {
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

bool Report::ok() const noexcept {
    for (const Finding& finding : findings) {
        if (finding.severity == Severity::Error) {
            return false;
        }
    }
    return true;
}

bool Report::contains(IssueCode code) const noexcept {
    for (const Finding& finding : findings) {
        if (finding.code == code) {
            return true;
        }
    }
    return false;
}

Report audit_bytes(const std::uint8_t* data, std::size_t size,
                   const AuditOptions& options) {
    Report report;
    if (data == nullptr && size != 0) {
        Finding finding;
        finding.code = IssueCode::FileReadError;
        finding.severity = Severity::Error;
        finding.detail = "null data pointer with non-zero size";
        report.findings.push_back(std::move(finding));
        return report;
    }
    if (size < 64) {
        Finding finding;
        finding.code = IssueCode::FileTooSmall;
        finding.severity = Severity::Error;
        finding.detail = "file is too small for a DOS header";
        report.findings.push_back(std::move(finding));
        return report;
    }
    Parser parser(data, size, options, report);
    parser.run();
    return report;
}

Report audit_bytes(const std::vector<std::uint8_t>& data,
                   const AuditOptions& options) {
    return audit_bytes(data.data(), data.size(), options);
}

Report audit_file(const std::string& path, const AuditOptions& options) {
    std::ifstream input(path, std::ios::binary | std::ios::ate);
    if (!input) {
        Report report;
        Finding finding;
        finding.code = IssueCode::FileReadError;
        finding.severity = Severity::Error;
        finding.detail = "cannot open input file";
        report.findings.push_back(std::move(finding));
        return report;
    }
    const std::streamoff end = input.tellg();
    if (end < 0) {
        Report report;
        Finding finding;
        finding.code = IssueCode::FileReadError;
        finding.severity = Severity::Error;
        finding.detail = "cannot determine input file size";
        report.findings.push_back(std::move(finding));
        return report;
    }
    const std::uint64_t file_size = static_cast<std::uint64_t>(end);
    if (file_size > static_cast<std::uint64_t>(
                        std::numeric_limits<std::size_t>::max())) {
        Report report;
        Finding finding;
        finding.code = IssueCode::FileReadError;
        finding.severity = Severity::Error;
        finding.detail = "input file is too large for this process";
        report.findings.push_back(std::move(finding));
        return report;
    }
    std::vector<std::uint8_t> bytes(static_cast<std::size_t>(file_size));
    input.seekg(0, std::ios::beg);
    if (!bytes.empty() &&
        !input.read(reinterpret_cast<char*>(bytes.data()),
                    static_cast<std::streamsize>(bytes.size()))) {
        Report report;
        Finding finding;
        finding.code = IssueCode::FileReadError;
        finding.severity = Severity::Error;
        finding.detail = "short read from input file";
        report.findings.push_back(std::move(finding));
        return report;
    }
    return audit_bytes(bytes, options);
}

}  // namespace pe_audit
