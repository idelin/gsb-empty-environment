/**
 * 运行样例：允许判定伴随义务的合并、冲突失败、撤回恢复。
 * 运行：node examples/demo.ts
 */
import {
  AttributeStore, audit, boolAttr, dependentLimit, evaluate, format, intAttr,
  limit, mask, orderedSteps, setAttr, withdrawRule,
  type Decision, type Policy,
} from '../src/index.ts';

function show(title: string, decision: Decision): void {
  console.log(`\n=== ${title} ===`);
  console.log(`判定: ${decision.effect}`);
  console.log(`适用规则: [${decision.applicableRules.join(', ')}]`);
  if (decision.suppressed.length > 0) {
    console.log(`被抑制义务 ${decision.suppressed.length} 条（显式保留）:`);
    for (const s of decision.suppressed) {
      console.log(`  - ${s.obligation.kind} 来自 ${s.fromRule}，被 [${s.suppressedBy.join(', ')}] 抑制`);
    }
  }
  const merged = decision.merged;
  if (merged.status === 'ok') {
    console.log('合并解:', JSON.stringify(merged.solution, null, 2));
    console.log('来源证据:', JSON.stringify(merged.evidence, null, 2));
  } else if (merged.status === 'conflict') {
    console.log(`明确失败（冲突）: ${merged.message}`);
    console.log(`冲突义务 ${merged.conflicting.length} 条，全部保留未丢弃`);
  } else {
    console.log(`明确失败（不可满足）: ${merged.message}`);
    console.log('最小不可满足核:', merged.core.map((o) => `${o.kind}@${o.source}`).join(', '));
  }
}

const attrs = new AttributeStore({
  clearance: intAttr(4),
  department: setAttr(['finance']),
  mfaEnabled: boolAttr(true),
});

const policy: Policy = {
  rules: [
    {
      id: 'analyst-read',
      effect: 'permit',
      condition: { op: 'intCmp', attr: 'clearance', cmp: '>=', value: 2 },
      obligations: [
        limit('maxRows', 'max', 1000, 'analyst-read'),
        mask(['salary'], 'analyst-read'),
        audit(['read'], 'analyst-read'),
        orderedSteps(['tokenize', 'encrypt'], 'analyst-read'),
      ],
    },
    {
      id: 'finance-strict',
      effect: 'permit',
      condition: { op: 'inSet', attr: 'department', value: 'finance' },
      obligations: [
        limit('maxRows', 'max', 100, 'finance-strict'),
        limit('reserveRows', 'min', 10, 'finance-strict'),
        dependentLimit('maxRows', 'reserveRows', 20, 'finance-strict'),
        mask(['ssn'], 'finance-strict'),
        audit(['finance-access'], 'finance-strict'),
        format('json', 'finance-strict'),
        orderedSteps(['audit-log'], 'finance-strict'),
      ],
    },
    {
      id: 'deny-no-mfa',
      effect: 'deny',
      condition: { op: 'boolIs', attr: 'mfaEnabled', value: false },
      obligations: [audit(['denied'], 'deny-no-mfa')],
    },
  ],
};

// 1. 正常允许：限额取更严格、遮蔽取并集、依赖求解、来源证据、顺序保序
show('允许 + 义务合并', evaluate(policy, attrs));

// 2. 互斥输出格式：明确失败
const conflictPolicy: Policy = {
  rules: [
    { id: 'fmt-json', effect: 'permit', condition: { op: 'boolIs', attr: 'mfaEnabled', value: true }, obligations: [format('json', 'fmt-json')] },
    { id: 'fmt-csv', effect: 'permit', condition: { op: 'intCmp', attr: 'clearance', cmp: '>=', value: 1 }, obligations: [format('csv', 'fmt-csv')] },
  ],
};
show('互斥输出格式冲突', evaluate(conflictPolicy, attrs));

// 3. 依赖限额不可满足：最小核
const unsatPolicy: Policy = {
  rules: [
    { id: 'cap', effect: 'permit', condition: { op: 'boolIs', attr: 'mfaEnabled', value: true }, obligations: [limit('budget', 'max', 100, 'cap')] },
    { id: 'floor', effect: 'permit', condition: { op: 'boolIs', attr: 'mfaEnabled', value: true }, obligations: [limit('reserve', 'min', 90, 'floor')] },
    { id: 'dep', effect: 'permit', condition: { op: 'boolIs', attr: 'mfaEnabled', value: true }, obligations: [dependentLimit('budget', 'reserve', 50, 'dep')] },
  ],
};
show('依赖限额不可满足', evaluate(unsatPolicy, attrs));

// 4. deny 抑制允许义务，撤回后恢复
const noMfa = new AttributeStore({
  clearance: intAttr(4),
  department: setAttr(['finance']),
  mfaEnabled: boolAttr(false),
});
show('deny 抑制允许义务', evaluate(policy, noMfa));
show('撤回 deny 后恢复', evaluate(withdrawRule(policy, 'deny-no-mfa'), noMfa));
