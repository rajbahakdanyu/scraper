# Tasks 21 and 22 Report

## Task 21

- Extracted shared signal watching, parallel download, ID assignment, and JSON item-result orchestration.
- Added `model.TitleOf`, `model.AssignIDs`, and `model.WrapPermanent`.
- Applied title fallback and permanent-error handling across the requested callers.
- Commit: `9867c7d refactor: extract shared orchestration helpers in plain.go and model`

## Task 22

- Removed the verified-unused retry helper, fields, JSON fields/helper, style constant, wrappers, batch type, filter flag, and exported YouTube wrapper.
- Retargeted dry-run tests and removed references to deleted fields.

## Verification

- `/opt/homebrew/bin/gofmt -w .`
- `/opt/homebrew/bin/go vet ./...`
- `/opt/homebrew/bin/go test -race ./...`

All verification commands passed.
