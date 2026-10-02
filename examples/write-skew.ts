/**
 * 运行样例：写倾斜（Write Skew）异常在纯 SI 与 SSI 下的对比。
 *
 * 场景：账户 A=100，B=100，业务约束 A+B>=0。
 *   T1：读 A、B，扣 A；T2：读 A、B，扣 B。
 *   两个事务各自在快照内校验 A+B-deduct>=0 均通过。
 *
 * 注：扣款额取 150（>100）才能使双重扣款后 A+B 真正为负；
 * 取 80 时最终 A+B=40 不违约，但 rw-反依赖环相同，SSI 行为一致。
 */
import { AbortError, SsiDatabase, type IsolationMode } from '../src/ssi.ts';

const DEDUCT = 150;

function run(mode: IsolationMode): void {
  const db = new SsiDatabase(mode);
  db.seed('A', 100);
  db.seed('B', 100);

  const t1 = db.begin();
  const t2 = db.begin();

  const a1 = t1.read('A');
  const b1 = t1.read('B');
  const a2 = t2.read('A');
  const b2 = t2.read('B');
  console.log(`  T${t1.id} 快照读到 A=${a1}, B=${b1}；校验 A+B-${DEDUCT}=${a1 + b1 - DEDUCT} >= 0 ✓`);
  console.log(`  T${t2.id} 快照读到 A=${a2}, B=${b2}；校验 A+B-${DEDUCT}=${a2 + b2 - DEDUCT} >= 0 ✓`);

  t1.write('A', a1 - DEDUCT);
  t2.write('B', b2 - DEDUCT);

  t1.commit();
  console.log(`  T${t1.id} 提交成功（A 扣为 ${a1 - DEDUCT}）`);

  try {
    t2.commit();
    console.log(`  T${t2.id} 提交成功（B 扣为 ${b2 - DEDUCT}）`);
  } catch (err) {
    if (err instanceof AbortError) {
      console.log(`  T${t2.id} 被 SSI 中止：${err.message}`);
    } else {
      throw err;
    }
  }

  const sum = db.latest('A') + db.latest('B');
  console.log(
    `  最终状态：A=${db.latest('A')}, B=${db.latest('B')}, A+B=${sum}，` +
      `约束 A+B>=0 ${sum >= 0 ? '保持 ✓' : '被破坏 ✗'}`,
  );
}

console.log('=== 纯快照隔离（SI）===');
run('si');
console.log('');
console.log('=== 可串行化快照隔离（SSI）===');
run('ssi');
