# tlpool —— 带线程本地归还缓存的对象池（C++17）

单头文件实现：`thread_local_pool.h`。测试：`tests.cpp`。

## 所有权图

```
                        ┌──────────────────────────────┐
                        │ Pool<T> 句柄（可随时销毁）      │
                        │   shared_ptr<Core> core_      │
                        └──────────────┬───────────────┘
                                       │ strong
        ┌──────────────────────────────┼───────────────────────────┐
        │                              │                           │
┌───────▼───────────┐        ┌─────────▼──────────┐      ┌─────────▼──────────┐
│ Lease（外借对象）   │        │ ThreadCache（线程A） │      │ ThreadCache（线程B） │
│ unique_ptr<T, D>  │        │  shared_ptr<Core>  │      │  shared_ptr<Core>  │
│ D{shared_ptr<Core>}│       │  vector<T*> items  │      │  vector<T*> items  │
└───────┬───────────┘        └─────────┬──────────┘      └─────────┬──────────┘
        │ strong                       │ strong                    │ strong
        └──────────────┬───────────────┴───────────────┬───────────┘
                       ▼                               ▼
              ┌─────────────────────────────────────────────────────┐
              │ Core 控制块（shared 所有权，引用计数续命）              │
              │   mutex + vector<T*> stash + closed + destroy        │
              │   ~Core()：对 stash 中每个对象调用 destroy            │
              └─────────────────────────────────────────────────────┘

对象 T* 的唯一所有权在任一时刻恰好属于以下三者之一（随指针移动而转移）：

    Lease（外借中）  ⇄  ThreadCache.items（线程缓存）  ⇄  Core.stash（全局池）

只有 ~Core() 会析构对象  ⇒  每个对象恰好析构一次。
Core 的存活由 shared_ptr 引用计数保证：Pool 先死、线程后死、
Lease 最迟归还，任意顺序都安全 —— 不依赖池比所有线程活得久。
```

## 协议

### 归还（热路径无锁）
`Lease` 的 deleter 把对象放进**本线程**缓存；缓存攒到 `cache_high`
阈值时，一次性把 `cache_high - cache_keep` 个对象批量刷回 `Core.stash`
（单次加锁），保留 `cache_keep` 个热对象供本线程复用。

### 线程退出
首次归还时建立 `thread_local Sentry`。线程退出时 Sentry 析构，
把本线程所有缓存**整批**刷回各自的 Core 并置 `retired` 标志：

- 未达阈值的残余批次不会随线程退出而遗失；
- Sentry 析构之后（TLS 析构阶段）发生的迟归还，deleter 看到
  `retired` 后绕过已退役的缓存，直接交回 Core —— 不触碰顺序
  不可控的 TLS 对象（TLS 槽位本体是有意常驻的空壳结构，每线程
  一份，退出时已清空，见 `thread_local_pool.h` 中 `TlsSlot` 注释）。

### 池关闭
`close()` 置 `closed` 标志：`lease()` 返回空 Lease；deleter 检测到
`closed` 后绕过线程缓存直接交回 Core，保证对象及时回到全局池，
等待 Core 销毁时统一析构。

### 迟归还 / 池先死
每个 Lease 的 deleter 和每个线程缓存都持有 `shared_ptr<Core>`。
Pool 句柄销毁后 Core 仍被续命；当最后一个引用（最后的 Lease、
最后的线程缓存）消失时，`~Core()` 统一析构 stash 中的全部对象。

## 测试（tests.cpp）

| 测试 | 证明的协议 |
|---|---|
| `test_basic_reuse` | 归还进线程缓存、再借命中复用 |
| `test_batch_threshold` | 攒到 high 阈值才批量回刷，保留 keep 个热对象 |
| `test_thread_exit_flush` | 线程退出时未达阈值的批次整批刷回，不遗失 |
| `test_close_protocol` | 关闭后不再借出；迟归还绕过缓存直接回 Core |
| `test_late_return_after_pool_death` | Pool 销毁后 Lease 才释放，Core 被续命、对象恰好析构一次 |
| `test_late_return_cross_thread_after_pool_death` | 迟归还发生在另一个线程 |
| `test_thread_outlives_pool` | Pool 先于线程销毁，线程退出时缓存仍安全刷回 |
| `test_tls_teardown_late_release` | TLS 析构阶段的迟归还走 retired 直通路径 |
| `test_cross_thread_release` | 借入线程与归还线程不同 |
| `test_stress_exactly_once` | 8 线程 × 2 万次随机借还，`ctor == dtor` |

`Tracked` 类型用全局 live-set 断言：任何地址被析构两次或泄漏都会
立即 abort；进程退出时的 `final_audit`（atexit，在主线程 TLS 析构
之后运行）校验 `ctor 总数 == dtor 总数` 且 live-set 为空。

## 构建与运行

```sh
# 直接编译（ASan + UBSan + LSan）
clang++ -std=c++17 -Wall -Wextra -Werror -g -O1 \
    -fsanitize=address,undefined -pthread tests.cpp -o tests && ./tests

# ThreadSanitizer
clang++ -std=c++17 -fsanitize=thread -pthread tests.cpp -o tests_tsan && ./tests_tsan

# 或 CMake
cmake -B build && cmake --build build && ./build/tlpool_tests
```

## 复杂度

- 借出 / 归还（缓存命中）：无线程间同步，O(1)。
- 批量回刷：每 `cache_high - cache_keep` 次归还摊还一次加锁，O(1) 均摊。
- 线程退出：O(本线程缓存对象数)，单次加锁整批移交。
