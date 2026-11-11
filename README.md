# symbolcache

崩溃服务（crash service）使用的 Go 符号缓存包。PDB 从多个上游镜像（symbol
server image）获取，落盘后以 `io.ReaderAt` 形式对外提供只读内容。

## 设计要点

- **先校验后发布**：下载结果先写入 `tmp/` 下的私有临时文件，完整解析 MSF 7.0
  容器——校验超级块、块映射、流目录（计数、各流大小、块列表，以及块不得越界或
  相互重叠）——再读取 PDB info 流（stream 1）核对 GUID 与 age。只有流目录结构
  合法且 GUID、age 同时匹配请求时，才用同目录 `rename` 原子发布；任何失败都不会
  在对象目录留下文件。
- **镜像回退**：按配置顺序尝试各 `Mirror`，一个镜像返回截断数据或伪造内容时丢弃
  临时文件并继续尝试下一个；全部失败时返回聚合错误。
- **同键合并下载、独立取消**：相同索引路径的并发 `Get` 只触发一次下载并共享结果。
  每个调用方有独立的 `context`：单个调用方取消只影响自己；当所有等待者都离开时，
  协调者才取消底层请求并停止写入。
- **崩溃恢复**：`New` 启动时清扫 `tmp/` 中残留的部分文件，中断/崩溃的写入永远不
  会被当成已发布对象。
- **独立文件描述符**：每个 `Handle` 持有自己的 `*os.File`，调用方负责 `Close`，
  可直接当作 `io.ReaderAt` 按偏移读取。

## 磁盘布局

采用符号服务器规范的索引路径，键由 PDB 文件名与原始 GUID、age 构成：

```
<root>/<NAME>/<GUIDHEX><AGEHEX>/<NAME>
<root>/tmp/fetch-*.part
```

例如 `app.pdb`、GUID `0102...10`、age `7` 对应
`app.pdb/0102030405060708090A0B0C0D0E0F107/app.pdb`。

## 用法

```go
cache, err := symbolcache.New("/var/lib/symbols",
    &symbolcache.HTTPMirror{BaseURL: "https://mirror-a.example.com"},
    &symbolcache.HTTPMirror{BaseURL: "https://mirror-b.example.com"},
)
if err != nil {
    log.Fatal(err)
}

h, err := cache.Get(ctx, symbolcache.Key{
    FileName: "app.pdb",
    GUID:     guid, // [16]byte，磁盘字节序
    Age:      age,  // uint32
})
if err != nil {
    log.Fatal(err)
}
defer h.Close()

buf := make([]byte, n)
if _, err := h.ReadAt(buf, offset); err != nil { ... }
```

## 测试

`go test -race ./...` 覆盖：缓存命中与 `ReaderAt`、GUID/age 不匹配拒绝、伪造魔数/
流目录/截断镜像不污染缓存、有损镜像回退与恢复、中断写入后的清扫恢复、同键单次共享
下载、独立取消与最后等待者取消停止下载、HTTP 镜像（经注入 `RoundTripper`）以及键
路径安全性。
