# Multi-Paxos 多数派共识与无空洞日志提交引擎（Go 标准库）

无单点故障的分布式共识日志协议实现，含持久化编解码与序列化零拷贝还原。
仅使用 Go 标准库。

## 结构

- `paxos/` — Multi-Paxos 共识核心
  - `node.go` — Prepare/Promise + Accept/Accepted 两阶段协议；领导者租约下
    绕过 Phase 1 直接提交连续 LogEntry；新领导者 Phase 1 补全未提交槽位
    （最高 ballot 值优先，空洞填 no-op）；无空洞提交引擎（只按槽位顺序提交
    多数派确认的连续前缀）；少数派脏日志被高 ballot 已提交值安全覆写。
  - `network.go` — 可控分区的进程内传输（单向/双向断链、分组分区、消息计数）。
  - `persister.go` — 内存/文件两级持久化（文件级崩溃安全：追加 + fsync +
    重启时撕裂尾部截断）。
- `store/` — 紧凑二进制持久化
  - `codec.go` — varint 版本化编码（v1/v2），逐字段边界校验（长度上限、
    varint 溢出、声明长度 > 剩余字节），零拷贝解码（Command 别名输入缓冲）。
  - `segment.go` — 段文件：magic 头 + 格式版本 + `crc32|len|payload` 记录帧；
    长度帧实现向前兼容（跳过未知尾部字段），缺失字段补零实现向后兼容；
    撕裂尾部写被识别为截断而非损坏。
  - `mmap_unix.go` — `syscall.Mmap` 只读映射解析（非 unix 平台回退为读入内存）。
- `cmd/demo/` — 验收场景运行样例。

## 运行样例

```
go run ./cmd/demo
```

输出（5 节点集群：旧 Leader 被分区取代 → Phase 1 补全 → 脏日志安全覆写）：

```
== 1. elect node1; slot1 commits cluster-wide ==
   node1 is leader
   proposed slot=1 cmd="set x=1"
   node5 applied=[1:set x=1]
== 2. ack links 2..5->1 blocked: slot2 accepted everywhere, uncommitted ==
   proposed slot=2 cmd="set a=2"
   node1 commitIndex=1 (slot2 accepted but not committed)
== 3. partition {1,2}|{3,4,5}; old leader dirties slot3 on minority ==
   proposed slot=3 cmd="set x=3"
   node1 commitIndex=1, dirty slot3 on {1,2}
== 4. node3 wins majority; Phase 1 carries slot2 forward ==
   node3 is leader
   proposed slot=3 cmd="set y=3"
   node3 applied=[1:set x=1 2:set a=2 3:set y=3]
== 5. heal; leader syncs: dirty slot3 on {1,2} safely overwritten ==
   node1 commitIndex=3 applied=[1:set x=1 2:set a=2 3:set y=3]
   node2 commitIndex=3 applied=[1:set x=1 2:set a=2 3:set y=3]
   node3 commitIndex=3 applied=[1:set x=1 2:set a=2 3:set y=3]
   node4 commitIndex=3 applied=[1:set x=1 2:set a=2 3:set y=3]
   node5 commitIndex=3 applied=[1:set x=1 2:set a=2 3:set y=3]
== 6. persistence: replay node1's log via zero-copy mmap ==
   segment format=v2, zero-copy entries:
     slot=1 term=1 node=1 flags=0 cmd="set x=1"
     slot=2 term=1 node=1 flags=0 cmd="set a=2"
     slot=3 term=1 node=1 flags=0 cmd="set x=3"   <- 脏值
     slot=2 term=2 node=3 flags=0 cmd="set a=2"
     slot=3 term=2 node=3 flags=0 cmd="set y=3"   <- 高 ballot 覆写
demo OK
```

## 测试

```
go test -race ./...
```

结果（29 个测试全部通过，race 检测无告警）：

```
ok  multipaxos/paxos   — 选举/提交、租约绕过 Phase 1、租约过期、
                          分区脑裂验收场景、空洞 no-op 填充、旧 Leader
                          无法提交、并发提案(-race)、持久化重放、goroutine 无泄漏
ok  multipaxos/store   — v1/v2 编解码往返、零拷贝别名断言、逐字节截断、
                          长度/varint 溢出防护、前向/后向兼容、CRC 校验、
                          撕裂尾部容忍、超限记录、并发 mmap 读取(-race)、
                          fd/goroutine 无泄漏、崩溃恢复续写、v1 文件兼容
```
