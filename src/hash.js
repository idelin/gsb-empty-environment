'use strict';

const { createHash } = require('crypto');

/** Stable sha256 hex digest of a string. */
function hash(text) {
  return createHash('sha256').update(text).digest('hex');
}

module.exports = { hash };
