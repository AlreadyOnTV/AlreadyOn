// Exercise real capability negotiation and UI-only failures without changing host codecs.
module.exports = async ({ browser, login, assert }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    let scenario = "capabilities";
    let lastCapabilities;
    await page.route("**/api/channels/*/tune", async (route) => {
      const body = route.request().postDataJSON();
      lastCapabilities = body.capabilities;
      if (scenario === "capabilities") {
        body.capabilities = {
          ...body.capabilities,
          video: [],
          audio: [],
          hls: false,
        };
        return route.continue({ postData: JSON.stringify(body) });
      }
      const errors = {
        server: {
          code: "server_decoder_missing",
          error:
            "The AlreadyOn server cannot read this title’s eac3 format. Ask the server owner to install a full FFmpeg build on the server.",
        },
        native: {
          code: "native_server_decoder_missing",
          error:
            "The stream could not play. The local development server is missing the required codecs to decode this file.",
          missingCodecs: ["hevc", "eac3"],
        },
        network: { error: "The media server could not be reached." },
        budget: {
          code: "playback_incompatible",
          error: "No compatible output within the connection budget.",
        },
        missing: {
          code: "source_missing",
          error: "The stream could not play. The video file is missing.",
        },
      };
      return route.fulfill({ status: 422, json: errors[scenario] });
    });
    await login(page);
    await page.locator(".guide-slot-box").first().click();
    const help = page
      .locator(".playback-message")
      .getByRole("button", { name: "More Info", exact: true });
    const retry = async () => {
      await page.reload();
      await page.locator(".guide-slot-box").first().click();
    };
    await help.waitFor();
    assert(
      await page
        .getByRole("alert")
        .textContent()
        .then(
          (t) =>
            t ===
            "The stream could not play. The browser does not support the required video or audio codecs.More Info",
        ),
      "Client error must contain the codec message and only More Info",
    );
    assert(
      (await page
        .getByRole("button", { name: "Retry With Conversion" })
        .count()) === 0,
      "Conversion retry must not appear in the player",
    );
    await help.click();
    const modal = page.getByRole("dialog", {
      name: "Help With Browser Playback",
    });
    await modal.waitFor();
    assert(
      (await modal.locator("p").first().innerText()).trim().split(/\s+/)
        .length <= 100,
      "Codec explanation exceeds 100 words",
    );
    const links = await modal
      .locator("a")
      .evaluateAll((elements) =>
        elements.map((a) => ({
          text: a.textContent,
          href: a.href,
          target: a.target,
          rel: a.rel,
        })),
      );
    assert(
      JSON.stringify(await modal.locator("h3").allTextContents()) ===
        JSON.stringify([
          "Windows",
          "macOS",
          "Ubuntu / Debian-based Linux",
          "Fedora / Red Hat / CentOS-based Linux",
        ]),
      "OS sections must match the requested order",
    );
    assert(
      (await modal.locator("section ol").count()) === 4,
      "Each OS needs installation steps",
    );
    assert(
      links.every(
        (a) =>
          a.href.startsWith("https://") &&
          a.target === "_blank" &&
          a.rel.includes("noopener"),
      ),
      "Guide links need safe external navigation",
    );
    await page.keyboard.press("Shift+Tab");
    assert(
      await modal
        .locator("a")
        .last()
        .evaluate((a) => a === document.activeElement),
      "Focus must wrap to the last help link",
    );
    assert(
      (await modal
        .getByRole("button", { name: "Retry With Conversion" })
        .count()) === 0,
      "Conversion retry must not appear in codec help",
    );
    await page.keyboard.press("Tab");
    const close = modal.getByRole("button", { name: "Close playback help" });
    assert(
      await close.evaluate((a) => a === document.activeElement),
      "Tab must return to Close",
    );
    await page.keyboard.press("Escape");
    await modal.waitFor({ state: "detached" });
    assert(
      await help.evaluate((a) => a === document.activeElement),
      "Dismissal must restore trigger focus",
    );
    await help.click();
    await close.click();
    await modal.waitFor({ state: "detached" });
    scenario = "native";
    await retry();
    await page
      .getByRole("alert")
      .filter({
        hasText: "local development server is missing the required codecs",
      })
      .waitFor();
    await help.waitFor();
    assert(
      (await page.getByRole("alert").innerText()).includes(
        "local development server is missing the required codecs",
      ),
      "Native failure needs local development wording",
    );
    await help.click();
    const nativeModal = page.getByRole("dialog", {
      name: "Set Up Local Server Playback",
    });
    await nativeModal.waitFor();
    assert(
      (await nativeModal.locator("p").first().innerText()).includes(
        "(hevc, eac3)",
      ),
      "Native help must name missing server codecs",
    );
    assert(
      (await nativeModal.innerText()).includes(
        "sudo dnf swap ffmpeg-free ffmpeg --allowerasing",
      ),
      "Native help must give full Fedora FFmpeg setup",
    );
    assert(
      (await nativeModal.innerText()).includes("make preview"),
      "Native help must explain server restart",
    );
    assert(
      JSON.stringify(await nativeModal.locator("h3").allTextContents()) ===
        JSON.stringify([
          "Windows",
          "macOS",
          "Ubuntu / Debian-based Linux",
          "Fedora / Red Hat / CentOS-based Linux",
        ]),
      "Native OS instructions must retain requested order",
    );
    await nativeModal
      .getByRole("button", { name: "Close playback help" })
      .click();
    scenario = "server";
    await retry();
    await page
      .getByRole("alert")
      .filter({ hasText: "server cannot read" })
      .waitFor();
    assert(
      lastCapabilities.video.length > 0 && lastCapabilities.audio.length > 0,
      "Retuning must retain browser capabilities",
    );
    assert(
      (await help.count()) === 0,
      "Server decoder failure must not show client installation advice",
    );
    for (const next of ["network", "budget", "missing"]) {
      scenario = next;
      await retry();
      await page
        .getByRole("alert")
        .filter({
          hasText:
            next === "network"
              ? "could not be reached"
              : next === "missing"
                ? "video file is missing"
                : "connection budget",
        })
        .waitFor();
      assert(
        (await help.count()) === 0,
        `${next} failure misclassified as missing codecs`,
      );
    }
    await page.unroute("**/api/channels/*/tune");
    for (const code of [3, 4, 2]) {
      await retry();
      await page.waitForFunction(
        () => {
          const video = document.querySelector("video");
          return (
            video?.currentTime > 1 && !video.paused && video.readyState >= 2
          );
        },
        null,
        { timeout: 30000 },
      );
      // Exercise both the automatic fallback and its final diagnostic.
      for (let attempt = 0; attempt < 2; attempt++) {
        await page.evaluate((code) => {
          const video = document.querySelector("video");
          Object.defineProperty(video, "error", {
            configurable: true,
            value: { code },
          });
          video.dispatchEvent(new Event("error"));
          delete video.error;
        }, code);
        if (attempt === 0)
          await page.waitForFunction(
            () => {
              const video = document.querySelector("video");
              return (
                video?.currentTime > 1 && !video.paused && video.readyState >= 2
              );
            },
            null,
            { timeout: 30000 },
          );
      }
      await page.getByRole("alert").waitFor();
      assert(
        (await help.count()) === (code === 4 ? 1 : 0),
        `Wrong advice for MediaError ${code}`,
      );
      assert(
        (await page
          .getByRole("button", { name: "Retry With Conversion" })
          .count()) === 0,
        "Final errors must not offer conversion retries",
      );
    }
    console.log(
      "PASS: codec message and More Info, no conversion retry, ordered OS instructions, <=100-word introduction, focus/Escape/Close, server/network/budget/missing separation, decode/format/network media errors",
    );
  } finally {
    await context.close();
  }
};
