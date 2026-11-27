#include "cbpf/verifier.hpp"

#include <array>
#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <string>
#include <vector>

namespace {

constexpr std::uint16_t kLdWImm = 0x00U;
constexpr std::uint16_t kLdWAbs = 0x20U;
constexpr std::uint16_t kLdWMem = 0x60U;
constexpr std::uint16_t kLdxWMem = 0x61U;
constexpr std::uint16_t kSt = 0x02U;
constexpr std::uint16_t kStx = 0x03U;
constexpr std::uint16_t kAluAddK = 0x04U;
constexpr std::uint16_t kAluSubK = 0x14U;
constexpr std::uint16_t kAluDivK = 0x34U;
constexpr std::uint16_t kAluAndK = 0x54U;
constexpr std::uint16_t kAluLshK = 0x64U;
constexpr std::uint16_t kJa = 0x05U;
constexpr std::uint16_t kJeqK = 0x15U;
constexpr std::uint16_t kJgeK = 0x35U;
constexpr std::uint16_t kJsetK = 0x45U;
constexpr std::uint16_t kRetK = 0x06U;
constexpr std::uint16_t kRetA = 0x16U;
constexpr std::uint16_t kTax = 0x07U;
constexpr std::uint16_t kTxa = 0x87U;

constexpr std::uint32_t kNrOffset = 0U;
constexpr std::uint32_t kArchOffset = 4U;
constexpr std::uint32_t kArg0LowOffset = 16U;
constexpr std::uint32_t kArg0HighOffset = 20U;

constexpr std::uint32_t kExecve = 59U;
constexpr std::uint32_t kClone = 56U;
constexpr std::uint32_t kGetpid = 39U;
constexpr std::uint32_t kOpenat = 257U;

int failures = 0;

void check(bool condition, const std::string& message) {
    if (!condition) {
        ++failures;
        std::cerr << "FAIL: " << message << '\n';
    }
}

cbpf::SockFilter stmt(std::uint16_t code, std::uint32_t k = 0U) {
    return cbpf::SockFilter{code, 0U, 0U, k};
}

cbpf::SockFilter jump(std::uint16_t code, std::uint32_t k, std::uint8_t jt,
                      std::uint8_t jf) {
    return cbpf::SockFilter{code, jt, jf, k};
}

bool has_error(const cbpf::ValidationResult& result, cbpf::ValidationErrorKind kind) {
    for (const cbpf::ValidationError& error : result.errors) {
        if (error.kind == kind) {
            return true;
        }
    }
    return false;
}

const cbpf::Finding* find_kind(const cbpf::AnalysisResult& result,
                               cbpf::ViolationKind kind) {
    for (const cbpf::Finding& finding : result.findings) {
        if (finding.kind == kind) {
            return &finding;
        }
    }
    return nullptr;
}

std::size_t count_kind(const cbpf::AnalysisResult& result,
                       cbpf::ViolationKind kind) {
    std::size_t count = 0U;
    for (const cbpf::Finding& finding : result.findings) {
        if (finding.kind == kind) {
            ++count;
        }
    }
    return count;
}

cbpf::Policy execve_policy() {
    cbpf::Policy policy;
    cbpf::DeniedSyscallRule rule;
    rule.number = kExecve;
    policy.denied_syscalls.push_back(rule);
    return policy;
}

std::uint32_t execute_witness(const cbpf::Program& program, const cbpf::Witness& witness) {
    std::array<std::uint32_t, 16U> data{};
    data[0] = witness.nr;
    data[1] = witness.arch;
    data[2] = static_cast<std::uint32_t>(witness.instruction_pointer);
    data[3] = static_cast<std::uint32_t>(witness.instruction_pointer >> 32U);
    for (std::size_t argument = 0U; argument < 6U; ++argument) {
        data[4U + argument * 2U] = static_cast<std::uint32_t>(witness.args[argument]);
        data[5U + argument * 2U] =
            static_cast<std::uint32_t>(witness.args[argument] >> 32U);
    }

    std::uint32_t a = 0U;
    std::uint32_t x = 0U;
    std::array<std::uint32_t, 16U> memory{};
    std::size_t pc = 0U;
    for (std::size_t steps = 0U; steps < 100000U; ++steps) {
        if (pc >= program.size()) {
            return 0xFFFFFFFFU;
        }
        const cbpf::SockFilter& instruction = program[pc];
        const std::uint16_t code = instruction.code;
        const std::uint16_t instruction_class = static_cast<std::uint16_t>(code & 0x0007U);
        const std::uint16_t mode = static_cast<std::uint16_t>(code & 0x00E0U);
        const std::uint16_t op = static_cast<std::uint16_t>(code & 0x00F0U);
        const bool uses_x = (code & 0x0008U) != 0U;

        if (instruction_class == 0x00U) {
            if (mode == 0x00U) {
                a = instruction.k;
            } else if (mode == 0x20U) {
                a = data[static_cast<std::size_t>(instruction.k / 4U)];
            } else {
                a = memory[static_cast<std::size_t>(instruction.k)];
            }
            ++pc;
            continue;
        }
        if (instruction_class == 0x01U) {
            if (mode == 0x00U) {
                x = instruction.k;
            } else {
                x = memory[static_cast<std::size_t>(instruction.k)];
            }
            ++pc;
            continue;
        }
        if (instruction_class == 0x02U) {
            memory[static_cast<std::size_t>(instruction.k)] = a;
            ++pc;
            continue;
        }
        if (instruction_class == 0x03U) {
            memory[static_cast<std::size_t>(instruction.k)] = x;
            ++pc;
            continue;
        }
        if (instruction_class == 0x04U) {
            const std::uint32_t right = uses_x ? x : instruction.k;
            switch (op) {
                case 0x00U:
                    a = static_cast<std::uint32_t>(a + right);
                    break;
                case 0x10U:
                    a = static_cast<std::uint32_t>(a - right);
                    break;
                case 0x20U:
                    a = static_cast<std::uint32_t>(a * right);
                    break;
                case 0x30U:
                    a = right == 0U ? 0U : a / right;
                    break;
                case 0x40U:
                    a |= right;
                    break;
                case 0x50U:
                    a &= right;
                    break;
                case 0x60U:
                    a = right >= 32U ? 0U : static_cast<std::uint32_t>(a << right);
                    break;
                case 0x70U:
                    a = right >= 32U ? 0U : a >> right;
                    break;
                case 0x80U:
                    a = static_cast<std::uint32_t>(0U - a);
                    break;
                case 0x90U:
                    a = right == 0U ? 0U : a % right;
                    break;
                case 0xA0U:
                    a ^= right;
                    break;
                default:
                    return 0xFFFFFFFFU;
            }
            ++pc;
            continue;
        }
        if (instruction_class == 0x05U) {
            if (op == 0x00U) {
                pc = pc + 1U + static_cast<std::size_t>(instruction.k);
                continue;
            }
            const std::uint32_t right = uses_x ? x : instruction.k;
            bool taken = false;
            switch (op) {
                case 0x10U:
                    taken = a == right;
                    break;
                case 0x20U:
                    taken = a > right;
                    break;
                case 0x30U:
                    taken = a >= right;
                    break;
                case 0x40U:
                    taken = (a & right) != 0U;
                    break;
                default:
                    return 0xFFFFFFFFU;
            }
            pc = pc + 1U + static_cast<std::size_t>(taken ? instruction.jt : instruction.jf);
            continue;
        }
        if (instruction_class == 0x06U) {
            const std::uint16_t source = static_cast<std::uint16_t>(code & 0x0018U);
            return source == 0x10U ? a : instruction.k;
        }
        if (instruction_class == 0x07U) {
            if (code == kTax) {
                x = a;
            } else {
                a = x;
            }
            ++pc;
            continue;
        }
        return 0xFFFFFFFFU;
    }
    return 0xFFFFFFFFU;
}

void check_witnesses(const cbpf::Program& program, const cbpf::AnalysisResult& result) {
    for (const cbpf::Finding& finding : result.findings) {
        check(execute_witness(program, finding.witness) == cbpf::kSeccompRetAllow,
              std::string("witness must replay to ALLOW: ") + cbpf::to_string(finding.kind));
    }
}

void test_static_validation() {
    check(cbpf::validate({}).valid() == false, "empty program must be rejected");

    cbpf::Program too_long(4097U, stmt(kRetK, cbpf::kSeccompRetAllow));
    check(cbpf::validate(too_long).valid() == false,
          "programs longer than 4096 instructions must be rejected");
    cbpf::Program at_limit(4096U, stmt(kRetK, cbpf::kSeccompRetAllow));
    check(cbpf::validate(at_limit).valid(),
          "a 4096-instruction program must be accepted");

    cbpf::Program missing_return = {stmt(kLdWImm, 1U), stmt(kAluAddK, 1U)};
    check(has_error(cbpf::validate(missing_return),
                    cbpf::ValidationErrorKind::missing_terminal_return),
          "missing final return must be rejected");

    cbpf::Program bad_jump = {jump(kJa, 1U, 0U, 0U), stmt(kRetK, 0U)};
    check(has_error(cbpf::validate(bad_jump),
                    cbpf::ValidationErrorKind::out_of_bounds_jump),
          "out-of-bounds jump must be rejected");

    cbpf::Program bad_offset = {stmt(kLdWAbs, 62U), stmt(kRetK, 0U)};
    check(has_error(cbpf::validate(bad_offset),
                    cbpf::ValidationErrorKind::invalid_seccomp_offset),
          "misaligned/out-of-range seccomp_data load must be rejected");

    cbpf::Program uninitialized_ret = {stmt(kRetA)};
    check(has_error(cbpf::validate(uninitialized_ret),
                    cbpf::ValidationErrorKind::uninitialized_register),
          "RET A with an uninitialized A register must be rejected");

    cbpf::Program uninitialized_scratch = {
        stmt(kLdWMem, 3U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
    };
    check(has_error(cbpf::validate(uninitialized_scratch),
                    cbpf::ValidationErrorKind::uninitialized_scratch),
          "reading an uninitialized scratch slot must be rejected");

    cbpf::Program division_by_zero = {
        stmt(kLdWImm, 5U),
        stmt(kAluDivK, 0U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
    };
    check(has_error(cbpf::validate(division_by_zero),
                    cbpf::ValidationErrorKind::invalid_operand),
          "constant division by zero must be rejected");

    cbpf::Program invalid_shift = {
        stmt(kLdWImm, 5U),
        stmt(kAluLshK, 32U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
    };
    check(has_error(cbpf::validate(invalid_shift),
                    cbpf::ValidationErrorKind::invalid_operand),
          "constant shift count of 32 or more must be rejected");

    cbpf::Program valid_register_program = {
        stmt(kLdWImm, 7U),
        stmt(kTax),
        stmt(kSt, 0U),
        stmt(kTxa),
        stmt(kStx, 1U),
        stmt(kLdxWMem, 1U),
        stmt(kLdWMem, 1U),
        stmt(kRetA),
    };
    check(cbpf::validate(valid_register_program).valid(),
          "initialized register/scratch dataflow must be accepted");
}

void test_all_paths_allow_reports_all_core_classes() {
    cbpf::Program program = {stmt(kRetK, cbpf::kSeccompRetAllow)};
    cbpf::AnalysisResult result = cbpf::analyze(program, execve_policy());
    check(result.validation.valid(), "single RET ALLOW program should validate");

    const cbpf::Finding* foreign =
        find_kind(result, cbpf::ViolationKind::foreign_architecture);
    check(foreign != nullptr, "foreign architecture should reach allow");
    if (foreign != nullptr) {
        check(foreign->witness.arch == cbpf::kAuditArchI386,
              "foreign-architecture witness should prefer AUDIT_ARCH_I386");
    }

    const cbpf::Finding* x32 = find_kind(result, cbpf::ViolationKind::x32_abi);
    check(x32 != nullptr, "x32-marked syscall should reach allow");
    if (x32 != nullptr) {
        check((x32->witness.nr & cbpf::kX32SyscallBit) != 0U,
              "x32 witness must carry the 0x40000000 bit");
        check(x32->witness.nr == (kExecve | cbpf::kX32SyscallBit),
              "x32 witness should mask a denied syscall");
        check(x32->witness.arch == cbpf::kAuditArchX86_64,
              "x32 witness should use the target architecture");
    }

    const cbpf::Finding* denied =
        find_kind(result, cbpf::ViolationKind::denied_syscall);
    check(denied != nullptr, "denied syscall should reach allow");
    if (denied != nullptr) {
        check(denied->witness.nr == kExecve, "denied-syscall witness number mismatch");
        check(denied->witness.arch == cbpf::kAuditArchX86_64,
              "denied-syscall witness should use the target architecture");
    }
    check_witnesses(program, result);
}

void test_x32_bypass() {
    cbpf::Program program = {
        stmt(kLdWAbs, kNrOffset + 4U),
        jump(kJeqK, cbpf::kAuditArchX86_64, 0U, 5U),
        stmt(kLdWAbs, kNrOffset),
        jump(kJeqK, kExecve, 3U, 0U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
        stmt(kRetK, 0x00000000U),
        stmt(kRetK, 0x00000000U),
        stmt(kRetK, 0x00000000U),
    };
    cbpf::AnalysisResult result = cbpf::analyze(program, execve_policy());
    check(result.validation.valid(), "x32 test filter should validate");

    const cbpf::Finding* x32 = find_kind(result, cbpf::ViolationKind::x32_abi);
    check(x32 != nullptr, "missing x32 mask must be detected");
    if (x32 != nullptr) {
        check((x32->witness.nr & cbpf::kX32SyscallBit) != 0U,
              "x32 witness must set the x32 flag");
        check(x32->witness.nr == (kExecve | cbpf::kX32SyscallBit),
              "x32 witness should be the masked denied syscall number");
        check(x32->witness.arch == cbpf::kAuditArchX86_64,
              "x32 witness architecture should be X86_64");
    }
    check(find_kind(result, cbpf::ViolationKind::foreign_architecture) == nullptr,
          "x32 filter correctly rejects foreign architectures");
    check(find_kind(result, cbpf::ViolationKind::denied_syscall) == nullptr,
          "x32 filter still rejects the exact denied syscall number");
    check_witnesses(program, result);
}

void test_compat_32bit_escape() {
    cbpf::Program program = {
        stmt(kLdWAbs, kNrOffset),
        jump(kJeqK, kExecve, 1U, 0U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
        stmt(kRetK, 0x00000000U),
    };
    cbpf::Policy policy = execve_policy();
    policy.reject_x32 = false;
    cbpf::AnalysisResult result = cbpf::analyze(program, policy);
    check(result.validation.valid(), "compat test filter should validate");

    const cbpf::Finding* foreign =
        find_kind(result, cbpf::ViolationKind::foreign_architecture);
    check(foreign != nullptr, "missing architecture check must expose compat escape");
    if (foreign != nullptr) {
        check(foreign->witness.arch != cbpf::kAuditArchX86_64,
              "compat witness must use a non-target architecture");
        check(foreign->witness.nr != kExecve,
              "compat witness must use the architecture-specific syscall number");
    }
    check(count_kind(result, cbpf::ViolationKind::foreign_architecture) == 1U,
          "foreign architecture should be reported once");
    check(find_kind(result, cbpf::ViolationKind::denied_syscall) == nullptr,
          "exact X86_64 denied syscall remains blocked");
    check_witnesses(program, result);
}

void test_64bit_argument_truncation() {
    cbpf::Program program = {
        stmt(kLdWAbs, kArchOffset),
        jump(kJeqK, cbpf::kAuditArchX86_64, 0U, 8U),
        stmt(kLdWAbs, kNrOffset),
        jump(kJsetK, cbpf::kX32SyscallBit, 6U, 0U),
        jump(kJeqK, kClone, 1U, 0U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
        stmt(kLdWAbs, kArg0LowOffset),
        stmt(kAluAndK, 1U),
        jump(kJeqK, 1U, 1U, 0U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
        stmt(kRetK, 0x00000000U),
    };

    cbpf::Policy policy;
    cbpf::DeniedSyscallRule clone_rule;
    clone_rule.number = kClone;
    clone_rule.deny_number = false;
    cbpf::ArgumentRule argument;
    argument.argument = 0U;
    argument.forbidden_mask = 0x0000000100000000ULL;
    argument.forbidden_value = 0x0000000100000000ULL;
    clone_rule.arguments.push_back(argument);
    policy.denied_syscalls.push_back(clone_rule);

    cbpf::AnalysisResult result = cbpf::analyze(program, policy);
    check(result.validation.valid(), "argument-truncation filter should validate");

    const cbpf::Finding* denied_arg =
        find_kind(result, cbpf::ViolationKind::denied_argument);
    check(denied_arg != nullptr,
          "a forbidden 64-bit argument value must be detected");
    if (denied_arg != nullptr) {
        check((denied_arg->witness.args[0] & 0x0000000100000000ULL) != 0U,
              "denied-argument witness must set the forbidden high bit");
        check(denied_arg->witness.nr == kClone,
              "denied-argument witness must use clone");
    }

    const cbpf::Finding* upper =
        find_kind(result, cbpf::ViolationKind::upper_half_ignored);
    check(upper != nullptr, "upper-half truncation must be detected");
    if (upper != nullptr) {
        check((upper->witness.args[0] >> 32U) != 0U,
              "upper-half witness must have a non-zero high 32 bits");
    }
    check(find_kind(result, cbpf::ViolationKind::x32_abi) == nullptr,
          "truncation filter correctly rejects x32 syscalls");
    check(find_kind(result, cbpf::ViolationKind::foreign_architecture) == nullptr,
          "truncation filter correctly rejects foreign architectures");
    check(find_kind(result, cbpf::ViolationKind::denied_syscall) == nullptr,
          "clone is not configured as an unconditionally denied syscall");
    check_witnesses(program, result);
}

void test_compliant_whitelist() {
    cbpf::Program program = {
        stmt(kLdWAbs, kArchOffset),
        jump(kJeqK, cbpf::kAuditArchX86_64, 0U, 7U),
        stmt(kLdWAbs, kNrOffset),
        jump(kJsetK, cbpf::kX32SyscallBit, 5U, 0U),
        jump(kJeqK, kGetpid, 0U, 4U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
        stmt(kRetK, 0x00000000U),
        stmt(kRetK, 0x00000000U),
        stmt(kRetK, 0x00000000U),
        stmt(kRetK, 0x00000000U),
    };

    cbpf::Policy policy = execve_policy();
    cbpf::DeniedSyscallRule clone_rule;
    clone_rule.number = kClone;
    clone_rule.deny_number = false;
    cbpf::ArgumentRule argument;
    argument.argument = 0U;
    argument.forbidden_mask = 0x0000000100000001ULL;
    argument.forbidden_value = 0x0000000100000001ULL;
    clone_rule.arguments.push_back(argument);
    policy.denied_syscalls.push_back(clone_rule);

    cbpf::AnalysisResult result = cbpf::analyze(program, policy);
    check(result.validation.valid(), "whitelist filter should validate");
    check(result.secure(), "compliant whitelist filter should have no findings");
    check(result.findings.empty(), "compliant filter must not produce a witness");
    check_witnesses(program, result);
}

void test_openat_argument_index() {
    cbpf::Program program = {
        stmt(kLdWAbs, kArchOffset),
        jump(kJeqK, cbpf::kAuditArchX86_64, 0U, 8U),
        stmt(kLdWAbs, kNrOffset),
        jump(kJsetK, cbpf::kX32SyscallBit, 6U, 0U),
        jump(kJeqK, kOpenat, 1U, 0U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
        stmt(kLdWAbs, 16U + 2U * 8U),
        stmt(kAluAndK, 1U),
        jump(kJeqK, 1U, 1U, 0U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
        stmt(kRetK, 0x00000000U),
    };

    cbpf::Policy policy;
    cbpf::DeniedSyscallRule rule;
    rule.number = kOpenat;
    rule.deny_number = false;
    cbpf::ArgumentRule argument;
    argument.argument = 2U;
    argument.forbidden_mask = 0x0000000100000000ULL;
    argument.forbidden_value = 0x0000000100000000ULL;
    rule.arguments.push_back(argument);
    policy.denied_syscalls.push_back(rule);

    cbpf::AnalysisResult result = cbpf::analyze(program, policy);
    check(result.validation.valid(), "openat argument filter should validate");
    const cbpf::Finding* finding =
        find_kind(result, cbpf::ViolationKind::denied_argument);
    check(finding != nullptr, "argument index 2 must be analyzed as a 64-bit value");
    if (finding != nullptr) {
        check((finding->witness.args[2] & 0x0000000100000000ULL) != 0U,
              "openat witness must set the forbidden bit in args[2]");
        check(finding->witness.nr == kOpenat, "openat witness syscall mismatch");
    }
    check_witnesses(program, result);
}

void test_wide_argument_compliance() {
    cbpf::Program program = {
        stmt(kLdWAbs, kArchOffset),
        jump(kJeqK, cbpf::kAuditArchX86_64, 0U, 12U),
        stmt(kLdWAbs, kNrOffset),
        jump(kJsetK, cbpf::kX32SyscallBit, 10U, 0U),
        jump(kJeqK, kGetpid, 0U, 1U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
        jump(kJeqK, kClone, 0U, 7U),
        stmt(kLdWAbs, kArg0HighOffset),
        jump(kJeqK, 0U, 0U, 5U),
        stmt(kLdWAbs, kArg0LowOffset),
        stmt(kAluAndK, 1U),
        jump(kJeqK, 1U, 2U, 0U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
        stmt(kRetK, 0x00000000U),
        stmt(kRetK, 0x00000000U),
    };

    cbpf::Policy policy = execve_policy();
    cbpf::DeniedSyscallRule clone_rule;
    clone_rule.number = kClone;
    clone_rule.deny_number = false;
    cbpf::ArgumentRule argument;
    argument.argument = 0U;
    argument.forbidden_mask = 0x0000000100000000ULL;
    argument.forbidden_value = 0x0000000100000000ULL;
    clone_rule.arguments.push_back(argument);
    policy.denied_syscalls.push_back(clone_rule);

    cbpf::AnalysisResult result = cbpf::analyze(program, policy);
    check(result.validation.valid(), "wide-argument whitelist filter should validate");
    check(result.secure(),
          "checking the argument high half must prevent a truncation finding");
    check_witnesses(program, result);
}

void test_range_path_sensitivity() {
    cbpf::Program program = {
        stmt(kLdWAbs, kNrOffset),
        jump(kJgeK, 100U, 1U, 0U),
        stmt(kRetK, 0x00000000U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
    };
    cbpf::Policy policy = execve_policy();
    policy.reject_foreign_arch = false;
    policy.reject_x32 = false;
    cbpf::AnalysisResult blocked = cbpf::analyze(program, policy);
    check(blocked.validation.valid(), "range filter should validate");
    check(blocked.secure(), "denied syscall below the range must not be reported");

    policy.denied_syscalls[0].number = 150U;
    cbpf::AnalysisResult allowed = cbpf::analyze(program, policy);
    const cbpf::Finding* denied =
        find_kind(allowed, cbpf::ViolationKind::denied_syscall);
    check(denied != nullptr, "denied syscall above the range must be reported");
    if (denied != nullptr) {
        check(denied->witness.nr == 150U,
              "range witness must satisfy the symbolic lower-bound check");
    }
    check_witnesses(program, allowed);
}

void test_instruction_pointer_witness() {
    constexpr std::uint32_t kIpLow = 0x12345678U;
    cbpf::Program program = {
        stmt(kLdWAbs, 8U),
        jump(kJeqK, kIpLow, 1U, 0U),
        stmt(kRetK, 0x00000000U),
        stmt(kRetK, cbpf::kSeccompRetAllow),
    };
    cbpf::AnalysisResult result = cbpf::analyze(program, execve_policy());
    check(result.validation.valid(), "ip-constrained filter should validate");
    const cbpf::Finding* denied =
        find_kind(result, cbpf::ViolationKind::denied_syscall);
    check(denied != nullptr, "ip-constrained filter should still expose denied syscall");
    if (denied != nullptr) {
        check(static_cast<std::uint32_t>(denied->witness.instruction_pointer) == kIpLow,
              "witness must retain the path-constrained instruction pointer");
    }
    check_witnesses(program, result);
}

void test_ret_a_and_scratch_program() {
    cbpf::Program program = {
        stmt(kLdWImm, 0x12345678U),
        stmt(kSt, 0U),
        stmt(kLdxWMem, 0U),
        stmt(kTxa),
        stmt(kAluSubK, 0x12345678U - cbpf::kSeccompRetAllow),
        stmt(kRetA),
    };
    check(cbpf::validate(program).valid(),
          "scratch and TAX/TXA program should validate");
    cbpf::AnalysisResult result = cbpf::analyze(program, cbpf::Policy{});
    check(result.validation.valid(), "scratch return program should analyze");
    check(!result.findings.empty(), "RET A that computes ALLOW should be detected");
    check_witnesses(program, result);

    cbpf::Program never_allow = {
        stmt(kLdWImm, 0x12345678U),
        stmt(kRetA),
    };
    cbpf::AnalysisResult none = cbpf::analyze(never_allow, cbpf::Policy{});
    check(none.validation.valid(), "RET A never-allow program should validate");
    check(none.findings.empty(), "RET A with a non-ALLOW action must not be reported");
    check_witnesses(never_allow, none);
}

}  // namespace

int main() {
    test_static_validation();
    test_all_paths_allow_reports_all_core_classes();
    test_x32_bypass();
    test_compat_32bit_escape();
    test_64bit_argument_truncation();
    test_compliant_whitelist();
    test_openat_argument_index();
    test_wide_argument_compliance();
    test_range_path_sensitivity();
    test_instruction_pointer_witness();
    test_ret_a_and_scratch_program();

    if (failures != 0) {
        std::cerr << failures << " test(s) failed\n";
        return EXIT_FAILURE;
    }
    std::cout << "all cBPF verifier tests passed\n";
    return EXIT_SUCCESS;
}
