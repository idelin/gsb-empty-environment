package netsec

import (
	"fmt"
	"sort"
)

// Predicate must be deterministic and free of side effects. It is called once
// for the reconstructed old row and once for the reconstructed new row using
// defensive copies. Returning true means the row is allowed to be replicated.
type Predicate func(Relation, Row) bool

// EmitFunc receives only committed changes. It is called synchronously during
// Commit, in event order, after the transaction's cache has been published.
type EmitFunc func(Change)

// Config creates a Filter.
type Config struct {
	Predicate Predicate
	Emit      EmitFunc
}

// Filter is a stateful logical-replication row filter. It is intended to be
// driven from a single replication goroutine and is not safe for concurrent
// use.
type Filter struct {
	predicate Predicate
	emit      EmitFunc

	relations map[RelationID]*relationState
	cache     map[cacheKey]cachedRow
	txs       map[TransactionID]*txState
}

type relationState struct {
	current  *schema
	versions map[RelationVersion]*schema
	history  map[ColumnID]Column
}

type cacheKey struct {
	relation RelationID
	version  RelationVersion
	key      string
}

type cachedRow struct {
	row Row
}

type txCacheEntry struct {
	row     Row
	removed bool
}

type txState struct {
	overlay  map[cacheKey]txCacheEntry
	buffered []Change
}

type cacheAction struct {
	key cacheKey
	row Row
	del bool
}

// NewFilter creates a filter from a Config.
func NewFilter(config Config) (*Filter, error) {
	if config.Predicate == nil {
		return nil, fmt.Errorf("%w: predicate is required", ErrInvalidConfig)
	}
	emit := config.Emit
	if emit == nil {
		emit = func(Change) {}
	}
	return &Filter{
		predicate: config.Predicate,
		emit:      emit,
		relations: make(map[RelationID]*relationState),
		cache:     make(map[cacheKey]cachedRow),
		txs:       make(map[TransactionID]*txState),
	}, nil
}

// New is a convenience constructor for a relation-aware predicate.
func New(predicate Predicate, emit EmitFunc) (*Filter, error) {
	return NewFilter(Config{Predicate: predicate, Emit: emit})
}

// NewRowFilter is a convenience constructor for predicates that only inspect
// the reconstructed row.
func NewRowFilter(predicate func(Row) bool, emit EmitFunc) (*Filter, error) {
	if predicate == nil {
		return nil, fmt.Errorf("%w: predicate is required", ErrInvalidConfig)
	}
	return New(func(_ Relation, row Row) bool { return predicate(row) }, emit)
}

// Apply consumes one event from the stream.
//
// Tuple rules:
//   - An UNCHANGED marker is valid only on a column whose metadata has
//     AllowUnchanged set.
//   - Insert rows must be complete and cannot contain markers.
//   - Update old rows may be partial. Missing old values and allowed markers
//     are filled from the exact relation-version/primary-key cache.
//   - Update new rows must contain explicit primary-key values and either a
//     value or an UNCHANGED marker for every non-key column. Markers can be
//     filled only when the primary key is unchanged. If that cache row is
//     absent, Apply returns a NeedsFetchError.
//   - Delete old rows may be partial but must include the primary key.
//
// No state is changed for an event that returns an error.
func (f *Filter) Apply(event Event) error {
	switch event := event.(type) {
	case Begin:
		return f.begin(event.XID)
	case Commit:
		return f.commit(event.XID)
	case Abort:
		return f.abort(event.XID)
	case RelationChange:
		return f.installRelation(event.Relation)
	case Insert:
		return f.applyInsert(event)
	case Update:
		return f.applyUpdate(event)
	case Delete:
		return f.applyDelete(event)
	default:
		return fmt.Errorf("%w: %T", ErrInvalidTuple, event)
	}
}

// Begin starts a transaction.
func (f *Filter) Begin(xid TransactionID) error {
	return f.begin(xid)
}

// Commit publishes a transaction.
func (f *Filter) Commit(xid TransactionID) error {
	return f.commit(xid)
}

// Abort discards a transaction.
func (f *Filter) Abort(xid TransactionID) error {
	return f.abort(xid)
}

// Relation installs or migrates a relation version.
func (f *Filter) Relation(relation Relation) error {
	return f.installRelation(relation)
}

// Handle is an alias for Apply.
func (f *Filter) Handle(event Event) error {
	return f.Apply(event)
}

// Insert stages an insert event. It is equivalent to Apply(Insert{...}).
func (f *Filter) Insert(xid TransactionID, relationID RelationID, row Row) error {
	return f.applyInsert(Insert{XID: xid, RelationID: relationID, Row: row})
}

