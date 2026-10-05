import { extractContours, rasterize, masksEqual } from './dist/index.js';

const art = [
  '#####',
  '#...#',
  '#.#.#',
  '#...#',
  '#####',
];
const width = art[0].length;
const height = art.length;
const data = new Uint8Array(width * height);
art.forEach((row, y) => {
  for (let x = 0; x < width; x++) data[y * width + x] = row[x] === '#' ? 1 : 0;
});

const { contours, nodes, roots } = extractContours({ width, height, data });
for (let i = 0; i < contours.length; i++) {
  const c = contours[i];
  console.log(
    `轮廓#${i} ${c.isHole ? '孔洞' : '外边界'} 深度=${nodes[i].depth} ` +
      `父=${nodes[i].parent} 顶点=${c.vertices.map((p) => `(${p.x},${p.y})`).join(' ')}`,
  );
}
console.log('根轮廓:', roots);
console.log('重建一致:', masksEqual(rasterize(contours, width, height), { width, height, data }));
