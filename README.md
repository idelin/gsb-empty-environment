# CSP 片段合成器（per-response fragment synthesizer）

面向 TypeScript 渲染宿主的可导入库，处理“每响应独立 CSP 授权 + 服务端/缓存片段混合拼装”。

## 模块

- `src/bdd.ts` — 固定序 ROBDD。开关变量在构造时按固定顺序声明，所有门表达式编译成
  规范化 BDD 节点；可达性以定点符号条件计算，全程不枚举开关取值。提供 `and/or/not/implies/
  entails/isSat/witness/compile`。
- `src/model.ts` — 片段、脚本、加载规则、来源表达式、可信根与 CSP 策略类型；SHA-256
  内容承诺；`NonceAuthority` 为每个响应独立签发 nonce，校验响应 ID、树位置、脚本 ID 与
  内容承诺绑定，并要求 nonce 已发布。旧响应的 nonce 在新权威下直接判为 `nonce-foreign-response`。
- `src/synthesizer.ts` — `FragmentSynthesizer`。
  - `instantiate` 把片段树展开为“带位置的脚本 + 加载边”。同一片段节点可在多个位置复用，
    每个出现位置拥有独立身份、独立 nonce，并区分片段自身的可继承来源与父 slot 的位置继承来源。
    加载边允许形成有环有向图（片段树本身保持有限）。
  - 评估时为每个可信根建立携带“根来源（provenance）”的信任标签，沿加载边做最小定点传播；
    `strict-dynamic` 下主机名命中本身不能引导信任，只有受信根实际发起且来源可继承的链才放行。
  - 多策略按各自完整判定（含根来源与符号条件）求交，而不是先把来源集合求并。
  - `decide(targetKey)` 返回精确允许条件 BDD、每策略条件、是否允许、最短授权链（BFS）和拒绝原因；
    撤销根后仍存在的环会以 Tarjan SCC 报告为 `orphan-cycle-no-root`。
  - `renderConfigsFor(positionKey)` 产出缓存片段所有可接受渲染配置，钉住响应、位置、承诺、
    nonce 与可继承来源。
- `src/cache.ts` — `CachePublisher` 用 HMAC-SHA256 给共享片段缓存签发证书，覆盖全部可接受
  渲染配置；校验时对规范化 JSON 验签，并要求当前符号条件被证书条件蕴含。任何字节变化导致
  承诺不一致即返回 `commitment-changed`，对应授权失效。

## 入口

统一从 `src/index.ts` 导入：

```ts
import {
  FixedOrderBDD, gate, NonceAuthority, FragmentSynthesizer, CachePublisher,
} from './src/index.ts';
```

## 参考核对

`test/` 用小配置逐页构造与图遍历作为参考实现，覆盖：

- 两个策略各自存在链，但门控互斥时没有共同执行配置；
- 完整判定求交 vs 来源集合求并、strict-dynamic 下主机名不足；
- 同一共享片段在两个树位置的不同继承，一处放行一处 `source-not-inheritable`；
- 撤销可信根后 s1↔s2 成为孤立环；
- 同主机不同脚本字节触发 `commitment-mismatch`；
- nonce 未发布即取消授权，旧响应 nonce 进入新页被拒；
- 最短授权链的精确跳数与 BDD 符号条件。

运行：

```sh
npm test
```
