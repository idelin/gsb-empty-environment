/**
 * 租赁排程器
 *
 * 规则：
 * - 同型号设备可互换：订单只要求型号，具体用哪台设备由排程决定。
 * - 每台设备累计工作时长跨订单保留；累计达到维护阈值后，必须先在其型号
 *   指定的维护工位上占用工位完成维护（时长固定），累计时长清零后才能继续接单。
 * - 设备空闲不等于维护：只有显式排在工位上的维护事件才能清零累计时长。
 * - 每个工位同一时刻只能维护一台设备。
 * - 订单可以拒单，目标是最小化拒单损失之和；实例最多 15 单。
 */

export interface OrderSpec {
  id: string;
  modelId: string;
  /** 租赁开始时刻（小时，含） */
  start: number;
  /** 租赁结束时刻（小时，不含） */
  end: number;
  /** 拒单损失 */
  rejectPenalty: number;
}

export interface ModelSpec {
  id: string;
  /** 累计工作时长阈值，达到后必须维护 */
  maintenanceThreshold: number;
  /** 维护占用工位的时长 */
  maintenanceDuration: number;
  /** 指定维护工位 */
  stationId: string;
}

export interface UnitSpec {
  id: string;
  modelId: string;
  /** 排程开始前已累计的工作时长（跨订单保留的初始值） */
  initialUsedHours?: number;
}

export interface ProblemInstance {
  orders: OrderSpec[];
  models: ModelSpec[];
  units: UnitSpec[];
}

export interface RentalEvent {
  kind: "rental";
  orderId: string;
  unitId: string;
  start: number;
  end: number;
}

export interface MaintenanceEvent {
  kind: "maintenance";
  unitId: string;
  stationId: string;
  start: number;
  end: number;
}

export interface ScheduleResult {
  totalRejectLoss: number;
  accepted: RentalEvent[];
  maintenances: MaintenanceEvent[];
  rejectedOrderIds: string[];
  /** 排程结束时各设备的累计工作时长（维护后清零，跨订单保留） */
  finalUsedHours: Record<string, number>;
}

export const MAX_ORDERS = 15;

interface Interval {
  start: number;
  end: number;
}

interface UnitState {
  id: string;
  modelId: string;
  /** 设备下一次可用时刻（上一个租赁/维护的结束时刻） */
  available: number;
  /** 距上次维护后的累计工作时长 */
  used: number;
}

/**
 * 在工位时间线上找最早的维护槽位：不早于 earliest、时长 duration、
 * 且不晚于 deadline 结束。找不到返回 null。
 */
function findEarliestSlot(
  timeline: Interval[],
  earliest: number,
  duration: number,
  deadline: number
): Interval | null {
  let s = earliest;
  for (const iv of timeline) {
    if (iv.end <= s) continue;
    if (iv.start - s >= duration) break;
    s = iv.end;
  }
  return s + duration <= deadline ? { start: s, end: s + duration } : null;
}

