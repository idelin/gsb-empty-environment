package netsim

import (
	"sort"
	"time"

	"prescreen.local/ps-93493e0dc5/bbr"
)

// packet is one in-flight data packet and the carrier of its returning ACK.
type packet struct {
	flowID     int
	seq        int
	size       int
	sendAt     int64
	ecn        bool
	dropped    bool
	ackNum     int
	appLimited bool
	owner      *Flow
}

func (p *packet) flow() *Flow { return p.owner }

// Flow is one BBRv2 sender behind the shared bottleneck.
type Flow struct {
	id  int
	sim *Simulator
	bbr *bbr.BBR

	// application offer-rate token bucket (bytes); <=0 means always backlogged
	appRateBps int64
	appTokens  float64

	// pacing token bucket (bytes), refilled from BBR's pacing rate
	pacingTokens float64

	// sender/receiver bookkeeping
	outstanding map[int]*packet
	nextSeq     int
	sndUna      int
	dupAcks     int
	rcvNext     int
	rcvSeqs     map[int]bool
	mss         int

	// periodic driver and delivery-rate sampler state
	lastWake        int64
	lastDeliveredAt int64
	wakeGen         int64

	// RTO retransmission bookkeeping
	rtoGen   int64
	rtoTimer int64

	stats FlowStats
}

// BBR exposes the underlying state machine for assertions and hooks.
func (f *Flow) BBR() *bbr.BBR { return f.bbr }

// ID returns the flow identifier.
func (f *Flow) ID() int { return f.id }

// onReceive records an in-order/out-of-order arrival at the receiver and
// advances the cumulative receive pointer across any contiguous run.
func (f *Flow) onReceive(seq int) {
	f.rcvSeqs[seq] = true
	for f.rcvSeqs[f.rcvNext] {
		delete(f.rcvSeqs, f.rcvNext)
		f.rcvNext++
	}
}

// wake is the single periodic driver: it ticks BBR, refills token buckets and
// sends as many packets as cwnd, pacing and the application permit.
func (f *Flow) wake(now int64) {
	// Time-based transitions (e.g. PROBE_RTT) must fire even when window-limited.
	f.bbr.Tick(now)

	if f.lastWake == 0 {
		f.lastWake = now
	}
	elapsed := now - f.lastWake
	f.lastWake = now
	if elapsed > 0 {
		if rate := f.bbr.PacingRate(); rate > 0 {
			f.pacingTokens += float64(rate) * float64(elapsed) / float64(time.Second)
		}
		if f.appRateBps > 0 {
			f.appTokens += float64(f.appRateBps) * float64(elapsed) / float64(time.Second)
		}
	}

	// A wake is app-limited when it ends because the application runs out of
	// tokens; every packet of that burst inherits the mark so BBR can exclude
	// the resulting samples from bw_hi.
	appLimited := false
	for f.bbr.CanSend() && f.pacingTokens >= float64(f.mss) {
		if f.appRateBps > 0 && f.appTokens < float64(f.mss) {
			appLimited = true
			break
		}
		f.sendOne(now, appLimited)
	}
	f.markBurstAppLimited(appLimited)

	f.scheduleNextWake(now)
}

// markBurstAppLimited flips the app-limited flag on packets sent in the most
// recent burst when the application could not keep the pipe full.
func (f *Flow) markBurstAppLimited(appLimited bool) {
	if !appLimited {
		return
	}
	for seq := f.sndUna; seq < f.nextSeq; seq++ {
		if p, ok := f.outstanding[seq]; ok {
			p.appLimited = true
		}
	}
}

func (f *Flow) sendOne(now int64, appLimited bool) {
	seq := f.nextSeq
	f.nextSeq++

	if f.appRateBps > 0 {
		f.appTokens -= float64(f.mss)
	}
	f.pacingTokens -= float64(f.mss)

	p := &packet{
		flowID:     f.id,
		seq:        seq,
		size:       f.mss,
		sendAt:     now,
		appLimited: appLimited,
		owner:      f,
	}
	f.outstanding[seq] = p
	f.stats.Sent++
	f.bbr.OnSent(1, now)

	f.sim.push(event{
		at:     now + f.sim.cfg.PropDelay.Nanoseconds(),
		typ:    evArrival,
		flowID: f.id,
		pkt:    p,
	})
	f.armRTO(now)
}

