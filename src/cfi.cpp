#include "dwarf/cfi.hpp"

#include <algorithm>
#include <limits>
#include <string>
#include <utility>

namespace dwarf::cfi {
namespace {

[[noreturn]] void fail(ErrorCode code, std::string message, std::size_t offset) {
    throw CfiError(code, std::move(message), offset);
}

std::string hex_byte(std::uint8_t value) {
    static constexpr char digits[] = "0123456789abcdef";
    std::string out = "0x";
    out.push_back(digits[(value >> 4) & 0xf]);
    out.push_back(digits[value & 0xf]);
    return out;
}

std::uint64_t read_uleb_raw(std::span<const std::uint8_t> input,
                            std::size_t& index,
                            std::size_t report_base) {
    std::uint64_t value = 0;
    for (unsigned byte_index = 0;; ++byte_index) {
        // ceil(64 / 7) == 10; an eleventh byte cannot contribute any of the
        // low 64 bits.
        if (byte_index >= 10) {
            fail(ErrorCode::leb128_too_long,
                 "ULEB128 value uses more than 10 bytes",
                 report_base + index);
        }
        if (index >= input.size()) {
            fail(ErrorCode::truncated_input,
                 "truncated ULEB128 value",
                 report_base + index);
        }
        const std::uint8_t byte = input[index++];
        const unsigned shift = byte_index * 7U;
        const std::uint64_t payload = static_cast<std::uint64_t>(byte & 0x7fU);
        if (byte_index == 9) {
            // Only bit 0 of the tenth byte fits in a 64-bit value.
            if (payload > 1U) {
                fail(ErrorCode::leb128_overflow,
                     "ULEB128 value overflows 64 bits",
                     report_base + index - 1);
            }
        }
        value |= payload << shift;
        if ((byte & 0x80U) == 0) {
            return value;
        }
    }
}

std::int64_t read_sleb_raw(std::span<const std::uint8_t> input,
                           std::size_t& index,
                           std::size_t report_base) {
    std::uint64_t bits = 0;
    for (unsigned byte_index = 0;; ++byte_index) {
        if (byte_index >= 10) {
            fail(ErrorCode::leb128_too_long,
                 "SLEB128 value uses more than 10 bytes",
                 report_base + index);
        }
        if (index >= input.size()) {
            fail(ErrorCode::truncated_input,
                 "truncated SLEB128 value",
                 report_base + index);
        }
        const std::uint8_t byte = input[index++];
        const unsigned shift = byte_index * 7U;
        const std::uint64_t payload = static_cast<std::uint64_t>(byte & 0x7fU);

        if (byte_index == 9) {
            // For a 64-bit signed value the tenth byte can only be a pure
            // sign extension: 0x00 (non-negative) or 0x7f (negative).
            if (payload != 0x00U && payload != 0x7fU) {
                fail(ErrorCode::leb128_overflow,
                     "SLEB128 value overflows 64 bits",
                     report_base + index - 1);
            }
            bits |= (payload & 1U) << 63U;
        } else {
            bits |= payload << shift;
        }

        if ((byte & 0x80U) == 0) {
            if (byte_index < 9 && (byte & 0x40U) != 0) {
                bits |= ~std::uint64_t{0} << (shift + 7U);
            }
            return static_cast<std::int64_t>(bits);
        }
    }
}

class Reader {
public:
    Reader(std::span<const std::uint8_t> data,
           std::size_t position,
           std::size_t end,
           bool little_endian,
           std::size_t report_base)
        : data_(data), position_(position), end_(end),
          little_endian_(little_endian), report_base_(report_base) {
        if (position_ > end_ || end_ > data_.size()) {
            fail(ErrorCode::invalid_length,
                 "invalid reader bounds",
                 report_base_ + position_);
        }
    }

    std::size_t position() const noexcept { return position_; }
    std::size_t end() const noexcept { return end_; }
    std::size_t remaining() const noexcept { return end_ - position_; }
    bool empty() const noexcept { return position_ == end_; }
    std::span<const std::uint8_t> data() const noexcept { return data_; }
    std::size_t report_offset() const noexcept { return report_base_ + position_; }

    void seek(std::size_t position) {
        if (position > end_) {
            fail(ErrorCode::truncated_input,
                 "seek past end of input",
                 report_base_ + position);
        }
        position_ = position;
    }

    void align(std::size_t alignment) {
        if (alignment == 0) {
            fail(ErrorCode::invalid_operand, "zero alignment", report_offset());
        }
        const std::size_t remainder = position_ % alignment;
        if (remainder != 0) {
            skip(alignment - remainder);
        }
    }

    void skip(std::size_t count) {
        require(count);
        position_ += count;
    }

    std::uint8_t u8() {
        require(1);
        return data_[position_++];
    }

    std::uint64_t uint(unsigned byte_count) {
        if (byte_count == 0 || byte_count > 8) {
            fail(ErrorCode::invalid_operand,
                 "invalid integer width",
                 report_offset());
        }
        require(byte_count);
        std::uint64_t value = 0;
        if (little_endian_) {
            for (unsigned i = 0; i < byte_count; ++i) {
                value |= static_cast<std::uint64_t>(data_[position_ + i]) << (8U * i);
            }
        } else {
            for (unsigned i = 0; i < byte_count; ++i) {
                value = (value << 8U) | static_cast<std::uint64_t>(data_[position_ + i]);
            }
        }
        position_ += byte_count;
        return value;
    }

    std::int64_t sint(unsigned byte_count) {
        if (byte_count == 0 || byte_count > 8) {
            fail(ErrorCode::invalid_operand,
                 "invalid integer width",
                 report_offset());
        }
        const std::uint64_t raw = uint(byte_count);
        if (byte_count < 8 &&
            (raw & (std::uint64_t{1} << (byte_count * 8U - 1U))) != 0) {
            return static_cast<std::int64_t>(
                raw | (~std::uint64_t{0} << (byte_count * 8U)));
        }
        return static_cast<std::int64_t>(raw);
    }

    std::uint64_t uleb() {
        const auto bounded = data_.first(end_);
        const std::uint64_t value = read_uleb_raw(bounded, position_, report_base_);
        return value;
    }

    std::int64_t sleb() {
        const auto bounded = data_.first(end_);
        const std::int64_t value = read_sleb_raw(bounded, position_, report_base_);
        return value;
    }

