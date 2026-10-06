import { checkPrecision } from '../src/precision.js';
import {
  lit,
  obj,
  union,
  required,
  optional,
  enumerate,
  valueKey,
} from '../src/types.js';
import { get, val, eq, iff, leaf } from '../src/ast.js';

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

const Event = union(
  obj({ tag: required(lit('click')), x: required(lit(0)) }),
  obj({ tag: optional(union(lit('scroll'), lit('hover'))), y: required(lit(0)) }),
  obj({ name: required(lit('anon')) }),
);

const eventProgram = iff(eq(get('tag'), val('scroll')), leaf('scroll'), leaf('other'));

export default {
  name: 'precision',
  cases: {
    '完整值集合上静态剩余类型精确(判别联合)'(assert) {
      const values = enumerate(Shape);
      assert.equal(values.length, 4);
      const r = checkPrecision(Shape, shapeProgram, values);
      assert.ok(r.pass);
      assert.deepEqual(
        r.leaves.map((l) => l.status),
        ['exact', 'exact', 'exact'],
      );
    },

    '完整值集合上静态剩余类型精确(含缺失 tag)'(assert) {
      const values = enumerate(Event);
      assert.deepEqual(
        values.map(valueKey).sort(),
        [
          '{"name":"anon"}',
          '{"tag":"click","x":0}',
          '{"tag":"hover","y":0}',
          '{"tag":"scroll","y":0}',
          '{"y":0}',
        ],
      );
      const r = checkPrecision(Event, eventProgram, values);
      assert.ok(r.pass);
      const scroll = r.leaves.find((l) => l.label === 'scroll');
      assert.equal(scroll.status, 'exact');
      const other = r.leaves.find((l) => l.label === 'other');
      assert.equal(other.status, 'exact');
    },

    '值集合不完整时报告 imprecise(spurious)'(assert) {
      const values = enumerate(Shape).filter((v) => !(v.tag === 'rect'));
      const r = checkPrecision(Shape, shapeProgram, values);
      assert.ok(!r.pass);
      const rect = r.leaves.find((l) => l.label === 'rect');
      assert.equal(rect.status, 'imprecise');
      assert.deepEqual(rect.spurious, ['{"h":2,"tag":"rect","w":1}']);
    },

    '输入类型之外的值报告 unsound(missing)并标记 outsideInput'(assert) {
      const values = [...enumerate(Shape), { tag: 'hexagon', edges: 6 }];
      const r = checkPrecision(Shape, shapeProgram, values);
      assert.ok(!r.pass);
      assert.equal(r.outsideInput.length, 1);
      const rect = r.leaves.find((l) => l.label === 'rect');
      assert.equal(rect.status, 'unsound');
      assert.deepEqual(rect.missing, ['{"edges":6,"tag":"hexagon"}']);
    },

    '不可达叶子动态值集合为空时判定 exact'(assert) {
      const prog = iff(
        eq(get('tag'), val('circle')),
        iff(eq(get('tag'), val('square')), leaf('impossible'), leaf('circle')),
        leaf('other'),
      );
      const r = checkPrecision(Shape, prog, enumerate(Shape));
      assert.ok(r.pass);
      assert.equal(r.unreachable.length, 1);
      const impossible = r.leaves.find((l) => l.label === 'impossible');
      assert.equal(impossible.reachable, false);
      assert.equal(impossible.status, 'exact');
    },
  },
};
