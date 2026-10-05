/**
 * 把充电调度问题建模为二分图上的最小费用流：
 *
 *   source --(demand, 0)--> 车辆节点 --(min(maxPower, demand), price[t])--> 时隙节点 --(powerLimit, 0)--> sink
 *
 * 车辆 -> 时隙的边只存在于其充电窗口 [arrival, departure) 内。
 * 该模型精确处理：到离站窗口、需求电量、单车最大功率、站点分时功率上限与分时电价。
 */
import { MinCostFlow } from './minCostFlow';
import type { FlowEdge } from './minCostFlow';
import type { ChargingProblem } from './types';

export interface FlowNetwork {
  mcf: MinCostFlow;
  source: number;
  sink: number;
  /** 被纳入网络的车辆原始下标（按局部顺序）。 */
  vehicleIndices: number[];
  /** source -> 车辆 的需求边，与 vehicleIndices 对齐。 */
  demandEdges: FlowEdge[];
  /** 车辆 -> 时隙 的充电边，与 vehicleIndices 对齐。 */
  chargeEdges: FlowEdge[][];
  /** 每条充电边对应的时隙下标，与 chargeEdges 对齐。 */
  chargeSlots: number[][];
}

/**
 * 构建流网络。
 * @param untilSlot 若指定，只保留时隙 [0, untilSlot]，且只纳入 departure - 1 <= untilSlot 的车辆
 *                  （用于时间窗缺口分析）。
 */
export function buildFlowNetwork(problem: ChargingProblem, untilSlot?: number): FlowNetwork {
  const slotCount = problem.slots.length;
  const lastSlot = untilSlot === undefined ? slotCount - 1 : Math.min(untilSlot, slotCount - 1);

  const vehicleIndices: number[] = [];
  for (let i = 0; i < problem.vehicles.length; i++) {
    if (problem.vehicles[i].departure - 1 <= lastSlot) {
      vehicleIndices.push(i);
    }
  }

  const source = 0;
  const vehicleBase = 1;
  const slotBase = vehicleBase + vehicleIndices.length;
  const sink = slotBase + (lastSlot + 1);
  const mcf = new MinCostFlow(sink + 1);

  const demandEdges: FlowEdge[] = [];
  const chargeEdges: FlowEdge[][] = [];
  const chargeSlots: number[][] = [];

  vehicleIndices.forEach((vehicleIndex, local) => {
    const v = problem.vehicles[vehicleIndex];
    const vehicleNode = vehicleBase + local;
    demandEdges.push(mcf.addEdge(source, vehicleNode, v.demand, 0));
    const edges: FlowEdge[] = [];
    const slots: number[] = [];
    const edgeCap = Math.min(v.maxPower, v.demand);
    if (edgeCap > 0) {
      for (let t = v.arrival; t < v.departure; t++) {
        edges.push(mcf.addEdge(vehicleNode, slotBase + t, edgeCap, problem.slots[t].price));
        slots.push(t);
      }
    }
    chargeEdges.push(edges);
    chargeSlots.push(slots);
  });

  for (let t = 0; t <= lastSlot; t++) {
    const limit = problem.slots[t].powerLimit;
    if (limit > 0) {
      mcf.addEdge(slotBase + t, sink, limit, 0);
    }
  }

  return { mcf, source, sink, vehicleIndices, demandEdges, chargeEdges, chargeSlots };
}

/** 从已求解的网络中读取每辆车的充电分配（与 vehicleIndices 对齐）。 */
export function extractAllocations(network: FlowNetwork): Map<number, number>[] {
  return network.chargeEdges.map((edges, local) => {
    const alloc = new Map<number, number>();
    edges.forEach((edge, j) => {
      const energy = edge.cap0 - edge.cap;
      if (energy > 0) {
        alloc.set(network.chargeSlots[local][j], energy);
      }
    });
    return alloc;
  });
}

/** 每辆车实际充入的能量（与 vehicleIndices 对齐）。 */
export function deliveredPerVehicle(network: FlowNetwork): number[] {
  return network.demandEdges.map((edge) => edge.cap0 - edge.cap);
}
