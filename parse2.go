package pgoutput

import "fmt"

func parseRelation(r *reader) (Relation, error) {
	id, err := r.uint32()
	if err != nil {
		return Relation{}, err
	}
	namespace, err := r.cstring()
	if err != nil {
		return Relation{}, err
	}
	name, err := r.cstring()
	if err != nil {
		return Relation{}, err
	}
	replicaIdentity, err := r.byte()
	if err != nil {
		return Relation{}, err
	}
	ncols, err := r.uint16()
	if err != nil {
		return Relation{}, err
	}
	cols := make([]RelationColumn, 0, ncols)
	for i := uint16(0); i < ncols; i++ {
		flags, err := r.byte()
		if err != nil {
			return Relation{}, err
		}
		colName, err := r.cstring()
		if err != nil {
			return Relation{}, err
		}
		dataType, err := r.uint32()
		if err != nil {
			return Relation{}, err
		}
		mod, err := r.int32()
		if err != nil {
			return Relation{}, err
		}
		cols = append(cols, RelationColumn{
			Key:          flags&colFlagKey != 0,
			Binary:       flags&colFlagBinary != 0,
			Name:         colName,
			DataType:     dataType,
			TypeModifier: mod,
		})
	}
	return Relation{
		ID:              id,
		Namespace:       namespace,
		Name:            name,
		ReplicaIdentity: replicaIdentity,
		Columns:         cols,
	}, nil
}

func parseType(r *reader) (Type, error) {
	id, err := r.uint32()
	if err != nil {
		return Type{}, err
	}
	namespace, err := r.cstring()
	if err != nil {
		return Type{}, err
	}
	name, err := r.cstring()
	if err != nil {
		return Type{}, err
	}
	return Type{ID: id, Namespace: namespace, Name: name}, nil
}

func parseTuple(r *reader) ([]TupleColumn, error) {
	ncols, err := r.uint16()
	if err != nil {
		return nil, err
	}
	cols := make([]TupleColumn, 0, ncols)
	for i := uint16(0); i < ncols; i++ {
		tag, err := r.byte()
		if err != nil {
			return nil, err
		}
		col := TupleColumn{Flag: tag}
		switch tag {
		case tupleNull:
		case tupleUnchanged:
			col.Unchanged = true
		case tupleText, tupleBinary:
			value, err := r.bytes16()
			if err != nil {
				return nil, err
			}
			col.Value = value
		default:
			return nil, fmt.Errorf("pgoutput: unknown tuple value tag %q", tag)
		}
		cols = append(cols, col)
	}
	return cols, nil
}

func parseInsert(r *reader) (Insert, error) {
	relID, err := r.uint32()
	if err != nil {
		return Insert{}, err
	}
	tuple, err := parseTuple(r)
	if err != nil {
		return Insert{}, err
	}
	return Insert{RelationID: relID, Row: tuple}, nil
}

func parseUpdate(r *reader) (Update, error) {
	relID, err := r.uint32()
	if err != nil {
		return Update{}, err
	}
	u := Update{RelationID: relID}
	marker, err := r.byte()
	if err != nil {
		return Update{}, err
	}
	switch marker {
	case 'K':
		u.OldKey = true
		u.Old, err = parseTuple(r)
		if err != nil {
			return Update{}, err
		}
	case 'O':
		u.Old, err = parseTuple(r)
		if err != nil {
			return Update{}, err
		}
	case 'N':
		// replica identity NOTHING: no old tuple
	default:
		return Update{}, fmt.Errorf("pgoutput: unexpected update tuple marker %q", marker)
	}
	newMarker, err := r.byte()
	if err != nil {
		return Update{}, err
	}
	if newMarker != 'N' {
		return Update{}, fmt.Errorf("pgoutput: unexpected update new-tuple marker %q", newMarker)
	}
	u.New, err = parseTuple(r)
	if err != nil {
		return Update{}, err
	}
	return u, nil
}

