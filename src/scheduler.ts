/**
 * 租赁排程器
 *
 * 规则:
 *  - 同型号设备可互换, 订单在固定时间窗 [start, end) 内独占一台设备。
 *  - 每台设备的累计工作时长跨订单保留; 累计时长 + 新订单时长 > 阈值时,
 *    必须先在维护工位完成维护(时长 = maintenanceDuration), 维护后累计清零。
 *  - 维护必须显式调度并占用指定工位; 设备空闲时间不会被自动算作维护。
 *  - 目标: 最小化被拒订单的损失之和 (订单数 <= 15)。
 *
 * 算法: 按订单开始时间做分支定界 DFS。
 *  - 每个订单分支: 拒绝(计入损失) 或 分配到某台可行设备。
 *  - 分配时若累计时长将超阈值, 则搜索 [设备空闲时刻, 订单开始-维护时长] 内
 *    所有"有意义的"维护槽位(区间端点: 下界、上界、工位占用区间端点);
 *    同时也尝试"预防性维护"(未超阈值但提前维护), 以覆盖工位稍后繁忙的情形。
 *  - 记忆化: 相同 (订单序号, 各设备 availableAt/累计, 工位占用) 状态下,
 *    只保留历史最小损失, 其余剪枝。
 */

export const MAX_ORDERS = 15;

export interface ModelSpec {
  name: string;
  units: number; // 该型号设备台数
  threshold: number; // 累计工作时长阈值
  maintenanceDuration: number; // 维护占用工位的时长
}

export interface OrderSpec {
  id: string;
  model: string;
  start: number;
  end: number;
  penalty: number; // 拒单损失
}

export interface Scenario {
  stationCount: number; // 维护工位数量
  models: ModelSpec[];
  orders: OrderSpec[];
}

export interface RentalEvent {
  type: "rental";
  orderId: string;
  start: number;
  end: number;
}

export interface MaintenanceEvent {
  type: "maintenance";
  stationId: number;
  start: number;
  end: number;
}

export type TimelineEvent = RentalEvent | MaintenanceEvent;

export interface UnitTimeline {
  unitId: string;
  model: string;
  events: TimelineEvent[];
}

export interface Solution {
  totalLoss: number;
  accepted: OrderSpec[];
  rejected: OrderSpec[];
  units: UnitTimeline[];
}

interface BusyInterval {
  start: number;
  end: number;
}

interface UnitState {
  unitId: string;
  model: string;
  availableAt: number;
  cum: number; // 累计工作时长(跨订单保留, 仅维护可清零)
  events: TimelineEvent[];
}

interface StationState {
  stationId: number;
  busy: BusyInterval[];
}

function isFree(station: StationState, start: number, end: number): boolean {
  for (const b of station.busy) {
    if (b.start < end && start < b.end) return false;
  }
  return true;
}

function insertBusy(station: StationState, start: number, end: number): void {
  station.busy.push({ start, end });
  station.busy.sort((a, b) => a.start - b.start);
}

/**
 * 在 [lo, deadline] 内为某台设备寻找维护槽位(维护须在 deadline 前完成)。
 * 候选起点取可行区间的端点: lo、hi、工位占用区间的 end、占用区间 start - 维护时长。
 * 同一可行区间内, 端点足以代表整段(更早的起点不劣于区间内的点)。
 */
function maintenanceSlots(
  stations: StationState[],
  maintenanceDuration: number,
  lo: number,
  deadline: number
): { stationId: number; start: number }[] {
  const hi = deadline - maintenanceDuration;
  if (hi < lo) return [];
  const found = new Map<string, { stationId: number; start: number }>();
  for (const st of stations) {
    const candidates = new Set<number>([lo, hi]);
    for (const b of st.busy) {
      if (b.end > lo && b.end < hi) candidates.add(b.end);
      const beforeBusy = b.start - maintenanceDuration;
      if (beforeBusy > lo && beforeBusy < hi) candidates.add(beforeBusy);
    }
    for (const s of candidates) {
      if (s < lo || s > hi) continue;
      if (!isFree(st, s, s + maintenanceDuration)) continue;
      const key = st.stationId + "@" + s;
      if (!found.has(key)) found.set(key, { stationId: st.stationId, start: s });
    }
  }
  const out: { stationId: number; start: number }[] = [];
  found.forEach((v) => out.push(v));
  out.sort((a, b) => a.start - b.start || a.stationId - b.stationId);
  return out;
}

function cloneUnits(units: UnitState[]): UnitState[] {
  return units.map((u) => ({
    unitId: u.unitId,
    model: u.model,
    availableAt: u.availableAt,
    cum: u.cum,
    events: u.events.slice(),
  }));
}

function cloneStations(stations: StationState[]): StationState[] {
  return stations.map((st) => ({
    stationId: st.stationId,
    busy: st.busy.map((b) => ({ start: b.start, end: b.end })),
  }));
}