// Update stages an update event. It is equivalent to Apply(Update{...}).
func (f *Filter) Update(xid TransactionID, relationID RelationID, oldKey Key, oldTuple, newTuple Row) error {
	return f.applyUpdate(Update{
		XID:        xid,
		RelationID: relationID,
		OldKey:     oldKey,
		OldTuple:   oldTuple,
		NewTuple:   newTuple,
	})
}

// Delete stages a delete event. It is equivalent to Apply(Delete{...}).
func (f *Filter) Delete(xid TransactionID, relationID RelationID, oldTuple Row) error {
	return f.applyDelete(Delete{XID: xid, RelationID: relationID, OldTuple: oldTuple})
}

// ResyncRelation installs a relation after manually resynchronizing its rows.
// It drops every cached row for the relation and allows a fresh column-ID
// history. Use it only after aborting or completing transactions that refer to
// the previous cache. Normal automatic migration refuses deleted/reused IDs.
func (f *Filter) ResyncRelation(relation Relation) error {
	next, err := newSchema(relation)
	if err != nil {
		return err
	}
	for key := range f.cache {
		if key.relation == relation.ID {
			delete(f.cache, key)
		}
	}
	state := &relationState{
		current:  next,
		versions: map[RelationVersion]*schema{next.relation.Version: next},
		history:  historyOf(next),
	}
	f.relations[relation.ID] = state
	return nil
}

func (f *Filter) begin(xid TransactionID) error {
	if _, exists := f.txs[xid]; exists {
		return fmt.Errorf("%w: %d", ErrTransactionExists, xid)
	}
	f.txs[xid] = &txState{overlay: make(map[cacheKey]txCacheEntry)}
	return nil
}

func (f *Filter) commit(xid TransactionID) error {
	tx, exists := f.txs[xid]
	if !exists {
		return fmt.Errorf("%w: %d", ErrUnknownTransaction, xid)
	}

	// Publish the private overlay before invoking the callback so observers see
	// the same state as the emitted changes.
	for key, entry := range tx.overlay {
		if entry.removed {
			delete(f.cache, key)
			continue
		}
		f.cache[key] = cachedRow{row: cloneRow(entry.row)}
	}
	delete(f.txs, xid)

	for _, change := range tx.buffered {
		f.emit(cloneChange(change))
	}
	return nil
}

func (f *Filter) abort(xid TransactionID) error {
	if _, exists := f.txs[xid]; !exists {
		return fmt.Errorf("%w: %d", ErrUnknownTransaction, xid)
	}
	delete(f.txs, xid)
	return nil
}

func (f *Filter) installRelation(relation Relation) error {
	next, err := newSchema(relation)
	if err != nil {
		return err
	}

	state, exists := f.relations[relation.ID]
	if !exists {
		f.relations[relation.ID] = &relationState{
			current:  next,
			versions: map[RelationVersion]*schema{next.relation.Version: next},
			history:  historyOf(next),
		}
		return nil
	}

	if next.relation.Version == state.current.relation.Version {
		if state.current.sameDefinition(next) {
			return nil
		}
		return fmt.Errorf("%w: relation %d version %d", ErrRelationVersionReuse, relation.ID, relation.Version)
	}
	if _, known := state.versions[next.relation.Version]; known {
		return fmt.Errorf("%w: relation %d version %d", ErrRelationVersionReuse, relation.ID, relation.Version)
	}

	if err := validateMigration(state, next); err != nil {
		return err
	}

	migrated, err := f.migrateCache(state.current, next)
	if err != nil {
		return err
	}

	// Commit the schema change only after every cached row has been migrated.
	for key := range f.cache {
		if key.relation == relation.ID && key.version == state.current.relation.Version {
			delete(f.cache, key)
		}
	}
	for key, row := range migrated {
		f.cache[key] = row
	}
	state.versions[next.relation.Version] = next
	state.current = next
	for id, column := range next.byID {
		state.history[id] = column
	}
	return nil
}

func validateMigration(old *relationState, next *schema) error {
	for _, oldColumn := range old.current.relation.Columns {
		newColumn, exists := next.byID[oldColumn.ID]
		if !exists {
			return &ColumnError{ColumnID: oldColumn.ID, Kind: ErrColumnDeleted, Reason: "column was deleted"}
		}
		if oldColumn.Type != newColumn.Type {
			return &ColumnError{ColumnID: oldColumn.ID, Kind: ErrColumnReused, Reason: "column type changed"}
		}
	}
	for _, newColumn := range next.relation.Columns {
		if oldColumn, exists := old.current.byID[newColumn.ID]; exists {
			if oldColumn.Type != newColumn.Type {
				return &ColumnError{ColumnID: newColumn.ID, Kind: ErrColumnReused, Reason: "column type changed"}
			}
			continue
		}
		if _, everKnown := old.history[newColumn.ID]; everKnown {
			return &ColumnError{ColumnID: newColumn.ID, Kind: ErrColumnReused, Reason: "column ID was previously deleted"}
		}
	}
	return nil
}

