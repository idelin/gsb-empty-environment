# geofence — 多边形时空地理围栏进出事件判定机

纯 Go 标准库实现，支持时间有效期的动态多边形围栏监控，对实体轨迹流实时判定
**Enter / Dwell / Exit** 事件，并通过迟滞带（hysteresis band）消除边界抖动毛刺。

## 设计要点

- **时间有效期**：围栏仅在 `[ActiveFrom, ActiveUntil)` 窗口内生效；窗口外不产生
  Enter/Dwell，窗口关闭时仍在内部的实体补发 Exit 并复位。
- **动态形状**：可选 `ScaleFunc(t)` 按时间返回缩放因子，围栏绕形心动态缩放。
- **迟滞防抖**：基于点到边界的符号距离（内正外负）。`Outside→Inside` 需距离
  `> +Hysteresis`，`Inside→Outside` 需距离 `< -Hysteresis`；迟滞带内的采样保持
  原状态，杜绝边界抖动穿透。
- **Dwell 判定**：进入后持续停留超过 `DwellAfter` 触发一次 Dwell，不重复上报。
- **多实体**：`Monitor` 按实体 ID 独立维护状态机。

## 文件结构

- `geofence.go` — Point / Polygon（射线法包含判定、符号距离、缩放）、Fence 定义
- `monitor.go` — Monitor 状态机与 Enter / Dwell / Exit 事件
- `geofence_test.go` — 验收场景与边界测试（`go test ./...`）
- `cmd/demo/main.go` — 运行样例（`go run cmd/demo/main.go`）

## 运行

```sh
go test ./... -v        # 运行测试
go run cmd/demo/main.go # 运行样例
```

## 验收场景（已覆盖于测试与样例）

围栏 08:00–10:00 生效：车辆 07:50 进入多边形内部（无事件）→ 08:00 精准触发
Enter → 停留 30 分钟触发 Dwell → 09:05–09:08 在边界 ±1.5 范围反复抖动（迟滞带
±2 内，零毛刺）→ 09:30 真正离开触发 Exit。
