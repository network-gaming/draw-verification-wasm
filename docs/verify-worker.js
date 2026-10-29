// Web Worker for the draw verifier: runs the WASM off the main thread so a
// ten-million-entry pool sorts and hashes without freezing the page.
//
// Messages in:  {type:"verify", id, bundle, poolUrl?, poolFile?, allocationUrl?, allocationFile?}
// Messages out: {type:"progress", id, loaded}        (characters of pool / placement read)
//               {type:"result", id, result, poolInfo?, allocationInfo?}
//               {type:"error", id, error}
//
// poolUrl / poolFile is the entry pool of a main draw or vault draw
// (bundle.poolFile); allocationUrl / allocationFile is the prize placement of
// an instant bundle (bundle.allocationFile). Either is a URL to fetch or a
// File the visitor chose.
importScripts("wasm_exec.js");

const ready = (async () => {
  const go = new Go();
  const buf = await fetch("drawverify.wasm").then(r => r.arrayBuffer());
  const { instance } = await WebAssembly.instantiate(buf, go.importObject);
  go.run(instance); // registers drawverify, drawverifyPoolFeed/Reset/Info, drawverifyAllocationFeed/Reset/Info
})();

// Feeds a ReadableStream of text into a WASM feed function one chunk at a
// time, carrying a partial last line between chunks.
async function feedStream(stream, id, gzipped, feed) {
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
    if (whole) { const err = feed(whole); if (err) throw new Error(err); }
    postMessage({ type: "progress", id, loaded });
  }
  if (carry.trim()) { const err = feed(carry); if (err) throw new Error(err); }
}

// Feeds a chosen File or a fetched URL. A server that sends
// Content-Encoding: gzip is decoded by fetch itself; a .gz object served
// as-is is decoded here.
async function feedSource(file, url, id, feed, what) {
  if (file) {
    await feedStream(file.stream(), id, /\.gz$/i.test(file.name), feed);
    return;
  }
  const resp = await fetch(url);
  if (!resp.ok) throw new Error(`${what} file fetch failed: ${resp.status}`);
  const ct = resp.headers.get("content-type") || "";
  await feedStream(resp.body, id, /gzip/.test(ct) || /\.gz(\?|$)/i.test(url), feed);
}

onmessage = async (ev) => {
  const { type, id, bundle, poolUrl, poolFile, allocationUrl, allocationFile } = ev.data;
  if (type !== "verify") return;
  try {
    await ready;
    drawverifyPoolReset();
    drawverifyAllocationReset();
    let poolInfo = null, allocationInfo = null;
    const needsPool = (bundle.kind === "MAIN_DRAW" || bundle.kind === "VAULT") && !(bundle.pool && bundle.pool.length) && (poolUrl || poolFile);
    if (needsPool) {
      await feedSource(poolFile, poolUrl, id, drawverifyPoolFeed, "pool");
      poolInfo = JSON.parse(drawverifyPoolInfo());
    }
    const hasInline = (bundle.allocation && Object.keys(bundle.allocation).length) || (bundle.units && Object.keys(bundle.units).length);
    const needsAllocation = bundle.kind === "INSTANT" && !hasInline && (allocationUrl || allocationFile);
    if (needsAllocation) {
      await feedSource(allocationFile, allocationUrl, id, drawverifyAllocationFeed, "placement");
      allocationInfo = JSON.parse(drawverifyAllocationInfo());
    }
    const result = JSON.parse(drawverify(JSON.stringify(bundle)));
    postMessage({ type: "result", id, result, poolInfo, allocationInfo });
  } catch (e) {
    postMessage({ type: "error", id, error: e.message || String(e) });
  }
};
