# 范围规则规范化工具

把一组带优先级的整数多维盒规则(1~3 维,半开区间 `[lo, hi)`)规范化为
**互不重叠、与输入顺序无关**的带标签分区。

## 规则语义

- 每条规则:`{ box: { lo, hi }, label, priority }`,坐标为整数,`lo < hi`。
- 盒可重叠;重叠处 **优先级高者胜**;优先级相同取 **标签字典序较小者**
  (确定性 tie-break,保证结果与规则输入顺序无关)。

## 算法

1. **坐标压缩**:收集所有规则边界,得到每个维度的有序坐标,切出基本单元网格。
2. **单元决胜**:每个基本单元被规则要么完全覆盖要么完全不覆盖,
   取覆盖规则中的胜者作为单元标签(无覆盖则为空)。
3. **确定性合并**:对每个标签的单元集合,反复取字典序最小的剩余单元,
   按维度 0..D-1 的顺序贪心扩张成最大盒。合并是单元集合的确定性函数,
   不依赖任何遍历顺序 —— 不存在"只合并一轮、结果依赖输入顺序"的问题。

## API

```ts
import { normalize, queryPoint, verify } from './src/normalize';

const regions = normalize(rules);        // Region[] = { box, label }[]
queryPoint(regions, [3, 4]);             // => label | null
const report = verify(rules, regions);   // { ok, disjoint, semanticsMatch,
                                         //   regionVolume, coveredVolume, errors }
```

`verify` 核验三项:区域两两不重叠;每个基本单元(及每个区域的 `lo` 角)上
分区标签与规则决胜结果一致;区域体积之和等于被覆盖单元体积之和。

## 构建与测试

```sh
npm install        # 安装 typescript(需要网络)
npm run build      # tsc -> dist/
npm test           # node test/normalize.test.js
```

`dist/normalize.js` 是 `src/normalize.ts` 的预构建等价物,测试直接跑在它上面;
有工具链时用 `npm run build` 重新生成。
