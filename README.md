# 事务化多插件 JSON Patch 准入组合包

本模块把多个准入插件对同一个 Pod 风格对象的修改合并成**一次事务**发布，仅依赖 Go
标准库（`encoding/json`、`crypto/sha256`），无外部依赖。

## 包结构

- `pkg/jsonpatch`：受限的 RFC 6902 JSON Patch 实现。
- `pkg/patchtx`：固定点事务引擎，负责按需重跑插件、合并补丁、回滚与循环见证。
- `cmd/admission-demo`：可运行的准入场景演示。

## 受限 JSON Patch（`pkg/jsonpatch`）

支持且仅支持六种标准操作：`add`、`remove`、`replace`、`move`、`copy`、`test`。

- 操作严格按顺序作用于**前一操作之后的视图**；对数组而言，每个索引都在当前视图上
  解释，`-` 表示在数组末尾插入，`add` 会使后续元素整体移位。
- `Apply` 先对输入做 JSON 深拷贝，再在副本上执行，因此**绝不会修改入参**。
- 任一操作失败即中止整段补丁并丢弃工作副本。`test` 失败返回
  `*jsonpatch.TestFailureError`，它包装哨兵错误 `jsonpatch.ErrTestFailed`，可用
  `errors.Is` 判定；错误中包含操作序号、JSON Pointer、期望值与实际值。
- 指针遵循 RFC 6901 转义（`~1` → `/`，`~0` → `~`），`move` 禁止把节点移入自身子树。

```go
next, err := jsonpatch.Apply(pod, []jsonpatch.Op{
    {Op: "add",     Path: "/spec/containers/-", Value: sidecar},
    {Op: "test",    Path: "/spec/containers/1/name", Value: nameOfLogger},
    {Op: "replace", Path: "/metadata/labels/injected", Value: yes},
})
```

## 事务引擎（`pkg/patchtx`）

插件实现统一接口：

```go
type Plugin interface {
    Name() string                 // 引擎内唯一，重名在 NewEngine 时 panic
    Deps() []string               // 依赖的 JSON Pointer 列表
    Patch(view any) ([]Op, error) // 基于当前视图产出补丁
}
```

### 一轮事务

1. 对每个插件计算其依赖指针的指纹（指针路径 + 规范 JSON 值的哈希）。
2. 指纹与上次调用一致时**复用缓存补丁**，否则才真正调用 `Patch`——无依赖的插件
   在一次 `Run` 中至多执行一次。
3. 按插件顺序把所有补丁合并成一个补丁序列，整体调用 `jsonpatch.Apply`：数组索引
   因此跨插件链式地按前序插件留下的视图解释。
4. 合并补丁中的任何 `test` 失败或非法操作都会令整轮回滚，返回
   `*patchtx.RoundFailureError`（包装 `jsonpatch.ErrTestFailed`），对象保持原样。

### 固定点与循环见证

- 一轮结束后若对象不再变化，即到达固定点：提交缓存并发布合并后的对象。
- 引擎记录每个状态的哈希；再次遇到相同哈希说明状态在已访问状态之间振荡，立即停止
  并返回 `*patchtx.NonConvergenceError`，其中 `Cycle` 是从首次出现到再次出现的完整
  状态见证（含轮次、哈希和对象快照），首尾哈希相同。
- `MaxRounds`（默认 32）作为兜底上限；未精确重复但耗尽轮次时同样返回带见证的
  `NonConvergenceError`。

### 重新调用的缓存语义

缓存按“依赖指纹 → 补丁”保存，并在成功收敛后跨 `Run` 保留。重新调用时，只有依赖
子树确实变动的插件会重新执行；依赖未变的插件直接复用补丁。注意被复用的补丁会在新
视图上重新应用，因此插件补丁应只依赖其声明的 `Deps` 子树（这是接口契约）。

## 验证

```sh
go vet ./...
go test ./...
go run ./cmd/admission-demo
```

测试覆盖的关键性质：

- 数组索引随前序操作的视图解释（插入、删除、替换链式生效）。
- 失败的 `test` 撤销本轮全部修改，调用方原对象字节级不变。
- 固定点搜索只重跑受依赖变动影响的插件，稳定重跑时零调用。
- 故意振荡的插件（`oscillatorPlugin` 在两个相位间反复横跳）触发循环见证，且原始
  对象完整保留。
