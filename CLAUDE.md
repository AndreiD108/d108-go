# d108-go

The Go library of the d108 observability contract (`../d108/standard.md`): one package per
job, built in the order the standard's section 9 lists them. This batch holds the parts that
name the program and write the line: `identity`, `failure`, `logging`, `paths`, `atomicfile`.
`internal/golden` runs the contract's corpus through `logging` and is the acceptance test.

## Scope

The whole module `github.com/AndreiD108/d108-go`. No child maps.

## File tree

Use this tree as a map — scan it to find the parts your task touches, then go straight there.

<!-- BEGIN FILE TREE -->
```
Makefile                 `test`; `sync-contract` copies cases.json and the schema from ../d108
identity/                service.name, service.version, service.instance.id (ULID), pid, env prefix
  identity.go            Identity struct, New (name check, version by ldflags > build info > vcs > dev), EnvPrefix
  identity_test.go       Fields, prefix rules, bad names panic, version resolution table
failure/                 Classed errors, the shared exit-code table 0–8 plus 130 and 143, the stderr envelope
  failure.go             Class constants, Register (9–125), New, Wrap, ClassOf, Code, ExitCode, From, Write
  failure_test.go        Table, class of plain and context errors, Wrap cause, Register limits, envelope shape
logging/                 slog.Handler writing the standard's JSON line; files, cap, retention, text on stderr
  handler.go             New with its options, the handler (Enabled, Handle, WithAttrs, WithGroup), the fallback lines
  line.go                The ordered line: fixed keys, attribute flattening, errors to exception.*, JSON encoding
  value.go               plainValue: any Go value to JSON-shaped data by reflection; non-finite floats as strings
  truncate.go            64 KiB limit: longest string first, middle cut with the sentinel, 256-byte floor
  output.go              Settings and Resolve (output and level with their sources), Check, journal detection
  files.go               Sinks: one write per line; dated files, rollover, size cap, log.capped, log.dropped, sweep
  text.go                Text rendering for stderr: time, level, event or body, key=value, long values indented
  ctx.go                 WithAttrs and Attrs on the context; NewContext and FromContext for the logger
  logging_test.go        Value conversions, errors, groups, context precedence, env level and output, fallback, text
  truncate_test.go       Longest first, ties, once per value, floor, UTF-8 boundaries, unfittable line, sentinel digits
  files_test.go          Rollover, size cap and reopen, dropped count, partial write, retention, 8 goroutines and 8 processes
  value_test.go          Nested NaN keeps siblings, inline groups under WithGroup, json.Number grammar, structs, cycles
paths/                   The six per-app directories on Linux and macOS, env overrides
  paths.go               Paths struct, For with the Prefix option, resolution order per platform
  paths_test.go          Defaults per platform, resolution order, env through For
atomicfile/              Temp file, sync and rename writes
  atomicfile.go          Write(path, data, mode); creates the parent 0700
  atomicfile_test.go     Mode, overwrite, no temp left, target kept on failure
internal/
  golden/                Acceptance test against the contract's corpus
    cases.json           Copy of ../d108/golden/cases.json
    log-line.schema.json Copy of ../d108/schema/log-line.schema.json
    golden_test.go       Every case through logging and logcheck.Check; run cases emitted as work will; copy drift
```
<!-- END FILE TREE -->

## Discipline

- Source code files cap at ~500 lines (tests included). Split by concern if crossing.
- Above rule applies even to files you have not authored yourself. If you touch it, you fix it.
- A directory's listing — its files and its subdirectories with their one-liners — reads as the complete picture of one module at that level.
- CLAUDE.md file trees mirror reality. Update the relevant CLAUDE.md in the same change as a file add/remove/rename/repurpose.
- Tree descriptions: few words, elliptical, what the file is or a rough overview of its contents — not a sentence; ≤120 chars.
- Directories with their own CLAUDE.md get a folder summary, not a recursive tree.

## Contract

- `internal/golden/cases.json` and `log-line.schema.json` are copies; edit the sources in
  `../d108` and run `make sync-contract`. `TestCopiesMatchContract` fails while they differ.
- Tests import `github.com/AndreiD108/d108/logcheck` through a `replace` to `../d108/logcheck`
  in `go.mod`; both repos sit side by side under `~/Projects/Devbox/common/`.
- `logging.Event(name)` is the one way to set `event_name`; a plain `event_name` attribute is
  written as `attr.event_name` like any other fixed key.
- An attribute whose value is an `error` becomes `exception.type` and `exception.message`
  (plus `exception.stacktrace` when the error has `StackTrace() string`); its key is dropped.

## Writing style

Simplified Technical English in every file: short sentences, one term per thing, active voice.
No "later" lists and no "proposed" labels; a thing is built or dropped with its reason.
