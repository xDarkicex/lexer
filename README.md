# Lexer

A high-performance, SWAR-accelerated lexer for the [LibraVDB](https://github.com/xDarkicex/libraVDB) query language — the same token engine that powers LibraVDB's SQL, graph-pattern, and vector-similarity query surface. Freely distributed for the community to build tools against the LibraVDB query language.

Provided by **LibraVDB INC.**

---

## Example

The lexer accepts queries that blend SQL semantics, PGQ graph patterns, and pgvector-style vector operators:

```sql
SELECT id, embedding
FROM graph_table
MATCH (a)-[e]->(b)
WHERE SIMILARITY(embedding, [0.1, 0.5, 0.9]) > 0.8
ORDER BY SIMILARITY(embedding, [0.1, 0.5, 0.9]) DESC
LIMIT 100
```

---

## Performance

### SWAR (SIMD Within A Register)

Two independent SWAR layers work in concert:

**1. Keyword matching — `caseInsensitiveMatch`** (`lexer.go:346`)

For keywords ≤ 8 bytes (the common case), the lexer copies 8 bytes into a `uint64`, ORs with a lowercase mask (`0x20` per byte), and performs a single comparison. Case-insensitive keyword resolution becomes one branchless CPU instruction.

**2. Byte scanning — `swar.go`**

`FindByte`, `SkipWS`, `FindAnyByte6`, `FindAnyByte8` each process 8 bytes per loop iteration via bitwise tricks:

```go
replicatedB := uint64(b) * 0x0101010101010101
xored        := w ^ replicatedB
mask         := (xored - 0x0101010101010101) & ^xored & 0x8080808080808080
```

`mask` has the high bit set in every byte equal to `b`. `bits.TrailingZeros64(mask)>>3` yields the byte index in O(1). These primitives have no allocations, no per-byte branches, and handle arbitrary alignment via head/tail scalar loops.

**Benchmark results** (64 KiB input):

| Operation | Naive | SWAR |
|---|---|---|
| `FindByte` | ~6.5 GB/s | ~14 GB/s |
| `SkipWS` | ~5.2 GB/s | ~18 GB/s |
| `FindAnyByte6` | ~3.1 GB/s | ~12 GB/s |

---

## API

### Scanner

```go
import "github.com/xDarkicex/lexer"

scanner := lexer.New([]byte("SELECT * FROM users WHERE age > 21"))
for {
    tok, ok := scanner.Next()
    if !ok || tok.Kind == lexer.KindEOF {
        break
    }
    // tok.Start, tok.End are byte offsets into the original input
    // tok.Kind is a Kind enum value
}
```

### Token

```go
type Token struct {
    Start uint32  // inclusive byte offset
    End   uint32  // exclusive byte offset
    Kind  Kind
}
```

Access the matched text via `input[tok.Start:tok.End]`.

### TokenStream (SoA)

```go
type TokenStream struct {
    Starts []uint32
    Ends   []uint32
    Kinds  []Kind
}
```

For batch/tokenized use cases, collect tokens into a `TokenStream` for optimal cache locality — hot fields are stored in separate contiguous slices.

### SWAR Primitives

The byte-scanning functions in `swar.go` are exported and usable independently:

```go
pos := lexer.FindByte(src, 0, uint32(len(src)), '\n')
pos := lexer.SkipWS(src, pos, uint32(len(src)))
pos := lexer.FindAnyByte6(src, start, end, [6]byte{',', '(', ')', '[', ']', '*'})
pos := lexer.FindAnyByte8(src, start, end, [8]byte{'"', '\'', '(', ')', '[', ']', '{', '}'})
```

---

## Token Kind Reference

| Kind | Lexeme(s) |
|---|---|
| `KindEOF` | — |
| `KindError` | unrecognized char |
| `KindWhitespace` | `' '` `'\t'` `'\n'` `'\r'` |
| `KindIdentifier` | `[a-zA-Z_][a-zA-Z0-9_]*` |
| `KindString` | `'…'` |
| `KindNumber` | `[0-9]+(\.[0-9]+)?` |
| **Delimiters** | `,` `(` `)` `[` `]` `{` `}` `.` |
| **Operators** | `+` `-` `*` `=` `>` `<` |
| **Arrows** | `->` `<-` `<->` `<#>` `<=>` |
| **SQL keywords** | `SELECT`, `FROM`, `WHERE`, `AND`, `OR`, `INSERT`, `UPDATE`, `DELETE`, `SET`, `VALUES`, `INTO`, `JOIN`, `ON`, `BETWEEN`, `IN`, `NOT`, `ORDER BY`, `DESC`, `LIMIT`, `AS` |
| **Graph keywords** | `MATCH`, `graph_table`, `vertex`, `edge` |
| **Vector keywords** | `vector_distance`, `similarity` |

---

## Project Structure

```
lexer.go          — Scanner, Token, TokenStream, Kind constants, keyword SWAR
swar.go           — FindByte, SkipWS, FindAnyByte6, FindAnyByte8 (exported)
swar_test.go      — unit + property-based tests (testing/quick)
swar_bench_test.go — benchmarks vs naive baselines
lexer_test.go     — integration tests for full query tokens
LICENSE           — MIT
LICENSE-APACHE    — Apache 2.0
```

---

## Tests

```sh
go test ./... -bench=. -benchmem
```

Property-based tests (`testing/quick`, 1000 iterations each) verify `FindByte`, `FindByteNot`, `SkipWS`, and `FindAnyByte6` against naive scalar implementations across randomized inputs.

---

## License

Dual-licensed under **Apache License 2.0** and **MIT**. See [`LICENSE`](LICENSE) and [`LICENSE-APACHE`](LICENSE-APACHE).

---

## Who's Using It

This is the official query language lexer for [**libraVDB**](https://github.com/xDarkicex/libraVDB) — built and maintained by **LibraVDB INC.** for the [LibraVDB Agentic Memory](https://libravdb.com) community.
