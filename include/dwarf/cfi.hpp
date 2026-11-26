#ifndef DWARF_CFI_HPP
#define DWARF_CFI_HPP

// DWARF v5 call-frame information (CFI) decoder and frame-table evaluator.
//
// The library is intentionally usable by profilers and zero-overhead unwinders:
// it interprets CFI, stores opaque DWARF expression bytes without evaluating
// them, and returns the exact register rules that are in effect at a queried
// PC.

#include <cstddef>
#include <cstdint>
#include <map>
#include <optional>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace dwarf::cfi {

inline constexpr int version_major = 1;
inline constexpr int version_minor = 0;

// Principal DWARF CFA opcodes.  The low two bits of a byte select the compact
// forms; the remaining values are the extended opcodes.
inline constexpr std::uint8_t DW_CFA_advance_loc       = 0x40;
inline constexpr std::uint8_t DW_CFA_offset            = 0x80;
inline constexpr std::uint8_t DW_CFA_restore           = 0xc0;

inline constexpr std::uint8_t DW_CFA_nop               = 0x00;
inline constexpr std::uint8_t DW_CFA_set_loc           = 0x01;
inline constexpr std::uint8_t DW_CFA_advance_loc1      = 0x02;
inline constexpr std::uint8_t DW_CFA_advance_loc2      = 0x03;
inline constexpr std::uint8_t DW_CFA_advance_loc4      = 0x04;
inline constexpr std::uint8_t DW_CFA_offset_extended   = 0x05;
inline constexpr std::uint8_t DW_CFA_restore_extended  = 0x06;
inline constexpr std::uint8_t DW_CFA_undefined         = 0x07;
inline constexpr std::uint8_t DW_CFA_same_value        = 0x08;
inline constexpr std::uint8_t DW_CFA_register          = 0x09;
inline constexpr std::uint8_t DW_CFA_remember_state    = 0x0a;
inline constexpr std::uint8_t DW_CFA_restore_state     = 0x0b;
inline constexpr std::uint8_t DW_CFA_def_cfa           = 0x0c;
inline constexpr std::uint8_t DW_CFA_def_cfa_register  = 0x0d;
inline constexpr std::uint8_t DW_CFA_def_cfa_offset    = 0x0e;
inline constexpr std::uint8_t DW_CFA_def_cfa_expression = 0x0f;
inline constexpr std::uint8_t DW_CFA_expression        = 0x10;
inline constexpr std::uint8_t DW_CFA_offset_extended_sf = 0x11;
inline constexpr std::uint8_t DW_CFA_def_cfa_sf        = 0x12;
inline constexpr std::uint8_t DW_CFA_def_cfa_offset_sf = 0x13;
inline constexpr std::uint8_t DW_CFA_val_offset        = 0x14;
inline constexpr std::uint8_t DW_CFA_val_offset_sf     = 0x15;
inline constexpr std::uint8_t DW_CFA_val_expression    = 0x16;

// GNU extensions that occur in real .eh_frame sections.
inline constexpr std::uint8_t DW_CFA_GNU_window_save           = 0x2d;
inline constexpr std::uint8_t DW_CFA_GNU_args_size             = 0x2e;
inline constexpr std::uint8_t DW_CFA_GNU_negative_offset_extended = 0x2f;

// DW_EH_PE pointer encodings used by .eh_frame augmentation data.
inline constexpr std::uint8_t DW_EH_PE_absptr  = 0x00;
inline constexpr std::uint8_t DW_EH_PE_uleb128 = 0x01;
inline constexpr std::uint8_t DW_EH_PE_udata2  = 0x02;
inline constexpr std::uint8_t DW_EH_PE_udata4  = 0x03;
inline constexpr std::uint8_t DW_EH_PE_udata8  = 0x04;
inline constexpr std::uint8_t DW_EH_PE_sleb128 = 0x09;
inline constexpr std::uint8_t DW_EH_PE_sdata2  = 0x0a;
inline constexpr std::uint8_t DW_EH_PE_sdata4  = 0x0b;
inline constexpr std::uint8_t DW_EH_PE_sdata8  = 0x0c;
inline constexpr std::uint8_t DW_EH_PE_pcrel   = 0x10;
inline constexpr std::uint8_t DW_EH_PE_textrel = 0x20;
inline constexpr std::uint8_t DW_EH_PE_datarel = 0x30;
inline constexpr std::uint8_t DW_EH_PE_funcrel = 0x40;
inline constexpr std::uint8_t DW_EH_PE_aligned = 0x50;
inline constexpr std::uint8_t DW_EH_PE_indirect = 0x80;
inline constexpr std::uint8_t DW_EH_PE_omit    = 0xff;

