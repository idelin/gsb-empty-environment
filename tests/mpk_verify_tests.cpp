// Zero-dependency unit tests for the MPK/PKU sandbox verifier.
// Covers hidden in-instruction opcodes, XRSTOR bypasses, writable code-page
// injection, PKEY 0 / host-key leaks, trampoline exclusion, jump targets and
// the PKRU entry/exit state machine.

#include <cstdint>
#include <iostream>
#include <vector>

#include "mpk_verify/mpk_verify.hpp"

namespace {

using mpkverify::byte_t;
using mpkverify::PkruTransition;
using mpkverify::VerifyConfig;
using mpkverify::Violation;
using mpkverify::ViolationKind;

int g_checks = 0;
int g_failures = 0;

#define CHECK(cond)                                                     \
    do {                                                                \
        ++g_checks;                                                     \
        if (!(cond)) {                                                  \
            ++g_failures;                                               \
            std::cerr << "FAIL " << __FILE__ << ':' << __LINE__ << ' '  \
                      << #cond << '\n';                                 \
        }                                                               \
    } while (0)

std::vector<byte_t> bytes(std::initializer_list<int> values) {
    std::vector<byte_t> out;
    out.reserve(values.size());
    for (int value : values) {
        out.push_back(static_cast<byte_t>(value));
    }
    return out;
}

const Violation* find_violation(const std::vector<Violation>& violations,
                                ViolationKind kind) {
    for (const Violation& violation : violations) {
        if (violation.kind == kind) {
            return &violation;
        }
    }
    return nullptr;
}

const Violation* find_violation_at(const std::vector<Violation>& violations,
                                   ViolationKind kind,
                                   std::int64_t offset) {
    for (const Violation& violation : violations) {
        if (violation.kind == kind && violation.segment_offset == offset) {
            return &violation;
        }
    }
    return nullptr;
}

constexpr std::uint64_t kCodeBase = 0x10000ULL;
constexpr std::uint64_t kDataBase = 0x20000ULL;
constexpr std::uint64_t kHostBase = 0x30000ULL;
constexpr std::uint64_t kTrampStart = 0x40000ULL;
constexpr std::uint64_t kTrampEnd = 0x40100ULL;

mpkverify::PageMapping code_page(std::uint64_t base, int pkey,
                                 bool writable = false) {
    return mpkverify::PageMapping{base, /*readable=*/true, writable,
                                  /*executable=*/true, pkey};
}

mpkverify::PageMapping data_page(std::uint64_t base, int pkey) {
    return mpkverify::PageMapping{base, /*readable=*/true, /*writable=*/true,
                                  /*executable=*/false, pkey};
}

// Deny (AD=WD=1) key 0 and host key 2; plugin key 1 stays accessible.
constexpr std::uint32_t kGuestPkru = 0x00000033u;

VerifyConfig base_clean_config() {
    VerifyConfig cfg;
    cfg.trampolines.push_back(
        mpkverify::TrampolineRange{kTrampStart, kTrampEnd});
    cfg.page_table.push_back(code_page(kCodeBase, 1));
    cfg.page_table.push_back(data_page(kDataBase, 1));
    cfg.host_protected_pkeys.push_back(2);
    cfg.host_baseline_pkru = 0;
    cfg.transitions.push_back(
        PkruTransition{PkruTransition::Type::Entry, kGuestPkru, kTrampStart});
    cfg.transitions.push_back(PkruTransition{
        PkruTransition::Type::Exit, 0, kTrampStart + 8u});
    return cfg;
}

// 1. WRPKRU hidden inside the immediate of a benign 10-byte MOV RAX, imm64
//    (48 B8 <imm64>). The scanner must still flag the embedded offset 2.
void test_hidden_wrpkru_inside_instruction() {
    VerifyConfig cfg = base_clean_config();
    mpkverify::CodeSegment segment;
    segment.base_address = kCodeBase;
    segment.bytes = bytes({
        0x48, 0xB8,                    // mov rax, imm64
        0x0F, 0x01, 0xEF, 0x90, 0x90,  // hidden 0F 01 EF at offset 2
        0x90, 0x90, 0x90,
        0xC3                           // ret
    });
    cfg.segments.push_back(segment);

    mpkverify::VerifyResult result = mpkverify::verify_sandbox(cfg);
    CHECK(!result.ok);
    const Violation* violation =
        find_violation_at(result.violations, ViolationKind::HiddenWrPkru, 2);
    CHECK(violation != nullptr);
    if (violation != nullptr) {
        CHECK(violation->witness_len >= 3);
        CHECK(violation->witness[0] == 0x0F);
        CHECK(violation->witness[1] == 0x01);
        CHECK(violation->witness[2] == 0xEF);
        CHECK(violation->address == kCodeBase + 2u);
    }
}

// 2. Head-aligned WRPKRU is rejected; plain benign code passes.
void test_plain_wrpkru_and_benign_code() {
    VerifyConfig cfg = base_clean_config();
    mpkverify::CodeSegment segment;
    segment.base_address = kCodeBase;
    segment.bytes = bytes({0x90, 0x90, 0xC3});  // nop; nop; ret
    cfg.segments.push_back(segment);
    CHECK(mpkverify::verify_sandbox(cfg).ok);

    VerifyConfig bad = base_clean_config();
    mpkverify::CodeSegment evil;
    evil.base_address = kCodeBase;
    evil.bytes = bytes({0x0F, 0x01, 0xEF, 0xC3});  // wrpku; ret
    bad.segments.push_back(evil);
    mpkverify::VerifyResult result = mpkverify::verify_sandbox(bad);
    CHECK(!result.ok);
    CHECK(find_violation_at(result.violations,
                            ViolationKind::HiddenWrPkru, 0) != nullptr);
}

// 3. Opcodes inside the trusted trampoline range are whitelisted; bytes just
//    outside a narrower range are still caught.
void test_trampoline_exclusion() {
    VerifyConfig cfg = base_clean_config();
    mpkverify::CodeSegment segment;
    segment.base_address = kTrampStart;
    segment.bytes = bytes({0x0F, 0x01, 0xEF, 0x0F, 0x05, 0xCD, 0x80});
    cfg.segments.push_back(segment);
    CHECK(mpkverify::verify_sandbox(cfg).ok);

    VerifyConfig cfg2 = base_clean_config();
    cfg2.trampolines.clear();
    cfg2.trampolines.push_back(
        mpkverify::TrampolineRange{kTrampStart, kTrampStart + 4u});
    mpkverify::CodeSegment segment2;
    segment2.base_address = kTrampStart;
    segment2.bytes = bytes({0x90, 0x90, 0x90, 0x90,
                            0x0F, 0x01, 0xEF});  // WRPKRU at offset 4
    cfg2.segments.push_back(segment2);
    mpkverify::VerifyResult result2 = mpkverify::verify_sandbox(cfg2);
    CHECK(find_violation_at(result2.violations,
                            ViolationKind::HiddenWrPkru, 4) != nullptr);
}

// 4. XRSTOR (0F AE /5) and XRSTORS (0F C7 /3), including hidden bytes;
//    sibling /digit encodings must not be misclassified.
void test_xrstor_family() {
    VerifyConfig cfg = base_clean_config();
    mpkverify::CodeSegment segment;
    segment.base_address = kCodeBase;
    segment.bytes = bytes({
        0x48, 0xB8,        // mov rax, imm64
        0x0F, 0xAE, 0x28,  // hidden XRSTOR /5
        0x0F, 0xC7, 0x18,  // hidden XRSTORS /3
        0x90, 0x90, 0x90,
        0x0F, 0xAE, 0x20,  // FXSAVE /0 -> allowed
        0x0F, 0xC7, 0x08,  // /1 -> allowed
        0xC3
    });
    cfg.segments.push_back(segment);
    mpkverify::VerifyResult result = mpkverify::verify_sandbox(cfg);
    CHECK(find_violation_at(result.violations, ViolationKind::Xrstor,
                            2) != nullptr);
    CHECK(find_violation_at(result.violations, ViolationKind::Xrstors,
                            5) != nullptr);
    CHECK(find_violation(result.violations, ViolationKind::Syscall) ==
          nullptr);
}

// 5. SYSCALL and INT 0x80 are rejected; other INT immediates are not.
void test_syscall_and_int80() {
    VerifyConfig cfg = base_clean_config();
    mpkverify::CodeSegment segment;
    segment.base_address = kCodeBase;
    segment.bytes = bytes({
        0x0F, 0x05,  // syscall
        0xCD, 0x80,  // int 0x80
        0xCD, 0x03,  // int 3 (two-byte form, not int 0x80)
        0xCC,        // int3
        0xC3
    });
    cfg.segments.push_back(segment);
    mpkverify::VerifyResult result = mpkverify::verify_sandbox(cfg);
    CHECK(find_violation_at(result.violations, ViolationKind::Syscall,
                            0) != nullptr);
    CHECK(find_violation_at(result.violations, ViolationKind::Int80,
                            2) != nullptr);
    int int80_count = 0;
    for (const Violation& violation : result.violations) {
        if (violation.kind == ViolationKind::Int80) {
            ++int80_count;
        }
    }
    CHECK(int80_count == 1);
}

// 6. Page-table checks: writable code page (self-injection), PKEY 0 leak,
//    host PKEY leak and out-of-range PKEY assignments.
void test_page_table_validation() {
    VerifyConfig cfg = base_clean_config();
    cfg.page_table.clear();
    cfg.page_table.push_back(code_page(kCodeBase, 1, /*writable=*/true));
    cfg.page_table.push_back(data_page(kDataBase, 0));
    cfg.page_table.push_back(data_page(kHostBase, 2));
    cfg.page_table.push_back(
        mpkverify::PageMapping{0x50000ULL, true, false, false, 16});
    mpkverify::CodeSegment segment;
    segment.base_address = kCodeBase;
    segment.bytes = bytes({0xC3});
    cfg.segments.push_back(segment);

    mpkverify::VerifyResult result = mpkverify::verify_sandbox(cfg);
    CHECK(find_violation(result.violations,
                         ViolationKind::CodePageWritable) != nullptr);
    CHECK(find_violation(result.violations,
                         ViolationKind::PageUsesPkey0) != nullptr);
    CHECK(find_violation(result.violations,
                         ViolationKind::PageUsesHostPkey) != nullptr);
    CHECK(find_violation(result.violations,
                         ViolationKind::InvalidPkey) != nullptr);
}

// 7. Control-flow targets must never reach PKEY 0 / host pages and must stay
//    executable; trampoline and valid plugin targets are accepted.
void test_jump_target_validation() {
    VerifyConfig cfg = base_clean_config();
    cfg.page_table.push_back(code_page(kHostBase, 2));
    cfg.page_table.push_back(data_page(0x60000ULL, 1));
    mpkverify::CodeSegment segment;
    segment.base_address = kCodeBase;
    segment.bytes = bytes({0xC3});
    segment.jump_targets = {
        kHostBase,        // host protected page
        0x70000ULL,       // unmapped / outside any segment
        0x60000ULL,       // non-executable data page
        kTrampStart + 2,  // trusted trampoline: allowed
        kCodeBase         // valid plugin RX page with PKEY 1
    };
    cfg.segments.push_back(segment);

    mpkverify::VerifyResult result = mpkverify::verify_sandbox(cfg);
    CHECK(find_violation(result.violations,
                         ViolationKind::JumpTargetHostPage) != nullptr);
    CHECK(find_violation(result.violations,
                         ViolationKind::JumpTargetUnmapped) != nullptr);
    CHECK(find_violation(result.violations,
                         ViolationKind::JumpTargetNonExecutable) != nullptr);
    CHECK(find_violation(result.violations,
                         ViolationKind::JumpTargetPkey0) == nullptr);
}

// 8. A code page backed by PKEY 0 triggers both page and entry-point leaks.
void test_pkey0_code_leak() {
    VerifyConfig cfg = base_clean_config();
    cfg.page_table.clear();
    cfg.page_table.push_back(code_page(kCodeBase, 0));
    cfg.host_protected_pkeys.clear();
    mpkverify::CodeSegment segment;
    segment.base_address = kCodeBase;
    segment.bytes = bytes({0xC3});
    segment.entry_points.push_back(kCodeBase);
    cfg.segments.push_back(segment);

    mpkverify::VerifyResult result = mpkverify::verify_sandbox(cfg);
    CHECK(find_violation(result.violations,
                         ViolationKind::PageUsesPkey0) != nullptr);
    CHECK(find_violation(result.violations,
                         ViolationKind::JumpTargetPkey0) != nullptr);
}

// 9. PKRU entry/exit state machine: mask correctness and ordering.
void test_pkru_state_machine() {
    VerifyConfig good = base_clean_config();
    mpkverify::CodeSegment segment;
    segment.base_address = kCodeBase;
    segment.bytes = bytes({0xC3});
    good.segments.push_back(segment);
    CHECK(mpkverify::verify_sandbox(good).ok);

    // Entry mask that forgets WD for host key 2.
    VerifyConfig bad_entry = base_clean_config();
    bad_entry.transitions[0].pkru = 0x00000011u;
    mpkverify::VerifyResult entry_result =
        mpkverify::verify_sandbox(bad_entry);
    CHECK(find_violation(entry_result.violations,
                         ViolationKind::BadEntryPkru) != nullptr);

    // Exit that fails to restore the host baseline.
    VerifyConfig bad_exit = base_clean_config();
    bad_exit.transitions[1].pkru = 0x00000033u;
    mpkverify::VerifyResult exit_result =
        mpkverify::verify_sandbox(bad_exit);
    CHECK(find_violation(exit_result.violations,
                         ViolationKind::BadExitPkru) != nullptr);

    // Two entries back-to-back violate HOST -> GUEST ordering.
    VerifyConfig bad_order = base_clean_config();
    bad_order.transitions.push_back(PkruTransition{
        PkruTransition::Type::Entry, kGuestPkru, kTrampStart + 16u});
    bad_order.transitions.push_back(PkruTransition{
        PkruTransition::Type::Entry, kGuestPkru, kTrampStart + 24u});
    mpkverify::VerifyResult order_result =
        mpkverify::verify_sandbox(bad_order);
    CHECK(find_violation(order_result.violations,
                         ViolationKind::TransitionOrdering) != nullptr);
}

// 10. End-to-end attack: a writable RX page plus hidden WRPKRU and a jump to a
//     host page is fully attributed with exact offsets and witnesses.
void test_end_to_end_attack_report() {
    VerifyConfig cfg = base_clean_config();
    cfg.page_table.clear();
    cfg.page_table.push_back(code_page(kCodeBase, 1, /*writable=*/true));
    cfg.page_table.push_back(code_page(kHostBase, 2));
    mpkverify::CodeSegment segment;
    segment.base_address = kCodeBase;
    segment.bytes = bytes({
        0x48, 0xB8, 0x0F, 0x01, 0xEF,  // mov rax, ... (hidden WRPKRU @2)
        0x90, 0x90, 0x90, 0x90, 0x90,
        0x0F, 0xAE, 0x28,              // XRSTOR /5 @10
        0xC3
    });
    segment.jump_targets.push_back(kHostBase);
    cfg.segments.push_back(segment);

    mpkverify::VerifyResult result = mpkverify::verify_sandbox(cfg);
    CHECK(!result.ok);
    CHECK(find_violation_at(result.violations, ViolationKind::HiddenWrPkru,
                            2) != nullptr);
    CHECK(find_violation_at(result.violations, ViolationKind::Xrstor,
                            10) != nullptr);
    CHECK(find_violation(result.violations,
                         ViolationKind::CodePageWritable) != nullptr);
    CHECK(find_violation(result.violations,
                         ViolationKind::JumpTargetHostPage) != nullptr);

    bool formatted_report_ok = false;
    for (const Violation& violation : result.violations) {
        std::string report = mpkverify::to_string(violation);
        if (violation.kind == ViolationKind::HiddenWrPkru &&
            report.find("HiddenWrPkru") != std::string::npos &&
            report.find("0f 01 ef") != std::string::npos &&
            report.find("offset=2") != std::string::npos) {
            formatted_report_ok = true;
        }
    }
    CHECK(formatted_report_ok);
}

}  // namespace

int main() {
    test_hidden_wrpkru_inside_instruction();
    test_plain_wrpkru_and_benign_code();
    test_trampoline_exclusion();
    test_xrstor_family();
    test_syscall_and_int80();
    test_page_table_validation();
    test_jump_target_validation();
    test_pkey0_code_leak();
    test_pkru_state_machine();
    test_end_to_end_attack_report();

    std::cout << g_checks - g_failures << '/' << g_checks
              << " checks passed\n";
    if (g_failures != 0) {
        std::cout << g_failures << " checks FAILED\n";
        return 1;
    }
    std::cout << "all MPK/PKU verifier tests passed\n";
    return 0;
}
