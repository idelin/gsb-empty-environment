import test from 'node:test';
import assert from 'node:assert/strict';
import {
  decide,
  enumerateLegalAssignments,
  selectProofs,
  verifyEvidence,
  type Domain,
  type Policy,
  type Proof,
  type SelectionInput,
} from '../src/index.ts';

const claim = (id: string, attr: string, value: string, cost: number): Proof => ({
  kind: 'claim',
  id,
  attr,
  value,
  cost,
  signature: `sig:${id}`,
});

const memberOf = (id: string, attr: string, values: string[], cost: number): Proof => ({
  kind: 'setMembership',
  id,
  attr,
  values,
  cost,
  signature: `sig:${id}`,
});

function run(input: SelectionInput) {
  const result = selectProofs(input);
  assert.ok(result, '期望存在可行证明组');
  assert.ok(
    verifyEvidence(input.domain, input.policy, result.evidence),
    '充分性依据必须能通过独立验证',
  );
  return result;
}

// ---------------------------------------------------------------------------
// 策略求值语义：最后一条匹配生效 + 默认效果
// ---------------------------------------------------------------------------

test('策略求值：最后一条匹配规则生效，无匹配取默认', () => {
  const policy: Policy = {
    rules: [
      { effect: 'permit', when: { op: 'eq', attr: 'dept', value: 'eng' } },
      { effect: 'deny', when: { op: 'eq', attr: 'loc', value: 'lab' } },
    ],
  };
  assert.equal(decide(policy, { dept: 'eng', loc: 'hq' }), 'permit');
  assert.equal(decide(policy, { dept: 'eng', loc: 'lab' }), 'deny'); // 后规则覆写
  assert.equal(decide(policy, { dept: 'sales', loc: 'hq' }), 'deny'); // 默认 deny
});

// ---------------------------------------------------------------------------
// 场景一：有依据的否定 + 禁止规则覆写
// ---------------------------------------------------------------------------

test('否定条件与禁止覆写：集合成员证明支撑 not 条件，deny 覆写 permit', () => {
  const domain: Domain = {
    attributes: {
      dept: ['eng', 'sales'],
      loc: ['hq', 'lab'],
      clearance: ['none', 'basic', 'high'],
    },
  };
  const policy: Policy = {
    rules: [
      { effect: 'permit', when: { op: 'eq', attr: 'dept', value: 'eng' } },
      {
        effect: 'deny',
        when: {
          op: 'and',
          conds: [
            { op: 'eq', attr: 'loc', value: 'lab' },
            { op: 'not', cond: { op: 'eq', attr: 'clearance', value: 'high' } },
          ],
        },
      },
    ],
  };
  const proofs: Proof[] = [
    claim('c-dept', 'dept', 'eng', 1),
    claim('c-loc', 'loc', 'lab', 1),
    claim('c-clear', 'clearance', 'basic', 3),
    memberOf('m-clear', 'clearance', ['none', 'basic'], 1),
  ];
  const trueAttributes = { dept: 'eng', loc: 'lab', clearance: 'basic' };
  assert.equal(decide(policy, trueAttributes), 'deny'); // 禁止覆写许可

  const result = run({ domain, policy, trueAttributes, proofs });
  assert.equal(result.evidence.decision, 'deny');
  // 最优：loc 确值 + clearance 集合成员（不揭示确值），总代价 2
  assert.deepEqual(result.evidence.proofIds, ['c-loc', 'm-clear']);
  assert.equal(result.totalCost, 2);
  assert.equal(result.disclosedValues, 1);

  // 极小性反例：去掉 loc 后可取 hq 而获准；去掉 clearance 约束后可取 high 而获准
  const witnessFor = (id: string) =>
    result.evidence.minimality.find((w) => w.removedProofId === id)?.counterexample;
  assert.equal(decide(policy, witnessFor('c-loc')!), 'permit');
  assert.equal(decide(policy, witnessFor('m-clear')!), 'permit');
  assert.equal(witnessFor('m-clear')!.clearance, 'high');
});

// ---------------------------------------------------------------------------
// 场景二：缺失声明
// ---------------------------------------------------------------------------

test('缺失声明：结论与未知属性无关时无需披露它', () => {
  const domain: Domain = {
    attributes: { dept: ['eng', 'sales'], loc: ['hq', 'lab'] },
  };
  const policy: Policy = {
    rules: [{ effect: 'permit', when: { op: 'eq', attr: 'dept', value: 'eng' } }],
  };
  // loc 没有任何可用证明，但结论不依赖它
  const result = run({
    domain,
    policy,
    trueAttributes: { dept: 'eng', loc: 'hq' },
    proofs: [claim('c-dept', 'dept', 'eng', 1)],
  });
  assert.deepEqual(result.evidence.proofIds, ['c-dept']);
});

