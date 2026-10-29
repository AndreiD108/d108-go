module github.com/AndreiD108/d108-go

go 1.27.3

require (
	github.com/AndreiD108/d108/logcheck v0.0.0-00010101000000-000000000000
	github.com/oklog/ulid/v2 v2.1.2
	go.opentelemetry.io/otel/trace v1.47.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	golang.org/x/text v0.14.0 // indirect
)

replace github.com/AndreiD108/d108/logcheck => ../d108/logcheck
