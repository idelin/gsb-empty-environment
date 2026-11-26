# PKCS#11 Key Policy State Machine and Attack Search

这是一个可导入的 Go 包，用于建模 HSM/KMS 控制面的 PKCS#11 密钥属性策略，并在合法的 API 子集上搜索密钥明文窃取路径。

模块路径：

```text
formal.local/w27-d9fc99f2e0
```

包名：`hsm`

## 能力

- 完整属性向量：`CKA_SENSITIVE`、`CKA_EXTRACTABLE`、`CKA_ENCRYPT`、`CKA_DECRYPT`、`CKA_WRAP`、`CKA_UNWRAP`、`CKA_DERIVE`、`CKA_TRUSTED`、`CKA_WRAP_WITH_TRUSTED`、`CKA_ALWAYS_SENSITIVE`、`CKA_NEVER_EXTRACTABLE`。
- 对称 AES 密钥与非对称密钥对对象；非对称私钥材料可作为不透明字节被对称机制包装。
- 真实机制：RFC 3394 AES-KW、AES-CBC + PKCS#7、AES-GCM；派生使用 HKDF-SHA256。
- 在线裁决：`WrapKey`、`UnwrapKey`、`Encrypt`、`Decrypt`、`DeriveKey`、`SetAttributeValue`、`CopyObject`。
- 调用方权限：API 位图、按对象限制、SO 级 `CanSetTrusted`。
- 包装来源记录：解包时校验机制、来源属性、历史标志和模板单调性。
- 有界 BFS 攻击搜索：最多 6 步，返回最短路径；状态指纹去重，支持全枚举和加固对比。

## 快速开始

```go
package main

import (
	"bytes"
	"fmt"
	"log"

	hsm "formal.local/w27-d9fc99f2e0"
)

func main() {
	target := hsm.KeySpec{
		ID:       "master",
		Class:    hsm.ClassSecretKey,
		Type:     hsm.TypeAES,
		Material: bytes.Repeat([]byte{0x11}, 32),
		Attributes: hsm.Attributes{
			Sensitive:       true,
			Extractable:     true,
			AlwaysSensitive: true,
		},
	}
	wrapper := hsm.KeySpec{
		ID:       "wrapper",
		Class:    hsm.ClassSecretKey,
		Type:     hsm.TypeAES,
		Material: bytes.Repeat([]byte{0x22}, 32),
		Attributes: hsm.Attributes{
			Sensitive: true,
			Wrap:      true,
			Unwrap:    true,
		},
	}

	h, err := hsm.NewHardened(target, wrapper)
	if err != nil {
		log.Fatal(err)
	}
	blob, err := h.WrapKey("wrapper", "master", hsm.AESKeyWrap)
	if err != nil {
		log.Fatal(err)
	}
	id, err := h.UnwrapKey("wrapper", blob, hsm.KeyTemplate{
		Label:      "master-clone",
		Class:      hsm.ClassSecretKey,
		Type:       hsm.TypeAES,
		Attributes: target.Attributes,
	}, hsm.AESKeyWrap)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(id)
}
```

`KeyTemplate.Attributes` 是完整请求向量，不是稀疏 patch。`AttributePatch` 才表示仅修改部分属性。

## 默认安全语义

`Policy{}` / `HardenedPolicy()` 是严格策略：

- `CKA_SENSITIVE=true -> false` 被拒绝。
- `CKA_EXTRACTABLE=false -> true` 被拒绝。
- `CKA_ALWAYS_SENSITIVE`、`CKA_NEVER_EXTRACTABLE` 为只读历史标志。
- `CKA_WRAP_WITH_TRUSTED=true` 一旦设置不可清除。
- 不能通过模板或属性变更创建同一对象上的 `CKA_WRAP + CKA_DECRYPT`、`CKA_UNWRAP + CKA_ENCRYPT` 组合；已有遗留组合上的相关操作也会被拒绝。
- `SetAttributeValue`、`CopyObject` 和受追踪来源的 `UnwrapKey` 不允许改变 usage role 向量，防止通过复制/模板拆分同一密钥值的包装与解密能力。
- `CKA_WRAP_WITH_TRUSTED=true` 的目标只能由 `CKA_TRUSTED=true` 的密钥包装。
- `CKA_TRUSTED` 不能通过应用 API 提升。
- 相同密钥材料不能被独立导入为不同策略向量；该检查阻止 role-splitting 配置。
- 派生出的新密钥必须保持 `CKA_SENSITIVE=true`、`CKA_EXTRACTABLE=false`。
- 派生使用公开参数的确定性 HKDF；若基础密钥已泄露，派生密钥也会被搜索器视为已知。

`InsecurePolicy()` 打开所有兼容性缺陷，仅用于复现攻击和加固回归测试，不应部署。

