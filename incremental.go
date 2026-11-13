package netsec

// ApplyAndIncrementalRefresh commits row changes and then refreshes the
// affected partition incrementally. When a row transitions visible ->
// invisible its contribution is retracted through the view's monoid; a
// non-retractable aggregate forces a full rebuild instead.
func (h *Host) ApplyAndIncrementalRefresh(viewName string, subject Subject, events []RowEvent) (RefreshStats, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	view, src, snap, key, err := h.planRefresh(viewName, subject)
	if err != nil {
		return RefreshStats{}, err
	}

	// Apply the transaction first so visibility is evaluated against current
	// data while old snapshots remain available for retraction.
	for _, ev := range events {
		if err := applyOne(src, ev); err != nil {
			return RefreshStats{}, err
		}
	}
	src.version++

	groups, existed := h.accums[key]
	if !existed || !view.RequiresRetractable() {
		return h.fullRebuildLocked(view, src, snap, key)
	}

	for _, ev := range events {
		if err := h.applyEventToGroups(view, snap, groups, ev); err != nil {
			if err == ErrCannotRetract {
				return h.fullRebuildLocked(view, src, snap, key)
			}
			return RefreshStats{}, err
		}
	}

	entry, err := h.renderEntry(view, src, snap, key, groups)
	if err != nil {
		return RefreshStats{}, err
	}
	if err := h.publish(view, key, entry, groups); err != nil {
		return RefreshStats{}, err
	}
	return RefreshStats{Key: key, LSN: entry.RefreshLSN, SourceVersion: entry.SourceVersion}, nil
}

func (h *Host) fullRebuildLocked(view *ViewDef, src *Source, snap PolicySnapshot, key PartitionKey) (RefreshStats, error) {
	entry, groups, err := h.buildFull(view, src, snap, key)
	if err != nil {
		return RefreshStats{}, err
	}
	if err := h.publish(view, key, entry, groups); err != nil {
		return RefreshStats{}, err
	}
	return RefreshStats{
		Key:           key,
		LSN:           entry.RefreshLSN,
		SourceVersion: entry.SourceVersion,
		FullRebuild:   true,
	}, nil
}

// applyEventToGroups maintains the monoid state for one change. The critical
// case is an update that moves a row from visible to invisible: the old
// contribution must be retracted even though the new tuple no longer passes
// RLS.
func (h *Host) applyEventToGroups(view *ViewDef, snap PolicySnapshot, groups map[GroupKey]*groupAccum, ev RowEvent) error {
	switch ev.Kind {
	case EventInsert:
		if !snap.visible(ev.New) {
			return nil
		}
		return foldNew(view, groups, ev.New)
	case EventDelete:
		old := ev.Old
		if old == nil {
			old = ev.New
		}
		if !snap.visible(old) {
			return nil
		}
		return retractOld(view, groups, old)
	case EventUpdate:
		old := ev.Old
		if old == nil {
			old = ev.New
		}
		oldVisible := snap.visible(old)
		newVisible := snap.visible(ev.New)
		switch {
		case oldVisible && !newVisible:
			// Visible -> invisible: remove the entire previous contribution.
			return retractOld(view, groups, old)
		case !oldVisible && newVisible:
			return foldNew(view, groups, ev.New)
		case oldVisible && newVisible:
			if err := retractOld(view, groups, old); err != nil {
				return err
			}
			return foldNew(view, groups, ev.New)
		default:
			return nil
		}
	default:
		return ErrInvalidView
	}
}

func foldNew(view *ViewDef, groups map[GroupKey]*groupAccum, row Row) error {
	gk, err := makeGroupKey(view.Projection.GroupBy, row)
	if err != nil {
		return err
	}
	kinds := view.aggregateKinds()
	g, ok := groups[gk]
	if !ok {
		g = newGroupAccum(kinds)
		groups[gk] = g
	}
	return g.add(view, row)
}

func retractOld(view *ViewDef, groups map[GroupKey]*groupAccum, row Row) error {
	gk, err := makeGroupKey(view.Projection.GroupBy, row)
	if err != nil {
		return err
	}
	g, ok := groups[gk]
	if !ok {
		return ErrCannotRetract
	}
	if err := g.retract(view, row); err != nil {
		return err
	}
	if g.rowCount <= 0 {
		delete(groups, gk)
	}
	return nil
}

func applyOne(src *Source, ev RowEvent) error {
	switch ev.Kind {
	case EventInsert:
		src.rows[ev.Key] = cloneRow(ev.New)
	case EventUpdate:
		src.rows[ev.Key] = cloneRow(ev.New)
	case EventDelete:
		delete(src.rows, ev.Key)
	default:
		return ErrInvalidView
	}
	return nil
}
