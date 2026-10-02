export function cellIdFor(
  lat: number,
  lon: number,
  precision: number,
): string {
  if (
    !Number.isFinite(lat) ||
    !Number.isFinite(lon) ||
    lat < -90 ||
    lat > 90 ||
    lon < -180 ||
    lon > 180
  ) {
    throw new RangeError(`invalid coordinates: (${lat}, ${lon})`);
  }
  return `${lat.toFixed(precision)}:${lon.toFixed(precision)}`;
}
