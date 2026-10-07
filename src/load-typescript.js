'use strict';

const CANDIDATE_PATHS = [
  process.env.TYPESCRIPT_PATH,
  '/Applications/Cursor.app/Contents/Resources/app/extensions/node_modules/typescript/lib/typescript.js',
].filter(Boolean);

function loadTypeScript() {
  try {
    return require('typescript');
  } catch (err) {
    // not resolvable from here; try fallback locations
  }
  for (const candidate of CANDIDATE_PATHS) {
    try {
      return require(candidate);
    } catch (err) {
      // try next candidate
    }
  }
  throw new Error(
    'Unable to locate the TypeScript compiler. ' +
      'Install the "typescript" package or set TYPESCRIPT_PATH to the absolute path of typescript.js.'
  );
}

module.exports = { loadTypeScript };
