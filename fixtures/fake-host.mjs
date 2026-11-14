// Fake native messaging host used by tests. Speaks the 4-byte LE length-prefixed
// JSON protocol on stdin/stdout. Mode is selected via argv[2].
const mode = process.argv[2] || 'echo';

let buffer = Buffer.alloc(0);

process.stdin.on('data', (chunk) => {
  buffer = Buffer.concat([buffer, chunk]);
  while (buffer.length >= 4) {
    const length = buffer.readUInt32LE(0);
    if (buffer.length < 4 + length) return;
    const message = JSON.parse(buffer.subarray(4, 4 + length).toString('utf8'));
    buffer = buffer.subarray(4 + length);
    handle(message);
  }
});

function send(value) {
  const body = Buffer.from(JSON.stringify(value), 'utf8');
  const header = Buffer.alloc(4);
  header.writeUInt32LE(body.length, 0);
  process.stdout.write(Buffer.concat([header, body]));
}

function handle(message) {
  switch (mode) {
    case 'echo':
      send({ echo: message });
      break;
    case 'big':
      send({ blob: 'x'.repeat(2 * 1024 * 1024) });
      break;
    case 'slow':
      setTimeout(() => send({ late: true }), 60_000);
      break;
    case 'garbage': {
      const junk = Buffer.from('this is not json');
      const header = Buffer.alloc(4);
      header.writeUInt32LE(junk.length, 0);
      process.stdout.write(Buffer.concat([header, junk]));
      break;
    }
    case 'exit':
      process.exit(1);
      break;
    default:
      send({ echo: message });
  }
}