test('缺失声明：关键属性无任何证明时选择失败', () => {
  const domain: Domain = {
    attributes: { dept: ['eng', 'sales'], loc: ['hq', 'lab'] },
  };
  const policy: Policy = {
    rules: [{ effect: 'permit', when: { op: 'eq', attr: 'dept', value: 'eng' } }],
  };
  const result = selectProofs({
    domain,
    policy,
    trueAttributes: { dept: 'eng', loc: 'hq' },
    proofs: [claim('c-loc', 'loc', 'hq', 1)], // 只有无关属性的证明
  });
  assert.equal(result, null);
});

test('缺失声明：结论本就与所有属性无关时空证明组即可', () => {
  const domain: Domain = { attributes: { dept: ['eng', 'sales'] } };
  const policy: Policy = { rules: [], defaultEffect: 'permit' };
  const result = run({ domain, policy, trueAttributes: { dept: 'eng' }, proofs: [] });
  assert.equal(result.totalCost, 0);
  assert.deepEqual(result.evidence.proofIds, []);
  assert.deepEqual(result.evidence.minimality, []);
});

// ---------------------------------------------------------------------------
// 场景三：两个弱证明组合（借助不可同时成立的组合）
// ---------------------------------------------------------------------------

test('两个弱证明组合：各自不充分，合起来借助互斥组合锁定结论', () => {
  const domain: Domain = {
    attributes: { x: [1, 2, 3], y: [1, 2, 3] },
    incompatible: [
      { x: 1, y: 1 },
      { x: 2, y: 2 },
      { x: 3, y: 3 },
    ],
  };
  const policy: Policy = {
    rules: [
      {
        effect: 'permit',
        when: {
          op: 'or',
          conds: [
            { op: 'eq', attr: 'x', value: 1 },
            { op: 'eq', attr: 'y', value: 1 },
          ],
        },
      },
    ],
  };
  const proofs: Proof[] = [
    memberOf('m-x', 'x', [1, 2], 1),
    memberOf('m-y', 'y', [1, 2], 1),
  ];
  const trueAttributes = { x: 2, y: 1 };

  // 单独任何一个弱证明都不充分
  assert.equal(selectProofs({ domain, policy, trueAttributes, proofs: [proofs[0]] }), null);
  assert.equal(selectProofs({ domain, policy, trueAttributes, proofs: [proofs[1]] }), null);

  // 组合后：x∈{1,2} 且 y∈{1,2}，排除 x=y，只剩 (1,2) 与 (2,1)，均获准
  const result = run({ domain, policy, trueAttributes, proofs });
  assert.equal(result.evidence.decision, 'permit');
  assert.deepEqual(result.evidence.proofIds, ['m-x', 'm-y']);
  assert.equal(result.disclosedValues, 0); // 两个确值都未披露

  // 极小性反例必须落在合法域内（不能取 x=y 的非法组合）
  for (const w of result.evidence.minimality) {
    assert.notEqual(w.counterexample.x, w.counterexample.y);
    assert.equal(decide(policy, w.counterexample), 'deny');
  }
});

// ---------------------------------------------------------------------------
// 场景四：某声明已撤销
// ---------------------------------------------------------------------------

test('已撤销声明：被排除在选择之外，回退到次优证明', () => {
  const domain: Domain = { attributes: { dept: ['eng', 'sales'] } };
  const policy: Policy = {
    rules: [{ effect: 'permit', when: { op: 'eq', attr: 'dept', value: 'eng' } }],
  };
  const proofs: Proof[] = [
    claim('c-cheap', 'dept', 'eng', 1),
    claim('c-backup', 'dept', 'eng', 3),
    memberOf('m-weak', 'dept', ['eng', 'sales'], 1), // 单独不充分
  ];
  const trueAttributes = { dept: 'eng' };

  const before = run({ domain, policy, trueAttributes, proofs });
  assert.deepEqual(before.evidence.proofIds, ['c-cheap']);

  const after = run({
    domain,
    policy,
    trueAttributes,
    proofs,
    revokedProofIds: ['c-cheap'],
  });
  assert.deepEqual(after.evidence.proofIds, ['c-backup']);
  assert.equal(after.totalCost, 3);

  // 全部撤销则无解
  assert.equal(
    selectProofs({
      domain,
      policy,
      trueAttributes,
      proofs,
      revokedProofIds: ['c-cheap', 'c-backup', 'm-weak'],
    }),
    null,
  );
});

// ---------------------------------------------------------------------------
// 决胜规则：总代价 → 披露确值个数 → 证明编号序列
// ---------------------------------------------------------------------------

