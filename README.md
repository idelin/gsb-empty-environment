# 嵌入式 SAML 2.0 响应验证组件

零依赖 TypeScript 实现，面向登录服务的单 Assertion、Browser SSO Bearer 流程。验证器不执行 XPath，也不在验证后再次搜索业务对象；认证声明只能通过验证器返回的不可变句柄读取。

## 使用

```ts
import {
  parseSamlXml,
  readRawRange,
  verifySamlResponse,
} from "./index.ts";

const rawXml = Buffer.from(httpRequestBody); // 原始 UTF-8 字节，不要重新序列化
const document = parseSamlXml(rawXml);

const result = verifySamlResponse({
  document,
  rawXml,
  policy: {
    requiredSignatures: "both", // response | assertion | both
    tenantId: "tenant-a",
    idpMetadata: {
      tenantId: "tenant-a",
      entityId: "https://idp.example.test/metadata",
      generation: 7,
      signingCertificates: [idpSigningCertificatePemOrDer],
    },
    destination: "https://sp.example.test/acs",
    audience: "https://sp.example.test/metadata",
    inResponseTo: pendingRequestId,
    now: new Date(),
    clockSkewMs: 60_000,
  },
});

// 业务代码必须使用这些句柄，而不是重新查询 document。
const nameId = result.nameId;
const issuer = result.issuer;
const destination = result.destination; // 仅当 Response 自身被签名时非 null
const audience = result.audience;
const inResponseTo = result.inResponseTo;
const expiresAt = result.notOnOrAfter;
const confirmation = result.subjectConfirmation;

// 可审计地取得被签节点在原始字节中的精确片段。
const signedAssertion = result.assertion;
const exactBytes = readRawRange(document, rawXml, signedAssertion.rawRange);
```

## 安全边界

- `parseSamlXml` 只接受 UTF-8，拒绝 DTD、自定义实体、XSLT、命名空间伪装、重复/别名 ID、重复展开属性名及非 UTF-8 编码。
- 每个节点都有解析器分配的稳定身份和原始 UTF-8 字节区间；验证时重新校验原始字节 SHA-256，防止把另一份树的句柄与当前输入混用。
- 每个签名必须直接位于其签名的 Response 或 Assertion 下，仅允许一个 Reference。Reference URI 必须精确等于当前节点的唯一 `#ID`。
- Reference 变换只允许 `enveloped-signature` 加一个可选的、配置允许的 C14N；显式 C14N 缺失时按 XMLDSig 默认 C14N 1.0 执行，且该算法仍须在允许列表。SHA-1、HMAC、XPath、外部 URI、额外 Reference 和未知变换均拒绝。
- Digest、SignedInfo C14N 和签名算法使用硬白名单及可进一步收窄的配置。支持 SHA-256/384/512、RSA PKCS#1 v1.5、XMLDSig ECDSA，以及 C14N 1.0/Exclusive C14N。
- 签名证书必须与所选租户 IdP 元数据代次中的证书 DER 完全一致，并在验证时刻有效；`KeyInfo` 可省略，若携带则必须精确命中元数据。返回结果带有租户和代次。
- `response`、`assertion`、`both` 是最低签名策略。树中任何已存在的 Response/Assertion 签名也必须有效，非策略要求的伪造签名不会因“未要求”而被忽略。
- 登录组件只接受 Response 下唯一的直接 Assertion。嵌入式/包装的 Assertion、签名位置错误的元素、DSig 元素出现在签名子树之外，都会失败。
- Issuer、Destination、Audience、InResponseTo、Conditions、IssueInstant、SubjectConfirmation 和 NameID 均从已签句柄的直接子节点读取。
- `assertion` 策略下 Response 未签名，因此其 Destination/InResponseTo/Status 不被信任，`result.destination` 为 `null`；Destination 由策略传入并校验签名 Assertion 内的 bearer Recipient。
- 组件负责校验 `InResponseTo` 与待处理请求绑定；调用方仍需在验证成功后原子消费该请求 ID，防止同一响应的并发重放。

## 测试

```sh
npm test
```

测试覆盖合法双签响应、仅 Response/仅 Assertion 策略、重复 ID、Assertion 移位/包装、命名空间重绑定、未签 Response 内的旧签名 Assertion、多 Reference、未知变换、SHA-1、证书代次错配、过期声明及原始字节与树错配。
