// Copyright 2025 xDarkicex
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// This file is also available under the MIT License terms at:
// https://opensource.org/licenses/MIT

package lexer

import (
	"encoding/binary"
)

// Kind enumerates token types emitted by the lexer.
type Kind uint8

const (
	KindEOF Kind = iota
	KindError
	KindWhitespace

	// Literals & Identifiers
	KindIdentifier
	KindString
	KindNumber

	// Operators & Symbols
	KindComma        // ,
	KindLeftParen    // (
	KindRightParen   // )
	KindLeftBracket  // [
	KindRightBracket // ]
	KindAsterisk     // *
	KindEquals       // =
	KindGreaterThan  // >
	KindLessThan     // <
	KindDot          // .
	KindArrowRight   // ->
	KindArrowLeft    // <-
	KindDash         // -
	KindLeftBrace    // {
	KindRightBrace   // }
	KindPlus         // +
	KindL2Dist       // <->
	KindIPDist       // <#>
	KindCosineDist   // <=>

	// Relational Keywords
	KindSelect
	KindFrom
	KindWhere
	KindAnd
	KindOr
	KindOrder
	KindBy
	KindDesc
	KindLimit
	KindJoin
	KindLeft    // LEFT JOIN
	KindRight   // RIGHT JOIN
	KindInner   // INNER JOIN
	KindOuter   // OUTER (used with LEFT/RIGHT)
	KindFull    // FULL JOIN
	KindCross   // CROSS JOIN
	KindOn
	KindAs
	KindBetween
	KindIn
	KindNot

	// Graph (PGQ) Keywords
	KindMatch
	KindGraphTable
	KindVertex
	KindEdge

	// Vector/ML Keywords
	KindVectorDistance
	KindSimilarity

	// CRUD Keywords
	KindInsert // INSERT
	KindInto   // INTO
	KindValues // VALUES
	KindUpdate // UPDATE
	KindSet    // SET
	KindDelete // DELETE

	// Aggregate Keywords
	KindCount // COUNT
	KindSum   // SUM
	KindAvg   // AVG
	KindMin   // MIN
	KindMax   // MAX

	// DDL Keywords
	KindCreate      // CREATE
	KindTable       // TABLE
	KindIndex       // INDEX
	KindDrop        // DROP
	KindAlter       // ALTER
	KindAdd         // ADD
	KindGroup       // GROUP
	KindHaving      // HAVING
	KindPrimary     // PRIMARY
	KindKey         // KEY
	KindNull        // NULL
	KindUnique      // UNIQUE
	KindDefault     // DEFAULT
	KindConstraint  // CONSTRAINT
	KindForeign     // FOREIGN
	KindReferences  // REFERENCES
	KindCheck       // CHECK
	KindIf          // IF
	KindExists      // EXISTS

	KindColon // :
)

// Token is a single lexer emission (iterator pattern).
type Token struct {
	Start uint32
	End   uint32
	Kind  Kind
}

// TokenStream is a SoA representation of a parsed token stream.
// Hot fields are contiguous to maximize cache locality.
type TokenStream struct {
	Starts []uint32
	Ends   []uint32
	Kinds  []Kind
}

// Scanner holds lexer state.
type Scanner struct {
	src []byte
	pos uint32
}

// New returns a Scanner over src. The scanner keeps a slice header only;
// it does not retain src beyond the call.
func New(src []byte) *Scanner {
	return &Scanner{src: src}
}

