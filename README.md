# 影子字节编码下的访问合法性判定

堆块释放后仍被读写、或越过块尾一个字节，都需要在运行时被发现。为Go内存错误检测库写可导入的影子内存包，供插桩回调调用：每 8 字节一个影子字节，0 为全部可访问，1 到 7 为前 k 字节可访问，其余负值表示左红区、右红区或已释放；对任意起点和 1、2、4、8、16 字节的访问给出判定与错误类型；释放块进入隔离区，总字节超过上限才从最旧开始回收。用块尾部分字节、跨段访问、释放后使用和隔离区回收验证。

## 用法

实现在 `shadow/` 子包（导入路径 `example.org/netsec/q0794/shadow`）：

```go
m := shadow.New(1 << 20) // 隔离区上限 1 MiB
m.Malloc(addr, size)     // 分配挂钩：登记块并布置左右红区
if err := m.Check(addr, size, isWrite); err != nil {
	// err.Kind: KindLeftRedzone / KindRightRedzone / KindUseAfterFree
}
m.Free(addr)             // 释放挂钩：毒化并送入隔离区，超限才回收最旧块
```

运行测试：`go test ./...`
