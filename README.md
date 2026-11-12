# ROP 小工具有界搜索

`netsec` 包扫描 AArch64 文本段，在 `RET`、`BR`、`BLR` 终止指令处向前回溯最多 K 条四字节指令，统计可被覆盖返回地址利用的 gadget 起点。

## 扫描语义

- 终止指令：`RET` 精确匹配 `0xD65F03C0`；`BR` 匹配 `0xD61F0000` 形式；`BLR` 匹配 `0xD63F0000` 形式。
- 终止指令自身也是一个起点；`MaxInstructions = K` 限制它之前最多回溯的指令数，`K = 0` 时只统计终止指令。
- 回溯遇到 `B`、`BL`、`B.cond`、`CBZ/CBNZ`、`TBZ/TBNZ` 等直接跳转，或任何其他 `RET/BR/BLR` 时停止，不跨越控制转移。
- 函数入口只排除“从该入口开始”的起点，不作为回溯屏障。
- 起点按地址去重；相同字节序列出现在不同地址时分别计数。`FirstAddress` 为所有起点中的最低地址。
- 输入按 4 字节小端指令解码，文本长度必须是 4 的倍数。

## 调用示例

```go
res, err := netsec.FindReturnGadgets(netsec.Config{
    Text:            text,
    FunctionEntries: []uint64{0x400100, 0x400180},
    MaxInstructions: 6,
    Budget:          4096,
    BaseAddress:     0x400000,
})
if errors.Is(err, netsec.ErrBudgetExceeded) {
    return fmt.Errorf("release gate rejected: %w", err)
}
if err != nil {
    return err
}
if res.Count > 0 {
    fmt.Printf("gadgets=%d first=%#x\n", res.Count, res.FirstAddress)
}
```

`Budget` 以四字节指令探测次数计：初次文本扫描和每次回溯探测各消耗 1 次预算；已由初次扫描解码的终止指令不重复计费。预算在扫描完成前耗尽时返回 `ErrBudgetExceeded`，且不返回部分计数。未设置 `BaseAddress` 时，函数入口和返回地址均使用相对文本起点的字节偏移。
