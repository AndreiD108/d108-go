# d108-go

The Go library of the d108 observability contract: `identity`, `failure`, `logging`, `paths`,
`atomicfile`, `work`, `tracing`, `metrics`, `httpserver`, `lifecycle`, `cli`, `doctor`,
`config` and `secrets`. The contract (`standard.md`, schema, golden corpus, `logcheck`) is the
private repo `github.com/AndreiD108/d108`; `CLAUDE.md` maps this module. MIT license.

## Use it

```sh
go get github.com/AndreiD108/d108-go@v0.1.1
```

The module is public. `v0.1.1` is the first version that builds without credentials;
`v0.1.0` still requires the private `logcheck` module.

## Releases

Tags `vX.Y.Z` on `main`. `v0.1.0` (2026-10-29) was the first; the API can still change before
the JS and Python libraries settle it.

## Develop

```sh
make test             # go test ./...
make sync-contract    # copy cases.json, the schema and secrets.json from ../d108
```

The tests check written lines against the contract with the `logcheck` binary of the private
contract repo (package `contracttest`). Without `logcheck` on PATH those checks skip and the
rest of the suite runs. To install it (needs access to the private repo):

```sh
GOPRIVATE='github.com/AndreiD108/*' go install github.com/AndreiD108/d108/logcheck/cmd/logcheck@v0.1.0
```

`GOPRIVATE` keeps `go` away from the public proxy and checksum database for the private
module; `git` also needs credentials for `https://github.com` (`gh auth setup-git`, or
`git config --global url.git@github.com:.insteadOf https://github.com/`).

`make sync-contract` copies the contract files by hand from a checkout of the private `d108`
next to this one; `internal/golden` fails while the copies differ from it, and skips that
comparison when the checkout is absent.
