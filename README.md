# 多边形时空地理围栏进出事件判定机

纯 Go 标准库实现，无第三方依赖。

## 能力

- **时间有效期**：围栏仅在 `[ValidFrom, ValidTo)` 内生效；失效期间静默复位，重新生效时若实体已在内部则在该时刻触发 `Enter`。
- **动态形状**：`ScaleFn(t)` 支持多边形相对质心随时间缩放。
- **事件判定**：实时输出 `Enter` / `Dwell`（停留超阈值，仅一次）/ `Exit`。
- **迟滞消抖**：基于带符号距离的迟滞带 `[-H, +H]`，进入需深入边界 `+H`，离开需越出 `-H`，带内状态保持，杜绝边界抖动穿透。

## 运行

```sh
go test ./... -v     # 测试（含验收场景）
go run ./cmd/demo    # 运行样例
```

## 用法

```go
fence := &geofence.Fence{
    ID: "yard-1",
    Polygon: []geofence.Point{{0,0},{10,0},{10,10},{0,10}},
    ValidFrom: t0800, ValidTo: t1000,
    Hysteresis: 0.5, DwellAfter: 30*time.Minute,
}
mon := geofence.NewMonitor(fence)
events := mon.Process("vehicle-001", geofence.Point{5,5}, time.Now())
```
