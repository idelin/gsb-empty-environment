#include "netsec.hpp"

#include <iostream>

using namespace netsec;

int main() {
    Policy policy;
    policy.default_action = Action::KillProcess;

    // Block filesystem-open family with EPERM.
    policy.rules.push_back(Rule{2, Action::Errno, 1, {}});   // open
    policy.rules.push_back(Rule{257, Action::Errno, 1, {}}); // openat

    // Allow write only on the standard streams (fd <= 2).
    policy.rules.push_back(
        Rule{1, Action::Allow, 0, {Condition{0, Cmp::Le, 2}}});

    // Allow mmap only with PROT_NONE..PROT_GROWSDOWN-style mask <= 7.
    policy.rules.push_back(
        Rule{9, Action::Allow, 0, {Condition{2, Cmp::Le, 7}}});

    // 64-bit address gate: both 32-bit words are inspected.
    policy.rules.push_back(
        Rule{10, Action::Allow, 0, {Condition{0, Cmp::Lt, 0x100000000ULL}}});

    policy.rules.push_back(Rule{231, Action::Allow, 0, {}}); // exit_group

    const auto program = compile(policy);
    std::cout << "compiled " << program.size() << " BPF instructions\n";

    const VerifyReport report = verify(policy);
    std::cout << "verify: cases=" << report.cases_run
              << " mismatches=" << report.mismatches
              << " size_ok=" << report.size_limit_ok
              << " jumps_ok=" << report.jump_range_ok << "\n";

    const BpfInterpreter interpreter;
    const auto describe = [&](const SeccompData& data) {
        BpfResult result = interpreter.run(program, data);
        std::cout << "arch=0x" << std::hex << data.arch << std::dec
                  << " nr=" << data.nr << " -> verdict="
                  << static_cast<int>(result.verdict)
                  << " raw=0x" << std::hex << result.raw << std::dec << "\n";
    };

    SeccompData allowed = SeccompData{};
    allowed.arch = AUDIT_ARCH_X86_64;
    allowed.nr = 1;
    allowed.args[0] = 2;
    describe(allowed);

    describe(SeccompData{2, AUDIT_ARCH_I386, 0, {}});
    describe(SeccompData{static_cast<int>(231 | X32_SYSCALL_BIT),
                         AUDIT_ARCH_X86_64, 0, {}});

    std::cout << "\nfirst 12 instructions:\n";
    const std::string text = disassemble(program);
    std::size_t lines = 0;
    for (char ch : text) {
        std::cout.put(ch);
        if (ch == '\n' && ++lines == 12) {
            break;
        }
    }

    return report.ok() ? 0 : 1;
}
