/**
 * 缝线裁剪（Seam Carving）工具
 *
 * - 在整数能量图上用动态规划找“顶到底”的最小能量缝线；
 *   相邻行的横坐标最多相差 1（8 连通中的下/左下/右下）。
 * - 保护点（protected）不得被删除：其代价视为无穷大，永远不会出现在缝线上。
 * - 连续删除多条缝线时，每一轮都在“当前已收缩”的能量图上重新计算，
 *   不会一次选多条而忽略重算；并通过列映射把每轮路径还原为原图坐标。
 * - 若某一轮不存在可行缝线（例如某一行被保护点占满），立即停止并报告。
 */

export interface SeamRecord {
  /** 第几轮（从 1 开始） */
  round: number;
  /** 本轮缝线的总能量 */
  energy: number;
  /** 本轮缝线在“当前图”中的横坐标（每行一个） */
  path: number[];
  /** 本轮缝线映射回“原图”的横坐标（每行一个） */
  originalPath: number[];
}

export interface CarveResult {
  /** 成功删除的缝线记录（含每轮能量与原始路径） */
  seams: SeamRecord[];
  /** 实际删除的条数（可能小于请求数量） */
  removedCount: number;
  /** 删除后的图宽 */
  finalWidth: number;
  /** 提前停止的原因；正常完成则为 undefined */
  stoppedReason?: string;
}

const INF = Number.POSITIVE_INFINITY;

function assertRectangular(energy: number[][]): void {
  if (energy.length === 0 || energy[0].length === 0) {
    throw new Error("能量图不能为空");
  }
  const width = energy[0].length;
  for (const row of energy) {
    if (row.length !== width) {
      throw new Error("能量图必须是矩形（每行宽度一致）");
    }
    for (const v of row) {
      if (!Number.isInteger(v)) {
        throw new Error("能量图必须是整数能量图");
      }
    }
  }
}

/**
 * 在当前能量图上找一条最小能量缝线。
 * protectedMask[y][x] 为 true 的像素不得出现在缝线上。
 * 找不到可行缝线时返回 null。
 */
export function findMinSeam(
  energy: number[][],
  protectedMask?: boolean[][],
): { path: number[]; energy: number } | null {
  assertRectangular(energy);
  const height = energy.length;
  const width = energy[0].length;

  const cost = (x: number, y: number): number =>
    protectedMask && protectedMask[y] && protectedMask[y][x]
      ? INF
      : energy[y][x];

  // dp[y][x]：以 (x, y) 结尾的缝线的最小总能量
  const dp: number[][] = new Array(height);
  dp[0] = new Array(width);
  for (let x = 0; x < width; x++) dp[0][x] = cost(x, 0);

  for (let y = 1; y < height; y++) {
    dp[y] = new Array(width);
    for (let x = 0; x < width; x++) {
      const c = cost(x, y);
      if (c === INF) {
        dp[y][x] = INF;
        continue;
      }
      let best = dp[y - 1][x];
      if (x > 0 && dp[y - 1][x - 1] < best) best = dp[y - 1][x - 1];
      if (x < width - 1 && dp[y - 1][x + 1] < best) best = dp[y - 1][x + 1];
      dp[y][x] = best === INF ? INF : best + c;
    }
  }

  // 终点：最后一行中能量最小者（平局取最左，保证确定性）
  let endX = -1;
  let endEnergy = INF;
  for (let x = 0; x < width; x++) {
    if (dp[height - 1][x] < endEnergy) {
      endEnergy = dp[height - 1][x];
      endX = x;
    }
  }
  if (endX === -1) return null; // 无可行缝线

  // 回溯路径
  const path = new Array<number>(height);
  path[height - 1] = endX;
  for (let y = height - 1; y > 0; y--) {
    const x = path[y];
    let prevX = x;
    let best = dp[y - 1][x];
    if (x > 0 && dp[y - 1][x - 1] < best) {
      best = dp[y - 1][x - 1];
      prevX = x - 1;
    }
    if (x < width - 1 && dp[y - 1][x + 1] < best) {
      best = dp[y - 1][x + 1];
      prevX = x + 1;
    }
    path[y - 1] = prevX;
  }
  return { path, energy: endEnergy };
}

/**
 * 连续删除 count 条垂直缝线。
 *
 * 每删除一条后，立即在收缩后的能量图上重新计算下一条；
 * 通过 colMap（当前列 -> 原图列）把每轮路径映射回原图坐标。
 * 保护点随图一起收缩，始终不得删除。
 */
export function carveSeams(
  energy: number[][],
  count: number,
  protectedMask?: boolean[][],
): CarveResult {
  assertRectangular(energy);
  if (!Number.isInteger(count) || count < 0) {
    throw new Error("count 必须是非负整数");
  }
  const height = energy.length;
  const width = energy[0].length;
  if (protectedMask) {
    if (
      protectedMask.length !== height ||
      protectedMask.some((row) => row.length !== width)
    ) {
      throw new Error("保护掩码尺寸必须与能量图一致");
    }
  }

  // 当前图（每轮都会删掉一列）
  let curEnergy = energy.map((row) => row.slice());
  let curMask = protectedMask
    ? protectedMask.map((row) => row.slice())
    : undefined;
  // colMap[y][curX] = 原图中的横坐标
  let colMap: number[][] = energy.map((_, y) =>
    Array.from({ length: width }, (_, x) => x),
  );

  const seams: SeamRecord[] = [];
  let stoppedReason: string | undefined;

  for (let round = 1; round <= count; round++) {
    if (curEnergy[0].length === 0) {
      stoppedReason = "图已被裁到宽度为 0，无法继续";
      break;
    }
    const seam = findMinSeam(curEnergy, curMask);
    if (!seam) {
      stoppedReason = `第 ${round} 轮无可行缝线（保护点阻断了所有顶到底路径）`;
      break;
    }

    // 映射回原图坐标
    const originalPath = seam.path.map((curX, y) => colMap[y][curX]);
    seams.push({
      round,
      energy: seam.energy,
      path: seam.path.slice(),
      originalPath,
    });

    // 删除该列：能量图、保护掩码、列映射同步收缩
    curEnergy = curEnergy.map((row, y) => {
      const next = row.slice();
      next.splice(seam.path[y], 1);
      return next;
    });
    if (curMask) {
      curMask = curMask.map((row, y) => {
        const next = row.slice();
        next.splice(seam.path[y], 1);
        return next;
      });
    }
    colMap = colMap.map((row, y) => {
      const next = row.slice();
      next.splice(seam.path[y], 1);
      return next;
    });
  }

  return {
    seams,
    removedCount: seams.length,
    finalWidth: curEnergy[0].length,
    stoppedReason,
  };
}
