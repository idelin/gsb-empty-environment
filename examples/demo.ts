import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database } from '../src/engine.ts';
import { IncrementalSnapshotManager } from '../src/snapshot.ts';
import { DiskStorage } from '../src/storage.ts';

const dir = mkdtempSync(join(tmpdir(), 'aries-demo-'));
console.log(`workspace: ${dir}\n`);

const db = new Database(dir);
const snaps = new IncrementalSnapshotManager(dir, db.pool, db.wal);

// --- Build a WAL: 2 committed + 3 uncommitted transactions, checkpoint in between.
const t1 = db.begin();
db.write(t1, 1, 10);
db.write(t1, 2, 20);
db.commit(t1);
console.log(`T${t1} committed: page1=10 page2=20`);

const t2 = db.begin();
db.write(t2, 3, 30);

const t3 = db.begin();
db.write(t3, 1, 100);

const ckpt = db.checkpoint();
console.log(`checkpoint @LSN ${ckpt.lsn}: TT=${JSON.stringify(ckpt.tt)} DPT=${JSON.stringify(ckpt.dpt)}`);

db.write(t2, 4, 40);
db.commit(t2);
console.log(`T${t2} committed: page3=30 page4=40`);

const t4 = db.begin();
db.write(t4, 5, 50);
const t5 = db.begin();
db.write(t5, 2, 200);
db.write(t5, 6, 60);
console.log(`T${t3}, T${t4}, T${t5} still open (losers)`);

db.wal.flush();
db.pool.flushPage(1); // steal an uncommitted page to disk
db.write(t5, 7, 70);  // unflushed tail, lost in the crash

// --- Incremental snapshot + checkpoint archiving before the crash.
// The WAL is pruned only up to the oldest still-active transaction's begin
// LSN, so the losers' undo chains survive in the live log.
const keepFrom = db.oldestActiveTxnBeginLSN() ?? ckpt.lsn + 1;
const snap = await snaps.archiveCheckpoint(ckpt.lsn, keepFrom);
console.log(`\narchived checkpoint: snapshot #${snap.id} covers LSN (${snap.baseLSN}, ${snap.horizonLSN}], ${snap.pageCount} pages; WAL pruned before LSN ${keepFrom}`);

console.log('\n*** CRASH ***\n');
db.crash();

// --- ARIES restart recovery.
const report = db.recover();
console.log(`analysis: losers=${JSON.stringify(report.undo.loserTxIds)} redoStartLSN=${report.analysis.redoStartLSN}`);
console.log(`redo:     applied=${report.redo.redoneCount} skipped=${report.redo.skippedCount}`);
console.log(`undo:     CLRs written=${report.undo.clrCount}`);

const pages = new DiskStorage(join(dir, 'data.json')).load();
const dump = [...pages.entries()].sort((a, b) => a[0] - b[0])
  .map(([id, img]) => `page${id}=${img.value}`).join(' ');
console.log(`\nrecovered disk state: ${dump}`);
console.log('expected: page1=10 page2=20 page3=30 page4=40 page5=0 page6=0 (page7 lost with the unflushed tail)');

const restored = snaps.restore();
console.log(`\nsnapshot chain restore: ${[...restored.entries()].sort((a, b) => a[0] - b[0]).map(([id, i]) => `page${id}=${i.value}`).join(' ')}`);
