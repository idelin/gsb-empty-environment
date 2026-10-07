'use strict';

const crypto = require('crypto');

function sha256(...parts) {
  const hash = crypto.createHash('sha256');
  for (const part of parts) {
    const buf = Buffer.isBuffer(part) ? part : Buffer.from(String(part), 'utf8');
    hash.update(buf);
    hash.update('\0');
  }
  return hash.digest('hex');
}

module.exports = { sha256 };
