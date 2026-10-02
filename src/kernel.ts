import { Rng } from './rng.ts';
import { canonicalize, hashState } from './canonical.ts';
import { Scheduler } from './scheduler.ts';
import { NetworkNode } from './node.ts';
import type { LogEntry, Snapshot, TraceLog } from './types.ts';

export interface RecordResult {
  log: TraceLog;
  snapshots: Snapshot[];
}

export interface TravelResult {
  snapshot: Snapshot;
  world: WorldView;
}

export interface WorldView {
  tick: number;
  consumedEvents: number;
  node: unknown;
}

const ENV_SEED_SALT = 0x9e3779b9;

/**
 * 确定性事件重放调试内核。
 *
 * 录制与重放共享同一条执行路径 runWorld()：
 *  - 录制时，外部输入（包到达）由环境随机流生成，写入日志；
 *  - 重放时，外部输入逐条取自日志，组件逻辑原样重跑，
 *    时钟中断（超时）由组件确定性地重新导出并与日志逐条校验。
 *
 * 时间旅行 travelTo(n) 不触碰任何“现场”状态：
 * 它从原点（种子 + 日志）全新构建一个世界，重放到第 n 个事件后返回。
 */
export class DeterministicKernel {
  /**
   * 录制一条轨迹：arrivalCount 个网络包到达 + 运行过程中产生的超时，
   * 全部外部输入、时钟中断与随机种子记入日志；每个事件后拍摄快照。
   */
  record(opts: {
    seed: number;
    arrivalCount: number;
    lossProb: number;
    timeoutTicks: number;
  }): RecordResult {
    const envSeed = (opts.seed ^ ENV_SEED_SALT) >>> 0;
    const header = {
      seed: opts.seed,
      envSeed,
      lossProb: opts.lossProb,
      timeoutTicks: opts.timeoutTicks,
      arrivalCount: opts.arrivalCount,
    };

    // 环境随机流：生成外部输入（到达 tick 与包大小），与组件随机流相互独立
    const envRng = new Rng(envSeed);
    const arrivals: Array<{ tick: number; size: number }> = [];
    let t = 0;
    for (let i = 0; i < opts.arrivalCount; i++) {
      t += 1 + envRng.nextInt(3);
      arrivals.push({ tick: t, size: 64 + envRng.nextInt(1024) });
    }

    const entries: LogEntry[] = [];
    const world = this.runWorld({
      header,
      arrivals,
      onEvent: (entry) => {
        entries.push(entry);
      },
    });

    return { log: { header, entries }, snapshots: world.snapshots };
  }

  /**
   * 时间旅行：跳转至第 eventIndex 个历史事件（0 = 初始状态），
   * 从零重放并返回该时刻的世界视图与快照。无副作用 —— 不影响任何现存世界。
   */
  travelTo(log: TraceLog, eventIndex: number): TravelResult {
    if (eventIndex < 0 || eventIndex > log.entries.length) {
      throw new RangeError(`eventIndex ${eventIndex} 超出日志范围 [0, ${log.entries.length}]`);
    }
    const arrivals = log.entries
      .filter((e) => e.kind === 'packet-arrival')
      .map((e) => ({ tick: e.tick, size: e.payload.size }));

    let verified = 0;
    const world = this.runWorld({
      header: log.header,
      arrivals,
      stopAfterEvents: eventIndex,
      onEvent: (entry) => {
        const expected = log.entries[verified];
        if (
          !expected ||
          expected.kind !== entry.kind ||
          expected.tick !== entry.tick ||
          canonicalize(expected.payload) !== canonicalize(entry.payload)
        ) {
          throw new Error(
            `日志校验失败 @事件${verified + 1}: 期望 ${JSON.stringify(expected)}, 实际 ${JSON.stringify(entry)}`,
          );
        }
        verified++;
      },
    });

    const snapshot = world.snapshots[eventIndex];
    return {
      snapshot,
      world: {
        tick: world.tick,
        consumedEvents: eventIndex,
        node: world.nodeState,
      },
    };
  }

  /** 完整重放并逐事件比对快照哈希，返回是否全部一致 */
  verify(log: TraceLog, recordedSnapshots: Snapshot[]): boolean {
    for (let i = 0; i <= log.entries.length; i++) {
      const { snapshot } = this.travelTo(log, i);
      if (snapshot.hash !== recordedSnapshots[i].hash) return false;
      if (snapshot.canonical !== recordedSnapshots[i].canonical) return false;
    }
    return true;
  }

  /**
   * 录制/重放共用的世界执行器。
   * 每个入日志事件被消费后立即拍摄快照，snapshots[i] 即“第 i 个事件之后”的世界状态。
   */
  private runWorld(config: {
    header: TraceLog['header'];
    arrivals: Array<{ tick: number; size: number }>;
    stopAfterEvents?: number;
    onEvent: (entry: LogEntry) => void;
  }): { snapshots: Snapshot[]; tick: number; nodeState: unknown } {
    const rng = new Rng(config.header.seed);
    const node = new NetworkNode(config.header.lossProb, config.header.timeoutTicks);
    const scheduler = new Scheduler();
    let now = 0;
    let consumed = 0;

    for (const a of config.arrivals) {
      scheduler.insert({ tick: a.tick, source: 'arrival', kind: 'packet-arrival', seq: 0, size: a.size, attempt: 0 });
    }

    const worldState = (): unknown => ({
      tick: now,
      rngState: rng.getState(),
      schedulerInsertions: scheduler.insertions,
      schedulerQueue: scheduler.dumpState(),
      node: node.state,
    });

    const snapshots: Snapshot[] = [];
    const takeSnapshot = (): void => {
      const canonical = canonicalize(worldState());
      snapshots.push({ afterEvent: consumed, tick: now, canonical, hash: hashState(worldState()) });
    };

    takeSnapshot(); // snapshots[0] = 初始状态

    const stop = config.stopAfterEvents ?? Number.POSITIVE_INFINITY;
    while (scheduler.size > 0 && consumed < stop) {
      const occ = scheduler.pop();
      if (occ === undefined) break;
      now = occ.tick;

      if (occ.source === 'arrival') {
        node.onArrival(now, occ.size, rng, (d, k, s, a) =>
          scheduler.insert({ tick: now + d, source: 'timer', kind: k, seq: s, size: 0, attempt: a }),
        );
        consumed++;
        config.onEvent({ index: consumed, tick: now, kind: 'packet-arrival', payload: { size: occ.size } });
        takeSnapshot();
      } else if (occ.kind === 'ack') {
        node.onAck(now, occ.seq, occ.attempt); // 内部事件，不入日志
      } else {
        const effective = node.onTimeout(now, occ.seq, occ.attempt, rng, (d, k, s, a) =>
          scheduler.insert({ tick: now + d, source: 'timer', kind: k, seq: s, size: 0, attempt: a }),
        );
        if (effective) {
          consumed++;
          config.onEvent({
            index: consumed,
            tick: now,
            kind: 'timeout',
            payload: { seq: occ.seq, attempt: occ.attempt },
          });
          takeSnapshot();
        }
      }
    }

    return { snapshots, tick: now, nodeState: node.state };
  }
}
