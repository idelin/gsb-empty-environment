# pdfactions

文档预览服务会把PDF中的页面动作送给前端。给Go预览器实现按修订绑定的安全动作解析包，允许页内导航，拦截脚本及外部程序；沿页树和间接引用解析时绑定同一代xref，不得把新版本动作复用到旧缓存。构造含循环引用、删除后复用对象号及增量更新的文档，检查预览回调收到的目标。

## 设计

无第三方依赖，自带最小 PDF 词法/语法解析器（`parser.go`、`objects.go`），
只解析动作安全所需的语法子集：字典、数组、名称、字符串、数值、间接引用、流。

- `document.go`：解析经典 xref 表，沿 `/Prev` 链合并出每个增量修订的
  xref 快照；加密文档与 xref stream 直接拒绝。
- 修订绑定：`Revision.resolve` 只查当前修订的 xref，并校验 generation；
  旧修订永远读不到后续更新写入的对象。对象解析缓存按修订隔离。
- 页树：`collectPages` 沿 `/Kids` 递归，visited 集合打破页树循环，每个
  可达页面只出现一次。
- `actions.go`：`Previewer` 输出安全导航 `Target` 与拦截事件
  `BlockedAction`。默认策略只放行 `GoTo` 与
  `FirstPage/PrevPage/NextPage/LastPage` 命名动作；`JavaScript`、
  `Launch`、`URI`、`SubmitForm` 等全部拦截。`/Next` 链与间接引用循环
  由每链 seen 集合终止。
- 动作缓存键为 `(revision, ObjectID)`，新版本动作不会污染旧修订缓存。

## 用法

```go
doc, err := pdfactions.Open(pdfBytes)
if err != nil { /* 拒绝加密等不受支持文档 */ }

previewer := pdfactions.NewPreviewer(pdfactions.DefaultPolicy())
previewer.Preview(doc.Latest(), pdfactions.Callbacks{
    OnTarget:  func(t pdfactions.Target)  { /* 跳转到 t.PageIndex */ },
    OnBlocked: func(b pdfactions.BlockedAction) { /* 记录拦截，不执行 */ },
})
```

## 测试

`pdfbuilder_test.go` 提供可生成增量更新的测试用 PDF 构造器，支持释放并
复用对象号（generation 递增）。测试覆盖：

- 动作策略过滤（导航放行、脚本/外部程序拦截）；
- 页树循环与动作 `/Next` 循环安全终止；
- 删除后复用对象号：旧代引用悬空、新代引用可达；
- 三次增量更新下，跨修订共享同一 `Previewer` 时回调目标与缓存隔离；
- 命名目标（`/Dests` 与 `/Names` 名称树，含名称树自环）；
- 加密文档拒绝、间接引用互指循环。

运行：`go test ./...`
