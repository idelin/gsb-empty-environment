import test from 'node:test';
import assert from 'node:assert/strict';
import {
  isTypeAllowed,
  parseTypeName,
  TypeNameParseError,
  type WhitelistEntry,
} from './binder.ts';

const WHITELIST: readonly WhitelistEntry[] = [
  { typeName: 'Ns.List`1', assemblyName: 'Good', version: '1.0.0.0', publicKeyToken: 'abc' },
  { typeName: 'Ns.Map`2', assemblyName: 'Good', version: '1.0.0.0', publicKeyToken: 'abc' },
  { typeName: 'Ns.A', assemblyName: 'Good', version: '1.0.0.0', publicKeyToken: 'abc' },
  { typeName: 'Ns.B', assemblyName: 'Good', version: '1.0.0.0', publicKeyToken: 'abc' },
  { typeName: 'Ns.Outer+Inner', assemblyName: 'Good', version: '1.0.0.0', publicKeyToken: 'abc' },
];

const AQ = ', Good, Version=1.0.0.0, PublicKeyToken=abc';

test('allows a simple whitelisted assembly-qualified type', () => {
  assert.equal(isTypeAllowed(`Ns.A${AQ}`, WHITELIST), true);
});

test('allows a generic container whose arguments are all whitelisted', () => {
  assert.equal(isTypeAllowed(`Ns.List\`1[[Ns.A${AQ}]]${AQ}`, WHITELIST), true);
});

test('rejects a restricted type smuggled as a generic argument', () => {
  const name = `Ns.List\`1[[Ns.Evil, Good, Version=1.0.0.0, PublicKeyToken=abc]]${AQ}`;
  assert.equal(isTypeAllowed(name, WHITELIST), false);
});

test('rejects a restricted type smuggled as an array element', () => {
  assert.equal(
    isTypeAllowed('Ns.Evil[], Good, Version=1.0.0.0, PublicKeyToken=abc', WHITELIST),
    false,
  );
});

test('allows whitelisted jagged and multidimensional arrays', () => {
  assert.equal(isTypeAllowed(`Ns.A[,]${AQ}`, WHITELIST), true);
  assert.equal(isTypeAllowed(`Ns.A[][]${AQ}`, WHITELIST), true);
  assert.equal(isTypeAllowed(`Ns.List\`1[[Ns.A${AQ}]][,]${AQ}`, WHITELIST), true);
});

test('rejects when the version does not match the whitelist', () => {
  assert.equal(
    isTypeAllowed('Ns.A, Good, Version=9.9.9.9, PublicKeyToken=abc', WHITELIST),
    false,
  );
});

test('rejects when the public key token does not match the whitelist', () => {
  assert.equal(
    isTypeAllowed('Ns.A, Good, Version=1.0.0.0, PublicKeyToken=deadbeef', WHITELIST),
    false,
  );
});

test('rejects when the assembly name does not match the whitelist', () => {
  assert.equal(
    isTypeAllowed('Ns.A, Evil, Version=1.0.0.0, PublicKeyToken=abc', WHITELIST),
    false,
  );
});

test('allows a legal nested generic two levels deep', () => {
  const inner = `Ns.List\`1[[Ns.B${AQ}]]${AQ}`;
  const name = `Ns.Map\`2[[Ns.A${AQ}],[${inner}]]${AQ}`;
  assert.equal(isTypeAllowed(name, WHITELIST), true);
});

test('rejects a nested generic hiding a restricted type at depth two', () => {
  const inner = 'Ns.List`1[[Ns.Evil, Good, Version=1.0.0.0, PublicKeyToken=abc]]' + AQ;
  const name = `Ns.Map\`2[[Ns.A${AQ}],[${inner}]]${AQ}`;
  assert.equal(isTypeAllowed(name, WHITELIST), false);
});

test('allows a whitelisted nested type using the + separator', () => {
  assert.equal(isTypeAllowed(`Ns.Outer+Inner${AQ}`, WHITELIST), true);
});

test('rejects unbalanced brackets', () => {
  assert.equal(isTypeAllowed(`Ns.List\`1[[Ns.A${AQ}]${AQ}`, WHITELIST), false);
  assert.equal(isTypeAllowed(`Ns.List\`1[[Ns.A${AQ}`, WHITELIST), false);
  assert.equal(isTypeAllowed(`Ns.A[,${AQ}`, WHITELIST), false);
});

test('rejects nesting deeper than the default max depth', () => {
  let name = `Ns.A${AQ}`;
  for (let i = 0; i < 40; i++) {
    name = `Ns.List\`1[[${name}]]${AQ}`;
  }
  assert.equal(isTypeAllowed(name, WHITELIST), false);
});

test('honours a custom maxDepth option', () => {
  const depth2 = `Ns.List\`1[[Ns.List\`1[[Ns.A${AQ}]]${AQ}]]${AQ}`;
  const depth3 = `Ns.List\`1[[${depth2}]]${AQ}`;
  assert.equal(isTypeAllowed(depth2, WHITELIST, { maxDepth: 2 }), true);
  assert.equal(isTypeAllowed(depth3, WHITELIST, { maxDepth: 2 }), false);
});

test('rejects names without a full assembly qualification', () => {
  assert.equal(isTypeAllowed('Ns.A', WHITELIST), false);
  assert.equal(isTypeAllowed('Ns.A, Good', WHITELIST), false);
  assert.equal(isTypeAllowed('Ns.A, Good, Version=1.0.0.0', WHITELIST), false);
});

test('rejects trailing garbage after a valid name', () => {
  assert.equal(isTypeAllowed(`Ns.A${AQ}]]`, WHITELIST), false);
  assert.equal(isTypeAllowed(`Ns.A${AQ} extra`, WHITELIST), false);
});

test('parseTypeName exposes generic args, array ranks and nested types', () => {
  const node = parseTypeName(`Ns.Map\`2[[Ns.A${AQ}],[Ns.Outer+Inner[,]${AQ}]][]${AQ}`);
  assert.equal(node.typeName, 'Ns.Map`2');
  assert.deepEqual(node.arrayRanks, [1]);
  assert.equal(node.assembly?.name, 'Good');
  assert.equal(node.assembly?.version, '1.0.0.0');
  assert.equal(node.assembly?.publicKeyToken, 'abc');
  assert.equal(node.genericArgs.length, 2);
  assert.equal(node.genericArgs[0].typeName, 'Ns.A');
  assert.equal(node.genericArgs[1].typeName, 'Ns.Outer+Inner');
  assert.deepEqual(node.genericArgs[1].arrayRanks, [2]);
});

test('parseTypeName throws TypeNameParseError on malformed input', () => {
  assert.throws(() => parseTypeName(''), TypeNameParseError);
  assert.throws(() => parseTypeName('Ns.List`1[[Ns.A, Good]'), TypeNameParseError);
  assert.throws(() => parseTypeName('Ns.A,'), TypeNameParseError);
});
