#include "netsec.hpp"

#include <cstdio>

namespace {

using netsec::ChainError;
using netsec::Mbuf;
using netsec::Mempool;

int failures = 0;

void expect(const char* name, ChainError got, ChainError want)
{
    if (got == want) {
        std::printf("[PASS] %-28s -> %s\n", name, netsec::to_string(got));
    } else {
        std::printf("[FAIL] %-28s -> got %s, want %s\n", name,
                    netsec::to_string(got), netsec::to_string(want));
        ++failures;
    }
}

// 用静态数组模拟 mempool,元素即 Mbuf,步长 sizeof(Mbuf)。
constexpr std::size_t kPoolSize = 8;
Mbuf g_pool[kPoolSize];
Mempool g_pool_desc{reinterpret_cast<const char*>(&g_pool[0]), sizeof(Mbuf),
                    kPoolSize};

Mbuf make_seg(std::uint16_t data_len, Mbuf* next = nullptr)
{
    Mbuf m{};
    m.data_off = 64;
    m.buf_len = 2048;
    m.data_len = data_len;
    m.pkt_len = 0;
    m.nb_segs = 1;
    m.refcnt = 1;
    m.next = next;
    return m;
}

void reset_pool()
{
    for (auto& m : g_pool)
        m = make_seg(0);
}

void test_valid_chain()
{
    reset_pool();
    g_pool[0] = make_seg(100, &g_pool[1]);
    g_pool[1] = make_seg(200, &g_pool[2]);
    g_pool[2] = make_seg(300);
    g_pool[0].pkt_len = 600;
    g_pool[0].nb_segs = 3;
    expect("valid chain", netsec::validate_chain(&g_pool[0], g_pool_desc),
           ChainError::Ok);
}

void test_valid_single_segment()
{
    reset_pool();
    g_pool[0] = make_seg(512);
    g_pool[0].pkt_len = 512;
    expect("valid single segment",
           netsec::validate_chain(&g_pool[0], g_pool_desc), ChainError::Ok);
}

void test_data_off_overflow()
{
    reset_pool();
    g_pool[0] = make_seg(100);
    g_pool[0].pkt_len = 100;
    g_pool[0].data_off = 2000; // 2000 + 100 > 2048
    expect("data_off + data_len > buf_len",
           netsec::validate_chain(&g_pool[0], g_pool_desc),
           ChainError::DataOutOfBounds);
}

void test_data_len_wraparound()
{
    reset_pool();
    g_pool[0] = make_seg(0);
    g_pool[0].data_off = 2048; // 恰在边界,合法
    g_pool[0].data_len = 1;    // 2048 + 1 越界
    g_pool[0].pkt_len = 1;
    expect("offset at edge + len 1",
           netsec::validate_chain(&g_pool[0], g_pool_desc),
           ChainError::DataOutOfBounds);
}

void test_pkt_len_mismatch()
{
    reset_pool();
    g_pool[0] = make_seg(100, &g_pool[1]);
    g_pool[1] = make_seg(200);
    g_pool[0].pkt_len = 999; // 实际总和 300
    g_pool[0].nb_segs = 2;
    expect("sum(data_len) != pkt_len",
           netsec::validate_chain(&g_pool[0], g_pool_desc),
           ChainError::PktLenMismatch);
}

void test_nb_segs_mismatch()
{
    reset_pool();
    g_pool[0] = make_seg(100, &g_pool[1]);
    g_pool[1] = make_seg(200);
    g_pool[0].pkt_len = 300;
    g_pool[0].nb_segs = 5; // 实际只有 2 段
    expect("nb_segs != actual count",
           netsec::validate_chain(&g_pool[0], g_pool_desc),
           ChainError::SegCountMismatch);
}

void test_cycle()
{
    reset_pool();
    g_pool[0] = make_seg(100, &g_pool[1]);
    g_pool[1] = make_seg(200, &g_pool[0]); // 指回首段成环
    g_pool[0].pkt_len = 300;
    g_pool[0].nb_segs = 2;
    expect("cycle in chain", netsec::validate_chain(&g_pool[0], g_pool_desc),
           ChainError::Cycle);
}

void test_self_cycle()
{
    reset_pool();
    g_pool[0] = make_seg(100, &g_pool[0]);
    g_pool[0].pkt_len = 100;
    expect("self cycle", netsec::validate_chain(&g_pool[0], g_pool_desc),
           ChainError::Cycle);
}

void test_forged_pointer_outside_pool()
{
    reset_pool();
    Mbuf fake = make_seg(100); // 栈上伪造的 mbuf,不在池内
    g_pool[0] = make_seg(100, &fake);
    g_pool[0].pkt_len = 200;
    g_pool[0].nb_segs = 2;
    expect("forged next outside pool",
           netsec::validate_chain(&g_pool[0], g_pool_desc),
           ChainError::PtrOutOfPool);
}

void test_forged_head_pointer()
{
    reset_pool();
    Mbuf fake = make_seg(100);
    fake.pkt_len = 100;
    expect("forged head outside pool",
           netsec::validate_chain(&fake, g_pool_desc),
           ChainError::PtrOutOfPool);
}

void test_misaligned_pointer()
{
    reset_pool();
    g_pool[0] = make_seg(100, &g_pool[1]);
    // 指向池内但偏移 1 字节,未按元素大小对齐
    g_pool[0].next =
        reinterpret_cast<Mbuf*>(reinterpret_cast<char*>(&g_pool[1]) + 1);
    g_pool[0].pkt_len = 200;
    g_pool[0].nb_segs = 2;
    expect("misaligned next pointer",
           netsec::validate_chain(&g_pool[0], g_pool_desc),
           ChainError::PtrMisaligned);
}

void test_zero_refcnt()
{
    reset_pool();
    g_pool[0] = make_seg(100, &g_pool[1]);
    g_pool[1] = make_seg(200);
    g_pool[1].refcnt = 0; // 已释放的段被挂进链
    g_pool[0].pkt_len = 300;
    g_pool[0].nb_segs = 2;
    expect("zero refcount segment",
           netsec::validate_chain(&g_pool[0], g_pool_desc),
           ChainError::ZeroRefcnt);
}

void test_null_head()
{
    expect("null head", netsec::validate_chain(nullptr, g_pool_desc),
           ChainError::NullHead);
}

} // namespace

int main()
{
    test_valid_chain();
    test_valid_single_segment();
    test_data_off_overflow();
    test_data_len_wraparound();
    test_pkt_len_mismatch();
    test_nb_segs_mismatch();
    test_cycle();
    test_self_cycle();
    test_forged_pointer_outside_pool();
    test_forged_head_pointer();
    test_misaligned_pointer();
    test_zero_refcnt();
    test_null_head();

    if (failures != 0) {
        std::printf("%d test(s) failed\n", failures);
        return 1;
    }
    std::printf("all tests passed\n");
    return 0;
}
