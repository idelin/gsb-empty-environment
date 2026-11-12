# FIDO2企业证明过滤

企业只想在受管设备注册时留存序列号级证明，攻击者可用自制认证器冒充企业证明，普通站点也不应拿到可关联标识。为TypeScript注册服务实现证明过滤组件，供注册完成回调调用：RP ID在管理员名单、请求与响应的企业证明格式一致、证书AAGUID等于认证器数据且在受管清单内才保存；其余降级为none并丢弃证书和序列号。用四类注册样例验证落库内容。

## 使用

```ts
import {
  EnterpriseAttestationFilter,
  type AttestationFilterPolicy,
} from "./index.ts";

const policy: AttestationFilterPolicy = {
  // 管理员维护的企业 RP ID 名单
  adminRpIds: ["login.example.com"],
  // 受管认证器的 AAGUID 清单，支持 UUID 或 16 字节十六进制
  managedAaguids: ["d6f2b6c8-4a27-4f1e-9d57-6b75a7f4c33a"],
};

const filter = new EnterpriseAttestationFilter(policy);

registrationService.onComplete(({ request, response }) => {
  const record = filter.onRegistrationComplete({ request, response });
  // record 即落库内容；降级时 certificate、serialNumber、aaguid 均为 null
  repository.saveAttestation(record);
});
```

也可以直接调用
`filterEnterpriseAttestation({ request, response }, policy)`，或使用
`filterEnterpriseAttestation(request, response, policy)`。

## 过滤规则

以下条件全部满足时才落库为 `enterprise`：

- `request.rpId` 在 `adminRpIds`（兼容 `allowedRpIds`/`trustedRpIds`）中。
- 请求声明 `attestation: "enterprise"`，且响应 CBOR `fmt` 与请求的
  `attestationFormats`（兼容 `requestedAttestationFormats`）一致。
- 认证器数据中的 AAGUID 与叶子证书的
  `1.3.6.1.4.1.45724.1.1.4` 扩展一致，且该 AAGUID 在 `managedAaguids`
  （兼容 `managedInventory`/`aaguidAllowlist`）中。
- 证书可提取序列号；优先使用证书 Subject 的 `serialNumber`
  (`2.5.4.5`)，否则使用 X.509 证书序列号。

任何一步失败都会返回 `attestation: "none"`、`fmt: "none"`，并将
`certificate`、`serialNumber`、`aaguid` 全部置为 `null`，避免普通站点获得
可关联标识。

实现不依赖第三方包，内置 CBOR 与 X.509 DER 解析。输入的
`attestationObject` 可以是原始字节、`ArrayBuffer`、base64 或 base64url；
证书 `x5c` 支持同样的编码形式。

## 验证

`npm test` 覆盖四类落库结果：受管企业注册（保留证书和序列号）、RP ID
不在管理员名单、请求/响应证明格式不一致、AAGUID 不在受管清单；另覆盖
证书与认证器 AAGUID 不一致、普通站点注册以及已是解析后响应的回调。
