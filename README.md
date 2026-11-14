# 连接跟踪表的五元组碰撞改写了NAT

NAT网关的conntrack表在哈希碰撞或zone混用时复用条目，攻击者的连接继承了另一租户的地址映射，流量被导向错误目标。请实现供C++20网关数据面调用的可导入连接跟踪组件。条目键包含zone、五元组、协议状态与租户身份，碰撞按完整键比较而不是哈希；新建、超时、复用与回收按状态机处理，回收前核对引用计数。ICMP错误与分片关联到正确条目。用五元组碰撞、zone混用、超时复用、分片关联和合法连接验证映射不串租户。

## 组件说明

`netsec` 库(`netsec.hpp` / `netsec.cpp`,C++20)提供 `netsec::ConntrackTable`:

- 完整键 `ConnKey` = zone + 租户 ID + 五元组 + 协议状态;哈希只定位桶,桶内按完整键逐一比较,碰撞不误命中。
- 状态机:NEW → ESTABLISHED → FIN_WAIT → TIME_WAIT → DYING;超时经 `tick()` 迁移到 DYING,`reclaim()` 仅回收引用计数为 0 的 DYING 条目,`insert()` 只可复用无引用的 DYING 槽位。
- `process_icmp_error()` 按内嵌原始五元组(正/反两个方向)关联条目;非首片分片按 (zone, tenant, src, dst, proto, ip_id) 关联首片建立的条目。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_AR=/Library/Developer/CommandLineTools/usr/bin/ar \
      -DCMAKE_RANLIB=/Library/Developer/CommandLineTools/usr/bin/ranlib
cmake --build build
ctest --test-dir build --output-on-failure
```

`test_conntrack.cpp` 覆盖:五元组碰撞、zone 混用、超时复用(含引用计数保护)、分片关联、合法连接全生命周期与 ICMP 差错关联。
