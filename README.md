# exact-compare

精确数比较库：整数、有理数、十进制、显式误差区间统一建模，比较结果为
`lt | eq | gt | undecided`。全程以 **BigInt / 精确有理数** 表示数值，
**不经过 Number 中转**，**不使用任何 epsilon** 把接近的数当相等。

## 运行环境

- Node.js >= 23.6（原生 TypeScript 类型擦除 + `node:test`，零依赖）
- Node 22.6–22.x 需加 `--experimental-strip-types`

```sh
npm test     # node --test 'test/*.test.ts'
npm run demo # node examples/demo.ts
```

## 设计

**精确表示**

- `Rational`：BigInt 分子/分母，构造时规范化（`den > 0`、约分），四则运算与比较全精确。
- `Decimal`：`coeff × 10^(-scale)`，BigInt 系数；除法必须显式给出舍入规则
  （`floor | ceil | truncate | half-up | half-even`），余数比较在 BigInt 上进行。
- 字面量解析直接把十进制字符串读进 BigInt；`NaN`/`Infinity` 按不支持显式拒绝
  （`UnsupportedLiteralError`）。

**证据模型（`src/value.ts`）**

值 = 精确中心 + 一组按来源（`source`）标记的误差项。求差 `a - b` 时：

- 相同 `source` 的误差**相关**：半径相消（共享来源不能被当作独立误差）；
- 不同 `source` 的误差**独立**：半径相加。

**可恢复的精度细化比较协议（`src/compare.ts`）**

对差值证据 `d = a - b`：

1. `lower(d) > 0` → `gt`；`upper(d) < 0` → `lt`（区间分离才下确定结论）；
2. `d` 无误差项且中心恰为 0 → `eq`（唯一的相等证据是精确相等）；
3. 否则区间重叠：若预算未耗尽且任一侧可细化（十进制展开逐位、几何收缩），
   消耗 1 步预算细化后重判；否则返回 `pending / undecided`。

`CompareState` 是纯数据，`resumeCompare(state, budget)` 可在 pending 处继续；
`done` 状态幂等。重叠的区间**不会**被误判为相等——无限逼近 1/3 的展开与精确
1/3 比较，任意预算下都保持 `undecided`。

**参考核验（`test/properties.test.ts`）**

用确定性伪随机生成的有限精确有理数集核验：全序性（精确值永不未决且与参考
`cmp` 一致）、反对称性、传递性（抽样三元组）、未决状态行为（分离则确定、
重叠则 pending 且追加预算不误判）。

## 运行样例（`npm run demo`，节选）

```text
1) 大整数相差 1（2^90 vs 2^90+1），不经过 Number：
  2^90 vs 2^90+1: verdict=lt status=done stepsUsed=0

2) 0.1 + 0.2 === 0.3（精确十进制，无浮点误差）：
  0.1 + 0.2 = 0.3
  0.1+0.2 vs 3/10: verdict=eq status=done stepsUsed=0

3) 误差区间：重叠不蕴含相等；共享来源的误差相消：
  1±0.5 vs 1.2±0.5 (独立来源): verdict=undecided status=pending
    step 0: a-b ∈ [-6/5, 4/5]
  10±1 vs 11±1 (同一把尺子): verdict=lt status=done
    step 0: a-b ∈ [-1, -1] (exact)        <- 共享来源相消，差值恰为 -1

4) 可恢复细化协议：1/3 的十进制展开 vs 0.334：
  budget=2: verdict=undecided status=pending stepsUsed=2
    step 2: a-b ∈ [-7/500, 3/500]          <- 预算耗尽，保持未决
  resume(+10): verdict=lt status=done stepsUsed=4
    step 3: a-b ∈ [-1/500, 0]              <- 上界触 0 不算分离，继续细化
    step 4: a-b ∈ [-1/1250, -3/5000]       <- 分离，判定 lt

5) 无限逼近也不误判相等：expansion(1/3) vs 精确 1/3，预算 12 仍 undecided

6) NaN 字面量按不支持拒绝：
  parseValue('NaN') -> UnsupportedLiteralError
```

## 测试结果（`npm test`，Node v23.9.0）

```text
✔ 精确值：相等只在确切相等时成立
✔ 大整数相差 1：lt/gt 可区分
✔ 无 epsilon：相差 10^-30 仍是 lt，不是 eq
✔ 静态区间：分离则确定，重叠则未决且恢复后依然未决
✔ 共享来源的误差相消，不得当作独立误差
✔ 部分共享来源：共享部分相消、独立部分相加
✔ 可恢复细化：预算不足时 pending，追加预算后分离
✔ 区间重叠不蕴含相等：无限逼近 1/3 也永不判 eq
✔ done 状态幂等 / startCompare 不消耗预算 / 半径 0 退化为精确值
✔ 显式舍入规则：1/3 保留 2 位；负数 floor/ceil/truncate；half-up/half-even 平局
✔ 0.1 + 0.2 === 0.3（精确十进制）
✔ 字面量解析：整数/小数/分数/指数/区间；NaN、Infinity、0x10 等拒绝
✔ 全序性 / 反对称性 / 传递性（精确参考核验）
✔ 未决状态：分离则确定，重叠则 pending 且恢复不改变结论
ℹ tests 31  ℹ pass 31  ℹ fail 0
```

## 目录结构

- `src/rational.ts` — 精确有理数（BigInt）
- `src/decimal.ts` — 精确十进制与显式舍入规则
- `src/value.ts` — 证据模型：来源标记的误差项、相关/独立合并、可细化值
- `src/compare.ts` — 可恢复细化比较协议
- `src/parse.ts` — 字面量解析（拒绝 NaN/Infinity）
- `test/` — node:test 单元测试与性质核验
- `examples/demo.ts` — 运行样例
