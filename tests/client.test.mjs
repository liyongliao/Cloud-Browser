import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import { normalizeServer, handoffURL } from "../extension/src/shared.ts";
test("extension validates server and encodes target only in fragment", () => {
  const target = "https://example.com/a?q=你好&next=https://other.com/#section";
  const u = new URL(handoffURL("https://cloud.example", target));
  assert.equal(u.search, "");
  assert.equal(
    new URLSearchParams(u.hash.slice(1)).get("open"),
    new URL(target).href,
  );
  assert.equal(
    normalizeServer("http://localhost:8080"),
    "http://localhost:8080",
  );
  for (const value of [
    "http://cloud.example",
    "https://user:pass@cloud.example",
    "https://cloud.example/nested",
    "https://cloud.example/?secret=1",
  ])
    assert.throws(() => normalizeServer(value));
  for (const target of [
    "file:///etc/passwd",
    "javascript:alert(1)",
    "chrome://settings",
    "https://user:pass@example.com",
  ])
    assert.throws(() => handoffURL("https://cloud.example", target));
});
function worklet(rate) {
  let instance;
  class Base {
    port = { onmessage: null };
  }
  vm.runInNewContext(readFileSync("web/public/audio-worklet.js", "utf8"), {
    AudioWorkletProcessor: Base,
    sampleRate: rate,
    registerProcessor: (_, C) => {
      instance = new C();
    },
  });
  return instance;
}
test("PCM worklet resamples without channel swaps or unbounded latency", () => {
  for (const rate of [44100, 48000, 96000]) {
    const p = worklet(rate);
    const frame = new Int16Array(3840);
    for (let i = 0; i < frame.length; i += 2) {
      frame[i] = 16384;
      frame[i + 1] = -16384;
    }
    for (let i = 0; i < 100; i++)
      p.port.onmessage({ data: frame.buffer.slice(0) });
    assert.ok(p.queued <= 12000);
    const out = [new Float32Array(128), new Float32Array(128)];
    p.process([], [out]);
    assert.ok(out[0].every((x) => Math.abs(x - 0.5) < 0.001));
    assert.ok(out[1].every((x) => Math.abs(x + 0.5) < 0.001));
    p.port.onmessage({ data: "reset" });
    const silence = [new Float32Array(128), new Float32Array(128)];
    p.process([], [silence]);
    assert.ok(silence[0].every((x) => x === 0));
  }
});
