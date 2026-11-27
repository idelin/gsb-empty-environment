#include "ehframe/validator.hpp"

#include <cstddef>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, const char* expression, int line) {
    if (!condition) {
        std::fprintf(stderr, "FAIL line %d: %s\n", line, expression);
        ++failures;
    }
}

#define CHECK(expression) check((expression), #expression, __LINE__)

struct Buffer {
    std::vector<std::uint8_t> bytes;

    std::size_t size() const {
        return bytes.size();
    }

    void u8(std::uint8_t value) {
        bytes.push_back(value);
    }

    void u32(std::uint32_t value) {
        for (unsigned int index = 0; index < 4U; ++index) {
            bytes.push_back(static_cast<std::uint8_t>(
                (value >> static_cast<unsigned int>(index * 8U)) & 0xffU));
        }
    }

    void raw(const std::uint8_t* data, std::size_t length) {
        bytes.insert(bytes.end(), data, data + length);
    }

    void raw(const char* data) {
        raw(reinterpret_cast<const std::uint8_t*>(data), std::strlen(data));
    }

    void uleb(std::uint64_t value) {
        do {
            std::uint8_t byte = static_cast<std::uint8_t>(value & 0x7fU);
            value >>= 7U;
            if (value != 0U) {
                byte = static_cast<std::uint8_t>(byte | 0x80U);
            }
            bytes.push_back(byte);
        } while (value != 0U);
    }

    void sleb(std::int64_t value) {
        bool more = true;
        while (more) {
            std::uint8_t byte =
                static_cast<std::uint8_t>(value & static_cast<std::int64_t>(0x7f));
            value >>= 7;
            if ((value == 0 && (byte & 0x40U) == 0U) ||
                (value == -1 && (byte & 0x40U) != 0U)) {
                more = false;
            } else {
                byte = static_cast<std::uint8_t>(byte | 0x80U);
            }
            bytes.push_back(byte);
        }
    }

    void patch_u32(std::size_t offset, std::uint32_t value) {
        bytes[offset] = static_cast<std::uint8_t>(value & 0xffU);
        bytes[offset + 1U] =
            static_cast<std::uint8_t>((value >> 8U) & 0xffU);
        bytes[offset + 2U] =
            static_cast<std::uint8_t>((value >> 16U) & 0xffU);
        bytes[offset + 3U] =
            static_cast<std::uint8_t>((value >> 24U) & 0xffU);
    }
};

const std::uint8_t kCieInstructions[] = {
    0x0cU, 0x07U, 0x08U,  // DW_CFA_def_cfa rsp, 8
    0x80U, 0x10U, 0x01U   // DW_CFA_offset rip, 1 (data_align = -8)
};

ehframe::ValidationOptions default_options() {
    ehframe::ValidationOptions options =
        ehframe::ValidationOptions::for_frame(0x1000U, 0x2000U, 0x1f80U, 7U);
    options.address_size = 4U;
    options.max_register = 255U;
    options.expected_return_address_register = 16U;
    (void)options.add_register_value(6U, 0x1f88U);
    return options;
}

std::size_t append_cie(Buffer& buffer,
                       std::uint8_t fde_encoding,
                       std::uint32_t code_alignment,
                       std::int32_t data_alignment,
                       std::uint8_t return_address_register,
                       const std::uint8_t* instructions,
                       std::size_t instruction_count) {
    const std::size_t start = buffer.size();
    buffer.u32(0U);
    buffer.u32(0U);  // CIE id in .eh_frame
    buffer.u8(1U);   // version
    buffer.raw("zR");
    buffer.u8(0U);
    buffer.uleb(code_alignment);
    buffer.sleb(data_alignment);
    buffer.u8(return_address_register);
    buffer.uleb(1U);  // augmentation data length
    buffer.u8(fde_encoding);
    buffer.raw(instructions, instruction_count);
    const std::size_t length = buffer.size() - start - 4U;
    CHECK(length <= 0xffffffffU);
    buffer.patch_u32(start, static_cast<std::uint32_t>(length));
    return start;
}

