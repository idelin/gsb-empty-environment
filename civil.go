package chronos

// Calendar math (Howard Hinnant's algorithms), proleptic Gregorian.

func divFloor(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func modFloor(a, b int64) int64 { return a - divFloor(a, b)*b }

func daysFromCivil(y int32, m, d uint8) int64 {
	yy := int64(y)
	if m <= 2 {
		yy--
	}
	era := divFloor(yy, 400)
	yoe := yy - era*400
	mp := modFloor(int64(m)+9, 12)
	doy := (153*mp+2)/5 + int64(d) - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

func civilFromDays(z int64) (int32, uint8, uint8) {
	z += 719468
	era := divFloor(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	m := mp + 3
	if mp >= 10 {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return int32(y), uint8(m), uint8(d)
}

func isLeap(y int32) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

func daysInMonth(y int32, m uint8) uint8 {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeap(y) {
			return 29
		}
		return 28
	}
	return 0
}
