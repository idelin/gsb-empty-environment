#include "cbpf/verifier.hpp"

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <optional>
#include <sstream>
#include <string>
#include <utility>
#include <vector>

namespace cbpf {
namespace detail {

constexpr std::uint16_t kClassMask = 0x0007U;
constexpr std::uint16_t kSizeMask = 0x0018U;
constexpr std::uint16_t kModeMask = 0x00E0U;
constexpr std::uint16_t kOpMask = 0x00F0U;
constexpr std::uint16_t kSourceMask = 0x0008U;

constexpr std::uint16_t kClassLd = 0x0000U;
constexpr std::uint16_t kClassLdx = 0x0001U;
constexpr std::uint16_t kClassSt = 0x0002U;
constexpr std::uint16_t kClassStx = 0x0003U;
constexpr std::uint16_t kClassAlu = 0x0004U;
constexpr std::uint16_t kClassJmp = 0x0005U;
constexpr std::uint16_t kClassRet = 0x0006U;
constexpr std::uint16_t kClassMisc = 0x0007U;

constexpr std::uint16_t kSizeWord = 0x0000U;
constexpr std::uint16_t kModeImm = 0x0000U;
constexpr std::uint16_t kModeAbs = 0x0020U;
constexpr std::uint16_t kModeMem = 0x0060U;

constexpr std::uint16_t kAluAdd = 0x0000U;
constexpr std::uint16_t kAluSub = 0x0010U;
constexpr std::uint16_t kAluMul = 0x0020U;
constexpr std::uint16_t kAluDiv = 0x0030U;
constexpr std::uint16_t kAluOr = 0x0040U;
constexpr std::uint16_t kAluAnd = 0x0050U;
constexpr std::uint16_t kAluLsh = 0x0060U;
constexpr std::uint16_t kAluRsh = 0x0070U;
constexpr std::uint16_t kAluNeg = 0x0080U;
constexpr std::uint16_t kAluMod = 0x0090U;
constexpr std::uint16_t kAluXor = 0x00A0U;

constexpr std::uint16_t kJmpJa = 0x0000U;
constexpr std::uint16_t kJmpJeq = 0x0010U;
constexpr std::uint16_t kJmpJgt = 0x0020U;
constexpr std::uint16_t kJmpJge = 0x0030U;
constexpr std::uint16_t kJmpJset = 0x0040U;

constexpr std::uint16_t kRetA = 0x0010U;
constexpr std::uint16_t kMiscTax = 0x0000U;
constexpr std::uint16_t kMiscTxa = 0x0080U;

struct DecodedInstruction {
    bool recognized = false;
    bool terminal = false;
    bool unconditional_jump = false;
    bool conditional_jump = false;
    bool jump_uses_x = false;
    std::uint16_t jump_op = kJmpJa;
    std::uint16_t alu_op = 0U;
    bool alu_uses_x = false;
};

[[nodiscard]] constexpr std::uint16_t instruction_class(std::uint16_t code) noexcept {
    return static_cast<std::uint16_t>(code & kClassMask);
}

[[nodiscard]] constexpr std::uint16_t instruction_size(std::uint16_t code) noexcept {
    return static_cast<std::uint16_t>(code & kSizeMask);
}

[[nodiscard]] constexpr std::uint16_t instruction_mode(std::uint16_t code) noexcept {
    return static_cast<std::uint16_t>(code & kModeMask);
}

[[nodiscard]] constexpr std::uint16_t instruction_op(std::uint16_t code) noexcept {
    return static_cast<std::uint16_t>(code & kOpMask);
}

[[nodiscard]] constexpr bool instruction_uses_x(std::uint16_t code) noexcept {
    return (code & kSourceMask) != 0U;
}

void add_unique_error(ValidationResult& result, ValidationErrorKind kind,
                      std::size_t pc, std::string message) {
    for (const ValidationError& existing : result.errors) {
        if (existing.kind == kind && existing.pc == pc) {
            return;
        }
    }
    result.errors.push_back(ValidationError{kind, pc, std::move(message)});
}

[[nodiscard]] bool is_valid_mem_index(std::uint32_t k) noexcept {
    return static_cast<std::size_t>(k) < kScratchWords;
}

[[nodiscard]] bool is_valid_seccomp_word_offset(std::uint32_t k) noexcept {
    return (k & 3U) == 0U && static_cast<std::uint64_t>(k) + 4U <=
                                  static_cast<std::uint64_t>(kSeccompDataSize);
}

[[nodiscard]] DecodedInstruction decode_instruction(const SockFilter& instruction,
                                                     ValidationResult& result,
                                                     std::size_t pc) {
    DecodedInstruction decoded;
    const std::uint16_t code = instruction.code;

    if ((code & 0xFF00U) != 0U) {
        add_unique_error(result, ValidationErrorKind::unknown_instruction, pc,
                         "instruction contains reserved opcode bits");
        return decoded;
    }

    switch (instruction_class(code)) {
        case kClassLd: {
            const std::uint16_t size = instruction_size(code);
            const std::uint16_t mode = instruction_mode(code);
            if (size != kSizeWord) {
                add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                                 "seccomp loads must be 32-bit word loads");
                return decoded;
            }
            if (mode == kModeImm) {
                decoded.recognized = true;
                return decoded;
            }
            if (mode == kModeAbs) {
                if (!is_valid_seccomp_word_offset(instruction.k)) {
                    add_unique_error(result, ValidationErrorKind::invalid_seccomp_offset, pc,
                                     "absolute load is outside aligned seccomp_data");
                    return decoded;
                }
                decoded.recognized = true;
                return decoded;
            }
            if (mode == kModeMem) {
                if (!is_valid_mem_index(instruction.k)) {
                    add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                                     "scratch-memory index is out of range");
                    return decoded;
                }
                decoded.recognized = true;
                return decoded;
            }
            add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                             "unsupported cBPF load mode");
            return decoded;
        }
        case kClassLdx: {
            const std::uint16_t size = instruction_size(code);
            const std::uint16_t mode = instruction_mode(code);
            if (size != kSizeWord) {
                add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                                 "index-register loads must be 32-bit word loads");
                return decoded;
            }
            if (mode == kModeImm || mode == kModeMem) {
                if (mode == kModeMem && !is_valid_mem_index(instruction.k)) {
                    add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                                     "scratch-memory index is out of range");
                    return decoded;
                }
                decoded.recognized = true;
                return decoded;
            }
            add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                             "unsupported cBPF index load mode");
            return decoded;
        }
        case kClassSt:
        case kClassStx: {
            if (instruction_size(code) != 0U || instruction_mode(code) != 0U) {
                add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                                 "scratch stores do not take size or mode fields");
                return decoded;
            }
            if (!is_valid_mem_index(instruction.k)) {
                add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                                 "scratch-memory index is out of range");
                return decoded;
            }
            decoded.recognized = true;
            return decoded;
        }
        case kClassAlu: {
            const std::uint16_t op = instruction_op(code);
            switch (op) {
                case kAluAdd:
                case kAluSub:
                case kAluMul:
                case kAluDiv:
                case kAluOr:
                case kAluAnd:
                case kAluLsh:
                case kAluRsh:
                case kAluMod:
                case kAluXor:
                    if (!instruction_uses_x(code) &&
                        (op == kAluDiv || op == kAluMod) && instruction.k == 0U) {
                        add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                                         "division or modulo by zero is invalid");
                        return decoded;
                    }
                    if (!instruction_uses_x(code) &&
                        (op == kAluLsh || op == kAluRsh) && instruction.k >= 32U) {
                        add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                                         "constant shift count must be less than 32");
                        return decoded;
                    }
                    decoded.alu_op = op;
                    decoded.alu_uses_x = instruction_uses_x(code);
                    decoded.recognized = true;
                    return decoded;
                case kAluNeg:
                    if (instruction_uses_x(code)) {
                        add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                                         "NEG does not accept an X source");
                        return decoded;
                    }
                    decoded.alu_op = op;
                    decoded.recognized = true;
                    return decoded;
                default:
                    add_unique_error(result, ValidationErrorKind::unknown_instruction, pc,
                                     "unknown cBPF ALU operation");
                    return decoded;
            }
        }
        case kClassJmp: {
            const std::uint16_t op = instruction_op(code);
            if (op == kJmpJa) {
                if (instruction_uses_x(code)) {
                    add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                                     "JA does not accept an X source");
                    return decoded;
                }
                decoded.recognized = true;
                decoded.unconditional_jump = true;
                return decoded;
            }
            if (op == kJmpJeq || op == kJmpJgt || op == kJmpJge || op == kJmpJset) {
                decoded.recognized = true;
                decoded.conditional_jump = true;
                decoded.jump_op = op;
                decoded.jump_uses_x = instruction_uses_x(code);
                return decoded;
            }
            add_unique_error(result, ValidationErrorKind::unknown_instruction, pc,
                             "unknown cBPF jump operation");
            return decoded;
        }
        case kClassRet: {
            const std::uint16_t source = static_cast<std::uint16_t>(code & 0x0018U);
            if (source == 0U || source == kRetA) {
                decoded.recognized = true;
                decoded.terminal = true;
                return decoded;
            }
            add_unique_error(result, ValidationErrorKind::invalid_operand, pc,
                             "return source must be K or A");
            return decoded;
        }
        case kClassMisc: {
            if (code == static_cast<std::uint16_t>(kClassMisc | kMiscTax) ||
                code == static_cast<std::uint16_t>(kClassMisc | kMiscTxa)) {
                decoded.recognized = true;
                return decoded;
            }
            add_unique_error(result, ValidationErrorKind::unknown_instruction, pc,
                             "unknown cBPF miscellaneous operation");
            return decoded;
        }
        default:
            add_unique_error(result, ValidationErrorKind::unknown_instruction, pc,
                             "unknown cBPF instruction class");
            return decoded;
    }
}

