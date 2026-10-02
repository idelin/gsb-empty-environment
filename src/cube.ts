/**
 * 时空密度立方体（Spatio-Temporal Data Cube）。
 *
 * 空间维：TileGrid 瓦片网格；时间维：以小时为粒度的环形缓冲（ring buffer）。
 * 每个小时槽位持有一个 Float64Array 密度切片，槽位数量固定，
 * 因此内存占用上界为 retentionHours * cellCount * 8 字节，严格受限。
 *
 * - addPoint：流式轨迹点 O(1) 落桶累加；过旧（已被逐出）或越界的点计入 dropped。
 * - topK：聚合任意时空窗口后，用容量 K 的最小堆做 Top-K 热点选择。
 */

import { TileGrid } from './grid.ts';
import type { GridConfig } from './grid.ts';
import { BoundedMinHeap } from './minHeap.ts';

export const HOUR_MS = 3_600_000;

export interface CubeConfig extends GridConfig {
  /** 保留的小时槽位数（超出后最旧的小时被自动逐出） */
  retentionHours: number;
}

export interface Hotspot {
  rank: number;
  cellIndex: number;
  lng: number;
  lat: number;
  weight: number;
}

export interface CubeStats {
  cellCount: number;
  retentionHours: number;
  activeHours: number;
  acceptedPoints: number;
  droppedPoints: number;
  /** 立方体常驻内存上界（字节，含切片、索引与查询暂存区） */
  memoryBudgetBytes: number;
}

/** 密度网格累加器：纯空间维度的权重累加（立方体每个小时切片的内核）。 */
export class DensityGridAccumulator {
  readonly cellCount: number;
  readonly weights: Float64Array;

  constructor(cellCount: number) {
    this.cellCount = cellCount;
    this.weights = new Float64Array(cellCount);
  }

  add(cellIndex: number, weight: number): void {
    this.weights[cellIndex] += weight;
  }

  reset(): void {
    this.weights.fill(0);
  }
}

export class SpatioTemporalCube {
  readonly grid: TileGrid;
  readonly retentionHours: number;
  private readonly slices: Float64Array[];
  private readonly sliceHourIndex: Int32Array; // 每个槽位对应的绝对小时编号，-1 表示空
  private readonly scratch: Float64Array;      // 查询聚合复用的暂存区，避免每次分配
  private maxHourIndex = -1;                   // 已见的最新小时水位，用于过期点判定
  private accepted = 0;
  private dropped = 0;

  constructor(config: CubeConfig) {
    this.grid = new TileGrid(config);
    if (config.retentionHours <= 0) {
      throw new Error('retentionHours must be positive');
    }
    this.retentionHours = config.retentionHours;
    this.slices = [];
    for (let i = 0; i < config.retentionHours; i++) {
      this.slices.push(new Float64Array(this.grid.cellCount));
    }
    this.sliceHourIndex = new Int32Array(config.retentionHours).fill(-1);
    this.scratch = new Float64Array(this.grid.cellCount);
  }

  /** 流式累加一个轨迹点。返回是否成功落桶。 */
  addPoint(lng: number, lat: number, timestampMs: number, weight = 1): boolean {
    const cellIndex = this.grid.cellIndexOf(lng, lat);
    if (cellIndex < 0) {
      this.dropped++;
      return false;
    }
    const hourIndex = Math.floor(timestampMs / HOUR_MS);
    // 早于保留窗口（相对最新水位）的点直接丢弃
    if (this.maxHourIndex !== -1 && hourIndex < this.maxHourIndex - this.retentionHours + 1) {
      this.dropped++;
      return false;
    }
    if (hourIndex > this.maxHourIndex) this.maxHourIndex = hourIndex;
    const slot = hourIndex % this.retentionHours;
    if (this.sliceHourIndex[slot] !== hourIndex) {
      this.slices[slot].fill(0); // 逐出旧小时，复用内存
      this.sliceHourIndex[slot] = hourIndex;
    }
    this.slices[slot][cellIndex] += weight;
    this.accepted++;
    return true;
  }

  /**
   * 查询 [startMs, endMs) 时空窗口内权重最高的 K 个网格。
   * 时间复杂度 O(hours * cellCount + cellCount * log K)。
   */
  topK(startMs: number, endMs: number, k: number): Hotspot[] {
    if (k <= 0 || endMs <= startMs) return [];
    const limit = Math.min(k, this.grid.cellCount);
    const scratch = this.scratch;
    scratch.fill(0);

    const startHour = Math.floor(startMs / HOUR_MS);
    const endHour = Math.floor((endMs - 1) / HOUR_MS);
    for (let h = startHour; h <= endHour; h++) {
      const slot = h % this.retentionHours;
      if (this.sliceHourIndex[slot] !== h) continue; // 该小时无数据或已被逐出
      const slice = this.slices[slot];
      for (let i = 0; i < slice.length; i++) {
        scratch[i] += slice[i];
      }
    }

    const heap = new BoundedMinHeap(limit);
    for (let i = 0; i < scratch.length; i++) {
      const w = scratch[i];
      if (w > 0 && w > heap.threshold()) heap.offer(w, i); // 跳过无数据的网格
    }

    return heap.drainSortedDesc().map((item, idx) => {
      const center = this.grid.cellCenter(item.value);
      return {
        rank: idx + 1,
        cellIndex: item.value,
        lng: center.lng,
        lat: center.lat,
        weight: item.key,
      };
    });
  }

  stats(): CubeStats {
    let activeHours = 0;
    for (let i = 0; i < this.retentionHours; i++) {
      if (this.sliceHourIndex[i] !== -1) activeHours++;
    }
    const perSlice = this.grid.cellCount * Float64Array.BYTES_PER_ELEMENT;
    return {
      cellCount: this.grid.cellCount,
      retentionHours: this.retentionHours,
      activeHours,
      acceptedPoints: this.accepted,
      droppedPoints: this.dropped,
      memoryBudgetBytes: perSlice * (this.retentionHours + 1) + this.retentionHours * 4,
    };
  }
}
