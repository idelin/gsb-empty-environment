#include "netsec.hpp"

#include <cstdio>
#include <vector>

using namespace netsec;

namespace {

int failures = 0;

#define CHECK(cond)                                                                                \
    do {                                                                                           \
        if (!(cond)) {                                                                             \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);                            \
            ++failures;                                                                            \
        }                                                                                          \
    } while (0)

Router make_router() {
    DeviceProfile unit1; // 常规设备：保持寄存器读写
    unit1.unit_id = 1;
    unit1.allowed_functions = {FunctionCode::ReadHoldingRegisters, FunctionCode::WriteSingleRegister,
                               FunctionCode::WriteMultipleRegisters};
    unit1.address_map[DataType::Register] = {{100, 10, true}, {200, 10, false}};

    DeviceProfile unit2; // 相邻设备：线圈 + 寄存器
    unit2.unit_id = 2;
    unit2.allowed_functions = {FunctionCode::ReadCoils, FunctionCode::ReadHoldingRegisters,
                               FunctionCode::WriteSingleCoil};
    unit2.address_map[DataType::Bit] = {{0, 16, true}};
    unit2.address_map[DataType::Register] = {{300, 10, true}};

    DeviceProfile unit3; // 写保护设备
    unit3.unit_id = 3;
    unit3.write_protected = true;
    unit3.allowed_functions = {FunctionCode::ReadHoldingRegisters, FunctionCode::WriteSingleRegister};
    unit3.address_map[DataType::Register] = {{400, 10, true}};

    return Router({unit1, unit2, unit3});
}

void test_broadcast_write_rejected() {
    Router r = make_router();
    CHECK(r.route({kBroadcastUnit, 6, 100, 1}) == Decision::RejectedBroadcastWrite);
    CHECK(r.route({kBroadcastUnit, 16, 100, 2}) == Decision::RejectedBroadcastWrite);
    CHECK(r.route({kBroadcastUnit, 5, 0, 1}) == Decision::RejectedBroadcastWrite);
    // 广播只允许只读功能
    CHECK(r.route({kBroadcastUnit, 3, 100, 4}) == Decision::Forwarded);
    CHECK(r.counters().broadcast_write == 3);
    CHECK(r.counters().forwarded == 1);
}

void test_unmapped_unit_rejected() {
    Router r = make_router();
    CHECK(r.route({9, 3, 100, 1}) == Decision::RejectedUnmappedUnit);
    CHECK(r.route({9, 6, 100, 1}) == Decision::RejectedUnmappedUnit);
    CHECK(r.counters().unmapped_unit == 2);
}

void test_function_code_spoofing() {
    Router r = make_router();
    // 未知功能码（伪装成私有/诊断码）
    CHECK(r.route({1, 0x2B, 100, 1}) == Decision::RejectedUnknownFunction);
    CHECK(r.route({1, 0x08, 100, 1}) == Decision::RejectedUnknownFunction);
    // 合法功能码但不在该设备白名单：unit1 不允许写线圈
    CHECK(r.route({1, 5, 0, 1}) == Decision::RejectedFunctionNotAllowed);
    // unit2 不允许写寄存器
    CHECK(r.route({2, 6, 300, 1}) == Decision::RejectedFunctionNotAllowed);
    CHECK(r.counters().unknown_function == 2);
    CHECK(r.counters().function_not_allowed == 2);
}

void test_legal_read_write() {
    Router r = make_router();
    CHECK(r.route({1, 3, 100, 10}) == Decision::Forwarded);   // 读满整个区间
    CHECK(r.route({1, 3, 200, 5}) == Decision::Forwarded);    // 读只读区间
    CHECK(r.route({1, 6, 105, 1}) == Decision::Forwarded);    // 写单个寄存器
    CHECK(r.route({1, 16, 100, 10}) == Decision::Forwarded);  // 批量写可写区间
    CHECK(r.route({2, 5, 3, 1}) == Decision::Forwarded);      // 写相邻设备线圈
    CHECK(r.counters().forwarded == 5);
    CHECK(r.counters().rejected_total() == 0);
}