export function validateScenario(scenario: Scenario): void {
  if (!Number.isInteger(scenario.stationCount) || scenario.stationCount < 0) {
    throw new Error("stationCount 必须是非负整数");
  }
  if (scenario.orders.length > MAX_ORDERS) {
    throw new Error(`订单数最多 ${MAX_ORDERS} 单, 当前 ${scenario.orders.length} 单`);
  }
  const modelNames = new Set<string>();
  for (const m of scenario.models) {
    if (modelNames.has(m.name)) throw new Error(`型号重复: ${m.name}`);
    modelNames.add(m.name);
    if (!(m.units > 0)) throw new Error(`型号 ${m.name} 的设备台数必须为正`);
    if (!(m.threshold > 0)) throw new Error(`型号 ${m.name} 的阈值必须为正`);
    if (!(m.maintenanceDuration > 0)) throw new Error(`型号 ${m.name} 的维护时长必须为正`);
  }
  const orderIds = new Set<string>();
  for (const o of scenario.orders) {
    if (orderIds.has(o.id)) throw new Error(`订单 id 重复: ${o.id}`);
    orderIds.add(o.id);
    if (!modelNames.has(o.model)) throw new Error(`订单 ${o.id} 引用了未知型号 ${o.model}`);
    if (!(o.end > o.start) || o.start < 0) throw new Error(`订单 ${o.id} 时间窗非法`);
    if (!(o.penalty >= 0)) throw new Error(`订单 ${o.id} 拒单损失非法`);
  }
}

export function solve(scenario: Scenario): Solution {
  validateScenario(scenario);
  const modelByName = new Map<string, ModelSpec>();
  for (const m of scenario.models) modelByName.set(m.name, m);

  const orders = scenario.orders
    .slice()
    .sort((a, b) => a.start - b.start || a.end - b.end || b.penalty - a.penalty);

  const initialUnits: UnitState[] = [];
  for (const m of scenario.models) {
    for (let k = 1; k <= m.units; k++) {
      initialUnits.push({
        unitId: m.name + "#" + k,
        model: m.name,
        availableAt: 0,
        cum: 0,
        events: [],
      });
    }
  }
  const initialStations: StationState[] = [];
  for (let s = 1; s <= scenario.stationCount; s++) {
    initialStations.push({ stationId: s, busy: [] });
  }

  let bestLoss = Infinity;
  let bestUnits: UnitState[] | null = null;
  const memo = new Map<string, number>();

  function stateKey(index: number, units: UnitState[], stations: StationState[]): string {
    const us = units
      .map((u) => u.model + ":" + u.availableAt + ":" + u.cum)
      .sort()
      .join(";");
    const ss = stations
      .map((st) => st.busy.map((b) => b.start + "-" + b.end).join(","))
      .join("|");
    return index + "#" + us + "#" + ss;
  }

  function dfs(index: number, loss: number, units: UnitState[], stations: StationState[]): void {
    if (loss >= bestLoss) return;
    if (index === orders.length) {
      bestLoss = loss;
      bestUnits = units;
      return;
    }
    const key = stateKey(index, units, stations);
    const seenLoss = memo.get(key);
    if (seenLoss !== undefined && seenLoss <= loss) return;
    memo.set(key, loss);

    const order = orders[index];
    const spec = modelByName.get(order.model) as ModelSpec;
    const dur = order.end - order.start;

    if (dur <= spec.threshold) {
      // 同型号且状态相同的设备完全等价, 去重后只分支一次
      const tried = new Set<string>();
      for (let ui = 0; ui < units.length; ui++) {
        const u = units[ui];
        if (u.model !== order.model || u.availableAt > order.start) continue;
        const sig = u.availableAt + ":" + u.cum;
        if (tried.has(sig)) continue;
        tried.add(sig);

        const needsMaintenance = u.cum + dur > spec.threshold;
        const options: ({ stationId: number; start: number } | null)[] = [];
        if (!needsMaintenance) options.push(null); // 直接接单
        // 维护后再接单: 包含"必须维护"与"预防性维护"两种情形
        if (u.availableAt + spec.maintenanceDuration <= order.start) {
          const slots = maintenanceSlots(
            stations,
            spec.maintenanceDuration,
            u.availableAt,
            order.start
          );
          for (const s of slots) options.push(s);
        }

        for (const opt of options) {
          const nextUnits = cloneUnits(units);
          const nextStations = cloneStations(stations);
          const nu = nextUnits[ui];
          if (opt !== null) {
            const mEnd = opt.start + spec.maintenanceDuration;
            nu.events.push({
              type: "maintenance",
              stationId: opt.stationId,
              start: opt.start,
              end: mEnd,
            });
            insertBusy(nextStations[opt.stationId - 1], opt.start, mEnd);
            nu.cum = 0;
            nu.availableAt = mEnd;
          }
          nu.events.push({ type: "rental", orderId: order.id, start: order.start, end: order.end });
          nu.cum += dur;
          nu.availableAt = order.end;
          dfs(index + 1, loss, nextUnits, nextStations);
        }
      }
    }

    // 拒绝该订单
    dfs(index + 1, loss + order.penalty, units, stations);
  }

  dfs(0, 0, initialUnits, initialStations);

  if (bestUnits === null) throw new Error("内部错误: 未找到任何可行方案");
  const finalUnits: UnitState[] = bestUnits;
  const acceptedIds = new Set<string>();
  for (const u of finalUnits) {
    for (const e of u.events) {
      if (e.type === "rental") acceptedIds.add(e.orderId);
    }
  }
  const accepted = scenario.orders.filter((o) => acceptedIds.has(o.id));
  const rejected = scenario.orders.filter((o) => !acceptedIds.has(o.id));
  return {
    totalLoss: bestLoss,
    accepted,
    rejected,
    units: finalUnits.map((u) => ({
      unitId: u.unitId,
      model: u.model,
      events: u.events
        .slice()
        .sort((a, b) => a.start - b.start || (a.type === b.type ? 0 : a.type === "maintenance" ? -1 : 1)),
    })),
  };
}

