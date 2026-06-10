//go:build js && wasm

// Command drawverify-wasm compiles the draw verifier to WebAssembly so a draw can
// be verified entirely in the user's browser, using the same `drawproof` code as
// the platform — the player does not have to trust the platform's result.
//
// Build:
//
//	GOOS=js GOARCH=wasm go build -o drawverify.wasm ./cmd/drawverify-wasm/
//	cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" .   # glue shipped with Go
//
// Use (browser):
//
//	const go = new Go();
//	WebAssembly.instantiateStreaming(fetch("drawverify.wasm"), go.importObject)
//	  .then(r => { go.run(r.instance); });
//	// then, given one proof bundle from the public verification endpoint:
//	const result = JSON.parse(drawverify(JSON.stringify(bundle)));
//	// result = { ok: bool, checks: [{name, ok, detail}, ...] }
//
// Note: drawverify reproduces the draw from the bundle's own values (seed, beacon
// value, pool). To make the beacon check fully independent, the page can also
// fetch the named pulse from a public drand relay
// (https://api.drand.sh/public/<beaconPulse>) and confirm its randomness equals
// the bundle's reveal.beaconValue — a plain string compare in JS.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/network-gaming/draw-verification-wasm/drawproof"
)

// verify is exposed to JS as the global function `drawverify(bundleJSON)`.
func verify(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("missing argument: bundle JSON string")
	}
	var b drawproof.Bundle
	if err := json.Unmarshal([]byte(args[0].String()), &b); err != nil {
		return errResult("invalid bundle JSON: " + err.Error())
	}
	res, err := drawproof.VerifyBundle(b)
	if err != nil {
		return errResult(err.Error())
	}
	out, err := json.Marshal(res)
	if err != nil {
		return errResult(err.Error())
	}
	return string(out)
}

func errResult(msg string) string {
	out, _ := json.Marshal(map[string]any{"ok": false, "error": msg})
	return string(out)
}

func main() {
	js.Global().Set("drawverify", js.FuncOf(verify))
	select {} // keep the Go runtime alive so the exported function stays callable
}
