package lexer

import (
	"math/rand"
	"testing"
)

// Corpus: 64 KiB random bytes. Aligns with the project benchmark pattern.
func benchInput(b *testing.B) []byte {
	b.Helper()
	rng := rand.New(rand.NewSource(1))
	const size = 64 * 1024
	data := make([]byte, size)
	rng.Read(data)
	return data
}

// --- Naive baselines for comparison ---

func naiveFindByteBench(src []byte, start, end uint32, target byte) uint32 {
	for i := start; i < end; i++ {
		if src[i] == target {
			return i
		}
	}
	return end
}

func naiveFindByteNotBench(src []byte, start, end uint32, target byte) uint32 {
	for i := start; i < end; i++ {
		if src[i] != target {
			return i
		}
	}
	return end
}

func naiveSkipWSBench(src []byte, pos, end uint32) uint32 {
	for pos < end && (src[pos] == ' ' || src[pos] == '\t') {
		pos++
	}
	return pos
}

// --- Benchmarks ---

func BenchmarkFindByte_Small(b *testing.B) {
	src := []byte("hello world")
	target := byte('w')
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = FindByte(src, 0, uint32(len(src)), target)
	}
}

func BenchmarkFindByte_64K(b *testing.B) {
	src := benchInput(b)
	target := byte('x')
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FindByte(src, 0, uint32(len(src)), target)
	}
}

func BenchmarkFindByte_64K_LastByte(b *testing.B) {
	// Target at the very end — forces walking the whole word stream.
	src := benchInput(b)
	target := byte('z')
	src[len(src)-1] = target
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FindByte(src, 0, uint32(len(src)), target)
	}
}

func BenchmarkNaiveFindByte_64K(b *testing.B) {
	src := benchInput(b)
	target := byte('x')
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = naiveFindByteBench(src, 0, uint32(len(src)), target)
	}
}

func BenchmarkFindByteNot_64K(b *testing.B) {
	// All bytes are 'x', so the first non-'x' is at the end.
	src := make([]byte, 64*1024)
	for i := range src {
		src[i] = 'x'
	}
	target := byte('x')
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FindByteNot(src, 0, uint32(len(src)), target)
	}
}

func BenchmarkNaiveFindByteNot_64K(b *testing.B) {
	src := make([]byte, 64*1024)
	for i := range src {
		src[i] = 'x'
	}
	target := byte('x')
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = naiveFindByteNotBench(src, 0, uint32(len(src)), target)
	}
}

func BenchmarkSkipWS_64K(b *testing.B) {
	// 64KB of whitespace — SkipWS hits the fast path on the whole buffer.
	src := make([]byte, 64*1024)
	for i := range src {
		src[i] = ' '
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SkipWS(src, 0, uint32(len(src)))
	}
}

func BenchmarkNaiveSkipWS_64K(b *testing.B) {
	src := make([]byte, 64*1024)
	for i := range src {
		src[i] = ' '
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = naiveSkipWSBench(src, 0, uint32(len(src)))
	}
}

func naiveFindAnyByte6Bench(src []byte, start, end uint32, sig [6]byte) uint32 {
	for i := start; i < end; i++ {
		b := src[i]
		if b == sig[0] || b == sig[1] || b == sig[2] ||
			b == sig[3] || b == sig[4] || b == sig[5] {
			return i
		}
	}
	return end
}

var inlineSigils = [6]byte{'\\', '`', '<', '[', '*', '_'}

func BenchmarkFindAnyByte6_64K(b *testing.B) {
	// Plain ASCII prose — sigils are absent, so the scan walks the whole buffer.
	src := make([]byte, 64*1024)
	for i := range src {
		src[i] = 'a' + byte(i%26)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FindAnyByte6(src, 0, uint32(len(src)), inlineSigils)
	}
}

func BenchmarkNaiveFindAnyByte6_64K(b *testing.B) {
	src := make([]byte, 64*1024)
	for i := range src {
		src[i] = 'a' + byte(i%26)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = naiveFindAnyByte6Bench(src, 0, uint32(len(src)), inlineSigils)
	}
}

func BenchmarkFindAnyByte6_64K_WithSigils(b *testing.B) {
	// Input with periodic sigils — exercises the early-exit path.
	src := make([]byte, 64*1024)
	for i := range src {
		src[i] = 'a'
	}
	for i := 0; i < len(src); i += 16 {
		src[i] = '*'
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FindAnyByte6(src, 0, uint32(len(src)), inlineSigils)
	}
}

func BenchmarkNaiveFindAnyByte6_64K_WithSigils(b *testing.B) {
	src := make([]byte, 64*1024)
	for i := range src {
		src[i] = 'a'
	}
	for i := 0; i < len(src); i += 16 {
		src[i] = '*'
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = naiveFindAnyByte6Bench(src, 0, uint32(len(src)), inlineSigils)
	}
}
