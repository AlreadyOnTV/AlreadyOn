// Synthetic isolated integration. Run through playback.sh, never against a library.
const { chromium } = require("../../web/node_modules/playwright");
const fs = require("node:fs");
const cp = require("node:child_process");
const path = require("node:path");
const { isIPv4 } = require("node:net");
const root = process.env.PLAYBACK_TEST_ROOT;
const url = process.env.PLAYBACK_TEST_URL;
if (!root || !fs.existsSync(path.join(root, ".synthetic-test")))
  throw Error("Run playback.sh");
const account = { username: "e2e-admin", password: "e2e-password-1" };
// PLAYBACK_TEST_ONLY=cache,recovery,transitions,short,controls runs a subset while iterating.
const only = process.env.PLAYBACK_TEST_ONLY
  ? process.env.PLAYBACK_TEST_ONLY.split(",")
  : null;
const seed = (mode) =>
  cp.execFileSync("python3", [
    path.join(__dirname, "seed.py"),
    ...(mode ? [mode] : []),
  ]);
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const assert = (ok, message) => {
  if (!ok) throw Error(message);
};
const forceEncode = (page, maxHeight) =>
  page.route("**/api/channels/*/tune", (route) => {
    const body = route.request().postDataJSON();
    body.forceEncode = true;
    if (maxHeight) body.maxHeight = maxHeight;
    return route.continue({ postData: JSON.stringify(body) });
  });