func (f *Filter) migrateCache(old, next *schema) (map[cacheKey]cachedRow, error) {
	migrated := make(map[cacheKey]cachedRow)
	for key, cached := range f.cache {
		if key.relation != old.relation.ID || key.version != old.relation.Version {
			continue
		}

		row := make(Row, len(next.relation.Columns))
		for _, column := range next.relation.Columns {
			if value, exists := cached.row[column.ID]; exists {
				row[column.ID] = cloneValue(value)
			}
		}

		nextKeyValues := make(Key, len(next.primary))
		for _, id := range next.primary {
			value, exists := row[id]
			if !exists || IsUnchanged(value) {
				return nil, fmt.Errorf("%w: relation %d version %d cannot derive key column %d",
					ErrCannotMigrate, next.relation.ID, next.relation.Version, id)
			}
			nextKeyValues[id] = value
		}

		nextKey := cacheKey{
			relation: next.relation.ID,
			version:  next.relation.Version,
			key:      keyString(nextKeyValues),
		}
		if _, duplicate := migrated[nextKey]; duplicate {
			return nil, fmt.Errorf("%w: duplicate migrated key for relation %d", ErrCannotMigrate, next.relation.ID)
		}
		migrated[nextKey] = cachedRow{row: cloneRow(row)}
	}
	return migrated, nil
}

func historyOf(schema *schema) map[ColumnID]Column {
	history := make(map[ColumnID]Column, len(schema.byID))
	for id, column := range schema.byID {
		history[id] = column
	}
	return history
}

func (f *Filter) applyInsert(event Insert) error {
	tx, state, err := f.eventContext(event.XID, event.RelationID)
	if err != nil {
		return err
	}
	row, err := completeTuple(state, event.Row, false)
	if err != nil {
		return err
	}
	key, err := primaryKeyFromTuple(state, row)
	if err != nil {
		return err
	}

	allowed := f.allows(state, row)
	action := cacheAction{key: f.cacheKey(state, key), del: !allowed}
	if allowed {
		action.row = cloneRow(row)
	}
	var change *Change
	if allowed {
		c := Change{
			Kind:            InsertChange,
			RelationID:      state.relation.ID,
			RelationVersion: state.relation.Version,
			Key:             cloneKey(key),
			Row:             cloneRow(row),
		}
		change = &c
	}
	tx.stage([]cacheAction{action}, change)
	return nil
}

func (f *Filter) applyDelete(event Delete) error {
	tx, state, err := f.eventContext(event.XID, event.RelationID)
	if err != nil {
		return err
	}
	if err := validateTupleIDs(state, event.OldTuple); err != nil {
		return err
	}
	key, err := primaryKeyFromTuple(state, event.OldTuple)
	if err != nil {
		return err
	}
	oldRow, err := f.reconstructOld(state, tx, key, event.OldTuple)
	if err != nil {
		return err
	}

	allowed := f.allows(state, oldRow)
	action := cacheAction{key: f.cacheKey(state, key), del: true}
	var change *Change
	if allowed {
		c := Change{
			Kind:            DeleteChange,
			RelationID:      state.relation.ID,
			RelationVersion: state.relation.Version,
			OldKey:          cloneKey(key),
		}
		change = &c
	}
	tx.stage([]cacheAction{action}, change)
	return nil
}

