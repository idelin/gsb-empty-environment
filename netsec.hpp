#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <stdexcept>
#include <string>
#include <vector>

namespace netsec {

// ---------------------------------------------------------------------------
// Linux seccomp / audit constants. The filter runs on x86_64; the host that
// runs the compiler or the tests may be any architecture, so these values are
// spelled out explicitly instead of pulling in <linux/seccomp.h>.
// ---------------------------------------------------------------------------

constexpr std::uint32_t AUDIT_ARCH_X86_64 = 0xC000003E;
constexpr std::uint32_t AUDIT_ARCH_I386   = 0x40000003;
constexpr std::uint32_t X32_SYSCALL_BIT    = 0x40000000;

constexpr std::uint32_t SECCOMP_RET_KILL_PROCESS = 0x80000000;
constexpr std::uint32_t SECCOMP_RET_KILL_THREAD  = 0x00000000;
constexpr std::uint32_t SECCOMP_RET_TRAP         = 0x00030000;
constexpr std::uint32_t SECCOMP_RET_ERRNO        = 0x00050000;
constexpr std::uint32_t SECCOMP_RET_USER_NOTIF   = 0x7FC00000;
constexpr std::uint32_t SECCOMP_RET_TRACE        = 0x7FF00000;
constexpr std::uint32_t SECCOMP_RET_LOG          = 0x7FFC0000;
constexpr std::uint32_t SECCOMP_RET_ALLOW        = 0x7FFF0000;

constexpr std::uint32_t SECCOMP_RET_ACTION_FULL = 0xFFFF0000;
constexpr std::uint16_t SECCOMP_RET_DATA_MASK   = 0xFFFF;

// BPF instruction classes / opcodes (classic BPF, <linux/filter.h>).
constexpr std::uint16_t BPF_LD   = 0x00;
constexpr std::uint16_t BPF_JMP  = 0x05;
constexpr std::uint16_t BPF_RET  = 0x06;
constexpr std::uint16_t BPF_W    = 0x00;
constexpr std::uint16_t BPF_ABS  = 0x20;
constexpr std::uint16_t BPF_JEQ  = 0x10;
constexpr std::uint16_t BPF_JGT  = 0x20;
constexpr std::uint16_t BPF_JGE  = 0x30;
constexpr std::uint16_t BPF_JSET = 0x40;
constexpr std::uint16_t BPF_K    = 0x00;
constexpr std::uint16_t BPF_JA   = 0x00;

// Hard kernel limit for seccomp filters.
constexpr std::size_t MAX_BPF_INSTRUCTIONS = 4096;

// One classic BPF instruction: sock_filter (struct sock_filter).
struct BpfInsn {
    std::uint16_t code;
    std::uint8_t  jt;
    std::uint8_t  jf;
    std::uint32_t k;
};

// Layout of struct seccomp_data (64 bytes), little-endian fields.
struct SeccompData {
    std::int32_t  nr;
    std::uint32_t arch;
    std::uint64_t instruction_pointer;
    std::uint64_t args[6];
};

// Seccomp_data word offsets used with BPF_LD | BPF_W | BPF_ABS.
constexpr std::uint32_t OFF_ARCH      = 4;
constexpr std::uint32_t OFF_NR        = 0;
constexpr std::uint32_t OFF_ARG_BASE  = 16;
constexpr std::uint32_t OFF_ARG_WORDS = 2; // 32-bit words per 64-bit arg

enum class Action {
    Allow,
    KillProcess,
    KillThread,
    Trap,
    Errno,
    Log,
};

enum class Cmp {
    Eq,
    Ne,
    Lt,
    Le,
    Gt,
    Ge,
};

// A single unsigned 64-bit argument comparison. arg_index ranges 0..5 and
// refers to seccomp_data.args[].
struct Condition {
    unsigned       arg_index = 0;
    Cmp            op        = Cmp::Eq;
    std::uint64_t  value     = 0;
};

struct Rule {
    std::uint32_t           syscall = 0;
    Action                  action  = Action::Allow;
    std::uint16_t           errno_data = 0; // used when action == Errno
    std::vector<Condition>  conditions;   // all must hold (AND)
};

struct Policy {
    Action        default_action = Action::KillProcess;
    std::uint16_t default_errno  = 1; // EPERM
    // Rules are evaluated first-match in this order.
    std::vector<Rule> rules;
};

class CompileError : public std::runtime_error {
public:
    explicit CompileError(const std::string& msg) : std::runtime_error(msg) {}
};

// Compile a policy into a classic BPF program. Throws CompileError when the
// program would exceed 4096 instructions, a jump target is out of the 8-bit
// offset range, or the policy is malformed.
std::vector<BpfInsn> compile(const Policy& policy);

enum class Verdict {
    Allow,
    KillProcess,
    KillThread,
    Trap,
    Errno,
    Log,
    Trace,
    UserNotif,
    Unknown,
};

struct BpfResult {
    std::uint32_t raw = 0; // raw SECCOMP_RET_* value produced by RET
    Verdict       verdict = Verdict::Unknown;
    std::uint16_t data = 0;
};

// Minimal in-process classic BPF interpreter. It executes exactly the subset
// of BPF emitted by compile() (absolute word loads, conditional/unconditional
// jumps, immediate returns) and is platform independent.
class BpfInterpreter {
public:
    BpfInterpreter() = default;

    BpfResult run(const std::vector<BpfInsn>& program, const SeccompData& input) const;

private:
    static std::uint32_t load_word(const std::uint8_t data[64], std::uint32_t offset);
};

// Reference policy evaluator that mirrors the compiler semantics without BPF.
// Wrong arch and x32 nr are handled before rule evaluation.
BpfResult evaluate_policy(const Policy& policy, const SeccompData& input);

// ---------------------------------------------------------------------------
// Differential verification: enumerate syscall numbers and argument boundary
// values, run every case through the BPF interpreter and compare it with the
// direct policy evaluator.
// ---------------------------------------------------------------------------

struct VerifyOptions {
    // Syscall numbers without the x32 bit are swept over [0, nr_limit).
    std::uint32_t nr_limit = 1024;
    // Extra syscall numbers referenced by policy rules are always covered.
    bool check_foreign_arch = true;
    bool check_x32         = true;
    // Maximum number of argument combinations per conditional rule.
    std::size_t max_combos_per_rule = 4096;
};

struct VerifyReport {
    std::size_t cases_run      = 0;
    std::size_t mismatches     = 0;
    std::size_t instructions   = 0;
    bool        size_limit_ok  = false;
    bool        jump_range_ok  = false;
    std::vector<std::string> mismatch_details;
    bool ok() const {
        return mismatches == 0 && size_limit_ok && jump_range_ok &&
               mismatch_details.empty();
    }
};

VerifyReport verify(const Policy& policy, const VerifyOptions& opts = {});

// Human-readable one-line disassembly, one instruction per line.
std::string disassemble(const std::vector<BpfInsn>& program);

} // namespace netsec
