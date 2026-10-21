# Handoff: issue #88 and alpha2

Snapshot: 2026-10-20. Continue in the primary checkout on `26.10.25`; PR #87 targets `dev`.

## Status

Issue #88's local implementation is complete. Existing negative-ID show parents are reconciled only when a TMDb ID or unique title/year identity confirms the match. Conflicting regions, ambiguous remakes and conflicting manual corrections remain separate. Reconciliation preserves episode/media IDs, episode overrides, show corrections and the stricter known certification. Repeated enrichment is idempotent; unmatched placeholders gain metadata in place. New scans attach to a managed local parent only when the local identity is unique.

The worktree is not yet committed or pushed. The full Go suite and `make lint` passed locally; web lint reports existing warnings. Hosted PR checks, merge, and alpha2 publication remain pending.

## Required delivery

1. Review the source diff, commit the implementation and tests on `26.10.25`, then push to update PR #87. Update its description with issue #88, final behavior, risks, and actual validation results.
2. Run the complete required PR workflow on the final combined head. Do not skip checks; investigate any recurrence of the documented fullscreen guide timeout.
3. Merge only after all required checks pass. Verify the dev-triggered publication produces `26.10.25-alpha2`; do not dispatch a duplicate candidate.
4. Verify source/tag provenance, image digest and attestations, signed phone/tablet and TV APKs, checksums, SBOMs, and the GitHub Release description. Use the required `Download Now`, `New Features`, `Bugfixes`, and `Known Issues` sections, with linked issue titles and explicit empty-section messages.
5. Keep #83 open. Reopen #85/#88 if GitHub auto-closes them on dev and set them to **In Review**. Do not promote to `test` or `main`, deploy to Catbug, or change live data; those actions are outside this authorization.
