/**
 * 运行样例：验收场景的时间线演示
 *   node examples/demo.ts
 */
import { TemporalDelegationKernel, INF } from '../src/kernel.ts';

const k = new TemporalDelegationKernel();
k.defineRole('R', ['doc:read', 'doc:write']);

console.log('== 事件时间线 ==');
console.log('t=0  授权: A 直接持有角色 R, 业务 [0, ∞)');
k.grantRole('A', 'R', 0, INF, 0);
console.log('t=10 委托: A -> B, 角色 R, 业务 [10, 50)');
k.delegate('A', 'B', 'R', 10, 50, 10);
console.log('t=20 委托: B -> C, 角色 R, 业务 [20, 60)');
k.delegate('B', 'C', 'R', 20, 60, 20);
console.log('t=30 收回: A 知悉并登记 "A->B 委托于业务 25 提前撤回"');
k.revokeDelegation('A', 'B', 'R', 25, 30);

const show = (user: string, bt: number, at: number) => {
  const perms = [...k.getEffectivePermissions(user, bt, at)];
  console.log(
    `  查询 ${user} @ 业务${String(bt).padStart(2)} / 知悉${String(at).padStart(2)} -> ` +
    (perms.length ? `有效 ${JSON.stringify(perms)}` : '已失效 []'),
  );
};

console.log('\n== 验收查询 ==');
console.log('知悉35 查询 C 在业务28（撤回已登记，链在25被截断）:');
show('C', 28, 35);

console.log('\n== 对照查询 ==');
console.log('同一业务28，但知悉25（撤回尚未登记）:');
show('C', 28, 25);
console.log('知悉35，但业务24（撤回生效点之前）:');
show('C', 24, 35);
console.log('中间环节过期：业务55（B 的委托50已到期）:');
show('C', 55, 25);
