# 性能计数器事件隔离

云主机上的不可信租户可通过 perf_event_open 读取缓存未命中等计数器，并配合采样地址推断其他租户行为。为 Go 沙箱网关写供其调用的 perf_event_attr 策略包，解析原始属性字节和 pid、cpu 参数，只允许当前线程、排除内核和 hypervisor、禁止 inherit、原始事件及 ADDR、PHYS_ADDR、LBR 采样；size 字段与未知标志位不得绕过。用系统范围 pid=-1、raw 事件、采样地址、旧版结构长度和合法用户态计数验证。

## 策略入口

```go
err := netsec.ValidatePerfEventAttr(rawAttr, pid, cpu)
```

`rawAttr` 是传入 `perf_event_open` 的 `struct perf_event_attr` 原始小端字节。
入口要求：

- `pid == 0`，且 `cpu >= -1`；
- 设置 `exclude_kernel` 与 `exclude_hv`；
- 不设置 `inherit`、`inherit_stat`、`inherit_thread`；
- 事件类型不是 `PERF_TYPE_RAW`；
- `sample_type` 不含 `ADDR`、`PHYS_ADDR`、`BRANCH_STACK`，且
  `branch_sample_type == 0`；
- 属性长度必须与声明的 `size` 完全一致，`size` 必须是已发布的布局且至少包含
  `branch_sample_type` 字段；未知标志位会被拒绝。

`ParsePerfEventAttr` 可单独用于检查并读取属性字段。
