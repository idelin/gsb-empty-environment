import { SourceNode } from './source';
import { TargetNode } from './target';

function assert(cond: boolean, msg: string): void {
  if (!cond) {
    throw new Error(`场景断言失败：${msg}`);
  }
}

/** 场景 1：删除先于旧 put 到达；旧 put 乱序到达/重发都不得复活记录 */
export function scenarioOutOfOrderDelete(): void {
  const b = new TargetNode();
  b.apply('k', { seq: 2, value: null }); // del 先到
  b.apply('k', { seq: 1, value: 'old' }); // 乱序的旧 put
  assert(b.visibleGet('k') === undefined, '删除不得被乱序旧写复活');
  b.apply('k', { seq: 1, value: 'old' }); // 旧 put 重发
  assert(b.visibleGet('k') === undefined, '删除不得被重发旧写复活');
  b.apply('k', { seq: 3, value: 'new' }); // 更新的写仍生效
  assert(b.visibleGet('k') === 'new', '更新版本的写必须生效');
}

/** 场景 2：重复投递幂等 */
export function scenarioDuplicateDelivery(): void {
  const b = new TargetNode();
  b.apply('k', { seq: 1, value: 'v1' });
  b.apply('k', { seq: 1, value: 'v1' });
  b.apply('k', { seq: 1, value: 'v1' });
  assert(b.visibleGet('k') === 'v1', '重复投递后值不变');
}

/** 场景 3：栅栏前接受的请求在栅栏后仍返回结果；栅栏后新写被拒绝 */
export async function scenarioFence(): Promise<void> {
  const a = new SourceNode({ latencyMs: () => 5 });
  const inFlight = a.write('k', 'v1'); // 栅栏前接受
  a.fence(); // 请求仍在飞行中
  const res = await inFlight;
  assert(res.ok, '栅栏前接受的请求必须正常返回结果');
  const after = await a.write('k', 'v2');
  assert(!after.ok && after.reason === 'fenced', '栅栏后新写必须被拒绝');
}

/** 场景 4：基础复制拿到旧快照，更新的增量必须覆盖；更旧的增量不得回滚 */
export function scenarioStaleBaseCopy(): void {
  const b = new TargetNode();
  b.apply('k', { seq: 1, value: 'base-stale' }); // 基础复制读到的旧值
  b.apply('k', { seq: 3, value: 'new' }); // 增量追平
  assert(b.visibleGet('k') === 'new', '新增量必须覆盖旧基础数据');
  b.apply('k', { seq: 2, value: 'mid' }); // 更旧的增量乱序到达
  assert(b.visibleGet('k') === 'new', '过期增量不得回滚已更新的数据');
}

export async function runScenarios(): Promise<void> {
  scenarioOutOfOrderDelete();
  scenarioDuplicateDelivery();
  await scenarioFence();
  scenarioStaleBaseCopy();
  console.log('确定性场景全部通过（乱序删除/重发幂等/栅栏语义/旧快照覆盖）');
}
