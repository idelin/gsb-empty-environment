package recovery

func normalizeSendEvent(event SendEvent) SendEvent {
	if event.Time == 0 {
		if event.At != 0 {
			event.Time = event.At
		} else if event.Timestamp != 0 {
			event.Time = event.Timestamp
		}
	}
	if event.PacketNumberSpace != Initial {
		event.Space = event.PacketNumberSpace
	}
	if event.PacketNumber == 0 {
		switch {
		case event.Number != 0:
			event.PacketNumber = event.Number
		case event.PN != 0:
			event.PacketNumber = event.PN
		}
	}
	if event.Bytes == 0 {
		switch {
		case event.SentBytes != 0:
			event.Bytes = event.SentBytes
		case event.Size != 0:
			event.Bytes = event.Size
		case event.Length != 0:
			event.Bytes = event.Length
		}
	}
	event.AckEliciting = event.AckEliciting || event.AckElicits || event.IsAckEliciting
	event.PureACK = event.PureACK || event.IsPureACK
	return event
}

func normalizeAckEvent(event AckEvent) AckEvent {
	if event.Time == 0 {
		if event.At != 0 {
			event.Time = event.At
		} else if event.Timestamp != 0 {
			event.Time = event.Timestamp
		}
	}
	if event.PacketNumberSpace != Initial {
		event.Space = event.PacketNumberSpace
	}
	if event.LargestAcked == 0 {
		switch {
		case event.LargestAcknowledged != 0:
			event.LargestAcked = event.LargestAcknowledged
		case event.Largest != 0:
			event.LargestAcked = event.Largest
		}
	}
	if event.AckDelay == 0 && event.AckDelayMicros != 0 {
		event.AckDelay = event.AckDelayMicros
	}
	return event
}

func normalizePathEvent(event PathEvent) PathEvent {
	if event.Time == 0 && event.At != 0 {
		event.Time = event.At
	}
	if event.PathValidated || event.Validated {
		validated := true
		event.PeerCompletedAddressValidation = &validated
	}
	if event.AntiAmplificationUnblocked {
		limited := false
		event.AntiAmplificationLimited = &limited
	}
	return event
}
