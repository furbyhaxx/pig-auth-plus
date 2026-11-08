package modelsfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// stripped returns data with comments and trailing commas blanked by spaces, so the result is
// plain JSON whose byte offsets equal those of data. trailing lists the blanked commas.
func stripped(data []byte) (plain []byte, trailing []int, err error) {
	plain = bytes.Clone(data)
	i := 0
	var lastComma = -1
	for i < len(plain) {
		switch c := plain[i]; {
		case c == '"':
			i++
			for i < len(plain) && plain[i] != '"' {
				if plain[i] == '\\' {
					i++
				}
				i++
			}
			i++
			lastComma = -1
		case c == '/' && i+1 < len(plain) && plain[i+1] == '/':
			for i < len(plain) && plain[i] != '\n' {
				plain[i] = ' '
				i++
			}
		case c == '/' && i+1 < len(plain) && plain[i+1] == '*':
			end := bytes.Index(plain[i+2:], []byte("*/"))
			if end < 0 {
				return nil, nil, errors.New("unterminated comment")
			}
			for j := i; j < i+2+end+2; j++ {
				if plain[j] != '\n' {
					plain[j] = ' '
				}
			}
			i += 2 + end + 2
		case c == ',':
			lastComma = i
			i++
		case c == '}' || c == ']':
			if lastComma >= 0 {
				plain[lastComma] = ' '
				trailing = append(trailing, lastComma)
			}
			lastComma = -1
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		default:
			lastComma = -1
			i++
		}
	}
	if !json.Valid(plain) {
		return nil, nil, errors.New("not valid JSON or JSONC")
	}
	return plain, trailing, nil
}

type member struct {
	key        string
	start, end int // key start .. value end
	val        *node
}

type node struct {
	obj        bool
	start, end int
	members    []member
}

// parse builds offset-carrying nodes for valid JSON; non-object values are leaves.
func parse(b []byte) (*node, error) {
	p := &parser{b: b}
	p.ws()
	n, err := p.value()
	if err != nil {
		return nil, err
	}
	return n, nil
}

type parser struct {
	b []byte
	i int
}

func (p *parser) ws() {
	for p.i < len(p.b) && (p.b[p.i] == ' ' || p.b[p.i] == '\t' || p.b[p.i] == '\n' || p.b[p.i] == '\r') {
		p.i++
	}
}

func (p *parser) str() (string, error) {
	start := p.i
	p.i++
	for p.i < len(p.b) && p.b[p.i] != '"' {
		if p.b[p.i] == '\\' {
			p.i++
		}
		p.i++
	}
	p.i++
	var s string
	if err := json.Unmarshal(p.b[start:p.i], &s); err != nil {
		return "", fmt.Errorf("bad string at %d: %w", start, err)
	}
	return s, nil
}

func (p *parser) value() (*node, error) {
	p.ws()
	if p.i >= len(p.b) {
		return nil, errors.New("unexpected end")
	}
	n := &node{start: p.i}
	switch p.b[p.i] {
	case '{':
		n.obj = true
		p.i++
		for {
			p.ws()
			if p.b[p.i] == '}' {
				p.i++
				break
			}
			if p.b[p.i] == ',' {
				p.i++
				continue
			}
			ks := p.i
			key, err := p.str()
			if err != nil {
				return nil, err
			}
			p.ws()
			p.i++ // ':'
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			n.members = append(n.members, member{key: key, start: ks, end: v.end, val: v})
		}
	case '[':
		p.i++
		for {
			p.ws()
			if p.b[p.i] == ']' {
				p.i++
				break
			}
			if p.b[p.i] == ',' {
				p.i++
				continue
			}
			if _, err := p.value(); err != nil {
				return nil, err
			}
		}
	case '"':
		if _, err := p.str(); err != nil {
			return nil, err
		}
	default:
		for p.i < len(p.b) && !bytes.ContainsRune([]byte(",}] \t\n\r"), rune(p.b[p.i])) {
			p.i++
		}
	}
	n.end = p.i
	return n, nil
}

func (n *node) find(key string) *member {
	for i := range n.members {
		if n.members[i].key == key {
			return &n.members[i]
		}
	}
	return nil
}
