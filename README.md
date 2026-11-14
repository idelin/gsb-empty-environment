# 证书固定只钉了主链没钉备用链

移动网关在根证书轮换后备用了另一条合法链，固定的摘要只覆盖主链，攻击者用备用链中的中间人证书完成握手。请实现供Go网关TLS客户端调用的可导入证书固定组件。固定集合按用途声明叶子公钥、签发者与SPKI摘要，主链和备用链都必须在集合内；证书链构建时逐张核对，任一证书不在集合即失败，不能回退到系统信任。轮换通过带版本与生效时间的固定策略发布，旧策略在宽限期并行有效，过期后失效。用备用链中间人、轮换前旧策略、过期策略、多固定项和合法备用链验证只有声明的链可用。

## 实现

组件位于 `pinning` 包（导入路径 `example.org/netsec/q0286/pinning`）：

- `PinSet` 按用途（`Purpose`）声明叶子公钥与签发者 CA 的 SPKI SHA-256 摘要，主链与备用链的每一张证书都必须声明在集合内。
- `Policy` 是带 `Version`、`NotBefore`、`NotAfter` 的固定策略；`Manager` 在当前时间并行评估所有生效策略，任一策略接受即通过，宽限期内新旧策略同时有效，过期策略自动失效。
- `Manager.Verify` 仅用策略自带的根池构建链（绝不回退系统信任），并对每条候选链逐张核对：叶子匹配 `LeafKeys`，全部签发者（中间 CA 与根）匹配 `IssuerKeys`，任一证书不在集合即拒绝。
- `Manager.TLSConfig` 返回禁用系统验证、改由固定管理器全权校验的 `*tls.Config`，供网关 TLS 客户端直接使用。

```go
m := pinning.NewManager(nil) // nil 使用系统时钟
m.AddPolicy(currentPolicy)   // 含 Roots 与按用途的 PinSet
cfg := m.TLSConfig("gateway-api", "gw.example.net")
conn, err := tls.Dial("tcp", addr, cfg)
```

运行测试：`go test ./pinning/`
