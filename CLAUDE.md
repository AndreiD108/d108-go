# d108-go

The Go library of the d108 observability contract (`../d108/standard.md`): one package per
job, built in the order the standard's section 9 lists them. This batch holds the parts that
name the program and write the line (`identity`, `failure`, `logging`, `paths`, `atomicfile`)
the units of work with the OTel SDK wiring (`work`, `tracing`, `metrics`, `httpserver`), the
process owner (`lifecycle`, with `lifecycletest` for service tests), the CLI kit (`cli`, `cli/clitest`, `doctor`) and the two kinds of
configuration (`config`, `secrets`). `contracttest` checks lines against the contract in tests.
`internal/golden` runs the contract's corpus through `logging` and is the acceptance test.

## Scope

The whole module `github.com/AndreiD108/d108-go`. No child maps.

## File tree

Use this tree as a map — scan it to find the parts your task touches, then go straight there.

<!-- BEGIN FILE TREE -->
```
Makefile                 `test`; `sync-contract` copies cases.json, the schema and secrets.json from ../d108
LICENSE                  MIT
README.md                What the module is (public), release tags, installing logcheck from the private repo
identity/                service.name, service.version, service.instance.id (ULID), pid, env prefix
  identity.go            Identity struct, New (name check, version by ldflags > build info > vcs > dev), EnvPrefix
  identity_test.go       Fields, prefix rules, bad names panic, version resolution table
failure/                 Classed errors, the shared exit-code table 0–8 plus 130 and 143, the stderr envelope
  failure.go             Class constants, Register (9–125), New, Wrap, ClassOf, Code, ExitCode, From, Write
  failure_test.go        Table, class of plain and context errors, Wrap cause, Register limits, envelope shape
logging/                 slog.Handler writing the standard's JSON line; files, cap, retention, text on stderr
  handler.go             New with its options (Writer, Level for tests), SettingsOf (what the logger does), the handler, fallback
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
  paths.go               Paths struct, For with the Prefix option (no trailing underscore), resolution order, TestEnv
  paths_test.go          Defaults per platform, resolution order, env through For
  testenv_test.go        TestEnv values and resolution, the inherited app variable wins, prefix form and panics in paths and config
atomicfile/              Temp file, sync and rename writes
  atomicfile.go          Write(path, data, mode); creates the parent 0700
  atomicfile_test.go     Mode, overwrite, no temp left, target kept on failure
work/                    Units of work (section 4): command.run, request.handled, span, Add, Env, job.progress
  attrs.go               Package doc; Add/AddAll onto the unit's AttrSet; slog attrs to span attrs; endSpan
  command.go             Command: span (parent from ctx or TRACEPARENT), end writes command.run; outcomeOf
  request.go             Request for socket and RPC servers: Traceparent and Caller options, server span, request.handled with rpc.method
  http.go                HTTP middleware: traceparent, caller.id, panic to 500 or abort, request.handled; SetRoute; status writer
  env.go                 Env (TRACEPARENT/TRACESTATE for children), parentFromEnv; the W3C propagator used directly
  progress.go            NewProgress/Done: job.progress every total/20 items, 10 s, or at the total
  command_test.go        Line fields per outcome, end once, Add outside a unit, TRACEPARENT in, span attrs equal the line
  request_test.go        request.handled per outcome, caller.id on inner lines, traceparent in, bad traceparent is a root, span attrs
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
lifecycle/               Run owns main: setup, default logger, process.start, the stop sequence, process.stop/crash, flush, exit code
  lifecycle.go           Package doc, options (Grace, Heartbeat, Logging), Run and start: logger, Check, SDKs, slog.SetDefault, process.start
  run.go                 The reasons; OnStop, Stop, Go over the run in ctx
  signals.go             SIGINT/SIGTERM/SIGHUP to the cause, a later signal forces; the signal codes (129, 130, 143)
  heartbeat.go           process.heartbeat every interval until the stop
  lifecycle_test.go      Parent side: runs the test binary per scenario, reads the log file, contract check, asserts
  scenarios_test.go      The child scenarios: clean, signals, stop, failing and hanging steps, panics, late causes, forcing, cap, flush, default logger
  internal_test.go       Heartbeat interval, second Run panics, foreign ctx panics
  example_test.go        The section 9.1 server, compiled
  internal/
    run/                 The run state and the stop sequence, shared by lifecycle and lifecycletest
      run.go             Run in ctx, Options, Cause; New, FromContext, OnStop, Go, Stop (first cause, late ones logged), Force
      stop.go            RunFunc, Wait (grace timer, forced, flush), Outcome, sequence (cancel, goroutines, steps reversed, fn), the last line
  lifecycletest/         The stop sequence in-process for a service test
    lifecycletest.go     Run(t, fn, opts) Result: the run over a buffer logger set as slog.Default, no signals or SDKs; Lines for contracttest
    lifecycletest_test.go  Stop order after the Go drain, default logger in and restored, failing steps, panics, two runs, grace forces
cli/                     The Cobra tree of a tool (section 9.2): --format, Output, envelope, work.Command per run
  root.go                Package doc, Root (identity, --format, doctor, help command, usage template with Formats), Group, IdentityOf
  command.go             Runner, Formats, Command (Use line); runLeaf: format check, work.Command, leaf in ctx, recover, args.flags
  output.go              Format constants, Output (JSON once, JSONL, Text; one write each), formatList
  execute.go             Execute with injected streams; Stdin, Stderr, Flags, CommandPath from ctx; details.command on every envelope
  envelope.go            Cobra checks to usage failures (flagError, wrapValidation, __complete), hook errors classed, incomplete lists commands
  secret.go              ReadSecret: echo off by termios, a poller-registered dup of stdin so ctx ends the read, first line when piped
  termios_linux.go       TCGETS and TCSETS
  termios_darwin.go      TIOCGETA and TIOCSETA
  cli_test.go            Sample tree through clitest: formats, envelope per class, usage and hook errors, panic, cancel, help, doctor
  ctx_test.go            Stderr, Flags, CommandPath in a runner and outside, the Use line in help, ReadSecret piped, blocked and cancelled
  envelope_test.go       details.command on every envelope and the app's own wins, help of an unknown command, __complete
  secret_linux_test.go   ReadSecret on a pty from /dev/ptmx: echo off at the read and on after, blocking mode restored, cancel
  clitest/               Run a tree in tests
    clitest.go           Run/RunContext (exit, stdout, stderr; debug logger to a buffer), Lines (decoded), Raw (for contracttest)
doctor/                  The report behind every tool's doctor command
  doctor.go              Item/Section/Report, Add hook, Build (program, logging, telemetry probe), Text, ordered MarshalJSON
  doctor_test.go         Env to items, logger in ctx wins, fallback, today's file, tracing switch, probe, Add order, text and JSON
config/                  Settings (section 9.5): config.json then the environment through go-envconfig, one parser for both
  load.go                Package doc (precedence, file values as variable strings), Prefix option (no trailing underscore), Load, error classing
  fields.go              The struct walk: a setting is a field with json and env tags; tag parsing; the env-then-file Lookuper, resolved so far
  file.go                config.json to parser strings per setting; unknown keys and wrong shapes are usage failures
  doctor.go              The "settings" section: a snapshot at Load with value and source; secret fields as set or unset; the error item on a failed Load
  config_test.go         Precedence matrix, Prefix(""), every error message, bad structs panic, embedded and pointers
  doctor_test.go         Doctor items and sources, secret fields and groups, the settings section after a failed Load
secrets/                 One encrypted file of names and values (section 9.5): AES-256-GCM, HKDF over a key file and the machine id
  store.go               Package doc, Store (Open, Save, Load, Delete, Names), read and write through atomicfile, failure classes
  crypto.go              The file format constants, envelope, deriveKey, seal, open; errFormat and errOpen
  machine.go             machineID, the source labels, readMachine hook for tests, normalize
  machine_linux.go       /etc/machine-id
  machine_darwin.go      IOPlatformUUID from ioreg
  doctor.go              The "secrets" section: names only, "none", or one error item
  secrets_test.go        Round trip, modes, no clear text, delete, other machine or platform is auth, damaged files, concurrent saves, doctor, fixture
  testdata/
    secrets.json         Copy of ../d108/golden/secrets.json: one envelope with its inputs and plaintext
contracttest/            Lines against the contract in tests: the logcheck binary on PATH, skip without it
  contracttest.go        Check(tb, lines): one logcheck run over a temp file; only a missing binary skips; Install
  contracttest_test.go   A shell-script logcheck: pass, missing, exit 1, exit 2, signal, no newline, one run over the joined lines
internal/
  otelres/               Shared by tracing and metrics
    otelres.go           EndpointVar switch, identity resource over the SDK default, error handler to the logger
    otelres_test.go      Resource attributes, Enabled, WARN line from the error handler
  golden/                Acceptance test against the contract's corpus
    cases.json           Copy of ../d108/golden/cases.json
    log-line.schema.json Copy of ../d108/schema/log-line.schema.json
    golden_test.go       Every case through logging and contracttest; run cases emitted as work will; copy drift
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
- This module is public (MIT); the contract repo `d108` is private. No Go import links them:
  tests check lines with `contracttest.Check`, which runs the `logcheck` binary from PATH and
  skips without it. Installing `logcheck` needs `GOPRIVATE=github.com/AndreiD108/*` and git
  credentials for `https://github.com` (README).
- Put `contracttest.Check` in its own `t.Run("contract", ...)` when assertions follow it, so
  its skip does not skip them. One call per batch of lines, not per line.
- Releases are tags `vX.Y.Z` on `main`; `v0.1.0` is the first (2026-10-29), `v0.1.1` the first
  without the private dependency. Consumers (`sift-cli`, `plottingroom`) require the tag, no
  `replace`.
- `logging.Event(name)` is the one way to set `event_name`; a plain `event_name` attribute is
  written as `attr.event_name` like any other fixed key.
- An attribute whose value is an `error` becomes `exception.type` and `exception.message`
  (plus `exception.stacktrace` when the error has `StackTrace() string`); its key is dropped.

## Writing style

Simplified Technical English in every file: short sentences, one term per thing, active voice.
No "later" lists and no "proposed" labels; a thing is built or dropped with its reason.
