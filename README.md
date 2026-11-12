# 哈希链跨窗合并检测

审计存储按时间窗切成多段，每段哈希链首条承接前段末哈希；攻击者持有存储写权限，可删除中间窗口、拼接两个副本的窗口，或重算尾部链并伪造新窗口。为Go审计库实现供归档校验调用的链验证包：每窗头记录含窗号、起始序号、条数与前窗末哈希，外部锚点（调用方在若干窗后发布的链头哈希与序号）不可更改；验证须窗号严格相邻、序号连续，锚点处链头必须相等；同一窗号出现两个不同窗头视为分叉并报告；窗内条数与实际不符、重算后与锚点不符均拒绝。用跨副本拼接、删窗、尾部重算、锚点前后篡改和合法链验证。

## 数据模型

- `Entry`：`Seq`、`Payload`（`Data` 为兼容别名）、可选 `Hash`。
- `WindowHeader`：`Number`、`StartSeq`、`Count`、`PrevHash`。
- `Window`（别名 `Segment`）：`Header` + `Entries`。
- `Anchor`：不可变外部锚点，按 `Seq` 定位链头哈希 `Hash`。

条目哈希为
`SHA-256(domain || PrevHash || Seq || len(Payload) || Payload)`，逐条链式重算。
`SealWindow`/`BuildWindows`/`NewWindow` 可生成带 `Hash` 的窗口。

## 校验规则

`VerifyChain(windows, anchors) error` 依次检查：

- 窗号严格相邻：`N, N+1, ...`。
- 序号连续：下一窗 `StartSeq` 等于上一窗 `StartSeq + Count`，窗内逐条连续。
- `Header.Count` 必须等于实际条目数。
- 每个窗口重算出的头哈希必须等于下一窗口的 `PrevHash`。
- 每个锚点必须等于该 `Seq` 处重算出的链头哈希；锚点位于首窗前时与首窗 `PrevHash` 对齐。
- 同窗号出现不同窗头，或同窗头但内容不同，返回 `*ForkError`（`errors.Is(err, ErrFork)`）。
- 重复且完全相同的窗口副本可去重；锚点序列缺失（包括尾部被删）返回 `ErrAnchorMissing`。
- 同一 `Seq` 的两个锚点哈希不同返回 `ErrAnchorConflict`。

`CheckChain`/`AuditChain` 返回 `*Report`，收集全部问题；`Report.Forks()` 取出分叉。
也可以用 `NewVerifier(anchors).Verify(windows)` 绑定不可变锚点后复用。

```go
windows, _ := netsec.BuildWindows(records, 2, 0, nil)
anchors, _ := netsec.AnchorsForWindows(windows, 1, 3)
if err := netsec.VerifyChain(windows, anchors); err != nil {
    var fork *netsec.ForkError
    if errors.As(err, &fork) {
        // 报告分叉窗号及两个窗头
    }
}
```

## 安全边界

锚点是唯一不可由存储写权限伪造的信任根。重算后的尾部只有在后续锚点覆盖时
才会被拒绝；最后一个锚点之后的未锚定新窗口只能做结构校验，无法证明其内容
未被伪造。因此归档校验应在尾部之后至少保留一个已发布锚点。
