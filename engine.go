package main

import (
	"errors"
	"io"
)

// Finding is the single JSON record emitted at the first disagreement.
// End offsets are exclusive; -1 marks a syntax error or truncation.
type Finding struct {
	Start      int64 `json:"start"`
	GatewayEnd int64 `json:"gatewayEnd"`
	OriginEnd  int64 `json:"originEnd"`
}

type framingOutcome struct {
	peer peerKind
	end  int64
	err  error
}

type peerKind int

const (
	peerGateway peerKind = iota
	peerOrigin
)

// Analyze consumes the full stream and returns the first boundary
// disagreement. A nil finding with a nil error means every parsed request had
// identical boundaries on both peers (OK).
func Analyze(src io.Reader) (*Finding, error) {
	pg := newPager(src)
	headCursor := pg.newCursor(0)

	for {
		start := headCursor.Offset()

		// A clean EOF between requests is the normal end of the stream.
		if _, err := headCursor.readByte(); err != nil {
			if errors.Is(err, io.EOF) {
				pg.release(headCursor)
				return nil, nil
			}
			pg.release(headCursor)
			return &Finding{Start: start, GatewayEnd: -1, OriginEnd: -1}, nil
		}
		headCursor.seek(start)

		head, err := readMessageHead(headCursor, maxLineLength)
		if err != nil {
			// Header syntax is shared wire syntax: both peers fail together.
			pg.release(headCursor)
			return &Finding{Start: start, GatewayEnd: -1, OriginEnd: -1}, nil
		}

		bodyStart := head.headEnd
		gatewayCursor := pg.newCursor(bodyStart)
		originCursor := pg.newCursor(bodyStart)
		// The head cursor is no longer needed; the two body cursors now keep
		// the required blocks alive.
		pg.release(headCursor)

		outcomes := make(chan framingOutcome, 2)
		go runFraming(peerGateway, pg, gatewayCursor, head, chooseFraming(head, true), outcomes)
		go runFraming(peerOrigin, pg, originCursor, head, chooseFraming(head, false), outcomes)

		var gateway, origin framingOutcome
		for i := 0; i < 2; i++ {
			o := <-outcomes
			if o.peer == peerGateway {
				gateway = o
			} else {
				origin = o
			}
		}
		// Both cursors have been released by runFraming; it is now safe to
		// reclaim blocks without racing an in-flight body read.
		pg.collectGarbage()

		gwEnd, gwFailed := outcomeEnd(gateway)
		orEnd, orFailed := outcomeEnd(origin)

		if gwFailed || orFailed || gwEnd != orEnd {
			return &Finding{Start: start, GatewayEnd: gwEnd, OriginEnd: orEnd}, nil
		}

		// Boundaries match: continue the pipeline from the shared next byte.
		headCursor = pg.newCursor(gwEnd)
	}
}

func runFraming(peer peerKind, pg *pager, c *Cursor, head *messageHead, f framing, out chan<- framingOutcome) {
	defer pg.release(c)
	end, err := scanBody(c, head, f)
	out <- framingOutcome{peer: peer, end: end, err: err}
}

func outcomeEnd(o framingOutcome) (int64, bool) {
	if o.err != nil {
		return -1, true
	}
	return o.end, false
}
