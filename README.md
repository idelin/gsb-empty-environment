# bigtx — 大事务分片传输与内存保护限流器

纯 Go 标准库实现。应对单个事务包含百万级行变更的场景：

- **自动分片**：事务行数突破阈值（`Threshold`）时，`Splitter` 将变更流切分为
  带阶段标记的子分片（`BEGIN / MIDDLE / END`，小事务为 `SINGLE`），逐片流式发送，
  源端通过迭代器（`ChangeIter`）惰性产出变更，从不把整事务物化到内存。
- **内存保护限流**：`MemLimiter` 是按字节计数的信号量，为在途分片提供背压；
  超出内存预算时 `Transport` 自动把分片溢写临时磁盘文件（`Spool`），接收端再逐片
  读回，防止接收端 OOM。
- **完整性追踪**：每个分片带序号与 FNV-1a 校验和；终结分片携带总分片数、总行数
  与全事务校验和，`Reassembler` 在接收端校验阶段标记、序号连续性、行数与校验和，
  任何篡改 / 乱序 / 截断都会报错。

## 结构

```
xport/chunk.go       变更、分片、阶段标记与校验和
xport/splitter.go    大事务 -> 有界分片流（滞后一片以便标记 END）
xport/limiter.go     字节级内存限流器（背压 + 峰值统计）
xport/spool.go       内存超额时的临时磁盘溢写 / 读回
xport/receiver.go    接收端重组与完整性校验
xport/transport.go   串联以上组件的流传输器
cmd/demo/main.go     运行样例：50 万行大事务流式传输
```

## 运行样例

```console
$ go run ./cmd/demo            # 参数: -rows 500000 -batch 5000 -mem 1048576
transaction #1: 500000 rows applied in 209ms
chunks: 100 (in-memory 83, spilled to disk 17 / 4.5 MB)
limiter peak reserved: 0.80 MB (budget 1.00 MB)
process heap growth:   6.27 MB (acceptance bound 10 MB)
integrity: OK (stage markers, sequence, totals and checksum verified)
```

## 测试

```console
$ go test ./... -race -count=1
ok  	github.com/example/bigtx/xport	2.897s
```

验收测试 `TestLargeTransactionStreaming`（`xport/transport_test.go`）：

- 源库一次大更新产生 **500,000** 条变更（迭代器惰性生成，不物化）；
- 传输器切分为 **100 个分片**流式写出，内存预算 1MB，超额部分溢写临时磁盘；
- 接收端逐行应用并校验事务完整性（阶段标记 / 序号 / 总行数 / 校验和）；
- 后台采样进程堆内存，断言 **峰值堆增长 < 10MB**（实测约 5.8MB）。

其余单测覆盖：阶段标记序列、小事务单分片、限流器阻塞/唤醒与峰值、
溢写文件往返、篡改校验和 / 乱序 / 截断 / 篡改总数的完整性失败检测。
