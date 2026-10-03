/** Shared demo schema: multi-level nesting + packed repeated + fixed fields. */
import type { MessageSchema } from '../src/schema.ts';

export const POINT_SCHEMA: MessageSchema = {
  name: 'Point',
  fields: [
    { name: 'x', number: 1, type: 'sint32', required: true },
    { name: 'y', number: 2, type: 'sint32', required: true },
  ],
};

export const HEADER_SCHEMA: MessageSchema = {
  name: 'Header',
  fields: [
    { name: 'id', number: 1, type: 'uint64', required: true },
    { name: 'flags', number: 2, type: 'fixed32' },
    { name: 'origin', number: 3, type: 'message', message: POINT_SCHEMA },
  ],
};

export const ENVELOPE_SCHEMA: MessageSchema = {
  name: 'Envelope',
  fields: [
    { name: 'header', number: 1, type: 'message', message: HEADER_SCHEMA, required: true },
    { name: 'label', number: 2, type: 'string' },
    { name: 'samples', number: 3, type: 'sint32', repeated: true, packed: true },
    { name: 'tags', number: 4, type: 'string', repeated: true },
    { name: 'checksum', number: 5, type: 'fixed64' },
    { name: 'delta', number: 6, type: 'sint64' },
    { name: 'blob', number: 7, type: 'bytes' },
    { name: 'active', number: 8, type: 'bool' },
  ],
};

export const SAMPLE_VALUE = {
  header: {
    id: 0x1fffffffffffffn,
    flags: 0xdeadbeef,
    origin: { x: -123, y: 456 },
  },
  label: '节点-α',
  samples: [0, -1, 1, -300, 300, 2147483647, -2147483648],
  tags: ['alpha', 'β', ''],
  checksum: 0xffffffffffffffffn,
  delta: -9223372036854775808n,
  blob: new Uint8Array([0, 1, 2, 250, 251, 255]),
  active: true,
};