enum class ErrorCode {
    none = 0,
    truncated_input,
    leb128_overflow,
    leb128_too_long,
    invalid_length,
    invalid_cie_id,
    unsupported_version,
    invalid_augmentation,
    invalid_pointer_encoding,
    missing_cie,
    invalid_fde,
    pc_out_of_range,
    state_stack_underflow,
    unknown_opcode,
    invalid_operand,
    arithmetic_overflow,
};

const char* error_code_name(ErrorCode code) noexcept;

class CfiError : public std::runtime_error {
public:
    CfiError(ErrorCode code, std::string message, std::size_t offset = 0);

    ErrorCode code() const noexcept { return code_; }
    std::size_t offset() const noexcept { return offset_; }

private:
    ErrorCode code_;
    std::size_t offset_;
};

struct Uleb128 {
    std::uint64_t value = 0;
    std::size_t length = 0;  // number of bytes consumed
};

struct Sleb128 {
    std::int64_t value = 0;
    std::size_t length = 0;  // number of bytes consumed
};

// Strict DWARF ULEB128/SLEB128 decoders.
//
// A value may occupy at most ceil(64 / 7) == 10 bytes.  Values that need
// bits above bit 63 are rejected with leb128_overflow.  A continuation byte
// at the end of the input is rejected with truncated_input.  For SLEB128 the
// sign bit is the high bit of the last byte and negative values are
// sign-extended from that byte, including the special 10-byte forms for
// INT64_MIN.
Uleb128 decode_uleb128(std::span<const std::uint8_t> input);
Sleb128 decode_sleb128(std::span<const std::uint8_t> input);

bool try_decode_uleb128(std::span<const std::uint8_t> input,
                        std::uint64_t& value,
                        std::size_t& consumed,
                        ErrorCode* error = nullptr,
                        std::string* message = nullptr) noexcept;
bool try_decode_sleb128(std::span<const std::uint8_t> input,
                        std::int64_t& value,
                        std::size_t& consumed,
                        ErrorCode* error = nullptr,
                        std::string* message = nullptr) noexcept;

// Convenience encoders, primarily useful to producers and tests.
std::vector<std::uint8_t> encode_uleb128(std::uint64_t value);
std::vector<std::uint8_t> encode_sleb128(std::int64_t value);

enum class SectionKind {
    auto_detect,
    debug_frame,
    eh_frame,
};

struct ParseOptions {
    SectionKind kind = SectionKind::auto_detect;
    std::uint8_t address_size = 8;
    bool little_endian = true;

    // Virtual address corresponding to input[0].  It is used to resolve
    // pcrel/datarel/textrel/funcrel encoded FDE addresses.
    std::uint64_t section_address = 0;
    std::uint64_t text_address = 0;
    std::uint64_t data_address = 0;
    std::uint64_t function_address = 0;

    // Offset added to reported instruction/section offsets.  Useful when a
    // section was copied into the middle of a larger buffer.
    std::uint64_t section_offset = 0;
};

struct Cie {
    std::uint64_t offset = 0;          // offset of the length field
    std::uint64_t length = 0;          // length excluding the length field
    bool dwarf64 = false;
    SectionKind kind = SectionKind::auto_detect;

    std::uint8_t version = 0;
    std::string augmentation;
    std::uint64_t code_alignment_factor = 1;
    std::int64_t data_alignment_factor = 1;
    std::uint64_t return_address_register = 0;
    std::uint64_t initial_instructions_offset = 0;
    std::vector<std::uint8_t> initial_instructions;

    std::uint8_t address_size = 8;
    bool has_z_augmentation = false;
    std::optional<std::uint8_t> fde_encoding;
    std::optional<std::uint8_t> lsda_encoding;
    std::optional<std::uint8_t> personality_encoding;
    std::uint64_t personality = 0;
    bool personality_indirect = false;
    bool signal_frame = false;
    std::vector<std::uint8_t> augmentation_data;
};

struct Fde {
    std::uint64_t offset = 0;  // offset of the length field
    std::uint64_t length = 0;
    bool dwarf64 = false;

    // For .eh_frame this is the distance from the CIE_pointer field back to
    // the CIE.  For .debug_frame it is the section offset of the CIE.
    std::uint64_t cie_pointer = 0;
    std::uint64_t cie_offset = 0;
    std::size_t cie_index = static_cast<std::size_t>(-1);
    SectionKind kind = SectionKind::auto_detect;

    std::uint64_t initial_location = 0;
    std::uint64_t address_range = 0;
    std::uint64_t instructions_offset = 0;
    std::vector<std::uint8_t> instructions;

    bool has_cie() const noexcept { return cie_index != static_cast<std::size_t>(-1); }
    bool contains(std::uint64_t pc) const noexcept {
        return pc >= initial_location && (pc - initial_location) < address_range;
    }
};

