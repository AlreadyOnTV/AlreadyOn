# Product website

The [GitHub Pages website](https://alreadyonapp.github.io/AlreadyOn/) introduces AlreadyOn to homelab testers and provides installation, usage, hosting and public-feedback guides. Its source lives in `website/`. It is a companion to the application, with no accounts, analytics, remote fonts or application API access.

Run `python3 scripts/build-website.py` to produce `_site/`. The build checks local pages, asset references and anchor targets. Preview that generated folder with a temporary static HTTP server. Paths are relative so the site works under GitHub Pages’ `/AlreadyOn/` prefix. Homepage links use the directory URL with section fragments; old `index.html` bookmarks retain their fragments while the browser displays the clean URL. The build uses only Python’s standard library. Screenshot filenames include a content hash; replacing a capture changes its published URL, avoiding stale browser/CDN images. Both inline images and enlarged dialogs use the versioned URL.

The Pages workflow normally publishes website changes pushed to `main`; `workflow_dispatch` can publish an explicitly approved revision. The `github-pages` deployment environment must allow that branch. Website publication does not authorize application promotion, image publication or live-server deployment. The October 18 release branch is permitted for the maintainer-authorized website delivery while application promotion is held.

Screenshots are real WebUI and native Android renders with isolated synthetic demo media. See `website/assets/screenshots/README.md` for provenance. Capture replacements against disposable fixtures, never a persistent library or owner account. Website CSS supplies framing and subtle gloss; do not fabricate product controls or claim untested device acceptance.

Keep beginner guidance consistent with the deployment, TLS and Android references. Public support links point to `AlreadyOnApp/AlreadyOn`; internal development tickets remain in the designated development tracker. Check mobile layouts, keyboard navigation, tabs, screenshot dialogs, copy controls and external destinations before publication. Verify the deployed page and assets afterward.
