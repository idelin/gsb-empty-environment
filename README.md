# 导入符号转发闭包

模块的导入符号可继续转发到另一模块，签名只检查第一跳会漏掉未授权代码。给Go加载宿主实现符号解析包，模块身份绑定摘要，转发项指向模块与符号，按固定版本图求最终地址；重复解析共享缓存，环与未授权模块均拒绝。用同名异摘要模块、共享转发链及更新事务测试所有最终目标有完整授权链。

## 实现

包 `netsec` 为 Go 加载宿主提供跨模块导入符号的转发闭包解析：

- 身份绑定摘要：`ModuleID{Name, Digest}`，同名异摘要是不同身份，引用不可互相顶替。
- 绑定二选一：具体地址（`Concrete` + 非零 `Addr`）或转发项（`To` 指向另一模块与符号），转发项可继续转发。
- 固定版本图：每次解析钉住开始时的不可变版本代（generation）；更新事务在私有副本上修改，`Commit` 整图校验通过后原子切换，失败或 `Discard` 不影响当前代。
- 完整授权链：沿转发链逐跳校验模块授权，链上任一未授权模块即拒绝（`ErrUnauthorized`），未授权目标无法藏在授权转发者之后。
- 环检测：DFS 灰色标记拦截自环与多模块环（`ErrCycle`），不返回部分地址。
- 共享缓存：每代一张解析缓存，成功与失败结果（地址 + 完整模块路径）均缓存，重复解析不重走链路。

入口：

- `NewLoader()` 创建加载宿主；`Loader.Begin/Commit/Discard` 管理更新事务；`Loader.Resolve(ref)` 返回 `Resolved{Addr, Trail}`。
- 错误用哨兵值分类，可用 `errors.Is` 判断：`ErrUnknownModule`、`ErrUnauthorized`、`ErrCycle`、`ErrUnknownSymbol`、`ErrInvalidAddress`、`ErrInvalidRef`、`ErrModuleExists`、`ErrTxClosed`；`*Error` 附模块身份与跳转路径，支持 `errors.As`。

## 测试

`go test ./...`（可用 `-race`）覆盖：同名异摘要身份隔离、逐跳授权与吊销恢复、共享转发链的缓存复用、自环/多模块环拒绝、事务隔离与固定版本图、非法绑定及并发解析。
