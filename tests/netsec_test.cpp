#include "netsec.hpp"

#include <cstdio>
#include <string>
#include <vector>

using namespace netsec;

namespace {

constexpr std::uint64_t kBufferBase = 0x10000000ULL;
constexpr std::uint64_t kExternalBase = 0x20000000ULL;
constexpr std::uint32_t kRegAllowed = 0x10;
constexpr std::uint32_t kRegPrivileged = 0x20;

int g_failures = 0;

void check(const char* name, bool condition, const char* detail = "") {
    if (condition) {
        std::printf("[ PASS ] %s\n", name);
    } else {
        std::printf("[ FAIL ] %s %s\n", name, detail);
        ++g_failures;
    }
}

// 小端 DWORD 流构造器，模拟驱动侧组包
class PacketBuilder {
public:
    PacketBuilder& header(std::uint16_t opcode,
                          std::uint16_t payload_dwords) {
        word(makePacketHeader(opcode, payload_dwords));
        return *this;
    }

    PacketBuilder& writeReg(std::uint32_t reg, std::uint32_t value) {
        header(kOpcodeWriteRegister, 2).word(reg).word(value);
        return *this;
    }

    PacketBuilder& jump(std::uint64_t target) {
        header(kOpcodeIndirectJump, 2)
            .word(static_cast<std::uint32_t>(target))
            .word(static_cast<std::uint32_t>(target >> 32));
        return *this;
    }

    PacketBuilder& word(std::uint32_t value) {
        writeLE32(push(), value);
        return *this;
    }

    PacketBuilder& rawByte(std::uint8_t value) {
        bytes_.push_back(value);
        return *this;
    }

    const std::vector<std::uint8_t>& bytes() const { return bytes_; }
    std::uint8_t* mutable_data() { return bytes_.data(); }

private:
    std::uint8_t* push() {
        bytes_.insert(bytes_.end(), 4, 0);
        return bytes_.data() + bytes_.size() - 4;
    }

    std::vector<std::uint8_t> bytes_;
};

SubmissionValidator makeValidator() {
    ValidatorLimits limits;
    limits.max_packets = 8;
    limits.max_jump_depth = 3;
    limits.jump_target_alignment = 4;

    SubmissionValidator validator(limits);
    validator.addAllowedRegister(kRegAllowed);
    validator.addAddressRange(kBufferBase, 0x1000);
    validator.addAddressRange(kExternalBase, 0x1000);
    return validator;
}

CommandBufferView view(const std::vector<std::uint8_t>& bytes,
                       std::uint64_t base = kBufferBase) {
    return CommandBufferView{bytes.data(), base, bytes.size()};
}

}  // namespace