void test_write_protected_device() {
    Router r = make_router();
    CHECK(r.route({3, 3, 400, 4}) == Decision::Forwarded); // 读仍允许
    CHECK(r.route({3, 6, 400, 1}) == Decision::RejectedWriteProtected);
    CHECK(r.counters().write_protected == 1);
    CHECK(r.counters().forwarded == 1);
}

void test_address_quantity_datatype() {
    Router r = make_router();
    // 地址越界
    CHECK(r.route({1, 3, 50, 4}) == Decision::RejectedAddressRange);    // 未映射区间
    CHECK(r.route({1, 3, 108, 4}) == Decision::RejectedAddressRange);   // 跨出区间上界
    CHECK(r.route({1, 3, 65535, 2}) == Decision::RejectedAddressRange); // 地址+数量溢出 16 位
    // 写只读区间
    CHECK(r.route({1, 6, 200, 1}) == Decision::RejectedAddressRange);
    CHECK(r.route({1, 16, 205, 3}) == Decision::RejectedAddressRange);
    // 数据类型不符：unit1 没有线圈映射
    CHECK(r.route({1, 3, 0, 1}) == Decision::RejectedAddressRange); // 寄存器读打到线圈区外
    // 数量逐项核对
    CHECK(r.route({1, 3, 100, 0}) == Decision::RejectedQuantity);   // 数量为 0
    CHECK(r.route({1, 3, 100, 126}) == Decision::RejectedQuantity); // 超过读寄存器上限 125
    CHECK(r.route({1, 6, 100, 2}) == Decision::RejectedQuantity);   // 单点写数量必须为 1
    CHECK(r.counters().bad_address_range == 6);
    CHECK(r.counters().bad_quantity == 3);
}

// 端到端：混合攻击流量下，写操作只到达合法目标单元。
void test_writes_reach_only_target() {
    Router r = make_router();
    struct Delivered {
        std::uint8_t unit;
        std::uint16_t address;
        std::uint16_t quantity;
    };
    std::vector<Delivered> sink;

    const Request traffic[] = {
        {kBroadcastUnit, 16, 300, 2}, // 广播写：企图落到相邻 unit2
        {kBroadcastUnit, 6, 100, 1},  // 广播写：企图落到 unit1
        {9, 6, 100, 1},               // 越界单元
        {1, 0x2B, 100, 1},            // 伪装功能码
        {2, 6, 300, 1},               // unit2 白名单外的写寄存器
        {3, 6, 400, 1},               // 写保护设备
        {1, 16, 100, 4},              // 合法批量写 -> 唯一应到达的写
        {1, 3, 100, 4},               // 合法读
    };
    for (const Request& q : traffic) {
        const Decision d = r.route(q);
        const auto fc = parse_function_code(q.function_code);
        if (d == Decision::Forwarded && fc && is_write(*fc) && q.unit_id != kBroadcastUnit) {
            sink.push_back({q.unit_id, q.address, q.quantity});
        }
    }

    CHECK(sink.size() == 1);
    if (sink.size() == 1) {
        CHECK(sink[0].unit == 1);
        CHECK(sink[0].address == 100);
        CHECK(sink[0].quantity == 4);
    }
    CHECK(r.counters().forwarded == 2);
    CHECK(r.counters().rejected_total() == 6);
}

} // namespace

int main() {
    test_broadcast_write_rejected();
    test_unmapped_unit_rejected();
    test_function_code_spoofing();
    test_legal_read_write();
    test_write_protected_device();
    test_address_quantity_datatype();
    test_writes_reach_only_target();

    if (failures == 0) {
        std::printf("all router tests passed\n");
        return 0;
    }
    std::printf("%d check(s) failed\n", failures);
    return 1;
}
