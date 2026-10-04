package snapcatchup

import (
	"encoding/json"
	"strings"
)

// StateMachine is a tiny kv store used to demonstrate snapshot install and
// log replay. Applied counts executed commands so tests can detect commands
// replayed twice across the snapshot boundary.
type StateMachine struct {
	Data    map[string]string `json:"data"`
	Applied uint64            `json:"applied"`
}

func NewStateMachine() *StateMachine {
	return &StateMachine{Data: make(map[string]string)}
}

// Apply executes a "set key value" command.
func (sm *StateMachine) Apply(cmd string) {
	parts := strings.SplitN(cmd, " ", 3)
	if len(parts) == 3 && parts[0] == "set" {
		sm.Data[parts[1]] = parts[2]
	}
	sm.Applied++
}

func (sm *StateMachine) Encode() []byte {
	b, _ := json.Marshal(sm)
	return b
}

func DecodeStateMachine(b []byte) (*StateMachine, error) {
	sm := NewStateMachine()
	if err := json.Unmarshal(b, sm); err != nil {
		return nil, err
	}
	if sm.Data == nil {
		sm.Data = make(map[string]string)
	}
	return sm, nil
}
