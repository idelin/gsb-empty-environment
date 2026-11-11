# rootedpublish

上传接口允许用户同时替换同目录下的文件，原先“先校验路径、再打开文件”的做法会在两次操作之间留下
TOCTOU 窗口，攻击者可借符号链接竞态把写入重定向到根目录之外。本包为运行在 macOS（arm64）上的 Go
服务提供一个以**目录描述符为信任锚**的文件发布组件：流式写入临时文件，随后在同一目录内原子替换目标，
逐级阻止路径越界与符号链接跟随，并在任何失败点清理临时对象。

## 安全模型

信任锚是根目录的一个 `O_DIRECTORY` 文件描述符（`Open` 时打开一次并长期持有）。此后所有路径解析都只
通过相对该描述符的 `*at(2)` 系统调用完成，不再使用可能被并发改名影响的绝对路径字符串：

- **逐级 `openat` + `O_NOFOLLOW`**：每个路径分量都相对上一级描述符打开；遇到符号链接直接返回
  `ErrSymlink`，链接永远不会被跟随，因此无法把查找重定向到根外。
- **路径分量白名单**：只接受规范相对路径，拒绝绝对路径、空分量（双斜杠）、`.` 与 `..`、NUL 字节，
  描述符遍历无需解释任何可越界的分量。
- **临时文件与目标同目录**：在最终父目录描述符下以 `O_CREAT|O_EXCL|O_NOFOLLOW` 创建 `0600` 临时文件
  （文件名含加密随机 nonce），保证与目标处于同一文件系统/卷，且既不会跟随攻击者预埋的链接，也不会
  覆盖既有条目。
- **流式 + 持久化 + 原子替换**：载荷分块写入裸描述符（不经过 `os.File`，避免其 finalizer 在外层关闭
  描述符），`fsync` 落盘、`fchmod` 设置权限后，用 `renameat` 在同一目录内原子改名覆盖目标；任何时刻
  目标要么是旧内容、要么是新内容，绝不会出现半文件。
- **失败即清理**：从临时文件创建成功起，每条错误路径都关闭描述符并经父目录描述符 `unlinkat` 删除
  临时条目，旧版本与根外哨兵文件保持不变。

> 平台说明：Go 标准库 `syscall` 在 darwin 上未封装 `*at` 系列，也未导出对应系统调用号。本包在
> `sys_darwin.go` 中通过 `syscall.Syscall6` 直接调用，系统调用号为经实测确认的 **arm64（arm64e）**
> 编号（`openat=463, fstatat=470, mkdirat=475, unlinkat=472, renameat=465, renamex_np=488`），与
> 旧版 x86_64 表不同。代码可在 amd64 上编译，但运行时行为仅在 arm64 macOS 上验证。

## 用法

```go
import (
    "context"
    "strings"

    "example.org/rootedpublish"
)

pub, err := rootedpublish.Open("/srv/data")
if err != nil { /* ... */ }
defer pub.Close()

err = pub.Publish(
    context.Background(),
    "users/alice/avatar.png",                 // 根内相对路径
    strings.NewReader(binaryPayload),         // 任意 io.Reader，流式写入
    rootedpublish.Options{
        Mode:        0o644,                   // 可选，默认 0644
        CreateDirs:  true,                    // 可选，自动创建缺失父目录(0755)
    },
)
```

错误可用 `errors.Is` 分类：`ErrInvalidPath`（越界/非规范路径）、`ErrSymlink`（任一分量或目标名是符号
链接）、`ErrNotDirectory`（应为目录的分量不是目录）。

## 可控同步点与竞态验证

`Options.Hook` 在发布流程的固定同步点回调，测试可在回调中阻塞并并发篡改目录树，再放行，从而确定性地
验证竞态无法劫持写入：

| 同步点 | 时机 |
| --- | --- |
| `HookResolvedParent` | 父目录描述符解析完成、临时文件创建之前 |
| `HookTempCreated` | 临时文件已创建、尚未写入任何载荷 |
| `HookAfterWrite` | 载荷写完并 `fsync` 完成、原子改名之前 |
| `HookAfterRename` | 改名提交之后（仅观察用途，返回错误被忽略） |

测试在根外放置**哨兵文件**、在目标处放置**旧版本文件**，并覆盖以下攻击与失败场景，逐一断言哨兵内容、
旧版本内容与临时对象清理情况：

- 中间分量/最终目标名是符号链接 → 拒绝，哨兵不变。
- 在 `HookResolvedParent` 阻塞时把真实父目录改名、换入指向根外的恶意目录 → 写入仍落在原描述符指向的
  旧目录，哨兵不变，恶意链接原样保留。
- 通过 `renamex_np(RENAME_EXCHANGE)` 在 `HookAfterWrite` 交换目标条目与符号链接 → `renameat` 原子替换
  当前条目，目标最终是普通文件，哨兵从未被打开写入。
- 临时文件打开的 `O_EXCL|O_NOFOLLOW` 语义：对既有符号链接返回 `ELOOP/EEXIST`，对既有条目返回
  `EEXIST`，均安全失败。
- 在每个可中止同步点注入错误、读取流失败、context 取消 → 旧版本保留、无临时对象泄漏、哨兵不变。
- 高并发发布同一目标/不同目标 → 无临时名冲突、无描述符串用，最终文件内容完整无撕裂。

## 运行测试

```sh
go test -race -count=1 ./...
```
