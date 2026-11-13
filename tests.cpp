// 三组场景验证：重复释放、改 next 为未对齐地址、正常交替。

#include "netsec.hpp"

#include <cassert>
#include <cstring>
#include <iostream>

namespace {

bool expect_violation(const char* scenario, void (*fn)(), const char* needle) {
    try {
        fn();
    } catch (const netsec::SecurityViolation& e) {
        assert(std::strstr(e.what(), needle) != nullptr);
        std::cout << "PASS " << scenario << " -> terminated: " << e.what() << "\n";
        return true;
    }
    std::cout << "FAIL " << scenario << " -> no violation raised\n";
    return false;
}

// 场景 1：重复释放同一块，key 匹配后遍历链发现重复。
void double_free() {
    netsec::TcacheAllocator a;
    void* x = a.alloc();
    a.free(x);
    a.free(x); // 应触发 double free detected
}

// 场景 2：攻击者把已释放块的 next 改为未对齐地址，取出时还原并拒绝。
void unaligned_next() {
    netsec::TcacheAllocator a;
    void* x = a.alloc();
    void* y = a.alloc();
    a.free(y);
    a.free(x); // 链：x -> y
    const auto base = reinterpret_cast<std::uintptr_t>(y);
    a.write_next(x, reinterpret_cast<void*>(base + 4)); // 未对齐目标
    a.alloc(); // 弹出 x，还原 next 得到未对齐地址，应终止
}

// 场景 3：正常 alloc/free 交替，验证 LIFO 复用与深度变化。
bool normal_interleave() {
    netsec::TcacheAllocator a;
    void* p1 = a.alloc();
    void* p2 = a.alloc();
    assert(p1 != p2);
    assert(a.depth() == 0);

    a.free(p1);
    assert(a.depth() == 1);
    void* p3 = a.alloc(); // LIFO 复用 p1
    assert(p3 == p1);
    assert(a.depth() == 0);

    a.free(p2);
    a.free(p3); // 链：p3 -> p2
    assert(a.depth() == 2);
    void* p4 = a.alloc();
    void* p5 = a.alloc();
    assert(p4 == p3);
    assert(p5 == p2);
    assert(a.depth() == 0);

    // 编码/还原往返一致性
    const auto pos = reinterpret_cast<std::uintptr_t>(p1);
    const auto ptr = reinterpret_cast<std::uintptr_t>(p2);
    assert(netsec::reveal_ptr(pos, netsec::protect_ptr(pos, ptr)) == ptr);

    std::cout << "PASS normal interleave -> LIFO reuse and depth ok\n";
    return true;
}

} // namespace

int main() {
    bool ok = true;
    ok &= expect_violation("double free", double_free, "double free");
    ok &= expect_violation("unaligned next", unaligned_next, "unaligned");
    ok &= normal_interleave();
    std::cout << (ok ? "all tests passed" : "tests FAILED") << "\n";
    return ok ? 0 : 1;
}