[[nodiscard]] std::vector<std::size_t> successors_for(
    const Program& program, std::size_t pc, const DecodedInstruction& decoded,
    ValidationResult& result) {
    const std::size_t length = program.size();
    std::vector<std::size_t> successors;

    auto check_target = [&](std::uint64_t target, const char* description) -> bool {
        if (target < static_cast<std::uint64_t>(pc)) {
            add_unique_error(result, ValidationErrorKind::backward_jump, pc,
                             std::string(description) + " jumps backwards");
            return false;
        }
        if (target >= static_cast<std::uint64_t>(length)) {
            add_unique_error(result, ValidationErrorKind::out_of_bounds_jump, pc,
                             std::string(description) + " leaves the program");
            return false;
        }
        return true;
    };

    if (decoded.terminal) {
        return successors;
    }
    if (!decoded.recognized) {
        return successors;
    }
    if (decoded.unconditional_jump) {
        const std::uint64_t target =
            static_cast<std::uint64_t>(pc) + 1U + static_cast<std::uint64_t>(program[pc].k);
        if (check_target(target, "JA")) {
            successors.push_back(static_cast<std::size_t>(target));
        }
        return successors;
    }
    if (decoded.conditional_jump) {
        const std::uint64_t target_true =
            static_cast<std::uint64_t>(pc) + 1U + static_cast<std::uint64_t>(program[pc].jt);
        const std::uint64_t target_false =
            static_cast<std::uint64_t>(pc) + 1U + static_cast<std::uint64_t>(program[pc].jf);
        if (check_target(target_true, "conditional-true")) {
            successors.push_back(static_cast<std::size_t>(target_true));
        }
        if (check_target(target_false, "conditional-false")) {
            successors.push_back(static_cast<std::size_t>(target_false));
        }
        return successors;
    }
    const std::uint64_t target = static_cast<std::uint64_t>(pc) + 1U;
    if (target >= static_cast<std::uint64_t>(length)) {
        add_unique_error(result, ValidationErrorKind::falls_off_program, pc,
                         "instruction falls off the end of the program");
        return successors;
    }
    successors.push_back(static_cast<std::size_t>(target));
    return successors;
}

struct InitState {
    bool a = false;
    bool x = false;
    std::array<bool, kScratchWords> memory{};
};

[[nodiscard]] InitState merge_states(const InitState& left, const InitState& right) {
    InitState merged;
    merged.a = left.a && right.a;
    merged.x = left.x && right.x;
    for (std::size_t i = 0U; i < kScratchWords; ++i) {
        merged.memory[i] = left.memory[i] && right.memory[i];
    }
    return merged;
}

[[nodiscard]] bool equal_states(const InitState& left, const InitState& right) {
    return left.a == right.a && left.x == right.x &&
           left.memory == right.memory;
}

void report_uninitialized(ValidationResult& result, std::size_t pc, bool a_missing,
                          bool x_missing, bool memory_missing, std::uint32_t memory_index) {
    if (a_missing) {
        add_unique_error(result, ValidationErrorKind::uninitialized_register, pc,
                         "A register is read before it is initialized");
    }
    if (x_missing) {
        add_unique_error(result, ValidationErrorKind::uninitialized_register, pc,
                         "X register is read before it is initialized");
    }
    if (memory_missing) {
        add_unique_error(result, ValidationErrorKind::uninitialized_scratch, pc,
                         "scratch slot " + std::to_string(memory_index) +
                             " is read before it is initialized");
    }
}

[[nodiscard]] InitState transfer_instruction(const Program& program, std::size_t pc,
                                              const DecodedInstruction& decoded,
                                              const InitState& input,
                                              ValidationResult& result) {
    InitState output = input;
    const SockFilter& instruction = program[pc];
    const std::uint16_t code = instruction.code;

    switch (instruction_class(code)) {
        case kClassLd: {
            const std::uint16_t mode = instruction_mode(code);
            if (mode == kModeImm || mode == kModeAbs) {
                output.a = true;
            } else if (mode == kModeMem) {
                const std::size_t index = static_cast<std::size_t>(instruction.k);
                if (!input.memory[index]) {
                    report_uninitialized(result, pc, false, false, true, instruction.k);
                    output.a = false;
                } else {
                    output.a = true;
                }
            }
            break;
        }
        case kClassLdx: {
            const std::uint16_t mode = instruction_mode(code);
            if (mode == kModeImm) {
                output.x = true;
            } else if (mode == kModeMem) {
                const std::size_t index = static_cast<std::size_t>(instruction.k);
                if (!input.memory[index]) {
                    report_uninitialized(result, pc, false, false, true, instruction.k);
                    output.x = false;
                } else {
                    output.x = true;
                }
            }
            break;
        }
        case kClassSt:
        case kClassStx: {
            const std::size_t index = static_cast<std::size_t>(instruction.k);
            if (instruction_class(code) == kClassSt) {
                if (!input.a) {
                    report_uninitialized(result, pc, true, false, false, instruction.k);
                }
                output.memory[index] = input.a;
            } else {
                if (!input.x) {
                    report_uninitialized(result, pc, false, true, false, instruction.k);
                }
                output.memory[index] = input.x;
            }
            break;
        }
        case kClassAlu: {
            const bool needs_x = decoded.alu_uses_x;
            if (!input.a || (needs_x && !input.x)) {
                report_uninitialized(result, pc, !input.a, needs_x && !input.x, false, 0U);
            }
            output.a = input.a && (!needs_x || input.x);
            break;
        }
        case kClassJmp: {
            if (decoded.conditional_jump) {
                const bool needs_x = decoded.jump_uses_x;
                if (!input.a || (needs_x && !input.x)) {
                    report_uninitialized(result, pc, !input.a, needs_x && !input.x, false, 0U);
                }
            }
            break;
        }
        case kClassRet: {
            const std::uint16_t source = static_cast<std::uint16_t>(code & 0x0018U);
            if (source == kRetA && !input.a) {
                report_uninitialized(result, pc, true, false, false, 0U);
            }
            break;
        }
        case kClassMisc: {
            if (code == static_cast<std::uint16_t>(kClassMisc | kMiscTax)) {
                if (!input.a) {
                    report_uninitialized(result, pc, true, false, false, 0U);
                }
                output.x = input.a;
            } else {
                if (!input.x) {
                    report_uninitialized(result, pc, false, true, false, 0U);
                }
                output.a = input.x;
            }
            break;
        }
        default:
            break;
    }
    return output;
}

}  // namespace detail

namespace detail {

using Literal = std::int32_t;
constexpr Literal kTrueLiteral = 1;
constexpr Literal kFalseLiteral = -1;

[[nodiscard]] constexpr Literal literal_not(Literal literal) noexcept {
    return -literal;
}

class CnfProblem {
  public:
    CnfProblem() {
        clauses_.push_back(std::vector<Literal>{kTrueLiteral});
    }

    [[nodiscard]] Literal fresh_variable() {
        if (next_variable_ == std::numeric_limits<Literal>::max()) {
            unsatisfiable_ = true;
            return 1;
        }
        return next_variable_++;
    }

