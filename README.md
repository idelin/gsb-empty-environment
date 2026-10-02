# SSI Guardian — 可串行化快照隔离防异常器

在快照隔离（SI/MVCC）之上实现 **Serializable Snapshot Isolation (SSI)**：
通过 SIREAD 锁追踪 **rw-反依赖（rw-antidependency）**，检测写倾斜危险结构
`Tn -rw-> Tp -rw-> Tm`（Tm 最先提交，Tn 可与 Tm 相同），在异常成环前夕
精准中止枢轴事务，保证完全可串行化。理论依据：Fekete et al. 证明了 SI 下
任何串行化异常环都必然包含该危险结构，因此切断它即可保证可串行化。

## 运行要求

Node.js >= 23.6（内置 TypeScript 类型擦除，可直接运行 `.ts`；
Node 22.6+ 可加 `--experimental-strip-types`）。测试使用 Node 内置 `node:test`，零依赖。

```bash
node --test test/ssi.test.ts   # 运行测试
node src/demo.ts               # 运行写倾斜演示（纯SI vs SSI）
```

## 文件

- `src/ssi.ts` — SSI 核心：`Database` / `Transaction` / `SerializationError`
- `src/demo.ts` — 写倾斜演示：纯 SI 双重扣款 vs SSI 精准中止 T2
- `test/ssi.test.ts` — 9 个内置测试（含验收场景）

## 算法要点

1. **MVCC 快照**：每 key 多版本，事务按 begin 时的 commitTs 读快照；写/写冲突
   采用 first-committer-wins。
2. **SIREAD 锁**：`read()` 在 key 上放置 SIREAD 锁，事务提交后锁仍保留。
3. **rw-反依赖边**：
   - 写 key 时，对该 key 的 SIREAD 锁持有者建立 `reader -rw-> writer`；
   - 读 key 时，对快照之外的已提交版本/未提交写的作者建立同样边。
4. **危险结构检测**（两个时机）：
   - 建边时：若形成 `reader -rw-> writer -rw-> X` 且 X 已提交（按 commitSeq
     确认 X 最先提交），中止枢轴 writer（不可行则中止 reader）；
   - 提交时：若形成 `N -rw-> P -rw-> 本事务` 且本事务最先提交，中止枢轴 P。
   - 两事务写倾斜 `T1 -rw-> T2 -rw-> T1` 是 Tn==Tm 的特例，由提交时检测覆盖。

## 验收场景

A=100, B=100，约束 A+B>=0；T1 读 A、B 扣 A 80；T2 并发读 A、B 扣 B 80。

- **纯 SI**（`new Database({ ssi: false })`）：两者基于同一快照各自校验通过，
  双重扣款均生效（非可串行化交错；扣款放大到 150 时 A+B=-100，约束被击穿）。
- **SSI**（默认）：T1 提交时检测到危险结构 `T2 -rw-> T1 -rw-> T2`，
  精准中止 T2；T2 重试后基于新快照串行成功，约束始终成立。
