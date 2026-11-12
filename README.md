# Bazel 远程缓存写入准入（`admission` 包）

团队的构建缓存同时服务 CI 与外部贡献者的 PR 构建。攻击者可在 PR 构建中写入被投毒的
`ActionResult`，使主干构建命中缓存并产出带后门的二进制。`admission` 包是可嵌入缓存服务
gRPC 前置层的写入准入引擎，在请求到达真实缓存之前统一裁决。

本包仅依赖 Go 标准库（离线环境无 grpc-go 依赖）：传输无关的准入核心 + 按 gRPC 全方法名
分发的前置层，嵌入方只需把 protobuf 消息适配为包内领域类型。

## 准入策略

| 写入路径 | 规则 | 拒绝码 |
| --- | --- | --- |
| `ActionCache/UpdateActionResult` | 仅可信写入者可更新；PR/匿名身份只读 | `permission_denied` |
| CAS / `BatchUpdateBlobs` | 重算 sha256，必须与资源名中的 hash、size 完全一致 | `digest_mismatch` / `size_mismatch` |
| `ByteStream/Write` | 流式分块累计哈希，`WriteCompleted` 时校验，超大分块提前拒绝 | 同上 |
| ActionResult 输出校验 | 每个输出文件/目录树/stdout/stderr 摘要必须已存在于 CAS 且存储大小相符 | `missing_blob` |
| 动作摘要覆盖 | 同一 action digest 写入不同结果即拒绝并审计；相同结果幂等放行 | `action_result_conflict` |
| 资源名 hash | 必须为 64 位小写十六进制（大写/长度错误/非 hex 一律拒绝） | `invalid_digest` |

所有拒绝都会输出结构化审计事件（`Logger`，含写入者、来源、digest、原因），可直接接
JSON 日志；后端故障统一映射为 `internal`。

## 包结构

- `admission/guard.go`：准入引擎 `Guard`（CAS 一次性写入、ActionResult 写入、blob 校验）
- `admission/blobwriter.go`：ByteStream 流式写入器，提交时才落库
- `admission/front.go`：gRPC 前置 `Front`，按全方法名分发，读操作透传，支持 fail-closed
- `admission/digest.go`：摘要与 ByteStream/CAS 资源名解析（`blobs/uploads` 与 `blobs/{hash}/{size}`）
- `admission/identity.go`：写入者身份，经 context 传播；缺省身份按不可信处理
- `admission/store.go`：`Store` 接口与并发安全的内存参考实现 `MemoryStore`
- `admission/logger.go`：审计事件接口与 JSON 行日志实现
- `admission/errors.go`：类型化准入错误与稳定错误码（可一一映射 gRPC status code）

## 嵌入方式

```go
guard := admission.NewGuard(backendStore, admission.NewJSONLogger(os.Stderr))
front := admission.NewFront(guard)

// 一元拦截器：从认证元数据推导身份后分发
func intercept(ctx context.Context, fullMethod string, req any) error {
    id := admission.Identity{Name: jobID, Source: source, Trusted: source != "pr"}
    ctx = admission.WithIdentity(ctx, id)
    return front.Intercept(ctx, fullMethod, req)
}

// ByteStream 流式写入：每个 WriteRequest 分块写入，完成时 Commit
w, _ := guard.NewBlobWriter(ctx, req.ResourceName)
w.Write(chunk)                 // 内部累计 sha256，超 size 立即报错
digest, err := w.Commit()      // 校验 hash/size 通过后才写入 Store
```

## 验证场景

`go test ./...`（含 `-race`）覆盖：

- 伪造摘要：声明 hash 与实际 sha256 不符 → `digest_mismatch`，blob 不落库并记录
- 大小不符：资源名 size 与字节数/存储大小不一致 → `size_mismatch` / `missing_blob`
- PR 写入：不可信身份更新 ActionCache → `permission_denied`，无任何持久化
- 缺失 blob：ActionResult 引用 CAS 中不存在的输出 → `missing_blob`，结果不写入
- 覆盖冲突：相同 action digest 不同输出 → `action_result_conflict`，原结果保持不变
- 资源名：大写 hex、长度错误、缺 query 参数、非数字 size 全部拒绝
