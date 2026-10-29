# d108-go

The Go library of the d108 observability contract: `identity`, `failure`, `logging`, `paths`,
`atomicfile`, `work`, `tracing`, `metrics`, `httpserver`, `lifecycle`, `cli`, `doctor`,
`config` and `secrets`. The contract (`standard.md`, schema, golden corpus, `logcheck`) is the
private repo `github.com/AndreiD108/d108`; `CLAUDE.md` maps this module.

## Use it

The module is private. Before `go get`, `go mod tidy` or `go mod vendor`:

```sh
export GOPRIVATE='github.com/AndreiD108/*'
gh auth setup-git    # or: git config --global url.git@github.com:.insteadOf https://github.com/
go get github.com/AndreiD108/d108-go@v0.1.0
```

`GOPRIVATE` keeps `go` away from the public proxy and checksum database for these modules;
the second line gives `git` credentials for `https://github.com`. A build with `-mod=vendor`
needs neither.

## Releases

Tags `vX.Y.Z` on `main`. `v0.1.0` (2026-10-29) is the first; the API can still change before
the JS and Python libraries settle it. Tests depend on `github.com/AndreiD108/d108/logcheck`
by its tag `logcheck/vX.Y.Z`.

## Develop

```sh
make test             # go test ./...
make sync-contract    # copy cases.json, the schema and secrets.json from ../d108
```
