import { BufferPool } from './bufferpool.ts';
import { FaultInjector } from './faults.ts';
import { DiskStorage } from './storage.ts';
import { TransactionManager } from './txn.ts';
import { createMedia, WAL, type DurableMedia } from './wal.ts';

export interface System {
  media: DurableMedia;
  disk: DiskStorage;
  wal: WAL;
  bp: BufferPool;
  tm: TransactionManager;
  injector: FaultInjector;
}

export interface BootOptions {
  media?: DurableMedia;
  disk?: DiskStorage;
  injector?: FaultInjector;
  seed?: number;
}

export function boot(options: BootOptions = {}): System {
  const injector = options.injector ?? new FaultInjector(options.seed);
  const media = options.media ?? createMedia();
  const disk = options.disk ?? new DiskStorage(injector);
  const wal = new WAL(media, injector);
  const bp = new BufferPool(disk, wal);
  const tm = new TransactionManager(wal, bp);
  return { media, disk, wal, bp, tm, injector };
}

/**
 * Simulates a sudden power loss: everything volatile (WAL buffer, buffer
 * pool, transaction table) vanishes; only durable media and disk survive.
 */
export function crash(sys: System): BootOptions {
  return { media: sys.media, disk: sys.disk, injector: sys.injector };
}
