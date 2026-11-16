# JNI 临界区指针使用检测

`netsec` 是供 Go 宿主接入 JNI 调用跟踪的临界区检测包。宿主把
`GetPrimitiveArrayCritical`/`GetStringCritical`、`Release...Critical`、
其他 JNI 调用、阻塞调用、指针读写和线程结束事件送入 `Detector`，检测器会
按线程维护临界区栈并报告违规。

## 快速开始

```go
detector := netsec.NewDetector()
unsubscribe := detector.Subscribe(func(f netsec.Finding) {
    log.Printf("%s", f)
})
defer unsubscribe()

detector.Record(netsec.GetEvent("worker-1", "arr-1", 0x1000, 8))

// 临界区内调用其他 JNI 函数：违规。
detector.Record(netsec.JNICallEvent("worker-1", "NewStringUTF"))

// 数组只有 8 字节，从偏移 8 写 1 字节：越界。
detector.Record(netsec.WriteEvent("worker-1", 0x1008, 1))

detector.Record(netsec.ReleaseEvent(
    "worker-1", "arr-1", 0x1000, 8, netsec.ReleaseModeDefault,
))

// 释放后写入旧指针：UseAfterRelease。
detector.Record(netsec.WriteEvent("worker-1", 0x1000, 1))
```

`Record` 返回本条事件产生的新 `Finding`；`Feed` 可以按顺序送入多条事件。
`Findings` 返回全部历史结果，`Reset` 清空线程状态和结果。

## 事件

| 事件 | 用途 | 关键字段 |
| --- | --- | --- |
| `EventGet` | 成功的 Get...Critical | `Thread`、`Array`、`Address`、`Length`（元素数）、`ElementSize`（字节） |
| `EventRelease` | Release...Critical | `Thread`、`Array`、`Address`、`Length`、`Mode` |
| `EventJNICall` | 其他 JNI 调用 | `Thread`、`Function` |
| `EventBlockingCall` | 可能阻塞的调用 | `Thread`、`Function` |
| `EventRead` / `EventWrite` | 临界区指针读写 | `Thread`、`Address`、`Length`（字节） |
| `EventThreadEnd` | JNI 线程结束 | `Thread` |

`Event` 也可以直接构造。`EventRead`/`EventWrite` 的 `Array` 字段是可选的：
当地址远离数组自身范围、无法用地址判定归属时，填入 `Array` 可明确关联到
活跃数组。`Size` 非零时优先于 `Length` 作为访问字节数。

## 检测项

| Finding | 严重级别 | 含义 |
| --- | --- | --- |
| `JNICallInCritical` | violation | 临界区内调用除 Get/Release...Critical 外的 JNI 函数 |
| `BlockingInCritical` | violation | 临界区内执行可能阻塞的调用 |
| `OutOfBoundsWrite` | violation | 写入超出活跃数组范围（按元素数与元素大小计算） |
| `OutOfBoundsRead` | violation | 读取超出活跃数组范围 |
| `UseAfterRelease` | violation | 读取或写入已释放数组的指针范围 |
| `DuplicateRelease` | violation | 同一数组已经释放后再次释放 |
| `UnreleasedAtThreadEnd` | violation | 线程结束时仍有临界区指针未释放 |
| `WritesDiscarded` | warning | `JNI_ABORT` 释放时丢弃了临界区内已记录的写入 |
| `InvalidRelease` | warning | Release 未匹配任何活跃或已释放数组 |

行为约定：

- 同一线程允许多层嵌套 `Get...Critical`，检测器按栈跟踪；Release 可按
  数组标识或指针地址匹配。
- `JNI_COMMIT` 会复制并清空尚未提交的写入，但临界区保持活跃；随后仍可被
  其他 JNI 调用或线程结束检测。`JNI_ABORT` 结束临界区，并将已记录写入作为
  丢弃项提示。
- 已释放数组会保留地址历史，用于 `UseAfterRelease`；同一地址重新 Get 后，
  活跃范围优先，不会误报。
- 线程标识需要在一段跟踪内保持唯一且稳定。
- `Detector` 可并发使用，不同线程的事件可并发送入；订阅回调在检测器锁之外
  执行。

## 验证

测试覆盖临界区内 `NewStringUTF`、越界一字节、释放后读写、重复释放、合规
读写、嵌套 Get、线程结束未释放、`JNI_ABORT` 丢弃提示、订阅和并发隔离。

```sh
go test ./...
go test -race ./...
go vet ./...
```
