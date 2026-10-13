// Circular navigation must preserve visible order and remain bounded at empty/single lists.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { circularChannelIndex } from '../src/playback/channelNavigation.ts';
const channels = [{ programs: [1] }, { programs: [] }, { programs: [2] }, { programs: [] }];
test('both directions wrap and skip empty rows', () => {
  assert.equal(circularChannelIndex(channels, 2, 1), 0);
  assert.equal(circularChannelIndex(channels, 0, -1), 2);
  assert.equal(circularChannelIndex(channels, 0, 1), 2);
  assert.equal(circularChannelIndex(channels, 2, -1), 0);
});
test('empty/single lists and stale selection are bounded without retuning', () => {
  assert.equal(circularChannelIndex([], 0, 1), null);
  assert.equal(circularChannelIndex([{programs:[]}], 0, -1), null);
  assert.equal(circularChannelIndex([{programs:[1]}], 0, 1), null);
  assert.equal(circularChannelIndex(channels, -1, 1), 0);
  assert.equal(circularChannelIndex(channels, -1, -1), 2);
});
test('repeated surfing preserves visible order over multiple loops', () => {
  let index = 0;
  const visited = [];
  for (let i=0; i<6; i++) { index = circularChannelIndex(channels, index, 1)!; visited.push(index); }
  assert.deepEqual(visited, [2,0,2,0,2,0]);
});
