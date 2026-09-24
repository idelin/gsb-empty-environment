# reversible-patch

带上下文校验的可逆文本补丁库（TypeScript，零依赖）。支持顺序组合、相邻互逆操作消除、
补丁间坐标变换与三方重定位；应用前验证基线摘要，失败时不产生部分结果。

## 运行要求

Node.js ≥ 22.6（使用内置 TypeScript 类型擦除与 `node:test`，无需编译、无第三方依赖）。

```sh
npm test    # node --test test/patch.test.ts
npm run demo
```

## 模型

- 坐标均为 **UTF-16 码元**（即 JS 字符串下标）；任何补丁边界不允许切开代理对，
  违反时抛 `SurrogateBoundaryError`。
- 补丁 = 针对基线顺序消费的操作流：
  - `retain(n)` 保留 n 个码元
  - `delete(n, removed, removedId?)` 删除 n 个码元；`removed` 为被删原文（应用时校验上下文），
    `removedId` 记录被删文本来源的 insert 操作身份（组合消除时核对）
  - `insert(text, id)` 插入文本；`id` 为操作身份——**相同文本由不同操作插入不会被误认作同一次编辑**
- 每个补丁携带 `baseDigest`/`baseLength`（SHA-256 + UTF-16 长度）与可选的
  `resultDigest`/`resultLength`。长度相同但内容不同的基线无法通过摘要校验。

## API

| 函数 | 说明 |
| --- | --- |
| `makePatch(base, edited, idGen?)` | 由前后文本构造补丁（公共前后缀 diff，代理对安全） |
| `apply(base, patch)` | 校验摘要与逐 op 上下文后应用；任一失败即抛错，**不产生部分结果** |
| `invert(patch, idGen?)` | 求逆；`apply(apply(b,P),invert(P))` 逐字节恢复 `b` |
| `compose(P, Q)` | 顺序组合，`apply(b, compose(P,Q)) === apply(apply(b,P),Q)`；相邻互逆操作消除 |
| `transform(P, Q, base)` | 坐标变换：P 重定位到并发编辑 Q 之后；交叉删除抛 `ConflictError` |
| `relocate(mine, theirs, base)` | 三方重定位（`transform` 的语义化别名） |
| `validateCommutation(P, Q, base)` | 验证交换图：两条应用顺序结果一致，否则抛 `CommutationError` |

## 语义要点

- **失败原子性**：`apply` 先验证基线摘要，再逐 op 校验 delete 上下文与代理对边界，
  全部通过后才返回新字符串；任何失败抛错，调用方原文不受影响。
- **组合消除**：`insert` 后紧跟删除同一插入文本的 `delete`（身份一致）相互消除；
  `delete` 后紧跟同文 `insert` 归约为 `retain`（坐标来源回到基线）。
  因此 `compose(P, invert(P))` 归约为纯 `retain` 的恒等补丁，后续操作坐标保持有效。
- **操作身份**：并发同位置 insert 按 `id` 字典序定序；组合时 `delete.removedId`
  与 insert 的 `id` 不符则拒绝消除（`ComposeError`）。
- **交换图**：不相交编辑满足 `apply(apply(b,P),transform(Q,P,b)) === apply(apply(b,Q),transform(P,Q,b))`；
  交叉删除返回 `ConflictError`。
- LF 与 CRLF 文本均被覆盖（换行本身只是普通码元）。

## 测试结果

```
✔ apply 与逐步参考一致：基本增删改（LF 与 CRLF）
✔ UTF-16 偏移：代理对占两个码元，且不得切开代理对
✔ makePatch 的公共前后缀不会切开代理对
✔ 摘要校验：长度相同但内容不同的基线不能通过
✔ 失败原子性：任一 op 校验失败即抛错，不产生部分结果
✔ P 再逆 P 恢复逐字节原文（含 CRLF 与 emoji）
✔ 组合等于顺序应用；compose(P, invert(P)) 为恒等
✔ 组合器消除互逆操作时保留后续操作的坐标来源
✔ 组合时身份不符的 delete 不能消除 insert
✔ 操作身份：同文本的并发插入不会被误认作同一次编辑
✔ 不相交编辑满足交换图；坐标随并发插入重定位
✔ 交叉删除返回冲突；不相交删除可交换
✔ 重复文本与换行变更：组合/求逆/原子性对照逐步参考
✔ 结果摘要自检：篡改结果摘要的补丁被拒绝
ℹ tests 14  ℹ pass 14  ℹ fail 0
```

## 运行样例（`npm run demo`）

```
base: "hello\r\nbrave 😀 world\r\n"
patch ops: [{"kind":"retain","count":7},{"kind":"delete","count":14,"removed":"brave 😀 world"},{"kind":"insert","text":"BRAVE 😀 new world!","id":"demo-1"},{"kind":"retain","count":2}]
applied: "hello\r\nBRAVE 😀 new world!\r\n"
inverted restores base (byte-exact): true
compose(P,Q) == apply(apply(P),Q): true
compose(P, invert(P)) ops (全为 retain): [{"kind":"retain","count":23}]
commutes: true
overlapping deletes: "ConflictError"
same-length different base: "DigestMismatchError"
```
