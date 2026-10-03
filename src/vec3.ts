export interface Vec3 {
  x: number;
  y: number;
  z: number;
}

export const vec3 = (x: number, y: number, z: number): Vec3 => ({ x, y, z });

export const add = (a: Vec3, b: Vec3): Vec3 => vec3(a.x + b.x, a.y + b.y, a.z + b.z);

export const sub = (a: Vec3, b: Vec3): Vec3 => vec3(a.x - b.x, a.y - b.y, a.z - b.z);

export const scale = (a: Vec3, s: number): Vec3 => vec3(a.x * s, a.y * s, a.z * s);

export const dot = (a: Vec3, b: Vec3): number => a.x * b.x + a.y * b.y + a.z * b.z;

export const lengthSq = (a: Vec3): number => dot(a, a);

export const length = (a: Vec3): number => Math.sqrt(lengthSq(a));

export const distance = (a: Vec3, b: Vec3): number => length(sub(a, b));

/** Position of a linear mover at absolute time t. */
export const extrapolate = (origin: Vec3, velocity: Vec3, t: number): Vec3 =>
  add(origin, scale(velocity, t));
