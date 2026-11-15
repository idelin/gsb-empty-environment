# 反序列化对象能力隔离

Go宿主通过对象图给插件传递能力，需要嵌入解码层的能力恢复包。输入图最多1024节点，节点类型为字符串、数组、字典、对象引用或能力引用，可有共享与环；能力引用只有不可猜测token，由可信注册器映射为租户、操作集合、代次及撤销状态。攻击者控制整张图，可能把同一能力藏在环内、混用其他租户token或重用旧代次。解码必须先验证整图，再原子取得全部能力的使用租约，失败不返回部分对象、不调用任何能力，保持共享和环身份。撤销返回后不能取得新租约，已有租约可完成；重复引用只计一次租约，释放幂等。提供可控调度测试撤销与整图解码竞态，验证跨租户、悬空引用、深环和正常共享对象。

## 设计

- `graph.go`：输入图（`Graph`/`Node`，子节点按下标引用，天然表达共享与环）与输出对象图（`Object`，同一输入下标恒为同一指针，保持共享与环身份；`KindRef` 为透明别名）。
- `registry.go`：可信注册器。token 为 256 位随机值；`Register`/`Reissue`/`Revoke` 管理代次与撤销；`acquireAll` 在同一把锁内复检并统一计数，原子取得全部租约；`releaseAll` 计数不为负，配合 `leaseSet` 的 `sync.Once` 保证释放幂等。
- `decode.go`：两阶段解码。阶段一整图验证（节点数 ≤1024、下标范围、纯引用环拒绝、每个去重 token 的存在性/租户/代次/撤销状态）；阶段二原子租约（锁内复检，关闭验证与租约间的 TOCTOU 窗口）。任一失败返回 `nil` 结果、不持有租约、不调用任何能力。
- 撤销语义：`Revoke` 返回后新租约必失败；在途租约可完成并正常释放。
- 可控调度：`Decoder.beforeAcquire` 是测试钩子，位于整图验证通过、原子租约之前，测试用 channel 精确编排“撤销 vs 解码”的两种交错（见 `decode_test.go` 的 `TestRaceRevokeBeforeAcquire` / `TestRaceAcquireBeforeRevoke`）。

## 用法

```go
reg := netsec.NewRegistry()
tok := reg.Register("tenant-a", []string{"read"})

res, err := (&netsec.Decoder{Reg: reg}).Decode("tenant-a", graph)
if err != nil {
    // 无部分对象、无租约残留
    return err
}
defer res.Release() // 幂等
if res.Root.Fields["cap"].Cap.Allows("read") { /* ... */ }
```

## 测试

```sh
go test -race ./...
```
