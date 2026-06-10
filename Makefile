.PHONY: test wasm serve

test:
	go test ./...

# Reproducible WASM build: -trimpath + no dependencies means the same Go
# toolchain version produces a byte-identical binary.
wasm:
	GOOS=js GOARCH=wasm go build -trimpath -o docs/drawverify.wasm ./cmd/drawverify-wasm/
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" docs/
	shasum -a 256 docs/drawverify.wasm

serve:
	python3 -m http.server -d docs 8080
