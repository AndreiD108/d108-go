CONTRACT ?= ../d108

.PHONY: test sync-contract

test:
	go test ./...

# Copy the corpus and the schema from the contract repo.
sync-contract:
	cp $(CONTRACT)/golden/cases.json internal/golden/cases.json
	cp $(CONTRACT)/schema/log-line.schema.json internal/golden/log-line.schema.json