    void add_clause(std::vector<Literal> clause) {
        std::vector<Literal> normalized;
        normalized.reserve(clause.size());
        for (Literal literal : clause) {
            if (literal == 0) {
                unsatisfiable_ = true;
                return;
            }
            bool duplicate = false;
            bool tautology = false;
            for (Literal existing : normalized) {
                if (existing == literal) {
                    duplicate = true;
                    break;
                }
                if (existing == -literal) {
                    tautology = true;
                    break;
                }
            }
            if (tautology) {
                return;
            }
            if (!duplicate) {
                normalized.push_back(literal);
            }
        }
        if (normalized.empty()) {
            unsatisfiable_ = true;
            return;
        }
        clauses_.push_back(std::move(normalized));
    }

    [[nodiscard]] bool unsatisfiable() const noexcept { return unsatisfiable_; }
    [[nodiscard]] std::size_t variable_count() const noexcept {
        return static_cast<std::size_t>(next_variable_ - 1);
    }
    [[nodiscard]] const std::vector<std::vector<Literal>>& clauses() const noexcept {
        return clauses_;
    }

  private:
    Literal next_variable_ = 2;
    bool unsatisfiable_ = false;
    std::vector<std::vector<Literal>> clauses_;
};

class SatSolver {
  public:
    SatSolver(const CnfProblem& problem, std::size_t variable_count)
        : problem_(problem),
          variable_count_(variable_count),
          assignments_(variable_count + 1U, -1),
          occurrences_(variable_count + 1U, 0U),
          clause_watches_(problem.clauses().size()),
          watches_(2U * (variable_count + 1U)) {
        for (const std::vector<Literal>& clause : problem_.clauses()) {
            for (Literal literal : clause) {
                const std::size_t variable = static_cast<std::size_t>(
                    literal < 0 ? -static_cast<std::int64_t>(literal)
                               : static_cast<std::int64_t>(literal));
                if (variable <= variable_count_) {
                    ++occurrences_[variable];
                }
            }
        }
        initialize_watches();
    }

    [[nodiscard]] bool solve(const std::vector<Literal>& assumptions) {
        if (problem_.unsatisfiable()) {
            return false;
        }

        if (!enqueue(kTrueLiteral)) {
            return false;
        }
        for (Literal literal : root_units_) {
            if (!enqueue(literal)) {
                return false;
            }
        }
        for (Literal literal : assumptions) {
            if (!enqueue(literal)) {
                return false;
            }
        }
        if (!propagate()) {
            return false;
        }
        return search();
    }

    [[nodiscard]] std::int8_t value(std::size_t variable) const {
        if (variable >= assignments_.size()) {
            return -1;
        }
        return assignments_[variable];
    }

    [[nodiscard]] std::int8_t value(Literal literal) const {
        if (literal == kTrueLiteral) {
            return 1;
        }
        if (literal == kFalseLiteral) {
            return 0;
        }
        const std::size_t variable = static_cast<std::size_t>(
            literal < 0 ? -static_cast<std::int64_t>(literal)
                       : static_cast<std::int64_t>(literal));
        const std::int8_t raw = value(variable);
        if (raw < 0) {
            return -1;
        }
        return literal > 0 ? raw : static_cast<std::int8_t>(raw == 0 ? 1 : 0);
    }

  private:
    void initialize_watches() {
        const std::vector<std::vector<Literal>>& clauses = problem_.clauses();
        for (std::size_t clause_index = 0U; clause_index < clauses.size(); ++clause_index) {
            const std::vector<Literal>& clause = clauses[clause_index];
            if (clause.empty()) {
                root_units_.push_back(kFalseLiteral);
            } else if (clause.size() == 1U) {
                root_units_.push_back(clause[0]);
            } else {
                clause_watches_[clause_index] = {0U, 1U};
                watch(clause_index, clause[0]);
                watch(clause_index, clause[1]);
            }
        }
    }

    [[nodiscard]] std::size_t watch_index(Literal literal) const noexcept {
        const std::size_t variable = static_cast<std::size_t>(
            literal < 0 ? -static_cast<std::int64_t>(literal)
                       : static_cast<std::int64_t>(literal));
        return variable * 2U + (literal < 0 ? 1U : 0U);
    }

    void watch(std::size_t clause_index, Literal literal) {
        watches_[watch_index(literal)].push_back(clause_index);
    }

    [[nodiscard]] bool enqueue(Literal literal) {
        if (literal == kTrueLiteral) {
            return true;
        }
        if (literal == kFalseLiteral) {
            return false;
        }
        const std::size_t variable = static_cast<std::size_t>(
            literal < 0 ? -static_cast<std::int64_t>(literal)
                       : static_cast<std::int64_t>(literal));
        const std::int8_t required = literal > 0 ? 1 : 0;
        if (assignments_[variable] >= 0) {
            return assignments_[variable] == required;
        }
        assignments_[variable] = required;
        trail_.push_back(literal);
        return true;
    }

    [[nodiscard]] bool propagate() {
        for (std::size_t queue_index = 0U; queue_index < trail_.size(); ++queue_index) {
            const Literal true_literal = trail_[queue_index];
            const Literal false_literal = literal_not(true_literal);
            std::vector<std::size_t>& watch_list = watches_[watch_index(false_literal)];
            std::size_t position = 0U;
            while (position < watch_list.size()) {
                const std::size_t clause_index = watch_list[position];
                const std::vector<Literal>& clause = problem_.clauses()[clause_index];
                auto& clause_watch = clause_watches_[clause_index];
                std::size_t moving_position = 2U;
                std::size_t other_position = 2U;
                if (clause[clause_watch[0]] == false_literal) {
                    moving_position = 0U;
                    other_position = 1U;
                } else if (clause[clause_watch[1]] == false_literal) {
                    moving_position = 1U;
                    other_position = 0U;
                }

                if (moving_position == 2U) {
                    watch_list[position] = watch_list.back();
                    watch_list.pop_back();
                    continue;
                }

                const Literal other_literal = clause[clause_watch[other_position]];
                const std::int8_t other_value = value(other_literal);
                if (other_value == 1) {
                    ++position;
                    continue;
                }

                bool replaced = false;
                for (std::size_t candidate = 0U; candidate < clause.size(); ++candidate) {
                    if (candidate == clause_watch[0] || candidate == clause_watch[1]) {
                        continue;
                    }
                    if (value(clause[candidate]) != 0) {
                        clause_watch[moving_position] = candidate;
                        watch_list[position] = watch_list.back();
                        watch_list.pop_back();
                        watch(clause_index, clause[candidate]);
                        replaced = true;
                        break;
                    }
                }
                if (replaced) {
                    continue;
                }

                if (other_value == 0) {
                    return false;
                }
                if (!enqueue(other_literal)) {
                    return false;
                }
                ++position;
            }
        }
        return true;
    }

    [[nodiscard]] Literal choose_variable() const {
        Literal best_variable = 0;
        std::size_t best_occurrences = 0U;
        for (std::size_t variable = 2U; variable <= variable_count_; ++variable) {
            if (assignments_[variable] >= 0) {
                continue;
            }
            if (occurrences_[variable] > best_occurrences) {
                best_occurrences = occurrences_[variable];
                best_variable = static_cast<Literal>(variable);
            }
        }
        return best_variable;
    }

    [[nodiscard]] bool search() {
        if (!propagate()) {
            return false;
        }
        const Literal next = choose_variable();
        if (next == 0) {
            return true;
        }
        const std::size_t saved_trail = trail_.size();
        if (enqueue(next) && search()) {
            return true;
        }
        backtrack(saved_trail);
        if (enqueue(-next) && search()) {
            return true;
        }
        backtrack(saved_trail);
        return false;
    }

    void backtrack(std::size_t saved_trail) {
        while (trail_.size() > saved_trail) {
            const Literal literal = trail_.back();
            trail_.pop_back();
            const std::size_t variable = static_cast<std::size_t>(
                literal < 0 ? -static_cast<std::int64_t>(literal)
                           : static_cast<std::int64_t>(literal));
            assignments_[variable] = -1;
        }
    }

    const CnfProblem& problem_;
    std::size_t variable_count_;
    std::vector<std::int8_t> assignments_;
    std::vector<std::size_t> occurrences_;
    std::vector<std::array<std::size_t, 2U>> clause_watches_;
    std::vector<std::vector<std::size_t>> watches_;
    std::vector<Literal> root_units_;
    std::vector<Literal> trail_;
};

class SymbolicBuilder {
  private:
    struct BitVector {
        bool constant = false;
        std::uint32_t value = 0U;
        std::vector<Literal> bits;
    };

    enum class BitwiseOp { and_op, or_op, xor_op };

  public:
    enum class BinaryOp {
        add,
        subtract,
        multiply,
        divide,
        modulo,
        bit_and,
        bit_or,
        bit_xor,
    };

