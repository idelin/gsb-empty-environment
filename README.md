# streamsel

面向超大 HTML 页面的流式字段抽取：不建整棵 DOM 树，输入按块到达，边读边输出匹配元素的文本，峰值内存只与元素嵌套深度（×选择器链长度）相关，与文档大小无关。

## 用法

```go
m, _ := streamsel.NewMatcher("div.item > span.price")
for chunk := range chunks {          // 任意大小的 []byte 块
    for _, text := range m.Write(chunk) {
        consume(text)                // 元素闭合即输出其 textContent
    }
}
for _, text := range m.Close() {     // EOF：隐式闭合所有未闭合元素
    consume(text)
}
```

## 能力

- 自研增量 HTML 分词器（`tokenizer.go`）：token 可跨块，缓冲不超过单个最大 token；处理注释、doctype、实体转义、`script`/`style` 等 raw-text 元素。
- 容错修复（`parser.go`）：未闭合标签（EOF 时隐式闭合）、自闭合元素（`/>` 与 void 元素）、隐式结束（`li`/`p`/`td`/`tr` 等）、游离结束标签。
- CSS 子集（`selector.go`）：标签、`.class`、`#id`、`[attr]`、`[attr=value]`、`*`，以及后代与子代（`>`）组合，链长上限 64。
- 匹配核心（`matcher.go`）：每个在栈元素用位掩码记录选择器链的匹配进度，后代组合子的进度沿树向下传播；命中的元素在打开期间捕获文本，闭合时输出。
- 朴素基线（`dom.go`）：`ParseDOM` 建完整树 + `QueryAll` 树上匹配，用于正确性对拍与性能对比。

## 测试与基准

```sh
go test ./...            # 含切块枚举：逐字节/双切点/三切点验证输出与切块方式无关
go test -bench . -benchmem
```

在 ~2MB、2 万个商品的页面上（Apple M1 Pro）：

| 实现 | 吞吐 | 每-op 分配 |
|---|---|---|
| 流式（32KiB 块） | ~115 MB/s | ~8 MB |
| 朴素建树 | ~71 MB/s | ~32 MB |

流式分配的绝对量不随文档增大而增长（只随嵌套深度增长），朴素建树的内存随文档线性增长。