std::size_t append_valid_cie(Buffer& buffer) {
    return append_cie(buffer, 0x00U, 1U, -8, 16U, kCieInstructions,
                      sizeof(kCieInstructions));
}

std::size_t append_fde(Buffer& buffer,
                       std::size_t cie_start,
                       std::uint32_t pc_begin,
                       std::uint32_t range,
                       const std::vector<std::uint8_t>& instructions) {
    const std::size_t start = buffer.size();
    buffer.u32(0U);
    const std::size_t pointer_offset = buffer.size();
    buffer.u32(static_cast<std::uint32_t>(pointer_offset - cie_start));
    buffer.u32(pc_begin);
    buffer.u32(range);
    buffer.uleb(0U);  // no FDE augmentation data
    buffer.raw(instructions.data(), instructions.size());
    const std::size_t length = buffer.size() - start - 4U;
    CHECK(length <= 0xffffffffU);
    buffer.patch_u32(start, static_cast<std::uint32_t>(length));
    return start;
}

ehframe::ValidationResult validate(const Buffer& buffer,
                                   const ehframe::ValidationOptions& options) {
    return ehframe::validate(buffer.bytes.data(), buffer.bytes.size(), options);
}

void expect_code(const ehframe::ValidationResult& result,
                 ehframe::ErrorCode code,
                 int line) {
    if (!result.ok() && result.error.code == code) {
        return;
    }
    std::fprintf(stderr,
                 "FAIL line %d: expected %u, got %u (%s) at offset %zu\n",
                 line,
                 static_cast<unsigned int>(code),
                 static_cast<unsigned int>(result.error.code),
                 result.error.message,
                 result.error.offset);
    ++failures;
}

#define EXPECT_CODE(result, code) expect_code((result), (code), __LINE__)

void test_normal_fde_state_machine() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    const std::vector<std::uint8_t> instructions = {
        0x41U,              // DW_CFA_advance_loc 1
        0x0dU, 0x06U,       // DW_CFA_def_cfa_register rbp
        0x0eU, 0x10U,       // DW_CFA_def_cfa_offset 16
        0x80U, 0x10U, 0x02U,// DW_CFA_offset rip, 2
        0x0aU,              // DW_CFA_remember_state
        0x0eU, 0x18U,       // DW_CFA_def_cfa_offset 24
        0x0bU,              // DW_CFA_restore_state
        0x03U, 0x10U, 0x00U,// DW_CFA_advance_loc2 16
        0x01U, 0x20U, 0x10U, 0x00U, 0x00U,  // DW_CFA_set_loc 0x1020
        0xc0U, 0x10U        // DW_CFA_restore rip
    };
    append_fde(buffer, cie, 0x1000U, 0x80U, instructions);

    const ehframe::ValidationResult result = validate(buffer, default_options());
    if (!result.ok()) {
        std::fprintf(stderr, "normal sample rejected: %s at %zu\n",
                     result.error.message, result.error.offset);
    }
    CHECK(result.ok());
}

void test_safe_def_cfa_expression() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    const std::vector<std::uint8_t> instructions = {
        0x0fU, 0x05U,        // DW_CFA_def_cfa_expression, length 5
        0x77U, 0x00U,        // DW_OP_breg7 0
        0x08U, 0x10U,        // DW_OP_const1u 16
        0x22U,               // DW_OP_plus
        0x80U, 0x10U, 0x02U  // DW_CFA_offset rip, 2
    };
    append_fde(buffer, cie, 0x1100U, 0x40U, instructions);

    const ehframe::ValidationResult result = validate(buffer, default_options());
    CHECK(result.ok());
}

void test_deref_ra_expression_rejected() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    const std::vector<std::uint8_t> instructions = {
        0x16U, 0x10U, 0x03U,  // DW_CFA_val_expression rip, length 3
        0x77U, 0x00U,         // DW_OP_breg7 0
        0x06U                 // DW_OP_deref
    };
    append_fde(buffer, cie, 0x1200U, 0x40U, instructions);

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::expression_deref_forbidden);
}

