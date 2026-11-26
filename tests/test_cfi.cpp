#include "dwarf/cfi.hpp"

#include <cstdint>
#include <cstring>
#include <iostream>
#include <limits>
#include <sstream>
#include <string>
#include <type_traits>
#include <vector>

namespace {

using dwarf::cfi::CfiError;
using dwarf::cfi::ErrorCode;
using dwarf::cfi::RegisterRuleKind;

int failures = 0;
const char* current_test = "<none>";

void report_failure(const char* file, int line, const std::string& message) {
    std::cerr << file << ':' << line << " [" << current_test
              << "]: FAIL: " << message << '\n';
    ++failures;
}

template <typename T>
std::string describe(const T& value) {
    if constexpr (requires(std::ostream& out, const T& item) { out << item; }) {
        std::ostringstream out;
        out << value;
        return out.str();
    } else if constexpr (requires(const T& item) {
                             item.begin();
                             item.end();
                         }) {
        std::ostringstream out;
        out << '[';
        bool first = true;
        for (const auto& item : value) {
            if (!first) {
                out << ", ";
            }
            first = false;
            out << describe(item);
        }
        out << ']';
        return out.str();
    } else {
        return "<unprintable>";
    }
}

#define CHECK(condition)                                                        \
    do {                                                                        \
        if (!(condition)) {                                                     \
            report_failure(__FILE__, __LINE__, "CHECK(" #condition ")");        \
        }                                                                       \
    } while (false)

#define CHECK_EQ(actual, expected)                                              \
    do {                                                                        \
        const auto actual_value = (actual);                                     \
        const auto expected_value = (expected);                                 \
        if (!(actual_value == expected_value)) {                                \
            report_failure(__FILE__, __LINE__,                                  \
                           "CHECK_EQ(" #actual ", " #expected ") got " +        \
                               describe(actual_value) + ", expected " +         \
                               describe(expected_value));                       \
        }                                                                       \
    } while (false)

template <typename Function>
void expect_error(ErrorCode expected, Function&& function) {
    try {
        function();
        report_failure(__FILE__, __LINE__, "expected CfiError was not thrown");
    } catch (const CfiError& error) {
        if (error.code() != expected) {
            report_failure(
                __FILE__, __LINE__,
                std::string("wrong error: ") + dwarf::cfi::error_code_name(error.code()) +
                    ", expected " + dwarf::cfi::error_code_name(expected));
        }
    } catch (const std::exception& error) {
        report_failure(__FILE__, __LINE__,
                       std::string("wrong exception type: ") + error.what());
    }
}

struct Bytes {
    std::vector<std::uint8_t> data;

    void u8(std::uint8_t value) { data.push_back(value); }
    void u16(std::uint16_t value) {
        data.push_back(static_cast<std::uint8_t>(value));
        data.push_back(static_cast<std::uint8_t>(value >> 8U));
    }
    void u32(std::uint32_t value) {
        for (unsigned i = 0; i < 4; ++i) {
            data.push_back(static_cast<std::uint8_t>(value >> (8U * i)));
        }
    }
    void u64(std::uint64_t value) {
        for (unsigned i = 0; i < 8; ++i) {
            data.push_back(static_cast<std::uint8_t>(value >> (8U * i)));
        }
    }
    void uleb(std::uint64_t value) {
        const auto encoded = dwarf::cfi::encode_uleb128(value);
        data.insert(data.end(), encoded.begin(), encoded.end());
    }
    void sleb(std::int64_t value) {
        const auto encoded = dwarf::cfi::encode_sleb128(value);
        data.insert(data.end(), encoded.begin(), encoded.end());
    }
    void cstr(const std::string& value) {
        data.insert(data.end(), value.begin(), value.end());
        data.push_back(0);
    }
    void append(const Bytes& bytes) {
        data.insert(data.end(), bytes.data.begin(), bytes.data.end());
    }
    void append(const std::vector<std::uint8_t>& bytes) {
        data.insert(data.end(), bytes.begin(), bytes.end());
    }
};

Bytes add_entry(const Bytes& content) {
    Bytes entry;
    entry.u32(static_cast<std::uint32_t>(content.data.size()));
    entry.append(content);
    return entry;
}

struct CieSpec {
    std::uint8_t version = 5;
    std::string augmentation;
    std::uint64_t code_alignment = 1;
    std::int64_t data_alignment = -8;
    std::uint64_t return_register = 16;
    std::vector<std::uint8_t> augmentation_data;
    std::vector<std::uint8_t> initial_instructions;
};

Bytes build_cie(dwarf::cfi::SectionKind kind, const CieSpec& spec) {
    Bytes content;
    if (kind == dwarf::cfi::SectionKind::eh_frame) {
        content.u32(0);
    } else {
        content.u32(0xffffffffU);
    }
    content.u8(spec.version);
    content.cstr(spec.augmentation);
    content.uleb(spec.code_alignment);
    content.sleb(spec.data_alignment);
    content.uleb(spec.return_register);
    if (!spec.augmentation.empty() && spec.augmentation[0] == 'z') {
        content.uleb(spec.augmentation_data.size());
        content.append(spec.augmentation_data);
    } else if (!spec.augmentation_data.empty()) {
        content.append(spec.augmentation_data);
    }
    content.append(spec.initial_instructions);
    return add_entry(content);
}

Bytes build_fde_absptr(std::uint64_t initial_location,
                       std::uint64_t address_range,
                       const std::vector<std::uint8_t>& instructions,
                       std::uint32_t cie_pointer = 0) {
    Bytes content;
    content.u32(cie_pointer);
    content.u64(initial_location);
    content.u64(address_range);
    content.append(instructions);
    return add_entry(content);
}

void append_advance(Bytes& bytes, unsigned delta) {
    if (delta == 0 || delta > 0x3f) {
        throw std::runtime_error("invalid compact advance delta");
    }
    bytes.u8(static_cast<std::uint8_t>(0x40U | delta));
}

void append_offset(Bytes& bytes, unsigned reg, std::uint64_t offset) {
    if (reg > 0x3f) {
        throw std::runtime_error("invalid compact offset register");
    }
    bytes.u8(static_cast<std::uint8_t>(0x80U | reg));
    bytes.uleb(offset);
}

void append_def_cfa_offset(Bytes& bytes, std::uint64_t offset) {
    bytes.u8(dwarf::cfi::DW_CFA_def_cfa_offset);
    bytes.uleb(offset);
}

void append_restore(Bytes& bytes, unsigned reg) {
    bytes.u8(static_cast<std::uint8_t>(0xc0U | reg));
}

void check_cfa(const dwarf::cfi::FrameRow& row,
               std::uint64_t reg,
               std::int64_t offset) {
    CHECK_EQ(static_cast<int>(row.cfa.kind),
             static_cast<int>(dwarf::cfi::CfaRuleKind::reg_offset));
    CHECK_EQ(row.cfa.reg, reg);
    CHECK_EQ(row.cfa.offset, offset);
}

void check_offset_rule(const dwarf::cfi::FrameRow& row,
                       std::uint64_t reg,
                       std::int64_t offset) {
    const dwarf::cfi::RegisterRule* rule = row.find_register(reg);
    CHECK(rule != nullptr);
    if (rule != nullptr) {
        CHECK_EQ(static_cast<int>(rule->kind),
                 static_cast<int>(RegisterRuleKind::offset));
        CHECK_EQ(rule->offset, offset);
    }
}

void check_absent(const dwarf::cfi::FrameRow& row, std::uint64_t reg) {
    CHECK(row.find_register(reg) == nullptr);
}

void test_uleb128() {
    using dwarf::cfi::decode_uleb128;

    CHECK_EQ(decode_uleb128(std::vector<std::uint8_t>{0x00}).value, 0U);
    CHECK_EQ(decode_uleb128(std::vector<std::uint8_t>{0x7f}).value, 127U);
    CHECK_EQ(decode_uleb128(std::vector<std::uint8_t>{0x80, 0x01}).value, 128U);
    CHECK_EQ(decode_uleb128(std::vector<std::uint8_t>{0xe5, 0x8e, 0x26}).value,
             624485U);
    CHECK_EQ(decode_uleb128(std::vector<std::uint8_t>{0xe5, 0x8e, 0x26}).length,
             3U);

    const std::vector<std::uint8_t> max_value{
        0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01};
    CHECK_EQ(decode_uleb128(max_value).value,
             std::numeric_limits<std::uint64_t>::max());
    CHECK_EQ(decode_uleb128(max_value).length, 10U);

    expect_error(ErrorCode::leb128_overflow, [] {
        const std::vector<std::uint8_t> bytes{
            0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02};
        (void)dwarf::cfi::decode_uleb128(bytes);
    });
    expect_error(ErrorCode::leb128_too_long, [] {
        const std::vector<std::uint8_t> bytes{
            0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00};
        (void)dwarf::cfi::decode_uleb128(bytes);
    });
    expect_error(ErrorCode::truncated_input, [] {
        const std::vector<std::uint8_t> bytes{0x80};
        (void)dwarf::cfi::decode_uleb128(bytes);
    });
    expect_error(ErrorCode::truncated_input, [] {
        (void)dwarf::cfi::decode_uleb128(
            std::span<const std::uint8_t>{});
    });

    std::uint64_t value = 0;
    std::size_t consumed = 0;
    ErrorCode error = ErrorCode::none;
    CHECK(!dwarf::cfi::try_decode_uleb128(
        std::vector<std::uint8_t>{0x80}, value, consumed, &error));
    CHECK_EQ(static_cast<int>(error), static_cast<int>(ErrorCode::truncated_input));
}

void test_sleb128() {
    using dwarf::cfi::decode_sleb128;

    CHECK_EQ(decode_sleb128(std::vector<std::uint8_t>{0x00}).value, 0);
    CHECK_EQ(decode_sleb128(std::vector<std::uint8_t>{0x7f}).value, -1);
    CHECK_EQ(decode_sleb128(std::vector<std::uint8_t>{0x3f}).value, 63);
    CHECK_EQ(decode_sleb128(std::vector<std::uint8_t>{0x40}).value, -64);
    CHECK_EQ(decode_sleb128(std::vector<std::uint8_t>{0xbf, 0x7f}).value, -65);
    CHECK_EQ(decode_sleb128(std::vector<std::uint8_t>{0x80, 0x7f}).value, -128);
    CHECK_EQ(decode_sleb128(std::vector<std::uint8_t>{0x80, 0x01}).value, 128);
    CHECK_EQ(decode_sleb128(std::vector<std::uint8_t>{0xc0, 0x00}).value, 64);

    const auto min_encoding =
        dwarf::cfi::encode_sleb128(std::numeric_limits<std::int64_t>::min());
    const auto max_encoding =
        dwarf::cfi::encode_sleb128(std::numeric_limits<std::int64_t>::max());
    CHECK_EQ(decode_sleb128(min_encoding).value,
             std::numeric_limits<std::int64_t>::min());
    CHECK_EQ(decode_sleb128(max_encoding).value,
             std::numeric_limits<std::int64_t>::max());
    CHECK_EQ(min_encoding.size(), 10U);
    CHECK_EQ(max_encoding.size(), 10U);

    for (const std::int64_t sample :
         {std::int64_t{-123456789}, std::int64_t{-1}, std::int64_t{0},
          std::int64_t{1}, std::int64_t{123456789},
          std::numeric_limits<std::int64_t>::min() + 1,
          std::numeric_limits<std::int64_t>::max() - 1}) {
        const auto encoded = dwarf::cfi::encode_sleb128(sample);
        CHECK_EQ(decode_sleb128(encoded).value, sample);
        CHECK_EQ(decode_sleb128(encoded).length, encoded.size());
    }

    expect_error(ErrorCode::leb128_overflow, [] {
        const std::vector<std::uint8_t> bytes{
            0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01};
        (void)dwarf::cfi::decode_sleb128(bytes);
    });
    expect_error(ErrorCode::leb128_overflow, [] {
        const std::vector<std::uint8_t> bytes{
            0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7e};
        (void)dwarf::cfi::decode_sleb128(bytes);
    });
    expect_error(ErrorCode::leb128_too_long, [] {
        const std::vector<std::uint8_t> bytes{
            0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00};
        (void)dwarf::cfi::decode_sleb128(bytes);
    });
    expect_error(ErrorCode::truncated_input, [] {
        const std::vector<std::uint8_t> bytes{0xff};
        (void)dwarf::cfi::decode_sleb128(bytes);
    });

    const std::vector<std::uint8_t> min_bytes{
        0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7f};
    CHECK_EQ(decode_sleb128(min_bytes).value,
             std::numeric_limits<std::int64_t>::min());
}

void test_cie_parse() {
    CieSpec spec;
    spec.augmentation = "zR";
    spec.code_alignment = 4;
    spec.data_alignment = -8;
    spec.return_register = 30;
    spec.augmentation_data = {0x1b};
    spec.initial_instructions = {0x0c, 0x07, 0x08};

    const Bytes cie_bytes =
        build_cie(dwarf::cfi::SectionKind::eh_frame, spec);
    const dwarf::cfi::Cie cie = dwarf::cfi::parse_cie(cie_bytes.data);

    CHECK_EQ(static_cast<int>(cie.kind),
             static_cast<int>(dwarf::cfi::SectionKind::eh_frame));
    CHECK_EQ(cie.version, 5);
    CHECK_EQ(cie.augmentation, std::string("zR"));
    CHECK_EQ(cie.code_alignment_factor, 4U);
    CHECK_EQ(cie.data_alignment_factor, -8);
    CHECK_EQ(cie.return_address_register, 30U);
    CHECK(cie.fde_encoding.has_value());
    CHECK_EQ(*cie.fde_encoding, 0x1b);
    CHECK_EQ(cie.initial_instructions, spec.initial_instructions);
    CHECK_EQ(cie.initial_instructions_offset, cie_bytes.data.size() - spec.initial_instructions.size());

    expect_error(ErrorCode::unsupported_version, [] {
        CieSpec bad;
        bad.version = 2;
        const Bytes bytes =
            build_cie(dwarf::cfi::SectionKind::eh_frame, bad);
        (void)dwarf::cfi::parse_cie(bytes.data);
    });
    expect_error(ErrorCode::invalid_cie_id, [] {
        const std::vector<std::uint8_t> bytes{0x04, 0x00, 0x00, 0x00,
                                              0x11, 0x22, 0x33, 0x44};
        (void)dwarf::cfi::parse_cie(bytes);
    });
    expect_error(ErrorCode::truncated_input, [] {
        CieSpec bad;
        Bytes content;
        content.u32(0);
        content.u8(5);
        content.cstr("");
        content.u8(0x80);  // truncated ULEB128 for code_alignment_factor
        (void)dwarf::cfi::parse_cie(add_entry(content).data);
    });
    expect_error(ErrorCode::leb128_overflow, [] {
        Bytes content;
        content.u32(0);
        content.u8(5);
        content.cstr("");
        const std::vector<std::uint8_t> bad{
            0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02};
        content.append(bad);
        (void)dwarf::cfi::parse_cie(add_entry(content).data);
    });
}

void test_debug_frame_cie_and_fde() {
    CieSpec spec;
    spec.code_alignment = 1;
    spec.data_alignment = -8;
    spec.return_register = 16;
    spec.initial_instructions = {0x0c, 0x07, 0x08};
    const Bytes cie_bytes =
        build_cie(dwarf::cfi::SectionKind::debug_frame, spec);
    const dwarf::cfi::Cie cie = dwarf::cfi::parse_cie(cie_bytes.data);
    CHECK_EQ(static_cast<int>(cie.kind),
             static_cast<int>(dwarf::cfi::SectionKind::debug_frame));

    const std::vector<std::uint8_t> instructions = {0x41, 0x0e, 0x10};
    const Bytes fde_bytes = build_fde_absptr(0x1234, 0x20, instructions);
    dwarf::cfi::ParseOptions options;
    options.kind = dwarf::cfi::SectionKind::debug_frame;
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie, options);
    CHECK_EQ(fde.initial_location, 0x1234U);
    CHECK_EQ(fde.address_range, 0x20U);
    CHECK_EQ(fde.instructions, instructions);

    const dwarf::cfi::FrameRow row = dwarf::cfi::evaluate_frame(cie, fde, 0x1234);
    check_cfa(row, 7, 8);
}

void test_cie_augmentation_and_signal_frame() {
    CieSpec spec;
    spec.augmentation = "zPSR";
    spec.augmentation_data = {
        0x00,  // P: DW_EH_PE_absptr
        0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
        0x00,  // R: DW_EH_PE_absptr
    };
    spec.initial_instructions = {0x0c, 0x07, 0x08};
    const Bytes cie_bytes =
        build_cie(dwarf::cfi::SectionKind::eh_frame, spec);
    const dwarf::cfi::Cie cie = dwarf::cfi::parse_cie(cie_bytes.data);
    CHECK(cie.signal_frame);
    CHECK(cie.personality_encoding.has_value());
    CHECK_EQ(*cie.personality_encoding, 0x00);
    CHECK_EQ(cie.personality, 0x8877665544332211ULL);
    CHECK(cie.fde_encoding.has_value());
    CHECK_EQ(*cie.fde_encoding, 0x00);
}

void test_pcrel_fde() {
    CieSpec spec;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x1b};  // DW_EH_PE_pcrel | DW_EH_PE_sdata4
    spec.initial_instructions = {0x0c, 0x07, 0x08};
    const Bytes cie_bytes =
        build_cie(dwarf::cfi::SectionKind::eh_frame, spec);
    const dwarf::cfi::Cie cie = dwarf::cfi::parse_cie(cie_bytes.data);

