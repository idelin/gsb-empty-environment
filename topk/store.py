"""Per-group segmented store.

The best `window_size` entries stay in a bounded in-memory window; all
remaining candidates live in sorted page files on disk.  Deleting a window
entry backfills from the head page only, so a delete never scans the whole
group.  Every page-file read/write is counted in BlockStats.
"""
import bisect
import json
import os


class Entry:
    __slots__ = ("id", "values")

    def __init__(self, id, values):
        self.id = id
        self.values = dict(values)

    def to_json(self):
        return {"id": self.id, "values": self.values}

    @classmethod
    def from_json(cls, data):
        return cls(data["id"], data["values"])


class BlockStats:
    def __init__(self):
        self.blocks_read = 0
        self.blocks_written = 0

    def reset(self):
        self.blocks_read = 0
        self.blocks_written = 0

    def snapshot(self):
        return {"blocks_read": self.blocks_read,
                "blocks_written": self.blocks_written}


class GroupStore:
    """Invariant: window holds the best min(window_size, total) entries;
    pages (sorted chain of page files) hold the rest, all worse."""

    def __init__(self, spec, group, directory, window_size, page_size, stats):
        self.spec = spec
        self.group = group
        self.dir = directory
        self.window_size = max(1, window_size)
        self.page_size = max(2, page_size)
        self.stats = stats
        self.window = []   # sorted list[Entry], best first
        self.pages = []    # [{"file": name, "hi": Entry}] in key order
        self.loc = {}      # id -> "w" or page file name
        self.page_seq = 0
        os.makedirs(self.dir, exist_ok=True)

    def __len__(self):
        return len(self.loc)

    # -- page file helpers -------------------------------------------------
    def _new_page_name(self):
        self.page_seq += 1
        return "p%06d.jsonl" % self.page_seq

    def _read_page(self, name):
        self.stats.blocks_read += 1
        with open(os.path.join(self.dir, name), "r", encoding="utf-8") as fh:
            return [Entry.from_json(json.loads(line))
                    for line in fh if line.strip()]

    def _write_page(self, name, entries):
        self.stats.blocks_written += 1
        with open(os.path.join(self.dir, name), "w", encoding="utf-8") as fh:
            for e in entries:
                fh.write(json.dumps(e.to_json(), ensure_ascii=False) + "\n")

    def _drop_page_file(self, name):
        path = os.path.join(self.dir, name)
        if os.path.exists(path):
            os.remove(path)

    def _page_insert(self, entry):
        key = self.spec.key()
        if not self.pages:
            name = self._new_page_name()
            self._write_page(name, [entry])
            self.pages.append({"file": name, "hi": entry})
            self.loc[entry.id] = name
            return
        his = [p["hi"] for p in self.pages]
        idx = bisect.bisect_left(his, key(entry), key=key)
        if idx == len(self.pages):
            idx = len(self.pages) - 1
        page = self.pages[idx]
        entries = self._read_page(page["file"])
        bisect.insort(entries, entry, key=key)
        if len(entries) > 2 * self.page_size:
            mid = len(entries) // 2
            left, right = entries[:mid], entries[mid:]
            self._write_page(page["file"], left)
            new_name = self._new_page_name()
            self._write_page(new_name, right)
            page["hi"] = left[-1]
            self.pages.insert(idx + 1, {"file": new_name, "hi": right[-1]})
            for e in right:
                self.loc[e.id] = new_name
            if self.spec.compare(entry, left[-1]) > 0:
                self.loc[entry.id] = new_name
            else:
                self.loc[entry.id] = page["file"]
        else:
            self._write_page(page["file"], entries)
            page["hi"] = entries[-1]
            self.loc[entry.id] = page["file"]

    def _backfill(self):
        """Refill the window from the head page(s); touches only the
        leading block(s), never the whole group."""
        while len(self.window) < self.window_size and self.pages:
            need = self.window_size - len(self.window)
            head = self.pages[0]
            entries = self._read_page(head["file"])
            take = entries[:need]
            rest = entries[len(take):]
            self.window.extend(take)
            for e in take:
                self.loc[e.id] = "w"
            if rest:
                self._write_page(head["file"], rest)
                head["hi"] = rest[-1]
            else:
                self._drop_page_file(head["file"])
                self.pages.pop(0)

    # -- public operations ---------------------------------------------------
    def add(self, entry):
        if entry.id in self.loc:
            self.remove(entry.id)
        self._backfill()  # restore "pages => window full" invariant
        if self.window and (self.pages or len(self.window) >= self.window_size):
            if self.spec.compare(entry, self.window[-1]) > 0:
                self._page_insert(entry)
                return
        bisect.insort(self.window, entry, key=self.spec.key())
        self.loc[entry.id] = "w"
        while len(self.window) > self.window_size:
            self._page_insert(self.window.pop())

    def remove(self, entry_id):
        where = self.loc.get(entry_id)
        if where is None:
            return None
        del self.loc[entry_id]
        if where == "w":
            hit = None
            for i, e in enumerate(self.window):
                if e.id == entry_id:
                    hit = self.window.pop(i)
                    break
            self._backfill()  # promote the next best candidate
            return hit
        entries = self._read_page(where)
        rest = [e for e in entries if e.id != entry_id]
        hit = next((e for e in entries if e.id == entry_id), None)
        if not rest:
            self._drop_page_file(where)
            self.pages = [p for p in self.pages if p["file"] != where]
        else:
            self._write_page(where, rest)
            for p in self.pages:
                if p["file"] == where:
                    p["hi"] = rest[-1]
        return hit

    def get(self, entry_id):
        where = self.loc.get(entry_id)
        if where == "w":
            for e in self.window:
                if e.id == entry_id:
                    return e
        elif where is not None:
            for e in self._read_page(where):
                if e.id == entry_id:
                    return e
        return None

    def topk(self, k):
        if k <= 0:
            return []
        self._backfill()
        return list(self.window[:k])

    # -- persistence ---------------------------------------------------------
    def save(self):
        with open(os.path.join(self.dir, "window.json"), "w",
                  encoding="utf-8") as fh:
            json.dump([e.to_json() for e in self.window], fh)
        meta = {
            "group": self.group,
            "window_size": self.window_size,
            "page_size": self.page_size,
            "page_seq": self.page_seq,
            "pages": [{"file": p["file"], "hi": p["hi"].to_json()}
                      for p in self.pages],
            "loc": [[i, w] for i, w in self.loc.items()],
        }
        with open(os.path.join(self.dir, "meta.json"), "w",
                  encoding="utf-8") as fh:
            json.dump(meta, fh)

    @classmethod
    def load(cls, spec, directory, stats):
        with open(os.path.join(directory, "meta.json"), "r",
                  encoding="utf-8") as fh:
            meta = json.load(fh)
        st = cls(spec, meta["group"], directory, meta["window_size"],
                 meta["page_size"], stats)
        st.page_seq = meta["page_seq"]
        st.pages = [{"file": p["file"], "hi": Entry.from_json(p["hi"])}
                    for p in meta["pages"]]
        st.loc = {i: w for i, w in meta["loc"]}
        with open(os.path.join(directory, "window.json"), "r",
                  encoding="utf-8") as fh:
            st.window = [Entry.from_json(d) for d in json.load(fh)]
        return st
