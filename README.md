# 聚合归因不带用户标识

源注册和触发上报若带上可链接的用户标识，跨站就能还原个人。本库只接受数字来源标识和触发标识的聚合记录，拒绝任何用户标识、原始 IP 和跨站目标不一致的配对；同一站点内来源与触发标识匹配的聚合记录会保留。

## 使用

```ts
import { filterAggregateRecords } from "./index.ts";

const { accepted, rejected } = filterAggregateRecords(records);
```

每条被接受的记录需要带有非负安全整数的 `sourceId` 与 `triggerId`（也兼容嵌套的 `source.id` / `trigger.id` 及常见下划线命名）。用户标识、原始 IP、非数字标识以及目标不一致的配对会进入 `rejected`，并附上 `reason` 和 `detail`。

上报组件 `AggregateReporter` 位于 `reporter.ts`，它调用同一个过滤库：

```ts
import { AggregateReporter } from "./reporter.ts";

const result = new AggregateReporter().report(records);
```

测试中包含一条携带用户标识的记录和一条目标错误的记录，见 `index.test.ts`。运行 `npm test` 即可验证。
