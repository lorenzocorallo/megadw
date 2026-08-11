import { describe, expect, it } from "vite-plus/test";
import common from "./locales/en/common.json";

describe("English application resources", () => {
  it("contain the bootstrap shell copy", () => {
    expect(common.app.name).toBe("megadw");
    expect(common.nav.dashboard).toBe("Dashboard");
  });
});
