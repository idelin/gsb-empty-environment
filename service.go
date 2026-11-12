package netsec

// Service is a BACnet confirmed-service choice.
type Service uint8

// BACnet confirmed service choices relevant to gateway admission.
const (
	ServiceReadProperty               Service = 12
	ServiceReadPropertyMultiple       Service = 14
	ServiceWriteProperty              Service = 15
	ServiceWritePropertyMultiple      Service = 16
	ServiceDeviceCommunicationControl Service = 17
	ServiceReinitializeDevice         Service = 20
	ServiceLifeSafetyOperation        Service = 27
)

// IsControlService reports whether a service can change device operation or
// availability rather than write a normal application value. Control services
// are denied unless explicitly enabled on a Policy.
func (s Service) IsControlService() bool {
	switch s {
	case ServiceDeviceCommunicationControl, ServiceReinitializeDevice, ServiceLifeSafetyOperation:
		return true
	default:
		return false
	}
}

func (s Service) String() string {
	switch s {
	case ServiceReadProperty:
		return "ReadProperty"
	case ServiceReadPropertyMultiple:
		return "ReadPropertyMultiple"
	case ServiceWriteProperty:
		return "WriteProperty"
	case ServiceWritePropertyMultiple:
		return "WritePropertyMultiple"
	case ServiceDeviceCommunicationControl:
		return "DeviceCommunicationControl"
	case ServiceReinitializeDevice:
		return "ReinitializeDevice"
	case ServiceLifeSafetyOperation:
		return "LifeSafetyOperation"
	default:
		return "Service(" + itoa(uint16(s)) + ")"
	}
}

func itoa(v uint16) string {
	if v == 0 {
		return "0"
	}
	var buf [5]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
