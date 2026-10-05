import type { Problem, PurchaseLine, Supplier } from './types.ts';

export const EPS = 1e-6;

export interface SupplierCostBreakdown {
  totalQty: number;
  subtotal: number;       // 折前金额
  tierIndex: number;      // 触发的阶梯下标，-1 表示未触发任何阶梯
  tierThreshold: number;  // 触发阶梯的门槛，未触发为 0
  discountRate: number;
  discountAmount: number;
  freight: number;
  total: number;          // subtotal - discountAmount + freight
}

/** 解析整单阶梯：返回折前金额为 subtotal 时命中的最高阶梯。 */
export function resolveTier(supplier: Supplier, subtotal: number): { index: number; threshold: number; rate: number } {
  const tiers = [...(supplier.discountTiers ?? [])].sort((a, b) => a.minSubtotal - b.minSubtotal);
  let index = -1;
  let threshold = 0;
  let rate = 0;
  for (let i = 0; i < tiers.length; i++) {
    if (subtotal + EPS >= tiers[i]!.minSubtotal) {
      index = i;
      threshold = tiers[i]!.minSubtotal;
      rate = tiers[i]!.discountRate;
    }
  }
  return { index, threshold, rate };
}

/** 单个供应商一张订单的真实成本（整单折扣 + 运费），这是所有求解器共用的核算函数。 */
export function supplierOrderCost(supplier: Supplier, qtyByMaterial: Map<string, number>): SupplierCostBreakdown {
  let subtotal = 0;
  let totalQty = 0;
  for (const item of supplier.items) {
    const qty = qtyByMaterial.get(item.materialId) ?? 0;
    if (qty > 0) {
      subtotal += qty * item.unitPrice;
      totalQty += qty;
    }
  }
  const tier = resolveTier(supplier, subtotal);
  const discountAmount = subtotal * tier.rate;
  const freightRule = supplier.freight ?? { fixed: 0 };
  let freight = 0;
  if (totalQty > 0) {
    const freeAbove = freightRule.freeAboveSubtotal;
    const fixedPart = freeAbove != null && subtotal + EPS >= freeAbove ? 0 : freightRule.fixed;
    freight = fixedPart + (freightRule.perUnit ?? 0) * totalQty;
  }
  return {
    totalQty,
    subtotal,
    tierIndex: tier.index,
    tierThreshold: tier.threshold,
    discountRate: tier.rate,
    discountAmount,
    freight,
    total: subtotal - discountAmount + freight,
  };
}

export interface OrderLineDetail {
  materialId: string;
  packs: number;
  packSize: number;
  qty: number;
  unitPrice: number;
  amount: number;
}

export interface SupplierOrderDetail extends SupplierCostBreakdown {
  supplierId: string;
  lines: OrderLineDetail[];
  nextThreshold: number | null; // 下一档阶梯门槛，无则为 null
}

export interface Evaluation {
  feasible: boolean;
  violations: string[];
  totalCost: number;
  merchandiseCost: number; // 折后货款合计
  discountAmount: number;
  freightCost: number;
  orders: SupplierOrderDetail[];
  purchasedByMaterial: Map<string, number>;
}

