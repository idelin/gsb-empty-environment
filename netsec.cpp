#include "netsec.hpp"

#include <algorithm>

namespace netsec {

namespace {

constexpr std::uint32_t kLengthMask = 0xFFFFu;
constexpr std::uint32_t kOpcodeShift = 16u;

bool isAligned(std::uint64_t value, std::uint64_t alignment) noexcept {
    // 对齐要求始终为 2 的幂（默认 4），用位与即可
    return (value & (alignment - 1)) == 0;
}

bool addWouldOverflow(std::uint64_t base, std::uint64_t size) noexcept {
    return size > (UINT64_MAX - base);
}

}  // namespace

SubmissionValidator::SubmissionValidator(ValidatorLimits limits)
    : limits_(limits) {}

bool SubmissionValidator::addAllowedRegister(std::uint32_t reg) {
    return allowed_registers_.insert(reg).second;
}

bool SubmissionValidator::addAddressRange(std::uint64_t base,
                                          std::uint64_t size) {
    if (size == 0 || addWouldOverflow(base, size)) {
        return false;
    }
    ranges_.push_back(AddressRange{base, size});
    return true;
}

const ValidatorLimits& SubmissionValidator::limits() const noexcept {
    return limits_;
}

const AddressRange* SubmissionValidator::findRange(
    std::uint64_t address) const {
    for (const AddressRange& range : ranges_) {
        if (address >= range.base && address < range.base + range.size) {
            return &range;
        }
    }
    return nullptr;
}

ValidationResult SubmissionValidator::validate(
    const CommandBufferView& buffer) const {
    if (buffer.data == nullptr || buffer.size_bytes == 0) {
        return {ValidationError::EmptyBuffer, 0, buffer.base_address};
    }

    const std::uint64_t alignment =
        limits_.jump_target_alignment == 0 ? kDefaultJumpAlignment
                                           : limits_.jump_target_alignment;

    // 基址必须满足目标对齐；长度非 DWORD 整倍数时由阶段 1 截断检测拦截
    if (!isAligned(buffer.base_address, alignment)) {
        return {ValidationError::InvalidAlignment, 0, buffer.base_address};
    }

    if (addWouldOverflow(buffer.base_address, buffer.size_bytes)) {
        return {ValidationError::InvalidAlignment, 0, buffer.base_address};
    }

    const std::uint64_t buffer_end = buffer.base_address + buffer.size_bytes;

    // 阶段 1：线性结构扫描，保证每个包边界与负载都在缓冲内且语义合法
    std::unordered_set<std::size_t> packet_starts;
    std::vector<JumpSite> jump_sites;

    std::size_t offset = 0;
    std::size_t packet_count = 0;
    while (offset < buffer.size_bytes) {
        if (buffer.size_bytes - offset < kHeaderSizeBytes) {
            return {ValidationError::TruncatedHeader, offset, 0};
        }

        const std::uint32_t header = readLE32(buffer.data + offset);
        const std::uint16_t length_dwords =
            static_cast<std::uint16_t>(header & kLengthMask);
        const std::uint16_t opcode =
            static_cast<std::uint16_t>(header >> kOpcodeShift);

        if (length_dwords == 0) {
            return {ValidationError::ZeroLength, offset, opcode};
        }

        if (packet_count >= limits_.max_packets) {
            return {ValidationError::PacketBudgetExceeded, offset,
                    packet_count};
        }

        const std::uint64_t payload_bytes =
            static_cast<std::uint64_t>(length_dwords) * 4u;
        if (payload_bytes > buffer.size_bytes - offset - kHeaderSizeBytes) {
            return {ValidationError::PayloadOutOfBounds, offset,
                    payload_bytes};
        }

        const std::size_t payload_offset = offset + kHeaderSizeBytes;
        const std::size_t next_offset =
            payload_offset + static_cast<std::size_t>(payload_bytes);

        packet_starts.insert(offset);
        ++packet_count;

        switch (opcode) {
            case kOpcodeWriteRegister: {
                // 负载为 (寄存器号, 值) 对，必须为偶数个 DWORD
                if ((length_dwords % 2u) != 0u) {
                    return {ValidationError::InvalidPayloadLength, offset,
                            length_dwords};
                }
                for (std::uint32_t i = 0; i < length_dwords; i += 2u) {
                    const std::uint32_t reg =
                        readLE32(buffer.data + payload_offset + i * 4u);
                    if (allowed_registers_.find(reg) ==
                        allowed_registers_.end()) {
                        return {ValidationError::RegisterNotAllowed, offset,
                                reg};
                    }
                }
                break;
            }
            case kOpcodeIndirectJump: {
                // 固定 2 DWORD 负载：64 位绝对跳转地址（低 32 位在前）
                if (length_dwords != 2u) {
                    return {ValidationError::InvalidPayloadLength, offset,
                            length_dwords};
                }
                const std::uint32_t low = readLE32(buffer.data +
                                                    payload_offset);
                const std::uint32_t high = readLE32(buffer.data +
                                                     payload_offset + 4u);
                const std::uint64_t target =
                    static_cast<std::uint64_t>(low) |
                    (static_cast<std::uint64_t>(high) << 32);
                jump_sites.push_back(JumpSite{offset, target});
                break;
            }
            default:
                return {ValidationError::UnknownOpcode, offset, opcode};
        }

        offset = next_offset;
    }

    // 阶段 2：模拟控制流，检查跳转预算、环链与目标合法性。
    // jump_sites 按包在缓冲中的偏移升序（阶段 1 线性追加）
    for (const JumpSite& jump : jump_sites) {
        std::uint64_t target = jump.target;
        std::size_t source = jump.offset;
        std::size_t depth = 0;
        std::unordered_set<std::size_t> visited;

        while (true) {
            if (depth >= limits_.max_jump_depth) {
                return {ValidationError::JumpDepthExceeded, source, depth};
            }
            ++depth;

            if (findRange(target) == nullptr) {
                return {ValidationError::JumpTargetOutOfRange, source,
                        target};
            }
            if (!isAligned(target, alignment)) {
                return {ValidationError::JumpTargetMisaligned, source,
                        target};
            }

            // 目标落在本缓冲内：必须精确落在某个包起始位置
            if (target >= buffer.base_address && target < buffer_end) {
                const std::size_t local_offset =
                    static_cast<std::size_t>(target - buffer.base_address);
                if (packet_starts.find(local_offset) == packet_starts.end()) {
                    return {ValidationError::JumpTargetInsidePacket, source,
                            target};
                }
                if (!visited.insert(local_offset).second) {
                    return {ValidationError::CircularChain, source, target};
                }

                // 从落地包开始顺序执行，找到下一个跳转包；没有则链终止
                auto next_jump = std::lower_bound(
                    jump_sites.begin(), jump_sites.end(), local_offset,
                    [](const JumpSite& site, std::size_t value) {
                        return site.offset < value;
                    });
                if (next_jump == jump_sites.end()) {
                    break;
                }
                source = next_jump->offset;
                target = next_jump->target;
            } else {
                break;  // 目标位于其他已登记区间，本地控制流分析结束
            }
        }
    }

    return {ValidationError::Ok, 0, 0};
}

const char* SubmissionValidator::errorName(ValidationError error) noexcept {
    switch (error) {
        case ValidationError::Ok: return "Ok";
        case ValidationError::EmptyBuffer: return "EmptyBuffer";
        case ValidationError::InvalidAlignment: return "InvalidAlignment";
        case ValidationError::TruncatedHeader: return "TruncatedHeader";
        case ValidationError::ZeroLength: return "ZeroLength";
        case ValidationError::PayloadOutOfBounds: return "PayloadOutOfBounds";
        case ValidationError::UnknownOpcode: return "UnknownOpcode";
        case ValidationError::InvalidPayloadLength:
            return "InvalidPayloadLength";
        case ValidationError::RegisterNotAllowed: return "RegisterNotAllowed";
        case ValidationError::PacketBudgetExceeded:
            return "PacketBudgetExceeded";
        case ValidationError::JumpDepthExceeded: return "JumpDepthExceeded";
        case ValidationError::CircularChain: return "CircularChain";
        case ValidationError::JumpTargetOutOfRange:
            return "JumpTargetOutOfRange";
        case ValidationError::JumpTargetMisaligned:
            return "JumpTargetMisaligned";
        case ValidationError::JumpTargetInsidePacket:
            return "JumpTargetInsidePacket";
    }
    return "Unknown";
}

}  // namespace netsec
