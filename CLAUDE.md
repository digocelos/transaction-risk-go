# transaction-risk-go

Go toolkit for payer-side Pix fraud scoring: validate inputs, prepare features, train and select, package, evaluate, and score. See `README.md` for usage and `docs/` for the contracts, the feature schema, the ADRs, and the operating limits.

## Layout

Workflow packages, in the order a model moves through them:

- `pixinput/`: decodes and validates `pix-payer-input-v2` scoring inputs.
- `pixrisk/`: the `pix-risk-v1` feature schema: preparation, fitted standardization, coverage.
- `trainingdata/`: reads and classifies `pix-training-example-v2` datasets.
- `trainingcsv/`: converts training data in the `pix-training-csv-v1` CSV layout to JSON Lines; the template is in `templates/training-data/`.
- `trainingconfig/`: decodes `pix-training-config-v2` configurations.
- `evaluation/`: temporal partitions, metrics, threshold selection.
- `nn/`: the dense network and full-batch gradient descent.
- `training/`: runs training, selection, and limits; writes `trainingreport` reports.
- `modelartifact/`, `trainingrun/`: the `pix-risk-model-v1` artifact and published runs.
- `modelevaluation/`: independent evaluation of saved artifacts.
- `scoring/`: loads an artifact once and scores inputs.
- `internal/`: strict JSON, synthetic datasets, and the end-to-end test.
- `cmd/`: `convert`, `synthesize`, `train`, `promote`, `evaluate`, `predict`.

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
- **Versions**: contracts, the feature schema, the training configuration, reports, and the artifact each have their own version (ADR 0003). Any change to what a version accepts or produces, including feature order or preparation, is a new version with its own specification; never change a released version in place (ADR 0006).
- **Data**: no real payment data, credentials, or production artifacts in the repository. Tests and examples use synthetic data and generate their artifacts. Real data belongs in `data/`, which git ignores.

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
