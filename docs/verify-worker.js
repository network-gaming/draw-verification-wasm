// Web Worker for the draw verifier: runs the WASM off the main thread so a
// ten-million-entry pool sorts and hashes without freezing the page.
//
// Messages in:  {type:"verify", id, bundle, poolUrl?, poolFile?}
// Messages out: {type:"progress", id, loaded}        (bytes of pool read)
//               {type:"result", id, result, poolInfo?}
//               {type:"error", id, error}
importScripts("wasm_exec.js");

const ready = (async () => {
  const go = new Go();
  const buf = await fetch("drawverify.wasm").then(r => r.arrayBuffer());
  const { instance } = await WebAssembly.instantiate(buf, go.importObject);
  go.run(instance); // registers drawverify, drawverifyPoolFeed, drawverifyPoolReset, drawverifyPoolInfo
})();

// Feeds a ReadableStream of text into the WASM pool one chunk at a time,
// carrying a partial last line between chunks.
async function feedStream(stream, id, gzipped) {
  let src = stream;
  if (gzipped) {
    if (typeof DecompressionStream === "undefined") throw new Error("this browser cannot decompress a .gz pool file; save it uncompressed");
    src = stream.pipeThrough(new DecompressionStream("gzip"));
  }
  const reader = src.pipeThrough(new TextDecoderStream()).getReader();
  let carry = "", loaded = 0;
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    loaded += value.length;
    const text = carry + value;
    const cut = text.lastIndexOf("\n");
    const whole = cut >= 0 ? text.slice(0, cut + 1) : "";
    carry = cut >= 0 ? text.slice(cut + 1) : text;
    if (whole) { const err = drawverifyPoolFeed(whole); if (err) throw new Error(err); }
    postMessage({ type: "progress", id, loaded });
  }
  if (carry.trim()) { const err = drawverifyPoolFeed(carry); if (err) throw new Error(err); }
}

onmessage = async (ev) => {
  const { type, id, bundle, poolUrl, poolFile } = ev.data;
  if (type !== "verify") return;
  try {
    await ready;
    drawverifyPoolReset();
    let poolInfo = null;
    const needsPool = (bundle.kind === "MAIN_DRAW" || bundle.kind === "VAULT") && !(bundle.pool && bundle.pool.length) && (poolUrl || poolFile);
    if (needsPool) {
      if (poolFile) {
        await feedStream(poolFile.stream(), id, /\.gz$/i.test(poolFile.name));
      } else {
        const resp = await fetch(poolUrl);
        if (!resp.ok) throw new Error(`pool file fetch failed: ${resp.status}`);
        // A server that sends Content-Encoding: gzip is decoded by fetch itself;
        // a .gz object served as-is is decoded here.
        const ct = resp.headers.get("content-type") || "";
        await feedStream(resp.body, id, /gzip/.test(ct) || /\.gz(\?|$)/i.test(poolUrl));
      }
      poolInfo = JSON.parse(drawverifyPoolInfo());
    }
    const result = JSON.parse(drawverify(JSON.stringify(bundle)));
    postMessage({ type: "result", id, result, poolInfo });
  } catch (e) {
    postMessage({ type: "error", id, error: e.message || String(e) });
  }
};
