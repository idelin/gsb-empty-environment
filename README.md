# 带权重地理围栏（Geo-Fence）多边形包含判定器

基于 **射线法（Ray Casting）** 与 **风向数法（Winding Number）** 双内核的
TypeScript 地理围栏判定组件，支持凹多边形、带孔洞多边形与自相交退化多边形，
并对边界临界点与顶点重合给出严格拓扑定义。

零运行时依赖；利用 Node.js ≥ 22.6 内置的 TypeScript 类型擦除直接运行 `.ts`，
测试使用 Node 内置测试框架（`node:test`），无需任何编译与安装步骤。

## 运行

```bash
npm test     # Node 内置测试（11 项）
npm run demo # 星型围栏 + 岛屿孔洞的运行样例
```

## 拓扑定义（严格）

- 坐标为平面 `[lng, lat]` 二元组；环为顶点序列，首尾自动闭合。
- **`boundary`**：点精确落在任一环的某条闭边线段上（含与顶点重合）。
  默认 `eps = 0`：叉积严格为 0 且落在边的包围盒内；`eps > 0` 可放宽。
- **`inside`**：严格内部（不含边界），按所选规则判定。
- **`outside`**：其余情形。孔洞内部对围栏为 `outside`，孔洞边属于围栏边界。
- **自相交多边形**：`even-odd`（射线法）与 `non-zero`（风向数法）均可判定；
  在环绕数 ≥ 2 的区域（如五角星 {5/2} 中心，环绕数为 2）两规则给出不同结果，
  均属定义的合法输出。
- 射线恰好穿过顶点时采用**半开区间规则**，保证穿越计数不重不漏。

## API 概览（`src/geofence.ts`）

| 函数 | 说明 |
| --- | --- |
| `locatePointInRing(ring, p, opts)` | 单环三点判定（boundary 优先） |
| `locatePointInFence(fence, p, opts)` | 外环 + 孔洞的围栏三点判定 |
| `rayCastingInside(ring, p)` | 射线法内核（even-odd） |
| `windingNumber(ring, p)` | 风向数内核（返回环绕次数） |
| `evaluateFences(fences, p)` | 多围栏命中，按权重降序 |
| `resolveDominantFence(fences, p)` | 权重最高的主导围栏 |

`opts`: `{ rule?: 'even-odd' | 'non-zero', eps?: number }`。

`src/presets.ts` 提供 `buildStarFence(center, opts)`：构造星型外环 +
居中方形岛屿孔洞的围栏（校验岛屿完整落于实体区域）。

## 验收场景（examples/demo.ts 输出）

星型围栏：外半径 0.05°、内半径 0.02°、中心镂空 0.008° 半边长方形岛屿，
中心 `[116.397, 39.908]`；另叠加一个权重 1 的矩形围栏。

```
场景                          射线法    风向数法  围栏判定  主导围栏(权重)
星尖实体（正上方）            inside    inside    inside    star-fence (w=10, inside)
星臂间凹口（54° 方向）        outside   outside   outside   city-overlay (w=1, inside)
岛屿孔洞中心                  inside    inside    outside   city-overlay (w=1, inside)
孔洞底边中点（边界）          inside    inside    boundary  star-fence (w=10, boundary)
孔洞顶点（边界）              inside    inside    boundary  star-fence (w=10, boundary)
星尖顶点（边界）              boundary  boundary  boundary  star-fence (w=10, boundary)
孔洞边上方 1e-9（属岛屿）     inside    inside    outside   city-overlay (w=1, inside)
孔洞边下方 1e-9（属实体）     inside    inside    inside    star-fence (w=10, inside)
射线穿顶点临界点              inside    inside    inside    star-fence (w=10, inside)
围栏之外                      outside   outside   outside   -
```

## 测试结果（npm test，11 项全部通过）

```
✔ 星型凹多边形：星尖实体部分判定为 inside（两种内核一致）
✔ 星型凹多边形：凹口（notch）区域判定为 outside
✔ 带孔洞：岛屿内部对围栏为 outside，对孔洞环为 inside
✔ 边界严格定义：孔洞边中点、孔洞顶点、外环顶点均为 boundary
✔ 边界临界点：eps=0 时 1e-9 级偏移即改变拓扑归属
✔ 射线恰好穿过顶点：半开区间规则保证不重不漏
✔ 简单凹多边形不变量：even-odd 与 non-zero 全网格一致（6561 点）
✔ 自相交退化多边形（蝴蝶结）：两内核均支持且结果一致
✔ 自相交五角星 {5/2}：中心环绕数为 2，两规则给出定义的合法分歧
✔ 带权重围栏集：命中按权重降序，主导围栏可取
✔ pointOnSegment：顶点重合与端点语义
ℹ pass 11  ℹ fail 0
```

## 浮点注意事项

边界判定默认 `eps = 0`，即严格精确。构造"恰好落在边上"的测试点时，
应从围栏几何顶点派生（如边中点取两端点均值），而非手写十进制字面量——
例如 `39.908 - 0.008` 在二进制浮点下不严格等于 `39.9`。
