#include <cmath>
#include <cstdio>
#include <cstdlib>

#include "netsec.hpp"

namespace {

int failures = 0;

#define CHECK(cond)                                                            \
    do {                                                                       \
        if (!(cond)) {                                                         \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
            ++failures;                                                        \
        }                                                                      \
    } while (0)

using netsec::ObjectId;
using netsec::ObjectPolicy;
using netsec::ObjectType;
using netsec::ObjectWriteProtector;
using netsec::Role;
using netsec::WriteError;

const ObjectId kChillerSetpoint{ObjectType::ChillerSetpoint, 1};
const ObjectId kAlarmThreshold{ObjectType::AlarmThreshold, 1};
const ObjectId kRoomSetpoint{ObjectType::AnalogValue, 10};

ObjectWriteProtector make_protector() {
    ObjectWriteProtector p;
    // 冷冻机出水温度设定：5..15 ℃，关键设备，仅 Admin/Supervisor。
    p.register_object(ObjectPolicy{kChillerSetpoint, 5.0, 15.0, 1,
                                   {Role::Admin, Role::Supervisor}, true},
                      7.0);
    // 报警阈值：0..50 ℃，关键设备，仅 Admin。
    p.register_object(ObjectPolicy{kAlarmThreshold, 0.0, 50.0, 1, {Role::Admin}, true},
                      30.0);
    // 普通房间设定：16..30 ℃，Operator 及以上。
    p.register_object(ObjectPolicy{kRoomSetpoint, 16.0, 30.0, 8,
                                   {Role::Admin, Role::Supervisor, Role::Operator}, false},
                      22.0);
    return p;
}

// 1. 越界写：超出允许范围的值被拒绝，设定值不变。
void test_out_of_range_write() {
    auto p = make_protector();
    auto r = p.write(Role::Admin, kChillerSetpoint, 1, 45.0);
    CHECK(!r.ok);
    CHECK(r.error == WriteError::ValueOutOfRange);
    CHECK(p.read(kChillerSetpoint).value() == 7.0);

    r = p.write(Role::Admin, kChillerSetpoint, 1, -100.0);
    CHECK(!r.ok);
    CHECK(r.error == WriteError::ValueOutOfRange);
    CHECK(p.read(kChillerSetpoint).value() == 7.0);
}

// 2. 优先级提升：低权限角色使用高优先级被拒绝；越界优先级被拒绝。
void test_priority_escalation() {
    auto p = make_protector();
    // Operator 最高只能用优先级 8，使用 1 属于优先级提升。
    auto r = p.write(Role::Operator, kRoomSetpoint, 1, 24.0);
    CHECK(!r.ok);
    CHECK(r.error == WriteError::PriorityEscalation);
    CHECK(p.read(kRoomSetpoint).value() == 22.0);

    // Guest 尝试以优先级 4 写报警阈值。
    r = p.write(Role::Guest, kAlarmThreshold, 4, 10.0);
    CHECK(!r.ok);
    CHECK(r.error == WriteError::PriorityEscalation);
    CHECK(p.read(kAlarmThreshold).value() == 30.0);

    // 优先级越界（0 与 17）。
    r = p.write(Role::Admin, kChillerSetpoint, 0, 8.0);
    CHECK(!r.ok);
    CHECK(r.error == WriteError::PriorityOutOfRange);
    r = p.write(Role::Admin, kChillerSetpoint, 17, 8.0);
    CHECK(!r.ok);
    CHECK(r.error == WriteError::PriorityOutOfRange);
    CHECK(p.read(kChillerSetpoint).value() == 7.0);
}

// 3. 对象伪造：未映射的对象标识被拒绝。
void test_object_forgery() {
    auto p = make_protector();
    ObjectId forged{ObjectType::ChillerSetpoint, 99};  // 未注册实例
    auto r = p.write(Role::Admin, forged, 1, 6.0);
    CHECK(!r.ok);
    CHECK(r.error == WriteError::ObjectNotMapped);
    CHECK(!p.read(forged).has_value());

    ObjectId forged2{ObjectType::AnalogOutput, 1};  // 未注册类型
    r = p.write(Role::Admin, forged2, 1, 6.0);
    CHECK(!r.ok);
    CHECK(r.error == WriteError::ObjectNotMapped);
}

// 4. 未授权角色写关键设备被拒绝。
void test_unauthorized_role_on_critical() {
    auto p = make_protector();
    auto r = p.write(Role::Operator, kChillerSetpoint, 8, 9.0);
    CHECK(!r.ok);
    CHECK(r.error == WriteError::UnauthorizedRole);
    CHECK(p.read(kChillerSetpoint).value() == 7.0);

    r = p.write(Role::Supervisor, kAlarmThreshold, 4, 20.0);
    CHECK(!r.ok);
    CHECK(r.error == WriteError::UnauthorizedRole);
    CHECK(p.read(kAlarmThreshold).value() == 30.0);
}

// 5. 合法写入成功，写后回读值与写入值一致。
void test_legitimate_write_and_readback() {
    auto p = make_protector();
    auto r = p.write(Role::Admin, kChillerSetpoint, 1, 9.5);
    CHECK(r.ok);
    CHECK(std::fabs(r.readback_value - 9.5) < 1e-9);
    CHECK(p.read(kChillerSetpoint).value() == 9.5);

    r = p.write(Role::Operator, kRoomSetpoint, 8, 25.0);
    CHECK(r.ok);
    CHECK(std::fabs(r.readback_value - 25.0) < 1e-9);
    CHECK(p.read(kRoomSetpoint).value() == 25.0);
}

// 6. 组合攻击后回读验证：所有被拒绝的写入均未篡改设定值。
void test_setpoint_not_tampered_after_attacks() {
    auto p = make_protector();
    // 一连串攻击：越界、优先级提升、伪造对象、未授权角色。
    p.write(Role::Admin, kChillerSetpoint, 1, 99.0);
    p.write(Role::Operator, kChillerSetpoint, 1, 6.0);
    p.write(Role::Admin, ObjectId{ObjectType::ChillerSetpoint, 7}, 1, 6.0);
    p.write(Role::Guest, kChillerSetpoint, 16, 6.0);
    p.write(Role::Admin, kAlarmThreshold, 1, 1000.0);
    // 回读验证：设定值与阈值保持初始值。
    CHECK(p.read(kChillerSetpoint).value() == 7.0);
    CHECK(p.read(kAlarmThreshold).value() == 30.0);
    // 合法写入后回读确认新值。
    auto r = p.write(Role::Admin, kChillerSetpoint, 2, 8.0);
    CHECK(r.ok);
    CHECK(p.read(kChillerSetpoint).value() == 8.0);
}

}  // namespace

int main() {
    test_out_of_range_write();
    test_priority_escalation();
    test_object_forgery();
    test_unauthorized_role_on_critical();
    test_legitimate_write_and_readback();
    test_setpoint_not_tampered_after_attacks();

    if (failures != 0) {
        std::fprintf(stderr, "%d check(s) failed\n", failures);
        return EXIT_FAILURE;
    }
    std::puts("all netsec tests passed");
    return EXIT_SUCCESS;
}
