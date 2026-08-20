package lexer

import (
	"testing"
)

func TestScanner(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []Kind
	}{
		{
			name:  "Basic SQL Select",
			input: "SELECT id, name FROM users WHERE id = 123",
			expected: []Kind{
				KindSelect, KindWhitespace, KindIdentifier, KindComma, KindWhitespace,
				KindIdentifier, KindWhitespace, KindFrom, KindWhitespace, KindIdentifier,
				KindWhitespace, KindWhere, KindWhitespace, KindIdentifier, KindWhitespace,
				KindEquals, KindWhitespace, KindNumber, KindEOF,
			},
		},
		{
			name:  "PGQ Match",
			input: "MATCH (a)-[e]->(b)",
			expected: []Kind{
				KindMatch, KindWhitespace, KindLeftParen, KindIdentifier, KindRightParen,
				KindDash, KindLeftBracket, KindIdentifier, KindRightBracket, KindArrowRight,
				KindLeftParen, KindIdentifier, KindRightParen, KindEOF,
			},
		},
		{
			name:  "Vector Similarity",
			input: "ORDER BY SIMILARITY(v, [1.0, 0.5]) DESC LIMIT 10",
			expected: []Kind{
				KindOrder, KindWhitespace, KindBy, KindWhitespace, KindSimilarity,
				KindLeftParen, KindIdentifier, KindComma, KindWhitespace,
				KindLeftBracket, KindNumber, KindComma, KindWhitespace, KindNumber, KindRightBracket,
				KindRightParen, KindWhitespace, KindDesc, KindWhitespace, KindLimit, KindWhitespace, KindNumber, KindEOF,
			},
		},
		{
			name:  "Array cosine similarity",
			input: "array_cosine_similarity(v, q)",
			expected: []Kind{
				KindArrayCosineSimilarity, KindLeftParen, KindIdentifier, KindComma,
				KindWhitespace, KindIdentifier, KindRightParen, KindEOF,
			},
		},
		{
			name:  "Explicit ascending order",
			input: "ORDER BY score ASC LIMIT 5",
			expected: []Kind{
				KindOrder, KindWhitespace, KindBy, KindWhitespace, KindIdentifier,
				KindWhitespace, KindAsc, KindWhitespace, KindLimit, KindWhitespace, KindNumber, KindEOF,
			},
		},
		{
			name:  "Mixed Case Keywords",
			input: "sEleCt FrOm gRaPh_TaBlE",
			expected: []Kind{
				KindSelect, KindWhitespace, KindFrom, KindWhitespace, KindGraphTable, KindEOF,
			},
		},
		{
			name:  "Strings and Numbers",
			input: "WHERE name = 'John Doe' AND age > 18.5",
			expected: []Kind{
				KindWhere, KindWhitespace, KindIdentifier, KindWhitespace, KindEquals, KindWhitespace,
				KindString, KindWhitespace, KindAnd, KindWhitespace, KindIdentifier, KindWhitespace,
				KindGreaterThan, KindWhitespace, KindNumber, KindEOF,
			},
		},
		{
			name:  "Path Quantifiers",
			input: "MATCH (a)-[e]->{1,3}(b) MATCH (x)-[y]->+(z) MATCH (m)-[n]->*(o)",
			expected: []Kind{
				KindMatch, KindWhitespace,
				KindLeftParen, KindIdentifier, KindRightParen,
				KindDash, KindLeftBracket, KindIdentifier, KindRightBracket, KindArrowRight,
				KindLeftBrace, KindNumber, KindComma, KindNumber, KindRightBrace,
				KindLeftParen, KindIdentifier, KindRightParen, KindWhitespace,
				KindMatch, KindWhitespace,
				KindLeftParen, KindIdentifier, KindRightParen,
				KindDash, KindLeftBracket, KindIdentifier, KindRightBracket, KindArrowRight,
				KindPlus,
				KindLeftParen, KindIdentifier, KindRightParen, KindWhitespace,
				KindMatch, KindWhitespace,
				KindLeftParen, KindIdentifier, KindRightParen,
				KindDash, KindLeftBracket, KindIdentifier, KindRightBracket, KindArrowRight,
				KindAsterisk,
				KindLeftParen, KindIdentifier, KindRightParen, KindEOF,
			},
		},
		{
			name:  "pgvector Operators",
			input: "ORDER BY v <-> q <#> r <=> s",
			expected: []Kind{
				KindOrder, KindWhitespace, KindBy, KindWhitespace,
				KindIdentifier, KindWhitespace, KindL2Dist, KindWhitespace,
				KindIdentifier, KindWhitespace, KindIPDist, KindWhitespace,
				KindIdentifier, KindWhitespace, KindCosineDist, KindWhitespace,
				KindIdentifier, KindEOF,
			},
		},
		{
			name:  "CRUD Keywords",
			input: "INSERT INTO users VALUES UPDATE SET DELETE FROM",
			expected: []Kind{
				KindInsert, KindWhitespace, KindInto, KindWhitespace,
				KindIdentifier, KindWhitespace, KindValues, KindWhitespace,
				KindUpdate, KindWhitespace, KindSet, KindWhitespace,
				KindDelete, KindWhitespace, KindFrom, KindEOF,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New([]byte(tt.input))
			var got []Kind
			for {
				tok, ok := s.Next()
				got = append(got, tok.Kind)
				if !ok || tok.Kind == KindEOF {
					break
				}
			}

			if len(got) != len(tt.expected) {
				t.Fatalf("length mismatch: got %d, expected %d\ngot: %v\nexp: %v", len(got), len(tt.expected), got, tt.expected)
			}
			for i, k := range got {
				if k != tt.expected[i] {
					t.Errorf("token %d mismatch: got %v, expected %v", i, k, tt.expected[i])
				}
			}
		})
	}
}

func BenchmarkScanner_SQL(b *testing.B) {
	input := []byte("SELECT a, b, c FROM graph_table MATCH (x)-[y]->(z) WHERE SIMILARITY(x.vec, y.vec) > 0.8 ORDER BY id LIMIT 100")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := New(input)
		for {
			tok, ok := s.Next()
			if !ok || tok.Kind == KindEOF {
				break
			}
		}
	}
}
