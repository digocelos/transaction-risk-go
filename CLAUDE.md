# transaction-risk-go

Go toolkit for learning and building financial transaction fraud detection models, starting with Pix. See `README.md` for usage.

## Layout

- `transaction/`: transaction model and feature extraction (`FeatureSchema`).
- `nn/`: dense neural network, training, and JSON persistence.
- `cmd/demo`: trains on fictional data and writes `model.json`.
- `cmd/predict`: scores one `pix-payer-input-v2` input with the artifact given by `-model`, through the `scoring` package.
- `cmd/synthesize`: writes synthetic datasets for measuring the toolkit; see `docs/operations/operating-limits.md`.

## Commands

Run from the repo root.

```bash
go build ./...
go vet ./...
go test ./...
gofmt -l .
```

## Conventions

- **Language**: write code, comments, documentation, error messages, tests, commit messages, and issue and PR content in English. Explain things to the user in Portuguese.
- **License**: the project is Apache-2.0. Keep `LICENSE` unchanged. Add only dependencies with Apache-2.0-compatible licenses.
- **Dependencies**: the module uses the standard library only. Adding an external dependency is an architectural decision; record it in `docs/adr/`.
- **Model compatibility**: if feature order or scaling in `transaction/features.go` changes, bump `FeatureSchema` and regenerate `model.json` with `go run ./cmd/demo`.

## Incremental development

Work one bounded phase at a time:

- A phase has one clear goal and explicit done criteria, usually one GitHub issue.
- Finish, verify (build, vet, test, gofmt), and report a phase before starting the next one.
- Do not pull work from later phases into the current one. Record follow-ups as new issues instead.

## Agent skills

### Issue tracker

Issues live in this repo's GitHub Issues, managed with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: root `GLOSSARY.md` plus `docs/adr/`. See `docs/agents/domain.md`.