// Parses one complete entry.  The input must begin at the entry's length
// field; trailing bytes are left untouched.
Cie parse_cie(std::span<const std::uint8_t> entry,
              const ParseOptions& options = {});
Fde parse_fde(std::span<const std::uint8_t> entry,
              const Cie& cie,
              const ParseOptions& options = {});

struct FrameRow;

struct CfiSection {
    std::vector<Cie> cies;
    std::vector<Fde> fdes;

    const Cie* find_cie(std::uint64_t offset) const noexcept;
    const Cie* cie_for(const Fde& fde) const noexcept;
    const Fde* find_fde(std::uint64_t pc) const noexcept;

    // Finds the FDE containing pc, builds its frame table, and evaluates pc.
    FrameRow lookup(std::uint64_t pc) const;
};

CfiSection parse_cfi_section(std::span<const std::uint8_t> section,
                             const ParseOptions& options = {});

enum class CfaRuleKind {
    undefined,
    reg_offset,  // CFA = register + offset
    expression,  // raw DWARF expression bytes in CfaRule::expression
};

struct CfaRule {
    CfaRuleKind kind = CfaRuleKind::undefined;
    std::uint64_t reg = 0;
    std::int64_t offset = 0;
    std::vector<std::uint8_t> expression;

    bool valid() const noexcept { return kind != CfaRuleKind::undefined; }
};

enum class RegisterRuleKind {
    undefined,
    same_value,
    offset,        // saved at CFA + offset
    val_offset,    // value is CFA + offset
    reg,           // value is in another register
    expression,    // raw expression in RegisterRule::expression
    val_expression,
};

struct RegisterRule {
    RegisterRuleKind kind = RegisterRuleKind::undefined;
    std::int64_t offset = 0;
    std::uint64_t reg = 0;
    std::vector<std::uint8_t> expression;
};

using RegisterRules = std::map<std::uint64_t, RegisterRule>;

struct FrameRow {
    // Address at which this row's rules begin.  lookup() leaves `location`
    // at the beginning of the row that covers the queried PC.
    std::uint64_t location = 0;

    CfaRule cfa;
    RegisterRules registers;
    std::uint64_t return_address_register = 0;

    const RegisterRule* find_register(std::uint64_t number) const noexcept {
        auto it = registers.find(number);
        return it == registers.end() ? nullptr : &it->second;
    }

    RegisterRule register_rule(std::uint64_t number) const {
        auto it = registers.find(number);
        return it == registers.end() ? RegisterRule{} : it->second;
    }

    const RegisterRule* return_address_rule() const noexcept {
        return find_register(return_address_register);
    }

    const RegisterRules& rules() const noexcept { return registers; }
};

struct EvalOptions {
    // Zero means "use Cie::address_size".
    std::uint8_t address_size = 0;
    bool little_endian = true;
};

class FrameTable {
public:
    FrameTable(const Cie& cie, const Fde& fde, const EvalOptions& options = {});

    // Throws CfiError(ErrorCode::pc_out_of_range) unless
    // initial_location <= pc < initial_location + address_range.
    FrameRow lookup(std::uint64_t pc) const;

    // Non-throwing variant.  On failure the error code and message are
    // populated when the corresponding pointer is non-null.
    bool try_lookup(std::uint64_t pc,
                    FrameRow& row,
                    ErrorCode* error = nullptr,
                    std::string* message = nullptr) const noexcept;

    // Returns every row produced by the FDE, in location order.
    std::vector<FrameRow> rows() const;

    const FrameRow& cie_initial_row() const noexcept { return initial_row_; }
    std::uint64_t return_address_register() const noexcept {
        return cie_.return_address_register;
    }
    bool contains(std::uint64_t pc) const noexcept {
        return fde_.contains(pc) && fde_.address_range != 0;
    }
    const Cie& cie() const noexcept { return cie_; }
    const Fde& fde() const noexcept { return fde_; }

private:
    Cie cie_;
    Fde fde_;
    EvalOptions options_;
    FrameRow initial_row_;
};

FrameRow evaluate_frame(const Cie& cie,
                        const Fde& fde,
                        std::uint64_t pc,
                        const EvalOptions& options = {});

std::vector<FrameRow> evaluate_frame_rows(const Cie& cie,
                                          const Fde& fde,
                                          const EvalOptions& options = {});

using CIE = Cie;
using FDE = Fde;
using UnwindRow = FrameRow;
using CfiEvaluator = FrameTable;

}  // namespace dwarf::cfi

namespace dwarf_cfi = dwarf::cfi;

#endif  // DWARF_CFI_HPP
