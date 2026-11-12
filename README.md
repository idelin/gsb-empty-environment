# TFTP传输标识与路径限定

攻击者用带..或绝对路径的文件名、伪造来源端口的数据包，或超大blksize，攻击限定在一个目录的TFTP服务。给Go服务端写供UDP收包调用的传输会话包：文件名规范化后须留在根目录且拒绝NUL；每次传输绑定客户端地址与端口（TID），其他来源的包回ERROR 5但不终止传输；blksize不超过调用方上限；写请求默认拒绝覆盖。用路径逃逸、第三方插话和超大blksize验证。

## 用法

服务端只负责收包，包处理由调用方完成：

```go
srv, err := netsec.NewServer(netsec.Config{
	Root:         "/srv/tftp",
	MaxBlockSize: 1024,
})
if err != nil {
	log.Fatal(err)
}
defer srv.Close()

// 首个 RRQ/WRQ 包：创建会话并拿到响应。
sess, reply, err := srv.Accept(packet, clientAddr)
if reply != nil {
	// 发送 reply 到 clientAddr
}

// 后续包：必须来自同一客户端 IP 与端口。
reply = sess.HandlePacket(packet, from)
```

## 安全约束

- `NormalizeFilename`/`ResolvePath` 拒绝 NUL、绝对路径、盘符路径和规范化后逃出根目录的路径；实际文件操作使用 Go 1.24 的 `os.Root`，符号链接也不能逃出根目录。
- `Session` 在创建时绑定客户端 `IP:port`（TID）。来自其他来源的包只返回 ERROR 5，不改变会话状态，合法客户端仍可继续传输。
- `blksize` 协商结果不会超过 `Config.MaxBlockSize`，并受 `HardMaxBlockSize`（65464）硬上限约束；请求值过大时下调到上限。
- WRQ 默认使用 `O_CREATE|O_EXCL`，目标已存在则返回 ERROR 6。只有显式设置 `AllowOverwrite` 才允许覆盖。

## 验证

```sh
go test -race ./...
```

测试覆盖：`../`/绝对路径/NUL 文件名拒绝、符号链接逃逸、读与写会话的第三方 TID 插话（返回 ERROR 5 且传输继续）、超大/溢出/非法 `blksize`、默认拒绝覆盖及显式覆盖。
