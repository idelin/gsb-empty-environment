// Deterministic synthetic system-log generator with heavy repetition,
// mimicking the redundancy of real production logs.

const HOSTS = ['web-01', 'web-02', 'api-gateway', 'db-primary', 'cache-03'];
const SERVICES = ['nginx', 'app-server', 'postgres', 'redis', 'auth-service'];
const MESSAGES = [
  'connection accepted from upstream',
  'request completed status=200 duration_ms=12',
  'request completed status=404 duration_ms=3',
  'slow query detected duration_ms=1842 statement="SELECT * FROM sessions"',
  'retrying upstream connection attempt=3 backoff_ms=400',
  'TLS handshake completed cipher=TLS_AES_256_GCM_SHA384',
  'health check passed service=database',
  'cache hit ratio=0.97 keys=1048576',
  'garbage collection pause duration_ms=48',
  'authentication succeeded method=oauth2 realm=internal',
];

// Tiny deterministic PRNG (mulberry32) so runs are reproducible.
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function generateSystemLog(targetBytes: number, seed = 42): Buffer {
  const rand = mulberry32(seed);
  const lines: string[] = [];
  let total = 0;
  let seq = 0;
  while (total < targetBytes) {
    const ts = new Date(1_700_000_000_000 + seq * 137).toISOString();
    const host = HOSTS[Math.floor(rand() * HOSTS.length)];
    const svc = SERVICES[Math.floor(rand() * SERVICES.length)];
    const msg = MESSAGES[Math.floor(rand() * MESSAGES.length)];
    const pid = 1000 + Math.floor(rand() * 20);
    const line = `${ts} ${host} ${svc}[${pid}]: ${msg} req_id=${seq}\n`;
    lines.push(line);
    total += line.length;
    seq++;
  }
  return Buffer.from(lines.join(''), 'utf8');
}
