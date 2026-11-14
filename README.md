# 导出任务读了整个租户的数据

报表导出按任务标识执行，攻击者修改过滤条件或复用他人任务，导出超出授权范围的数据。请实现供Go导出服务调用的可导入任务范围组件。任务绑定主体、租户、数据集与过滤条件，执行前按权限重算范围；条件改写、跨租户与任务复用拒绝。导出文件加密并带水印。用条件改写、跨租户、任务复用、合法导出和水印验证只导出授权范围。

## 组件：`netsec` 任务范围包

模块路径 `example.org/netsec/q0511`，包名 `netsec`，仅依赖 Go 标准库。

### 安全模型

- 任务绑定：`Task` 固化主体、租户、数据集与过滤条件，过滤条件做规范化序列化后用
  HMAC-SHA256 绑定；存储层被改写条件时执行前校验失败（`ErrFilterTampered`）。
- 创建即收窄：`CreateTask` 通过 `PermissionResolver` 取权限，约束键的冲突值按条件改写
  拒绝（`ErrConditionRewritten`），缺失约束自动注入，任务范围只能比授权更窄。
- 执行前重算：`ExecuteTask` 再次拉取当前权限并重算范围；权限收紧导致原范围过宽时
  返回 `ErrScopeExceeded`，权限被撤销返回 `ErrPermissionDenied`。
- 租户与属主：任务先原子 `Claim`（`pending → claimed`），再校验属主与租户；跨租户
  返回 `ErrCrossTenant`，同租户他人返回 `ErrTaskNotOwned`，重复执行返回
  `ErrTaskReused`。执行失败自动释放占用，成功后置为 `consumed`。
- 数据源纵深防御：`DataSource` 只按显式租户/数据集查询并应用全部等值过滤；
  `MemoryDataSource` 对未知租户直接返回 `ErrCrossTenant`。
- 加密与水印：导出 CSV 前两行为可见水印（任务、主体、租户、数据集、规范化范围）
  及其 HMAC 标签；随后用 AES-256-GCM 密封，随机 nonce，magic/version 作为 AAD。
  密钥经 HKDF 风格域分离派生，文件密钥、条件绑定密钥、水印密钥互不相同。

### 调用方式

```go
svc, _ := netsec.NewService(store, resolver, dataSource, masterKey) // masterKey 为 32 字节

task, err := svc.CreateTask(ctx, principal, "orders", netsec.Filters{"segment": "retail"})
result, err := svc.ExecuteTask(ctx, principal, task.ID)

plaintext, err := svc.VerifyExport(result.Envelope, task) // 解密并验水印
```

### 文件

- `types.go`：主体、权限、过滤条件、任务与错误哨兵
- `scope.go`：规范化序列化、权限匹配与范围重算
- `store.go`：`TaskStore` 接口与并发安全内存实现（原子 Claim）
- `datasource.go`：`DataSource` 接口与带租户隔离的内存实现
- `watermark.go`：水印生成与 HMAC 校验
- `crypto.go`：域分离派生密钥、AES-256-GCM 密封/开封、条件绑定
- `service.go`：`Service`（创建、执行、解密、验水印）
- `service_test.go`：条件改写、跨租户、任务复用、合法导出、水印/加密、
  权限收紧与过期场景

### 测试

```sh
go test ./... -race -count=1 -v
```
