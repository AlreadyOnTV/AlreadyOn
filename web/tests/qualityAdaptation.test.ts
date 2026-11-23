// Adaptation-state tests use controlled timestamps instead of browser timing.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { AutoQualityAdapter } from '../src/playback/qualityAdaptation.ts';

test('Auto waits for two low-buffer windows before stepping down one rung', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, bufferedSeconds: number) =>
    adapter.observe({ nowMs, bufferedSeconds, outputHeight: 1080 }, true);
  assert.equal(sample(0, 5), null);
  assert.equal(sample(5_000, 1), null);
  assert.equal(sample(10_000, 1), 720);
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

test('three healthy samples permit one increase within the source ceiling', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, maxHeight: number) =>
    adapter.observe({ nowMs, bufferedSeconds: 5, outputHeight: 360, maxHeight }, true);
  for (let i = 1; i < 3; i++) assert.equal(sample(i * 5_000, 480), null);
  assert.equal(sample(15_000, 480), 480);
  for (let i = 4; i < 7; i++) assert.equal(sample(i * 5_000, 360), null);
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
