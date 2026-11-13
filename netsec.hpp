#pragma once

#include <cstddef>
#include <cstdint>
#include <stdexcept>
#include <string>
#include <vector>

namespace netsec {

// glibc 风格 safe-linking：存放时编码，取出时还原。
// pos 为 next 字段所在（块）地址，ptr 为真实指针。
std::uintptr_t protect_ptr(std::uintptr_t pos, std::uintptr_t ptr) noexcept;
std::uintptr_t reveal_ptr(std::uintptr_t pos, std::uintptr_t enc) noexcept;

// 安全检查失败。库内以异常上报，真实部署中等价于 abort()。
class SecurityViolation : public std::runtime_error {
public:
    explicit SecurityViolation(const std::string& what) : std::runtime_error(what) {}
};

// tcache 式单链表空闲链分配器（固定大小块，LIFO）。
class TcacheAllocator {
public:
    static constexpr std::size_t kAlignment = 16;

    explicit TcacheAllocator(std::size_t chunk_size = 64);
    TcacheAllocator(const TcacheAllocator&) = delete;
    TcacheAllocator& operator=(const TcacheAllocator&) = delete;
    ~TcacheAllocator();

    // 空闲链非空则弹出链首（还原 next 并做 16 字节对齐检查），否则向 OS 申请新块。
    void* alloc();
    // 块内 key 等于本分配器随机值时，遍历空闲链检测重复释放。
    void free(void* p);

    // 模拟攻击者通过悬空指针把已释放块的 next 改写为指向 target。
    // 攻击者已知 safe-linking 方案与堆地址，因此写入的是编码后的值。
    void write_next(void* chunk, void* target) noexcept;

    std::size_t depth() const noexcept { return count_; }
    std::uintptr_t key() const noexcept { return key_; }

private:
    struct FreeChunk {
        std::uintptr_t next; // protect_ptr 编码后的 next
        std::uintptr_t key;  // tcache key，用于重复释放检测
    };

    [[noreturn]] void fail(const char* reason) const;

    FreeChunk* head_ = nullptr;
    std::size_t count_ = 0;
    std::size_t chunk_size_;
    std::uintptr_t key_;
    std::vector<void*> os_blocks_; // 从 OS 申请的块，析构时统一释放
};

} // namespace netsec
