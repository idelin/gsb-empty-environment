import type { MessageDescriptor } from '../src/index.ts';

export const GeoPoint: MessageDescriptor = {
  name: 'GeoPoint',
  fields: [
    { no: 1, name: 'lat', type: 'double' },
    { no: 2, name: 'lng', type: 'double' },
  ],
};

export const Address: MessageDescriptor = {
  name: 'Address',
  fields: [
    { no: 1, name: 'city', type: 'string' },
    { no: 2, name: 'zip', type: 'sint32' },
    { no: 3, name: 'geo', type: 'message', message: GeoPoint },
  ],
};

export const Person: MessageDescriptor = {
  name: 'Person',
  fields: [
    { no: 1, name: 'name', type: 'string' },
    { no: 2, name: 'id', type: 'uint64' },
    { no: 3, name: 'scores', type: 'sint32', repeated: true, packed: true },
    { no: 4, name: 'address', type: 'message', message: Address },
    { no: 5, name: 'aliases', type: 'string', repeated: true },
    { no: 6, name: 'balance', type: 'sint64' },
    { no: 7, name: 'lucky', type: 'fixed64' },
    { no: 8, name: 'flags', type: 'bool', repeated: true, packed: true },
    { no: 9, name: 'previous', type: 'message', message: Address, repeated: true },
    { no: 10, name: 'samples', type: 'fixed32', repeated: true, packed: true },
  ],
};

export const samplePerson = {
  name: 'Ada',
  id: 9007199254740993n,
  scores: [-7, 0, 42, 300, -2147483648, 2147483647],
  address: { city: '上海', zip: -200000, geo: { lat: 31.2304, lng: 121.4737 } },
  aliases: ['ada', 'a.lovelace', ''],
  balance: -123456789012345n,
  lucky: 18446744073709551615n,
  flags: [true, false, true],
  previous: [
    { city: 'London', zip: 1, geo: { lat: 51.5074, lng: -0.1278 } },
    { city: 'Paris', zip: -2, geo: { lat: 48.8566, lng: 2.3522 } },
  ],
  samples: [0, 4294967295, 16777216],
};
