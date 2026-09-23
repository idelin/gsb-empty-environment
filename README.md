# rangecoder

带检查点的进位无关范围编码器（carry-less range coder, Subbotin 风格）+
自适应 0 阶频率模型。纯 Go 标准库实现，不调用任何现成压缩库。

## 特性

- **检查点重置模型快照**：每个检查点冲刷并重置范围编码器（字节对齐），
  并快照自适应模型；段与段完全独立。
- **随机解码**：任意一段可仅凭其前驱检查点独立解码。
- **断点续编码**：`ResumeEncoder` 从检查点截断码流、恢复模型继续编码；
  只要数据与检查点策略相同，产出的码流与连续运行**逐字节一致**。
- **模型状态可验证序列化**：magic + 版本 + 长度 + CRC32，并校验
  频率总和、界值等内部一致性。
- **格式版本/长度/校验明确**：版本不符拒绝（`ErrUnsupportedVersion`），
  长度或 CRC 不符拒绝（`ErrBadLength` / `ErrBadChecksum`）。
- **解码输出额度有界**：所有解码入口都要求 `maxSymbols` 上限，
  输出绝不超额；遇 EOF 符号提前终止。
- **前段损坏不污染后段**：后段从前置检查点重建模型与比特对齐，
  前段字节损坏不影响后段解码（见 `TestCorruptionIsolation`）。

## 二进制格式（整数均大端）

码流 `RCST`：`magic(4) version(2) flags(2) payload...`，
payload 为若干段范围编码字节，每个检查点处冲刷对齐。

检查点 `RCKP`：`magic(4) version(2) flags(2) length(4) payload crc32(4)`；
payload = `offset(8) symbolIndex(8) model(...)`。

模型 `RMDL`：`magic(4) version(2) nsym(2) total(4) freq[257]x(4)`，
加载时校验 `total == sum(freq)` 及每个频率的界值。

检查点表 `RCKT`：`magic(4) version(2) count(4) (reclen(4) record)... crc32(4)`。

## 使用

```go
enc := rangecoder.NewEncoder()
for _, chunk := range chunks {
    enc.Encode(chunk)
    enc.Checkpoint()          // 段边界：重置编码器 + 快照模型
}
stream := enc.Finish()
table := rangecoder.MarshalCheckpointTable(enc.Checkpoints())

// 整流解码（按段接力，额度有界）
data, err := rangecoder.DecodeSegments(stream, cps, maxBytes)

// 从检查点随机解码某一段
d, _ := rangecoder.NewDecoder(stream, cps[i])
seg, _ := d.Decode(cps[i+1].SymbolIndex - cps[i].SymbolIndex)

// 断点续编码（码流与连续运行一致）
re, _ := rangecoder.ResumeEncoder(stream, cps[i])
```

## 运行

```sh
go test ./...                          # 全部测试
go run ./cmd/rcdemo -seg 8192          # 演示：编码/随机解码/续编码校验
go run ./cmd/rcdemo -in file -out out  # 压缩文件，生成 out.rc + out.ckt
```
