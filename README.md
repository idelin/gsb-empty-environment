# morphology

Go 灰度形态学库：对 8 位图像做膨胀（dilate）与腐蚀（erode），支持任意有限结构元素，输入可按行块流式到达，结果与全图一次运算完全一致。

## 语义

- 结构元素是有限非空偏移集合 `{(dx,dy)}`，可不对称、可不包含原点；空元素一律拒绝（`ErrEmptyElement`）。
- 图外像素固定为零，对膨胀和腐蚀都生效。腐蚀**不会**跳过图外采样点：任何落在图外的元素点都向最小值贡献 0，因此边界腐蚀结果被拉向 0。
- 流式处理只保留 `maxDY-minDY+1` 行输入（有界行缓存，与图像高度无关），输出行一旦确定即返回。

## 用法

```go
el, _ := morph.NewElement(morph.Point{X: 0, Y: 0}, morph.Point{X: -1, Y: 0}, morph.Point{X: 2, Y: 1})
s, _ := morph.NewStream(w, h, el, morph.Erode)
for _, block := range rowBlocks {          // 每块长度为 w 的整数倍
    out, err := s.WriteRows(block)         // 返回本次确定的输出行（可能为零或多行）
    ...
}
tail, err := s.Close()                     // 收尾，补齐剩余输出行
```

一次性接口：`morph.DilateImage` / `morph.ErodeImage` / `morph.Apply`。

## 验证

`morph/stream_test.go` 中的朴素逐像素全图模型作为参照：随机尺寸、随机（不对称）结构元素、随机行块切分下，流式结果与朴素模型逐字节一致；另有空元素拒绝、腐蚀边界置零、有界缓存等针对性测试。

```sh
go test ./morph/
```
