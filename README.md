# exact-units

精确单位换算库（TypeScript，零依赖）：

- **单位 = 量纲向量 + 精确比例（BigInt 有理数）+ 可选偏移**（如 °C 相对 K 的 273.15）
- **转换图**：边为仿射映射 `y = m·x + c`，成环时校验一致性，矛盾边直接拒绝
- **绝对量 / 差量类型区分**：Δ10 °C = Δ10 K（不加偏移）；绝对 20 °C = 293.15 K
- **全程 BigInt 精确运算**，大数不经过 Number；十进制输出必须显式指定舍入规则
- 量纲不匹配（米 → 秒）抛出 `IncommensurableError`

## 运行要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除；≥ 23.6 无需任何 flag）。

```sh
npm test    # node --test "test/*.test.ts"
npm run demo
```

## 结构

- `src/rational.ts` — BigInt 有理数：精确解析十进制字面量、7 种显式舍入模式
- `src/dimension.ts` — 7 个 SI 基本量纲的指数向量
- `src/unit.ts` — 单位定义与绝对/差量换算公式
- `src/quantity.ts` — `AbsoluteQuantity` / `DeltaQuantity`，禁止绝对量相加
- `src/graph.ts` — 转换图：路径复合、逆边推导、矛盾环检测
- `src/units.ts` — 预定义单位（m/cm/km/in、s/min/h、kg/g、K/°C/°F）
