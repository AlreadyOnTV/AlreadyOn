import assert from 'node:assert/strict';
import { test } from 'node:test';
import { AutoQualityAdapter } from '../src/playback/qualityAdaptation.ts';

test('Auto waits for two low-buffer windows before stepping down one rung', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, bufferedSeconds: number) =>
    adapter.observe({ nowMs, bufferedSeconds, outputHeight: 1080 }, true);
  assert.equal(sample(0, 5), null);
  assert.equal(sample(10_000, 1), null);
  assert.equal(sample(20_000, 1), 720);
});

test('dropped frames trigger the same hysteresis and cooldown', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, droppedVideoFrames: number) => adapter.observe({
    nowMs,
    bufferedSeconds: 5,
    outputHeight: 720,
    droppedVideoFrames,
    totalVideoFrames: nowMs / 33,
  }, true);
  assert.equal(sample(0, 0), null);
  assert.equal(sample(10_000, 20), null);
  assert.equal(sample(20_000, 40), 480);
  assert.equal(sample(30_000, 60), null);
});

test('healthy windows reset a single bad window and explicit quality never adapts', () => {
  const adapter = new AutoQualityAdapter();
  const sample = (nowMs: number, bufferedSeconds: number, enabled = true) =>
    adapter.observe({ nowMs, bufferedSeconds, outputHeight: 1080 }, enabled);
  assert.equal(sample(0, 5), null);
  assert.equal(sample(10_000, 1), null);
  assert.equal(sample(20_000, 5), null);
  assert.equal(sample(30_000, 1, false), null);
  assert.equal(sample(40_000, 1, false), null);
});
