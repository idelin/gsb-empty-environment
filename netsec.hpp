#pragma once

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <vector>

namespace netsec {

// 楼宇自控对象类型（类比 BACnet 对象类型）。
enum class ObjectType : std::uint16_t {
    AnalogInput = 0,
    AnalogOutput = 1,
    AnalogValue = 2,
    BinaryOutput = 4,
    BinaryValue = 5,
    ChillerSetpoint = 128,   // 冷冻机设定（关键设备）
    AlarmThreshold = 129,    // 报警阈值（关键设备）
};

// 对象标识：类型 + 实例号。
struct ObjectId {
    ObjectType type;
    std::uint32_t instance;

    friend bool operator<(const ObjectId& a, const ObjectId& b) {
        return a.type != b.type
                   ? static_cast<std::uint16_t>(a.type) < static_cast<std::uint16_t>(b.type)
                   : a.instance < b.instance;
    }
    friend bool operator==(const ObjectId& a, const ObjectId& b) = default;
};

// 网关侧调用主体角色。
enum class Role {
    Guest,       // 未授权
    Operator,    // 普通操作员
    Supervisor,  // 主管
    Admin,       // 管理员
};

// 单个对象的写保护策略。
struct ObjectPolicy {
    ObjectId id;
    double min_value;                 // 允许写入范围下限
    double max_value;                 // 允许写入范围上限
    std::uint8_t min_priority;        // 该对象可接受的最高优先级（数值最小）
    std::vector<Role> allowed_roles;  // 允许写入的角色（关键设备需收窄）
    bool critical = false;            // 关键设备标记
};

enum class WriteError {
    ObjectNotMapped,     // 对象未映射（对象伪造）
    TypeMismatch,        // 对象类型与注册策略不符
    PriorityOutOfRange,  // 优先级不在 1..16
    PriorityEscalation,  // 优先级提升：角色无权使用该优先级
    UnauthorizedRole,    // 角色无权写该对象
    ValueOutOfRange,     // 写入值越界
    ReadbackMismatch,    // 写后回读不一致
};

struct WriteResult {
    bool ok = false;
    WriteError error = WriteError::ObjectNotMapped;
    double readback_value = 0.0;  // 写后回读值（成功时有效）
};

const char* to_string(WriteError e);

// 对象写保护组件：楼宇网关在转发写请求前调用 write()。
class ObjectWriteProtector {
public:
    // 注册对象及其写保护策略与初始值。
    void register_object(ObjectPolicy policy, double initial_value);

    // 校验并执行写入：标识/类型/优先级/范围/角色全部通过才写入，
    // 写入后回读确认。任何一步失败均拒绝且原值不变。
    WriteResult write(Role role, ObjectId id, std::uint8_t priority, double value);

    // 读取当前值（用于回读验证设定值未被篡改）。
    std::optional<double> read(ObjectId id) const;

private:
    static std::uint8_t role_max_priority(Role role);

    struct Entry {
        ObjectPolicy policy;
        double value;
    };
    std::map<ObjectId, Entry> objects_;
};

}  // namespace netsec
