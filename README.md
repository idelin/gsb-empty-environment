# fmp4mux

直播打包服务要把H.264访问单元写成fMP4分片。给Go打包服务实现分片写入器：生成初始化段和moof、mdat，trun中的样本时长、大小、关键帧标志和合成时间偏移正确，含B帧时使用有符号偏移；分片只能从IDR开始，每个分片最后一个样本的时长由下一分片首帧推算；同时输出sidx。再写解析代码读回全部box，核对解码顺序和显示时间与输入一致。

## 实现

纯标准库实现，无外部依赖。

- `fmp4mux.go`：`Sample`/`Config` 类型、box 与 full-box 拼装基础工具。
- `avcc.go`：Annex B 与 4 字节长度前缀两种输入的 NAL 拆分、AVCC 样本转换、SPS/PPS 抽取、`avcC` 配置记录。
- `initsegment.go`：初始化段 `ftyp` + `moov`（含 `mvhd/trak/mdia/minf/stbl` 与 `mvex/trex`）。
- `fragmenter.go`：分片写入器 `Fragmenter`，输出 `styp/moof/mdat` 与独立的 `sidx`。
- `parse.go`：通用递归 box 解析器（含 full-box、`stsd` 视觉 sample entry）。
- `readback.go`：高层读回 API，重建每个样本的 DTS/CTO/PTS、关键帧标志和 mdat 字节，并解析 `sidx`。

## 关键行为

- 分片只允许从 IDR 开始，首个样本不是 IDR 时返回 `ErrFragmentStart`。
- `trun` 逐样本携带 duration/size/flags/CTO；只要分片内出现非零 CTO 即使用 version 1
  有符号偏移，正确表达 B 帧的负 composition offset；无 B 帧时退化为 version 0 且不带 CTO 字段。
- 分片内样本时长由相邻 DTS 差分得到；最后一个样本的时长在遇到下一个 IDR 时由其 DTS 推算
  （`Close` 收尾时回退为前一帧间距）。
- `tfdt` 使用 version 1（64 位 baseMediaDecodeTime），`sidx` 使用 version 1（64 位 EPT）。
- `trun.data_offset` 指向 mdat 负载起点，解析器据此切分每个样本的原始字节。
- SPS/PPS 可在 `Config` 显式提供（立即生成初始化段），也可从首个 IDR 自动抽取。
- 最早呈现时间为负时返回 `ErrNegativePTS`（sidx 的 EPT 为无符号数）。

## 用法

```go
f := fmp4mux.NewFragmenter(fmp4mux.Config{
    Timescale: 90000,
    Width: 1280,
    Height: 720,
    // SPS/PPS 省略时从首个 IDR 自动抽取
})

for _, s := range samples { // samples 按解码顺序排列
    seg, sidx, err := f.PushSample(s)
    // 新 IDR 到达时返回上一分片及其 sidx；否则 seg == nil
    _ = seg
    _ = sidx
}
lastSeg, lastSIDX, err := f.Close() // 收尾分片

init := f.InitSegment() // 初始化段

// 读回校验
parsed, err := fmp4mux.ParseMediaSegment(append(sidx, seg...))
for _, got := range parsed.Samples {
    // got.DTS / got.CTO / got.PTS / got.SyncSample / got.Data
}
```

## 测试

`go test ./...` 覆盖：B 帧 GOP 往返（解码顺序 DTS 与显示顺序 PTS 全量核对）、
有符号 trun、跨分片末帧时长推算、非均匀帧间距、Annex B/长度前缀输入、
非 IDR 起始拒绝、sidx 字段、完整 box 层级与字节级 trun 布局。
