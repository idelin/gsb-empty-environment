#pragma once

#include <array>
#include <cstdint>

#include "ghcb_protocol.hpp"

namespace netsec {

// 响应校验失败类别，供 #VC 处理程序按类别做错误处置。
enum class ValidationError {
    None,
    ExitCodeMismatch,      // 回填的 sw_exit_code 与请求不一致
    ExitCodeBitClear,      // sw_exit_code 的有效位未置
    HypervisorError,       // sw_exit_info_1 非 0，宿主声明模拟失败
    UnexpectedField,       // valid_bitmap 出现该退出码不允许的数据字段
    MissingField,          // 必需的返回字段对应有效位未置
    ScratchBitClear,       // 用到 scratch 但其有效位未置
    ScratchOutOfRange,     // scratch 未落在 GHCB 共享区内
    BadPortWidth,          // 端口访问宽度不是 1/2/4 字节
    UntrustedCpuidResult,  // CPUID 特性位不是可信表条目的子集
};

struct ValidationResult {
    ValidationError error = ValidationError::None;
    std::uint64_t rax = 0;
    std::uint64_t rbx = 0;
    std::uint64_t rcx = 0;
    std::uint64_t rdx = 0;

    bool ok() const { return error == ValidationError::None; }
    explicit operator bool() const { return ok(); }
};

// 调用方提供的可信 CPUID 表。表项代表该 leaf/subleaf 允许暴露给客户机的
// 最大特性集合：宿主回填值的每一位都必须是表项的子集，超出位清零。
struct CpuidTrustEntry {
    std::uint32_t leaf;
    std::uint32_t subleaf;
    std::uint32_t rax;
    std::uint32_t rbx;
    std::uint32_t rcx;
    std::uint32_t rdx;
};

class CpuidTrustTable {
public:
    static constexpr std::size_t kMaxEntries = 64;

    void clear() { count_ = 0; }

    bool add(const CpuidTrustEntry& entry) {
        if (count_ == entries_.size()) {
            return false;
        }
        entries_[count_++] = entry;
        return true;
    }

    const CpuidTrustEntry* find(std::uint32_t leaf,
                                std::uint32_t subleaf) const {
        for (std::size_t i = 0; i < count_; ++i) {
            if (entries_[i].leaf == leaf &&
                entries_[i].subleaf == subleaf) {
                return &entries_[i];
            }
        }
        return nullptr;
    }

    std::size_t size() const { return count_; }

private:
    std::array<CpuidTrustEntry, kMaxEntries> entries_{};
    std::size_t count_ = 0;
};

struct CpuidRequest {
    std::uint32_t leaf;
    std::uint32_t subleaf;
};

struct PortIoRequest {
    std::uint16_t port;
    std::uint8_t width_bytes;  // 仅允许 1、2、4
    bool is_read;
    // 端口写时 scratch 承载写数据缓冲；端口读时承载读回数据缓冲。
    std::uint32_t scratch_size;
};

struct MsrRequest {
    std::uint32_t index;
    bool is_read;
};

class GhcbResponseValidator {
public:
    // 请求发起前调用：清空 valid_bitmap 及所有协议控制/数据字段，
    // 保证宿主只能通过显式置位的字段回传数据。
    static void reset_for_request(Ghcb& ghcb);

    // 校验 CPUID 响应：四个寄存器均须置位，且特性位是可信表条目的子集。
    static ValidationResult validate_cpuid(const Ghcb& ghcb,
                                           const CpuidRequest& request,
                                           const CpuidTrustTable& trust_table);

    // 校验端口 I/O 响应：读仅允许 rax 且按请求宽度截断；写无返回字段。
    static ValidationResult validate_port_io(const Ghcb& ghcb,
                                             const PortIoRequest& request);

    // 校验 MSR 响应：读仅允许 rcx（GHCB MSR 协议值经 rcx 回传），
    // 写无返回字段；rbx 中的 MSR 索引是请求字段，允许保持置位。
    static ValidationResult validate_msr(const Ghcb& ghcb,
                                         const MsrRequest& request);
};

}  // namespace netsec