    std::span<const std::uint8_t> bytes(std::size_t count) {
        require(count);
        const auto result = data_.subspan(position_, count);
        position_ += count;
        return result;
    }

    std::string cstring() {
        const std::size_t start = position_;
        while (position_ < end_ && data_[position_] != 0) {
            ++position_;
        }
        if (position_ == end_) {
            fail(ErrorCode::truncated_input,
                 "unterminated augmentation string",
                 report_base_ + position_);
        }
        std::string result(
            reinterpret_cast<const char*>(data_.data() + start),
            position_ - start);
        ++position_;
        return result;
    }

private:
    void require(std::size_t count) const {
        if (count > end_ - position_) {
            fail(ErrorCode::truncated_input,
                 "truncated input",
                 report_base_ + position_);
        }
    }

    std::span<const std::uint8_t> data_;
    std::size_t position_;
    std::size_t end_;
    bool little_endian_;
    std::size_t report_base_;
};

struct EntryHeader {
    std::size_t start = 0;
    std::size_t content_start = 0;
    std::size_t end = 0;
    std::uint64_t length = 0;
    bool dwarf64 = false;
    unsigned offset_size = 4;
};

EntryHeader read_header(std::span<const std::uint8_t> data,
                        std::size_t start,
                        bool little_endian,
                        std::size_t report_base) {
    Reader reader(data, start, data.size(), little_endian, report_base);
    EntryHeader header;
    header.start = start;

    if (reader.remaining() < 4) {
        fail(ErrorCode::truncated_input,
             "truncated CFI entry length",
             reader.report_offset());
    }
    const std::uint32_t marker = static_cast<std::uint32_t>(reader.uint(4));
    if (marker == 0) {
        header.content_start = reader.position();
        header.end = reader.position();
        header.length = 0;
        return header;
    }
    if (marker == 0xffffffffU) {
        header.dwarf64 = true;
        header.offset_size = 8;
        header.length = reader.uint(8);
    } else {
        header.length = marker;
    }
    header.content_start = reader.position();
    if (header.length > reader.remaining()) {
        fail(ErrorCode::invalid_length,
             "CFI entry extends past end of input",
             reader.report_offset());
    }
    if (header.length < header.offset_size) {
        fail(ErrorCode::invalid_length,
             "CFI entry is too short for its identifier",
             reader.report_offset());
    }
    header.end = header.content_start + static_cast<std::size_t>(header.length);
    return header;
}

std::uint64_t all_ones(unsigned offset_size) {
    return offset_size == 8 ? std::numeric_limits<std::uint64_t>::max()
                            : std::uint64_t{0xffffffffU};
}

SectionKind detect_kind(std::uint64_t id, unsigned offset_size) {
    if (id == 0) {
        return SectionKind::eh_frame;
    }
    if (id == all_ones(offset_size)) {
        return SectionKind::debug_frame;
    }
    return SectionKind::auto_detect;
}

std::uint8_t normalized_address_size(std::uint8_t address_size) {
    if (address_size == 0) {
        return 8;
    }
    if (address_size > 8) {
        fail(ErrorCode::invalid_operand,
             "address size larger than 8 bytes",
             0);
    }
    return address_size;
}

std::uint64_t read_encoded_value(Reader& reader,
                                 std::uint8_t encoding,
                                 const ParseOptions& options,
                                 bool range_value,
                                 bool allow_indirect,
                                 bool* indirect_out) {
    if (indirect_out != nullptr) {
        *indirect_out = false;
    }
    if (encoding == DW_EH_PE_omit) {
        fail(ErrorCode::invalid_pointer_encoding,
             "DW_EH_PE_omit is not valid for this field",
             reader.report_offset());
    }

    const std::uint8_t application = encoding & 0x70U;
    if (application == DW_EH_PE_aligned && !range_value) {
        reader.align(normalized_address_size(options.address_size));
    }
    const std::size_t field_position = reader.position();
    const std::uint8_t format = encoding & 0x0fU;
    const std::uint8_t address_size = normalized_address_size(options.address_size);

    std::uint64_t value = 0;
    bool signed_value = false;
    switch (format) {
    case DW_EH_PE_absptr:
        signed_value = !range_value && application != DW_EH_PE_absptr &&
                       application != DW_EH_PE_aligned;
        value = signed_value ? static_cast<std::uint64_t>(reader.sint(address_size))
                             : reader.uint(address_size);
        break;
    case DW_EH_PE_uleb128:
        value = reader.uleb();
        break;
    case DW_EH_PE_udata2:
        value = reader.uint(2);
        break;
    case DW_EH_PE_udata4:
        value = reader.uint(4);
        break;
    case DW_EH_PE_udata8:
        value = reader.uint(8);
        break;
    case DW_EH_PE_sleb128: {
        const std::int64_t signed_encoded = reader.sleb();
        if (range_value && signed_encoded < 0) {
            fail(ErrorCode::invalid_operand,
                 "negative address range",
                 field_position);
        }
        value = static_cast<std::uint64_t>(signed_encoded);
        break;
    }
    case DW_EH_PE_sdata2:
        {
            const std::int64_t signed_encoded = reader.sint(2);
            if (range_value && signed_encoded < 0) {
                fail(ErrorCode::invalid_operand,
                     "negative address range",
                     field_position);
            }
            value = static_cast<std::uint64_t>(signed_encoded);
        }
        break;
    case DW_EH_PE_sdata4:
        {
            const std::int64_t signed_encoded = reader.sint(4);
            if (range_value && signed_encoded < 0) {
                fail(ErrorCode::invalid_operand,
                     "negative address range",
                     field_position);
            }
            value = static_cast<std::uint64_t>(signed_encoded);
        }
        break;
    case DW_EH_PE_sdata8:
        {
            const std::int64_t signed_encoded = reader.sint(8);
            if (range_value && signed_encoded < 0) {
                fail(ErrorCode::invalid_operand,
                     "negative address range",
                     field_position);
            }
            value = static_cast<std::uint64_t>(signed_encoded);
        }
        break;
    default:
        fail(ErrorCode::invalid_pointer_encoding,
             "unsupported DW_EH_PE format " + hex_byte(format),
             field_position);
    }

    if (range_value) {
        return value;
    }

    switch (application) {
    case DW_EH_PE_absptr:
    case DW_EH_PE_aligned:
        break;
    case DW_EH_PE_pcrel:
        value += options.section_address + field_position;
        break;
    case DW_EH_PE_textrel:
        value += options.text_address;
        break;
    case DW_EH_PE_datarel:
        value += options.data_address;
        break;
    case DW_EH_PE_funcrel:
        value += options.function_address;
        break;
    default:
        fail(ErrorCode::invalid_pointer_encoding,
             "unsupported DW_EH_PE application " +
                 hex_byte(static_cast<std::uint8_t>(application)),
             field_position);
    }

    if ((encoding & DW_EH_PE_indirect) != 0) {
        if (!allow_indirect) {
            fail(ErrorCode::invalid_pointer_encoding,
                 "indirect pointer cannot be resolved without memory access",
                 field_position);
        }
        if (indirect_out != nullptr) {
            *indirect_out = true;
        }
    }
    return value;
}

Cie parse_cie_body(Reader& reader,
                   std::uint64_t id,
                   const EntryHeader& header,
                   SectionKind kind,
                   const ParseOptions& options) {
    Cie cie;
    cie.offset = options.section_offset + header.start;
    cie.length = header.length;
    cie.dwarf64 = header.dwarf64;
    cie.kind = kind;
    cie.address_size = normalized_address_size(options.address_size);

    const std::uint64_t expected_id = kind == SectionKind::eh_frame
                                          ? 0
                                          : all_ones(header.offset_size);
    if (id != expected_id) {
        fail(ErrorCode::invalid_cie_id,
             "CIE identifier does not match the section kind",
             reader.report_offset() - header.offset_size);
    }

    cie.version = reader.u8();
    if (cie.version != 1 && cie.version != 3 && cie.version != 4 &&
        cie.version != 5) {
        fail(ErrorCode::unsupported_version,
             "unsupported CIE version " + std::to_string(cie.version),
             reader.report_offset() - 1);
    }
    cie.augmentation = reader.cstring();
    cie.code_alignment_factor = reader.uleb();
    cie.data_alignment_factor = reader.sleb();
    cie.return_address_register =
        cie.version == 1 ? reader.u8() : reader.uleb();

    if (!cie.augmentation.empty() && cie.augmentation[0] == 'z') {
        cie.has_z_augmentation = true;
        const std::uint64_t augmentation_length = reader.uleb();
        const std::size_t augmentation_start = reader.position();
        if (augmentation_length > reader.remaining()) {
            fail(ErrorCode::invalid_augmentation,
                 "augmentation data extends past CIE",
                 reader.report_offset());
        }
        const std::size_t augmentation_end =
            augmentation_start + static_cast<std::size_t>(augmentation_length);
        const auto augmentation_data =
            reader.data().subspan(augmentation_start,
                                  static_cast<std::size_t>(augmentation_length));
        cie.augmentation_data.assign(augmentation_data.begin(),
                                     augmentation_data.end());

        for (std::size_t i = 1; i < cie.augmentation.size(); ++i) {
            if (reader.position() >= augmentation_end) {
                fail(ErrorCode::invalid_augmentation,
                     "augmentation letters exceed augmentation data",
                     reader.report_offset());
            }
            const char letter = cie.augmentation[i];
            switch (letter) {
            case 'R':
                cie.fde_encoding = reader.u8();
                break;
            case 'L':
                cie.lsda_encoding = reader.u8();
                break;
            case 'P': {
                const std::uint8_t encoding = reader.u8();
                bool indirect = false;
                cie.personality = read_encoded_value(
                    reader, encoding, options, false, true, &indirect);
                cie.personality_encoding = encoding;
                cie.personality_indirect = indirect;
                break;
            }
            case 'S':
                cie.signal_frame = true;
                break;
            default:
                fail(ErrorCode::invalid_augmentation,
                     std::string("unsupported augmentation letter '") +
                         letter + "'",
                     reader.report_offset());
            }
            if (reader.position() > augmentation_end) {
                fail(ErrorCode::invalid_augmentation,
                     "augmentation letter overruns augmentation data",
                     reader.report_offset());
            }
        }
        reader.seek(augmentation_end);
    } else {
        // Legacy non-'z' augmentation strings are allowed to carry the same
        // known letters directly, without a length prefix.
        for (char letter : cie.augmentation) {
            switch (letter) {
            case 'R':
                cie.fde_encoding = reader.u8();
                break;
            case 'L':
                cie.lsda_encoding = reader.u8();
                break;
            case 'P': {
                const std::uint8_t encoding = reader.u8();
                bool indirect = false;
                cie.personality = read_encoded_value(
                    reader, encoding, options, false, true, &indirect);
                cie.personality_encoding = encoding;
                cie.personality_indirect = indirect;
                break;
            }
            case 'S':
                cie.signal_frame = true;
                break;
            case 'e':
            case 'h':
                // Historical GNU "eh" augmentation carries no data.
                break;
            default:
                fail(ErrorCode::invalid_augmentation,
                     std::string("unsupported augmentation letter '") +
                         letter + "'",
                     reader.report_offset());
            }
        }
    }

    cie.initial_instructions_offset = reader.report_offset();
    const auto instructions = reader.bytes(reader.remaining());
    cie.initial_instructions.assign(instructions.begin(), instructions.end());
    reader.seek(header.end);
    return cie;
}

Fde parse_fde_body(Reader& reader,
                   std::uint64_t cie_pointer,
                   const EntryHeader& header,
                   const Cie& cie,
                   SectionKind kind,
                   const ParseOptions& parse_options) {
    Fde fde;
    fde.offset = parse_options.section_offset + header.start;
    fde.length = header.length;
    fde.dwarf64 = header.dwarf64;
    fde.cie_pointer = cie_pointer;
    fde.kind = kind;

    ParseOptions options = parse_options;
    if (cie.address_size != 0) {
        options.address_size = cie.address_size;
    }
    const std::uint8_t encoding =
        cie.fde_encoding.value_or(DW_EH_PE_absptr);
    fde.initial_location =
        read_encoded_value(reader, encoding, options, false, false, nullptr);
    fde.address_range =
        read_encoded_value(reader, encoding, options, true, false, nullptr);

    fde.instructions_offset = reader.report_offset();
    const auto instructions = reader.bytes(reader.remaining());
    fde.instructions.assign(instructions.begin(), instructions.end());
    reader.seek(header.end);
    return fde;
}

bool checked_add(std::uint64_t left,
                 std::uint64_t right,
                 std::uint64_t& result) {
    if (right > std::numeric_limits<std::uint64_t>::max() - left) {
        return false;
    }
    result = left + right;
    return true;
}

bool checked_mul(std::uint64_t left,
                 std::uint64_t right,
                 std::uint64_t& result) {
    if (left != 0 &&
        right > std::numeric_limits<std::uint64_t>::max() / left) {
        return false;
    }
    result = left * right;
    return true;
}

std::uint64_t unsigned_magnitude(std::int64_t value) noexcept {
    return value < 0 ? std::uint64_t{0} - static_cast<std::uint64_t>(value)
                     : static_cast<std::uint64_t>(value);
}

std::int64_t signed_product(std::uint64_t magnitude,
                            bool negative,
                            std::size_t offset) {
    constexpr std::uint64_t min_magnitude = std::uint64_t{1} << 63U;
    if (!negative) {
        if (magnitude > static_cast<std::uint64_t>(
                            std::numeric_limits<std::int64_t>::max())) {
            fail(ErrorCode::arithmetic_overflow,
                 "CFI offset overflows 64 bits",
                 offset);
        }
        return static_cast<std::int64_t>(magnitude);
    }
    if (magnitude > min_magnitude) {
        fail(ErrorCode::arithmetic_overflow,
             "CFI offset underflows 64 bits",
             offset);
    }
    if (magnitude == min_magnitude) {
        return std::numeric_limits<std::int64_t>::min();
    }
    return -static_cast<std::int64_t>(magnitude);
}

std::int64_t scale_signed(std::int64_t operand,
                          std::int64_t factor,
                          std::size_t offset) {
    if (operand == 0 || factor == 0) {
        return 0;
    }
    const bool negative = (operand < 0) != (factor < 0);
    const std::uint64_t operand_magnitude = unsigned_magnitude(operand);
    const std::uint64_t factor_magnitude = unsigned_magnitude(factor);
    const std::uint64_t limit =
        negative ? (std::uint64_t{1} << 63U)
                 : static_cast<std::uint64_t>(
                       std::numeric_limits<std::int64_t>::max());
    if (operand_magnitude > limit / factor_magnitude) {
        fail(ErrorCode::arithmetic_overflow,
             "signed CFI offset overflows 64 bits",
             offset);
    }
    return signed_product(operand_magnitude * factor_magnitude, negative,
                          offset);
}

std::int64_t scale_unsigned(std::uint64_t operand,
                            std::int64_t factor,
                            std::size_t offset) {
    if (operand == 0 || factor == 0) {
        return 0;
    }
    const bool negative = factor < 0;
    const std::uint64_t factor_magnitude = unsigned_magnitude(factor);
    const std::uint64_t limit =
        negative ? (std::uint64_t{1} << 63U)
                 : static_cast<std::uint64_t>(
                       std::numeric_limits<std::int64_t>::max());
    if (operand > limit / factor_magnitude) {
        fail(ErrorCode::arithmetic_overflow,
             "CFI offset overflows 64 bits",
             offset);
    }
    return signed_product(operand * factor_magnitude, negative, offset);
}

std::int64_t negate_signed(std::int64_t value, std::size_t offset) {
    if (value == std::numeric_limits<std::int64_t>::min()) {
        fail(ErrorCode::arithmetic_overflow,
             "negated CFI offset overflows 64 bits",
             offset);
    }
    return -value;
}

std::int64_t uleb_as_offset(std::uint64_t value, std::size_t offset) {
    if (value > static_cast<std::uint64_t>(
                    std::numeric_limits<std::int64_t>::max())) {
        fail(ErrorCode::arithmetic_overflow,
             "CFI offset does not fit in a signed 64-bit value",
             offset);
    }
    return static_cast<std::int64_t>(value);
}

struct StateSnapshot {
    CfaRule cfa;
    RegisterRules registers;
};

class Machine {
public:
    Machine(const Cie& cie, const EvalOptions& options)
        : code_alignment_(cie.code_alignment_factor),
          data_alignment_(cie.data_alignment_factor),
          address_size_(options.address_size == 0
                            ? normalized_address_size(cie.address_size)
                            : normalized_address_size(options.address_size)),
          little_endian_(options.little_endian) {}