    constexpr std::uint64_t section_address = 0x400000;
    constexpr std::uint64_t target = 0x401234;
    constexpr std::uint64_t range = 0x40;
    const std::uint64_t field_position = 4 + 4;  // length + CIE pointer field
    const std::int32_t displacement = static_cast<std::int32_t>(
        target - (section_address + field_position));

    Bytes content;
    content.u32(0);
    content.u32(static_cast<std::uint32_t>(displacement));
    content.u32(static_cast<std::uint32_t>(range));
    content.u8(dwarf::cfi::DW_CFA_nop);
    const Bytes fde_bytes = add_entry(content);

    dwarf::cfi::ParseOptions options;
    options.section_address = section_address;
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie, options);
    CHECK_EQ(fde.initial_location, target);
    CHECK_EQ(fde.address_range, range);

    const dwarf::cfi::FrameRow row =
        dwarf::cfi::evaluate_frame(cie, fde, target + 1);
    check_cfa(row, 7, 8);
}

void test_section_parse() {
    CieSpec spec;
    spec.code_alignment = 1;
    spec.data_alignment = -8;
    spec.return_register = 16;
    spec.initial_instructions = {0x0c, 0x07, 0x08};
    const Bytes cie_bytes =
        build_cie(dwarf::cfi::SectionKind::eh_frame, spec);

    Bytes fde_content;
    const std::uint32_t cie_pointer =
        static_cast<std::uint32_t>(cie_bytes.data.size() + 4);
    fde_content.u32(cie_pointer);
    fde_content.u64(0x9000);
    fde_content.u64(0x100);
    append_advance(fde_content, 1);
    append_def_cfa_offset(fde_content, 16);
    const Bytes fde_bytes = add_entry(fde_content);

    Bytes section;
    section.append(cie_bytes);
    section.append(fde_bytes);

    const dwarf::cfi::CfiSection parsed =
        dwarf::cfi::parse_cfi_section(section.data);
    CHECK_EQ(parsed.cies.size(), 1U);
    CHECK_EQ(parsed.fdes.size(), 1U);
    CHECK_EQ(parsed.fdes[0].cie_offset, 0U);
    CHECK_EQ(parsed.fdes[0].initial_location, 0x9000U);
    CHECK(parsed.find_fde(0x9000) == &parsed.fdes[0]);
    CHECK(parsed.find_fde(0x90ff) == &parsed.fdes[0]);
    CHECK(parsed.find_fde(0x9100) == nullptr);

    const dwarf::cfi::FrameRow row = parsed.lookup(0x9001);
    check_cfa(row, 7, 16);
    expect_error(ErrorCode::pc_out_of_range, [&] {
        (void)parsed.lookup(0x9100);
    });
}

