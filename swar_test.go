package lexer

import (
	"math/rand"
	"testing"
	"testing/quick"
)

// --- Naive baselines for cross-checking ---

func naiveFindByte(src []byte, start, end uint32, b byte) uint32 {
	for i := start; i < end; i++ {
		if src[i] == b {
			return i
		}
	}
	return end
}

func naiveFindByteNot(src []byte, start, end uint32, b byte) uint32 {
	for i := start; i < end; i++ {
		if src[i] != b {
			return i
		}
	}
	return end
}

func naiveSkipWS(src []byte, pos, end uint32) uint32 {
	for pos < end && (src[pos] == ' ' || src[pos] == '\t') {
		pos++
	}
	return pos
}

// --- Unit tests ---

func TestFindByte_Empty(t *testing.T) {
	if got := FindByte(nil, 0, 0, 'a'); got != 0 {
		t.Errorf("empty: got %d, want 0", got)
	}
}

func TestFindByte_StartGreaterThanEnd(t *testing.T) {
	src := []byte("hello")
	if got := FindByte(src, 5, 5, 'l'); got != 5 {
		t.Errorf("start==end: got %d, want 5", got)
	}
	if got := FindByte(src, 5, 3, 'l'); got != 3 {
		t.Errorf("start>end: got %d, want 3", got)
	}
}

func TestFindByte_NoMatch(t *testing.T) {
	src := []byte("hello world")
	if got := FindByte(src, 0, uint32(len(src)), 'z'); got != uint32(len(src)) {
		t.Errorf("no match: got %d, want %d", got, len(src))
	}
}

func TestFindByte_FirstByte(t *testing.T) {
	src := []byte("hello")
	if got := FindByte(src, 0, uint32(len(src)), 'h'); got != 0 {
		t.Errorf("first byte: got %d, want 0", got)
	}
}

func TestFindByte_LastByte(t *testing.T) {
	src := []byte("hello")
	if got := FindByte(src, 0, uint32(len(src)), 'o'); got != 4 {
		t.Errorf("last byte: got %d, want 4", got)
	}
}

func TestFindByte_AllSameByte(t *testing.T) {
	src := []byte("aaaaaaaa")
	if got := FindByte(src, 0, 8, 'a'); got != 0 {
		t.Errorf("all same: got %d, want 0", got)
	}
}

func TestFindByte_AtAllOffsets(t *testing.T) {
	// Exercise every alignment offset 0..7.
	for target := 0; target < 16; target++ {
		src := make([]byte, 16)
		for i := range src {
			src[i] = 'x'
		}
		src[target] = 'y'
		got := FindByte(src, 0, uint32(len(src)), 'y')
		if got != uint32(target) {
			t.Errorf("target at %d: got %d", target, got)
		}
	}
}

func TestFindByte_CRLF(t *testing.T) {
	src := []byte("a\r\nb\r\n")
	// Find '\n' should return 2.
	got := FindByte(src, 0, uint32(len(src)), '\n')
	if got != 2 {
		t.Errorf("first \\n: got %d, want 2", got)
	}
	got = FindByte(src, 3, uint32(len(src)), '\n')
	if got != 5 {
		t.Errorf("second \\n: got %d, want 5", got)
	}
}

func TestFindByte_AlignedWord(t *testing.T) {
	// 8-byte input, target at position 5. Tests middle-word match.
	src := []byte("aaaaaXaaa")
	if got := FindByte(src, 0, 8, 'X'); got != 5 {
		t.Errorf("middle word: got %d, want 5", got)
	}
}

func TestFindByte_LongRun(t *testing.T) {
	// 64 bytes (8 words) with target at position 33.
	src := make([]byte, 64)
	for i := range src {
		src[i] = '.'
	}
	src[33] = 'X'
	if got := FindByte(src, 0, 64, 'X'); got != 33 {
		t.Errorf("long run: got %d, want 33", got)
	}
}

func TestFindByteNot_NoMatch(t *testing.T) {
	// All 'a' (target), so all bytes match `!= 'a'` is false.
	src := []byte("aaaa")
	if got := FindByteNot(src, 0, 4, 'a'); got != 4 {
		t.Errorf("no match: got %d, want 4", got)
	}
}