    explicit SymbolicBuilder(CnfProblem& problem) : problem_(problem) {
        for (std::size_t word = 0U; word < input_words_.size(); ++word) {
            input_words_[word] = make_input();
        }
    }

    [[nodiscard]] std::uint32_t input_word(std::size_t index) const {
        return input_words_.at(index);
    }

    [[nodiscard]] std::uint32_t constant(std::uint32_t value) {
        BitVector vector;
        vector.constant = true;
        vector.value = value;
        vector.bits.resize(32U);
        for (std::size_t bit = 0U; bit < 32U; ++bit) {
            vector.bits[bit] =
                ((value >> static_cast<std::uint32_t>(bit)) & 1U) != 0U
                    ? kTrueLiteral
                    : kFalseLiteral;
        }
        expressions_.push_back(std::move(vector));
        return static_cast<std::uint32_t>(expressions_.size() - 1U);
    }

    [[nodiscard]] const BitVector& expression(std::uint32_t id) const {
        return expressions_.at(id);
    }

    [[nodiscard]] std::uint32_t binary(BinaryOp operation, std::uint32_t left,
                                       std::uint32_t right) {
        const BitVector& lhs = expression(left);
        const BitVector& rhs = expression(right);
        if (lhs.constant && rhs.constant) {
            return constant(evaluate_constant(operation, lhs.value, rhs.value));
        }
        switch (operation) {
            case BinaryOp::add:
                if (lhs.constant && lhs.value == 0U) {
                    return right;
                }
                if (rhs.constant && rhs.value == 0U) {
                    return left;
                }
                return append(add_vectors(lhs.bits, rhs.bits, kFalseLiteral));
            case BinaryOp::subtract:
                if (rhs.constant && rhs.value == 0U) {
                    return left;
                }
                return binary(BinaryOp::add, left,
                              append(subtract_vectors(lhs.bits, rhs.bits)));
            case BinaryOp::multiply:
                if ((lhs.constant && lhs.value == 0U) ||
                    (rhs.constant && rhs.value == 0U)) {
                    return constant(0U);
                }
                if (lhs.constant && lhs.value == 1U) {
                    return right;
                }
                if (rhs.constant && rhs.value == 1U) {
                    return left;
                }
                return append(multiply_vectors(lhs.bits, rhs.bits));
            case BinaryOp::divide:
                return divide_or_modulo(left, right, true);
            case BinaryOp::modulo:
                return divide_or_modulo(left, right, false);
            case BinaryOp::bit_and:
                return append(bitwise(lhs.bits, rhs.bits, BitwiseOp::and_op));
            case BinaryOp::bit_or:
                return append(bitwise(lhs.bits, rhs.bits, BitwiseOp::or_op));
            case BinaryOp::bit_xor:
                return append(bitwise(lhs.bits, rhs.bits, BitwiseOp::xor_op));
        }
        return constant(0U);
    }

    [[nodiscard]] std::uint32_t shift(std::uint32_t value, std::uint32_t amount,
                                       bool left) {
        const BitVector& lhs = expression(value);
        const BitVector& rhs = expression(amount);
        if (rhs.constant) {
            if (rhs.value >= 32U) {
                return constant(0U);
            }
            return append(shift_constant(lhs.bits, rhs.value, left));
        }
        return append(shift_variable(lhs.bits, rhs.bits, left));
    }

    [[nodiscard]] std::uint32_t negate(std::uint32_t value) {
        const BitVector& vector = expression(value);
        if (vector.constant) {
            return constant(static_cast<std::uint32_t>(0U - vector.value));
        }
        return append(subtract_vectors(zero_bits(), vector.bits));
    }

    [[nodiscard]] Literal equal_const(std::uint32_t value, std::uint32_t expected) {
        const BitVector& vector = expression(value);
        if (vector.constant) {
            return vector.value == expected ? kTrueLiteral : kFalseLiteral;
        }
        Literal result = kTrueLiteral;
        for (std::size_t bit = 0U; bit < 32U; ++bit) {
            const Literal expression_bit = vector.bits[bit];
            const bool expected_bit =
                ((expected >> static_cast<std::uint32_t>(bit)) & 1U) != 0U;
            result = gate_and(result, expected_bit ? expression_bit
                                                   : literal_not(expression_bit));
        }
        return result;
    }

    [[nodiscard]] Literal equal(std::uint32_t left, std::uint32_t right) {
        const BitVector& lhs = expression(left);
        const BitVector& rhs = expression(right);
        if (lhs.constant && rhs.constant) {
            return lhs.value == rhs.value ? kTrueLiteral : kFalseLiteral;
        }
        Literal result = kTrueLiteral;
        for (std::size_t bit = 0U; bit < 32U; ++bit) {
            result = gate_and(result, literal_not(gate_xor(lhs.bits[bit], rhs.bits[bit])));
        }
        return result;
    }

    [[nodiscard]] Literal greater(std::uint32_t left, std::uint32_t right) {
        return greater_than(expression(left).bits, expression(right).bits);
    }

    [[nodiscard]] Literal greater_equal(std::uint32_t left, std::uint32_t right) {
        return greater_equal_than(expression(left).bits, expression(right).bits);
    }

    [[nodiscard]] Literal bit_set_or(std::uint32_t left, std::uint32_t right) {
        const std::size_t width = std::min(expression(left).bits.size(),
                                           expression(right).bits.size());
        Literal result = kFalseLiteral;
        for (std::size_t bit = 0U; bit < width; ++bit) {
            result = gate_or(result,
                             gate_and(expression(left).bits[bit],
                                      expression(right).bits[bit]));
        }
        return result;
    }

    [[nodiscard]] Literal any_high_bit(std::uint32_t value) {
        const BitVector& vector = expression(value);
        if (vector.constant) {
            return vector.value != 0U ? kTrueLiteral : kFalseLiteral;
        }
        Literal result = kFalseLiteral;
        for (Literal bit : vector.bits) {
            result = gate_or(result, bit);
        }
        return result;
    }

    [[nodiscard]] Literal required_bit(Literal bit, bool expected) const {
        return expected ? bit : literal_not(bit);
    }

    [[nodiscard]] Literal input_bit(std::size_t word, std::size_t bit) const {
        return expression(input_words_.at(word)).bits.at(bit);
    }

    [[nodiscard]] Literal masked_equal(std::uint32_t low_word, std::uint32_t high_word,
                                        std::uint64_t mask, std::uint64_t value) {
        if ((value & ~mask) != 0U) {
            return kFalseLiteral;
        }
        Literal result = kTrueLiteral;
        for (std::size_t bit = 0U; bit < 64U; ++bit) {
            const std::uint64_t bit_mask = std::uint64_t{1U} << bit;
            if ((mask & bit_mask) == 0U) {
                continue;
            }
            const bool expected = (value & bit_mask) != 0U;
            const Literal expression_bit =
                bit < 32U
                    ? expression(low_word).bits[bit]
                    : expression(high_word).bits[bit - 32U];
            result = gate_and(result, required_bit(expression_bit, expected));
        }
        return result;
    }

    [[nodiscard]] std::uint32_t evaluate(std::uint32_t id,
                                          const std::vector<std::int8_t>& model) const {
        const BitVector& vector = expression(id);
        if (vector.constant) {
            return vector.value;
        }
        std::uint32_t result = 0U;
        for (std::size_t bit = 0U; bit < 32U; ++bit) {
            if (literal_value(vector.bits[bit], model)) {
                result |= static_cast<std::uint32_t>(1U << static_cast<std::uint32_t>(bit));
            }
        }
        return result;
    }

  private:
    [[nodiscard]] std::uint32_t make_input() {
        BitVector vector;
        vector.bits.resize(32U);
        for (std::size_t bit = 0U; bit < 32U; ++bit) {
            vector.bits[bit] = problem_.fresh_variable();
        }
        expressions_.push_back(std::move(vector));
        return static_cast<std::uint32_t>(expressions_.size() - 1U);
    }

    [[nodiscard]] std::uint32_t append(std::vector<Literal> bits) {
        BitVector vector;
        vector.constant = false;
        vector.bits = std::move(bits);
        expressions_.push_back(std::move(vector));
        return static_cast<std::uint32_t>(expressions_.size() - 1U);
    }

