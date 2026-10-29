// Every API request is intercepted; the preview only serves static application code.
const { chromium } = require("../../web/node_modules/playwright");
const assert = require("node:assert/strict");

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({
      ignoreHTTPSErrors: true,
      viewport: { width: 1280, height: 800 },
      timezoneId: "America/New_York",
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const start = Date.parse("2026-10-24T23:29:59-04:00");
    await page.clock.install({ time: start - 1000 });
    await page.clock.pauseAt(start);
    const profile = {
      id: 1,
      name: "Synthetic viewer",
      role: "admin",
      audienceTier: "adult",
      hasPin: false,
      guideTimeFormat: "24h",
      subtitleMode: "off",
      audioLanguage: "en",
      subtitleLanguage: "en",
      lastWatchedChannelId: 101,
    };
    const programs = Array.from({ length: 40 }, (_, i) => ({
      mediaFileId: i + 1,
      kind: "program",
      mediaType: "movie",
      title: `Synthetic airing ${i}`,
      startsAt: new Date(start - 29 * 60_000 + i * 25 * 60_000).toISOString(),
      endsAt: new Date(
        start - 29 * 60_000 + (i + 1) * 25 * 60_000,
      ).toISOString(),
    }));
    const lineup = Array.from({ length: 20 }, (_, i) => ({
      channelId: 101 + i,
      number: 101 + i,
      name: `Synthetic ${101 + i}`,
      programs,
    }));
    let guideReads = 0;
    let tuneRequests = 0;
    let freezeGuide = false;
    await page.route(
      (url) => url.pathname.startsWith("/api/"),
      async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path === "/api/auth/status")
          return route.fulfill({
            json: {
              setupRequired: false,
              authenticated: true,
              username: "synthetic",
              role: "owner",
            },
          });
        if (path === "/api/profiles") return route.fulfill({ json: [profile] });
        if (path === "/api/profiles/1") return route.fulfill({ json: profile });
        if (path === "/api/status/startup")
          return route.fulfill({
            json: { active: false, startupComplete: true },
          });
        if (path === "/api/playback/probe")
          return route.fulfill({
            status: 503,
            json: { error: "No media in guide fixture" },
          });
        if (
          path === "/api/playback/activity" &&
          route.request().method() === "DELETE"
        )
          return route.fulfill({ status: 204 });
        if (path === "/api/guide") {
          guideReads++;
          // An unavailable refresh must not freeze the clock or watched identity.
          if (freezeGuide)
            return route.fulfill({
              status: 503,
              json: { error: "Synthetic delayed guide" },
            });
          const now = await page.evaluate(() => Date.now());
          return route.fulfill({
            json: lineup.map((c) => ({
              ...c,
              programs: c.programs.filter((p) => Date.parse(p.endsAt) > now),
            })),
          });
        }
        if (
          path === "/api/playback/sessions" &&
          route.request().method() === "POST"
        ) {
          tuneRequests++;
          return route.fulfill({
            status: 503,
            json: { error: "No media needed for guide geometry fixture" },
          });
        }
        if (route.request().method() !== "GET") {
          errors.push(`Unexpected write: ${route.request().method()} ${path}`);
          return route.abort();
        }
        return route.fulfill({ json: {} });
      },
    );
    await page.goto((process.argv[2] || "https://localhost:5173") + "/");
    const tuned = page.locator(".guide-slot-box-tuned");
    await tuned.waitFor();
    const currentStart = () =>
      page.evaluate(() => Math.floor(Date.now() / 1_800_000) * 1_800_000);
    const assertLive = async (number) => {
      await page.waitForFunction((n) => {
        const cell = document.querySelector(".guide-slot-box-tuned");
        return (
          cell?.closest(".guide-row").querySelector(".guide-channel-num")
            .textContent === String(n) &&
          Date.parse(cell.dataset.startsAt) <= Date.now() &&
          Date.now() < Date.parse(cell.dataset.endsAt)
        );
      }, number);
      assert.equal(
        await page.locator(".guide-slot-box-tuned").count(),
        1,
        "Exactly one watched airing",
      );
      assert.equal(
        await tuned.evaluate((cell) => getComputedStyle(cell).outlineColor),
        "rgb(255, 32, 32)",
        "Red indicator follows watched airing",
      );
      assert.equal(
        Number(
          await page
            .locator(".guide-head-slot")
            .first()
            .getAttribute("data-start-ms"),
        ),
        await currentStart(),
      );
    };
    const assertRestored = async (number) => {
      await assertLive(number);
      await page.waitForFunction(() => {
        const scroll = document.querySelector(".guide-scroll");
        const cell = document.querySelector(".guide-slot-box-tuned");
        const row = cell.closest(".guide-row").getBoundingClientRect();
        const head = document
          .querySelector(".guide-head-row")
          .getBoundingClientRect();
        return scroll.scrollLeft === 0 && Math.abs(row.top - head.bottom) <= 1;
      });
      const geometry = await tuned.evaluate((cell) => {
        const scroll = document
          .querySelector(".guide-scroll")
          .getBoundingClientRect();
        const channel = cell
          .closest(".guide-row")
          .querySelector(".guide-col-channel")
          .getBoundingClientRect();
        const box = cell.getBoundingClientRect();
        return {
          visible: box.right > channel.right && box.left < scroll.right,
          focused: cell.classList.contains("guide-slot-box-focused"),
        };
      });
      assert(
        geometry.visible && geometry.focused,
        "Live overlap and focus restored together",
      );
    };
    const enter = async () => {
      // The fixture has no media session, so use the player's fullscreen shortcut.
      await page.keyboard.press("f");
      await page.waitForFunction(() => document.fullscreenElement !== null);
    };
    const changeChannels = async (key, count) => {
      for (let i = 0; i < count; i++) {
        const before = await tuned.evaluate((cell) =>
          Number(
            cell.closest(".guide-row").querySelector(".guide-channel-num")
              .textContent,
          ),
        );
        const next =
          101 + ((before - 101 + (key === "ArrowDown" ? 1 : 19)) % 20);
        await page.keyboard.press(key);
        await page.waitForFunction(
          (number) => document.title === `AlreadyOn - CH ${number}`,
          next,
        );
      }
    };
    const leave = async () => {
      // Deliberately leave both axes pointing at stale guide content while hidden.
      await page.evaluate(async () => {
        const scroll = document.querySelector(".guide-scroll");
        scroll.scrollTop = 0;
        scroll.scrollLeft = 800;
        await document.exitFullscreen();
      });
      await page.waitForFunction(() => document.fullscreenElement === null);
    };
    await assertRestored(101);
    const guideViewport = () =>
      page.evaluate(() => {
        const scroll = document.querySelector(".guide-scroll");
        return {
          left: scroll.scrollLeft,
          top: scroll.scrollTop,
          origin: document.querySelector(".guide-head-slot").dataset.startMs,
          selected: document.querySelector(".guide-slot-box-focused").dataset
            .startsAt,
        };
      });
    await page.keyboard.press("ArrowRight");
    const browsing = await guideViewport();
    const pausedReads = guideReads;
    await page.clock.runFor(90_000);
    assert.deepEqual(
      await guideViewport(),
      browsing,
      "Airing/timeline ticks preserve manual browsing",
    );
    assert.equal(
      guideReads,
      pausedReads,
      "Polling pauses during keyboard navigation",
    );
    await page.locator(".guide-scroll").dispatchEvent("wheel", { deltaY: 100 });
    await page.evaluate(() => {
      document.querySelector(".guide-scroll").scrollTop = 400;
    });
    const scrolled = await guideViewport();
    await page.clock.runFor(90_000);
    assert.deepEqual(
      await guideViewport(),
      scrolled,
      "Wheel input restarts the full inactivity window",
    );
    assert.equal(guideReads, pausedReads);
    await page.evaluate(() => {
      document.querySelector(".guide-scroll").scrollTop = 500;
    });
    await page.clock.runFor(29_999);
    assert.equal(
      guideReads,
      pausedReads,
      "No refresh before two minutes after wheel input",
    );
    await page.clock.runFor(1);
    await page.waitForFunction(
      () => document.querySelector(".guide-scroll").scrollTop === 0,
    );
    await assertRestored(101);
    assert.equal(
      guideReads,
      pausedReads + 1,
      "Programmatic scrolling does not extend the pause or queue catch-up polls",
    );
    await page.clock.runFor(60_000);
    assert.equal(
      guideReads,
      pausedReads + 2,
      "One-minute polling resumes after inactivity",
    );
    for (const [type, event] of [
      ["pointermove", { buttons: 1, pointerType: "touch" }],
      [
        "touchmove",
        { touches: [{ identifier: 1, clientX: 400, clientY: 500 }] },
      ],
    ]) {
      const beforeDrag = guideReads;
      await page.locator(".guide-scroll").dispatchEvent(type, event);
      await page.clock.runFor(119_999);
      assert.equal(guideReads, beforeDrag, `${type} pauses guide refresh`);
      await page.clock.runFor(1);
      await assertRestored(101);
      assert.equal(
        guideReads,
        beforeDrag + 1,
        `${type} pause expires exactly once`,
      );
    }
    await enter();
    await changeChannels("ArrowDown", 14);
    await leave();
    await assertRestored(115);
    await page.setViewportSize({ width: 1280, height: 480 });
    await page.keyboard.press("t");
    await page.waitForFunction(() =>
      document
        .querySelector(".guide-page")
        ?.classList.contains("is-large-player"),
    );
    const outerScroll = await page.evaluate(() => {
      const page = document.querySelector(".guide-page");
      return page.scrollHeight - page.clientHeight;
    });
    assert(
      outerScroll > 0,
      "Short large-player viewport should allow outer page scrolling",
    );
    await page.evaluate(() => {
      const page = document.querySelector(".guide-page");
      page.scrollTop = page.scrollHeight;
    });
    await page.keyboard.press("t");
    await page.waitForFunction(
      () =>
        !document
          .querySelector(".guide-page")
          ?.classList.contains("is-large-player"),
    );
    await page.waitForFunction(
      () => document.querySelector(".guide-page").scrollTop === 0,
    );
    // Responsive layouts can keep the same width, so transitionend is not
    // guaranteed. Wait for active width animations, then assert final geometry.
    await page.locator(".preview-video-wrap").evaluate((element) =>
      Promise.all(
        element
          .getAnimations()
          .filter((animation) => animation.transitionProperty === "width")
          .map((animation) => animation.finished.catch(() => {})),
      ),
    );
    await assertRestored(115);
    await page.setViewportSize({ width: 1280, height: 800 });
    await enter();
    await changeChannels("ArrowUp", 14);
    await leave();
    await assertRestored(101);
    await enter();
    await changeChannels("ArrowDown", 14);
    await assertLive(115);
    const initialReads = guideReads;
    await page.clock.fastForward(1000);
    await assertLive(115);
    freezeGuide = true;
    const beforeTime = await tuned.getAttribute("data-starts-at");
    const beforeTunes = tuneRequests;
    await page.clock.fastForward(3_600_000);
    await assertLive(115);
    assert.notEqual(
      await tuned.getAttribute("data-starts-at"),
      beforeTime,
      "Program identity advances even if old airing remains cached",
    );
    assert(guideReads > initialReads, "Guide refreshes during fullscreen");
    assert.equal(
      tuneRequests,
      beforeTunes,
      "Clock/airing changes do not retune playback",
    );
    await leave();
    await assertRestored(115);
    freezeGuide = false;
    await enter();
    await changeChannels("ArrowUp", 12);
    await page.clock.fastForward(1_800_000);
    await leave();
    await assertRestored(103);
    // Last row must also reach the top, not be clamped to the viewport bottom.
    await enter();
    await changeChannels("ArrowDown", 17);
    await leave();
    await assertRestored(120);
    // Ordinary guide navigation still reveals future programs and remote focus.
    await page.keyboard.press("ArrowRight");
    assert.equal(await page.locator(".guide-slot-box-focused").count(), 1);
    assert.equal(
      await tuned.evaluate((cell) => getComputedStyle(cell).outlineColor),
      "rgb(255, 32, 32)",
      "Browsing ahead retains red live indicator",
    );
    assert.equal(errors.length, 0, errors.join("\n"));
    if (process.argv[3]) await page.screenshot({ path: process.argv[3] });
    console.log(
      "PASS: CH101 → fullscreen → CH115 → one hour/midnight, lower channels, last-row top alignment, live overlap, cached program transition, polling and keyboard focus",
    );
    await context.close();
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
