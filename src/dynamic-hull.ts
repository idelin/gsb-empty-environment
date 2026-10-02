import { cross, type Vec } from "./geometry.ts";

/** 带生命周期的时空点 */
export interface TimedPoint extends Vec {
  readonly id: number;
  readonly enteredAt: number;
  readonly expiresAt: number;
}

interface LivePoint {
  id: number;
  x: number;
  y: number;
  enteredAt: number;
  expiresAt: number;
  alive: boolean;
}

/** 演化器运行统计：用于验证“增量维护、从不全量重扫” */
export interface HullStats {
  /** 当前逻辑时间 */
  now: number;
  /** 当前存活点数 */
  aliveCount: number;
  /** 当前凸包顶点数 */
  hullVertexCount: number;
  /** 累计进入点数 */
  insertedTotal: number;
  /** 累计过期点数 */
  expiredTotal: number;
  /** 插入引发的局部链扫描次数（仅当新点落在包络外时触发） */
  insertSweeps: number;
  /** 过期顶点引发的局部链修复次数（仅当过期的点恰为凸包顶点时触发） */
  localRepairs: number;
  /** 局部修复累计扫描的候选点数（对比：全量 Graham 扫描每次需处理全部存活点） */
  repairPointsScanned: number;
  /** 全量重扫次数 —— 本实现恒为 0 */
  fullRescans: number;
}

class MinHeap<T> {
  private readonly items: T[] = [];
  private readonly key: (item: T) => number;

  constructor(key: (item: T) => number) {
    this.key = key;
  }

  get size(): number {
    return this.items.length;
  }

  peek(): T | undefined {
    return this.items[0];
  }

  push(item: T): void {
    const items = this.items;
    items.push(item);
    let i = items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.key(items[parent]) <= this.key(item)) break;
      items[i] = items[parent];
      i = parent;
    }
    items[i] = item;
  }

  pop(): T | undefined {
    const items = this.items;
    const top = items[0];
    const last = items.pop();
    if (items.length > 0 && last !== undefined) {
      items[0] = last;
      let i = 0;
      for (;;) {
        const left = i * 2 + 1;
        const right = left + 1;
        let smallest = i;
        if (left < items.length && this.key(items[left]) < this.key(items[smallest])) smallest = left;
        if (right < items.length && this.key(items[right]) < this.key(items[smallest])) smallest = right;
        if (smallest === i) break;
        const tmp = items[i];
        items[i] = items[smallest];
        items[smallest] = tmp;
        i = smallest;
      }
    }
    return top;
  }
}

function comparePoints(a: LivePoint, b: LivePoint): number {
  return a.x - b.x || a.y - b.y || a.id - b.id;
}

function toTimedPoint(p: LivePoint): TimedPoint {
  return { id: p.id, x: p.x, y: p.y, enteredAt: p.enteredAt, expiresAt: p.expiresAt };
}

/**
 * 带时效性的空间点集凸包演化器。
 *
 * 每个点携带进入时间 enteredAt 与 TTL，过期自动淡出。
 * 凸包以“上链 + 下链”两条 x 单调链增量维护：
 *  - 插入：新点落在当前包络内则 O(log n) 直接忽略；落在包络外则只对链做一次
 *    局部单调性扫描（新凸包顶点必为旧链顶点 ∪ {新点} 的子集）。
 *  - 过期：过期点不是凸包顶点则无需任何几何计算；是顶点则仅在其左右相邻顶点
 *    的 x 区间内，从存活点有序表中取切片做局部链重建，绝不全量重跑 Graham 扫描。
 */
export class DynamicConvexHull {
  private now = 0;
  private nextId = 1;
  /** 存活点，按 (x, y, id) 有序 */
  private sorted: LivePoint[] = [];
  /** 上包络链，x 严格递增 */
  private upper: LivePoint[] = [];
  /** 下包络链，x 严格递增 */
  private lower: LivePoint[] = [];
  private readonly enterHeap = new MinHeap<LivePoint>((p) => p.enteredAt);
  private readonly expireHeap = new MinHeap<LivePoint>((p) => p.expiresAt);
  private insertedTotal = 0;
  private expiredTotal = 0;
  private insertSweeps = 0;
  private localRepairs = 0;
  private repairPointsScanned = 0;