    [[nodiscard]] static std::uint32_t evaluate_constant(BinaryOp operation,
                                                          std::uint32_t left,
                                                          std::uint32_t right) {
        switch (operation) {
            case BinaryOp::add:
                return static_cast<std::uint32_t>(left + right);
            case BinaryOp::subtract:
                return static_cast<std::uint32_t>(left - right);
            case BinaryOp::multiply:
                return static_cast<std::uint32_t>(left * right);
            case BinaryOp::divide:
                return right == 0U ? 0U : left / right;
            case BinaryOp::modulo:
                return right == 0U ? 0U : left % right;
            case BinaryOp::bit_and:
                return left & right;
            case BinaryOp::bit_or:
                return left | right;
            case BinaryOp::bit_xor:
                return left ^ right;
        }
        return 0U;
    }

    [[nodiscard]] std::vector<Literal> zero_bits() const {
        return std::vector<Literal>(32U, kFalseLiteral);
    }

    [[nodiscard]] Literal gate_and(Literal left, Literal right) {
        if (left == kFalseLiteral || right == kFalseLiteral) {
            return kFalseLiteral;
        }
        if (left == kTrueLiteral) {
            return right;
        }
        if (right == kTrueLiteral) {
            return left;
        }
        if (left == right) {
            return left;
        }
        if (left == -right) {
            return kFalseLiteral;
        }
        const Literal output = problem_.fresh_variable();
        problem_.add_clause({-output, left});
        problem_.add_clause({-output, right});
        problem_.add_clause({output, -left, -right});
        return output;
    }

    [[nodiscard]] Literal gate_or(Literal left, Literal right) {
        if (left == kTrueLiteral || right == kTrueLiteral) {
            return kTrueLiteral;
        }
        if (left == kFalseLiteral) {
            return right;
        }
        if (right == kFalseLiteral) {
            return left;
        }
        if (left == right) {
            return left;
        }
        if (left == -right) {
            return kTrueLiteral;
        }
        return literal_not(gate_and(literal_not(left), literal_not(right)));
    }

    [[nodiscard]] Literal gate_xor(Literal left, Literal right) {
        if (left == kFalseLiteral) {
            return right;
        }
        if (right == kFalseLiteral) {
            return left;
        }
        if (left == kTrueLiteral) {
            return literal_not(right);
        }
        if (right == kTrueLiteral) {
            return literal_not(left);
        }
        if (left == right) {
            return kFalseLiteral;
        }
        if (left == -right) {
            return kTrueLiteral;
        }
        const Literal output = problem_.fresh_variable();
        problem_.add_clause({left, right, -output});
        problem_.add_clause({-left, -right, -output});
        problem_.add_clause({left, -right, output});
        problem_.add_clause({-left, right, output});
        return output;
    }

    [[nodiscard]] Literal gate_mux(Literal condition, Literal when_true,
                                    Literal when_false) {
        if (when_true == when_false) {
            return when_true;
        }
        if (condition == kTrueLiteral) {
            return when_true;
        }
        if (condition == kFalseLiteral) {
            return when_false;
        }
        if (when_true == kTrueLiteral && when_false == kFalseLiteral) {
            return condition;
        }
        if (when_true == kFalseLiteral && when_false == kTrueLiteral) {
            return literal_not(condition);
        }
        const Literal true_output = gate_and(condition, when_true);
        const Literal false_output = gate_and(literal_not(condition), when_false);
        return gate_or(true_output, false_output);
    }

    [[nodiscard]] std::vector<Literal> add_vectors(const std::vector<Literal>& left,
                                                    const std::vector<Literal>& right,
                                                    Literal carry_in) {
        const std::size_t width = std::min(left.size(), right.size());
        std::vector<Literal> output(width, kFalseLiteral);
        Literal carry = carry_in;
        for (std::size_t bit = 0U; bit < width; ++bit) {
            const Literal xor_ab = gate_xor(left[bit], right[bit]);
            output[bit] = gate_xor(xor_ab, carry);
            const Literal first = gate_and(left[bit], right[bit]);
            const Literal second = gate_and(carry, xor_ab);
            carry = gate_or(first, second);
        }
        return output;
    }

    [[nodiscard]] std::vector<Literal> subtract_vectors(const std::vector<Literal>& left,
                                                         const std::vector<Literal>& right) {
        std::vector<Literal> inverted(right.size(), kFalseLiteral);
        for (std::size_t bit = 0U; bit < right.size(); ++bit) {
            inverted[bit] = literal_not(right[bit]);
        }
        return add_vectors(left, inverted, kTrueLiteral);
    }

    [[nodiscard]] std::vector<Literal> bitwise(const std::vector<Literal>& left,
                                                const std::vector<Literal>& right,
                                                BitwiseOp operation) {
        const std::size_t width = std::min(left.size(), right.size());
        std::vector<Literal> output(width, kFalseLiteral);
        for (std::size_t bit = 0U; bit < width; ++bit) {
            switch (operation) {
                case BitwiseOp::and_op:
                    output[bit] = gate_and(left[bit], right[bit]);
                    break;
                case BitwiseOp::or_op:
                    output[bit] = gate_or(left[bit], right[bit]);
                    break;
                case BitwiseOp::xor_op:
                    output[bit] = gate_xor(left[bit], right[bit]);
                    break;
            }
        }
        return output;
    }

    [[nodiscard]] std::vector<Literal> multiply_vectors(
        const std::vector<Literal>& left, const std::vector<Literal>& right) {
        std::vector<Literal> result(32U, kFalseLiteral);
        for (std::size_t bit = 0U; bit < 32U; ++bit) {
            std::vector<Literal> partial(32U, kFalseLiteral);
            for (std::size_t source_bit = 0U; source_bit < 32U; ++source_bit) {
                const std::size_t target_bit = bit + source_bit;
                if (target_bit >= 32U) {
                    break;
                }
                partial[target_bit] = gate_and(left[source_bit], right[bit]);
            }
            result = add_vectors(result, partial, kFalseLiteral);
        }
        return result;
    }

    [[nodiscard]] Literal equal_bits(const std::vector<Literal>& left,
                                     const std::vector<Literal>& right) {
        const std::size_t width = std::min(left.size(), right.size());
        Literal result = kTrueLiteral;
        for (std::size_t bit = 0U; bit < width; ++bit) {
            result = gate_and(result,
                              literal_not(gate_xor(left[bit], right[bit])));
        }
        return result;
    }

    [[nodiscard]] Literal greater_than(const std::vector<Literal>& left,
                                       const std::vector<Literal>& right) {
        const std::size_t width = std::min(left.size(), right.size());
        Literal greater = kFalseLiteral;
        Literal equal_prefix = kTrueLiteral;
        for (std::size_t index = width; index-- > 0U;) {
            greater = gate_or(
                greater,
                gate_and(equal_prefix,
                         gate_and(left[index], literal_not(right[index]))));
            equal_prefix = gate_and(
                equal_prefix,
                literal_not(gate_xor(left[index], right[index])));
        }
        return greater;
    }

    [[nodiscard]] Literal greater_equal_than(const std::vector<Literal>& left,
                                             const std::vector<Literal>& right) {
        const std::size_t width = std::min(left.size(), right.size());
        Literal greater = kFalseLiteral;
        Literal equal_prefix = kTrueLiteral;
        for (std::size_t index = width; index-- > 0U;) {
            greater = gate_or(
                greater,
                gate_and(equal_prefix,
                         gate_and(left[index], literal_not(right[index]))));
            equal_prefix = gate_and(
                equal_prefix,
                literal_not(gate_xor(left[index], right[index])));
        }
        return gate_or(greater, equal_prefix);
    }

    [[nodiscard]] std::vector<Literal> shift_constant(const std::vector<Literal>& value,
                                                       std::uint32_t amount,
                                                       bool left) {
        std::vector<Literal> output(32U, kFalseLiteral);
        for (std::size_t bit = 0U; bit < 32U; ++bit) {
            const std::size_t source =
                left ? bit + static_cast<std::size_t>(amount)
                     : bit - static_cast<std::size_t>(amount);
            if ((left && source < 32U) || (!left && bit >= amount)) {
                output[bit] = value[source];
            }
        }
        return output;
    }

    [[nodiscard]] std::vector<Literal> shift_variable(const std::vector<Literal>& value,
                                                       const std::vector<Literal>& amount,
                                                       bool left) {
        std::vector<Literal> shifted = value;
        for (std::size_t stage = 0U; stage < 5U; ++stage) {
            const std::size_t distance = static_cast<std::size_t>(1U << stage);
            std::vector<Literal> next(32U, kFalseLiteral);
            for (std::size_t bit = 0U; bit < 32U; ++bit) {
                Literal source = kFalseLiteral;
                if (left) {
                    if (bit + distance < 32U) {
                        source = shifted[bit + distance];
                    }
                } else if (bit >= distance) {
                    source = shifted[bit - distance];
                }
                next[bit] = gate_mux(amount[stage], source, shifted[bit]);
            }
            shifted = std::move(next);
        }

        Literal high_amount = kFalseLiteral;
        for (std::size_t bit = 5U; bit < amount.size(); ++bit) {
            high_amount = gate_or(high_amount, amount[bit]);
        }
        std::vector<Literal> output(32U, kFalseLiteral);
        for (std::size_t bit = 0U; bit < 32U; ++bit) {
            output[bit] = gate_mux(literal_not(high_amount), shifted[bit], kFalseLiteral);
        }
        return output;
    }

