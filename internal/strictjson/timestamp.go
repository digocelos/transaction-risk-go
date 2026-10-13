package strictjson

import "time"

// ParseTimestamp parses a strict RFC 3339 date-time with an explicit offset:
// uppercase T and Z, seconds required, 1 to 9 fractional digits, no leap
// second, and no -00:00 offset. The returned time keeps the received offset.
func ParseTimestamp(s string) (time.Time, bool) {
	if len(s) < len("2006-01-02T15:04:05Z") ||
		s[4] != '-' || s[7] != '-' || s[10] != 'T' || s[13] != ':' || s[16] != ':' {
		return time.Time{}, false
	}

	year, okYear := number(s[0:4])
	month, okMonth := number(s[5:7])
	day, okDay := number(s[8:10])
	hour, okHour := number(s[11:13])
	minute, okMinute := number(s[14:16])
	second, okSecond := number(s[17:19])

	if !okYear || !okMonth || !okDay || !okHour || !okMinute || !okSecond ||
		year < 1 || month < 1 || month > 12 || day < 1 ||
		hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, false
	}

	rest := s[19:]
	nanosecond := 0

	if rest[0] == '.' {
		end := 1
		for end < len(rest) && isDigit(rest[end]) {
			end++
		}

		digits := rest[1:end]
		if len(digits) < 1 || len(digits) > 9 {
			return time.Time{}, false
		}

		nanosecond, _ = number(digits)
		for range 9 - len(digits) {
			nanosecond *= 10
		}

		rest = rest[end:]
	}

	location, ok := offset(rest)
	if !ok {
		return time.Time{}, false
	}

	parsed := time.Date(
		year, time.Month(month), day,
		hour, minute, second, nanosecond,
		location,
	)

	// time.Date normalizes dates such as February 30; reject them instead.
	if parsed.Year() != year || parsed.Month() != time.Month(month) || parsed.Day() != day {
		return time.Time{}, false
	}

	return parsed, true
}

// offset parses Z or ±hh:mm. The offset -00:00 means an unknown local
// offset in RFC 3339, so it is rejected.
func offset(s string) (*time.Location, bool) {
	if s == "Z" {
		return time.UTC, true
	}

	if len(s) != len("+00:00") || (s[0] != '+' && s[0] != '-') || s[3] != ':' || s == "-00:00" {
		return nil, false
	}

	hours, okHours := number(s[1:3])
	minutes, okMinutes := number(s[4:6])
	if !okHours || !okMinutes || hours > 23 || minutes > 59 {
		return nil, false
	}

	seconds := hours*60*60 + minutes*60
	if s[0] == '-' {
		seconds = -seconds
	}

	return time.FixedZone("", seconds), true
}

// number parses a non-empty string of ASCII digits.
func number(s string) (int, bool) {
	if s == "" {
		return 0, false
	}

	value := 0
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return 0, false
		}
		value = value*10 + int(s[i]-'0')
	}

	return value, true
}