// Next advances and returns the next token. Returns (Token{}, false) on EOF.
func (s *Scanner) Next() (Token, bool) {
	if int(s.pos) >= len(s.src) {
		return Token{Kind: KindEOF}, false
	}

	start := s.pos
	c := s.src[s.pos]

	// 0. SQL comments — treated as whitespace so the parser skips them.
	//    Handles both line comments (-- ...) and block comments (/* ... */).
	if c == '-' && int(s.pos)+1 < len(s.src) && s.src[s.pos+1] == '-' {
		// Line comment: skip to end of line
		s.pos += 2
		for int(s.pos) < len(s.src) && s.src[s.pos] != '\n' {
			s.pos++
		}
		return Token{Start: start, End: s.pos, Kind: KindWhitespace}, true
	}
	if c == '/' && int(s.pos)+1 < len(s.src) && s.src[s.pos+1] == '*' {
		// Block comment: skip to closing */
		s.pos += 2
		for int(s.pos)+1 < len(s.src) {
			if s.src[s.pos] == '*' && s.src[s.pos+1] == '/' {
				s.pos += 2
				break
			}
			s.pos++
		}
		return Token{Start: start, End: s.pos, Kind: KindWhitespace}, true
	}

	// 1. Whitespace SWAR acceleration
	if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
		s.pos++
		// Use SkipWS to jump ahead rapidly
		for int(s.pos) < len(s.src) {
			b := s.src[s.pos]
			if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
				s.pos++
			} else {
				break
			}
		}
		return Token{Start: start, End: s.pos, Kind: KindWhitespace}, true
	}

	// 2. Symbols
	switch c {
	case ',':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindComma}, true
	case '(':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindLeftParen}, true
	case ')':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindRightParen}, true
	case '[':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindLeftBracket}, true
	case ']':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindRightBracket}, true
	case '{':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindLeftBrace}, true
	case '}':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindRightBrace}, true
	case '+':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindPlus}, true
	case '*':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindAsterisk}, true
	case '=':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindEquals}, true
	case '>':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindGreaterThan}, true
	case '<':
		s.pos++
		if int(s.pos) < len(s.src) {
			switch s.src[s.pos] {
			case '-':
				s.pos++
				if int(s.pos) < len(s.src) && s.src[s.pos] == '>' {
					s.pos++
					return Token{Start: start, End: s.pos, Kind: KindL2Dist}, true
				}
				return Token{Start: start, End: s.pos, Kind: KindArrowLeft}, true
			case '#':
				if int(s.pos+1) < len(s.src) && s.src[s.pos+1] == '>' {
					s.pos += 2
					return Token{Start: start, End: s.pos, Kind: KindIPDist}, true
				}
				return Token{Start: start, End: s.pos - 1, Kind: KindLessThan}, true
			case '=':
				s.pos++
				if int(s.pos) < len(s.src) && s.src[s.pos] == '>' {
					s.pos++
					return Token{Start: start, End: s.pos, Kind: KindCosineDist}, true
				}
				s.pos-- // backtrack: <= without > is just < (LessThan)
				return Token{Start: start, End: s.pos, Kind: KindLessThan}, true
			}
		}
		return Token{Start: start, End: s.pos, Kind: KindLessThan}, true
	case '.':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindDot}, true
	case '-':
		s.pos++
		if int(s.pos) < len(s.src) && s.src[s.pos] == '>' {
			s.pos++
			return Token{Start: start, End: s.pos, Kind: KindArrowRight}, true
		}
		return Token{Start: start, End: s.pos, Kind: KindDash}, true
	case '\'':
		s.pos++
		for int(s.pos) < len(s.src) && s.src[s.pos] != '\'' {
			s.pos++
		}
		if int(s.pos) < len(s.src) {
			s.pos++ // consume closing quote
		}
		return Token{Start: start, End: s.pos, Kind: KindString}, true

	case ':':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindColon}, true
	}

	// 3. Numbers
	if c >= '0' && c <= '9' {
		for int(s.pos) < len(s.src) {
			b := s.src[s.pos]
			if (b >= '0' && b <= '9') || (b == '.' && !(int(s.pos+1) < len(s.src) && s.src[s.pos+1] == '.')) {
				s.pos++
			} else {
				break
			}
		}
		return Token{Start: start, End: s.pos, Kind: KindNumber}, true
	}

	// 4. Identifiers and Keywords
	// Scan until non-alphanumeric/underscore
	for int(s.pos) < len(s.src) {
		b := s.src[s.pos]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' {
			s.pos++
		} else {
			break
		}
	}

	// Fallback to prevent infinite loops on unrecognized characters
	if s.pos == start {
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindError}, true
	}

	// Fast Path Keyword Identification
	length := s.pos - start
	kind := KindIdentifier
	
	// We check exact byte length first to avoid unnecessary string conversions or slow checks
	if length >= 2 && length <= 15 {
		// Exact length switches
		switch length {
		case 2:
			if caseInsensitiveMatch(s.src[start:start+2], "as") {
				kind = KindAs
			} else if caseInsensitiveMatch(s.src[start:start+2], "by") {
				kind = KindBy
			} else if caseInsensitiveMatch(s.src[start:start+2], "in") {
				kind = KindIn
			} else if caseInsensitiveMatch(s.src[start:start+2], "on") {
				kind = KindOn
			} else if caseInsensitiveMatch(s.src[start:start+2], "or") {
				kind = KindOr
			} else if caseInsensitiveMatch(s.src[start:start+2], "if") {
				kind = KindIf
			}
		case 3:
			if caseInsensitiveMatch(s.src[start:start+3], "and") {
				kind = KindAnd
			} else if caseInsensitiveMatch(s.src[start:start+3], "not") {
				kind = KindNot
			} else if caseInsensitiveMatch(s.src[start:start+3], "set") {
				kind = KindSet
			} else if caseInsensitiveMatch(s.src[start:start+3], "sum") {
				kind = KindSum
			} else if caseInsensitiveMatch(s.src[start:start+3], "avg") {
				kind = KindAvg
			} else if caseInsensitiveMatch(s.src[start:start+3], "min") {
				kind = KindMin
			} else if caseInsensitiveMatch(s.src[start:start+3], "max") {
				kind = KindMax
			} else if caseInsensitiveMatch(s.src[start:start+3], "add") {
				kind = KindAdd
			} else if caseInsensitiveMatch(s.src[start:start+3], "key") {
				kind = KindKey
			}
		case 4:
			if caseInsensitiveMatch(s.src[start:start+4], "from") {
				kind = KindFrom
			} else if caseInsensitiveMatch(s.src[start:start+4], "into") {
				kind = KindInto
			} else if caseInsensitiveMatch(s.src[start:start+4], "join") {
				kind = KindJoin
			} else if caseInsensitiveMatch(s.src[start:start+4], "desc") {
				kind = KindDesc
			} else if caseInsensitiveMatch(s.src[start:start+4], "drop") {
				kind = KindDrop
			} else if caseInsensitiveMatch(s.src[start:start+4], "left") {
				kind = KindLeft
			} else if caseInsensitiveMatch(s.src[start:start+4], "full") {
				kind = KindFull
			} else if caseInsensitiveMatch(s.src[start:start+4], "null") {
				kind = KindNull
			}
		case 5:
			if caseInsensitiveMatch(s.src[start:start+5], "where") {
				kind = KindWhere
			} else if caseInsensitiveMatch(s.src[start:start+5], "order") {
				kind = KindOrder
			} else if caseInsensitiveMatch(s.src[start:start+5], "limit") {
				kind = KindLimit
			} else if caseInsensitiveMatch(s.src[start:start+5], "match") {
				kind = KindMatch
			} else if caseInsensitiveMatch(s.src[start:start+5], "count") {
				kind = KindCount
			} else if caseInsensitiveMatch(s.src[start:start+5], "table") {
				kind = KindTable
			} else if caseInsensitiveMatch(s.src[start:start+5], "group") {
				kind = KindGroup
			} else if caseInsensitiveMatch(s.src[start:start+5], "index") {
				kind = KindIndex
			} else if caseInsensitiveMatch(s.src[start:start+5], "right") {
				kind = KindRight
			} else if caseInsensitiveMatch(s.src[start:start+5], "inner") {
				kind = KindInner
			} else if caseInsensitiveMatch(s.src[start:start+5], "outer") {
				kind = KindOuter
			} else if caseInsensitiveMatch(s.src[start:start+5], "cross") {
				kind = KindCross
			} else if caseInsensitiveMatch(s.src[start:start+5], "alter") {
				kind = KindAlter
			} else if caseInsensitiveMatch(s.src[start:start+5], "check") {
				kind = KindCheck
			}
		case 6:
			if caseInsensitiveMatch(s.src[start:start+6], "select") {
				kind = KindSelect
			} else if caseInsensitiveMatch(s.src[start:start+6], "insert") {
				kind = KindInsert
			} else if caseInsensitiveMatch(s.src[start:start+6], "update") {
				kind = KindUpdate
			} else if caseInsensitiveMatch(s.src[start:start+6], "delete") {
				kind = KindDelete
			} else if caseInsensitiveMatch(s.src[start:start+6], "values") {
				kind = KindValues
			} else if caseInsensitiveMatch(s.src[start:start+6], "vertex") {
				kind = KindVertex
			} else if caseInsensitiveMatch(s.src[start:start+6], "having") {
				kind = KindHaving
			} else if caseInsensitiveMatch(s.src[start:start+6], "create") {
				kind = KindCreate
			} else if caseInsensitiveMatch(s.src[start:start+6], "unique") {
				kind = KindUnique
			} else if caseInsensitiveMatch(s.src[start:start+6], "exists") {
				kind = KindExists
			}
		case 7:
			if caseInsensitiveMatch(s.src[start:start+7], "between") {
				kind = KindBetween
			} else if caseInsensitiveMatch(s.src[start:start+7], "primary") {
				kind = KindPrimary
			} else if caseInsensitiveMatch(s.src[start:start+7], "default") {
				kind = KindDefault
			} else if caseInsensitiveMatch(s.src[start:start+7], "foreign") {
				kind = KindForeign
			}
		case 10:
			if caseInsensitiveMatch(s.src[start:start+10], "similarity") {
				kind = KindSimilarity
			} else if caseInsensitiveMatch(s.src[start:start+10], "constraint") {
				kind = KindConstraint
			} else if caseInsensitiveMatch(s.src[start:start+10], "references") {
				kind = KindReferences
			}
		case 11:
			if caseInsensitiveMatch(s.src[start:start+11], "graph_table") {
				kind = KindGraphTable
			}
		case 15:
			if caseInsensitiveMatch(s.src[start:start+15], "vector_distance") {
				kind = KindVectorDistance
			}
		}
	}

	return Token{Start: start, End: s.pos, Kind: kind}, true
}