    [[nodiscard]] std::uint32_t divide_or_modulo(std::uint32_t left, std::uint32_t right,
                                                  bool quotient) {
        const BitVector& dividend = expression(left);
        const BitVector& divisor = expression(right);
        if (divisor.constant) {
            if (divisor.value == 0U) {
                return constant(0U);
            }
            if (divisor.value == 1U) {
                return quotient ? left : constant(0U);
            }
        }

        constexpr std::size_t kDivisionWidth = 33U;
        std::vector<Literal> remainder(kDivisionWidth, kFalseLiteral);
        std::vector<Literal> quotient_bits(32U, kFalseLiteral);
        std::vector<Literal> divisor_extended(kDivisionWidth, kFalseLiteral);
        for (std::size_t bit = 0U; bit < 32U; ++bit) {
            divisor_extended[bit] = divisor.bits[bit];
        }

        for (std::size_t index = 32U; index-- > 0U;) {
            std::vector<Literal> shifted_remainder(kDivisionWidth, kFalseLiteral);
            for (std::size_t bit = kDivisionWidth; bit-- > 1U;) {
                shifted_remainder[bit] = remainder[bit - 1U];
            }
            shifted_remainder[0] = dividend.bits[index];
            const Literal can_subtract =
                greater_equal_than(shifted_remainder, divisor_extended);
            const std::vector<Literal> difference =
                subtract_vectors(shifted_remainder, divisor_extended);
            for (std::size_t bit = 0U; bit < kDivisionWidth; ++bit) {
                remainder[bit] = gate_mux(can_subtract, difference[bit],
                                          shifted_remainder[bit]);
            }
            quotient_bits[index] = can_subtract;
        }

        const Literal divisor_nonzero = literal_not(equal_bits(divisor.bits, zero_bits()));
        std::vector<Literal> result(32U, kFalseLiteral);
        const std::vector<Literal> raw =
            quotient ? quotient_bits
                     : std::vector<Literal>(remainder.begin(), remainder.begin() + 32);
        for (std::size_t bit = 0U; bit < 32U; ++bit) {
            result[bit] = gate_mux(divisor_nonzero, raw[bit], kFalseLiteral);
        }
        return append(std::move(result));
    }

    [[nodiscard]] static bool literal_value(Literal literal,
                                             const std::vector<std::int8_t>& model) {
        if (literal == kTrueLiteral) {
            return true;
        }
        if (literal == kFalseLiteral) {
            return false;
        }
        const std::size_t variable = static_cast<std::size_t>(
            literal < 0 ? -static_cast<std::int64_t>(literal)
                       : static_cast<std::int64_t>(literal));
        if (variable >= model.size()) {
            return false;
        }
        return model[variable] > 0;
    }

    CnfProblem& problem_;
    std::array<std::uint32_t, 16U> input_words_{};
    std::vector<BitVector> expressions_;
};

struct SymbolicState {
    std::size_t pc = 0U;
    std::optional<std::uint32_t> a;
    std::optional<std::uint32_t> x;
    std::array<std::optional<std::uint32_t>, kScratchWords> memory{};
    std::vector<Literal> assumptions;
};

class ProgramAnalyzer {
  public:
    ProgramAnalyzer(const Program& program, const Policy& policy, AnalysisResult& result)
        : program_(program),
          policy_(policy),
          result_(result),
          builder_(problem_),
          rule_seen_(policy.denied_syscalls.size(), false),
          argument_seen_(policy.denied_syscalls.size()),
          upper_seen_(policy.denied_syscalls.size()) {
        for (std::size_t rule = 0U; rule < policy_.denied_syscalls.size(); ++rule) {
            argument_seen_[rule] = false;
            upper_seen_[rule].assign(policy_.denied_syscalls[rule].arguments.size(), false);
        }
    }

    void run() {
        SymbolicState initial;
        initial.pc = 0U;
        std::vector<SymbolicState> pending;
        pending.push_back(std::move(initial));

        while (!pending.empty() && !all_requested_findings_found()) {
            SymbolicState state = std::move(pending.back());
            pending.pop_back();
            step(std::move(state), pending);
        }
    }

  private:
    [[nodiscard]] Literal condition_for_jump(const SymbolicState& state,
                                              const DecodedInstruction& decoded,
                                              const SockFilter& instruction) {
        if (!state.a.has_value()) {
            return kFalseLiteral;
        }
        const std::uint32_t right =
            decoded.jump_uses_x
                ? (state.x.has_value() ? *state.x : builder_.constant(0U))
                : builder_.constant(instruction.k);
        switch (decoded.jump_op) {
            case kJmpJeq:
                return builder_.equal(*state.a, right);
            case kJmpJgt:
                return builder_.greater(*state.a, right);
            case kJmpJge:
                return builder_.greater_equal(*state.a, right);
            case kJmpJset:
                return builder_.bit_set_or(*state.a, right);
            default:
                return kFalseLiteral;
        }
    }

    [[nodiscard]] Literal argument_condition(const ArgumentRule& rule) {
        if (rule.argument >= 6U) {
            return kFalseLiteral;
        }
        const std::size_t argument = static_cast<std::size_t>(rule.argument);
        return builder_.masked_equal(
            builder_.input_word(4U + argument * 2U),
            builder_.input_word(5U + argument * 2U), rule.forbidden_mask,
            rule.forbidden_value);
    }

    [[nodiscard]] Literal arch_is_target() {
        return builder_.equal_const(builder_.input_word(1U), policy_.target_arch);
    }

    [[nodiscard]] Literal nr_is(std::uint32_t number) {
        return builder_.equal_const(builder_.input_word(0U), number);
    }

    [[nodiscard]] Witness witness_from(const std::vector<std::int8_t>& model) const {
        Witness witness;
        witness.nr = builder_.evaluate(builder_.input_word(0U), model);
        witness.arch = builder_.evaluate(builder_.input_word(1U), model);
        const std::uint32_t ip_low =
            builder_.evaluate(builder_.input_word(2U), model);
        const std::uint32_t ip_high =
            builder_.evaluate(builder_.input_word(3U), model);
        witness.instruction_pointer =
            static_cast<std::uint64_t>(ip_low) |
            (static_cast<std::uint64_t>(ip_high) << 32U);
        for (std::size_t argument = 0U; argument < 6U; ++argument) {
            const std::uint32_t low =
                builder_.evaluate(builder_.input_word(4U + argument * 2U), model);
            const std::uint32_t high =
                builder_.evaluate(builder_.input_word(5U + argument * 2U), model);
            witness.args[argument] =
                static_cast<std::uint64_t>(low) |
                (static_cast<std::uint64_t>(high) << 32U);
        }
        return witness;
    }

    [[nodiscard]] bool query(const std::vector<Literal>& base,
                             const std::vector<Literal>& property,
                             std::vector<std::int8_t>& model) const {
        const std::size_t variable_count = problem_.variable_count();
        std::vector<Literal> assumptions = base;
        assumptions.insert(assumptions.end(), property.begin(), property.end());
        SatSolver solver(problem_, variable_count);
        if (!solver.solve(assumptions)) {
            return false;
        }
        model.assign(variable_count + 1U, -1);
        for (std::size_t variable = 1U; variable <= variable_count; ++variable) {
            model[variable] = solver.value(variable);
        }
        return true;
    }

    void add_finding(ViolationKind kind, Witness witness, std::size_t return_pc,
                     std::optional<std::size_t> rule_index,
                     std::optional<std::uint8_t> argument_index,
                     std::string detail) {
        Finding finding;
        finding.kind = kind;
        finding.witness = witness;
        finding.return_pc = return_pc;
        finding.rule_index = rule_index;
        finding.argument_index = argument_index;
        finding.detail = std::move(detail);
        result_.findings.push_back(std::move(finding));
    }

    [[nodiscard]] std::string witness_detail(const Witness& witness,
                                              std::size_t return_pc) const {
        std::ostringstream stream;
        stream << "witness arch=0x" << std::hex << witness.arch << " nr=0x"
               << witness.nr << std::dec << " returns ALLOW at pc " << return_pc;
        return stream.str();
    }

