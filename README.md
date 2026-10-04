# pkg-dependency-solver

整数版本的包依赖求解器（TypeScript，零运行时依赖）。针对最多约 20 个包的问题规模，
求满足全部约束且**升级数量最少**的安装方案；升级数并列时**优先更高版本**。
无解时返回**可复核的冲突子集**（关于删除极小的不可满足约束集），而不是只显示最后失败的依赖。

## 语义约定

- 版本号为非负整数；版本区间为闭区间 `{ min, max }`，缺省边界表示无限制。
- **升级数**：最终方案中与 `installed` 当前版本不一致（含新装）的包数量。
- **并列规则**：升级数相同时，按包名字典序比较版本向量，取字典序最大者（即优先更高版本）。
- **可选特性**：依赖边可声明 `feature`，仅当根需求通过 `features` 启用该特性时才生效。
- **软依赖**：依赖边声明 `optional: true` 时不会主动拉入目标包；
  但目标包因其他原因被安装时必须满足其区间。
- **冲突**：`conflicts` 声明的版本区间不允许与本版本共存（双向生效）。

## 用法

```ts
import { solve } from './src/index.ts';

const result = solve({
  repository: [
    { name: 'app', versions: [
      { version: 1, requires: [{ pkg: 'lib', range: { min: 2, max: 4 } }] },
    ]},
    { name: 'lib', versions: [1, 2, 3, 4, 5].map((version) => ({ version })) },
  ],
  required: [{ pkg: 'app' }],
  installed: { lib: 1 },
});

if (result.ok) {
  console.log(result.plan);     // { app: 1, lib: 4 }
  console.log(result.upgrades); // 发生变化的包
} else {
  console.log(result.conflict.summary); // 人类可读的冲突子集
  console.log(result.conflict.verify()); // true：子集无解且极小（去掉任意一条即可解）
}
```

## 冲突子集

无解时通过删除式极小化（MUS）计算约束子集：
反复尝试移除每条约束，若移除后仍无解则永久移除，直到不动点。
`conflict.verify()` 会复核两点：

1. 仅由该子集重建的问题仍然无解；
2. 子集极小——去掉其中任意一条约束后问题即可解。

## 实现要点

- `src/solver.ts`：深度优先回溯 + 约束传播（区间收缩、冲突排除、特性门控激活）
  + MRV 选包策略 + 分支限界（升级数下界剪枝）。
- `src/conflict.ts`：删除式极小不可满足子集计算与复核。
- 搜索节点数默认上限 200000，超出时抛出 `SearchLimitError`（可用 `maxNodes` 调整）。

## 运行测试

需要 Node ≥ 24（或 Node ≥ 22.6 并加 `--experimental-strip-types`），无需安装任何依赖：

```sh
node --test "test/*.test.ts"
```
