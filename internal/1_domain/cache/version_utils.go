package cache

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func CompareModuleVersions(a, b string) int {
	pa, oka := parseModuleVersion(a)
	pb, okb := parseModuleVersion(b)
	if !oka || !okb {
		return strings.Compare(a, b)
	}

	for i := 0; i < 3; i++ {
		if pa.core[i] != pb.core[i] {
			if pa.core[i] > pb.core[i] {
				return 1
			}
			return -1
		}
	}
	if pa.pre == "" && pb.pre == "" {
		return 0
	}
	if pa.pre == "" {
		return 1
	}
	if pb.pre == "" {
		return -1
	}
	return comparePreRelease(pa.pre, pb.pre)
}

type parsedModuleVersion struct {
	core [3]int64
	pre  string
}

func parseModuleVersion(v string) (parsedModuleVersion, bool) {
	if !strings.HasPrefix(v, "v") {
		return parsedModuleVersion{}, false
	}
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")

	main := v
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		main = v[:i]
		pre = v[i+1:]
	}

	parts := strings.Split(main, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return parsedModuleVersion{}, false
	}

	var core [3]int64
	for i := 0; i < len(parts); i++ {
		if parts[i] == "" {
			return parsedModuleVersion{}, false
		}
		n, err := strconv.ParseInt(parts[i], 10, 64)
		if err != nil {
			return parsedModuleVersion{}, false
		}
		core[i] = n
	}
	return parsedModuleVersion{core: core, pre: pre}, true
}

func comparePreRelease(a, b string) int {
	ai := strings.Split(a, ".")
	bi := strings.Split(b, ".")
	n := len(ai)
	if len(bi) < n {
		n = len(bi)
	}
	for i := 0; i < n; i++ {
		if ai[i] == bi[i] {
			continue
		}
		an, aNum := parseNumericIdentifier(ai[i])
		bn, bNum := parseNumericIdentifier(bi[i])
		if aNum && bNum {
			if an > bn {
				return 1
			}
			return -1
		}
		if aNum {
			return -1
		}
		if bNum {
			return 1
		}
		if ai[i] > bi[i] {
			return 1
		}
		return -1
	}
	if len(ai) == len(bi) {
		return 0
	}
	if len(ai) > len(bi) {
		return 1
	}
	return -1
}

func parseNumericIdentifier(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func EscapeModulePath(s string) (string, error) {
	return escapeString(s, true)
}

func EscapeModuleVersion(s string) (string, error) {
	return escapeString(s, false)
}

func escapeString(s string, isPath bool) (string, error) {
	if s == "" {
		return "", errors.New("empty value")
	}
	var b strings.Builder
	for _, r := range s {
		if r == '!' {
			b.WriteString("!!")
			continue
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("invalid character %q", r)
		}
		if !isPath && r == '/' {
			return "", errors.New("version must not contain slash")
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}

func UnescapeModulePath(s string) (string, error) {
	return unescapeString(s)
}

func UnescapeModuleVersion(s string) (string, error) {
	return unescapeString(s)
}

func unescapeString(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '!' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", errors.New("invalid escape")
		}
		next := s[i+1]
		if next == '!' {
			b.WriteByte('!')
			i++
			continue
		}
		if next < 'a' || next > 'z' {
			return "", errors.New("invalid escaped letter")
		}
		b.WriteByte(next - 'a' + 'A')
		i++
	}
	return b.String(), nil
}
