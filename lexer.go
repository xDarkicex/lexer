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
	KindSlash        // /
	KindPercent      // %
	KindConcat       // ||

	// Relational Keywords
	KindSelect
	KindFrom
	KindWhere
	KindAnd
	KindOr
	KindOrder
	KindBy
	KindAsc
	KindDesc
	KindLimit
	KindOffset
	KindJoin
	KindLeft  // LEFT JOIN
	KindRight // RIGHT JOIN
	KindInner // INNER JOIN
	KindOuter // OUTER (used with LEFT/RIGHT)
	KindFull  // FULL JOIN
	KindCross // CROSS JOIN
	KindOn
	KindAs
	KindOf
	KindTimestamp
	KindBetween
	KindIn
	KindNot
	KindBegin
	KindStart
	KindEpoch
	KindCommit
	KindRollback
	KindSavepoint
	KindRelease
	KindTo
	KindIs

	// Graph (PGQ) Keywords
	KindMatch
	KindGraphTable
	KindVertex
	KindEdge

	// Vector/ML Keywords
	KindVectorDistance
	KindSimilarity
	KindArrayCosineSimilarity
	KindGraphCentrality

	// CRUD Keywords
	KindInsert   // INSERT
	KindInto     // INTO
	KindValues   // VALUES
	KindConflict // CONFLICT
	KindDo       // DO
	KindNothing  // NOTHING
	KindExcluded // EXCLUDED
	KindUpdate   // UPDATE
	KindSet      // SET
	KindDelete   // DELETE

	// Aggregate Keywords
	KindCount // COUNT
	KindSum   // SUM
	KindAvg   // AVG
	KindMin   // MIN
	KindMax   // MAX

	// DDL Keywords
	KindCreate     // CREATE
	KindTable      // TABLE
	KindIndex      // INDEX
	KindDrop       // DROP
	KindAlter      // ALTER
	KindAdd        // ADD
	KindGroup      // GROUP
	KindHaving     // HAVING
	KindPrimary    // PRIMARY
	KindKey        // KEY
	KindNull       // NULL
	KindUnique     // UNIQUE
	KindDefault    // DEFAULT
	KindConstraint // CONSTRAINT
	KindForeign    // FOREIGN
	KindReferences // REFERENCES
	KindCheck      // CHECK
	KindCascade    // CASCADE
	KindRestrict   // RESTRICT
	KindNo         // NO
	KindAction     // ACTION
	KindIf         // IF
	KindExists     // EXISTS

	KindColon  // :
	KindDollar // $
	KindParam  // $identifier — named query parameter

	// Leiden Community Detection Keywords
	KindCompute // COMPUTE
	KindLeiden  // LEIDEN
	KindOptions // OPTIONS

	// CTE Keyword
	KindWith // WITH

	// Extension token kept at the end to preserve the numeric values of all
	// existing operators and keywords used by planner/executor contracts.
	KindEscapeString // PostgreSQL E'...' escape string
	KindCase
	KindWhen
	KindThen
	KindElse
	KindEnd
	KindNow
	KindNullif
	KindUnion
	KindAll
	KindPrepare
	KindExecute
	KindCast       // ::
	KindShiftLeft  // <<
	KindShiftRight // >>
	KindReset      // RESET
	KindLike       // LIKE
	KindILike      // ILIKE
	KindDistinct   // DISTINCT
	KindReturning  // RETURNING
	KindIntersect  // INTERSECT
	KindExcept     // EXCEPT
	// Comparison extensions are appended to preserve existing token values.
	KindGreaterEqual    // >=
	KindLessEqual       // <=
	KindNotEqual        // <> or !=
	KindVersions        // VERSIONS
	KindFTSMatch        // @@ (tsvector @@ tsquery)
	KindJSONExtract     // -> (JSON value extraction; graph paths use KindArrowRight directly)
	KindJSONExtractText // ->> (JSON text extraction)
	KindJSONContains    // @> (JSON containment)
	KindJSONContainedBy // <@ (JSON contained-by)
	KindJSONPath        // #> (JSON path extraction)
	KindJSONPathText    // #>> (JSON path text extraction)
	KindJSONExists      // ? (JSON object/array key existence)
	KindJSONAny         // ?| (any key from a text array exists)
	KindJSONAll         // ?& (all keys from a text array exist)
	KindJSONPathExists  // @? (JSONPath existence predicate)
	KindJSONDelete      // #- (JSON path delete)
	// Explain tokens are appended so all pre-existing token values remain
	// stable for optimizer/parser contracts.
	KindExplain  // EXPLAIN
	KindAnalyze  // ANALYZE
	KindOptional // OPTIONAL
	KindMerge    // MERGE
	// Graph/Cypher extensions are appended to preserve the numeric values of
	// every existing token used by the parser and physical-plan contracts.
	KindReturn       // RETURN
	KindShortestPath // shortestPath(...)
	KindPipe         // | (pattern-comprehension projection separator)
	KindDetach       // DETACH
	KindSkip         // SKIP
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

