set shell := ["bash", "-euo", "pipefail", "-c"]

tmp := justfile_directory() + "/.tmp"

go_sources := "find . -type f -name '*.go' -not -path './.*' -print0"

default: check

# Write a temporary modfile that resolves the SDK to the staged copy of the running pig.
_modfile:
	mkdir -p "{{tmp}}"
	cp go.mod "{{tmp}}/go.mod"
	if test -f go.sum; then cp go.sum "{{tmp}}/go.sum"; else : > "{{tmp}}/go.sum"; fi
	printf '\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "$(pig reload --sdk-path)" >> "{{tmp}}/go.mod"

fmt:
	{{go_sources}} | xargs -0 -r gofmt -w

fmt-check:
	files="$({{go_sources}} | xargs -0 -r gofmt -l)"; test -z "$files" || { printf '%s\n' "$files"; exit 1; }

vet: _modfile
	go vet -modfile="{{tmp}}/go.mod" ./...

test: _modfile
	go test -modfile="{{tmp}}/go.mod" ./...

race: _modfile
	go test -race -modfile="{{tmp}}/go.mod" ./...

# Add or upgrade a dependency against the staged SDK, then copy the result back.
get +pkgs: _modfile
	go get -modfile="{{tmp}}/go.mod" {{pkgs}}
	go mod tidy -modfile="{{tmp}}/go.mod"
	grep -v '^replace github.com/MichaelKinsy/PiG/extensions/sdk' "{{tmp}}/go.mod" > go.mod
	cp "{{tmp}}/go.sum" go.sum

validate:
	pig install . --validate-only --json

artifacts:
	python3 scripts/check_artifacts.py .

check: fmt-check vet test validate artifacts

# Refresh the bundled first-run catalog from the running pig's builtin anthropic catalog.
catalog:
	CATALOG_DUMP_OUT="{{justfile_directory()}}/internal/providers/claude/catalog.json" pig --no-extensions --offline --no-session -e scripts/catalog-dump -p "/catalog-dump anthropic"