/**
 * 校验一个方案是否满足全部规则; 返回问题列表(空数组 = 合法)。
 * 其中累计时长按时间线重放: 只有显式维护事件能清零, 空闲不清零。
 */
export function verify(scenario: Scenario, solution: Solution): string[] {
  const errors: string[] = [];
  const modelByName = new Map<string, ModelSpec>();
  for (const m of scenario.models) modelByName.set(m.name, m);
  const orderById = new Map<string, OrderSpec>();
  for (const o of scenario.orders) orderById.set(o.id, o);

  const rentedOrderIds = new Set<string>();
  const stationEvents = new Map<number, { start: number; end: number; unitId: string }[]>();

  for (const unit of solution.units) {
    const spec = modelByName.get(unit.model);
    if (!spec) {
      errors.push(`未知型号 ${unit.model}`);
      continue;
    }
    const events = unit.events.slice().sort((a, b) => a.start - b.start);
    let cum = 0;
    let prevEnd = -Infinity;
    for (const e of events) {
      if (e.start < prevEnd) errors.push(`${unit.unitId} 时间线重叠 @${e.start}`);
      prevEnd = e.end;
      if (e.type === "rental") {
        const o = orderById.get(e.orderId);
        if (!o) {
          errors.push(`${unit.unitId} 租赁了未知订单 ${e.orderId}`);
          continue;
        }
        if (rentedOrderIds.has(o.id)) errors.push(`订单 ${o.id} 被重复接单`);
        rentedOrderIds.add(o.id);
        if (o.model !== unit.model) errors.push(`订单 ${o.id} 型号与设备 ${unit.unitId} 不符`);
        if (o.start !== e.start || o.end !== e.end) errors.push(`订单 ${o.id} 未按时间窗执行`);
        const dur = e.end - e.start;
        if (cum + dur > spec.threshold) {
          errors.push(
            `${unit.unitId} 执行订单 ${o.id} 时累计 ${cum}+${dur} 超过阈值 ${spec.threshold}, 之前缺少显式维护`
          );
        }
        cum += dur;
      } else {
        if (e.end - e.start !== spec.maintenanceDuration) {
          errors.push(`${unit.unitId} 维护时长 ${e.end - e.start} 与规定 ${spec.maintenanceDuration} 不符`);
        }
        if (e.stationId < 1 || e.stationId > scenario.stationCount) {
          errors.push(`${unit.unitId} 使用了非法工位 ${e.stationId}`);
        }
        cum = 0;
        const list = stationEvents.get(e.stationId) || [];
        list.push({ start: e.start, end: e.end, unitId: unit.unitId });
        stationEvents.set(e.stationId, list);
      }
    }
  }

  stationEvents.forEach((list, sid) => {
    list.sort((a, b) => a.start - b.start);
    for (let i = 1; i < list.length; i++) {
      if (list[i].start < list[i - 1].end) {
        errors.push(`工位${sid} 上 ${list[i - 1].unitId} 与 ${list[i].unitId} 的维护重叠`);
      }
    }
  });

  const acceptedIds = new Set(solution.accepted.map((o) => o.id));
  const rejectedIds = new Set(solution.rejected.map((o) => o.id));
  for (const o of scenario.orders) {
    if (acceptedIds.has(o.id) === rejectedIds.has(o.id)) {
      errors.push(`订单 ${o.id} 的接受/拒绝状态异常`);
    }
    if (acceptedIds.has(o.id) && !rentedOrderIds.has(o.id)) {
      errors.push(`订单 ${o.id} 标记为接受但时间线中不存在`);
    }
  }
  const rejectedLoss = solution.rejected.reduce((sum, o) => sum + o.penalty, 0);
  if (rejectedLoss !== solution.totalLoss) {
    errors.push(`总损失 ${solution.totalLoss} 与被拒订单损失之和 ${rejectedLoss} 不符`);
  }
  return errors;
}
