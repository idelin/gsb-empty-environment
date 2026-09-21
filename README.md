# bridge — 跨协议版本的离线桥接器

仅用 Go 标准库实现。输入两个 schema（字段 ID、类型、必需性、迁移规则），
编译双向转换计划，在任意分块的字节流上无损桥接两个协议版本。

## 线格式

帧 = 4 字节大端长度前缀 + 载荷。载荷为字段序列：
`varint(fieldID) | byte(type) | varint(len) | bytes`。
schema 不认识的字段以原始字节保留（`Message.Unknown`）。

## 核心概念

- **Schema**（`schema.go`）：字段 ID/类型/必需性；`Defaults` 即迁移规则——
  对端无法提供某字段时注入的默认值。改名只改名字、不改 ID，线上无感。
- **计划编译**（`plan.go`）：`CompilePair` 生成双向计划，逐字段分类为
  `lossless` / `extension`（经扩展 blob 保留）/ `unrepresentable`；
  存在不可表示字段（如 v2 新增必需字段而 v1 无法提供且无默认规则）时
  拒绝协商。每对计划绑定一个 SHA-256 摘要。
- **扩展 blob**（`convert.go`）：目标 schema 无法表示的字段连同未知字段
  打包进保留字段 `ExtFieldID`，回译时解包还原——未知字段经 v1 转存再回
  v2 不丢。schema 定义 `ExtFieldID` 或线上出现伪造 blob 均属扩展冲突，报错。
- **会话与 epoch**（`session.go`）：`Negotiate` 绑定计划摘要；
  `Publish` 热更新计划并开启新 epoch。每个帧在读取长度前缀时绑定当时的
  计划，进行中的帧在新 schema 发布后完成仍按旧规则转换。转换是原子的：
  失败不发布半转换帧，并毒化该 endpoint。
- **分块解析**（`frame.go`）：`FrameDecoder.Feed` 接受任意分块；
  数据不足返回 `(nil, nil)`（缺数据），格式/配额错误返回 `ErrBadData`
  （坏数据）。长度额度在分配帧缓冲之前检查。

## 测试与重放

```sh
go test ./...            # 全部测试（迁移环、扩展冲突、资源超限、往返差异报告等）
go run ./cmd/replay      # 可重放样例：协商 -> 分块转换 -> 帧中途热更新 -> 往返报告
go test -run TestReplayGolden -update .   # 更新 golden
```

`TestReplayGolden` 将样例输出与 `testdata/replay.golden` 逐字节比对。
