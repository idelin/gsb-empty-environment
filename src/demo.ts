import { planPicking } from './solver.ts';
import { formatPlan } from './format.ts';
import type { PickItem, WarehouseGraph } from './types.ts';

// 小型通道图：出库口 D，主通道 M1-M2-M3，两条巷道 T（上排）与 B（下排）
//
//        T1 -- T2 -- T3
//        |     |     |
//   D -- M1 -- M2 -- M3
//        |     |     |
//        B1 -- B2 -- B3
const graph: WarehouseGraph = {
  nodes: ['D', 'M1', 'M2', 'M3', 'T1', 'T2', 'T3', 'B1', 'B2', 'B3'],
  edges: [
    { from: 'D', to: 'M1', dist: 1 },
    { from: 'M1', to: 'M2', dist: 2 },
    { from: 'M2', to: 'M3', dist: 2 },
    { from: 'T1', to: 'T2', dist: 2 },
    { from: 'T2', to: 'T3', dist: 2 },
    { from: 'B1', to: 'B2', dist: 2 },
    { from: 'B2', to: 'B3', dist: 2 },
    { from: 'M1', to: 'T1', dist: 1 },
    { from: 'M2', to: 'T2', dist: 1 },
    { from: 'M3', to: 'T3', dist: 1 },
    { from: 'M1', to: 'B1', dist: 1 },
    { from: 'M2', to: 'B2', dist: 1 },
    { from: 'M3', to: 'B3', dist: 1 },
  ],
  depot: 'D',
};

// 4 个订单共 12 个拣货项；O1 与 O2 共用货位 T2，O3 与 O4 共用货位 T3
const items: PickItem[] = [
  { id: 'i01', orderId: 'O1', location: 'T2', qty: 3 },
  { id: 'i02', orderId: 'O1', location: 'B3', qty: 2 },
  { id: 'i03', orderId: 'O2', location: 'T2', qty: 1 },
  { id: 'i04', orderId: 'O2', location: 'B1', qty: 4 },
  { id: 'i05', orderId: 'O3', location: 'T3', qty: 2 },
  { id: 'i06', orderId: 'O3', location: 'B2', qty: 2 },
  { id: 'i07', orderId: 'O3', location: 'B3', qty: 1 },
  { id: 'i08', orderId: 'O4', location: 'B1', qty: 1 },
  { id: 'i09', orderId: 'O4', location: 'T3', qty: 2 },
  { id: 'i10', orderId: 'O5', location: 'T1', qty: 2 },
  { id: 'i11', orderId: 'O5', location: 'B2', qty: 1 },
  { id: 'i12', orderId: 'O6', location: 'M2', qty: 3 },
];

const capacity = Number(process.argv[2] ?? 6);
const splitCost = Number(process.argv[3] ?? 5);

console.log(`车容量 = ${capacity}，拆单成本 = ${splitCost}`);
console.log(formatPlan(planPicking(graph, items, { capacity, splitCost })));
