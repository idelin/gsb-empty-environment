package desim

import (
	"encoding/gob"
	"io"
)

// Event payloads carried by the network model. They are concrete,
// gob-registered types so checkpoints can serialize pending events.
type (
	// SrcTick is a source timer expiration: generate one packet.
	SrcTick struct{}
	// PacketArrival delivers a packet into the queue.
	PacketArrival struct{ Pkt Packet }
	// TxDone signals that the link finished transmitting a packet.
	TxDone struct{ Pkt Packet }
	// SinkArrival delivers a packet to the sink.
	SinkArrival struct{ Pkt Packet }
)

func init() {
	gob.Register(SrcTick{})
	gob.Register(PacketArrival{})
	gob.Register(TxDone{})
	gob.Register(SinkArrival{})
}

// Packet is the unit of work moving through the model. Sizes are in
// bytes; all times are integer ticks.
type Packet struct {
	ID   uint64
	Size uint64
	Born uint64
}

// Source emits a packet every Interval ticks until Remaining hits 0.
type Source struct {
	Interval  uint64
	Size      uint64
	Remaining int
	NextID    uint64
}

// Queue is a bounded FIFO buffer in front of the link. Packets that
// arrive while the buffer is full are dropped.
type Queue struct {
	Cap     int
	Buf     []Packet
	Dropped uint64
}

// Link serializes packets at Rate bytes/tick and adds Delay ticks of
// propagation. It transmits one packet at a time.
type Link struct {
	Rate  uint64
	Delay uint64
	Busy  bool
}

// Delivery records one packet reaching the sink.
type Delivery struct {
	ID   uint64
	Tick uint64
}

// Sink counts and logs received packets. Log is the trajectory used
// to verify determinism across checkpoints.
type Sink struct {
	Log []Delivery
}

// World bundles the scheduler and the network entities.
type World struct {
	Sched *Scheduler
	Src   *Source
	Q     *Queue
	L     *Link
	Sn    *Sink
}

// NewWorld builds a world and schedules the first source tick at 0.
func NewWorld(src *Source, q *Queue, l *Link) *World {
	w := &World{Src: src, Q: q, L: l, Sn: &Sink{}}
	w.Sched = NewScheduler(w)
	w.registerHandlers()
	w.Sched.ScheduleAt(0, CatTimer, "src.tick", SrcTick{})
	return w
}

func (w *World) registerHandlers() {
	w.Sched.Register("src.tick", w.onSrcTick)
	w.Sched.Register("q.arrival", w.onArrival)
	w.Sched.Register("link.txdone", w.onTxDone)
	w.Sched.Register("sink.arrival", w.onSinkArrival)
}

func (w *World) onSrcTick(s *Scheduler, data interface{}) {
	if w.Src.Remaining == 0 {
		return
	}
	w.Src.Remaining--
	w.Src.NextID++
	pkt := Packet{ID: w.Src.NextID, Size: w.Src.Size, Born: s.Now()}
	// Same-tick arrival: ordered after all timers at this tick
	// because CatArrival > CatTimer.
	s.ScheduleAt(s.Now(), CatArrival, "q.arrival", PacketArrival{Pkt: pkt})
	if w.Src.Remaining > 0 {
		s.After(w.Src.Interval, CatTimer, "src.tick", SrcTick{})
	}
}

func (w *World) onArrival(s *Scheduler, data interface{}) {
	pkt := data.(PacketArrival).Pkt
	if w.L.Busy {
		if len(w.Q.Buf) >= w.Q.Cap {
			w.Q.Dropped++
			return
		}
		w.Q.Buf = append(w.Q.Buf, pkt)
		return
	}
	w.startTx(s, pkt)
}

func (w *World) startTx(s *Scheduler, pkt Packet) {
	w.L.Busy = true
	tx := (pkt.Size + w.L.Rate - 1) / w.L.Rate
	if tx == 0 {
		tx = 1
	}
	s.ScheduleAt(s.Now()+tx, CatLink, "link.txdone", TxDone{Pkt: pkt})
}

func (w *World) onTxDone(s *Scheduler, data interface{}) {
	pkt := data.(TxDone).Pkt
	s.ScheduleAt(s.Now()+w.L.Delay, CatArrival, "sink.arrival", SinkArrival{Pkt: pkt})
	if len(w.Q.Buf) > 0 {
		next := w.Q.Buf[0]
		w.Q.Buf = w.Q.Buf[1:]
		w.startTx(s, next)
		return
	}
	w.L.Busy = false
}

func (w *World) onSinkArrival(s *Scheduler, data interface{}) {
	pkt := data.(SinkArrival).Pkt
	w.Sn.Log = append(w.Sn.Log, Delivery{ID: pkt.ID, Tick: s.Now()})
}

type worldState struct {
	Sched schedState
	Src   Source
	Q     Queue
	L     Link
	Sn    Sink
}

// Save writes a checkpoint of the whole world (scheduler, pending
// events, cancellation slots, and every entity) to out.
func (w *World) Save(out io.Writer) error {
	st := worldState{
		Sched: w.Sched.snapshot(),
		Src:   *w.Src,
		Q:     *w.Q,
		L:     *w.L,
		Sn:    *w.Sn,
	}
	return gob.NewEncoder(out).Encode(st)
}

// LoadWorld restores a checkpoint written by Save. Continuing the
// restored world produces exactly the same trajectory as continuing
// the original one.
func LoadWorld(in io.Reader) (*World, error) {
	var st worldState
	if err := gob.NewDecoder(in).Decode(&st); err != nil {
		return nil, err
	}
	w := &World{Src: &st.Src, Q: &st.Q, L: &st.L, Sn: &st.Sn}
	w.Sched = NewScheduler(w)
	w.registerHandlers()
	w.Sched.restore(st.Sched)
	return w, nil
}
