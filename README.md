# singleflight（支持独立取消的同键并发去重）

同一 key 的并发 `Do` 调用共享一次 `fn` 计算；每个等待者可用自己的
`context` 独立取消；所有等待者取消时计算被终止；`fn` 的 panic 会转成
`*PanicError` 并释放所有等待者；完成（含失败）后状态先移除再唤醒，
新调用不会复用已关闭的失败状态。仅使用 Go 标准库同步原语
（`sync.Mutex`、channel、`context`）。

## 运行测试（含竞态检测）

```sh
go test -race -v ./...
```

## 运行样例

```sh
go run ./cmd/example
```
