# layer-dirty-region

图层合成器 + 最小保守脏区（dirty region）推导。纯 TypeScript，零依赖：
不使用 Canvas 滤镜或任何图像处理库，RGBA 位图、仿射变换、混合模式、
阴影衰减全部手写实现。测试使用 Node.js 内置测试运行器（`node:test`）。

## 运行

需要 Node.js >= 23.6（原生 TypeScript 类型擦除）：

```sh
nvm use 23        # 或任何 >= 23.6 的 node
npm test          # node --test tests/*.test.ts
npm run demo      # node examples/demo.ts
```

## 设计

- `src/math.ts` — 2D 仿射矩阵（Canvas 约定）与矩形运算；`transformRect`
  取四角变换后的轴对齐包围盒（保守），`roundOut` 向外取整到设备像素。
- `src/bitmap.ts` — 非预乘 RGBA8 位图；`blendPixel` 实现
  `source-over` / `multiply` / `additive`，并支持图层不透明度。
- `src/layer.ts` — 图层树：变换、不透明度、混合模式、本地裁剪矩形
  （作用于整个子树）、效果（声明半径的阴影）。提供世界变换、
  世界不透明度（沿父链累乘）、设备空间有效裁剪（逐祖先求交）、
  含效果扩展的内容包围盒。
- `src/dirty.ts` — `DirtyTracker`：
  - `markLocalEdit`：局部编辑矩形 → 世界变换 → 按声明阴影半径
    （含偏移）扩展 → 与全部祖先裁剪求交；**裁剪外的修改不产生重绘**。
  - `setTransform`：先快照旧包围盒再应用新变换，**脏区 = 旧位置 ∪ 新位置**。
  - `setOpacity` / `setClip` / `setEffects`：同样取变更前后包围盒的并集；
    不透明度变化沿父链**传播**，条目记录 `propagationPath`。
  - 每条脏区条目携带 `reason`（`local-edit` / `transform-changed` /
    `opacity-changed` / `clip-changed` / `effect-changed`）、
    所属图层、传播路径与可读描述。
- `src/compositor.ts` — 手写光栅化：对设备像素逆变换回图层本地坐标采样；
  阴影按声明半径线性衰减。传入 `dirty` 时先清空脏区再裁剪重绘所有图层，
  保证增量重绘与全量重绘逐像素一致。

## 验证策略（tests/）

- 脏区推导：平移/缩放/旋转映射、裁剪外交互为零、裁剪内求交、
  移动覆盖旧+新位置、阴影按声明半径（含偏移）扩展、不透明度传播路径。
- 合成正确性：对自建 RGBA 小图，比较「增量重绘脏区」与「全量重绘」
  逐像素相等，并断言脏区外像素完全不变（保守且最小）。
- 混合数学：`source-over` / `multiply` / `additive` 与父不透明度累乘的
  精确数值断言。

## 测试结果

```
ℹ tests 20
ℹ pass 20
ℹ fail 0
```