/** 完整校验并核算一个采购方案。 */
export function evaluateSolution(problem: Problem, lines: PurchaseLine[]): Evaluation {
  const violations: string[] = [];
  const maxSuppliers = problem.maxSuppliers ?? 6;
  const supplierById = new Map(problem.suppliers.map((s) => [s.id, s]));

  // 聚合每个供应商每种物料的数量，并校验包数/起订量/单物料上限
  const qtyBySupplier = new Map<string, Map<string, number>>();
  for (const line of lines) {
    const supplier = supplierById.get(line.supplierId);
    if (!supplier) {
      violations.push(`未知供应商: ${line.supplierId}`);
      continue;
    }
    const item = supplier.items.find((it) => it.materialId === line.materialId);
    if (!item) {
      violations.push(`供应商 ${line.supplierId} 不提供物料 ${line.materialId}`);
      continue;
    }
    if (!Number.isInteger(line.packs) || line.packs < 0) {
      violations.push(`${line.supplierId}/${line.materialId} 包数必须为非负整数: ${line.packs}`);
      continue;
    }
    if (line.packs === 0) continue;
    const packSize = item.packSize ?? 1;
    const moq = item.moq ?? 0;
    const qty = line.packs * packSize;
    if (qty + EPS < moq) {
      violations.push(`${line.supplierId}/${line.materialId} 数量 ${qty} 低于起订量 ${moq}`);
    }
    if (item.maxQty != null && qty > item.maxQty + EPS) {
      violations.push(`${line.supplierId}/${line.materialId} 数量 ${qty} 超过单项交付上限 ${item.maxQty}`);
    }
    const map = qtyBySupplier.get(supplier.id) ?? new Map<string, number>();
    map.set(item.materialId, (map.get(item.materialId) ?? 0) + qty);
    qtyBySupplier.set(supplier.id, map);
  }

  // 需求覆盖与超购上限
  const purchasedByMaterial = new Map<string, number>();
  for (const qtyMap of qtyBySupplier.values()) {
    for (const [materialId, qty] of qtyMap) {
      purchasedByMaterial.set(materialId, (purchasedByMaterial.get(materialId) ?? 0) + qty);
    }
  }
  for (const d of problem.demands) {
    const ratio = d.maxOverbuyRatio ?? problem.maxOverbuyRatio ?? 0;
    const upper = d.demand * (1 + ratio);
    const purchased = purchasedByMaterial.get(d.materialId) ?? 0;
    if (purchased + EPS < d.demand) {
      violations.push(`物料 ${d.materialId} 采购量 ${purchased} 低于需求 ${d.demand}`);
    }
    if (purchased > upper + EPS) {
      violations.push(`物料 ${d.materialId} 采购量 ${purchased} 超过超购上限 ${upper}`);
    }
  }

  // 供应商产能与订单成本
  const orders: SupplierOrderDetail[] = [];
  let totalCost = 0;
  let merchandiseCost = 0;
  let discountAmount = 0;
  let freightCost = 0;
  for (const [supplierId, qtyMap] of qtyBySupplier) {
    const supplier = supplierById.get(supplierId)!;
    const cost = supplierOrderCost(supplier, qtyMap);
    if (supplier.capacity != null && cost.totalQty > supplier.capacity + EPS) {
      violations.push(`供应商 ${supplierId} 交付总量 ${cost.totalQty} 超过产能上限 ${supplier.capacity}`);
    }
    const tiers = [...(supplier.discountTiers ?? [])].sort((a, b) => a.minSubtotal - b.minSubtotal);
    const next = tiers.find((t) => t.minSubtotal > cost.subtotal + EPS);
    const orderLines: OrderLineDetail[] = [];
    for (const item of supplier.items) {
      const qty = qtyMap.get(item.materialId) ?? 0;
      if (qty > 0) {
        orderLines.push({
          materialId: item.materialId,
          packs: qty / (item.packSize ?? 1),
          packSize: item.packSize ?? 1,
          qty,
          unitPrice: item.unitPrice,
          amount: qty * item.unitPrice,
        });
      }
    }
    orders.push({
      supplierId,
      lines: orderLines,
      nextThreshold: next ? next.minSubtotal : null,
      ...cost,
    });
    totalCost += cost.total;
    merchandiseCost += cost.subtotal - cost.discountAmount;
    discountAmount += cost.discountAmount;
    freightCost += cost.freight;
  }

  if (orders.length > maxSuppliers) {
    violations.push(`使用供应商 ${orders.length} 家，超过上限 ${maxSuppliers} 家`);
  }

  return {
    feasible: violations.length === 0,
    violations,
    totalCost,
    merchandiseCost,
    discountAmount,
    freightCost,
    orders,
    purchasedByMaterial,
  };
}
