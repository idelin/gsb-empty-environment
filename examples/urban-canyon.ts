/**
 * Demo: match a multipath-corrupted GPS trace in an urban canyon
 * back onto the road network with the HMM/Viterbi matcher.
 *
 * Run: npm run example
 */
import { haversine } from "../src/geometry.ts";
import { MapMatcher } from "../src/matcher.ts";
import { buildGridCity, toLonLat } from "./grid-city.ts";

const { network, truth, trace } = buildGridCity();
const matcher = new MapMatcher(network, {
  sigma: 20, // urban-canyon GPS std-dev (m)
  beta: 10,
  candidateRadius: 80,
  maxSpeed: 60,
});

const results = matcher.match(trace);

console.log("HMM map matching — urban canyon multipath scenario");
console.log("=".repeat(78));
console.log(
  "idx | raw drift (m) | snapped residual (m) | matched edge      | snap XY (m)",
);
console.log("-".repeat(78));

let sumRaw = 0;
let sumMatched = 0;
results.forEach((r, i) => {
  const truePos = toLonLat(truth[i].x, truth[i].y);
  const rawDrift = haversine(r.observation.point, truePos);
  const matchedErr = haversine(r.candidate.snapped, truePos);
  sumRaw += rawDrift;
  sumMatched += matchedErr;
  const snapX = ((r.candidate.snapped.lon - 121.4) * 111320 * Math.cos(31.2 * Math.PI / 180)).toFixed(1);
  const snapY = ((r.candidate.snapped.lat - 31.2) * 111320).toFixed(1);
  console.log(
    `${String(i).padStart(3)} | ${rawDrift.toFixed(1).padStart(13)} | ` +
      `${matchedErr.toFixed(1).padStart(20)} | ${r.edgeId.padEnd(17)} | ` +
      `(${snapX}, ${snapY})`,
  );
});
console.log("-".repeat(78));
console.log(
  `mean | ${(sumRaw / results.length).toFixed(1).padStart(13)} | ` +
    `${(sumMatched / results.length).toFixed(1).padStart(20)} |`,
);
console.log("\nCanyon fixes #3-#6 were biased 35-55 m toward North Ave;");
console.log("Viterbi keeps them on Main St via topology + speed consistency.");
