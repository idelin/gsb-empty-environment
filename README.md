# Thread-Affinity Resource Handles

C++17 资源句柄与可步进执行器，保证：

- 资源只能在创建线程（执行器所属线程）销毁；
- 句柄可复制/移动并自由跨线程传递；
- 最后一个句柄释放时，清理任务被投递回所属执行器，在其所属线程上恰好执行一次；
- 执行器 `shutdown()` 排空全部清理（包括关闭期间竞争到达的清理）后才返回；
- 执行器关闭后创建资源抛出 `std::runtime_error`。

## 文件

- `executor.hpp` — 单线程手动泵取执行器：任意线程 `post`/`post_cleanup`，仅所属线程 `step`/`step_all`/`shutdown`；通过 `register_resource` + 清理完成计数保证排空语义。
- `resource.hpp` — `Resource` 句柄：`shared_ptr` 控制块，deleter 在最后释放线程上仅做“投递清理”，真正析构回调在执行器所属线程运行。
- `tests.cpp` — 两个可步进执行器核对：析构线程、恰一次清理、跨执行器路由、异线程所属执行器、关闭竞争与关闭后禁止创建。

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build --output-on-failure
```