void test_debug_frame_section() {
    CieSpec spec;
    spec.initial_instructions = {0x0c, 0x07, 0x08};
    const Bytes cie_bytes =
        build_cie(dwarf::cfi::SectionKind::debug_frame, spec);

    Bytes fde_content;
    fde_content.u32(0);  // absolute CIE offset in .debug_frame
    fde_content.u64(0x2000);
    fde_content.u64(0x80);
    fde_content.u8(dwarf::cfi::DW_CFA_nop);
    const Bytes fde_bytes = add_entry(fde_content);

    Bytes section;
    section.append(cie_bytes);
    section.append(fde_bytes);
    dwarf::cfi::ParseOptions options;
    options.kind = dwarf::cfi::SectionKind::debug_frame;
    const dwarf::cfi::CfiSection parsed =
        dwarf::cfi::parse_cfi_section(section.data, options);
    CHECK_EQ(parsed.cies.size(), 1U);
    CHECK_EQ(parsed.fdes.size(), 1U);
    CHECK_EQ(parsed.fdes[0].cie_offset, 0U);
    const dwarf::cfi::FrameRow row = parsed.lookup(0x2000);
    check_cfa(row, 7, 8);
}

void test_prologue_and_rows() {
    CieSpec spec;
    spec.code_alignment = 1;
    spec.data_alignment = -8;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x00};
    spec.initial_instructions = {
        0x0c, 0x07, 0x08,              // def_cfa r7, 8
        0x90, 0x01,                    // offset r16, 1 -> -8
        0x86, 0x02,                    // offset r6, 2 -> -16
    };
    const Bytes cie_bytes =
        build_cie(dwarf::cfi::SectionKind::eh_frame, spec);
    const dwarf::cfi::Cie cie = dwarf::cfi::parse_cie(cie_bytes.data);

    Bytes instructions;
    append_advance(instructions, 1);
    append_def_cfa_offset(instructions, 16);
    append_offset(instructions, 5, 3);  // -24
    append_advance(instructions, 1);
    append_def_cfa_offset(instructions, 24);
    append_offset(instructions, 6, 4);  // -32
    instructions.u8(dwarf::cfi::DW_CFA_advance_loc1);
    instructions.u8(2);
    instructions.u8(dwarf::cfi::DW_CFA_def_cfa_register);
    instructions.uleb(5);

    const Bytes fde_bytes = build_fde_absptr(0x1000, 0x100, instructions.data);
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie);
    const dwarf::cfi::FrameTable table(cie, fde);

    const dwarf::cfi::FrameRow row0 = table.lookup(0x1000);
    check_cfa(row0, 7, 8);
    check_offset_rule(row0, 16, -8);
    check_offset_rule(row0, 6, -16);
    check_absent(row0, 5);

    const dwarf::cfi::FrameRow row1 = table.lookup(0x1001);
    check_cfa(row1, 7, 16);
    check_offset_rule(row1, 5, -24);
    check_offset_rule(row1, 6, -16);

    const dwarf::cfi::FrameRow row2 = table.lookup(0x1003);
    check_cfa(row2, 7, 24);
    check_offset_rule(row2, 6, -32);
    check_offset_rule(row2, 5, -24);

    const dwarf::cfi::FrameRow row3 = table.lookup(0x1004);
    check_cfa(row3, 5, 24);
    check_offset_rule(row3, 6, -32);

    const std::vector<dwarf::cfi::FrameRow> rows = table.rows();
    CHECK_EQ(rows.size(), 4U);
    if (rows.size() == 4) {
        CHECK_EQ(rows[0].location, 0x1000U);
        CHECK_EQ(rows[1].location, 0x1001U);
        CHECK_EQ(rows[2].location, 0x1002U);
        CHECK_EQ(rows[3].location, 0x1004U);
        check_cfa(rows[3], 5, 24);
    }

    dwarf::cfi::FrameRow out;
    ErrorCode error = ErrorCode::none;
    CHECK(table.try_lookup(0x1002, out, &error));
    CHECK_EQ(static_cast<int>(error), static_cast<int>(ErrorCode::none));
    CHECK(!table.contains(0x0fff));
    CHECK(!table.contains(0x1100));
}

