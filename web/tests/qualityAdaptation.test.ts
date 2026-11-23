// Adaptation-state tests use controlled timestamps instead of browser timing.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { AutoQualityAdapter, qualityRungHeight } from '../src/playback/qualityAdaptation.ts';

test('Auto waits for two low-buffer windows before stepping down one rung', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, bufferedSeconds: number) =>
    adapter.observe({ nowMs, bufferedSeconds, outputHeight: 1080 }, true);
  assert.equal(sample(0, 5), null);
  assert.equal(sample(5_000, 1), null);
  assert.equal(sample(10_000, 1), 720);
  assert.equal(adapter.decision?.reason, "low_buffer");
  assert.equal(adapter.decision?.bufferMs, 1000);
});

test('dropped frames trigger the same hysteresis', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, droppedVideoFrames: number) => adapter.observe({
    nowMs,
    bufferedSeconds: 5,
    outputHeight: 720,
    droppedVideoFrames,
    totalVideoFrames: nowMs / 33,
  }, true);
  assert.equal(sample(0, 0), null);
  assert.equal(sample(5_000, 20), null);
  assert.equal(sample(10_000, 40), 480);
  assert.equal(adapter.decision?.reason, "dropped_frames");
  assert.equal(adapter.decision?.droppedFrames, 20);
  assert.equal(sample(15_000, 60), null);
});

test('healthy windows reset a single bad window and explicit quality never adapts', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, bufferedSeconds: number, enabled = true) =>
    adapter.observe({ nowMs, bufferedSeconds, outputHeight: 1080 }, enabled);
  assert.equal(sample(0, 5), null);
  assert.equal(sample(5_000, 1), null);
  assert.equal(sample(10_000, 5), null);
  assert.equal(sample(15_000, 1, false), null);
  assert.equal(sample(20_000, 1, false), null);
});

test('six healthy samples permit one increase within the source ceiling', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, maxHeight: number) =>
    adapter.observe({ nowMs, bufferedSeconds: 5, outputHeight: 360, maxHeight }, true);
  for (let i = 1; i < 6; i++) assert.equal(sample(i * 5_000, 480), null);
  assert.equal(sample(30_000, 480), 480);
  for (let i = 7; i < 13; i++) assert.equal(sample(i * 5_000, 360), null);
});

test('admission pressure lets Auto yield headroom even with a healthy local buffer', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, outputHeight: number) => adapter.observe({
    nowMs, outputHeight, bufferedSeconds: 5, serverPressure: true,
  }, true);
  assert.equal(sample(5_000, 720), null);
  assert.equal(sample(10_000, 720), 480);
  for (let i = 3; i < 10; i++) assert.equal(sample(i * 5_000, 360), null);
});

test('normal segment variation within the live buffer target permits an upgrade', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, bufferedSeconds: number) =>
    adapter.observe({ nowMs, bufferedSeconds, outputHeight: 720, maxHeight: 1080 }, true);
  assert.equal(sample(5_000, 4.9), null);
  assert.equal(sample(10_000, 3.2), null);
  assert.equal(sample(15_000, 4.1), null);
  assert.equal(sample(20_000, 4.9), null);
  assert.equal(sample(25_000, 3.2), null);
  assert.equal(sample(30_000, 4.1), 1080);
});


test('a wide film steps up from its fitted quality rung', () => {
  const adapter = new AutoQualityAdapter();
  const outputHeight = qualityRungHeight(1280, 532);
  assert.equal(outputHeight, 720);
  assert.equal(qualityRungHeight(1920, 800), 1080);
  assert.equal(qualityRungHeight(854, 356), 480);
  const sample = (nowMs: number) => adapter.observe({ nowMs, bufferedSeconds: 4, outputHeight, maxHeight: 1080 }, true);
  for (let i = 1; i < 6; i++) assert.equal(sample(i * 5_000), null);
  assert.equal(sample(30_000), 1080);
});

test('the shared ceiling lets a lower viewer climb and holds both at the same rung', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, outputHeight: number, serverCeiling: number, bufferedSeconds = 5) =>
    adapter.observe({ nowMs, outputHeight, serverCeiling, bufferedSeconds }, true);
  assert.equal(sample(5_000, 1080, 720), null);
  assert.equal(sample(10_000, 1080, 720), 720);
  assert.equal(adapter.decision?.reason, "server_ceiling");
  assert.equal(adapter.decision?.serverCeiling, 720);
  adapter.reset();
  for (let i = 3; i < 14; i++) assert.equal(sample(i * 5_000, 480, 720), null);
  assert.equal(sample(70_000, 480, 720), 720);
  for (let i = 15; i <= 20; i++) assert.equal(sample(i * 5_000, 720, 720), null);
  assert.equal(sample(105_000, 720, 1080), 1080);
  assert.equal(sample(110_000, 360, 240, 1), null);
  assert.equal(sample(115_000, 360, 240, 1), null);
});

test('a steady shallow buffer holds quality without prompting an upgrade or downgrade', () => {
  const adapter = new AutoQualityAdapter();
  for (let i = 0; i < 12; i++) {
    assert.equal(adapter.observe({
      nowMs: i * 5_000, outputHeight: 720, bufferedSeconds: 2.5,
    }, true), null);
  }
});
