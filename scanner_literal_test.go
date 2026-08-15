package lexer

import "testing"

func TestScannerSQLStringEscapesRemainOneToken(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{name: "doubled quote", src: "'foo''bar'", want: "foo'bar"},
		{name: "backslash quote", src: "'foo\\'bar'", want: "foo'bar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			s := New(src)
			tok, ok := s.Next()
			if !ok || tok.Kind != KindString || tok.End != uint32(len(src)) {
				t.Fatalf("token=%#v ok=%v", tok, ok)
			}
			if _, ok := s.Next(); ok {
				t.Fatal("escaped string was split into multiple tokens")
			}
			var scratch [64]byte
			decoded, ok := DecodeStringLiteralInto(src, tok.Start, tok.End, scratch[:])
			if !ok || string(decoded) != tc.want {
				t.Fatalf("decoded=%q ok=%v want=%q", decoded, ok, tc.want)
			}
		})
	}
}

func TestScannerEscapeStringToken(t *testing.T) {
	src := []byte(`E'line\nnext\tvalue'`)
	s := New(src)
	tok, ok := s.Next()
	if !ok || tok.Kind != KindEscapeString || tok.Start != 1 || tok.End != uint32(len(src)) {
		t.Fatalf("token=%#v ok=%v", tok, ok)
	}
}

func TestScannerScientificNotation(t *testing.T) {
	s := New([]byte("1e5 1.5e-3 2E+4"))
	want := []string{"1e5", "1.5e-3", "2E+4"}
	for i, expected := range want {
		if i > 0 {
			sep, ok := s.Next()
			if !ok || sep.Kind != KindWhitespace {
				t.Fatalf("separator=%#v ok=%v", sep, ok)
			}
		}
		tok, ok := s.Next()
		if !ok || tok.Kind != KindNumber {
			t.Fatalf("token=%#v ok=%v", tok, ok)
		}
		if got := string(s.src[tok.Start:tok.End]); got != expected {
			t.Fatalf("number=%q want=%q", got, expected)
		}
	}
}

func TestScannerErrorIsSticky(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{name: "single pipe", src: "| SELECT"},
		{name: "bare at", src: "@ SELECT"},
		{name: "unrecognized char", src: "; SELECT"},
		{name: "double quote", src: `"foo`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New([]byte(tc.src))
			tok, ok := s.Next()
			if !ok || tok.Kind != KindError {
				t.Fatalf("token=%#v ok=%v", tok, ok)
			}
			if _, ok := s.Next(); ok {
				t.Fatal("scanner continued after KindError")
			}
		})
	}
}

func TestScannerUnterminatedBlockCommentIsError(t *testing.T) {
	s := New([]byte("/* unterminated"))
	tok, ok := s.Next()
	if !ok || tok.Kind != KindError {
		t.Fatalf("token=%#v ok=%v", tok, ok)
	}
	if _, ok := s.Next(); ok {
		t.Fatal("scanner continued after unterminated comment")
	}
}

func TestScannerTokenizationDoesNotAllocate(t *testing.T) {
	src := []byte("SELECT id, value FROM counters WHERE value >= 1.5e-3")
	var s Scanner
	allocs := testing.AllocsPerRun(1000, func() {
		s.src = src
		s.pos = 0
		s.failed = false
		for {
			_, ok := s.Next()
			if !ok {
				break
			}
		}
	})
	if allocs != 0 {
		t.Fatalf("scanner tokenization allocations=%v, want 0", allocs)
	}
}

func TestScannerScanIntoSoAUsesCallerCapacity(t *testing.T) {
	src := []byte("SELECT id FROM t")
	var starts [16]uint32
	var ends [16]uint32
	var kinds [16]Kind
	stream := TokenStream{Starts: starts[:0], Ends: ends[:0], Kinds: kinds[:0]}
	s := New(src)
	count, ok := s.ScanInto(&stream)
	if !ok || count != len(stream.Kinds) || count == 0 {
		t.Fatalf("count=%d ok=%v stream=%#v", count, ok, stream)
	}
	if got := string(src[stream.Starts[0]:stream.Ends[0]]); got != "SELECT" {
		t.Fatalf("first token=%q", got)
	}
}
