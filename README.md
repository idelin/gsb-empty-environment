隐私沙箱只向外发布合成位置所属的粗区域，几次不同网格发布的交集却可能比每个单独区域小得多。给 TypeScript 发布宿主写可导入的地理披露准入组件，只使用合成区域，不接收或返回个人坐标。每条已签声明用有理数顶点的多边形并集表示可能区域，允许洞、相接边和多个不连通部分；策略规定每个授权观察者及其可串通组经过整批发布后，可行区域的每个正面积连通分量都须达到指定面积下限。仅一条公共边相接算连通，单点相接不算，零面积交集不能拿来凑阈值。不同观测可属于同一可能轨迹或互斥的匿名批次；缺少经认证的关联时不可直接将两组假设合成同一交集。判断是否可发布，返回按观察组汇总的最小分量面积及独立可核验的有理数证书，禁止输出定位点或个体路径。更新可撤回或更正某个区域声明，拒绝发布不变更历史。用小整数格点边界的独立分割参考核对重叠洞、极窄交集、单点接触和两次各自合规却联合低于阈值；对非整数交点保持精确判定，不用浮点容差替代权限边界。

## 用法

```ts
import {
  createDisclosureHost,
  polygonRegion,
  verifyCertificate,
} from "./index.ts";

const host = createDisclosureHost({ minComponentArea: "4" });

host.addClaim({
  claimId: "a",
  observerId: "observer-a",
  associationId: "trajectory-1",
  signature: "detached-signature",
  region: polygonRegion([
    [0, 0],
    [8, 0],
    [8, 8],
    [0, 8],
  ]),
});

host.addClaim({
  claimId: "b",
  observerId: "observer-b",
  associationId: "trajectory-1",
  signature: "detached-signature",
  region: polygonRegion([
    [6, 0],
    [14, 0],
    [14, 8],
    [6, 8],
  ]),
});

const result = host.publish();
if (!result.publishable) {
  console.log(result.groups.map((group) => group.minimumComponentArea));
}
```

`region` 是合成区域的并集。一个多边形可用 `{ outer, holes }` 表示洞，`polygons`
数组表示多个不连通部分。所有顶点与面积用 `bigint` 有理数计算；也可传入
`{ numerator, denominator }`、`"n/d"`、十进制字符串或有理数元组。

同一 `observerId` 且没有匿名批次标记的多次签名观测会合并为一个可行区域；
可用 `{ linkSameObserver: false }` 关闭该默认行为。跨 observer 的声明只有带
同一非空认证关联（`associationId`、`trajectoryId`、`subjectId`、`linkId`、
`groupId` 或 `authenticatedLinkId`）时才会相交。匿名 `batchId` 只用于审计
和互斥批次标记，绝不会自动把声明连成同一轨迹。显式 `options.groups` 可给出
`{ claimIds, authenticated: true }`；若混用了不同关联，会抛出
`UnauthenticatedAssociationError`。

`publish()` 返回：

- `publishable`：所有组的每个正面积连通分量都达到下限，且至少有一个分量。
- `groups[].minimumComponentArea`：每组最小正面积连通分量的精确有理数。
- `certificate`：只含策略值、组 ID、声明摘要和面积，不含量化顶点、定位点或
  个体路径的证书。

`verifyCertificate(certificate, declarations, policy, options)` 从头重算精确
交集并核对摘要。相交仅共享一条正长度边时合并为一个连通分量；仅共享一个
顶点时保持分离；空交集或零面积交集不能通过阈值。

## 更新历史

`correctClaim(id, { region, signature })` 追加新修订，
`retractClaim(id)` 追加撤回事件。历史只追加，旧声明摘要永远保留。
`publish()` 被策略拒绝时不会写入历史，也不会替换 `lastPublication`；只有成功
发布才追加 `batch-published` 事件。

## 测试

```sh
npm test
```
