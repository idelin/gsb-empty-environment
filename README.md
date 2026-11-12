# 遗留 Pin 与 Expect-CT 头策略

供 TypeScript 网络栈导入的安全响应头策略包。`Public-Key-Pins`、
`Public-Key-Pins-Report-Only` 和 `Expect-CT` 被识别但没有副作用：不保存动态
pin、不开启 CT 强制、不读取或请求 `report-uri`。同一响应中的 HSTS 仍按正常
规则处理。

## 使用

```ts
import {
  NetworkSecurityPolicy,
  type ChainCertificate,
} from './index.ts';

const policy = new NetworkSecurityPolicy({
  staticPins: {
    'example.com': {
      pins: [
        'sha256/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
        'sha256/BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=',
      ],
      includeSubDomains: true,
    },
  },
  exemptUserInstalledRoots: false,
  legacyPinStore: '/path/to/legacy-pins.json',
});

const result = policy.processResponse('https://example.com/', {
  'strict-transport-security': 'max-age=31536000; includeSubDomains',
  'public-key-pins': 'pin-sha256="..."; max-age=31536000; report-uri="..."',
  'expect-ct': 'max-age=86400; enforce; report-uri="..."',
});

const chain: ChainCertificate[] = [
  { spkiSha256: 'sha256/leaf-pin' },
  { spkiSha256: 'sha256/root-pin', userInstalledRoot: false },
];
const pinResult = policy.verifyCertificateChain('example.com', chain);
```

`staticPins` 也可使用数组形式：

```ts
staticPins: [
  {
    host: 'example.com',
    pins: ['sha256/...'],
    includeSubDomains: true,
  },
]
```

Pin 值接受 HPKP 风格的 `sha256/<base64>`、裸 base64，或 64 字符十六进制
SHA-256。证书链条目可以是 PEM/DER、`X509Certificate`，或
`{ spkiSha256 }` / `{ spkiDer }` 形式的预计算值。包内使用证书 SPKI 的
SHA-256 与静态 pin 比对，任意链证书匹配即通过。

## 行为

- `processResponse` 只改变 HSTS 状态；HPKP、HPKP-Report-Only、Expect-CT 均
  返回在 `ignoredHeaders` 中，但不产生状态或报告。
- 没有静态 pin 的主机正常通过 pin 层校验（`reason: "no-static-pins"`）。
- 有静态 pin 的主机必须有一张链证书匹配，否则
  `allowed: false, reason: "pin-mismatch"`。
- `exemptUserInstalledRoots` 默认为 `false`。为 `true` 时，链末端标记为
  用户安装根证书的连接会跳过该主机的静态 pin 强制。
- `getDynamicPins` 始终返回空数组；`getExpectCtState` 始终返回
  `undefined`；`getReportRequests` 始终返回空数组。
- 每条响应中的 STS 头独立处理；HPKP/Expect-CT 的存在不会阻止、清除或覆盖
  HSTS 更新。HSTS 只在 HTTPS/WSS 响应中生效，`max-age=0` 清除对应主机的
  策略。

## 启动清理

传入 `legacyPinStore` 路径时，构造函数会在启动阶段同步调用
`purgeDynamicPinsAtStartup()`。旧文件可以是记录数组、按主机名映射，或包含
`pins` / `hosts` / `dynamicPins` / `staticPins` 容器。

清理规则：显式标记为静态来源（`source: "static"`、`kind: "static"`、
`static: true` 等）的记录保留；其余遗留记录都视为旧版本动态 pin 并删除。
清理后文件规范化为：

```json
{
  "version": 2,
  "pins": [],
  "updatedAt": "..."
}
```

无法解析的旧文件会被替换为空快照。磁盘 pin 记录不会进入
`verifyCertificateChain` 的判定，只有调用方在 `staticPins` 中编译的规则会
参与证书判定。若不希望构造函数自动清理，可设置
`purgeLegacyPinsOnStartup: false`，之后显式调用
`purgeLegacyPinsOnStartup()`。

也可以直接使用独立清理函数：

```ts
import { purgeLegacyDynamicPinsAtStartup } from './index.ts';

const cleanup = purgeLegacyDynamicPinsAtStartup('/path/to/legacy-pins.json');
```

## 测试

```sh
npm test
```

测试覆盖动态 pin 设置无效、旧动态记录启动清理、静态 pin 强制、用户安装根证书
默认不豁免、HSTS 共存处理，以及 HPKP/Expect-CT 的 `report-uri` 不产生外发。
