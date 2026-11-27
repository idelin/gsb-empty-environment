#include "ehframe/validator.hpp"

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <cstring>
#include <limits>
#include <new>
#include <utility>
#include <vector>

namespace ehframe {
namespace {

constexpr std::uint64_t kMaxU64 = std::numeric_limits<std::uint64_t>::max();
constexpr std::int64_t kMaxI64 = std::numeric_limits<std::int64_t>::max();

class Cursor {
public:
    Cursor(const std::uint8_t* data, std::size_t begin, std::size_t end, bool big_endian)
        : data_(data), pos_(begin), end_(end), big_endian_(big_endian) {}

    [[nodiscard]] std::size_t position() const noexcept {
        return pos_;
    }

    [[nodiscard]] std::size_t end() const noexcept {
        return end_;
    }

    [[nodiscard]] std::size_t remaining() const noexcept {
        return end_ - pos_;
    }

    [[nodiscard]] bool empty() const noexcept {
        return pos_ == end_;
    }

    bool read_u8(std::uint8_t& value) {
        if (pos_ >= end_) {
            return false;
        }
        value = data_[pos_];
        ++pos_;
        return true;
    }

    bool read_uint(std::size_t width, std::uint64_t& value) {
        if (width == 0 || width > 8 || width > remaining()) {
            return false;
        }
        std::uint64_t result = 0;
        for (std::size_t index = 0; index < width; ++index) {
            const std::uint8_t byte = data_[pos_ + index];
            if (big_endian_) {
                result = (result << 8U) | static_cast<std::uint64_t>(byte);
            } else {
                const unsigned int shift = static_cast<unsigned int>(index * 8U);
                result |= static_cast<std::uint64_t>(byte) << shift;
            }
        }
        pos_ += width;
        value = result;
        return true;
    }

    bool read_uleb(std::uint64_t& value) {
        std::uint64_t result = 0;
        unsigned int shift = 0;
        std::size_t count = 0;

        for (;;) {
            if (pos_ >= end_) {
                return false;
            }
            const std::uint8_t byte = data_[pos_];
            ++pos_;
            ++count;

            if (count == 10 && ((byte & 0x80U) != 0U || (byte & 0x7fU) > 1U)) {
                return false;
            }
            if (count > 10) {
                return false;
            }

            result |= static_cast<std::uint64_t>(byte & 0x7fU) << shift;
            if ((byte & 0x80U) == 0U) {
                if (count > 1 && (byte & 0x7fU) == 0U) {
                    return false;
                }
                value = result;
                return true;
            }
            shift += 7U;
        }
    }

    bool read_sleb(std::int64_t& value) {
        const std::size_t start = pos_;
        std::uint64_t raw = 0;
        unsigned int shift = 0;
        std::size_t count = 0;

        for (;;) {
            if (pos_ >= end_) {
                return false;
            }
            const std::uint8_t byte = data_[pos_];
            ++pos_;
            ++count;

            if (count == 10) {
                if ((byte & 0x80U) != 0U) {
                    return false;
                }
                const std::uint8_t payload = static_cast<std::uint8_t>(byte & 0x7fU);
                if (payload != 0x00U && payload != 0x7fU) {
                    return false;
                }
                raw |= static_cast<std::uint64_t>(payload & 0x01U) << 63U;
                break;
            }

            raw |= static_cast<std::uint64_t>(byte & 0x7fU) << shift;
            shift += 7U;
            if ((byte & 0x80U) == 0U) {
                if ((byte & 0x40U) != 0U && shift < 64U) {
                    raw |= (~std::uint64_t{0}) << shift;
                }
                break;
            }
        }

        std::int64_t signed_value = 0;
        if ((raw & (std::uint64_t{1} << 63U)) == 0U) {
            signed_value = static_cast<std::int64_t>(raw);
        } else {
            const std::uint64_t inverted = ~raw;
            signed_value = -1 - static_cast<std::int64_t>(inverted);
        }

        std::uint8_t canonical[10] = {};
        const std::size_t canonical_size = encode_sleb(signed_value, canonical);
        const std::size_t encoded_size = pos_ - start;
        if (canonical_size != encoded_size ||
            std::memcmp(canonical, data_ + start, encoded_size) != 0) {
            return false;
        }

        value = signed_value;
        return true;
    }

    bool skip(std::size_t count) {
        if (count > remaining()) {
            return false;
        }
        pos_ += count;
        return true;
    }

    bool align_to(std::size_t alignment) {
        if (alignment == 0) {
            return false;
        }
        const std::size_t remainder = pos_ % alignment;
        if (remainder == 0) {
            return true;
        }
        return skip(alignment - remainder);
    }

private:
    static std::size_t encode_sleb(std::int64_t value, std::uint8_t out[10]) {
        std::size_t count = 0;
        bool more = true;
        while (more) {
            std::uint8_t byte = static_cast<std::uint8_t>(value & 0x7f);
            value >>= 7;
            if ((value == 0 && (byte & 0x40U) == 0U) ||
                (value == -1 && (byte & 0x40U) != 0U)) {
                more = false;
            } else {
                byte = static_cast<std::uint8_t>(byte | 0x80U);
            }
            out[count] = byte;
            ++count;
        }
        return count;
    }

    const std::uint8_t* data_;
    std::size_t pos_;
    std::size_t end_;
    bool big_endian_;
};

enum class RuleKind : std::uint8_t {
    undefined = 0,
    same_value,
    offset,
    val_offset,
    register_rule,
    expression,
    val_expression
};

struct Rule {
    RuleKind kind = RuleKind::undefined;
    std::uint32_t register_number = 0;
    std::int64_t offset = 0;
};

struct CfaRule {
    bool defined = false;
    bool expression = false;
    std::uint32_t register_number = 0;
    std::int64_t offset = 0;
    std::uint64_t value = 0;
};

struct State {
    CfaRule cfa{};
    std::vector<Rule> rules;
};

struct CieInfo {
    std::size_t record_start = 0;
    std::uint64_t code_alignment = 0;
    std::int64_t data_alignment = 0;
    std::uint32_t return_address_register = 0;
    std::uint8_t fde_encoding = 0;
    bool has_z_augmentation = false;
    State initial_state{};
};

struct CodeInterval {
    std::uint64_t begin = 0;
    std::uint64_t end = 0;
    std::size_t record_offset = 0;
};

bool add_signed(std::uint64_t base, std::int64_t delta, std::uint64_t& result) {
    if (delta >= 0) {
        const std::uint64_t magnitude = static_cast<std::uint64_t>(delta);
        if (base > kMaxU64 - magnitude) {
            return false;
        }
        result = base + magnitude;
        return true;
    }

    const std::uint64_t magnitude =
        static_cast<std::uint64_t>(-(delta + 1)) + std::uint64_t{1};
    if (base < magnitude) {
        return false;
    }
    result = base - magnitude;
    return true;
}

bool multiply_offset(std::uint64_t factor, std::int64_t alignment, std::int64_t& result) {
    if (factor == 0U || alignment == 0) {
        result = 0;
        return true;
    }

    const bool negative = alignment < 0;
    const std::uint64_t magnitude = negative
        ? static_cast<std::uint64_t>(-(alignment + 1)) + std::uint64_t{1}
        : static_cast<std::uint64_t>(alignment);
    if (factor > static_cast<std::uint64_t>(kMaxI64) / magnitude) {
        return false;
    }

    const std::uint64_t product = factor * magnitude;
    if (product > static_cast<std::uint64_t>(kMaxI64)) {
        return false;
    }
    if (negative) {
        result = -static_cast<std::int64_t>(product);
    } else {
        result = static_cast<std::int64_t>(product);
    }
    return true;
}

std::int64_t signed_from_bits(std::uint64_t bits) {
    if ((bits & (std::uint64_t{1} << 63U)) == 0U) {
        return static_cast<std::int64_t>(bits);
    }
    const std::uint64_t inverted = ~bits;
    return -1 - static_cast<std::int64_t>(inverted);
}

bool sign_extend(std::uint64_t& value, unsigned int bits) {
    if (bits == 0U || bits >= 64U) {
        return bits == 64U;
    }
    const std::uint64_t sign = std::uint64_t{1} << (bits - 1U);
    if ((value & sign) != 0U) {
        const std::uint64_t mask = (~std::uint64_t{0}) << bits;
        value |= mask;
    }
    return true;
}

bool is_deref_opcode(std::uint8_t opcode) {
    switch (opcode) {
        case 0x06:  // DW_OP_deref
        case 0x18:  // DW_OP_xderef
        case 0x94:  // DW_OP_deref_size
        case 0x95:  // DW_OP_xderef_size
        case 0xa6:  // DW_OP_deref_type
        case 0xa7:  // DW_OP_xderef_type
            return true;
        default:
            return false;
    }
}

class Validator {
public:
    Validator(const std::uint8_t* data, std::size_t size, const ValidationOptions& options)
        : data_(data), size_(size), options_(options) {}

