// UI-only fixtures: every API call is intercepted; no server account is changed.
const { chromium } = require("../../web/node_modules/playwright");
const assert = require("node:assert/strict");
const url = process.argv[2];
if (!url)
  throw Error("Usage: node settings-feedback.cjs <synthetic-server-url>");
(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const profile = {
      id: 1,
      name: "Synthetic",
      role: "admin",
      audienceTier: "adult",
      hasPin: false,
      icon: null,
      guideTimeFormat: "12h",
      subtitleMode: "off",
      audioLanguage: "en",
      subtitleLanguage: "en",
      lastWatchedChannelId: null,
    };
    let pinWrites = 0;
    await page.route(
      (url) => url.pathname.startsWith("/api/"),
      (route) => {
        const request = route.request(),
          path = new URL(request.url()).pathname;
        if (path === "/api/playback/probe") return route.fulfill({ json: {} });
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
        if (path === "/api/auth/tv-pin") {
          if (request.method() === "PUT") pinWrites++;
          return route.fulfill({ json: { enabled: false } });
        }
        if (request.method() !== "GET") {
          errors.push("Unexpected write: " + path);
          return route.abort();
        }
        if (path === "/api/users")
          return route.fulfill({
            json: [
              {
                id: 1,
                username: "synthetic",
                role: "owner",
                mustChangePassword: false,
                passwordLocked: false,
                maxStreams: 0,
              },
            ],
          });
        if (path === "/api/auth/sessions") return route.fulfill({ json: [] });
        if (path === "/api/auth/session-lifetime")
          return route.fulfill({ json: { days: 30 } });
        return route.fulfill({ json: {} });
      },
    );
    // Both pages expose the same Back action.
    for (const route of ["/profile", "/account"]) {
      await page.goto(url + route);
      const back = page.locator(".admin-topbar-inner .admin-back");
      await back.waitFor();
    }
    const helpLabel = page.locator('label[for="tv-pairing-pin"]');
    await helpLabel.hover();
    const help = await helpLabel.getAttribute("title");
    assert.ok(help.includes("used with your username"));
    assert.equal(
      await page.locator("#tv-pairing-pin").getAttribute("aria-description"),
      help,
    );
    const save = page.getByRole("button", {
      name: "Save Changes",
      exact: true,
    });
    await page.waitForFunction(
      () =>
        document.querySelector(".account-save-row button")?.disabled === false,
    );
    const feedback = page.locator(".account-save-feedback");
    await save.click();
    assert.equal(await feedback.innerText(), "There are no changes to save.");
    await page.getByLabel("TV Pairing Pin", { exact: true }).fill("123456");
    await save.click();
    await page.getByText("Changes saved.", { exact: true }).waitFor();
    assert.equal(pinWrites, 1);
    await page.goto(url + "/admin/accounts");
    await page.getByRole("button", { name: "Edit", exact: true }).click();
    const form = page.locator(".account-form");
    for (const text of [
      "Simultaneous Streams",
      "Require Password Change",
      "Password Locked",
    ]) {
      const label = form.locator("label").filter({ hasText: text });
      await label.hover();
      assert.ok((await label.getAttribute("title")).length > 70);
    }
    assert.equal(
      await page
        .getByText("A locked password can only be changed by an owner,", {
          exact: false,
        })
        .count(),
      0,
    );
    assert.deepEqual(errors, []);
    console.log(
      "PASS: settings help, no-op/changed saves, PIN persistence and account help",
    );
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
