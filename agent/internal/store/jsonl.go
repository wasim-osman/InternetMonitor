package store

import (
	"encoding/json"
	"math"
	"strconv"
	"unsafe"
)

// appendJSONString appends s as a JSON string literal.
func appendJSONString(b []byte, s string) []byte {
	const hex = "0123456789abcdef"
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			b = append(b, '\\', c)
		case c == '\n':
			b = append(b, '\\', 'n')
		case c == '\r':
			b = append(b, '\\', 'r')
		case c == '\t':
			b = append(b, '\\', 't')
		case c < 0x20:
			b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
		default:
			b = append(b, c)
		}
	}
	return append(b, '"')
}

// appendNum appends a float with up to 3 decimals.
func appendNum(b []byte, v float64) []byte {
	return strconv.AppendFloat(b, math.Round(v*1000)/1000, 'f', -1, 64)
}

// scanFlat iterates over the key/value pairs of a flat JSON object line
// (string/number/bool/null values only) without allocating. Values of string
// type are returned raw (still quoted). Returns false on malformed input.
func scanFlat(line []byte, fn func(key, val []byte)) bool {
	i := 0
	n := len(line)
	skipWS := func() {
		for i < n && (line[i] == ' ' || line[i] == '\t' || line[i] == '\r' || line[i] == '\n') {
			i++
		}
	}
	readString := func() (int, int, bool) { // returns span including quotes
		if i >= n || line[i] != '"' {
			return 0, 0, false
		}
		s := i
		i++
		for i < n {
			switch line[i] {
			case '\\':
				i += 2
				continue
			case '"':
				i++
				return s, i, true
			}
			i++
		}
		return 0, 0, false
	}
	skipWS()
	if i >= n || line[i] != '{' {
		return false
	}
	i++
	for {
		skipWS()
		if i < n && line[i] == '}' {
			return true
		}
		ks, ke, ok := readString()
		if !ok {
			return false
		}
		skipWS()
		if i >= n || line[i] != ':' {
			return false
		}
		i++
		skipWS()
		var vs, ve int
		if i < n && line[i] == '"' {
			vs, ve, ok = readString()
			if !ok {
				return false
			}
		} else {
			vs = i
			for i < n && line[i] != ',' && line[i] != '}' && line[i] != ' ' {
				i++
			}
			ve = i
			if ve == vs {
				return false
			}
		}
		fn(line[ks+1:ke-1], line[vs:ve])
		skipWS()
		if i >= n {
			return false
		}
		if line[i] == ',' {
			i++
			continue
		}
		if line[i] == '}' {
			return true
		}
		return false
	}
}

func bstr(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// numVal parses a number value; null/invalid -> NaN.
func numVal(v []byte) float64 {
	if len(v) == 0 || v[0] == 'n' || v[0] == '"' {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(bstr(v), 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

func intVal(v []byte) int64 {
	var x int64
	neg := false
	for i, c := range v {
		if i == 0 && c == '-' {
			neg = true
			continue
		}
		if c < '0' || c > '9' {
			if c == '.' {
				break
			}
			return 0
		}
		x = x*10 + int64(c-'0')
	}
	if neg {
		return -x
	}
	return x
}

// strVal unquotes a raw string value (allocates).
func strVal(v []byte) string {
	if len(v) < 2 || v[0] != '"' {
		return ""
	}
	inner := v[1 : len(v)-1]
	for _, c := range inner {
		if c == '\\' {
			var s string
			if json.Unmarshal(v, &s) == nil {
				return s
			}
			return ""
		}
	}
	return string(inner)
}

// rawStrEq compares a raw quoted value with s without allocating.
func rawStrEq(v []byte, s string) bool {
	return len(v) == len(s)+2 && v[0] == '"' && bstr(v[1:len(v)-1]) == s
}