func (f *Filter) applyUpdate(event Update) error {
	tx, state, err := f.eventContext(event.XID, event.RelationID)
	if err != nil {
		return err
	}
	if err := validateTupleIDs(state, event.OldTuple); err != nil {
		return err
	}
	if err := validateTupleIDs(state, event.NewTuple); err != nil {
		return err
	}

	oldKey, err := normalizeKey(state, event.OldKey)
	if err != nil {
		return err
	}
	oldRow, err := f.reconstructOld(state, tx, oldKey, event.OldTuple)
	if err != nil {
		return err
	}
	newKey, err := primaryKeyFromTuple(state, event.NewTuple)
	if err != nil {
		return err
	}
	newRow, err := f.reconstructNew(state, tx, oldKey, newKey, event.NewTuple)
	if err != nil {
		return err
	}

	oldAllowed := f.allows(state, oldRow)
	newAllowed := f.allows(state, newRow)
	keyChanged := !sameKey(oldKey, newKey)

	actions := make([]cacheAction, 0, 3)
	if keyChanged {
		actions = append(actions,
			cacheAction{key: f.cacheKey(state, oldKey), del: true},
			cacheAction{key: f.cacheKey(state, newKey), del: true},
		)
	} else if !newAllowed {
		actions = append(actions, cacheAction{key: f.cacheKey(state, oldKey), del: true})
	}
	if newAllowed {
		actions = append(actions, cacheAction{
			key: f.cacheKey(state, newKey),
			row: cloneRow(newRow),
		})
	}

	var change *Change
	switch {
	case oldAllowed && newAllowed:
		c := Change{
			Kind:            UpdateChange,
			RelationID:      state.relation.ID,
			RelationVersion: state.relation.Version,
			OldKey:          cloneKey(oldKey),
			Key:             cloneKey(newKey),
			Row:             cloneRow(newRow),
		}
		change = &c
	case oldAllowed && !newAllowed:
		c := Change{
			Kind:            DeleteChange,
			RelationID:      state.relation.ID,
			RelationVersion: state.relation.Version,
			OldKey:          cloneKey(oldKey),
		}
		change = &c
	case !oldAllowed && newAllowed:
		c := Change{
			Kind:            InsertChange,
			RelationID:      state.relation.ID,
			RelationVersion: state.relation.Version,
			Key:             cloneKey(newKey),
			Row:             cloneRow(newRow),
		}
		change = &c
	}

	tx.stage(actions, change)
	return nil
}

func (f *Filter) eventContext(xid TransactionID, relationID RelationID) (*txState, *schema, error) {
	tx, exists := f.txs[xid]
	if !exists {
		return nil, nil, fmt.Errorf("%w: %d", ErrUnknownTransaction, xid)
	}
	state, exists := f.relations[relationID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: %d", ErrUnknownRelation, relationID)
	}
	return tx, state.current, nil
}

func (f *Filter) allows(state *schema, row Row) bool {
	relation := cloneRelation(state.relation)
	return f.predicate(relation, cloneRow(row))
}

func (f *Filter) cacheKey(state *schema, key Key) cacheKey {
	return cacheKey{
		relation: state.relation.ID,
		version:  state.relation.Version,
		key:      keyString(key),
	}
}

func (f *Filter) lookup(tx *txState, key cacheKey) (Row, bool) {
	if entry, exists := tx.overlay[key]; exists {
		if entry.removed {
			return nil, false
		}
		return entry.row, true
	}
	cached, exists := f.cache[key]
	if !exists {
		return nil, false
	}
	return cached.row, true
}

func (tx *txState) stage(actions []cacheAction, change *Change) {
	for _, action := range actions {
		if action.del {
			tx.overlay[action.key] = txCacheEntry{removed: true}
			continue
		}
		tx.overlay[action.key] = txCacheEntry{row: cloneRow(action.row)}
	}
	if change != nil {
		tx.buffered = append(tx.buffered, *change)
	}
}

func cloneChange(change Change) Change {
	return Change{
		Kind:            change.Kind,
		RelationID:      change.RelationID,
		RelationVersion: change.RelationVersion,
		OldKey:          cloneKey(change.OldKey),
		Key:             cloneKey(change.Key),
		Row:             cloneRow(change.Row),
	}
}

func completeTuple(state *schema, tuple Row, allowMarkers bool) (Row, error) {
	if err := validateTupleIDs(state, tuple); err != nil {
		return nil, err
	}
	row := make(Row, len(state.relation.Columns))
	missing := make([]ColumnID, 0)
	for _, column := range state.relation.Columns {
		value, exists := tuple[column.ID]
		if !exists {
			missing = append(missing, column.ID)
			continue
		}
		if IsUnchanged(value) {
			if !allowMarkers || !column.AllowUnchanged {
				return nil, fmt.Errorf("%w: column %d cannot be UNCHANGED", ErrInvalidTuple, column.ID)
			}
			missing = append(missing, column.ID)
			continue
		}
		row[column.ID] = cloneValue(value)
	}
	if len(missing) > 0 {
		sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
		return nil, fmt.Errorf("%w: insert row is missing columns %v", ErrInvalidTuple, missing)
	}
	return row, nil
}

func validateTupleIDs(state *schema, tuple Row) error {
	for id := range tuple {
		if _, exists := state.byID[id]; !exists {
			return fmt.Errorf("%w: unknown column ID %d", ErrInvalidTuple, id)
		}
	}
	return nil
}