export function solve(problem: ProblemInstance): ScheduleResult {
  if (problem.orders.length > MAX_ORDERS) {
    throw new Error(`最多支持 ${MAX_ORDERS} 单，当前 ${problem.orders.length} 单`);
  }
  const modelById = new Map(problem.models.map((m) => [m.id, m]));
  for (const o of problem.orders) {
    if (!modelById.has(o.modelId)) throw new Error(`订单 ${o.id} 引用了未知型号 ${o.modelId}`);
    if (!(o.end > o.start)) throw new Error(`订单 ${o.id} 的时间窗非法`);
  }
  for (const u of problem.units) {
    if (!modelById.has(u.modelId)) throw new Error(`设备 ${u.id} 引用了未知型号 ${u.modelId}`);
  }

  const orders = [...problem.orders].sort(
    (a, b) => a.start - b.start || a.end - b.end || a.id.localeCompare(b.id)
  );

  const units: UnitState[] = problem.units.map((u) => ({
    id: u.id,
    modelId: u.modelId,
    available: 0,
    used: u.initialUsedHours ?? 0,
  }));

  const stations: Record<string, Interval[]> = {};
  for (const m of problem.models) stations[m.stationId] = stations[m.stationId] ?? [];

  const rentals: RentalEvent[] = [];
  const maintenances: MaintenanceEvent[] = [];
  const rejected: string[] = [];

  // 初始上界：全部拒单
  let bestLoss = orders.reduce((sum, o) => sum + o.rejectPenalty, 0);
  let bestRentals: RentalEvent[] = [];
  let bestMaintenances: MaintenanceEvent[] = [];
  let bestRejected: string[] = orders.map((o) => o.id);
  let bestUnits: UnitState[] = units.map((u) => ({ ...u }));

  /** 忽略工位冲突，判断订单是否还有可能某台设备承接（用于下界剪枝） */
  function canEverAccept(o: OrderSpec): boolean {
    const m = modelById.get(o.modelId)!;
    const dur = o.end - o.start;
    if (dur > m.maintenanceThreshold) return false;
    return units.some(
      (u) =>
        u.modelId === o.modelId &&
        u.available <= o.start &&
        (u.used + dur <= m.maintenanceThreshold ||
          (u.used > 0 && u.available + m.maintenanceDuration <= o.start))
    );
  }

  function dfs(idx: number, loss: number): void {
    if (loss >= bestLoss) return;
    if (idx === orders.length) {
      bestLoss = loss;
      bestRentals = rentals.slice();
      bestMaintenances = maintenances.slice();
      bestRejected = rejected.slice();
      bestUnits = units.map((u) => ({ ...u }));
      return;
    }
    // 下界：剩余订单中无论如何都接不了的，其损失必然发生
    let lowerBound = loss;
    for (let j = idx; j < orders.length; j++) {
      if (!canEverAccept(orders[j])) {
        lowerBound += orders[j].rejectPenalty;
        if (lowerBound >= bestLoss) return;
      }
    }

    const o = orders[idx];
    const m = modelById.get(o.modelId)!;
    const dur = o.end - o.start;

    const candidates = units
      .map((u, i) => ({ u, i }))
      .filter(({ u }) => u.modelId === o.modelId && u.available <= o.start)
      .sort((a, b) => a.u.available - b.u.available || a.u.used - b.u.used);

    const seenStates = new Set<string>();
    for (const { u, i } of candidates) {
      // 同型号且状态完全相同的设备互为对称，只试一台
      const key = `${u.available}|${u.used}`;
      if (seenStates.has(key)) continue;
      seenStates.add(key);

      // 选择 1：直接接单（累计时长达不到阈值）
      if (u.used + dur <= m.maintenanceThreshold) {
        const prev = units[i];
        units[i] = { ...u, available: o.end, used: u.used + dur };
        rentals.push({ kind: "rental", orderId: o.id, unitId: u.id, start: o.start, end: o.end });
        dfs(idx + 1, loss);
        rentals.pop();
        units[i] = prev;
      }

      // 选择 2：先占工位维护（清零累计时长）再接单；
      // 累计时长未达阈值时也允许主动维护，以避开后续工位冲突。
      if (u.used > 0 && dur <= m.maintenanceThreshold) {
        const slot = findEarliestSlot(
          stations[m.stationId],
          u.available,
          m.maintenanceDuration,
          o.start
        );
        if (slot) {
          const prev = units[i];
          const timeline = stations[m.stationId];
          let pos = timeline.findIndex((iv) => iv.start > slot.start);
          if (pos < 0) pos = timeline.length;
          timeline.splice(pos, 0, slot);
          units[i] = { ...u, available: o.end, used: dur };
          maintenances.push({
            kind: "maintenance",
            unitId: u.id,
            stationId: m.stationId,
            start: slot.start,
            end: slot.end,
          });
          rentals.push({ kind: "rental", orderId: o.id, unitId: u.id, start: o.start, end: o.end });
          dfs(idx + 1, loss);
          rentals.pop();
          maintenances.pop();
          timeline.splice(pos, 1);
          units[i] = prev;
        }
      }
    }

    // 选择 3：拒单
    rejected.push(o.id);
    dfs(idx + 1, loss + o.rejectPenalty);
    rejected.pop();
  }

  dfs(0, 0);

  const finalUsedHours: Record<string, number> = {};
  for (const u of bestUnits) finalUsedHours[u.id] = u.used;

  return {
    totalRejectLoss: bestLoss,
    accepted: bestRentals,
    maintenances: bestMaintenances,
    rejectedOrderIds: bestRejected,
    finalUsedHours,
  };
}

/**
 * 校验排程结果是否满足全部规则，返回错误列表（空数组表示合法）。
 * 其中累计时长只被显式维护事件清零——设备空闲不会被算作维护完成。
 */
