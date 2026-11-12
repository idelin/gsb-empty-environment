#include "netsec.hpp"

#include <cassert>
#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

using namespace netsec;

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& message) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "FAIL: " << message << "\n";
    }
}

SeccompData call(std::uint32_t arch, std::uint32_t nr) {
    SeccompData data{};
    data.arch = arch;
    data.nr = static_cast<std::int32_t>(nr);
    return data;
}

SeccompData x64call(std::uint32_t nr) {
    return call(AUDIT_ARCH_X86_64, nr);
}

Policy demo_policy() {
    Policy policy;
    policy.default_action = Action::KillProcess;

    // open/openat are unconditionally blocked.
    policy.rules.push_back(Rule{2, Action::Errno, 1, {}});      // open
    policy.rules.push_back(Rule{257, Action::Errno, 1, {}});    // openat

    // write only allowed to fds 0..2 (first arg is the fd).
    policy.rules.push_back(
        Rule{1, Action::Allow, 0, {Condition{0, Cmp::Le, 2}}});

    // mmap prot must be <= PROT_ALL (0x7).
    policy.rules.push_back(
        Rule{9, Action::Allow, 0, {Condition{2, Cmp::Le, 7}}});

    // mprotect with a 64-bit address boundary so both 32-bit words matter.
    constexpr std::uint64_t high_addr = 0x100000007ULL;
    policy.rules.push_back(
        Rule{10, Action::Allow, 0, {Condition{0, Cmp::Lt, high_addr}}});

    // exit_group always allowed.
    policy.rules.push_back(Rule{231, Action::Allow, 0, {}});

    return policy;
}

void test_prologue_guards() {
    const auto program = compile(demo_policy());
    const BpfInterpreter interpreter;

    // First instruction loads arch; wrong ABI dies even for a permitted nr.
    auto foreign = call(AUDIT_ARCH_I386, 231);
    check(interpreter.run(program, foreign).verdict == Verdict::KillProcess,
          "foreign arch must kill");
    check(interpreter.run(program, call(0, 231)).verdict == Verdict::KillProcess,
          "zero/garbage arch must kill");

    // x32-tagged calls die, including aliases of native allowlisted numbers.
    check(interpreter.run(program, x64call(231 | X32_SYSCALL_BIT))
                  .verdict == Verdict::KillProcess,
          "x32 exit_group alias must kill");
    check(interpreter.run(program, x64call(2 | X32_SYSCALL_BIT))
                  .verdict == Verdict::KillProcess,
          "x32 open alias must kill");
}

void test_basic_actions() {
    const auto program = compile(demo_policy());
    const BpfInterpreter interpreter;

    check(interpreter.run(program, x64call(231)).verdict == Verdict::Allow,
          "exit_group allowed");
    check(interpreter.run(program, x64call(2)).verdict == Verdict::Errno,
          "open returns errno");
    check(interpreter.run(program, x64call(2)).data == 1,
          "open errno data preserved");
    check(interpreter.run(program, x64call(257)).verdict == Verdict::Errno,
          "openat returns errno");
    check(interpreter.run(program, x64call(99)).verdict == Verdict::KillProcess,
          "unknown syscall hits default kill");
}

void test_64bit_arguments_use_both_words() {
    const auto program = compile(demo_policy());
    const BpfInterpreter interpreter;

    // write fd <= 2.
    SeccompData w = x64call(1);
    w.args[0] = 2;
    check(interpreter.run(program, w).verdict == Verdict::Allow, "write fd 2");
    w.args[0] = 3;
    check(interpreter.run(program, w).verdict == Verdict::KillProcess,
          "write fd 3 falls through to default");
    w.args[0] = 0x100000002ULL;
    check(interpreter.run(program, w).verdict == Verdict::KillProcess,
          "write fd with high word set must not pass low-word-only compare");

    // mprotect addr < 0x100000007.
    SeccompData m = x64call(10);
    m.args[0] = 0x100000006ULL;
    check(interpreter.run(program, m).verdict == Verdict::Allow,
          "mprotect just below high boundary");
    m.args[0] = 0x100000007ULL;
    check(interpreter.run(program, m).verdict == Verdict::KillProcess,
          "mprotect at boundary is not less-than");
    m.args[0] = 0x000000006ULL;
    check(interpreter.run(program, m).verdict == Verdict::Allow,
          "mprotect low address shares low word but smaller high word");
    m.args[0] = 0x200000006ULL;
    check(interpreter.run(program, m).verdict == Verdict::KillProcess,
          "mprotect same low word but larger high word");
}

