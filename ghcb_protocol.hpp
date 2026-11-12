#pragma once

#include <cstddef>
#include <cstdint>
#include <cstring>

namespace netsec {

// GHCB（Guest-Hypervisor Communication Block）协议常量。
//
// GHCB 是一页与宿主共享的 4 KiB 内存，#VC 处理程序通过它请求宿主模拟
// CPUID / MSR / 端口 I/O。宿主不可信（Iago 攻击），响应必须逐字段校验。
//
// 布局（本组件使用的自洽 ABI，语义与 AMD GHCB 规范一致）：
//   [0x0000, 0x0800)  save area，含寄存器、sw_* 控制字段与 valid_bitmap
//   [0x0800, 0x1000)  shared buffer，唯一允许 sw_scratch 指向的区域
//
// valid_bitmap 的第 N 位校验 save area 中字节偏移 8*N 处的 8 字节字段。
inline constexpr std::uint32_t kGhcbPageSize = 0x1000;
inline constexpr std::uint32_t kGhcbSaveAreaSize = 0x0800;
inline constexpr std::uint32_t kGhcbSharedBufferOffset = kGhcbSaveAreaSize;
inline constexpr std::uint32_t kGhcbSharedBufferSize = 0x0800;

// 通用寄存器 qword 在 save area 中的字节偏移（位图位号 = 偏移 / 8）。
inline constexpr std::uint32_t kGhcbRaxOffset = 0x0100;
inline constexpr std::uint32_t kGhcbRbxOffset = 0x0108;
inline constexpr std::uint32_t kGhcbRcxOffset = 0x0110;
inline constexpr std::uint32_t kGhcbRdxOffset = 0x0118;

// #VC 协议控制字段。
inline constexpr std::uint32_t kGhcbSwExitCodeOffset = 0x0300;
inline constexpr std::uint32_t kGhcbSwExitInfo1Offset = 0x0308;
inline constexpr std::uint32_t kGhcbSwExitInfo2Offset = 0x0310;
inline constexpr std::uint32_t kGhcbSwScratchOffset = 0x0318;

// 256 字节能覆盖 save area 全部 2048 个 qword 位。
inline constexpr std::uint32_t kGhcbValidBitmapOffset = 0x0400;
inline constexpr std::uint32_t kGhcbValidBitmapSize = 0x0100;

enum class GhcbField : unsigned {
    Rax = 0,
    Rbx = 1,
    Rcx = 2,
    Rdx = 3,
};

inline constexpr std::uint32_t ghcb_field_offset(GhcbField field) {
    return kGhcbRaxOffset + static_cast<unsigned>(field) * sizeof(std::uint64_t);
}

inline constexpr unsigned ghcb_field_bit(GhcbField field) {
    return ghcb_field_offset(field) / sizeof(std::uint64_t);
}

// SVM 退出码（GHCB sw_exit_code 取值）。
enum class GhcbExitCode : std::uint64_t {
    Cpuid = 0x72,
    IoIo = 0x7b,
    MsrRead = 0x7c,
    MsrWrite = 0x7d,
};

#pragma pack(push, 1)

struct GhcbSaveArea {
    std::uint8_t reserved_before_gpr[kGhcbRaxOffset];
    std::uint64_t rax;
    std::uint64_t rbx;
    std::uint64_t rcx;
    std::uint64_t rdx;
    std::uint8_t reserved_before_sw[kGhcbSwExitCodeOffset -
                                    (kGhcbRdxOffset + sizeof(std::uint64_t))];
    std::uint64_t sw_exit_code;
    std::uint64_t sw_exit_info_1;
    std::uint64_t sw_exit_info_2;
    std::uint64_t sw_scratch;
    std::uint8_t reserved_before_bitmap[kGhcbValidBitmapOffset -
                                        (kGhcbSwScratchOffset +
                                         sizeof(std::uint64_t))];
    std::uint8_t valid_bitmap[kGhcbValidBitmapSize];
    std::uint8_t reserved_tail[kGhcbSaveAreaSize -
                               (kGhcbValidBitmapOffset + kGhcbValidBitmapSize)];
};

struct Ghcb {
    GhcbSaveArea save_area;
    std::uint8_t shared_buffer[kGhcbSharedBufferSize];
};

#pragma pack(pop)

static_assert(offsetof(GhcbSaveArea, rax) == kGhcbRaxOffset, "rax offset");
static_assert(offsetof(GhcbSaveArea, rbx) == kGhcbRbxOffset, "rbx offset");
static_assert(offsetof(GhcbSaveArea, rcx) == kGhcbRcxOffset, "rcx offset");
static_assert(offsetof(GhcbSaveArea, rdx) == kGhcbRdxOffset, "rdx offset");
static_assert(offsetof(GhcbSaveArea, sw_exit_code) == kGhcbSwExitCodeOffset,
              "sw_exit_code offset");
static_assert(offsetof(GhcbSaveArea, sw_exit_info_1) == kGhcbSwExitInfo1Offset,
              "sw_exit_info_1 offset");
static_assert(offsetof(GhcbSaveArea, sw_exit_info_2) == kGhcbSwExitInfo2Offset,
              "sw_exit_info_2 offset");
static_assert(offsetof(GhcbSaveArea, sw_scratch) == kGhcbSwScratchOffset,
              "sw_scratch offset");
static_assert(offsetof(GhcbSaveArea, valid_bitmap) == kGhcbValidBitmapOffset,
              "valid_bitmap offset");
static_assert(sizeof(GhcbSaveArea) == kGhcbSaveAreaSize, "save area size");
static_assert(offsetof(Ghcb, shared_buffer) == kGhcbSharedBufferOffset,
              "shared buffer offset");
static_assert(sizeof(Ghcb) == kGhcbPageSize, "ghcb page size");

inline bool ghcb_bit_is_set(const Ghcb& ghcb, unsigned bit) {
    return (ghcb.save_area.valid_bitmap[bit / 8] &
            static_cast<std::uint8_t>(1u << (bit % 8))) != 0;
}

inline void ghcb_set_bit(Ghcb& ghcb, unsigned bit) {
    ghcb.save_area.valid_bitmap[bit / 8] |=
        static_cast<std::uint8_t>(1u << (bit % 8));
}

inline void ghcb_clear_bit(Ghcb& ghcb, unsigned bit) {
    ghcb.save_area.valid_bitmap[bit / 8] &=
        static_cast<std::uint8_t>(~(1u << (bit % 8)));
}

inline std::uint64_t ghcb_read_offset(const Ghcb& ghcb,
                                      std::uint32_t offset) {
    std::uint64_t value = 0;
    std::memcpy(&value,
                reinterpret_cast<const std::uint8_t*>(&ghcb) + offset,
                sizeof(value));
    return value;
}

inline void ghcb_write_offset(Ghcb& ghcb, std::uint32_t offset,
                              std::uint64_t value) {
    std::memcpy(reinterpret_cast<std::uint8_t*>(&ghcb) + offset, &value,
                sizeof(value));
}

inline std::uint64_t ghcb_read_field(const Ghcb& ghcb, GhcbField field) {
    return ghcb_read_offset(ghcb, ghcb_field_offset(field));
}

inline void ghcb_write_field(Ghcb& ghcb, GhcbField field,
                             std::uint64_t value) {
    ghcb_write_offset(ghcb, ghcb_field_offset(field), value);
}

}  // namespace netsec
