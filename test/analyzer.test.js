import { analyze } from '../src/analyzer.js';
import {
  never,
  lit,
  obj,
  union,
  required,
  optional,
  typeEquals,
} from '../src/types.js';
import { get, val, eq, and, or, not, iff, leaf } from '../src/ast.js';

const Shape = union(
  obj({ tag: required(lit('circle')), radius: required(union(lit(1), lit(2))) }),
  obj({ tag: required(lit('square')), side: required(lit(1)) }),
  obj({ tag: required(lit('rect')), w: required(lit(1)), h: required(lit(2)) }),
);

const shapeProgram = iff(
  eq(get('tag'), val('circle')),
  leaf('circle'),
  iff(eq(get('tag'), val('square')), leaf('square'), leaf('rect')),
);

export default {
  name: 'analyzer',
  cases: {
    'tag 等值收窄: 各分支剩余类型精确到单个成员'(assert) {
      const r = analyze(Shape, shapeProgram);
      assert.equal(r.diagnostics.length, 0);
      assert.equal(r.unreachable.length, 0);
      const byLabel = Object.fromEntries(r.leaves.map((l) => [l.label, l.type]));
      assert.ok(
        typeEquals(
          byLabel.circle,
          obj({ tag: required(lit('circle')), radius: required(union(lit(1), lit(2))) }),
        ),
      );
      assert.ok(
        typeEquals(byLabel.square, obj({ tag: required(lit('square')), side: required(lit(1)) })),
      );
      assert.ok(
        typeEquals(
          byLabel.rect,
          obj({ tag: required(lit('rect')), w: required(lit(1)), h: required(lit(2)) }),
        ),
      );
    },

    '分支合流取联合: residual 等于输入类型'(assert) {
      const r = analyze(Shape, shapeProgram);
      assert.ok(typeEquals(r.residual, Shape));
    },

    '未知字段访问报错, 且肯定分支不可达'(assert) {
      const prog = iff(eq(get('color'), val('red')), leaf('red'), leaf('other'));
      const r = analyze(Shape, prog);
      assert.equal(r.diagnostics.length, 1);
      assert.equal(r.diagnostics[0].code, 'UNKNOWN_FIELD');
      assert.equal(r.diagnostics[0].field, 'color');
      assert.equal(r.diagnostics[0].severity, 'error');
      const byLabel = Object.fromEntries(r.leaves.map((l) => [l.label, l]));
      assert.equal(byLabel.red.type, never);
      assert.equal(byLabel.red.reachable, false);
      assert.ok(typeEquals(byLabel.other.type, Shape));
    },

    '不可达分支单独标记, 不计入错误诊断'(assert) {
      const prog = iff(
        eq(get('tag'), val('circle')),
        iff(eq(get('tag'), val('square')), leaf('impossible'), leaf('circle')),
        leaf('other'),
      );
      const r = analyze(Shape, prog);
      assert.equal(r.diagnostics.length, 0);
      assert.equal(r.unreachable.length, 1);
      assert.equal(r.unreachable[0].label, 'impossible');
      assert.ok(r.unreachable[0].path.some((p) => p.startsWith('then(')));
    },

    '缺失 tag: 可选字段与无 tag 成员在否定分支保留'(assert) {
      const Event = union(
        obj({ tag: required(lit('click')), x: required(lit(0)) }),
        obj({ tag: optional(union(lit('scroll'), lit('hover'))), y: required(lit(0)) }),
        obj({ name: required(lit('anon')) }),
      );
      const prog = iff(eq(get('tag'), val('scroll')), leaf('scroll'), leaf('other'));
      const r = analyze(Event, prog);
      assert.equal(r.diagnostics.length, 0);
      const byLabel = Object.fromEntries(r.leaves.map((l) => [l.label, l.type]));
      assert.ok(
        typeEquals(
          byLabel.scroll,
          obj({ tag: required(lit('scroll')), y: required(lit(0)) }),
        ),
      );
      assert.ok(
        typeEquals(
          byLabel.other,
          union(
            obj({ tag: required(lit('click')), x: required(lit(0)) }),
            obj({ tag: optional(lit('hover')), y: required(lit(0)) }),
            obj({ name: required(lit('anon')) }),
          ),
        ),
      );
    },

    'and/or/not 组合收窄'(assert) {
      const T = obj({ k: required(union(lit('a'), lit('b'), lit('c'))) });
      const prog = iff(
        or(eq(get('k'), val('a')), eq(get('k'), val('b'))),
        leaf('ab'),
        leaf('c'),
      );
      const r = analyze(T, prog);
      const byLabel = Object.fromEntries(r.leaves.map((l) => [l.label, l.type]));
      assert.ok(
        typeEquals(
          byLabel.ab,
          union(obj({ k: required(lit('a')) }), obj({ k: required(lit('b')) })),
        ),
      );
      assert.ok(typeEquals(byLabel.c, obj({ k: required(lit('c')) })));

      const prog2 = iff(and(eq(get('k'), val('a')), not(eq(get('k'), val('a')))), leaf('x'), leaf('y'));
      const r2 = analyze(T, prog2);
      assert.equal(r2.unreachable.length, 1);
      assert.equal(r2.unreachable[0].label, 'x');
    },

    '字面值在左侧的等值判断同样收窄'(assert) {
      const prog = iff(eq(val('circle'), get('tag')), leaf('circle'), leaf('other'));
      const r = analyze(Shape, prog);
      const byLabel = Object.fromEntries(r.leaves.map((l) => [l.label, l.type]));
      assert.ok(
        typeEquals(
          byLabel.circle,
          obj({ tag: required(lit('circle')), radius: required(union(lit(1), lit(2))) }),
        ),
      );
    },
  },
};
