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
    assert.equal(await page.getByRole("button", { name: "Server Settings Navigation" }).isVisible(), false);
    assert.equal(await settingsNav.isVisible(), true);
    const movies = page.getByRole("region", { name: "Manage Movies" });
    const shows = page.getByRole("region", { name: "Manage TV Shows" });
    const movieFilters = page.getByRole("complementary", {
      name: "Movie filters",
    });
    const showFilters = page.getByRole("complementary", {
      name: "TV Show filters",
    });
    await movieFilters.getByPlaceholder("Title or file path").waitFor();
    assert.equal(await page.getByRole("button", { name: /Toggle .* filters/ }).count(), 0);
    await movies.locator("thead th").first().waitFor();
    assert.deepEqual(await movies.locator("thead th").allTextContents(), ["", "Title", "Status"]);
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
    const headerBox = await page.locator(".admin-topbar-inner").boundingBox();
    const contentBox = await page.locator(".admin-layout").boundingBox();
    assert.ok(
      headerBox &&
        contentBox &&
        Math.abs(headerBox.x - contentBox.x) < 1 &&
        Math.abs(headerBox.width - contentBox.width) < 1,
      "Server Settings header and content shells should share the 1280px width",
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
    await wait(200);
    const filterMenuBox = await movieFilters.boundingBox();
    const genrePanelBox = await genreOptions.boundingBox();
    assert.ok(
      filterMenuBox &&
        genrePanelBox &&
        genrePanelBox.y >= filterMenuBox.y &&
        genrePanelBox.y + genrePanelBox.height <=
          filterMenuBox.y + filterMenuBox.height,
      `Expanded Genre options should auto-scroll fully into the filter sidebar: ${JSON.stringify({ filterMenuBox, genrePanelBox, scrollTop: await movieFilters.evaluate((element) => element.scrollTop) })}`,
    );
    assert.equal(
      await genreOptions
        .getByLabel("Anime")
        .evaluate(
          (element) => getComputedStyle(element.parentElement).textAlign,
        ),
      "left",
    );
    const genrePanelGeometry = await genreOptions.evaluate((panel) => ({
      clientWidth: panel.clientWidth,
      scrollWidth: panel.scrollWidth,
      checkboxWidth: panel
        .querySelector('input[type="checkbox"]')
        .getBoundingClientRect().width,
    }));
    assert.equal(
      genrePanelGeometry.scrollWidth,
      genrePanelGeometry.clientWidth,
      `Genre options should not overflow horizontally: ${JSON.stringify(genrePanelGeometry)}`,
    );
    assert(
      genrePanelGeometry.checkboxWidth <= 20,
      `Genre checkboxes should retain a compact width: ${JSON.stringify(genrePanelGeometry)}`,
    );
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
    const columnWidthsBeforeSort = await movies
      .locator("thead th")
      .evaluateAll((headers) =>
        headers.map((header) => header.getBoundingClientRect().width),
      );
    await titleSortButton.click();
    await page.waitForFunction(
      () =>
        document.querySelector(
          '[data-library-section="movies"] tbody tr:first-child td:nth-child(2) strong',
        )?.textContent === "Film 55",
    );
    const columnWidthsAfterSort = await movies
      .locator("thead th")
      .evaluateAll((headers) =>
        headers.map((header) => header.getBoundingClientRect().width),
      );
    assert.ok(
      columnWidthsBeforeSort.every(
        (width, index) => Math.abs(width - columnWidthsAfterSort[index]) < 1,
      ),
      "Column widths should remain fixed while sorting",
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
    const movieHeaderLayout = await movies.evaluate((element) => {
      const box = element.getBoundingClientRect();
      const title = element
        .querySelector(".library-box-title")
        .getBoundingClientRect();
      const actions = element
        .querySelector(".library-box-actions")
        .getBoundingClientRect();
      const pageControls = element
        .querySelector(".library-page-controls")
        .getBoundingClientRect();
      return {
        box: {
          left: box.left,
          right: box.right,
          center: (box.left + box.right) / 2,
        },
        title: {
          left: title.left,
          right: title.right,
          center: (title.left + title.right) / 2,
        },
        actions,
        pageControls,
      };
    });
    assert.ok(
      Math.abs(movieHeaderLayout.title.center - movieHeaderLayout.box.center) <
        2 &&
        movieHeaderLayout.actions.left <= movieHeaderLayout.title.left &&
        movieHeaderLayout.pageControls.right <=
          movieHeaderLayout.box.right - 12,
      "Desktop catalog title should center between left actions and right-side paging controls",
    );
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
    const dialogBox = await dialog.boundingBox();
    assert.ok(
      dialogBox && dialogBox.width <= 430,
      "Edit dialog should be 35% narrower",
    );
    const formWidths = await dialog
      .locator(".admin-field-row :is(input, select, .multi-select)")
      .evaluateAll((elements) =>
        elements.map((element) => element.getBoundingClientRect().width),
      );
    assert.ok(
      Math.max(...formWidths) - Math.min(...formWidths) < 1,
      "Edit controls should share one width",
    );
    const tmdbFieldBox = await dialog
      .locator(".library-tmdb-field")
      .boundingBox();
    const titleFieldBox = await dialog.getByLabel("Show title").boundingBox();
    assert.ok(
      tmdbFieldBox && titleFieldBox && tmdbFieldBox.y < titleFieldBox.y,
      "TMDb ID should appear directly above Title",
    );
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
    await page.setViewportSize({ width: 390, height: 600 });
    await dialog.getByRole("button", { name: "Genres" }).click();
    const genrePanel = dialog.locator(".multi-select-panel-inline");
    await genrePanel
      .getByRole("checkbox", { name: "Drama", exact: true })
      .waitFor();
    await wait(300);
    const expandedDialogBox = await dialog.boundingBox();
    const modalGenrePanelBox = await genrePanel.boundingBox();
    const saveButtonBox = await dialog
      .getByRole("button", { name: "Save Changes" })
      .boundingBox();
    const cancelButtonBox = await dialog
      .getByRole("button", { name: "Cancel" })
      .boundingBox();
    const expandedFooterGap =
      expandedDialogBox && cancelButtonBox
        ? expandedDialogBox.y +
          expandedDialogBox.height -
          cancelButtonBox.y -
          cancelButtonBox.height
        : -1;
    assert.ok(
      expandedDialogBox &&
        modalGenrePanelBox &&
        saveButtonBox &&
        cancelButtonBox &&
        modalGenrePanelBox.y >= expandedDialogBox.y &&
        modalGenrePanelBox.y + modalGenrePanelBox.height <= saveButtonBox.y &&
        saveButtonBox.y + saveButtonBox.height <=
          expandedDialogBox.y + expandedDialogBox.height &&
        cancelButtonBox.y + cancelButtonBox.height <=
          expandedDialogBox.y + expandedDialogBox.height &&
        expandedDialogBox.y + expandedDialogBox.height <=
          (await page.evaluate(() => window.innerHeight)),
      `Expanded Genres and both actions should fit inside the modal viewport: ${JSON.stringify({ expandedDialogBox, genrePanelBox: modalGenrePanelBox, saveButtonBox, cancelButtonBox })}`,
    );
    assert.ok(
      expandedFooterGap >= 16,
      "Expanded Genre state should retain padding below the modal actions",
    );
    await dialog.getByRole("button", { name: "Genres" }).click();
    await wait(200);
    const collapsedDialogBox = await dialog.boundingBox();
    const collapsedCancelBox = await dialog
      .getByRole("button", { name: "Cancel" })
      .boundingBox();
    const collapsedFooterGap =
      collapsedDialogBox && collapsedCancelBox
        ? collapsedDialogBox.y +
          collapsedDialogBox.height -
          collapsedCancelBox.y -
          collapsedCancelBox.height
        : -1;
    assert.ok(
      Math.abs(collapsedFooterGap - expandedFooterGap) < 2,
      "Modal action padding should stay consistent when Genre options collapse",
    );
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
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.evaluate(() => window.scrollTo(0, 700));
    await wait(200);
    const filterBox = await movieFilters.boundingBox();
    assert.ok(filterBox && Math.abs(filterBox.y - 16) < 2, "Movie filters stick in the sidebar");
    await shows.getByRole("heading", { name: "Manage TV Shows" }).scrollIntoViewIfNeeded();
    await showFilters.waitFor();
    assert.equal(await movieFilters.isVisible(), false);
    await page.evaluate(() => window.scrollTo(0, 0));
    await movieFilters.waitFor();
    await movies.getByRole("button", { name: "Edit Film 01", exact: true }).click();
    await page.getByRole("dialog").waitFor();
    assert.equal(await page.evaluate(() => getComputedStyle(document.documentElement).overflowY), "hidden");
    const lockedY = await page.evaluate(() => window.scrollY);
    await page.mouse.move(10, 500);
    await page.mouse.wheel(0, 600);
    await wait(100);
    assert.equal(await page.evaluate(() => window.scrollY), lockedY, "Modal blocks background scrolling");
    await page.keyboard.press("Escape");
    await page.getByRole("dialog").waitFor({ state: "hidden" });
    assert.notEqual(await page.evaluate(() => getComputedStyle(document.documentElement).overflowY), "hidden");
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
    const mobileHeaderLayout = await movies.evaluate((element) => {
      const title = element
        .querySelector(".library-box-title")
        .getBoundingClientRect();
      const actions = element
        .querySelector(".library-box-actions")
        .getBoundingClientRect();
      const pageControls = element
        .querySelector(".library-page-controls")
        .getBoundingClientRect();
      return { title, actions, pageControls };
    });
    assert.ok(
      Math.abs(
        mobileHeaderLayout.title.top - mobileHeaderLayout.pageControls.top,
      ) < 2 &&
        mobileHeaderLayout.actions.top >= mobileHeaderLayout.title.bottom - 1 &&
        mobileHeaderLayout.title.left < mobileHeaderLayout.pageControls.left,
      "Mobile catalog header should left-align title, keep page controls right, and place actions beneath",
    );
    assert.ok(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth + 1,
      ),
      "Page overflow: " +
        JSON.stringify(
          await page.evaluate(() =>
            [...document.querySelectorAll("body *")]
              .filter(
                (e) =>
                  e.getBoundingClientRect().right > innerWidth + 1 &&
                  !e.closest(".library-table-scroll") &&
                  getComputedStyle(e).position !== "absolute",
              )
              .slice(0, 20)
              .map((e) => ({
                tag: e.tagName,
                cls: e.className,
                width: e.getBoundingClientRect().width,
                right: e.getBoundingClientRect().right,
              })),
          ),
        ),
    );
    await shows
      .getByRole("button", { name: "Edit Example Academy", exact: true })
      .click();
    dialog = page.getByRole("dialog");
    await page.screenshot({
      path: path.join(root, "library-mobile-dialog.png"),
      fullPage: false,
    });
    assert.ok(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth + 1,
      ),
      "Dialog must not overflow at mobile width",
    );
    await page.keyboard.press("Escape");
    await dialog.waitFor({ state: "hidden" });
    assert.equal(
      await shows
        .getByRole("button", { name: "Edit Example Academy", exact: true })
        .evaluate((element) => element === document.activeElement),
      true,
      "Dialog restores focus",
    );
    const assertShellAligned = async (contentSelector) => {
      const header = await page.locator(".admin-topbar").boundingBox();
      const content = await page.locator(contentSelector).boundingBox();
      assert.ok(
        header &&
          content &&
          Math.abs(header.x - content.x) < 1 &&
          Math.abs(header.width - content.width) < 1,
        `${contentSelector} should align with the 1280px header shell`,
      );
    };
    await page.goto(url + "/profile");
    await page.getByRole("heading", { name: "Display Name" }).waitFor();
    await assertShellAligned(".admin-content");
    await page.goto(url + "/account");
    await page.locator(".admin-content").waitFor();
    await assertShellAligned(".admin-content");
    await page.setViewportSize({ width: 1440, height: 1100 });
    const measureNoticeGap = async () =>
      page.evaluate(() => {
        const headerBottom = document
          .querySelector(".admin-topbar")
          .getBoundingClientRect().bottom;
        const contentTop = document
          .querySelector(".startup-notice")
          .getBoundingClientRect().top;
        return contentTop - headerBottom;
      });
    await page.goto(url + "/admin/dashboard");
    await page.locator(".startup-notice").waitFor();
    const dashboardGap = await measureNoticeGap();
    await page.goto(url + "/admin/manage-media");
    await page.locator(".startup-notice").waitFor();
    const manageMediaGap = await measureNoticeGap();
    assert.ok(
      Math.abs(dashboardGap - manageMediaGap) < 1,
      "Manage Media's top content spacing should match Dashboard",
    );
    await page.goto(url + "/admin/server");
    const desktopSettingsNav = page.getByRole("navigation", {
      name: "Server Settings",
    });
    await desktopSettingsNav.waitFor();
    await assertShellAligned(".admin-layout");
    const desktopNavBox = await desktopSettingsNav.boundingBox();
    const desktopContentBox = await page
      .locator(".admin-content")
      .boundingBox();
    assert.ok(
      desktopNavBox &&
        desktopContentBox &&
        Math.abs(
          desktopContentBox.x - desktopNavBox.x - desktopNavBox.width - 12,
        ) < 1,
      "Standard Server Settings should use a 12px sidebar-to-content gap",
    );
    await page.setViewportSize({ width: 769, height: 1000 });
    assert.equal(await desktopSettingsNav.isVisible(), true);
    assert.equal(
      await page
        .getByRole("button", { name: "Server Settings Navigation" })
        .isVisible(),
      false,
    );
    await page.setViewportSize({ width: 768, height: 1000 });
    assert.equal(await desktopSettingsNav.isVisible(), false);
    const responsiveNavButton = page.getByRole("button", {
      name: "Server Settings Navigation",
    });
    assert.equal(await responsiveNavButton.isVisible(), true);
    const mobileBrandBox = await page
      .locator(".admin-topbar-inner .brand-link")
      .boundingBox();
    const mobileProfileBox = await page
      .locator(".admin-topbar-inner > .profile-menu")
      .boundingBox();
    const mobileNavBox = await responsiveNavButton.boundingBox();
    const mobileBackBox = await page
      .getByRole("button", { name: "Back To Guide" })
      .boundingBox();
    const mobileHeaderBox = await page
      .locator(".admin-topbar-inner")
      .boundingBox();
    assert.ok(
      mobileBrandBox &&
        mobileProfileBox &&
        mobileNavBox &&
        mobileBackBox &&
        Math.abs(mobileBrandBox.y - mobileProfileBox.y) < 2 &&
        Math.abs(mobileNavBox.y - mobileBackBox.y) < 2 &&
        mobileNavBox.y > mobileBrandBox.y &&
        Math.abs(mobileNavBox.x - mobileHeaderBox.x - 12) < 1 &&
        mobileNavBox.x + mobileNavBox.width < mobileBackBox.x,
      `At 768px, header should use two rows with logo/profile above left-aligned hamburger/back controls: ${JSON.stringify({ mobileBrandBox, mobileProfileBox, mobileNavBox, mobileBackBox, mobileHeaderBox })}`,
    );
    assert.deepEqual(errors, []);
    console.log(
      "PASS: Library Paths order, expanded navigation, sticky section filters, modal scroll lock, three-column catalog, pagination, cross-page bulk edits, official rating preservation, show-level inheritance, mobile layout and keyboard focus.",
    );
  } finally {
    await browser.close();
  }
})().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
