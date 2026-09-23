# pkv — PKV1 结构化样本生成 / 单点变异 / 缩减 / 可重放测试框架

纯 Go 标准库实现。对自定义二进制格式 **PKV1** 做属性测试：结构化合法样本生成、
带约束标签的单点变异、往返 / 确定性 / 拒绝行为检查、失败样本自动缩减（保错误类别、
1-minimal 记录）、失败落盘可重放，并用一个已知缺陷解码器验证检测能力。

## PKV1 格式（全部大端）

```
magic    4 B   "PKV1"
count    u16   记录数，必须等于实际解析出的记录数
record   klen u16 | key | vlen u16 | quoted-value     （重复 count 次）
checksum u32   之前全部字节的 CRC32-IEEE
```

约束依赖图（生成器维护，每个约束对应一种单点变异）：

| # | 约束 | 变异 | 期望错误类别 |
|---|------|------|--------------|
| C1 | magic | 翻转 magic 字节，重算 CRC | `magic` |
| C2 | count 一致 | count+1，重算 CRC | `count` |
| C3 | key 长度 | klen 改为 MaxKeyLen+1，重算 CRC | `key_length` |
| C4 | key 字符集 `[A-Za-z0-9_.-]` | key 首字节改为 `=`，重算 CRC | `key_charset` |
| C5 | value 长度一致 | vlen+1（声明≠实际），重算 CRC | `value_length` |
| C6 | 引用/转义 `\" \\ \n \t` | 破坏转义序列或起始引号，重算 CRC | `quoting` |
| C7 | CRC32 校验 | 翻转校验和字节（**不**重算） | `checksum` |
| — | 截断 | 截到 9 字节 | `truncated` |
| — | 仍合法变异 | key 首字节换成另一合法字符，重算 CRC | 必须**接受**且解出期望值 |

每个变异只破坏一个约束，其余前提（长度前缀、引用、校验和）保持合法；
仍合法的变异不会被强判为解析器错误。

## 序列化范围（自行定义）

- key：1..64 字节，字符集 `[A-Za-z0-9_.-]`
- value：可打印 ASCII（0x20..0x7E）加 `\n`、`\t`，编码后 ≤ 4096 字节
- 记录数 ≤ 1024；消息总长 ≤ 65536 字节
- 范围内消息编码超预算 ⇒ 视为**实现缺陷**（`encode_budget`），不记为解析错误
- 解码不执行宿主代码：纯字节解析，无反射/eval/插件；所有长度前缀先验证再分配

## 结构

- `format/`  被测实现（SUT）：规范、canonical 编码器、严格解码器、错误类别
- `oracle/`  独立第二实现（独立类型与代码路径），测试 oracle 不循环调用被测实现
- `gen/`     合法样本生成器 + 单点变异规则（维护语法与约束依赖）
- `shrink/`  失败缩减：候选=删记录/清空或减半 value/缩短 key；谓词保证同一错误类别；
             输出 1-minimal 检查记录
- `harness/` 往返、确定性、拒绝检查；oracle 交叉验证；失败记录（seed + 最小失败字节）
             JSON 落盘与 `Replay` 重放
- `buggy/`   已知缺陷解码器（跳过 CRC 校验），用于验证检测能力
- `cmd/pkvdemo/` 运行样例

## 运行

```
go test ./...
go run ./cmd/pkvdemo
```

## 测试结果（go1.24.2 darwin/arm64）

```
ok  	pkv/format	4.142s
ok  	pkv/harness	4.227s
```
（`go test -race -count=1 ./...` 全绿）

demo 输出要点：clean SUT 300 轮 0 失败；buggy SUT 每轮即被检出
`kind=accept mutation=checksum`，最小失败样本缩减为 10 字节
（空消息 + 损坏 CRC），1-minimal 记录完整，落盘重载后重放仍复现。