    ValidationResult run() {
        if (!validate_options()) {
            return result();
        }
        if (data_ == nullptr || size_ == 0) {
            fail(ErrorCode::input_truncated, 0, "empty .eh_frame stream");
            return result();
        }

        std::size_t offset = 0;
        std::size_t record_count = 0;
        while (offset < size_) {
            if (record_count >= options_.max_records) {
                fail(ErrorCode::too_many_records, offset, "record limit exceeded");
                return result();
            }

            if (size_ - offset < 4U) {
                fail(ErrorCode::record_length_truncated, offset,
                     "truncated length field");
                return result();
            }

            Cursor header(data_, offset, size_, options_.big_endian);
            std::uint64_t length = 0;
            if (!header.read_uint(4U, length)) {
                fail(ErrorCode::record_length_truncated, offset,
                     "truncated length field");
                return result();
            }

            std::size_t header_size = 4U;
            std::size_t pointer_width = 4U;
            bool extended_length = false;
            if (length == 0xffffffffU) {
                extended_length = true;
                if (!header.read_uint(8U, length)) {
                    fail(ErrorCode::record_length_truncated, offset,
                         "truncated extended length field");
                    return result();
                }
                header_size = 12U;
                pointer_width = 8U;
            }

            if (length == 0U) {
                if (extended_length) {
                    fail(ErrorCode::record_length_invalid, offset,
                         "extended record length is zero");
                    return result();
                }
                for (std::size_t index = offset + header_size; index < size_; ++index) {
                    if (data_[index] != 0U) {
                        fail(ErrorCode::trailing_data_after_terminator, index,
                             "non-zero data after .eh_frame terminator");
                        return result();
                    }
                }
                break;
            }

            if (header_size > size_ - offset) {
                fail(ErrorCode::record_length_overflow, offset,
                     "record header exceeds section");
                return result();
            }
            const std::size_t content_offset = offset + header_size;
            const std::uint64_t available =
                static_cast<std::uint64_t>(size_ - content_offset);
            if (length > available) {
                fail(ErrorCode::record_length_overflow, offset,
                     "record length exceeds section");
                return result();
            }
            if (length < static_cast<std::uint64_t>(pointer_width)) {
                fail(ErrorCode::record_too_short, offset,
                     "record is too short for its CIE identifier");
                return result();
            }

            const std::size_t record_end =
                content_offset + static_cast<std::size_t>(length);
            Cursor identifier(data_, content_offset, record_end, options_.big_endian);
            std::uint64_t cie_identifier = 0;
            if (!identifier.read_uint(pointer_width, cie_identifier)) {
                fail(ErrorCode::record_too_short, offset,
                     "truncated CIE identifier");
                return result();
            }

            bool ok = false;
            if (cie_identifier == 0U) {
                ok = parse_cie(offset, content_offset, pointer_width, record_end);
            } else {
                ok = parse_fde(offset, content_offset, pointer_width, record_end,
                               cie_identifier);
            }
            if (!ok) {
                return result();
            }

            ++record_count;
            offset = record_end;
        }

        if (!check_overlaps()) {
            return result();
        }
        return result();
    }

private:
    ValidationResult result() const {
        ValidationResult value;
        value.error = error_;
        return value;
    }

    bool fail(ErrorCode code, std::size_t offset, const char* message) {
        if (error_.code == ErrorCode::none) {
            error_ = ValidationError{code, offset, message};
        }
        return false;
    }

    bool validate_options() {
        if (options_.frame.low >= options_.frame.high) {
            return fail(ErrorCode::invalid_options, 0, "invalid frame bounds");
        }
        if (options_.frame.stack_pointer < options_.frame.low ||
            options_.frame.stack_pointer >= options_.frame.high) {
            return fail(ErrorCode::invalid_options, 0,
                        "stack pointer is outside frame bounds");
        }
        if (options_.address_size != 2U && options_.address_size != 4U &&
            options_.address_size != 8U) {
            return fail(ErrorCode::invalid_options, 0, "unsupported address size");
        }
        if (options_.max_register > 65535U) {
            return fail(ErrorCode::invalid_options, 0, "register limit is too large");
        }
        if (options_.expected_return_address_register !=
                std::numeric_limits<std::uint32_t>::max() &&
            options_.expected_return_address_register > options_.max_register) {
            return fail(ErrorCode::invalid_options, 0,
                        "expected return-address register is not permitted");
        }
        if (options_.stack_pointer_register > options_.max_register) {
            return fail(ErrorCode::invalid_options, 0,
                        "stack-pointer register is not permitted");
        }
        if (options_.max_state_stack == 0U || options_.max_expression_steps == 0U ||
            options_.max_expression_depth == 0U || options_.max_records == 0U ||
            options_.max_instructions == 0U) {
            return fail(ErrorCode::invalid_options, 0, "a resource limit is zero");
        }
        if (options_.max_expression_depth > 65536U) {
            return fail(ErrorCode::invalid_options, 0,
                        "expression depth limit is too large");
        }
        if (options_.register_value_count > options_.register_values.size()) {
            return fail(ErrorCode::invalid_options, 0,
                        "register value count exceeds storage");
        }
        for (std::size_t index = 0; index < options_.register_value_count; ++index) {
            if (options_.register_values[index].number > options_.max_register) {
                return fail(ErrorCode::invalid_options, 0,
                            "register value uses an invalid register");
            }
            for (std::size_t other = index + 1;
                 other < options_.register_value_count; ++other) {
                if (options_.register_values[index].number ==
                    options_.register_values[other].number) {
                    return fail(ErrorCode::invalid_options, 0,
                                "duplicate register value");
                }
            }
        }
        return true;
    }

    State make_state() const {
        State state;
        state.rules.assign(static_cast<std::size_t>(options_.max_register) + 1U, Rule{});
        return state;
    }

    bool read_uleb(Cursor& cursor, std::uint64_t& value, std::size_t offset) {
        if (!cursor.read_uleb(value)) {
            return fail(ErrorCode::malformed_leb128, offset, "malformed ULEB128");
        }
        return true;
    }

    bool read_sleb(Cursor& cursor, std::int64_t& value, std::size_t offset) {
        if (!cursor.read_sleb(value)) {
            return fail(ErrorCode::malformed_leb128, offset, "malformed SLEB128");
        }
        return true;
    }

    bool read_register(Cursor& cursor, std::uint32_t& reg, std::size_t offset) {
        std::uint64_t raw = 0;
        if (!read_uleb(cursor, raw, offset)) {
            return false;
        }
        if (raw > options_.max_register) {
            return fail(ErrorCode::invalid_register, offset, "invalid register number");
        }
        reg = static_cast<std::uint32_t>(raw);
        return true;
    }

    bool read_fixed(Cursor& cursor,
                    std::size_t width,
                    std::uint64_t& value,
                    std::size_t offset,
                    ErrorCode code,
                    const char* message) {
        if (!cursor.read_uint(width, value)) {
            return fail(code, offset, message);
        }
        return true;
    }

    bool point_in_frame(std::uint64_t value) const {
        return value >= options_.frame.low && value < options_.frame.high;
    }

    bool range_in_frame(std::uint64_t address, std::uint64_t size) const {
        if (size == 0U) {
            return point_in_frame(address);
        }
        if (address < options_.frame.low || address > options_.frame.high) {
            return false;
        }
        return size <= options_.frame.high - address;
    }

    bool lookup_register(std::uint32_t reg, std::uint64_t& value) const {
        for (std::size_t index = 0; index < options_.register_value_count; ++index) {
            const RegisterValue& entry = options_.register_values[index];
            if (entry.number == reg) {
                value = entry.value;
                return true;
            }
        }
        if (reg == options_.stack_pointer_register) {
            value = options_.frame.stack_pointer;
            return true;
        }
        return false;
    }

    bool compute_cfa(std::uint32_t reg,
                     std::int64_t offset,
                     std::uint64_t& value,
                     std::size_t error_offset) {
        std::uint64_t base = 0;
        if (!lookup_register(reg, base)) {
            return fail(ErrorCode::unknown_cfa_base, error_offset,
                        "CFA uses a register without a configured value");
        }
        if (!add_signed(base, offset, value)) {
            return fail(ErrorCode::address_overflow, error_offset,
                        "CFA computation overflows");
        }
        if (!point_in_frame(value)) {
            return fail(ErrorCode::cfa_outside_frame, error_offset,
                        "CFA lies outside the frame stack window");
        }
        return true;
    }

    bool check_rule_memory(const State& state,
                           const Rule& rule,
                           std::size_t error_offset,
                           ErrorCode outside_code) {
        if (!state.cfa.defined) {
            return fail(ErrorCode::cfa_undefined, error_offset,
                        "register rule requires a defined CFA");
        }
        std::uint64_t address = 0;
        if (!add_signed(state.cfa.value, rule.offset, address)) {
            return fail(ErrorCode::address_overflow, error_offset,
                        "register offset overflows");
        }
        if (!range_in_frame(address,
                            static_cast<std::uint64_t>(options_.address_size))) {
            return fail(outside_code, error_offset,
                        "saved register location lies outside the frame");
        }
        return true;
    }

