import { LogManager } from '../src/log.ts';
import { BufferPool, Disk } from '../src/storage.ts';
import { TransactionManager } from '../src/txn.ts';

export interface AcceptanceScenario {
  log: LogManager;
  disk: Disk;
  buffer: BufferPool;
  tm: TransactionManager;
  winners: number[];
  losers: number[];
  loserUpdateCount: number;
}

export function buildAcceptanceScenario(): AcceptanceScenario {
  const log = new LogManager();
  const disk = new Disk();
  const buffer = new BufferPool(disk);
  const tm = new TransactionManager(log, buffer);

  const t1 = tm.begin();
  tm.update(t1, 1, 100);
  tm.update(t1, 2, 50);
  tm.commit(t1);

  tm.checkpoint();

  const t2 = tm.begin();
  tm.update(t2, 3, 7);
  tm.commit(t2);

  const t3 = tm.begin();
  tm.update(t3, 1, 1);
  tm.update(t3, 4, 40);

  const t4 = tm.begin();
  tm.update(t4, 2, 2);

  const t5 = tm.begin();
  tm.update(t5, 5, 5);
  tm.update(t5, 3, 70);

  log.force(); // group commit / background log writer pushes WAL to stable storage
  buffer.crash();
  log.crash();

  return {
    log, disk, buffer, tm,
    winners: [t1, t2],
    losers: [t3, t4, t5],
    loserUpdateCount: 5,
  };
}

export const EXPECTED_DISK: [number, number][] = [
  [1, 100],
  [2, 50],
  [3, 7],
  [4, 0],
  [5, 0],
];
