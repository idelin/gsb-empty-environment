# 交接哈希纳入转录

会话转录不算交接数据，设备签的是不完整的会话。转录必须包含交接字节的哈希、阅读器公钥和设备公钥；少一项就失败。三项齐全的转录接受。可导入的Go库供钱包调用。从转录中去掉交接哈希。

## 实现说明

本库是可导入的 Go 包（`example.org/netsec/q0187`，包名 `netsec`），供钱包在向阅读器出示凭证时构造并校验设备签名所覆盖的会话转录。

### 关于需求中的矛盾

规格正文要求转录**必须包含三项**（交接字节哈希、阅读器公钥、设备公钥），且“少一项就失败，三项齐全才接受”；而最后一句“从转录中去掉交接哈希”会让必备项之一缺失，从而导致**每一份转录都必然失败**，与正文规则及标题“交接哈希纳入转录”直接冲突，无法同时满足。

交接哈希是会话转录绑定交接上下文、抵御中继/重放与转录拼接攻击的关键组成（与 ISO/IEC 18013-5 会话转录的安全意图一致）。去掉它会让设备在不完整会话上签名，正是本规格要防止的情形。

因此本实现遵循安全且自洽的规则：**保留交接哈希，三项齐全才接受，缺任一即失败；不实现“去掉交接哈希”这一矛盾指令。**

### 转录内容（三项，缺一不可）

- `HandoffHash`：对交接字节取 SHA-256。原始交接字节属于交接数据，不进入转录，转录只持有其摘要。
- `ReaderPublicKey`：阅读器会话公钥（原始密钥字节）。
- `DevicePublicKey`：设备会话公钥（原始密钥字节）。

### 主要 API

- `NewSessionTranscript(handoff, readerKey, deviceKey)`：由原始交接字节构造，内部计算 SHA-256；任一入参为空即返回错误，无法构造不完整转录。
- `NewSessionTranscriptFromHash(hash, readerKey, deviceKey)`：在已持有交接摘要时使用，并校验摘要长度为 32 字节。
- `(*SessionTranscript).Validate()` / `Complete()`：三项齐全且哈希为合法 SHA-256 才通过。
- `(*SessionTranscript).SigningInput()`：返回设备应签名的确定性规范字节；转录不完整时拒绝返回，杜绝在不完整会话上签名。
- `(*SessionTranscript).Encode()` 与 `Decode()`：带魔数与版本的长度前缀规范编码，支持确定性往返，并拒绝截断、尾随数据与不完整编码。

### 用法示例

```go
import "example.org/netsec/q0187"

tr, err := netsec.NewSessionTranscript(handoffBytes, readerPublicKey, devicePublicKey)
if err != nil {
    // 缺任一必备项，按失败处理，不要签名
    return err
}
if !tr.Complete() {
    return errors.New("incomplete session transcript")
}
toSign, err := tr.SigningInput() // 设备对该字节签名
```

### 测试

`go test ./...` 覆盖：三项齐全接受、缺任一即失败、构造器拒绝空入参、哈希长度校验、编码往返与确定性、畸形编码拒绝、以及三项均被绑定进签名输入（改动交接会改变待签名字节）。
