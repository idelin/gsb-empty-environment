# benefit-ledger 会员权益账本

按套餐有效区间管理次数额度的会员权益库，TypeScript 实现，零运行时依赖。

## 核心语义

- **有效区间**：每个套餐对每类权益给出次数额度，半开区间 `[validFrom, validTo)` 内有效；未生效/已过期套餐不参与扣减。
- **叠加规则**（按权益类型注册，注册后不可变更）：
  - `stack`：有效套餐额度求和；扣减按「先到期先用，到期相同按套餐 ID 升序」分配。
  - `max`：有效套餐额度取最大；扣减全部落在剩余额度最大的套餐上（并列时先到期、再按 ID）。
- **确定性扣减**：分配顺序完全由有效期、套餐 ID 与规则决定，相同操作序列必然得到相同分配；所有时间由调用方传入，库内不读墙钟。
- **撤销**：按原 allocations 逐笔返还来源套餐；不延长任何套餐有效期——已过期套餐被返还的额度依然不可消费。重复撤销抛 `AlreadyRevokedError`。
- **追溯更正不改写历史**：余额一律由消费日志推导，`correctPackage` 只更新套餐当前版本，已确认消费的分配永不变更；更正与历史消费矛盾时由 `detectConflicts()` 显式报告（额度低于已消费、消费时刻不在有效期内等），绝不悄悄改写。

## API 概览

```ts
const ledger = new BenefitLedger();
ledger.registerBenefitType('wash', 'stack');
ledger.addPackage('m1', { packageId: 'p1', validFrom: t0, validTo: t0 + 30 * DAY, quotas: { wash: 10 } });
const record = ledger.consume('m1', 'wash', 6, t0 + DAY);   // 确定性分配
ledger.revoke(record.consumptionId, t0 + 2 * DAY);          // 返还原来源
ledger.correctPackage('m1', 'p1', { quotas: { wash: 4 } }, t0 + 3 * DAY); // 追溯更正
ledger.getBalance('m1', 'wash', t0 + 4 * DAY);              // 余额与套餐明细
ledger.getConsumptionHistory({ memberId: 'm1' });           // 历史查询（深拷贝）
ledger.detectConflicts('m1');                               // 更正与历史的冲突报告
ledger.getEvents();                                         // 全量审计事件（含更正前后快照）
```

## 运行测试

需要 Node ≥ 22.6（原生 TypeScript type-stripping；23.6+ 默认开启）：

```bash
node --test test/ledger.test.ts
```

有 TypeScript 编译器时可做类型检查：`tsc --noEmit`（配置见 `tsconfig.json`，启用 `strict` 与 `erasableSyntaxOnly`）。
