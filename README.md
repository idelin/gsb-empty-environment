审计卷只授权追加，普通写权限包装仍可能允许覆盖旧字节。写供Go服务调用的追加句柄包，写入偏移由服务端原子分配，多段写允许短写但只暴露已提交连续前缀；调用方不得自行移动位置。取消后未提交预留必须填成显式空洞而非交给其他请求复用，用故障文件测试并发追加及中断恢复。

## appendhandle

`appendhandle` 包把底层文件包装成只追加的审计卷：

- `Open`/`OpenVolume` 打开卷，`Reserve(size)` 在互斥锁内原子分配下一段偏移并扩展文件，调用方拿到的 `*Handle` 不暴露 `Seek`，写入位置只能是“服务端偏移 + 已接受字节数”。
- `Handle.Write(ctx, p)` 支持短写与瞬时存储故障，返回已接受字节数；再次调用从第一个未写字节继续，直到占满预留。
- 只有连续已提交前缀可通过 `Volume.ReadAt`/`Volume.Size` 观察到，跨前缀的读被裁剪并返回 `io.EOF`。
- `Commit` 按偏移顺序推进前缀并在每步 `Sync`；`Sync` 失败或 `ctx` 超时不改变状态，可重试。
- `Abort`（或 `Write` 期间 `ctx` 取消）对整段预留调用 `PunchHole`：Darwin 用 `F_PUNCHHOLE`、Linux 用 `fallocate(PUNCH_HOLE|KEEP_SIZE)` 真正释放块，其他平台退化为写零；空洞偏移永不复用，后续 `Reserve` 只向高水位之后扩展。

测试用 `FaultFile`/`MemFile` 注入短写、周期性写错误和 `Sync` 故障，并对重叠写做硬性检测，覆盖并发追加、中断恢复、取消空洞化、提交排序和真实文件块释放（见 `appendhandle/*_test.go`）。
