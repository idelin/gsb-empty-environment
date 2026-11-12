#include "netsec.hpp"

#include <cstring>
#include <limits>
#include <set>
#include <sstream>
#include <utility>

namespace netsec {

namespace {

// ---------------------------------------------------------------------------
// Program builder with symbolic labels.
//
// Classic BPF conditional jumps carry two 8-bit relative offsets, so every
// jump is emitted against a label and resolved once the whole program is
// laid out. All generated jumps are short forward jumps; a distance that does
// not fit in 8 bits is a hard compile error rather than silently corrupting
// the filter.
// ---------------------------------------------------------------------------

struct Fixup {
    std::size_t insn_index; // jump instruction that needs patching
    int         slot;       // 0 -> jt, 1 -> jf, 2 -> unconditional k offset
    std::string label;
};

class ProgramBuilder {
public:
    std::string new_label(const std::string& hint) {
        std::string name = hint + "_" + std::to_string(counter_++);
        labels_.emplace_back(name, Label{false, 0});
        return name;
    }

    void mark(const std::string& label) {
        Label& entry = require(label);
        entry.marked = true;
        entry.position = code_.size();
    }

    void emit_raw(BpfInsn insn) { code_.push_back(insn); }

    void emit_ld_abs(std::uint32_t offset) {
        emit_raw(BpfInsn{static_cast<std::uint16_t>(BPF_LD | BPF_W | BPF_ABS),
                         0, 0, offset});
    }

    void emit_ret(std::uint32_t value) {
        emit_raw(BpfInsn{BPF_RET, 0, 0, value});
    }

    void emit_jeq(std::uint32_t k, const std::string& jt, const std::string& jf) {
        fixups_.push_back(Fixup{code_.size(), 0, jt});
        fixups_.push_back(Fixup{code_.size(), 1, jf});
        emit_raw(BpfInsn{static_cast<std::uint16_t>(BPF_JMP | BPF_JEQ | BPF_K),
                         0, 0, k});
    }

    void emit_jgt(std::uint32_t k, const std::string& jt, const std::string& jf) {
        fixups_.push_back(Fixup{code_.size(), 0, jt});
        fixups_.push_back(Fixup{code_.size(), 1, jf});
        emit_raw(BpfInsn{static_cast<std::uint16_t>(BPF_JMP | BPF_JGT | BPF_K),
                         0, 0, k});
    }

    void emit_jge(std::uint32_t k, const std::string& jt, const std::string& jf) {
        fixups_.push_back(Fixup{code_.size(), 0, jt});
        fixups_.push_back(Fixup{code_.size(), 1, jf});
        emit_raw(BpfInsn{static_cast<std::uint16_t>(BPF_JMP | BPF_JGE | BPF_K),
                         0, 0, k});
    }

    void emit_jset(std::uint32_t k, const std::string& jt, const std::string& jf) {
        fixups_.push_back(Fixup{code_.size(), 0, jt});
        fixups_.push_back(Fixup{code_.size(), 1, jf});
        emit_raw(BpfInsn{static_cast<std::uint16_t>(BPF_JMP | BPF_JSET | BPF_K),
                         0, 0, k});
    }

    void emit_ja(const std::string& target) {
        fixups_.push_back(Fixup{code_.size(), 2, target});
        emit_raw(BpfInsn{static_cast<std::uint16_t>(BPF_JMP | BPF_JA), 0, 0, 0});
    }

    std::vector<BpfInsn> finalize() {
        for (const Fixup& fix : fixups_) {
            const Label& target = require(fix.label);
            if (!target.marked) {
                throw CompileError("internal: jump to unmarked label " + fix.label);
            }
            if (target.position <= fix.insn_index) {
                throw CompileError("internal: only forward jumps are supported");
            }
            std::size_t distance = target.position - fix.insn_index - 1;
            if (distance > std::numeric_limits<std::uint8_t>::max()) {
                throw CompileError("jump target out of 8-bit BPF offset range");
            }
            auto byte = static_cast<std::uint8_t>(distance);
            BpfInsn& insn = code_[fix.insn_index];
            if (fix.slot == 0) {
                insn.jt = byte;
            } else if (fix.slot == 1) {
                insn.jf = byte;
            } else {
                insn.k = static_cast<std::uint32_t>(distance);
            }
        }
        if (code_.size() > MAX_BPF_INSTRUCTIONS) {
            throw CompileError("BPF program exceeds 4096 instructions");
        }
        return code_;
    }

