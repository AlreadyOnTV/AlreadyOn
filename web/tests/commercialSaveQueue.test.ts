// Request ordering and retry use controlled promises, independent of network timing.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { commercialSaveQueue } from '../src/pages/admin/commercialSaveQueue.ts';

const base = { enabled: true, mode: 'between-only' as const, intervalMinutes: 30, tvCommercialsPerBreak: 2, movieCommercialsPerBreak: 5, volumeOffsetDb: 0 };

test('unsent edits coalesce into one immutable latest snapshot', async () => {
  const calls: typeof base[] = [];
  const queue = commercialSaveQueue(async (settings) => { calls.push(settings as typeof base); }, () => {});
  queue.enqueue(base);
  const next = { ...base, tvCommercialsPerBreak: 4 };
  queue.enqueue(next);
  next.tvCommercialsPerBreak = 1;
  await queue.flush();
  assert.deepEqual(calls, [{ ...base, tvCommercialsPerBreak: 4 }]);
});

test('edits during an in-flight save are merged and serialized after it', async () => {
  const calls: typeof base[] = [];
  const releases: (() => void)[] = [];
  let started!: () => void;
  const secondStarted = new Promise<void>((resolve) => { started = resolve; });
  let active = 0, maximum = 0;
  const queue = commercialSaveQueue(async (settings) => {
    calls.push(settings as typeof base);
    active++; maximum = Math.max(maximum, active);
    const gate = new Promise<void>((resolve) => { releases.push(resolve); });
    if (calls.length === 2) started();
    await gate;
    active--;
  }, () => {});
  queue.enqueue(base);
  const finished = queue.flush();
  queue.enqueue({ ...base, tvCommercialsPerBreak: 3 });
  queue.enqueue({ ...base, tvCommercialsPerBreak: 4, movieCommercialsPerBreak: 1 });
  assert.equal(calls.length, 1);
  releases[0]();
  await secondStarted;
  assert.deepEqual(calls[1], { ...base, tvCommercialsPerBreak: 4, movieCommercialsPerBreak: 1 });
  releases[1]();
  await finished;
  assert.equal(maximum, 1);
});

test('a failed latest save reports an error and retries the latest desired value', async () => {
  let failed = true;
  const calls: typeof base[] = [], states: string[] = [];
  const queue = commercialSaveQueue(async (settings) => {
    calls.push(settings as typeof base);
    if (failed) throw new Error('Synthetic persistence failure');
  }, (state, error) => { states.push(error ?? state); });
  queue.enqueue({ ...base, volumeOffsetDb: -6 });
  await queue.flush();
  assert.equal(states.at(-1), 'Synthetic persistence failure');
  failed = false;
  await queue.retry();
  assert.equal(calls.length, 2);
  assert.equal(calls[1].volumeOffsetDb, -6);
  assert.equal(states.at(-1), 'saved');
});
