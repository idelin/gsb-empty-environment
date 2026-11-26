容器运行时在宿主机侧代表容器打开卷路径或执行 `pivot_root`/绑定挂载（`MS_BIND`/`MS_REC`）时，若仅在打开前做字符串前缀检查而未模拟内核 `openat2(RESOLVE_IN_ROOT | RESOLVE_NO_MAGICLINKS)` 语义与挂载传播标志（`MS_SHARED`/`MS_SLAVE`/`MS_PRIVATE`/`MNT_LOCKED`），容器内进程可通过深层符号链接、`..` 跨越挂载点或 `/proc/self/fd` 魔术链接逃逸读取宿主机根目录文件。为 Go 容器运行时安全宿主构建可导入的虚拟文件系统挂载树与防逃逸路径解析验证包。系统建模带父子挂载关系的挂载命名空间树（每节点含根目录 inode、挂载点、只读标志、`MNT_LOCKED` 不可卸载锁与传播 peer-group）、用户命名空间 UID/GID 映射以及支持普通文件、目录、绝对/相对符号链接与 procfs 魔术链接的目录图。核心功能包括：（1）精确实现容器根沙箱路径解析器：逐分量遍历路径（最大符号链接跳数 40 防环），绝对符号链接须重锚定到容器根而非宿主根，遇到 `..` 到达容器根时保持在容器根，严禁穿越受限挂载边界或跟随 `/proc/*/fd/*` 魔术链接逃出容器根，并在并发 `rename` 交换目录的 TOCTOU 场景下验证无路径拼接漏洞；（2）挂载操作安全裁决：禁止非初始用户命名空间进程卸载 `MNT_LOCKED` 挂载或剥离只读绑定挂载的 `MS_RDONLY` 标志，正确传播 `MS_SHARED` 与 `MS_SLAVE` 事件而不反向泄漏给父命名空间；（3）内置有界 6 步对抗搜索器，在并发 `symlink`/`rename`/`open` 交错下搜索能触发宿主文件越权访问的最小操作序列并附小目录树全枚举验证。

## 包结构

- `vfs/`：并发安全的内存目录图。节点建模普通文件、目录、静态绝对/相对符号链接与 procfs 魔术链接；提供原子 `Rename` 与 `renameat2(RENAME_EXCHANGE)` 语义，节点指针在重命名/交换后保持有效（RCU 式保证）。
- `userns/`：用户命名空间层级、嵌套 UID/GID 映射、凭证、能力门控（`CAP_SYS_ADMIN` 等）与 DAC 读权限判定。
- `mount/`：挂载命名空间树。每个挂载含根 inode、挂载点、`MS_RDONLY`、`MNT_LOCKED`、传播类型与 peer-group；实现卸载/只读变更裁决与 `MS_SHARED`/`MS_SLAVE`/`MS_PRIVATE` 事件投递，slave 事件永不反向泄漏给 master。
- `resolve/`：
  - `SecureResolver` 复刻 `openat2(RESOLVE_IN_ROOT | RESOLVE_NO_MAGICLINKS)`：逐分量遍历、40 跳符号链接预算、绝对符号链接重锚定到容器根、`..` 在容器根钳制、魔术链接拒绝、最终挂载命名空间包含性校验。
  - `PrefixResolver` 为故意保留的脆弱对照实现（仅字符串前缀检查、绝对链接锚定宿主根、无最终包含校验），仅供对抗验证，禁止生产使用。
  - `OpenSlow` 提供逐分量可恢复遍历，用于在受害者步进之间插入攻击者变更以覆盖 TOCTOU 交错。
- `adversarial/`：有界 6 步穷举搜索器，枚举 symlink/rename/exchange/open 序列与受害者/攻击者交错点；在脆弱解析器上复现宿主文件越权，同时断言安全解析器零逃逸，并对小目录树做全枚举。
- `cmd/gsbsecure/`：可运行演示，展示攻击对比与搜索报告。

## 快速开始

```bash
go test ./...        # 单元测试
go test -race ./...  # 并发/TOCTOU 竞态检测
go run ./cmd/gsbsecure
```

## 安全不变量

- 绝对符号链接与魔术链接目标始终重锚定到容器根，绝不锚定宿主根。
- `..` 到达容器根或挂载根时钳制在容器内，绑定挂载 `..` 返回挂载点父目录而非宿主源父目录。
- 解析结果必须通过挂载命名空间 `Reachable` 包含性校验，否则返回 `ErrEscapedRoot`。
- 非初始用户命名空间无法卸载 `MNT_LOCKED` 挂载，也无法剥离只读绑定挂载的 `MS_RDONLY`。
- 对抗搜索报告中 `SecureEscapes` 必须恒为 0；任何大于 0 的值都表示回归。