// caseInsensitiveMatch is a SWAR-accelerated 8-byte masking match for SQL keywords.
// For short keywords <= 8 bytes, it processes them in a single uint64 comparison.
// Note: target MUST be lowercase string of exact length as src
func caseInsensitiveMatch(src []byte, target string) bool {
	l := len(src)
	if l != len(target) {
		return false
	}
	
	// SWAR fast path for <= 8 bytes
	if l <= 8 {
		var buf [8]byte
		copy(buf[:], src)
		// 0x20 OR mask forces ASCII lowercase
		// We only mask the bytes that are actually part of the string
		mask := uint64(0)
		for i := 0; i < l; i++ {
			mask |= uint64(0x20) << (i * 8)
		}
		
		v := binary.LittleEndian.Uint64(buf[:])
		v |= mask
		
		var tbuf [8]byte
		copy(tbuf[:], target)
		tv := binary.LittleEndian.Uint64(tbuf[:])
		
		// Zero out the padded bytes beyond length l
		shift := (8 - l) * 8
		v = (v << shift) >> shift
		tv = (tv << shift) >> shift
		
		return v == tv
	}
	
	// Fallback for > 8 bytes (VectorDistance, GraphTable, Similarity)
	for i := 0; i < l; i++ {
		c := src[i]
		if c >= 'A' && c <= 'Z' {
			c |= 0x20 // lowercase
		}
		if c != target[i] {
			return false
		}
	}
	return true
}