void test_safe_ra_expression_still_rejected() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    const std::vector<std::uint8_t> instructions = {
        0x16U, 0x10U, 0x02U,  // DW_CFA_val_expression rip, length 2
        0x77U, 0x00U          // DW_OP_breg7 0
    };
    append_fde(buffer, cie, 0x1250U, 0x40U, instructions);

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::invalid_ra_rule);
}

void test_overlapping_fde_rejected() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    append_fde(buffer, cie, 0x1300U, 0x80U, {});
    append_fde(buffer, cie, 0x1370U, 0x80U, {});

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::fde_overlap);
}

void test_empty_restore_state_rejected() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    append_fde(buffer, cie, 0x1400U, 0x40U, {0x0bU});

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::state_stack_underflow);
}

void test_remember_state_overflow_rejected() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    append_fde(buffer, cie, 0x1410U, 0x40U,
               {0x0aU, 0x0aU});
    ehframe::ValidationOptions options = default_options();
    options.max_state_stack = 1U;

    EXPECT_CODE(validate(buffer, options),
                ehframe::ErrorCode::state_stack_overflow);
}

void test_malformed_uleb128_rejected() {
    Buffer buffer;
    const std::size_t start = buffer.size();
    buffer.u32(0U);
    buffer.u32(0U);
    buffer.u8(1U);
    buffer.raw("zR");
    buffer.u8(0U);
    buffer.u8(0x80U);  // unterminated ULEB128
    buffer.patch_u32(start,
                     static_cast<std::uint32_t>(buffer.size() - start - 4U));

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::malformed_leb128);
}

void test_overlong_uleb128_rejected() {
    Buffer buffer;
    const std::size_t start = buffer.size();
    buffer.u32(0U);
    buffer.u32(0U);
    buffer.u8(1U);
    buffer.raw("zR");
    buffer.u8(0U);
    buffer.u8(0x80U);
    buffer.u8(0x00U);  // non-canonical encoding of zero
    buffer.patch_u32(start,
                     static_cast<std::uint32_t>(buffer.size() - start - 4U));

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::malformed_leb128);
}

void test_malformed_sleb128_rejected() {
    Buffer buffer;
    const std::size_t start = buffer.size();
    buffer.u32(0U);
    buffer.u32(0U);
    buffer.u8(1U);
    buffer.raw("zR");
    buffer.u8(0U);
    buffer.uleb(1U);      // code alignment
    buffer.u8(0x80U);     // unterminated SLEB128 data alignment
    buffer.patch_u32(start,
                     static_cast<std::uint32_t>(buffer.size() - start - 4U));

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::malformed_leb128);
}

void test_out_of_bounds_record_rejected() {
    Buffer buffer;
    buffer.u32(0x40U);
    buffer.u32(0U);

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::record_length_overflow);
}

void test_fde_range_wrap_rejected() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    append_fde(buffer, cie, 0xfffffff0U, 0x100U, {});

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::fde_range_overflow);
}

void test_ra_value_offset_rejected() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    append_fde(buffer, cie, 0x1500U, 0x40U,
               {0x14U, 0x10U, 0x01U});

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::invalid_ra_rule);
}

void test_ra_outside_frame_rejected() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    append_fde(buffer, cie, 0x1600U, 0x40U,
               {0x80U, 0x10U, 0x80U, 0x04U});  // factor 512 => -4096 bytes

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::ra_outside_frame);
}

void test_cie_pointer_must_target_cie() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    const std::size_t start = buffer.size();
    buffer.u32(0U);
    const std::size_t pointer_offset = buffer.size();
    buffer.u32(static_cast<std::uint32_t>(
        pointer_offset - cie - 1U));  // points inside the CIE, not at its start
    buffer.u32(0x1700U);
    buffer.u32(0x40U);
    buffer.uleb(0U);
    buffer.patch_u32(start,
                     static_cast<std::uint32_t>(buffer.size() - start - 4U));

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::cie_target_not_cie);
}

