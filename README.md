# NSEC3迭代上限与否定证明

攻击者的区发布迭代 65535 的 NSEC3，耗尽验证解析器的 CPU。为Go解析宿主写可导入的 NSEC3 否定应答验证包：迭代数大于100的应答按不安全处理且不做哈希，盐长超过8字节拒绝；其余按 SHA-1 迭代哈希，核对最近包围者、覆盖下一更近名与通配符的证明；覆盖下一更近名的记录带 opt-out 时结论只能是不安全而非不存在；全应答哈希次数设上限。测超限迭代、区间回绕和 opt-out。

## 包 `nsec3`

可导入路径：`example.org/netsec/q0330/nsec3`。

```go
v := nsec3.NewVerifier() // 每个应答一个 Verifier，哈希预算按应答计
conclusion, err := v.Verify("q.example.", records)
```

- 迭代数 > 100：返回 `Insecure`，不执行任何哈希（`Verifier.HashCount` 保持 0）。
- 盐长 > 8 字节：拒绝，返回 `ErrSaltTooLong`。
- 其余按 RFC 5155 做 SHA-1 迭代哈希，依次核对最近包围者、覆盖下一更近名与通配符，结论为 `NXDOMAIN` / `NoData` / `Wildcard` / `Insecure`。
- 覆盖下一更近名的记录带 opt-out 标志时，结论只会是 `Insecure`，绝不判定不存在。
- 每个应答的哈希总次数由 `Verifier.MaxHashes` 封顶（默认 8），超限按 `Insecure` 处理。

测试：`go test ./nsec3/`，覆盖超限迭代、区间回绕与 opt-out。
