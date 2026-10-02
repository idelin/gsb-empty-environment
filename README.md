# 可追溯员工组织架构时态树（Bitemporal Org Chart）

支持历史回溯与追溯修订的组织架构树。部门归属与汇报线均为**双时态**关系：

- **业务时间** `[validFrom, validTo)`：事实在真实世界的生效时间段；
- **系统时间** `[sysFrom, sysTo)`：该事实被系统知悉 / 录入的时间段。

所有区间左闭右开，`OPEN`（`Infinity`）表示开放端点。修订与补录永不删除历史：
旧记录在重叠处被封闭（`sysTo = 录入时刻`），未覆盖的前 / 后段自动分裂为新版本，
因此任意「知悉时刻 × 业务时刻」的历史视图都可重建，全程可审计。

## 环境要求

Node.js ≥ 22.18（内置 TypeScript 类型擦除与 `node:test`，无需任何依赖与构建步骤）。

## 运行

```sh
npm test    # Node.js 内置测试运行器
npm run demo # 运行验收场景样例
```

## 使用示例

```ts
import { OrgChart } from "./src/org-chart.ts";

const org = new OrgChart();

// 知悉 1 录入：E 在业务 [1,10) 汇报给 M1
org.assignManager("E", "M1", 1, 10, 1);
// 知悉 5 补录：E 在业务 [3,7) 汇报给 M2（历史调岗，自动分裂重叠时间段）
org.assignManager("E", "M2", 3, 7, 5);

org.managerAt("E", 4, 6);          // "M2" —— 知悉 6 看业务 4
org.managerAt("E", 8, 6);          // "M1" —— 知悉 6 看业务 8
org.managerAt("E", 4, 4);          // "M1" —— 知悉 4 时补录尚未录入
org.reportingChain("E", 4, 6);     // ["E", "M2", ...] 完整汇报链
org.reportingAuditTrail("E");      // 全部历史版本，含已封闭记录
```

部门归属（`assignDepartment` / `departmentAt` / `departmentAuditTrail`）语义相同。

## 结构

- `src/bitemporal.ts` —— 通用双时态关系存储：重叠检测、时间段自动分裂、双时态点查、审计轨迹
- `src/org-chart.ts` —— 组织架构层：汇报线、部门归属、汇报链查询（带环检测）
- `test/org-chart.test.ts` —— `node:test` 测试，覆盖验收边界与分裂 / 回溯语义
- `examples/demo.ts` —— 可运行的验收场景样例
