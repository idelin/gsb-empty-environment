# JNI 临界区指针使用检测

`GetPrimitiveArrayCritical` 可能把 Java 数组的直接指针交给原生代码。临界区内调用其他 JNI 函数或阻塞会让虚拟机无法推进 GC；释放后继续使用指针、重复释放、越界写则会造成内存安全问题。

本包是纯 Go 的 JNI 跟踪事件消费者：宿主把 JNI wrapper、JVMTI、uprobe 等来源产生的 Get、Release、其他 JNI 调用、阻塞调用和指针读写事件送入 `Detector`，由它按线程维护临界区状态并产生结构化告警。本包不自行拦截 JVM。

## 快速使用

```go
detector := netsec.NewDetector()
cancel := detector.SubscribeFindings(func(f netsec.Finding) {
	log.Printf("%s thread=%s array=%s offset=%d: %s",
		f.Kind, f.Thread, f.Array, f.Offset, f.Message)
})
defer cancel()

// GetPrimitiveArrayCritical 返回 ptr，数组有 4 个 int32 元素。
detector.Get("java-main", "jarray:42", ptr, 4, 4)

// 合规访问：从 ptr 开始的 4 个字节。
detector.Write("java-main", ptr, 4)

// 临界区内调用 NewStringUTF 会立即报告 jni_call_in_critical。
detector.JNICall("java-main", "NewStringUTF")

detector.Release("java-main", "jarray:42", netsec.ReleaseCopyBack)
```

也可以直接送入 `Event`，适合把已有跟踪流原样接入：

```go
detector.Observe(netsec.Event{
	Kind:        netsec.EventGetCritical,
	Thread:      "worker-7",
	Array:       "jarray:99",
	Address:     ptr,
	ArrayLength: length,
	ElementSize: 1,
})
```

`ThreadID` 和 `ArrayID` 都是字符串，可放原生线程号、goroutine 标识或 jobject 句柄的稳定表示。

## 事件与长度

- Get/Release：`Array` 是数组标识，`ArrayLength` 是元素个数，`ElementSize` 是单元素字节数（缺省为 1）。
- 指针读写：`Address` 是起始地址，`Length` 是字节数。
- JNI/阻塞调用：`Function` 是函数名，阻塞事件可带 `Duration`。
- 每个归一化事件都会填入全局递增的 `Seq`、`Time` 和当前线程的 `CriticalDepth`。
- Release 事件会从对应的 Get 回填 `ArrayLength`、`ElementSize` 和基址，因此订阅者总能看到完整上下文。

线程有嵌套临界区时，建议读写事件用 `ReadArray`/`WriteArray` 指定数组，或直接设置 `Event.Array`，以便把越界访问归因到正确的数组。

## 检测规则

| Finding | 含义 |
| --- | --- |
| `jni_call_in_critical` | 临界区内调用了 `Release*Critical`、嵌套 `Get*Critical` 之外的 JNI 函数 |
| `blocking_call_in_critical` | 临界区内发生阻塞调用 |
| `out_of_bounds_write` / `out_of_bounds_read` | 访问区间没有完全落在数组的字节范围内 |
| `use_after_release` | Release 后再次读写旧指针 |
| `duplicate_release` | 同一线程对同一数组连续 Release，期间没有新的 Get |
| `unreleased_at_thread_exit` | `ThreadExit` 时仍有未释放的临界区，每个数组一条告警 |
| `aborted_writes_discarded` | `JNI_ABORT` 丢弃了自上次提交以来的写入，严重级别为 warning |
| `release_without_get` | Release 找不到匹配的 Get |

## Release 模式

- `ReleaseCopyBack`（0）：回写并结束临界区。
- `ReleaseCommit`（`JNI_COMMIT`）：回写但保持 pinned；后续必须再次 Release。期间仍按临界区约束检测。
- `ReleaseAbort`（`JNI_ABORT`）：不回写并结束临界区；若有未提交写入，产生 `aborted_writes_discarded` 提示，统计写入次数和字节数。

`ReleasePointer` 可额外提供被释放指针，用于区分同一数组的嵌套 Get。

## 验证

```sh
go test ./...
```

测试覆盖：临界区内 `NewStringUTF`、越界一字节写入、释放后读写、重复释放、合规读写、`JNI_ABORT` 丢弃提示、阻塞调用、线程退出泄漏、事件订阅和 `JNI_COMMIT` 保持 pinned。
