# 双时态价格规则引擎（Bitemporal Price Book）

同时按 **业务生效时间（Valid Time）** 与 **系统知悉时间（Transaction Time）** 查询的价格账本。

## 语义

- 价格区间左闭右开：`[validFrom, validTo)`、`[txFrom, txTo)`
- 重叠规则按知悉时间**后入为主**（`txFrom` 最大者胜出；并列时后登记者胜出）
- 撤销（`retract`）只封闭规则的知悉区间，**仅影响撤销时刻及之后**的知悉时间查询，历史查询不受影响
- 金额一律使用**整数分**

## API

```ts
const book = new PriceBook();
const id = book.addRule({ priceCents: 10, validFrom: 0, validTo: 20, txFrom: 2 });
book.priceAt(7, 7);   // => 10
book.retract(id, 12); // 知悉时刻 12 起撤销
```

## 运行

需要 Node.js >= 22.6（使用内置 `node:test` 与 `--experimental-strip-types`，无需编译步骤）。

```bash
npm test    # 运行内置测试
npm run demo # 运行样例
```

## 文件

- `src/priceBook.ts` — 引擎实现
- `src/demo.ts` — 运行样例
- `test/priceBook.test.ts` — Node.js 内置测试（含验收边界）