void test_cie_pointer_cycle_rejected() {
    Buffer buffer;
    (void)append_valid_cie(buffer);
    const std::size_t start = buffer.size();
    buffer.u32(0U);
    const std::size_t pointer_offset = buffer.size();
    buffer.u32(static_cast<std::uint32_t>(pointer_offset - start));
    buffer.u32(0x1750U);
    buffer.u32(0x40U);
    buffer.uleb(0U);
    buffer.patch_u32(start,
                     static_cast<std::uint32_t>(buffer.size() - start - 4U));

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::cie_pointer_forward);
}

void test_invalid_register_rejected() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    append_fde(buffer, cie, 0x1780U, 0x40U,
               {0x07U, 0x80U, 0x02U});  // DW_CFA_undefined r256

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::invalid_register);
}

void test_advance_beyond_fde_rejected() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    append_fde(buffer, cie, 0x1800U, 0x10U,
               {0x02U, 0x20U});  // DW_CFA_advance_loc1 32, range is 16

    EXPECT_CODE(validate(buffer, default_options()),
                ehframe::ErrorCode::location_out_of_range);
}

void test_expression_depth_limit() {
    Buffer buffer;
    const std::size_t cie = append_valid_cie(buffer);
    const std::vector<std::uint8_t> instructions = {
        0x0fU, 0x04U,        // DW_CFA_def_cfa_expression, length 4
        0x08U, 0x01U,        // DW_OP_const1u 1
        0x08U, 0x02U         // DW_OP_const1u 2
    };
    append_fde(buffer, cie, 0x1900U, 0x40U, instructions);
    ehframe::ValidationOptions options = default_options();
    options.max_expression_depth = 1U;

    EXPECT_CODE(validate(buffer, options),
                ehframe::ErrorCode::expression_stack_overflow);
}

void test_random_inputs_do_not_escape_bounds() {
    std::uint32_t state = 0x9e3779b9U;
    for (std::size_t iteration = 0; iteration < 4096U; ++iteration) {
        state ^= state << 13U;
        state ^= state >> 17U;
        state ^= state << 5U;
        const std::size_t length = static_cast<std::size_t>(state % 257U);
        Buffer buffer;
        buffer.bytes.resize(length);
        for (std::size_t index = 0; index < length; ++index) {
            state ^= state << 13U;
            state ^= state >> 17U;
            state ^= state << 5U;
            buffer.bytes[index] = static_cast<std::uint8_t>(state & 0xffU);
        }
        const ehframe::ValidationResult result =
            validate(buffer, default_options());
        CHECK(result.error.code <= ehframe::ErrorCode::allocation_failure);
    }
}

}  // namespace

int main() {
    test_normal_fde_state_machine();
    test_safe_def_cfa_expression();
    test_deref_ra_expression_rejected();
    test_safe_ra_expression_still_rejected();
    test_overlapping_fde_rejected();
    test_empty_restore_state_rejected();
    test_remember_state_overflow_rejected();
    test_malformed_uleb128_rejected();
    test_overlong_uleb128_rejected();
    test_malformed_sleb128_rejected();
    test_out_of_bounds_record_rejected();
    test_fde_range_wrap_rejected();
    test_ra_value_offset_rejected();
    test_ra_outside_frame_rejected();
    test_cie_pointer_must_target_cie();
    test_cie_pointer_cycle_rejected();
    test_invalid_register_rejected();
    test_advance_beyond_fde_rejected();
    test_expression_depth_limit();
    test_random_inputs_do_not_escape_bounds();

    if (failures != 0) {
        std::fprintf(stderr, "%d test failure(s)\n", failures);
        return EXIT_FAILURE;
    }
    std::printf("all .eh_frame validator tests passed\n");
    return EXIT_SUCCESS;
}
