package lexer

import (
	"math/bits"
	"unsafe"
)

// SWAR (SIMD Within A Register) byte classifiers on uint64. Each helper
// processes 8 bytes per iteration through bitwise ops, with a single
// branch per 8-byte chunk. No allocations, no per-byte branches inside
// the loop. Heap-allocated `[]byte` is only 2-byte aligned, so each
// helper handles a 0–7 byte scalar head, an aligned 8-byte word middle,
// and a 0–7 byte scalar tail.
//
// The trick: for a uint64 word `w` holding 8 bytes, find positions where
// `w[i] == b`:
//
//	replicatedB := uint64(b) * 0x0101010101010101
//	xored := w ^ replicatedB
//	mask := (xored - 0x0101010101010101) & ^xored & 0x8080808080808080
//
// `mask` has the high bit set in every byte equal to `b`. When the
// mask is zero, all 8 bytes differed; otherwise `bits.TrailingZeros64`
// yields the byte index.
//
// The `!= b` variant XORs with `b^0xFF` so the same trick finds bytes
// that differ from `b`. SkipWS runs two parallel masks (space, tab)
// and ORs them.

const (
	wordBytes      = 8
	wordMask       = 0x8080808080808080
	wordReplicator = 0x0101010101010101
)

// FindByte returns the smallest i in [start,end) with src[i]==b,
// or end if none. end<=start returns end.
func FindByte(src []byte, start, end uint32, b byte) uint32 {
	if start >= end {
		return end
	}
	// Scalar head: walk until we hit an 8-byte boundary. This also
	// catches a match in the first 0–7 bytes regardless of alignment.
	for start < end && uintptr(unsafe.Pointer(&src[start]))%wordBytes != 0 {
		if src[start] == b {
			return start
		}
		start++
	}
	// Aligned 8-byte middle.
	chunk := uint64(b) * wordReplicator
	for end-start >= wordBytes {
		w := *(*uint64)(unsafe.Pointer(&src[start]))
		xored := w ^ chunk
		mask := (xored - wordReplicator) &^ xored & wordMask
		if mask != 0 {
			return start + uint32(bits.TrailingZeros64(mask)>>3)
		}
		start += wordBytes
	}
	// Scalar tail.
	for start < end {
		if src[start] == b {
			return start
		}
		start++
	}
	return end
}

// FindByteNot returns the smallest i in [start,end) with src[i]!=b,
// or end if none. end<=start returns end.
func FindByteNot(src []byte, start, end uint32, b byte) uint32 {
	if start >= end {
		return end
	}
	chunk := uint64(b) * wordReplicator
	for start < end && uintptr(unsafe.Pointer(&src[start]))%wordBytes != 0 {
		if src[start] != b {
			return start
		}
		start++
	}
	for end-start >= wordBytes {
		w := *(*uint64)(unsafe.Pointer(&src[start]))
		xored := w ^ chunk
		// eqMask: high bit set per byte where byte == b.
		eqMask := (xored - wordReplicator) &^ xored & wordMask
		// If any byte is not b, ^eqMask & wordMask has high bit set there.
		if eqMask != wordMask {
			nonMatch := ^eqMask & wordMask
			return start + uint32(bits.TrailingZeros64(nonMatch)>>3)
		}
		start += wordBytes
	}
	for start < end {
		if src[start] != b {
			return start
		}
		start++
	}
	return end
}

// SkipWS advances pos past leading ' ' and '\t' up to end. Returns the
// first non-WS byte position (or end if all WS).
func SkipWS(src []byte, pos, end uint32) uint32 {
	if pos >= end {
		return end
	}
	for pos < end && uintptr(unsafe.Pointer(&src[pos]))%wordBytes != 0 {
		if !isWSByte(src[pos]) {
			return pos
		}
		pos++
	}
	spaceChunk := uint64(' ') * wordReplicator
	tabChunk := uint64('\t') * wordReplicator
	for end-pos >= wordBytes {
		w := *(*uint64)(unsafe.Pointer(&src[pos]))
		// spaceMask: high bit set per byte where byte == ' '.
		x := w ^ spaceChunk
		spaceMask := (x - wordReplicator) &^ x & wordMask
		// tabMask: high bit set per byte where byte == '\t'.
		y := w ^ tabChunk
		tabMask := (y - wordReplicator) &^ y & wordMask
		// wsMask: high bit set for any WS byte (' ' or '\t').
		wsMask := spaceMask | tabMask
		// nonWSMask: high bit set for non-WS bytes (= where we stop).
		// When nonWSMask == 0, the whole word is whitespace.
		nonWS := ^wsMask & wordMask
		if nonWS != 0 {
			return pos + uint32(bits.TrailingZeros64(nonWS)>>3)
		}
		pos += wordBytes
	}
	for pos < end {
		if !isWSByte(src[pos]) {
			return pos
		}
		pos++
	}
	return end
}