func TestFindByteNot_FirstByte(t *testing.T) {
	src := []byte("xyz")
	if got := FindByteNot(src, 0, 3, 'a'); got != 0 {
		t.Errorf("first byte: got %d, want 0", got)
	}
}

func TestSkipWS_Empty(t *testing.T) {
	if got := SkipWS(nil, 0, 0); got != 0 {
		t.Errorf("empty: got %d, want 0", got)
	}
}

func TestSkipWS_AllWS(t *testing.T) {
	src := []byte("   \t  \t  ")
	if got := SkipWS(src, 0, uint32(len(src))); got != uint32(len(src)) {
		t.Errorf("all WS: got %d, want %d", got, len(src))
	}
}

func TestSkipWS_LeadingNonWS(t *testing.T) {
	src := []byte("hello world")
	if got := SkipWS(src, 0, uint32(len(src))); got != 0 {
		t.Errorf("leading non-WS: got %d, want 0", got)
	}
}

func TestSkipWS_AtAllOffsets(t *testing.T) {
	for nonWS := 0; nonWS < 16; nonWS++ {
		src := make([]byte, 16)
		for i := range src {
			src[i] = ' '
		}
		src[nonWS] = 'x'
		got := SkipWS(src, 0, uint32(len(src)))
		if got != uint32(nonWS) {
			t.Errorf("non-WS at %d: got %d", nonWS, got)
		}
	}
}

func TestSkipWS_MixedWhitespace(t *testing.T) {
	src := []byte("  \t\t  x")
	if got := SkipWS(src, 0, uint32(len(src))); got != 6 {
		t.Errorf("mixed WS: got %d, want 6", got)
	}
}

// --- Property tests ---

