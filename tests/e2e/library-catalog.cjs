// Runs against library-catalog.sh's isolated server, using real API/UI writes.
const { chromium } = require("../../web/node_modules/playwright");
const fs = require("node:fs");
const path = require("node:path");
const cp = require("node:child_process");
const assert = require("node:assert/strict");
const root = process.env.LIBRARY_TEST_ROOT;
const url = process.env.LIBRARY_TEST_URL;
if (
  !root ||
  !path.basename(root).startsWith("alreadyon-library-e2e.") ||
  !fs.existsSync(path.join(root, ".synthetic-test"))
)
  throw Error("Run library-catalog.sh");
const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
(async () => {
  for (let n = 0; n < 100; n++) {
    try {
      if (
        (await fetch(url + "/healthz")).ok &&
        fs
          .readFileSync(path.join(root, "server.log"), "utf8")
          .includes("keyless metadata: done")
      )
        break;
    } catch {}
    await wait(100);
  }
  cp.execFileSync(
    "python3",
    [path.join(__dirname, "library-catalog-seed.py")],
    { stdio: "inherit" },
  );
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({
      viewport: { width: 1440, height: 1100 },
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (err) => errors.push(err.message));
    const setup = await page.request.post(url + "/api/auth/setup", {
      data: {
        username: "library-test",
        password: "synthetic-library-123",
        serverName: "Library Test",
      },
    });
    assert.equal(setup.status(), 201, await setup.text());
    await page.goto(url + "/admin/media");
    await page.waitForURL("**/admin/library-paths");
    await page.getByRole("heading", { name: "TV Show Folders" }).waitFor();
    assert.equal(
      await page.getByText("Exclusion Filters", { exact: true }).count(),
      0,
    );
    const pathHeadings = await page
      .getByRole("heading", { level: 3 })
      .allTextContents();
    assert.deepEqual(pathHeadings, [
      "Refresh Media",
      "Polling Interval",
      "Movie Folders",
      "TV Show Folders",
      "Commercial Folders",
      "Commercial Breaks",
    ]);
    const settingsNav = page.getByRole("navigation", {
      name: "Server Settings",
    });
    const navLabels = await settingsNav.getByRole("button").allTextContents();
    assert.equal(navLabels.includes("Commercials"), false);
    assert.equal(
      navLabels.indexOf("Manage Media"),
      navLabels.indexOf("Library Paths") + 1,
    );
    await settingsNav.getByRole("button", { name: "Manage Media" }).click();
    await page.waitForURL("**/admin/manage-media");
    assert.equal(
      await page.getByRole("button", { name: "Apply Channels" }).count(),
      0,
    );
    assert.equal(
      await page
        .getByRole("heading", { name: "Manage Media", exact: true })
        .count(),
      0,
    );
    assert.equal(
      await page
        .getByRole("button", { name: "Server Settings Navigation" })
        .isVisible(),
      false,
    );
    assert.equal(await settingsNav.isVisible(), true);
    const movies = page.getByRole("region", { name: "Manage Movies" });
    const shows = page.getByRole("region", { name: "Manage TV Shows" });
    const movieFilters = page.getByRole("complementary", {
      name: "Movie filters",
    });
    const showFilters = page.getByRole("complementary", {
      name: "TV Show filters",
    });
    const movieFilterToggle = movieFilters.getByRole("button", {
      name: "Filter Movies",
      exact: true,
    });
    const showFilterToggle = showFilters.getByRole("button", {
      name: "Filter TV Shows",
      exact: true,
    });
    assert.equal(
      await movieFilterToggle.getAttribute("aria-expanded"),
      "false",
    );
    assert.equal(await showFilterToggle.getAttribute("aria-expanded"), "false");
    assert.equal(
      await movieFilters.getByPlaceholder("Title or file path").isVisible(),
      false,
    );
    await movieFilterToggle.click();
    await movieFilters.getByPlaceholder("Title or file path").waitFor();
    await showFilterToggle.click();
    await showFilters.getByPlaceholder("Title or file path").waitFor();
    assert.equal(
      await movieFilterToggle.getAttribute("aria-expanded"),
      "true",
      "Each filter menu expands independently",
    );
    await showFilterToggle.click();
    await page.evaluate(() => window.scrollTo(0, 0));
    assert.equal(
      await page.getByRole("button", { name: /Toggle .* filters/ }).count(),
      0,
    );
    await movies.locator("thead th").first().waitFor();
    assert.deepEqual(await movies.locator("thead th").allTextContents(), [
      "",
      "Title",
      "Status",
    ]);
    assert.equal(
      (await movieFilters.locator("label").nth(0).innerText()).split("\n")[0],
      "Search",
    );
    const filterLabels = await movieFilters
      .locator("label")
      .evaluateAll((labels) =>
        labels.map((label) => label.innerText.split("\n")[0]),
      );
    assert.deepEqual(filterLabels, [
      "Search",
      "Channel Status",
      "Audience",
      "Content Rating",
      "Genre",
    ]);
    assert.equal(
      await movieFilters.getByRole("button", { name: "Sort" }).count(),
      0,
    );
    assert.equal(
      await movieFilters.getByText("Metadata", { exact: true }).count(),
      0,
    );
    const moviePageSize = movies.getByLabel("Items per page");
    assert.equal(await moviePageSize.inputValue(), "10");
    assert.deepEqual(
      await moviePageSize
        .locator("option")
        .evaluateAll((options) => options.map((option) => option.value)),
      ["10", "25", "50", "100"],
    );
    assert.equal(
      await movies.locator(".library-page-controls button").count(),
      2,
    );
    await moviePageSize.selectOption("50");
    await page.waitForFunction(
      () =>
        document.querySelector('[data-library-section="movies"] tbody')?.rows
          .length === 50,
    );
    assert.equal(
      await movies
        .getByRole("button", { name: "Previous", exact: true })
        .count(),
      2,
    );
    assert.equal(
      await movies.getByRole("button", { name: "Next", exact: true }).count(),
      2,
    );
    await page.setViewportSize({ width: 390, height: 600 });
    await page.evaluate(() => window.scrollTo(0, 0));
    await movieFilters.getByRole("button", { name: "Genres" }).click();
    const genreOptions = movieFilters.getByRole("listbox", { name: "Genres" });
    await genreOptions.getByLabel("Anime").check();
    await genreOptions.getByLabel("Family").check();
    await movieFilters.getByRole("button", { name: "Genres" }).click();
    await page.setViewportSize({ width: 1440, height: 1100 });
    await movieFilters.getByLabel("Content Rating").selectOption("PG");
    const multiFilterResult = await (
      await page.request.get(
        url +
          "/api/library/catalog?view=movies&genre=anime%2Cfamily&rating=PG&limit=100",
      )
    ).json();
    assert.equal(multiFilterResult.total, 55);
    await movies
      .getByRole("button", { name: "Reset filters", exact: true })
      .click();
    await page.waitForFunction(
      () =>
        document.querySelector('[data-library-section="movies"] tbody')?.rows
          .length === 50,
    );
    await shows
      .getByRole("button", { name: "Edit Example Academy", exact: true })
      .waitFor();
    assert.equal(await movies.locator("tbody tr").count(), 50);
    const titleSortButton = movies
      .getByRole("columnheader", { name: "Title" })
      .getByRole("button", { name: "Title" });
    assert.equal(
      await movies
        .locator("tbody tr:first-child td:nth-child(2) strong")
        .textContent(),
      "Film 01",
    );
    await titleSortButton.click();
    await page.waitForFunction(
      () =>
        document.querySelector(
          '[data-library-section="movies"] tbody tr:first-child td:nth-child(2) strong',
        )?.textContent === "Film 55",
    );
    assert.equal(
      await movies
        .getByRole("columnheader", { name: "Title" })
        .getAttribute("aria-sort"),
      "descending",
    );
    await titleSortButton.click();
    await page.waitForFunction(
      () =>
        document.querySelector(
          '[data-library-section="movies"] tbody tr:first-child td:nth-child(2) strong',
        )?.textContent === "Film 01",
    );
    for (const columnName of ["Status"]) {
      const sortButton = movies
        .getByRole("columnheader", { name: columnName })
        .getByRole("button", { name: columnName });
      await sortButton.click();
      assert.equal(
        await movies
          .getByRole("columnheader", { name: columnName })
          .getAttribute("aria-sort"),
        "ascending",
      );
      await sortButton.click();
      assert.equal(
        await movies
          .getByRole("columnheader", { name: columnName })
          .getAttribute("aria-sort"),
        "descending",
      );
    }
    await titleSortButton.click();
    await page.waitForFunction(
      () =>
        document.querySelector(
          '[data-library-section="movies"] tbody tr:first-child td:nth-child(2) strong',
        )?.textContent === "Film 01",
    );
    assert.equal(
      await page.getByRole("button", { name: "TV Shows", exact: true }).count(),
      0,
    );
    await page.evaluate(() => window.scrollTo(0, 0));
    await wait(200);
    await page.screenshot({
      path: path.join(root, "library-desktop.png"),
      fullPage: false,
    });
    await movies
      .getByRole("checkbox", { name: "Select Film 01", exact: true })
      .check();
    await movies
      .locator(".library-page-controls")
      .getByRole("button", { name: "Next", exact: true })
      .click();
    await movies
      .getByRole("checkbox", { name: "Select Film 51", exact: true })
      .check();
    await movies
      .getByRole("button", { name: "Edit selected (2)", exact: true })
      .click();
    let dialog = page.getByRole("dialog");
    const reviewNote = dialog.getByRole("note");
    assert.equal(
      await reviewNote.count(),
      0,
      "Automatic PG audiences must not require manual review",
    );
    const automaticPG = await (
      await page.request.get(
        url + "/api/library/catalog?view=movies&rating=PG&limit=200",
      )
    ).json();
    assert.ok(automaticPG.items.length > 0);
    assert.ok(
      automaticPG.items.every(
        (item) =>
          item.audience === "young_child" &&
          !item.needsReview &&
          item.audienceOverride == null,
      ),
      "PG films should automatically belong to Kids before owner edits",
    );
    assert.equal(await dialog.getByText(/Apply Channels/).count(), 0);
    await dialog
      .getByLabel("Intended audience", { exact: true })
      .selectOption("young_child");
    await dialog
      .getByRole("button", { name: "Save Changes", exact: true })
      .click();
    await dialog.waitFor({ state: "hidden" });
    let result = await (
      await page.request.get(
        url +
          "/api/library/catalog?view=movies&audience=young_child&rating=PG&source=manual",
      )
    ).json();
    assert.equal(result.total, 2);
    assert.ok(
      result.items.every(
        (item) =>
          item.certification === "PG" && item.audience === "young_child",
      ),
    );
    await shows
      .getByRole("button", { name: "Edit Example Academy", exact: true })
      .click();
    dialog = page.getByRole("dialog");
    await dialog
      .getByLabel("Intended audience", { exact: true })
      .selectOption("teen");
    await dialog
      .getByRole("button", { name: "Save Changes", exact: true })
      .click();
    await dialog.waitFor({ state: "hidden" });
    result = await (
      await page.request.get(
        url + "/api/library/catalog?view=episodes&showId=9056",
      )
    ).json();
    assert.equal(result.total, 3);
    assert.ok(result.items.every((item) => item.audience === "teen"));
    assert.equal(
      await shows
        .getByRole("button", { name: "Edit Pilot", exact: true })
        .count(),
      0,
    );
    await shows
      .getByRole("button", { name: "Edit Example Academy", exact: true })
      .click();
    dialog = page.getByRole("dialog");
    await dialog
      .getByRole("button", { name: "Genres", exact: true })
      .getByText("Comedy", { exact: true })
      .waitFor();
    await page.route("**/api/library/items/9056/tmdb-preview*", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          tmdbId: 77,
          title: "Preview Academy",
          year: 2001,
          genres: ["drama"],
          certification: "TV-14",
        }),
      }),
    );
    const originalIdentity = {
      id: await dialog.getByLabel("TMDb ID").inputValue(),
      title: await dialog.getByLabel("Show title").inputValue(),
      year: await dialog.getByLabel("Show premiere year").inputValue(),
      genres: await dialog
        .getByRole("button", { name: "Genres", exact: true })
        .innerText(),
      rating: await dialog
        .getByLabel("Content Rating")
        .locator("option")
        .first()
        .textContent(),
    };
    await dialog.getByLabel("TMDb ID").fill("77");
    await dialog.getByLabel("TMDb ID").press("Enter");
    await page.waitForFunction(
      () =>
        document.querySelector("#library-edit-title")?.value ===
        "Preview Academy",
    );
    assert.equal(
      await dialog.getByLabel("Show premiere year").inputValue(),
      "2001",
    );
    assert.equal(
      await dialog
        .getByLabel("Content Rating")
        .locator("option")
        .first()
        .textContent(),
      "Current (TV-14)",
    );
    await dialog.getByLabel("TMDb ID").fill(originalIdentity.id);
    await dialog.getByLabel("Show title").focus();
    assert.equal(
      await dialog.getByLabel("Show title").inputValue(),
      originalIdentity.title,
    );
    assert.equal(
      await dialog.getByLabel("Show premiere year").inputValue(),
      originalIdentity.year,
    );
    assert.equal(
      await dialog
        .getByRole("button", { name: "Genres", exact: true })
        .innerText(),
      originalIdentity.genres,
    );
    assert.equal(
      await dialog
        .getByLabel("Content Rating")
        .locator("option")
        .first()
        .textContent(),
      originalIdentity.rating,
    );
    let finishLatePreview;
    await page.route("**/tmdb-preview?tmdbId=78", async (route) => {
      await new Promise((resolve) => {
        finishLatePreview = resolve;
      });
      await route.fulfill({
        json: {
          tmdbId: 78,
          title: "Stale preview",
          year: 2002,
          genres: ["drama"],
          certification: "TV-14",
        },
      });
    });
    await dialog.getByLabel("TMDb ID").fill("78");
    await Promise.all([
      page.waitForRequest("**/tmdb-preview?tmdbId=78"),
      dialog.getByLabel("TMDb ID").press("Tab"),
    ]);
    await dialog.getByLabel("TMDb ID").fill(originalIdentity.id);
    await dialog.getByLabel("TMDb ID").press("Tab");
    const lateResponse = page.waitForResponse("**/tmdb-preview?tmdbId=78");
    finishLatePreview();
    await lateResponse;
    await page.evaluate(
      () =>
        new Promise((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(resolve)),
        ),
    );
    assert.equal(
      await dialog.getByLabel("Show title").inputValue(),
      originalIdentity.title,
    );
    assert.equal(
      await dialog.getByText("Looking up…", { exact: true }).count(),
      0,
    );
    // Blurring an unchanged original ID must preserve edits made to that draft.
    await dialog.getByLabel("Show title").fill("Manual original draft");
    await dialog.getByLabel("TMDb ID").focus();
    await dialog.getByLabel("Show title").focus();
    assert.equal(
      await dialog.getByLabel("Show title").inputValue(),
      "Manual original draft",
    );
    await dialog.getByLabel("TMDb ID").fill("77");
    await dialog.getByLabel("TMDb ID").press("Enter");
    await page.waitForFunction(
      () =>
        document.querySelector("#library-edit-title")?.value ===
        "Preview Academy",
    );
    await dialog.getByLabel("TMDb ID").fill(originalIdentity.id);
    await dialog.getByLabel("TMDb ID").press("Enter");
    assert.equal(
      await dialog.getByLabel("Show title").inputValue(),
      "Manual original draft",
    );
    await dialog.getByLabel("TMDb ID").fill("77");
    await dialog.getByLabel("TMDb ID").press("Enter");
    await page.waitForFunction(
      () =>
        document.querySelector("#library-edit-title")?.value ===
        "Preview Academy",
    );
    await page.setViewportSize({ width: 390, height: 600 });
    await dialog.getByRole("button", { name: "Genres" }).click();
    const genrePanel = dialog.locator(".multi-select-panel-inline");
    await genrePanel
      .getByRole("checkbox", { name: "Drama", exact: true })
      .waitFor();
    await wait(300);
    // Review explanations can make the dialog taller than its viewport.
    // Keep the option panel and actions operable through dialog scrolling.
    await genrePanel.scrollIntoViewIfNeeded();
    const saveChanges = dialog.getByRole("button", { name: "Save Changes" });
    await saveChanges.scrollIntoViewIfNeeded();
    await saveChanges.waitFor();
    await dialog.getByRole("button", { name: "Genres" }).click();
    await wait(200);
    const storedShow = await (
      await page.request.get(
        url +
          "/api/library/catalog?view=shows&search=Example%20Academy&limit=10",
      )
    ).json();
    assert.equal(
      storedShow.items[0].title,
      "Example Academy",
      "TMDb preview must not persist before Save Changes",
    );
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await dialog.waitFor({ state: "hidden" });
    await page.evaluate(() => window.scrollTo(0, 0));
    await movieFilters.getByPlaceholder("Title or file path").fill("Film 01");
    await movies
      .getByRole("button", { name: "Edit Film 01", exact: true })
      .waitFor();
    await wait(350);
    assert.equal(await movies.locator("tbody tr").count(), 1);
    await movies
      .getByRole("button", { name: "Reset filters", exact: true })
      .click();
    await page.waitForFunction(
      () =>
        document.querySelector('[data-library-section="movies"] tbody')?.rows
          .length === 50,
    );
    await shows
      .getByRole("heading", { name: "Manage TV Shows" })
      .scrollIntoViewIfNeeded();
    await showFilters.waitFor();
    assert.equal(await showFilterToggle.getAttribute("aria-expanded"), "false");
    assert.equal(await movieFilters.isVisible(), true);
    await showFilterToggle.click();
    await showFilters.getByPlaceholder("Title or file path").fill("Example");
    await showFilterToggle.click();
    await showFilterToggle.click();
    assert.equal(
      await showFilters.getByPlaceholder("Title or file path").inputValue(),
      "Example",
      "Collapse preserves filter values",
    );
    await showFilters.getByPlaceholder("Title or file path").fill("");
    await showFilterToggle.click();
    await page.evaluate(() => window.scrollTo(0, 0));
    await movieFilters.waitFor();
    await movies
      .getByRole("button", { name: "Edit Film 01", exact: true })
      .click();
    await page.getByRole("dialog").waitFor();
    assert.equal(
      await page.evaluate(
        () => getComputedStyle(document.documentElement).overflowY,
      ),
      "hidden",
    );
    const lockedY = await page.evaluate(() => window.scrollY);
    await page.mouse.move(10, 500);
    await page.mouse.wheel(0, 600);
    await wait(100);
    assert.equal(
      await page.evaluate(() => window.scrollY),
      lockedY,
      "Modal blocks background scrolling",
    );
    await page.keyboard.press("Escape");
    await page.getByRole("dialog").waitFor({ state: "hidden" });
    assert.notEqual(
      await page.evaluate(
        () => getComputedStyle(document.documentElement).overflowY,
      ),
      "hidden",
    );
    for (const width of [769, 768, 390]) {
      await page.setViewportSize({ width, height: 844 });
      assert.equal(await settingsNav.isVisible(), width > 768);
      assert.equal(
        await page
          .getByRole("button", { name: "Server Settings Navigation" })
          .isVisible(),
        width <= 768,
      );
    }
    await page.setViewportSize({ width: 390, height: 844 });
    await page.evaluate(() => window.scrollTo(0, 0));
    await movies
      .getByRole("button", { name: "Edit Film 01", exact: true })
      .waitFor();
    await wait(200);
    await page.screenshot({
      path: path.join(root, "library-mobile.png"),
      fullPage: false,
    });
    await shows
      .getByRole("button", { name: "Edit Example Academy", exact: true })
      .click();
    dialog = page.getByRole("dialog");
    await page.screenshot({
      path: path.join(root, "library-mobile-dialog.png"),
      fullPage: false,
    });
    await page.keyboard.press("Escape");
    await dialog.waitFor({ state: "hidden" });
    assert.equal(
      await shows
        .getByRole("button", { name: "Edit Example Academy", exact: true })
        .evaluate((element) => element === document.activeElement),
      true,
      "Dialog restores focus",
    );
    assert.deepEqual(errors, []);
    console.log(
      "PASS: Library Paths order, expanded navigation, independent collapsible right filters, modal scroll lock, three-column catalog, pagination, cross-page bulk edits, official rating preservation, show-level inheritance, mobile layout and keyboard focus.",
    );
  } finally {
    await browser.close();
  }
})().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