// isWSByte is the scalar predicate for one byte. Used by the head/tail
// loops and as a fallback. Sentinel — kept private since callers should
// use SkipWS for runs.
func isWSByte(b byte) bool {
	return b == ' ' || b == '\t'
}

// FindAnyByte6 returns the smallest i in [start,end) with src[i] in sigils,
// or end if none. end<=start returns end. 8 bytes per cycle via SWAR.
// Six sigils are handled by computing six byte-masks per 8-byte word and
// OR-ing them; the first set bit gives the position. The fixed-size
// signature keeps the sigil array on the caller's stack — no heap
// allocation.
func FindAnyByte6(src []byte, start, end uint32, sig [6]byte) uint32 {
	if start >= end {
		return end
	}
	// Scalar head to the next 8-byte boundary.
	for start < end && uintptr(unsafe.Pointer(&src[start]))%wordBytes != 0 {
		b := src[start]
		if b == sig[0] || b == sig[1] || b == sig[2] ||
			b == sig[3] || b == sig[4] || b == sig[5] {
			return start
		}
		start++
	}
	c0 := uint64(sig[0]) * wordReplicator
	c1 := uint64(sig[1]) * wordReplicator
	c2 := uint64(sig[2]) * wordReplicator
	c3 := uint64(sig[3]) * wordReplicator
	c4 := uint64(sig[4]) * wordReplicator
	c5 := uint64(sig[5]) * wordReplicator
	for end-start >= wordBytes {
		w := *(*uint64)(unsafe.Pointer(&src[start]))
		m := byteMask(w, c0) | byteMask(w, c1) | byteMask(w, c2) |
			byteMask(w, c3) | byteMask(w, c4) | byteMask(w, c5)
		if m != 0 {
			return start + uint32(bits.TrailingZeros64(m)>>3)
		}
		start += wordBytes
	}
	for start < end {
		b := src[start]
		if b == sig[0] || b == sig[1] || b == sig[2] ||
			b == sig[3] || b == sig[4] || b == sig[5] {
			return start
		}
		start++
	}
	return end
}

// byteMask returns a uint64 whose high bit is set in each byte where w
// equals chunk. Standard SWAR trick: `(x - rep) &^ x & 0x80...`.
func byteMask(w, chunk uint64) uint64 {
	x := w ^ chunk
	return (x - wordReplicator) &^ x & wordMask
}

// FindAnyByte8 returns the smallest i in [start,end) with src[i] in sigils,
// or end if none. 8 bytes per cycle via SWAR. Same algorithm as
// FindAnyByte6 but with two extra sigils (the inline parser uses 8).
func FindAnyByte8(src []byte, start, end uint32, sig [8]byte) uint32 {
	if start >= end {
		return end
	}
	for start < end && uintptr(unsafe.Pointer(&src[start]))%wordBytes != 0 {
		b := src[start]
		if b == sig[0] || b == sig[1] || b == sig[2] || b == sig[3] ||
			b == sig[4] || b == sig[5] || b == sig[6] || b == sig[7] {
			return start
		}
		start++
	}
	c := [8]uint64{
		uint64(sig[0]) * wordReplicator,
		uint64(sig[1]) * wordReplicator,
		uint64(sig[2]) * wordReplicator,
		uint64(sig[3]) * wordReplicator,
		uint64(sig[4]) * wordReplicator,
		uint64(sig[5]) * wordReplicator,
		uint64(sig[6]) * wordReplicator,
		uint64(sig[7]) * wordReplicator,
	}
	for end-start >= wordBytes {
		w := *(*uint64)(unsafe.Pointer(&src[start]))
		m := byteMask(w, c[0]) | byteMask(w, c[1]) | byteMask(w, c[2]) | byteMask(w, c[3]) |
			byteMask(w, c[4]) | byteMask(w, c[5]) | byteMask(w, c[6]) | byteMask(w, c[7])
		if m != 0 {
			return start + uint32(bits.TrailingZeros64(m)>>3)
		}
		start += wordBytes
	}
	for start < end {
		b := src[start]
		if b == sig[0] || b == sig[1] || b == sig[2] || b == sig[3] ||
			b == sig[4] || b == sig[5] || b == sig[6] || b == sig[7] {
			return start
		}
		start++
	}
	return end
}
