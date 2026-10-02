import { IncrementalMaxFlow, type MinCutCertificate } from "../src/incremental-max-flow.ts";

const NAMES = ["s", "a", "b", "d", "t"];

function show(title: string, cert: MinCutCertificate): void {
  const side = (nodes: number[]) => `{ ${nodes.map((n) => NAMES[n]).join(", ")} }`;
  console.log(`\n=== ${title} ===`);
  console.log(`max flow value : ${cert.maxFlow}`);
  console.log(`S side (reachable from s) : ${side(cert.sourceSide)}`);
  console.log(`T side                    : ${side(cert.sinkSide)}`);
  console.log("cut edges (S -> T, saturated):");
  for (const e of cert.cutEdges) {
    console.log(
      `  edge#${e.id}  ${NAMES[e.from]} -> ${NAMES[e.to]}  capacity=${e.capacity} flow=${e.flow}`,
    );
  }
  console.log(`cut capacity   : ${cert.cutCapacity}  (== max flow, certificate valid)`);
}

// Network:  s->a (5), s->b (5), a->b (5, key), a->d (1), d->t (1), b->t (10)
const mf = new IncrementalMaxFlow(5, 0, 4);
mf.addEdge(0, 1, 5); // #0 s->a
mf.addEdge(0, 2, 5); // #1 s->b
const keyEdge = mf.addEdge(1, 2, 5); // #2 a->b  (key edge)
mf.addEdge(1, 3, 1); // #3 a->d  (alternative outlet)
mf.addEdge(3, 4, 1); // #4 d->t
mf.addEdge(2, 4, 10); // #5 b->t

show("initial max flow", mf.computeMaxFlow());

console.log(`\n>>> key edge #${keyEdge} (a -> b): capacity 5 -> 2`);
console.log("    excess 3 units withdrawn along the residual network,");
console.log("    then alternative path s -> a -> d -> t recovers 1 unit.");
show("after decreasing key edge to 2", mf.decreaseCapacity(keyEdge, 3));

console.log("\n>>> restore key edge capacity 2 -> 5");
show("after increasing key edge back to 5", mf.increaseCapacity(keyEdge, 3));
