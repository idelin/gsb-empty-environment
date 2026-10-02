# geo-fence

带权重地理围栏（Geo-Fence）多边形包含判定器，TypeScript 实现，零运行时依赖。

## 特性

- **双几何内核**：射线法（Ray Casting，even-odd 规则）与风向数法（Winding Number，non-zero 规则），可按 `rule` 选项切换。
- **凹多边形**：五角星等任意凹环精确判定。
- **带孔洞多边形**：外环 + 任意数量孔洞环（岛屿镂空），孔洞内部判定为 `outside`。
- **自相交退化多边形**：五角星 `{5/2}`、蝴蝶结等，两种填充规则各自给出符合其拓扑定义的结果（even-odd 与非零环绕数的差异区域）。
- **边界严格定义**：见下节。

## 边界严格定义

- 多边形边界 = 外环全部边 ∪ 全部孔洞环的边（符合 OGC：孔洞边缘属于多边形边界）。
- 点 `P` 在边 `(A,B)` 上 ⟺ 叉积 `(B-A)×(P-A)` 的绝对值 ≤ `boundaryTolerance`（默认 `0`，即严格精确），且 `P` 落在边的包围盒内（含端点）。
- **顶点重合**是上述定义的特例：`P` 与任一顶点坐标完全相等时判定为 `boundary`。
- 射线法与风向数法均采用 `yi > y` 半开区间统计穿越，射线恰好经过顶点高度时不重复计数。
- 分类结果三态：`inside`（实体部分）/ `outside`（外部或孔洞内部）/ `boundary`（任一环的边上）。
- `contains()` 默认将 `boundary` 计入围栏生效范围，可用 `includeBoundary: false` 排除。

## API

```ts
import { classifyPoint, contains } from './src/geofence.ts';

classifyPoint(point, polygon, { rule: 'evenodd' });   // 'inside' | 'outside' | 'boundary'
classifyPoint(point, polygon, { rule: 'nonzero' });   // 风向数法
contains(point, polygon, { includeBoundary: true });  // boolean
```

`polygon = { outer: [{lng, lat}, ...], holes: [[{lng, lat}, ...]] }`，环无需显式闭合，零长度退化边自动跳过。

## 环境要求与运行

需要 Node.js ≥ 22.6（`--experimental-strip-types`）或 ≥ 23.6（原生类型剥离），无需安装任何依赖。

```bash
npm test       # Node.js 内置测试运行器（node:test）
npm run demo   # 星型围栏 + 岛屿镂空演示
```

## 验收样例

五角星凹多边形外环（10 顶点），中央镂空方形岛屿 `(8,8)-(12,8)-(12,11)-(8,11)`，
定义见 `examples/star-fence.ts`。`npm run demo` 输出：

```
=== 星型地理围栏（含岛屿镂空）点判定演示 ===

外环顶点数: 10（五角星凹多边形）, 孔洞: 1（中央方形岛屿）

┌─────────┬───────────────────────────┬─────────────┬────────────┬────────────┬────────────┬────────┬──────┐
│ (index) │ label                     │ point       │ expected   │ evenodd    │ nonzero    │ fenced │ ok   │
├─────────┼───────────────────────────┼─────────────┼────────────┼────────────┼────────────┼────────┼──────┤
│ 0       │ 'top-right arm (solid)'   │ '(13, 14)'  │ 'inside'   │ 'inside'   │ 'inside'   │ true   │ 'OK' │
│ 1       │ 'top-left arm (solid)'    │ '(7, 14)'   │ 'inside'   │ 'inside'   │ 'inside'   │ true   │ 'OK' │
│ 2       │ 'bottom arm (solid)'      │ '(10, 3)'   │ 'inside'   │ 'inside'   │ 'inside'   │ true   │ 'OK' │
│ 3       │ 'right arm (solid)'       │ '(15, 8)'   │ 'inside'   │ 'inside'   │ 'inside'   │ true   │ 'OK' │
│ 4       │ 'left arm (solid)'        │ '(5, 8)'    │ 'inside'   │ 'inside'   │ 'inside'   │ true   │ 'OK' │
│ 5       │ 'core below hole (solid)' │ '(10, 7.5)' │ 'inside'   │ 'inside'   │ 'inside'   │ true   │ 'OK' │
│ 6       │ 'hole center (hollow)'    │ '(10, 9.5)' │ 'outside'  │ 'outside'  │ 'outside'  │ false  │ 'OK' │
│ 7       │ 'hole interior (hollow)'  │ '(9, 10)'   │ 'outside'  │ 'outside'  │ 'outside'  │ false  │ 'OK' │
│ 8       │ 'top notch (concave)'     │ '(10, 16)'  │ 'outside'  │ 'outside'  │ 'outside'  │ false  │ 'OK' │
│ 9       │ 'between arms, left'      │ '(3, 14)'   │ 'outside'  │ 'outside'  │ 'outside'  │ false  │ 'OK' │
│ 10      │ 'between arms, right'     │ '(17, 14)'  │ 'outside'  │ 'outside'  │ 'outside'  │ false  │ 'OK' │
│ 11      │ 'far above'               │ '(10, 20)'  │ 'outside'  │ 'outside'  │ 'outside'  │ false  │ 'OK' │
│ 12      │ 'outer tip vertex'        │ '(10, 0)'   │ 'boundary' │ 'boundary' │ 'boundary' │ true   │ 'OK' │
│ 13      │ 'concave vertex'          │ '(10, 14)'  │ 'boundary' │ 'boundary' │ 'boundary' │ true   │ 'OK' │
│ 14      │ 'edge midpoint'           │ '(11, 3.5)' │ 'boundary' │ 'boundary' │ 'boundary' │ true   │ 'OK' │
│ 15      │ 'horizontal edge'         │ '(15, 7)'   │ 'boundary' │ 'boundary' │ 'boundary' │ true   │ 'OK' │
│ 16      │ 'hole edge'               │ '(10, 8)'   │ 'boundary' │ 'boundary' │ 'boundary' │ true   │ 'OK' │
│ 17      │ 'hole vertex'             │ '(8, 8)'    │ 'boundary' │ 'boundary' │ 'boundary' │ true   │ 'OK' │
└─────────┴───────────────────────────┴─────────────┴────────────┴────────────┴────────────┴────────┴──────┘
全部测试点判定符合拓扑定义。
```

## 测试结果

`npm test`（Node.js 内置 `node:test` 运行器，10 个测试全部通过）：

```
✔ 星型围栏：射线法与风向数法对所有验收点判定一致且符合拓扑定义
✔ 星型围栏：实体部分判定为 inside
✔ 星型围栏：孔洞（岛屿）内部判定为 outside，且不计入围栏
✔ 星型围栏：顶点重合与边上临界点严格判定为 boundary
✔ 边界严格定义：isOnSegment 含端点、排斥共线外延点
✔ 射线恰好穿过顶点高度时不重复计数（半开区间约定）
✔ 凹多边形：星形凹陷区域（两臂之间）判定为 outside
✔ 自相交五角星 {5/2}：evenodd 与 nonzero 规则给出各自正确定义的结果
✔ 自相交蝴蝶结：两瓣内部为 inside，交叉点为 boundary
✔ 退化输入：重复闭合点与零长度边不影响判定
ℹ tests 10  ℹ pass 10  ℹ fail 0
```

## 目录结构

```
src/geofence.ts        # 判定内核：射线法、风向数法、边界判定、带孔洞分类
examples/star-fence.ts # 验收夹具：星型围栏 + 岛屿孔洞 + 标注测试点
examples/demo.ts       # 运行样例
test/geofence.test.ts  # node:test 测试
```