// armRTO (re)starts the retransmission timer for the current head-of-line
// packet. The timeout is a conservative 4 RTTs, capped to keep tests fast.
func (f *Flow) armRTO(now int64) {
	if f.rtoTimer != 0 {
		return
	}
	rtt := f.bbr.MinRTT()
	if rtt <= 0 {
		rtt = 40 * time.Millisecond
	}
	timeout := 4 * rtt
	if timeout > time.Second {
		timeout = time.Second
	}
	f.rtoGen++
	f.rtoTimer = now + timeout.Nanoseconds()
	f.sim.push(event{at: f.rtoTimer, typ: evRTO, flowID: f.id, gen: f.rtoGen})
}

func (f *Flow) cancelRTO() {
	f.rtoTimer = 0
	f.rtoGen++
}

// onRTO retransmits the lowest outstanding (typically dropped) packet and
// reports the loss explicitly to BBR.
func (f *Flow) onRTO(now int64) {
	f.rtoTimer = 0
	seq := f.lowestOutstanding()
	if seq < 0 {
		return
	}
	_, ok := f.outstanding[seq]
	if !ok {
		return
	}
	delete(f.outstanding, seq)
	f.stats.Lost++

	rtt := f.bbr.MinRTT()
	if rtt <= 0 {
		rtt = 40 * time.Millisecond
	}
	f.bbr.HandleAck(bbr.AckEvent{
		Now:         now,
		RTT:         rtt,
		LostPackets: 1,
	})
	f.dupAcks = 0

	// Re-send the same sequence with a fresh packet object.
	rp := &packet{
		flowID: f.id,
		seq:    seq,
		size:   f.mss,
		sendAt: now,
		owner:  f,
	}
	f.outstanding[seq] = rp
	f.stats.Sent++
	// The original was decremented as lost above; re-add the replacement so
	// in-flight accounting stays closed (its eventual ACK decrements it once).
	f.bbr.OnSent(1, now)
	f.sim.push(event{
		at:     now + f.sim.cfg.PropDelay.Nanoseconds(),
		typ:    evArrival,
		flowID: f.id,
		pkt:    rp,
	})
	// Keep the timer running: earlier gaps (multiple consecutive drops) are
	// recovered one RTO at a time as the head-of-line advances.
	f.armRTO(now)
}

// scheduleNextWake picks the earliest time at which a token (or a tick) can
// make progress, capping idle sleeps at the configured tick interval.
func (f *Flow) scheduleNextWake(now int64) {
	// Always schedule a tick: an ACK may open the cwnd before the next token
	// would otherwise be available, and time-based BBR transitions (PROBE_RTT)
	// need to fire even while window-limited.
	delay := f.sim.cfg.TickInterval.Nanoseconds()

	if f.bbr.CanSend() {
		if f.appRateBps > 0 && f.appTokens < float64(f.mss) {
			need := float64(f.mss) - f.appTokens
			d := int64(need * float64(time.Second) / float64(f.appRateBps))
			if d > 0 && d < delay {
				delay = d
			}
		}
		if rate := f.bbr.PacingRate(); rate > 0 && f.pacingTokens < float64(f.mss) {
			need := float64(f.mss) - f.pacingTokens
			d := int64(need * float64(time.Second) / float64(rate))
			if d > 0 && d < delay {
				delay = d
			}
		}
	}
	if delay < 1 {
		delay = 1
	}
	f.sim.scheduleWake(f, now+delay)
}

