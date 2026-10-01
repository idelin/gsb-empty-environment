# sacksim

Discrete-event simulation of SACK-based reliable transfer in pure Go
(standard library only). No real network traffic: every packet, queue and
link runs inside an integer-tick event model.

## Design

- sim/interval.go — interval set over extended integers (math/big.Int
  sequence numbers). Overlapping/adjacent intervals merge, so the SACK
  union is counted exactly once; Gaps computes missing byte ranges.
- sim/event.go — discrete-event queue (min-heap on integer ticks).
- sim/link.go — simulated link with fixed tick delay and deterministic
  loss (every Nth packet dropped).
- sim/sender.go — sender keeps sndUna/sndNxt, the SACK set and the
  in-flight retransmission set. It retransmits ONLY the gaps below the
  highest SACKed byte (or the whole outstanding window on RTO), clamped
  to the current window, so window shrink is honored. ACKs for never-sent
  bytes are rejected; stale/duplicate cumulative ACKs are ignored and
  already-acked bytes are never retransmitted.
- sim/receiver.go — receiver keeps received intervals, advances the
  cumulative ACK over contiguous data and reports the rest as SACK blocks.

## Run

    go run ./cmd/demo
    go test ./... -v

## Sample run (abridged)

    tick=0      TX seg[1180591620717411303424,1180591620717411304424)
    ...
    tick=25     WINDOW 4000 -> 2000
    tick=30     TX-ACK ack=1180591620717411309424 sacks=[[1180591620717411310424,1180591620717411311424)]
    tick=40     GAP [1180591620717411309424,1180591620717411310424) -> retransmit
    tick=40     TX-RETX seg[1180591620717411309424,1180591620717411310424)
    tick=115    REJECT ack=1180591620717411318424 (unsent byte, sndNxt=1180591620717411313424)
    tick=125    ACK stale/dup ack=1180591620717411303424 (una=1180591620717411313424)
    ---- final state ----
    ISN (extended int)      : 1180591620717411303424
    data pkts sent/dropped  : 11 / 1
    bytes on wire (total)   : 11000
    bytes retransmitted     : 1000
    receiver good bytes     : 10000
    rejected ACKs           : 1
    RESULT: OK - all 10000 bytes delivered and acknowledged

## Test results

    === RUN   TestIntervalUnionCountedOnce        --- PASS
    === RUN   TestGaps                            --- PASS
    === RUN   TestRejectAckOfUnsentBytes          --- PASS
    === RUN   TestOldAckDoesNotRetransmit         --- PASS
    === RUN   TestGapOnlyRetransmit               --- PASS
    === RUN   TestSenderSackUnionOverlap          --- PASS
    === RUN   TestOutOfOrderAcks                  --- PASS
    === RUN   TestWindowShrinkClampsRetransmission --- PASS
    === RUN   TestFullTransferWithLossAndExtendedSeq --- PASS
    === RUN   TestRTORecoversLostRetransmission   --- PASS
    PASS
    ok      sacksim/sim