void test_advance_loc_forms_and_code_alignment() {
    CieSpec spec;
    spec.code_alignment = 4;
    spec.data_alignment = -8;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x00};
    spec.initial_instructions = {0x0c, 0x07, 0x08};
    const dwarf::cfi::Cie cie =
        dwarf::cfi::parse_cie(build_cie(
                                  dwarf::cfi::SectionKind::eh_frame, spec)
                                  .data);

    Bytes instructions;
    instructions.u8(dwarf::cfi::DW_CFA_advance_loc1);
    instructions.u8(2);
    append_def_cfa_offset(instructions, 16);
    instructions.u8(dwarf::cfi::DW_CFA_advance_loc2);
    instructions.u16(3);
    append_def_cfa_offset(instructions, 24);
    instructions.u8(dwarf::cfi::DW_CFA_advance_loc4);
    instructions.u32(1);
    append_def_cfa_offset(instructions, 32);

    const Bytes fde_bytes = build_fde_absptr(0x2000, 0x100, instructions.data);
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie);
    const dwarf::cfi::FrameTable table(cie, fde);

    check_cfa(table.lookup(0x2007), 7, 8);
    check_cfa(table.lookup(0x2008), 7, 16);
    check_cfa(table.lookup(0x2013), 7, 16);
    check_cfa(table.lookup(0x2014), 7, 24);
    check_cfa(table.lookup(0x2017), 7, 24);
    check_cfa(table.lookup(0x2018), 7, 32);
}

