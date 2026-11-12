# taskpkg

面向 Go 播放宿主的双声道预测补音（PLC）包，输入输出为 planar `float64`
PCM。它用最近 `N` 个真实样本估计自相关并做 Levinson-Durbin 预测；补音时
只推进合成状态，不把补音样本写入统计窗。左右声道共用一个周期判定，但保留
各自的预测器、残差能量、噪声源和合成历史。

## 快速接入

```go
cfg := taskpkg.DefaultConfig(48000)
plc, err := taskpkg.New(cfg)
if err != nil {
    return err
}

// valid[i] == false 表示第 i 帧丢失。nil 表示整块都是真实 PCM。
outL, outR := plc.ProcessStereo(inL, inR, valid)
```

输出长度始终与输入帧数完全一致。若宿主已经知道整块丢失，可直接调用
`ConcealStereo(frames)`；恢复时继续调用 `ProcessStereo`/`FeedStereo`，
引擎会在前 `Crossfade` 个真实样本上完成线性交叉淡化。`ProcessStereoInto`
可复用目标缓冲区，`ProcessInterleavedStereo` 处理 `L,R,L,R` 交织数据。

## 配置

- `Window`：统计窗 `N`，只取最近的真实样本。
- `Order`：最大预测阶 `P`。反射系数绝对值达到或超过 1 时，保留之前的稳定阶。
- `MinPeriod`/`MaxPeriod`：周期搜索范围，单位为样本，包含两端。
- `MinCorrelation`：预测残差最高归一化相关的采用阈值。
- `Decay`：每多一个连续丢失样本的包络增益；第 `m` 个丢失样本增益为
  `Decay^m`，所以第一个丢失样本保持满幅。
- `Crossfade`：恢复时真实与预测分量的交叉淡化样本数 `L`。
- `Seed`：确定性噪声种子；相同配置、输入和种子会产生逐样本相同的输出。

`DefaultConfig` 以采样率给出 40 ms 统计窗、2–20 ms 周期范围、5 ms 交叉
淡化，并采用 `Decay=0.9999`。

## 算法要点

1. 对最近 `N` 个真实样本计算
   `r[k] = sum(x[i]x[i-k], i=k..N-1) / N`。
2. Levinson-Durbin 返回稳定的全极点预测器；非稳定反射系数不进入模型。
3. 预测残差在周期范围内逐声道归一化相关，再按残差能量加权，因此左右反相
   不会互相抵消。
4. 相关达到阈值时复制一个周期的残差；否则使用逐块零均值、RMS 精确匹配真实
   残差的确定性高斯噪声。噪声块不以宿主分块为单位，任意分块不会改变结果。
5. 合成器每输出一个样本都更新自己的历史，但真实统计窗保持冻结；缺口包络按
   `Decay` 指数衰减。
6. 恢复的前 `L` 个样本按线性权重混合。最后一个混合样本的真实权重为 1，
   之后逐样本直通真实 PCM。

## 验证

```sh
go test ./...
go test -race ./...
```

测试覆盖已知稳定 AR 信号的系数/残差、单位反射系数截断、静音、左右反相、
周期残差、噪声可复现与能量匹配、指数衰减、恢复边界和任意宿主分块一致性。
