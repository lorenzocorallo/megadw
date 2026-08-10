import { Component, type ErrorInfo, type ReactNode } from "react";
import { defineChart, lineY } from "@tanstack/charts";
import { scaleLinear } from "@tanstack/charts-scales/linear";
import { Chart } from "@tanstack/react-charts";

export type ThroughputPoint = {
  timestamp: number;
  bytesPerSecond: number;
};

type ThroughputChartProps = {
  points: ThroughputPoint[];
  ariaLabel: string;
  unavailableLabel: string;
  emptyLabel: string;
};

export function ThroughputChart(props: ThroughputChartProps) {
  if (props.points.length < 2) {
    return (
      <p className="flex min-h-48 items-center justify-center text-sm text-slate-500">
        {props.emptyLabel}
      </p>
    );
  }
  return (
    <ChartFailureBoundary fallback={props.unavailableLabel}>
      <TanStackThroughputChart {...props} />
    </ChartFailureBoundary>
  );
}

function TanStackThroughputChart({ points, ariaLabel }: ThroughputChartProps) {
  const definition = defineChart(() => ({
    marks: [lineY(points, { x: "timestamp", y: "bytesPerSecond" })],
    x: { scale: scaleLinear },
    y: { scale: scaleLinear, nice: true, grid: true },
  }));
  return (
    <Chart definition={definition} ariaLabel={ariaLabel} ariaDescription={ariaLabel} height={240} />
  );
}

class ChartFailureBoundary extends Component<
  { fallback: string; children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  componentDidCatch(_error: Error, _info: ErrorInfo) {
    // The detail route remains useful when a pre-1.0 chart renderer fails.
  }

  render() {
    if (this.state.failed) {
      return (
        <p className="flex min-h-48 items-center justify-center text-sm text-amber-300">
          {this.props.fallback}
        </p>
      );
    }
    return this.props.children;
  }
}