    void set_state(const CfaRule& cfa, const RegisterRules& registers) {
        cfa_ = cfa;
        registers_ = registers;
        stack_.clear();
    }

    void set_restore_base(const RegisterRules* registers) {
        restore_base_ = registers;
    }

    void set_location(std::uint64_t location) { location_ = location; }
    std::uint64_t location() const noexcept { return location_; }
    const CfaRule& cfa() const noexcept { return cfa_; }
    const RegisterRules& registers() const noexcept { return registers_; }

    void set_return_address_register(std::uint64_t value) {
        return_address_register_ = value;
    }

    FrameRow make_row(std::uint64_t location) const {
        FrameRow row;
        row.location = location;
        row.cfa = cfa_;
        row.registers = registers_;
        row.return_address_register = return_address_register_;
        return row;
    }

    void execute(std::span<const std::uint8_t> instructions,
                 std::uint64_t instruction_base,
                 std::optional<std::uint64_t> stop_pc,
                 bool limit_rows,
                 std::uint64_t range_start,
                 std::uint64_t range_size,
                 std::vector<FrameRow>* rows) {
        Reader reader(instructions, 0, instructions.size(), little_endian_,
                      static_cast<std::size_t>(instruction_base));

        auto in_range = [&](std::uint64_t location) {
            return !limit_rows ||
                   (location >= range_start &&
                    (location - range_start) < range_size);
        };

        auto emit = [&](std::uint64_t location) {
            if (rows != nullptr) {
                rows->push_back(make_row(location));
            }
        };

        bool stopped = false;
        while (!reader.empty()) {
            const std::uint8_t opcode = reader.u8();
            const std::uint8_t primary = opcode & 0xc0U;

            if (primary == DW_CFA_advance_loc) {
                const std::uint64_t delta = opcode & 0x3fU;
                if (!advance_location(delta, reader.report_offset() - 1,
                                      stop_pc, in_range, emit, rows, stopped)) {
                    break;
                }
                continue;
            }
            if (primary == DW_CFA_offset) {
                const std::uint64_t reg = opcode & 0x3fU;
                const std::uint64_t raw = reader.uleb();
                set_offset(reg, scale_unsigned(raw, data_alignment_,
                                               reader.report_offset()));
                continue;
            }
            if (primary == DW_CFA_restore) {
                restore_register(opcode & 0x3fU);
                continue;
            }

            switch (opcode) {
            case DW_CFA_nop:
                break;
            case DW_CFA_set_loc: {
                const std::uint64_t new_location = reader.uint(address_size_);
                if (!change_location(new_location, stop_pc, in_range, emit,
                                     rows, stopped)) {
                    return;
                }
                break;
            }
            case DW_CFA_advance_loc1: {
                const std::uint64_t delta = reader.uint(1);
                if (!advance_location(delta, reader.report_offset() - 1,
                                      stop_pc, in_range, emit, rows, stopped)) {
                    return;
                }
                break;
            }
            case DW_CFA_advance_loc2: {
                const std::uint64_t delta = reader.uint(2);
                if (!advance_location(delta, reader.report_offset() - 2,
                                      stop_pc, in_range, emit, rows, stopped)) {
                    return;
                }
                break;
            }
            case DW_CFA_advance_loc4: {
                const std::uint64_t delta = reader.uint(4);
                if (!advance_location(delta, reader.report_offset() - 4,
                                      stop_pc, in_range, emit, rows, stopped)) {
                    return;
                }
                break;
            }
            case DW_CFA_offset_extended: {
                const std::uint64_t reg = reader.uleb();
                const std::uint64_t raw = reader.uleb();
                set_offset(reg, scale_unsigned(raw, data_alignment_,
                                               reader.report_offset()));
                break;
            }
            case DW_CFA_restore_extended:
                restore_register(reader.uleb());
                break;
            case DW_CFA_undefined:
                set_undefined(reader.uleb());
                break;
            case DW_CFA_same_value:
                set_same_value(reader.uleb());
                break;
            case DW_CFA_register: {
                const std::uint64_t reg = reader.uleb();
                const std::uint64_t other = reader.uleb();
                RegisterRule rule;
                rule.kind = RegisterRuleKind::reg;
                rule.reg = other;
                registers_[reg] = std::move(rule);
                break;
            }
            case DW_CFA_remember_state:
                stack_.push_back(StateSnapshot{cfa_, registers_});
                break;
            case DW_CFA_restore_state:
                if (stack_.empty()) {
                    fail(ErrorCode::state_stack_underflow,
                         "DW_CFA_restore_state with an empty state stack",
                         reader.report_offset() - 1);
                }
                cfa_ = stack_.back().cfa;
                registers_ = stack_.back().registers;
                stack_.pop_back();
                break;
            case DW_CFA_def_cfa: {
                const std::uint64_t reg = reader.uleb();
                const std::uint64_t raw = reader.uleb();
                cfa_.kind = CfaRuleKind::reg_offset;
                cfa_.reg = reg;
                cfa_.offset = uleb_as_offset(raw, reader.report_offset());
                cfa_.expression.clear();
                break;
            }
            case DW_CFA_def_cfa_register: {
                const std::uint64_t reg = reader.uleb();
                if (cfa_.kind != CfaRuleKind::reg_offset) {
                    cfa_.offset = 0;
                }
                cfa_.kind = CfaRuleKind::reg_offset;
                cfa_.reg = reg;
                cfa_.expression.clear();
                break;
            }
            case DW_CFA_def_cfa_offset: {
                const std::uint64_t raw = reader.uleb();
                if (cfa_.kind != CfaRuleKind::reg_offset) {
                    cfa_.reg = 0;
                }
                cfa_.kind = CfaRuleKind::reg_offset;
                cfa_.offset = uleb_as_offset(raw, reader.report_offset());
                cfa_.expression.clear();
                break;
            }
            case DW_CFA_def_cfa_expression: {
                const std::uint64_t length = reader.uleb();
                const auto block = reader.bytes(static_cast<std::size_t>(length));
                cfa_.kind = CfaRuleKind::expression;
                cfa_.reg = 0;
                cfa_.offset = 0;
                cfa_.expression.assign(block.begin(), block.end());
                break;
            }
            case DW_CFA_expression: {
                const std::uint64_t reg = reader.uleb();
                const std::uint64_t length = reader.uleb();
                const auto block = reader.bytes(static_cast<std::size_t>(length));
                RegisterRule rule;
                rule.kind = RegisterRuleKind::expression;
                rule.expression.assign(block.begin(), block.end());
                registers_[reg] = std::move(rule);
                break;
            }
            case DW_CFA_offset_extended_sf: {
                const std::uint64_t reg = reader.uleb();
                const std::int64_t raw = reader.sleb();
                set_offset(reg, scale_signed(raw, data_alignment_,
                                             reader.report_offset()));
                break;
            }
            case DW_CFA_def_cfa_sf: {
                const std::uint64_t reg = reader.uleb();
                const std::int64_t raw = reader.sleb();
                cfa_.kind = CfaRuleKind::reg_offset;
                cfa_.reg = reg;
                cfa_.offset = scale_signed(raw, data_alignment_,
                                           reader.report_offset());
                cfa_.expression.clear();
                break;
            }
            case DW_CFA_def_cfa_offset_sf: {
                const std::int64_t raw = reader.sleb();
                if (cfa_.kind != CfaRuleKind::reg_offset) {
                    cfa_.reg = 0;
                }
                cfa_.kind = CfaRuleKind::reg_offset;
                cfa_.offset = scale_signed(raw, data_alignment_,
                                           reader.report_offset());
                cfa_.expression.clear();
                break;
            }
            case DW_CFA_val_offset: {
                const std::uint64_t reg = reader.uleb();
                const std::uint64_t raw = reader.uleb();
                set_val_offset(reg, scale_unsigned(raw, data_alignment_,
                                                   reader.report_offset()));
                break;
            }
            case DW_CFA_val_offset_sf: {
                const std::uint64_t reg = reader.uleb();
                const std::int64_t raw = reader.sleb();
                set_val_offset(reg, scale_signed(raw, data_alignment_,
                                                 reader.report_offset()));
                break;
            }
            case DW_CFA_val_expression: {
                const std::uint64_t reg = reader.uleb();
                const std::uint64_t length = reader.uleb();
                const auto block = reader.bytes(static_cast<std::size_t>(length));
                RegisterRule rule;
                rule.kind = RegisterRuleKind::val_expression;
                rule.expression.assign(block.begin(), block.end());
                registers_[reg] = std::move(rule);
                break;
            }
            case DW_CFA_GNU_args_size:
                (void)reader.uleb();
                break;
            case DW_CFA_GNU_window_save:
                // The state change is architecture specific (SPARC window
                // registers / AArch64 return-address signing).  It does not
                // alter the generic CFA rule or the DWARF register table.
                break;
            case DW_CFA_GNU_negative_offset_extended: {
                const std::uint64_t reg = reader.uleb();
                const std::uint64_t raw = reader.uleb();
                const std::int64_t value =
                    scale_unsigned(raw, data_alignment_, reader.report_offset());
                set_offset(reg, negate_signed(value, reader.report_offset()));
                break;
            }
            default:
                fail(ErrorCode::unknown_opcode,
                     "unknown CFA opcode " + hex_byte(opcode),
                     reader.report_offset() - 1);
            }
        }

        if (rows != nullptr && !stopped && in_range(location_)) {
            emit(location_);
        }
    }

private:
    template <typename InRange, typename Emit>
    bool advance_location(std::uint64_t delta,
                          std::size_t opcode_offset,
                          std::optional<std::uint64_t> stop_pc,
                          InRange&& in_range,
                          Emit&& emit,
                          std::vector<FrameRow>* rows,
                          bool& stopped) {
        std::uint64_t scaled = 0;
        if (!checked_mul(delta, code_alignment_, scaled)) {
            fail(ErrorCode::arithmetic_overflow,
                 "DW_CFA_advance_loc delta overflows 64 bits",
                 opcode_offset);
        }
        std::uint64_t new_location = 0;
        if (!checked_add(location_, scaled, new_location)) {
            fail(ErrorCode::arithmetic_overflow,
                 "DW_CFA_advance_loc address overflows 64 bits",
                 opcode_offset);
        }
        if (!change_location(new_location, stop_pc, in_range, emit, rows,
                             stopped)) {
            return false;
        }
        return true;
    }

