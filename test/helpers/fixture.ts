import { defineSchema, type MessageSchema } from '../../src/schema.ts';
import type { MessageObject } from '../../src/message.ts';

// message PhoneNumber { string number = 1; int32 type = 2; }
export const phoneSchema: MessageSchema = defineSchema('PhoneNumber', [
  { name: 'number', number: 1, kind: 'string', required: true },
  { name: 'type', number: 2, kind: 'int32', defaultValue: 0 },
]);

// message Person {
//   string name = 1; int32 id = 2; string email = 3;
//   repeated PhoneNumber phones = 4;
//   repeated sint32 scores = 5 [packed];
//   bytes avatar = 6;
//   fixed64 serial = 7;
//   double ratio = 8;
// }
export const personSchema: MessageSchema = defineSchema('Person', [
  { name: 'name', number: 1, kind: 'string', required: true },
  { name: 'id', number: 2, kind: 'int32' },
  { name: 'email', number: 3, kind: 'string', defaultValue: '' },
  { name: 'phones', number: 4, kind: 'message', repeated: true, message: phoneSchema },
  { name: 'scores', number: 5, kind: 'sint32', repeated: true, packed: true },
  { name: 'avatar', number: 6, kind: 'bytes' },
  { name: 'serial', number: 7, kind: 'fixed64' },
  { name: 'ratio', number: 8, kind: 'double' },
]);

// message AddressBook { repeated Person people = 1; uint32 version = 2; }
export const addressBookSchema: MessageSchema = defineSchema('AddressBook', [
  { name: 'people', number: 1, kind: 'message', repeated: true, message: personSchema },
  { name: 'version', number: 2, kind: 'uint32', defaultValue: 1 },
]);

// Recursive: message Node { Node child = 1; sint64 value = 2; }
export const nodeSchema: MessageSchema = defineSchema('Node', [
  { name: 'child', number: 1, kind: 'message', message: () => nodeSchema },
  { name: 'value', number: 2, kind: 'sint64' },
]);

export function sampleBook(): MessageObject {
  return {
    people: [
      {
        name: 'Alice',
        id: 123,
        email: 'alice@example.com',
        phones: [
          { number: '555-1234', type: 1 },
          { number: '555-5678', type: 2 },
        ],
        scores: [0, -1, 1, -2, 300, -2147483648, 2147483647],
        avatar: new Uint8Array([0xde, 0xad, 0xbe, 0xef, 0x00, 0xff]),
        serial: 0xffffffffffffffffn,
        ratio: 3.141592653589793,
      },
      {
        name: '张三',
        id: -7,
        phones: [{ number: '138-0000-0000', type: 0 }],
        scores: [],
        serial: 42n,
        ratio: -0.5,
      },
    ],
    version: 3,
  };
}
