import { DimensionVector } from './dimension.ts';
import { Unit } from './unit.ts';
import { UnitRegistry } from './registry.ts';
import { Quantity } from './quantity.ts';
import type { RationalLike } from './rational.ts';

const L = DimensionVector.base('L');
const M = DimensionVector.base('M');
const T = DimensionVector.base('T');
const I = DimensionVector.base('I');
const THETA = DimensionVector.base('Θ');
const N = DimensionVector.base('N');
const J = DimensionVector.base('J');

/** 构造一个含 SI 基准单位与常用导出单位的注册表。 */
export function createSIRegistry(): UnitRegistry {
  const registry = new UnitRegistry();

  registry.registerBaseUnit(new Unit({ symbol: 'm', name: '米', dimension: L, scale: 1 }));
  registry.registerBaseUnit(new Unit({ symbol: 'kg', name: '千克', dimension: M, scale: 1 }));
  registry.registerBaseUnit(new Unit({ symbol: 's', name: '秒', dimension: T, scale: 1 }));
  registry.registerBaseUnit(new Unit({ symbol: 'A', name: '安培', dimension: I, scale: 1 }));
  registry.registerBaseUnit(new Unit({ symbol: 'K', name: '开尔文', dimension: THETA, scale: 1 }));
  registry.registerBaseUnit(new Unit({ symbol: 'mol', name: '摩尔', dimension: N, scale: 1 }));
  registry.registerBaseUnit(new Unit({ symbol: 'cd', name: '坎德拉', dimension: J, scale: 1 }));

  // 长度：比例全部是精确十进制/整数，1 cm = 1/100 m。
  registry.registerUnit(new Unit({ symbol: 'mm', name: '毫米', dimension: L, scale: '0.001' }));
  registry.registerUnit(new Unit({ symbol: 'cm', name: '厘米', dimension: L, scale: '0.01' }));
  registry.registerUnit(new Unit({ symbol: 'dm', name: '分米', dimension: L, scale: '0.1' }));
  registry.registerUnit(new Unit({ symbol: 'km', name: '千米', dimension: L, scale: 1000 }));
  registry.registerUnit(new Unit({ symbol: 'in', name: '英寸', dimension: L, scale: '0.0254' }));
  registry.registerUnit(new Unit({ symbol: 'ft', name: '英尺', dimension: L, scale: '0.3048' }));

  // 质量、时间。
  registry.registerUnit(new Unit({ symbol: 'g', name: '克', dimension: M, scale: '0.001' }));
  registry.registerUnit(new Unit({ symbol: 'min', name: '分钟', dimension: T, scale: 60 }));
  registry.registerUnit(new Unit({ symbol: 'h', name: '小时', dimension: T, scale: 3600 }));

  // 温度：°C 与 °F 带绝对零点偏移；差量转换时偏移不参与。
  registry.registerUnit(
    new Unit({ symbol: '°C', name: '摄氏度', dimension: THETA, scale: 1, offset: '273.15' }),
  );
  registry.registerUnit(
    new Unit({
      symbol: '°F',
      name: '华氏度',
      dimension: THETA,
      scale: '5/9',
      offset: '45967/180', // 255.37222…，使 32 °F = 273.15 K
    }),
  );

  return registry;
}

/** 便捷构造：在指定注册表上创建绝对量。 */
export function absolute(registry: UnitRegistry, value: RationalLike, symbol: string): Quantity {
  return Quantity.absolute(value, registry.get(symbol), registry);
}

/** 便捷构造：在指定注册表上创建差量。 */
export function delta(registry: UnitRegistry, value: RationalLike, symbol: string): Quantity {
  return Quantity.delta(value, registry.get(symbol), registry);
}
