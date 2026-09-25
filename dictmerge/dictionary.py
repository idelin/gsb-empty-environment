"""Versioned local dictionary with stable term identity.

A local dictionary maps codes (contiguous integers) to string terms.
Each term also carries a stable ``term_id`` that never changes for the
lifetime of the dictionary, even across retraction and re-encoding, so
that a term keeps its identity while codes may be reassigned.

Appending new terms does not change the meaning of existing codes, so it
does not bump the version.  Retraction changes what may be encoded, so it
bumps the version; old snapshots remain valid for decoding old batches.

The lookup index is built on a pluggable hash function so tests can force
hash collisions; correctness never depends on hash uniqueness.
"""


class _Term:
    __slots__ = ("term_id", "value", "retracted")

    def __init__(self, term_id, value, retracted=False):
        self.term_id = term_id
        self.value = value
        self.retracted = retracted


class LocalDictionary:
    def __init__(self, version=1, hash_fn=None):
        self.version = version
        self._hash_fn = hash_fn or hash
        self._terms = []    # code -> _Term
        self._buckets = {}  # hash(value) -> [code, ...]
        self._next_term_id = 0

    # -- internal ---------------------------------------------------------
    def _bucket(self, value):
        return self._buckets.setdefault(self._hash_fn(value), [])

    def _append(self, value):
        code = len(self._terms)
        self._terms.append(_Term(self._next_term_id, value))
        self._next_term_id += 1
        self._bucket(value).append(code)
        return code

    # -- public API -------------------------------------------------------
    def add(self, value):
        """Return the code for ``value``, inserting it if necessary."""
        for code in self._bucket(value):
            term = self._terms[code]
            if term.value == value and not term.retracted:
                return code
        return self._append(value)

    def add_alias(self, value):
        """Force a second code for the same value (same term, new code)."""
        return self._append(value)

    def retract(self, value):
        """Withdraw a term: it may no longer be encoded, but old codes
        still decode.  Bumps the dictionary version."""
        for term in self._terms:
            if term.value == value and not term.retracted:
                term.retracted = True
                self.version += 1
                return
        raise KeyError(value)

    def encode(self, value):
        from .errors import TermRetractedError

        for code in self._bucket(value):
            term = self._terms[code]
            if term.value == value:
                if term.retracted:
                    raise TermRetractedError(value)
                return code
        raise KeyError(value)

    def decode(self, code):
        if code < 0 or code >= len(self._terms):
            raise KeyError(code)
        return self._terms[code].value

    def is_retracted(self, code):
        return self._terms[code].retracted

    def term_id(self, code):
        return self._terms[code].term_id

    def __len__(self):
        return len(self._terms)

    # -- snapshots --------------------------------------------------------
    def to_dict(self):
        return {
            "version": self.version,
            "next_term_id": self._next_term_id,
            "terms": [
                [t.term_id, t.value, t.retracted] for t in self._terms
            ],
        }

    @classmethod
    def from_dict(cls, payload, hash_fn=None):
        d = cls(version=payload["version"], hash_fn=hash_fn)
        for term_id, value, retracted in payload["terms"]:
            d._terms.append(_Term(term_id, value, retracted))
            d._bucket(value).append(len(d._terms) - 1)
        d._next_term_id = payload["next_term_id"]
        return d
