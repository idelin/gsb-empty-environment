# proofaccept

进程校验服务不能为了判定页状态而冻结整个进程。本包给 Go 测量宿主提供不阻塞、可取消的独立验收逻辑：输入一组局部一致页集，输出是否存在一个共同因果切面，以及紧凑的切面或最小冲突证书。

## 模型

- 映射、改页、重新映射、撤销组成事件 DAG；`Deps` 表示因果先后。
- 页状态身份由 `Frame + Object + Generation + Digest + Executable + Signer` 共同决定。只有摘要相同、映射代不同时，仍视为不同状态。
- 虚拟页可通过 `Input.Aliases` 指向同一个 `Frame`；不同别名不产生第二份底层页状态。
- 每个 `Sample` 的 `States` 保证在自身采样区间内存在共同瞬间。`After` 是已包含事件，`Before` 是尚未包含事件；多个样本要求同一个切面同时落入所有区间。
- `Sample.IncompletePages` 表示没有收到该页完整变更清单。相关证据不会用于制造冲突；若结论依赖它，则返回 `evidence_insufficient`。
- 撤销对象是粘性的：一旦包含撤销事件，该对象此后的映射仍不可信。可执行页只有在对象当前受信时才安全。
- 同一底层页的页面事件必须由 DAG 明确排序；否则切面无法唯一决定页状态，输入会作为非法历史拒绝。

## 使用

```go
events := []proofaccept.Event{
    {
        ID: "map", Op: proofaccept.OpMap,
        Page: "heap", Object: "obj-a", Generation: 1,
        Digest: "sha256:...", Executable: true, Signer: "build-key",
        Trusted: true,
    },
    {
        ID: "revoke", Op: proofaccept.OpRevoke,
        Object: "obj-a", Deps: []proofaccept.EventID{"map"},
    },
}

in := proofaccept.Input{
    Events: events,
    Samples: []proofaccept.Sample{{
        ID: "s1",
        States: []proofaccept.PageState{{
            Page: "heap", Object: "obj-a", Generation: 1,
            Digest: "sha256:...", Executable: true, Signer: "build-key",
        }},
        After: []proofaccept.EventID{"revoke"},
    }},
}

result := proofaccept.Check(in)
// result.Status == proofaccept.StatusUnsafe:
// 撤销后仍有可执行页，且对象已不在当前受信集合内。
```

主要入口：

- `Check` / `CheckContext`：完整判定。返回 `compatible`、`incompatible`、`evidence_insufficient`、`possibly_unsafe`、`unsafe` 或 `canceled`；带 `Context` 的入口可在搜索过程中立即取消。
- `AcceptFrontier`：独立校验宿主指定的紧凑切面。切面是包含事件集合的极大元，不是所有已包含事件。
- `AcceptConflict`：独立校验最小不可相容样本集合；必须等于按样本编号字典序破同分后的规范结果。
- `Accept`：接受 `Certificate{Kind: ...}`，便于 RPC/文件传输。

不兼容时 `Result.Conflict` 是最小基数冲突集；同基数时按样本编号字典序取最小。多个相容切面中若同时存在安全和不安全切面，返回 `possibly_unsafe`；若所有相容切面都不安全，返回 `unsafe`。

## 实现约束

求解器不枚举全局拓扑排序。它把每个观测状态映射为底层页事件链上的允许区间，再只搜索区间候选；所有已包含事件通过前驱闭包计算，最终用极大元（frontier）表示切面。因此 32 个互不相关事件仍是常量级切面表示，而不是 `2^32` 个前缀。

运行测试：

```sh
go test ./...
```
