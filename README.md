# 自动扩容身份模板冻结

扩容控制器从模板创建工作负载，模板在审批后变更会带入未经批准的服务身份。实现供Go控制宿主调用的模板能力包，批准绑定规范化模板、镜像摘要、身份及安全选项，派生实例只允许改变给定的非安全字段。用结构化补丁模拟字段移动、删除后默认值恢复及并发模板更新，返回每项变化的授权依据。

## 设计模型

包 `netsec` 向 Go 控制宿主暴露三个核心能力：

- `NormalizeTemplate` / `BindApproval`：把评审过的模板规范化并冻结为 `Approval`，绑定规范化指纹、镜像摘要、服务身份、安全选项以及一份非安全可变字段白名单。
- `ApplyPatch`：执行 RFC 6902 风格的结构化补丁（`add`、`replace`、`remove`、`move`、`copy`、`test`），路径使用 JSON Pointer，支持对象与数组。
- `TemplateStore.Derive`：基于批准快照派生实例，逐项授权并在 `ChangeDecision` 中返回裁决（`ALLOW`/`DENY`）和授权依据。

## 授权依据

| 依据常量 | 含义 |
| --- | --- |
| `FROZEN_SECURITY_BINDING` | 身份、安全选项、能力等冻结字段未偏离批准值；删除后恢复的默认值与批准值一致时同样放行 |
| `APPROVED_IMAGE_DIGEST` | 镜像引用变化但仓库与摘要仍与批准绑定一致；换摘要或换仓库即拒绝 |
| `MUTABLE_FIELD_ALLOWLIST` | 变化落在批准时声明的非安全可变白名单内 |
| `IMMUTABLE_FIELD` | 已注册的非安全字段不在白名单内 |
| `UNREGISTERED_FIELD` | 批准从未分类过的字段，默认拒绝（隐式不信任） |
| `NO_OP` | 补丁在规范化后未产生实际变化 |

## 关键安全属性

- 镜像必须带 `@digest`，浮动标签在规范化阶段直接拒绝；标签仅作可读保留，授权只看仓库+摘要。
- 安全布尔位使用指针类型（`*bool`），缺省与显式 `false` 可区分；规范化会把默认值物化，使“删除恢复默认值”成为可审计的具体取值。
- 已弃用的 `spec.serviceAccount` 别名在规范化时迁移到 `spec.identity.serviceAccountName`，与显式身份冲突时拒绝；未知字段通过自定义 JSON 编解码保留在 `Extra` 中，不会被静默丢弃。
- 环境变量、用户组、能力等有序集合统一排序去重，保证等价模板指纹稳定。
- 模板更新采用乐观并发（版本号）。任何批准后更新都会使旧批准失效，`Derive` 返回 `errStaleApproval`，重新批准后才能继续派生；被拒绝的派生只返回批准时的规格，拒绝候选仅出现在审计决策中。

## 快速使用

```go
store := netsec.NewTemplateStore()
_ = store.Create(template)
approval, err := store.ApproveTemplate("billing-worker", "ap-1",
    []string{"/spec/replicas", "/spec/labels"})

instance, err := store.Derive("billing-worker", approval.ApprovalID, "inst-1",
    []netsec.PatchOperation{
        {Op: "replace", Path: "/spec/replicas", Value: float64(12)},
    })
// instance.Decisions[i].Verdict / Basis 给出每项变化的授权依据。
```

## 测试

`go test ./...` 覆盖：摘要钉选与跨仓库拒绝、别名迁移与冲突、默认值物化与删除恢复、字段移动的双向授权、未注册字段默认拒绝、乐观并发冲突与批准失效，以及 `ExampleTemplateStore` 端到端演示。
