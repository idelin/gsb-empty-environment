"""Independent full-sort reference used to verify the index in tests."""
from .store import Entry


def reference_topk(records, spec, k, group_field):
    """records: {pk: values}. Returns {group: [pk, ...]} sorted per spec."""
    groups = {}
    for pk, values in records.items():
        groups.setdefault(values.get(group_field), []).append(Entry(pk, values))
    out = {}
    for g, entries in groups.items():
        entries.sort(key=spec.key())
        out[g] = [e.id for e in entries[:k]] if k > 0 else []
    return out
