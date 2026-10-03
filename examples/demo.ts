import { mkdtemp, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setImmediate as setImmediateP } from 'node:timers/promises';
import { LogStreamPipeline } from '../src/pipeline.ts';
import { FileSink } from '../src/sink.ts';

const dir = await mkdtemp(join(tmpdir(), 'log-pipeline-demo-'));
const file = join(dir, 'demo.log');
const pipeline = new LogStreamPipeline(new FileSink(file));

pipeline.onPublish((lsn) => {
  console.log(`[publish] durable frontier advanced to LSN ${lsn}`);
});

const barrierPromises: Promise<void>[] = [];

async function producer(streamId: string, count: number): Promise<void> {
  for (let i = 0; i < count; i++) {
    const lsn = pipeline.append(streamId, `${streamId} message #${i}`);
    if (lsn % 50 === 0) {
      const barrierLsn = lsn;
      barrierPromises.push(
        pipeline.insertBarrier().then(() => {
          console.log(`[barrier] all logs up to LSN ${barrierLsn} are fsynced`);
        }),
      );
    }
    if (i % 10 === 0) await setImmediateP();
  }
}

await Promise.all(Array.from({ length: 4 }, (_, s) => producer(`stream-${s}`, 50)));
await Promise.all(barrierPromises);
await pipeline.close();

const lines = (await readFile(file, 'utf8')).trim().split('\n');
console.log(`[done] file=${file}`);
console.log(`[done] persisted ${lines.length} records, stats=`, pipeline.stats);
console.log('[done] first record:', lines[0]);
console.log('[done] last record:', lines[lines.length - 1]);
