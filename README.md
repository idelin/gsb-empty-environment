# modresolve

多文件模块符号解析库（TypeScript）。模块仅支持三种构造：命名导出
（本地定义）、显式命名导入、命名再导出。

## 模型

```ts
interface ModuleSpec {
  definitions: string[];    // export const x —— 本地定义并导出
  imports: ImportEdge[];    // import { imported as local } from 'from'
  reexports: ReexportEdge[];// export { imported as exported } from 'from'
}
```

## 解析语义（`Engine.resolve(module, name, version?)`）

- `defined`：解析到唯一定义点，附带最短来源链（等长时取字典序最小，
  结果与 spec 字段顺序无关）。本地定义遮蔽同名的导入/再导出。
- `conflict`：同名符号可达多个不同定义点，返回两条最短来源链。
- `undefined`：无定义可达。循环导入允许存在，解析自然终止为此结果。
- `cyclic-reexport`：可达范围内存在纯再导出边构成的环且没有任何定义，
  拒绝并返回该环。经过导入边的环不算。

## 增量与快照

- `update(module, spec | null)` 替换/删除单个模块，产生新版本。
- 每个版本的解析结果按 `(module, name)` 缓存，并记录解析过程中实际
  观察到的模块集合；更新模块 M 时仅失效依赖集含 M 的条目，受影响符号
  在下次查询时重算。
- 旧版本的模块表（结构共享）与缓存全部保留，`resolve(m, n, oldVersion)`
  可随时查询旧快照。

## 验证（`src/difftest.ts`）

参考实现 `src/reference.ts` 与引擎不共享遍历代码：对每个引用点从零
穷举所有简单路径。差分测试覆盖：

- 定向场景：菱形依赖收敛、as 重命名链、删除最终定义/定义模块、
  不同长度的两条最短冲突链、纯再导出环拒绝、循环导入放行、
  定义遮蔽破环、旧快照一致性；
- 随机模糊（3 个种子 × 400 次单模块修改）：每步全量比对当前版本，
  并周期性及最终回放全部历史版本与参考实现逐符号比对。

## 运行

需要 Node ≥ 22.6（直接擦除类型运行 TS；23+ 无需任何标志）：

```sh
node src/difftest.ts
```
