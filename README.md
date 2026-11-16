# Android 网络安全配置求值

`netsec` 是可导入的 Go 包，用于在构建期计算 Android
`network-security-config` 对某个主机的有效策略。

```go
cfg, err := netsec.ParseFile("network_security_config.xml")
if err != nil {
	// 配置无法解析
}

policy, err := cfg.Evaluate("api.example.com", netsec.Options{
	TargetSDK: 35,
	Debug:     false,
	Now:       time.Now(),
})
if err != nil {
	// 主机名无效
}

fmt.Println(policy.CleartextTrafficPermitted)
fmt.Println(policy.TrustAnchors.System, policy.TrustAnchors.User)
fmt.Println(policy.Pinning, policy.Pins)
```

求值规则：

- 匹配同时考虑 `domain` 和 `includeSubdomains`，最长、最具体的
  `domain-config` 生效；完全匹配优先于同名的子域匹配。
- 未设置的值沿嵌套链继承。`domain-config` 从父
  `domain-config` 或 `base-config` 继承，`base-config` 最终落到平台默认值。
- `cleartextTrafficPermitted` 未设置时，targetSdk 28 及以上默认为
  `false`，27 及以下默认为 `true`。
- 未指定 `trust-anchors` 时，默认只信任 system CA；targetSdk 低于 24
  时同时信任 user CA。
- `pin-set` 会随配置继承。超过 `expiration` 后 `Pinning` 为 `false`，
  `Pins` 为空，但 `PinSet` 仍保留原始配置；过期子项不会回退到父项固定集合。
- `debug-overrides` 只在 `Options.Debug` 为 true 时合并进信任锚集合，
  release 构建完全忽略。
- 主机名匹配不区分大小写，并接受末尾点、端口和完整 URL。

主要入口是 `Parse` / `ParseConfig`、`Config.Evaluate`，以及便捷的
`Evaluate`、`EvaluateFile`。`Policy` 还提供 `AllowsCleartext`、
`PinsEnforced` 等只读辅助方法。
