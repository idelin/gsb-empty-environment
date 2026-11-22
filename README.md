堆块释放后仍被读写、或越过块尾一个字节，都需要在运行时被发现。为Go内存错误检测库写可导入的影子内存包，供插桩回调调用：每 8 字节一个影子字节，0 为全部可访问，1 到 7 为前 k 字节可访问，其余负值表示左红区、右红区或已释放；对任意起点和 1、2、4、8、16 字节的访问给出判定与错误类型；释放块进入隔离区，总字节超过上限才从最旧开始回收。用块尾部分字节、跨段访问、释放后使用和隔离区回收验证。

## shadow 包

导入路径：`prescreen.local/ps-4798e0f970/shadow`

影子字节编码（8 个应用字节对应 1 个影子字节）：

- `0`：8 字节全部可访问；
- `1..7`：仅前 k 字节可访问（块尾不足 8 字节的部分单元）；
- `-1` 左红区、`-2` 右红区、`-3` 已释放（隔离区）。

插桩回调入口：

```go
d := shadow.New(
    shadow.WithCapacity(1<<20),
    shadow.WithQuarantineLimit(64<<10),
)
p := d.Alloc(13) // 返回 unsafe.Pointer，两侧带红区

if err := d.Load(p, 8); err != nil {          // 读，1/2/4/8/16 字节
    var ae *shadow.AccessError
    errors.As(err, &ae)
    fmt.Println(ae.Kind) // KindHeapOverflow / KindUseAfterFree / ...
}
if err := d.Store(unsafe.Add(p, 13), 1); err != nil { /* 越块尾 1 字节 */ }

_ = d.Free(p) // 进入 FIFO 隔离区，超限才回收最旧块
```

错误类型见 `shadow.ErrorKind`：`KindHeapUnderflow`、`KindHeapOverflow`、
`KindUseAfterFree`、`KindInvalidAccess`、`KindInvalidSize`。

验证：`go test ./...`（含 `-race`），覆盖块尾部分字节、跨段访问、
释放后使用、隔离区 FIFO 回收与并发场景。
