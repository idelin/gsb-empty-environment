#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <mutex>
#include <vector>

namespace netsec {

// 告警类型。背景噪声只通过 backgroundNoiseDetected() 暴露，按需求不产生告警。
enum class AlarmType {
    ErrorPassive,    // 发送错误计数估计达到被动错误阈值（默认 >= 128）
    BusOff,          // 发送错误计数估计超过 bus-off 阈值（默认 > 255）
    InjectionAttack, // 同一 ID 连续 16 次错误后缺席两个周期
};

enum class NodeState {
    ErrorActive,
    ErrorPassive,
    BusOff,
};

struct Alarm {
    uint32_t id = 0;
    AlarmType type = AlarmType::ErrorPassive;
    std::size_t tec = 0;
    std::size_t consecutive_errors = 0;
    std::size_t absent_periods = 0;
};

struct NodeStatus {
    uint32_t id = 0;
    std::size_t tec = 0;
    std::size_t consecutive_errors = 0;
    std::size_t absent_periods = 0;
    bool attack_watch = false;
    bool attack_flagged = false;
    NodeState state = NodeState::ErrorActive;
};

struct DetectorConfig {
    std::size_t error_increment = 8;       // 错误帧 TEC 增量
    std::size_t success_decrement = 1;     // 成功帧 TEC 减量
    std::size_t passive_threshold = 128;   // 达到该值记被动错误
    std::size_t busoff_threshold = 255;    // 超过该值记 bus-off
    std::size_t noise_tec_limit = 64;      // 各 ID 估计值低于该值视为噪声区间
    std::size_t attack_error_run = 16;     // 连续错误次数阈值
    std::size_t attack_absent_periods = 2; // 触发攻击所需缺席周期数
};

// 线程安全的 CAN 显性位注入 / bus-off 检测器。
// 错误帧回调线程调用 onErrorFrame/onSuccessFrame，
// 调度线程每个周期调用 tickPeriod，告警消费线程调用 drainAlarms。
class CanAttackDetector {
public:
    explicit CanAttackDetector(DetectorConfig config = {});

    // 供 CAN 错误帧回调直接调用：对应 ID 的 TEC 估计加 8。
    void onErrorFrame(uint32_t id);

    // 供发送成功回调调用：对应 ID 的 TEC 估计减 1，不低于 0。
    void onSuccessFrame(uint32_t id);

    // 每个调度周期结束时调用一次；返回本周期新判定的注入攻击节点数。
    std::size_t tickPeriod();

    // 取出并清空累计的告警队列。
    std::vector<Alarm> drainAlarms();

    NodeStatus getStatus(uint32_t id) const;
    std::vector<NodeStatus> listStatus() const;
    bool hasAttack(uint32_t id) const;
    bool backgroundNoiseDetected() const;
    std::size_t trackedNodeCount() const;
    void reset();

private:
    struct Node {
        std::size_t tec = 0;
        std::size_t streak = 0;
        std::size_t absent_periods = 0;
        bool active_this_period = false;     // 当前周期内是否收到过帧
        bool attack_watch = false;
        bool attack_flagged = false;
        bool passive_announced = false;
        bool busoff_announced = false;
    };

    NodeState stateOf(const Node& node) const;
    void queueAlarm(uint32_t id, AlarmType type, const Node& node);
    void recomputeNoise();

    DetectorConfig config_;
    std::map<uint32_t, Node> nodes_;
    std::vector<Alarm> alarms_;
    bool background_noise_ = false;
    mutable std::mutex mutex_;
};

const char* alarmTypeName(AlarmType type);
const char* nodeStateName(NodeState state);

} // namespace netsec
