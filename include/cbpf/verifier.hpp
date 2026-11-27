#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <string>
#include <vector>

namespace cbpf {

inline constexpr std::size_t kMaxInstructions = 4096U;
inline constexpr std::size_t kScratchWords = 16U;
inline constexpr std::size_t kSeccompDataSize = 64U;

inline constexpr std::uint32_t kAuditArchX86_64 = 0xC000003EU;
inline constexpr std::uint32_t kAuditArchI386 = 0x40000003U;
inline constexpr std::uint32_t kX32SyscallBit = 0x40000000U;
inline constexpr std::uint32_t kSeccompRetAllow = 0x7FFF0000U;

struct SockFilter {
    std::uint16_t code = 0U;
    std::uint8_t jt = 0U;
    std::uint8_t jf = 0U;
    std::uint32_t k = 0U;
};

using sock_filter = SockFilter;
using Program = std::vector<SockFilter>;

enum class ValidationErrorKind {
    empty_program,
    program_too_long,
    unknown_instruction,
    invalid_operand,
    out_of_bounds_jump,
    backward_jump,
    falls_off_program,
    missing_terminal_return,
    uninitialized_register,
    uninitialized_scratch,
    invalid_seccomp_offset,
};

struct ValidationError {
    ValidationErrorKind kind = ValidationErrorKind::unknown_instruction;
    std::size_t pc = 0U;
    std::string message;
};

struct ValidationResult {
    std::vector<ValidationError> errors;

    [[nodiscard]] bool valid() const noexcept { return errors.empty(); }
};

struct ArgumentRule {
    std::uint8_t argument = 0U;
    std::uint64_t forbidden_mask = 0U;
    std::uint64_t forbidden_value = 0U;

    // When true, the analyzer also checks whether a path accepts a non-zero
    // upper 32-bit half while satisfying this argument condition.
    bool require_upper_half_check = true;
};

struct DeniedSyscallRule {
    std::uint32_t number = 0U;
    // If true, every allowed input with this exact syscall number is a
    // finding. Arguments can still add a narrower forbidden condition.
    bool deny_number = true;
    std::vector<ArgumentRule> arguments;
};

struct Policy {
    std::uint32_t target_arch = kAuditArchX86_64;
    std::vector<DeniedSyscallRule> denied_syscalls;
    bool reject_foreign_arch = true;
    bool reject_x32 = true;
};

struct Witness {
    std::uint32_t arch = 0U;
    std::uint32_t nr = 0U;
    std::array<std::uint64_t, 6U> args{};
    std::uint64_t instruction_pointer = 0U;
};

enum class ViolationKind {
    foreign_architecture,
    x32_abi,
    denied_syscall,
    denied_argument,
    upper_half_ignored,
};

struct Finding {
    ViolationKind kind = ViolationKind::denied_syscall;
    Witness witness;
    std::size_t return_pc = 0U;
    std::optional<std::size_t> rule_index;
    std::optional<std::uint8_t> argument_index;
    std::string detail;
};

struct AnalysisResult {
    ValidationResult validation;
    std::vector<Finding> findings;

    [[nodiscard]] bool secure() const noexcept {
        return validation.valid() && findings.empty();
    }
};

[[nodiscard]] ValidationResult validate(const Program& program);
[[nodiscard]] AnalysisResult analyze(const Program& program, const Policy& policy);

[[nodiscard]] const char* to_string(ValidationErrorKind kind) noexcept;
[[nodiscard]] const char* to_string(ViolationKind kind) noexcept;

}  // namespace cbpf