void test_remember_restore_state() {
    CieSpec spec;
    spec.code_alignment = 1;
    spec.data_alignment = -8;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x00};
    spec.initial_instructions = {
        0x0c, 0x07, 0x08, 0x86, 0x02};  // CFA r7+8, r6 -> -16
    const dwarf::cfi::Cie cie =
        dwarf::cfi::parse_cie(build_cie(
                                  dwarf::cfi::SectionKind::eh_frame, spec)
                                  .data);

    Bytes instructions;
    append_advance(instructions, 1);
    append_def_cfa_offset(instructions, 16);
    append_offset(instructions, 6, 3);  // -24
    append_advance(instructions, 1);
    instructions.u8(dwarf::cfi::DW_CFA_remember_state);
    append_def_cfa_offset(instructions, 32);
    append_offset(instructions, 6, 5);  // -40
    append_offset(instructions, 7, 2);  // -16
    append_advance(instructions, 1);
    instructions.u8(dwarf::cfi::DW_CFA_restore_state);
    append_advance(instructions, 1);
    append_def_cfa_offset(instructions, 8);
    append_advance(instructions, 1);

    const Bytes fde_bytes = build_fde_absptr(0x3000, 0x100, instructions.data);
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie);
    const dwarf::cfi::FrameTable table(cie, fde);

    const dwarf::cfi::FrameRow row0 = table.lookup(0x3000);
    check_cfa(row0, 7, 8);
    check_offset_rule(row0, 6, -16);

    const dwarf::cfi::FrameRow row1 = table.lookup(0x3001);
    check_cfa(row1, 7, 16);
    check_offset_rule(row1, 6, -24);
    check_absent(row1, 7);

    const dwarf::cfi::FrameRow row2 = table.lookup(0x3002);
    check_cfa(row2, 7, 32);
    check_offset_rule(row2, 6, -40);
    check_offset_rule(row2, 7, -16);

    const dwarf::cfi::FrameRow row3 = table.lookup(0x3003);
    check_cfa(row3, 7, 16);
    check_offset_rule(row3, 6, -24);
    check_absent(row3, 7);

    const dwarf::cfi::FrameRow row4 = table.lookup(0x3004);
    check_cfa(row4, 7, 8);
    check_offset_rule(row4, 6, -24);
}

void test_nested_state_stack() {
    CieSpec spec;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x00};
    spec.initial_instructions = {0x0c, 0x07, 0x08, 0x86, 0x01};
    const dwarf::cfi::Cie cie =
        dwarf::cfi::parse_cie(build_cie(
                                  dwarf::cfi::SectionKind::eh_frame, spec)
                                  .data);

    Bytes instructions;
    append_advance(instructions, 1);
    append_offset(instructions, 6, 2);  // -16
    instructions.u8(dwarf::cfi::DW_CFA_remember_state);
    append_offset(instructions, 7, 3);  // -24
    append_advance(instructions, 1);
    instructions.u8(dwarf::cfi::DW_CFA_remember_state);
    append_offset(instructions, 8, 4);  // -32
    append_advance(instructions, 1);
    instructions.u8(dwarf::cfi::DW_CFA_restore_state);
    append_advance(instructions, 1);
    instructions.u8(dwarf::cfi::DW_CFA_restore_state);
    append_advance(instructions, 1);

    const Bytes fde_bytes = build_fde_absptr(0x3500, 0x100, instructions.data);
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie);
    const dwarf::cfi::FrameTable table(cie, fde);

    check_offset_rule(table.lookup(0x3501), 7, -24);
    check_absent(table.lookup(0x3501), 8);
    check_offset_rule(table.lookup(0x3502), 8, -32);
    const dwarf::cfi::FrameRow restored_once = table.lookup(0x3503);
    check_offset_rule(restored_once, 7, -24);
    check_absent(restored_once, 8);
    const dwarf::cfi::FrameRow restored_twice = table.lookup(0x3504);
    check_offset_rule(restored_twice, 6, -16);
    check_absent(restored_twice, 7);
}

void test_restore_uses_cie_initial_rule() {
    CieSpec spec;
    spec.code_alignment = 1;
    spec.data_alignment = -8;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x00};
    spec.initial_instructions = {
        0x0c, 0x07, 0x08,              // CFA r7+8
        0x86, 0x02,                    // r6 -> -16
        0x90, 0x01,                    // r16 -> -8
    };
    const dwarf::cfi::Cie cie =
        dwarf::cfi::parse_cie(build_cie(
                                  dwarf::cfi::SectionKind::eh_frame, spec)
                                  .data);

    Bytes instructions;
    append_advance(instructions, 1);
    append_offset(instructions, 6, 4);  // -32
    append_advance(instructions, 1);
    instructions.u8(dwarf::cfi::DW_CFA_remember_state);
    append_offset(instructions, 6, 6);  // -48, top of state stack
    append_advance(instructions, 1);
    append_restore(instructions, 6);    // must return to CIE's -16
    append_advance(instructions, 1);
    append_offset(instructions, 6, 4);  // -32
    instructions.u8(dwarf::cfi::DW_CFA_restore_extended);
    instructions.uleb(6);               // must return to CIE's -16
    append_advance(instructions, 1);
    instructions.u8(dwarf::cfi::DW_CFA_remember_state);
    append_offset(instructions, 6, 8);  // -64
    instructions.u8(dwarf::cfi::DW_CFA_restore_state);
    append_offset(instructions, 7, 1);  // -8

    const Bytes fde_bytes = build_fde_absptr(0x4000, 0x100, instructions.data);
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie);
    const dwarf::cfi::FrameTable table(cie, fde);

    check_offset_rule(table.lookup(0x4001), 6, -32);
    check_offset_rule(table.lookup(0x4002), 6, -48);
    // The state stack top contains -48, but DW_CFA_restore must use the CIE
    // initial rule (-16), not the remembered state.
    check_offset_rule(table.lookup(0x4003), 6, -16);
    check_offset_rule(table.lookup(0x4004), 6, -16);
    const dwarf::cfi::FrameRow row5 = table.lookup(0x4005);
    check_offset_rule(row5, 6, -16);
    check_offset_rule(row5, 7, -8);
}

