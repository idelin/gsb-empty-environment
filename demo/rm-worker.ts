import { ResourceManager } from '../src/rm.ts';

/**
 * CLI front-end standing in for a resource-manager process driven by an
 * external coordinator.
 *
 *   node demo/rm-worker.ts <dir> prepare  <xid> <key> <jsonValue>
 *   node demo/rm-worker.ts <dir> commit   <xid>
 *   node demo/rm-worker.ts <dir> rollback <xid>
 *   node demo/rm-worker.ts <dir> check    <xid> <key> <otherXid>
 *
 * `prepare` stays alive after voting YES so the parent can SIGKILL it,
 * simulating a crash between prepare and the coordinator's decision.
 */
const [dir, command, ...args] = process.argv.slice(2);
const rm = ResourceManager.open(dir);

switch (command) {
  case 'prepare': {
    const [xid, key, jsonValue] = args;
    rm.begin(xid);
    rm.write(xid, key, JSON.parse(jsonValue));
    rm.prepare(xid);
    console.log(`PREPARED ${xid} key=${key}`);
    setInterval(() => {}, 1000); // stay alive until killed
    break;
  }
  case 'commit': {
    const [xid] = args;
    rm.commit(xid);
    console.log(`COMMITTED ${xid}`);
    rm.close();
    break;
  }
  case 'rollback': {
    const [xid] = args;
    rm.rollback(xid);
    console.log(`ROLLEDBACK ${xid}`);
    rm.close();
    break;
  }
  case 'check': {
    const [xid, key, otherXid] = args;
    console.log(`status(${xid}) = ${rm.status(xid)}`);
    console.log(`locked(${key}) = ${rm.isLocked(key)} holder=${rm.lockHolder(key) ?? '-'}`);
    console.log(`staged(${xid}, ${key}) = ${JSON.stringify(rm.read(xid, key))}`);
    try {
      rm.begin(otherXid);
      rm.write(otherXid, key, 'dirty');
      console.log(`conflict-check: UNEXPECTED - write by ${otherXid} succeeded`);
      process.exitCode = 1;
    } catch (err) {
      console.log(`conflict-check: write by ${otherXid} rejected (${(err as Error).message})`);
    }
    rm.close();
    break;
  }
  default:
    console.error(`unknown command: ${command}`);
    process.exit(2);
}
