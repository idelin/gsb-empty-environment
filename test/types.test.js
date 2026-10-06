import {
  never,
  lit,
  obj,
  union,
  required,
  optional,
  enumerate,
  valueInType,
  valueKey,
  typeEquals,
  typeToString,
  removeLiteral,
  keepOnlyLiteral,
} from '../src/types.js';

export default {
  name: 'types',
  cases: {
    '联合规范化: 拍平/去重/空联合归约 never'(assert) {
      const a = obj({ tag: required(lit('a')) });
      const b = obj({ tag: required(lit('b')) });
      const u = union(union(a, b), a, never);
      assert.equal(u.kind, 'union');
      assert.equal(u.members.length, 2);
      assert.equal(union(never, never), never);
      assert.equal(union(a), a);
    },

    'typeEquals 与字段顺序无关'(assert) {
      const x = obj({ a: required(lit(1)), b: optional(lit('x')) });
      const y = obj({ b: optional(lit('x')), a: required(lit(1)) });
      assert.ok(typeEquals(x, y));
      assert.ok(!typeEquals(x, obj({ a: required(lit(1)) })));
    },

    '枚举: 可选字段产生缺失取值(缺失 tag)'(assert) {
      const t = obj({ tag: optional(union(lit('x'), lit('y'))) });
      const values = enumerate(t);
      assert.deepEqual(
        values.map(valueKey).sort(),
        ['{"tag":"x"}', '{"tag":"y"}', '{}'],
      );
    },

    '枚举: 联合与 never'(assert) {
      assert.deepEqual(enumerate(never), []);
      const u = union(lit('a'), lit('b'));
      assert.deepEqual(enumerate(u), ['a', 'b']);
    },

    'valueInType: 字面值严格相等, 对象允许额外字段'(assert) {
      assert.ok(valueInType('a', lit('a')));
      assert.ok(!valueInType(1, lit('1')));
      const t = obj({ tag: required(lit('a')) });
      assert.ok(valueInType({ tag: 'a', extra: 1 }, t));
      assert.ok(!valueInType({ tag: 'b' }, t));
      assert.ok(!valueInType({}, t));
      assert.ok(!valueInType('a', t));
    },

    'removeLiteral / keepOnlyLiteral'(assert) {
      const u = union(lit('a'), lit('b'), lit('c'));
      assert.ok(typeEquals(removeLiteral(u, 'b'), union(lit('a'), lit('c'))));
      assert.equal(removeLiteral(lit('a'), 'a'), never);
      assert.ok(typeEquals(keepOnlyLiteral(u, 'b'), lit('b')));
      assert.equal(keepOnlyLiteral(u, 'z'), never);
    },

    'typeToString 可读输出'(assert) {
      const t = union(
        obj({ tag: required(lit('a')) }),
        obj({ tag: optional(lit('b')) }),
      );
      assert.equal(
        typeToString(t),
        '{ tag: "a" } | { tag?: "b" }',
      );
    },
  },
};
