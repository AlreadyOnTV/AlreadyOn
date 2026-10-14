# Profile subtitles

WebUI, Android phone and Google TV Profile Settings show **Subtitle language**, then **Subtitles**:

- **Always on:** use available subtitles in the chosen language.
- **For other audio:** enable those subtitles when the playing audio differs from Preferred Audio Language.
- **Off:** do not enable subtitles automatically.

For example, preferred English audio and English subtitles enable captions for a Japanese track; selecting English audio turns automatic captions off. A profile preferring Japanese audio and Spanish subtitles instead enables Spanish captions for English audio. Available alternate audio tracks do not trigger captions unless selected.

Unknown audio language metadata and missing matching subtitle tracks leave playback uninterrupted. Full subtitle tracks are preferred over forced/signs-only tracks. Explicit player subtitle choices remain available; choosing an audio track in For other audio mode returns to automatic selection.

Audio and subtitle language selectors always list English first, then other languages alphabetically by displayed name. Reordering does not change saved language codes.

Settings are saved per profile, shared across devices and retained across sessions. Save Changes commits the selected language/mode; Cancel discards the draft.

## API compatibility

Profiles expose nullable `subtitleMode`: `always`, `other_audio` or `off`. Null preserves the existing `subtitlesEnabled` preference. Setting a mode updates that legacy field (true only for always); a legacy on/off patch clears the mode. Older apps retain their binary subtitle behavior, without gaining automatic mode. Unrelated profile patches preserve the saved mode. See [OpenAPI](../api/openapi.yaml).

Run `node --test web/tests/subtitlePreference.test.ts` for browser policy tests. Android unit tests exercise the shared phone/TV selection policy; profile/API tests use temporary databases.
