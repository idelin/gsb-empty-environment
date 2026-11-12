#include "netsec.hpp"

#include <unordered_set>

namespace netsec {

namespace {

// 指针是否落在 mempool 地址范围内且按元素大小对齐。
bool ptr_in_pool(const Mbuf* ptr, const Mempool& pool) noexcept
{
    if (pool.base == nullptr || pool.elt_size == 0 || pool.elt_count == 0)
        return false;
    const auto addr = reinterpret_cast<std::uintptr_t>(ptr);
    const auto base = reinterpret_cast<std::uintptr_t>(pool.base);
    if (addr < base)
        return false;
    const std::uintptr_t offset = addr - base;
    // 防 elt_size * elt_count 溢出:先比商再取模。
    if (offset / pool.elt_size >= pool.elt_count)
        return false;
    return offset % pool.elt_size == 0;
}

} // namespace

ChainError validate_chain(const Mbuf* head, const Mempool& pool) noexcept
{
    if (head == nullptr)
        return ChainError::NullHead;

    std::unordered_set<const Mbuf*> visited;
    std::uint64_t total_len = 0;
    std::uint32_t seg_count = 0;
    std::uint32_t pkt_len = 0;
    std::uint16_t nb_segs = 0;

    for (const Mbuf* seg = head; seg != nullptr; seg = seg->next) {
        // 先验证指针归属,再允许解引用。
        if (!ptr_in_pool(seg, pool)) {
            const auto addr = reinterpret_cast<std::uintptr_t>(seg);
            const auto base = reinterpret_cast<std::uintptr_t>(pool.base);
            if (pool.base != nullptr && pool.elt_size != 0 && addr >= base &&
                (addr - base) % pool.elt_size != 0 &&
                (addr - base) / pool.elt_size < pool.elt_count)
                return ChainError::PtrMisaligned;
            return ChainError::PtrOutOfPool;
        }
        if (!visited.insert(seg).second)
            return ChainError::Cycle;
        if (seg->refcnt == 0)
            return ChainError::ZeroRefcnt;
        // 防回绕的边界检查:data_off + data_len <= buf_len。
        if (seg->data_off > seg->buf_len ||
            seg->data_len > static_cast<std::uint32_t>(seg->buf_len) - seg->data_off)
            return ChainError::DataOutOfBounds;

        if (seg_count == 0) {
            pkt_len = seg->pkt_len;
            nb_segs = seg->nb_segs;
        }
        total_len += seg->data_len;
        ++seg_count;
    }

    if (total_len != pkt_len)
        return ChainError::PktLenMismatch;
    if (seg_count != nb_segs)
        return ChainError::SegCountMismatch;
    return ChainError::Ok;
}

const char* to_string(ChainError err) noexcept
{
    switch (err) {
    case ChainError::Ok:              return "ok";
    case ChainError::NullHead:        return "null head";
    case ChainError::PtrOutOfPool:    return "mbuf pointer outside mempool";
    case ChainError::PtrMisaligned:   return "mbuf pointer misaligned";
    case ChainError::ZeroRefcnt:      return "zero refcount";
    case ChainError::DataOutOfBounds: return "data_off + data_len exceeds buf_len";
    case ChainError::PktLenMismatch:  return "segment lengths != pkt_len";
    case ChainError::SegCountMismatch:return "nb_segs != actual segment count";
    case ChainError::Cycle:           return "cycle in chain";
    }
    return "unknown";
}

} // namespace netsec
