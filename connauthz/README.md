# connauthz — 长连接按代次复验的连接授权包

面向“机房切换信任根”场景：TLS 长连接在握手时合法，之后其证书被撤销，
服务仍可能允许它继续开新流。本包把授权从“握手一次”改为“每开一条新流复验一次”。

## 模型

- `Policy`：不可变的一代策略，包含信任根、额外已知中间证书、撤销集，带严格递增的
  `Generation`。撤销以 `(签发者 DN, 序列号)` 定位单张证书，交叉签名的同名证书互不牵连。
- `Authorizer`：内部用 `atomic.Pointer[Policy]` 持有当前代；`Rotate` 做 CAS 换入，
  拒绝代次回退。
- `Connection`：TLS 握手时用 `ConnectionFromTLS(state)` 记录对端 DER 证书链（防御性拷贝）。
- `AuthorizedMux`：包装底层多路复用器，`OpenStream` 前先 `Authorize`，通过才真正开流。

## 接入示例

```go
authz, _ := connauthz.NewAuthorizer(initialPolicy)

for rawConn := range accepted {
    tlsConn := rawConn.(*tls.Conn)
    state := tlsConn.ConnectionState()
    recorded := connauthz.ConnectionFromTLS(state) // 握手时只记录链
    mux := yourmux.Server(tlsConn)
    // 每条新流都按最新代策略复验
    gated := connauthz.NewAuthorizedMux(mux, authz, recorded)
    go serve(gated)
}

// 信任根轮换 / 证书撤销：构建新一代并原子换入
newPolicy, _ := connauthz.NewPolicyBuilder(nextGen).
    AddRoot(newRoot).
    AddIntermediate(crossCert).
    Revoke(compromisedCert).
    Build()
if err := authz.Rotate(newPolicy); err != nil { /* 代次回退等 */ }
```

## 授权生效顺序（轮换与开流并发）

1. 单次 `Authorize` 只读取一次策略指针，全程使用同一代，判定结果与该代自洽。
2. 与 `Rotate` 并发的开流：换入完成前的走旧代、之后的走新代，不存在半新半旧的判定。
3. `Rotate` 成功返回即建立 happens-before，之后的调用必观察到新代；CAS 保证代次单调不减，
   已观察到第 N 代的调用方不会再看到第 N-1 代。
4. 授权只发生在开流时刻。已经打开的流不被追溯关闭，旧流按既有决定运行到自身结束。

## 交叉签名路径隔离

测试 PKI 中，`leaf -> inter` 只有一张共享中间证书，上方有两条路径：

```
leaf -> inter -> rootA
leaf -> inter -> crossAbyB(主体=rootA, 由 rootB 签发) -> rootB
```

复验时 `x509.Verify` 构建出两条可信链；只要存在一条全程未撤销的路径即放行：

- 撤销 `crossAbyB`：只断 rootB 路径，rootA 路径照常放行；
- 撤销 `rootA` 自签名证书：交叉证书是另一份证书，rootB 路径不受影响；
- 撤销共享 `inter` 或 `leaf`：两条路径同时失效，正确拒绝。

## 验证

```
go vet ./...
go test ./... -race -count=1
```

测试覆盖：交叉签名双路径与撤销隔离、真实 TLS 长连接的逐流复验、
轮换/开流并发下的代次单调与判定自洽、旧流不被追溯关闭、过期与畸形链拒绝。
