# KVS1 结构化生成 / 单点变异 / 缩减 测试框架

纯 Go 标准库实现。围绕自定义二进制格式 `KVS1` 提供：合法样本生成、
单点变异、往返/确定性/拒绝行为检查、失败样本自动缩减（1-minimal，
可重放）、以及对已知缺陷解码器的检测能力验证。

## 格式规范（KVS1）

```
magic   4 字节   "KVS1"
count   uint16LE 条目数，<= 64
entry   重复 count 次:
  klen  uint8    1..32
  key   klen     可打印 ASCII 0x21..0x7E，排除 '='
  vlen  uint16LE 0..512
  value vlen     任意字节
crc32   uint32LE 对之前全部字节的 IEEE 校验和
```

总编码长度不得超过 4096 字节（预算）。

## 序列化范围与安全属性

- 支持范围：有序键值条目列表；键为上述受限字符集、长度 1..32；
  值为任意字节、长度 0..512；条目数 <= 64（允许重复键）；总输出 <= 4096。
- 解码器为纯函数，不执行宿主代码（无反射调用、无外部进程、无插件）。
- 所有长度先与剩余输入核对再分配/拷贝（`format/decode.go`），
  超大 count/klen/vlen 在分配前即被拒绝。
- 编码输出超预算视为实现问题，由 `budget` 检查直接记为失败。

## 结构

- `format/`   被测实现：编码器 + 严格解码器，错误按约束类别分类。
- `oracle/`   独立规范解析器（测试 oracle），与 `format` 不共享任何代码，
  不循环调用被测实现；`fuzz` 用它判定变异后是否仍合法。
- `fuzz/`     生成器（维护语法与长度/字符集/校验和依赖）、单点变异、
  检查器、缩减器。
- `buggy/`    已知缺陷解码器（不校验 CRC32），用于验证检测能力。
- `cmd/demo/` 端到端演示。

## 检查项（`fuzz/check.go`）

- `roundtrip`：decode(encode(x)) == x。
- `determinism`：encode(decode(bytes)) 与原字节完全一致。
- `budget`：编码输出超预算记为实现失败。
- `rejection`：变异样本必须被被测解码器以对应错误类别拒绝。
- `mutation-drift`：变异实际破坏的约束必须与声明一致（由 oracle 判定）。
- 变异后仍合法的样本跳过拒绝断言，不强判解析器错误。

## 单点变异（`fuzz/mutate.go`）

每个变异只破坏一个约束，其余前提保持合法（字段编辑后重算 CRC）：

| 变异 | 破坏的约束 |
|---|---|
| `magic-byte` | magic |
| `count-overflow` | count（设为 65 > 64） |
| `keylen-zero` | keylen（设为 0） |
| `keychar-invalid` | keychar（键内置换行符） |
| `vallen-overflow` | vallen（设为 513 > 512） |
| `crc-flip` | crc |
| `truncated-crc` | truncated（截断先于校验和检查被报告） |

## 缩减与重放

`fuzz.Shrink` 在保持谓词（同一错误类别 + 必需依赖，如"oracle 仍判定
crc 无效且被测解码器仍接受"）为真的前提下做块删除（ddmin 风格）与
字节简化（单调递减保证终止），迭代至不动点；随后逐字节验证
1-minimality 并把每次检查写入日志。`Failure` 记录 seed 与失败字节，
两者即可重放。

## 运行

```sh
go test ./...
go run ./cmd/demo -seed 1 -n 200
```

## 测试结果

```
ok  	kvsfmt/buggy	0.179s   # TestDetectsBuggyDecoder, TestShrinkAndReplay
ok  	kvsfmt/format	0.356s   # 往返/拒绝类别/长度先于分配
ok  	kvsfmt/fuzz  	0.437s   # 生成器确定性/语料检查/oracle 一致性/缩减
```

## 运行样例（`go run ./cmd/demo -seed 1 -n 200`）

```
-- single-point mutations vs reference decoder --
  magic-byte       breaks=magic      oracle=magic      decoder=magic: bad magic
  count-overflow   breaks=count      oracle=count      decoder=count: count exceeds max entries
  keylen-zero      breaks=keylen     oracle=keylen     decoder=keylen: key length out of range
  keychar-invalid  breaks=keychar    oracle=keychar    decoder=keychar: invalid key byte
  vallen-overflow  breaks=vallen     oracle=vallen     decoder=vallen: value length out of range
  crc-flip         breaks=crc        oracle=crc        decoder=crc: checksum mismatch
  truncated-crc    breaks=truncated  oracle=truncated  decoder=truncated: missing checksum

-- corpus check: reference decoder --
  failures: 0

-- corpus check: buggy decoder (known defect: no CRC check) --
  failures: 200
  first failure: kind=rejection mut=crc-flip want=crc got=
  replay: seed=1 bytes=4b5653310600...50bc9db8

-- shrinking first failure (same error class preserved) --
  checks performed: 1279
  minimal failing bytes (115): 4b5653310600...00000000
  replay minimal: reproduces=true
  shrink log (tail):
    check #1279 1-min delete@114       reproduces=false
    result is 1-minimal
```
