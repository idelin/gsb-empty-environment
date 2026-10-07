# frozen-replace

TypeScript 替换库：先在旧文本上冻结全部非重叠匹配，再同时应用；支持位置映射与多文档事务。

## 核心语义

- **冻结匹配，同时应用**：`replaceAll` 先在旧文本上从左到右确定全部非重叠匹配，再一次性拼接出新文本。替换文本中产生的新匹配在本轮不会被处理。
- **零宽匹配每边界最多一次**：零宽命中后扫描位置前进一个码点（unicode 模式下按码点前进），同一边界不会重复命中。与原生 `String.prototype.replace`（全局）语义对齐，测试中以原生行为作为 oracle 交叉验证。
- **正则全局化**：无论 `RegExp` 是否带 `g` 标志都按全局处理；字符串模式按字面量全局匹配。

## 替换模板语法

- 捕获引用：`$1`–`$99`（两位优先，超出组数退化为一位 + 字面数字）、`$<name>` 命名组；未参与的组展开为空串，不存在的组引用按字面量保留。
- 特殊序列：`$$` → `$`，`$&` → 整个匹配，`` $` `` / `$'` → 匹配前/后的**旧文本**（因为全部匹配在冻结的旧文本上确定、同时应用）。
- 反斜杠转义：`\n` `\t` `\r` `\\` `\$`；未知转义保留反斜杠原样。
- 也支持函数替换：`(matchText, groups, offset, text) => string`。

## 位置映射

`ReplaceResult.mapPosition(pos, assoc)` 把旧偏移映射到新偏移：

- 保留区内的位置线性映射；
- 替换区域内部的位置按 `assoc` 落在替换文本起点（`'start'`）或终点（`'end'`）；
- 零宽编辑所在边界同理：`'start'` 在插入文本之前，`'end'` 在其后；
- `mapRange(start, end)` 等价于 `[mapPosition(start,'start'), mapPosition(end,'end')]`。

提示：要提取**保留文本**对应的新区间，用 `mapPosition(a,'end')` 到 `mapPosition(b,'start')`（跳过边界上的零宽插入）。

`Edit` 记录每个替换的新旧区间与文本，`applyForward`/`applyInverse` 提供朴素拼接模型的正向/逆向应用，测试用它核对前后引用、空替换、相邻匹配与映射一致性。

## 多文档事务

```ts
const store = new DocumentStore();
store.register('a.txt', 'foo bar');
const v = store.get('a.txt').version;
const res = commitTransaction(store, [
  { docId: 'a.txt', expectedVersion: v, pattern: /foo/g, replacement: 'baz' },
]);
if (res.ok) {
  // res.applied[*]: 可逆编辑 edits、新旧版本、result（含范围映射）
  res.undo(); // 恢复文本与版本；若提交后被外部修改则抛错
} else {
  // res.conflicts: 任一文档版本漂移则整批拒绝，无任何文档被修改
}
```

- 所有操作针对同一冻结版本校验；任一文档版本变化（或文档缺失、同批重复文档）→ 整批拒绝。
- 成功时先纯计算再一次性应用全部文档，返回可逆编辑、范围映射与 `undo()`。

## 构建与测试

环境无网络、无 tsc，故 `scripts/build.mjs` 用受控子集的类型剥离器把 `src/*.ts` 转为 `dist/*.js`（子集约束见脚本头注释）。有 tsc 时可直接用 `tsconfig.json` 编译 `src/`。

```sh
npm test   # 构建 + 运行 test/ 下全部测试
```