    template <typename InRange, typename Emit>
    bool change_location(std::uint64_t new_location,
                         std::optional<std::uint64_t> stop_pc,
                         InRange&& in_range,
                         Emit&& emit,
                         std::vector<FrameRow>* rows,
                         bool& stopped) {
        if (stop_pc.has_value() && new_location > *stop_pc) {
            stopped = true;
            return false;
        }

        if (rows != nullptr && new_location != location_) {
            emit(location_);
            if (!in_range(new_location)) {
                stopped = true;
                return false;
            }
        } else if (rows == nullptr && !in_range(new_location)) {
            stopped = true;
            return false;
        }

        location_ = new_location;
        return true;
    }

    void set_offset(std::uint64_t reg, std::int64_t offset) {
        RegisterRule rule;
        rule.kind = RegisterRuleKind::offset;
        rule.offset = offset;
        registers_[reg] = std::move(rule);
    }

    void set_val_offset(std::uint64_t reg, std::int64_t offset) {
        RegisterRule rule;
        rule.kind = RegisterRuleKind::val_offset;
        rule.offset = offset;
        registers_[reg] = std::move(rule);
    }

    void set_undefined(std::uint64_t reg) {
        RegisterRule rule;
        rule.kind = RegisterRuleKind::undefined;
        registers_[reg] = std::move(rule);
    }

