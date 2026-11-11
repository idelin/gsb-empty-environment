# tailfollow

日志采集代理在文件轮转时会丢行或重复。给Go采集代理实现文件跟随读取器：按设备号和inode识别文件，处理重命名轮转和copytruncate，旧文件读完再切换；未以换行结尾的末行等待补全，超时后发出；检查点记录inode和偏移并原子保存，重启后从正确位置继续。用写入与轮转交错的测试核对每行恰好交付一次。

## 用法

```go
fw := tailfollow.New(tailfollow.Config{
    Path:           "/var/log/app.log",
    CheckpointPath: "/var/lib/agent/app.ckpt",
    PollInterval:   50 * time.Millisecond, // 轮询间隔
    LineTimeout:    time.Second,           // 末行补全等待超时
})
go func() { fw.Run(ctx) }()
for line := range fw.Lines() {
    // 每行恰好交付一次
}
```

## 行为说明

- 文件按 `(设备号, inode)` 识别：重命名不影响已打开的文件，路径被替换时先排干旧文件再切换；路径暂时消失期间继续读旧描述符，不丢轮转后仍写入旧文件的行。
- copytruncate 通过"同一 inode 且文件收缩到已读偏移之下"检测，随后从 0 重新读取；重写后长度恰好等于原偏移的情形无法与"无写入"区分，属于固有歧义。
- 未以 `\n` 结尾的末行会等待补全，超过 `LineTimeout` 后按现有内容发出。
- 检查点记录 inode 与偏移（不含未完成的末行字节），通过临时文件 + rename 原子保存；重启后若路径仍是同一 inode 则从偏移处继续，否则从头读取新文件。

## 测试

```sh
go test -race ./...
```