  /** 当前逻辑时间 */
  get time(): number {
    return this.now;
  }

  /** 当前存活点数 */
  get aliveCount(): number {
    return this.sorted.length;
  }

  /** 在当前时刻加入一个点，ttl 后过期，返回点 id */
  add(x: number, y: number, ttl: number): number {
    return this.schedule(x, y, ttl, this.now);
  }

  /** 预约一个未来时刻 enteredAt 进入的点 */
  schedule(x: number, y: number, ttl: number, enteredAt: number): number {
    if (!Number.isFinite(x) || !Number.isFinite(y)) {
      throw new TypeError("coordinates must be finite numbers");
    }
    if (!(ttl > 0)) {
      throw new RangeError("ttl must be a positive number");
    }
    if (enteredAt < this.now) {
      throw new RangeError("enteredAt cannot be earlier than current time");
    }
    const p: LivePoint = {
      id: this.nextId++,
      x,
      y,
      enteredAt,
      expiresAt: enteredAt + ttl,
      alive: false,
    };
    if (enteredAt <= this.now) {
      p.alive = true;
      this.insertLive(p);
      this.expireHeap.push(p);
    } else {
      this.enterHeap.push(p);
    }
    return p.id;
  }

  /**
   * 将逻辑时间推进到 t：按时间顺序处理过期淡出与新点进入
   * （同一时刻先过期后进入），返回该时刻的凸包顶点序列（逆时针）。
   */
  advanceTo(t: number): TimedPoint[] {
    if (!(t >= this.now)) {
      throw new RangeError("time cannot move backwards");
    }
    for (;;) {
      let expireTop = this.expireHeap.peek();
      while (expireTop && !expireTop.alive) {
        this.expireHeap.pop();
        expireTop = this.expireHeap.peek();
      }
      const enterTop = this.enterHeap.peek();
      const tExpire = expireTop ? expireTop.expiresAt : Infinity;
      const tEnter = enterTop ? enterTop.enteredAt : Infinity;
      if (Math.min(tExpire, tEnter) > t) break;
      if (tExpire <= tEnter && expireTop) {
        this.expireHeap.pop();
        this.removeLive(expireTop);
      } else if (enterTop) {
        this.enterHeap.pop();
        enterTop.alive = true;
        this.insertLive(enterTop);
        this.expireHeap.push(enterTop);
      } else {
        break;
      }
    }
    this.now = t;
    return this.getHull();
  }

  /** 当前凸包多边形顶点序列（逆时针、不含共线冗余点） */
  getHull(): TimedPoint[] {
    const poly: LivePoint[] = [...this.lower];
    for (let i = this.upper.length - 1; i >= 0; i--) poly.push(this.upper[i]);

    const pts: LivePoint[] = [];
    for (const p of poly) {
      const last = pts[pts.length - 1];
      if (last && last.x === p.x && last.y === p.y) continue;
      pts.push(p);
    }
    while (
      pts.length > 1 &&
      pts[0].x === pts[pts.length - 1].x &&
      pts[0].y === pts[pts.length - 1].y
    ) {
      pts.pop();
    }

    let changed = true;
    while (changed && pts.length > 2) {
      changed = false;
      for (let i = 0; i < pts.length; i++) {
        const prev = pts[(i - 1 + pts.length) % pts.length];
        const cur = pts[i];
        const next = pts[(i + 1) % pts.length];
        if (cross(prev, cur, next) === 0) {
          pts.splice(i, 1);
          changed = true;
          break;
        }
      }
    }
    return pts.map(toTimedPoint);
  }

  /** 当前全部存活点（按 x 有序） */
  getAlivePoints(): TimedPoint[] {
    return this.sorted.map(toTimedPoint);
  }

  getStats(): HullStats {
    return {
      now: this.now,
      aliveCount: this.sorted.length,
      hullVertexCount: this.getHull().length,
      insertedTotal: this.insertedTotal,
      expiredTotal: this.expiredTotal,
      insertSweeps: this.insertSweeps,
      localRepairs: this.localRepairs,
      repairPointsScanned: this.repairPointsScanned,
      fullRescans: 0,
    };
  }

