package httphandlers

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var singleRequireRE = regexp.MustCompile(`^require\s+(\S+)\s+(\S+)`)

func parseGoModRequires(content string) ([]modReq, error) {
	var (
		reqs    []modReq
		inBlock bool
	)
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "require (") {
			inBlock = true
			continue
		}
		if inBlock && line == ")" {
			inBlock = false
			continue
		}
		if inBlock {
			line = strings.Split(line, "//")[0]
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				path, err := decodeGoModToken(fields[0])
				if err != nil {
					return nil, err
				}
				ver, err := decodeGoModToken(fields[1])
				if err != nil {
					return nil, err
				}
				reqs = append(reqs, modReq{Path: path, Version: ver})
			}
			continue
		}
		m := singleRequireRE.FindStringSubmatch(line)
		if len(m) == 3 {
			path, err := decodeGoModToken(m[1])
			if err != nil {
				return nil, err
			}
			ver, err := decodeGoModToken(m[2])
			if err != nil {
				return nil, err
			}
			reqs = append(reqs, modReq{Path: path, Version: ver})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return reqs, nil
}

func decodeGoModToken(tok string) (string, error) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return "", errors.New("empty token in go.mod")
	}
	if strings.HasPrefix(tok, `"`) && strings.HasSuffix(tok, `"`) {
		v, err := strconv.Unquote(tok)
		if err != nil {
			return "", fmt.Errorf("invalid quoted token %q: %w", tok, err)
		}
		return v, nil
	}
	return tok, nil
}
