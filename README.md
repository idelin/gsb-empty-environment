# 可追溯员工组织架构时态树

双时态（bitemporal）组织架构：部门归属与汇报线均携带 **业务时间**（生效区间）与
**系统时间**（知悉/录入区间），支持历史任意时刻回溯与补录修订。

## 能力

- 汇报线、部门归属均为双时态记录：`业务[validFrom, validTo) × 系统[sysFrom, sysTo)`（半开区间）。
- 点查询：`managerAt / departmentAt(员工, 业务时刻, 知悉时刻)` 还原“当时系统认知下的当时事实”。
- 汇报链：`reportingChainAt` 沿汇报线逐级还原完整链路（含环检测）。
- 补录修订：录入与既有“当前认知”重叠的区间时，自动关闭旧记录并分裂出不重叠的残余段，
  历史记录完整保留于审计轨迹（`managerHistory / departmentHistory`）。

## 运行

需 Node.js ≥ 23.6（原生运行 TypeScript；22.6+ 可加 `--experimental-strip-types`）。

```bash
npm test    # Node 内置测试（node:test）
npm run demo  # 运行样例：复现验收场景
```

## 验收场景

知悉1 录入 E 业务[1,10) 汇报给 M1；知悉5 补录 E 业务[3,7) 汇报给 M2。则：

| 查询 | 结果 |
| --- | --- |
| 知悉6 查业务4 | M2 |
| 知悉6 查业务8 | M1 |
| 知悉4 查业务4 | M1 |

补录后审计轨迹：原记录 `M1 [1,10)` 在系统时刻5被关闭，分裂出 `M1 [1,3)`、`M1 [7,10)`，
并新增 `M2 [3,7)`，三者自系统时刻5起生效。

## 结构

- `src/bitemporal.ts` — 通用双时态表：插入、自动分裂、点查询、时间线、审计轨迹
- `src/org-chart.ts` — 组织架构：汇报线/部门归属录入与查询、汇报链还原
- `test/org-chart.test.ts` — 验收与边界测试（node:test）
- `examples/demo.ts` — 运行样例
