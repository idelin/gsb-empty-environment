#include "netsec.hpp"

#include <algorithm>
#include <cmath>

namespace netsec {

namespace {
constexpr std::uint8_t kPriorityMin = 1;   // 最高优先级
constexpr std::uint8_t kPriorityMax = 16;  // 最低优先级
constexpr double kReadbackTolerance = 1e-9;
}  // namespace

const char* to_string(WriteError e) {
    switch (e) {
        case WriteError::ObjectNotMapped: return "object not mapped";
        case WriteError::TypeMismatch: return "object type mismatch";
        case WriteError::PriorityOutOfRange: return "priority out of range";
        case WriteError::PriorityEscalation: return "priority escalation";
        case WriteError::UnauthorizedRole: return "unauthorized role";
        case WriteError::ValueOutOfRange: return "value out of range";
        case WriteError::ReadbackMismatch: return "readback mismatch";
    }
    return "unknown";
}

void ObjectWriteProtector::register_object(ObjectPolicy policy, double initial_value) {
    objects_[policy.id] = Entry{std::move(policy), initial_value};
}

std::uint8_t ObjectWriteProtector::role_max_priority(Role role) {
    // 各角色允许使用的最高优先级（数值越小权限越高）。
    switch (role) {
        case Role::Admin: return 1;
        case Role::Supervisor: return 4;
        case Role::Operator: return 8;
        case Role::Guest: return 16;
    }
    return kPriorityMax;
}

WriteResult ObjectWriteProtector::write(Role role, ObjectId id, std::uint8_t priority,
                                        double value) {
    // 1. 对象标识校验：未映射对象（伪造标识）直接拒绝。
    auto it = objects_.find(id);
    if (it == objects_.end()) {
        return {false, WriteError::ObjectNotMapped, 0.0};
    }
    Entry& entry = it->second;

    // 2. 类型一致性校验：请求标识的类型必须与注册策略一致。
    if (entry.policy.id.type != id.type) {
        return {false, WriteError::TypeMismatch, 0.0};
    }

    // 3. 优先级校验：必须在 1..16 内。
    if (priority < kPriorityMin || priority > kPriorityMax) {
        return {false, WriteError::PriorityOutOfRange, 0.0};
    }

    // 4. 优先级提升校验：角色不得使用超出其权限的更高优先级。
    if (priority < role_max_priority(role)) {
        return {false, WriteError::PriorityEscalation, 0.0};
    }

    // 5. 对象级优先级下限校验。
    if (priority < entry.policy.min_priority) {
        return {false, WriteError::PriorityEscalation, 0.0};
    }

    // 6. 角色授权校验：关键设备仅允许授权角色写入。
    const auto& allowed = entry.policy.allowed_roles;
    if (std::find(allowed.begin(), allowed.end(), role) == allowed.end()) {
        return {false, WriteError::UnauthorizedRole, 0.0};
    }

    // 7. 写入范围校验：越界值拒绝。
    if (!std::isfinite(value) || value < entry.policy.min_value ||
        value > entry.policy.max_value) {
        return {false, WriteError::ValueOutOfRange, 0.0};
    }

    // 8. 执行写入并回读确认。
    entry.value = value;
    const double readback = entry.value;
    if (std::fabs(readback - value) > kReadbackTolerance) {
        return {false, WriteError::ReadbackMismatch, readback};
    }
    return {true, WriteError::ObjectNotMapped, readback};
}

std::optional<double> ObjectWriteProtector::read(ObjectId id) const {
    auto it = objects_.find(id);
    if (it == objects_.end()) {
        return std::nullopt;
    }
    return it->second.value;
}

}  // namespace netsec
