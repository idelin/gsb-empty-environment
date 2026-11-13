# Android网络安全配置的域继承与固定过期

应用对自家域强制证书固定，又给调试构建放宽，配置层层嵌套后容易让明文或用户证书悄悄放行。为Go构建期检查包写可导入的 network-security-config 求值：最具体的 domain-config 生效并继承父项信任锚与固定集合；cleartextTrafficPermitted 缺省在 targetSdk 28 及以上为 false；固定集合过期后不再强制；debug-overrides 仅调试构建生效；用户 CA 仅在 targetSdk 低于 24 缺省信任。用嵌套继承、过期和调试构建验证。

## 使用

```go
import (
	"fmt"
	"log"
	"strings"
	"time"

	"example.org/netsec/q0820"
)

cfg, err := netsec.ParseConfig(strings.NewReader(xml), netsec.Build{
	TargetSDK: 34,
	Debug:     false,
})
if err != nil {
	log.Fatal(err)
}

policy, err := cfg.PolicyFor("api.example.com")
if err != nil {
	log.Fatal(err)
}

fmt.Println(policy.CleartextTrafficPermitted)
fmt.Println(policy.TrustAnchors.System, policy.TrustAnchors.User)
fmt.Println(policy.PinsEnforcedAt(time.Now()))
```

`PolicyFor` 返回最具体匹配的 `domain-config`，并从外层配置继承未显式设置的
信任锚和固定集合。`pin-set` 的 `expiration` 按 UTC 零点解释；到达该时刻后
`PinsEnforcedAt` 返回 false，`EffectivePins` 返回 nil。`Policy.Pins` 仍保留
原始固定集合，便于诊断配置，但不应直接用它判断是否强制固定。

默认值：

- `targetSdk >= 28` 时 `cleartextTrafficPermitted` 为 false，低于 28 为 true。
- `targetSdk < 24` 时缺省信任 system 与 user CA，否则仅信任 system CA。
- `debug-overrides` 中额外的信任锚只对 `Build.Debug == true` 生效。
