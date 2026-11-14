#include "netsec.hpp"

namespace netsec {

bool is_read_only(FunctionCode fc) {
    switch (fc) {
    case FunctionCode::ReadCoils:
    case FunctionCode::ReadDiscreteInputs:
    case FunctionCode::ReadHoldingRegisters:
    case FunctionCode::ReadInputRegisters:
        return true;
    default:
        return false;
    }
}

bool is_write(FunctionCode fc) { return !is_read_only(fc); }

DataType data_type_of(FunctionCode fc) {
    switch (fc) {
    case FunctionCode::ReadCoils:
    case FunctionCode::ReadDiscreteInputs:
    case FunctionCode::WriteSingleCoil:
    case FunctionCode::WriteMultipleCoils:
        return DataType::Bit;
    default:
        return DataType::Register;
    }
}

std::optional<std::pair<std::uint16_t, std::uint16_t>> quantity_limits(FunctionCode fc) {
    switch (fc) {
    case FunctionCode::ReadCoils:
    case FunctionCode::ReadDiscreteInputs:
        return std::pair<std::uint16_t, std::uint16_t>{1, 2000};
    case FunctionCode::ReadHoldingRegisters:
    case FunctionCode::ReadInputRegisters:
        return std::pair<std::uint16_t, std::uint16_t>{1, 125};
    case FunctionCode::WriteSingleCoil:
    case FunctionCode::WriteSingleRegister:
        return std::pair<std::uint16_t, std::uint16_t>{1, 1};
    case FunctionCode::WriteMultipleCoils:
        return std::pair<std::uint16_t, std::uint16_t>{1, 1968};
    case FunctionCode::WriteMultipleRegisters:
        return std::pair<std::uint16_t, std::uint16_t>{1, 123};
    }
    return std::nullopt;
}

std::optional<FunctionCode> parse_function_code(std::uint8_t raw) {
    switch (raw) {
    case 1:
        return FunctionCode::ReadCoils;
    case 2:
        return FunctionCode::ReadDiscreteInputs;
    case 3:
        return FunctionCode::ReadHoldingRegisters;
    case 4:
        return FunctionCode::ReadInputRegisters;
    case 5:
        return FunctionCode::WriteSingleCoil;
    case 6:
        return FunctionCode::WriteSingleRegister;
    case 15:
        return FunctionCode::WriteMultipleCoils;
    case 16:
        return FunctionCode::WriteMultipleRegisters;
    default:
        return std::nullopt;
    }
}

std::size_t Counters::rejected_total() const {
    return unmapped_unit + broadcast_write + unknown_function + function_not_allowed +
           bad_quantity + bad_address_range + write_protected;
}

Router::Router(std::vector<DeviceProfile> devices) {
    for (auto& d : devices) {
        devices_.emplace(d.unit_id, std::move(d));
    }
}

Decision Router::reject(Decision d) {
    switch (d) {
    case Decision::RejectedUnmappedUnit:
        ++counters_.unmapped_unit;
        break;
    case Decision::RejectedBroadcastWrite:
        ++counters_.broadcast_write;
        break;
    case Decision::RejectedUnknownFunction:
        ++counters_.unknown_function;
        break;
    case Decision::RejectedFunctionNotAllowed:
        ++counters_.function_not_allowed;
        break;
    case Decision::RejectedQuantity:
        ++counters_.bad_quantity;
        break;
    case Decision::RejectedAddressRange:
        ++counters_.bad_address_range;
        break;
    case Decision::RejectedWriteProtected:
        ++counters_.write_protected;
        break;
    case Decision::Forwarded:
        break;
    }
    return d;
}

Decision Router::route(const Request& req) {
    const auto raw_fc = parse_function_code(req.function_code);
    if (!raw_fc) {
        return reject(Decision::RejectedUnknownFunction);
    }
    const FunctionCode fc = *raw_fc;

    // 广播单元：只允许只读功能，写请求一律拒绝，避免落到相邻设备。
    if (req.unit_id == kBroadcastUnit) {
        if (is_write(fc)) {
            return reject(Decision::RejectedBroadcastWrite);
        }
        ++counters_.forwarded;
        return Decision::Forwarded;
    }

    // 单元标识必须命中设备映射白名单。
    const auto it = devices_.find(req.unit_id);
    if (it == devices_.end()) {
        return reject(Decision::RejectedUnmappedUnit);
    }
    const DeviceProfile& dev = it->second;

    // 功能码必须在该设备的允许列表内。
    if (dev.allowed_functions.find(fc) == dev.allowed_functions.end()) {
        return reject(Decision::RejectedFunctionNotAllowed);
    }

    // 数量逐项核对（含单点写固定为 1）。
    const auto limits = quantity_limits(fc);
    if (!limits || req.quantity < limits->first || req.quantity > limits->second) {
        return reject(Decision::RejectedQuantity);
    }

    // 写保护设备拒绝一切写操作。
    if (dev.write_protected && is_write(fc)) {
        return reject(Decision::RejectedWriteProtected);
    }

    // 地址范围与数据类型核对：整个区间须落在该类型某一条映射内，
    // 写操作还要求该区间可写。用 32 位运算防止 address+quantity 溢出。
    const DataType type = data_type_of(fc);
    const std::uint32_t begin = req.address;
    const std::uint32_t end = begin + req.quantity; // 开区间上界
    const auto map_it = dev.address_map.find(type);
    if (map_it != dev.address_map.end()) {
        for (const AddressRange& range : map_it->second) {
            const std::uint32_t r_begin = range.start;
            const std::uint32_t r_end = r_begin + range.count;
            if (begin >= r_begin && end <= r_end) {
                if (is_write(fc) && !range.writable) {
                    break;
                }
                ++counters_.forwarded;
                return Decision::Forwarded;
            }
        }
    }
    return reject(Decision::RejectedAddressRange);
}

} // namespace netsec
