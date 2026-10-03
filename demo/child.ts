/**
 * Child-process entrypoint used by the demo and the acceptance test.
 * Each invocation is a fresh "process lifetime" of the RM.
 *
 *   node child.ts prepare <dir>   -> prepare tx-kill, print, then HANG
 *                                    (the parent SIGKILLs it = crash)
 *   node child.ts check   <dir>   -> recover, report lock/commit state
 *   node child.ts commit  <dir>   -> recover, commit the in-doubt txn
 *   node child.ts verify  <dir>   -> recover, print final state
 */
import { ResourceManager } from '../src/resource-manager.ts';

const [cmd, dir] = process.argv.slice(2);
const XID = 'tx-kill';
const KEY = 'account:A';

const rm = new ResourceManager(dir);

switch (cmd) {
  case 'prepare': {
    rm.prepare(XID, [{ type: 'put', key: KEY, value: 42 }]);
    console.log(`PREPARED ${XID} (fsync'd to WAL, lock acquired)`);
    // Hang forever: the parent will SIGKILL us to simulate a crash.
    setInterval(() => {}, 1000);
    break;
  }
  case 'check': {
    const locked = rm.isLocked(KEY);
    console.log(locked ? `LOCKED ${KEY} by ${rm.lockHolder(KEY)}` : `UNLOCKED ${KEY}`);
    console.log(rm.read(KEY) === undefined ? `NOT_COMMITTED ${KEY}` : `COMMITTED ${KEY}`);
    console.log(`IN_DOUBT ${JSON.stringify(rm.recoverScan())}`);
    rm.close();
    break;
  }
  case 'commit': {
    rm.commit(XID);
    console.log(`COMMITTED ${XID}`);
    rm.close();
    break;
  }
  case 'verify': {
    console.log(`VALUE ${KEY} = ${JSON.stringify(rm.read(KEY))}`);
    console.log(rm.isLocked(KEY) ? `LOCKED ${KEY}` : `UNLOCKED ${KEY}`);
    rm.close();
    break;
  }
  default:
    console.error(`unknown command: ${cmd}`);
    process.exit(2);
}