// Reset clears a caller-owned token stream without releasing its backing
// arrays. This keeps repeated scans allocation-free when the caller reserves
// capacity once.
func (ts *TokenStream) Reset() {
	if ts == nil {
		return
	}
	ts.Starts = ts.Starts[:0]
	ts.Ends = ts.Ends[:0]
	ts.Kinds = ts.Kinds[:0]
}

// Scanner holds lexer state.
type Scanner struct {
	src    []byte
	pos    uint32
	failed bool
}

// New returns a Scanner over src. The scanner keeps a slice header only;
// it does not retain src beyond the call.
func New(src []byte) *Scanner {
	return &Scanner{src: src}
}

// Reset reuses a Scanner for a new source buffer without allocating. It is
// equivalent to constructing a fresh Scanner with New, while allowing hot
// parsers to keep the scanner inline in their state.
func (s *Scanner) Reset(src []byte) {
	s.src = src
	s.pos = 0
	s.failed = false
}

// Next advances and returns the next token. Returns (Token{}, false) on EOF.
func (s *Scanner) Next() (Token, bool) {
	if s.failed {
		return Token{Kind: KindEOF}, false
	}
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
		closed := false
		for int(s.pos)+1 < len(s.src) {
			if s.src[s.pos] == '*' && s.src[s.pos+1] == '/' {
				s.pos += 2
				closed = true
				break
			}
			s.pos++
		}
		if !closed {
			s.failed = true
			return Token{Start: start, End: s.pos, Kind: KindError}, true
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
	case '/':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindSlash}, true
	case '%':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindPercent}, true
	case '|':
		s.pos++
		if int(s.pos) < len(s.src) && s.src[s.pos] == '|' {
			s.pos++
			return Token{Start: start, End: s.pos, Kind: KindConcat}, true
		}
		return Token{Start: start, End: s.pos, Kind: KindPipe}, true
	case '=':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindEquals}, true
	case '>':
		s.pos++
		if int(s.pos) < len(s.src) && s.src[s.pos] == '=' {
			s.pos++
			return Token{Start: start, End: s.pos, Kind: KindGreaterEqual}, true
		}
		if int(s.pos) < len(s.src) && s.src[s.pos] == '>' {
			s.pos++
			return Token{Start: start, End: s.pos, Kind: KindShiftRight}, true
		}
		return Token{Start: start, End: s.pos, Kind: KindGreaterThan}, true
	case '<':
		s.pos++
		if int(s.pos) < len(s.src) {
			switch s.src[s.pos] {
			case '<':
				s.pos++
				return Token{Start: start, End: s.pos, Kind: KindShiftLeft}, true
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
				if int(s.pos+1) < len(s.src) && s.src[s.pos+1] == '-' {
					s.pos += 2
					return Token{Start: start, End: s.pos, Kind: KindJSONDelete}, true
				}
				return Token{Start: start, End: s.pos - 1, Kind: KindLessThan}, true
			case '=':
				s.pos++
				if int(s.pos) < len(s.src) && s.src[s.pos] == '>' {
					s.pos++
					return Token{Start: start, End: s.pos, Kind: KindCosineDist}, true
				}
				return Token{Start: start, End: s.pos, Kind: KindLessEqual}, true
			case '>':
				s.pos++
				return Token{Start: start, End: s.pos, Kind: KindNotEqual}, true
			case '@':
				s.pos++
				return Token{Start: start, End: s.pos, Kind: KindJSONContainedBy}, true
			}
		}
		return Token{Start: start, End: s.pos, Kind: KindLessThan}, true
	case '!':
		s.pos++
		if int(s.pos) < len(s.src) && s.src[s.pos] == '=' {
			s.pos++
			return Token{Start: start, End: s.pos, Kind: KindNotEqual}, true
		}
		s.failed = true
		return Token{Start: start, End: s.pos, Kind: KindError}, true
	case '.':
		s.pos++
		return Token{Start: start, End: s.pos, Kind: KindDot}, true
	case '-':
		s.pos++
		if int(s.pos) < len(s.src) && s.src[s.pos] == '>' {
			s.pos++
			if int(s.pos) < len(s.src) && s.src[s.pos] == '>' {
				s.pos++
				return Token{Start: start, End: s.pos, Kind: KindJSONExtractText}, true
			}
			return Token{Start: start, End: s.pos, Kind: KindArrowRight}, true
		}
		return Token{Start: start, End: s.pos, Kind: KindDash}, true
	case '\'':
		return s.scanString(start, KindString)
	case 'e', 'E':
		// PostgreSQL escape strings use an adjacent E/e prefix. Keep the
		// prefix out of the token span and retain the distinct kind in the
		// AST so \n/\t decoding is explicit rather than changing ordinary
		// string semantics.
		if int(s.pos)+1 < len(s.src) && s.src[s.pos+1] == '\'' {
			s.pos++ // move to opening quote
			return s.scanString(start, KindEscapeString)
		}
	case '"':
		// Quoted identifiers bypass keyword classification. The token span
		// excludes the surrounding quotes so existing AST/catalog code can
		// consume the identifier bytes without a second representation.
		s.pos++
		innerStart := s.pos
		closed := false
		for int(s.pos) < len(s.src) {
			if s.src[s.pos] == '"' {
				if int(s.pos)+1 < len(s.src) && s.src[s.pos+1] == '"' {
					s.pos += 2
					continue
				}
				innerEnd := s.pos
				s.pos++
				closed = true
				if innerEnd == innerStart {
					s.failed = true
					return Token{Start: innerStart, End: innerEnd, Kind: KindError}, true
				}
				return Token{Start: innerStart, End: innerEnd, Kind: KindIdentifier}, true
			}
			s.pos++
		}
		if !closed {
			s.failed = true
			return Token{Start: start, End: s.pos, Kind: KindError}, true
		}

	case ':':
		s.pos++
		if int(s.pos) < len(s.src) && s.src[s.pos] == ':' {
			s.pos++
			return Token{Start: start, End: s.pos, Kind: KindCast}, true
		}
		return Token{Start: start, End: s.pos, Kind: KindColon}, true
	case '$':
		s.pos++
		// Parse identifier after $: $prompt_vec
		startID := s.pos
		for int(s.pos) < len(s.src) {
			b := s.src[s.pos]
			if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' {
				s.pos++
			} else {
				break
			}
		}
		if s.pos > startID {
			return Token{Start: start, End: s.pos, Kind: KindParam}, true
		}
		return Token{Start: start, End: s.pos, Kind: KindDollar}, true
	case '@':
		if int(s.pos+1) < len(s.src) && s.src[s.pos+1] == '@' {
			s.pos += 2
			return Token{Start: start, End: s.pos, Kind: KindFTSMatch}, true
		}
		if int(s.pos+1) < len(s.src) && s.src[s.pos+1] == '?' {
			s.pos += 2
			return Token{Start: start, End: s.pos, Kind: KindJSONPathExists}, true
		}
		if int(s.pos+1) < len(s.src) && s.src[s.pos+1] == '>' {
			s.pos += 2
			return Token{Start: start, End: s.pos, Kind: KindJSONContains}, true
		}
		s.pos++
		// Accept @name as an alias for the existing named-parameter syntax.
		// The parser deliberately uses the same KindParam node so downstream
		// binders do not need a second parameter representation.
		startID := s.pos
		for int(s.pos) < len(s.src) {
			b := s.src[s.pos]
			if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' {
				s.pos++
			} else {
				break
			}
		}
		if s.pos > startID {
			return Token{Start: start, End: s.pos, Kind: KindParam}, true
		}
		// A bare '@' with no identifier is a lexical error. Mark the scanner
		// failed so the stream terminates instead of yielding garbage tokens.
		s.failed = true
		return Token{Start: start, End: s.pos, Kind: KindError}, true
	case '#':
		s.pos++
		if int(s.pos) < len(s.src) && s.src[s.pos] == '>' {
			s.pos++
			if int(s.pos) < len(s.src) && s.src[s.pos] == '>' {
				s.pos++
				return Token{Start: start, End: s.pos, Kind: KindJSONPathText}, true
			}
			return Token{Start: start, End: s.pos, Kind: KindJSONPath}, true
		}
		if int(s.pos) < len(s.src) && s.src[s.pos] == '-' {
			s.pos++
			return Token{Start: start, End: s.pos, Kind: KindJSONDelete}, true
		}
		s.failed = true
		return Token{Start: start, End: s.pos, Kind: KindError}, true
	case '?':
		s.pos++
		if int(s.pos) < len(s.src) {
			if s.src[s.pos] == '|' {
				s.pos++
				return Token{Start: start, End: s.pos, Kind: KindJSONAny}, true
			}
			if s.src[s.pos] == '&' {
				s.pos++
				return Token{Start: start, End: s.pos, Kind: KindJSONAll}, true
			}
		}
		return Token{Start: start, End: s.pos, Kind: KindJSONExists}, true
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
		if int(s.pos) < len(s.src) && (s.src[s.pos] == 'e' || s.src[s.pos] == 'E') {
			exponentStart := s.pos
			s.pos++
			if int(s.pos) < len(s.src) && (s.src[s.pos] == '+' || s.src[s.pos] == '-') {
				s.pos++
			}
			digitsStart := s.pos
			for int(s.pos) < len(s.src) && s.src[s.pos] >= '0' && s.src[s.pos] <= '9' {
				s.pos++
			}
			if s.pos == digitsStart {
				s.pos = exponentStart
				s.failed = true
				return Token{Start: start, End: s.pos, Kind: KindError}, true
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

	// Fallback to prevent infinite loops on unrecognized characters. Mark the
	// scanner failed so an illegal byte is terminal rather than emitting a
	// stream of one-char error tokens for the rest of the input.
	if s.pos == start {
		s.pos++
		s.failed = true
		return Token{Start: start, End: s.pos, Kind: KindError}, true
	}

	// Fast Path Keyword Identification
	length := s.pos - start
	kind := KindIdentifier

	// We check exact byte length first to avoid unnecessary string conversions or slow checks
	if length >= 2 && length <= 23 {
		// Exact length switches
		switch length {
		case 2:
			if caseInsensitiveMatch(s.src[start:start+2], "as") {
				kind = KindAs
			} else if caseInsensitiveMatch(s.src[start:start+2], "by") {
				kind = KindBy
			} else if caseInsensitiveMatch(s.src[start:start+2], "to") {
				kind = KindTo
			} else if caseInsensitiveMatch(s.src[start:start+2], "in") {
				kind = KindIn
			} else if caseInsensitiveMatch(s.src[start:start+2], "on") {
				kind = KindOn
			} else if caseInsensitiveMatch(s.src[start:start+2], "or") {
				kind = KindOr
			} else if caseInsensitiveMatch(s.src[start:start+2], "if") {
				kind = KindIf
			} else if caseInsensitiveMatch(s.src[start:start+2], "of") {
				kind = KindOf
			} else if caseInsensitiveMatch(s.src[start:start+2], "no") {
				kind = KindNo
			} else if caseInsensitiveMatch(s.src[start:start+2], "is") {
				kind = KindIs
			} else if caseInsensitiveMatch(s.src[start:start+2], "do") {
				kind = KindDo
			}
		case 3:
			if caseInsensitiveMatch(s.src[start:start+3], "and") {
				kind = KindAnd
			} else if caseInsensitiveMatch(s.src[start:start+3], "end") {
				kind = KindEnd
			} else if caseInsensitiveMatch(s.src[start:start+3], "now") {
				kind = KindNow
			} else if caseInsensitiveMatch(s.src[start:start+3], "all") {
				kind = KindAll
			} else if caseInsensitiveMatch(s.src[start:start+3], "asc") {
				kind = KindAsc
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
			} else if caseInsensitiveMatch(s.src[start:start+4], "edge") {
				kind = KindEdge
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
			} else if caseInsensitiveMatch(s.src[start:start+4], "with") {
				kind = KindWith
			} else if caseInsensitiveMatch(s.src[start:start+4], "case") {
				kind = KindCase
			} else if caseInsensitiveMatch(s.src[start:start+4], "when") {
				kind = KindWhen
			} else if caseInsensitiveMatch(s.src[start:start+4], "then") {
				kind = KindThen
			} else if caseInsensitiveMatch(s.src[start:start+4], "else") {
				kind = KindElse
			} else if caseInsensitiveMatch(s.src[start:start+4], "like") {
				kind = KindLike
			} else if caseInsensitiveMatch(s.src[start:start+4], "skip") {
				kind = KindSkip
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
			} else if caseInsensitiveMatch(s.src[start:start+5], "ilike") {
				kind = KindILike
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
			} else if caseInsensitiveMatch(s.src[start:start+5], "union") {
				kind = KindUnion
			} else if caseInsensitiveMatch(s.src[start:start+5], "begin") {
				kind = KindBegin
			} else if caseInsensitiveMatch(s.src[start:start+5], "start") {
				kind = KindStart
			} else if caseInsensitiveMatch(s.src[start:start+5], "epoch") {
				kind = KindEpoch
			} else if caseInsensitiveMatch(s.src[start:start+5], "abort") {
				kind = KindRollback
			} else if caseInsensitiveMatch(s.src[start:start+5], "reset") {
				kind = KindReset
			} else if caseInsensitiveMatch(s.src[start:start+5], "merge") {
				kind = KindMerge
			}
		case 6:
			if caseInsensitiveMatch(s.src[start:start+6], "select") {
				kind = KindSelect
			} else if caseInsensitiveMatch(s.src[start:start+6], "offset") {
				kind = KindOffset
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
			} else if caseInsensitiveMatch(s.src[start:start+6], "commit") {
				kind = KindCommit
			} else if caseInsensitiveMatch(s.src[start:start+6], "leiden") {
				kind = KindLeiden
			} else if caseInsensitiveMatch(s.src[start:start+6], "action") {
				kind = KindAction
			} else if caseInsensitiveMatch(s.src[start:start+6], "nullif") {
				kind = KindNullif
			} else if caseInsensitiveMatch(s.src[start:start+6], "except") {
				kind = KindExcept
			} else if caseInsensitiveMatch(s.src[start:start+6], "return") {
				kind = KindReturn
			} else if caseInsensitiveMatch(s.src[start:start+6], "detach") {
				kind = KindDetach
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
			} else if caseInsensitiveMatch(s.src[start:start+7], "release") {
				kind = KindRelease
			} else if caseInsensitiveMatch(s.src[start:start+7], "compute") {
				kind = KindCompute
			} else if caseInsensitiveMatch(s.src[start:start+7], "options") {
				kind = KindOptions
			} else if caseInsensitiveMatch(s.src[start:start+7], "cascade") {
				kind = KindCascade
			} else if caseInsensitiveMatch(s.src[start:start+7], "nothing") {
				kind = KindNothing
			} else if caseInsensitiveMatch(s.src[start:start+7], "prepare") {
				kind = KindPrepare
			} else if caseInsensitiveMatch(s.src[start:start+7], "execute") {
				kind = KindExecute
			} else if caseInsensitiveMatch(s.src[start:start+7], "explain") {
				kind = KindExplain
			} else if caseInsensitiveMatch(s.src[start:start+7], "analyze") {
				kind = KindAnalyze
			}
		case 8:
			if caseInsensitiveMatch(s.src[start:start+8], "rollback") {
				kind = KindRollback
			} else if caseInsensitiveMatch(s.src[start:start+8], "versions") {
				kind = KindVersions
			} else if caseInsensitiveMatch(s.src[start:start+8], "distinct") {
				kind = KindDistinct
			} else if caseInsensitiveMatch(s.src[start:start+8], "restrict") {
				kind = KindRestrict
			} else if caseInsensitiveMatch(s.src[start:start+8], "conflict") {
				kind = KindConflict
			} else if caseInsensitiveMatch(s.src[start:start+8], "excluded") {
				kind = KindExcluded
			} else if caseInsensitiveMatch(s.src[start:start+8], "optional") {
				kind = KindOptional
			}
		case 9:
			if caseInsensitiveMatch(s.src[start:start+9], "timestamp") {
				kind = KindTimestamp
			} else if caseInsensitiveMatch(s.src[start:start+9], "savepoint") {
				kind = KindSavepoint
			} else if caseInsensitiveMatch(s.src[start:start+9], "returning") {
				kind = KindReturning
			} else if caseInsensitiveMatch(s.src[start:start+9], "intersect") {
				kind = KindIntersect
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
		case 12:
			if caseInsensitiveMatch(s.src[start:start+12], "shortestpath") {
				kind = KindShortestPath
			}
		case 15:
			if caseInsensitiveMatch(s.src[start:start+15], "vector_distance") {
				kind = KindVectorDistance
			}
		case 16:
			if caseInsensitiveMatch(s.src[start:start+16], "graph_centrality") {
				kind = KindGraphCentrality
			}
		case 23:
			if caseInsensitiveMatch(s.src[start:start+23], "array_cosine_similarity") {
				kind = KindArrayCosineSimilarity
			}
		}
	}

	return Token{Start: start, End: s.pos, Kind: kind}, true
}

func (s *Scanner) scanString(start uint32, kind Kind) (Token, bool) {
	quoteStart := s.pos
	s.pos++
	closed := false
	for int(s.pos) < len(s.src) {
		switch s.src[s.pos] {
		case '\\':
			if int(s.pos)+1 < len(s.src) {
				s.pos += 2
			} else {
				s.pos++
			}
		case '\'':
			if int(s.pos)+1 < len(s.src) && s.src[s.pos+1] == '\'' {
				s.pos += 2
				continue
			}
			s.pos++
			closed = true
		default:
			s.pos++
		}
		if closed {
			break
		}
	}
	if !closed {
		s.failed = true
		return Token{Start: start, End: s.pos, Kind: KindError}, true
	}
	return Token{Start: quoteStart, End: s.pos, Kind: kind}, true
}

// ScanInto materializes tokens into caller-owned SoA slices. It never grows
// those slices: callers must provide equal sufficient capacity. The boolean
// is false when capacity is insufficient or the scanner emits KindError.
// Streaming callers should continue using Next instead.
func (s *Scanner) ScanInto(dst *TokenStream) (int, bool) {
	if dst == nil {
		return 0, false
	}
	dst.Reset()
	count := 0
	for {
		tok, ok := s.Next()
		if !ok {
			return count, true
		}
		if tok.Kind == KindError || len(dst.Starts) == cap(dst.Starts) || len(dst.Ends) == cap(dst.Ends) || len(dst.Kinds) == cap(dst.Kinds) {
			return count, false
		}
		i := len(dst.Starts)
		dst.Starts = dst.Starts[:i+1]
		dst.Ends = dst.Ends[:i+1]
		dst.Kinds = dst.Kinds[:i+1]
		dst.Starts[i] = tok.Start
		dst.Ends[i] = tok.End
		dst.Kinds[i] = tok.Kind
		count++
	}
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
