VERSION = $(shell git describe --abbrev=0 --candidates 0 2>/dev/null || echo "development")

.PHONY: clean
clean:
	@rm -rf build

.PHONY: release
release: clean
	@mkdir build
	gox -osarch="darwin/amd64 linux/amd64" -output="build/{{.Dir}}_{{.OS}}_{{.Arch}}" -ldflags "-X main.version=$(VERSION)"

