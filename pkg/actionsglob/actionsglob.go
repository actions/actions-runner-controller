package actionsglob

import (
	"fmt"
	"strings"
)

func Match(pat string, s string) bool {
	if len(pat) == 0 {
		panic(fmt.Sprintf("unexpected length of pattern: %d", len(pat)))
	}

	var inverse bool

	if pat[0] == '!' {
		pat = pat[1:]
		inverse = true
	}

	tokens := strings.SplitAfter(pat, "*")

	var wildcardInHead bool

	for i := 0; i < len(tokens); i++ {
		p := tokens[i]

		if p == "" {
			s = ""
			break
		}

		if p == "*" {
			if i == len(tokens)-1 {
				s = ""
				break
			}

			wildcardInHead = true

			continue
		}

		wildcardInTail := p[len(p)-1] == '*'
		if wildcardInTail {
			p = p[:len(p)-1]
		}

		subs := strings.SplitN(s, p, 2)

		if len(subs) == 0 {
			break
		}

		if subs[0] != "" {
			if !wildcardInHead {
				break
			}
		}

		if subs[1] != "" {
			if !wildcardInTail {
				break
			}
		}

		s = subs[1]

		wildcardInHead = wildcardInTail
	}

	r := s == ""

	if inverse {
		r = !r
	}

	return r
}
