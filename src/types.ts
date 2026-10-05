/**
 * 充电调度问题的公共类型定义。
 *
 * 约定：
 * - 时间被离散为整数时隙，时隙 t 表示区间 [t, t+1)。
 * - 能量、功率均为整数单位；一个时隙内充入的能量即该时隙的平均功率。
 * - 车辆充电窗口为 [arrival, departure)，即 departure 时隙不可再充电。
 */

/** 站点分时定义：每个时隙的功率上限与单位电价。 */
export interface Slot {
  /** 该时隙站点可输出的最大能量（整数，>= 0）。 */
  powerLimit: number;
  /** 该时隙单位能量价格（整数，可为负，负价表示反向激励）。 */
  price: number;
}

/** 一辆待调度车辆。 */
export interface Vehicle {
  id: string;
  /** 到站时隙（含），>= 0。 */
  arrival: number;
  /** 离站时隙（不含），必须满足 arrival < departure <= 时隙总数。 */
  departure: number;
  /** 离站前需要充入的总能量（整数，>= 0）。 */
  demand: number;
  /** 单时隙最大充电能量/功率（整数，>= 0）。 */
  maxPower: number;
  /** 允许的最大充电启动次数；缺省表示不限制。 */
  maxStarts?: number;
}

export interface SolveOptions {
  /** 启动次数修复循环的最大迭代轮数，默认 16。 */
  maxRepairIterations?: number;
}

export interface ChargingProblem {
  slots: Slot[];
  vehicles: Vehicle[];
  options?: SolveOptions;
}

/** 单个时隙的充电量分配。 */
export interface SlotAllocation {
  slot: number;
  energy: number;
}

/** 一辆车的充电计划。 */
export interface VehiclePlan {
  vehicleId: string;
  /** 实际充入总能量（等于需求）。 */
  energy: number;
  /** 该车的充电费用。 */
  cost: number;
  /** 充电启动次数（连续充电段的段数）。 */
  starts: number;
  /** 非零充电时隙，按时隙升序。 */
  allocations: SlotAllocation[];
}

export interface FeasibleResult {
  feasible: true;
  totalCost: number;
  totalEnergy: number;
  plan: VehiclePlan[];
}

/** 单车能量缺口：把该车单独放在站点时最多能充多少。 */
export interface VehicleDeficit {
  vehicleId: string;
  windowStart: number;
  /** 不含。 */
  windowEnd: number;
  required: number;
  /** 考虑窗口、最大功率与启动次数限制后，单车独占站点时的最大可充能量。 */
  deliverable: number;
  deficit: number;
}

/** 时间窗能量缺口：到某时隙为止，必须完成的能量与可交付能量之差。 */
export interface WindowDeficit {
  /** 时间窗右端点（含），即窗口 [0, untilSlot]。 */
  untilSlot: number;
  /** 离站时隙 <= untilSlot + 1 的车辆需求之和。 */
  requiredByThen: number;
  /** 在 [0, untilSlot] 内这些车辆最多可充入的能量（最大流精确值）。 */
  deliverableByThen: number;
  deficit: number;
}

export interface InfeasibleResult {
  feasible: false;
  totalDemand: number;
  /** totalDemand - totalDeficit。 */
  totalDeliverable: number;
  /**
   * 总体能量缺口的下界：
   * max(联合最大流缺口, 各车独立缺口之和)。
   */
  totalDeficit: number;
  vehicleDeficits: VehicleDeficit[];
  windowDeficits: WindowDeficit[];
}

export type ChargingResult = FeasibleResult | InfeasibleResult;
