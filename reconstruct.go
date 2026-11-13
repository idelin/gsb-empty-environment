package netsec

import (
	"fmt"
	"reflect"
	"sort"
)

func (f *Filter) reconstructOld(state *schema, tx *txState, key Key, tuple Row) (Row, error) {
	if err := validateTupleIDs(state, tuple); err != nil {
		return nil, err
	}

	row := make(Row, len(state.relation.Columns))
	for id, value := range key {
		row[id] = cloneValue(value)
	}

	var needed []ColumnID
	seenNeeded := make(map[ColumnID]struct{})
	addNeeded := func(id ColumnID) {
		if _, exists := seenNeeded[id]; exists {
			return
		}
		seenNeeded[id] = struct{}{}
		needed = append(needed, id)
	}

	for id, value := range tuple {
		if IsUnchanged(value) {
			column, _ := state.column(id)
			if state.isPrimary(id) {
				return nil, fmt.Errorf("%w: primary-key column %d cannot be UNCHANGED", ErrInvalidTuple, id)
			}
			if !column.AllowUnchanged {
				return nil, fmt.Errorf("%w: column %d cannot be UNCHANGED", ErrInvalidTuple, id)
			}
			addNeeded(id)
			continue
		}
		if state.isPrimary(id) {
			if !reflect.DeepEqual(value, key[id]) {
				return nil, fmt.Errorf("%w: old tuple key column %d disagrees with OldKey", ErrInvalidTuple, id)
			}
		}
		row[id] = cloneValue(value)
	}

	for _, column := range state.relation.Columns {
		if _, exists := row[column.ID]; !exists {
			addNeeded(column.ID)
		}
	}
	if len(needed) == 0 {
		return row, nil
	}

	cached, exists := f.lookup(tx, f.cacheKey(state, key))
	if !exists {
		sortColumnIDs(needed)
		return nil, f.needsFetch(state, key, needed, "old row cache miss")
	}

	missing := make([]ColumnID, 0, len(needed))
	for _, id := range needed {
		value, exists := cached[id]
		if !exists || IsUnchanged(value) {
			missing = append(missing, id)
			continue
		}
		row[id] = cloneValue(value)
	}
	if len(missing) > 0 {
		sortColumnIDs(missing)
		return nil, f.needsFetch(state, key, missing, "old row cache is incomplete")
	}
	return row, nil
}

func (f *Filter) reconstructNew(state *schema, tx *txState, oldKey, newKey Key, tuple Row) (Row, error) {
	if err := validateTupleIDs(state, tuple); err != nil {
		return nil, err
	}

	keyChanged := !sameKey(oldKey, newKey)
	row := make(Row, len(state.relation.Columns))
	var needed []ColumnID
	seenNeeded := make(map[ColumnID]struct{})

	for id, value := range tuple {
		if IsUnchanged(value) {
			column, _ := state.column(id)
			if state.isPrimary(id) {
				return nil, fmt.Errorf("%w: primary-key column %d cannot be UNCHANGED", ErrInvalidTuple, id)
			}
			if !column.AllowUnchanged {
				return nil, fmt.Errorf("%w: column %d cannot be UNCHANGED", ErrInvalidTuple, id)
			}
			seenNeeded[id] = struct{}{}
			needed = append(needed, id)
			continue
		}
		row[id] = cloneValue(value)
	}

	for _, id := range state.primary {
		value, exists := row[id]
		if !exists {
			return nil, fmt.Errorf("%w: new tuple is missing primary-key column %d", ErrInvalidTuple, id)
		}
		if !reflect.DeepEqual(value, newKey[id]) {
			return nil, fmt.Errorf("%w: new tuple key column %d disagrees with derived key", ErrInvalidTuple, id)
		}
	}

	missingExplicit := make([]ColumnID, 0)
	for _, column := range state.relation.Columns {
		if _, exists := row[column.ID]; exists {
			continue
		}
		if _, alreadyNeeded := seenNeeded[column.ID]; alreadyNeeded {
			continue
		}
		missingExplicit = append(missingExplicit, column.ID)
	}
	if len(missingExplicit) > 0 {
		sortColumnIDs(missingExplicit)
		return nil, f.needsFetch(state, newKey, missingExplicit,
			"new tuple omitted a non-key value without an UNCHANGED marker")
	}

	if keyChanged && len(needed) > 0 {
		sortColumnIDs(needed)
		return nil, f.needsFetch(state, newKey, needed,
			"primary key changed; UNCHANGED values cannot come from the old-key cache")
	}
	if len(needed) == 0 {
		return row, nil
	}

	cached, exists := f.lookup(tx, f.cacheKey(state, newKey))
	if !exists {
		sortColumnIDs(needed)
		return nil, f.needsFetch(state, newKey, needed, "new row cache miss")
	}

	missing := make([]ColumnID, 0, len(needed))
	for _, id := range needed {
		value, exists := cached[id]
		if !exists || IsUnchanged(value) {
			missing = append(missing, id)
			continue
		}
		row[id] = cloneValue(value)
	}
	if len(missing) > 0 {
		sortColumnIDs(missing)
		return nil, f.needsFetch(state, newKey, missing, "new row cache is incomplete")
	}
	return row, nil
}

func (f *Filter) needsFetch(state *schema, key Key, missing []ColumnID, reason string) *NeedsFetchError {
	return &NeedsFetchError{
		RelationID: state.relation.ID,
		Version:    state.relation.Version,
		Key:        cloneKey(key),
		Missing:    append([]ColumnID(nil), missing...),
		Reason:     reason,
	}
}

func sortColumnIDs(ids []ColumnID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
