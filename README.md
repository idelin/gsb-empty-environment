# 性能计数器事件隔离

云主机上的不可信租户可通过 perf_event_open 读取缓存未命中等计数器，并配合采样地址推断其他租户行为。为 Go 沙箱网关写供其调用的 perf_event_attr 策略包，解析原始属性字节和 pid、cpu 参数，只允许当前线程、排除内核和 hypervisor、禁止 inherit、原始事件及 ADDR、PHYS_ADDR、LBR 采样；size 字段与未知标志位不得绕过。用系统范围 pid=-1、raw 事件、采样地址、旧版结构长度和合法用户态计数验证。
