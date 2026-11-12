# 聚合贡献准入包（contribution admission gate）

在隔离 worklet 中为 `contributeToHistogram` 提供准入校验：恶意脚本无法把加密报告
导向自建协调方，也无法用畸形 bucket/value、超长或伪造的 `context_id`、冲突的
`filtering_id` 干扰聚合结果。所有校验失败统一抛 `TypeError`，且本次调用（含批量）
整体不写入任何内容。

## 准入规则

- `aggregation_coordinator_origin`：必须命中宿主登记白名单（仅接受 http/https origin，
  登记与调用时都做规范化，带路径/凭据/非 http(s) 方案无法绕过），未登记则抛 `TypeError`。
- `bucket`：必须是 `bigint` 且落在 `[0, 2^128)`；number 类型一律拒绝（无法保证 128 位精度）。
- `value`：必须是非负 number 整数；负值、浮点、`NaN`、`Infinity`、bigint 均拒绝。
- 报告容量：同一 `context_id` 汇聚为一份报告，最多 20 条；超出部分按顺序静默丢弃，不抛错。
- `context_id`：非空字符串，UTF-8 编码不超过 64 字节。
- `filtering_id`：`[0, 255]` 整数（默认 0），同一份报告（含批量与跨调用）只能使用同一个值，
  报告级声明与单条覆盖值冲突时抛 `TypeError`。
- 调试模式：只有 `thirdPartyCookiesEnabled === true` 时请求才生效，否则按未开启处理；
  一旦生效在该报告上锁定。
- 可观察性：报告状态保存在闭包中，worklet 拿到的冻结 gate 只有 `contributeToHistogram`；
  报告只能由宿主通过创建时返回的 `drainReport` 交给可信 sink，drain 返回的元数据不含贡献内容，
  报告对象及内部数组/条目全部冻结。

## 用法

```ts
import { createContributionGate } from './index.ts';

// 宿主侧（worklet 之外）创建，只把 handle.gate 暴露给广告脚本。
const { gate, drainReport } = createContributionGate({
  allowedCoordinatorOrigins: ['https://aggsvc.example'],
  capabilities: { thirdPartyCookiesEnabled: areThirdPartyCookiesEnabled() },
});

gate.contributeToHistogram(
  { bucket: 123456789012345678901234567890n, value: 1 },
  {
    aggregationCoordinatorOrigin: 'https://aggsvc.example',
    contextId: 'campaign-42',
    filteringId: 0,
    debugMode: false,
  },
);

// 宿主在报告时机取出并加密/发送；不存在时返回 null。
const record = drainReport('campaign-42', {
  send: (report) => encryptAndSchedule(report),
});
```

## 结构

- `index.ts`：包入口，仅导出类型与 `createContributionGate`/origin 工具。
- `src/types.ts`：公开接口与报告数据结构。
- `src/validation.ts`：origin 白名单与 bucket/value/filtering_id/context_id 校验。
- `src/gate.ts`：准入 gate、每 context 报告状态、容量截断、debug 门控与 host-only drain。
- `test/contribution-gate.test.ts`：26 个用例，含六个指定验收场景。

## 验证

```sh
npm test
```

指定验收场景均在测试中覆盖：未登记协调方、负 `value`、第 21 条贡献丢弃、
`context_id` 超长（含 UTF-8 多字节计数）、`filtering_id` 冲突、
Cookie 不可用时调试模式被忽略。另含批量原子回滚、调用后对象篡改防护、
drain 后冻结与删除、worklet 面无读取通道等隔离性用例。
