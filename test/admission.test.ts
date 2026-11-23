import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  admit,
  certifyInclusion,
  compileBaseline,
  compileNewDeployment,
  PolicyError,
  RULE_VERSION,
  validateGraph,
  verifyCertificate,
  type Grammar,
  type InclusionCertificate,
  type PolicyTable,
  type TypeGraph,
} from '../src/index.ts';
import {
  bareAliasGraph,
  baselineGrammar,
  baselinePolicy,
  cyclicListGraph,
  deepListGraph,
  graph,
  KEY_A,
  KEY_B,
  node,
  recursiveListGraph,
  sameNameDifferentKeyGraph,
  sharedPositionGraph,
} from './fixtures.ts';
import { finiteTreeMember, unfold, treeSize } from './helpers.ts';

function certifiedPair(policy: PolicyTable, budget = 1024) {
  const old = compileBaseline(policy);
  const next = compileNewDeployment(policy);
  const cert = certifyInclusion(next, old, budget);
  return { old, next, cert };
}

test('identical deployment grammars are a subset and the certificate verifies independently', () => {
  const { old, next, cert } = certifiedPair(baselinePolicy());
  assert.equal(cert.verdict, 'subset');
  assert.equal(cert.ruleVersion, RULE_VERSION);
  assert.equal(verifyCertificate(cert, next, old).valid, true);
});

test('recursive List graph is admitted and agrees with every depth-limited unfolding', () => {
  const { old, cert } = certifiedPair(baselinePolicy());
  const cyclic = cyclicListGraph();
  validateGraph(cyclic);

  const result = admit(cyclic, old, cert);
  assert.equal(result.admitted, true);
  assert.equal(result.ruleVersion, RULE_VERSION);

  for (const maxDepth of [0, 1, 2, 5, 12]) {
    const finite = unfold(cyclic, maxDepth);
    assert.notEqual(finiteTreeMember(finite, old.start, old), false);
    assert.ok(treeSize(finite) > 0);
  }

  const recursive = recursiveListGraph(4);
  assert.equal(admit(recursive, old, cert).admitted, true);
  for (const maxDepth of [1, 3, 6]) {
    assert.equal(finiteTreeMember(unfold(recursive, maxDepth), old.start, old), true);
  }
});

test('covariant positions accept narrowing; invariant positions reject it with a path', () => {
  const policy = baselinePolicy();
  const old = compileBaseline(policy);
  const cert = certifyInclusion(old, old, 1024);

  const listString = graph('ls', [
    node({ id: 'ls', typeName: 'List`1', args: ['str'] }),
    node({ id: 'str', typeName: 'String' }),
  ]);
  assert.equal(admit(listString, old, cert).admitted, true);

  const pairWrong = graph('pw', [
    node({ id: 'pw', typeName: 'Pair`2', args: ['inner', 'str'] }),
    node({ id: 'inner', typeName: 'List`1', args: ['str'] }),
    node({ id: 'str', typeName: 'String' }),
  ]);
  const pairGrammar: Grammar = { ...old, start: 'P' };
  const pairCert = certifyInclusion(pairGrammar, pairGrammar, 1024);
  const rejected = admit(pairWrong, pairGrammar, pairCert);
  assert.equal(rejected.admitted, false);
  assert.deepEqual(rejected.rejection?.path, [0]);
  assert.match(rejected.rejection?.reason ?? '', /no alternative/);
});

test('same type name under a different public key is rejected and widens inclusion', () => {
  const { old, cert } = certifiedPair(baselinePolicy());
  const result = admit(sameNameDifferentKeyGraph(), old, cert);
  assert.equal(result.admitted, false);
  assert.equal(result.rejection?.nodeId, 'evil');

  const widened: Grammar = structuredClone(old);
  widened.productions[0].alts.push({
    assembly: { name: 'CoreLib', publicKey: KEY_B, versions: [{ min: '1.0.0.0', max: '1.0.0.0' }] },
    typeName: 'String',
  });
  const wideningCert = certifyInclusion(widened, old, 1024);
  assert.equal(wideningCert.verdict, 'widened');
  assert.ok(wideningCert.counterexample);
  assert.equal(verifyCertificate(wideningCert, widened, old).valid, false);
});