  private insertLive(p: LivePoint): void {
    this.insertedTotal++;
    let lo = 0;
    let hi = this.sorted.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (comparePoints(this.sorted[mid], p) < 0) lo = mid + 1;
      else hi = mid;
    }
    this.sorted.splice(lo, 0, p);
    this.insertIntoChain(this.upper, 1, p);
    this.insertIntoChain(this.lower, -1, p);
  }

  private removeLive(p: LivePoint): void {
    this.expiredTotal++;
    p.alive = false;
    let lo = 0;
    let hi = this.sorted.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (comparePoints(this.sorted[mid], p) < 0) lo = mid + 1;
      else hi = mid;
    }
    while (lo < this.sorted.length && this.sorted[lo] !== p && comparePoints(this.sorted[lo], p) === 0) {
      lo++;
    }
    if (lo < this.sorted.length && this.sorted[lo] === p) {
      this.sorted.splice(lo, 1);
    }
    this.removeFromChain(this.upper, 1, p);
    this.removeFromChain(this.lower, -1, p);
  }

  /**
   * 将新点合并进一条链。新凸包顶点集 ⊆ 旧链顶点 ∪ {p}，
   * 因此只需在链数组上做一遍单调性扫描，无需触碰其余存活点。
   */
  private insertIntoChain(chain: LivePoint[], sign: 1 | -1, p: LivePoint): void {
    if (chain.length === 0) {
      chain.push(p);
      return;
    }
    let lo = 0;
    let hi = chain.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (chain[mid].x < p.x) lo = mid + 1;
      else hi = mid;
    }
    const i = lo;
    if (i < chain.length && chain[i].x === p.x) {
      // 同一竖列上只有最极端的点可能留在链上
      const moreExtreme = sign === 1 ? p.y > chain[i].y : p.y < chain[i].y;
      if (!moreExtreme) return;
      chain[i] = p;
    } else {
      if (i > 0 && i < chain.length) {
        // 位于当前包络内侧（或边上）：对凸包无影响
        if (cross(chain[i - 1], chain[i], p) * sign <= 0) return;
      }
      chain.splice(i, 0, p);
    }
    this.insertSweeps++;
    const out: LivePoint[] = [];
    for (const q of chain) {
      while (out.length >= 2 && cross(out[out.length - 2], out[out.length - 1], q) * sign >= 0) {
        out.pop();
      }
      out.push(q);
    }
    chain.length = 0;
    chain.push(...out);
  }

  /**
   * 从链上删除过期点。若 p 不是链顶点则零代价返回；
   * 否则仅在其左右相邻链顶点夹出的 x 区间内做局部链重建：
   * 由支撑线论证，左右邻居必然仍是凸包顶点，新暴露的顶点
   * 只会出现在该开区间内。
   */
  private removeFromChain(chain: LivePoint[], sign: 1 | -1, p: LivePoint): void {
    let lo = 0;
    let hi = chain.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (chain[mid].x < p.x) lo = mid + 1;
      else hi = mid;
    }
    const i = lo;
    if (i >= chain.length || chain[i] !== p) return;

    chain.splice(i, 1);
    const left = i > 0 ? chain[i - 1] : undefined;
    const right = i < chain.length ? chain[i] : undefined;
    const loX = left ? left.x : p.x;
    const hiX = right ? right.x : p.x;

    const sub: LivePoint[] = [];
    const push = (q: LivePoint): void => {
      while (sub.length >= 2 && cross(sub[sub.length - 2], sub[sub.length - 1], q) * sign >= 0) {
        sub.pop();
      }
      sub.push(q);
    };
    if (left) push(left);
    let scanned = 0;
    for (const q of this.sorted) {
      if (q.x < loX) continue;
      if (q.x > hiX) break;
      scanned++;
      if (left && q.x === loX) continue;
      if (right && q.x === hiX) continue;
      push(q);
    }
    if (right) push(right);

    const start = left ? i - 1 : i;
    const deleteCount = (left ? 1 : 0) + (right ? 1 : 0);
    chain.splice(start, deleteCount, ...sub);
    this.localRepairs++;
    this.repairPointsScanned += scanned;
  }
}
