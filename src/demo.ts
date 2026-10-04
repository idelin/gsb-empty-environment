import { WaitlistEngine } from './waitlist';

const engine = new WaitlistEngine();
for (const id of ['A', 'B', 'C']) engine.addSession(id, 1, 'G');

let cursor = 0;

function showEvents(title: string): void {
  console.log(`\n-- ${title} --`);
  const events = engine.eventsSince(cursor);
  console.log(engine.formatChain(events) || '（无事件）');
  cursor = engine.lastEventId();
}

function showState(title: string): void {
  console.log(`\n== ${title} ==`);
  for (const id of ['A', 'B', 'C']) {
    const confirmed = engine.getConfirmed(id).join(', ') || '空';
    const queue = engine
      .getQueue(id)
      .map(
        (e) =>
          `${e.position}.${e.personId}(优先级${e.priority}/序号${e.seq}` +
          `${e.declinedOffer ? '/已撤销确认' : ''})`,
      )
      .join('  ');
    console.log(`${id}: 确认[${confirmed}]  候补[${queue || '空'}]`);
  }
}

// 1) 一人报名多个互斥场次，按偏好排序
engine.register('张三', 'A', { priority: 100, preference: 1 });
engine.register('张三', 'B', { priority: 100, preference: 2 });
engine.register('张三', 'C', { priority: 100, preference: 3 });
engine.register('李四', 'A', { priority: 90, preference: 1 });
engine.register('李四', 'B', { priority: 90, preference: 2 });
engine.register('王五', 'B', { priority: 80, preference: 1 });
engine.register('王五', 'C', { priority: 80, preference: 2 });
engine.register('赵六', 'C', { priority: 70, preference: 1 });
showEvents('报名与初始递补');
showState('初始稳定状态');

// 2) 张三取消 A：李四递补 A 并释放 B，B 再递补张三 —— 级联直到稳定
engine.cancel('张三', 'A');
showEvents('张三取消 A：级联递补链');
showState('级联后状态');

// 3) 李四撤销 A 的确认：按原候补资格回到 A 队首（序号不变），而非重新排尾
engine.revokeConfirmation('李四', 'A');
showEvents('李四撤销 A 的确认');
showState('撤销后：李四回到 A 候补第 1 位');

// 4) 孙七补位 A；钱八（与李四同优先级）只能排在李四原序号之后
engine.register('孙七', 'A', { priority: 10, preference: 1 });
engine.register('钱八', 'A', { priority: 90, preference: 1 });
showEvents('孙七补位、钱八报名');
showState('钱八排在李四原序号之后');

// 5) 李四恢复递补资格；孙七取消后，李四凭原候补资格（而非队尾）优先递补
engine.reinstate('李四', 'A');
engine.cancel('孙七', 'A');
showEvents('李四恢复资格 + 孙七取消：李四按原位递补');
showState('最终状态');

console.log('\n== 完整递补链 ==');
console.log(engine.formatChain());
