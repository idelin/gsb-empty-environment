# dash-cenc-guard

面向 TypeScript DASH 播放器的 CENC 清单完整性校验包。零第三方依赖，同构运行于
浏览器与 Node.js，用于抵御“篡改 MPD 诱使播放器向错误密钥/许可证服务器申请授权”的攻击。

## 威胁模型

攻击者改写 MPD 后，可能让 `cenc:default_KID`、初始化分段 `tenc` 盒子的 KID、
`PSSH` 中的密钥标识三者互不一致，或把 `licenseUrl` 指向受控主机。播放器若仅凭
清单内容调用 EME `requestMediaKeySystemAccess` / `createLicenseRequest`，就会为
攻击者指定的密钥申请许可证，造成密钥泄露或内容不可解密。

本包在把清单交给 EME 之前，强制校验以下不变量。

## 校验规则

对每个 `AdaptationSet`：

1. **default_KID 一致**：其下所有 `Representation` 解析出的 `cenc:default_KID`
   必须相同（自动忽略连字符、大小写、`urn:uuid:` 前缀）。
2. **default_KID == tenc KID**：每个初始化分段 `sinf/tenc` 中的 16 字节 KID
   必须等于清单 KID。
3. **方案一致**：清单声明的 CENC 方案（`cenc` / `cbcs` / `cbc1` / `cens`）
   必须等于同轨道 `sinf/schm` 的 `schemeType`。
4. **PSSH 含该 KID**：初始化分段至少包含一个 `PSSH` 盒子，且其中能找到 tenc KID。
   - 标准 `PSSH v1`：直接读取 KID 数组。
   - Widevine `PSSH v0`：解析 protobuf 中的 `KeyId` 字段。
   - 可选 `requireMpdPsshKid`：同时要求 MPD 内嵌 `cenc:pssh` 覆盖该 KID。
5. **licenseUrl 白名单**：必须是 `https`、不含 userinfo、默认仅 443 端口，
   且主机与路径命中白名单（支持 `https://*.example.com` 子域通配）。

## 快速开始

### 端到端（推荐）

```ts
import { validateDashManifest } from 'dash-cenc-guard';

const result = await validateDashManifest(mpdText, {
  allowedLicenseUrls: [
    'https://license.example.com/v1/license',
    'https://*.drm.partner.org',
  ],
  async fetchInitSegment(representationId) {
    const res = await fetch(initSegmentUrlFor(representationId));
    return new Uint8Array(await res.arrayBuffer());
  },
});

if (!result.ok) {
  // 阻止清单进入播放/EME 流程
  throw new Error(`CENC 校验失败: ${result.issues.map((i) => i.code).join(', ')}`);
}
```

### 使用播放器已有的解析结果

```ts
import { parseMpd, parseInitSegment, validateManifest } from 'dash-cenc-guard';

const manifest = parseMpd(mpdText);
for (const adaptationSet of manifest.adaptationSets) {
  for (const representation of adaptationSet.representations) {
    representation.initSegment = await fetchBytes(representation.id);
  }
}
const result = validateManifest(manifest, {
  allowedLicenseUrls: ['https://license.example.com/'],
  expectedScheme: 'cenc', // 清单未声明方案 URN 时显式提供
});
```

## 错误码

| Code | 含义 |
| --- | --- |
| `DEFAULT_KID_MISMATCH` | 同一 AdaptationSet 内各 Representation KID 不一致 |
| `DEFAULT_KID_MALFORMED` / `DEFAULT_KID_MISSING` | KID 非法或未声明 |
| `TENC_MISSING` / `TENC_KID_MISMATCH` | 缺少 tenc 或其 KID 与清单不符 |
| `SCHEME_MISSING` / `SCHEME_MISMATCH` | 缺少 schm 或方案值不一致 |
| `PSSH_MISSING` / `PSSH_KID_MISSING` | 缺少 PSSH 或其中找不到 tenc KID |
| `LICENSE_URL_MISSING` / `LICENSE_URL_NOT_HTTPS` / `LICENSE_URL_MALFORMED` / `LICENSE_URL_NOT_ALLOWLISTED` | 许可证地址违规 |
| `INIT_SEGMENT_MISSING` / `NO_CONTENT_PROTECTION` / `ADAPTATION_SET_EMPTY` | 输入结构不完整 |

每个 `ValidationIssue` 都带 `message` 与 `locator`（指向 AdaptationSet /
Representation / 盒子路径），便于安全日志关联。

## 模块结构

- `src/types.ts`：与解析实现解耦的中立数据模型与错误码。
- `src/kid.ts`：KID 规范化（忽略连字符/大小写/URN）与字节转换。
- `src/mp4.ts`：防御性 ISO-BMFF 盒子遍历，提取 `tenc` / `schm` / `PSSH`。
- `src/xml.ts` / `src/mpd.ts`：零依赖 XML 与 MPD 解析、ContentProtection 继承。
- `src/license.ts`：https 与白名单匹配。
- `src/validator.ts`：规则编排与 Widevine PSSH KID 提取。

## 测试

```sh
npm test
```

测试使用 Node 内置 test runner（需 Node >= 22.6 的类型剥离），共覆盖 KID 规范化、
MP4/MPD 解析、白名单匹配，以及五类篡改攻击的拦截场景。
