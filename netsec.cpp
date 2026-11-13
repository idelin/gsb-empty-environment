#include "netsec.hpp"

namespace netsec {

CanAttackDetector::CanAttackDetector(DetectorConfig config) : config_(config) {}

NodeState CanAttackDetector::stateOf(const Node& node) const {
    if (node.tec > config_.busoff_threshold) {
        return NodeState::BusOff;
    }
    if (node.tec >= config_.passive_threshold) {
        return NodeState::ErrorPassive;
    }
    return NodeState::ErrorActive;
}

void CanAttackDetector::queueAlarm(uint32_t id, AlarmType type, const Node& node) {
    alarms_.push_back(Alarm{
        id,
        type,
        node.tec,
        node.streak,
        node.absent_periods,
    });
}

void CanAttackDetector::recomputeNoise() {
    bool any_active = false;
    for (const auto& entry : nodes_) {
        if (entry.second.tec > 0) {
            any_active = true;
            if (entry.second.tec >= config_.noise_tec_limit) {
                background_noise_ = false;
                return;
            }
        }
    }
    background_noise_ = any_active && nodes_.size() >= 2;
}

void CanAttackDetector::onErrorFrame(uint32_t id) {
    std::lock_guard<std::mutex> lock(mutex_);

    Node& node = nodes_[id];
    node.active_this_period = true;

    node.tec += config_.error_increment;
    node.streak += 1;

    if (node.tec >= config_.passive_threshold && !node.passive_announced) {
        node.passive_announced = true;
        queueAlarm(id, AlarmType::ErrorPassive, node);
    }
    if (node.tec > config_.busoff_threshold && !node.busoff_announced) {
        node.busoff_announced = true;
        queueAlarm(id, AlarmType::BusOff, node);
    }

    if (node.streak >= config_.attack_error_run) {
        node.attack_watch = true;
    }

    recomputeNoise();
}

void CanAttackDetector::onSuccessFrame(uint32_t id) {
    std::lock_guard<std::mutex> lock(mutex_);

    Node& node = nodes_[id];
    node.active_this_period = true;

    if (node.tec >= config_.success_decrement) {
        node.tec -= config_.success_decrement;
    } else {
        node.tec = 0;
    }

    node.streak = 0;
    node.attack_watch = false;

    if (node.tec < config_.passive_threshold) {
        node.passive_announced = false;
    }
    if (node.tec <= config_.busoff_threshold) {
        node.busoff_announced = false;
    }

    recomputeNoise();
}

std::size_t CanAttackDetector::tickPeriod() {
    std::lock_guard<std::mutex> lock(mutex_);

    std::size_t new_attacks = 0;
    for (auto& entry : nodes_) {
        Node& node = entry.second;
        // 本周期内收到过帧：活跃，缺席计数清零，结算后进入下一周期。
        if (node.active_this_period) {
            node.active_this_period = false;
            node.absent_periods = 0;
            continue;
        }

        // 本周期无帧：若在攻击监视中则累计缺席。
        if (!node.attack_watch) {
            continue;
        }

        ++node.absent_periods;
        if (node.absent_periods >= config_.attack_absent_periods) {
            node.attack_watch = false;
            node.attack_flagged = true;
            queueAlarm(entry.first, AlarmType::InjectionAttack, node);
            ++new_attacks;
        }
    }
    return new_attacks;
}

std::vector<Alarm> CanAttackDetector::drainAlarms() {
    std::lock_guard<std::mutex> lock(mutex_);
    std::vector<Alarm> result;
    result.swap(alarms_);
    return result;
}

NodeStatus CanAttackDetector::getStatus(uint32_t id) const {
    std::lock_guard<std::mutex> lock(mutex_);

    NodeStatus result;
    result.id = id;
    auto it = nodes_.find(id);
    if (it == nodes_.end()) {
        return result;
    }

    const Node& node = it->second;
    result.tec = node.tec;
    result.consecutive_errors = node.streak;
    result.absent_periods = node.absent_periods;
    result.attack_watch = node.attack_watch;
    result.attack_flagged = node.attack_flagged;
    result.state = stateOf(node);
    return result;
}

std::vector<NodeStatus> CanAttackDetector::listStatus() const {
    std::lock_guard<std::mutex> lock(mutex_);

    std::vector<NodeStatus> result;
    result.reserve(nodes_.size());
    for (const auto& entry : nodes_) {
        const Node& node = entry.second;
        NodeStatus status;
        status.id = entry.first;
        status.tec = node.tec;
        status.consecutive_errors = node.streak;
        status.absent_periods = node.absent_periods;
        status.attack_watch = node.attack_watch;
        status.attack_flagged = node.attack_flagged;
        status.state = stateOf(node);
        result.push_back(status);
    }
    return result;
}

bool CanAttackDetector::hasAttack(uint32_t id) const {
    std::lock_guard<std::mutex> lock(mutex_);
    auto it = nodes_.find(id);
    return it != nodes_.end() && it->second.attack_flagged;
}

bool CanAttackDetector::backgroundNoiseDetected() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return background_noise_;
}

std::size_t CanAttackDetector::trackedNodeCount() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return nodes_.size();
}

void CanAttackDetector::reset() {
    std::lock_guard<std::mutex> lock(mutex_);
    nodes_.clear();
    alarms_.clear();
    background_noise_ = false;
}

const char* alarmTypeName(AlarmType type) {
    switch (type) {
        case AlarmType::ErrorPassive:
            return "error-passive";
        case AlarmType::BusOff:
            return "bus-off";
        case AlarmType::InjectionAttack:
            return "injection-attack";
    }
    return "unknown";
}

const char* nodeStateName(NodeState state) {
    switch (state) {
        case NodeState::ErrorActive:
            return "error-active";
        case NodeState::ErrorPassive:
            return "error-passive";
        case NodeState::BusOff:
            return "bus-off";
    }
    return "unknown";
}

} // namespace netsec
