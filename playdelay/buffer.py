"""Fixed-memory reordering jitter buffer.

Memory bound: at most `capacity` packets plus a fixed-size bitset for
duplicate detection (SEEN_WINDOW_BITS bits; sequence numbers alias modulo the
window, which is exact for any run shorter than the window).

Every push is classified into exactly one outcome so that duplicates, late
packets and buffer overflow stay traceable:
  'duplicate' - this sequence number was already seen (bit set);
  'late'      - the packet's source interval is at/behind the playout position,
                i.e. it would have to rewrite already-played time;
  'overflow'  - buffer full, the incoming packet is dropped;
  'stored'    - queued for reordering.
"""
from . import config


class SeenWindow:
    def __init__(self, bits=config.SEEN_WINDOW_BITS):
        self.bits = bits
        self.state = 0

    def check(self, seq):
        return bool((self.state >> (seq % self.bits)) & 1)

    def mark(self, seq):
        self.state |= 1 << (seq % self.bits)

    def reset(self):
        self.state = 0


class JitterBuffer:
    def __init__(self, capacity=config.BUFFER_CAPACITY):
        self.capacity = capacity
        self.packets = {}          # seq -> Packet, len <= capacity
        self.seen = SeenWindow()

    def reset(self):
        self.packets.clear()
        self.seen.reset()

    def push(self, pkt, playout_pos):
        if self.seen.check(pkt.seq):
            return 'duplicate'
        if pkt.src_pos < playout_pos:
            return 'late'
        if len(self.packets) >= self.capacity:
            return 'overflow'
        self.packets[pkt.seq] = pkt
        self.seen.mark(pkt.seq)
        return 'stored'

    def packet_at(self, pos):
        for p in self.packets.values():
            if p.src_pos <= pos < p.src_pos + len(p.payload):
                return p
        return None

    def next_start_after(self, pos):
        cands = [p.src_pos for p in self.packets.values() if p.src_pos > pos]
        return min(cands) if cands else None

    def level(self, playout_pos):
        """Buffered samples at/after the playout position."""
        lvl = 0
        for p in self.packets.values():
            end = p.src_pos + len(p.payload)
            lvl += max(0, end - max(p.src_pos, playout_pos))
        return lvl

    def discard_before(self, playout_pos):
        gone = [s for s, p in self.packets.items()
                if p.src_pos + len(p.payload) <= playout_pos]
        for s in gone:
            del self.packets[s]
