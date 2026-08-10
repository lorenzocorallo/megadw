import { expect, test, type Page, type Route } from "@playwright/test";

type Download = {
  id: string;
  sourceKind: "file" | "folder";
  displayName: string;
  totalBytes: number;
  destinationSubdirectory: string;
  completeRoot: string;
  incompleteRoot: string;
  state: string;
  createdAt: string;
  updatedAt: string;
  bytesCommitted: number;
  speedBytesPerSecond: number;
  files: Array<{
    id: string;
    finalRelativePath: string;
    sizeBytes: number;
    bytesCommitted: number;
    state: string;
  }>;
  quotaNextRetryAt?: string;
};

type MockState = {
  setupRequired: boolean;
  authenticated: boolean;
  downloads: Download[];
  accounts: Array<Record<string, unknown>>;
  proxies: Array<Record<string, unknown>>;
  settings: {
    paths: { incompleteRoot: string; completeRoot: string };
    downloads: {
      autoStart: boolean;
      segmentSizeBytes: number;
      workersPerFile: number;
      maxActiveFiles: number;
      maxGlobalWorkers: number;
      globalSpeedLimitBytesPerSecond: number;
      perJobDefaultLimitBytesPerSecond: number;
      conflictPolicy: "rename" | "overwrite" | "fail";
      checkpointIntervalMs: number;
      checkpointBytes: number;
      normalRetryLimit: number;
    };
    network: {
      connectTimeoutSeconds: number;
      responseHeaderTimeoutSeconds: number;
      readIdleTimeoutSeconds: number;
    };
    ui: { theme: "light" | "dark" | "system"; locale: "en" };
  };
};

const timestamp = "2026-08-10T12:00:00Z";

const emptyDownloads: Download[] = [];

