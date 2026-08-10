import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vite-plus/test";
import { ThroughputChart } from "./throughput-chart";

describe("ThroughputChart adapter", () => {
  it("renders an inline empty state without loading the chart renderer", () => {
    const markup = renderToStaticMarkup(
      <ThroughputChart
        points={[]}
        ariaLabel="Download throughput"
        unavailableLabel="Chart unavailable"
        emptyLabel="Waiting for telemetry"
      />,
    );
    expect(markup).toContain("Waiting for telemetry");
    expect(markup).not.toContain("Chart unavailable");
  });
});