    void try_foreign_arch(const std::vector<Literal>& base, std::size_t return_pc) {
        if (found_foreign_ || !policy_.reject_foreign_arch) {
            return;
        }
        std::vector<std::int8_t> model;
        if (policy_.target_arch != kAuditArchI386 &&
            query(base, {builder_.equal_const(builder_.input_word(1U), kAuditArchI386)},
                  model)) {
            add_finding(ViolationKind::foreign_architecture, witness_from(model), return_pc,
                        std::nullopt, std::nullopt,
                        "I386-compatible input reaches allow: " +
                            witness_detail(witness_from(model), return_pc));
            found_foreign_ = true;
            return;
        }
        const Literal not_target = literal_not(arch_is_target());
        if (query(base, {not_target}, model)) {
            const Witness witness = witness_from(model);
            add_finding(ViolationKind::foreign_architecture, witness, return_pc,
                        std::nullopt, std::nullopt,
                        "foreign-architecture input reaches allow: " +
                            witness_detail(witness, return_pc));
            found_foreign_ = true;
        }
    }

    void try_x32(const std::vector<Literal>& base, std::size_t return_pc) {
        if (found_x32_ || !policy_.reject_x32) {
            return;
        }
        std::vector<std::int8_t> model;
        for (const DeniedSyscallRule& rule : policy_.denied_syscalls) {
            const std::uint32_t marked_number = rule.number | kX32SyscallBit;
            if (query(base, {arch_is_target(), nr_is(marked_number)}, model)) {
                const Witness witness = witness_from(model);
                add_finding(ViolationKind::x32_abi, witness, return_pc, std::nullopt,
                            std::nullopt,
                            "x32-marked denied syscall reaches allow: " +
                                witness_detail(witness, return_pc));
                found_x32_ = true;
                return;
            }
        }

        const Literal x32_flag = builder_.input_bit(0U, 30U);
        if (query(base, {arch_is_target(), x32_flag}, model)) {
            const Witness witness = witness_from(model);
            add_finding(ViolationKind::x32_abi, witness, return_pc, std::nullopt,
                        std::nullopt,
                        "x32-marked syscall reaches allow: " +
                            witness_detail(witness, return_pc));
            found_x32_ = true;
        }
    }

    void try_denied_rule(std::size_t rule_index, const std::vector<Literal>& base,
                         std::size_t return_pc) {
        const DeniedSyscallRule& rule = policy_.denied_syscalls[rule_index];
        if (rule.deny_number && !rule_seen_[rule_index]) {
            std::vector<std::int8_t> model;
            if (query(base, {arch_is_target(), nr_is(rule.number)}, model)) {
                const Witness witness = witness_from(model);
                add_finding(ViolationKind::denied_syscall, witness, return_pc, rule_index,
                            std::nullopt,
                            "denied syscall reaches allow: " +
                                witness_detail(witness, return_pc));
                rule_seen_[rule_index] = true;
            }
        }

        if (!rule.arguments.empty() && !argument_seen_[rule_index]) {
            std::vector<Literal> property{arch_is_target(), nr_is(rule.number)};
            for (const ArgumentRule& argument : rule.arguments) {
                property.push_back(argument_condition(argument));
            }
            std::vector<std::int8_t> model;
            if (query(base, property, model)) {
                const Witness witness = witness_from(model);
                add_finding(ViolationKind::denied_argument, witness, return_pc, rule_index,
                            std::nullopt,
                            "forbidden 64-bit argument reaches allow: " +
                                witness_detail(witness, return_pc));
                argument_seen_[rule_index] = true;
            }
        }

        for (std::size_t argument_index = 0U; argument_index < rule.arguments.size();
             ++argument_index) {
            const ArgumentRule& argument = rule.arguments[argument_index];
            if (!argument.require_upper_half_check || argument.argument >= 6U ||
                upper_seen_[rule_index][argument_index]) {
                continue;
            }
            std::vector<Literal> property{arch_is_target(), nr_is(rule.number)};
            for (const ArgumentRule& required_argument : rule.arguments) {
                property.push_back(argument_condition(required_argument));
            }
            property.push_back(builder_.any_high_bit(builder_.input_word(
                5U + static_cast<std::size_t>(argument.argument) * 2U)));
            std::vector<std::int8_t> model;
            if (query(base, property, model)) {
                const Witness witness = witness_from(model);
                add_finding(ViolationKind::upper_half_ignored, witness, return_pc,
                            rule_index, argument.argument,
                            "argument " + std::to_string(argument.argument) +
                                " has a non-zero upper half on an allowed path: " +
                                witness_detail(witness, return_pc));
                upper_seen_[rule_index][argument_index] = true;
            }
        }
    }

    void examine_return(const SymbolicState& state) {
        const SockFilter& instruction = program_[state.pc];
        const std::uint16_t code = instruction.code;
        const std::uint16_t source = static_cast<std::uint16_t>(code & 0x0018U);
        std::vector<Literal> base = state.assumptions;

        if (source == kRetA) {
            if (!state.a.has_value()) {
                return;
            }
            const Literal is_allow =
                builder_.equal_const(*state.a, kSeccompRetAllow);
            if (is_allow == kFalseLiteral) {
                return;
            }
            if (is_allow != kTrueLiteral) {
                base.push_back(is_allow);
            }
        } else if (instruction.k != kSeccompRetAllow) {
            return;
        }

        const std::size_t return_pc = state.pc;
        try_foreign_arch(base, return_pc);
        try_x32(base, return_pc);
        for (std::size_t rule = 0U; rule < policy_.denied_syscalls.size(); ++rule) {
            try_denied_rule(rule, base, return_pc);
        }
    }

    [[nodiscard]] bool all_requested_findings_found() const {
        if (policy_.reject_foreign_arch && !found_foreign_) {
            return false;
        }
        if (policy_.reject_x32 && !found_x32_) {
            return false;
        }
        for (std::size_t rule_index = 0U; rule_index < policy_.denied_syscalls.size();
             ++rule_index) {
            const DeniedSyscallRule& rule = policy_.denied_syscalls[rule_index];
            if (rule.deny_number && !rule_seen_[rule_index]) {
                return false;
            }
            if (!rule.arguments.empty() && !argument_seen_[rule_index]) {
                return false;
            }
            for (std::size_t argument_index = 0U; argument_index < rule.arguments.size();
                 ++argument_index) {
                const ArgumentRule& argument = rule.arguments[argument_index];
                if (argument.require_upper_half_check && argument.argument < 6U &&
                    !upper_seen_[rule_index][argument_index]) {
                    return false;
                }
            }
        }
        return true;
    }

    [[nodiscard]] std::uint32_t binary_result(
        SymbolicBuilder::BinaryOp operation, std::uint32_t left, std::uint32_t right) {
        return builder_.binary(operation, left, right);
    }

    void execute_register_instruction(SymbolicState& state,
                                      const DecodedInstruction& decoded) {
        const SockFilter& instruction = program_[state.pc];
        const std::uint16_t code = instruction.code;
        switch (instruction_class(code)) {
            case kClassLd: {
                const std::uint16_t mode = instruction_mode(code);
                if (mode == kModeImm) {
                    state.a = builder_.constant(instruction.k);
                } else if (mode == kModeAbs) {
                    state.a = builder_.input_word(
                        static_cast<std::size_t>(instruction.k / 4U));
                } else if (mode == kModeMem) {
                    state.a = state.memory[static_cast<std::size_t>(instruction.k)];
                }
                break;
            }
            case kClassLdx: {
                const std::uint16_t mode = instruction_mode(code);
                if (mode == kModeImm) {
                    state.x = builder_.constant(instruction.k);
                } else if (mode == kModeMem) {
                    state.x = state.memory[static_cast<std::size_t>(instruction.k)];
                }
                break;
            }
            case kClassSt:
                state.memory[static_cast<std::size_t>(instruction.k)] = state.a;
                break;
            case kClassStx:
                state.memory[static_cast<std::size_t>(instruction.k)] = state.x;
                break;
            case kClassAlu: {
                if (!state.a.has_value()) {
                    break;
                }
                SymbolicBuilder::BinaryOp operation = SymbolicBuilder::BinaryOp::add;
                bool is_shift = false;
                bool shift_left = false;
                switch (decoded.alu_op) {
                    case kAluAdd:
                        operation = SymbolicBuilder::BinaryOp::add;
                        break;
                    case kAluSub:
                        operation = SymbolicBuilder::BinaryOp::subtract;
                        break;
                    case kAluMul:
                        operation = SymbolicBuilder::BinaryOp::multiply;
                        break;
                    case kAluDiv:
                        operation = SymbolicBuilder::BinaryOp::divide;
                        break;
                    case kAluOr:
                        operation = SymbolicBuilder::BinaryOp::bit_or;
                        break;
                    case kAluAnd:
                        operation = SymbolicBuilder::BinaryOp::bit_and;
                        break;
                    case kAluXor:
                        operation = SymbolicBuilder::BinaryOp::bit_xor;
                        break;
                    case kAluMod:
                        operation = SymbolicBuilder::BinaryOp::modulo;
                        break;
                    case kAluLsh:
                        is_shift = true;
                        shift_left = true;
                        break;
                    case kAluRsh:
                        is_shift = true;
                        shift_left = false;
                        break;
                    case kAluNeg:
                        state.a = builder_.negate(*state.a);
                        break;
                    default:
                        break;
                }
                if (decoded.alu_op == kAluNeg) {
                    break;
                }
                const std::uint32_t right =
                    decoded.alu_uses_x
                        ? (state.x.has_value() ? *state.x : builder_.constant(0U))
                        : builder_.constant(instruction.k);
                if (is_shift) {
                    state.a = builder_.shift(*state.a, right, shift_left);
                } else {
                    state.a = binary_result(operation, *state.a, right);
                }
                break;
            }
            case kClassMisc:
                if (code == static_cast<std::uint16_t>(kClassMisc | kMiscTax)) {
                    state.x = state.a;
                } else {
                    state.a = state.x;
                }
                break;
            default:
                break;
        }
    }

