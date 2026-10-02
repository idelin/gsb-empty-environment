/**
 * Start a real HTTP exporter (for use outside sandboxed environments).
 *
 * Run: npm run serve
 * Then: curl localhost:9464/metrics
 */
import { TokenBucketRateLimiter } from "../src/rate-limiter.ts";
import { MetricsExporter } from "../src/exporter.ts";

const limiter = new TokenBucketRateLimiter({ capacity: 100, refillPerSec: 1_000 });
const exporter = new MetricsExporter(limiter.metrics, { port: 9464 });
const { host, port } = await exporter.start();
console.log(`metrics: http://${host}:${port}/metrics`);

// Background traffic so there is something to observe.
setInterval(() => {
  for (let i = 0; i < 2_000; i++) limiter.tryAcquire();
}, 100);
