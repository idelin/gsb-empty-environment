package replsim

// Replay delivers each request in order to a fresh node backed by storage
// and returns the responses. It is the deterministic event-replay driver
// used to reproduce a recorded message script.
func Replay(storage Storage, script []AppendRequest) ([]AppendResponse, error) {
	node, err := NewNode(storage)
	if err != nil {
		return nil, err
	}
	responses := make([]AppendResponse, 0, len(script))
	for _, req := range script {
		responses = append(responses, node.HandleAppend(req))
	}
	return responses, nil
}
