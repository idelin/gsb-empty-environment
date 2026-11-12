# Android导出组件保护判定

应用商店审核时要找出导出却未受保护的 Android 组件，攻击者的应用把含 intent-filter 的 Activity、Service 与 Provider 暴露给任意应用。为Go审核组件实现可导入的清单判定包：targetSdk 低于 31 时无 android:exported 且含 intent-filter 视为导出，31 及以上缺失该属性则报错；Provider 在 targetSdk 17 以上默认不导出；组件的 android:permission 覆盖应用级设置；引用的权限未在清单定义、protectionLevel 缺失或为 normal 或 dangerous 时视为不受保护，signature 系视为受保护；activity-alias 随 targetActivity 判定。用默认值边界、权限覆盖、未定义权限和别名验证。

## 判定规则

- 显式 `android:exported` 始终优先；缺失时走平台默认值。
- `targetSdk < 31`：含 `intent-filter` 且未声明 `android:exported` 的 Activity/Service 视为导出。
- `targetSdk >= 31`：含 `intent-filter` 却缺失 `android:exported` 返回 `ExportedAttributeError`，对应 Android 12 的安装期硬性要求。
- Provider：`targetSdk <= 16` 默认导出，`targetSdk >= 17` 默认不导出。
- `activity-alias` 的导出状态跟随 `targetActivity`；别名自身的显式 `android:exported` 仍优先，引用不到目标 Activity 时返回 `MissingTargetActivityError`。
- 权限取值：组件级 `android:permission` 覆盖应用级 `android:permission`，组件级为空则回退应用级。
- 保护强度：权限未在清单 `<permission>` 中定义、`protectionLevel` 缺失、或为 `normal`/`dangerous` 均判为不受保护；仅 `signature` 系（含 `signatureOrSystem` 及 `signature|...` 组合）判为受保护。

## 用法

```go
import netsec "example.org/netsec/q0215"

f, _ := os.Open("AndroidManifest.xml")
manifest, err := netsec.ParseManifest(f)
if err != nil {
    log.Fatal(err)
}

report, err := netsec.Analyze(manifest)
// err 可包含 ExportedAttributeError / MissingTargetActivityError，
// 其余可判定组件的结果仍在 report.Findings 中。
for _, finding := range report.Findings {
    log.Printf("exported but unprotected: %s %s (%s)",
        finding.Kind, finding.Name, finding.Reason)
}
```

也可以直接构造 `netsec.Manifest`，或调用细粒度的 `ResolveExported`、
`EffectivePermission`、`EvaluateProtection` 进行单点判定。

## 测试

```sh
go test ./... -race -cover
```

测试覆盖默认值边界（sdk 16/17、30/31）、组件级对应用级权限覆盖、
未定义/缺失/弱保护级别权限，以及 activity-alias 跟随 targetActivity 的各类场景，
并包含文本清单 XML 的端到端解析用例。
