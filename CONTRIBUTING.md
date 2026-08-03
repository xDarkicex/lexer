# Contributing to Lexer

Thank you for your interest in contributing.

## How to Contribute

1. **Fork** the repository and create a branch from `main`.
2. **Write tests** for any new functionality — unit tests and property-based tests (`testing/quick`) are both welcome.
3. **Run the test suite** before opening a pull request:

   ```bash
   go test ./... -bench=. -benchmem
   ```

4. **Open a pull request** against `main`. Describe the change and its motivation.
5. PRs are reviewed by the maintainers; feedback is provided within a few days.

## Code Style

- Follow the standard Go formatting conventions (`gofmt`).
- Keep SWAR implementations annotated with the bitwise trick being used.
- Avoid allocations in hot paths.

## Reporting Issues

Bugs, performance regressions, and missing token kinds are all welcome. Please include:

- The input that was lexed incorrectly.
- The expected token kinds.
- The actual token kinds.
- Go version and CPU architecture.
