# 秘密分层缓存清理

解密服务的明文同时存在读缓存、索引缓存和派生片段中，撤销一份引用不能漏掉其他副本。实现供Go缓存宿主调用的敏感对象依赖图，副本注册时绑定来源对象与密钥代，撤销按依赖闭包标记不可用并等待使用者退出再擦除；共享派生物含多个来源时任一撤销即失效。用并发派生与撤销测试无漏清、无提前清零。

## 依赖图 API（`package netsec`）

- `NewGraph(opts ...Option) *Graph`：创建依赖图，可选 `WithWiper` 注入擦除观测点（默认整块写零）。
- `RegisterSource(id, gen, data)`：登记解密明文根对象并绑定密钥代。
- `RegisterCopy(id, source, data)`：登记读缓存/索引缓存副本，自动继承来源的密钥代；来源已撤销时返回 `ErrRevoked`，杜绝撤销后借新登记回流。
- `RegisterDerived(id, gen, data, sources)`：登记派生片段，可绑定多个来源；重复来源自动去重，任一来源已撤销即拒绝登记。
- `Acquire(id) (*Handle, error)`：取得在用句柄，句柄存活期间底层字节保证不被清零；用完必须 `Release`（可重复调用，幂等）。
- `Revoke(id)`：沿 dependents 边 BFS 求依赖闭包，闭包内对象立即标记不可用，拒绝新登记/新取用；随后等待所有句柄退出，再对每个对象调用一次 wiper 擦除。
- `RevokeGeneration(gen)`：撤销某密钥代下全部对象及其依赖闭包，其他密钥代不受影响。
- `IsRevoked` / `IsWiped` / `Holders` / `Wiped`：状态观测，供宿主与测试核对。

## 生命周期保证

- **无漏清**：撤销闭包覆盖来源本身、同代副本、派生片段及传递依赖，返回时闭包内全部对象已擦除；测试通过外部保存的字节切片逐一断言为零。
- **无提前清零**：擦除在 `sync.Cond` 上等待 `holders == 0`；已发放句柄观察到的字节始终完整，`Acquire` 与 `Release` 全程加锁并由 `-race` 覆盖。
- **多来源失效**：共享派生物挂在每个来源的 dependents 列表上，任一来源撤销都会把它纳入闭包。
- **按代隔离**：副本继承来源密钥代，按代撤销只波及该代对象，跨代明文保持可用。

## 测试

```sh
go test -race -count=1 ./...
```

`TestConcurrentDerivationAndRevocation` 以派生/副本/读者/撤销多协程并发运行，终态断言登记对象数 == 擦除事件数 == 全零字节数，且每个对象恰好擦除一次；`TestRevokeWaitsForHolders` 专门验证“先标记、后等待、再擦除”的顺序。
