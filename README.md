# jitterbuf

语音网关收到的RTP包会乱序、重复和迟到。给Go语音网关实现嵌入接收路径的抖动缓冲组件：按序列号（处理回绕）和RTP时间戳排序，目标延迟随到达抖动估计自适应调整，只在静音段增减延迟；迟于播放点的包丢弃并计数，长时间断流后重新同步，时间戳跳变当作新话段。用虚拟时钟驱动到达序列，核对每次取帧的结果和延迟变化。

## 设计

`JitterBuffer` 嵌入 RTP 接收路径，用可注入的 `Clock` 驱动；生产环境用
`WallClock`，测试用 `VirtualClock` 手动推进时间，从而确定性地复现任意
到达序列。

- 排序：16 位序列号与 32 位 RTP 时间戳都按 RFC 3550 风格扩展为 64 位，
  正确处理前向回绕与乱序/迟到包；内部用最小堆按扩展序列号出帧。
- 自适应延迟：按 RFC 3550 A.8 用平滑抖动 `J += (|D| - J)/16` 估计到达
  抖动，目标延迟 `clamp(MinDelay, J + DelayMargin, MaxDelay)`。
- 只在静音段改延迟：话段进行中每帧严格相隔一个帧长，新目标延迟只在下一
  个话段首帧生效；M 位、SID 帧或时间戳跳变（超过 `JumpFrames` 帧）都
  视为新话段。
- 迟到与重复：播放点之后到达的包计入 `LateDropped` 丢弃；已在缓冲中的
  重复序列号计入 `Duplicates`；缺帧时 `Pop` 返回 `PopLate` 供上层做丢包
  补偿，并计入 `Lost`。
- 断流重同步：到达间隔超过 `ResyncThreshold` 时清空缓冲（计入
  `Flushed`/`Resyncs`），下一个包重新锚定播放时间线。

## 用法

```go
clock := jitterbuf.NewVirtualClock(time.Unix(0, 0))
jb, err := jitterbuf.New(jitterbuf.DefaultConfig(), clock)
if err != nil {
    log.Fatal(err)
}

// 接收路径：每个到达包调用 Push（用当前时钟）。
jb.Push(jitterbuf.Packet{Seq: seq, Timestamp: ts, Marker: marker, Payload: p})

// 播放路径：每帧周期调用 Pop。
switch r := jb.Pop(); r.Status {
case jitterbuf.PopOK:
    play(r.Packet.Payload)                 // r.FrameDelay 为本帧实际缓冲延迟
case jitterbuf.PopLate:
    concealOneFrame()                      // 丢包补偿
case jitterbuf.PopNotReady:
    // 首帧尚未攒够或缓冲暂时为空
}
```

## 测试

`jitterbuf_test.go` 全部由 `VirtualClock` 驱动，逐次核对取帧状态、计划
播放时刻与目标延迟变化，覆盖：顺序播放、乱序与 16/32 位回绕、重复、迟到
丢弃、缺帧、抖动自适应且话段内不漂移、长断流重同步、时间戳跳变新话段、
配置校验与并发访问。

```sh
go test -race ./...
```