void test_every_comparison_operator() {
    struct Case {
        Cmp op;
        std::uint64_t threshold;
        std::uint64_t yes;
        std::uint64_t no;
    };
    const std::uint64_t v = 0x200000005ULL;
    const std::vector<Case> cases = {
        {Cmp::Eq, v, v, v + 1},
        {Cmp::Ne, v, v + 1, v},
        {Cmp::Lt, v, v - 1, v},
        {Cmp::Le, v, v, v + 1},
        {Cmp::Gt, v, v + 1, v},
        {Cmp::Ge, v, v, v - 1},
    };

    const BpfInterpreter interpreter;
    for (const Case& tc : cases) {
        Policy policy;
        policy.default_action = Action::KillProcess;
        policy.rules.push_back(
            Rule{100, Action::Allow, 0, {Condition{1, tc.op, tc.threshold}}});
        const auto program = compile(policy);

        SeccompData good = x64call(100);
        good.args[1] = tc.yes;
        check(interpreter.run(program, good).verdict == Verdict::Allow,
              "operator positive case");

        SeccompData bad = x64call(100);
        bad.args[1] = tc.no;
        check(interpreter.run(program, bad).verdict == Verdict::KillProcess,
              "operator negative case");
    }
}

void test_first_match_and_and_conditions() {
    Policy policy;
    policy.default_action = Action::KillProcess;
    policy.rules.push_back(
        Rule{100, Action::Allow, 0,
             {Condition{0, Cmp::Eq, 5}, Condition{1, Cmp::Lt, 10}}});

    const auto program = compile(policy);
    const BpfInterpreter interpreter;

    SeccompData ok = x64call(100);
    ok.args[0] = 5;
    ok.args[1] = 9;
    check(interpreter.run(program, ok).verdict == Verdict::Allow, "both hold");

    SeccompData second_bad = x64call(100);
    second_bad.args[0] = 5;
    second_bad.args[1] = 10;
    check(interpreter.run(program, second_bad).verdict == Verdict::KillProcess,
          "second condition fails");

    SeccompData first_bad = x64call(100);
    first_bad.args[0] = 6;
    first_bad.args[1] = 0;
    check(interpreter.run(program, first_bad).verdict == Verdict::KillProcess,
          "first condition fails");
}

void test_size_limit() {
    Policy policy;
    policy.default_action = Action::Allow;
    for (std::uint32_t nr = 0; nr < 500; ++nr) {
        policy.rules.push_back(Rule{nr, Action::Errno, 1, {}});
    }
    const auto program = compile(policy);
    check(program.size() <= MAX_BPF_INSTRUCTIONS, "500 rules fit under 4096");

    Policy huge;
    huge.default_action = Action::Allow;
    for (std::uint32_t nr = 0; nr < 2000; ++nr) {
        huge.rules.push_back(Rule{nr, Action::Errno, 1, {}});
    }
    bool threw = false;
    try {
        (void)compile(huge);
    } catch (const CompileError&) {
        threw = true;
    }
    check(threw, "oversized policy is rejected with CompileError");
}

void test_differential_verification() {
    const VerifyReport report = verify(demo_policy());
    check(report.ok(), "differential verification passes");
    check(report.cases_run > 1000, "enumeration covers many cases");
    check(report.size_limit_ok, "program within 4096 instructions");
    check(report.jump_range_ok, "all jump offsets stay in range");
    if (!report.mismatch_details.empty()) {
        for (const auto& detail : report.mismatch_details) {
            std::cerr << "  " << detail << "\n";
        }
    }
}

void test_verify_all_operators_with_boundaries() {
    const std::vector<Cmp> ops = {
        Cmp::Eq, Cmp::Ne, Cmp::Lt, Cmp::Le, Cmp::Gt, Cmp::Ge};
    const std::vector<std::uint64_t> values = {
        0, 1, 0xFFFFFFFFULL, 0x100000000ULL, 0x123456789ABCULL,
        0xFFFFFFFFFFFFFFFFULL};

    for (Cmp op : ops) {
        for (std::uint64_t value : values) {
            Policy policy;
            policy.default_action = Action::KillProcess;
            policy.rules.push_back(
                Rule{77, Action::Allow, 0, {Condition{3, op, value}}});
            const VerifyReport report = verify(policy);
            check(report.ok(), "boundary enumeration matches policy");
        }
    }
}

} // namespace

int main() {
    test_prologue_guards();
    test_basic_actions();
    test_64bit_arguments_use_both_words();
    test_every_comparison_operator();
    test_first_match_and_and_conditions();
    test_size_limit();
    test_differential_verification();
    test_verify_all_operators_with_boundaries();

    std::cout << "checks=" << g_checks << " failures=" << g_failures << "\n";
    return g_failures == 0 ? 0 : 1;
}
