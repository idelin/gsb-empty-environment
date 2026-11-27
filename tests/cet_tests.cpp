#include "cet/cet.hpp"

#include <atomic>
#include <cstdio>
#include <functional>
#include <thread>

namespace {

using cet::Address;
using cet::CetMachine;
using cet::Cpl;
using cet::Opcode;
using cet::PageKind;
using cet::RestoreToken;
using cet::Result;
using cet::Status;
using cet::ThreadState;
using cet::TrackerState;

int failures = 0;

void expect(
    bool condition,
    const char* expression,
    const char* test_name,
    int line) {
    if (!condition) {
        ++failures;
        std::fprintf(
            stderr,
            "FAIL %s:%d [%s] %s\n",
            test_name,
            line,
            expression,
            "check failed");
    }
}

void expect_status(
    Result result,
    Status expected,
    const char* expression,
    const char* test_name,
    int line) {
    if (result.code != expected) {
        ++failures;
        std::fprintf(
            stderr,
            "FAIL %s:%d [%s] expected=%s actual=%s\n",
            test_name,
            line,
            expression,
            cet::status_message(expected),
            cet::status_message(result.code));
    }
}

#define EXPECT(condition) expect((condition), #condition, __func__, __LINE__)
#define EXPECT_STATUS(result, expected) \
    expect_status((result), (expected), #result, __func__, __LINE__)

constexpr Address kUserStackA = 0x0000000010000000ULL;
constexpr Address kUserStackB = 0x0000000020000000ULL;
constexpr Address kUserTokenPage = 0x0000000030000000ULL;
constexpr Address kUserNormalPage = 0x0000000040000000ULL;
constexpr Address kUserCodeA = 0x0000000000401000ULL;
constexpr Address kUserCodeB = 0x0000000000402000ULL;
constexpr Address kKernelStack = 0xffff888000000000ULL;

void map_pages(
    CetMachine& machine,
    Address base,
    Address count,
    PageKind kind) {
    for (Address index = 0ULL; index < count; ++index) {
        EXPECT_STATUS(machine.map_page(base + index * cet::kPageSize, kind), Status::Ok);
    }
}

void attach_user_stack(
    CetMachine& machine,
    ThreadState& thread,
    Address base = kUserStackA,
    Address pages = 2ULL) {
    EXPECT_STATUS(
        machine.attach_thread(
            thread,
            Cpl::Ring3,
            base,
            base + pages * cet::kPageSize),
        Status::Ok);
}

void test_call_ret_and_mismatch() {
    CetMachine machine;
    map_pages(machine, kUserStackA, 2ULL, PageKind::ShadowStackReadOnly);

    ThreadState thread;
    attach_user_stack(machine, thread);

    EXPECT(thread.tracker() == TrackerState::Idle);
    EXPECT_STATUS(machine.direct_call(thread, kUserCodeA, kUserCodeB), Status::Ok);
    const Address after_call = thread.ssp();
    EXPECT(after_call == kUserStackA + 2ULL * cet::kPageSize - sizeof(Address));

    EXPECT_STATUS(machine.ret(thread, kUserCodeB), Status::ReturnAddressMismatch);
    EXPECT(thread.ssp() == after_call);

    EXPECT_STATUS(machine.ret(thread, kUserCodeA), Status::Ok);
    EXPECT(thread.ssp() == kUserStackA + 2ULL * cet::kPageSize);
    EXPECT_STATUS(machine.ret(thread, kUserCodeA), Status::ShadowStackEmpty);
}

void test_incsspq_limits() {
    CetMachine machine;
    map_pages(machine, kUserStackA, 2ULL, PageKind::ShadowStackReadOnly);

    ThreadState thread;
    attach_user_stack(machine, thread);

    EXPECT_STATUS(machine.direct_call(thread, kUserCodeA, kUserCodeB), Status::Ok);
    EXPECT_STATUS(machine.direct_call(thread, kUserCodeB, kUserCodeA), Status::Ok);
    EXPECT_STATUS(machine.direct_call(thread, kUserCodeA, kUserCodeB), Status::Ok);

    const Address before_limit = thread.ssp();
    EXPECT_STATUS(machine.incsspq(thread, 256U), Status::IncsspqLimitExceeded);
    EXPECT(thread.ssp() == before_limit);

    EXPECT_STATUS(machine.incsspq(thread, 2U), Status::Ok);
    EXPECT_STATUS(machine.ret(thread, kUserCodeA), Status::Ok);
    EXPECT_STATUS(machine.ret(thread, kUserCodeA), Status::ShadowStackEmpty);

    EXPECT_STATUS(machine.direct_call(thread, kUserCodeA, kUserCodeB), Status::Ok);
    EXPECT_STATUS(machine.incsspq(thread, 2U), Status::IncsspqFrameExceeded);
}

void test_ibt_and_exception_boundary() {
    CetMachine machine;
    map_pages(machine, kUserStackA, 2ULL, PageKind::ShadowStackReadOnly);

    ThreadState thread;
    attach_user_stack(machine, thread);

    EXPECT_STATUS(machine.indirect_call(thread, kUserCodeA, kUserCodeB), Status::Ok);
    EXPECT(thread.tracker() == TrackerState::WaitForEndbranch);
    EXPECT_STATUS(machine.fetch(thread, Opcode::Other), Status::IbtMissingEndbr64);
    EXPECT(thread.tracker() == TrackerState::WaitForEndbranch);
    EXPECT_STATUS(machine.fetch(thread, Opcode::Endbr64), Status::Ok);
    EXPECT(thread.tracker() == TrackerState::Idle);
    EXPECT_STATUS(machine.ret(thread, kUserCodeA), Status::Ok);

    EXPECT_STATUS(machine.indirect_jump(thread, kUserCodeB), Status::Ok);
    EXPECT(thread.tracker() == TrackerState::WaitForEndbranch);
    EXPECT_STATUS(machine.conditional_jump(thread, kUserCodeA), Status::IbtMissingEndbr64);
    EXPECT_STATUS(machine.direct_jump(thread, kUserCodeA), Status::IbtMissingEndbr64);

    EXPECT_STATUS(machine.enter_exception(thread), Status::Ok);
    EXPECT(thread.tracker() == TrackerState::Idle);
    EXPECT_STATUS(machine.fetch(thread, Opcode::Other), Status::Ok);
    EXPECT_STATUS(machine.direct_jump(thread, kUserCodeA), Status::Ok);
    EXPECT_STATUS(machine.leave_exception(thread), Status::Ok);
    EXPECT(thread.tracker() == TrackerState::WaitForEndbranch);
    EXPECT_STATUS(machine.fetch(thread, Opcode::Endbr64), Status::Ok);
    EXPECT(thread.tracker() == TrackerState::Idle);
    EXPECT_STATUS(machine.leave_exception(thread), Status::NoExceptionFrame);

    EXPECT_STATUS(machine.direct_call(thread, kUserCodeA, kUserCodeB), Status::Ok);
    EXPECT(thread.tracker() == TrackerState::Idle);
    EXPECT_STATUS(machine.ret(thread, kUserCodeA), Status::Ok);
}

void test_valid_token_restore_and_busy_reuse() {
    CetMachine machine;
    map_pages(machine, kUserStackA, 1ULL, PageKind::ShadowStackReadOnly);
    map_pages(machine, kUserStackB, 1ULL, PageKind::ShadowStackReadOnly);

    const Address token_address = kUserStackB + 0x80ULL;
    EXPECT_STATUS(
        machine.place_restore_token(
            token_address,
            cet::make_restore_token(token_address, Cpl::Ring3)),
        Status::Ok);

    ThreadState first;
    ThreadState second;
    attach_user_stack(machine, first, kUserStackA, 1ULL);
    attach_user_stack(machine, second, kUserStackA, 1ULL);

    EXPECT_STATUS(machine.rstorssp(first, token_address), Status::Ok);
    EXPECT(first.ssp() == token_address + sizeof(Address));
    const auto busy = machine.read_restore_token(token_address);
    EXPECT(busy.has_value() && busy->busy);

    EXPECT_STATUS(machine.rstorssp(second, token_address), Status::TokenBusy);
    EXPECT(second.ssp() == kUserStackA + cet::kPageSize);
}

void test_token_race_has_single_winner() {
    CetMachine machine;
    map_pages(machine, kUserStackA, 1ULL, PageKind::ShadowStackReadOnly);
    map_pages(machine, kUserStackB, 1ULL, PageKind::ShadowStackReadOnly);

    const Address token_address = kUserStackB + 0x180ULL;
    EXPECT_STATUS(
        machine.place_restore_token(
            token_address,
            cet::make_restore_token(token_address, Cpl::Ring3)),
        Status::Ok);

    ThreadState first;
    ThreadState second;
    attach_user_stack(machine, first, kUserStackA, 1ULL);
    attach_user_stack(machine, second, kUserStackA, 1ULL);

    std::atomic<bool> start{false};
    std::atomic<int> successes{0};
    std::atomic<int> busy{0};
    std::atomic<int> unexpected{0};

    auto contender = [&](ThreadState& thread) {
        while (!start.load(std::memory_order_acquire)) {
            std::this_thread::yield();
        }
        const Result response = machine.rstorssp(thread, token_address);
        if (response.code == Status::Ok) {
            successes.fetch_add(1, std::memory_order_relaxed);
        } else if (response.code == Status::TokenBusy) {
            busy.fetch_add(1, std::memory_order_relaxed);
        } else {
            unexpected.fetch_add(1, std::memory_order_relaxed);
        }
    };

    std::thread one(contender, std::ref(first));
    std::thread two(contender, std::ref(second));
    start.store(true, std::memory_order_release);
    one.join();
    two.join();

    EXPECT(successes.load(std::memory_order_relaxed) == 1);
    EXPECT(busy.load(std::memory_order_relaxed) == 1);
    EXPECT(unexpected.load(std::memory_order_relaxed) == 0);
    const auto token = machine.read_restore_token(token_address);
    EXPECT(token.has_value() && token->busy);
}

void test_forged_tokens_and_page_rules() {
    CetMachine machine;
    map_pages(machine, kUserStackA, 1ULL, PageKind::ShadowStackReadOnly);
    map_pages(machine, kUserTokenPage, 2ULL, PageKind::ShadowStackReadOnly);
    map_pages(machine, kUserNormalPage, 1ULL, PageKind::NormalWritable);

    ThreadState thread;
    attach_user_stack(machine, thread, kUserStackA, 1ULL);

    const Address token_address = kUserTokenPage + 0x200ULL;
    EXPECT_STATUS(
        machine.place_restore_token(
            token_address,
            cet::make_restore_token(token_address + 8ULL, Cpl::Ring3)),
        Status::Ok);
    EXPECT_STATUS(machine.rstorssp(thread, token_address), Status::TokenAddressMismatch);

    EXPECT_STATUS(
        machine.place_restore_token(
            token_address,
            cet::make_restore_token(token_address, Cpl::Ring0)),
        Status::Ok);
    EXPECT_STATUS(machine.rstorssp(thread, token_address), Status::TokenModeMismatch);

    const RestoreToken valid =
        cet::make_restore_token(token_address, Cpl::Ring3, false);
    EXPECT_STATUS(
        machine.write_shadow_word(
            token_address,
            cet::encode_restore_token(valid) | cet::kTokenReservedBit),
        Status::Ok);
    EXPECT_STATUS(machine.rstorssp(thread, token_address), Status::InvalidRestoreToken);

    EXPECT_STATUS(
        machine.place_restore_token(
            kUserNormalPage + 0x40ULL,
            cet::make_restore_token(kUserNormalPage + 0x40ULL, Cpl::Ring3)),
        Status::WrongPageKind);
    EXPECT_STATUS(
        machine.write_normal_word(
            kUserNormalPage + 0x40ULL,
            cet::encode_restore_token(
                cet::make_restore_token(kUserNormalPage + 0x40ULL, Cpl::Ring3))),
        Status::Ok);
    EXPECT_STATUS(
        machine.rstorssp(thread, kUserNormalPage + 0x40ULL),
        Status::WrongPageKind);

    EXPECT_STATUS(machine.rstorssp(thread, token_address + 4ULL), Status::MisalignedAddress);
}

void test_cross_privilege_and_normal_page_ssp() {
    CetMachine machine;
    map_pages(machine, kUserStackA, 1ULL, PageKind::ShadowStackReadOnly);
    map_pages(machine, kUserNormalPage, 1ULL, PageKind::NormalWritable);
    map_pages(machine, kKernelStack, 1ULL, PageKind::ShadowStackReadOnly);

    ThreadState user_thread;
    attach_user_stack(machine, user_thread, kUserStackA, 1ULL);

    EXPECT_STATUS(
        machine.attach_thread(
            user_thread,
            Cpl::Ring3,
            kKernelStack,
            kKernelStack + cet::kPageSize),
        Status::CrossPrivilegeAddress);
    EXPECT_STATUS(
        machine.attach_thread(
            user_thread,
            Cpl::Ring3,
            kUserNormalPage,
            kUserNormalPage + cet::kPageSize),
        Status::WrongPageKind);

    const Address kernel_token = kKernelStack + 0x100ULL;
    EXPECT_STATUS(
        machine.place_restore_token(
            kernel_token,
            cet::make_restore_token(kernel_token, Cpl::Ring0)),
        Status::Ok);
    EXPECT_STATUS(machine.rstorssp(user_thread, kernel_token), Status::CrossPrivilegeAddress);
    const auto untouched = machine.read_restore_token(kernel_token);
    EXPECT(untouched.has_value() && !untouched->busy);
    EXPECT_STATUS(
        machine.switch_shadow_stack(
            user_thread,
            kernel_token,
            Cpl::Ring0,
            cet::SwitchReason::SignalReturn),
        Status::CrossPrivilegeAddress);

    ThreadState kernel_thread;
    EXPECT_STATUS(
        machine.attach_thread(
            kernel_thread,
            Cpl::Ring0,
            kKernelStack,
            kKernelStack + cet::kPageSize),
        Status::Ok);
    EXPECT_STATUS(machine.rstorssp(kernel_thread, kernel_token), Status::Ok);
    EXPECT(kernel_thread.ssp() == kernel_token + sizeof(Address));

    const Address user_token = kUserStackA + 0x380ULL;
    EXPECT_STATUS(
        machine.place_restore_token(
            user_token,
            cet::make_restore_token(user_token, Cpl::Ring3)),
        Status::Ok);
    EXPECT_STATUS(
        machine.signal_return_to_user(kernel_thread, user_token),
        Status::Ok);
    EXPECT(kernel_thread.cpl() == Cpl::Ring3);
    EXPECT(kernel_thread.ssp() == user_token + sizeof(Address));
}

void test_normal_page_after_token_is_rejected() {
    CetMachine machine;
    map_pages(machine, kUserStackA, 1ULL, PageKind::ShadowStackReadOnly);
    map_pages(machine, kUserTokenPage, 1ULL, PageKind::ShadowStackReadOnly);
    map_pages(machine, kUserTokenPage + cet::kPageSize, 1ULL, PageKind::NormalWritable);

    const Address token_address = kUserTokenPage + cet::kPageSize - sizeof(Address);
    EXPECT_STATUS(
        machine.place_restore_token(
            token_address,
            cet::make_restore_token(token_address, Cpl::Ring3)),
        Status::Ok);

    ThreadState thread;
    attach_user_stack(machine, thread, kUserStackA, 1ULL);
    EXPECT_STATUS(machine.rstorssp(thread, token_address), Status::WrongPageKind);
    const auto token = machine.read_restore_token(token_address);
    EXPECT(token.has_value() && !token->busy);
}

void test_saveprevssp_round_trip() {
    CetMachine machine;
    map_pages(machine, kUserStackA, 1ULL, PageKind::ShadowStackReadOnly);

    ThreadState thread;
    attach_user_stack(machine, thread, kUserStackA, 1ULL);

    const Address token_address =
        kUserStackA + cet::kPageSize - sizeof(Address);
    EXPECT_STATUS(machine.saveprevssp(thread, token_address), Status::Ok);
    EXPECT(thread.ssp() == token_address);
    const auto saved = machine.read_restore_token(token_address);
    EXPECT(saved.has_value());
    if (saved.has_value()) {
        EXPECT(saved->address == token_address);
        EXPECT(saved->mode == Cpl::Ring3);
        EXPECT(!saved->busy);
    }

    EXPECT_STATUS(machine.rstorssp(thread, token_address), Status::Ok);
    EXPECT(thread.ssp() == kUserStackA + cet::kPageSize);
    const auto restored = machine.read_restore_token(token_address);
    EXPECT(restored.has_value() && restored->busy);
    EXPECT_STATUS(machine.saveprevssp(thread, token_address), Status::TokenBusy);
}

void test_cross_privilege_code_targets() {
    CetMachine machine;
    map_pages(machine, kUserStackA, 1ULL, PageKind::ShadowStackReadOnly);

    ThreadState thread;
    attach_user_stack(machine, thread, kUserStackA, 1ULL);
    EXPECT_STATUS(
        machine.direct_call(thread, kUserCodeA, kKernelStack + 0x1000ULL),
        Status::CrossPrivilegeAddress);
    EXPECT_STATUS(
        machine.direct_call(thread, kKernelStack + 0x1000ULL, kUserCodeA),
        Status::CrossPrivilegeAddress);
}

void test_canonical_address_edges() {
    CetMachine machine;
    map_pages(machine, kUserStackA, 1ULL, PageKind::ShadowStackReadOnly);

    ThreadState thread;
    attach_user_stack(machine, thread, kUserStackA, 1ULL);
    EXPECT_STATUS(
        machine.direct_jump(thread, 0x0000800000000000ULL),
        Status::NonCanonicalAddress);
    EXPECT_STATUS(
        machine.map_page(0xfffffffffffff000ULL, PageKind::ShadowStackReadOnly),
        Status::InvalidArgument);
}

}  // namespace

int main() {
    test_call_ret_and_mismatch();
    test_incsspq_limits();
    test_ibt_and_exception_boundary();
    test_valid_token_restore_and_busy_reuse();
    test_token_race_has_single_winner();
    test_forged_tokens_and_page_rules();
    test_cross_privilege_and_normal_page_ssp();
    test_normal_page_after_token_is_rejected();
    test_saveprevssp_round_trip();
    test_cross_privilege_code_targets();
    test_canonical_address_edges();

    if (failures != 0) {
        std::fprintf(stderr, "%d test failure(s)\n", failures);
        return 1;
    }

    std::printf("all CET state-machine tests passed\n");
    return 0;
}
