import { MessageSchema } from '../src/schema.ts';
import { LockFreeQueue } from '../src/queue.ts';

const Address = new MessageSchema('Address', [
  { name: 'street', number: 1, type: 'string', required: true },
  { name: 'zip', number: 2, type: 'sint32' },
]);

const Person = new MessageSchema('Person', [
  { name: 'id', number: 1, type: 'uint64', required: true },
  { name: 'name', number: 2, type: 'string', required: true },
  { name: 'scores', number: 3, type: 'sint32', repeated: true, packed: true },
  { name: 'address', number: 4, type: Address },
  { name: 'balance', number: 5, type: 'sfixed64' },
  { name: 'active', number: 6, type: 'bool' },
]);

const person = {
  id: 9007199254740993n,
  name: 'Ada Lovelace',
  scores: [-3, 0, 42, 2147483647],
  address: { street: '12 Analytical Way', zip: -94107 },
  balance: -5000000000n,
  active: true,
};

const bytes = Person.encode(person);
console.log(`encoded ${bytes.length} bytes:`);
console.log(`  ${Buffer.from(bytes).toString('hex')}`);

const decoded = Person.decode(bytes);
console.log('decoded structure:');
console.dir(decoded, { depth: null });

const truncated = bytes.subarray(0, bytes.length - 3);
try {
  Person.decode(truncated);
  console.log('truncated stream decoded (prefix formed a valid message)');
} catch (err) {
  console.log(`truncated stream safely rejected: ${(err as Error).message}`);
}

const queue = new LockFreeQueue<Uint8Array>(4);
queue.enqueue(bytes);
queue.enqueue(Person.encode({ id: 2n, name: 'Grace Hopper' }));
console.log(`queue size=${queue.size}`);
const first = queue.dequeue();
console.log(`dequeued ${first?.length} bytes, queue size=${queue.size}`);
console.log(`live pool slots after dequeue: ${queue.countLiveSlots()}`);
