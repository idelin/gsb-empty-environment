#pragma once

#include <cstddef>
#include <cstdint>

namespace netsec {

// 数据面 mbuf 描述符的极简视图,字段布局仿 DPDK rte_mbuf。
// 收包路径上这些字段可能被不可信的客户机驱动污染,
// 解析前必须先经 validate_chain 校验。
struct Mbuf {
    uint16_t data_off; // 数据起始偏移(相对缓冲起点)
    uint16_t buf_len;  // 本段缓冲长度
    uint16_t data_len; // 本段有效数据长度
    uint32_t pkt_len;  // 仅首段有效:整包总长度
    uint16_t nb_segs;  // 仅首段有效:链段数
    uint16_t refcnt;   // 引用计数
    Mbuf* next;        // 下一段,末段为 nullptr
};

// mempool 地址范围:元素从 base 开始,按 elt_size 步长排列。
struct Mempool {
    const char* base;
    std::size_t elt_size;
    std::size_t elt_count;
};

enum class ChainError {
    Ok = 0,
    NullHead,        // 链头为空
    PtrOutOfPool,    // mbuf 指针不在 mempool 地址范围内
    PtrMisaligned,   // mbuf 指针未按元素大小对齐
    ZeroRefcnt,      // 引用计数为零(已释放/伪造)
    DataOutOfBounds, // data_off + data_len 超出 buf_len
    PktLenMismatch,  // 各段 data_len 之和与首段 pkt_len 不等
    SegCountMismatch,// nb_segs 与实际段数不一致
    Cycle,           // 链中出现环
};

// 解析前调用:校验整条 mbuf 链,返回 Ok 才允许继续解析。
// 全程不解引用未通过范围/对齐校验的指针。
ChainError validate_chain(const Mbuf* head, const Mempool& pool) noexcept;

const char* to_string(ChainError err) noexcept;

} // namespace netsec
