#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <optional>
#include <set>
#include <vector>

namespace netsec {

// Modbus 风格功能码。请求里携带原始字节，未知值按伪装功能码拒绝。
enum class FunctionCode : std::uint8_t {
    ReadCoils = 1,
    ReadDiscreteInputs = 2,
    ReadHoldingRegisters = 3,
    ReadInputRegisters = 4,
    WriteSingleCoil = 5,
    WriteSingleRegister = 6,
    WriteMultipleCoils = 15,
    WriteMultipleRegisters = 16,
};

enum class DataType { Bit, Register };

constexpr std::uint8_t kBroadcastUnit = 0;

bool is_read_only(FunctionCode fc);
bool is_write(FunctionCode fc);
DataType data_type_of(FunctionCode fc);
// 已知功能码的合法数量区间；单点写固定为 [1,1]。
std::optional<std::pair<std::uint16_t, std::uint16_t>> quantity_limits(FunctionCode fc);
std::optional<FunctionCode> parse_function_code(std::uint8_t raw);

struct AddressRange {
    std::uint16_t start = 0;
    std::uint16_t count = 0;
    bool writable = false;
};

struct DeviceProfile {
    std::uint8_t unit_id = 0;
    bool write_protected = false;
    std::set<FunctionCode> allowed_functions;
    std::map<DataType, std::vector<AddressRange>> address_map;
};

struct Request {
    std::uint8_t unit_id = 0;
    std::uint8_t function_code = 0; // 线上原始字节
    std::uint16_t address = 0;
    std::uint16_t quantity = 1;
};

enum class Decision {
    Forwarded,
    RejectedUnmappedUnit,
    RejectedBroadcastWrite,
    RejectedUnknownFunction,
    RejectedFunctionNotAllowed,
    RejectedQuantity,
    RejectedAddressRange,
    RejectedWriteProtected,
};

struct Counters {
    std::size_t forwarded = 0;
    std::size_t unmapped_unit = 0;
    std::size_t broadcast_write = 0;
    std::size_t unknown_function = 0;
    std::size_t function_not_allowed = 0;
    std::size_t bad_quantity = 0;
    std::size_t bad_address_range = 0;
    std::size_t write_protected = 0;

    std::size_t rejected_total() const;
};

// 请求路由组件：按设备映射白名单校验功能码与单元标识，
// 逐项核对地址范围、数量与数据类型，拒绝并计数非法请求。
class Router {
public:
    explicit Router(std::vector<DeviceProfile> devices);

    Decision route(const Request& req);
    const Counters& counters() const { return counters_; }

private:
    Decision reject(Decision d);

    std::map<std::uint8_t, DeviceProfile> devices_;
    Counters counters_;
};

} // namespace netsec
