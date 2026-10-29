# d108-go

The Go library of the d108 observability contract (`../d108/standard.md`): one package per
job, built in the order the standard's section 9 lists them. This batch holds the parts that
name the program and write the line (`identity`, `failure`, `logging`, `paths`, `atomicfile`)
the units of work with the OTel SDK wiring (`work`, `tracing`, `metrics`, `httpserver`) and
the process owner (`lifecycle`).
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
  ctx.go                 WithAttrs/Attrs, AttrSet (the unit's mutable attrs), NewContext/FromContext, Flatten for spans
  ctx_test.go            AttrSet on later lines, nesting, record over set over fixed precedence, nil set
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
work/                    Units of work (section 4): command.run, request.handled, span, Add, Env, job.progress
  attrs.go               Package doc; Add/AddAll onto the unit's AttrSet; slog attrs to span attrs; endSpan
  command.go             Command: span (parent from ctx or TRACEPARENT), end writes command.run; outcomeOf
  http.go                HTTP middleware: traceparent, caller.id, panic to 500 or abort, request.handled; SetRoute; status writer
  env.go                 Env (TRACEPARENT/TRACESTATE for children), parentFromEnv; the W3C propagator used directly
  progress.go            NewProgress/Done: job.progress every total/20 items, 10 s, or at the total
  command_test.go        Line fields per outcome, end once, Add outside a unit, TRACEPARENT in, span attrs equal the line
  http_test.go           Fields, panic 500 or abort, ErrAbortHandler line, 1xx, SetRoute via clone, traceparent, Flusher
  env_test.go            Env nil without span, header values, round trip through a child test process
  progress_test.go       Every twentieth, last item, small totals, unknown total by time, interval with total
tracing/                 Trace SDK on when OTEL_EXPORTER_OTLP_ENDPOINT is set
  tracing.go             Setup: OTLP gRPC exporter, batcher, identity resource, W3C propagators; shutdown flushes
  tracing_test.go        Off: no SDK provider, no goroutines; on: provider, propagator fields, bounded shutdown
metrics/                 Metric SDK, same switch and resource
  metrics.go             Setup: OTLP gRPC exporter, periodic reader every 60 s; shutdown flushes
  metrics_test.go        Off: no SDK provider, no goroutines; on: provider, a counter, bounded shutdown
httpserver/              http.Server with work.HTTP, /healthz, CORS, pprof
  httpserver.go          New with Addr/CORS/Pprof, hand dispatch so r.Pattern stays the app's; ListenAndServe(ctx); Shutdown
  httpserver_test.go     /healthz silent, panic line, no route for a plain handler, CORS, pprof, cancel waits for requests
lifecycle/               Run owns main: setup, process.start, the stop sequence, process.stop/crash, flush, exit code
  lifecycle.go           Package doc, options (Grace, Heartbeat, Logging), Run and start: logger, Check, SDKs, process.start
  run.go                 The run state in ctx, reasons, cause; OnStop, Stop, Go; stop records the first cause, logs late ones
  stop.go                runFunc, wait (grace timer, forced), sequence (cancel, goroutines, steps reversed, fn), the last line
  signals.go             SIGINT/SIGTERM/SIGHUP to the cause, a later signal forces; the signal codes (129, 130, 143)
  heartbeat.go           process.heartbeat every interval until the stop
  lifecycle_test.go      Parent side: runs the test binary per scenario, reads the log file through logcheck, asserts
  scenarios_test.go      The child scenarios: clean, signals, stop, failing and hanging steps, panics, late causes, forcing, cap, flush
  internal_test.go       Heartbeat interval, second Run panics, foreign ctx panics
  example_test.go        The section 9.1 server, compiled
internal/
  otelres/               Shared by tracing and metrics
    otelres.go           EndpointVar switch, identity resource over the SDK default, error handler to the logger
    otelres_test.go      Resource attributes, Enabled, WARN line from the error handler
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
