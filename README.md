# 会员权益账本（membership-benefit-ledger）

按套餐有效区间管理次数型权益额度的确定性账本，纯 TypeScript、零依赖。

## 运行要求

- Node.js ≥ 23.6（原生 TypeScript 类型擦除），或 ≥ 22.6 时加 `--experimental-strip-types`。
- 仓库含 `.nvmrc`（v23.9.0），使用 nvm 时先 `nvm use`。

```bash
npm test
```

## 领域模型

- **权益类型**：按代码注册，携带合并规则 `stack`（多套餐次数叠加）或 `max`（取各套餐最大值）。
- **套餐**：在有效区间 `[validFrom, validTo)`（左闭右开）内为若干权益类型提供次数额度。
- **消费**：指定权益类型、次数与发生时刻；只有时刻处于有效期内的套餐参与扣减。
- **撤销**：把次数精确返还原来源套餐。
- **追溯更正**：套餐额度/区间的更正是追加式版本记录，不回溯改写历史。

## 核心语义

### 确定性扣减
同一时刻参与扣减的套餐按 `(validTo, validFrom, packageId)` 升序分配——先到期先用，
到期日相同按生效日、再按套餐号字典序。相同输入必然得到相同分配，与注册顺序无关。
额度不足时整体失败（`InsufficientQuotaError`），不产生部分扣减。

### 合并规则
- `stack`：可用额度 = 各有效套餐剩余次数之和，每个套餐最多承担自身剩余额度。
- `max`：可用额度 = 当前有效套餐额度的最大值 − 这些套餐上已确认未撤销的消耗。
  额度归属"当前有效套餐集合"：套餐过期后，其历史消耗不再占用集合额度。

### 撤销
撤销按原 `allocations` 逐笔返还来源套餐。返还次数的可用性完全由来源套餐自身的
有效区间决定：来源已过期则返还次数不可用（`expiredOnReturn = true`），
撤销本身绝不延长任何有效期。重复撤销抛 `AlreadyRevokedError`。

### 追溯更正不改写历史
`correctPackage` 只追加一条 `PackageCorrection`（含更正前后版本与原因）并切换当前版本：

- 已确认消费的 `allocations` 在确认时即冻结，更正前后历史查询结果逐字节一致；
- 若更正把额度调到已确认消耗之下，差额以 `Balance.deficit` / `PackageBalance.deficit`
  显式暴露，而不是悄悄抹平或重分配；
- 全部注册、更正、确认、撤销都进入 `getEvents()` 审计流。

## API 概览

```ts
const ledger = new BenefitLedger();
ledger.registerBenefitType('massage', 'stack');
ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 100, grants: { massage: 10 } });

const c = ledger.consume({ benefitType: 'massage', quantity: 3, at: 10 });
ledger.available('massage', 10);          // 7
ledger.getBalance('massage', 10);         // 含逐套餐明细与 deficit
ledger.revoke(c.id, 20);                  // 返还 P1
ledger.correctPackage('P1', { grants: { massage: 8 } }, { at: 30, reason: '录错' });
ledger.getConsumptionHistory({ status: 'confirmed' });
ledger.getPackageCorrections('P1');
ledger.getEvents();
```

时间戳由调用方显式传入（建议 epoch ms），账本不读取系统时钟，保证行为可复现。

## 目录结构

- `src/types.ts` — 领域类型
- `src/errors.ts` — 错误类型
- `src/ledger.ts` — `BenefitLedger` 核心实现
- `test/ledger.test.ts` — 13 个行为测试（node:test）
