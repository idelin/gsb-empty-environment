# 克隆恢复后的随机源重播种

`example.org/netsec/q0823` 提供一个可嵌入令牌签发路径的 fork-aware 随机源。内部使用 AES-256 计数器模式确定性生成器，种子来自调用方注入的熵源。

## 行为

- 每次取数前读取调用方提供的 `GenerationID`（进程号 + 虚拟机世代 ID）和单调时钟。
- 世代二元组变化、时钟相对上一次观测值回退，或上次熵源调用失败时，自动重播种。
- 重播种材料按“旧状态 ‖ 新熵 ‖ 世代标识 ‖ 时钟读数”顺序混合，派生新的 AES-256 密钥并把块计数器归零。
- 重播种前丢弃尚未消费的缓冲字节；重播种期间持有互斥锁，所有并发取数阻塞到完成。
- 熵源失败时 `Read`/`Bytes` 返回包装了 `ErrEntropy` 的错误且不写出任何字节；后续调用会继续重试重播种，不会回退到旧状态输出。
- `Read` 实现 `io.Reader`，`Bytes(n)` 是不返回部分结果的便捷接口。

## 接入

```go
start := time.Now()

entropy := netsec.EntropyFunc(func(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	return b, nil
})

generation := func() netsec.GenerationID {
	return netsec.GenerationID{
		ProcessID:      os.Getpid(),
		VMGenerationID: currentVMGenerationID(),
	}
}

clock := netsec.ClockFunc(func() int64 {
	return int64(time.Since(start))
})

src, err := netsec.New(netsec.Config{
	Entropy:    entropy,
	Generation: generation,
	Clock:      clock,
})
if err != nil {
	return err
}

token, err := src.Bytes(32)
```

`currentVMGenerationID` 必须来自虚拟机/宿主机提供的追踪信息，并在快照恢复或 fork 后发生变化。`Generation` 必须在每次调用时读取当前进程的 PID，不能缓存初始化时的值。时钟只要求单调，不要求与墙上时钟一致。

## 安全约束

- 生成器是确定性的。不要把 `Source` 状态写入磁盘、跨进程复用，或在没有世代变化的情况下复制。
- 熵源必须提供新鲜的密码学随机字节；回放固定字节仅用于测试。
- 进程号与 VM 世代 ID 相同的两个副本无法被本包区分，调用方必须保证该二元组在克隆间唯一。

## 测试

```sh
go test -race ./...
```

测试覆盖两个克隆共享同一熵回放但世代不同的输出不相交、PID/VM 世代变化、时钟回退、熵源失败零输出、失败后的缓冲丢弃、重播种期间并发阻塞，以及并发取数不重复。
