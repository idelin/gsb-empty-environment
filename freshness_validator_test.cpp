#include "netsec.hpp"

#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <limits>
#include <string>
#include <vector>

namespace {

using netsec::FreshnessValidator;
using netsec::RejectReason;

struct CheckFailure {
    std::string name;
    std::string detail;
};

std::vector<CheckFailure> g_failures;

void record_failure(const std::string& name, const std::string& detail) {
    g_failures.push_back({name, detail});
}

#define CHECK_TRUE(name, cond)                                                \
    do {                                                                      \
        if (!(cond)) {                                                        \
            record_failure((name), #cond " (line " +                          \
                                      std::to_string(__LINE__) + ")");        \
        }                                                                     \
    } while (0)

#define CHECK_EQ(name, actual, expected)                                      \
    do {                                                                      \
        auto actual_v = (actual);                                             \
        auto expected_v = (expected);                                         \
        if (!(actual_v == expected_v)) {                                      \
            record_failure((name), #actual " != " #expected                   \
                                      " (line " +                             \
                                      std::to_string(__LINE__) + ")");        \
        }                                                                     \
    } while (0)

constexpr std::uint8_t low_byte(std::uint64_t value) {
    return static_cast<std::uint8_t>(value & FreshnessValidator::kLowMask);
}

// T1 回绕边界：高位进位只在 MAC 通过后生效，MAC 失败不推进 L。
void test_wrap_boundary_and_mac_failure() {
    const std::string name = "T1 wrap boundary / MAC failure no advance";
    FreshnessValidator validator(/*initial=*/250, /*window=*/256);

    // 250 的低 8 位是 250，报文低位 5：最小 V 为 261（跨 256 回绕）。
    auto result = validator.reconstruct(/*low=*/5);
    CHECK_TRUE(name, static_cast<bool>(result));
    CHECK_EQ(name, *result.candidate, 261ULL);
    CHECK_EQ(name, validator.counter(), 250ULL); // 重建不推进

    // MAC 失败：L 保持 250，连续失败 +1，进位不生效。
    validator.notifyMacFailure();
    CHECK_EQ(name, validator.counter(), 250ULL);
    CHECK_EQ(name, validator.consecutiveFailures(), 1U);

    // 同一报文仍可重建出同样的候选值。
    auto retry = validator.reconstruct(5);
    CHECK_TRUE(name, static_cast<bool>(retry));
    CHECK_EQ(name, *retry.candidate, 261ULL);

    // MAC 通过后提交，高位进位才生效。
    CHECK_TRUE(name, validator.commit(261));
    CHECK_EQ(name, validator.counter(), 261ULL);
    CHECK_EQ(name, validator.consecutiveFailures(), 0U);

    // 低位正好回绕到 0：先到 511（低 8 位 255），再提交 512（低 8 位 0，
    // 跨 512 边界的高位进位随 commit 一起生效）。
    auto at_255 = validator.reconstruct(255);
    CHECK_TRUE(name, static_cast<bool>(at_255));
    CHECK_EQ(name, *at_255.candidate, 511ULL);
    CHECK_TRUE(name, validator.commit(511));
    CHECK_EQ(name, validator.counter(), 511ULL);

    auto wrap_to_zero = validator.reconstruct(0);
    CHECK_TRUE(name, static_cast<bool>(wrap_to_zero));
    CHECK_EQ(name, *wrap_to_zero.candidate, 512ULL);
    CHECK_TRUE(name, validator.commit(512));
    CHECK_EQ(name, validator.counter(), 512ULL);
    CHECK_EQ(name, low_byte(validator.counter()), 0);
}

// T2 前向窗口边界：delta == window 接受，delta == window+1 拒绝。
void test_forward_window_boundary() {
    const std::string name = "T2 forward window boundary";
    FreshnessValidator validator(/*initial=*/100, /*window=*/128);

    auto inside = validator.reconstruct(low_byte(228)); // delta 128
    CHECK_TRUE(name, static_cast<bool>(inside));
    CHECK_EQ(name, *inside.candidate, 228ULL);
    CHECK_TRUE(name, validator.commit(228));

    auto outside = validator.reconstruct(low_byte(357)); // delta 129
    CHECK_TRUE(name, !static_cast<bool>(outside));
    CHECK_EQ(name, outside.reason, RejectReason::OutOfWindow);
    CHECK_EQ(name, validator.lastReason(), RejectReason::OutOfWindow);
    CHECK_EQ(name, validator.counter(), 228ULL); // 拒绝不推进

    // 窗口为 0 时，任何普通报文（delta 最小为 1）都被拒绝。
    FreshnessValidator strict(/*initial=*/10, /*window=*/0);
    auto strict_result = strict.reconstruct(low_byte(11));
    CHECK_TRUE(name, !static_cast<bool>(strict_result));
    CHECK_EQ(name, strict_result.reason, RejectReason::OutOfWindow);
}

// T3 重放旧值：候选值严格大于 L；旧低位只能映射到未来值或被窗口拒绝。
void test_replay_old_value() {
    const std::string name = "T3 replay of old value";
    FreshnessValidator validator(/*initial=*/300, /*window=*/128);

    // 重放低位 0x00（对应历史值 256）：映射为下一圈的 512，delta 212 超窗。
    auto replay = validator.reconstruct(0);
    CHECK_TRUE(name, !static_cast<bool>(replay));
    CHECK_EQ(name, replay.reason, RejectReason::OutOfWindow);

    // 合法推进到 310。
    auto valid = validator.reconstruct(low_byte(310));
    CHECK_TRUE(name, static_cast<bool>(valid));
    CHECK_EQ(name, *valid.candidate, 310ULL);
    CHECK_TRUE(name, validator.commit(310));

    // 重放刚提交过的低位（44）：严格大于 310 的同低位值为 566，超窗。
    auto replay_same_low = validator.reconstruct(low_byte(310));
    CHECK_TRUE(name, !static_cast<bool>(replay_same_low));
    CHECK_EQ(name, replay_same_low.reason, RejectReason::OutOfWindow);

    // 即使外部误用旧候选值调用 commit，也必须被拒绝且 L 不变。
    CHECK_TRUE(name, !validator.commit(300));
    CHECK_EQ(name, validator.lastReason(), RejectReason::StaleCandidate);
    CHECK_EQ(name, validator.counter(), 310ULL);
    CHECK_TRUE(name, !validator.commit(310)); // 等于 L 同样拒绝
    CHECK_EQ(name, validator.counter(), 310ULL);
}

// T4 连续 3 次失败进入重同步；期间普通报文一律拒绝；受信同步可恢复。
void test_resync_state_machine() {
    const std::string name = "T4 resync state machine";
    FreshnessValidator validator(/*initial=*/0, /*window=*/128);

    int mac_calls = 0;
    auto mac_always_fail = [&](std::uint64_t) {
        ++mac_calls;
        return false;
    };

    for (int i = 1; i <= 2; ++i) {
        CHECK_TRUE(name, !validator.processNormalPdu(low_byte(10 * i),
                                                     mac_always_fail));
        CHECK_EQ(name, validator.consecutiveFailures(),
                 static_cast<unsigned>(i));
        CHECK_TRUE(name, !validator.resyncActive());
    }

    // 第三次失败触发重同步。
    CHECK_TRUE(name, !validator.processNormalPdu(30, mac_always_fail));
    CHECK_EQ(name, mac_calls, 3);
    CHECK_TRUE(name, validator.resyncActive());
    CHECK_EQ(name, validator.counter(), 0ULL); // 失败始终不推进

    // 重同步期间普通报文在重建阶段即被拒绝，且不再调用 MAC。
    auto blocked = validator.reconstruct(31);
    CHECK_TRUE(name, !static_cast<bool>(blocked));
    CHECK_EQ(name, blocked.reason, RejectReason::ResyncActive);
    CHECK_TRUE(name, !validator.processNormalPdu(31, mac_always_fail));
    CHECK_EQ(name, mac_calls, 3);
    CHECK_TRUE(name, !validator.commit(31));
    CHECK_EQ(name, validator.lastReason(), RejectReason::ResyncActive);

    // 只有受信同步消息可以装载计数器并退出重同步。
    constexpr std::uint64_t kTrusted = 9000; // 低 8 位 = 40
    validator.applyTrustedSync(kTrusted);
    CHECK_TRUE(name, !validator.resyncActive());
    CHECK_EQ(name, validator.counter(), kTrusted);
    CHECK_EQ(name, validator.consecutiveFailures(), 0U);

    // 恢复后普通报文重新可用。
    auto recovered = validator.reconstruct(low_byte(kTrusted + 5));
    CHECK_TRUE(name, static_cast<bool>(recovered));
    CHECK_EQ(name, *recovered.candidate, kTrusted + 5);
}

// T5 任意一次成功提交都会清零连续失败计数。
void test_success_resets_failures() {
    const std::string name = "T5 success resets failure counter";
    FreshnessValidator validator(/*initial=*/0, /*window=*/128);

    validator.notifyMacFailure();
    validator.notifyMacFailure();
    CHECK_EQ(name, validator.consecutiveFailures(), 2U);

    auto result = validator.reconstruct(50);
    CHECK_TRUE(name, static_cast<bool>(result));
    CHECK_TRUE(name, validator.commit(*result.candidate));
    CHECK_EQ(name, validator.consecutiveFailures(), 0U);
    CHECK_EQ(name, validator.counter(), 50ULL);

    // 再失败两次不应触发重同步（中间的成功已经清零）。
    validator.notifyMacFailure();
    validator.notifyMacFailure();
    CHECK_TRUE(name, !validator.resyncActive());
    CHECK_EQ(name, validator.consecutiveFailures(), 2U);
}

// T6 64 位计数器耗尽：靠近 UINT64_MAX 的回绕被溢出拒绝而非静默归零。
void test_counter_overflow() {
    const std::string name = "T6 64-bit counter overflow";
    constexpr std::uint64_t kMax =
        std::numeric_limits<std::uint64_t>::max();

    FreshnessValidator near_end(/*initial=*/kMax, /*window=*/256);
    auto overflow = near_end.reconstruct(low_byte(kMax)); // delta 256
    CHECK_TRUE(name, !static_cast<bool>(overflow));
    CHECK_EQ(name, overflow.reason, RejectReason::CounterOverflow);
    CHECK_EQ(name, near_end.counter(), kMax);

    // 差一步时仍有合法候选值 kMax，提交后空间耗尽。
    FreshnessValidator one_before(/*initial=*/kMax - 1, /*window=*/256);
    auto last_step = one_before.reconstruct(low_byte(kMax)); // delta 1
    CHECK_TRUE(name, static_cast<bool>(last_step));
    CHECK_EQ(name, *last_step.candidate, kMax);
    CHECK_TRUE(name, one_before.commit(kMax));

    auto no_room = one_before.reconstruct(low_byte(kMax));
    CHECK_TRUE(name, !static_cast<bool>(no_room));
    CHECK_EQ(name, no_room.reason, RejectReason::CounterOverflow);
}

// T7 成功重建会清除上一次的拒绝原因。
void test_reason_cleared_on_success() {
    const std::string name = "T7 reject reason cleared";
    FreshnessValidator validator(/*initial=*/100, /*window=*/128);

    auto rejected = validator.reconstruct(low_byte(500)); // delta 400
    CHECK_TRUE(name, !static_cast<bool>(rejected));
    CHECK_EQ(name, validator.lastReason(), RejectReason::OutOfWindow);

    auto accepted = validator.reconstruct(low_byte(101)); // delta 1
    CHECK_TRUE(name, static_cast<bool>(accepted));
    CHECK_EQ(name, validator.lastReason(), RejectReason::None);
    CHECK_EQ(name, validator.lastReason(), accepted.reason);
}

// T8 processNormalPdu 集成路径：窗口外/重同步拒绝不调用 MAC、不计失败。
void test_process_normal_pdu_integration() {
    const std::string name = "T8 processNormalPdu integration";
    FreshnessValidator validator(/*initial=*/0, /*window=*/128);

    int mac_calls = 0;
    auto mac = [&](std::uint64_t candidate) {
        ++mac_calls;
        return candidate == 40ULL; // 仅候选值 40 的报文通过
    };

    // 超窗报文：MAC 不被调用，失败计数不增加。
    CHECK_TRUE(name, !validator.processNormalPdu(low_byte(400), mac));
    CHECK_EQ(name, mac_calls, 0);
    CHECK_EQ(name, validator.consecutiveFailures(), 0U);

    // 窗口内但 MAC 失败。
    CHECK_TRUE(name, !validator.processNormalPdu(30, mac));
    CHECK_EQ(name, mac_calls, 1);
    CHECK_EQ(name, validator.consecutiveFailures(), 1U);

    // MAC 通过并提交。
    CHECK_TRUE(name, validator.processNormalPdu(40, mac));
    CHECK_EQ(name, mac_calls, 2);
    CHECK_EQ(name, validator.counter(), 40ULL);
    CHECK_EQ(name, validator.consecutiveFailures(), 0U);

    // 触发重同步后，普通 PDU 直接拒绝且 MAC 不被调用。
    FreshnessValidator resyncing(0, 128);
    auto always_fail = [&](std::uint64_t) { return false; };
    resyncing.processNormalPdu(10, always_fail);
    resyncing.processNormalPdu(11, always_fail);
    resyncing.processNormalPdu(12, always_fail);
    CHECK_TRUE(name, resyncing.resyncActive());

    int calls_during_resync = 0;
    auto counting_mac = [&](std::uint64_t) {
        ++calls_during_resync;
        return true;
    };
    CHECK_TRUE(name, !resyncing.processNormalPdu(13, counting_mac));
    CHECK_EQ(name, calls_during_resync, 0);
}

} // namespace

int main() {
    test_wrap_boundary_and_mac_failure();
    test_forward_window_boundary();
    test_replay_old_value();
    test_resync_state_machine();
    test_success_resets_failures();
    test_counter_overflow();
    test_reason_cleared_on_success();
    test_process_normal_pdu_integration();

    if (g_failures.empty()) {
        std::cout << "all freshness validator tests passed\n";
        return EXIT_SUCCESS;
    }

    for (const CheckFailure& failure : g_failures) {
        std::cerr << "FAIL [" << failure.name << "]: " << failure.detail
                  << '\n';
    }
    std::cerr << g_failures.size() << " check(s) failed\n";
    return EXIT_FAILURE;
}
