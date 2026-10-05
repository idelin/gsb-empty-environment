# annotation-intervals

多层叠加批注区间库(TypeScript)。在可变文本上维护任意多层重叠批注,
提供编辑映射、逐位置/范围覆盖查询与分段输出;被编辑整体删除的批注保留
失效记录(tombstone),恢复编辑可将其重新定位。

## 运行

```sh
bun test   # 运行全部单元测试与差分测试
```

## 核心概念

- **位置与区间**:文档是字符串,位置为字符间隙 `0..length`,区间为半开
  `[start, end)`;`start === end` 是附着在间隙上的零宽点批注。
- **左右黏附位(affinity)**:决定边界插入是否扩展批注。
  - `startAffinity: 'left'` 起点黏住左侧字符 → 在起点插入的文本被吞入批注;
    `'right'` → 插入文本留在批注外。
  - `endAffinity: 'right'` 终点黏住右侧字符 → 在终点插入的文本被吞入批注;
    `'left'` → 插入文本留在批注外。
  - 默认 `startAffinity: 'left'`、`endAffinity: 'right'`(两端均扩展)。
  - 零宽点批注作为整体按 `startAffinity` 映射,始终保持零宽。
- **编辑映射**:`applyEdit({ start, deleteCount, text })` 更新文本并把全部
  批注映射通过该编辑;`applyEdits(edits)` 应用一批以编辑前坐标给出、互不
  重叠的编辑;`replaceAll(search, replacement)` 批量替换所有出现。
  - 删除范围与批注部分重叠 → 批注收缩。
  - 删除范围完全覆盖批注且有替换文本 → 批注迁移到替换文本上。
  - 删除范围完全覆盖批注且无替换文本 → 批注变为**失效记录**。
- **失效记录与恢复**:整体被删的批注不消失,而是坍缩为删除点处的
  tombstone(`invalid: true`,并记录 `invalidatedFrom` 原区间)。失效记录
  会跟随后续编辑移动;当某次编辑恰好在其位置插入文本(如撤销/恢复编辑),
  批注自动复活并重新定位到插入的文本上。显式 `remove(id)` 不留记录。
- **查询**(结果均按 ID 排序):
  - `annotationsAt(pos)` 逐位置覆盖查询(含落在该间隙的零宽批注);
  - `annotationsInRange(start, end)` 范围相交查询;
  - `segments()` 按批注边界切分文档,输出每段活跃批注 ID 集合;
  - `tombstones()` 失效记录列表;`annotations()` / `activeAnnotations()`。

## 验证

`test/differential.test.ts` 用独立的逐字符参考模型
(`test/reference-model.ts`,把批注建模为被覆盖字符下标集合)做随机化
差分测试:随机多层批注(含同点零宽)、随机单编辑/批量编辑/批量替换/
增删批注的操作组合,每一步之后逐位置核对覆盖集合、失效记录与分段输出。
