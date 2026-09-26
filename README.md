# layer-dirty-region

图层合成器 + 最小保守重绘区域（脏区）推导。纯 TypeScript，零依赖：
不调用 Canvas 滤镜或任何图像处理库，混合、裁剪、变换、阴影全部逐像素手写，
用自建 RGBA 小图验证。要求 Node.js >= 22.18（原生运行 TypeScript + 内置 `node:test`）。

## 运行

```sh
npm test    # Node.js 内置测试运行器，19 个用例
npm run demo
```

## 设计

- `src/geometry.ts` — 轴对齐矩形运算（交/并/膨胀/平移）与 平移+缩放 变换复合；
  `transformRect` 对结果向下/向上取整，保证保守覆盖所有触及的设备像素。
- `src/layer.ts` — 图层树：RGBA 像素、变换、透明度、混合模式
  （normal / multiply / additive）、局部裁剪、声明式阴影（半径+偏移）、子图层。
- `src/compositor.ts` — 对每类局部编辑推导根坐标系下的最小保守脏区，并输出重绘原因：
  - `editContent`：编辑矩形 → 世界变换 → 与有效裁剪（含祖先链）求交；
    完全落在裁剪外 → 空脏区 + `outside-clip(...):no-repaint`；
    有阴影时按声明半径膨胀（含偏移）→ `shadow-expanded(radius=N)`。
  - `setTransform`：子树每个节点 旧位置 ∪ 新位置；
    自身记 `moved(...):old+new-bounds`，后代记 `parent-transform-changed(...)`。
  - `setOpacity`：透明度变化传播到整个子树的绘制足迹，
    后代记 `ancestor-opacity-changed(...)`。
  - `setClip`：旧裁剪足迹 ∪ 新裁剪足迹。
  - `takeDirty`：合并并清空累计脏区。
- `src/renderer.ts` — 逐像素渲染器：straight-alpha source-over 合成 +
  逐像素混合模式；最近邻采样；阴影为内容 alpha 在声明半径内的膨胀（max 滤波）。
  传入 `region` 时只重算该区域、区域外像素保持不动——即脏区重绘语义。

## 验证方式（test/）

- 混合数学：normal（含半透明）、multiply、additive、图层透明度，断言精确像素值（±1）。
- 变换/裁剪/阴影：缩放平移采样、裁剪只露出可见部分、阴影恰好覆盖声明半径。
- 脏区推导：移动覆盖旧+新位置、裁剪外修改不重绘、阴影按半径膨胀、
  透明度与父变换变化传播到子树。
- 保守性：局部重绘（仅脏区重算）与全量重渲染逐字节相等（内容编辑与移动各一例）。

## 样例输出（npm run demo）

```
[1] 初始渲染       脏区=(0,0) 24x16  原因=[initial-render]
[2] 局部编辑 sprite (1,1,1,1): 脏区=(3,3) 5x5  原因=[content-changed(sprite), shadow-expanded(radius=2)]
[3] 移动 group → (6,4)   : 脏区=(2,2) 14x10  原因=[moved(group):old+new-bounds, parent-transform-changed(sprite), parent-transform-changed(badge)]
[4] group 透明度 → 0.6 : 脏区=(6,4) 10x8  原因=[opacity-changed(group), ancestor-opacity-changed(sprite), ancestor-opacity-changed(badge)]
[5] sprite 裁剪 → (0,0,3,4): 脏区=(6,4) 10x8  原因=[clip-changed(sprite)]
[6] 裁剪外修改 sprite (4,1): 脏区=空(无需重绘)  原因=[outside-clip(sprite):no-repaint]
```

## 测试结果

```
ℹ tests 19
ℹ pass 19
ℹ fail 0
```
