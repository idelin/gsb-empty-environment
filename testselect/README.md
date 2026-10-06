# testselect

基于函数版本与调用图的 Go 回归测试选择库。

## 工作原理

基线快照（`Snapshot`）持久化三类信息：

- `Functions`：函数 ID -> 版本哈希（内容指纹）
- `CallGraph` / `Dynamic`：静态调用边与运行期观测的动态分派边
- `Coverage`：测试 ID -> 覆盖到的函数集合

每次选择时与当前代码状态（`Current`）对比：

1. **函数修改**：版本哈希变化的函数成为种子，选中覆盖它的测试，
   并沿调用图反向传播到全部传递调用方。
2. **调用边改变**：静态边与动态边的增删都使边的调用方成为种子，
   传播在**旧图与新图的并集**上进行，保证删边场景不遗漏。
3. **新增函数**：覆盖未知，无法确定影响面，触发全量运行标记（`RunAll`）。
4. **删除函数 / 动态边 / 覆盖过期**：对每个测试的覆盖集求**全依赖闭包**
   （含动态边），闭包触及已删除函数、或覆盖集引用未知函数时，
   判定覆盖过期（`Stale`）并选中重跑。

## 用法

```go
snap, _ := testselect.Load("snapshot.json") // 无快照时返回 nil
sel := testselect.Select(snap, cur)
if sel.RunAll {
    // 全量运行
} else {
    // 只运行 sel.Tests，sel.Reasons 记录选中原因
}
// 跑完后用新的版本/调用图/覆盖数据更新快照并保存
testselect.Save(newSnap, "snapshot.json")
```

## 测试

```sh
GO111MODULE=on go test ./...
```
