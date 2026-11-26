/**
 * Cross-origin isolation & XS-Leak policy adjudication toolkit.
 *
 * Pure TypeScript, zero runtime dependencies, offline by construction.
 * Import the individual engines or the whole surface from this barrel.
 */

export * from './origin.ts';
export * from './coop.ts';
export * from './coep.ts';
export * from './cors.ts';
export * from './fetch-metadata.ts';
export * from './searcher.ts';
export * from './scenario-matrix.ts';

export const PACKAGE_VERSION = '1.0.0';
