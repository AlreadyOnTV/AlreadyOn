// Storage fixtures model separate browser devices without touching real site data.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readVolume, writeVolume, VOLUME_KEY_PREFIX } from '../src/playback/volumePreference.ts';

function device(values = new Map<string, string>()) {
  const writes: [string, string][] = [];
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { writes.push([key, value]); values.set(key, value); },
  } });
  return { values, writes };
}

test('reads saved levels including zero without writing; defaults only missing/invalid levels', () => {
  const { values, writes } = device();
  assert.equal(readVolume(100), 1);
  const inputs = ['0', '0.5', '1', '-0.1', '1.1', 'null', 'true', '"0.5"', '[]', '{}', '', 'NaN', '1e999', 'x'.repeat(100)];
  inputs.forEach((raw, index) => {
    values.set(VOLUME_KEY_PREFIX + (101 + index), raw);
    assert.equal(readVolume(101 + index), index < 3 ? [0, 0.5, 1][index] : 1, raw);
  });
  assert.deepEqual(writes, []);
});

test('manual adjustments are scoped by globally unique profile ID and persist as numbers', () => {
  const { values, writes } = device();
  writeVolume(201, 0.5);
  writeVolume(202, 0.25);
  writeVolume(203, 0);
  assert.equal(readVolume(201), 0.5);
  assert.equal(readVolume(202), 0.25);
  assert.equal(readVolume(203), 0);
  assert.equal(readVolume(204), 1);
  assert.equal(values.get(VOLUME_KEY_PREFIX + 201), '0.5');
  assert.equal(writes.length, 3);
  for (const value of [-1, 2, NaN, Infinity]) writeVolume(201, value);
  assert.equal(readVolume(201), 0.5);
  assert.equal(writes.length, 3);
});

test('unavailable reads/writes preserve playback and the last manual choice for this visit', () => {
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, get() { throw new Error('storage disabled'); } });
  assert.equal(readVolume(301), 1);
  writeVolume(301, 0.35);
  writeVolume(302, 0);
  assert.equal(readVolume(301), 0.35);
  assert.equal(readVolume(302), 0);
  const { values } = device(new Map([[VOLUME_KEY_PREFIX + 301, '0.9']]));
  // A failed write must not restore an obsolete persisted value on profile switch.
  assert.equal(values.get(VOLUME_KEY_PREFIX + 301), '0.9');
  assert.equal(readVolume(301), 0.35);
});