export function validateSchedule(problem: ProblemInstance, result: ScheduleResult): string[] {
  const errors: string[] = [];
  const modelById = new Map(problem.models.map((m) => [m.id, m]));
  const unitById = new Map(problem.units.map((u) => [u.id, u]));
  const orderById = new Map(problem.orders.map((o) => [o.id, o]));

  // 每单要么被接且只接一次，要么被拒
  const acceptedOrderIds = new Set<string>();
  for (const r of result.accepted) {
    const o = orderById.get(r.orderId);
    if (!o) {
      errors.push(`租赁事件引用了未知订单 ${r.orderId}`);
      continue;
    }
    if (acceptedOrderIds.has(r.orderId)) errors.push(`订单 ${r.orderId} 被重复承接`);
    acceptedOrderIds.add(r.orderId);
    if (r.start !== o.start || r.end !== o.end)
      errors.push(`订单 ${r.orderId} 的租赁时间与订单时间窗不一致`);
    const u = unitById.get(r.unitId);
    if (!u) errors.push(`租赁事件引用了未知设备 ${r.unitId}`);
    else if (u.modelId !== o.modelId)
      errors.push(`订单 ${r.orderId} 型号 ${o.modelId} 与设备 ${r.unitId} 型号 ${u.modelId} 不符`);
  }
  for (const id of result.rejectedOrderIds) {
    if (!orderById.has(id)) errors.push(`拒单列表包含未知订单 ${id}`);
    if (acceptedOrderIds.has(id)) errors.push(`订单 ${id} 既被承接又被拒单`);
  }
  for (const o of problem.orders) {
    if (!acceptedOrderIds.has(o.id) && !result.rejectedOrderIds.includes(o.id))
      errors.push(`订单 ${o.id} 既未被承接也未被拒单`);
  }

  // 逐台设备按时间模拟：时间不重叠、累计时长跨订单保留、
  // 只有显式维护事件才能清零累计时长（空闲不算维护）
  for (const u of problem.units) {
    const m = modelById.get(u.modelId)!;
    const events = [
      ...result.accepted.filter((r) => r.unitId === u.id),
      ...result.maintenances.filter((mt) => mt.unitId === u.id),
    ].sort((a, b) => a.start - b.start || a.end - b.end);
    let used = u.initialUsedHours ?? 0;
    let cursor = 0;
    for (const ev of events) {
      if (ev.start < cursor)
        errors.push(`设备 ${u.id} 在 [${ev.start}, ${ev.end}) 与之前的事件时间重叠`);
      cursor = ev.end;
      if (ev.kind === "rental") {
        const dur = ev.end - ev.start;
        if (used + dur > m.maintenanceThreshold)
          errors.push(
            `设备 ${u.id} 接订单 ${(ev as RentalEvent).orderId} 时累计时长 ${
              used + dur
            } 超过阈值 ${m.maintenanceThreshold}，且之前没有显式维护`
          );
        used += dur;
      } else {
        if ((ev as MaintenanceEvent).stationId !== m.stationId)
          errors.push(`设备 ${u.id} 的维护未在指定工位 ${m.stationId} 进行`);
        if (ev.end - ev.start !== m.maintenanceDuration)
          errors.push(`设备 ${u.id} 的维护时长 ${ev.end - ev.start} 应为 ${m.maintenanceDuration}`);
        used = 0;
      }
    }
    if (result.finalUsedHours[u.id] !== used)
      errors.push(`设备 ${u.id} 的最终累计时长 ${result.finalUsedHours[u.id]} 应为 ${used}`);
  }

  // 工位容量：同一工位同一时刻只能维护一台设备
  const stationIds = new Set(problem.models.map((m) => m.stationId));
  for (const sid of stationIds) {
    const evs = result.maintenances
      .filter((mt) => mt.stationId === sid)
      .sort((a, b) => a.start - b.start);
    for (let i = 1; i < evs.length; i++) {
      if (evs[i].start < evs[i - 1].end)
        errors.push(`工位 ${sid} 上 ${evs[i - 1].unitId} 与 ${evs[i].unitId} 的维护时间重叠`);
    }
  }

  // 拒单损失
  const expectedLoss = result.rejectedOrderIds.reduce(
    (sum, id) => sum + (orderById.get(id)?.rejectPenalty ?? 0),
    0
  );
  if (result.totalRejectLoss !== expectedLoss)
    errors.push(`总拒单损失 ${result.totalRejectLoss} 应为 ${expectedLoss}`);

  return errors;
}
