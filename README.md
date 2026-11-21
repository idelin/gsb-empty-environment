# 恢复准入包 `admission`

主机从旧镜像恢复时，往往只持有片段化的签名更新收据，无法确定两个并行
增量究竟哪一个先持久化。本包不猜测持久化顺序，而是枚举**所有与检查点
相容的历史**，对请求区间逐段给出保守且精确的三分类结论。

导入路径：`prescreen.local/ddd-10496-v4/admission`

## 三分类结论

- `VerdictExecutable`（确定可执行）：在每一条相容历史中，该位置都是
  **同一可信内容**，且每一条历史都保留了所需（执行）权限。
- `VerdictForbidden`（确定禁止）：在每一条相容历史中该位置都缺所需
  权限，执行必然被拒绝，与内容是否可信无关。
- `VerdictUnknown`（未知）：历史之间存在分歧（内容不同、可信性不同），
  无法给出确定结论，宿主必须保守处理。

## 核心不变量

- 每条更新收据，无论是内容型（`KindContent`）还是仅收紧权限型
  （`KindPermission`），都绑定发布者、旧根、新根和作用页区间；收据沿
  旧根/新根构成更新 DAG。
- 内容以摘要标识，**别名页可以共享同一摘要**；但权限是映像位置的属性，
  收紧权限只作用于被声明的位置区间，绝不随摘要传播。
- 每条历史都从检查点独立重放，跨历史结论只在完整的位置状态之间逐段
  比较。因此不可能把 A 历史的可信内容与 B 历史的签名/权限范围拼接成
  一个“确定可执行”的结果。
- 页号为 48 位（`[0, 2^48)`）。所有状态与判定都用有序区间（边界列表）
  表示，复杂度只与收据和分片的边界数量相关，**从不按页展开**。
- 分片边界由宿主提供并被强制校验：必须有序、首尾相接、完整覆盖
  `[0, 2^48)`；内容收据的作用区间必须由完整分片（或单分片内的首尾）
  构成。

## 数据模型

- `Checkpoint`：恢复起点，含根、分片边界、完整覆盖页空间的初始权限，
  以及分片对齐的基线内容（可只覆盖部分空间）。
- `Receipt`：签名更新收据。内容收据通过 `Content` 声明若干分片对齐的
  内容条目（条目必须恰好覆盖作用区间）；权限收据通过 `RevokeExec`
  声明被撤销执行权的子区间，且只允许收紧、不允许授予权限。
- `Verifier`：宿主实现的接口，负责密码学签名验证与发布者信任判定，
  返回 `TrustTrusted` / `TrustUntrusted` / `TrustRejected`。
- `AdmissionInput.Heads`：宿主认为可能是“当前根”的集合。为空时取 DAG
  的所有叶根；显式传入内部根即可表达“某条更新可能尚未持久化”的提交
  确认丢失场景。

## 使用方式

```go
adm, err := admission.Build(admission.AdmissionInput{
    Checkpoint: cp,
    Receipts:   receipts,
    Heads:      candidateRoots, // 可选；为空表示所有叶根
    Request:    admission.Range{Start: 0, End: 4095},
    Verifier:   hostVerifier,
})
if err != nil {
    return err // 结构非法、不可达、根冲突等
}
decisions, err := adm.Admit()
executable, forbidden, unknown := admission.RangesByVerdict(decisions)
```

`Build` 只做一次校验与 DAG 构建，之后可对多个请求区间重复调用 `Admit`；
返回的 `*Admission` 可并发只读使用。

## 参考实现与测试

- `ReferenceAdmit` 是仅供小地址域（页数不超过 `ReferenceMaxPages`）
  使用的独立逐页参考实现，代码路径与区间引擎完全不同。
- 差分测试在 5 个随机种子、共 500 组随机更新 DAG（含共享分支、并行
  增量、混合内容/权限收据、不可信发布者、提交确认丢失）上逐页对照两条
  实现，结论必须完全一致。
- 命名场景测试覆盖：丢失提交确认、别名同内容异权限、只撤销半区间、
  旁支缺口不冻结无关安全区、反跨历史拼接、48 位边界与各类非法输入。

## 目录

- `admission/range.go`：48 位页区间、分片校验与对齐判定
- `admission/types.go`：公开类型、枚举与错误
- `admission/immap.go`：只存边界的分段常量区间映射
- `admission/graph.go`：收据 DAG 构建、可达性与候选根解析
- `admission/checkpoint.go`：检查点校验与基线状态
- `admission/receipt.go`：收据结构校验与单历史状态推进
- `admission/replay.go`：记忆化历史回放与跨历史三分类裁决
- `admission/reference.go`：小地址域逐页参考实现