void test_negative_data_alignment() {
    CieSpec spec;
    spec.code_alignment = 1;
    spec.data_alignment = -4;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x00};
    spec.initial_instructions = {
        0x0c, 0x07, 0x08,
        0x90, 0x02,  // r16 -> 2 * -4 == -8
    };
    const dwarf::cfi::Cie cie =
        dwarf::cfi::parse_cie(build_cie(
                                  dwarf::cfi::SectionKind::eh_frame, spec)
                                  .data);

    Bytes instructions;
    append_offset(instructions, 6, 4);  // -16
    instructions.u8(dwarf::cfi::DW_CFA_offset_extended);
    instructions.uleb(5);
    instructions.uleb(3);               // -12
    instructions.u8(dwarf::cfi::DW_CFA_offset_extended_sf);
    instructions.uleb(4);
    instructions.sleb(-3);              // +12
    instructions.u8(dwarf::cfi::DW_CFA_val_offset);
    instructions.uleb(8);
    instructions.uleb(2);               // -8
    instructions.u8(dwarf::cfi::DW_CFA_val_offset_sf);
    instructions.uleb(9);
    instructions.sleb(-2);              // +8
    instructions.u8(dwarf::cfi::DW_CFA_def_cfa_sf);
    instructions.uleb(10);
    instructions.sleb(-2);              // CFA r10 + 8
    instructions.u8(dwarf::cfi::DW_CFA_def_cfa_offset_sf);
    instructions.sleb(-3);              // CFA r10 + 12

    const Bytes fde_bytes = build_fde_absptr(0x5000, 0x100, instructions.data);
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie);
    const dwarf::cfi::FrameRow row =
        dwarf::cfi::evaluate_frame(cie, fde, 0x5000);
    check_cfa(row, 10, 12);
    check_offset_rule(row, 16, -8);
    check_offset_rule(row, 6, -16);
    check_offset_rule(row, 5, -12);
    check_offset_rule(row, 4, 12);

    const dwarf::cfi::RegisterRule* val8 = row.find_register(8);
    CHECK(val8 != nullptr);
    if (val8 != nullptr) {
        CHECK_EQ(static_cast<int>(val8->kind),
                 static_cast<int>(RegisterRuleKind::val_offset));
        CHECK_EQ(val8->offset, -8);
    }
    const dwarf::cfi::RegisterRule* val9 = row.find_register(9);
    CHECK(val9 != nullptr);
    if (val9 != nullptr) {
        CHECK_EQ(static_cast<int>(val9->kind),
                 static_cast<int>(RegisterRuleKind::val_offset));
        CHECK_EQ(val9->offset, 8);
    }
}

void test_register_rule_kinds() {
    CieSpec spec;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x00};
    spec.initial_instructions = {0x0c, 0x07, 0x08, 0x86, 0x01};
    const dwarf::cfi::Cie cie =
        dwarf::cfi::parse_cie(build_cie(
                                  dwarf::cfi::SectionKind::eh_frame, spec)
                                  .data);

    Bytes instructions;
    instructions.u8(dwarf::cfi::DW_CFA_undefined);
    instructions.uleb(6);
    instructions.u8(dwarf::cfi::DW_CFA_same_value);
    instructions.uleb(7);
    instructions.u8(dwarf::cfi::DW_CFA_register);
    instructions.uleb(8);
    instructions.uleb(9);
    instructions.u8(dwarf::cfi::DW_CFA_expression);
    instructions.uleb(10);
    instructions.uleb(2);
    instructions.u8(0x6c);
    instructions.u8(0x06);
    instructions.u8(dwarf::cfi::DW_CFA_val_expression);
    instructions.uleb(11);
    instructions.uleb(1);
    instructions.u8(0x6c);

    const Bytes fde_bytes = build_fde_absptr(0x6000, 0x100, instructions.data);
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie);
    const dwarf::cfi::FrameRow row =
        dwarf::cfi::evaluate_frame(cie, fde, 0x6000);

    const auto* r6 = row.find_register(6);
    CHECK(r6 != nullptr && r6->kind == RegisterRuleKind::undefined);
    const auto* r7 = row.find_register(7);
    CHECK(r7 != nullptr && r7->kind == RegisterRuleKind::same_value);
    const auto* r8 = row.find_register(8);
    CHECK(r8 != nullptr && r8->kind == RegisterRuleKind::reg && r8->reg == 9);
    const auto* r10 = row.find_register(10);
    CHECK(r10 != nullptr && r10->kind == RegisterRuleKind::expression);
    if (r10 != nullptr) {
        CHECK_EQ(r10->expression,
                 (std::vector<std::uint8_t>{0x6c, 0x06}));
    }
    const auto* r11 = row.find_register(11);
    CHECK(r11 != nullptr && r11->kind == RegisterRuleKind::val_expression);
}

void test_def_cfa_opcodes() {
    CieSpec spec;
    spec.code_alignment = 1;
    spec.data_alignment = -4;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x00};
    spec.initial_instructions = {0x0c, 0x07, 0x08};
    const dwarf::cfi::Cie cie =
        dwarf::cfi::parse_cie(build_cie(
                                  dwarf::cfi::SectionKind::eh_frame, spec)
                                  .data);

    Bytes instructions;
    instructions.u8(dwarf::cfi::DW_CFA_def_cfa);
    instructions.uleb(3);
    instructions.uleb(20);
    append_advance(instructions, 1);
    instructions.u8(dwarf::cfi::DW_CFA_def_cfa_register);
    instructions.uleb(4);
    append_advance(instructions, 1);
    instructions.u8(dwarf::cfi::DW_CFA_def_cfa_offset);
    instructions.uleb(24);
    append_advance(instructions, 1);
    instructions.u8(dwarf::cfi::DW_CFA_def_cfa_sf);
    instructions.uleb(5);
    instructions.sleb(-2);  // 8
    append_advance(instructions, 1);
    instructions.u8(dwarf::cfi::DW_CFA_def_cfa_offset_sf);
    instructions.sleb(-3);  // 12

    const Bytes fde_bytes = build_fde_absptr(0x5500, 0x100, instructions.data);
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie);
    const dwarf::cfi::FrameTable table(cie, fde);

    check_cfa(table.lookup(0x5500), 3, 20);
    check_cfa(table.lookup(0x5501), 4, 20);
    check_cfa(table.lookup(0x5502), 4, 24);
    check_cfa(table.lookup(0x5503), 5, 8);
    check_cfa(table.lookup(0x5504), 5, 12);
}

