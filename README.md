# resample

直播转码要把44.1kHz和48kHz的多声道PCM统一重采样，输入块大小随网络抖动变化。给Go音频管线实现供转码任务调用的流式重采样器：多相加窗sinc滤波，滤波器长度和截止频率可配置，跨块保留历史样本与分数相位，结果与整段一次处理逐样本一致；采样率在流中途切换时先排空旧滤波器。用正弦扫频测量通带误差和混叠抑制。

## 实现

- `resample.go`：流式重采样器核心。多相（polyphase）加窗 sinc 插值，
  相位按 `Phases`（默认 4096）离散化为子滤波器表；整数累加器推进分数
  相位，无浮点漂移。`HalfLength`（默认 32）、`CutoffHz`（默认
  0.85×较低奈奎斯特）、窗函数（Hann/Hamming/Blackman/Blackman-Harris）
  均可配置。
- `sweep.go`：对数正弦扫频生成与质量测量（radix-2 FFT），输出通带误差、
  下采样混叠抑制、上采样镜像抑制，单位 dB。
- `cmd/sweep-bench`：可执行测量命令，`go run ./cmd/sweep-bench`。

## 用法

```go
r, _ := resample.New(resample.Config{
    InRate: 44100, OutRate: 48000, Channels: 2,
})
for chunk := range networkChunks {        // 块大小随网络抖动任意变化
    out := r.Process(chunk)               // 交织 float64 PCM
    write(out)
}
write(r.Drain())                           // 流结束排空尾段

// 流中途切换采样率：先排空旧滤波器再重建
tail, _ := r.SwitchRate(resample.Config{InRate: 48000, OutRate: 32000, Channels: 2})
write(tail)
```

## 一致性与测量

- 任意分块方式（含单样本块）的输出与整段一次处理逐样本（bit 级）一致，
  由 `TestChunkedMatchesWhole`、`TestRepeatedSegments` 覆盖。
- 扫频测量（L=32 Blackman）：44.1k↔48k 通带误差约 -81 dB、混叠/镜像
  抑制约 -61~-67 dB；整倍数比例通带误差达机器精度（约 -304 dB）。
- `go test ./...` 运行全部测试。
