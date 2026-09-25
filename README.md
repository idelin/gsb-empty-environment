# color-pipeline

TypeScript 实现的 sRGB / 线性 RGB / 带显式白点的 XYZ 转换图。矩阵与传递函数分离表示，支持色域裁剪（`clip`）与保留超域（`preserve`）两种策略，不依赖 Canvas 滤镜或任何图像处理库。

## 运行

需要 Node.js >= 22.6（使用内置 TypeScript 类型剥离，无需编译步骤、无第三方依赖）：

```sh
npm test        # Node 内置测试运行器 (node:test)
npm run example # 可运行样例
```

## 设计

- **传递函数**（`src/transfer.ts`）：`srgb` / `linear` / `gamma(n)`，与矩阵分离表示。负线性分量按符号镜像定义（扩展 sRGB）处理，绝不产生 NaN。
- **矩阵**（`src/matrix.ts`）：3x3 行主序，行列式、求逆（奇异返回 `null`）。
- **白点适配**（`src/adaptation.ts`）：Bradford CAT。白点不同的边禁止省略适配（`AdaptationRequiredError`）。
- **色彩空间**（`src/spaces.ts`）：显式白点（XYZ 三刺激值）+ 传递函数 + 线性 RGB→XYZ 矩阵。
- **转换图编译**（`src/graph.ts`）：`compileGraph` 在编译期检查
  - 奇异矩阵（空间矩阵与自定义矩阵边）→ `SingularMatrixError`
  - 循环/多路径矛盾：枚举所有简单路径，在探针点上数值比对，不一致 → `CycleContradictionError`
  - 可逆性：每条边必须可逆（矩阵可逆、传递函数双射）
  - 理论等价的双路径（如 sRGB→XYZ 直连 vs 经 linear-sRGB）编译期通过且数值一致
  - 产物 `ConversionPlan.version` 为配置内容哈希
- **缓存**（`src/cache.ts`）：`PlanCache` 按版本绑定；热换配置产生新版本，旧版本缓存条目隔离、不被复用。
- **批处理**（`src/pipeline.ts`）：`processBatch` 分块执行（`chunkSize` + `onChunk` 回调），结果先写入私有暂存区，任一像素非法（非有限值、alpha 越界）即整批抛 `BatchError`，绝不发布半批。输出：
  - `clipMask`：每像素位掩码（R=1 G=2 B=4），记录不可逆裁剪
  - `reversible`：任何一次裁剪（含 `priorClipMask` 携带的历史裁剪）都会置假——重复裁剪不会谎称可逆
  - `roundtrip`：正向→逆向往返的最大/平均误差
  - 透明像素（alpha=0）正常转换且 alpha 原样保留
- **验证图**（`src/image.ts`）：自建 4x4 RGBA 图，含黑白基色、灰阶、透明/半透明、超域（1.2 / -0.2）像素。

## 测试

`test/` 下 26 个用例（`node:test` + `assert`）：传递函数往返与负值处理、矩阵求逆与奇异性、图编译三类错误、等价双路径对照、Bradford 适配、黑白基色往返容差、分块一致性、部分批非法原子失败、透明像素、裁剪掩码与不可逆性、重复裁剪、热换配置与旧版本缓存隔离。
