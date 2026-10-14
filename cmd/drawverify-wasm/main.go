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
// Large main-draw pools are published as a file (bundle.poolFile) rather than
// inline. Feed the file's text to drawverifyPoolFeed in chunks (any order, one
// entry per line), then call drawverify with the bundle: it verifies against
// the fed pool when the bundle carries no inline pool. drawverifyPoolReset
// clears it. The pool is held as int32 values, 40 MB for ten million entries.
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

var pool = &drawproof.NumericPool{}

// verify is exposed to JS as the global function `drawverify(bundleJSON)`.
func verify(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("missing argument: bundle JSON string")
	}
	var b drawproof.Bundle
	if err := json.Unmarshal([]byte(args[0].String()), &b); err != nil {
		return errResult("invalid bundle JSON: " + err.Error())
	}
	var res drawproof.VerifyResult
	var err error
	if (b.Kind == drawproof.KindMainDraw || b.Kind == drawproof.KindVault) && len(b.Pool) == 0 && pool.Len() > 0 {
		if b.PoolCount > 0 && b.PoolCount != pool.Len() {
			return errResult("pool file holds " + itoa(pool.Len()) + " entries but the bundle declares " + itoa(b.PoolCount))
		}
		res = pool.Verify(b.Commit, b.Reveal)
	} else {
		res, err = drawproof.VerifyBundle(b)
		if err != nil {
			return errResult(err.Error())
		}
	}
	out, err := json.Marshal(res)
	if err != nil {
		return errResult(err.Error())
	}
	return string(out)
}

// poolFeed is `drawverifyPoolFeed(text)`: appends the entries in text.
func poolFeed(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return "missing argument: text"
	}
	if err := pool.Feed(args[0].String()); err != nil {
		return err.Error()
	}
	return ""
}

// poolReset is `drawverifyPoolReset()`.
func poolReset(this js.Value, args []js.Value) any {
	pool = &drawproof.NumericPool{}
	return nil
}

// poolInfo is `drawverifyPoolInfo()`: {count, digest} of the fed pool.
func poolInfo(this js.Value, args []js.Value) any {
	out, _ := json.Marshal(map[string]any{"count": pool.Len(), "digest": pool.Digest()})
	return string(out)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func errResult(msg string) string {
	out, _ := json.Marshal(map[string]any{"ok": false, "error": msg})
	return string(out)
}

func main() {
	js.Global().Set("drawverify", js.FuncOf(verify))
	js.Global().Set("drawverifyPoolFeed", js.FuncOf(poolFeed))
	js.Global().Set("drawverifyPoolReset", js.FuncOf(poolReset))
	js.Global().Set("drawverifyPoolInfo", js.FuncOf(poolInfo))
	select {} // keep the Go runtime alive so the exported functions stay callable
}
