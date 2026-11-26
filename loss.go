package recovery

import "sort"

func (m *Machine) detectLostPackets(space Space, now int64) []LostPacket {
	state := &m.spaces[space]
	state.lossTime = 0
	if state.largestAcked < 0 {
		return []LostPacket{}
	}

	lossDelay := TimeThresholdDelay(m.smoothedRTT, m.latestRTT)

	var lost []LostPacket
	var earliestLossTime int64

	for pn, pkt := range state.packets {
		if !pkt.outstanding || pkt.acked || pkt.lost || pn > state.largestAcked {
			continue
		}

		byPacket := state.largestAcked >= pn+PacketThreshold
		byTime := now-pkt.sentAt >= lossDelay
		if byPacket || byTime {
			reason := TimeThresholdLoss
			if byPacket {
				reason = PacketThresholdLoss
			}
			pkt.lost = true
			if pkt.outstanding {
				pkt.outstanding = false
				m.bytesInFlight -= pkt.bytes
				if pkt.ackEliciting {
					state.ackElicitingInFlight--
				}
			}
			lost = append(lost, LostPacket{
				Space:        space,
				PacketNumber: pn,
				SentAt:       pkt.sentAt,
				Bytes:        pkt.bytes,
				Reason:       reason,
			})
			continue
		}

		deadline := pkt.sentAt + lossDelay
		if earliestLossTime == 0 || deadline < earliestLossTime {
			earliestLossTime = deadline
		}
	}

	state.lossTime = earliestLossTime
	sort.Slice(lost, func(i, j int) bool { return lost[i].PacketNumber < lost[j].PacketNumber })
	return lost
}

func (m *Machine) detectAllLostPackets(now int64) []LostPacket {
	var lost []LostPacket
	for space := Initial; space <= ApplicationData; space++ {
		lost = append(lost, m.detectLostPackets(space, now)...)
	}
	sort.Slice(lost, func(i, j int) bool {
		if lost[i].Space != lost[j].Space {
			return lost[i].Space < lost[j].Space
		}
		return lost[i].PacketNumber < lost[j].PacketNumber
	})
	return lost
}
