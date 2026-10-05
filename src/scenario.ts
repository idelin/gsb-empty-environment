// 内置演示场景：分区下两个边缘节点借用配额。
// 覆盖：授予回执丢失、节点重启、重复归还、截止 tick 消费、窗口切换、
// 迟到授予、确认后重发、超时不回收。
import type { Scenario } from './sim.ts';

export const demoScenario: Scenario = {
  ticks: 260,
  windowLength: 100,
  windowBudget: 400,
  nodes: ['n1', 'n2'],
  ops: [
    { tick: 0, op: 'issue', node: 'n1', amount: 150 },               // G1 -> n1
    { tick: 1, op: 'drop', to: 'n2', kind: 'grant', count: 1 },      // G2 回执丢失
    { tick: 1, op: 'issue', node: 'n2', amount: 100 },               // G2 -> n2（被丢包）
    { tick: 5, op: 'spend', node: 'n1', requestId: 'r1', cost: 60 },
    { tick: 6, op: 'spend', node: 'n1', requestId: 'r1', cost: 60 }, // 幂等去重
    { tick: 7, op: 'spend', node: 'n1', requestId: 'r1', cost: 70 }, // 同 ID 不同成本 -> 冲突
    { tick: 10, op: 'partition', from: 'center', to: 'n1' },
    { tick: 10, op: 'partition', from: 'n1', to: 'center' },
    { tick: 10, op: 'partition', from: 'center', to: 'n2' },
    { tick: 10, op: 'partition', from: 'n2', to: 'center' },
    { tick: 12, op: 'issue', node: 'n2', amount: 50 },               // G3 签发但分区丢弃
    { tick: 15, op: 'spend', node: 'n1', requestId: 'r2', cost: 40 },// 分区中花已借额度
    { tick: 20, op: 'restart', node: 'n1' },                         // 重启后账本重放
    { tick: 20, op: 'spend', node: 'n1', requestId: 'r1', cost: 60 },// 重启后仍幂等
    { tick: 21, op: 'spend', node: 'n1', requestId: 'r3', cost: 30 },
    { tick: 22, op: 'spend', node: 'n1', requestId: 'r4', cost: 200 },// 余额不足，分区中无法重新生成
    { tick: 30, op: 'return', node: 'n1', grantId: 'G1' },           // 归还消息被分区丢弃
    { tick: 40, op: 'heal' },
    { tick: 41, op: 'drop', to: 'n1', kind: 'return-confirm', count: 1 }, // 确认丢失
    { tick: 41, op: 'returnAll', node: 'n1' },                       // 重发 R-n1-G1 (unspent=20)
    { tick: 45, op: 'returnAll', node: 'n1' },                       // 未获确认再重发 -> 中心幂等去重
    { tick: 50, op: 'issue', node: 'n1', amount: 120 },              // 确认归还 20 后才可重发
    { tick: 52, op: 'spend', node: 'n1', requestId: 'r5', cost: 100 },
    { tick: 99, op: 'spend', node: 'n1', requestId: 'r6', cost: 20 },// 截止 tick（含端点）消费
    { tick: 100, op: 'spend', node: 'n1', requestId: 'r7', cost: 1 },// 窗口切换，旧额度失效
    { tick: 101, op: 'issue', node: 'n1', amount: 200 },             // G5 (win1)
    { tick: 101, op: 'issue', node: 'n2', amount: 100, delay: 150 }, // G6 迟到（t=251 > exp 199）
    { tick: 105, op: 'spend', node: 'n1', requestId: 'r8', cost: 150 },
    { tick: 110, op: 'restart', node: 'center' },                    // 中心重启，账本重放
    { tick: 111, op: 'issue', node: 'n2', amount: 100 },             // G7
    { tick: 120, op: 'spend', node: 'n2', requestId: 'r9', cost: 60 },
    { tick: 130, op: 'returnAll', node: 'n2' },                      // 归还 G7 未消费 40
    { tick: 135, op: 'issue', node: 'n2', amount: 40 },              // 用确认归还的额度重发 G8
    { tick: 140, op: 'spend', node: 'n2', requestId: 'r10', cost: 40 },
    { tick: 252, op: 'spend', node: 'n2', requestId: 'r11', cost: 20 }, // 迟到授予不能复活过期预算
  ],
};
