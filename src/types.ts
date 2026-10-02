/** A single raw GNSS/IMU sample collected by the vehicle terminal. */
export interface TrajectoryPoint {
  /** Epoch milliseconds. */
  t: number;
  /** Latitude in degrees (WGS-84). */
  lat: number;
  /** Longitude in degrees (WGS-84). */
  lon: number;
  /** Elevation in meters. */
  ele: number;
  /** Ground speed in m/s. */
  speed: number;
  /** Heading in degrees, 0 = north, clockwise. */
  heading: number;
}

/** A point retained by the compressor, tagged with the trigger reason(s). */
export interface KeptPoint extends TrajectoryPoint {
  /** Why this point was retained (e.g. "turn-rate", "curve", "radius", "elevation", "max-gap", "boundary"). */
  reasons: string[];
}

/** Tunable thresholds for the adaptive sampler. */
export interface CompressorOptions {
  /** Yaw rate (deg/s) above which dense sampling kicks in (sharp turns, lane changes). Default 6. */
  turnRateDegPerSec?: number;
  /** Accumulated heading change (deg) since the last kept point that forces a keep. Default 8. */
  headingAccumDeg?: number;
  /** Instantaneous turn radius (m) below which a point is kept. Default 250. */
  minRadiusM?: number;
  /** Elevation change (m) versus the last kept point that forces a keep. Default 6. */
  eleStepM?: number;
  /** Maximum distance (m) between kept points; bounds shape error on long straights. Default 600. */
  maxGapM?: number;
  /** Maximum time gap (ms) between kept points. Default 60_000. */
  maxGapMs?: number;
}

export const DEFAULT_OPTIONS: Required<CompressorOptions> = {
  turnRateDegPerSec: 6,
  headingAccumDeg: 8,
  minRadiusM: 250,
  eleStepM: 6,
  maxGapM: 600,
  maxGapMs: 60_000,
};
