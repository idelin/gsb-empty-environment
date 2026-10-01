# Slotted-Page：紧凑可变长字节槽位页管理器

TypeScript 实现的类数据库 Slotted-Page 内存页存储，页大小 4KB，零依赖，
使用 Node.js 内置测试框架验证（Node ≥ 23.6 原生运行 `.ts`）。

## 页内布局

```
0                     freeStart           freeEnd                4096
┌─────────┬────────────┬~~~~~~~~~~~~~~~~~~~┬──────────────────────┐
│ 页头 10B │ 槽位数组 →→ │   连续空闲空间     │ ←← 记录数据（页尾对齐）│
└─────────┴────────────┴~~~~~~~~~~~~~~~~~~~┴──────────────────────┘
```

- **页头（10B）**：magic / slotCount / freeStart / freeEnd / tombstoneCount
- **槽位数组**：从页头之后向前增长，每项 5B：`offset(u16) length(u16) flags(u8)`
- **记录数据**：可变长字节，从页尾向页前填充
- **删除**：仅打 tombstone 标记，产生页内碎片，空间不立即回收
- **compact()**：存活记录按物理顺序重新向页尾对齐、重写槽位偏移，
  空闲空间收缩为槽位数组与数据区之间的一段连续区间，利用率最大化
- **插入**：优先复用 tombstone 槽位；连续空间不足但碎片足够时自动触发 compact

## API

```ts
import { SlottedPage } from "./src/slotted-page.ts";

const page = new SlottedPage();          // 4KB 页
const id = page.insert("任意长度文本");   // → slotId
page.getString(id);                      // 读回
page.delete(id);                         // 标记删除（产生碎片）
page.compact();                          // 碎片整理：数据向页尾对齐
page.stats();                            // 空间统计
```

## 运行

```bash
npm test    # node --test test/   （12 个用例）
npm run demo # 演示：写入 → 删除 → 碎片 → compact → 空间回收
```

## 测试覆盖

- 空页初始化、长短不一记录（含空串、中文、1500B 长记录）写入与读回
- 槽位前向增长 / 数据页尾对齐的物理布局断言
- 删除中间记录产生碎片，连续空间不变、碎片可统计
- compact 后：碎片归零、连续空间吸收被删空间、存活记录内容不变且页尾对齐
- 插入时连续空间不足自动 compact；tombstone 槽位复用
- 页满 `PageFullError`、超大记录 `RecordTooLargeError`
- 页字节快照序列化 / 重新加载
- 40 轮随机长短记录插入/删除/整理的压力与空间不变式校验
