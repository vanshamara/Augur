# Release Checklist

Use this before making the repo public or cutting a release.

## Code

- [ ] Run `go test ./...`.
- [ ] Run `go vet ./...`.
- [ ] Run `go run ./cmd/demo` and confirm all six product promises hold.
- [ ] Run `go test -race ./...`.
- [ ] Run `bash -n scripts/smoke-test.sh`.
- [ ] Run `scripts/smoke-test.sh`.
- [ ] Run `scripts/routing-smoke-test.sh`.
- [ ] Build the Docker image with `docker build -t augur:local .`.
- [ ] Check `git diff --check`.
- [ ] Check `git status --short --untracked-files=all`.

## Config

- [ ] Confirm every public config in `configs/` loads in tests.
- [ ] Keep real config files outside the repo.
- [ ] Keep `OPENAI_API_KEY`, gateway keys, and tenant keys in the environment.
- [ ] Confirm `.gitignore` and `.dockerignore` still exclude local secrets.

## Docs

- [ ] Update `README.md`.
- [ ] Update `docs/config-reference.md` when config fields change.
- [ ] Update `docs/deployment.md` when runtime behavior changes.
- [ ] Confirm public docs separate built, partial, and not included features.
- [ ] Confirm canary and fallback wording matches the implementation.
- [ ] Keep `docs-private/` out of public commits.

## Smoke

- [ ] Run the local health smoke test.
- [ ] Run the multi-backend routing smoke test.
- [ ] Run a real chat smoke test with a small model before public launch.
- [ ] Verify `/healthz` and `/readyz` work without gateway auth.
- [ ] Verify `/v1/chat/completions` and `/debug/*` require auth when
  `AUGUR_GATEWAY_API_KEYS` is set.

## Publish

- [ ] Run a final secret scan.
- [ ] Review the staged files.
- [ ] Commit with a short message.
- [ ] Push from your own terminal.
