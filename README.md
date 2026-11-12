# ECH内外ClientHello重建

支持ECH的前端服务器收到外层ClientHello后解密内层，攻击者可在外层扩展引用里制造环、乱序或引用ECH自身，让路由和握手看到不同的ClientHello。给Go前端写供TLS握手前置处理调用的ClientHelloInner重建包：按draft-ietf-tls-esni展开ech_outer_extensions，被引用扩展须按外层顺序出现且不得引用ech或自身，内层必须带类型为inner的ech扩展，展开后不得有重复扩展类型；后端只按内层SNI选择，解密失败时只用公共名并返回retry配置。用循环引用、乱序引用、内外SNI不同和解密失败验证。

## 用法

前端在TLS握手解析出ClientHelloOuter后调用`ProcessClientHello`。解密函数由调用方注入，通常封装HPKE `Open`；返回的明文是`EncodedClientHelloInner`（可带尾部零填充）。

```go
frontend := &netsec.ECHFrontend{
    Decrypt: func(outer *netsec.ClientHello) ([]byte, error) {
        return openECH(outer)
    },
    RetryConfigs: currentECHConfigList,
}

result, err := frontend.ProcessClientHello(clientHelloBytes)
if err != nil {
    return err
}

backendName := result.ServerName
forwardedClientHello := result.Wire
```

成功时`result.ServerName`和`result.Wire`都来自同一份重建后的内层ClientHello，后端不得再读取外层SNI。`Wire`可直接交给后端TLS握手。解密失败时`ECHRejected`为真、`RetryConfigs`非空，`ServerName`仅取配置的`PublicName`（未配置时取外层公共SNI），并把外层ClientHello原样转发。若客户端未发送ECH扩展，也会返回已配置的retry配置。若解密成功但内层格式非法，返回`illegal_parameter`，不会回退到外层SNI。

也可直接调用`ReconstructClientHelloInner`完成解析、展开和校验。重建会执行以下检查：

- `ech_outer_extensions`只允许出现在加密内层，且列表长度为`2..254`字节。
- 引用的扩展必须存在且在外层唯一，引用顺序与外层相对顺序一致。
- 不允许重复引用、引用`encrypted_client_hello`、引用`ech_outer_extensions`自身或引用外层`server_name`。
- 内层必须恰好有一个`encrypted_client_hello`扩展，类型为`inner`。
- 展开并替换到原`ech_outer_extensions`位置后，最终扩展类型不得重复。
- `legacy_session_id`从外层复制，尾部填充必须全为零。