test('同价时优先少披露确值，再按证明编号序列', () => {
  const domain: Domain = { attributes: { dept: ['eng', 'sales'] } };
  const policy: Policy = {
    rules: [{ effect: 'permit', when: { op: 'eq', attr: 'dept', value: 'eng' } }],
  };
  const trueAttributes = { dept: 'eng' };

  // 同价 2：集合成员证明（0 个确值披露）胜过确值声明
  const byDisclosure = run({
    domain,
    policy,
    trueAttributes,
    proofs: [claim('c-dept', 'dept', 'eng', 2), memberOf('m-dept', 'dept', ['eng'], 2)],
  });
  assert.deepEqual(byDisclosure.evidence.proofIds, ['m-dept']);
  assert.equal(byDisclosure.disclosedValues, 0);

  // 同价同披露数：按编号序列取字典序最小
  const byIds = run({
    domain,
    policy,
    trueAttributes,
    proofs: [claim('p-b', 'dept', 'eng', 1), claim('p-a', 'dept', 'eng', 1)],
  });
  assert.deepEqual(byIds.evidence.proofIds, ['p-a']);
});

// ---------------------------------------------------------------------------
// 充分性依据的独立验证：接受合法依据，拒绝篡改
// ---------------------------------------------------------------------------

test('独立验证：篡改结论、删除反例或伪造反例均被拒绝', () => {
  const domain: Domain = { attributes: { dept: ['eng', 'sales'] } };
  const policy: Policy = {
    rules: [{ effect: 'permit', when: { op: 'eq', attr: 'dept', value: 'eng' } }],
  };
  const result = run({
    domain,
    policy,
    trueAttributes: { dept: 'eng' },
    proofs: [claim('c-dept', 'dept', 'eng', 1)],
  });
  const good = result.evidence;
  assert.ok(verifyEvidence(domain, policy, good));

  // 篡改结论
  assert.ok(!verifyEvidence(domain, policy, { ...good, decision: 'deny' }));
  // 删除极小性反例
  assert.ok(!verifyEvidence(domain, policy, { ...good, minimality: [] }));
  // 伪造反例：与剩余证明不一致
  assert.ok(
    !verifyEvidence(domain, policy, {
      ...good,
      minimality: [{ removedProofId: 'c-dept', counterexample: { dept: 'eng' } }],
    }),
  );
  // 伪造反例：非法赋值（域外取值）
  assert.ok(
    !verifyEvidence(domain, policy, {
      ...good,
      minimality: [{ removedProofId: 'c-dept', counterexample: { dept: 'root' } }],
    }),
  );
  // 证明编号列表与证明不符
  assert.ok(!verifyEvidence(domain, policy, { ...good, proofIds: ['nope'] }));
});

// ---------------------------------------------------------------------------
// 全枚举交叉验证：对所有合法真实属性，选择结果与暴力结论一致
// ---------------------------------------------------------------------------

test('全枚举：每个合法真实属性的选择结果都充分且极小', () => {
  const domain: Domain = {
    attributes: { dept: ['eng', 'sales'], loc: ['hq', 'lab'] },
    incompatible: [{ dept: 'sales', loc: 'lab' }],
  };
  const policy: Policy = {
    rules: [
      {
        effect: 'permit',
        when: {
          op: 'and',
          conds: [
            { op: 'in', attr: 'dept', values: ['eng', 'sales'] },
            { op: 'not', cond: { op: 'eq', attr: 'loc', value: 'lab' } },
          ],
        },
      },
    ],
  };
  const proofs: Proof[] = [
    claim('c-dept-eng', 'dept', 'eng', 2),
    claim('c-dept-sales', 'dept', 'sales', 2),
    claim('c-loc-hq', 'loc', 'hq', 1),
    claim('c-loc-lab', 'loc', 'lab', 1),
    memberOf('m-loc', 'loc', ['hq'], 1),
  ];
  for (const trueAttributes of enumerateLegalAssignments(domain)) {
    const result = selectProofs({ domain, policy, trueAttributes, proofs });
    assert.ok(result, `真实属性 ${JSON.stringify(trueAttributes)} 应有可行证明组`);
    assert.equal(result.evidence.decision, decide(policy, trueAttributes));
    assert.ok(verifyEvidence(domain, policy, result.evidence));
    // 每个被选中证明都可出具（与真实属性一致）
    for (const p of result.evidence.proofs) {
      const satisfied =
        p.kind === 'claim'
          ? trueAttributes[p.attr as keyof typeof trueAttributes] === p.value
          : (p.values as readonly unknown[]).includes(trueAttributes[p.attr as keyof typeof trueAttributes]);
      assert.ok(satisfied);
    }
  }
});