    bool check_val_offset(const State& state,
                          const Rule& rule,
                          std::size_t error_offset) {
        if (!state.cfa.defined) {
            return fail(ErrorCode::cfa_undefined, error_offset,
                        "value offset requires a defined CFA");
        }
        std::uint64_t value = 0;
        if (!add_signed(state.cfa.value, rule.offset, value)) {
            return fail(ErrorCode::address_overflow, error_offset,
                        "value offset overflows");
        }
        if (!point_in_frame(value)) {
            return fail(ErrorCode::rule_outside_frame, error_offset,
                        "value offset lies outside the frame");
        }
        return true;
    }

    bool check_state(const State& state,
                     std::uint32_t return_address_register,
                     bool require_return_address,
                     std::size_t error_offset) {
        if (!state.cfa.defined && require_return_address) {
            return fail(ErrorCode::cfa_undefined, error_offset,
                        "final state has no CFA rule");
        }

        if (return_address_register >= state.rules.size()) {
            return fail(ErrorCode::invalid_register, error_offset,
                        "return-address register is out of range");
        }
        const Rule& ra_rule = state.rules[return_address_register];
        if (ra_rule.kind == RuleKind::undefined) {
            if (require_return_address) {
                return fail(ErrorCode::invalid_ra_rule, error_offset,
                            "final state has no return-address rule");
            }
            return true;
        }
        if (ra_rule.kind != RuleKind::offset) {
            return fail(ErrorCode::invalid_ra_rule, error_offset,
                        "return address must be stored at a bounded CFA offset");
        }
        return check_rule_memory(state, ra_rule, error_offset,
                                 ErrorCode::ra_outside_frame);
    }

    bool commit_rule(State& state,
                     std::uint32_t reg,
                     Rule rule,
                     std::uint32_t return_address_register,
                     std::size_t error_offset,
                     bool allow_undefined_return_address) {
        if (reg > options_.max_register) {
            return fail(ErrorCode::invalid_register, error_offset,
                        "invalid register number");
        }
        if (reg == return_address_register) {
            if (rule.kind == RuleKind::undefined) {
                if (!allow_undefined_return_address) {
                    return fail(ErrorCode::invalid_ra_rule, error_offset,
                                "return address cannot be undefined");
                }
            } else if (rule.kind != RuleKind::offset) {
                return fail(ErrorCode::invalid_ra_rule, error_offset,
                            "return address must use an offset rule");
            }
        }

        if (rule.kind == RuleKind::offset) {
            const ErrorCode outside = reg == return_address_register
                ? ErrorCode::ra_outside_frame
                : ErrorCode::rule_outside_frame;
            if (!check_rule_memory(state, rule, error_offset, outside)) {
                return false;
            }
        } else if (rule.kind == RuleKind::val_offset) {
            if (!check_val_offset(state, rule, error_offset)) {
                return false;
            }
        }

        state.rules[reg] = rule;
        return true;
    }

    bool apply_offset_rule(State& state,
                           std::uint32_t reg,
                           std::uint64_t factor,
                           std::int64_t data_alignment,
                           std::uint32_t return_address_register,
                           std::size_t error_offset) {
        std::int64_t offset = 0;
        if (!multiply_offset(factor, data_alignment, offset)) {
            return fail(ErrorCode::address_overflow, error_offset,
                        "register offset multiplication overflows");
        }
        Rule rule;
        rule.kind = RuleKind::offset;
        rule.offset = offset;
        return commit_rule(state, reg, rule, return_address_register,
                           error_offset, false);
    }

    bool apply_signed_offset_rule(State& state,
                                  std::uint32_t reg,
                                  std::int64_t offset,
                                  std::uint32_t return_address_register,
                                  std::size_t error_offset) {
        Rule rule;
        rule.kind = RuleKind::offset;
        rule.offset = offset;
        return commit_rule(state, reg, rule, return_address_register,
                           error_offset, false);
    }

    bool restore_rule(State& state,
                      const State& initial,
                      std::uint32_t reg,
                      std::uint32_t return_address_register,
                      std::size_t error_offset) {
        if (reg > options_.max_register) {
            return fail(ErrorCode::invalid_register, error_offset,
                        "invalid register number");
        }
        return commit_rule(state, reg, initial.rules[reg],
                           return_address_register, error_offset, true);
    }

    bool advance_location(std::uint64_t delta,
                          std::uint64_t code_alignment,
                          bool is_cie,
                          std::uint64_t pc_end,
                          std::uint64_t& location,
                          std::size_t error_offset) {
        if (is_cie) {
            return fail(ErrorCode::location_in_cie, error_offset,
                        "location advance is not valid in CIE instructions");
        }
        if (delta != 0U && code_alignment > kMaxU64 / delta) {
            return fail(ErrorCode::location_overflow, error_offset,
                        "location advance overflows");
        }
        const std::uint64_t amount = delta * code_alignment;
        if (location > pc_end || amount > pc_end - location) {
            return fail(ErrorCode::location_out_of_range, error_offset,
                        "location advance exceeds the FDE range");
        }
        location += amount;
        return true;
    }

    bool read_block(Cursor& cursor,
                    std::size_t& begin,
                    std::size_t& end,
                    std::size_t error_offset) {
        std::uint64_t length = 0;
        if (!read_uleb(cursor, length, error_offset)) {
            return false;
        }
        if (length == 0U) {
            return fail(ErrorCode::expression_empty, error_offset,
                        "empty DWARF expression");
        }
        if (length > static_cast<std::uint64_t>(cursor.remaining())) {
            return fail(ErrorCode::instruction_out_of_bounds, error_offset,
                        "DWARF expression exceeds its record");
        }
        begin = cursor.position();
        end = begin + static_cast<std::size_t>(length);
        return cursor.skip(static_cast<std::size_t>(length));
    }

    bool expression_push(std::vector<std::uint64_t>& stack,
                         std::uint64_t value,
                         std::size_t error_offset) {
        if (stack.size() >= options_.max_expression_depth) {
            return fail(ErrorCode::expression_stack_overflow, error_offset,
                        "DWARF expression stack is too deep");
        }
        stack.push_back(value);
        return true;
    }

    bool expression_pop(std::vector<std::uint64_t>& stack,
                        std::uint64_t& value,
                        std::size_t error_offset) {
        if (stack.empty()) {
            return fail(ErrorCode::expression_stack_underflow, error_offset,
                        "DWARF expression stack underflow");
        }
        value = stack.back();
        stack.pop_back();
        return true;
    }

