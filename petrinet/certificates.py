"""Certificate store with incremental revalidation.

Certificates are cached together with the net version they were checked
against.  When transitions are added / removed / replaced, the net records
the affected transition ids as "dirty"; revalidate() then re-checks each
cached certificate only against those dirty transitions instead of the
whole net.  Certificates that were invalid become fully re-verified (a
structural fix may have repaired them).
"""
from __future__ import annotations

from .invariants import verify_certificate


class CertificateStore:
    def __init__(self, net):
        self.net = net
        self._certs = {}
        self._valid = {}
        self._checked_version = -1

    def register(self, cert):
        """Verify a certificate against the current net and cache it."""
        ok = verify_certificate(self.net, cert)
        self._certs[cert.name] = cert
        self._valid[cert.name] = ok
        self._checked_version = self.net.version
        return ok

    def revalidate(self):
        """Incrementally re-check cached certificates after net edits.

        Only transitions modified since the last validation are re-verified
        for certificates that were valid; invalid ones get a full re-check.
        Returns {certificate_name: is_valid} and clears the dirty set.
        """
        dirty = self.net.dirty_transitions()
        for name, cert in self._certs.items():
            if self._valid.get(name, False) and dirty:
                self._valid[name] = verify_certificate(
                    self.net, cert, only_transitions=dirty)
            elif not self._valid.get(name, False):
                self._valid[name] = verify_certificate(self.net, cert)
        self.net.clear_dirty()
        self._checked_version = self.net.version
        return dict(self._valid)

    def is_valid(self, name):
        return self._valid.get(name, False)

    def valid_certificates(self):
        return [self._certs[n] for n, ok in self._valid.items() if ok]
