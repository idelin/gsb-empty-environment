package netsec

import (
	"fmt"
)

type schema struct {
	relation Relation
	byID     map[ColumnID]Column
	primary  []ColumnID
}

func newSchema(relation Relation) (*schema, error) {
	if len(relation.Columns) == 0 {
		return nil, fmt.Errorf("%w: relation %d has no columns", ErrInvalidRelation, relation.ID)
	}
	if len(relation.PrimaryKey) == 0 {
		return nil, fmt.Errorf("%w: relation %d has no primary key", ErrInvalidRelation, relation.ID)
	}

	columns := make([]Column, len(relation.Columns))
	byID := make(map[ColumnID]Column, len(relation.Columns))
	for i, column := range relation.Columns {
		if _, exists := byID[column.ID]; exists {
			return nil, fmt.Errorf("%w: relation %d has duplicate column ID %d", ErrInvalidRelation, relation.ID, column.ID)
		}
		columns[i] = column
		byID[column.ID] = column
	}

	primary := make([]ColumnID, len(relation.PrimaryKey))
	seenPrimary := make(map[ColumnID]struct{}, len(relation.PrimaryKey))
	for i, id := range relation.PrimaryKey {
		if _, exists := byID[id]; !exists {
			return nil, fmt.Errorf("%w: relation %d primary key references missing column %d", ErrInvalidRelation, relation.ID, id)
		}
		if _, exists := seenPrimary[id]; exists {
			return nil, fmt.Errorf("%w: relation %d repeats primary-key column %d", ErrInvalidRelation, relation.ID, id)
		}
		seenPrimary[id] = struct{}{}
		primary[i] = id
	}

	relation.Columns = columns
	relation.PrimaryKey = primary
	return &schema{relation: relation, byID: byID, primary: primary}, nil
}

func (s *schema) column(id ColumnID) (Column, bool) {
	column, ok := s.byID[id]
	return column, ok
}

func (s *schema) isPrimary(id ColumnID) bool {
	for _, primaryID := range s.primary {
		if primaryID == id {
			return true
		}
	}
	return false
}

func (s *schema) sameDefinition(other *schema) bool {
	if s.relation.ID != other.relation.ID || s.relation.Version != other.relation.Version {
		return false
	}
	if len(s.relation.PrimaryKey) != len(other.relation.PrimaryKey) {
		return false
	}
	for i := range s.relation.PrimaryKey {
		if s.relation.PrimaryKey[i] != other.relation.PrimaryKey[i] {
			return false
		}
	}
	if len(s.relation.Columns) != len(other.relation.Columns) {
		return false
	}
	for i := range s.relation.Columns {
		if s.relation.Columns[i] != other.relation.Columns[i] {
			return false
		}
	}
	return true
}

func cloneRelation(relation Relation) Relation {
	relation.Columns = append([]Column(nil), relation.Columns...)
	relation.PrimaryKey = append([]ColumnID(nil), relation.PrimaryKey...)
	return relation
}

func cloneRow(row Row) Row {
	if row == nil {
		return nil
	}
	out := make(Row, len(row))
	for id, value := range row {
		out[id] = cloneValue(value)
	}
	return out
}

func cloneKey(key Key) Key {
	if key == nil {
		return nil
	}
	out := make(Key, len(key))
	for id, value := range key {
		out[id] = cloneValue(value)
	}
	return out
}

func cloneValue(value any) any {
	switch value := value.(type) {
	case []byte:
		return append([]byte(nil), value...)
	case []any:
		return append([]any(nil), value...)
	case Row:
		return cloneRow(value)
	case Key:
		return cloneKey(value)
	default:
		return value
	}
}

func sameKey(a, b Key) bool {
	return keyString(a) == keyString(b)
}