func TestPropertyFindByte(t *testing.T) {
	f := func(data []byte, start16, len16 uint16, b8 uint8) bool {
		if len(data) == 0 {
			return true
		}
		src := data
		start := uint32(start16) % uint32(len(src))
		end := start + uint32(len16)%uint32(len(src)-int(start))+1
		if end > uint32(len(src)) {
			end = uint32(len(src))
		}
		got := FindByte(src, start, end, b8)
		want := naiveFindByte(src, start, end, b8)
		if got != want {
			t.Logf("FindByte(src=%v, start=%d, end=%d, b=%q): got %d, want %d",
				src, start, end, b8, got, want)
			return false
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}

func TestPropertyFindByteNot(t *testing.T) {
	f := func(data []byte, start16, len16 uint16, b8 uint8) bool {
		if len(data) == 0 {
			return true
		}
		src := data
		start := uint32(start16) % uint32(len(src))
		end := start + uint32(len16)%uint32(len(src)-int(start))+1
		if end > uint32(len(src)) {
			end = uint32(len(src))
		}
		got := FindByteNot(src, start, end, b8)
		want := naiveFindByteNot(src, start, end, b8)
		if got != want {
			t.Logf("FindByteNot(src=%v, start=%d, end=%d, b=%q): got %d, want %d",
				src, start, end, b8, got, want)
			return false
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}

func TestPropertySkipWS(t *testing.T) {
	// Use a restricted alphabet to make WS more likely.
	alphabet := []byte{' ', '\t', 'x', 'y', 'z', 'a', '\n', 'b'}
	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))
		n := int(rng.Intn(64)) + 1
		data := make([]byte, n)
		for i := range data {
			data[i] = alphabet[rng.Intn(len(alphabet))]
		}
		// Pick a random start in [0, len(data)].
		start := uint32(rng.Intn(n + 1))
		got := SkipWS(data, start, uint32(n))
		want := naiveSkipWS(data, start, uint32(n))
		if got != want {
			t.Logf("SkipWS(data=%v, start=%d, end=%d): got %d, want %d",
				data, start, n, got, want)
			return false
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}

// --- FindAnyByte6 helpers ---

func naiveFindAnyByte6(src []byte, start, end uint32, sig [6]byte) uint32 {
	for i := start; i < end; i++ {
		b := src[i]
		if b == sig[0] || b == sig[1] || b == sig[2] ||
			b == sig[3] || b == sig[4] || b == sig[5] {
			return i
		}
	}
	return end
}

func TestFindAnyByte6_NoMatch(t *testing.T) {
	src := []byte("hello world")
	sig := [6]byte{'!', '?', '@', '#', '$', '%'}
	if got := FindAnyByte6(src, 0, uint32(len(src)), sig); got != uint32(len(src)) {
		t.Errorf("got %d, want %d", got, len(src))
	}
}

func TestFindAnyByte6_FirstByte(t *testing.T) {
	src := []byte("hello world")
	sig := [6]byte{'h', '!', '?', '@', '#', '$'}
	if got := FindAnyByte6(src, 0, uint32(len(src)), sig); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestFindAnyByte6_LastByte(t *testing.T) {
	src := []byte("hello world")
	sig := [6]byte{'!', '?', '@', '#', '$', 'd'}
	if got := FindAnyByte6(src, 0, uint32(len(src)), sig); got != 10 {
		t.Errorf("got %d, want 10", got)
	}
}

func TestFindAnyByte6_AtAllOffsets(t *testing.T) {
	for target := 0; target < 16; target++ {
		src := make([]byte, 16)
		for i := range src {
			src[i] = '.'
		}
		src[target] = '*'
		sig := [6]byte{'*', '!', '?', '@', '#', '$'}
		got := FindAnyByte6(src, 0, uint32(len(src)), sig)
		if got != uint32(target) {
			t.Errorf("target at %d: got %d", target, got)
		}
	}
}

func TestFindAnyByte6_StartGreaterThanEnd(t *testing.T) {
	src := []byte("hello")
	sig := [6]byte{'h', '!', '?', '@', '#', '$'}
	if got := FindAnyByte6(src, 3, 3, sig); got != 3 {
		t.Errorf("start==end: got %d, want 3", got)
	}
	if got := FindAnyByte6(src, 5, 3, sig); got != 3 {
		t.Errorf("start>end: got %d, want 3", got)
	}
}

func TestFindAnyByte6_DuplicateSigils(t *testing.T) {
	src := []byte("hello")
	sig := [6]byte{'l', 'h', 'l', 'l', 'l', 'l'}
	if got := FindAnyByte6(src, 0, uint32(len(src)), sig); got != 0 {
		t.Errorf("first byte: got %d, want 0", got)
	}
}

func TestFindAnyByte6_WordMatch(t *testing.T) {
	// 8-byte input, target at position 5. Tests middle-word match.
	src := []byte("aaaaaXaaa")
	sig := [6]byte{'X', '!', '?', '@', '#', '$'}
	if got := FindAnyByte6(src, 0, 8, sig); got != 5 {
		t.Errorf("middle word: got %d, want 5", got)
	}
}

func TestFindAnyByte6_LongRun(t *testing.T) {
	// 64 bytes (8 words) with target at position 33.
	src := make([]byte, 64)
	for i := range src {
		src[i] = '.'
	}
	src[33] = '*'
	sig := [6]byte{'*', '!', '?', '@', '#', '$'}
	if got := FindAnyByte6(src, 0, 64, sig); got != 33 {
		t.Errorf("long run: got %d, want 33", got)
	}
}

func TestPropertyFindAnyByte6(t *testing.T) {
	f := func(data []byte, start16, len16 uint16, s0, s1, s2, s3, s4, s5 uint8) bool {
		if len(data) == 0 {
			return true
		}
		src := data
		start := uint32(start16) % uint32(len(src))
		end := start + uint32(len16)%uint32(len(src)-int(start))+1
		if end > uint32(len(src)) {
			end = uint32(len(src))
		}
		sig := [6]byte{s0, s1, s2, s3, s4, s5}
		got := FindAnyByte6(src, start, end, sig)
		want := naiveFindAnyByte6(src, start, end, sig)
		if got != want {
			t.Logf("FindAnyByte6(src=%v, start=%d, end=%d, sig=%v): got %d, want %d",
				src, start, end, sig, got, want)
			return false
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Error(err)
	}
}
