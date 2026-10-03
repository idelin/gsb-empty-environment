import { defineMessage } from '../src/schema.ts';

export const Address = defineMessage('Address', [
  { number: 1, name: 'city', type: 'string' },
  { number: 2, name: 'zip', type: 'fixed32' },
]);

export const Person = defineMessage('Person', [
  { number: 1, name: 'id', type: 'uint64' },
  { number: 2, name: 'name', type: 'string', required: true },
  { number: 3, name: 'email', type: 'string' },
  { number: 4, name: 'scores', type: 'sint32', repeated: true, packed: true },
  { number: 5, name: 'address', type: 'message', message: Address },
  { number: 6, name: 'tags', type: 'string', repeated: true },
  { number: 7, name: 'lucky', type: 'bool', repeated: true, packed: true },
  { number: 8, name: 'balance', type: 'sfixed64' },
]);

export const samplePerson = {
  id: 9007199254740993n,
  name: 'Ada',
  email: 'ada@example.com',
  scores: [1, -1, 300, -2147483648],
  address: { city: 'hz', zip: 310000 },
  tags: ['x', 'yz'],
  lucky: [true, false, true],
  balance: -123456789012345n,
};
