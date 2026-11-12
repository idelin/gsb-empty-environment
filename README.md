# Capsicum权利单调收窄

被攻陷的沙箱进程想放宽描述符权利，或用 openat 的 .. 与绝对路径逃出目录描述符。为C++17沙箱库写可嵌入的权利模型：cap_rights_limit 只能收窄；openat 须基准描述符含 CAP_LOOKUP，路径不得是绝对路径、不得经 .. 越出基准，新描述符权利不超过基准；fcntl、ioctl 需对应子权利，ioctl 命令须在允许表内。以内存描述符表模型测试放宽、越界、权利继承、子权利。

## 模型

- `netsec::FdTable`：内存描述符表，每个 `FdEntry` 持有权利位掩码、ioctl/fcntl 命令允许表（`std::optional`，空值表示未受限）与相对沙箱根的规范化路径。
- `cap_rights_limit`：单调收窄，`next & ~current != 0` 即拒绝（`NotCapable`）。
- `cap_ioctls_limit` / `cap_fcntls_limit`：允许表只能从「未受限」到「受限」、或收缩为子集。
- `openat`：基准描述符须含 `CAP_LOOKUP`；拒绝绝对路径；`..` 不得弹出基准目录（`resolve_relative` 纯函数）；新描述符权利为 `requested & base.rights`，继承传递单调。
- `fcntl` / `ioctl`：分别要求 `CAP_FCNTL` / `CAP_IOCTL` 子权利，且命令须落在允许表内。

## 构建与测试

```sh
cmake -S . -B build && cmake --build build && ctest --test-dir build --output-on-failure
```
