# kinematics — 移动物体时空范围速度与加速度极值探测器

多维运动学指标违规监测组件（纯 Go 标准库）。在流式轨迹上基于
**滑动窗口多项式拟合**（Savitzky-Golay 风格最小二乘）估计一阶瞬时速度与
二阶加速度，平滑 GPS 微小定位噪声；检测到**超速、急加速、急刹车、
向心加速度超限（侧翻风险）**时输出覆盖违规前后各 10 秒的完整运动学证据链。

## 结构

- `polyfit.go` — 最小二乘多项式拟合（正规方程 + 部分主元高斯消元）
- `kinematics.go` — 滑动窗口微分器：窗口内时间归一化到 [-1,1]，
  位置/速度/加速度由拟合多项式解析求值；加速度分解为切向与法向（向心）分量
- `detector.go` — 流式状态机：`Add(Sample)` 增量计算，`Flush()` 收尾；
  连续 N 点违规防抖立案，事件结案后经 `OnEvent` 回调输出证据链 `Event.Curve`
- `cmd/hazmat-demo` — 危险品运输车过弯监控运行样例

## 运行样例

```
$ go run ./cmd/hazmat-demo
⚠ 事件 #1  违规时段 [30.2s, 37.7s]  类型: excessive_centripetal
  峰值违规: excessive_centripetal t=32.0s value=7.03 limit=4.00
  证据链: 275 个运动学采样点，覆盖 [20.2s, 47.8s]（违规前后各约 10 秒）
  t(s)   速度(m/s)  切向加速度(m/s²)  向心加速度(m/s²)  违规
  ----   ---------  ---------------  ---------------  ----
   20.2      12.19            -0.40             0.09
   ...
   30.4      11.68             0.13             4.73  excessive_centripetal
   32.4      11.70             0.72             6.21  excessive_centripetal
   34.4      11.79             0.14             6.52  excessive_centripetal
   36.4      11.68             0.02             5.24  excessive_centripetal
   37.4      11.56             0.55             5.86  excessive_centripetal
   38.4      11.77             0.69             1.62
   ...
共结案 1 起违规事件
```

场景：危险品运输车（10Hz GPS，定位噪声 σ=0.4m）以约 12 m/s 进入半径 25m
转弯道，瞬时向心加速度 v²/r≈5.76 m/s² 超过侧翻安全阈值 4.0 m/s²（≈0.41g），
被准确捕获；直行段向心加速度估计贴近 0，无误报。

## 测试结果

```
$ go test -v ./...
=== RUN   TestPolyFitExact
--- PASS: TestPolyFitExact (0.00s)
=== RUN   TestSmoothsGPSNoise
    max speed err 0.3253 m/s, max spurious accel 1.1641 m/s²
--- PASS: TestSmoothsGPSNoise (0.00s)
=== RUN   TestCurveRolloverDetection
    event #1 [30.3, 37.7]s mean=5.57 peak=6.70 m/s² (theory 5.76), evidence 274 points [20.3, 47.8]s
--- PASS: TestCurveRolloverDetection (0.01s)
=== RUN   TestHarshBraking
--- PASS: TestHarshBraking (0.00s)
=== RUN   TestOverspeed
--- PASS: TestOverspeed (0.00s)
=== RUN   TestNormalDrivingNoFalsePositive
--- PASS: TestNormalDrivingNoFalsePositive (0.00s)
=== RUN   TestStreamingCallbackAndFlush
--- PASS: TestStreamingCallbackAndFlush (0.00s)
PASS
ok  	kinematics
```

覆盖：多项式拟合精确性、GPS 噪声平滑（匀速段速度/加速度估计误差）、
弯道侧翻阈值超限捕获与 ±10s 证据链完整性、急刹车、超速、
正常驾驶零误报、流式回调与 Flush 结案。
