#ifndef EHFRAME_VALIDATOR_HPP
#define EHFRAME_VALIDATOR_HPP

#include <array>
#include <cstddef>
#include <cstdint>
#include <limits>

namespace ehframe {

// A deliberately small, fail-closed API for auditing untrusted .eh_frame data.
// All addresses are absolute virtual addresses. The caller must describe the
// stack window that is valid for the frame being audited. The window is
// [low, high): low is inclusive and high is exclusive.
struct FrameBounds {
    std::uint64_t low = 0;
    std::uint64_t high = 0;
    std::uint64_t stack_pointer = 0;
};

struct RegisterValue {
    std::uint32_t number = 0;
    std::uint64_t value = 0;
};

struct ValidationOptions {
    FrameBounds frame{};

    // DWARF register number used for the stack pointer. If no explicit
    // register_values entry is supplied for this register, frame.stack_pointer
    // is used as its value.
    std::uint32_t stack_pointer_register = 7;

    // Optional expected CIE return-address register. The maximum uint32 value
    // means "accept the register declared by the CIE" after max_register.
    std::uint32_t expected_return_address_register =
        std::numeric_limits<std::uint32_t>::max();

    // Upper bound for any DWARF register number in a CIE or CFA instruction.
    std::uint32_t max_register = 4095;

    // Address size used by absptr/set_loc operands. Must be 2, 4, or 8.
    std::uint8_t address_size = 8;

    // Runtime address of the first byte of the supplied .eh_frame section.
    // This is needed to resolve pcrel-encoded FDE initial locations.
    std::uint64_t section_address = 0;

    // Runtime address of .text, used only to resolve DW_EH_PE_textrel.
    std::uint64_t text_address = 0;

    bool big_endian = false;

    std::size_t max_state_stack = 64;
    std::size_t max_expression_steps = 256;
    std::size_t max_expression_depth = 32;
    std::size_t max_records = 65536;
    std::size_t max_instructions = 1U << 20U;

    // Extra CFA-base register values. This is useful when an FDE changes the
    // CFA register (for example from rsp to rbp) during the prologue.
    std::array<RegisterValue, 16> register_values{};
    std::size_t register_value_count = 0;

    static ValidationOptions for_frame(std::uint64_t low,
                                       std::uint64_t high,
                                       std::uint64_t stack_pointer,
                                       std::uint32_t stack_pointer_register = 7) {
        ValidationOptions options;
        options.frame.low = low;
        options.frame.high = high;
        options.frame.stack_pointer = stack_pointer;
        options.stack_pointer_register = stack_pointer_register;
        return options;
    }

    bool add_register_value(std::uint32_t number, std::uint64_t value) {
        if (register_value_count >= register_values.size()) {
            return false;
        }
        register_values[register_value_count++] = RegisterValue{number, value};
        return true;
    }
};

enum class ErrorCode : std::uint8_t {
    none = 0,
    invalid_options,
    input_truncated,
    record_length_truncated,
    record_length_invalid,
    record_length_overflow,
    record_too_short,
    trailing_data_after_terminator,
    unsupported_version,
    invalid_augmentation,
    augmentation_length_invalid,
    invalid_alignment,
    invalid_register,
    unexpected_return_address_register,
    invalid_encoding,
    unsupported_encoding,
    pointer_dereference_forbidden,
    address_overflow,
    fde_range_invalid,
    fde_range_overflow,
    fde_overlap,
    cie_pointer_forward,
    cie_pointer_invalid,
    cie_target_not_cie,
    malformed_leb128,
    instruction_out_of_bounds,
    unsupported_cfa_opcode,
    location_in_cie,
    location_overflow,
    location_out_of_range,
    state_stack_overflow,
    state_stack_underflow,
    cfa_undefined,
    invalid_cfa_operation,
    unknown_cfa_base,
    cfa_outside_frame,
    invalid_ra_rule,
    ra_outside_frame,
    rule_outside_frame,
    expression_empty,
    expression_steps_exceeded,
    expression_depth_exceeded,
    expression_stack_underflow,
    expression_stack_overflow,
    expression_deref_forbidden,
    expression_unsupported_opcode,
    expression_invalid_operand,
    expression_not_one_value,
    expression_outside_frame,
    too_many_records,
    too_many_instructions,
    allocation_failure
};

struct ValidationError {
    ErrorCode code = ErrorCode::none;
    std::size_t offset = 0;
    const char* message = "";
};

struct ValidationResult {
    ValidationError error{};

    [[nodiscard]] bool ok() const noexcept {
        return error.code == ErrorCode::none;
    }

    [[nodiscard]] explicit operator bool() const noexcept {
        return ok();
    }
};

// Validates one complete .eh_frame byte stream. The function never partially
// accepts a stream: any malformed record, unsafe CFA rule, or unsupported
// opcode makes the result fail with the first detected error.
[[nodiscard]] ValidationResult validate(const std::uint8_t* data,
                                        std::size_t size,
                                        const ValidationOptions& options);

}  // namespace ehframe

#endif  // EHFRAME_VALIDATOR_HPP
