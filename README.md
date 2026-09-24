# selection-mapping

多选择区在文档版本间的双向映射（TypeScript，零依赖，Node.js 内置测试）。

## 设计

- **逐字符身份模型**（`src/model.ts`）：每个版本是带稳定身份 `CharId` 的码点序列；
  编辑产生新版本 + 结构化映射，绝不只保存最终偏移。
- **结构化映射**（`src/mapping.ts`）：`forward`/`backward` 索引关系，支持
  `composeMappings`（组合）与 `invertMapping`（反向）；`mapBoundary` 按
  粘附性（`left`/`right`）映射零宽位置，删除处向删除点坍缩。
- **选择区**（`src/selection.ts`）：锚端与活动端各自独立保存（端点 + 粘附性），
  方向是派生属性——反向选择经前方插入后方向不变。删除把多个选择区压到同一
  范围时合并：保留主选区身份，`mergedFrom` 记录全部来源（ID、方向、零宽粘附）。
  逆向映射：有来源记录时确定性恢复（撤销）；否则对丢失信息返回歧义及歧义来源。
- **历史**（`src/history.ts`）：版本链 + 边映射；`mappingBetween` 组合/反向任意
  存活版本对；`compress()` 回收未 pin 的中间版本并组合边，存活版本查询不变；
  `undo`/`redo` 沿链移动，撤销后新编辑截断未 pin 的重做尾部。
- **UTF-16 公开偏移**：`utf16ToBoundary` / `boundaryToUtf16` 保证代理对不被切开
  （对中偏移向下吸附）；LF 与 CRLF 均被覆盖（`\r`、`\n` 各有独立身份）。

## 运行

```sh
npm test     # node --test（Node >= 23.6，原生类型擦除）
npm run sample
```

## 验证

`test/fuzz.test.ts` 用确定性随机编辑（含 emoji、LF/CRLF、合并选择区、撤销分支、
历史回收）把每条组合/反向映射与逐字符身份真值比对，并校验选择区不变量。
