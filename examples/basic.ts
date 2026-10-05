import { createSpline } from '../src/index.ts';

// 非均匀节点、含平台与陡变的单调数据
const xs = [0, 0.5, 1, 2, 2.5, 6];
const ys = [0, 0, 0.5, 3.5, 3.5, 4];

const spline = createSpline(xs, ys);

console.log('nodal derivatives:', spline.derivatives);
console.log('evaluate(1.5) =', spline.evaluate(1.5));
console.log('derivative(1.5) =', spline.derivative(1.5));

for (const seg of spline.getCoefficients()) {
  console.log(
    `segment ${seg.index} on [${seg.x0}, ${seg.x1}]: r=${seg.r}`,
    `N=${[...seg.numerator]} D=${[...seg.denominator]} W=${[...seg.derivativeNumerator]}`,
  );
}

const report = spline.verify();
console.log('monotonicity holds:', report.holds, 'margin:', report.certifiedMinMargin);
for (const cert of spline.getMonotonicityEvidence()) {
  console.log(`segment ${cert.segmentIndex} (${cert.trend}): ${cert.proof}`);
}