    void step(SymbolicState state, std::vector<SymbolicState>& pending) {
        if (state.pc >= program_.size()) {
            return;
        }
        const DecodedInstruction decoded =
            decode_instruction(program_[state.pc], result_.validation, state.pc);
        if (!decoded.recognized) {
            return;
        }
        if (decoded.terminal) {
            examine_return(state);
            return;
        }

        if (decoded.conditional_jump) {
            const Literal condition = condition_for_jump(state, decoded, program_[state.pc]);
            const std::uint64_t target_true =
                static_cast<std::uint64_t>(state.pc) + 1U +
                static_cast<std::uint64_t>(program_[state.pc].jt);
            const std::uint64_t target_false =
                static_cast<std::uint64_t>(state.pc) + 1U +
                static_cast<std::uint64_t>(program_[state.pc].jf);
            if (target_true == target_false) {
                state.pc = static_cast<std::size_t>(target_true);
                pending.push_back(std::move(state));
                return;
            }

            auto add_branch = [&](std::uint64_t target, Literal assumption) {
                SymbolicState branch = state;
                branch.pc = static_cast<std::size_t>(target);
                std::vector<Literal> branch_assumptions = branch.assumptions;
                if (assumption != kTrueLiteral) {
                    branch_assumptions.push_back(assumption);
                }
                branch.assumptions = std::move(branch_assumptions);
                pending.push_back(std::move(branch));
            };

            if (condition == kTrueLiteral) {
                add_branch(target_true, kTrueLiteral);
            } else if (condition == kFalseLiteral) {
                add_branch(target_false, kTrueLiteral);
            } else {
                add_branch(target_true, condition);
                add_branch(target_false, literal_not(condition));
            }
            return;
        }

        if (decoded.unconditional_jump) {
            state.pc = static_cast<std::size_t>(
                static_cast<std::uint64_t>(state.pc) + 1U +
                static_cast<std::uint64_t>(program_[state.pc].k));
            pending.push_back(std::move(state));
            return;
        }

        execute_register_instruction(state, decoded);
        state.pc += 1U;
        pending.push_back(std::move(state));
    }

    const Program& program_;
    const Policy& policy_;
    AnalysisResult& result_;
    CnfProblem problem_;
    SymbolicBuilder builder_;
    bool found_foreign_ = false;
    bool found_x32_ = false;
    std::vector<bool> rule_seen_;
    std::vector<bool> argument_seen_;
    std::vector<std::vector<bool>> upper_seen_;
};

}  // namespace detail

const char* to_string(ValidationErrorKind kind) noexcept {
    switch (kind) {
        case ValidationErrorKind::empty_program:
            return "empty program";
        case ValidationErrorKind::program_too_long:
            return "program too long";
        case ValidationErrorKind::unknown_instruction:
            return "unknown instruction";
        case ValidationErrorKind::invalid_operand:
            return "invalid operand";
        case ValidationErrorKind::out_of_bounds_jump:
            return "out-of-bounds jump";
        case ValidationErrorKind::backward_jump:
            return "backward jump";
        case ValidationErrorKind::falls_off_program:
            return "falls off program";
        case ValidationErrorKind::missing_terminal_return:
            return "missing terminal return";
        case ValidationErrorKind::uninitialized_register:
            return "uninitialized register";
        case ValidationErrorKind::uninitialized_scratch:
            return "uninitialized scratch memory";
        case ValidationErrorKind::invalid_seccomp_offset:
            return "invalid seccomp_data offset";
    }
    return "unknown validation error";
}

const char* to_string(ViolationKind kind) noexcept {
    switch (kind) {
        case ViolationKind::foreign_architecture:
            return "foreign architecture reaches allow";
        case ViolationKind::x32_abi:
            return "x32 ABI syscall reaches allow";
        case ViolationKind::denied_syscall:
            return "denied syscall reaches allow";
        case ViolationKind::denied_argument:
            return "denied 64-bit argument reaches allow";
        case ViolationKind::upper_half_ignored:
            return "non-zero upper argument half reaches allow";
    }
    return "unknown violation";
}

ValidationResult validate(const Program& program) {
    using namespace detail;

    ValidationResult result;
    if (program.empty()) {
        add_unique_error(result, ValidationErrorKind::empty_program, 0U,
                         "program contains no instructions");
        return result;
    }
    if (program.size() > kMaxInstructions) {
        add_unique_error(result, ValidationErrorKind::program_too_long, 0U,
                         "program exceeds the 4096-instruction limit");
        return result;
    }

    const std::size_t length = program.size();
    std::vector<DecodedInstruction> decoded;
    decoded.reserve(length);
    std::vector<std::vector<std::size_t>> successors;
    successors.reserve(length);

    for (std::size_t pc = 0U; pc < length; ++pc) {
        decoded.push_back(decode_instruction(program[pc], result, pc));
        successors.push_back(successors_for(program, pc, decoded.back(), result));
    }

    if (!decoded.back().terminal) {
        add_unique_error(result, ValidationErrorKind::missing_terminal_return, length - 1U,
                         "the final instruction must be BPF_RET");
    }

    std::vector<bool> reachable(length, false);
    std::vector<std::size_t> pending;
    reachable[0] = true;
    pending.push_back(0U);
    while (!pending.empty()) {
        const std::size_t pc = pending.back();
        pending.pop_back();
        if (!decoded[pc].recognized) {
            continue;
        }
        if (!decoded[pc].terminal && successors[pc].empty()) {
            add_unique_error(result, ValidationErrorKind::falls_off_program, pc,
                             "reachable path does not reach BPF_RET");
        }
        for (std::size_t next : successors[pc]) {
            if (!reachable[next]) {
                reachable[next] = true;
                pending.push_back(next);
            }
        }
    }

    std::vector<std::optional<InitState>> states(length);
    std::vector<bool> queued(length, false);
    std::vector<std::size_t> worklist;
    states[0] = InitState{};
    queued[0] = true;
    worklist.push_back(0U);

    while (!worklist.empty()) {
        const std::size_t pc = worklist.back();
        worklist.pop_back();
        queued[pc] = false;
        if (!states[pc].has_value() || !decoded[pc].recognized) {
            continue;
        }

        const InitState output =
            transfer_instruction(program, pc, decoded[pc], *states[pc], result);
        for (std::size_t next : successors[pc]) {
            if (!states[next].has_value()) {
                states[next] = output;
                if (!queued[next]) {
                    queued[next] = true;
                    worklist.push_back(next);
                }
            } else {
                const InitState merged = merge_states(*states[next], output);
                if (!equal_states(*states[next], merged)) {
                    states[next] = merged;
                    if (!queued[next]) {
                        queued[next] = true;
                        worklist.push_back(next);
                    }
                }
            }
        }
    }

    return result;
}

AnalysisResult analyze(const Program& program, const Policy& policy) {
    AnalysisResult result;
    result.validation = validate(program);
    if (!result.validation.valid()) {
        return result;
    }
    detail::ProgramAnalyzer analyzer(program, policy, result);
    analyzer.run();
    return result;
}

}  // namespace cbpf
