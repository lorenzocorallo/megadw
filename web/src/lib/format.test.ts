import { describe, expect, it } from "vite-plus/test";
import { formatBytes } from "./format";

describe("formatBytes", () => {
  it("keeps byte values readable at unit boundaries", () => {
    expect(formatBytes(512, ["B", "KiB", "MiB"])).toBe("512 B");
    expect(formatBytes(1024, ["B", "KiB", "MiB"])).toBe("1.0 KiB");
    expect(formatBytes(1024 * 1024, ["B", "KiB", "MiB"])).toBe("1.0 MiB");
  });
});