test.describe("Phase G browser flows", () => {
  test("first-run administrator setup", async ({ page }) => {
    await installMockBackend(page, { setupRequired: true, authenticated: false });
    await page.goto("/");
    await expect(page).toHaveURL(/\/setup$/);
    await page.getByLabel("Username").fill("admin");
    await page.getByLabel("Password").fill("correct horse battery");
    await page.getByRole("button", { name: "Create administrator" }).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  });

  test("login and logout", async ({ page }) => {
    await installMockBackend(page, { setupRequired: false, authenticated: false });
    await page.goto("/");
    await expect(page).toHaveURL(/\/login$/);
    await page.getByLabel("Username").fill("admin");
    await page.getByLabel("Password").fill("correct horse battery");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).toHaveURL(/\/$/);
    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(page).toHaveURL(/\/login$/);
  });

  test("adds a valid single-file link", async ({ page }) => {
    await installMockBackend(page);
    await page.goto("/");
    await openAddDialog(page, "https://mega.nz/file/valid#key");
    await expect(page.getByTestId("resolved-download")).toContainText("example.bin");
    await page.getByRole("button", { name: "Add download" }).last().click();
    await page.getByRole("link", { name: "View all downloads" }).click();
    await expect(page.getByRole("link", { name: "example.bin" })).toBeVisible();
  });

  test("shows an invalid-link error", async ({ page }) => {
    await installMockBackend(page);
    await page.goto("/");
    await openAddDialog(page, "not-a-mega-link");
    await expect(page.getByRole("alert")).toContainText("invalid");
  });

  test("invalidates resolved metadata when the source URL changes", async ({ page }) => {
    await installMockBackend(page);
    await page.goto("/");
    await openAddDialog(page, "https://mega.nz/file/valid#key");
    await expect(page.getByTestId("resolved-download")).toBeVisible();
    await page.getByLabel("MEGA URL").fill("https://mega.nz/file/different#key");
    await expect(page.getByTestId("resolved-download")).toHaveCount(0);

    await page.getByLabel("MEGA URL").fill("https://mega.nz/file/slow#key");
    await page.getByRole("button", { name: "Resolve" }).click();
    await page.getByLabel("MEGA URL").fill("https://mega.nz/file/changed-again#key");
    await page.waitForTimeout(100);
    await expect(page.getByTestId("resolved-download")).toHaveCount(0);
  });

  test("resolves a folder and displays its file tree", async ({ page }) => {
    await installMockBackend(page);
    await page.goto("/");
    await openAddDialog(page, "https://mega.nz/folder/folder#key");
    await expect(page.getByTestId("resolved-download")).toContainText("media");
    await expect(page.getByText("media/one.bin")).toBeVisible();
    await expect(page.getByText("nested/two.bin")).toBeVisible();
  });

  test("pauses and resumes a queued download", async ({ page }) => {
    await installMockBackend(page, {
      downloads: [download("pause-1", "pause-me.bin", "downloading")],
    });
    await page.goto("/downloads");
    await page.getByRole("button", { name: "Pause", exact: true }).click();
    await expect(
      page.getByRole("table", { name: "Downloads queue" }).getByText("Paused", { exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Resume now" }).click();
    await expect(
      page
        .getByRole("table", { name: "Downloads queue" })
        .getByText("Downloading", { exact: true }),
    ).toBeVisible();
  });

  test("shows the waiting-for-quota state and retry time", async ({ page }) => {
    await installMockBackend(page, {
      downloads: [
        {
          ...download("quota-1", "quota.bin", "waiting_quota"),
          quotaNextRetryAt: "2026-08-10T12:05:00Z",
        },
      ],
    });
    await page.goto("/downloads/quota-1");
    await expect(
      page.locator("header").getByText("Waiting for quota", { exact: true }),
    ).toBeVisible();
    await expect(page.getByText("MEGA quota is exhausted.")).toBeVisible();
  });

  test("adds, tests, and removes an account", async ({ page }) => {
    await installMockBackend(page);
    await page.goto("/settings/accounts");
    await page.getByLabel("Label").fill("Personal");
    await page.getByLabel("Email").fill("personal@example.test");
    await page.getByLabel("Password").fill("account-secret");
    await page.getByRole("button", { name: "Add account" }).click();
    const row = page.getByRole("row", { name: /Personal/ });
    await expect(row).toBeVisible();
    await row.getByRole("button", { name: "Test" }).click();
    page.once("dialog", (dialog) => void dialog.accept());
    await row.getByRole("button", { name: "Remove" }).click();
    await expect(row).toHaveCount(0);
  });

  test("adds, tests, and removes a proxy", async ({ page }) => {
    await installMockBackend(page);
    await page.goto("/settings/proxies");
    await page.getByLabel("Name", { exact: true }).fill("Home proxy");
    await page.getByLabel("Type").selectOption("http");
    await page.getByLabel("Host").fill("proxy.example.test");
    await page.getByLabel("Port").fill("8080");
    await page.getByRole("button", { name: "Add proxy" }).click();
    const row = page.getByRole("row", { name: /Home proxy/ });
    await expect(row).toBeVisible();
    await row.getByRole("button", { name: "Test" }).click();
    page.once("dialog", (dialog) => void dialog.accept());
    await row.getByRole("button", { name: "Remove" }).click();
    await expect(row).toHaveCount(0);
  });

  test("persists settings across a reload", async ({ page }) => {
    await installMockBackend(page);
    await page.goto("/settings/general");
    const completeRoot = page.getByLabel("Complete root", { exact: true });
    await completeRoot.fill("/tmp/complete");
    await page.getByRole("button", { name: "Save settings" }).click();
    await expect(page.getByRole("status")).toContainText("Settings saved.");
    await page.reload();
    await expect(page.getByLabel("Complete root", { exact: true })).toHaveValue("/tmp/complete");
  });

  test("reconnects SSE without losing the current status", async ({ page }) => {
    await installMockBackend(page, {
      downloads: [download("live-1", "live.bin", "downloading")],
    });
    await page.goto("/");
    await expect(page.getByText("Live updates", { exact: true })).toBeVisible();
    await page.evaluate(() => {
      const browser = window as unknown as Window & {
        __megadSSE: { disconnect: () => void };
      };
      browser.__megadSSE.disconnect();
    });
    await expect(page.getByText("Reconnecting", { exact: true })).toBeVisible();
    await expect(page.getByText("Live updates", { exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: "live.bin" })).toBeVisible();
  });

  test("switches dark, light, and system themes", async ({ page }) => {
    await installMockBackend(page);
    await page.goto("/settings/appearance");
    const theme = page.getByLabel("Theme");
    await theme.selectOption("dark");
    await expect.poll(() => page.locator("html").getAttribute("data-theme")).toBe("dark");
    await expect
      .poll(() => page.locator("html").evaluate((element) => element.classList.contains("dark")))
      .toBe(true);
    await theme.selectOption("light");
    await expect.poll(() => page.locator("html").getAttribute("data-theme")).toBe("light");
    await expect
      .poll(() => page.locator("html").evaluate((element) => element.classList.contains("dark")))
      .toBe(false);
    await theme.selectOption("system");
    await expect.poll(() => page.locator("html").getAttribute("data-theme")).toBe("system");
  });
});

async function openAddDialog(page: Page, url: string) {
  await page.getByTestId("add-download").click();
  await page.getByLabel("MEGA URL").fill(url);
  await page.getByRole("button", { name: "Resolve" }).click();
}

function download(id: string, displayName: string, state: string): Download {
  return {
    id,
    sourceKind: "file",
    displayName,
    totalBytes: 1024,
    destinationSubdirectory: "",
    completeRoot: "/complete",
    incompleteRoot: "/incomplete",
    state,
    createdAt: timestamp,
    updatedAt: timestamp,
    bytesCommitted: state === "completed" ? 1024 : 128,
    speedBytesPerSecond: state === "downloading" ? 2048 : 0,
    files: [
      {
        id: `${id}-file`,
        finalRelativePath: displayName,
        sizeBytes: 1024,
        bytesCommitted: state === "completed" ? 1024 : 128,
        state,
      },
    ],
  };
}

async function installMockBackend(
  page: Page,
  options: {
    setupRequired?: boolean;
    authenticated?: boolean;
    downloads?: Download[];
  } = {},
): Promise<MockState> {
  const state = createMockState(options);
  await page.addInitScript(() => {
    type Source = {
      readyState: number;
      dispatchEvent: (event: Event) => boolean;
      __closed?: boolean;
    };
    const sources: Source[] = [];
    class MockEventSource extends EventTarget {
      readonly url: string;
      readonly withCredentials = true;
      readyState = 0;

      constructor(url: string) {
        super();
        this.url = url;
        sources.push(this as unknown as Source);
        setTimeout(() => {
          if (this.readyState !== 2) {
            this.readyState = 1;
            this.dispatchEvent(new Event("open"));
          }
        }, 0);
      }

      close() {
        this.readyState = 2;
        (this as unknown as Source).__closed = true;
      }
    }
    Object.defineProperty(window, "EventSource", {
      configurable: true,
      value: MockEventSource,
    });
    Object.defineProperty(window, "__megadSSE", {
      configurable: true,
      value: {
        disconnect() {
          for (const source of sources) {
            if (source.readyState !== 2) source.dispatchEvent(new Event("error"));
          }
          setTimeout(() => {
            for (const source of sources) {
              if (source.readyState !== 2) {
                source.readyState = 1;
                source.dispatchEvent(new Event("open"));
              }
            }
          }, 50);
        },
      },
    });
  });
  await page.route("**/api/v1/**", async (route) => handleMockRequest(route, state));
  return state;
}

function createMockState(options: {
  setupRequired?: boolean;
  authenticated?: boolean;
  downloads?: Download[];
}): MockState {
  return {
    setupRequired: options.setupRequired ?? false,
    authenticated: options.authenticated ?? true,
    downloads: options.downloads ?? emptyDownloads.map((item) => ({ ...item })),
    accounts: [],
    proxies: [],
    settings: {
      paths: { incompleteRoot: "/incomplete", completeRoot: "/complete" },
      downloads: {
        autoStart: true,
        segmentSizeBytes: 1_048_576,
        workersPerFile: 4,
        maxActiveFiles: 2,
        maxGlobalWorkers: 8,
        globalSpeedLimitBytesPerSecond: 0,
        perJobDefaultLimitBytesPerSecond: 0,
        conflictPolicy: "rename",
        checkpointIntervalMs: 5000,
        checkpointBytes: 8 * 1_048_576,
        normalRetryLimit: 4,
      },
      network: {
        connectTimeoutSeconds: 15,
        responseHeaderTimeoutSeconds: 30,
        readIdleTimeoutSeconds: 60,
      },
      ui: { theme: "system", locale: "en" },
    },
  };
}

async function handleMockRequest(route: Route, state: MockState) {
  const request = route.request();
  const url = new URL(request.url());
  const path = url.pathname;
  const method = request.method();
  let body: Record<string, unknown> | null = null;
  try {
    body = request.postDataJSON() as Record<string, unknown> | null;
  } catch {
    body = null;
  }
  const ok = (data: unknown, status = 200) =>
    route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify({ data }),
    });
  const fail = (message: string, status = 400) =>
    route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "mock_error", message } }),
    });

  if (path === "/api/v1/auth/status" && method === "GET") {
    return ok({ setupRequired: state.setupRequired, authenticated: state.authenticated });
  }
  if (path === "/api/v1/auth/setup" && method === "POST") {
    state.setupRequired = false;
    state.authenticated = true;
    return ok({ id: "admin", username: textValue(body, "username", "admin") }, 201);
  }
  if (path === "/api/v1/auth/login" && method === "POST") {
    if (body?.username !== "admin" || body?.password !== "correct horse battery") {
      return fail("invalid username or password", 401);
    }
    state.authenticated = true;
    return ok({ id: "admin", username: "admin" });
  }
  if (path === "/api/v1/auth/logout" && method === "POST") {
    state.authenticated = false;
    return ok({ loggedOut: true });
  }
  if (path === "/api/v1/settings" && method === "GET") return ok(state.settings);
  if (path === "/api/v1/settings" && method === "PUT") {
    state.settings = body as unknown as MockState["settings"];
    return ok(state.settings);
  }
  if (path === "/api/v1/dashboard" && method === "GET") {
    const activeJobs = state.downloads.filter((item) =>
      ["resolving", "downloading", "finalizing"].includes(item.state),
    ).length;
    return ok({
      activeJobs,
      queuedJobs: state.downloads.filter((item) => item.state === "queued").length,
      waitingQuotaJobs: state.downloads.filter((item) => item.state === "waiting_quota").length,
      currentSpeedBytesPerSecond: state.downloads.reduce(
        (total, item) => total + item.speedBytesPerSecond,
        0,
      ),
      bytesDownloadedThisSession: 128,
      diskFreeBytes: 100 * 1024 * 1024 * 1024,
    });
  }
  if (path === "/api/v1/downloads" && method === "GET") return ok(state.downloads);
  if (path === "/api/v1/downloads/resolve" && method === "POST") {
    const urlValue = textValue(body, "url");
    if (!urlValue.includes("mega.nz")) return fail("invalid MEGA link");
    if (urlValue.includes("/slow#")) await new Promise((resolve) => setTimeout(resolve, 50));
    if (urlValue.includes("folder")) {
      return ok({
        kind: "folder",
        displayName: "media",
        totalBytes: 2048,
        fileCount: 2,
        files: [
          { nodeId: "node-1", relativePath: "media/one.bin", size: 1024 },
          { nodeId: "node-2", relativePath: "nested/two.bin", size: 1024 },
        ],
      });
    }
    return ok({
      kind: "file",
      displayName: "example.bin",
      totalBytes: 1024,
      fileCount: 1,
      files: [{ nodeId: "node-1", relativePath: "example.bin", size: 1024 }],
    });
  }
  if (path === "/api/v1/downloads" && method === "POST") {
    const isFolder = textValue(body, "url").includes("folder");
    const item = download(
      `job-${state.downloads.length + 1}`,
      isFolder ? "media" : "example.bin",
      body?.startImmediately === false ? "ready" : "queued",
    );
    item.sourceKind = isFolder ? "folder" : "file";
    state.downloads.push(item);
    return ok(item, 201);
  }
  const downloadMatch = path.match(/^\/api\/v1\/downloads\/([^/]+)(?:\/(.*))?$/);
  if (downloadMatch) {
    const id = downloadMatch[1];
    const action = downloadMatch[2] ?? "";
    const item = state.downloads.find((candidate) => candidate.id === id);
    if (!item) return fail("download was not found", 404);
    if (method === "GET" && action === "") return ok(item);
    if (method === "GET" && action === "events") return ok([]);
    if (method === "POST") {
      if (action === "pause") item.state = "paused";
      if (action === "resume" || action === "retry") item.state = "downloading";
      if (action === "cancel") item.state = "cancelled";
      item.updatedAt = timestamp;
      return ok(item);
    }
    if (method === "DELETE") {
      state.downloads = state.downloads.filter((candidate) => candidate.id !== id);
      return ok({ deleted: true });
    }
  }
  if (path === "/api/v1/queue/pause" && method === "POST") return ok({ paused: true });
  if (path === "/api/v1/queue/resume" && method === "POST") return ok({ paused: false });
  if (path === "/api/v1/accounts" && method === "GET") return ok(state.accounts);
  if (path === "/api/v1/accounts" && method === "POST") {
    const account = {
      id: `account-${state.accounts.length + 1}`,
      label: textValue(body, "label"),
      email: textValue(body, "email"),
      status: "active",
      defaultForDownloads: Boolean(body?.defaultForDownloads),
    };
    state.accounts.push(account);
    return ok(account, 201);
  }
  const accountMatch = path.match(/^\/api\/v1\/accounts\/([^/]+)(?:\/(test))?$/);
  if (accountMatch) {
    const index = state.accounts.findIndex((item) => item.id === accountMatch[1]);
    if (index < 0) return fail("account was not found", 404);
    if (method === "POST" && accountMatch[2] === "test") return ok({ status: "active" });
    if (method === "PUT") {
      state.accounts[index] = { ...state.accounts[index], ...body };
      return ok(state.accounts[index]);
    }
    if (method === "DELETE") {
      state.accounts.splice(index, 1);
      return ok({ deleted: true });
    }
  }
  if (path === "/api/v1/proxies" && method === "GET") return ok(state.proxies);
  if (path === "/api/v1/proxies" && method === "POST") {
    const proxy = {
      id: `proxy-${state.proxies.length + 1}`,
      name: textValue(body, "name"),
      type: textValue(body, "type", "http"),
      host: textValue(body, "host"),
      port: Number(body?.port ?? 0),
      enabled: true,
      defaultForDownloads: Boolean(body?.defaultForDownloads),
    };
    state.proxies.push(proxy);
    return ok(proxy, 201);
  }
  const proxyMatch = path.match(/^\/api\/v1\/proxies\/([^/]+)(?:\/(test))?$/);
  if (proxyMatch) {
    const index = state.proxies.findIndex((item) => item.id === proxyMatch[1]);
    if (index < 0) return fail("proxy was not found", 404);
    if (method === "POST" && proxyMatch[2] === "test") return ok({ ok: true });
    if (method === "PUT") {
      state.proxies[index] = { ...state.proxies[index], ...body };
      return ok(state.proxies[index]);
    }
    if (method === "DELETE") {
      state.proxies.splice(index, 1);
      return ok({ deleted: true });
    }
  }
  return fail(`unhandled mock request: ${method} ${path}`, 500);
}

function textValue(body: Record<string, unknown> | null, key: string, fallback = "") {
  const value = body?.[key];
  return typeof value === "string" ? value : fallback;
}