int main() {
    const SubmissionValidator validator = makeValidator();

    // 1. 合法缓冲：白名单写寄存器，顺序收尾
    {
        PacketBuilder b;
        b.writeReg(kRegAllowed, 0xDEADBEEF).writeReg(kRegAllowed, 0x1);
        const auto r = validator.validate(view(b.bytes()));
        check("legal write-register buffer", r.ok(),
              SubmissionValidator::errorName(r.error));
    }

    // 2. 合法缓冲：本地跳转链 + 外部已登记区间收尾
    {
        PacketBuilder b;
        b.writeReg(kRegAllowed, 1);
        const std::size_t jump_a = b.bytes().size();
        b.jump(0);  // 占位，稍后回填
        b.writeReg(kRegAllowed, 2);
        const std::size_t jump_b = b.bytes().size();
        b.jump(kExternalBase);
        b.writeReg(kRegAllowed, 3);

        auto patch = [&](std::size_t at, std::uint64_t target) {
            writeLE32(b.mutable_data() + at + 4,
                      static_cast<std::uint32_t>(target));
            writeLE32(b.mutable_data() + at + 8,
                      static_cast<std::uint32_t>(target >> 32));
        };
        patch(jump_a, kBufferBase + jump_b);
        patch(jump_b, kExternalBase);

        const auto r = validator.validate(view(b.bytes()));
        check("legal jump chain to external range", r.ok(),
              SubmissionValidator::errorName(r.error));
    }

    // 3. 长度为零的包直接拒绝
    {
        PacketBuilder b;
        b.header(kOpcodeWriteRegister, 0).word(kRegAllowed).word(0);
        const auto r = validator.validate(view(b.bytes()));
        check("zero length packet rejected",
              r.error == ValidationError::ZeroLength);
    }

    // 4. 声明长度越过缓冲尾部：负载越界
    {
        PacketBuilder b;
        b.header(kOpcodeWriteRegister, 4);
        b.word(kRegAllowed).word(0).word(kRegAllowed);  // 只给 3 个 DWORD
        const auto r = validator.validate(view(b.bytes()));
        check("payload out of bounds rejected",
              r.error == ValidationError::PayloadOutOfBounds);
    }

    // 5. 缓冲尾部不足一个完整包头：截断
    {
        PacketBuilder b;
        b.writeReg(kRegAllowed, 0).rawByte(0x01);
        const auto r = validator.validate(view(b.bytes()));
        check("truncated header rejected",
              r.error == ValidationError::TruncatedHeader);
    }

    // 6. 写特权/非白名单寄存器拒绝
    {
        PacketBuilder b;
        b.writeReg(kRegPrivileged, 0x1);
        const auto r = validator.validate(view(b.bytes()));
        check("privileged register rejected",
              r.error == ValidationError::RegisterNotAllowed);
        check("privileged register reports address",
              r.address == kRegPrivileged);
    }

    // 7. 环链：包 A 跳到包 B，包 B 跳回包 A
    {
        PacketBuilder b;
        b.writeReg(kRegAllowed, 0);
        const std::size_t jump_a = b.bytes().size();
        b.jump(0);
        b.writeReg(kRegAllowed, 0);
        const std::size_t jump_b = b.bytes().size();
        b.jump(0);

        writeLE32(b.mutable_data() + jump_a + 4,
                  static_cast<std::uint32_t>(kBufferBase + jump_b));
        writeLE32(b.mutable_data() + jump_b + 4,
                  static_cast<std::uint32_t>(kBufferBase + jump_a));

        const auto r = validator.validate(view(b.bytes()));
        check("circular chain rejected",
              r.error == ValidationError::CircularChain);
    }

    // 8. 自环也是环
    {
        PacketBuilder b;
        b.writeReg(kRegAllowed, 0);
        const std::size_t at = b.bytes().size();
        b.jump(kBufferBase + at);
        const auto r = validator.validate(view(b.bytes()));
        check("self loop rejected",
              r.error == ValidationError::CircularChain);
    }

    // 9. 包总数预算
    {
        ValidatorLimits tight;
        tight.max_packets = 4;
        tight.max_jump_depth = 3;
        SubmissionValidator v(tight);
        v.addAllowedRegister(kRegAllowed);
        v.addAddressRange(kBufferBase, 0x1000);

        PacketBuilder b;
        for (int i = 0; i < 5; ++i) {
            b.writeReg(kRegAllowed, 0);
        }
        const auto r = v.validate(view(b.bytes()));
        check("packet budget exceeded rejected",
              r.error == ValidationError::PacketBudgetExceeded);
    }

    // 10. 跳转深度预算：无环的长链也受预算约束
    {
        ValidatorLimits limits;
        limits.max_packets = 32;
        limits.max_jump_depth = 2;
        SubmissionValidator v(limits);
        v.addAllowedRegister(kRegAllowed);
        v.addAddressRange(kBufferBase, 0x10000);

        PacketBuilder b;
        b.writeReg(kRegAllowed, 0);
        const std::size_t j1 = b.bytes().size();
        b.jump(0);
        b.writeReg(kRegAllowed, 0);
        const std::size_t j2 = b.bytes().size();
        b.jump(0);
        b.writeReg(kRegAllowed, 0);
        const std::size_t j3 = b.bytes().size();
        b.jump(kBufferBase + j3);  // 终点自指，但先触发深度预算

        writeLE32(b.mutable_data() + j1 + 4,
                  static_cast<std::uint32_t>(kBufferBase + j2));
        writeLE32(b.mutable_data() + j2 + 4,
                  static_cast<std::uint32_t>(kBufferBase + j3));

        const auto r = v.validate(view(b.bytes()));
        check("jump depth budget rejected",
              r.error == ValidationError::JumpDepthExceeded);
    }

    // 11. 跳转目标不在任何登记区间
    {
        PacketBuilder b;
        b.jump(0xDEAD0000ULL);
        const auto r = validator.validate(view(b.bytes()));
        check("jump out of registered range rejected",
              r.error == ValidationError::JumpTargetOutOfRange);
    }

    // 12. 跳转目标未对齐
    {
        PacketBuilder b;
        b.jump(kExternalBase + 1);
        const auto r = validator.validate(view(b.bytes()));
        check("misaligned jump target rejected",
              r.error == ValidationError::JumpTargetMisaligned);
    }

    // 13. 未知操作码
    {
        PacketBuilder b;
        b.header(0x09, 1).word(0);
        const auto r = validator.validate(view(b.bytes()));
        check("unknown opcode rejected",
              r.error == ValidationError::UnknownOpcode);
    }

    // 14. 跳转落在包内部而非包起始
    {
        PacketBuilder b;
        b.writeReg(kRegAllowed, 0);
        b.jump(kBufferBase + 4);  // 4 字节对齐，但落在第一个包的负载内部
        const auto r = validator.validate(view(b.bytes()));
        check("jump into packet body rejected",
              r.error == ValidationError::JumpTargetInsidePacket);
    }

    // 15. 空缓冲
    {
        const std::vector<std::uint8_t> empty;
        const auto r = validator.validate(view(empty));
        check("empty buffer rejected",
              r.error == ValidationError::EmptyBuffer);
    }

    // 16. 写寄存器负载不是 (寄存器, 值) 对
    {
        PacketBuilder b;
        b.header(kOpcodeWriteRegister, 1).word(kRegAllowed);
        const auto r = validator.validate(view(b.bytes()));
        check("odd write payload rejected",
              r.error == ValidationError::InvalidPayloadLength);
    }

    // 17. 缓冲基址未对齐
    {
        PacketBuilder b;
        b.writeReg(kRegAllowed, 0);
        const auto r = validator.validate(view(b.bytes(), kBufferBase + 1));
        check("unaligned buffer base rejected",
              r.error == ValidationError::InvalidAlignment);
    }

    // 18. 零长度登记区间被拒绝，且跳转不会命中
    {
        SubmissionValidator v;
        v.addAllowedRegister(kRegAllowed);
        check("zero size range registration refused",
              !v.addAddressRange(kExternalBase, 0));
        PacketBuilder b;
        b.jump(kExternalBase);
        const auto r = v.validate(view(b.bytes()));
        check("jump without valid range rejected",
              r.error == ValidationError::JumpTargetOutOfRange);
    }

    if (g_failures == 0) {
        std::printf("\nAll submission buffer validator tests passed.\n");
        return 0;
    }
    std::printf("\n%d test(s) failed.\n", g_failures);
    return 1;
}
