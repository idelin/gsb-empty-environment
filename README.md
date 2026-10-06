# mini-union-analyzer

小语言类型系统(对象 / 字面值 / 联合)+ TypeScript 风格的判别联合收窄分析器,
并以有限对象值集合动态执行条件, 核对静态剩余类型是否精确。

## 运行

```sh
npm test    # node test/run.js, 19 个用例
npm run demo
```

无第三方依赖, Node >= 16 即可。源码为 ESM JavaScript + JSDoc 类型注解;
若本机装有 TypeScript, 可执行 `npm run typecheck`(checkJs 严格模式)。

## 类型语言(src/types.js)

```text
Type ::= never
       | literal(字符串/数字/布尔)
       | object({ 字段名: { type, optional } })   optional 字段可缺失(缺失 tag)
       | union(成员...)                            规范化: 拍平/去重/无 never
```

所有类型都是有限的, `enumerate(type)` 可枚举其 denoted 的全部值
(可选字段产生"字段缺失"的取值), 这是精度核对的基础。

## 静态分析(src/analyzer.js)

对分支决策树(`iff(cond, then, else)` / `leaf(label)`, 条件由
`eq / and / or / not / get / val` 组成)自顶向下收窄输入类型:

- **tag 等值收窄**: `value.tag === "x"` 肯定分支只保留 tag 可能为 `"x"` 的
  联合成员并把该字段收窄到字面值; 否定分支用 `removeLiteral` 剔除该字面值。
- **缺失 tag**: 可选字段或无此字段的联合成员, 缺失值在肯定分支被排除
  (缺失 !== 字面值), 在否定分支保留。
- **未知字段报错**: 字段在所有联合成员上都不存在时产生 `UNKNOWN_FIELD`
  error 诊断。
- **分支合流取联合**: 结果中的 `residual` 为全部叶子剩余类型的规范化联合。
- **不可达单独标记**: 剩余类型为 never 的叶子进入 `unreachable` 列表,
  与错误诊断分开。

## 精度核对(src/precision.js)

`checkPrecision(inputType, program, values)` 在每个叶子上比较:

- `expected` = 静态剩余类型枚举出的值集合
- `actual`   = 动态执行(src/interpreter.js)中实际到达该叶子的值集合

判定 `exact` / `unsound`(missing, 动态有静态无) / `imprecise`(spurious,
静态有动态无)。传入 `enumerate(inputType)` 作为值集合时, spurious 即真正的
静态不精确; 不在输入类型内的值会被列入 `outsideInput`。

## 目录

- `src/types.js`        类型构造、规范化、枚举、包含判定
- `src/ast.js`          条件表达式与分支程序构造器
- `src/analyzer.js`     静态收窄分析
- `src/interpreter.js`  有限值集合上的动态执行
- `src/precision.js`    静态/动态对照的精度核对
- `test/`               测试(node test/run.js)
- `examples/demo.js`    含缺失 tag 的完整演示