void test_dwarf64_debug_frame() {
    Bytes cie_content;
    cie_content.u64(0xffffffffffffffffULL);
    cie_content.u8(5);
    cie_content.cstr("");
    cie_content.uleb(1);
    cie_content.sleb(-8);
    cie_content.uleb(16);
    cie_content.append(std::vector<std::uint8_t>{
        0x0c, 0x07, 0x08, 0x90, 0x01});

    Bytes cie;
    cie.u32(0xffffffffU);
    cie.u64(cie_content.data.size());
    cie.append(cie_content);

    Bytes fde_content;
    fde_content.u64(0);
    fde_content.u64(0x3100);
    fde_content.u64(0x80);
    fde_content.u8(dwarf::cfi::DW_CFA_nop);

    Bytes fde;
    fde.u32(0xffffffffU);
    fde.u64(fde_content.data.size());
    fde.append(fde_content);

    Bytes section;
    section.append(cie);
    section.append(fde);

    const dwarf::cfi::CfiSection parsed =
        dwarf::cfi::parse_cfi_section(section.data);
    CHECK_EQ(parsed.cies.size(), 1U);
    CHECK_EQ(parsed.fdes.size(), 1U);
    CHECK(parsed.cies[0].dwarf64);
    CHECK(parsed.fdes[0].dwarf64);
    CHECK_EQ(static_cast<int>(parsed.cies[0].kind),
             static_cast<int>(dwarf::cfi::SectionKind::debug_frame));
    const dwarf::cfi::FrameRow row = parsed.lookup(0x3100);
    check_cfa(row, 7, 8);
    check_offset_rule(row, 16, -8);
}

void test_big_endian_debug_frame() {
    Bytes cie_content;
    // 0xffffffff in big-endian order.
    cie_content.u8(0xff);
    cie_content.u8(0xff);
    cie_content.u8(0xff);
    cie_content.u8(0xff);
    cie_content.u8(5);
    cie_content.cstr("");
    cie_content.uleb(1);
    cie_content.sleb(-8);
    cie_content.uleb(16);
    cie_content.append(std::vector<std::uint8_t>{0x0c, 0x07, 0x08});

    Bytes cie;
    cie.u8(0);
    cie.u8(0);
    cie.u8(0);
    cie.u8(static_cast<std::uint8_t>(cie_content.data.size()));
    cie.append(cie_content);

    Bytes fde_content;
    for (int i = 0; i < 4; ++i) {
        fde_content.u8(0);
    }
    // initial_location 0x4100, range 0x80, both big-endian.
    for (int shift = 56; shift >= 0; shift -= 8) {
        fde_content.u8(static_cast<std::uint8_t>(0x4100ULL >> shift));
    }
    for (int shift = 56; shift >= 0; shift -= 8) {
        fde_content.u8(static_cast<std::uint8_t>(0x80ULL >> shift));
    }
    fde_content.u8(dwarf::cfi::DW_CFA_nop);

    Bytes fde;
    fde.u8(0);
    fde.u8(0);
    fde.u8(0);
    fde.u8(static_cast<std::uint8_t>(fde_content.data.size()));
    fde.append(fde_content);

    Bytes section;
    section.append(cie);
    section.append(fde);
    dwarf::cfi::ParseOptions options;
    options.kind = dwarf::cfi::SectionKind::debug_frame;
    options.little_endian = false;
    const dwarf::cfi::CfiSection parsed =
        dwarf::cfi::parse_cfi_section(section.data, options);
    CHECK_EQ(parsed.fdes[0].initial_location, 0x4100U);
    CHECK_EQ(parsed.fdes[0].address_range, 0x80U);
    check_cfa(parsed.lookup(0x4100), 7, 8);
}

void test_address_size_4() {
    CieSpec spec;
    spec.initial_instructions = {0x0c, 0x07, 0x08};
    const Bytes cie_bytes =
        build_cie(dwarf::cfi::SectionKind::debug_frame, spec);
    dwarf::cfi::ParseOptions options;
    options.kind = dwarf::cfi::SectionKind::debug_frame;
    options.address_size = 4;
    const dwarf::cfi::Cie cie =
        dwarf::cfi::parse_cie(cie_bytes.data, options);
    CHECK_EQ(cie.address_size, 4);

    Bytes fde_content;
    fde_content.u32(0);
    fde_content.u32(0x6200);
    fde_content.u32(0x40);
    fde_content.u8(dwarf::cfi::DW_CFA_nop);
    const Bytes fde_bytes = add_entry(fde_content);
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie);
    CHECK_EQ(fde.initial_location, 0x6200U);
    CHECK_EQ(fde.address_range, 0x40U);
    check_cfa(dwarf::cfi::evaluate_frame(cie, fde, 0x6200), 7, 8);
}

void test_offset_overflow_rejected() {
    dwarf::cfi::Cie cie;
    cie.code_alignment_factor = 1;
    cie.data_alignment_factor = std::numeric_limits<std::int64_t>::max();
    cie.return_address_register = 16;
    cie.address_size = 8;
    cie.kind = dwarf::cfi::SectionKind::eh_frame;
    cie.initial_instructions = {0x0c, 0x07, 0x08};

    dwarf::cfi::Fde fde;
    fde.initial_location = 0x7000;
    fde.address_range = 0x100;
    fde.instructions = {0x86, 0x02};  // offset r6, 2 * INT64_MAX
    const dwarf::cfi::FrameTable table(cie, fde);
    expect_error(ErrorCode::arithmetic_overflow, [&] {
        (void)table.lookup(0x7000);
    });
}

