# swr — 固定窗口滑动读取库

面向大文件的 C++23 读取库：文件被划分为固定大小的滑动窗口，按需映射、限量驻留，
跨窗口读取返回拥有生命周期的视图。

## 设计

- **`IMappingBackend`**（`include/swr/mapping_backend.hpp`）：映射来源抽象。
  `map(offset, length, token)` 返回 RAII 句柄 `Mapping`，析构即解除映射。
  自带 POSIX `MmapBackend`（`include/swr/mmap_backend.hpp`，页对齐由后端内部处理）。
- **`SlidingWindowReader`**（`include/swr/sliding_window_reader.hpp`）：
  - 窗口大小固定（尾部窗口可能更短）；`max_resident_windows` 限制同时驻留窗口数，
    驻留窗口 + 在途加载共同占用槽位。
  - 槽位满时优先驱逐未被借用的窗口；全部被借用则立即返回
    `Error::kBackpressure`（背压），不阻塞、不超限。
  - 同一窗口的并发加载去重：后来者等待首个加载完成，共享一次 `map()`。
  - 映射在缓存锁外执行，慢后端不阻塞无关读；加载失败或取消都会释放槽位，
    后续读取可重试。
- **`ReadView`**（`include/swr/read_view.hpp`）：跨窗口读取结果。每个分片以
  `shared_ptr` 持有其窗口，因此**移动/驱逐/销毁缓存都不会让仍被借用的数据悬空**；
  跨边界数据以 chunk 序列暴露（`for_each_chunk` / `copy_to` / `to_vector`）。
- **取消**：`CancelToken`（`include/swr/cancel_token.hpp`）贯穿同步/异步读取与后端
  `map()`；`read_async` 在工作线程上执行，取消阻塞中的映射后返回 `Error::kCancelled`。

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
./build/swr_tests        # 或 ctest --test-dir build
```

测试（`tests/test_main.cpp`）使用虚拟映射后端
（`tests/virtual_mapping_backend.hpp`，支持延迟、阻塞至取消、故障注入与映射计数），
覆盖：跨边界读取、零长读取（不触发映射）、取消后槽位与映射释放、
映射失败后重试、全占用时背压、释放视图后恢复、尾部短窗口与越界、
并发同窗口去重，以及 mmap 后端的真实文件回读。
