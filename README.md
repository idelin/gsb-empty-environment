# safearch — SAR 安全归档工具

用 Go 标准库实现的最小防篡改归档格式（SAR v1）与配套 CLI，重点解决：
路径规范化与校验、大小与摘要校验、解包防路径穿越与符号链接逃逸、
写入先暂存再发布、失败不留部分正式输出。

## 序列化范围（自定义）

- 仅支持**普通文件**与**目录**两种条目；权限位只保留 `0o000–0o777`。
- 符号链接、设备、FIFO、套接字等特殊文件：打包时直接报错拒绝，
  格式上根本无法表示，解码器永远不可能创建它们。
- 解码**不执行宿主代码**：条目按固定小端线格式手工解析，
  不使用 gob/反射/插件等任何可触达宿主行为的机制。
- 条目按路径排序后写入，打包结果确定性可复现。

## 线格式（整数均为小端）

```
magic    4B   "SAR1"
version  1B   0x01
entries  若干条目，以单个 0x00 字节终止
digest   32B  此前全部字节的 SHA-256（整体摘要）

文件条目: type=1 | nameLen u16 | name | mode u32 | size u64 | payload | sha256(payload) 32B
目录条目: type=2 | nameLen u16 | name | mode u32
```

## 安全设计

- **路径规范化**：`ValidatePath` 拒绝绝对路径、`..`/`.`/空分量、反斜杠、
  盘符前缀、非 UTF-8 与超长名；`../目标` 与绝对路径一律拒绝。
- **先验证长度再分配**：nameLen（≤4096）、条目数、单文件/总大小均先过
  `Limits` 检查再分配或落盘；负载始终流式处理，不全量进内存。
- **大小与摘要校验**：每条目 SHA-256 + 整体 SHA-256 双摘要；声明大小与
  实际负载不符、任何字节被篡改、截断、尾随垃圾都会失败。
- **防路径穿越**：词法校验之外，`safeJoin` 再做根目录包含性检查，
  并逐分量 Lstat 确认无符号链接；暂存目录为全新私有目录（0700），
  攻击者无法预置链接，符号链接逃逸被结构性杜绝。
- **重复路径不覆盖**：条目集合查重（文件/目录冲突也算），落盘使用
  `O_CREATE|O_EXCL`，重复路径永远无法覆盖先写条目。
- **先暂存再发布**：解包先完整解到临时目录并全部校验通过，再 `rename`
  原子发布；打包先写临时文件 fsync 后再 rename。目标已存在即拒绝，
  任何失败都清理暂存，不留部分正式输出。

## 使用

```sh
go build -o sar ./cmd/sar
sar pack <目录> <输出.sar>     # 目标已存在则拒绝
sar list <归档>                # 列出条目（流式校验，不落盘）
sar unpack <归档> <目录>       # 解到全新目录，已存在则拒绝
```

库 API：`Pack` / `PackFile` / `Unpack` / `List` / `ValidatePath`，
错误均为哨兵错误，可用 `errors.Is` 分类（如 `ErrUnsafePath`、`ErrDigest`）。

## 运行样例（实录）

```console
$ ./sar pack src app.sar && ls -l app.sar
-rw-------@ 1 delin  wheel  1278 Sep 23 23:26 app.sar
$ ./sar list app.sar
dir  -rwxr-xr-x        0  bin
file -rw-------     1024  bin/blob.bin
dir  -rwxr-xr-x        0  docs
file -rw-r--r--       9  docs/readme.md
file -rw-r--r--      10  hello.txt
5 entries
$ ./sar unpack app.sar out
extracted to out
$ diff -r src out && echo identical
identical
$ ./sar unpack app.sar out            # 重复发布：拒绝覆盖
sar: destination exists, refusing to overwrite: safearch: output already exists: "out"
$ ./sar unpack tampered.sar out2      # 篡改 1 字节：摘要校验失败，无残留
sar: safearch: digest mismatch: overall
$ ./sar unpack evil.sar out3          # 含 ../evil.txt 的恶意归档：拒绝
sar: safearch: unsafe path: bad component in path "../evil.txt"
```

## 测试结果（实录）

`go test -race ./...`：15 个测试（含子测试共 40+ 用例）全部通过。

```console
ok  	safearch	0.6s
--- PASS: TestRoundTrip                 含确定性打包、权限位与内容逐树对比
--- PASS: TestEmptyArchive
--- PASS: TestValidatePath              ../、绝对路径、反斜杠、盘符、非UTF-8等
--- PASS: TestFormatGuards              魔数/版本/截断/尾随/未知类型/声明大小/特权位
--- PASS: TestDigestTamper              负载、条目摘要、整体摘要、名节任一字节翻转
--- PASS: TestUnpackRejectsTraversal    6 种穿越路径，目标外零文件
--- PASS: TestUnpackRejectsDuplicates   重复文件/目录、文件当父、目录覆盖文件
--- PASS: TestLimitsEnforced            单文件/总量/条目数/超大声明先验拒绝
--- PASS: TestPackRejectsSymlinks       源含符号链接直接失败
--- PASS: TestPackRejectsSpecialFiles   FIFO 等特殊文件拒绝
--- PASS: TestPublishNeverOverwrites    打包与解包均不覆盖已存在目标
--- PASS: TestFailureLeavesNoPartialOutput 失败后无部分输出、无暂存泄漏
--- PASS: TestPackFileRoundTrip
--- PASS: TestErrorsAreSentinels
--- PASS: TestListStreamsPayloads
```

## 文件布局

- `sar.go` — 常量、哨兵错误、`Limits`、路径校验、条目查重
- `scan.go` — 解码器：先验证长度再分配、流式负载、双摘要校验
- `pack.go` — 打包（拒绝特殊文件）与原子发布 `PackFile`
- `unpack.go` — 暂存解包、符号链接逐分量检查、rename 发布
- `cmd/sar/main.go` — CLI
- `sar_test.go` / `attack_test.go` / `mkfifo_test.go` — 测试