    std::size_t size() const { return code_.size(); }

private:
    struct Label {
        bool        marked;
        std::size_t position;
    };

    Label& require(const std::string& name) {
        for (auto& entry : labels_) {
            if (entry.first == name) {
                return entry.second;
            }
        }
        // Unreachable for well-formed internal code; keep a hard guard.
        throw CompileError("internal: unknown label " + name);
    }

    std::size_t counter_ = 0;
    std::vector<BpfInsn> code_;
    std::vector<Fixup> fixups_;
    std::vector<std::pair<std::string, Label>> labels_;
};

std::uint32_t ret_value(Action action, std::uint16_t data) {
    switch (action) {
    case Action::Allow:       return SECCOMP_RET_ALLOW;
    case Action::KillProcess: return SECCOMP_RET_KILL_PROCESS;
    case Action::KillThread:  return SECCOMP_RET_KILL_THREAD;
    case Action::Trap:        return SECCOMP_RET_TRAP | data;
    case Action::Errno:       return SECCOMP_RET_ERRNO | data;
    case Action::Log:         return SECCOMP_RET_LOG;
    }
    return SECCOMP_RET_KILL_PROCESS;
}

std::uint32_t arg_word_offset(unsigned arg_index, bool high_word) {
    // Each arg occupies two 32-bit words; the low word comes first on
    // little-endian x86_64.
    return OFF_ARG_BASE + (arg_index * OFF_ARG_WORDS + (high_word ? 1 : 0)) * 4;
}

// Emit one unsigned 64-bit comparison against (low, high). Both 32-bit halves
// of the argument are loaded and tested so a filter can never be bypassed by
// placing significant bits in the upper 32 bits.
void emit_condition(ProgramBuilder& builder, const Condition& cond,
                    const std::string& pass, const std::string& fail) {
    if (cond.arg_index > 5) {
        throw CompileError("condition arg_index out of range (must be 0..5)");
    }
    const std::uint32_t low  = static_cast<std::uint32_t>(cond.value);
    const std::uint32_t high = static_cast<std::uint32_t>(cond.value >> 32);
    const std::uint32_t low_off  = arg_word_offset(cond.arg_index, false);
    const std::uint32_t high_off = arg_word_offset(cond.arg_index, true);

    switch (cond.op) {
    case Cmp::Eq:
    {
        // A == V: high == Vh AND low == Vl. The high-word match may only
        // fall into the low-word check; jumping straight to pass would skip
        // the second 32-bit word.
        builder.emit_ld_abs(high_off);
        std::string eq_low = builder.new_label("eq_low");
        builder.emit_jeq(high, eq_low, fail);
        builder.mark(eq_low);
        builder.emit_ld_abs(low_off);
        builder.emit_jeq(low, pass, fail);
        break;
    }

    case Cmp::Ne:
    {
        // A != V: high != Vh OR low != Vl.
        builder.emit_ld_abs(high_off);
        std::string ne_low = builder.new_label("ne_low");
        builder.emit_jeq(high, ne_low, pass); // high equal -> low check, differs -> pass
        builder.mark(ne_low);
        builder.emit_ld_abs(low_off);
        builder.emit_jeq(low, fail, pass);     // low equals -> fail, differs -> pass
        break;
    }

    case Cmp::Lt:
    {
        // A < V: Ah < Vh, or (Ah == Vh and Al < Vl).
        builder.emit_ld_abs(high_off);
        std::string lt_equal = builder.new_label("lt_equal");
        std::string lt_low = builder.new_label("lt_low");
        builder.emit_jge(high, lt_equal, pass); // Ah < Vh -> pass
        builder.mark(lt_equal);
        builder.emit_jgt(high, fail, lt_low);   // Ah > Vh -> fail, == -> low
        builder.mark(lt_low);
        builder.emit_ld_abs(low_off);
        builder.emit_jge(low, fail, pass);
        break;
    }

    case Cmp::Le:
    {
        // A <= V: Ah < Vh, or (Ah == Vh and Al <= Vl).
        builder.emit_ld_abs(high_off);
        std::string le_equal = builder.new_label("le_equal");
        std::string le_low = builder.new_label("le_low");
        builder.emit_jge(high, le_equal, pass); // Ah < Vh -> pass
        builder.mark(le_equal);
        builder.emit_jgt(high, fail, le_low);   // Ah > Vh -> fail, == -> low
        builder.mark(le_low);
        builder.emit_ld_abs(low_off);
        builder.emit_jgt(low, fail, pass);
        break;
    }

    case Cmp::Gt:
    {
        // A > V: Ah > Vh, or (Ah == Vh and Al > Vl).
        builder.emit_ld_abs(high_off);
        std::string gt_equal = builder.new_label("gt_equal");
        std::string gt_low = builder.new_label("gt_low");
        builder.emit_jgt(high, pass, gt_equal); // Ah > Vh -> pass, else inspect
        builder.mark(gt_equal);
        builder.emit_jge(high, gt_low, fail);   // Ah == Vh -> low, Ah < Vh -> fail
        builder.mark(gt_low);
        builder.emit_ld_abs(low_off);
        builder.emit_jgt(low, pass, fail);
        break;
    }

    case Cmp::Ge:
    {
        // A >= V: Ah > Vh, or (Ah == Vh and Al >= Vl).
        builder.emit_ld_abs(high_off);
        std::string ge_equal = builder.new_label("ge_equal");
        std::string ge_low = builder.new_label("ge_low");
        builder.emit_jgt(high, pass, ge_equal);
        builder.mark(ge_equal);
        builder.emit_jge(high, ge_low, fail);
        builder.mark(ge_low);
        builder.emit_ld_abs(low_off);
        builder.emit_jge(low, pass, fail);
        break;
    }
    }
}

} // namespace

std::vector<BpfInsn> compile(const Policy& policy) {
    ProgramBuilder builder;

    // 1) Architecture gate: any non-x86_64 ABI is killed before anything else.
    builder.emit_ld_abs(OFF_ARCH);
    std::string arch_ok = builder.new_label("arch_ok");
    std::string arch_bad = builder.new_label("arch_bad");
    builder.emit_jeq(AUDIT_ARCH_X86_64, arch_ok, arch_bad);
    builder.mark(arch_bad);
    builder.emit_ret(SECCOMP_RET_KILL_PROCESS);
    builder.mark(arch_ok);

    // 2) x32 ABI shares the x86_64 arch value but sets bit 30 on the nr.
    //    Kill every such call so it cannot alias a native syscall number.
    builder.emit_ld_abs(OFF_NR);
    std::string native_nr = builder.new_label("native_nr");
    std::string x32_kill = builder.new_label("x32_kill");
    builder.emit_jset(X32_SYSCALL_BIT, x32_kill, native_nr);
    builder.mark(x32_kill);
    builder.emit_ret(SECCOMP_RET_KILL_PROCESS);
    builder.mark(native_nr);

    // 3) First-match rule dispatch.
    for (const Rule& rule : policy.rules) {
        if (rule.syscall & X32_SYSCALL_BIT) {
            throw CompileError("rule syscall number must not set the x32 bit");
        }
        std::string rule_fail = builder.new_label("rule_fail");

        builder.emit_ld_abs(OFF_NR);
        std::string nr_hit = builder.new_label("nr_hit");
        builder.emit_jeq(rule.syscall, nr_hit, rule_fail);
        builder.mark(nr_hit);

        if (rule.conditions.empty()) {
            builder.emit_ret(ret_value(rule.action, rule.errno_data));
        } else {
            // Pre-create every entry label so the previous condition can jump
            // forward to it; the final entry lands on the action return.
            std::vector<std::string> entries;
            entries.reserve(rule.conditions.size() + 1);
            for (std::size_t i = 0; i < rule.conditions.size(); ++i) {
                entries.push_back(builder.new_label("cond"));
            }
            std::string action_label = builder.new_label("action");

            for (std::size_t i = 0; i < rule.conditions.size(); ++i) {
                builder.mark(entries[i]);
                std::string next = (i + 1 < rule.conditions.size())
                                       ? entries[i + 1]
                                       : action_label;
                emit_condition(builder, rule.conditions[i], next, rule_fail);
            }
            builder.mark(action_label);
            builder.emit_ret(ret_value(rule.action, rule.errno_data));
        }

        builder.mark(rule_fail);
    }

    // 4) Default action for unmatched native syscalls.
    builder.emit_ret(ret_value(policy.default_action, policy.default_errno));

    return builder.finalize();
}

namespace {

BpfResult decode_result(std::uint32_t raw) {
    BpfResult result;
    result.raw = raw;
    result.data = static_cast<std::uint16_t>(raw & SECCOMP_RET_DATA_MASK);
    const std::uint32_t action = raw & SECCOMP_RET_ACTION_FULL;
    switch (action) {
    case SECCOMP_RET_ALLOW:       result.verdict = Verdict::Allow; break;
    case SECCOMP_RET_KILL_PROCESS: result.verdict = Verdict::KillProcess; break;
    case SECCOMP_RET_KILL_THREAD: result.verdict = Verdict::KillThread; break;
    case SECCOMP_RET_TRAP:        result.verdict = Verdict::Trap; break;
    case SECCOMP_RET_ERRNO:       result.verdict = Verdict::Errno; break;
    case SECCOMP_RET_LOG:         result.verdict = Verdict::Log; break;
    case SECCOMP_RET_TRACE:       result.verdict = Verdict::Trace; break;
    case SECCOMP_RET_USER_NOTIF:  result.verdict = Verdict::UserNotif; break;
    default:                      result.verdict = Verdict::Unknown; break;
    }
    return result;
}

BpfResult action_result(Action action, std::uint16_t data) {
    return decode_result(ret_value(action, data));
}

bool condition_holds(const Condition& cond, std::uint64_t actual) {
    const std::uint64_t expected = cond.value;
    switch (cond.op) {
    case Cmp::Eq: return actual == expected;
    case Cmp::Ne: return actual != expected;
    case Cmp::Lt: return actual <  expected;
    case Cmp::Le: return actual <= expected;
    case Cmp::Gt: return actual >  expected;
    case Cmp::Ge: return actual >= expected;
    }
    return false;
}

SeccompData make_input(std::uint32_t arch, std::uint32_t nr) {
    SeccompData data{};
    data.arch = arch;
    data.nr = static_cast<std::int32_t>(nr);
    return data;
}

} // namespace

// ---------------------------------------------------------------------------
// Interpreter
// ---------------------------------------------------------------------------

std::uint32_t BpfInterpreter::load_word(const std::uint8_t data[64],
                                        std::uint32_t offset) {
    if (offset > 60) {
        throw std::runtime_error("BPF load out of seccomp_data bounds");
    }
    // seccomp_data words are little endian.
    return static_cast<std::uint32_t>(data[offset]) |
           (static_cast<std::uint32_t>(data[offset + 1]) << 8) |
           (static_cast<std::uint32_t>(data[offset + 2]) << 16) |
           (static_cast<std::uint32_t>(data[offset + 3]) << 24);
}

BpfResult BpfInterpreter::run(const std::vector<BpfInsn>& program,
                              const SeccompData& input) const {
    if (program.empty()) {
        throw std::runtime_error("cannot run empty BPF program");
    }

    std::uint8_t memory[64];
    std::memcpy(memory, &input, sizeof(memory));

    std::uint32_t reg_a = 0;
    std::size_t pc = 0;
    std::size_t steps = 0;
    constexpr std::size_t max_steps = 1u << 20;

    while (pc < program.size()) {
        if (++steps > max_steps) {
            throw std::runtime_error("BPF program did not terminate");
        }
        const BpfInsn& insn = program[pc];
        const std::uint16_t cls = insn.code & 0x07;

        if (cls == (BPF_LD & 0x07)) {
            reg_a = load_word(memory, insn.k);
            ++pc;
            continue;
        }

        if (cls == BPF_RET) {
            return decode_result(insn.k);
        }

        if (cls == BPF_JMP) {
            const std::uint16_t op = insn.code & 0xF0;
            if (op == BPF_JA) {
                pc += 1 + insn.k;
                continue;
            }
            bool taken = false;
            switch (op) {
            case BPF_JEQ:  taken = (reg_a == insn.k); break;
            case BPF_JGT:  taken = (reg_a >  insn.k); break;
            case BPF_JGE:  taken = (reg_a >= insn.k); break;
            case BPF_JSET: taken = ((reg_a & insn.k) != 0); break;
            default: throw std::runtime_error("unsupported BPF jump opcode");
            }
            pc += 1 + (taken ? insn.jt : insn.jf);
            continue;
        }

        throw std::runtime_error("unsupported BPF instruction class");
    }

    throw std::runtime_error("BPF program fell off the end without RET");
}

// ---------------------------------------------------------------------------
// Reference policy evaluation (mirrors compile() exactly)
// ---------------------------------------------------------------------------

BpfResult evaluate_policy(const Policy& policy, const SeccompData& input) {
    if (input.arch != AUDIT_ARCH_X86_64) {
        return action_result(Action::KillProcess, 0);
    }

    const std::uint32_t nr = static_cast<std::uint32_t>(input.nr);
    if (nr & X32_SYSCALL_BIT) {
        return action_result(Action::KillProcess, 0);
    }

    for (const Rule& rule : policy.rules) {
        if (rule.syscall != nr) {
            continue;
        }
        bool all_hold = true;
        for (const Condition& cond : rule.conditions) {
            if (!condition_holds(cond, input.args[cond.arg_index])) {
                all_hold = false;
                break;
            }
        }
        if (all_hold) {
            return action_result(rule.action, rule.errno_data);
        }
        // Failing conditions fall through, exactly like the BPF filter.
    }

    return action_result(policy.default_action, policy.default_errno);
}

// ---------------------------------------------------------------------------
// Differential verification
// ---------------------------------------------------------------------------

namespace {

std::vector<std::uint64_t> boundary_values(std::uint64_t value) {
    std::set<std::uint64_t> values;
    values.insert(0);
    values.insert(std::numeric_limits<std::uint64_t>::max());
    values.insert(value);
    if (value > 0) {
        values.insert(value - 1);
    }
    if (value < std::numeric_limits<std::uint64_t>::max()) {
        values.insert(value + 1);
    }
    // Explicitly flip the upper 32-bit word so a low-word-only comparison
    // cannot pass the verification unnoticed.
    const std::uint64_t high_part = value & 0xFFFFFFFF00000000ULL;
    if (high_part > 0) {
        values.insert(value - 0x100000000ULL);
    }
    if (high_part <= 0xFFFFFFFF00000000ULL - 0x100000000ULL) {
        values.insert(value + 0x100000000ULL);
    }
    return std::vector<std::uint64_t>(values.begin(), values.end());
}

bool jumps_in_bounds(const std::vector<BpfInsn>& program) {
    for (std::size_t i = 0; i < program.size(); ++i) {
        const BpfInsn& insn = program[i];
        if ((insn.code & 0x07) != BPF_JMP) {
            continue;
        }
        if ((insn.code & 0xF0) == BPF_JA) {
            std::size_t target = i + 1 + insn.k;
            if (target >= program.size()) {
                return false;
            }
        } else {
            if (i + 1 + insn.jt >= program.size() ||
                i + 1 + insn.jf >= program.size()) {
                return false;
            }
        }
    }
    return true;
}

} // namespace

VerifyReport verify(const Policy& policy, const VerifyOptions& opts) {
    VerifyReport report;
    const std::vector<BpfInsn> program = compile(policy);
    const BpfInterpreter interpreter;

    report.instructions = program.size();
    report.size_limit_ok = program.size() <= MAX_BPF_INSTRUCTIONS;
    report.jump_range_ok = jumps_in_bounds(program);

    std::size_t comparisons = 0;
    auto check = [&](const SeccompData& data, const std::string& label) {
        const BpfResult expected = evaluate_policy(policy, data);
        const BpfResult actual = interpreter.run(program, data);
        ++comparisons;
        if (expected.raw != actual.raw) {
            ++report.mismatches;
            if (report.mismatch_details.size() < 20) {
                report.mismatch_details.push_back(
                    label + ": expected raw=0x" +
                    std::to_string(expected.raw) + " actual raw=0x" +
                    std::to_string(actual.raw));
            }
        }
    };

    // Foreign architecture must always be killed.
    if (opts.check_foreign_arch) {
        check(make_input(AUDIT_ARCH_I386, 2), "foreign arch i386 nr=2");
        check(make_input(AUDIT_ARCH_I386, 0), "foreign arch i386 nr=0");
    }

    // Every native syscall number in the configured range, plus each
    // referenced number and its neighbors.
    std::set<std::uint32_t> native_numbers;
    for (std::uint32_t nr = 0; nr < opts.nr_limit; ++nr) {
        native_numbers.insert(nr);
    }
    for (const Rule& rule : policy.rules) {
        native_numbers.insert(rule.syscall);
        if (rule.syscall > 0) {
            native_numbers.insert(rule.syscall - 1);
        }
        native_numbers.insert(rule.syscall + 1);
    }
    for (std::uint32_t nr : native_numbers) {
        check(make_input(AUDIT_ARCH_X86_64, nr), "native nr=" + std::to_string(nr));
    }

    // x32-tagged calls, including aliases of policy syscalls, must die.
    if (opts.check_x32) {
        std::set<std::uint32_t> x32_numbers{0, 2, 59};
        for (const Rule& rule : policy.rules) {
            x32_numbers.insert(rule.syscall);
        }
        for (std::uint32_t nr : x32_numbers) {
            check(make_input(AUDIT_ARCH_X86_64, nr | X32_SYSCALL_BIT),
                  "x32 nr=" + std::to_string(nr));
        }
    }

    // Argument boundary enumeration for every conditional rule.
    for (const Rule& rule : policy.rules) {
        if (rule.conditions.empty()) {
            continue;
        }
        std::vector<std::vector<std::uint64_t>> axes;
        axes.reserve(rule.conditions.size());
        std::size_t combos = 1;
        for (const Condition& cond : rule.conditions) {
            auto axis = boundary_values(cond.value);
            combos *= axis.size();
            axes.push_back(std::move(axis));
        }
        combos = std::min(combos, opts.max_combos_per_rule);

        for (std::size_t combo = 0; combo < combos; ++combo) {
            SeccompData data = make_input(AUDIT_ARCH_X86_64, rule.syscall);
            std::size_t remaining = combo;
            for (std::size_t axis = 0; axis < axes.size(); ++axis) {
                const std::size_t picked = remaining % axes[axis].size();
                remaining /= axes[axis].size();
                data.args[rule.conditions[axis].arg_index] = axes[axis][picked];
            }
            check(data, "rule nr=" + std::to_string(rule.syscall) +
                            " combo=" + std::to_string(combo));
        }
    }

    report.cases_run = comparisons;
    return report;
}

// ---------------------------------------------------------------------------
// Disassembly (debugging aid)
// ---------------------------------------------------------------------------

std::string disassemble(const std::vector<BpfInsn>& program) {
    std::ostringstream out;
    for (std::size_t i = 0; i < program.size(); ++i) {
        const BpfInsn& insn = program[i];
        out << i << ": ";
        const std::uint16_t cls = insn.code & 0x07;
        if (cls == (BPF_LD & 0x07)) {
            out << "ld_abs #" << insn.k;
        } else if (cls == BPF_RET) {
            out << "ret #0x" << std::hex << insn.k << std::dec;
        } else if (cls == BPF_JMP) {
            const std::uint16_t op = insn.code & 0xF0;
            if (op == BPF_JA) {
                out << "ja +" << insn.k;
            } else {
                const char* mnemonic = "j??";
                switch (op) {
                case BPF_JEQ:  mnemonic = "jeq"; break;
                case BPF_JGT:  mnemonic = "jgt"; break;
                case BPF_JGE:  mnemonic = "jge"; break;
                case BPF_JSET: mnemonic = "jset"; break;
                default: break;
                }
                out << mnemonic << " #" << insn.k << " +"
                    << static_cast<unsigned>(insn.jt) << " +"
                    << static_cast<unsigned>(insn.jf);
            }
        } else {
            out << "word 0x" << std::hex << insn.code << std::dec;
        }
        out << "\n";
    }
    return out.str();
}

} // namespace netsec
