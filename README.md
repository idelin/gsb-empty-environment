# mask-compositor

零依赖的 TypeScript 覆盖率遮罩合成器：组合矩形、路径与反向遮罩，支持布尔并/交/差、
软边覆盖率与嵌套坐标变换，并与逐像素暴力参考实现比对验证。不使用 Canvas 滤镜或任何
现成图像处理库，验证图像是自建的 RGBA 小图（`Uint8ClampedArray`）。

## 运行要求

- Node.js ≥ 22.6（22.x 通过 `--experimental-strip-types` 原生执行 TypeScript，
  23+ 开箱即用；`npm` 脚本会自动判断是否需要该标志）
- 无任何第三方依赖

```sh
npm test    # Node 内置测试运行器（node:test）
npm run demo
```

## 数学定义

覆盖率 `c ∈ [0,1]` 表示像素被形状覆盖的比例（软边由超采样产生）。所有运算结果
都被限制在 `[0,1]` 内：

| 运算 | 定义 |
| --- | --- |
| 并 union | `1 - (1-a)(1-b)` |
| 交 intersect | `a * b` |
| 差 subtract | `a * (1-b)` |
| 反向 invert | 指定范围内 `1 - a`，范围外恒为 `0` |

- **反向遮罩只在指定画布范围补集**：`invert(mask, region?)` 默认范围为整个画布，
  区域之外结果恒为 0，补集不会泄漏到画布/区域之外。
- **多重遮罩重复应用按数学定义**：`applyMask` 每次执行 `alpha *= coverage`，
  重复应用即重复相乘（200 → 100 → 50 → 25），不做幂等钳制。
- **嵌套变换**：节点变换为「局部 → 父级」的仿射矩阵，渲染时按 `父 * 子` 复合，
  采样点经逆变换映回形状局部坐标判定。

## 验证方法

快速路径（`src/scene.ts`）先光栅化各形状为覆盖率，再按上表做覆盖率代数；
参考实现（`src/reference.ts`）是独立的暴力路径：对每个子采样点直接求值场景的
布尔表达式（点 ∈ 形状 / 区域判定），不做任何覆盖率运算。测试以更高采样率
（48×48 子像素）的参考结果逐像素比对快速路径（12×12）：

- 参考值 ≈ 0 的像素：快速路径必须 ≤ 0.1
- 参考值 ≈ 1 的像素：快速路径必须 ≥ 0.9
- 边缘像素：`|diff| ≤ 0.15`（约为 1/快速采样率）
- 所有覆盖率必须落在 `[0,1]`

## 运行样例（`npm run demo`）

```text
canvas 32x22, fast ss=8, reference ss=32

per-pixel vs reference: max |diff| = 0.1143
pixels beyond tolerance 0.15: 0
coverage values outside [0,1]: 0

coverage mask (ascii):
                               
               ==:             
    @#+:       =@@@%*=:        
   :@@@@@#+:   -@@@@@@@=       
   +@@@%-@@@@+ .@@@@@@%        
   #@@=  =@@@+  @@%#+=         
   @@-    %@@:                 
  :@@%    -@@                  
  +@@@=  =@@#                  
  +@@@@-%@@@+                  
    :+#@@@@@:           @@@@@@@@
        :+#@            @@@@@@@@
           -            @@    @@
           @+           @@    @@
        :*#@@*          @@    @@
      :%@@@@@@#         @@    @@
      .@@@@@@#-         @@@@@@@@
       #*:+=.           @@@@@@@@

wrote artifacts/demo-mask.pgm and artifacts/demo.ppm

DEMO OK: fast path matches per-pixel reference
```

演示场景包含：旋转矩形减菱形（差）、三角形与矩形相交（交）、限制在子区域内的
反向遮罩（补集成边框）、三层嵌套变换（translate ∘ rotate ∘ scale）下的矩形并
三角形，最后把遮罩绘制到自建 RGBA 图上并重复应用遮罩打洞。

## 测试结果（`npm test`）

Node v23.9.0 与 v22.14.0 均通过：

```text
ℹ tests 28
ℹ pass 28
ℹ fail 0
```

覆盖内容：

- `test/matrix.test.ts` — 仿射组合顺序、嵌套复合、矩阵求逆
- `test/mask.test.ts` — 整数对齐精确 0/1、软边覆盖率、布尔代数定义、[0,1] 界限
  随机性质测试、De Morgan 恒等式、区域限制反向遮罩、evenodd 路径、面积守恒
- `test/image.test.ts` — 逐字节 paint 结果、source-over 合成、重复应用遮罩的
  乘法语义（非幂等）、零覆盖率只清 alpha
- `test/scene.test.ts` — 嵌套变换精确落点、并/交/差/反向/混合场景与逐像素
  参考实现比对

## 目录结构

```text
src/matrix.ts     2D 仿射矩阵（组合、求逆）
src/geometry.ts   矩形/路径形状与点包含判定（nonzero / evenodd）
src/mask.ts       覆盖率遮罩、超采样光栅化、布尔并交差、区域限制反向
src/scene.ts      场景图：shape / group(op) / invert 节点，嵌套变换
src/reference.ts  独立暴力参考实现（逐子采样点求值布尔表达式）
src/image.ts      自建 RGBA 图、paint（source-over）、applyMask、PGM/PPM 导出
test/             node:test 测试（含逐像素参考比对）
examples/demo.ts  运行样例
```
