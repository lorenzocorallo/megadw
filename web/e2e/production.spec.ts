import { expect, test } from "@playwright/test";

test("production embedded binary serves setup, API, SSE, and direct SPA routes", async ({
  page,
  request,
}) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/setup$/);
  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill("correct horse battery");
  const sseResponse = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/events") && response.status() === 200,
  );
  await page.getByRole("button", { name: "Create administrator" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  await expect(page.getByText("Live updates", { exact: true })).toBeVisible();
  expect((await sseResponse).headers()["content-type"]).toContain("text/event-stream");

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();

  const health = await request.get("/api/v1/health");
  expect(health.ok()).toBeTruthy();
  expect((await health.json()).data.status).toBe("ok");

  const version = await request.get("/api/v1/version");
  expect(version.ok()).toBeTruthy();
  const metadata = (await version.json()).data as {
    version: string;
    commit: string;
    buildTime: string;
  };
  expect(metadata.version).not.toBe("");
  expect(metadata.commit).not.toBe("");
  expect(metadata.buildTime).not.toBe("");

  for (const route of ["/", "/downloads", "/settings", "/settings/general"]) {
    await page.goto(route);
    await page.waitForLoadState("domcontentloaded");
    await page.reload();
    await expect(page.locator("body")).not.toContainText("Cannot GET");
  }
  await page.goto("/downloads");
  await expect(page.getByRole("heading", { name: "Downloads" })).toBeVisible();
  await page.goto("/settings");
  await expect(page.getByRole("heading", { name: "Settings", exact: true })).toBeVisible();
});
