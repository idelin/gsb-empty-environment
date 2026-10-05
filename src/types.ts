/** 车辆充电需求（时间格与能量均为整数单位）。 */
export interface VehicleInput {
  /** 车辆标识。 */
  id: string;
  /** 到站时间格（含）。 */
  arrivalSlot: number;
  /** 离站时间格（不含），即可充电窗口为 [arrivalSlot, departureSlot)。 */
  departureSlot: number;
  /** 需求电量（整数能量单位）。 */
  demandEnergy: number;
  /** 每个时间格最多可充能量（= 最大功率 × 格长，整数能量单位）。 */
  maxPowerPerSlot: number;
  /** 充电启动次数上限（连续充电段数上限），缺省为不限。 */
  maxStarts?: number;
}

/** 站点分时功率与电价。 */
export interface StationProfile {
  /** 时间格总数 T，时间格编号 0..T-1。 */
  slotCount: number;
  /** 每个时间格的站点功率上限（整数能量单位/格）。 */
  powerLimitPerSlot: number[];
  /** 每个时间格的电价（费用/能量单位，非负整数）。 */
  pricePerSlot: number[];
}

/** 单个时间格的充电量。 */
export interface SlotAllocation {
  slot: number;
  energy: number;
}

/** 一段连续充电会话，[startSlot, endSlot)。 */
export interface ChargeSession {
  startSlot: number;
  endSlot: number;
  energy: number;
}

/** 单辆车的充电计划。 */
export interface VehicleSchedule {
  vehicleId: string;
  /** 按时间格升序的非零充电量。 */
  allocations: SlotAllocation[];
  /** 连续充电会话（启动次数 = sessions.length）。 */
  sessions: ChargeSession[];
  deliveredEnergy: number;
  cost: number;
}

/** 未满足的需求。 */
export interface UnmetDemand {
  vehicleId: string;
  requested: number;
  delivered: number;
  missing: number;
}

/** 时间窗能量缺口的成因。 */
export type DeficitKind =
  | "station-capacity" // 站点分时功率不足（区间供需缺口）
  | "vehicle-power" // 车辆自身最大功率 × 窗口长度不足
  | "start-limit"; // 启动次数限制导致能量无法安置

/** 无解时的时间窗能量缺口。 */
export interface WindowDeficit {
  /** 窗口起始时间格（含）。 */
  startSlot: number;
  /** 窗口结束时间格（不含）。 */
  endSlot: number;
  /** 窗口内必须完成的电量。 */
  requiredEnergy: number;
  /** 窗口内可提供的电量。 */
  availableEnergy: number;
  /** 缺口 = requiredEnergy - availableEnergy。 */
  deficitEnergy: number;
  kind: DeficitKind;
}

/** 求解结果。 */
export interface ChargePlan {
  /** 是否按时满足全部需求（含启动次数限制）。 */
  feasible: boolean;
  /** 总费用（无解时为已交付部分的费用）。 */
  totalCost: number;
  totalDemand: number;
  totalDelivered: number;
  schedules: VehicleSchedule[];
  /** 未满足需求的车辆及缺口。 */
  unmetDemand: UnmetDemand[];
  /** 无解时的时间窗能量缺口（可行时为空）。 */
  windowDeficits: WindowDeficit[];
}
