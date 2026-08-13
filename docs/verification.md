# Verification

Run the following commands from the repository root to verify the project before merging or submitting changes:

```bash
go fmt ./...
go build ./...
go vet ./...
go test -count=1 ./...
```

## Known notes

- `go mod tidy` may require network access for transitive test dependencies of `yaml.v3`.
- All commands should be run from the repository root.