const noWorkers = async (unreleased = () => 0) => {
  const cache = path.join(root, "data/transcode");
  const started = Date.now();
  for (;;) {
    await delay(200);
    const children = cp.spawnSync(
      "pgrep",
      ["-a", "-P", process.env.PLAYBACK_TEST_SERVER_PID],
      { encoding: "utf8" },
    );
    const output = !fs.existsSync(cache)
      ? []
      : fs.readdirSync(cache).filter((name) => name.startsWith("stream-"));
    if (children.status === 1 && !output.length) {
      if (process.env.PLAYBACK_TEST_DEBUG)
        console.log(
          `cleanup after ${Date.now() - started}ms; unreleased grants ${unreleased()}`,
        );
      return;
    }
    if (Date.now() - started > (process.env.PLAYBACK_TEST_DEBUG ? 40000 : 1200))
      throw Error(
        `Worker/output remained ${Date.now() - started}ms after player unmount (${unreleased()} unreleased grants): ${children.stdout
          .split("\n")
          .map((l) => l.replace(/token=[^&\s]+/g, "token=…").slice(0, 160))
          .join(" | ")} ${output}`,
      );
  }
};
(async () => {
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(url + "/healthz")).ok) break;
    } catch {}
    await delay(100);
  }
  // Startup scheduler has no fixture folders yet. Wait for its completion before seeding.
  for (let i = 0; i < 100; i++) {
    if (
      fs
        .readFileSync(path.join(root, "server.log"), "utf8")
        .includes("library pipeline finished")
    )
      break;
    await delay(100);
  }
  seed();
  const browser = await chromium.launch({
    headless: true,
    args: ["--autoplay-policy=document-user-activation-required"],
  });
  const errors = [];
  // The first browser creates the account through the setup dialog; later ones sign in.
  const login = async (page) => {
    await page.goto(url);
    const guide = page.locator(".guide-slot-box").first();
    const dialog = page.getByRole("dialog");
    await guide.or(dialog).waitFor();
    if (await dialog.count()) {
      // Sign-in uses the brand rather than a heading; distinguish dialogs by their accessible name.
      const setup =
        (await dialog.getAttribute("aria-label")) ===
        "Create Your AlreadyOn Account";
      const username = dialog.getByLabel("Username");
      const password = dialog.getByLabel("Password", { exact: true });
      assert(
        (await username.getAttribute("name")) === "username" &&
          (await username.getAttribute("autocomplete")) ===
            (setup ? "off" : "username"),
        "Only the actual login username exposes password-manager metadata",
      );
      assert(
        (await password.getAttribute("name")) === "password" &&
          (await password.getAttribute("autocomplete")) ===
            (setup ? "off" : "current-password"),
        "Login password exposes the correct password-manager metadata",
      );
      const visibility = async (field, value) => {
        await field.fill(value);
        const toggle = field.locator("..").getByRole("button");
        assert(
          (await field.getAttribute("type")) === "password" &&
            (await toggle.getAttribute("aria-pressed")) === "false",
          "Passwords start obscured",
        );
        await toggle.click();
        assert(
          (await field.getAttribute("type")) === "text" &&
            (await field.inputValue()) === value &&
            (await toggle.getAttribute("aria-pressed")) === "true",
          "Show Password reveals the unchanged value",
        );
        await toggle.click();
        assert(
          (await field.getAttribute("type")) === "password" &&
            (await field.inputValue()) === value &&
            (await toggle.getAttribute("aria-pressed")) === "false",
          "Hide Password obscures the unchanged value",
        );
      };
      await username.fill(account.username);
      await visibility(password, account.password);
      if (setup) {
        const name = dialog.getByLabel("Server Name", { exact: true });
        await name.fill("AlreadyOn E2E Library");
        const confirmation = dialog.getByLabel("Confirm password");
        assert(
          (await confirmation.getAttribute("name")) === "confirmPassword" &&
            (await confirmation.getAttribute("autocomplete")) === "off",
          "Account setup confirmation suppresses password-manager autofill",
        );
        await visibility(confirmation, account.password);
      }
      await dialog
        .getByRole("button", {
          name: setup ? "Create account and sign in" : "Sign in",
        })
        .click();
    }
    await guide.waitFor();
  };
  const playing = (page) =>
    page.waitForFunction(
      () => {
        const v = document.querySelector("video");
        return v?.currentTime > 1 && !v.paused && v.readyState >= 2;
      },
      {},
      { timeout: 25000 },
    );
  // Every channel authorization the page obtains must be explicitly revoked.
  const grants = new Map();
  const trackGrants = (page) => {
    page.on("response", async (r) => {
      if (
        r.request().method() === "POST" &&
        /\/api\/playback\/sessions$/.test(r.url()) &&
        r.status() === 201
      ) {
        const s = await r.json().catch(() => null);
        if (s) grants.set(s.token, false);
      }
    });
    page.on("request", (r) => {
      if (
        r.method() === "DELETE" &&
        /\/api\/playback\/sessions$/.test(r.url())
      ) {
        const s = r.postDataJSON();
        if (grants.has(s?.token)) grants.set(s.token, true);
      }
    });
  };
  const unreleased = () =>
    [...grants.values()].filter((released) => !released).length;
  const leave = async (page) => {
    await page.evaluate(() => {
      history.pushState({}, "", "/admin/users");
      dispatchEvent(new PopStateEvent("popstate"));
    });
    await page.waitForSelector("video", { state: "detached" });
  };
  // These layout scenarios create many browsers under one synthetic principal.
  // Wait for grant revocation before closing its transport, not just DOM removal.
  const leavePlaying = async (page) => {
    const released = page.waitForResponse((response) => response.ok() &&
      response.request().method() === "DELETE" &&
      /\/api\/playback\/sessions$/.test(response.url()));
    await leave(page);
    await released;
  };
  const watch = async (index, history) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    // One direct-capable browser and one forced conversion also prove transport
    // differences follow the same channel clock.
    if (index === 1) await forceEncode(page);
    trackGrants(page);
    page.on("response", async (r) => {
      if (/\/tune$/.test(r.url()) && r.status() === 201) {
        const s = await r.json();
        history[index].push({
          kind: s.kind,
          media: s.mediaFileId,
          offset: s.sourceOffsetSeconds,
          stream: s.streamOffsetSeconds,
          mode: s.plan.mode,
        });
      }
    });
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    return page;
  };

  async function audioTracksAndCommercialVolume() {
    seed("audio");
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    trackGrants(page);
    const sessions = [];
    page.on("response", async (r) => {
      if (/\/tune$/.test(r.url()) && r.status() === 201)
        sessions.push(await r.json());
    });
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    assert(
      sessions.at(-1).plan.audioTrackIndex === 2,
      "English must override the Japanese default track",
    );
    assert(
      sessions.at(-1).audioTracks.length === 3,
      "Menu must expose same-language alternate mixes",
    );
    const player = page.locator(".playback-player");
    await player.hover();
    await player.getByRole("button", { name: "Settings", exact: true }).click();
    await page
      .getByRole("menuitem", { name: "Audio Tracks (eng)", exact: true })
      .waitFor();
    await page.getByRole("menuitem", { name: /^Audio Tracks/ }).click();
    await page.getByRole("menuitemradio", { name: /Japanese/ }).click();
    for (let i = 0; i < 100 && sessions.at(-1)?.plan.audioTrackIndex !== 1; i++)
      await delay(100);
    assert(
      sessions.at(-1).plan.audioTrackIndex === 1,
      "Japanese track selection did not retune",
    );
    await playing(page);
    await player.hover();
    await player.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("menuitem", { name: /^Audio Tracks/ }).click();
    await page.getByRole("menuitemradio", { name: /Commentary/ }).click();
    for (let i = 0; i < 100 && sessions.at(-1)?.plan.audioTrackIndex !== 3; i++)
      await delay(100);
    assert(
      sessions.at(-1).plan.audioTrackIndex === 3,
      "Same-language commentary was not selected",
    );
    await page.goto(url + "/profile");
    assert(
      (await page.getByLabel("Preferred Audio Language").inputValue()) === "en",
      "Profile default must be English",
    );
    await page.getByLabel("Preferred Audio Language").selectOption("ja");
    await page
      .getByRole("status", { name: "audioLanguage saved", exact: true })
      .waitFor();
    await page.getByRole("button", { name: /Back To Guide/ }).click();
    await playing(page);
    assert(
      sessions.at(-1).plan.audioTrackIndex === 1,
      "Saved profile preference must select Japanese",
    );
    await page.goto(url + "/admin/commercials");
    await page.getByLabel("Commercial Volume Offset (dB)").fill("-6");
    await Promise.all([
      page.waitForEvent("load"),
      page.getByRole("button", { name: "Save Changes" }).click(),
    ]);
    await page.getByLabel("Commercial Volume Offset (dB)").waitFor();
    assert(
      (await page.getByLabel("Commercial Volume Offset (dB)").inputValue()) ===
        "-6",
      "Commercial offset did not persist",
    );
    assert(
      (await page
        .locator(".commercial-settings")
        .locator(".admin-field-row label")
        .last()
        .textContent()) === "Commercial Volume Offset (dB)",
      "Commercial offset should be below the break counts",
    );
    seed("audio-commercial");
    await page.goto(url);
    await page.locator(".guide-slot-box").first().click();
    await playing(page).catch(async (e) => {
      console.error(
        "Commercial playback state:",
        JSON.stringify(
          sessions
            .slice(-3)
            .map((s) => ({ kind: s.kind, plan: s.plan, ready: s.ready })),
        ),
      );
      console.error(await page.locator(".playback-player").innerText());
      throw e;
    });
    assert(
      sessions.at(-1).kind === "commercial" &&
        sessions.at(-1).plan.volumeOffsetDb === -6 &&
        !sessions.at(-1).plan.audioCopy,
      "Commercial offset must be applied server-side",
    );
    seed("audio");
    await page.reload();
    await playing(page);
    assert(
      !sessions.at(-1).plan.volumeOffsetDb,
      "Commercial offset leaked onto regular content",
    );
    // Missing language falls back to the default audio, never silence or a subtitle index.
    await page.goto(url + "/profile");
    await page.getByLabel("Preferred Audio Language").selectOption("fr");
    await page
      .getByRole("status", { name: "audioLanguage saved", exact: true })
      .waitFor();
    await page.getByRole("button", { name: /Back To Guide/ }).click();
    await playing(page);
    assert(
      sessions.at(-1).plan.audioTrackIndex === 1,
      "Unavailable language should use the default track",
    );
    await page.goto(url + "/profile");
    await page.getByLabel("Preferred Audio Language").selectOption("en");
    await page
      .getByRole("status", { name: "audioLanguage saved", exact: true })
      .waitFor();
    await page.getByRole("button", { name: /Back To Guide/ }).click();
    await playing(page);
    await leave(page);
    await noWorkers();
    await context.close();
    console.log(
      "PASS: audio language defaults, explicit tracks, same-language commentary, and commercial-only volume offset",
    );
  }

  async function singleSlotChanges() {
    seed("audio");
    const admin = await browser.newContext();
    const setup = await admin.newPage();
    await login(setup);
    await leave(setup);
    await noWorkers();
    const settings = await (
      await admin.request.get(url + "/api/settings/playback")
    ).json();
    assert(
      (
        await admin.request.put(url + "/api/settings/playback", {
          data: { ...settings, autoProcesses: false, maxProcesses: 1 },
        })
      ).ok(),
      "Set one conversion slot",
    );
    let maxWorkers = 0;
    const sample = setInterval(() => {
      const children = cp.spawnSync(
        "pgrep",
        ["-a", "-P", process.env.PLAYBACK_TEST_SERVER_PID],
        { encoding: "utf8" },
      ).stdout;
      maxWorkers = Math.max(
        maxWorkers,
        children.split("\n").filter((s) => /ffmpeg/.test(s)).length,
      );
    }, 100);
    try {
      const commercials = await (
        await admin.request.get(url + "/api/settings/commercials")
      ).json();
      assert(
        (
          await admin.request.put(url + "/api/settings/commercials", {
            data: { ...commercials, volumeOffsetDb: 0 },
          })
        ).ok(),
        "Reset offset so the repeated save changes a value",
      );
      await audioTracksAndCommercialVolume();
      seed("quality");
      const qualityContext = await browser.newContext();
      const qualityPage = await qualityContext.newPage();
      await login(qualityPage);
      await playing(qualityPage);
      for (const height of [480, 360]) {
        await qualityPage.locator(".playback-player").hover();
        await qualityPage
          .getByRole("button", { name: "Settings", exact: true })
          .click();
        await qualityPage.getByRole("menuitem", { name: /^Quality/ }).click();
        await qualityPage
          .getByRole("menuitemradio", { name: `${height}p`, exact: true })
          .click();
        await qualityPage.waitForFunction(
          (h) => {
            const v = document.querySelector("video");
            return v?.videoHeight === h && !v.paused && v.readyState >= 2;
          },
          height,
          { timeout: 20000 },
        );
      }
      await leave(qualityPage);
      await noWorkers();
      await qualityContext.close();
      seed("audio");
      const context = await browser.newContext();
      const page = await context.newPage();
      trackGrants(page);
      let latest,
        busy = 0,
        tunes = 0;
      page.on("request", (r) => {
        if (/\/tune$/.test(r.url())) tunes++;
      });
      page.on("response", async (r) => {
        if (/\/tune$/.test(r.url())) {
          const data = await r.json().catch(() => ({}));
          if (r.status() === 201) latest = data;
          if (data.code === "stream_capacity") busy++;
        }
      });
      await login(page);
      await playing(page);
      const choose = async (name) => {
        await page.locator(".playback-player").hover();
        await page
          .getByRole("button", { name: "Settings", exact: true })
          .click();
        await page.getByRole("menuitem", { name: /^Audio Tracks/ }).click();
        await page.getByRole("menuitemradio", { name }).click();
      };
      await page.locator(".playback-player").hover();
      await page.getByRole("button", { name: "Pause", exact: true }).click();
      await choose(/Japanese/);
      for (let i = 0; i < 150 && latest?.plan.audioTrackIndex !== 1; i++)
        await delay(100);
      assert(
        latest?.plan.audioTrackIndex === 1 && busy > 0,
        "Paused replacement must recover from real capacity rejection",
      );
      await page.waitForFunction(() => {
        const v = document.querySelector("video");
        return v?.paused && v.readyState >= 2;
      });
      await page.locator(".playback-player").hover();
      await page.getByRole("button", { name: "Play", exact: true }).click();
      await playing(page);
      // A second viewer owns the same Japanese rendition; releasing this viewer
      // must not kill the shared conversion or over-admit a replacement.
      const other = await browser.newContext();
      const survivor = await other.newPage();
      await survivor.route("**/api/channels/*/tune", (route) =>
        route.continue({
          postData: JSON.stringify({
            ...route.request().postDataJSON(),
            audioLanguage: "ja",
            audioTrackIndex: 1,
            audioMediaFileId: 993,
          }),
        }),
      );
      await login(survivor);
      await playing(survivor);
      await choose(/Commentary/);
      await page
        .getByRole("alert")
        .filter({
          hasText: "No conversion slot is available. Try playback again.",
        })
        .waitFor({ timeout: 20000 });
      const attempts = tunes,
        before = await survivor.locator("video").evaluate((v) => v.currentTime);
      await delay(2000);
      assert(tunes === attempts, "A blocked replacement must stop retrying");
      assert(
        (await survivor
          .locator("video")
          .evaluate((v) => !v.paused && v.currentTime)) >
          before + 1,
        "The other viewer must keep playing",
      );
      await context.close();
      await leave(survivor);
      await noWorkers();
      await other.close();
      assert(maxWorkers <= 1, `Exceeded the one-process limit: ${maxWorkers}`);
      console.log(
        "PASS: one-slot audio replacement, paused state, shared-viewer isolation, bounded retries and process limit",
      );
    } finally {
      clearInterval(sample);
      await admin.request.put(url + "/api/settings/playback", {
        data: settings,
      });
      await admin.close();
    }
  }

  async function cacheAndSwitching() {
    const context = await browser.newContext();
    await context.addInitScript(() => {
      window.capabilityCalls = 0;
      if (navigator.mediaCapabilities) {
        const original = navigator.mediaCapabilities.decodingInfo.bind(
          navigator.mediaCapabilities,
        );
        navigator.mediaCapabilities.decodingInfo = (config) => {
          window.capabilityCalls++;
          return original(config);
        };
      }
    });
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    let probes = 0;
    page.on("request", (r) => {
      if (r.url().includes("/probe?")) probes++;
    });
    let probeSessions = 0;
    page.on("request", (r) => {
      if (r.method() === "POST" && r.url().endsWith("/api/playback/probe"))
        probeSessions++;
    });
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    assert(
      (await page.locator(".playback-player").getAttribute("data-mode")) ===
        "Direct Play",
      "Compatible fixture must direct play",
    );
    const initialProbes = probes;
    assert(initialProbes === 7, "Expected bounded startup network measurement");
    assert(
      (await page.evaluate(() => window.capabilityCalls)) > 0,
      "No startup device discovery",
    );
    await page.reload();
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    assert(probes === initialProbes, "Fresh cache repeated network test");
    assert(
      (await page.evaluate(() => window.capabilityCalls)) === 0,
      "Fresh cache repeated codec probes",
    );
    await page.evaluate(() => {
      const k = "alreadyon.playbackCapabilities";
      const s = JSON.parse(localStorage.getItem(k));
      s.capturedAt -= 86400001;
      localStorage.setItem(k, JSON.stringify(s));
    });
    await page.reload();
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    assert(
      probes === initialProbes + 7,
      "Expired cache did not refresh network",
    );
    assert(
      (await page.evaluate(() => window.capabilityCalls)) > 0,
      "Expired cache did not refresh codecs",
    );
    console.log("PASS: direct play, cache reuse and 24-hour expiry");
    await forceEncode(page);
    const sessionsBeforeSwitching = probeSessions;
    for (let i = 0; i < 24; i++) {
      await page
        .locator(".guide-row")
        .nth((i + 1) % 2)
        .locator(".guide-slot-box")
        .first()
        .click();
      await delay(i % 3 ? 30 : 220);
    }
    await playing(page);
    await leave(page);
    assert(
      probeSessions === sessionsBeforeSwitching,
      "Channel switches re-requested a network probe session",
    );
    await noWorkers();
    await context.close();
    console.log(
      "PASS: rapid channel switching reaps workers, and a fresh device cache is reused without new probe requests",
    );
  }

  async function transitions() {
    seed("transitions");
    const history = [[], []];
    const a = await watch(0, history);
    await delay(5000);
    const b = await watch(1, history);
    const sample = (page) =>
      page.evaluate(() => {
        const v = document.querySelector("video");
        return {
          kind: v.dataset.kind,
          time: Number(v.dataset.scheduleTime),
          position: v.currentTime,
          startup: v.dataset.firstPictureMs,
          playing: !v.paused && v.readyState >= 2,
          width: v.videoWidth,
          error: document.querySelector("[role=alert]")?.textContent,
        };
      });
    const seen = new Set();
    let maxSkew = 0;
    let retainedActivity = null;
    let activityFrozen = false;
    const deadline = Date.now() + 39000;
    while (Date.now() < deadline) {
      const [x, y] = await Promise.all([sample(a), sample(b)]);
      assert(!x.error && !y.error, JSON.stringify({ x, y }));
      if (x.playing && y.playing && x.kind === y.kind) {
        const skew = Math.abs(x.time - y.time) / 1000;
        maxSkew = Math.max(maxSkew, skew);
        assert(
          skew <= 2.5,
          "Schedule skew: " + JSON.stringify({ x, y, skew, history }),
        );
        const state =
          x.kind === "commercial"
            ? "commercial"
            : seen.has("commercial")
              ? "resumed"
              : "program";
        seen.add(state);
        assert(
          x.width === (state === "commercial" ? 640 : 320) &&
            x.width === y.width,
          "Wrong decoded resolution",
        );
        if (state === "commercial") {
          const rows = await a.evaluate(
            async () =>
              (await (await fetch("/api/server/activity")).json()).sessions,
          );
          assert(
            rows.length <= 2,
            "Prefetched commercials must not add viewer rows",
          );
          if (rows.length === 2 && rows.every((s) => s.commercial)) {
            const retained = rows.map((s) => ({
              id: s.id,
              video: s.video,
              bandwidth: s.bandwidthMbps,
              latency: s.latencyMs,
              updated: s.updatedAt,
            }));
            assert(
              retained.every((s) => s.video === "program.mp4"),
              "Commercial replaced the program title",
            );
            if (retainedActivity) {
              assert(
                JSON.stringify(retained) === retainedActivity,
                "Program readings changed during the commercial",
              );
              activityFrozen = true;
            }
            retainedActivity = JSON.stringify(retained);
          }
        }
        if (state === "resumed") {
          await delay(2000);
          break;
        }
      }
      await delay(200);
    }
    assert(activityFrozen, "Did not observe frozen commercial activity");
    assert(
      seen.has("resumed"),
      "Transition did not finish: " +
        JSON.stringify({ seen: [...seen], history }),
    );
    assert(
      history.every(
        (h) =>
          h.some((s) => s.kind === "commercial") &&
          h.some((s) => s.offset === 38),
      ),
      "Program source offset was not resumed",
    );
    await leave(a);
    await leave(b);
    await noWorkers(unreleased);
    await a.context().close();
    await b.context().close();
    console.log(
      "PASS: two browsers through mismatched program/commercial/program; maximum diagnostic skew " +
        maxSkew.toFixed(3) +
        "s",
    );
  }

  // Two ads shorter than the presentation delay between a program and its
  // resumed half. Evidence comes from decoded pixels: ad colors and the
  // program's source-second blocks, not the player's own schedule diagnostics.
  async function shortEntries() {
    seed("short");
    const started = Date.now();
    const history = [[], []];
    const pages = await Promise.all([watch(0, history), watch(1, history)]);
    const frame = (page) =>
      page.evaluate(() => {
        const v = document.querySelector("video");
        const out = {
          playing: !v.paused && v.readyState >= 3,
          width: v.videoWidth,
          error: document.querySelector("[role=alert]")?.textContent,
        };
        if (v.videoWidth && v.readyState >= 2) {
          const c = (window.probeCanvas ??= document.createElement("canvas"));
          c.width = v.videoWidth;
          c.height = v.videoHeight;
          const g = c.getContext("2d", { willReadFrequently: true });
          g.drawImage(v, 0, 0);
          const px = (x, y) => [...g.getImageData(x, y, 1, 1).data.slice(0, 3)];
          const digit = (r) => Math.round((r / 1.164 + 16 - 20) / 18);
          if (v.videoWidth === 320)
            out.second = digit(px(10, 10)[0]) * 12 + digit(px(30, 10)[0]);
          else {
            const [r, gr, b] = px(v.videoWidth >> 1, v.videoHeight >> 1);
            out.color =
              r > 150 && gr < 100
                ? "red"
                : gr > 100 && r < 100
                  ? "green"
                  : b > 150
                    ? "blue"
                    : "unknown";
          }
        }
        return out;
      });
    const runs = [[], []];
    const stalls = [0, 0];
    const stalledSince = [null, null];
    let decodedSkew = 0;
    const label = (i, f) =>
      f.second != null
        ? runs[i].some((r) => r.label === "red" || r.label === "green")
          ? "resumed"
          : "program"
        : f.color;
    const deadline = Date.now() + 40000;
    while (
      Date.now() < deadline &&
      !runs.every(
        (r) => r.at(-1)?.label === "resumed" && r.at(-1).seconds.length >= 8,
      )
    ) {
      const now = Date.now();
      const frames = await Promise.all(pages.map(frame));
      frames.forEach((f, i) => {
        assert(!f.error, JSON.stringify({ i, f, history: history[i] }));
        if (!f.playing) {
          stalledSince[i] ??= now;
          return;
        }
        if (stalledSince[i] != null && runs[i].length)
          stalls[i] = Math.max(stalls[i], now - stalledSince[i]);
        stalledSince[i] = null;
        const l = label(i, f);
        if (!l) return;
        if (runs[i].at(-1)?.label !== l)
          runs[i].push({ label: l, seconds: [], at: now - started });
        if (f.second != null) runs[i].at(-1).seconds.push(f.second);
      });
      if (
        frames.every((f) => f.playing && f.second != null) &&
        label(0, frames[0]) === label(1, frames[1])
      )
        decodedSkew = Math.max(
          decodedSkew,
          Math.abs(frames[0].second - frames[1].second),
        );
      await delay(150);
    }
    const median = (values) =>
      [...values].sort((x, y) => x - y)[values.length >> 1];
    const evidence = JSON.stringify({
      runs: runs.map((r) =>
        r.map(({ label, seconds, at }) => ({
          label,
          at,
          first: seconds.slice(0, 3),
          last: seconds.slice(-3),
        })),
      ),
      history,
    });
    runs.forEach((r, i) => {
      // Brief undecodable frames during a handoff are tolerated only if the
      // following content is the expected next entry.
      const order = r.map((x) => x.label).filter((l) => l !== "unknown");
      assert(
        JSON.stringify(order) ===
          JSON.stringify(["program", "red", "green", "resumed"]),
        `Context ${i} decoded wrong entry order: ${evidence}`,
      );
      const before = r.find((x) => x.label === "program").seconds,
        after = r.find((x) => x.label === "resumed").seconds;
      assert(
        Math.max(...before) <= 36 && Math.max(...before) >= 34,
        `Context ${i} left the program early or late: ${evidence}`,
      );
      const resumed = median(after.slice(0, 3));
      assert(
        resumed >= 36 && resumed <= 37,
        `Context ${i} resumed at source second ${resumed}, want 36-37 (replayed or skipped content): ${evidence}`,
      );
    });
    assert(
      decodedSkew <= 3,
      `Decoded skew ${decodedSkew}s exceeds 2.5s tolerance plus one-second quantization: ${evidence}`,
    );
    for (const page of pages) await leave(page);
    await noWorkers(unreleased);
    for (const page of pages) await page.context().close();
    console.log(
      `PASS: two browsers decoded program/2s ad/2s ad/program in order; resumed at source ${runs.map((r) => median(r.find((x) => x.label === "resumed").seconds.slice(0, 3))).join(",")}s; decoded skew <= ${decodedSkew}s; longest handoff stall ${(Math.max(...stalls) / 1000).toFixed(1)}s`,
    );
  }

  const instrumented = async () => {
    const context = await browser.newContext();
    await context.addInitScript(() => {
      window.capabilityCalls = 0;
      const original = navigator.mediaCapabilities.decodingInfo.bind(
        navigator.mediaCapabilities,
      );
      navigator.mediaCapabilities.decodingInfo = (config) => {
        window.capabilityCalls++;
        return original(config);
      };
    });
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    trackGrants(page);
    const counts = { probes: 0 };
    page.on("request", (r) => {
      if (r.url().includes("/probe?")) counts.probes++;
    });
    return { context, page, counts };
  };
  const tune = async (page) => {
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
  };

  // #127: cache damage, denied storage, and forced conversion that must not
  // outlive the entry that failed.
  async function recovery() {
    seed();
    const { context, page, counts } = await instrumented();
    await login(page);
    await tune(page);
    await page.evaluate(() => {
      const k = "alreadyon.playbackCapabilities";
      const s = JSON.parse(localStorage.getItem(k));
      s.network = { origin: 5 };
      localStorage.setItem(k, JSON.stringify(s));
    });
    counts.probes = 0;
    await page.reload();
    await login(page);
    await tune(page);
    assert(
      counts.probes === 7,
      `Damaged network entry was not remeasured (${counts.probes} probes)`,
    );
    assert(
      (await page.evaluate(() => window.capabilityCalls)) === 0,
      "Damaged network entry repeated codec discovery",
    );
    await leave(page);
    await noWorkers(unreleased);
    await context.close();

    const denied = await browser.newContext();
    await denied.addInitScript(() => {
      Object.defineProperty(window, "localStorage", {
        get() {
          throw new DOMException("denied", "SecurityError");
        },
      });
    });
    const blocked = await denied.newPage();
    blocked.on("pageerror", (e) => errors.push(e.message));
    trackGrants(blocked);
    await login(blocked);
    await tune(blocked);
    await leave(blocked);
    await noWorkers(unreleased);
    await denied.close();
    console.log(
      "PASS: damaged network cache remeasures without codec discovery; denied storage still plays",
    );

    seed("split");
    const history = [];
    const { context: scoped, page: forced } = await instrumented();
    let broken = false;
    // The first direct file is undecodable: its entry falls back to conversion.
    await forced.route("**/media/*/source?*", (route) => {
      if (broken) return route.continue();
      broken = true;
      return route.fulfill({
        status: 200,
        contentType: "video/mp4",
        headers: { "Access-Control-Allow-Origin": "*" },
        body: "not a video",
      });
    });
    forced.on("response", async (r) => {
      if (/\/tune$/.test(r.url()) && r.status() === 201) {
        const s = await r.json();
        history.push({ offset: s.sourceOffsetSeconds, mode: s.plan.mode });
      }
    });
    await login(forced);
    await tune(forced);
    await forced
      .waitForFunction(
        () =>
          document.querySelector(".playback-player")?.dataset.mode ===
            "Direct Play" &&
          Number(document.querySelector("video")?.dataset.mediaId) === 999 &&
          document.querySelector("video").currentTime > 1,
        {},
        { timeout: 30000 },
      )
      .catch(() => {});
    const evidence = JSON.stringify(history);
    assert(
      history[0]?.mode === "direct" &&
        history[1]?.mode !== "direct" &&
        history[1]?.offset === 0,
      `First entry did not fall back to conversion: ${evidence}`,
    );
    assert(
      history.some((h) => h.offset === 34 && h.mode === "direct"),
      `Forced conversion outlived the failing entry: ${evidence}`,
    );
    await leave(forced);
    await noWorkers(unreleased);
    await scoped.close();
    console.log(
      "PASS: forced conversion is limited to the entry that failed " + evidence,
    );
  }

  // Player controls: real episode length, a pause that sticks while new segments
  // arrive, arrow keys that still drive the guide, and subtitle language choice.
  async function controls() {
    seed();
    const context = await browser.newContext();
    await context.addInitScript(() => {
      window.__wakeLocks = { acquired: 0, held: 0 };
      Object.defineProperty(navigator, "wakeLock", {
        configurable: true,
        value: {
          request: async (kind) => {
            if (kind !== "screen") throw new Error("Unexpected wake lock type");
            const lock = new EventTarget();
            lock.released = false;
            window.__wakeLocks.acquired++;
            window.__wakeLocks.held++;
            lock.release = async () => {
              if (lock.released) return;
              lock.released = true;
              window.__wakeLocks.held--;
              lock.dispatchEvent(new Event("release"));
            };
            return lock;
          },
        },
      });
    });
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    await forceEncode(page);
    trackGrants(page);
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    await page.waitForFunction(() => window.__wakeLocks.held === 1);
    await page.locator(".playback-player").hover();
    await page.getByRole("button", { name: "Pause", exact: true }).click();
    await page.waitForFunction(() => window.__wakeLocks.held === 0);
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await playing(page);
    await page.waitForFunction(() => window.__wakeLocks.held === 1);
    await page.locator(".playback-player").hover();
    // Use actual mouse dragging: assigning input.value would miss canceled defaults.
    const dragVolume = async () => {
      await page.locator(".playback-player").hover();
      const slider = page.getByRole("slider", { name: "Volume", exact: true });
      const box = await slider.boundingBox();
      await page.mouse.move(box.x + box.width - 7, box.y + box.height / 2);
      await page.mouse.down();
      await page.mouse.move(
        box.x + 7 + (box.width - 14) * 0.3,
        box.y + box.height / 2,
        { steps: 8 },
      );
      await page.mouse.up();
      const value = Number(await slider.inputValue());
      assert(
        value > 0 && value < 0.6,
        "Mouse dragging changes the volume slider",
      );
      await page.waitForFunction((v) => {
        const video = document.querySelector("video");
        return Math.abs(video.volume - v) < 0.01 && !video.muted;
      }, value);
      // Return focus to the guide so fullscreen and guide shortcuts keep working.
      await page.locator(".playback-time").click();
    };
    await dragVolume();
    const total = (await page.locator(".playback-time").textContent())
      .split("/")[1]
      .trim();
    assert(
      total === "2:00",
      `Total time is ${total}, want the episode's real 2:00 rather than the buffered length`,
    );

    assert(
      await page.locator(".playback-program").isHidden(),
      "Compact player must hide program details",
    );

    // Every control explains itself on hover.
    const untitled = await page
      .locator(
        ".playback-controls button, .playback-controls input, .playback-time",
      )
      .evaluateAll((els) =>
        els.filter((e) => !e.title).map((e) => e.getAttribute("aria-label")),
      );
    assert(
      untitled.length === 0,
      `Controls without a tooltip: ${JSON.stringify(untitled)}`,
    );

    // Outside means outside the menu, including the rest of the control bar.
    // Capture must still dismiss when the clicked element stops propagation.
    await page
      .locator(".playback-time")
      .evaluate((el) =>
        el.addEventListener("pointerdown", (e) => e.stopPropagation()),
      );
    for (const submenu of [null, /^Audio Tracks/, /^Subtitles/]) {
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      if (submenu) await page.getByRole("menuitem", { name: submenu }).click();
      assert(
        await page.getByRole("menu").isVisible(),
        "Navigating a submenu should keep it open",
      );
      await page.locator(".playback-time").click();
      assert(
        (await page.getByRole("menu").count()) === 0,
        "Clicking the control bar must close the entire menu",
      );
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      if (submenu) await page.getByRole("menuitem", { name: submenu }).click();
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      assert(
        (await page.getByRole("menu").count()) === 0,
        "Three-dot toggle must close any submenu",
      );
    }

    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.locator(".page-header").click({ position: { x: 250, y: 10 } });
    assert(
      (await page.getByRole("menu").count()) === 0,
      "Clicking outside the player must close the menu",
    );
    await page.locator(".playback-player").hover();
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    assert(
      (await page.getByRole("menu").count()) === 0,
      "Settings toggle must close without reopening",
    );

    // The large player fills the window's width, hides the show details and pushes the guide down,
    // and the same video element keeps playing through both changes.
    await page.evaluate(() => {
      window.__video = document.querySelector("video");
    });
    const wrap = page.locator(".preview-video-wrap");
    const smallBox = await wrap.boundingBox();
    await page
      .getByRole("button", { name: "Large Player", exact: true })
      .click();
    await page.waitForFunction(
      (w) =>
        document.querySelector(".preview-video-wrap").getBoundingClientRect()
          .width >
        w * 2,
      smallBox.width,
      { timeout: 5000 },
    );
    await delay(400);
    await dragVolume();
    const largeBox = await wrap.boundingBox();
    assert(
      await page.locator(".playback-program").isVisible(),
      "Large player must show program details",
    );
    const overlayLayout = await page
      .locator(".playback-program")
      .evaluate((el) => {
        const children = [...el.querySelectorAll("span")];
        return {
          sizes: children.map((c) => parseFloat(getComputedStyle(c).fontSize)),
          bottom: el.getBoundingClientRect().bottom,
          timeTop: document
            .querySelector(".playback-time")
            .getBoundingClientRect().top,
        };
      });
    assert(
      overlayLayout.sizes[0] > overlayLayout.sizes[1] &&
        overlayLayout.sizes[0] > overlayLayout.sizes[2],
      "Channel/title font hierarchy changed",
    );
    assert(
      overlayLayout.bottom <= overlayLayout.timeTop,
      "Program details must sit above the time code",
    );

    const guideTop = (await page.locator(".guide-section").boundingBox()).y;
    assert(
      largeBox.width > smallBox.width * 2 &&
        (await page.locator(".preview-detail").isHidden()),
      "Large player should widen the video and hide the show details",
    );
    assert(
      Math.abs(largeBox.height - (largeBox.width * 9) / 16) < 8,
      `Large player lost its 16:9 shape: ${JSON.stringify(largeBox)}`,
    );
    assert(
      guideTop >= largeBox.y + largeBox.height - 1,
      "The guide should sit below the large player",
    );
    // It fits the visible area without scrolling and keeps a bottom margin like the side margins.
    const viewport = await page.evaluate(() => ({
      w: innerWidth,
      h: innerHeight,
    }));
    const headerBottom =
      (await page.locator(".page-header").boundingBox()).y +
      (await page.locator(".page-header").boundingBox()).height;
    assert(
      largeBox.y >= headerBottom - 1 &&
        largeBox.y + largeBox.height <= viewport.h - 12 &&
        largeBox.y + largeBox.height >= viewport.h - 40,
      `Large player should end just above the bottom edge: ${JSON.stringify({ largeBox, viewport })}`,
    );
    assert(
      largeBox.width <= viewport.w - 36,
      `Large player should keep its side margins: ${JSON.stringify({ largeBox, viewport })}`,
    );
    assert(
      await page.evaluate(
        () =>
          window.__video === document.querySelector("video") &&
          !window.__video.paused,
      ),
      "Growing the player must not restart the video",
    );
    assert(
      await page.evaluate(() => window.__wakeLocks.held === 1),
      "Large player must retain the screen wake lock",
    );
    await page.keyboard.press("f");
    await page.waitForFunction(() => document.fullscreenElement !== null);
    await dragVolume();
    assert(
      await page.evaluate(() => window.__wakeLocks.held === 1),
      "Fullscreen player must retain the screen wake lock",
    );
    await page.keyboard.press("f");
    await page.waitForFunction(() => document.fullscreenElement === null);
    await page.keyboard.press("m");
    // In a narrow window the video is width-limited, and the guide moves up to fill the space below it.
    await page.setViewportSize({ width: 800, height: 900 });
    await delay(500);
    const narrowVideo = await wrap.boundingBox();
    const narrowGuide = await page.locator(".guide-section").boundingBox();
    assert(
      narrowGuide.y < 900 - 200 &&
        narrowGuide.y >= narrowVideo.y + narrowVideo.height - 1 &&
        narrowGuide.y + narrowGuide.height <= 901,
      `The guide should fill the space under a narrow large player: ${JSON.stringify({ narrowVideo, narrowGuide })}`,
    );
    await page.setViewportSize({ width: 1280, height: 720 });
    await delay(300);
    await page.locator(".playback-player").hover();
    await page
      .getByRole("button", { name: "Small Player", exact: true })
      .click();
    await page.waitForFunction(
      () =>
        document.querySelector(".preview-video-wrap").getBoundingClientRect()
          .width < 400,
      null,
      { timeout: 5000 },
    );
    assert(
      (await page.locator(".preview-detail").isVisible()) &&
        (await page.evaluate(
          () => window.__video === document.querySelector("video"),
        )),
      "Small player should restore the show details",
    );
    await page.setViewportSize({ width: 1000, height: 800 });
    // The isolated playback fixture contains only a movie. Exercise the real
    // preview layout with a temporary episode label instead of depending on
    // unrelated fixture media or the wall-clock schedule.
    const episodeLayout = await page
      .locator(".preview-detail-main")
      .evaluate((main) => {
        const element = document.createElement("div");
        element.className = "preview-episode";
        element.textContent =
          "S01E01 — Dungeons and Dragons The Queens Cradle With A Deliberately Long Episode Name";
        main.appendChild(element);
        const result = {
          width: element.clientWidth,
          content: element.scrollWidth,
          wrap: getComputedStyle(element).overflowWrap,
        };
        element.remove();
        return result;
      });
    assert(
      episodeLayout.content <= episodeLayout.width + 1 &&
        episodeLayout.wrap === "anywhere",
      "Split-screen episode title wraps inside the preview: " +
        JSON.stringify(episodeLayout),
    );
    await page.setViewportSize({ width: 1280, height: 720 });

    const channel = () =>
      page
        .locator(".guide-slot-box-focused")
        .evaluate(
          (el) =>
            el.closest(".guide-row").querySelector(".guide-channel-num")
              .textContent,
        );
    const before = await channel();
    await page.getByRole("button", { name: "Pause", exact: true }).click();
    await page.keyboard.press("ArrowDown");
    assert(
      (await channel()) !== before,
      "Arrow keys did not move the guide after using the player controls",
    );
    const held = await page.evaluate(
      () => document.querySelector("video").currentTime,
    );
    await delay(6000);
    const after = await page.evaluate(() => {
      const v = document.querySelector("video");
      return { paused: v.paused, time: v.currentTime };
    });
    assert(
      after.paused && Math.abs(after.time - held) < 0.3,
      `A paused player moved on when new segments arrived: ${JSON.stringify({ held, ...after })}`,
    );
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await playing(page);

    const captions = async () => {
      await page.locator(".playback-player").hover();
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      await page.getByRole("menuitem", { name: /^Subtitles/ }).click();
    };
    await captions();
    assert(
      (await page.getByRole("menuitemradio").allTextContents()).join() ===
        "Off,English,German,French,Spanish,Italian,Portuguese",
      "Subtitle menu lacks the file's languages",
    );
    // Seven entries do not all fit: the list shows five rows at most and scrolls.
    const box = await page
      .locator(".playback-menu-list")
      .evaluate((el) => ({ visible: el.clientHeight, total: el.scrollHeight }));
    assert(
      box.total > box.visible && box.visible <= 146,
      `Subtitle list should scroll within five rows: ${JSON.stringify(box)}`,
    );
    await page
      .getByRole("menuitemradio", { name: "Portuguese" })
      .scrollIntoViewIfNeeded();
    const caption = (text) =>
      page.waitForFunction(
        (t) =>
          (document.querySelector(".playback-subtitles")?.textContent ?? "") ===
          t,
        text,
        { timeout: 25000 },
      );
    await page.getByRole("menuitemradio", { name: "English" }).click();
    await caption("E2E SUBTITLE ONE");
    await captions();
    await page.getByRole("menuitemradio", { name: "German" }).click();
    await caption("E2E UNTERTITEL");
    await captions();
    await page.getByRole("menuitemradio", { name: "Off" }).click();
    await caption("");
    // Full screen unmutes; a fresh page starts muted so the browser allows autoplay.
    assert(
      await page.evaluate(() => document.querySelector("video").muted),
      "A fresh page should start muted",
    );
    await page
      .getByRole("button", { name: "Full Screen", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Mute", exact: true })
      .waitFor({ state: "attached", timeout: 5000 });
    assert(
      !(await page.evaluate(() => document.querySelector("video").muted)),
      "Entering full screen should unmute",
    );
    await leave(page);
    await noWorkers(unreleased);
    await context.close();
    console.log(
      "PASS: real total time, pause sticks, arrow keys stay on the guide, subtitle languages switch",
    );
  }

  async function responsiveTouchUi() {
    seed();
    const context = await browser.newContext({
      viewport: { width: 390, height: 844 },
      screen: { width: 390, height: 844 },
      hasTouch: true,
    });
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    // This scenario repeatedly resizes the browser window. Native fullscreen
    // prevents Browser.setWindowBounds on hosted Chromium; exercise viewport
    // fallback here and leave accepted fullscreen to mobileLandscape's mock.
    await page.addInitScript(() => {
      Element.prototype.requestFullscreen = () =>
        Promise.reject(
          new DOMException(
            "Layout scenario uses viewport fallback",
            "NotAllowedError",
          ),
        );
    });
    await login(page);
    await page.waitForFunction(() =>
      document
        .querySelector(".guide-page")
        ?.classList.contains("is-large-player"),
    );
    assert(
      (await page
        .getByRole("button", { name: "Large Player", exact: true })
        .count()) === 0,
      "Narrow touch view hides the player-size choice",
    );
    const phone = await page.evaluate(() => ({
      width: innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      playerLarge: document
        .querySelector(".playback-player")
        ?.classList.contains("is-large"),
      headerTarget: getComputedStyle(
        document.querySelector(".page-header-control"),
      ).height,
    }));
    assert(
      phone.playerLarge &&
        phone.documentWidth <= phone.width + 1 &&
        parseFloat(phone.headerTarget) >= 44,
      `Phone portrait remains contained with touch-sized controls: ${JSON.stringify(phone)}`,
    );

    await page.locator(".guide-slot-box").first().click();
    await page.locator(".playback-controls").waitFor();
    assert(
      (await page
        .getByRole("button", { name: "Large Player", exact: true })
        .count()) === 0,
      "The small-player choice stays unavailable while the phone is narrow",
    );
    const controlsOpacity = await page
      .locator(".playback-controls")
      .evaluate((element) => getComputedStyle(element).opacity);
    assert(
      controlsOpacity === "1",
      "Playback controls remain visible without hover on touch screens",
    );

    for (const viewport of [
      { width: 844, height: 390 }, { width: 667, height: 375 },
      { width: 768, height: 1024 },
      { width: 1280, height: 800 },
    ]) {
      await page.setViewportSize(viewport);
      await page.waitForFunction(() =>
        document
          .querySelector(".guide-page")
          ?.classList.contains("is-large-player"),
      );
      const dimensions = await page.evaluate(() => ({
        width: innerWidth,
        documentWidth: document.documentElement.scrollWidth,
      }));
      assert(
        dimensions.documentWidth <= dimensions.width + 1,
        `Viewport ${viewport.width}x${viewport.height} has no page-level horizontal overflow`,
      );
      assert(
        (await page
          .getByRole("button", { name: /^(Large|Small) Player$/ })
          .count()) === 0,
        `Touch viewport ${viewport.width}x${viewport.height} must not expose the compact player choice`,
      );
      assert(
        (await page
          .getByRole("button", { name: "Full Screen", exact: true })
          .count()) === 1,
        "Fullscreen remains available in the forced large player",
      );
    }

    await page.setViewportSize({ width: 390, height: 844 });
    await playing(page);
    await leavePlaying(page);
    await page.goto(url + "/admin/accounts");
    const navToggle = page.getByRole("button", {
      name: "Server Settings Navigation",
    });
    await navToggle.waitFor();
    assert(
      !(await page.locator(".admin-tabs").isVisible()),
      "Narrow Server Settings hides the desktop tab row",
    );
    await navToggle.click();
    await page
      .getByRole("menuitem", { name: "Home Users", exact: true })
      .click();
    await page.waitForURL("**/admin/accounts");
    assert(
      (await page.getByRole("menu").count()) === 0,
      "Choosing a compact navigation item closes the hamburger menu",
    );
    await page.setViewportSize({ width: 844, height: 390 });
    await page.goto(url + "/admin/dashboard");
    await page.locator(".metrics-grid .metric-panel").first().waitFor();
    const landscapeMetrics = await page.evaluate(() => {
      const card = document.querySelector(".server-metrics")?.parentElement;
      const graph = document.querySelector(".metrics-grid .metric-panel");
      if (!card || !graph) return null;
      const style = getComputedStyle(card);
      return {
        graphWidth: graph.getBoundingClientRect().width,
        innerWidth:
          card.clientWidth -
          parseFloat(style.paddingLeft) -
          parseFloat(style.paddingRight),
      };
    });
    assert(
      landscapeMetrics &&
        Math.abs(landscapeMetrics.graphWidth - landscapeMetrics.innerWidth) < 2,
      `Landscape touch graphs fill the card content width: ${JSON.stringify(landscapeMetrics)}`,
    );
    await context.close();
    console.log(
      "PASS: mobile touch layout, player breakpoint, resizing, and password visibility/autofill semantics",
    );
  }

  // Quality ceiling: chosen per profile on this device, applied to every channel,
  // swapped in without leaving the page, and not shared with another device.
  async function quality() {
    seed("quality");
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    trackGrants(page);
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    const height = () =>
      page.evaluate(() => document.querySelector("video").videoHeight);
    const menu = async () => {
      await page.locator(".playback-player").hover();
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      await page.getByRole("menuitem", { name: /^Quality/ }).click();
    };
    assert(
      (await height()) === 720,
      `Expected the 720p source to play as-is, got ${await height()}p`,
    );
    // The Settings menu order, and Show Playback Details puts the live panel in the top left corner.
    await page.locator(".playback-player").hover();
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    const statusRows = await page
      .locator('.playback-menu [role^="menuitem"]')
      .allTextContents();
    assert(
      statusRows[0] === "Quality (auto)",
      "Quality row reports active Auto mode",
    );
    assert(
      statusRows[1] === "Audio Tracks",
      "Untagged audio omits the unknown language suffix",
    );
    assert(
      /^Subtitles \(on|off\)$/.test(statusRows[2]),
      "Subtitle row reports active on/off status",
    );
    const items = statusRows.map((t) =>
      t
        .replace(/\s*(Auto|Normal|Off|On|\d+p|[\d.]+×)$/, "")
        .replace(/ \(c\)$/, "")
        .replace(/ \([^)]*\)$/, "")
        .replace(/^Audio Tracks.*$/, "Audio Tracks"),
    );
    const wanted = [
      "Quality",
      "Audio Tracks",
      "Subtitles",
      "Picture In Picture",
      "Show Playback Details",
    ].filter((n) => items.includes(n));
    assert(
      items.join("|") === wanted.join("|") &&
        items[0] === "Quality" &&
        items.at(-1) === "Show Playback Details" &&
        !items.includes("Playback Speed"),
      `Menu order should be Quality, Audio Tracks, Subtitles, Picture In Picture, Show Playback Details: ${JSON.stringify(items)}`,
    );
    await page
      .getByRole("menuitemcheckbox", { name: /^Show Playback Details/ })
      .click();
    const panel = page.locator(".playback-details");
    await panel.waitFor();
    const names = await panel.locator("dt").allTextContents();
    const detailFields = [
      "File Name",
      "Source Video",
      "Output Video",
      "Video Decoder",
      "Video Encoder",
      "Video Reason",
      "Source Audio",
      "Output Audio",
      "Audio Reason",
      "Startup Time",
      "Resolution",
      "Dropped Frames",
      "Local Buffer",
      "Live Delay",
      "Download Speed",
      "CPU Util",
    ];
    assert(
      names.join("|") === detailFields.join("|"),
      `Playback details fields/order are wrong: ${JSON.stringify(names)}`,
    );
    const values = await panel.locator("dd").allTextContents();
    const details = Object.fromEntries(
      names.map((name, index) => [name, values[index]]),
    );
    assert(
      values.every(Boolean),
      `Playback details should use explicit values or N/A: ${JSON.stringify(details)}`,
    );
    assert(
      /^(N\/A|Measuring…|(?:≥ )?\d+(?:\.\d+)? Mbps(?: \((?:Probe|Local|Idle)(?:, (?:Local|Idle))?\))?)$/.test(
        details["Download Speed"],
      ) && /^(N\/A|\d+(?:\.\d+)?%)$/.test(details["CPU Util"]),
      `Runtime metrics should be formatted or unavailable: ${JSON.stringify(details)}`,
    );
    const box = await panel.boundingBox();
    const stage = await page.locator(".playback-player").boundingBox();
    assert(
      box.x - stage.x < 20 && box.y - stage.y < 20,
      `Playback details should sit in the top left corner: ${JSON.stringify({ box, stage })}`,
    );
    assert(
      /\.(mp4|mkv|mov)/i.test(await panel.locator("dd").first().innerText()),
      "Playback details should name the file",
    );
    await page.locator(".playback-player").hover();
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page
      .locator(".playback-menu button", { hasText: "Show Playback Details" })
      .click();
    await panel.waitFor({ state: "detached" });
    await menu();
    assert(
      (await page.getByRole("menuitemradio").allTextContents()).join() ===
        "Auto,720p,480p,360p",
      "Quality menu should offer the rungs at or below the source",
    );
    const before = await page.evaluate(() =>
      Number(document.querySelector("video").dataset.scheduleTime),
    );
    await page.getByRole("menuitemradio", { name: "360p" }).click();
    await page.waitForFunction(
      () =>
        document.querySelector("video").videoHeight === 360 &&
        !document.querySelector("video").paused &&
        document.querySelector("video").readyState >= 3,
      null,
      { timeout: 30000 },
    );
    const after = await page.evaluate(() =>
      Number(document.querySelector("video").dataset.scheduleTime),
    );
    assert(after > before, "Playback went backwards when the quality changed");
    assert(
      await page.locator(".playback-freeze").isHidden(),
      "The held frame stayed on screen after the switch",
    );
    // The choice is not stored in the browser.
    assert(
      (await page.evaluate(() =>
        localStorage.getItem("alreadyon.playbackQuality"),
      )) === null,
      "Quality must not be written to localStorage",
    );

    // It survives moving around the app, including Profile Settings and the Admin tabs...
    const goTo = (path) =>
      page.evaluate((p) => {
        history.pushState({}, "", p);
        dispatchEvent(new PopStateEvent("popstate"));
      }, path);
    const startAgain = async () => {
      await page.locator(".guide-slot-box").first().click();
      await playing(page);
    };
    await leave(page);
    await goTo("/profile");
    await page.locator(".admin-field-row").first().waitFor();
    await goTo("/");
    await startAgain();
    assert(
      (await height()) === 360,
      `Moving around the app lost the quality choice (${await height()}p)`,
    );
    // ...and saving settings, which reloads the page on the app's behalf.
    await leave(page);
    await goTo("/admin/server");
    await page.locator(".settings-grid").waitFor();
    const dns = page
      .locator(".setting-row", { hasText: "Dynamic DNS" })
      .first()
      .locator("select");
    assert(
      (await dns.locator("option").allTextContents()).join("|") ===
        "None|Cloudflare|No-IP",
      "Only supported DDNS providers should be available",
    );
    // This quality-persistence scenario exercises shared Save Changes/reload,
    // not a real DNS service. Keep its provider credentials and responses local.
    // Provider network behavior is covered by isolated Go transport fixtures.
    let ddns = {
      provider: "none",
      running: "",
      configs: [
        {
          provider: "no-ip",
          hostname: "",
          zoneId: "",
          username: "",
          hasSecret: false,
          enabled: false,
          lastAttempt: 0,
          lastUpdate: 0,
          lastVerified: 0,
          lastIp: "",
          lastError: "",
          nextAttempt: 0,
          blocked: false,
        },
      ],
    };
    await page.route(url + "/api/settings/ddns", async (route) => {
      if (route.request().method() === "PUT") {
        const input = route.request().postDataJSON();
        assert(
          input.provider === "none" || input.provider === "no-ip",
          "Unexpected test provider",
        );
        ddns.provider = input.provider;
        if (input.provider === "no-ip")
          ddns.configs[0] = {
            ...ddns.configs[0],
            hostname: input.hostname,
            username: input.username,
            hasSecret: true,
            enabled: true,
            lastAttempt: 1,
            lastVerified: 1,
          };
      }
      await route.fulfill({ json: ddns });
    });
    await dns.selectOption("no-ip");
    await page
      .getByLabel("Public Hostname", { exact: true })
      .fill("fixture.example.com");
    await page
      .getByLabel("DDNS Username", { exact: true })
      .fill("synthetic-user");
    const settingsSecret = page.getByLabel("DDNS credential");
    for (const attr of ["data-lpignore", "data-1p-ignore", "data-bwignore"]) {
      assert(
        (await settingsSecret.getAttribute(attr)) === "true",
        `Settings autofill suppression missing ${attr}`,
      );
    }
    assert(
      (await settingsSecret.getAttribute("autocomplete")) === "off",
      "Settings must not invite credential autofill",
    );
    await settingsSecret.fill("synthetic-password");
    await Promise.all([
      page.waitForNavigation(),
      page.getByRole("button", { name: "Save Changes" }).click(),
    ]);
    await page.getByRole("button", { name: /Back To Guide/ }).click();
    await startAgain();
    assert(
      (await height()) === 360,
      `Saving settings lost the quality choice (${await height()}p)`,
    );
    await leave(page);
    await goTo("/admin/server");
    await page.locator(".settings-grid").waitFor();
    await page
      .locator(".setting-row", { hasText: "Dynamic DNS" })
      .first()
      .locator("select")
      .selectOption("none");
    await Promise.all([
      page.waitForNavigation(),
      page.getByRole("button", { name: "Save Changes" }).click(),
    ]);

    // A fresh page load starts on Auto again.
    await page.reload();
    await login(page);
    await startAgain();
    assert(
      (await height()) === 720,
      `A fresh load should start on Auto (${await height()}p)`,
    );
    // Another device (fresh storage) has its own, automatic, setting.
    const other = await browser.newContext();
    const otherPage = await other.newPage();
    trackGrants(otherPage);
    await login(otherPage);
    await otherPage.locator(".guide-slot-box").first().click();
    await playing(otherPage);
    assert(
      (await otherPage.evaluate(
        () => document.querySelector("video").videoHeight,
      )) === 720,
      "A second device did not start on its own automatic quality",
    );
    await leave(otherPage);
    await other.close();

    await menu();
    await page.getByRole("menuitemradio", { name: "Auto" }).click();
    await page.waitForFunction(
      () =>
        document.querySelector("video").videoHeight === 720 &&
        !document.querySelector("video").paused &&
        document.querySelector("video").readyState >= 3,
      null,
      { timeout: 30000 },
    );
    await leave(page);
    await noWorkers(unreleased);
    await context.close();
    console.log(
      "PASS: quality menu offers rungs, swaps in place, is kept while the app is open (not across a fresh load), other devices stay automatic",
    );
  }

  // A large conversion is a two-rung ladder: it starts on the small rung and
  // moves up to the main picture without leaving the page.
  async function ladder() {
    seed("ladder");
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    // Test the explicit 720p ladder, independent of load-sensitive Auto capacity.
    await forceEncode(page, 720);
    trackGrants(page);
    await page.addInitScript(() => {
      window.__heights = [];
      const note = (e) => {
        if (e.target.tagName === "VIDEO" && e.target.videoHeight)
          window.__heights.push(e.target.videoHeight);
      };
      document.addEventListener("loadedmetadata", note, true);
      document.addEventListener("resize", note, true);
    });
    const requested = [];
    page.on("request", (r) => {
      const m =
        /\/media\/\d+\/(index\.m3u8|stream-\d\.m3u8|segment-\d-\d+\.ts)/.exec(
          r.url(),
        );
      if (m) requested.push(m[1]);
    });
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    try {
      await page.waitForFunction(() => window.__heights.includes(720), null, {
        timeout: 30000,
      });
    } catch (error) {
      // Record playback state and token-free rendition names, never session URLs.
      console.error(
        "LADDER diagnostics:",
        JSON.stringify({
          requested,
          state: await page.evaluate(() => {
            const v = document.querySelector("video");
            return {
              heights: window.__heights,
              height: v?.videoHeight,
              time: v?.currentTime,
              paused: v?.paused,
              ready: v?.readyState,
              error: v?.error?.code,
            };
          }),
        }),
      );
      throw error;
    }
    const heights = await page.evaluate(() => window.__heights);
    assert(
      heights[0] === 360,
      `A ladder stream should start on its 360p rung, saw ${JSON.stringify(heights)}`,
    );
    assert(
      requested[0] === "index.m3u8" &&
        requested.includes("stream-0.m3u8") &&
        requested.includes("stream-1.m3u8"),
      `Master and both rungs should be requested: ${JSON.stringify(requested.slice(0, 8))}`,
    );
    const first = requested.findIndex((r) => r.startsWith("segment-"));
    assert(
      requested[first].startsWith("segment-0-"),
      `The first segment should be from the low rung, got ${requested[first]}`,
    );
    assert(
      requested.some((r) => r.startsWith("segment-1-")),
      "The main rung never loaded",
    );
    await delay(6000);
    assert(
      !(await page.evaluate(() => document.querySelector("video").paused)) &&
        (await page.evaluate(
          () => document.querySelector("video").videoHeight,
        )) === 720,
      "Playback did not settle on the main rung",
    );
    await leave(page);
    await noWorkers(unreleased);
    await context.close();
    console.log(
      `PASS: ladder starts on 360p and moves up to 720p (${JSON.stringify(heights)})`,
    );
  }

  // A fresh page load tunes by itself with no click, so the browser only allows muted autoplay.
  async function autoplay(converted) {
    seed("quality");
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    if (converted) await forceEncode(page);
    trackGrants(page);
    await login(page);
    await page.reload();
    await page
      .waitForFunction(
        () => {
          const v = document.querySelector("video");
          return v && !v.paused && v.currentTime > 1 && v.readyState >= 3;
        },
        null,
        { timeout: 30000 },
      )
      .catch(async () => {
        throw new Error(
          `Fresh page did not autoplay: ${JSON.stringify(
            await page.evaluate(() => {
              const v = document.querySelector("video");
              return {
                paused: v?.paused,
                muted: v?.muted,
                t: v?.currentTime,
                msg: document.querySelector(".playback-message")?.textContent,
              };
            }),
          )}`,
        );
      });
    assert(
      await page.evaluate(() => document.querySelector("video").muted),
      "Autoplay on a fresh page must be muted",
    );
    await page.locator(".playback-player").hover();
    assert(
      (await page
        .getByRole("button", { name: "Pause", exact: true })
        .count()) === 1,
      "A playing video should offer Pause",
    );
    await leave(page);
    await noWorkers(unreleased);
    await context.close();
    console.log(
      `PASS: a fresh page load autoplays muted with no click (${converted ? "converted" : "direct"})`,
    );
  }

  // Exercise real touch input and computed CSS so a coarse-pointer override
  // cannot silently defeat the idle timer in either phone orientation.
  async function mobileOverlay() {
    for (const viewport of [
      { width: 390, height: 844 },
      { width: 844, height: 390 },
    ]) {
      seed();
      const context = await browser.newContext({
        viewport: { width: 390, height: 844 },
        isMobile: true,
        hasTouch: true,
      });
      const page = await context.newPage();
      page.on("pageerror", (e) => errors.push(e.message));
      await login(page);
      await page.locator(".guide-slot-box").first().tap();
      await playing(page);
      await page.setViewportSize(viewport);
      const video = page.locator("video");
      // Selecting a guide slot may scroll the landscape page past the player.
      // Bring it back before waiting for idle so every reveal tap reaches video.
      await video.scrollIntoViewIfNeeded();
      const opacity = () =>
        page
          .locator(".playback-controls")
          .evaluate((el) => getComputedStyle(el).opacity);
      const visible = () =>
        page.waitForFunction(
          () =>
            getComputedStyle(document.querySelector(".playback-controls"))
              .opacity === "1",
          null,
          { timeout: 3000 },
        );
      const hidden = (timeout = 8000) =>
        page.waitForFunction(
          () =>
            getComputedStyle(document.querySelector(".playback-controls"))
              .opacity === "0",
          null,
          { timeout },
        );
      // Locator taps scroll, wait for stable geometry and hit-test the target;
      // raw touchscreen coordinates can miss after guide-driven page scrolling.
      const tapVideo = () => video.tap();
      await hidden();
      if (viewport.width > viewport.height) {
        // Reproduce the scrolled landscape position that raw coordinates miss.
        await page.locator(".guide-page").evaluate((el) => {
          el.scrollTop = el.scrollHeight;
        });
      }
      await tapVideo();
      // React state and the CSS fade settle asynchronously after a real touch.
      await visible();
      assert((await opacity()) === "1", "Touch reveals the hidden overlay");
      assert(
        await page.locator("video").evaluate((el) => !el.paused),
        "Reveal tap must not pause video",
      );
      await delay(2000);
      await page.getByRole("button", { name: "Pause", exact: true }).tap();
      assert(
        await page.locator("video").evaluate((el) => el.paused),
        "Control tap still pauses",
      );
      await delay(2000);
      assert(
        (await opacity()) === "1",
        "Control interaction resets the three-second deadline",
      );
      await hidden(2000);
      await tapVideo();
      assert(
        await page.locator("video").evaluate((el) => el.paused),
        "Reveal tap preserves paused state",
      );
      await page.getByRole("button", { name: "Play", exact: true }).tap();
      await playing(page);
      await hidden();
      await tapVideo();
      await page.getByRole("button", { name: "Settings", exact: true }).tap();
      assert(
        (await page
          .getByRole("menu", { name: "Settings", exact: true })
          .count()) === 1,
        "Settings action still opens menu",
      );
      await hidden();
      await leavePlaying(page);
      await context.close();
    }
    console.log(
      "PASS: touch overlays hide and reset after three seconds in portrait and landscape",
    );
  }

  // These native APIs are synthetic. Actual iPad Chrome/Safari and Android
  // rendering still need device testing; decoded video stays real in this suite.
  async function mobileLandscape() {
    const devices = [
      { width: 1024, height: 768, phone: false },
      { width: 844, height: 390, phone: true },
      { width: 667, height: 375, phone: true },
    ];
    const installFullscreen = ({ policy, apple = false, portrait = false }) => {
      let element = null;
      let nativeVideo = null;
      let orientation = portrait ? "portrait-primary" : "landscape-primary";
      let angle = portrait ? 0 : 90;
      window.__rotationFullscreen = { stageRequests: 0, videoRequests: 0 };
      Object.defineProperty(screen.orientation, "type", { get: () => orientation });
      if (apple) {
        Object.defineProperty(navigator, "platform", { get: () => "MacIntel" });
        Object.defineProperty(window, "orientation", { get: () => angle });
      }
      window.__rotateDevice = (nextPortrait) => {
        angle = nextPortrait ? 0 : 90;
        // Apple regression deliberately keeps ScreenOrientation stale while its
        // legacy physical angle changes, as well as delaying viewport resize.
        if (!apple) orientation = nextPortrait ? "portrait-primary" : "landscape-primary";
        window.dispatchEvent(new Event("orientationchange"));
        screen.orientation.dispatchEvent(new Event("change"));
      };
      Object.defineProperty(document, "fullscreenElement", { get: () => element });
      Element.prototype.requestFullscreen = ["legacy", "unsupported"].includes(policy) ? undefined : function () {
        window.__rotationFullscreen.stageRequests++;
        if (policy === "denied") return Promise.reject(new Error("Denied"));
        element = this;
        document.dispatchEvent(new Event("fullscreenchange"));
        return Promise.resolve();
      };
      document.exitFullscreen = () => {
        element = null;
        document.dispatchEvent(new Event("fullscreenchange"));
        return Promise.resolve();
      };
      HTMLVideoElement.prototype.webkitEnterFullscreen = policy === "unsupported" ? undefined : function () {
        window.__rotationFullscreen.videoRequests++;
        nativeVideo = this;
        this.dispatchEvent(new Event("webkitbeginfullscreen"));
      };
      HTMLVideoElement.prototype.webkitExitFullscreen = function () {
        nativeVideo = null;
        this.dispatchEvent(new Event("webkitendfullscreen"));
      };
      Object.defineProperty(HTMLVideoElement.prototype, "webkitDisplayingFullscreen", {
        get() { return nativeVideo === this; },
      });
    };
    const state = (page) => page.evaluate(() => ({
      large: document.querySelector(".guide-page").classList.contains("is-large-player"),
      native: !!document.fullscreenElement || !!document.querySelector("video").webkitDisplayingFullscreen,
      viewport: !!document.querySelector(".playback-player.is-landscape-player"),
    }));
    const toggle = async (page) => {
      await page.locator(".playback-player").dispatchEvent("pointermove");
      await page.getByRole("button", { name: "Full Screen", exact: true }).click();
    };
    const rotate = async (page, portrait, device) => {
      await page.evaluate((value) => window.__rotateDevice(value), portrait);
      await page.setViewportSize(portrait
        ? { width: device.height, height: device.width }
        : { width: device.width, height: device.height });
    };
    for (const device of devices) {
      for (const policy of ["allowed", "denied", "legacy", "unsupported"]) {
        console.log(`Rotation case: ${device.phone ? "phone" : "tablet"} ${device.width}x${device.height}, ${policy}`);
        seed();
        const context = await browser.newContext({
          viewport: { width: device.width, height: device.height },
          screen: { width: device.width, height: device.height },
          hasTouch: true,
          ...(device.phone ? {userAgent: "Mozilla/5.0 (Linux; Android 16) AppleWebKit/537.36 Chrome/156.0.0.0 Mobile Safari/537.36"} : {}),
        });
        const page = await context.newPage();
        page.setDefaultTimeout(15000);
        page.on("pageerror", (e) => errors.push(e.message));
        await page.addInitScript(installFullscreen, { policy });
        await login(page);
        if (!device.phone) await page.locator(".guide-slot-box").first().tap();
        try {
          await playing(page);
        } catch (error) {
          console.log("Rotation playback state:", await page.locator("video").evaluate((v) => ({
            paused: v.paused, muted: v.muted, readyState: v.readyState,
            time: v.currentTime, width: v.videoWidth, error: v.error?.code,
            message: document.querySelector(".playback-message")?.textContent,
          })));
          throw error;
        }
        await page.locator("video").evaluate((video) => { window.__rotationVideo = video; });
        const supported = !["denied", "unsupported"].includes(policy);
        assert((await state(page)).large === device.phone, "Phone guide stays large; tablet landscape guide stays compact");
        assert((await state(page)).viewport === device.phone, "Only phones automatically fill landscape");
        assert(await page.getByRole("button", { name: /Large Player|Small Player/ }).count() === 0,
          "Touch browsers have no size toggle");
        if (device.phone) {
          const bounds = await page.locator(".playback-player").boundingBox();
          assert(Math.abs(bounds.width - device.width) < 1 && Math.abs(bounds.height - device.height) < 1,
            "Phone landscape playback fills the viewport");
          assert((await state(page)).native === supported, "Phone rotation requests native fullscreen when possible");
          // Browser Done/Escape must dismiss the viewport fallback too.
          if (supported) await page.evaluate(() => {
            if (document.fullscreenElement) return document.exitFullscreen();
            document.querySelector("video").webkitExitFullscreen();
          });
          else await toggle(page);
          assert(!(await state(page)).viewport && !(await state(page)).native && (await state(page)).large,
            "Phone fullscreen exit returns to the large guide while still landscape");
          await page.evaluate(() => window.dispatchEvent(new Event("resize")));
          await page.evaluate(() => new Promise(requestAnimationFrame));
          assert(!(await state(page)).viewport, "Resize cannot immediately re-enter dismissed phone fullscreen");
        } else {
          assert(!(await state(page)).native, "Tablet landscape never enters fullscreen automatically");
          await toggle(page);
          assert((await state(page)).native === supported, "Tablet explicit fullscreen obeys browser policy");
          if (supported) await toggle(page);
          assert(!(await state(page)).large && !(await state(page)).native, "Tablet exit restores compact guide");
        }
        await toggle(page);
        await rotate(page, true, device);
        await page.waitForFunction(() => !document.fullscreenElement &&
          !document.querySelector("video").webkitDisplayingFullscreen &&
          !document.querySelector(".is-landscape-player") &&
          document.querySelector(".guide-page.is-large-player"));
        await toggle(page);
        assert((await state(page)).native === supported, "Manual portrait fullscreen stays open");
        if (supported) await toggle(page);
        await rotate(page, false, device);
        if (device.phone) await page.waitForSelector(".is-landscape-player");
        assert((await state(page)).viewport === device.phone, "New physical landscape rotation re-enables phone fullscreen only");
        assert(await page.locator("video").evaluate((video) => video === window.__rotationVideo &&
          !video.paused && video.videoWidth > 0), "Rotation preserves mounted, decoded, playing video");
        await leavePlaying(page);
        await context.close();
      }
    }
    // Chrome on iPad: legacy angle and viewport/ScreenOrientation can disagree.
    // Exercise both first load in landscape and rotation from portrait.
    for (const initialPortrait of [true, false]) {
      seed();
      const context = await browser.newContext({
        viewport: initialPortrait ? { width: 768, height: 1024 } : { width: 1024, height: 768 },
        screen: { width: 768, height: 1024 },
        hasTouch: true,
        userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 CriOS/156 Version/11.1.1 Safari/605.1.15",
      });
      const page = await context.newPage();
      page.setDefaultTimeout(15000);
      page.on("pageerror", (e) => errors.push(e.message));
      await page.addInitScript(installFullscreen, { policy: "allowed", apple: true, portrait: initialPortrait });
      await login(page);
      await page.locator(".guide-slot-box").first().tap();
      await playing(page);
      assert((await state(page)).large === initialPortrait, "iPad Chrome initial layout follows physical orientation");
      assert(await page.locator('meta[name="viewport"]').getAttribute("content") === "initial-scale=1.0, viewport-fit=cover",
        "iPad Chrome infers viewport width after rotation without disabling zoom");
      await page.evaluate(() => window.__rotateDevice(false));
      await page.waitForFunction(() => !document.querySelector(".guide-page.is-large-player"));
      await page.setViewportSize({ width: 1024, height: 768 });
      await toggle(page);
      await page.setViewportSize({ width: 768, height: 1024 });
      assert((await state(page)).native, "Stale portrait viewport cannot cancel physical landscape fullscreen");
      const requests = await page.evaluate(() => window.__rotationFullscreen);
      assert(requests.videoRequests === 1 && requests.stageRequests === 0,
        "iPad Chrome chooses native video fullscreen even when container fullscreen exists");
      await page.evaluate(() => window.__rotateDevice(true));
      await page.waitForFunction(() => document.querySelector(".guide-page.is-large-player") &&
        !document.querySelector("video").webkitDisplayingFullscreen);
      assert(await page.locator("video").evaluate((video) => !video.paused && video.videoWidth > 0),
        "iPad Chrome landscape load/rotation retains decoded playback");
      await leavePlaying(page);
      await context.close();
    }
    const desktop = await browser.newContext({ viewport: { width: 1280, height: 800 } });
    const page = await desktop.newPage();
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    assert((await page.locator(".is-landscape-player").count()) === 0,
      "Desktop landscape never triggers automatic fullscreen");
    await leavePlaying(page);
    await desktop.close();
    console.log("PASS: phone automatic fullscreen/dismissal, tablet guide layouts, iPad Chrome delayed viewport/native fullscreen, decoded playback and desktop behavior");
  }

  // Keyboard: small-player arrows browse; expanded Up/Down tunes and horizontal arrows navigate controls.
  async function keyboard() {
    seed();
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    trackGrants(page);
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    // The controls show on hover, fade after a few idle seconds and return on mouse movement.
    const controlsOpacity = () =>
      page
        .locator(".playback-controls")
        .evaluate((el) => getComputedStyle(el).opacity);
    const box = await page.locator(".playback-player").boundingBox();
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.waitForFunction(
      () =>
        getComputedStyle(document.querySelector(".playback-controls"))
          .opacity === "1",
      null,
      { timeout: 3000 },
    );
    await page.waitForFunction(
      () =>
        getComputedStyle(document.querySelector(".playback-controls"))
          .opacity === "0",
      null,
      { timeout: 8000 },
    );
    await page.mouse.move(
      box.x + box.width / 2 + 5,
      box.y + box.height / 2 + 5,
    );
    await page.waitForFunction(
      () =>
        getComputedStyle(document.querySelector(".playback-controls"))
          .opacity === "1",
      null,
      { timeout: 3000 },
    );
    assert(
      (await controlsOpacity()) === "1",
      "Moving the mouse should bring the controls back",
    );
    const numbers = await page.locator(".guide-channel-num").allTextContents();
    const rowOf = (cls) =>
      page
        .locator(cls)
        .evaluate(
          (el) =>
            el.closest(".guide-row").querySelector(".guide-channel-num")
              .textContent,
        );
    const video = () =>
      page.evaluate(() => {
        const v = document.querySelector("video");
        return { muted: v.muted, paused: v.paused };
      });
    assert(numbers.length >= 2, "Need two channels");
    const start = await rowOf(".guide-slot-box-tuned");
    const at = numbers.indexOf(start);
    const [next, prev] = at === 0 ? [1, 0] : [at, at - 1];
    const forward = at === 0 ? "ArrowDown" : "ArrowUp",
      back = at === 0 ? "ArrowUp" : "ArrowDown";
    // Small player: the arrows only browse; nothing is tuned.
    await page.keyboard.press(forward);
    assert(
      (await rowOf(".guide-slot-box-focused")) === numbers[next] &&
        (await rowOf(".guide-slot-box-tuned")) === start,
      "Small player arrows should move the cursor without tuning",
    );
    await page.keyboard.press(back);
    // Large player: the arrows tune.
    await page.keyboard.press("t");
    await page.waitForFunction(
      () => !!document.querySelector(".preview-section.is-large"),
      null,
      { timeout: 5000 },
    );
    await page.keyboard.press(forward);
    await page.waitForFunction(
      (n) =>
        document
          .querySelector(".guide-slot-box-tuned")
          ?.closest(".guide-row")
          .querySelector(".guide-channel-num").textContent === n,
      numbers[next],
      { timeout: 5000 },
    );
    await playing(page);
    await page.keyboard.press(back);
    await page.waitForFunction(
      (n) =>
        document
          .querySelector(".guide-slot-box-tuned")
          ?.closest(".guide-row")
          .querySelector(".guide-channel-num").textContent === n,
      numbers[prev],
      { timeout: 5000 },
    );
    await playing(page);
    // Expanded playback uses a horizontal control cursor and menu-owned arrows.
    const assertControlCursor = async () => {
      const channelBefore = await rowOf(".guide-slot-box-tuned");
      await page.keyboard.press("Escape");
      await page.keyboard.press("ArrowRight");
      await page.waitForFunction(() =>
        ["Mute", "Unmute"].includes(
          document.activeElement?.getAttribute("aria-label"),
        ),
      );
      assert(
        (await rowOf(".guide-slot-box-tuned")) === channelBefore,
        "Horizontal arrows never tune",
      );
      const mutedBefore = (await video()).muted;
      await page.keyboard.press("Enter");
      assert(
        (await video()).muted !== mutedBefore,
        "Enter activates the selected Mute control",
      );
      await page.keyboard.press("ArrowRight");
      await page.keyboard.press(" ");
      await page.getByRole("menu", { name: "Settings", exact: true }).waitFor();
      // Every source exposes the status rows; Quality is first even for Auto-only sources.
      await page.waitForFunction(
        () => document.activeElement?.getAttribute("role") === "menuitem",
      );
      await page.keyboard.press("Enter");
      await page.getByRole("menu", { name: "Quality", exact: true }).waitFor();
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("ArrowUp");
      assert(
        (await rowOf(".guide-slot-box-tuned")) === channelBefore,
        "Submenu Up/Down navigate without tuning",
      );
      await page.keyboard.press("Escape");
      await page.getByRole("menu", { name: "Settings", exact: true }).waitFor();
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("ArrowUp");
      assert(
        (await rowOf(".guide-slot-box-tuned")) === channelBefore,
        "Root menu Up/Down navigate without tuning",
      );
      await page.keyboard.press("Escape");
      await page.locator(".playback-menu").waitFor({ state: "detached" });
      await page.keyboard.press("Escape");
      await page.waitForFunction(
        () =>
          getComputedStyle(document.querySelector(".playback-controls"))
            .opacity === "0",
      );
      // Reproduce the banner/paused HUD state without relying on tune timing.
      await page
        .locator(".playback-player")
        .evaluate((stage) =>
          stage.classList.add("show-channel-overlay", "is-paused"),
        );
      assert(
        (await page
          .locator(".playback-controls")
          .evaluate((el) => getComputedStyle(el).opacity)) === "0",
        "Escape hides the HUD even during its channel banner or paused state",
      );
      await page
        .locator(".playback-player")
        .evaluate((stage) =>
          stage.classList.remove("show-channel-overlay", "is-paused"),
        );
      await page.keyboard.press("ArrowLeft");
      await page.waitForFunction(
        () => document.activeElement?.getAttribute("aria-label") === "Pause",
      );
      await page.waitForFunction(
        () =>
          getComputedStyle(document.querySelector(".playback-controls"))
            .opacity === "1",
      );
    };
    await assertControlCursor();
    // m toggles mute, Space toggles pause, even with a guide slot focused.
    const before = await video();
    await page.keyboard.press("m");
    assert((await video()).muted !== before.muted, "m should toggle mute");
    await page.keyboard.press("s");
    await page.locator(".playback-details").waitFor();
    assert(
      (await page
        .locator('.playback-details dt[aria-label="Download Speed"]')
        .count()) === 1 &&
        (await page
          .locator('.playback-details dt[aria-label="CPU Util"]')
          .count()) === 1,
      "s should show browser download speed and process CPU diagnostics",
    );
    await page.keyboard.press("s");
    await page.locator(".playback-details").waitFor({ state: "detached" });
    await page.keyboard.press(" ");
    assert((await video()).paused, "Space should pause");
    await page.keyboard.press(" ");
    await playing(page);
    // There is no speed control: live TV plays at normal speed, and Shift+> / Shift+< do nothing.
    await page.keyboard.press(">");
    await page.keyboard.press("<");
    assert(
      (await page.evaluate(
        () => document.querySelector("video").playbackRate,
      )) === 1,
      "The speed keys should not change the playback rate",
    );
    // f goes full screen and the arrows still change channel there.
    await page.keyboard.press("f");
    await page.waitForFunction(
      () => document.fullscreenElement !== null,
      null,
      { timeout: 5000 },
    );
    await page.keyboard.press(forward);
    await page.waitForFunction(
      (n) =>
        document
          .querySelector(".guide-slot-box-tuned")
          ?.closest(".guide-row")
          .querySelector(".guide-channel-num").textContent === n,
      numbers[next],
      { timeout: 5000 },
    );
    await playing(page);
    await assertControlCursor();
    // t in full screen leaves full screen and switches the player size.
    await page.keyboard.press("t");
    await page.waitForFunction(
      () => document.fullscreenElement === null,
      null,
      { timeout: 5000 },
    );
    await page.waitForFunction(
      () => !document.querySelector(".preview-section.is-large"),
      null,
      { timeout: 5000 },
    );
    // Back to small: the arrows browse again.
    const tunedNow = await rowOf(".guide-slot-box-tuned");
    await page.keyboard.press(tunedNow === numbers[next] ? back : forward);
    assert(
      (await rowOf(".guide-slot-box-tuned")) === tunedNow,
      "Small player arrows must not tune",
    );
    // The address names the tuned channel, and a refresh returns to it.
    assert(
      new URL(page.url()).searchParams.get("channel") === tunedNow,
      "The address should carry the tuned channel",
    );
    await page.reload();
    await page.locator(".guide-slot-box").first().waitFor();
    await page
      .waitForFunction(
        (n) =>
          document
            .querySelector(".guide-slot-box-tuned")
            ?.closest(".guide-row")
            .querySelector(".guide-channel-num").textContent === n,
        tunedNow,
        { timeout: 10000 },
      )
      .catch(async (e) => {
        throw new Error(
          "After a refresh the tuned row is " +
            (await rowOf(".guide-slot-box-tuned")) +
            " at " +
            page.url() +
            ", wanted " +
            tunedNow +
            ": " +
            e.message,
        );
      });
    // Long descriptions must not stretch the player or reserve an empty title column.
    const previewGeometry = async () =>
      page.locator(".preview-detail").evaluate((detail) => {
        const main = detail.querySelector(".preview-detail-main");
        const description = detail.querySelector(".preview-description");
        const overview = description.firstElementChild;
        const original = overview.textContent;
        try {
          overview.textContent =
            "The neighbors prepare a community celebration while an unexpected visitor changes their plans. ".repeat(
              12,
            );
          const mainBox = main.getBoundingClientRect();
          const descriptionBox = description.getBoundingClientRect();
          const videoBox = document
            .querySelector(".preview-video-wrap")
            .getBoundingClientRect();
          return {
            direction: getComputedStyle(detail).flexDirection,
            grow: getComputedStyle(main).flexGrow,
            gap: descriptionBox.x - mainBox.right,
            descriptionBelow: descriptionBox.y >= mainBox.bottom,
            mainFits: main.scrollWidth <= main.clientWidth + 1,
            ratio: videoBox.width / videoBox.height,
            documentFits:
              document.documentElement.scrollWidth <= innerWidth + 1,
          };
        } finally {
          overview.textContent = original;
        }
      });
    await page.setViewportSize({ width: 1400, height: 800 });
    for (const reload of [false, true]) {
      if (reload) {
        await page.reload();
        await page.locator(".preview-description").waitFor();
      }
      const geometry = await previewGeometry();
      assert(
        geometry.direction === "row" &&
          geometry.grow === "0" &&
          geometry.gap >= 0 &&
          geometry.gap <= 25 &&
          geometry.mainFits &&
          geometry.documentFits &&
          Math.abs(geometry.ratio - 16 / 9) < 0.02,
        "Desktop preview remains content-sized after reload: " +
          JSON.stringify(geometry),
      );
    }
    await page.setViewportSize({ width: 900, height: 800 });
    const narrowPreview = await previewGeometry();
    assert(
      narrowPreview.direction === "column" &&
        narrowPreview.descriptionBelow &&
        narrowPreview.mainFits &&
        narrowPreview.documentFits &&
        Math.abs(narrowPreview.ratio - 16 / 9) < 0.02,
      "Narrow desktop keeps readable metadata and an unstretched player: " +
        JSON.stringify(narrowPreview),
    );
    await leave(page);
    await noWorkers(unreleased);
    await context.close();
    console.log(
      "PASS: keyboard: small player browses, large/full-screen Up/Down tunes, horizontal controls and menu cursors work, m f t and Space work, and the address keeps the channel",
    );
  }

  // Profile subtitle defaults and the C key.
  async function subtitleDefaults() {
    seed();
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    trackGrants(page);
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    const caption = (text) =>
      page.waitForFunction(
        (t) =>
          (document.querySelector(".playback-subtitles")?.textContent ?? "") ===
          t,
        text,
        { timeout: 25000 },
      );
    // Nothing chosen yet: C switches subtitles on in English, and off again.
    await page.keyboard.press("c");
    await caption("E2E SUBTITLE ONE");
    await page.keyboard.press("c");
    await caption("");
    // Select the language and mode by their public labels, independent of order.
    await page.goto(url + "/profile");
    assert(
      (await page.getByText("Time Span Shown").count()) === 0,
      "The guide time span setting should be gone",
    );
    const card = page.locator(".admin-card", {
      has: page.getByRole("heading", { name: "Subtitles", exact: true }),
    });
    await card
      .getByLabel("Subtitle language", { exact: true })
      .selectOption("de");
    await card.getByLabel("Subtitles", { exact: true }).selectOption("always");
    await page
      .getByRole("status", { name: "subtitleLanguage saved", exact: true })
      .waitFor();
    await page
      .getByRole("status", { name: "subtitleMode saved", exact: true })
      .waitFor();
    await page.getByRole("button", { name: /Back To Guide/ }).click();
    await page.locator(".guide-slot-box").first().waitFor();
    await playing(page);
    await caption("E2E UNTERTITEL");
    await page.keyboard.press("c");
    await caption("");
    await page.keyboard.press("c");
    await caption("E2E UNTERTITEL");
    // A default language the file lacks is reported instead of silently doing nothing.
    const patch = (body) =>
      page.evaluate(async (body) => {
        const list = await (await fetch("/api/profiles")).json();
        const me = list.find((p) => p.subtitlesEnabled === true) ?? list[0];
        const res = await fetch("/api/profiles/" + me.id, {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        return res.status;
      }, body);
    assert(
      (await patch({ subtitleLanguage: "ja" })) === 200,
      "Could not change the default language",
    );
    await page.reload();
    await playing(page);
    await page.keyboard.press("c");
    await page
      .getByText("No Japanese subtitles for this title")
      .waitFor({ timeout: 5000 });
    assert(
      (await patch({ subtitlesEnabled: null, subtitleLanguage: null })) === 200,
      "Could not reset the profile",
    );
    await leave(page);
    await noWorkers(unreleased);
    await context.close();
    console.log(
      "PASS: profile subtitle defaults apply, and C toggles them (English when never set)",
    );
  }

  // A pause holds the picture, even across a program boundary, and resume goes to the live point.
  async function pausedBoundary() {
    seed("transitions");
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    trackGrants(page);
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    await page
      .getByRole("button", { name: "Pause", exact: true })
      .waitFor({ state: "attached" });
    await page.keyboard.press(" ");
    const held = await page.evaluate(() => {
      const v = document.querySelector("video");
      return { t: v.currentTime, id: v.dataset.mediaId, kind: v.dataset.kind };
    });
    await delay(19000); // the program ends and an ad is scheduled while paused
    const after = await page.evaluate(() => {
      const v = document.querySelector("video");
      return {
        t: v.currentTime,
        id: v.dataset.mediaId,
        kind: v.dataset.kind,
        paused: v.paused,
      };
    });
    assert(
      after.paused &&
        after.kind === "program" &&
        after.id === held.id &&
        Math.abs(after.t - held.t) < 0.3,
      `The paused picture changed: ${JSON.stringify({ held, after })}`,
    );
    await page.keyboard.press(" ");
    await page.waitForFunction(
      () => {
        const v = document.querySelector("video");
        return (
          v.dataset.kind === "commercial" && !v.paused && v.currentTime > 0.5
        );
      },
      null,
      { timeout: 20000 },
    );
    await leave(page);
    await noWorkers(unreleased);
    await context.close();
    console.log(
      "PASS: a paused picture stays put across a program boundary and resume joins the live point",
    );
  }

  // Program boxes sit under the header columns for their true times.
  async function guideAlignment() {
    seed("guide");
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    await login(page);
    const bad = await page.evaluate(() => {
      const heads = [...document.querySelectorAll(".guide-head-slot")];
      const w =
        heads[1].getBoundingClientRect().left -
        heads[0].getBoundingClientRect().left;
      const t0 = Number(heads[0].dataset.startMs),
        x0 = heads[0].getBoundingClientRect().left;
      const out = [];
      let checked = 0;
      for (const b of document.querySelectorAll(".guide-slot-box")) {
        const r = b.getBoundingClientRect();
        const s = Date.parse(b.dataset.startsAt),
          e = Date.parse(b.dataset.endsAt);
        const wantLeft = x0 + Math.max(0, ((s - t0) / 1800000) * w) + 3;
        const wantRight =
          x0 + Math.min(heads.length * w, ((e - t0) / 1800000) * w) - 3;
        checked++;
        if (
          Math.abs(r.left - wantLeft) > 2 ||
          Math.abs(r.right - wantRight) > 2.5
        )
          out.push({
            title: b.title,
            left: r.left - wantLeft,
            right: r.right - wantRight,
          });
      }
      return { checked, out };
    });
    // 12 hours of columns, each 30 minutes, scaled from the time line's text width (a 20-minute chunk is twice it).
    const scale = await page.evaluate(() => {
      const heads = [...document.querySelectorAll(".guide-head-slot")];
      const font = getComputedStyle(
        document.querySelector(".guide-slot-time"),
      ).font;
      const ctx = document.createElement("canvas").getContext("2d");
      ctx.font = font;
      return {
        columns: heads.length,
        slot:
          heads[1].getBoundingClientRect().left -
          heads[0].getBoundingClientRect().left,
        expected: Math.round(
          (2 * (ctx.measureText("00:00am - 00:00am").width + 2) * 30) / 20,
        ),
      };
    });
    assert(
      await page.evaluate(
        () =>
          getComputedStyle(document.querySelector(".guide-head-slot"))
            .justifyContent === "flex-start",
      ),
      "Header times should be left-aligned",
    );
    assert(
      scale.columns === 24 && Math.abs(scale.slot - scale.expected) <= 2,
      `Guide scale is wrong: ${JSON.stringify(scale)}`,
    );
    assert(
      bad.checked >= 3 && bad.out.length === 0,
      `Program boxes are not under their time columns: ${JSON.stringify(bad)}`,
    );
    await context.close();
    console.log(
      `PASS: ${bad.checked} guide boxes line up under their time columns`,
    );
  }

  async function sessionsAndGuideHistory() {
    const context = await browser.newContext({
      timezoneId: "America/New_York",
    });
    const page = await context.newPage();
    await login(page);
    // The heading renders before session rows arrive. Hold the response until
    // the heading is visible so this regression also covers a slow sessions API.
    let releaseSessions;
    const sessionsReady = new Promise((resolve) => { releaseSessions = resolve; });
    const pendingSessions = async (route) => {
      await sessionsReady;
      await route.continue();
    };
    await page.route("**/api/auth/sessions", pendingSessions);
    await page.goto(url + "/account");
    await page
      .getByRole("heading", { name: "Sessions", exact: true })
      .waitFor();
    releaseSessions();
    assert(
      (await page
        .getByRole("columnheader", { name: "Type", exact: true })
        .count()) === 0,
      "Session Type column is removed",
    );
    await page.getByRole("cell", { name: "Linux", exact: true }).first().waitFor();
    await page.unroute("**/api/auth/sessions", pendingSessions);
    const toggle = page.getByRole("switch", {
      name: "Remove Inactive Sessions",
      exact: true,
    });
    const days = page.locator("#inactive-session-days");
    await page.waitForFunction(
      () => document.querySelector("#inactive-session-days")?.value === "14",
    );
    assert(
      (await toggle.getAttribute("aria-checked")) === "true",
      "Inactivity removal defaults On",
    );
    const saved = async (action) => {
      const response = page.waitForResponse(
        (r) =>
          r.url().endsWith("/api/auth/session-inactivity") &&
          r.request().method() === "PUT",
      );
      await action();
      const result = await response;
      assert(result.ok(), "Inactivity policy saves successfully");
      return result.json();
    };
    assert(
      (await saved(() => toggle.click())).enabled === false,
      "Cleanup can be disabled",
    );
    assert(
      await days.isDisabled(),
      "Inactive days are disabled with cleanup Off",
    );
    assert(
      (await saved(() => toggle.click())).enabled === true,
      "Cleanup can be enabled",
    );
    assert(
      (
        await saved(async () => {
          await days.fill("21");
          await days.blur();
        })
      ).days === 21,
      "Inactive days are configurable",
    );
    await saved(async () => {
      await days.fill("14");
      await days.blur();
    });
    const lifetime = page.locator("#account-days");
    for (const [field, endpoint, value, status] of [
      [lifetime, "session-lifetime", "31", "Sign-in lifetime saved"],
      [days, "session-inactivity", "15", "Inactivity policy saved"],
    ]) {
      let writes = 0;
      const listener = (request) => {
        if (
          request.url().endsWith("/api/auth/" + endpoint) &&
          request.method() === "PUT"
        )
          writes++;
      };
      page.on("request", listener);
      await field.fill(value);
      const before = await field.boundingBox();
      await page.waitForTimeout(3200);
      assert(writes === 0, "Day fields must not save while still focused");
      const response = page.waitForResponse(
        (r) =>
          r.url().endsWith("/api/auth/" + endpoint) &&
          r.request().method() === "PUT",
      );
      await field.blur();
      assert((await response).ok(), "Blur saves the day field");
      await page.getByRole("status", { name: status, exact: true }).waitFor();
      const after = await field.boundingBox();
      assert(
        before.x === after.x && before.width === after.width,
        "Save feedback must not shift the input",
      );
      await page.waitForTimeout(5200);
      assert(
        (await page
          .getByRole("status", { name: status, exact: true })
          .count()) === 0,
        "Save checkmark clears after five seconds",
      );
      page.off("request", listener);
    }
    const restoreLifetime = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/auth/session-lifetime") &&
        response.request().method() === "PUT",
    );
    await lifetime.fill("30");
    await lifetime.blur();
    assert(
      (await restoreLifetime).ok(),
      "Restore the sign-in lifetime for following scenarios",
    );
    await page.waitForFunction(
      async () =>
        (await (await fetch("/api/auth/session-lifetime")).json()).days === 30,
    );
    let deferFirstPolicySave = true;
    await page.route("**/api/auth/session-inactivity", async (route) => {
      if (route.request().method() === "PUT" && deferFirstPolicySave) {
        deferFirstPolicySave = false;
        const response = await route.fetch();
        await page.waitForTimeout(400);
        await route.fulfill({ response });
        return;
      }
      await route.continue();
    });
    const policyResponse = (enabled, value) =>
      page.waitForResponse((response) => {
        const request = response.request();
        if (
          !request.url().endsWith("/api/auth/session-inactivity") ||
          request.method() !== "PUT"
        )
          return false;
        const body = request.postDataJSON();
        return body.enabled === enabled && body.days === value;
      });
    const blurredSave = policyResponse(true, 16);
    const toggledSave = policyResponse(false, 16);
    await days.fill("16");
    await toggle.click();
    assert(
      (await blurredSave).ok(),
      "Blurred policy save completes before the toggle save",
    );
    assert(
      (await toggledSave).ok(),
      "Toggle save completes after the blurred policy save",
    );
    const racedPolicy = await (
      await page.request.get(url + "/api/auth/session-inactivity")
    ).json();
    assert(
      racedPolicy.enabled === false && racedPolicy.days === 16,
      "Rapid blur then toggle persists the latest policy",
    );
    await page.unroute("**/api/auth/session-inactivity");
    const restoredPolicy = policyResponse(true, 16);
    await toggle.click();
    assert(
      (await restoredPolicy).ok(),
      "Cleanup can be re-enabled after the race check",
    );
    const resetDays = policyResponse(true, 14);
    await days.fill("14");
    await days.blur();
    assert(
      (await resetDays).ok(),
      "Inactivity days can be restored after the race check",
    );

    const actual = await (
      await page.request.get(url + "/api/auth/sessions")
    ).json();
    const current = actual.find((session) => session.current);
    await page.route("**/api/auth/sessions", (route) =>
      route.fulfill({
        json: [
          {
            ...current,
            id: "old",
            current: false,
            deviceName: "Old device",
            lastActiveAt: "2026-10-01T00:00:00Z",
          },
          {
            ...current,
            id: "recent",
            current: false,
            deviceName: "Recent device",
            lastActiveAt: "2026-10-13T00:00:00Z",
          },
          {
            ...current,
            deviceName: "Current device",
            lastActiveAt: "2026-09-01T00:00:00Z",
          },
        ],
      }),
    );
    await page.reload();
    await page.locator(".session-current-pill").waitFor();
    const sessionHeaderAlignment = await page
      .locator(".sessions-table thead th")
      .evaluateAll((headers) =>
        headers.map((header) => getComputedStyle(header).textAlign),
      );
    assert(
      JSON.stringify(sessionHeaderAlignment) ===
        JSON.stringify(["left", "left", "left", "left", "center"]),
      "Session headings align with their content, except centered sign-out actions",
    );
    const sessionTextAlignment = await page
      .locator(".sessions-table")
      .evaluate((table) =>
        [...table.tHead.rows[0].cells].slice(0, 4).map((header, index) => {
          const textStart = (cell) => {
            const range = document.createRange();
            range.selectNodeContents(cell);
            return range.getBoundingClientRect().left;
          };
          return Math.abs(
            textStart(header) -
              textStart(table.tBodies[0].rows[0].cells[index]),
          );
        }),
      );
    assert(
      sessionTextAlignment.every((offset) => offset <= 1),
      "Session header text starts above its column content: " +
        JSON.stringify(sessionTextAlignment),
    );
    const sessionActionAlignment = await page
      .locator(".sessions-table")
      .evaluate((table) => {
        const header = table.tHead.rows[0].cells[4];
        const pill = table.querySelector(".session-current-pill");
        const range = document.createRange();
        range.selectNodeContents(header);
        const heading = range.getBoundingClientRect();
        const action = pill.getBoundingClientRect();
        return Math.abs(
          (heading.left + heading.right) / 2 - (action.left + action.right) / 2,
        );
      });
    assert(
      sessionActionAlignment <= 1,
      "This Session pill centers under the Sign Out heading: " +
        sessionActionAlignment,
    );
    const rowsText = await page
      .locator(".sessions-table tbody tr td:first-child")
      .allTextContents();
    assert(
      JSON.stringify(rowsText.map((text) => text.trim())) ===
        JSON.stringify(["Current device", "Recent device", "Old device"]),
      "Current session first, then newest activity",
    );
    assert(
      (await page
        .locator(".sessions-table tbody tr")
        .first()
        .getByRole("button")
        .count()) === 0,
      "Current session has no clickable sign-out control",
    );
    await page.locator("#inactive-session-days").scrollIntoViewIfNeeded();
    for (const width of [320, 375, 600, 1280]) {
      await page.setViewportSize({ width, height: 900 });
      const layout = await page.evaluate(() => {
        const input = document
          .querySelector("#inactive-session-days")
          .getBoundingClientRect();
        const toggle = document
          .querySelector(
            '[role="switch"][aria-label="Remove Inactive Sessions"]',
          )
          .getBoundingClientRect();
        const controls = [
          ...document.querySelectorAll(".session-policy-controls"),
        ].map((el) => el.getBoundingClientRect());
        return {
          overflow: document.documentElement.scrollWidth > innerWidth,
          right: toggle.x > input.right,
          fits: controls.every(
            (rect) => rect.right <= innerWidth && rect.left >= 0,
          ),
        };
      });
      assert(
        !layout.overflow && layout.right && layout.fits,
        `Session controls fit at ${width}px: ${JSON.stringify(layout)}`,
      );
      await page.screenshot({ path: `/tmp/alreadyon-sessions-${width}.png` });
    }
    await page.screenshot({ path: "/tmp/alreadyon-sessions-settings.png" });
    const channels = await (await page.request.get(url + "/api/guide")).json();
    const base = channels[0];
    const fixed = Date.parse("2026-10-15T01:48:00Z");
    const stamp = (hour, minute) =>
      new Date(
        Date.parse("2026-10-15T00:00:00Z") + hour * 3600000 + minute * 60000,
      ).toISOString();
    const program = (title, start, end) => ({
      ...base.programs[0],
      kind: "program",
      mediaType: "episode",
      title,
      seriesTitle: title,
      episodeTitle: "An exceptionally long previous episode title for clipping",
      seasonNumber: 1,
      episodeNumber: 2,
      startsAt: start,
      endsAt: end,
    });
    const rows = [
      {
        ...base,
        number: 998,
        previousPrograms: [
          program(
            "Wide ended show with a long title",
            stamp(1, 15),
            stamp(1, 45),
          ),
        ],
        programs: [program("Current show", stamp(1, 46), stamp(2, 15))],
      },
      {
        ...base,
        number: 999,
        previousPrograms: [
          program("Skinny ended show", stamp(1, 0), stamp(1, 31)),
        ],
        programs: [program("Current second show", stamp(1, 32), stamp(2, 2))],
      },
    ];
    await page.clock.install({ time: fixed });
    await page.route("**/api/guide**", (route) =>
      route.fulfill({ json: rows }),
    );
    await page.goto(url);
    await page.locator(".guide-slot-history").first().waitFor();
    const cells = page.locator(".guide-slot-history");
    assert(
      (await cells.count()) === 2,
      "Ended airings have separate display cells",
    );
    assert(
      (await cells.nth(0).textContent()).includes("Wide ended show"),
      "Wide history cell includes title",
    );
    assert(
      (
        await cells.nth(0).locator(".guide-slot-episode").textContent()
      ).includes("S01E02"),
      "Wide ended episode includes season/episode text",
    );
    assert(
      (await cells.nth(1).textContent()).trim() === "",
      "Skinny history cell omits all text",
    );
    const metrics = await page.evaluate(() => {
      const cells = [...document.querySelectorAll(".guide-slot-history")];
      const slot = document.querySelector(".guide-head-slot");
      const time = cells[0].querySelector(".guide-slot-time");
      return {
        origin: Number(slot.dataset.startMs),
        slotWidth: slot.getBoundingClientRect().width,
        widths: cells.map((c) => c.getBoundingClientRect().width),
        left: cells.map((c) => parseFloat(c.style.left)),
        timeFits: time.scrollWidth <= time.clientWidth,
        grey: getComputedStyle(cells[0]).backgroundColor,
        current: getComputedStyle(document.querySelector(".guide-slot-box"))
          .backgroundColor,
      };
    });
    assert(
      metrics.origin === Date.parse(stamp(1, 30)),
      "First column remains the current half hour",
    );
    assert(
      Math.abs(metrics.widths[0] - (Math.round(metrics.slotWidth / 2) - 6)) <=
        2,
      "Wide history is clipped to fifteen real minutes",
    );
    assert(
      metrics.widths[1] < metrics.widths[0] / 4,
      "Skinny ended airing preserves its real narrow width",
    );
    assert(
      metrics.left.every((left) => left === 3),
      "Ended cells clip at the visible slot origin",
    );
    assert(
      metrics.timeFits && metrics.grey !== metrics.current,
      "Ended time fits and grey distinguishes history",
    );
    await page.screenshot({ path: "/tmp/alreadyon-guide-history-web.png" });
    await context.close();
    console.log(
      "PASS: device labels, inactivity settings and grey clipped previous airings with width-based text",
    );
  }

  async function profileRefresh() {
    const context = await browser.newContext();
    const page = await context.newPage();
    await page.clock.install();
    await login(page);
    const list = await (await page.request.get(url + "/api/profiles")).json();
    const active = list[0];
    const refreshed = {
      ...active,
      name: "Remote profile update",
      icon: "owl",
      guideTimeFormat: "24h",
    };
    let polls = 0;
    await page.route("**/api/profiles", (route) => {
      polls++;
      return route.fulfill({
        json: list.map((p) => (p.id === active.id ? refreshed : p)),
      });
    });
    await page.clock.fastForward(5 * 60 * 1000);
    await page
      .locator(".profile-menu-trigger")
      .filter({ hasText: refreshed.name })
      .waitFor();
    assert(
      polls >= 1,
      "Visible WebUI polls profiles at five minutes without a focus event",
    );
    assert(
      await page
        .locator(".profile-menu-trigger")
        .textContent()
        .then((t) => t.includes("🦉")),
      "Remote avatar preset reaches active profile",
    );
    await page.goto(url + "/profile");
    const nameInput = page
      .locator(".admin-card")
      .filter({
        has: page.getByRole("heading", { name: "Display Name", exact: true }),
      })
      .locator("input");
    await nameInput.fill(""); // Incomplete input is not autosaved.
    refreshed.name = "Second remote update";
    refreshed.icon = "/profile-icons/synthetic-refresh.png";
    await page.route("**/profile-icons/synthetic-refresh.png", (route) =>
      route.fulfill({
        status: 200,
        contentType: "image/png",
        body: Buffer.from(
          "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jWZkAAAAASUVORK5CYII=",
          "base64",
        ),
      }),
    );
    await page.clock.fastForward(5 * 60 * 1000);
    await page.locator(".profile-menu-trigger img").waitFor();
    assert(
      (await page.locator(".profile-menu-trigger img").getAttribute("src")) ===
        refreshed.icon,
      "Uploaded avatar changes reach the active profile",
    );
    assert(
      (await nameInput.inputValue()) === "",
      "Polling preserves incomplete local profile edits",
    );
    await context.close();
    console.log(
      "PASS: active WebUI refreshes remote profile name/avatar every five minutes and preserves drafts",
    );
  }

  // Browser fixtures verify visibility without changing any persistent profile.
  async function profileMenuRoles() {
    for (const accountRole of ["owner", "viewer"]) {
      for (const profileRole of ["admin", "viewer"]) {
        const context = await browser.newContext();
        const page = await context.newPage();
        await login(page);
        const profiles = await (
          await page.request.get(url + "/api/profiles")
        ).json();
        const profile = {
          ...profiles[0],
          name: "A deliberately long household profile name for header layout",
          role: profileRole,
        };
        await page.addInitScript(
          (id) => localStorage.setItem("alreadyon.currentUserId", String(id)),
          profile.id,
        );
        await page.route("**/api/auth/status", (route) =>
          route.fulfill({
            json: {
              setupRequired: false,
              authenticated: true,
              role: accountRole,
              username: "menu-fixture",
              mustChangePassword: false,
            },
          }),
        );
        await page.route("**/api/profiles", (route) =>
          route.fulfill({ json: [profile] }),
        );
        const expected = [
          "Switch Profile",
          "Profile",
          ...(profileRole === "admin" ? ["Settings"] : []),
          ...(accountRole === "owner" ? ["Server"] : []),
          "Sign Out",
        ];
        const routes = [
          "/",
          "/profile",
          ...(profileRole === "admin" ? ["/account"] : []),
          ...(accountRole === "owner" ? ["/admin/accounts"] : []),
        ];
        // Check real navigation, both hit targets, keyboard access and long-name
        // containment. Fixtures change browser responses, never stored profiles.
        for (const width of [1280, 360]) {
          await page.setViewportSize({ width, height: 900 });
          for (const route of routes) {
            await page.goto(url + route);
            const trigger = page.locator(".profile-menu-trigger");
            await trigger.waitFor();
            assert(
              (await trigger.count()) === 1,
              "One profile trigger per page",
            );
            assert(
              (await trigger
                .locator(".profile-menu-trigger-name")
                .textContent()) === profile.name,
              "Selected name remains visible",
            );
            await trigger.locator("span").first().click();
            const actual = (
              await page.locator(".profile-menu-item").allTextContents()
            ).map((s) => s.trim());
            assert(
              JSON.stringify(actual) === JSON.stringify(expected),
              `${accountRole}/${profileRole} ${route} menu: ${actual}`,
            );
            await page.keyboard.press("Escape");
            await trigger.locator(".profile-menu-trigger-name").click();
            await page.locator(".profile-menu-panel").waitFor();
            await page.keyboard.press("Escape");
            await trigger.focus();
            await trigger.press("Enter");
            await page.locator(".profile-menu-panel").waitFor();
            assert(
              (await trigger.getAttribute("aria-expanded")) === "true",
              "Keyboard opens profile menu",
            );
            assert(
              await page.evaluate(
                () => document.documentElement.scrollWidth <= innerWidth + 1,
              ),
              "Long-name header remains viewport-bound",
            );
            await page.keyboard.press("Escape");
          }
        }
        await page.goto(url);
        await page.locator(".profile-menu-trigger").click();
        if (accountRole === "owner") {
          await page
            .getByRole("button", { name: "Server", exact: true })
            .click();
          await page.waitForURL("**/admin/dashboard");
          await page
            .getByRole("heading", { name: "Server Metrics", exact: true })
            .waitFor();
          assert(
            (await page.locator(".admin-tab").first().innerText()) ===
              "Dashboard",
            "Dashboard is the first Server tab",
          );
        } else {
          await page.goto(url + "/admin/accounts");
          await page.waitForURL(url + "/");
        }
        await context.close();
      }
    }
    console.log(
      "PASS: profile menu order, role permissions and owner Dashboard navigation",
    );
  }

  // Admin: activity cards precede server performance and the settings grid.
  async function adminTabs() {
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    await login(page);
    await page.goto(url + "/admin/users");
    await page.waitForURL("**/account");
    await page.locator("#account-username").waitFor();
    assert(
      (await page
        .getByRole("button", { name: "Profiles", exact: true })
        .count()) === 0,
      "Profiles are managed on Admin Settings, not a Server Settings tab",
    );
    const headings = await page.locator(".admin-card h3").allTextContents();
    assert(
      JSON.stringify(headings) === JSON.stringify(["Profiles", "Sessions"]),
      `Admin Settings should show the account form with TV Pairing Pin, Profiles, then Sessions: ${JSON.stringify(headings)}`,
    );
    await page
      .getByRole("columnheader", { name: "Last Active", exact: true })
      .waitFor();
    const sessions = await (
      await page.request.get(url + "/api/auth/sessions")
    ).json();
    assert(
      sessions.some(
        (s) => s.current && Number.isFinite(Date.parse(s.lastActiveAt)),
      ),
      "Current session should report its last activity",
    );
    await page.goto(url + "/admin");
    await page.waitForURL("**/admin/dashboard");
    const tabs = await page.locator(".admin-tab").allTextContents();
    assert(
      tabs.join(",") ===
        "Dashboard,Home Users,Library Paths,Manage Media,Channels,Server,Logs,API",
      "Settings tab order: " + tabs,
    );
    await page.goto(url + "/dashboard");
    await page
      .getByRole("heading", { name: "Server Metrics", exact: true })
      .waitFor();
    assert(
      (
        await page.locator(".admin-card h2, .admin-card h3").allTextContents()
      ).join(",") ===
        "Active Streaming Sessions,Active Background Processes,Server Metrics",
      "Dashboard card order",
    );
    await page.setViewportSize({ width: 1100, height: 800 });
    assert(
      (await page.locator(".admin-tabs").count()) === 1,
      "Dashboard belongs to Server navigation",
    );
    const graphLayout = await page
      .locator(".metrics-grid .metric-panel")
      .evaluateAll((panels) =>
        panels.map((panel) => ({
          left: Math.round(panel.getBoundingClientRect().left),
          top: Math.round(panel.getBoundingClientRect().top),
        })),
      );
    assert(
      new Set(graphLayout.map((box) => box.top)).size === 4 &&
        new Set(graphLayout.map((box) => box.left)).size === 1,
      "Split-screen metrics stack on one aligned column: " +
        JSON.stringify(graphLayout),
    );
    const chartWidths = await page
      .locator(".metrics-grid")
      .evaluate((grid) => ({
        available: grid.getBoundingClientRect().width,
        panels: [...grid.querySelectorAll(".metric-panel")].map(
          (panel) => panel.getBoundingClientRect().width,
        ),
      }));
    assert(
      chartWidths.panels.every(
        (width) => Math.abs(width - chartWidths.available) < 2,
      ),
      "Split-screen charts fill the available card width: " +
        JSON.stringify(chartWidths),
    );
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto(url + "/admin/channels");
    await page
      .getByRole("heading", { name: "Age Restrictions", exact: true })
      .waitFor();
    for (const [title, action] of [
      ["Age Restrictions", "+ Add Age Restriction"],
      ["Date Range", "+ Add Date Range"],
    ]) {
      const card = page.locator(".admin-card").filter({
        has: page.getByRole("heading", { name: title, exact: true }),
      });
      assert((await card.count()) === 1, title + " has its own card");
      const heading = await card
        .getByRole("heading", { name: title, exact: true })
        .boundingBox();
      const button = await card
        .getByRole("button", { name: action, exact: true })
        .boundingBox();
      assert(
        Math.abs(
          heading.y + heading.height / 2 - button.y - button.height / 2,
        ) < 5,
        title + " add button is aligned",
      );
      const headers = await card
        .locator(".filter-definition-table th")
        .allTextContents();
      assert(
        headers[0] === "Actions" && headers[1] === "Name",
        title + " puts icon actions before Name",
      );
      const firstActions = card.locator(".filter-definition-actions").first();
      // Shared definitions use the same table layout. Both
      // kinds share this row renderer, so inspect actions whenever a row exists.
      if (await firstActions.count()) {
        const iconTops = await firstActions
          .locator("button")
          .evaluateAll((buttons) =>
            buttons.map((button) =>
              Math.round(button.getBoundingClientRect().top),
            ),
          );
        assert(
          new Set(iconTops).size === 1 &&
            (await firstActions.getByTitle("Edit filter").count()) === 1 &&
            (await firstActions.getByTitle("Delete filter").count()) === 1,
          title + " uses horizontal icon actions with tooltips",
        );
      }
    }
    assert(
      (await page
        .getByRole("heading", { name: "Library Coverage" })
        .count()) === 0,
      "Coverage removed",
    );
    const selectAll = page.getByRole("button", {
      name: "Select All",
      exact: true,
    });
    const deselectAll = page.getByRole("button", {
      name: "Deselect All",
      exact: true,
    });
    assert(
      (await selectAll.evaluate((el) => el.closest("th").colSpan)) === 1,
      "Bulk links occupy the selection column",
    );
    const selectBox = await selectAll.boundingBox(),
      deselectBox = await deselectAll.boundingBox();
    assert(
      Math.abs(deselectBox.y - selectBox.y) < 2 && deselectBox.x > selectBox.x,
      "Bulk links share one line",
    );
    assert(
      (await page
        .locator(".configured-channels-table .table-filter-row")
        .count()) === 0,
      "Configure Channels has no filter row",
    );
    await selectAll.click();
    const boxes = page.locator(
      '.configured-channels-table tbody input[type="checkbox"]',
    );
    assert(
      (await boxes.count()) > 0 &&
        (await boxes.evaluateAll((nodes) => nodes.every((n) => n.checked))),
      "Select All selects visible channels",
    );
    await page
      .getByRole("button", { name: "Deselect All", exact: true })
      .click();
    assert(
      await boxes.evaluateAll((nodes) => nodes.every((n) => !n.checked)),
      "Deselect All clears channels",
    );
    const originalFolders = await (
      await page.request.get(url + "/api/settings/folders")
    ).json();
    const initialFolder = path.join(root, "initial-commercials");
    fs.mkdirSync(initialFolder, { recursive: true });
    const initialSave = await page.request.put(url + "/api/settings/folders", {
      data: { commercials: [initialFolder] },
    });
    assert(initialSave.ok(), "Prepared an isolated folder for removal");
    await page.goto(url + "/admin/commercials");
    const breaks = page.locator(".commercial-settings");
    await breaks
      .getByRole("button", { name: "Save Changes", exact: true })
      .waitFor();
    assert(
      (await breaks
        .getByRole("button", { name: "Cancel", exact: true })
        .count()) === 1,
      "Break actions inside card",
    );
    assert(
      (await breaks.getByText(/Breaks never land/).count()) === 0,
      "Timing hint removed",
    );
    const labelLines = await breaks
      .locator(".admin-field-row > label")
      .evaluateAll((labels) =>
        labels.map((el) => {
          const range = document.createRange();
          range.selectNodeContents(el);
          return range.getClientRects().length;
        }),
      );
    assert(
      labelLines.every((lines) => lines === 1),
      "Commercial labels stay on one line: " + JSON.stringify(labelLines),
    );
    const folders = page.locator(".admin-card").filter({
      has: page.getByRole("heading", {
        name: "Commercial Folders",
        exact: true,
      }),
    });
    await folders.waitFor();
    const savedFolders = await (
      await page.request.get(url + "/api/settings/folders")
    ).json();
    // Keep these mutations in the disposable fixture, preserving the break draft.
    await page.getByLabel("Commercial Volume Offset (dB)").fill("-3");
    const folderSaved = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/settings/folders") &&
        r.request().method() === "PUT",
    );
    await folders
      .getByRole("button", { name: "Remove", exact: true })
      .first()
      .click();
    assert((await folderSaved).ok(), "Folder removal persisted immediately");
    assert(
      (await page.getByLabel("Commercial Volume Offset (dB)").inputValue()) ===
        "-3",
      "Immediate folder save preserves break draft",
    );
    const pathsAfter = await (
      await page.request.get(url + "/api/settings/folders")
    ).json();
    assert(
      pathsAfter.commercials.paths.length ===
        savedFolders.commercials.paths.length - 1,
      "Folder removal stored without Save Changes",
    );
    // Failed persistence must leave the previously saved list on screen.
    await page.route("**/api/settings/folders", (route) =>
      route.request().method() === "PUT"
        ? route.fulfill({
            status: 500,
            json: { error: "Synthetic folder failure" },
          })
        : route.continue(),
    );
    await folders
      .getByRole("button", { name: "Reset To Default", exact: true })
      .click();
    await page.getByText("Synthetic folder failure", { exact: true }).waitFor();
    assert(
      (await folders.locator(".folder-path").count()) ===
        pathsAfter.commercials.paths.length,
      "Failed update retains saved folder list",
    );
    await page.unroute("**/api/settings/folders");
    const addedPath = path.join(root, "empty-commercials");
    fs.mkdirSync(addedPath, { recursive: true });
    await folders
      .getByRole("button", { name: "+ Add folder", exact: true })
      .click();
    const folderBrowser = page.getByRole("dialog", {
      name: "Add folder — Commercial Folders",
      exact: true,
    });
    await folderBrowser.getByLabel("Folder path").fill(addedPath);
    await folderBrowser
      .getByRole("button", { name: "Go", exact: true })
      .click();
    await folderBrowser
      .getByText("No subfolders. You can select this folder.", { exact: true })
      .waitFor();
    const added = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/settings/folders") &&
        r.request().method() === "PUT",
    );
    await folderBrowser
      .getByRole("button", { name: "Select Folder", exact: true })
      .click();
    assert((await added).ok(), "Folder addition persisted immediately");
    await folders.getByText(addedPath, { exact: true }).waitFor();
    await breaks.getByRole("button", { name: "Cancel", exact: true }).click();
    await folders.getByText(addedPath, { exact: true }).waitFor();
    assert(
      (await page.getByLabel("Commercial Volume Offset (dB)").inputValue()) !==
        "-3",
      "Cancel discards break draft while keeping saved folders",
    );
    const folderRuns = () => {
      const log = fs.readFileSync(path.join(root, "server.log"), "utf8");
      return {
        started: (log.match(/content folders changed: rescanning/g) ?? []).length,
        finished: (log.match(/content folders changed: rescan complete/g) ?? []).length,
      };
    };
    const beforeRestore = folderRuns();
    const restoredFolders = await page.request.put(url + "/api/settings/folders", {
      data: {
        commercials:
          originalFolders.commercials.source === "default"
            ? null
            : originalFolders.commercials.paths.map((p) => p.path),
      },
    });
    assert(restoredFolders.ok(), "Restore the original synthetic folder settings");
    // A successful folder save queues a debounced rescan. Drain that exact
    // restore before later scenarios reseed schedules; otherwise it can erase
    // their guide after login has already started on a slower CI runner.
    const rescanDeadline = Date.now() + 120000;
    for (;;) {
      const runs = folderRuns();
      if (runs.started > beforeRestore.started && runs.finished >= runs.started) break;
      assert(Date.now() < rescanDeadline, "Restored folder rescan did not finish");
      await delay(100);
    }
    await page.goto(url + "/admin/server");
    await page.locator(".settings-grid").waitFor();
    assert(
      (await page.locator(".admin-card").count()) === 2,
      "Server should show build details and settings",
    );
    await page.getByText("Server Version", { exact: true }).waitFor();
    const details = await (
      await page.request.get(url + "/api/server/details")
    ).json();
    assert(
      details.uptimeSeconds >= 0 &&
        details.appVersion &&
        details.timeZone &&
        details.ffmpegVersion !== "Unavailable",
      "Server identity should report real metadata",
    );
    const restartBox = page.locator(".admin-card").first();
    await restartBox
      .getByRole("heading", { name: "Server Details", exact: true })
      .waitFor();
    assert(
      (await restartBox
        .locator(".folder-card-header .admin-btn:not(.admin-btn-secondary)")
        .count()) === 1,
      "Restart uses primary styling in the header",
    );
    // Loopback and virtual interfaces are intentionally included; only IPv6 is excluded.
    assert(
      details.ipAddresses.every(isIPv4),
      "Server details must contain valid IPv4 addresses",
    );
    if (details.ipAddresses.length)
      await restartBox
        .getByText(details.ipAddresses.join(", "), { exact: true })
        .waitFor();
    assert(details.appVersion.includes("_"), "Version uses underscore");
    assert(
      (await restartBox
        .getByRole("button", { name: "Restart Server", exact: true })
        .count()) === 1 &&
        (await restartBox
          .getByRole("button", { name: "View Logs", exact: true })
          .count()) === 0,
      "Only restart remains in Server Details",
    );
    assert(
      (await page
        .locator(".admin-card")
        .last()
        .locator(".settings-grid")
        .count()) === 1,
      "The settings box should follow the restart box",
    );
    const names = await page
      .locator(".setting-row:not(.setting-sub) .setting-name")
      .allTextContents();
    assert(
      JSON.stringify(names) ===
        JSON.stringify([
          "Server Name",
          "Hardware Encoding",
          "Hardware Decoding",
          "Loudness Detection",
          "TMDb API Key",
          "Dynamic DNS",
          "TLS Certificates",
        ]),
      `Settings out of order: ${JSON.stringify(names)}`,
    );
    // More Info opens a centered modal for that setting, and Escape closes it.
    await page
      .locator(".setting-row", { hasText: "Hardware Encoding" })
      .first()
      .getByRole("button", { name: "More Info" })
      .click();
    const modal = page.getByRole("dialog", { name: "Hardware Encoding" });
    await modal.waitFor();
    const box = await modal.boundingBox();
    const view = page.viewportSize();
    assert(
      Math.abs(box.x + box.width / 2 - view.width / 2) < 4 &&
        Math.abs(box.y + box.height / 2 - view.height / 2) < 40,
      `The modal should be centered: ${JSON.stringify({ box, view })}`,
    );
    await page.keyboard.press("Escape");
    await modal.waitFor({ state: "detached" });
    // A hardware choice can be tested; the result says whether it works here, and Reset Default goes back.
    const row = page
      .locator(".setting-row", { hasText: "Hardware Encoding" })
      .first();
    const decodeRow = page
      .locator(".setting-row", { hasText: "Hardware Decoding" })
      .first();
    assert(
      (await row.getByRole("button", { name: "Test" }).count()) === 0,
      "Software needs no test button",
    );
    // Hardware Decoding needs a hardware encoder: locked with software, and software turns it back off.
    assert(
      (await decodeRow.locator("select").isEnabled()) === false,
      "Hardware Decoding must wait for a hardware encoder",
    );
    await decodeRow.getByRole("button", { name: "More Info" }).click();
    await page.getByRole("dialog", { name: "Hardware Decoding" }).waitFor();
    await page.keyboard.press("Escape");
    await decodeRow.locator("select").waitFor();
    await row.locator("select").selectOption("intel");
    assert(
      await decodeRow.locator("select").isEnabled(),
      "Hardware Decoding should unlock with a hardware encoder",
    );
    assert(
      (await decodeRow.getByRole("button", { name: "Test" }).count()) === 0,
      "Disabled decoding needs no test button",
    );
    assert(
      (await decodeRow.locator("option").allTextContents()).join("|") ===
        "Disabled (Default)|Enabled",
      "Decoding options should be Disabled (Default) and Enabled",
    );
    await decodeRow.locator("select").selectOption("on");
    await row.getByRole("button", { name: "Test" }).click();
    await row
      .locator(".setting-note", { hasText: /^[✓✗]/ })
      .waitFor({ timeout: 30000 });
    await decodeRow.getByRole("button", { name: "Test" }).click();
    await decodeRow
      .locator(".setting-note", { hasText: /^[✓✗]/ })
      .waitFor({ timeout: 30000 });
    await row.getByRole("button", { name: "Reset Default" }).click();
    assert(
      (await row.locator("select").inputValue()) === "software",
      "Reset Default should choose software",
    );
    assert(
      (await decodeRow.locator("select").inputValue()) === "off" &&
        (await decodeRow.locator("select").isEnabled()) === false,
      "Software should turn Hardware Decoding off",
    );
    // Changes enable Save Changes, and Save persists them across a reload.
    const save = page.getByRole("button", { name: "Save Changes" });
    assert(await save.isDisabled(), "Save Changes should wait for a change");
    await row.locator("select").selectOption("intel");
    await decodeRow.locator("select").selectOption("on");
    await page
      .getByLabel("Server Name", { exact: true })
      .fill("Family E2E Library");
    assert(await save.isEnabled(), "Save Changes should enable after a change");
    await Promise.all([page.waitForNavigation(), save.click()]);
    await page.locator(".settings-grid").waitFor();
    assert(
      (await row.locator("select").inputValue()) === "intel" &&
        (await decodeRow.locator("select").inputValue()) === "on",
      "Saved Hardware Encoding/Decoding should survive a reload",
    );
    assert(
      (await page.getByLabel("Server Name", { exact: true }).inputValue()) ===
        "Family E2E Library",
      "Friendly server name survives a reload",
    );
    // Browser fixtures run through a proxy; LAN pairing metadata deliberately rejects proxy headers.
    // Direct HTTPS name propagation is covered by server_name_test.go instead.
    const namedSettings = await (
      await page.request.get(url + "/api/settings/server")
    ).json();
    assert(
      namedSettings.serverName === "Family E2E Library",
      "Settings API immediately uses the renamed server",
    );
    assert(
      await save.isDisabled(),
      "Save Changes should be off again after saving",
    );
    await row.getByRole("button", { name: "Reset Default" }).click();
    await Promise.all([page.waitForNavigation(), save.click()]);
    await page.locator(".settings-grid").waitFor();
    assert(
      (await row.locator("select").inputValue()) === "software" &&
        (await decodeRow.locator("select").inputValue()) === "off",
      "Resetting and saving should persist software",
    );
    if (process.env.SETTINGS_SCREENSHOT_DIR) {
      fs.mkdirSync(process.env.SETTINGS_SCREENSHOT_DIR, { recursive: true });
      for (const tab of [
        "dashboard",
        "users",
        "commercials",
        "channels",
        "server",
      ]) {
        await page.goto(url + "/admin/" + tab);
        await page.locator(".admin-card").first().waitFor();
        await page.evaluate(() => document.fonts.ready);
        await delay(500);
        await page.screenshot({
          path: path.join(process.env.SETTINGS_SCREENSHOT_DIR, tab + ".png"),
          fullPage: true,
        });
      }
    }
    await context.close();
    console.log(
      "PASS: Admin Settings, Accounts navigation and Server activity/settings cards with More Info modals and a transcoding test",
    );
  }

  // Both startup benchmarks spawn FFmpeg; finish them before timing-sensitive playback/cleanup checks.
  async function waitForTuner() {
    seed(); // The previous scenario may have exhausted its short schedule.
    const context = await browser.newContext();
    const page = await context.newPage();
    await login(page);
    await leave(page);
    const began = Date.now();
    for (;;) {
      const status = await (
        await context.request.get(url + "/api/settings/transcoding/threads")
      ).json();
      const capacity = await (
        await context.request.get(url + "/api/settings/transcoding/capacity")
      ).json();
      if (status.state === "idle" && capacity.state === "idle") {
        assert(
          status.threads >= 2 && status.cpus >= 1,
          "Unexpected thread tuning status: " + JSON.stringify(status),
        );
        console.log(
          `PASS: thread and capacity measurements finished (${status.threads} of ${status.cpus} threads) in ${Math.round((Date.now() - began) / 1000)}s more`,
        );
        break;
      }
      assert(
        Date.now() - began < 780000,
        "Startup thread/capacity measurements never finished",
      );
      await new Promise((r) => setTimeout(r, 1000));
    }
    await noWorkers();
    await context.close();
    seed(); // Benchmarks can outlast the disposable 90-second guide.
  }

  // Profile Settings hold display preferences, the Channels tab has per-channel Auto/Show/Hide,
  // the Admin Account box sets how long browsers stay signed in, and Logs remain accessible through their tab.
  async function settingsUi() {
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    await login(page);
    // Exercise the width cap with a legitimate long name; intrinsic grid tracks
    // can exceed the capped item when a runner's font metrics are wider.
    const headerProfiles = await (
      await page.request.get(url + "/api/profiles")
    ).json();
    await page.route("**/api/profiles", (route) =>
      route.fulfill({
        json: headerProfiles.map((profile) => ({
          ...profile,
          name: "An extraordinarily long profile name for layout",
        })),
      }),
    );
    const assertSettingsHeaders = async (width) => {
      await page.setViewportSize({ width, height: 844 });
      for (const route of [
        "/admin/dashboard",
        "/admin/accounts",
        "/admin/media",
        "/admin/commercials",
        "/admin/channels",
        "/admin/server",
        "/admin/logs",
        "/admin/api",
        "/account",
        "/profile",
      ]) {
        await page.goto(url + route);
        const menu = page.locator(".admin-topbar-inner > .profile-menu");
        await menu.waitFor();
        const trigger = menu.getByRole("button");
        assert(
          await trigger.isVisible(),
          `${route} profile menu trigger is visible`,
        );
        assert(
          (await page.locator(".profile-menu-panel").count()) === 0,
          `${route} menu starts closed`,
        );
        assert(
          await menu.locator(".profile-menu-trigger-name").isVisible(),
          `${route} header retains the avatar and single-line name`,
        );
        if (route === "/account" || route === "/admin/accounts") {
          const table = page.locator(
            route === "/account" ? ".profiles-table" : ".accounts-table",
          );
          assert(
            (await table
              .locator("th")
              .first()
              .evaluate((el) => getComputedStyle(el).whiteSpace)) === "nowrap",
            `${route} table headings keep whole words on compact screens`,
          );
          assert(
            (await table.locator("..").getAttribute("tabindex")) === "0",
            `${route} table scroll region is keyboard accessible`,
          );
        }
        const contentBefore = await page
          .locator(".admin-content")
          .boundingBox();
        await trigger.click();
        const panel = page.locator(".profile-menu-panel");
        await panel.waitFor();
        const alignment = await page.evaluate(() => {
          const header = document.querySelector(".admin-topbar-inner");
          const menu = header.querySelector(":scope > .profile-menu");
          const panel = menu.querySelector(".profile-menu-panel");
          const menuBox = menu.getBoundingClientRect();
          const panelBox = panel.getBoundingClientRect();
          const headerBox = header.getBoundingClientRect();
          const contentBox = document
            .querySelector(".admin-content")
            .getBoundingClientRect();
          return {
            menuRight: menuBox.right,
            headerRight: headerBox.right - parseFloat(getComputedStyle(header).paddingRight),
            panelLeft: panelBox.left,
            panelRight: panelBox.right,
            contentTop: contentBox.top,
            contentWidth: contentBox.width,
            viewport: document.documentElement.clientWidth,
            scrollWidth: document.documentElement.scrollWidth,
          };
        });
        assert(
          Math.abs(alignment.menuRight - alignment.headerRight) < 2 &&
            alignment.panelLeft >= 0 &&
            alignment.panelRight <= alignment.viewport + 1 &&
            Math.abs(alignment.contentTop - contentBefore.y) < 1 &&
            Math.abs(alignment.contentWidth - contentBefore.width) < 1 &&
            alignment.menuRight <= alignment.viewport + 1 &&
            alignment.scrollWidth <= alignment.viewport + 1,
          `${route} profile menu stays aligned and in the ${width}px viewport: ` +
            JSON.stringify(alignment),
        );
      }
    };
    await assertSettingsHeaders(320);
    await assertSettingsHeaders(390);
    await assertSettingsHeaders(768);
    await assertSettingsHeaders(1280);
    await page.unroute("**/api/profiles");
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.goto(url + "/");
    await page.locator(".guide-slot-box").first().waitFor();
    assert(
      (await page.locator(".page-header select").count()) === 0,
      "The Theme picker should have left the guide header",
    );
    assert(
      !/^Movie$/.test(
        (await page.locator(".guide-slot-episode").first().innerText()).trim(),
      ),
      "A movie box shows its content rating, not the word Movie",
    );

    await page.goto(url + "/profile");
    const field = (label) =>
      page.locator(".admin-field-row", { hasText: label }).locator("select");
    assert((await field("Theme").count()) === 0, "Appearance is fixed");
    const timeSelect = field("Time Format");
    const currentTime = await timeSelect.inputValue();
    await timeSelect.selectOption(currentTime === "24h" ? "12h" : "24h");
    await page
      .getByRole("status", { name: "guideTimeFormat saved", exact: true })
      .waitFor();
    await page.reload();
    assert(
      (await field("Time Format").inputValue()) !== currentTime,
      "Time format autosave survives reload",
    );
    await field("Time Format").selectOption(currentTime);
    await page
      .getByRole("status", { name: "guideTimeFormat saved", exact: true })
      .waitFor();

    await page.goto(url + "/admin/channels");
    await page.locator("table.admin-table").last().waitFor();
    const heads = await page
      .locator("table.admin-table")
      .last()
      .locator("th")
      .allTextContents();
    assert(
      !heads.includes("Visibility"),
      "The Visibility column should be gone",
    );
    for (const name of ["Hide Selected", "Show Selected", "Set to Auto"]) {
      assert(
        await page.getByRole("button", { name, exact: true }).isDisabled(),
        `${name} needs a checked channel`,
      );
    }
    const row = page.locator("tbody tr", { hasText: "Synthetic channel 998" });
    await row.locator("input[type=checkbox]").check();
    assert(
      await page
        .getByRole("button", { name: "Hide Selected", exact: true })
        .isEnabled(),
      "Hide Selected should enable with a checked channel",
    );
    await row.locator("input[type=checkbox]").uncheck();
    // Table modals scroll only their table; the title, Name and buttons stay in view.
    await page.setViewportSize({ width: 1280, height: 560 });
    await page
      .getByRole("button", { name: "+ Add Age Restriction", exact: true })
      .click();
    const ageDialog = page.getByRole("dialog", {
      name: "Configure Age Restriction",
      exact: true,
    });
    await ageDialog.waitFor();
    const layout = await ageDialog.evaluate((el) => {
      const view = innerHeight,
        r = (n) => el.querySelector(n).getBoundingClientRect();
      const table = el.querySelector(".modal-table-scroll");
      const name = el.querySelector("#filter-name").getBoundingClientRect(),
        label = el
          .querySelector("label[for=filter-name]")
          .getBoundingClientRect();
      return {
        dialogScrolls: el.scrollHeight > el.clientHeight + 1,
        tableScrolls: table.scrollHeight > table.clientHeight + 1,
        headerIn: r(".admin-modal-header").top >= 0,
        buttonsIn: r(".admin-table-actions").bottom <= view,
        closeIn: r(".admin-modal-close").top >= 0,
        gap: Math.round(name.left - label.right),
      };
    });
    assert(
      !layout.dialogScrolls &&
        layout.tableScrolls &&
        layout.headerIn &&
        layout.buttonsIn &&
        layout.closeIn,
      "Only the rating table should scroll: " + JSON.stringify(layout),
    );
    assert(
      layout.gap < 60,
      "The Name field should sit next to its label: " + JSON.stringify(layout),
    );
    await ageDialog
      .getByRole("button", { name: "Cancel", exact: true })
      .click();
    await page.setViewportSize({ width: 1280, height: 720 });
    // A short window so the table scrolls; changing a channel must leave the page where it was.
    await page.setViewportSize({ width: 1280, height: 500 });
    await row.scrollIntoViewIfNeeded();
    const scrolledTo = await page.evaluate(() => Math.round(window.scrollY));
    assert(
      scrolledTo > 200,
      "The Channels table should be scrolled for this check: " + scrolledTo,
    );
    const cycle = row.locator(".channel-visibility-action");
    const visibilityMode = async () =>
      (await cycle.getAttribute("aria-label")).replace("Visibility: ", "");
    const first = await visibilityMode();
    const seen = [first];
    for (let i = 0; i < 3; i++) {
      await cycle.click();
      await page.waitForFunction(
        ([r, prev]) => {
          const tr = [...document.querySelectorAll("tbody tr")].find((x) =>
            x.textContent.includes(r),
          );
          const b = tr && tr.querySelector(".channel-visibility-action");
          return !!b && b.getAttribute("aria-label") !== `Visibility: ${prev}`;
        },
        ["Synthetic channel 998", seen[seen.length - 1]],
      );
      seen.push(await visibilityMode());
    }
    const stayedAt = await page.evaluate(() => Math.round(window.scrollY));
    assert(
      Math.abs(stayedAt - scrolledTo) <= 40,
      `Changing a channel should keep the scroll position (${scrolledTo} -> ${stayedAt})`,
    );
    assert(
      seen.join(">") ===
        {
          Hide: "Hide>Show>Auto>Hide",
          Show: "Show>Auto>Hide>Show",
          Auto: "Auto>Hide>Show>Auto",
        }[first],
      "The per-channel button should cycle Hide, Show, Auto: " + seen.join(">"),
    );
    assert(
      (await cycle.getAttribute("title")).startsWith(first),
      "Visibility icon tooltip states its current mode",
    );
    assert(
      (await row
        .getByRole("button", { name: "Edit Synthetic channel 998" })
        .getAttribute("title")) === "Edit channel",
      "Channel pencil has an edit tooltip",
    );
    assert(
      (await row
        .getByRole("button", { name: "Delete Synthetic channel 998" })
        .getAttribute("title")) === "Delete channel",
      "Channel trash can has a delete tooltip",
    );
    const channelIconStyle = await row
      .getByRole("button", { name: "Edit Synthetic channel 998" })
      .evaluate((button) => {
        const style = getComputedStyle(button);
        return {
          background: style.backgroundColor,
          borderRadius: style.borderRadius,
          width: button.getBoundingClientRect().width,
        };
      });
    assert(
      channelIconStyle.background !== "rgb(239, 239, 239)" &&
        channelIconStyle.borderRadius !== "0px" &&
        channelIconStyle.width === 40,
      "Channel edit/delete actions use the shared styled icon buttons: " +
        JSON.stringify(channelIconStyle),
    );
    await page.setViewportSize({ width: 390, height: 844 });
    const channelActionRows = await row.evaluate((tr) => ({
      primary: tr
        .querySelector(".channel-primary-actions")
        .getBoundingClientRect().top,
      edit: tr.querySelector(".channel-edit-actions").getBoundingClientRect()
        .top,
    }));
    assert(
      channelActionRows.edit > channelActionRows.primary,
      "Narrow channel actions wrap edit/delete below selection and visibility",
    );
    await page.setViewportSize({ width: 1280, height: 720 });

    await page.goto(url + "/account");
    await page.locator("#account-days").waitFor();
    const accountFieldOrder = await page
      .locator(".account-card form .admin-field-row label")
      .allTextContents();
    assert(
      accountFieldOrder.slice(0, 2).join(",") === "Username,Current Password",
      "Username appears before Current Password",
    );
    const profileHeads = (
      await page.locator(".profiles-table thead th").allTextContents()
    ).map((text) => text.replace(/\s*[▲▼]$/, ""));
    assert(
      profileHeads.join(",") === "Actions,Name,Role,Audience,PIN",
      "Profile table columns put actions first: " + profileHeads.join(","),
    );
    const firstProfile = page.locator(".profiles-table tbody tr").first();
    assert(
      (await firstProfile
        .locator("td")
        .first()
        .getByTitle("Edit profile")
        .count()) === 1 &&
        (await firstProfile
          .locator("td")
          .first()
          .getByTitle("Delete profile")
          .count()) === 1,
      "Profile edit/delete icons and tooltips share the first column",
    );
    const profileActionTops = await firstProfile
      .locator("td")
      .first()
      .locator(".admin-icon-btn")
      .evaluateAll((buttons) =>
        buttons.map((button) => Math.round(button.getBoundingClientRect().top)),
      );
    assert(
      new Set(profileActionTops).size === 1,
      "Profile pencil and trash can stay side by side",
    );
    const profileIconStyle = await firstProfile
      .locator(".admin-icon-btn")
      .first()
      .evaluate((button) => {
        const style = getComputedStyle(button);
        return {
          background: style.backgroundColor,
          borderRadius: style.borderRadius,
          width: button.getBoundingClientRect().width,
        };
      });
    assert(
      profileIconStyle.background !== "rgb(239, 239, 239)" &&
        profileIconStyle.borderRadius !== "0px" &&
        profileIconStyle.width === 40,
      "Profile actions use the shared styled icon buttons: " +
        JSON.stringify(profileIconStyle),
    );
    await firstProfile.getByTitle("Edit profile").click();
    assert(
      (await page.getByTitle("Save profile").count()) === 1 &&
        (await page.getByTitle("Cancel profile edit").count()) === 1,
      "Profile editor uses checkmark and X actions with tooltips",
    );
    await page.getByTitle("Cancel profile edit").click();
    assert(
      (await page.locator("#account-days").inputValue()) === "30",
      "Browsers stay signed in for 30 days by default",
    );
    assert(
      (await page
        .getByRole("button", { name: "Save", exact: true })
        .count()) === 0,
      "Sign-in lifetime no longer needs a Save button",
    );
    const expirationToggle = page.getByRole("switch", {
      name: "Never Expire",
    });
    const expirationLabel = page.locator(".session-never-label > span");
    assert(
      (await expirationToggle.textContent()) === "Off" &&
        (await expirationLabel.textContent()) === "Expires",
      "Off state labels the finite sign-in lifetime as Expires",
    );
    await page.locator("#account-days").fill("45");
    await delay(1000);
    assert(
      (
        await (
          await context.request.get(url + "/api/auth/session-lifetime")
        ).json()
      ).days === 30,
      "Number entry waits while the administrator is still typing",
    );
    const save45 = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/auth/session-lifetime") &&
        response.request().method() === "PUT",
    );
    await page.locator("#account-days").blur();
    assert((await save45).ok(), "Blur saves the sign-in lifetime");
    await page.waitForFunction(
      async () =>
        (await (await fetch("/api/auth/session-lifetime")).json()).days === 45,
      {},
      { timeout: 6000 },
    );
    const save30 = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/auth/session-lifetime") &&
        response.request().method() === "PUT",
    );
    await page.locator("#account-days").fill("30");
    await page.locator("#account-days").blur();
    assert((await save30).ok(), "Blur saves the restored sign-in lifetime");
    await page.waitForFunction(
      async () =>
        (await (await fetch("/api/auth/session-lifetime")).json()).days === 30,
      {},
      { timeout: 6000 },
    );
    await expirationToggle.click();
    assert(
      (await expirationToggle.textContent()) === "On" &&
        (await expirationLabel.textContent()) === "Never Expire",
      "Never Expire uses the shared On/Off control",
    );
    assert(
      await page.locator("#account-days").isDisabled(),
      "The days box should lock when Never Expire is checked",
    );
    await page.waitForFunction(
      async () =>
        (await (await fetch("/api/auth/session-lifetime")).json()).days === 0,
    );
    const lifetimeSaved = page.getByRole("status", {
      name: "Sign-in lifetime saved",
      exact: true,
    });
    await lifetimeSaved.waitFor();
    for (const viewport of [
      { width: 390, height: 844 },
      { width: 844, height: 390 },
    ]) {
      await page.setViewportSize(viewport);
      const placement = await page
        .locator(".session-policy-row", { has: page.locator("#account-days") })
        .evaluate((element) => {
          const check = element.querySelector(".session-save-check");
          const input = element.querySelector("#account-days");
          return {
            checkRight: check.getBoundingClientRect().right,
            inputLeft: input.getBoundingClientRect().left,
            checkText: check.textContent.trim(),
            visible: Number(getComputedStyle(check).opacity) > 0,
          };
        });
      assert(
        placement.checkRight <= placement.inputLeft &&
          placement.checkText === "✓" &&
          placement.visible,
        `Lifetime confirmation stays beside its field at ${viewport.width}x${viewport.height}: ${JSON.stringify(placement)}`,
      );
    }
    await page.setViewportSize({ width: 390, height: 844 });
    const neverLines = await expirationLabel.evaluate((element) => {
      const range = document.createRange();
      range.selectNodeContents(element);
      return range.getClientRects().length;
    });
    assert(
      neverLines >= 2,
      "Never Expire wraps on a narrow User tab while enabled",
    );
    await lifetimeSaved.waitFor({ state: "detached", timeout: 6000 });
    await expirationToggle.click();
    await page.waitForFunction(
      async () =>
        (await (await fetch("/api/auth/session-lifetime")).json()).days === 30,
    );
    assert(
      (await expirationLabel.textContent()) === "Expires",
      "Off state changes the lifetime label to Expires",
    );
    const expiresLines = await expirationLabel.evaluate((element) => {
      const range = document.createRange();
      range.selectNodeContents(element);
      return range.getClientRects().length;
    });
    assert(
      expiresLines === 1,
      "Expires stays on one line on a narrow User tab",
    );
    const sessionActions = await page
      .locator(".session-browser-actions .admin-btn")
      .evaluateAll((buttons) =>
        buttons.map((button) => {
          const bounds = button.getBoundingClientRect();
          const text = document.createRange();
          text.selectNodeContents(button);
          return {
            right: bounds.right,
            lines: text.getClientRects().length,
            contained: button.scrollWidth <= button.clientWidth,
          };
        }),
      );
    assert(
      sessionActions.every(
        (action) =>
          action.right <= page.viewportSize().width + 1 &&
          action.lines <= 2 &&
          action.contained,
      ),
      "Portrait browser sign-out actions fit and wrap to at most two lines: " +
        JSON.stringify(sessionActions),
    );
    await page.setViewportSize({ width: 1280, height: 720 });

    // Account inputs share one left edge, and the two explanatory paragraphs are gone.
    const lefts = await page.evaluate(() =>
      [
        "account-current",
        "account-username",
        "account-new",
        "account-confirm",
      ].map((id) =>
        Math.round(document.getElementById(id).getBoundingClientRect().left),
      ),
    );
    assert(
      new Set(lefts).size === 1,
      "Admin Account inputs should left-align: " + lefts.join(","),
    );
    const accountText = (
      await page.locator(".account-card").allInnerTexts()
    ).join(" ");
    assert(
      !accountText.includes("This server has one account") &&
        !accountText.includes("keep the expiry"),
      "The two explanatory paragraphs should be gone",
    );
    await page.goto(url + "/admin/accounts");
    await page.locator(".admin-tab").first().waitFor();
    const tops = await page
      .locator(".admin-tab")
      .evaluateAll((els) =>
        els.map((e) => Math.round(e.getBoundingClientRect().top)),
      );
    assert(
      tops.length === 8 &&
        tops.every((top, index) => index === 0 || top > tops[index - 1]),
      "Desktop Server Settings uses an ordered vertical sidebar: " +
        tops.join(","),
    );

    // The refresh boxes say when they last ran.
    await page.goto(url + "/admin/media");
    await page.getByText("Last Refreshed:").waitFor();
    await page.goto(url + "/admin/channels");
    await page.getByText("Last Updated:").waitFor();

    await page.goto(url + "/admin/server");
    assert(
      (await page.getByRole("button", { name: "View Logs" }).count()) === 0,
      "View Logs button removed",
    );
    const serverAlignment = await page
      .locator(".settings-grid")
      .evaluate((grid) => {
        const names = [...grid.querySelectorAll(".setting-name")];
        const controls = [...grid.querySelectorAll(".setting-control")].filter(
          (control) => control.getBoundingClientRect().width > 0,
        );
        return {
          gap: Math.round(
            Math.min(
              ...controls.map(
                (control) => control.getBoundingClientRect().left,
              ),
            ) -
              Math.max(
                ...names.map((name) => name.getBoundingClientRect().right),
              ),
          ),
        };
      });
    assert(
      serverAlignment.gap > 0 && serverAlignment.gap <= 60,
      "Server controls align about four characters after the longest label: " +
        JSON.stringify(serverAlignment),
    );
    assert((await page.getByText("Automatic Metadata", { exact: true }).count()) === 0, "Automatic Metadata has no configurable setting");
    await page.goto(url + "/admin/logs");
    await page.waitForURL("**/admin/logs");
    // The Logs box fits the window; the page itself does not scroll.
    await page.locator(".log-viewer").waitFor();
    const fit = await page.evaluate(() => ({
      page: document.documentElement.scrollHeight,
      view: window.innerHeight,
      box: document.querySelector(".log-viewer").getBoundingClientRect().bottom,
    }));
    assert(
      fit.page <= fit.view + 1 && fit.box <= fit.view,
      "The Logs box should fit the window: " + JSON.stringify(fit),
    );
    const logTime = page.locator(".log-line-time").first();
    // The viewer mounts before its first asynchronous log poll resolves.
    await logTime.waitFor();
    assert(
      (await logTime.locator("span").count()) === 2,
      "Log timestamps stack date and time in one column",
    );
    await page.goto(url + "/admin/api");
    await page.getByRole("button", { name: "Generate", exact: true }).waitFor();
    assert(
      (await page
        .getByRole("button", { name: "Generate", exact: true })
        .count()) === 1,
      "API key action uses the compact Generate label",
    );
    await page.setViewportSize({ width: 390, height: 844 });
    const apiGap = await page.locator(".api-key-create-row").evaluate((el) => {
      const label = el.querySelector("label").getBoundingClientRect();
      const input = el.querySelector("input").getBoundingClientRect();
      return Math.round(input.left - label.right);
    });
    assert(
      apiGap <= 24,
      "Narrow API key name stays close to its label: " + apiGap,
    );
    await context.close();
    console.log(
      "PASS: Profile Settings (time format), Channels Auto/Show/Hide, sign-in lifetime and the Logs tab",
    );
  }

  async function serverActivity() {
    seed();
    const viewer = await browser.newContext();
    const page = await viewer.newPage();
    page.on("pageerror", (e) => errors.push(e.message));
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    await playing(page);
    await page.waitForFunction(
      async () => {
        const a = await (await fetch("/api/server/activity")).json();
        return a.sessions.length === 1 && a.sessions[0].bandwidthMbps !== null;
      },
      {},
      { timeout: 20000, polling: 500 },
    );
    const a = await page.evaluate(async () =>
      (await fetch("/api/server/activity")).json(),
    );
    assert(
      a.sessions[0].profile === "Admin" &&
        a.sessions[0].sourceIp &&
        a.sessions[0].device,
      "Activity identifies the active viewer",
    );
    assert(/^\d{2}\.\d{2}\.\d{2}_[a-f0-9]{7}$/.test(a.sessions[0].appVersion), 'Player must report its bundled app version');
    assert(
      !JSON.stringify(a).includes("token"),
      "Activity never exposes playback tokens",
    );
    const monitor = await browser.newContext();
    const dashboard = await monitor.newPage();
    await login(dashboard);
    await dashboard.goto(url + "/admin/dashboard");
    await dashboard
      .getByRole("heading", { name: "Active Streaming Sessions" })
      .waitFor();
    await dashboard
      .getByRole("heading", { name: "Active Background Processes" })
      .waitFor();
    await dashboard.waitForFunction(() =>
      document.querySelector(".activity-session"),
    );
    assert(
      a.sessions[0].videoReason && a.sessions[0].audioReason,
      "Activity includes video and audio reasons",
    );
    await dashboard
      .locator(".activity-reasons")
      .filter({ hasText: a.sessions[0].videoReason })
      .waitFor();
    assert(
      !(
        await dashboard.locator(".server-activity").first().textContent()
      ).includes("Bandwidth measures media bytes"),
      "Generic text should be replaced by per-track reasons",
    );
    const firstCard = await dashboard
      .locator(".admin-card")
      .first()
      .textContent();
    assert(
      firstCard.includes("Active Streaming Sessions"),
      "Activity card is above other Server boxes",
    );
    if (process.env.ACTIVITY_SCREENSHOT)
      await dashboard.screenshot({
        path: process.env.ACTIVITY_SCREENSHOT,
        fullPage: true,
      });
    await leave(page);
    await dashboard.waitForFunction(() =>
      document
        .querySelector(".server-activity")
        ?.textContent.includes("No active streaming sessions."),
    );
    await monitor.close();
    await viewer.close();
    console.log(
      "PASS: Active session telemetry, identity, Server cards, and cleanup",
    );
  }

  async function advancedCapabilities() {
    // Verify negotiation and fail-closed behavior, not physical HDR/AVR output.
    for (const scenario of [
      { hdr: true, supported: true },
      { hdr: false, supported: true },
      { hdr: true, supported: false },
    ]) {
      const context = await browser.newContext();
      await context.addInitScript(({ hdr, supported }) => {
        const original = window.matchMedia.bind(window);
        window.matchMedia = (query) => {
          const result = original(query);
          if (query === "(dynamic-range: high)")
            Object.defineProperty(result, "matches", { value: hdr });
          return result;
        };
        const canPlay = HTMLMediaElement.prototype.canPlayType;
        HTMLMediaElement.prototype.canPlayType = function (type) {
          return type.includes("codecs=")
            ? "probably"
            : canPlay.call(this, type);
        };
        Object.defineProperty(navigator, "mediaCapabilities", {
          value: {
            decodingInfo: async (configuration) => ({
              supported,
              smooth: supported,
              powerEfficient: true,
              configuration,
            }),
          },
        });
      }, scenario);
      const page = await context.newPage();
      await login(page);
      await page.waitForFunction(
        () =>
          JSON.parse(
            localStorage.getItem("alreadyon.playbackCapabilities") || "null",
          )?.version === 4,
      );
      const caps = await page.evaluate(
        () =>
          JSON.parse(localStorage.getItem("alreadyon.playbackCapabilities"))
            .capabilities,
      );
      assert(
        caps.video.length <= 64 && caps.audio.length <= 32,
        "Expanded probes must stay bounded",
      );
      const hdr = caps.video.some((v) => v.colorTransfer === "smpte2084");
      const dolby = caps.video.some((v) => v.dolbyVisionProfile === 5);
      assert(
        hdr === (scenario.hdr && scenario.supported) && dolby === hdr,
        "HDR/Dolby Vision needs a compatible display and decoder",
      );
      assert(
        caps.audio.some((a) => a.codec === "truehd" && a.channels === 10) ===
          scenario.supported,
        "Lossless surround must be explicitly reported",
      );
      assert(
        caps.audio.some(
          (a) => a.codec === "aac" && a.channels === 8 && a.hls,
        ) === scenario.supported,
        "Surround HLS needs its own probe",
      );
      await context.close();
    }
    console.log(
      "PASS: bounded HDR/Dolby Vision and surround capability negotiation; unsupported/display-limited configurations fail closed",
    );
  }

  try {
    await waitForTuner();
    if (!only || only.includes("last-channel"))
      await require("./last-channel.cjs")({
        browser,
        login,
        url,
        assert,
        root,
        seed,
      });
    // Run permanent deletion before folder-setting scenarios trigger asynchronous rescans.
    if (!only || only.includes("capabilities")) await advancedCapabilities();
    if (!only || only.includes("codec-help"))
      await require("./codec-help.cjs")({ browser, login, assert });
    if (!only || only.includes("cache")) await cacheAndSwitching();
    if (!only || only.includes("recovery")) await recovery();
    if (!only || only.includes("transitions")) await transitions();
    if (!only || only.includes("short")) await shortEntries();
    if (!only || only.includes("controls")) await controls();
    if (!only || only.includes("mobile-ui")) await responsiveTouchUi();
    if (!only || only.includes("mobile-overlay")) await mobileOverlay();
    if (!only || only.includes("mobile-landscape")) await mobileLandscape();
    if (!only || only.includes("quality")) await quality();
    if (!only || only.includes("ladder")) await ladder();
    if (!only || only.includes("keyboard")) await keyboard();
    if (!only || only.includes("audio")) await audioTracksAndCommercialVolume();
    if (!only || only.includes("single-slot")) await singleSlotChanges();
    if (!only || only.includes("subtitles")) await subtitleDefaults();
    if (!only || only.includes("pause")) await pausedBoundary();
    if (!only || only.includes("alignment")) await guideAlignment();
    if (!only || only.includes("guide-live"))
      cp.execFileSync(
        process.execPath,
        [path.join(__dirname, "guide-live.cjs"), url],
        { stdio: "inherit" },
      );
    if (!only || only.includes("autofill-policy"))
      await require("./autofill-policy.cjs")({ browser, login, url, assert });
    if (!only || only.includes("credential-drafts"))
      await require("./credential-drafts.cjs")({ browser, login, url, assert });
    if (!only || only.includes("profile-creation"))
      await require("./profile-creation.cjs")({ browser, login, url, assert });
    if (!only || only.includes("profile-menu")) await profileMenuRoles();
    if (!only || only.includes("profile-refresh")) await profileRefresh();
    if (!only || only.includes("sessions-guide-history"))
      await sessionsAndGuideHistory();
    if (!only || only.includes("admin")) await adminTabs();
    if (!only || only.includes("admin-metrics-logs"))
      await require("./admin-metrics-logs.cjs")({
        browser,
        login,
        url,
        assert,
        delay,
        seed,
      });
    if (!only || only.includes("activity")) await serverActivity();
    await waitForTuner(); // saving encoder settings re-measures
    if (!only || only.includes("settings")) await settingsUi();
    if (!only || only.includes("autoplay")) {
      await autoplay(false);
      await autoplay(true);
    }
    if (!only || only.includes("channel-commercials"))
      await require("./channel-commercials.cjs")({
        browser,
        login,
        url,
        assert,
      });
    assert(errors.length === 0, "Browser errors: " + JSON.stringify(errors));
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e.stack || e.message);
  process.exit(1);
});
