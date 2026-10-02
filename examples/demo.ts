/**
 * Demo: stream 1,000,000 taxi pickup points into the density cube, then ask
 * "which 10 tile centers had the highest density during last Friday's
 * evening peak (18:00-20:00)?" and print timing + memory usage.
 *
 * Run with: node examples/demo.ts   (Node >= 23.6, runs TypeScript natively)
 */
import { DensityAnalyzer, MS_PER_HOUR } from '../src/index.ts';

// --- City grid: central Beijing, 250m tiles -------------------------------
const analyzer = new DensityAnalyzer({
  grid: { minLat: 39.75, minLng: 116.20, maxLat: 40.05, maxLng: 116.60, tileSizeMeters: 250 },
  capacityHours: 24 * 31, // retain ~1 month of hourly layers
});

// --- Deterministic PRNG (mulberry32) so the demo is reproducible ----------
function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rand = mulberry32(42);
function gauss(): number {
  return (rand() + rand() + rand() + rand() - 2) / 2; // approx N(0, ~0.17)
}

// --- "Last Friday 18:00-20:00 local" relative to now ----------------------
function lastFridayPeak(): { start: number; end: number } {
  const now = new Date();
  const d = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const daysSinceFriday = (d.getDay() - 5 + 7) % 7 || 7; // strictly in the past
  d.setDate(d.getDate() - daysSinceFriday);
  const start = d.getTime() + 18 * MS_PER_HOUR;
  return { start, end: start + 2 * MS_PER_HOUR };
}
const peak = lastFridayPeak();

// --- Simulate 1M pickup points --------------------------------------------
// Hotspots (lat, lng, spread-deg, share): CBD, Sanlitun, Xidan, Wangjing...
const hotspots = [
  { lat: 39.908, lng: 116.457, spread: 0.006, share: 0.30 }, // Guomao/CBD
  { lat: 39.937, lng: 116.447, spread: 0.004, share: 0.20 }, // Sanlitun
  { lat: 39.912, lng: 116.374, spread: 0.005, share: 0.15 }, // Xidan
  { lat: 39.996, lng: 116.469, spread: 0.007, share: 0.10 }, // Wangjing
];
const N = 1_000_000;
const DAY = 24 * MS_PER_HOUR;

console.log(`Ingesting ${N.toLocaleString()} taxi pickup points...`);
const t0 = performance.now();
for (let i = 0; i < N; i++) {
  let lat: number;
  let lng: number;
  const pick = rand();
  let acc = 0;
  let hs: (typeof hotspots)[number] | undefined;
  for (const h of hotspots) {
    acc += h.share;
    if (pick < acc) {
      hs = h;
      break;
    }
  }
  if (hs) {
    lat = hs.lat + gauss() * hs.spread;
    lng = hs.lng + gauss() * hs.spread;
  } else {
    lat = 39.75 + rand() * 0.3; // uniform background
    lng = 116.2 + rand() * 0.4;
  }
  // 70% of trips land in the Friday 17:00-21:00 window, rest across the week.
  const ts =
    rand() < 0.7
      ? peak.start - MS_PER_HOUR + rand() * 4 * MS_PER_HOUR
      : peak.start - 3 * DAY + rand() * 6 * DAY;
  analyzer.addPoint(lat, lng, ts);
}
const ingestMs = performance.now() - t0;

// --- Query: Top-10 densest tile centers, last Friday 18:00-20:00 ----------
const q0 = performance.now();
const top = analyzer.queryTopK(peak.start, peak.end, 10);
const queryMs = performance.now() - q0;

const stats = analyzer.stats();
const rss = process.memoryUsage().rss;
console.log(`\nIngest:  ${ingestMs.toFixed(0)} ms (${(N / (ingestMs / 1000)).toFixed(0)} pts/s)`);
console.log(`Query:   ${queryMs.toFixed(1)} ms`);
console.log(
  `Memory:  cube bound ${(stats.cubeMemoryBytes / 1024 / 1024).toFixed(1)} MiB ` +
    `(${stats.hoursStored} h x ${stats.cells} cells), process RSS ${(rss / 1024 / 1024).toFixed(1)} MiB`,
);
console.log(`Stats:   ${stats.pointsAdded.toLocaleString()} added, ${stats.dropped} dropped, ${stats.evictions} evictions`);

const fmt = (ms: number): string => {
  const d = new Date(ms);
  const p = (x: number): string => String(x).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:00`;
};
console.log(
  `\nTop-10 density hotspots, ${fmt(peak.start)} - ${fmt(peak.end)} (last Friday evening peak):`,
);
for (const h of top) {
  console.log(
    `  #${String(h.rank).padStart(2)}  lat ${h.lat.toFixed(5)}, lng ${h.lng.toFixed(5)}  weight ${h.weight.toFixed(0)}`,
  );
}
