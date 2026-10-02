import type { Vec3 } from './vec3.ts';
import { normalize } from './vec3.ts';

/** A ray: origin + normalized direction, with precomputed inverse direction for slab tests. */
export class Ray {
  readonly origin: Vec3;
  readonly direction: Vec3;
  readonly invDirection: Vec3;

  constructor(origin: Vec3, direction: Vec3) {
    this.origin = origin;
    this.direction = normalize(direction);
    this.invDirection = {
      x: 1 / this.direction.x,
      y: 1 / this.direction.y,
      z: 1 / this.direction.z,
    };
  }

  at(t: number): Vec3 {
    return {
      x: this.origin.x + this.direction.x * t,
      y: this.origin.y + this.direction.y * t,
      z: this.origin.z + this.direction.z * t,
    };
  }
}
