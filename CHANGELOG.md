# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
