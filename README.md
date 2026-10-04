# ev::Event — 快照分发型事件监听库 (C++17)

仅头文件库(`include/event.hpp`), 无依赖, `-std=c++17 -pthread` 即可使用。

## 语义

- **快照分发**: `fire()` 开始时对存活监听器取快照, 随后在**不持有任何全局锁**的情况下逐个回调。回调中可自由订阅/注销(包括注销自身)。
- **注销即隔离**: `unsubscribe()` 返回后, 该监听器不再进入任何新一轮分发; 本轮已准入(在途)的调用可正常结束。
- **可等待的注销句柄**: `Subscription::wait()` 阻塞至该监听器的在途调用全部结束, 之后销毁回调捕获的资源是安全的。`unsubscribe_and_wait()` 一步到位; 句柄析构时自动执行。
- **自注销安全**: 监听器在自己的回调中注销/销毁自身句柄不会死锁(`wait` 检测到自调用立即返回)。

## 用法

```cpp
ev::Event<int> ev;

auto sub = ev.subscribe([](int v) { /* ... */ });
ev.fire(42);

sub.unsubscribe();        // 标记注销, 立即返回
sub.wait();               // 等待在途调用结束
// 或: sub.unsubscribe_and_wait();
```

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build
```

测试覆盖: 快照语义、屏障验证的销毁时机、自注销、注销后不再进入新一轮、多线程压力(TSan 干净)。
