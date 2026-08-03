# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.1] — 2025-08-03

### Added

- **Aggregate tokens**: `KindCount`, `KindSum`, `KindAvg`, `KindMin`, `KindMax` with SWAR keyword matching.
- **DDL tokens**: `KindCreate`, `KindTable`, `KindIndex`, `KindDrop`, `KindAlter`, `KindAdd`, `KindGroup`, `KindHaving` with SWAR matching.
- **JOIN type tokens**: `KindLeft`, `KindRight`, `KindInner`, `KindOuter`, `KindFull`, `KindCross` with SWAR matching.
- **Constraint tokens**: `KindPrimary`, `KindKey`, `KindNull`, `KindUnique`, `KindDefault`, `KindConstraint`, `KindForeign`, `KindReferences`, `KindCheck` with SWAR matching.
- **Control flow tokens**: `KindIf`, `KindExists` with SWAR matching.
- **Parser/AST**:
  - `AggregateExpr` — `COUNT(*)`, `COUNT(col)`, `SUM(col)`, `AVG(col)`, `MIN(col)`, `MAX(col)` with `DISTINCT` modifier.
  - `SubqueryExpr` — parenthesized `SELECT` subqueries in expression context.
  - `CreateTableStmt`, `DropTableStmt`, `CreateIndexStmt`, `DropIndexStmt`, `AlterTableStmt` — full DDL parsing with `IF EXISTS`/`IF NOT EXISTS` support.
  - `ColumnDef.Flags` bitmask: `ColFlagNotNull`, `ColFlagPrimaryKey`, `ColFlagUnique`.
  - `parseColumnConstraints()` — `NOT NULL`, `PRIMARY KEY`, `UNIQUE`, `DEFAULT`, `CHECK`, `REFERENCES`, `CONSTRAINT name`.
  - `SelectStmt` extended: `GroupBy` (column references), `HavingExpr` (binary expression), `ORDER BY`, `LIMIT`.
  - `JoinClause.Type` enum: `JoinInner`, `JoinLeft`, `JoinRight`, `JoinFull`, `JoinCross` with `[LEFT|RIGHT|FULL] [OUTER] JOIN` parsing.
  - `Projection.Alias` — `SELECT expr AS alias` support.
- 29 new lexer tokens total, 692 insertions.

## [0.1.0] — 2025-08-02

### Added

- Initial release.
- SWAR-accelerated lexer (`lexer.go`) with full token kind set for LibraVDB's hybrid SQL, graph-pattern (PGQ), and vector-similarity (pgvector) query language.
- `caseInsensitiveMatch` — single-`uint64` branchless keyword resolution for keywords ≤ 8 bytes.
- Byte-scanning primitives (`swar.go`): `FindByte`, `FindByteNot`, `SkipWS`, `FindAnyByte6`, `FindAnyByte8` — all SWAR-accelerated, allocation-free.
- `TokenStream` SoA type for cache-optimal batch token storage.
- Unit tests and property-based tests (`testing/quick`) for all SWAR primitives.
- Benchmark suite with naive baselines.
- Dual licensing: Apache 2.0 + MIT.
