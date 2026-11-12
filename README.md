# EAPOL-Key重放与密钥重装

重传的四次握手第三条消息若令接收方重装密钥，攻击者即可让包号复位并重放帧。为Go无线客户端实现EAPOL-Key状态机，供接收路径调用：重放计数器须在MIC验证通过后才更新且严格递增，Key Data也在MIC通过后解密；同一ANonce对应的PTK只安装一次，重复的消息3只回复确认。用旧计数器、MIC错误、重复消息3和ANonce改变验证。

## 接收路径用法

```go
install := func(anonce []byte, ptk *netsec.PTK) error {
    // 安装 KCK/KEK/TK，并在安装成功后启动计数器。
    return nil
}

sm := netsec.NewEAPOLKeyStateMachine(netsec.Config{
    PMK:               pmk,
    SNonce:            snonce,
    SupplicantAddr:    spa,
    AuthenticatorAddr: aa,
    InstallPTK:        install,
    KeyDataHandler:    func(plain []byte) error {
        // 仅在 MIC 通过后调用；plain 是已经解包的 Key Data。
        return nil
    },
})

// frame 是收到的完整 EAPOL packet。返回值是可直接发送的 Message 4。
response, err := sm.HandleEAPOLKey(frame)
```

`ProcessMessage3` 还会返回结构化结果（`Duplicate`、`Installed`、`PTK`、
已解密的 `KeyData` 和提交后的 `ReplayCounter`）。

## 安全语义

- MIC 验证失败时，不更新 replay counter、不安装 PTK、不解密 Key Data。
- replay counter 只有在新值更大且 MIC 通过后才提交；相同值仅允许作为
  已安装 ANonce 的合法重传。
- 每个 ANonce 最多安装一次 PTK。重复 Message 3 只生成 Message 4 确认。
- ANonce 改变且 replay counter 递增时，重新派生并安装新的 PTK。
- `AESKeyWrap`/`AESKeyUnwrap`、`AES128CMAC`、`PRF384`、`DerivePTK` 和
  `ComputeEAPOLKeyMIC` 均作为独立原语导出，便于接收路径和测试复用。

## 验证

`go test ./...` 覆盖旧 replay counter、错误 MIC、重复 Message 3、ANonce
改变，以及 RFC 3394 AES key wrap 和 RFC 4493 AES-CMAC 标准向量。
