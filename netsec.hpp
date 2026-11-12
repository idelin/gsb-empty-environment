#pragma once

#include <cstddef>
#include <cstdint>
#include <unordered_set>
#include <vector>

namespace netsec {

// 包操作码（包头高 16 位）
inline constexpr uint32_t kOpcodeWriteRegister = 0x01;
inline constexpr uint32_t kOpcodeIndirectJump = 0x02;

// 包头占 1 个 DWORD；低 16 位为负载 DWORD 数
inline constexpr uint32_t kHeaderSizeBytes = 4;
inline constexpr uint64_t kDefaultJumpAlignment = 4;
inline constexpr std::size_t kDefaultMaxPackets = 256;
inline constexpr std::size_t kDefaultMaxJumpDepth = 16;

// 校验失败原因；Ok 表示缓冲可安全提交
enum class ValidationError {
    Ok,
    EmptyBuffer,
    InvalidAlignment,
    TruncatedHeader,
    ZeroLength,
    PayloadOutOfBounds,
    UnknownOpcode,
    InvalidPayloadLength,
    RegisterNotAllowed,
    PacketBudgetExceeded,
    JumpDepthExceeded,
    CircularChain,
    JumpTargetOutOfRange,
    JumpTargetMisaligned,
    JumpTargetInsidePacket,
};

struct ValidationResult {
    ValidationError error = ValidationError::Ok;
    // 出错包在缓冲内的字节偏移（无包上下文时为 0 或截断位置）
    std::size_t offset = 0;
    // 出错相关绝对地址（跳转目标 / 寄存器号 / 缓冲基址）
    std::uint64_t address = 0;

    constexpr bool ok() const noexcept { return error == ValidationError::Ok; }
    constexpr explicit operator bool() const noexcept { return ok(); }
};

// 进程已登记的可寻址区间（GPU/进程虚拟地址）
struct AddressRange {
    std::uint64_t base = 0;
    std::uint64_t size = 0;
};

// 待校验的提交缓冲：data 指向本地映射，base_address 是其在进程地址空间中的地址
struct CommandBufferView {
    const std::uint8_t* data = nullptr;
    std::uint64_t base_address = 0;
    std::uint64_t size_bytes = 0;
};

struct ValidatorLimits {
    std::size_t max_packets = kDefaultMaxPackets;
    std::size_t max_jump_depth = kDefaultMaxJumpDepth;
    std::uint64_t jump_target_alignment = kDefaultJumpAlignment;
};

// 线程安全约定：配置完成后可多线程并发调用 validate()
class SubmissionValidator {
public:
    SubmissionValidator() = default;
    explicit SubmissionValidator(ValidatorLimits limits);

    // 登记允许写入的寄存器号；重复登记返回 false
    bool addAllowedRegister(std::uint32_t reg);
    // 登记合法跳转区间；零长度或 base+size 溢出返回 false
    bool addAddressRange(std::uint64_t base, std::uint64_t size);

    const ValidatorLimits& limits() const noexcept;

    // 校验入口：先线性结构扫描，再模拟跳转控制流（预算 + 环检测）
    ValidationResult validate(const CommandBufferView& buffer) const;

    static const char* errorName(ValidationError error) noexcept;

private:
    struct JumpSite {
        std::size_t offset;
        std::uint64_t target;
    };

    const AddressRange* findRange(std::uint64_t address) const;

    std::unordered_set<std::uint32_t> allowed_registers_;
    std::vector<AddressRange> ranges_;
    ValidatorLimits limits_;
};

// 小端编解码辅助，保证跨平台与线上线协议一致
inline std::uint32_t readLE32(const std::uint8_t* p) noexcept {
    return static_cast<std::uint32_t>(p[0]) |
           (static_cast<std::uint32_t>(p[1]) << 8) |
           (static_cast<std::uint32_t>(p[2]) << 16) |
           (static_cast<std::uint32_t>(p[3]) << 24);
}

inline void writeLE32(std::uint8_t* p, std::uint32_t value) noexcept {
    p[0] = static_cast<std::uint8_t>(value);
    p[1] = static_cast<std::uint8_t>(value >> 8);
    p[2] = static_cast<std::uint8_t>(value >> 16);
    p[3] = static_cast<std::uint8_t>(value >> 24);
}

inline std::uint32_t makePacketHeader(std::uint16_t opcode,
                                      std::uint16_t payload_dwords) noexcept {
    return static_cast<std::uint32_t>(payload_dwords) |
           (static_cast<std::uint32_t>(opcode) << 16);
}

}  // namespace netsec
