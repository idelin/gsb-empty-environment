import { Layer, Scene } from './layer.ts';
import { Compositor, type DirtyRegion } from './compositor.ts';
import { render } from './renderer.ts';

function fmt(dirty: DirtyRegion): string {
  const { x, y, w, h } = dirty.rect;
  const area = w <= 0 || h <= 0 ? '空(无需重绘)' : `(${x},${y}) ${w}x${h}`;
  return `脏区=${area}  原因=[${dirty.reasons.join(', ')}]`;
}

const scene = new Scene(24, 16);
const compositor = new Compositor(scene);

scene.add(Layer.solid(24, 16, [24, 28, 36, 255], 'bg'));

const group = new Layer(0, 0, 'group');
group.transform = { tx: 2, ty: 2, sx: 1, sy: 1 };

const sprite = Layer.solid(6, 4, [220, 120, 60, 255], 'sprite');
sprite.transform = { tx: 1, ty: 1, sx: 1, sy: 1 };
sprite.shadow = { radius: 2, offsetX: 1, offsetY: 1, color: [0, 0, 0, 160] };
sprite.setPixel(2, 1, 255, 240, 120, 255);
sprite.setPixel(3, 2, 255, 240, 120, 255);
group.add(sprite);

const badge = Layer.solid(2, 2, [80, 160, 255, 255], 'badge');
badge.transform = { tx: 8, ty: 2, sx: 1, sy: 1 };
group.add(badge);
scene.add(group);

const tint = Layer.solid(24, 16, [255, 220, 140, 40], 'tint');
tint.blendMode = 'multiply';
scene.add(tint);

console.log('场景 24x16 | 图层: bg, group{sprite(阴影r=2), badge}, tint(multiply)\n');

console.log('[1] 初始渲染      ', fmt(compositor.initialDirty()));

sprite.setPixel(1, 1, 0, 255, 128, 255);
console.log('[2] 局部编辑 sprite (1,1,1,1):', fmt(compositor.editContent(sprite, { x: 1, y: 1, w: 1, h: 1 })));

console.log('[3] 移动 group → (6,4)   :', fmt(compositor.setTransform(group, { tx: 6, ty: 4, sx: 1, sy: 1 })));

console.log('[4] group 透明度 → 0.6 :', fmt(compositor.setOpacity(group, 0.6)));

console.log('[5] sprite 裁剪 → (0,0,3,4):', fmt(compositor.setClip(sprite, { x: 0, y: 0, w: 3, h: 4 })));

sprite.setPixel(4, 1, 255, 0, 0, 255);
console.log('[6] 裁剪外修改 sprite (4,1):', fmt(compositor.editContent(sprite, { x: 4, y: 1, w: 1, h: 1 })));

const buf = render(scene);
const ramp = ' .:-=+*#%@';
console.log('\n最终渲染 (ASCII, 按亮度):');
for (let y = 0; y < scene.height; y++) {
  let line = '';
  for (let x = 0; x < scene.width; x++) {
    const i = (y * scene.width + x) * 4;
    const lum = (0.2126 * buf[i] + 0.7152 * buf[i + 1] + 0.0722 * buf[i + 2]) * (buf[i + 3] / 255);
    line += ramp[Math.min(ramp.length - 1, Math.floor((lum / 255) * ramp.length))];
  }
  console.log(line);
}
