import { validate, verifyDiagnosis } from '../src/index.ts';
import type { Json, Schema } from '../src/index.ts';

const schema: Schema = {
  type: 'object',
  required: ['name'],
  properties: {
    name: { type: 'string', minLength: 1 },
    id: { anyOf: [{ type: 'string', pattern: '^ID-' }, { type: 'integer' }] },
    contact: {
      oneOf: [
        {
          type: 'object',
          required: ['email'],
          properties: { email: { type: 'string', pattern: '@' } },
        },
        {
          type: 'object',
          required: ['phone'],
          properties: { phone: { type: 'string', minLength: 7 } },
        },
      ],
    },
  },
};

function show(title: string, instance: Json, options = {}): void {
  const errors = validate(schema, instance, options);
  console.log(`\n=== ${title} ===`);
  console.log('instance:', JSON.stringify(instance));
  if (errors.length === 0) {
    console.log('valid: true (no errors)');
    return;
  }
  console.log('valid: false');
  console.log(JSON.stringify(errors, null, 2));
  const verification = verifyDiagnosis(schema, instance, errors);
  console.log('diagnosis sufficient:', verification.sufficient, verification.problems);
}

show('1. valid instance (oneOf branch 0 matches, anyOf branch 1 matches)', {
  name: 'ada',
  id: 42,
  contact: { email: 'ada@example.com' },
});

show('2. oneOf ambiguity: contact matches BOTH branches', {
  name: 'ada',
  id: 'ID-7',
  contact: { email: 'ada@example.com', phone: '1234567' },
});

show('3. oneOf zero matches: minimal error set refutes both branches', {
  name: 'ada',
  id: 'ID-7',
  contact: { phone: '12' },
});

show('4. anyOf zero matches with a shared rule (dedup by rule identity)', {
  name: 'ada',
  id: 3.5,
  contact: { email: 'ada@example.com' },
});

show('5. error budget truncates sole evidence -> complete: false', {
  name: 'ada',
  id: 'ID-7',
  contact: { phone: '12' },
}, { maxErrors: 1 });
