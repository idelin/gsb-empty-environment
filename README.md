# drc — 无后台线程的代次延迟回收（C++17）

资源句柄先被标记为退休，等所有已开始的读取结束后才真正释放。
回收动作只发生在调用方线程的 `retire()` / `collect()` 内，库本身不创建任何后台线程。

## 结构

- `include/drc/epoch_reclaimer.hpp` — 代次回收器。
  - 每个线程首次 `pin()` 时注册到线程注册表，退出时自动注销。
  - `Pin` 是 RAII 读侧临界区：可移动、禁止复制；析构即解除钉住。
  - 退休对象进入按 `epoch % 3` 分桶的 limbo 列表；当所有线程都不落后于
    当前代次时 `collect()` 推进全局代次，并回收落后两代（必然已无人可达）的桶。
- `include/drc/deferred_cell.hpp` — 句柄层。
  - `DeferredCell<T>` 持有当前发布的对象，`retire()` 将其摘除并延迟销毁。
  - `Handle<T>` 是可复制的 `(cell, generation)` 快照令牌。
  - `try_read(handle)` 先钉住代次，再校验指针非空且代次号未变；
    退休或重新发布后旧句柄永远取不到新守卫——即使新对象复用了旧地址（防 ABA）。
  - `ReaderGuard<T>` 可移动、禁止复制，存活期间对象保证不被回收。

## 验证

`tests/reclamation_test.cpp` 用析构计数器（`Counted`）、人为暂停的读者线程，
以及确定性的地址复用池（`ReusePool`）验证：

1. 守卫可移动、禁止复制（编译期 `static_assert`）。
2. 退休后旧句柄立即取不到新守卫。
3. 读者暂停期间对象不被析构（`destroyed` 不变、limbo 中挂起 1 个），
   读者恢复后仍读到旧对象的值，随后回收完成。
4. 旧地址被新对象复用后，旧句柄仍被拒绝，新句柄读到新对象。

## 构建与测试

```sh
make test          # 构建并运行测试
```

已在 Apple Clang (arm64) 下通过 `-Wall -Wextra -Werror`，
以及 ThreadSanitizer 与 Address/UB Sanitizer 验证。
