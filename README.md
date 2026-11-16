# 多租户压缩字典泄漏

Go消息服务拟共享压缩器，但攻击者能提交自己的消息并观察压缩后长度。实现嵌入服务的分区字典压缩包，采用本题定义的LZ匹配：窗口4096字节，最长匹配至少3字节，长度最多255，最长优先且同长选最小距离，文字编码为0及原字节，匹配编码为1、两字节大端距离及一字节长度。字典按租户ID和公开或机密标签隔离，跨标签和跨租户不得复用；流取消、出错、重置或租户标识复用后不得泄漏旧字典。支持并发流及解码，禁止依赖全局串行锁完成整段压缩。用对照实验验证更换其他租户秘密不会影响攻击者压缩输出，测试重叠匹配、窗口滑动、取消和同租户合法复用；明确流提交时字典何时可见。

## API

纯编解码接口只处理字节和显式字典快照：

```go
encoded := netsec.Compress(dictionary, message)
decoded, err := netsec.Decompress(dictionary, encoded)
```

服务接口按 `Scope{TenantID, Class}`（租户 ID + `Public`/`Confidential`）隔离字典：

```go
svc := netsec.NewCompressor()
result, err := svc.CompressResult(tenantID, netsec.Public, message)
decoded, err := result.Decode() // 使用 result 携带的精确字典快照
```

`Compress`/`CompressContext` 返回裸压缩字节，适合只需要长度或自行维护修订号的传输层。延迟解码或并发解码应使用 `CompressResult` + `Result.Decode`/`DecompressResult`，否则后续字典提交可能改变分区当前字典。

## 字典生命周期

- `NewStream` 在返回时短暂锁住分区并复制当前已提交字典，之后压缩全程不持有服务锁或分区锁。
- `Write` 只向流私有缓冲追加字节。`Submit` 针对流启动时取得的快照一次性编码，因此 token 选择不依赖 `Write` 的分块边界。
- `Submit` 在最终发布点短暂锁住分区，原子发布本流最后 4096 个输入字节并递增修订号；编码本身不持有服务锁或分区锁。只有这次提交返回成功后，后续 `NewStream` 才能看到该内容。
- `Cancel`、`Reset` 或任何写入/上下文错误会清除流的输入、输出和私有字典，绝不会发布。
- `ResetScope` 退役当前分区并清空字典；旧流之后调用 `Submit` 得到 `ErrStaleDictionary`。
- 租户 ID 被业务层释放或复用时，调用 `ReleaseTenant`。同名租户的新流会获得全新分区，旧租户流不能向新分区提交。

匹配器允许匹配与当前输出重叠，并严格使用 4096 字节回看窗口、3 字节最短匹配、255 字节最长匹配、最长优先且同长取最小距离。解码器会拒绝零距离、超出窗口、过短长度、未知 tag 和截断 token。

测试入口：

```sh
go test ./...
go test -race ./...
```