// onAck processes a returning ACK: cumulative acknowledgement, duplicate-ACK
// loss detection and ECN echo, then feeds one consolidated AckEvent to BBR.
func (f *Flow) onAck(p *packet, now int64) {
	acked, lost, ecn, lostSeqs := f.processAck(p)
	if acked == 0 && lost == 0 {
		// A duplicate ACK still carries an ECN echo; BBR consumes it via Tick
		// only when there is no explicit signal to report.
		if ecn > 0 {
			f.bbr.HandleAck(bbr.AckEvent{Now: now, RTT: f.bbr.MinRTT(), ECNEchoes: ecn})
		} else {
			f.bbr.Tick(now)
		}
		return
	}

	deliveredBytes, interval, appLimited := f.sampleRate(p, acked, now)
	rtt := time.Duration(now - p.sendAt)
	f.bbr.HandleAck(bbr.AckEvent{
		Now:            now,
		RTT:            rtt,
		AckedPackets:   acked,
		LostPackets:    lost,
		ECNEchoes:      ecn,
		BytesDelivered: deliveredBytes,
		Interval:       interval,
		AppLimited:     appLimited,
	})
	// Fast-retransmit every packet just declared lost by duplicate ACKs. The
	// loss already decremented BBR's flight, so the replacement must not call
	// OnSent again (otherwise in-flight accounting would double-count).
	if lost > 0 {
		f.fastRetransmit(now, lostSeqs)
	}
	if f.lowestOutstanding() < 0 {
		f.cancelRTO()
	}
}

// fastRetransmit re-sends the lowest outstanding head-of-line packet. It is
// used after a duplicate-ACK loss declaration; RTO-driven recovery lives in
// onRTO and additionally reports the loss to BBR.
func (f *Flow) fastRetransmit(now int64, seqs []int) {
	for _, seq := range seqs {
		if _, ok := f.outstanding[seq]; ok {
			continue
		}
		rp := &packet{
			flowID: f.id,
			seq:    seq,
			size:   f.mss,
			sendAt: now,
			owner:  f,
		}
		f.outstanding[seq] = rp
		f.stats.Sent++
		// Re-add the replacement: the duplicate-ACK loss report already
		// decremented the flight once.
		f.bbr.OnSent(1, now)
		f.sim.push(event{
			at:     now + f.sim.cfg.PropDelay.Nanoseconds(),
			typ:    evArrival,
			flowID: f.id,
			pkt:    rp,
		})
	}
}

// processAck advances the cumulative receive pointer, declares loss after three
// duplicate ACKs and counts the ECN echo on the ACK-bearing packet.
func (f *Flow) processAck(p *packet) (acked, lost, ecn int, lostSeqs []int) {
	if p.ecn {
		ecn = 1
	}

	ackNum := p.ackNum
	if ackNum > f.sndUna {
		for seq := f.sndUna; seq < ackNum; seq++ {
			if _, ok := f.outstanding[seq]; ok {
				delete(f.outstanding, seq)
				acked++
			}
		}
		f.sndUna = ackNum
		f.dupAcks = 0
	} else {
		f.dupAcks++
	}

	// Three duplicate ACKs: declare the lowest outstanding dropped packet lost.
	if f.dupAcks >= 3 {
		if seq := f.lowestOutstanding(); seq >= 0 {
			if op, ok := f.outstanding[seq]; ok && op.dropped {
				delete(f.outstanding, seq)
				lost++
				lostSeqs = append(lostSeqs, seq)
				f.dupAcks = 0
			}
		}
	}
	return acked, lost, ecn, lostSeqs
}

func (f *Flow) lowestOutstanding() int {
	if len(f.outstanding) == 0 {
		return -1
	}
	seqs := make([]int, 0, len(f.outstanding))
	for seq := range f.outstanding {
		seqs = append(seqs, seq)
	}
	sort.Ints(seqs)
	return seqs[0]
}

// sampleRate computes the delivery-rate interval for this ACK. The sample is
// app-limited when an application-limited packet was delivered in the interval.
func (f *Flow) sampleRate(p *packet, acked int, now int64) (int, time.Duration, bool) {
	appLimited := p.appLimited
	if f.lastDeliveredAt == 0 {
		f.lastDeliveredAt = p.sendAt
	}
	interval := time.Duration(now - f.lastDeliveredAt)
	f.lastDeliveredAt = now
	return acked * f.mss, interval, appLimited
}