void test_set_loc_and_pc_range() {
    CieSpec spec;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x00};
    spec.initial_instructions = {0x0c, 0x07, 0x08};
    const dwarf::cfi::Cie cie =
        dwarf::cfi::parse_cie(build_cie(
                                  dwarf::cfi::SectionKind::eh_frame, spec)
                                  .data);

    Bytes instructions;
    instructions.u8(dwarf::cfi::DW_CFA_set_loc);
    instructions.u64(0x7010);
    append_def_cfa_offset(instructions, 16);
    append_advance(instructions, 1);
    append_def_cfa_offset(instructions, 24);

    const Bytes fde_bytes = build_fde_absptr(0x7000, 0x100, instructions.data);
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie);
    const dwarf::cfi::FrameTable table(cie, fde);

    check_cfa(table.lookup(0x7000), 7, 8);
    check_cfa(table.lookup(0x700f), 7, 8);
    check_cfa(table.lookup(0x7010), 7, 16);
    check_cfa(table.lookup(0x7011), 7, 24);

    expect_error(ErrorCode::pc_out_of_range, [&] {
        (void)table.lookup(0x6fff);
    });
    expect_error(ErrorCode::pc_out_of_range, [&] {
        (void)table.lookup(0x7100);
    });

    dwarf::cfi::FrameRow out;
    ErrorCode error = ErrorCode::none;
    CHECK(!table.try_lookup(0x7100, out, &error));
    CHECK_EQ(static_cast<int>(error),
             static_cast<int>(ErrorCode::pc_out_of_range));
}

void test_empty_state_stack_and_bad_instructions() {
    CieSpec spec;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x00};
    spec.initial_instructions = {0x0c, 0x07, 0x08};
    const dwarf::cfi::Cie cie =
        dwarf::cfi::parse_cie(build_cie(
                                  dwarf::cfi::SectionKind::eh_frame, spec)
                                  .data);

    {
        const std::vector<std::uint8_t> instructions{
            dwarf::cfi::DW_CFA_restore_state};
        const Bytes fde_bytes =
            build_fde_absptr(0x8000, 0x100, instructions);
        const dwarf::cfi::Fde fde =
            dwarf::cfi::parse_fde(fde_bytes.data, cie);
        expect_error(ErrorCode::state_stack_underflow, [&] {
            (void)dwarf::cfi::evaluate_frame(cie, fde, 0x8000);
        });
    }

    {
        const std::vector<std::uint8_t> instructions{
            0x80, 0x80};  // DW_CFA_offset r0 with truncated ULEB128 operand
        const Bytes fde_bytes =
            build_fde_absptr(0x8100, 0x100, instructions);
        const dwarf::cfi::Fde fde =
            dwarf::cfi::parse_fde(fde_bytes.data, cie);
        expect_error(ErrorCode::truncated_input, [&] {
            (void)dwarf::cfi::evaluate_frame(cie, fde, 0x8100);
        });
    }

    {
        const std::vector<std::uint8_t> instructions{
            dwarf::cfi::DW_CFA_def_cfa, 0x07,
            0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02};
        const Bytes fde_bytes =
            build_fde_absptr(0x8200, 0x100, instructions);
        const dwarf::cfi::Fde fde =
            dwarf::cfi::parse_fde(fde_bytes.data, cie);
        expect_error(ErrorCode::leb128_overflow, [&] {
            (void)dwarf::cfi::evaluate_frame(cie, fde, 0x8200);
        });
    }

    {
        const std::vector<std::uint8_t> instructions{0x17};
        const Bytes fde_bytes =
            build_fde_absptr(0x8300, 0x100, instructions);
        const dwarf::cfi::Fde fde =
            dwarf::cfi::parse_fde(fde_bytes.data, cie);
        expect_error(ErrorCode::unknown_opcode, [&] {
            (void)dwarf::cfi::evaluate_frame(cie, fde, 0x8300);
        });
    }
}

void test_lookup_stops_before_later_bad_bytes() {
    CieSpec spec;
    spec.augmentation = "zR";
    spec.augmentation_data = {0x00};
    spec.initial_instructions = {0x0c, 0x07, 0x08};
    const dwarf::cfi::Cie cie =
        dwarf::cfi::parse_cie(build_cie(
                                  dwarf::cfi::SectionKind::eh_frame, spec)
                                  .data);

    Bytes instructions;
    append_advance(instructions, 1);
    append_def_cfa_offset(instructions, 16);
    append_advance(instructions, 1);
    instructions.u8(0x17);  // invalid, but after the queried PC

    const Bytes fde_bytes = build_fde_absptr(0x9000, 0x100, instructions.data);
    const dwarf::cfi::Fde fde =
        dwarf::cfi::parse_fde(fde_bytes.data, cie);
    check_cfa(dwarf::cfi::evaluate_frame(cie, fde, 0x9000), 7, 8);
    check_cfa(dwarf::cfi::evaluate_frame(cie, fde, 0x9001), 7, 16);
    expect_error(ErrorCode::unknown_opcode, [&] {
        (void)dwarf::cfi::evaluate_frame(cie, fde, 0x9002);
    });
}

}  // namespace

namespace {

void run_test(const char* name, void (*function)()) {
    current_test = name;
    function();
}

}  // namespace

int main() {
    run_test("uleb128", test_uleb128);
    run_test("sleb128", test_sleb128);
    run_test("cie_parse", test_cie_parse);
    run_test("debug_frame_cie_and_fde", test_debug_frame_cie_and_fde);
    run_test("cie_augmentation_and_signal",
             test_cie_augmentation_and_signal_frame);
    run_test("pcrel_fde", test_pcrel_fde);
    run_test("section_parse", test_section_parse);
    run_test("debug_frame_section", test_debug_frame_section);
    run_test("prologue_and_rows", test_prologue_and_rows);
    run_test("advance_loc_forms", test_advance_loc_forms_and_code_alignment);
    run_test("remember_restore_state", test_remember_restore_state);
    run_test("nested_state_stack", test_nested_state_stack);
    run_test("restore_cie_rule", test_restore_uses_cie_initial_rule);
    run_test("negative_data_alignment", test_negative_data_alignment);
    run_test("register_rule_kinds", test_register_rule_kinds);
    run_test("def_cfa_opcodes", test_def_cfa_opcodes);
    run_test("dwarf64_debug_frame", test_dwarf64_debug_frame);
    run_test("big_endian_debug_frame", test_big_endian_debug_frame);
    run_test("address_size_4", test_address_size_4);
    run_test("offset_overflow", test_offset_overflow_rejected);
    run_test("set_loc_and_pc_range", test_set_loc_and_pc_range);
    run_test("empty_stack_and_bad_insns",
             test_empty_state_stack_and_bad_instructions);
    run_test("lookup_stops_early", test_lookup_stops_before_later_bad_bytes);

    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "all DWARF CFI tests passed\n";
    return 0;
}
