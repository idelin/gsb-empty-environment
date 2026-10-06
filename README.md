# doc-format-core

文档节点（文本 + 无序格式集合 + 可选锚点）的规范化与范围格式操作 TypeScript 库。

## 模型

```ts
interface Doc { nodes: DocNode[] }
interface DocNode {
  id: string;
  text: string;
  marks: Mark[];        // 无序集合，按 (type, attrs) 去重
  anchor?: Anchor;      // 锚点，带左右亲和性
}
interface Mark { type: MarkType; attrs?: Record<string, string> }
interface Anchor { id: string; affinity: 'left' | 'right' }
```

- 格式集合**无序**：`sameMarks` 按集合比较，`[bold, italic]` 与 `[italic, bold]` 等价。
- **链接不可嵌套**：同一节点至多一个 `link` 标记，规范化保留首个；`addMark` 添加链接时替换已有链接。

## 规范化 `normalize(doc) → { doc, map }`

- 合并相邻且格式集合相同的文本节点（合并后保留最左节点 id）；
- 移除无锚点的空文本节点；**带锚点的空节点必须保留**；
- 带锚点的节点是合并屏障，保证锚点位置语义不变；
- `map[i]` 给出原节点 `i` 在新文档中的范围 `[start, end)`，被移除的节点为 `null`；
- **幂等**：对结果再次执行不改变文档（测试含恒等映射核对）。

等价性核对：`flatten(doc)` 把文档渲染为扁平的「字符 + 格式键序列」，
`equivalent(a, b)` 与测试中的独立参考模型均以此判定等价。

## 格式操作

```ts
addMark(doc, start, end, mark)      // 按字符范围添加标记
removeMark(doc, start, end, type)   // 按字符范围移除某类标记
toggleMark(doc, start, end, mark)   // 全覆盖则移除，否则添加
applyEdits(doc, edits)              // 整批编辑，全部基于原坐标
splitAt(doc, pos)                   // 在字符偏移处拆分节点
```

- 范围跨节点时先 `splitAt` 拆分，应用后再 `normalize` 合并；
- 拆分时锚点按亲和性保留：`left` 留在左片，`right` 移到右片；
- `applyEdits` 的编辑坐标全部相对于**原文档**（格式编辑不改变文本长度，
  内部按起点降序应用，保证原坐标始终有效）；
- 每个操作返回 `{ doc, undo }`；`undo()` 返回原文档对象本身，
  因此**节点身份与锚点被完全恢复**（测试断言引用相等）；
- 空范围是无操作（返回原对象）；非法范围抛 `RangeError`；
- 所有操作不可变，不修改输入文档。

## 验证

测试用独立的扁平字符-标记参考模型交叉核对：

- 规范化：渲染等价、幂等、映射分组文本一致、锚点保留；
- 格式操作：随机化核对（200 组随机文档 × 随机编辑批次）——
  扁平模型等价、锚点多重集不变、空锚点节点保留、链接不嵌套、结果为规范化不动点；
- 可逆性：`undo()` 与原对象引用相等，节点身份逐一相同。

```sh
npm test    # 构建（类型擦除）+ 运行全部测试
npm run build
```

说明：源码为 `src/*.ts`（TypeScript）。本环境无 tsc/网络，
`scripts/build.mjs` 对受控 TS 子集做类型擦除生成 `dist/`；
有 tsc 的环境中可直接用 `tsconfig.json` 类型检查。
