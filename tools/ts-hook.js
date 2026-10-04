'use strict';
// require() hook that lets Node execute the repo's .ts files directly by
// stripping type-only syntax (see strip-types.js for the supported subset).
const fs = require('fs');
const stripTypes = require('./strip-types');

require.extensions['.ts'] = function (module, filename) {
  const src = fs.readFileSync(filename, 'utf8');
  const js = stripTypes(src, filename);
  module._compile(js, filename);
};
