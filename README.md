# loudness

播出平台要求节目响度符合EBU R128。给Go音频处理管线实现供播出前检查调用的流式响度计：按BS.1770做K加权滤波，任意采样率都重新计算系数；400毫秒块75%重叠，先用-70 LUFS绝对门限再用相对门限，输出积分响度、短期响度和4倍过采样的真峰值；多声道按规定权重，LFE不计入。按EBU公开测试信号的规格合成音频，核对误差在0.1 LU以内。

## 用法

```go
m, _ := loudness.NewStandardMeter(48000, 6) // 5.1, SMPTE 声道顺序
for buf := range pipeline {                 // 流式喂入交织采样
    m.Write(buf)
}
integrated := m.Integrated()  // 积分响度 (LUFS)，门控后
shortTerm  := m.ShortTerm()   // 短期响度 (LUFS)，3 秒窗
momentary  := m.Momentary()   // 瞬时响度 (LUFS)，400 毫秒窗
maxTP      := m.MaxTruePeak() // 真峰值 (dBTP)，4 倍过采样
```

- K 加权滤波（预滤波 + RLB 高通）按 BS.1770 模拟原型对实际采样率做双线性变换，48 kHz 下与标准公布系数逐一吻合。
- 积分响度：400 ms 块、75% 重叠，先过 -70 LUFS 绝对门限，再过门控后响度 -10 LU 的相对门限。
- 声道权重按 BS.1770：前声道 1.0、环绕 1.41（+1.5 dB）、LFE 为 0 不计入。
- 测试按 EBU Tech 3341/3342 公开测试信号规格合成音频，全部误差在 0.1 LU 以内：`go test ./...`