test('a shared node is accepted at one position and rejected at another', () => {
  const { graph: sharedGraph, grammar } = sharedPositionGraph();
  const cert = certifyInclusion(grammar, grammar, 1024);
  assert.equal(admit(sharedGraph, grammar, cert).admitted, true);

  const shared = sharedGraph.nodes.shared;
  shared.typeName = 'Pair`2';
  shared.args = ['x', 'y'];
  sharedGraph.nodes.x = node({ id: 'x', typeName: 'String' });
  sharedGraph.nodes.y = node({ id: 'y', typeName: 'String' });
  const result = admit(sharedGraph, grammar, cert);
  assert.equal(result.admitted, false);
  assert.deepEqual(result.rejection?.path, [0]);
});

test('bare alias rings are rejected at the back-reference', () => {
  const { old, cert } = certifiedPair(baselinePolicy());
  const result = admit(bareAliasGraph(), old, cert);
  assert.equal(result.admitted, false);
  assert.match(result.rejection?.reason ?? '', /bare alias ring/);
});

test('a same-key deployment with a contained interval stays a subset', () => {
  const policy = baselinePolicy();
  policy.productions[0].alts[1].assembly.versions = [{ min: '1.0.0.0', max: '2.0.0.0' }];
  policy.renames = [
    {
      oldAssembly: { version: '2.0.0.0', publicKey: KEY_A, name: 'CoreLib' },
      oldTypeName: 'List`1',
      newAssembly: { version: '1.5.0.0', publicKey: KEY_A, name: 'CoreLib' },
      newTypeName: 'List`1',
      versionRange: { min: '1.2.0.0', max: '1.8.0.0' },
    },
  ];
  const old = compileBaseline(policy);
  const next = compileNewDeployment(policy);
  const renamed = next.productions[0].alts.find((alt) => alt.typeName === 'List`1');
  assert.ok(renamed);
  const cert = certifyInclusion(next, old, 1024);
  assert.equal(cert.verdict, 'subset');
  assert.equal(verifyCertificate(cert, next, old).valid, true);
});

test('a renamed assembly under a new public key cannot inherit the old permission', () => {
  const policy = baselinePolicy();
  policy.renames = [
    {
      oldAssembly: { version: '1.0.0.0', publicKey: KEY_A, name: 'CoreLib' },
      oldTypeName: 'String',
      newAssembly: { version: '2.0.0.0', publicKey: KEY_B, name: 'CoreLib' },
      newTypeName: 'String',
    },
  ];
  const { old, next, cert } = certifiedPair(policy);
  assert.equal(cert.verdict, 'widened');
  assert.equal(verifyCertificate(cert, next, old).valid, false);
});

test('version intervals escaping the old table are detected as widening', () => {
  const old = baselineGrammar();
  const widened = structuredClone(old);
  widened.productions[0].alts[0].assembly.versions = [{ min: '1.0.0.0', max: '9.0.0.0' }];
  const widenedCert = certifyInclusion(widened, old, 1024);
  assert.equal(widenedCert.verdict, 'widened');
  assert.equal(verifyCertificate(widenedCert, widened, old).valid, false);

  const narrowed = structuredClone(old);
  narrowed.productions[0].alts[1].assembly.versions = [{ min: '1.0.0.0', max: '1.0.0.0' }];
  const subsetCert = certifyInclusion(narrowed, old, 1024);
  assert.equal(subsetCert.verdict, 'subset');
  assert.equal(verifyCertificate(subsetCert, narrowed, old).valid, true);
});

test('implicit covariance relaxation across versions is illegal', () => {
  const policy = baselinePolicy();
  policy.renames = [
    {
      oldAssembly: { version: '1.0.0.0', publicKey: KEY_A, name: 'CoreLib' },
      oldTypeName: 'Pair`2',
      newAssembly: { version: '2.0.0.0', publicKey: KEY_A, name: 'CoreLib' },
      newTypeName: 'Pair`2',
      variance: ['covariant', 'invariant'],
    },
  ];
  assert.throws(
    () => compileNewDeployment(policy),
    (error: unknown) => error instanceof PolicyError && /illegal variance relaxation/.test(error.message),
  );
});

test('parameter permutations must be bijections over the arity', () => {
  const policy = baselinePolicy();
  policy.renames = [
    {
      oldAssembly: { version: '1.0.0.0', publicKey: KEY_A, name: 'CoreLib' },
      oldTypeName: 'Pair`2',
      newAssembly: { version: '2.0.0.0', publicKey: KEY_A, name: 'CoreLib' },
      newTypeName: 'Pair`2',
      paramPermutation: [0, 0],
    },
  ];
  assert.throws(() => compileNewDeployment(policy), /bijection/);
});

