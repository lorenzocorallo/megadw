export function formatBytes(value: number, units: string[]) {
  if (value < 1024) return `${value} ${units[0]}`;
  let scaled = value;
  let unit = units[0];
  for (const next of units.slice(1)) {
    scaled /= 1024;
    unit = next;
    if (scaled < 1024) break;
  }
  return `${scaled.toFixed(scaled >= 10 ? 0 : 1)} ${unit}`;
}

export function formatDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "" : date.toLocaleString();
}
