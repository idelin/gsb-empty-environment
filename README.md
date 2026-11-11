# 跨编码秘密泄漏定位

源码安全服务需要发现被多次编码后嵌入文本的已知秘密。写供Go检测宿主调用的解码溯源包，输入字节文本、秘密集合和由调用方标出的候选区间；允许严格十六进制与Base64解码，最多三层，每层记录解码字节到原区间的映射。共享相同解码节点但保留不同来源链，累计展开预算耗尽返回未检查范围。命中返回最小原文覆盖区间和可重放解码链，用交叠候选、填充错误及同内容不同来源测试。

## 用法

```go
res := netsec.Scan(text, secrets, candidates, netsec.Options{
    MaxExpansions: 1000, // 累计展开预算；<=0 表示不限
})
for _, f := range res.Findings {
    // f.Cover 是秘密在原文中的最小覆盖区间，f.Chain 可重放：
    decoded, err := netsec.Replay(text, f.Chain)
    _ = decoded // 包含 secrets[f.Secret]
}
// 预算耗尽时，res.Unchecked 列出未检查的区间（NodeID 0 表示原文坐标）。
```

## 设计要点

- `Scan` 仅解码调用方标出的候选区间；解码产物内部自动识别编码样 token 继续下探，最多三层（`Options.MaxDepth`，上限 3）。
- 严格解码：十六进制要求偶数长度全 hex 字符；Base64 使用 `Strict` 模式，拒绝错误填充与非规范尾位（同时接受带填充与 raw 无填充形式）。
- 每层解码记录“解码字节 → 上层区间”的逐字节映射（hex 2 字符/字节，Base64 按量子精确到字符），命中时沿来源链回溯出原文最小覆盖区间（不含填充字符）。
- 相同解码内容共享同一节点（按内容去重，内层 token 只展开一次），但每个来源各保留一条入边，命中按不同来源链分别上报。
- `Options.MaxExpansions` 限制累计成功展开次数；耗尽后剩余候选（含内层）进入 `Result.Unchecked`。
