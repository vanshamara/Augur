# Contributing

Keep changes small, tested, and easy to review.

## Before You Start

- Open an issue for larger behavior changes.
- Keep real API keys, tenant keys, local configs, and traces out of commits.
- Use the existing package structure and naming style.

## Local Checks

Run these before opening a pull request:

```bash
go test ./...
go vet ./...
go run ./cmd/demo
go run ./cmd/augur explain --config configs/request-aware.example.yaml --prompt "Say hello." --type chat
```

Also run the config validation command from the README quick start when config
behavior changes.

For routing, fallback, provider, or concurrency changes, also run:

```bash
go test -race ./...
```

## Pull Requests

- Include tests with behavior changes.
- Update README or docs when user-facing behavior changes.
- Keep generated files, private notes, and local state out of the diff.
- Explain the user-visible change and any tradeoff in plain language.

## Public Docs

Write docs for operators and application developers. Prefer short examples.
