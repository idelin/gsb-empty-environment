# COOP与COEP头隔离门

攻击者借重复头或Report-Only值，诱使页面误判为跨源隔离。为TypeScript浏览器宿主实现可导入的隔离判定组件：COOP、COEP按Structured Fields解析，same-origin与require-corp同时有效才开放SharedArrayBuffer；重复头、解析失败、Report-Only不作数。用变形头测试。

## 用法

```ts
import {
  canUseSharedArrayBuffer,
  evaluateCrossOriginIsolation,
} from "./index.ts";

const decision = evaluateCrossOriginIsolation([
  ["Cross-Origin-Opener-Policy", "same-origin"],
  ["Cross-Origin-Embedder-Policy", "require-corp"],
]);

decision.isolated;
decision.sharedArrayBuffer;
decision.coop.status;
decision.failures;

const enabled = canUseSharedArrayBuffer([
  ["Cross-Origin-Opener-Policy", "same-origin"],
  ["Cross-Origin-Embedder-Policy", "require-corp"],
]);
```

输入可以是保留重复字段行的 `[name, value]` 列表、`Headers` 实例，或值为字符串/字符串数组的对象。浏览器宿主应优先传入原始字段行；重复的实际头会使对应策略判定为 `duplicate`，不会采用其中任意一个值。`Headers` 会把重复行合并为逗号分隔值，Item 解析因此失败。

`Headers` 中存在逗号时无法排除“引号内逗号”与“重复行拼接”两种来源，因此该字段失败关闭；确需合法引号内逗号时请传入原始字段行列表。

判定规则：

- `Cross-Origin-Opener-Policy` 必须是有效的 Structured Fields Item，且 token 为 `same-origin`。
- `Cross-Origin-Embedder-Policy` 必须是有效的 Structured Fields Item，且 token 为 `require-corp`。
- `*-Report-Only` 头完全忽略，不参与隔离判定。
- 实际头重复、字段值含多个 Item、值不是 token、参数语法错误或 `report-to` 非字符串时失败关闭。
- `isolated` 与 `sharedArrayBuffer` 仅在 COOP、COEP 同时有效时为 `true`。

## 测试

```sh
npm test
```
