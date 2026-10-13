import assert from 'node:assert/strict';
import { test } from 'node:test';
import { preferredSubtitleLanguage, pickTrack } from '../src/playback/subtitles.ts';
const tracks = [{ index: 1, codec: 'srt', language: 'eng', forced: true },
  { index: 2, codec: 'srt', language: 'en' }, { index: 3, codec: 'srt', language: 'spa' }];
test('other audio compares the playing language and selects the chosen subtitle language', () => {
  for (const audio of ['ja', 'jpn', 'ES', 'es-MX']) {
    const language = preferredSubtitleLanguage(audio, 'en', 'other_audio', tracks, 'en');
    assert.equal(language, 'en');
    assert.equal(pickTrack(tracks, language!)?.index, 2);
  }
  assert.equal(preferredSubtitleLanguage('eng', 'ja', 'other_audio', tracks, 'es'), 'es');
  assert.equal(preferredSubtitleLanguage('jpn', 'ja', 'other_audio', tracks, 'es'), null);
});
test('unknown, off and missing subtitles do not activate captions', () => {
  for (const audio of ['en', 'eng', 'EN-us', '', 'und', undefined])
    assert.equal(preferredSubtitleLanguage(audio, 'en', 'other_audio', tracks, 'es'), null);
  assert.equal(preferredSubtitleLanguage('ja', 'en', 'off', tracks, 'en'), null);
  assert.equal(preferredSubtitleLanguage('ja', 'en', 'other_audio', [tracks[2]], 'en'), null);
  assert.equal(preferredSubtitleLanguage('ja', 'en', 'always', [], 'en'), null);
});
test('always on and audio transitions retain the requested mode', () => {
  assert.equal(preferredSubtitleLanguage('eng', 'en', 'always', tracks, 'es'), 'es');
  assert.deepEqual(['eng', 'jpn', 'eng'].map(audio => preferredSubtitleLanguage(audio, 'en', 'other_audio', tracks, 'en')), [null, 'en', null]);
});