| 开关 | 脆弱行为 |
| --- | --- |
| `AllowWrapOfNonExtractable` | 允许包装 `CKA_EXTRACTABLE=false` 的对象 |
| `AllowRoleOverlap` | 允许 `WRAP+DECRYPT` / `UNWRAP+ENCRYPT` |
| `AllowRoleMutation` | 允许 API 修改 usage roles |
| `AllowUntrustedWrapWithTrusted` | 绕过 trusted wrapper 检查 |
| `AllowUnwrapTemplateDowngrade` | 允许解包模板削弱敏感/可导出属性 |
| `AllowUntrustedPermissiveUnwrap` | 允许非信任解包密钥生成宽松模板 |
| `AllowMechanismConfusion` | 解包/解密不绑定原包装机制 |
| `AllowTrustedPromotion` | 允许 `SetAttributeValue`/`CopyObject` 提升 `CKA_TRUSTED` |
| `AllowDeriveMaterialCopy` | 派生 API 错误地复制基础密钥材料 |
| `AllowPermissiveDerivation` | 允许派生 `CKA_SENSITIVE=false` / `CKA_EXTRACTABLE=true` 的助手密钥 |
| `AllowDuplicateKeyMaterial` | 允许相同材料以不同策略导入 |

## 调用方权限

```go
perms := hsm.PermissionsFor(hsm.APIWrapKey | hsm.APIDecrypt)
session := h.As(perms)

_, _ = session.WrapKey("wrapper", "master", hsm.AESKeyWrap)
```

`Permissions.KeyIDs` 非空时限制可寻址对象。`CanSetTrusted` 表示调用方拥有 SO 级信任设置能力；严格策略仍禁止通过普通属性 API 改变已有对象的信任状态。

## 攻击搜索

```go
cfg := hsm.AttackConfig{
	Keys: []hsm.KeySpec{
		{
			ID: "master", Class: hsm.ClassSecretKey, Type: hsm.TypeAES,
			Material: bytes.Repeat([]byte{0x11}, 32),
			Attributes: hsm.Attributes{
				Sensitive: true, Extractable: false,
				AlwaysSensitive: true, NeverExtractable: true,
			},
		},
		{
			ID: "legacy-wrapper", Class: hsm.ClassSecretKey, Type: hsm.TypeAES,
			Material: bytes.Repeat([]byte{0x22}, 32),
			Attributes: hsm.Attributes{
				Sensitive: true, Wrap: true, Decrypt: true,
			},
		},
	},
	Policy:      hsm.InsecurePolicy(),
	Permissions: hsm.PermissionsFor(hsm.APIWrapKey | hsm.APIDecrypt),
	Targets:     []hsm.ID{"master"},
	MaxDepth:    6, // 0 -> 6；大于 6 被拒绝
	MaxStates:   100000,
}

path, err := hsm.FindAttack(cfg)
if err != nil {
	log.Fatal(err)
}
if path != nil {
	fmt.Println(path.Explain())
	// 1. WrapKey(legacy-wrapper -> wraps master) using CKM_AES_KEY_WRAP
	// 2. Decrypt(legacy-wrapper) using CKM_AES_KEY_WRAP
	// leaked: master
}
```

搜索模型：

- `Targets` 为空时，所有 `CKA_SENSITIVE=true` 的对象都是目标。
- `KnownKeys` 指定攻击者初始已掌握的密钥；`CKA_SENSITIVE=false` 的对象自动视为已知。
- 包装密文带有来源、包装密钥、机制与源属性；已知包装密钥的密文可离线解开，因此会立即扩大攻击者知识。
- `UnwrapKey` 支持模板重写、权限提升和宽松输出属性。
- `Decrypt` 支持用同一密钥值的另一对象（复制体）解密包装密文。
- `SetAttributeValue` / `CopyObject` 支持角色扩张和角色拆分攻击建模。
- `DeriveKey` 支持错误材料复制和从可控基础密钥铸造助手密钥。
- `Encrypt` 由在线裁决器完整实现；搜索器不会生成纯 Encrypt 动作，因为对未知密钥材料执行 AEAD/分组加密不能增加目标明文知识。

BFS 每层代表一次 API 调用，因此第一个泄露结果就是最短路径。状态按密钥属性、材料指纹、来源、已知材料和密文来源去重。

## 加固验证

```go
report, err := hsm.VerifyHardening(cfg)
if err != nil {
	log.Fatal(err)
}

if report.Secure {
	fmt.Println("insecure policy leaks:", len(report.Insecure.Leaks))
	fmt.Println("hardened policy leaked:", !report.Hardened.NoLeak)
	fmt.Println("states explored:", report.Hardened.Explored)
}
```

`EnumerateAttacks` 对给定深度做全枚举；`NoLeak && Exhausted && !Truncated` 才表示该小规模状态空间内的零泄露证明。`MaxStates` 溢出会将 `Truncated` 置为 true，此时不能把 `NoLeak` 当作证明。

零泄露是相对于具体初始密钥集合和属性配置的结论。对于 `CKA_EXTRACTABLE=true` 且未设置 `CKA_WRAP_WITH_TRUSTED` 的目标，任何攻击者已知或可确定性派生的包装密钥都可能合法导出它；这类配置应继续保持可检测状态，而不是被强行隐藏。

## 机制与边界

- 支持 `CKM_AES_KEY_WRAP`、`CKM_AES_CBC_PAD`、`CKM_AES_GCM` 和 `CKM_HKDF_SHA256`。
- 非对称 RSA/EC 对象当前作为策略与包装目标建模；未实现 RSA-OAEP/PSS 或 ECDSA 运算。
- 搜索深度硬上限为 6；复杂密钥集合应先用 `MaxStates` 限制并检查 `Truncated`。
- 搜索是符号来源分析，在线 API 使用真实加密实现；两者共享同一裁决器与机制代码。
- 包不持久化对象或密钥材料，调用方负责安全存储导入配置。
