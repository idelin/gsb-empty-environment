#include "netsec.hpp"

#include <iostream>

using namespace netsec;

namespace {

void printAlarms(CanAttackDetector& detector, const char* stage) {
    std::vector<Alarm> alarms = detector.drainAlarms();
    for (const Alarm& alarm : alarms) {
        std::cout << "[" << stage << "] id=0x" << std::hex << alarm.id << std::dec
                  << " alarm=" << alarmTypeName(alarm.type)
                  << " tec=" << alarm.tec
                  << " streak=" << alarm.consecutive_errors
                  << " absent=" << alarm.absent_periods << "\n";
    }
}

} // namespace

int main() {
    // 场景 1：连续注入显性位，受害节点 TEC 每次 +8，最终 bus-off。
    std::cout << "== 连续注入 ===\n";
    CanAttackDetector injector;
    for (int frame = 0; frame < 33; ++frame) {
        injector.onErrorFrame(0x100); // 错误帧回调（同一周期内持续活动）
    }
    printAlarms(injector, "inject");

    // 场景 2：背景噪声，多个 ID 各自只积累少量错误，不告警。
    std::cout << "== 背景噪声 ===\n";
    CanAttackDetector noise;
    for (int round = 0; round < 7; ++round) {
        for (uint32_t id : {0x11u, 0x22u, 0x33u, 0x44u}) {
            noise.onErrorFrame(id);
        }
        noise.tickPeriod(); // 每周期末结算，各 ID 本周期均活跃
    }
    printAlarms(noise, "noise");
    std::cout << "background noise flagged: "
              << (noise.backgroundNoiseDetected() ? "yes" : "no") << "\n";

    // 场景 3：16 连错后节点消失两个周期，判注入攻击。
    std::cout << "== 注入攻击签名 ===\n";
    CanAttackDetector attacker;
    for (int frame = 0; frame < 16; ++frame) {
        attacker.onErrorFrame(0x200);
    }
    printAlarms(attacker, "arm");
    attacker.tickPeriod(); // 结算连错所在的活跃周期，不缺席
    attacker.tickPeriod(); // 缺席第 1 个周期
    attacker.tickPeriod(); // 缺席第 2 个周期 -> 攻击
    printAlarms(attacker, "attack");

    // 场景 4：成功帧让 TEC 逐帧下降，节点恢复主动状态。
    std::cout << "== 恢复 ===\n";
    CanAttackDetector recovering;
    for (int frame = 0; frame < 32; ++frame) {
        recovering.onErrorFrame(0x100); // 先打到 bus-off（TEC=256）
    }
    for (int frame = 0; frame < 264; ++frame) {
        recovering.onSuccessFrame(0x100);
    }
    NodeStatus recovered = recovering.getStatus(0x100);
    std::cout << "victim 0x100 tec=" << recovered.tec
              << " state=" << nodeStateName(recovered.state) << "\n";
    printAlarms(recovering, "recover");

    return 0;
}
