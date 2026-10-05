# tlpool — 带线程本地归还缓存的对象池（C++17）

归还路径先进入**线程本地（TLS）缓存**，攒到阈值 `flushThreshold` 后加一次锁、批量交回全局空闲栈；
借出路径直接从全局空闲栈弹出，空手时由工厂新建。头文件库：`include/tlpool/pool.hpp`。

## 所有权图

```
                        ┌────────────────────────────────────────────┐
                        │               SharedState<T>               │
                        │  mutex                                     │
  ┌──────────┐ shared   │  freeList : vector<T*> ────── owns ──────▶ T*（空闲）
  │  Pool<T> │ ──ptr──▶ │  closed : bool                             │
  └──────────┘          │  factory / deleter / flushThreshold        │
       │ acquire        │  stats: created/destroyed/batchesFlushed   │
       ▼                └────────────────────────────────────────────┘
  ┌──────────┐ shared      ▲                          ▲
  │  Ptr<T>  │ ──ptr───────┘                          │ returnBatch（每批一次锁）
  │（外借句柄）│                                        │
  └──────────┘          ┌─────────────────────────────┴─────────────┐
       │ reset          │  TLS 缓存（每线程 × 每池一个 TlsEntry）     │
       └──────────────▶ │  vector<T*> objs ────────── owns ───────▶ T*（缓存中）
                        │  shared_ptr<SharedState> state            │
                        └────────────────────────────────────────────┘
```

**共享所有权**：`SharedState` 由 `Pool`、每个存活的外借句柄 `Ptr`、每个非空 TLS 缓存条目
共同以 `shared_ptr` 持有。因此**池对象不需要比任何线程活得久**——`Pool` 析构后，
状态对象被句柄与 TLS 条目续命，直到最后一个引用消失。

**不变式**：任一时刻，每个 `T*` 恰好处于四种状态之一，且归属唯一：

1. 外借中（在某个 `Ptr` 内）；
2. 缓存在某个线程的某个 `TlsEntry::objs` 中；
3. 空闲在 `SharedState::freeList` 中；
4. 已析构。

所有交接都是移动语义（指针从唯一来源移到唯一去向），不存在复制，因此
**每个对象恰好被析构一次**。析构点只有三处：

| # | 析构点 | 触发 |
|---|--------|------|
| 1 | `~SharedState()` | 最后一个 shared 引用消失时，销毁 freeList 残余 |
| 2 | `returnBatch()`（closed 分支） | 池已关闭，归还批次立即销毁 |
| 3 | `close()` | 关闭时抽干并销毁全局空闲栈 |

## 三个协议

- **线程退出**：线程死亡时 TLS 缓存 map 析构，每个 `TlsEntry` 的析构函数把
  不足阈值的残余批次 `returnBatch` 给全局状态（条目内的 `shared_ptr` 保证状态仍存活）。
  线程退出不会遗失任何批次。
- **池关闭**：`close()` 置 `closed` 并抽干销毁空闲栈（幂等）。此后所有归还——
  阈值批量、线程退出冲刷、外借对象迟归还——都在 `returnBatch` 的 closed 分支
  被立即析构，不再入池；`acquire` 抛 `std::logic_error`。
- **迟归还**：外借句柄 `Ptr` 自带 `shared_ptr<SharedState>`，即使 `Pool` 对象
  已销毁、甚至已 `close()`，`reset()`/析构依然安全：池开着就入 TLS 缓存，
  关了就当场析构。

## 测试（`tests/pool_tests.cpp`）

用带原子计数器的 `Tracked` 类型证明「恰好析构一次」：析构时 `live` 减一，
若减到负数（双重析构）置 `doubleFree`；每个用例结束断言
`live == 0 && ctors == dtors && !doubleFree`。

| 用例 | 证明点 |
|------|--------|
| `basic_reuse` | 归还对象被后续 acquire 复用 |
| `batch_threshold` | 阈值前 `freeCount==0`，第 N 个归还触发恰好一次批量 flush |
| `thread_exit_flush` | 线程带着 3 个（< 阈值 8）缓存对象退出，join 后 `freeCount==3`，批次不遗失 |
| `close_with_outstanding` | close 销毁空闲对象；外借对象迟归还被立即析构 |
| `pool_destroyed_before_threads` | 多线程借还期间池对象可安全析构 |
| `pool_dies_handles_outlive` | 池先死、句柄后归还：状态靠 shared 所有权续命，最终全部析构 |
| `late_return_cross_thread` | 关闭后由另一线程迟归还，恰好析构一次 |
| `stress` | 8 线程随机借还、运行中 close，~500 万次构造/析构计数相等 |

## 构建与运行

```sh
cmake -B build && cmake --build build && ./build/pool_tests
# 或带 ASan+UBSan：
cmake -B build-asan -DTLPOOL_SANITIZERS=ON && cmake --build build-asan && ./build-asan/pool_tests
# TSan：
clang++ -std=c++17 -fsanitize=thread -g -Iinclude tests/pool_tests.cpp -pthread -o /tmp/tsan && /tmp/tsan
```

已知限制：TLS 缓存在线程退出时冲刷，因此不要把 `Ptr` 存进比池缓存
**更晚析构**的其他 `thread_local` 对象里（TLS 析构顺序问题，与主流
malloc 实现的限制相同）；跨线程归还（如测试 6）不受此限。
