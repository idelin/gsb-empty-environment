# color-graph

TypeScript 实现的 sRGB / 线性 RGB / XYZ(显式白点)颜色转换图。零运行时依赖,
不调用 Canvas 滤镜或任何图像处理库;测试使用 Node.js 内置 `node:test`。

## 运行要求

- Node.js >= 23.6(原生 TypeScript 类型擦除);或 Node >= 22.6 时加 `--experimental-strip-types`。
- 可选:安装 TypeScript 后 `npm run typecheck` 做完整类型检查(仓库附 `tsconfig.json`)。

```sh
npm test    # node --test "test/*.test.ts"
npm run demo
```

## 设计

- **矩阵与传递函数分离**:色彩空间 = (基色 + 白点) 推导的线性矩阵 + 独立的传递函数
  (`src/spaces.ts`, `src/transfer.ts`)。传递函数按保号定义扩展到负值
  (`f(-x) = -f(x)`),负线性分量不会产生 NaN。
- **转换图编译期校验**(`src/graph.ts` `compile()`):
  奇异/不可逆矩阵拒绝(行列式检查 + `M * M^-1 ≈ I`);显式边构成的环路必须复合为
  单位阵,否则抛 `CycleContradictionError`;显式边必须与 XYZ 枢纽推导路径一致
  (理论等价路径对照),否则抛 `PathContradictionError`。
- **白点适配不可省略**:源/目标白点不同(如 D65 → D50)时,管线步骤中必然包含
  Bradford `adapt` 步骤(`src/adapt.ts`, `src/pipeline.ts`)。
- **色域策略**:`clip`(钳制到 [0,1],不可逆)与 `preserve`(保留超域值,可往返)。
  批处理输出不可逆裁剪掩码与往返误差;`priorMask` 让重复裁剪的不可逆性累积,
  不会谎称可逆。
- **分批与缓存**:`processBatch` 按块执行,任一块失败抛 `BatchError`,不发布半批结果;
  `PipelineCache` 以 (图指纹 + 配置) 的内容哈希为版本键,热换配置即换版本,
  旧版本缓存完全隔离。
- **alpha 通道**原样透传;全透明像素不会产生 NaN。

## 内置空间

`srgb` (D65)、`srgb-linear`、`display-p3` (D65)、`prophoto` (D50, gamma 1.8)、`xyz`(枢纽)。

## 测试结果

```
ℹ tests 29
ℹ pass 29
ℹ fail 0
```

覆盖:黑/白/基色往返容差、负线性分量、等价路径对照、白点适配存在性、
重复裁剪不可逆、热换配置、部分批非法全失败、透明像素、旧版本缓存隔离、
奇异矩阵与环路矛盾编译期拒绝。