    bool evaluate_expression(std::size_t begin,
                             std::size_t end,
                             const State& state,
                             std::uint64_t& result_value,
                             std::size_t error_offset) {
        Cursor cursor(data_, begin, end, options_.big_endian);
        std::vector<std::uint64_t> stack;
        stack.reserve(options_.max_expression_depth < 64U
                          ? options_.max_expression_depth
                          : 64U);
        std::size_t steps = 0;

        while (!cursor.empty()) {
            if (steps >= options_.max_expression_steps) {
                return fail(ErrorCode::expression_steps_exceeded, error_offset,
                            "DWARF expression step limit exceeded");
            }
            ++steps;

            const std::size_t op_offset = cursor.position();
            std::uint8_t opcode = 0;
            if (!cursor.read_u8(opcode)) {
                return fail(ErrorCode::instruction_out_of_bounds, op_offset,
                            "truncated DWARF expression");
            }

            if (is_deref_opcode(opcode)) {
                return fail(ErrorCode::expression_deref_forbidden, op_offset,
                            "DW_OP_deref family is forbidden");
            }

            if (opcode >= 0x30U && opcode <= 0x4fU) {
                const std::uint64_t literal =
                    static_cast<std::uint64_t>(opcode - 0x30U);
                if (!expression_push(stack, literal, op_offset)) {
                    return false;
                }
                continue;
            }
            if (opcode >= 0x70U && opcode <= 0x8fU) {
                std::int64_t offset = 0;
                if (!read_sleb(cursor, offset, op_offset)) {
                    return false;
                }
                const std::uint32_t reg =
                    static_cast<std::uint32_t>(opcode - 0x70U);
                std::uint64_t base = 0;
                if (!lookup_register(reg, base)) {
                    return fail(ErrorCode::expression_invalid_operand, op_offset,
                                "DW_OP_breg uses an unknown register");
                }
                std::uint64_t value = 0;
                if (!add_signed(base, offset, value)) {
                    return fail(ErrorCode::address_overflow, op_offset,
                                "DW_OP_breg arithmetic overflows");
                }
                if (!expression_push(stack, value, op_offset)) {
                    return false;
                }
                continue;
            }

            switch (opcode) {
                case 0x08: {  // DW_OP_const1u
                    std::uint64_t value = 0;
                    if (!read_fixed(cursor, 1U, value, op_offset,
                                    ErrorCode::instruction_out_of_bounds,
                                    "truncated expression operand") ||
                        !expression_push(stack, value, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x09: {  // DW_OP_const1s
                    std::uint64_t raw = 0;
                    if (!read_fixed(cursor, 1U, raw, op_offset,
                                    ErrorCode::instruction_out_of_bounds,
                                    "truncated expression operand")) {
                        return false;
                    }
                    (void)sign_extend(raw, 8U);
                    if (!expression_push(stack, raw, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x0a: {  // DW_OP_const2u
                    std::uint64_t value = 0;
                    if (!read_fixed(cursor, 2U, value, op_offset,
                                    ErrorCode::instruction_out_of_bounds,
                                    "truncated expression operand") ||
                        !expression_push(stack, value, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x0b: {  // DW_OP_const2s
                    std::uint64_t raw = 0;
                    if (!read_fixed(cursor, 2U, raw, op_offset,
                                    ErrorCode::instruction_out_of_bounds,
                                    "truncated expression operand")) {
                        return false;
                    }
                    (void)sign_extend(raw, 16U);
                    if (!expression_push(stack, raw, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x0c: {  // DW_OP_const4u
                    std::uint64_t value = 0;
                    if (!read_fixed(cursor, 4U, value, op_offset,
                                    ErrorCode::instruction_out_of_bounds,
                                    "truncated expression operand") ||
                        !expression_push(stack, value, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x0d: {  // DW_OP_const4s
                    std::uint64_t raw = 0;
                    if (!read_fixed(cursor, 4U, raw, op_offset,
                                    ErrorCode::instruction_out_of_bounds,
                                    "truncated expression operand")) {
                        return false;
                    }
                    (void)sign_extend(raw, 32U);
                    if (!expression_push(stack, raw, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x0e: {  // DW_OP_const8u
                    std::uint64_t value = 0;
                    if (!read_fixed(cursor, 8U, value, op_offset,
                                    ErrorCode::instruction_out_of_bounds,
                                    "truncated expression operand") ||
                        !expression_push(stack, value, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x0f: {  // DW_OP_const8s
                    std::uint64_t raw = 0;
                    if (!read_fixed(cursor, 8U, raw, op_offset,
                                    ErrorCode::instruction_out_of_bounds,
                                    "truncated expression operand") ||
                        !expression_push(stack, raw, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x10: {  // DW_OP_constu
                    std::uint64_t value = 0;
                    if (!read_uleb(cursor, value, op_offset) ||
                        !expression_push(stack, value, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x11: {  // DW_OP_consts
                    std::int64_t value = 0;
                    if (!read_sleb(cursor, value, op_offset)) {
                        return false;
                    }
                    const std::uint64_t bits = static_cast<std::uint64_t>(value);
                    if (!expression_push(stack, bits, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x12: {  // DW_OP_dup
                    if (stack.empty()) {
                        return fail(ErrorCode::expression_stack_underflow,
                                    op_offset, "DW_OP_dup on empty stack");
                    }
                    if (!expression_push(stack, stack.back(), op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x13: {  // DW_OP_drop
                    std::uint64_t discarded = 0;
                    if (!expression_pop(stack, discarded, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x14: {  // DW_OP_over
                    if (stack.size() < 2U) {
                        return fail(ErrorCode::expression_stack_underflow,
                                    op_offset, "DW_OP_over on shallow stack");
                    }
                    if (!expression_push(stack, stack[stack.size() - 2U],
                                         op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x15: {  // DW_OP_pick
                    std::uint64_t index = 0;
                    if (!read_fixed(cursor, 1U, index, op_offset,
                                    ErrorCode::instruction_out_of_bounds,
                                    "truncated DW_OP_pick operand")) {
                        return false;
                    }
                    if (index >= static_cast<std::uint64_t>(stack.size())) {
                        return fail(ErrorCode::expression_stack_underflow,
                                    op_offset, "DW_OP_pick index is too large");
                    }
                    if (!expression_push(
                            stack,
                            stack[stack.size() - 1U -
                                  static_cast<std::size_t>(index)],
                            op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x16: {  // DW_OP_swap
                    if (stack.size() < 2U) {
                        return fail(ErrorCode::expression_stack_underflow,
                                    op_offset, "DW_OP_swap on shallow stack");
                    }
                    std::swap(stack[stack.size() - 1U], stack[stack.size() - 2U]);
                    break;
                }
                case 0x17: {  // DW_OP_rot
                    if (stack.size() < 3U) {
                        return fail(ErrorCode::expression_stack_underflow,
                                    op_offset, "DW_OP_rot on shallow stack");
                    }
                    const std::uint64_t top = stack[stack.size() - 1U];
                    stack[stack.size() - 1U] = stack[stack.size() - 2U];
                    stack[stack.size() - 2U] = stack[stack.size() - 3U];
                    stack[stack.size() - 3U] = top;
                    break;
                }
                case 0x1a:   // DW_OP_and
                case 0x1c:   // DW_OP_minus
                case 0x21:   // DW_OP_or
                case 0x22:   // DW_OP_plus
                case 0x27: { // DW_OP_xor
                    std::uint64_t top = 0;
                    std::uint64_t next = 0;
                    if (!expression_pop(stack, top, op_offset) ||
                        !expression_pop(stack, next, op_offset)) {
                        return false;
                    }
                    std::uint64_t value = 0;
                    if (opcode == 0x1a) {
                        value = next & top;
                    } else if (opcode == 0x21) {
                        value = next | top;
                    } else if (opcode == 0x27) {
                        value = next ^ top;
                    } else if (opcode == 0x22) {
                        if (next > kMaxU64 - top) {
                            return fail(ErrorCode::address_overflow, op_offset,
                                        "DW_OP_plus overflows");
                        }
                        value = next + top;
                    } else {
                        if (next < top) {
                            return fail(ErrorCode::address_overflow, op_offset,
                                        "DW_OP_minus underflows");
                        }
                        value = next - top;
                    }
                    if (!expression_push(stack, value, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x23: {  // DW_OP_plus_uconst
                    std::uint64_t constant = 0;
                    if (!read_uleb(cursor, constant, op_offset)) {
                        return false;
                    }
                    std::uint64_t value = 0;
                    if (!expression_pop(stack, value, op_offset)) {
                        return false;
                    }
                    if (value > kMaxU64 - constant) {
                        return fail(ErrorCode::address_overflow, op_offset,
                                    "DW_OP_plus_uconst overflows");
                    }
                    if (!expression_push(stack, value + constant, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x92: {  // DW_OP_bregx
                    std::uint32_t reg = 0;
                    std::int64_t offset = 0;
                    if (!read_register(cursor, reg, op_offset) ||
                        !read_sleb(cursor, offset, op_offset)) {
                        return false;
                    }
                    std::uint64_t base = 0;
                    if (!lookup_register(reg, base)) {
                        return fail(ErrorCode::expression_invalid_operand,
                                    op_offset,
                                    "DW_OP_bregx uses an unknown register");
                    }
                    std::uint64_t value = 0;
                    if (!add_signed(base, offset, value)) {
                        return fail(ErrorCode::address_overflow, op_offset,
                                    "DW_OP_bregx arithmetic overflows");
                    }
                    if (!expression_push(stack, value, op_offset)) {
                        return false;
                    }
                    break;
                }
                case 0x96:   // DW_OP_nop
                case 0x9f:   // DW_OP_stack_value
                    break;
                case 0x9c: {  // DW_OP_call_frame_cfa
                    if (!state.cfa.defined) {
                        return fail(ErrorCode::cfa_undefined, op_offset,
                                    "DW_OP_call_frame_cfa before CFA definition");
                    }
                    if (!expression_push(stack, state.cfa.value, op_offset)) {
                        return false;
                    }
                    break;
                }
                default:
                    return fail(ErrorCode::expression_unsupported_opcode,
                                op_offset,
                                "DWARF expression opcode is not allowed");
            }
        }

        if (stack.size() != 1U) {
            return fail(ErrorCode::expression_not_one_value, error_offset,
                        "DWARF expression did not produce one value");
        }
        result_value = stack.back();
        return true;
    }

    bool execute_cfi(Cursor& cursor,
                     State& state,
                     const State& initial,
                     std::uint32_t return_address_register,
                     std::uint64_t code_alignment,
                     std::int64_t data_alignment,
                     bool is_cie,
                     std::uint64_t pc_begin,
                     std::uint64_t pc_end,
                     std::uint64_t& location,
                     bool require_return_address) {
        std::vector<State> state_stack;
        while (!cursor.empty()) {
            if (instruction_count_ >= options_.max_instructions) {
                return fail(ErrorCode::too_many_instructions, cursor.position(),
                            "CFI instruction limit exceeded");
            }
            ++instruction_count_;

            const std::size_t op_offset = cursor.position();
            std::uint8_t opcode = 0;
            if (!cursor.read_u8(opcode)) {
                return fail(ErrorCode::instruction_out_of_bounds, op_offset,
                            "truncated CFA opcode");
            }

            const std::uint8_t primary = static_cast<std::uint8_t>(opcode & 0xc0U);
            if (primary == 0x40U) {
                const std::uint64_t delta =
                    static_cast<std::uint64_t>(opcode & 0x3fU);
                if (!advance_location(delta, code_alignment, is_cie, pc_end,
                                      location, op_offset)) {
                    return false;
                }
            } else if (primary == 0x80U) {
                std::uint32_t reg = 0;
                std::uint64_t factor = 0;
                if (!read_register(cursor, reg, op_offset) ||
                    !read_uleb(cursor, factor, op_offset) ||
                    !apply_offset_rule(state, reg, factor, data_alignment,
                                       return_address_register, op_offset)) {
                    return false;
                }
            } else if (primary == 0xc0U) {
                std::uint32_t reg = 0;
                if (!read_register(cursor, reg, op_offset) ||
                    !restore_rule(state, initial, reg,
                                  return_address_register, op_offset)) {
                    return false;
                }
            } else {
                switch (opcode) {
                    case 0x00:  // DW_CFA_nop
                        break;
                    case 0x01: {  // DW_CFA_set_loc
                        if (is_cie) {
                            return fail(ErrorCode::location_in_cie, op_offset,
                                        "DW_CFA_set_loc is not valid in a CIE");
                        }
                        std::uint64_t target = 0;
                        if (!read_fixed(cursor,
                                        static_cast<std::size_t>(
                                            options_.address_size),
                                        target, op_offset,
                                        ErrorCode::instruction_out_of_bounds,
                                        "truncated DW_CFA_set_loc operand")) {
                            return false;
                        }
                        if (target < pc_begin || target > pc_end ||
                            target < location) {
                            return fail(ErrorCode::location_out_of_range,
                                        op_offset,
                                        "DW_CFA_set_loc is outside or behind the FDE");
                        }
                        location = target;
                        break;
                    }
                    case 0x02: {  // DW_CFA_advance_loc1
                        std::uint64_t delta = 0;
                        if (!read_fixed(cursor, 1U, delta, op_offset,
                                        ErrorCode::instruction_out_of_bounds,
                                        "truncated DW_CFA_advance_loc1") ||
                            !advance_location(delta, code_alignment, is_cie,
                                              pc_end, location, op_offset)) {
                            return false;
                        }
                        break;
                    }
                    case 0x03: {  // DW_CFA_advance_loc2
                        std::uint64_t delta = 0;
                        if (!read_fixed(cursor, 2U, delta, op_offset,
                                        ErrorCode::instruction_out_of_bounds,
                                        "truncated DW_CFA_advance_loc2") ||
                            !advance_location(delta, code_alignment, is_cie,
                                              pc_end, location, op_offset)) {
                            return false;
                        }
                        break;
                    }
                    case 0x04: {  // DW_CFA_advance_loc4
                        std::uint64_t delta = 0;
                        if (!read_fixed(cursor, 4U, delta, op_offset,
                                        ErrorCode::instruction_out_of_bounds,
                                        "truncated DW_CFA_advance_loc4") ||
                            !advance_location(delta, code_alignment, is_cie,
                                              pc_end, location, op_offset)) {
                            return false;
                        }
                        break;
                    }
                    case 0x05: {  // DW_CFA_offset_extended
                        std::uint32_t reg = 0;
                        std::uint64_t factor = 0;
                        if (!read_register(cursor, reg, op_offset) ||
                            !read_uleb(cursor, factor, op_offset) ||
                            !apply_offset_rule(state, reg, factor,
                                               data_alignment,
                                               return_address_register,
                                               op_offset)) {
                            return false;
                        }
                        break;
                    }
                    case 0x06: {  // DW_CFA_restore_extended
                        std::uint32_t reg = 0;
                        if (!read_register(cursor, reg, op_offset) ||
                            !restore_rule(state, initial, reg,
                                          return_address_register, op_offset)) {
                            return false;
                        }
                        break;
                    }
                    case 0x07: {  // DW_CFA_undefined
                        std::uint32_t reg = 0;
                        if (!read_register(cursor, reg, op_offset)) {
                            return false;
                        }
                        Rule rule;
                        rule.kind = RuleKind::undefined;
                        if (!commit_rule(state, reg, rule,
                                         return_address_register, op_offset,
                                         false)) {
                            return false;
                        }
                        break;
                    }
                    case 0x08: {  // DW_CFA_same_value
                        std::uint32_t reg = 0;
                        if (!read_register(cursor, reg, op_offset)) {
                            return false;
                        }
                        Rule rule;
                        rule.kind = RuleKind::same_value;
                        if (!commit_rule(state, reg, rule,
                                         return_address_register, op_offset,
                                         false)) {
                            return false;
                        }
                        break;
                    }
                    case 0x09: {  // DW_CFA_register
                        std::uint32_t reg = 0;
                        std::uint32_t other = 0;
                        if (!read_register(cursor, reg, op_offset) ||
                            !read_register(cursor, other, op_offset)) {
                            return false;
                        }
                        Rule rule;
                        rule.kind = RuleKind::register_rule;
                        rule.register_number = other;
                        if (!commit_rule(state, reg, rule,
                                         return_address_register, op_offset,
                                         false)) {
                            return false;
                        }
                        break;
                    }
                    case 0x0a:  // DW_CFA_remember_state
                        if (state_stack.size() >= options_.max_state_stack) {
                            return fail(ErrorCode::state_stack_overflow,
                                        op_offset,
                                        "DW_CFA_remember_state stack overflow");
                        }
                        state_stack.push_back(state);
                        break;
                    case 0x0b:  // DW_CFA_restore_state
                        if (state_stack.empty()) {
                            return fail(ErrorCode::state_stack_underflow,
                                        op_offset,
                                        "DW_CFA_restore_state with empty stack");
                        }
                        state = std::move(state_stack.back());
                        state_stack.pop_back();
                        break;
                    case 0x0c: {  // DW_CFA_def_cfa
                        std::uint32_t reg = 0;
                        std::uint64_t offset = 0;
                        if (!read_register(cursor, reg, op_offset) ||
                            !read_uleb(cursor, offset, op_offset)) {
                            return false;
                        }
                        if (offset > static_cast<std::uint64_t>(kMaxI64)) {
                            return fail(ErrorCode::address_overflow, op_offset,
                                        "DW_CFA_def_cfa offset is too large");
                        }
                        std::uint64_t value = 0;
                        if (!compute_cfa(reg, static_cast<std::int64_t>(offset),
                                         value, op_offset)) {
                            return false;
                        }
                        state.cfa = CfaRule{};
                        state.cfa.defined = true;
                        state.cfa.register_number = reg;
                        state.cfa.offset = static_cast<std::int64_t>(offset);
                        state.cfa.value = value;
                        break;
                    }
                    case 0x0d: {  // DW_CFA_def_cfa_register
                        if (!state.cfa.defined || state.cfa.expression) {
                            return fail(ErrorCode::invalid_cfa_operation,
                                        op_offset,
                                        "DW_CFA_def_cfa_register without a register CFA");
                        }
                        std::uint32_t reg = 0;
                        if (!read_register(cursor, reg, op_offset)) {
                            return false;
                        }
                        std::uint64_t value = 0;
                        if (!compute_cfa(reg, state.cfa.offset, value,
                                         op_offset)) {
                            return false;
                        }
                        state.cfa.register_number = reg;
                        state.cfa.value = value;
                        break;
                    }
                    case 0x0e: {  // DW_CFA_def_cfa_offset
                        if (!state.cfa.defined || state.cfa.expression) {
                            return fail(ErrorCode::invalid_cfa_operation,
                                        op_offset,
                                        "DW_CFA_def_cfa_offset without a register CFA");
                        }
                        std::uint64_t offset = 0;
                        if (!read_uleb(cursor, offset, op_offset)) {
                            return false;
                        }
                        if (offset > static_cast<std::uint64_t>(kMaxI64)) {
                            return fail(ErrorCode::address_overflow, op_offset,
                                        "CFA offset is too large");
                        }
                        std::uint64_t value = 0;
                        if (!compute_cfa(state.cfa.register_number,
                                         static_cast<std::int64_t>(offset),
                                         value, op_offset)) {
                            return false;
                        }
                        state.cfa.offset = static_cast<std::int64_t>(offset);
                        state.cfa.value = value;
                        break;
                    }
                    case 0x0f: {  // DW_CFA_def_cfa_expression
                        std::size_t begin = 0;
                        std::size_t end = 0;
                        if (!read_block(cursor, begin, end, op_offset)) {
                            return false;
                        }
                        std::uint64_t value = 0;
                        if (!evaluate_expression(begin, end, state, value,
                                                 op_offset)) {
                            return false;
                        }
                        if (!point_in_frame(value)) {
                            return fail(ErrorCode::expression_outside_frame,
                                        op_offset,
                                        "CFA expression lies outside the frame");
                        }
                        state.cfa = CfaRule{};
                        state.cfa.defined = true;
                        state.cfa.expression = true;
                        state.cfa.value = value;
                        break;
                    }
                    case 0x10: {  // DW_CFA_expression
                        std::uint32_t reg = 0;
                        std::size_t begin = 0;
                        std::size_t end = 0;
                        if (!read_register(cursor, reg, op_offset) ||
                            !read_block(cursor, begin, end, op_offset)) {
                            return false;
                        }
                        std::uint64_t address = 0;
                        if (!evaluate_expression(begin, end, state, address,
                                                 op_offset)) {
                            return false;
                        }
                        if (reg == return_address_register) {
                            return fail(ErrorCode::invalid_ra_rule, op_offset,
                                        "return address cannot use an expression");
                        }
                        if (!range_in_frame(
                                address,
                                static_cast<std::uint64_t>(
                                    options_.address_size))) {
                            return fail(ErrorCode::expression_outside_frame,
                                        op_offset,
                                        "register expression lies outside the frame");
                        }
                        Rule rule;
                        rule.kind = RuleKind::expression;
                        if (!commit_rule(state, reg, rule,
                                         return_address_register, op_offset,
                                         false)) {
                            return false;
                        }
                        break;
                    }
                    case 0x11: {  // DW_CFA_offset_extended_sf
                        std::uint32_t reg = 0;
                        std::int64_t offset = 0;
                        if (!read_register(cursor, reg, op_offset) ||
                            !read_sleb(cursor, offset, op_offset) ||
                            !apply_signed_offset_rule(
                                state, reg, offset, return_address_register,
                                op_offset)) {
                            return false;
                        }
                        break;
                    }
                    case 0x12: {  // DW_CFA_def_cfa_sf
                        std::uint32_t reg = 0;
                        std::int64_t offset = 0;
                        if (!read_register(cursor, reg, op_offset) ||
                            !read_sleb(cursor, offset, op_offset)) {
                            return false;
                        }
                        std::uint64_t value = 0;
                        if (!compute_cfa(reg, offset, value, op_offset)) {
                            return false;
                        }
                        state.cfa = CfaRule{};
                        state.cfa.defined = true;
                        state.cfa.register_number = reg;
                        state.cfa.offset = offset;
                        state.cfa.value = value;
                        break;
                    }
                    case 0x13: {  // DW_CFA_def_cfa_offset_sf
                        if (!state.cfa.defined || state.cfa.expression) {
                            return fail(ErrorCode::invalid_cfa_operation,
                                        op_offset,
                                        "DW_CFA_def_cfa_offset_sf without a register CFA");
                        }
                        std::int64_t offset = 0;
                        if (!read_sleb(cursor, offset, op_offset)) {
                            return false;
                        }
                        std::uint64_t value = 0;
                        if (!compute_cfa(state.cfa.register_number, offset,
                                         value, op_offset)) {
                            return false;
                        }
                        state.cfa.offset = offset;
                        state.cfa.value = value;
                        break;
                    }
                    case 0x14: {  // DW_CFA_val_offset
                        std::uint32_t reg = 0;
                        std::uint64_t factor = 0;
                        if (!read_register(cursor, reg, op_offset) ||
                            !read_uleb(cursor, factor, op_offset)) {
                            return false;
                        }
                        if (reg == return_address_register) {
                            return fail(ErrorCode::invalid_ra_rule, op_offset,
                                        "return address cannot use a value offset");
                        }
                        std::int64_t offset = 0;
                        if (!multiply_offset(factor, data_alignment, offset)) {
                            return fail(ErrorCode::address_overflow, op_offset,
                                        "value offset multiplication overflows");
                        }
                        Rule rule;
                        rule.kind = RuleKind::val_offset;
                        rule.offset = offset;
                        if (!commit_rule(state, reg, rule,
                                         return_address_register, op_offset,
                                         false)) {
                            return false;
                        }
                        break;
                    }
                    case 0x15: {  // DW_CFA_val_offset_sf
                        std::uint32_t reg = 0;
                        std::int64_t offset = 0;
                        if (!read_register(cursor, reg, op_offset) ||
                            !read_sleb(cursor, offset, op_offset)) {
                            return false;
                        }
                        if (reg == return_address_register) {
                            return fail(ErrorCode::invalid_ra_rule, op_offset,
                                        "return address cannot use a value offset");
                        }
                        Rule rule;
                        rule.kind = RuleKind::val_offset;
                        rule.offset = offset;
                        if (!commit_rule(state, reg, rule,
                                         return_address_register, op_offset,
                                         false)) {
                            return false;
                        }
                        break;
                    }
                    case 0x16: {  // DW_CFA_val_expression
                        std::uint32_t reg = 0;
                        std::size_t begin = 0;
                        std::size_t end = 0;
                        if (!read_register(cursor, reg, op_offset) ||
                            !read_block(cursor, begin, end, op_offset)) {
                            return false;
                        }
                        std::uint64_t ignored = 0;
                        if (!evaluate_expression(begin, end, state, ignored,
                                                 op_offset)) {
                            return false;
                        }
                        if (reg == return_address_register) {
                            return fail(ErrorCode::invalid_ra_rule, op_offset,
                                        "return address cannot use a value expression");
                        }
                        Rule rule;
                        rule.kind = RuleKind::val_expression;
                        if (!commit_rule(state, reg, rule,
                                         return_address_register, op_offset,
                                         false)) {
                            return false;
                        }
                        break;
                    }
                    case 0x2e: {  // DW_CFA_GNU_args_size
                        std::uint64_t args_size = 0;
                        if (!read_uleb(cursor, args_size, op_offset)) {
                            return false;
                        }
                        if (args_size > options_.frame.high -
                                            options_.frame.low) {
                            return fail(ErrorCode::rule_outside_frame,
                                        op_offset,
                                        "DW_CFA_GNU_args_size exceeds the frame");
                        }
                        break;
                    }
                    default:
                        return fail(ErrorCode::unsupported_cfa_opcode,
                                    op_offset,
                                    "unsupported or forbidden DW_CFA opcode");
                }
            }

            if (!check_state(state, return_address_register, false, op_offset)) {
                return false;
            }
        }

        return check_state(state, return_address_register,
                           require_return_address, cursor.end());
    }

    bool parse_cie(std::size_t record_start,
                   std::size_t content_offset,
                   std::size_t pointer_width,
                   std::size_t record_end) {
        Cursor cursor(data_, content_offset + pointer_width, record_end,
                      options_.big_endian);
        const std::size_t header_offset = cursor.position();

        std::uint8_t version = 0;
        if (!cursor.read_u8(version)) {
            return fail(ErrorCode::record_too_short, header_offset,
                        "truncated CIE header");
        }
        if (version != 1U && version != 3U && version != 4U) {
            return fail(ErrorCode::unsupported_version, header_offset,
                        "unsupported CIE version");
        }

        char augmentation[33] = {};
        std::size_t augmentation_length = 0;
        for (;;) {
            if (augmentation_length >= 32U) {
                return fail(ErrorCode::invalid_augmentation,
                            cursor.position(),
                            "CIE augmentation is too long");
            }
            std::uint8_t byte = 0;
            if (!cursor.read_u8(byte)) {
                return fail(ErrorCode::invalid_augmentation,
                            cursor.position(),
                            "unterminated CIE augmentation");
            }
            if (byte == 0U) {
                break;
            }
            const char character = static_cast<char>(byte);
            if (character != 'z' && character != 'L' && character != 'P' &&
                character != 'R' && character != 'S') {
                return fail(ErrorCode::invalid_augmentation,
                            cursor.position() - 1U,
                            "unsupported CIE augmentation character");
            }
            augmentation[augmentation_length] = character;
            ++augmentation_length;
        }

        const bool has_z = augmentation_length > 0U && augmentation[0] == 'z';
        if (!has_z && augmentation_length != 0U) {
            return fail(ErrorCode::invalid_augmentation, header_offset,
                        "CIE augmentation requires a leading z");
        }
        for (std::size_t index = 1; index < augmentation_length; ++index) {
            if (augmentation[index] == 'z') {
                return fail(ErrorCode::invalid_augmentation, header_offset,
                            "duplicate z CIE augmentation");
            }
        }

        std::uint64_t code_alignment = 0;
        if (!read_uleb(cursor, code_alignment, cursor.position())) {
            return false;
        }
        if (code_alignment == 0U) {
            return fail(ErrorCode::invalid_alignment, cursor.position(),
                        "zero code alignment");
        }

        std::int64_t data_alignment = 0;
        if (!read_sleb(cursor, data_alignment, cursor.position())) {
            return false;
        }
        if (data_alignment == 0) {
            return fail(ErrorCode::invalid_alignment, cursor.position(),
                        "zero data alignment");
        }

        std::uint32_t return_address_register = 0;
        if (version == 1U) {
            std::uint64_t raw = 0;
            if (!read_fixed(cursor, 1U, raw, cursor.position(),
                            ErrorCode::record_too_short,
                            "truncated return-address register")) {
                return false;
            }
            if (raw > options_.max_register) {
                return fail(ErrorCode::invalid_register, cursor.position() - 1U,
                            "invalid return-address register");
            }
            return_address_register = static_cast<std::uint32_t>(raw);
        } else {
            if (!read_register(cursor, return_address_register,
                               cursor.position())) {
                return false;
            }
        }
        if (options_.expected_return_address_register !=
                std::numeric_limits<std::uint32_t>::max() &&
            return_address_register !=
                options_.expected_return_address_register) {
            return fail(ErrorCode::unexpected_return_address_register,
                        cursor.position(),
                        "CIE return-address register does not match policy");
        }

        if (version == 4U) {
            std::uint64_t cie_address_size = 0;
            std::uint64_t segment_size = 0;
            if (!read_fixed(cursor, 1U, cie_address_size, cursor.position(),
                            ErrorCode::record_too_short,
                            "truncated CIE address size") ||
                !read_fixed(cursor, 1U, segment_size, cursor.position(),
                            ErrorCode::record_too_short,
                            "truncated CIE segment size")) {
                return false;
            }
            if (segment_size != 0U) {
                return fail(ErrorCode::unsupported_encoding, cursor.position(),
                            "segmented CIE addresses are unsupported");
            }
            if (cie_address_size != options_.address_size) {
                return fail(ErrorCode::invalid_encoding, cursor.position(),
                            "CIE address size does not match policy");
            }
        }

        std::uint8_t fde_encoding = 0x00U;  // DW_EH_PE_absptr
        bool seen_l = false;
        bool seen_p = false;
        bool seen_r = false;
        bool seen_s = false;

        if (has_z) {
            std::uint64_t augmentation_data_length = 0;
            if (!read_uleb(cursor, augmentation_data_length,
                           cursor.position())) {
                return false;
            }
            if (augmentation_data_length >
                static_cast<std::uint64_t>(cursor.remaining())) {
                return fail(ErrorCode::augmentation_length_invalid,
                            cursor.position(),
                            "CIE augmentation data exceeds record");
            }
            const std::size_t augmentation_end =
                cursor.position() +
                static_cast<std::size_t>(augmentation_data_length);
            Cursor augmentation_cursor(
                data_, cursor.position(), augmentation_end, options_.big_endian);

            for (std::size_t index = 1; index < augmentation_length; ++index) {
                const char character = augmentation[index];
                if (character == 'L') {
                    if (seen_l) {
                        return fail(ErrorCode::invalid_augmentation,
                                    augmentation_cursor.position(),
                                    "duplicate L augmentation");
                    }
                    seen_l = true;
                    std::uint8_t ignored = 0;
                    if (!augmentation_cursor.read_u8(ignored)) {
                        return fail(ErrorCode::augmentation_length_invalid,
                                    augmentation_cursor.position(),
                                    "truncated L augmentation");
                    }
                } else if (character == 'P') {
                    if (seen_p) {
                        return fail(ErrorCode::invalid_augmentation,
                                    augmentation_cursor.position(),
                                    "duplicate P augmentation");
                    }
                    seen_p = true;
                    std::uint8_t encoding = 0;
                    if (!augmentation_cursor.read_u8(encoding)) {
                        return fail(ErrorCode::augmentation_length_invalid,
                                    augmentation_cursor.position(),
                                    "truncated P augmentation encoding");
                    }
                    if (!skip_encoded(augmentation_cursor, encoding)) {
                        return false;
                    }
                } else if (character == 'R') {
                    if (seen_r) {
                        return fail(ErrorCode::invalid_augmentation,
                                    augmentation_cursor.position(),
                                    "duplicate R augmentation");
                    }
                    seen_r = true;
                    if (!augmentation_cursor.read_u8(fde_encoding)) {
                        return fail(ErrorCode::augmentation_length_invalid,
                                    augmentation_cursor.position(),
                                    "truncated R augmentation");
                    }
                    if ((fde_encoding & 0x0fU) == 0x0fU) {
                        return fail(ErrorCode::invalid_encoding,
                                    augmentation_cursor.position() - 1U,
                                    "omitted FDE encoding");
                    }
                } else if (character == 'S') {
                    if (seen_s) {
                        return fail(ErrorCode::invalid_augmentation,
                                    augmentation_cursor.position(),
                                    "duplicate S augmentation");
                    }
                    seen_s = true;
                }
            }

            if (!augmentation_cursor.empty()) {
                return fail(ErrorCode::augmentation_length_invalid,
                            augmentation_cursor.position(),
                            "unconsumed CIE augmentation data");
            }
            if (!cursor.skip(static_cast<std::size_t>(
                    augmentation_data_length))) {
                return false;
            }
        } else if (augmentation_length != 0U) {
            return fail(ErrorCode::invalid_augmentation, header_offset,
                        "non-z CIE augmentation is unsupported");
        }

        State initial = make_state();
        State baseline = make_state();
        std::uint64_t location = 0;
        if (!execute_cfi(cursor, initial, baseline,
                         return_address_register, code_alignment,
                         data_alignment, true, 0, 0, location, false)) {
            return false;
        }

        CieInfo info;
        info.record_start = record_start;
        info.code_alignment = code_alignment;
        info.data_alignment = data_alignment;
        info.return_address_register = return_address_register;
        info.fde_encoding = fde_encoding;
        info.has_z_augmentation = has_z;
        info.initial_state = std::move(initial);
        cies_.push_back(std::move(info));
        return true;
    }

    bool parse_fde(std::size_t record_start,
                   std::size_t content_offset,
                   std::size_t pointer_width,
                   std::size_t record_end,
                   std::uint64_t cie_pointer) {
        const std::size_t pointer_offset = content_offset;
        if (cie_pointer > static_cast<std::uint64_t>(pointer_offset)) {
            return fail(ErrorCode::cie_pointer_forward, pointer_offset,
                        "FDE CIE pointer points forward");
        }
        const std::size_t cie_offset =
            pointer_offset - static_cast<std::size_t>(cie_pointer);
        if (cie_offset >= record_start) {
            return fail(ErrorCode::cie_pointer_forward, pointer_offset,
                        "FDE CIE pointer does not point backward");
        }

        const CieInfo* cie = find_cie(cie_offset);
        if (cie == nullptr) {
            return fail(ErrorCode::cie_target_not_cie, pointer_offset,
                        "FDE CIE pointer does not target a validated CIE");
        }

        Cursor cursor(data_, content_offset + pointer_width, record_end,
                      options_.big_endian);
        std::uint64_t pc_begin = 0;
        if (!decode_initial_location(cursor, cie->fde_encoding, pc_begin)) {
            return false;
        }

        std::uint64_t range = 0;
        const std::uint8_t range_encoding =
            static_cast<std::uint8_t>(cie->fde_encoding & 0x0fU);
        if (!decode_range(cursor, range_encoding, range)) {
            return false;
        }
        if (range == 0U) {
            return fail(ErrorCode::fde_range_invalid, record_start,
                        "FDE has an empty address range");
        }
        if (options_.address_size < 8U) {
            const unsigned int bits =
                static_cast<unsigned int>(options_.address_size) * 8U;
            const std::uint64_t address_space = std::uint64_t{1} << bits;
            if (pc_begin >= address_space ||
                range > address_space - pc_begin) {
                return fail(ErrorCode::fde_range_overflow, record_start,
                            "FDE address range wraps around");
            }
        } else if (range > kMaxU64 - pc_begin) {
            return fail(ErrorCode::fde_range_overflow, record_start,
                        "FDE address range wraps around");
        }
        const std::uint64_t pc_end = pc_begin + range;

        if (cie->has_z_augmentation) {
            std::uint64_t augmentation_length = 0;
            if (!read_uleb(cursor, augmentation_length, cursor.position())) {
                return false;
            }
            if (augmentation_length >
                static_cast<std::uint64_t>(cursor.remaining())) {
                return fail(ErrorCode::augmentation_length_invalid,
                            cursor.position(),
                            "FDE augmentation data exceeds record");
            }
            if (!cursor.skip(
                    static_cast<std::size_t>(augmentation_length))) {
                return false;
            }
        }

        State state = cie->initial_state;
        State initial = cie->initial_state;
        std::uint64_t location = pc_begin;
        if (!execute_cfi(cursor, state, initial,
                         cie->return_address_register, cie->code_alignment,
                         cie->data_alignment, false, pc_begin, pc_end,
                         location, true)) {
            return false;
        }

        intervals_.push_back(CodeInterval{pc_begin, pc_end, record_start});
        return true;
    }

    const CieInfo* find_cie(std::size_t offset) const {
        std::size_t low = 0;
        std::size_t high = cies_.size();
        while (low < high) {
            const std::size_t middle = low + (high - low) / 2U;
            if (cies_[middle].record_start < offset) {
                low = middle + 1U;
            } else {
                high = middle;
            }
        }
        if (low < cies_.size() && cies_[low].record_start == offset) {
            return &cies_[low];
        }
        return nullptr;
    }

    bool decode_raw(Cursor& cursor,
                    std::uint8_t format,
                    std::uint64_t& value,
                    bool& negative,
                    std::size_t error_offset) {
        negative = false;
        switch (format) {
            case 0x00: {  // DW_EH_PE_absptr
                if (!read_fixed(cursor,
                                static_cast<std::size_t>(options_.address_size),
                                value, error_offset,
                                ErrorCode::instruction_out_of_bounds,
                                "truncated encoded pointer")) {
                    return false;
                }
                return true;
            }
            case 0x01:  // DW_EH_PE_uleb128
                return read_uleb(cursor, value, error_offset);
            case 0x02: {  // DW_EH_PE_udata2
                if (!read_fixed(cursor, 2U, value, error_offset,
                                ErrorCode::instruction_out_of_bounds,
                                "truncated encoded pointer")) {
                    return false;
                }
                return true;
            }
            case 0x03: {  // DW_EH_PE_udata4
                if (!read_fixed(cursor, 4U, value, error_offset,
                                ErrorCode::instruction_out_of_bounds,
                                "truncated encoded pointer")) {
                    return false;
                }
                return true;
            }
            case 0x04: {  // DW_EH_PE_udata8
                if (!read_fixed(cursor, 8U, value, error_offset,
                                ErrorCode::instruction_out_of_bounds,
                                "truncated encoded pointer")) {
                    return false;
                }
                return true;
            }
            case 0x09: {  // DW_EH_PE_sleb128
                std::int64_t signed_value = 0;
                if (!read_sleb(cursor, signed_value, error_offset)) {
                    return false;
                }
                value = static_cast<std::uint64_t>(signed_value);
                negative = signed_value < 0;
                return true;
            }
            case 0x0a: {  // DW_EH_PE_sdata2
                if (!read_fixed(cursor, 2U, value, error_offset,
                                ErrorCode::instruction_out_of_bounds,
                                "truncated encoded pointer")) {
                    return false;
                }
                (void)sign_extend(value, 16U);
                negative = (value & (std::uint64_t{1} << 63U)) != 0U;
                return true;
            }
            case 0x0b: {  // DW_EH_PE_sdata4
                if (!read_fixed(cursor, 4U, value, error_offset,
                                ErrorCode::instruction_out_of_bounds,
                                "truncated encoded pointer")) {
                    return false;
                }
                (void)sign_extend(value, 32U);
                negative = (value & (std::uint64_t{1} << 63U)) != 0U;
                return true;
            }
            case 0x0c: {  // DW_EH_PE_sdata8
                if (!read_fixed(cursor, 8U, value, error_offset,
                                ErrorCode::instruction_out_of_bounds,
                                "truncated encoded pointer")) {
                    return false;
                }
                negative = (value & (std::uint64_t{1} << 63U)) != 0U;
                return true;
            }
            default:
                return fail(ErrorCode::unsupported_encoding, error_offset,
                            "unsupported DW_EH_PE pointer encoding");
        }
    }

    bool decode_initial_location(Cursor& cursor,
                                 std::uint8_t encoding,
                                 std::uint64_t& value) {
        const std::size_t encoding_offset = cursor.position();
        if ((encoding & 0x80U) != 0U) {
            return fail(ErrorCode::pointer_dereference_forbidden,
                        encoding_offset,
                        "indirect FDE address encoding is forbidden");
        }

        const std::uint8_t application =
            static_cast<std::uint8_t>(encoding & 0x70U);
        if (application == 0x40U || application == 0x60U ||
            application == 0x70U) {
            return fail(ErrorCode::unsupported_encoding, encoding_offset,
                        "unsupported FDE address application");
        }
        if (application == 0x50U) {
            if (!cursor.align_to(
                    static_cast<std::size_t>(options_.address_size))) {
                return fail(ErrorCode::instruction_out_of_bounds,
                            encoding_offset,
                            "cannot align encoded address");
            }
        }

        const std::size_t field_offset = cursor.position();
        std::uint64_t raw = 0;
        bool negative = false;
        if (!decode_raw(cursor,
                        static_cast<std::uint8_t>(encoding & 0x0fU),
                        raw, negative, field_offset)) {
            return false;
        }

        std::uint64_t base = 0;
        if (application == 0x00U || application == 0x50U) {
            if (negative) {
                return fail(ErrorCode::invalid_encoding, field_offset,
                            "absolute FDE address is negative");
            }
            value = raw;
            return true;
        } else if (application == 0x10U) {  // DW_EH_PE_pcrel
            const std::uint64_t field_address =
                static_cast<std::uint64_t>(field_offset);
            if (field_address > kMaxU64 - options_.section_address) {
                return fail(ErrorCode::address_overflow, field_offset,
                            "section base plus field offset overflows");
            }
            base = options_.section_address +
                   field_address;
        } else if (application == 0x20U) {  // DW_EH_PE_textrel
            base = options_.text_address;
        } else if (application == 0x30U) {  // DW_EH_PE_datarel
            base = options_.section_address;
        } else {
            return fail(ErrorCode::unsupported_encoding, field_offset,
                        "unsupported FDE address application");
        }

        if (negative) {
            if (!add_signed(base, signed_from_bits(raw), value)) {
                return fail(ErrorCode::address_overflow, field_offset,
                            "relative FDE address underflows");
            }
        } else {
            if (raw > kMaxU64 - base) {
                return fail(ErrorCode::address_overflow, field_offset,
                            "relative FDE address overflows");
            }
            value = base + raw;
        }
        return true;
    }

    bool decode_range(Cursor& cursor,
                      std::uint8_t format,
                      std::uint64_t& value) {
        const std::size_t field_offset = cursor.position();
        bool negative = false;
        if (!decode_raw(cursor, format, value, negative, field_offset)) {
            return false;
        }
        if (negative) {
            return fail(ErrorCode::fde_range_invalid, field_offset,
                        "FDE range is negative");
        }
        return true;
    }

    bool skip_encoded(Cursor& cursor, std::uint8_t encoding) {
        const std::size_t encoding_offset = cursor.position();
        if ((encoding & 0x0fU) == 0x0fU) {
            return fail(ErrorCode::invalid_encoding, encoding_offset,
                        "omitted personality pointer encoding");
        }
        const std::uint8_t application =
            static_cast<std::uint8_t>(encoding & 0x70U);
        if (application == 0x50U) {
            if (!cursor.align_to(
                    static_cast<std::size_t>(options_.address_size))) {
                return fail(ErrorCode::unsupported_encoding, encoding_offset,
                            "cannot align personality pointer");
            }
        } else if (application == 0x40U || application == 0x60U ||
                   application == 0x70U) {
            return fail(ErrorCode::unsupported_encoding, encoding_offset,
                        "unsupported personality pointer application");
        }

        std::uint64_t ignored = 0;
        bool negative = false;
        return decode_raw(cursor,
                          static_cast<std::uint8_t>(encoding & 0x0fU),
                          ignored, negative, encoding_offset);
    }

    bool check_overlaps() {
        std::sort(intervals_.begin(), intervals_.end(),
                  [](const CodeInterval& left, const CodeInterval& right) {
                      if (left.begin != right.begin) {
                          return left.begin < right.begin;
                      }
                      return left.end < right.end;
                  });
        for (std::size_t index = 1; index < intervals_.size(); ++index) {
            if (intervals_[index].begin < intervals_[index - 1U].end) {
                return fail(ErrorCode::fde_overlap,
                            intervals_[index].record_offset,
                            "overlapping FDE address ranges");
            }
        }
        return true;
    }

    const std::uint8_t* data_;
    std::size_t size_;
    const ValidationOptions& options_;
    ValidationError error_{};
    std::vector<CieInfo> cies_;
    std::vector<CodeInterval> intervals_;
    std::size_t instruction_count_ = 0;
};

}  // namespace

ValidationResult validate(const std::uint8_t* data,
                          std::size_t size,
                          const ValidationOptions& options) {
    try {
        Validator validator(data, size, options);
        return validator.run();
    } catch (const std::bad_alloc&) {
        ValidationResult result;
        result.error = ValidationError{ErrorCode::allocation_failure, 0,
                                       "allocation failed during validation"};
        return result;
    }
}

}  // namespace ehframe