func parseDelete(r *reader) (Delete, error) {
	relID, err := r.uint32()
	if err != nil {
		return Delete{}, err
	}
	marker, err := r.byte()
	if err != nil {
		return Delete{}, err
	}
	d := Delete{RelationID: relID}
	switch marker {
	case 'K':
		d.OldKey = true
	case 'O':
	default:
		return Delete{}, fmt.Errorf("pgoutput: unexpected delete tuple marker %q", marker)
	}
	d.Old, err = parseTuple(r)
	if err != nil {
		return Delete{}, err
	}
	return d, nil
}

func parseTruncate(r *reader) (Truncate, error) {
	nrels, err := r.uint32()
	if err != nil {
		return Truncate{}, err
	}
	options, err := r.byte()
	if err != nil {
		return Truncate{}, err
	}
	ids := make([]uint32, 0, nrels)
	for i := uint32(0); i < nrels; i++ {
		id, err := r.uint32()
		if err != nil {
			return Truncate{}, err
		}
		ids = append(ids, id)
	}
	return Truncate{RelationIDs: ids, Options: options}, nil
}

func parseLogicalMessage(r *reader, transactional bool) (LogicalMessage, error) {
	lsn, err := r.uint64()
	if err != nil {
		return LogicalMessage{}, err
	}
	prefix, err := r.cstring()
	if err != nil {
		return LogicalMessage{}, err
	}
	content, err := r.bytes32()
	if err != nil {
		return LogicalMessage{}, err
	}
	return LogicalMessage{
		Transactional: transactional,
		LSN:           LSN(lsn),
		Prefix:        prefix,
		Content:       content,
	}, nil
}

// parseStreamStart accepts both protocol v2 (flags + xid, 5 bytes) and the
// protocol v4 subtransaction form which appends a subxid (9 bytes).
func parseStreamStart(r *reader) (StreamStart, error) {
	flags, err := r.byte()
	if err != nil {
		return StreamStart{}, err
	}
	xid, err := r.uint32()
	if err != nil {
		return StreamStart{}, err
	}
	s := StreamStart{XID: xid, FirstSegment: flags&1 != 0}
	if r.remaining() > 0 {
		subxid, err := r.uint32()
		if err != nil {
			return StreamStart{}, err
		}
		s.SubXID = subxid
	}
	return s, nil
}

func parseStreamCommit(r *reader) (StreamCommit, error) {
	xid, err := r.uint32()
	if err != nil {
		return StreamCommit{}, err
	}
	flags, err := r.byte()
	if err != nil {
		return StreamCommit{}, err
	}
	commitLSN, err := r.uint64()
	if err != nil {
		return StreamCommit{}, err
	}
	endLSN, err := r.uint64()
	if err != nil {
		return StreamCommit{}, err
	}
	micros, err := r.uint64()
	if err != nil {
		return StreamCommit{}, err
	}
	return StreamCommit{
		XID:        xid,
		Flags:      flags,
		CommitLSN:  LSN(commitLSN),
		EndLSN:     LSN(endLSN),
		CommitTime: microsToTime(int64(micros)),
	}, nil
}

func parseStreamAbort(r *reader) (StreamAbort, error) {
	xid, err := r.uint32()
	if err != nil {
		return StreamAbort{}, err
	}
	subxid, err := r.uint32()
	if err != nil {
		return StreamAbort{}, err
	}
	lsn, err := r.uint64()
	if err != nil {
		return StreamAbort{}, err
	}
	endLSN, err := r.uint64()
	if err != nil {
		return StreamAbort{}, err
	}
	micros, err := r.uint64()
	if err != nil {
		return StreamAbort{}, err
	}
	return StreamAbort{
		XID:        xid,
		SubXID:     subxid,
		SubAbort:   xid != subxid,
		LSN:        LSN(lsn),
		EndLSN:     LSN(endLSN),
		CommitTime: microsToTime(int64(micros)),
	}, nil
}

func parseKeepalive(r *reader) (Keepalive, error) {
	lsn, err := r.uint64()
	if err != nil {
		return Keepalive{}, err
	}
	micros, err := r.uint64()
	if err != nil {
		return Keepalive{}, err
	}
	flags, err := r.byte()
	if err != nil {
		return Keepalive{}, err
	}
	return Keepalive{
		LSN:            LSN(lsn),
		Time:           microsToTime(int64(micros)),
		ReplyRequested: flags&1 != 0,
	}, nil
}
