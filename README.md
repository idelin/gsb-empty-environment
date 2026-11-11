# tsdemux

面向直播录制场景的 MPEG-TS 解复用读取器。录制流普遍存在丢包、
continuity_counter 跳变、重复包以及 PTS/DTS/PCR 回绕，本库在解复用的同时
处理这些传输层异常，并把短位宽时钟展开为单调递增的 64 位时间。

## 能力

- **PAT/PMT 解析与版本更新**：按 `table_id`、`version`、`current_next_indicator`
  管理节目表，整段 MPEG-2 CRC-32 校验，同版本重复广播不重建，新版本原子替换。
- **跨包 PES 重组**：以 `payload_unit_start_indicator` 为边界，按 PID 拼接
  任意长度 PES，解析 PES 头中的 PTS/DTS；无 PES 头字段的特殊 stream ID
  （padding、DSM-CC 等）按规范从固定偏移取负载。
- **连续性处理**：
  - CC 跳变、`transport_error_indicator`、`discontinuity_indicator` 都会把
    当前 PES 标记为 `Corrupted`，并在 `Corruptions()` 中留痕；
  - 相同 CC 的重复包整体跳过（含其 PUSI 标志），不会制造虚假 PES 边界；
  - 仅适配字段包允许 CC 不递增。
- **适配字段与填充**：解析 adaptation field 长度、flags、PCR，正确跳过
  stuffing；PSI section 按 `section_length` 精确截断，不吞入 `0xFF` 填充。
- **时间戳回绕展开**：PTS/DTS（33 位/90kHz）与 PCR（27MHz，周期
  `2^33*300`）各自独立展开；半周期阈值判定回绕，负向抖动钳制保持单调；
  遇到 discontinuity 时复位基准，新时间轴从新原点开始。
- **同步恢复**：流中杂散字节后自动重新定位 `0x47` 同步字节。

## 用法

```go
rd := tsdemux.NewReader(file)
for {
    event, err := rd.Next()
    if err == io.EOF {
        break
    }
    if err != nil {
        log.Fatal(err)
    }
    switch event.Type {
    case tsdemux.EventPES:
        pes := event.PES
        if pes.Corrupted {
            // 丢包/不连续导致受损，按业务策略丢弃或容错
            continue
        }
        _ = pes.PTSUnwrapped // 单调 PTS（90kHz）
        process(pes.PID, pes.Data)
    case tsdemux.EventPCR:
        _ = event.PCR.Time // 单调 PCR（27MHz）
    }
}

fmt.Println(rd.Stats())       // 包数、PES 数、各类异常计数
fmt.Println(rd.Programs())    // 当前节目/PMT/基本流视图
fmt.Println(rd.Corruptions()) // 逐条传输层异常（PID、类型、CC、包序号）
```

## 测试

测试用内置的 TS 流构造器生成合规的 PAT/PMT/PES/PCR，并主动注入：
中间包与起始包丢失、重复包、PTS/DTS 33 位回绕、PCR 回绕、
discontinuity indicator、TEI、CRC 破坏、失步重同步等，逐条核对
PES 边界、负载完整性、受损标记与展开后的时间戳。

```sh
go test ./...
go test -race ./...
```