test('a valid swap permutation preserves the language and stays certified', () => {
  const policy = baselinePolicy();
  policy.renames = [
    {
      oldAssembly: { version: '1.0.0.0', publicKey: KEY_A, name: 'CoreLib' },
      oldTypeName: 'Pair`2',
      newAssembly: { version: '1.0.0.0', publicKey: KEY_A, name: 'CoreLib' },
      newTypeName: 'SwappedPair`2',
      paramPermutation: [1, 0],
    },
  ];
  const { old, next, cert } = certifiedPair(policy);
  const swapped = next.productions[1].alts.find((alt) => alt.typeName === 'SwappedPair`2');
  assert.deepEqual(swapped?.args?.map((argument) => argument.nt), ['P', 'P']);
  assert.equal(cert.verdict, 'subset');
});

test('budget overflow returns unknown and can never gate admission', () => {
  const { old, next } = certifiedPair(baselinePolicy());
  const cert = certifyInclusion(next, old, 0);
  assert.equal(cert.verdict, 'unknown');
  assert.equal(cert.proof.length, 0);
  assert.equal(verifyCertificate(cert, next, old).valid, false);

  const admission = admit(recursiveListGraph(2), old, cert);
  assert.equal(admission.admitted, false);
  assert.match(admission.rejection?.reason ?? '', /subset certificates/);
});

test('runtime rejects foreign rule versions and swapped grammars', () => {
  const { old, cert } = certifiedPair(baselinePolicy());
  const foreign: InclusionCertificate = { ...cert, ruleVersion: 'type-admission/rules/v0' };
  const versionResult = admit(recursiveListGraph(1), old, foreign);
  assert.equal(versionResult.admitted, false);
  assert.match(versionResult.rejection?.reason ?? '', /rule version mismatch/);

  const swapped: Grammar = { ...structuredClone(old), start: 'P' };
  const hashResult = admit(recursiveListGraph(1), swapped, cert);
  assert.equal(hashResult.admitted, false);
  assert.match(hashResult.rejection?.reason ?? '', /grammar hash/);
});

test('input depth beyond the JS call stack is handled without recursion', () => {
  const { old, cert } = certifiedPair(baselinePolicy());
  const depth = 200_000;
  const deep = deepListGraph(depth);
  assert.equal(admit(deep, old, cert).admitted, true);

  const broken = structuredClone(deep);
  const mid = `d${Math.floor(depth / 2)}`;
  broken.nodes[mid].typeName = 'NotInGrammar';
  const rejected = admit(broken, old, cert);
  assert.equal(rejected.admitted, false);
  assert.ok((rejected.rejection?.path.length ?? 0) > 0);
});

test('admission never invokes constructors or methods on message data', () => {
  const { old, cert } = certifiedPair(baselinePolicy());
  const target = recursiveListGraph(3);
  const proxied = new Proxy(target, {
    get(targetObj, property, receiver) {
      const value = Reflect.get(targetObj, property, receiver);
      if (typeof value === 'function') {
        throw new Error(`unexpected invocation: ${String(property)}`);
      }
      return value;
    },
  });
  assert.equal(admit(proxied, old, cert).admitted, true);
});

test('coinductive gate and finite unfoldings agree across checked depths', () => {
  const { old, cert } = certifiedPair(baselinePolicy());
  const withLeaf: TypeGraph = recursiveListGraph(5);
  for (const maxDepth of [0, 1, 2, 4, 8]) {
    const tree = unfold(withLeaf, maxDepth);
    if (finiteTreeMember(tree, old.start, old) === true) {
      assert.equal(admit(withLeaf, old, cert).admitted, true);
    }
  }
});

test('array element positions are invariant and report the first rejection', () => {
  const grammarObj = baselineGrammar();
  grammarObj.start = 'ARR';
  const cert = certifyInclusion(grammarObj, grammarObj, 1024);
  const validArray = graph('a', [
    node({ id: 'a', typeName: 'Array', arrayRank: 1, element: 's' }),
    node({ id: 's', typeName: 'String' }),
  ]);
  assert.equal(admit(validArray, grammarObj, cert).admitted, true);

  const badArray = graph('b', [
    node({ id: 'b', typeName: 'Array', arrayRank: 1, element: 'p' }),
    node({ id: 'p', typeName: 'Pair`2', args: ['s', 's'] }),
    node({ id: 's', typeName: 'String' }),
  ]);
  const rejected = admit(badArray, grammarObj, cert);
  assert.equal(rejected.admitted, false);
  assert.deepEqual(rejected.rejection?.path, [0]);
});