    void set_same_value(std::uint64_t reg) {
        RegisterRule rule;
        rule.kind = RegisterRuleKind::same_value;
        registers_[reg] = std::move(rule);
    }

    void restore_register(std::uint64_t reg) {
        if (restore_base_ == nullptr) {
            registers_.erase(reg);
            return;
        }
        const auto it = restore_base_->find(reg);
        if (it == restore_base_->end()) {
            registers_.erase(reg);
        } else {
            registers_[reg] = it->second;
        }
    }

    std::uint64_t code_alignment_;
    std::int64_t data_alignment_;
    std::uint8_t address_size_;
    bool little_endian_;
    std::uint64_t location_ = 0;
    CfaRule cfa_;
    RegisterRules registers_;
    std::vector<StateSnapshot> stack_;
    const RegisterRules* restore_base_ = nullptr;
    std::uint64_t return_address_register_ = 0;
};

}  // namespace

const char* error_code_name(ErrorCode code) noexcept {
    switch (code) {
    case ErrorCode::none:
        return "none";
    case ErrorCode::truncated_input:
        return "truncated_input";
    case ErrorCode::leb128_overflow:
        return "leb128_overflow";
    case ErrorCode::leb128_too_long:
        return "leb128_too_long";
    case ErrorCode::invalid_length:
        return "invalid_length";
    case ErrorCode::invalid_cie_id:
        return "invalid_cie_id";
    case ErrorCode::unsupported_version:
        return "unsupported_version";
    case ErrorCode::invalid_augmentation:
        return "invalid_augmentation";
    case ErrorCode::invalid_pointer_encoding:
        return "invalid_pointer_encoding";
    case ErrorCode::missing_cie:
        return "missing_cie";
    case ErrorCode::invalid_fde:
        return "invalid_fde";
    case ErrorCode::pc_out_of_range:
        return "pc_out_of_range";
    case ErrorCode::state_stack_underflow:
        return "state_stack_underflow";
    case ErrorCode::unknown_opcode:
        return "unknown_opcode";
    case ErrorCode::invalid_operand:
        return "invalid_operand";
    case ErrorCode::arithmetic_overflow:
        return "arithmetic_overflow";
    }
    return "unknown";
}

CfiError::CfiError(ErrorCode code, std::string message, std::size_t offset)
    : std::runtime_error(std::string(error_code_name(code)) + ": " + message +
                         " (at offset " + std::to_string(offset) + ")"),
      code_(code),
      offset_(offset) {}

Uleb128 decode_uleb128(std::span<const std::uint8_t> input) {
    std::size_t index = 0;
    const std::uint64_t value = read_uleb_raw(input, index, 0);
    return Uleb128{value, index};
}

Sleb128 decode_sleb128(std::span<const std::uint8_t> input) {
    std::size_t index = 0;
    const std::int64_t value = read_sleb_raw(input, index, 0);
    return Sleb128{value, index};
}

bool try_decode_uleb128(std::span<const std::uint8_t> input,
                        std::uint64_t& value,
                        std::size_t& consumed,
                        ErrorCode* error,
                        std::string* message) noexcept {
    try {
        const Uleb128 result = decode_uleb128(input);
        value = result.value;
        consumed = result.length;
        if (error != nullptr) {
            *error = ErrorCode::none;
        }
        if (message != nullptr) {
            message->clear();
        }
        return true;
    } catch (const CfiError& caught) {
        if (error != nullptr) {
            *error = caught.code();
        }
        if (message != nullptr) {
            *message = caught.what();
        }
        consumed = 0;
        return false;
    } catch (...) {
        if (error != nullptr) {
            *error = ErrorCode::invalid_operand;
        }
        if (message != nullptr) {
            *message = "unknown error while decoding ULEB128";
        }
        consumed = 0;
        return false;
    }
}

bool try_decode_sleb128(std::span<const std::uint8_t> input,
                        std::int64_t& value,
                        std::size_t& consumed,
                        ErrorCode* error,
                        std::string* message) noexcept {
    try {
        const Sleb128 result = decode_sleb128(input);
        value = result.value;
        consumed = result.length;
        if (error != nullptr) {
            *error = ErrorCode::none;
        }
        if (message != nullptr) {
            message->clear();
        }
        return true;
    } catch (const CfiError& caught) {
        if (error != nullptr) {
            *error = caught.code();
        }
        if (message != nullptr) {
            *message = caught.what();
        }
        consumed = 0;
        return false;
    } catch (...) {
        if (error != nullptr) {
            *error = ErrorCode::invalid_operand;
        }
        if (message != nullptr) {
            *message = "unknown error while decoding SLEB128";
        }
        consumed = 0;
        return false;
    }
}

std::vector<std::uint8_t> encode_uleb128(std::uint64_t value) {
    std::vector<std::uint8_t> result;
    do {
        std::uint8_t byte = static_cast<std::uint8_t>(value & 0x7fU);
        value >>= 7U;
        if (value != 0) {
            byte |= 0x80U;
        }
        result.push_back(byte);
    } while (value != 0);
    return result;
}

std::vector<std::uint8_t> encode_sleb128(std::int64_t value) {
    std::vector<std::uint8_t> result;
    bool more = true;
    while (more) {
        std::uint8_t byte = static_cast<std::uint8_t>(value & 0x7f);
        const bool sign_set = (byte & 0x40U) != 0;
        value >>= 7;
        if ((value == 0 && !sign_set) || (value == -1 && sign_set)) {
            more = false;
        } else {
            byte |= 0x80U;
        }
        result.push_back(byte);
    }
    return result;
}

Cie parse_cie(std::span<const std::uint8_t> entry,
              const ParseOptions& options) {
    const EntryHeader header =
        read_header(entry, 0, options.little_endian, options.section_offset);
    if (header.length == 0) {
        fail(ErrorCode::invalid_length,
             "empty CFI entry is not a CIE",
             options.section_offset);
    }
    Reader reader(entry, header.content_start, header.end,
                  options.little_endian, options.section_offset);
    const std::uint64_t id = reader.uint(header.offset_size);

    SectionKind kind = options.kind;
    if (kind == SectionKind::auto_detect) {
        kind = detect_kind(id, header.offset_size);
        if (kind == SectionKind::auto_detect) {
            fail(ErrorCode::invalid_cie_id,
                 "entry does not contain a CIE identifier",
                 reader.report_offset() - header.offset_size);
        }
    }
    return parse_cie_body(reader, id, header, kind, options);
}

Fde parse_fde(std::span<const std::uint8_t> entry,
              const Cie& cie,
              const ParseOptions& options) {
    const EntryHeader header =
        read_header(entry, 0, options.little_endian, options.section_offset);
    if (header.length == 0) {
        fail(ErrorCode::invalid_length,
             "empty CFI entry is not an FDE",
             options.section_offset);
    }
    Reader reader(entry, header.content_start, header.end,
                  options.little_endian, options.section_offset);
    const std::uint64_t cie_pointer = reader.uint(header.offset_size);

    SectionKind kind = options.kind;
    if (kind == SectionKind::auto_detect) {
        kind = cie.kind == SectionKind::auto_detect
                   ? SectionKind::eh_frame
                   : cie.kind;
    }

    Fde fde = parse_fde_body(reader, cie_pointer, header, cie, kind, options);
    if (kind == SectionKind::eh_frame) {
        if (cie_pointer > header.content_start) {
            fail(ErrorCode::invalid_fde,
                 "FDE CIE pointer points before the section",
                 reader.report_offset() - header.offset_size);
        }
        fde.cie_offset =
            options.section_offset + (header.content_start - cie_pointer);
    } else {
        fde.cie_offset = cie_pointer;
    }
    return fde;
}

CfiSection parse_cfi_section(std::span<const std::uint8_t> section,
                             const ParseOptions& options) {
    CfiSection result;
    SectionKind kind = options.kind;
    std::map<std::uint64_t, std::size_t> cie_offsets;
    cie_offsets.clear();

    struct RawFde {
        std::size_t start = 0;
        EntryHeader header;
        std::uint64_t cie_pointer = 0;
    };
    std::vector<RawFde> raw_fdes;

    std::size_t position = 0;
    while (position < section.size()) {
        if (section.size() - position < 4) {
            const bool only_zeroes = std::all_of(
                section.begin() + static_cast<std::ptrdiff_t>(position),
                section.end(),
                [](std::uint8_t byte) { return byte == 0; });
            if (only_zeroes) {
                break;
            }
            fail(ErrorCode::truncated_input,
                 "trailing bytes are shorter than a CFI entry header",
                 options.section_offset + position);
        }

        const EntryHeader header =
            read_header(section, position, options.little_endian,
                        options.section_offset);
        if (header.length == 0) {
            break;
        }

        Reader reader(section, header.content_start, header.end,
                      options.little_endian, options.section_offset);
        const std::uint64_t id = reader.uint(header.offset_size);

        bool is_cie = false;
        if (kind == SectionKind::auto_detect) {
            const SectionKind detected = detect_kind(id, header.offset_size);
            if (detected != SectionKind::auto_detect) {
                kind = detected;
                is_cie = true;
            } else {
                // A section starting with an FDE is unusual; .eh_frame's
                // relative CIE pointers are by far the more common form.
                kind = SectionKind::eh_frame;
                is_cie = false;
            }
        } else if (kind == SectionKind::eh_frame) {
            is_cie = id == 0;
        } else {
            is_cie = id == all_ones(header.offset_size);
        }

        if (is_cie) {
            Cie cie = parse_cie_body(reader, id, header, kind, options);
            cie_offsets.emplace(header.start, result.cies.size());
            result.cies.push_back(std::move(cie));
        } else {
            raw_fdes.push_back(RawFde{position, header, id});
        }
        position = header.end;
    }

    for (const RawFde& raw : raw_fdes) {
        std::uint64_t relative_cie_offset = 0;
        if (kind == SectionKind::eh_frame) {
            if (raw.cie_pointer > raw.header.content_start) {
                fail(ErrorCode::invalid_fde,
                     "FDE CIE pointer points before the section",
                     options.section_offset + raw.header.content_start);
            }
            relative_cie_offset = raw.header.content_start - raw.cie_pointer;
        } else {
            if (raw.cie_pointer < options.section_offset) {
                fail(ErrorCode::invalid_fde,
                     "FDE CIE pointer lies before the parsed section slice",
                     options.section_offset + raw.start);
            }
            relative_cie_offset = raw.cie_pointer - options.section_offset;
        }

        auto cie_it = cie_offsets.find(relative_cie_offset);
        if (cie_it == cie_offsets.end()) {
            fail(ErrorCode::missing_cie,
                 "FDE references a CIE that is not present in the section",
                 options.section_offset + raw.start);
        }
        const Cie& cie = result.cies[cie_it->second];

        Reader reader(section, raw.header.content_start, raw.header.end,
                      options.little_endian, options.section_offset);
        (void)reader.uint(raw.header.offset_size);
        Fde fde = parse_fde_body(reader, raw.cie_pointer, raw.header, cie,
                                 kind, options);
        fde.cie_offset = options.section_offset + relative_cie_offset;
        fde.cie_index = cie_it->second;
        result.fdes.push_back(std::move(fde));
    }
    return result;
}

const Cie* CfiSection::find_cie(std::uint64_t offset) const noexcept {
    for (const Cie& cie : cies) {
        if (cie.offset == offset) {
            return &cie;
        }
    }
    return nullptr;
}

const Cie* CfiSection::cie_for(const Fde& fde) const noexcept {
    if (fde.cie_index < cies.size()) {
        return &cies[fde.cie_index];
    }
    return find_cie(fde.cie_offset);
}

const Fde* CfiSection::find_fde(std::uint64_t pc) const noexcept {
    for (const Fde& fde : fdes) {
        if (fde.contains(pc)) {
            return &fde;
        }
    }
    return nullptr;
}

FrameRow CfiSection::lookup(std::uint64_t pc) const {
    const Fde* fde = find_fde(pc);
    if (fde == nullptr) {
        fail(ErrorCode::pc_out_of_range,
             "no FDE covers the requested PC",
             pc);
    }
    const Cie* cie = cie_for(*fde);
    if (cie == nullptr) {
        fail(ErrorCode::missing_cie,
             "FDE does not have a matching CIE",
             fde->offset);
    }
    return FrameTable(*cie, *fde).lookup(pc);
}

FrameTable::FrameTable(const Cie& cie,
                       const Fde& fde,
                       const EvalOptions& options)
    : cie_(cie), fde_(fde), options_(options) {
    if (options_.address_size == 0) {
        options_.address_size =
            cie_.address_size == 0 ? 8 : cie_.address_size;
    }

    Machine machine(cie_, options_);
    machine.set_location(fde_.initial_location);
    machine.set_return_address_register(cie_.return_address_register);
    machine.execute(cie_.initial_instructions,
                    cie_.initial_instructions_offset,
                    std::nullopt,
                    false,
                    0,
                    0,
                    nullptr);

    initial_row_.location = fde_.initial_location;
    initial_row_.cfa = machine.cfa();
    initial_row_.registers = machine.registers();
    initial_row_.return_address_register = cie_.return_address_register;
}

bool FrameTable::try_lookup(std::uint64_t pc,
                            FrameRow& row,
                            ErrorCode* error,
                            std::string* message) const noexcept {
    try {
        row = lookup(pc);
        if (error != nullptr) {
            *error = ErrorCode::none;
        }
        if (message != nullptr) {
            message->clear();
        }
        return true;
    } catch (const CfiError& caught) {
        if (error != nullptr) {
            *error = caught.code();
        }
        if (message != nullptr) {
            *message = caught.what();
        }
        return false;
    } catch (...) {
        if (error != nullptr) {
            *error = ErrorCode::invalid_operand;
        }
        if (message != nullptr) {
            *message = "unknown error while evaluating CFI";
        }
        return false;
    }
}

FrameRow FrameTable::lookup(std::uint64_t pc) const {
    if (fde_.address_range == 0 || pc < fde_.initial_location ||
        (pc - fde_.initial_location) >= fde_.address_range) {
        fail(ErrorCode::pc_out_of_range,
             "PC is outside the FDE address range",
             pc);
    }

    Machine machine(cie_, options_);
    machine.set_state(initial_row_.cfa, initial_row_.registers);
    machine.set_restore_base(&initial_row_.registers);
    machine.set_return_address_register(cie_.return_address_register);
    machine.set_location(fde_.initial_location);
    machine.execute(fde_.instructions,
                    fde_.instructions_offset,
                    pc,
                    false,
                    0,
                    0,
                    nullptr);
    return machine.make_row(machine.location());
}

std::vector<FrameRow> FrameTable::rows() const {
    std::vector<FrameRow> result;
    if (fde_.address_range == 0) {
        return result;
    }

    Machine machine(cie_, options_);
    machine.set_state(initial_row_.cfa, initial_row_.registers);
    machine.set_restore_base(&initial_row_.registers);
    machine.set_return_address_register(cie_.return_address_register);
    machine.set_location(fde_.initial_location);
    machine.execute(fde_.instructions,
                    fde_.instructions_offset,
                    std::nullopt,
                    true,
                    fde_.initial_location,
                    fde_.address_range,
                    &result);
    return result;
}

FrameRow evaluate_frame(const Cie& cie,
                        const Fde& fde,
                        std::uint64_t pc,
                        const EvalOptions& options) {
    return FrameTable(cie, fde, options).lookup(pc);
}

std::vector<FrameRow> evaluate_frame_rows(const Cie& cie,
                                          const Fde& fde,
                                          const EvalOptions& options) {
    return FrameTable(cie, fde, options).rows();
}

}  // namespace dwarf::cfi
