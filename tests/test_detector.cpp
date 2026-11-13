#include "netsec.hpp"

#include <atomic>
#include <cassert>
#include <iostream>
#include <thread>

using namespace netsec;

namespace {

int g_failures = 0;

#define CHECK(condition)                                                                          \
    do {                                                                                          \
        if (!(condition)) {                                                                       \
            std::cerr << "CHECK failed: " #condition " (line " << __LINE__ << ")\n";              \
            ++g_failures;                                                                         \
        }                                                                                         \
    } while (0)

std::size_t countType(const std::vector<Alarm>& alarms, AlarmType type) {
    std::size_t count = 0;
    for (const Alarm& alarm : alarms) {
        if (alarm.type == type) {
            ++count;
        }
    }
    return count;
}

// 连续显性位注入：每帧 TEC +8，验证 128 被动错误与 >255 bus-off。
void testContinuousInjection() {
    CanAttackDetector detector;
    const uint32_t victim = 0x100;

    for (int frame = 1; frame <= 32; ++frame) {
        detector.onErrorFrame(victim);
        CHECK(detector.getStatus(victim).tec == static_cast<std::size_t>(frame) * 8);
    }

    NodeStatus status = detector.getStatus(victim);
    CHECK(status.tec == 256);
    CHECK(status.state == NodeState::BusOff);
    // 连续 32 次错误已满足 16 连错，节点进入攻击监视（等待缺席确认）。
    CHECK(status.attack_watch);
    CHECK(status.absent_periods == 0);

    std::vector<Alarm> alarms = detector.drainAlarms();
    CHECK(countType(alarms, AlarmType::ErrorPassive) == 1);
    CHECK(countType(alarms, AlarmType::BusOff) == 1);
    CHECK(countType(alarms, AlarmType::InjectionAttack) == 0);

    const Alarm& passive = alarms.front();
    CHECK(passive.type == AlarmType::ErrorPassive && passive.tec == 128);
    CHECK(alarms.back().type == AlarmType::BusOff && alarms.back().tec == 256);

    // 告警是边沿触发：持续故障不会重复上报。
    detector.onErrorFrame(victim);
    CHECK(detector.drainAlarms().empty());

    // 节点每个周期都出现活动：即使跨周期，缺席计数也始终为 0，不判攻击。
    for (int period = 0; period < 4; ++period) {
        detector.onErrorFrame(victim); // 新周期内节点活动
        CHECK(detector.tickPeriod() == 0); // 周期末结算：活跃，不缺席
        CHECK(detector.getStatus(victim).absent_periods == 0);
    }
    CHECK(!detector.hasAttack(victim));
}

// 背景噪声：错误分散在多个 ID，每个估计值低于 64，不产生任何告警。
void testBackgroundNoise() {
    CanAttackDetector detector;
    const uint32_t ids[] = {0x11, 0x22, 0x33, 0x44};

    // 每个 ID 最多 7 次错误 -> TEC 56，低于噪声上限 64。
    for (int round = 0; round < 7; ++round) {
        for (uint32_t id : ids) {
            detector.onErrorFrame(id);
        }
        detector.tickPeriod();
    }

    for (uint32_t id : ids) {
        CHECK(detector.getStatus(id).tec == 56);
        CHECK(detector.getStatus(id).state == NodeState::ErrorActive);
    }
    CHECK(detector.backgroundNoiseDetected());
    CHECK(detector.drainAlarms().empty());

    // 单个 ID 不构成“分散在多个 ID”，不标记背景噪声。
    CanAttackDetector single;
    for (int frame = 0; frame < 3; ++frame) {
        single.onErrorFrame(0x55);
    }
    CHECK(!single.backgroundNoiseDetected());
    CHECK(single.drainAlarms().empty());

    // 任一 ID 达到 64 即脱离噪声区间。
    detector.onErrorFrame(ids[0]);
    CHECK(detector.getStatus(ids[0]).tec == 64);
    CHECK(!detector.backgroundNoiseDetected());
}

// 注入攻击签名：同一 ID 连续 16 次错误后缺席两个周期。
void testInjectionAttack() {
    const uint32_t victim = 0x200;

    // 场景 A：16 连错后连续两个周期无活动，第二个周期末判定攻击。
    {
        CanAttackDetector detector;
        for (int frame = 0; frame < 16; ++frame) {
            detector.onErrorFrame(victim);
        }
        NodeStatus armed = detector.getStatus(victim);
        CHECK(armed.attack_watch);
        CHECK(armed.consecutive_errors == 16);
        // 16 次错误时 TEC=128 会顺带产生一次被动错误告警，先消费掉。
        (void)detector.drainAlarms();

        // 第 1 次 tick 结算 16 连错所在的活跃周期，不计缺席。
        CHECK(detector.tickPeriod() == 0);
        CHECK(detector.getStatus(victim).absent_periods == 0);

        // 随后两个周期无活动，分别计为第 1、2 个缺席周期。
        CHECK(detector.tickPeriod() == 0);
        CHECK(detector.getStatus(victim).absent_periods == 1);
        CHECK(!detector.hasAttack(victim));

        CHECK(detector.tickPeriod() == 1); // 第 2 个缺席周期末判定攻击
        CHECK(detector.hasAttack(victim));
        CHECK(!detector.getStatus(victim).attack_watch);

        std::vector<Alarm> alarms = detector.drainAlarms();
        CHECK(alarms.size() == 1);
        CHECK(alarms.front().type == AlarmType::InjectionAttack);
        CHECK(alarms.front().absent_periods == 2);
        CHECK(detector.tickPeriod() == 0);
    }

    // 场景 B：缺席一个周期后节点恢复活动，缺席计数清零，不判攻击。
    {
        CanAttackDetector detector;
        for (int frame = 0; frame < 16; ++frame) {
            detector.onErrorFrame(victim);
        }
        CHECK(detector.tickPeriod() == 0); // 结算连错所在的活跃周期
        CHECK(detector.tickPeriod() == 0); // 缺席第 1 个周期
        CHECK(detector.getStatus(victim).absent_periods == 1);

        // 下一周期节点恢复活动，该周期末缺席计数被清零。
        detector.onErrorFrame(victim);
        CHECK(detector.tickPeriod() == 0);
        CHECK(detector.getStatus(victim).absent_periods == 0);
        CHECK(!detector.hasAttack(victim));

        // 之后重新连续缺席两个周期，才判定攻击。
        CHECK(detector.tickPeriod() == 0);
        CHECK(detector.getStatus(victim).absent_periods == 1);
        CHECK(detector.tickPeriod() == 1);
        CHECK(detector.hasAttack(victim));
    }

    // 场景 C：只有 15 次连续错误，缺席两个周期也不判攻击。
    {
        CanAttackDetector detector;
        for (int frame = 0; frame < 15; ++frame) {
            detector.onErrorFrame(victim);
        }
        CHECK(!detector.getStatus(victim).attack_watch);
        CHECK(detector.tickPeriod() == 0); // 活跃周期
        CHECK(detector.tickPeriod() == 0);
        CHECK(detector.tickPeriod() == 0);
        CHECK(!detector.hasAttack(victim));
        CHECK(detector.drainAlarms().empty());
    }

    // 场景 D：16 连错后出现成功帧，监视解除；重新计数前不判攻击。
    {
        CanAttackDetector detector;
        for (int frame = 0; frame < 16; ++frame) {
            detector.onErrorFrame(victim);
        }
        detector.onSuccessFrame(victim);
        CHECK(!detector.getStatus(victim).attack_watch);
        CHECK(detector.getStatus(victim).consecutive_errors == 0);
        CHECK(detector.tickPeriod() == 0); // 成功帧所在周期活跃
        CHECK(detector.tickPeriod() == 0);
        CHECK(detector.tickPeriod() == 0);
        CHECK(!detector.hasAttack(victim));
    }
}

// 恢复：成功帧 TEC 减 1 且不低于 0，状态迁移与告警重新武装正确。
void testRecovery() {
    CanAttackDetector detector;
    const uint32_t victim = 0x300;

    // 17 次错误 -> TEC 136，进入被动错误。
    for (int frame = 0; frame < 17; ++frame) {
        detector.onErrorFrame(victim);
    }
    CHECK(detector.getStatus(victim).state == NodeState::ErrorPassive);
    CHECK(detector.drainAlarms().size() == 1);

    // 成功帧逐帧递减；降到 127 时恢复主动状态，且不重复被动告警。
    for (std::size_t expected = 135; expected > 0; --expected) {
        detector.onSuccessFrame(victim);
        CHECK(detector.getStatus(victim).tec == expected);
    }
    CHECK(detector.getStatus(victim).state == NodeState::ErrorActive);
    CHECK(detector.drainAlarms().empty());

    // 已经在 0，成功帧仍钳制在 0。
    detector.onSuccessFrame(victim);
    detector.onSuccessFrame(victim);
    CHECK(detector.getStatus(victim).tec == 0);

    // 再次爬升可重新触发被动错误与 bus-off 告警。
    for (int frame = 0; frame < 32; ++frame) {
        detector.onErrorFrame(victim);
    }
    CHECK(detector.getStatus(victim).state == NodeState::BusOff);
    std::vector<Alarm> alarms = detector.drainAlarms();
    CHECK(countType(alarms, AlarmType::ErrorPassive) == 1);
    CHECK(countType(alarms, AlarmType::BusOff) == 1);

    // 从 256 一路成功帧恢复：越过 255 与 128 均无“恢复”告警，最终回到主动状态。
    for (int frame = 0; frame < 256; ++frame) {
        detector.onSuccessFrame(victim);
    }
    NodeStatus recovered = detector.getStatus(victim);
    CHECK(recovered.tec == 0);
    CHECK(recovered.state == NodeState::ErrorActive);
    CHECK(!detector.hasAttack(victim));
    CHECK(detector.drainAlarms().empty());
}

// 多 ID 隔离与并发回调下的线程安全冒烟测试。
void testThreadSafety() {
    CanAttackDetector detector;
    std::atomic<bool> stop{false};

    std::thread consumer([&] {
        while (!stop.load()) {
            for (const Alarm& alarm : detector.drainAlarms()) {
                (void)alarm;
            }
        }
    });

    std::thread producer([&] {
        for (int iteration = 0; iteration < 2000; ++iteration) {
            uint32_t id = static_cast<uint32_t>(iteration % 8);
            if (iteration % 5 == 0) {
                detector.onSuccessFrame(id);
            } else {
                detector.onErrorFrame(id);
            }
        }
    });

    std::thread ticker([&] {
        for (int tick = 0; tick < 500; ++tick) {
            detector.tickPeriod();
        }
    });

    producer.join();
    ticker.join();
    stop.store(true);
    consumer.join();

    CHECK(detector.trackedNodeCount() == 8);
    for (uint32_t id = 0; id < 8; ++id) {
        NodeStatus status = detector.getStatus(id);
        CHECK(status.tec <= 250 * 8);
        if (status.tec > 255) {
            CHECK(status.state == NodeState::BusOff);
        } else if (status.tec >= 128) {
            CHECK(status.state == NodeState::ErrorPassive);
        } else {
            CHECK(status.state == NodeState::ErrorActive);
        }
    }
}

} // namespace

int main() {
    testContinuousInjection();
    testBackgroundNoise();
    testInjectionAttack();
    testRecovery();
    testThreadSafety();

    if (g_failures != 0) {
        std::cerr << g_failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "all detector tests passed\n";
    return 0;
}
