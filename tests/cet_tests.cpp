// Self-contained CET verifier test suite.  No external test framework is used:
// every scenario is driven through the public cet::CetMachine API and the
// process exits non-zero on the first failed expectation.
#include "cet/cet.h"

#include <iostream>
#include <string>
#include <vector>

namespace {

using cet::CetMachine;
using cet::ControlProtectionFault;
using cet::FaultCode;
using cet::FrameKind;
using cet::Instruction;

constexpr std::uint64_t kNormalBase = 0x0000'1000ULL;
constexpr std::uint64_t kNormalSize = 0x0000'4000ULL;
constexpr std::uint64_t kShadowBaseA = 0x0000'8000ULL;
constexpr std::uint64_t kShadowSize = 0x0000'2000ULL;
constexpr std::uint64_t kShadowBaseB = 0x0000'C000ULL;
constexpr std::uint64_t kRspInit = 0x0000'4F00ULL;
constexpr std::uint64_t kSspA = 0x0000'9000ULL;
constexpr std::uint64_t kSspB = 0x0000'D000ULL;

int g_tests = 0;
int g_failures = 0;

void check(bool condition, const std::string& label) {
    ++g_tests;
    if (!condition) {
        ++g_failures;
        std::cerr << "FAIL: " << label << '\n';
    }
}

CetMachine make_machine() {
    CetMachine machine;
    machine.map_region(kNormalBase, kNormalSize, false);
    machine.map_region(kShadowBaseA, kShadowSize, true);
    machine.map_region(kShadowBaseB, kShadowSize, true);
    machine.set_rsp(kRspInit);
    machine.set_ssp(kSspA);
    machine.set_rip(0x1000ULL);
    return machine;
}

void expect_fault(CetMachine& machine,
                  const Instruction& insn,
                  FaultCode expected,
                  const std::string& label) {
    bool raised = false;
    try {
        machine.step(insn);
    } catch (const ControlProtectionFault& fault) {
        raised = fault.code() == expected;
        if (!raised) {
            std::cerr << "  got fault " << cet::fault_name(fault.code())
                      << " in " << label << ": " << fault.what() << '\n';
        }
    }
    check(raised, label);
}

void test_happy_call_ret() {
    CetMachine machine = make_machine();
    machine.run({cet::make_call_direct(), cet::make_call_direct(),
                 cet::make_ret(), cet::make_ret()});
    check(machine.normal_depth() == 0U, "happy path empties normal stack");
    check(machine.shadow_depth() == 0U, "happy path empties shadow stack");
    check(machine.ssp() == kSspA, "happy path restores SSP");
    check(machine.rsp() == kRspInit, "happy path restores RSP");
}

void test_rop_tamper() {
    CetMachine machine = make_machine();
    machine.step(cet::make_call_direct());
    const std::uint64_t saved_rsp = machine.rsp();
    check(machine.normal_top() == machine.shadow_top(),
          "frames agree before tampering");
    machine.step(cet::make_store(saved_rsp, 0xDEAD'BEEFULL));
    check(machine.normal_top() != machine.shadow_top(),
          "tampered normal slot diverges from shadow slot");
    expect_fault(machine, cet::make_ret(),
                 FaultCode::ReturnAddressMismatch,
                 "ROP tamper triggers #CP on RET");
}

void test_plain_write_to_shadow_denied() {
    CetMachine machine = make_machine();
    expect_fault(machine, cet::make_store(kSspA - 8U, 0xCAFEULL),
                 FaultCode::ShadowStoreDenied,
                 "plain STORE on shadow page is rejected");
    expect_fault(machine, cet::make_load(kSspA - 8U),
                 FaultCode::ShadowLoadDenied,
                 "plain LOAD from shadow page is rejected");
    check(!machine.has_shadow_slot(kSspA - 8U),
          "denied store leaves shadow page untouched");
}

void test_wrss_is_allowed() {
    CetMachine machine = make_machine();
    machine.step(cet::make_wrss(kSspA - 8U, 0x1234ULL));
    check(machine.read_shadow(kSspA - 8U) == 0x1234ULL,
          "WRSS can write a shadow page");
    expect_fault(machine, cet::make_wrss(kNormalBase, 0x1ULL),
                 FaultCode::ShadowAccessOutsidePage,
                 "WRSS outside a shadow page is rejected");
}

void test_ibt_missing_endbr() {
    CetMachine machine = make_machine();
    machine.step(cet::make_jmp_indirect(0x4000ULL));
    check(machine.ibt_state() == cet::IbtState::WaitForEndbranch,
          "indirect JMP arms WAIT_FOR_ENDBRANCH");
    expect_fault(machine, cet::make_ret(), FaultCode::MissingEndBranch,
                 "indirect JMP landing without ENDBR64 is rejected");

    CetMachine machine2 = make_machine();
    machine2.step(cet::make_call_indirect(0x5000ULL));
    expect_fault(machine2, cet::make_call_direct(),
                 FaultCode::MissingEndBranch,
                 "indirect CALL landing without ENDBR64 is rejected");

    CetMachine good = make_machine();
    good.step(cet::make_jmp_indirect(0x4000ULL));
    good.step(cet::make_endbr64());
    check(good.ibt_state() == cet::IbtState::Idle,
          "ENDBR64 clears the IBT wait state");
    good.step(cet::make_endbr64());
    check(true, "ENDBR64 in IDLE is a NOP");
}

void test_token_alignment_and_forgery() {
    CetMachine machine = make_machine();
    expect_fault(machine, cet::make_rstorssp(kSspA - 4U),
                 FaultCode::MisalignedToken,
                 "RSTORSSP rejects a misaligned token address");

    const std::uint64_t forged_slot = kSspA - 0x40U;
    machine.step(cet::make_wrss(forged_slot, kSspB | 0x2ULL));
    check(machine.has_shadow_slot(forged_slot),
          "forged word is physically present in the shadow page");
    expect_fault(machine, cet::make_rstorssp(forged_slot),
                 FaultCode::TokenForged,
                 "unregistered forged token is rejected");
}

void test_token_mode_and_busy() {
    CetMachine machine = make_machine();
    const std::uint64_t token_slot = kSspA - 0x80U;
    machine.issue_restore_token(token_slot, kSspB, 1U);

    machine.step(cet::make_wrss(token_slot, kSspB));
    expect_fault(machine, cet::make_rstorssp(token_slot),
                 FaultCode::TokenModeBitClear,
                 "token with 64-bit mode bit clear is rejected");

    machine.step(cet::make_wrss(token_slot, kSspB | 0x2ULL));
    machine.step(cet::make_rstorssp(token_slot));
    check(machine.ssp() == kSspB, "valid RSTORSSP switches SSP");
    check((machine.read_shadow(token_slot) & 0x1ULL) == 0x1ULL,
          "RSTORSSP atomically sets the busy flag");
    expect_fault(machine, cet::make_rstorssp(token_slot),
                 FaultCode::TokenBusy,
                 "busy token replay is rejected");
}

void test_dual_active_same_shadow_stack() {
    CetMachine machine = make_machine();
    machine.issue_restore_token(kSspA - 0x80U, kSspA, 1U);
    machine.issue_restore_token(kSspA - 0x90U, kSspB, 2U);
    machine.step(cet::make_rstorssp(kSspA - 0x90U));
    machine.step(cet::make_rstorssp(kSspA - 0x80U));
    // A second, distinct non-busy token still targets the now-active SSP B.
    machine.issue_restore_token(kSspA - 0xA0U, kSspB, 2U);
    expect_fault(machine, cet::make_rstorssp(kSspA - 0xA0U),
                 FaultCode::TokenReplay,
                 "re-activating a live shadow stack is rejected");
}

void test_incssp_overreach() {
    CetMachine machine = make_machine();
    machine.set_domain(7U);
    machine.step(cet::make_call_direct());
    machine.set_domain(8U);
    machine.step(cet::make_call_direct());
    expect_fault(machine, cet::make_incssp(2U),
                 FaultCode::IncsspOverreach,
                 "INCSSP cannot skip another domain's return frame");

    CetMachine same_domain = make_machine();
    same_domain.set_domain(3U);
    same_domain.step(cet::make_call_direct());
    same_domain.step(cet::make_call_direct());
    same_domain.step(cet::make_incssp(2U));
    check(same_domain.shadow_depth() == 0U,
          "same-domain INCSSP unwinds matching frames");

    CetMachine boundary = make_machine();
    const std::uint64_t token_slot = kSspA - 0x80U;
    boundary.issue_restore_token(token_slot, kSspB, 1U);
    boundary.step(cet::make_rstorssp(token_slot));
    boundary.step(cet::make_saveprev_ssp());
    expect_fault(boundary, cet::make_incssp(1U),
                 FaultCode::IncsspOverreach,
                 "INCSSP cannot skip a SAVEPREVSSP boundary token");
}

void test_full_coroutine_switch() {
    CetMachine machine = make_machine();
    const std::uint64_t token_slot = kSspA - 0x80U;
    machine.set_domain(1U);
    machine.step(cet::make_call_direct());
    machine.issue_restore_token(token_slot, kSspB, 2U);

    machine.step(cet::make_rstorssp(token_slot));
    check(machine.ssp() == kSspB, "coroutine switch lands on shadow stack B");
    check(machine.domain() == 2U, "RSTORSSP restores the owner domain");
    machine.step(cet::make_saveprev_ssp());
    check(machine.shadow_top_kind() == FrameKind::PrevSspToken,
          "SAVEPREVSSP pushes a busy previous-SSP token");
    check((machine.shadow_top() & 0x3ULL) == 0x3ULL,
          "previous-SSP token carries busy + 64-bit flags");

    machine.set_domain(2U);
    machine.step(cet::make_call_direct());
    machine.step(cet::make_ret());
    const std::uint64_t back_slot = kSspB - 0x40U;
    machine.issue_restore_token(back_slot, kSspA, 1U);
    machine.step(cet::make_rstorssp(back_slot));
    check(machine.ssp() == kSspA, "return switch restores SSP A");
    check(machine.shadow_depth() == 1U, "stack A frame is still protected");
    machine.step(cet::make_ret());
    check(machine.shadow_depth() == 0U, "coroutine RET validates stack A");
}

void test_saveprev_without_rstorssp() {
    CetMachine machine = make_machine();
    expect_fault(machine, cet::make_saveprev_ssp(),
                 FaultCode::NoPreviousShadowStack,
                 "SAVEPREVSSP without RSTORSSP is rejected");
}

void test_setjmp_longjmp() {
    CetMachine machine = make_machine();
    const std::uint64_t jmp_ssp_slot = kNormalBase + 0x100U;
    const std::uint64_t longjmp_token = kSspA - 0x80U;
    const std::uint64_t saved_rsp = kRspInit - 0x40U;

    machine.step(cet::make_rdssp(jmp_ssp_slot));
    check(machine.read_normal(jmp_ssp_slot) == kSspA,
          "RDSSP records SSP into the jmp_buf");
    machine.step(cet::make_call_direct());

    machine.issue_restore_token(longjmp_token, kSspA, 0U);
    machine.step(cet::make_rstorssp(longjmp_token));
    machine.step(cet::make_set_rsp(saved_rsp));
    check(machine.ssp() == kSspA, "longjmp restores the recorded SSP");
    check(machine.rsp() == saved_rsp, "longjmp restores RSP from normal memory");
}

void test_verify_report_api() {
    CetMachine machine = make_machine();
    const std::vector<Instruction> good = {
        cet::make_call_direct(), cet::make_ret()};
    const cet::VerificationReport ok_report = machine.verify(good);
    check(ok_report.ok, "verify() reports success for a clean stream");

    CetMachine victim = make_machine();
    victim.step(cet::make_call_direct());
    victim.step(cet::make_store(victim.rsp(), 0xBADULL));
    const std::vector<Instruction> bad = {cet::make_ret()};
    const cet::VerificationReport bad_report = victim.verify(bad);
    check(!bad_report.ok &&
              bad_report.fault == FaultCode::ReturnAddressMismatch,
          "verify() captures a #CP without throwing");
}

} // namespace

int main() {
    test_happy_call_ret();
    test_rop_tamper();
    test_plain_write_to_shadow_denied();
    test_wrss_is_allowed();
    test_ibt_missing_endbr();
    test_token_alignment_and_forgery();
    test_token_mode_and_busy();
    test_dual_active_same_shadow_stack();
    test_incssp_overreach();
    test_full_coroutine_switch();
    test_saveprev_without_rstorssp();
    test_setjmp_longjmp();
    test_verify_report_api();

    std::cout << "ran " << g_tests << " checks, " << g_failures
              << " failures\n";
    return g_failures == 0 ? 0 : 1;
}
