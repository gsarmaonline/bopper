// A single palette, so every scene reads as one system.
export const C = {
  bg: "#0d1117",
  panel: "#161b22",
  panelEdge: "#30363d",
  text: "#e6edf3",
  dim: "#8b949e",
  blue: "#58a6ff",
  green: "#3fb950",
  red: "#f85149",
  amber: "#d29922",
  violet: "#bc8cff",
};

export const FONT_MONO =
  "'SF Mono', Menlo, Monaco, 'Cascadia Code', 'Roboto Mono', monospace";
export const FONT_SANS =
  "Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif";

// Scene boundaries, in frames at 30fps. Kept in one place so Root and the
// composition cannot disagree.
export const SCENES = {
  title: { from: 0, dur: 70 },
  problem: { from: 70, dur: 105 },
  adopt: { from: 175, dur: 130 },
  up: { from: 305, dur: 150 },
  overlay: { from: 455, dur: 125 },
  status: { from: 580, dur: 100 },
  data: { from: 680, dur: 160 },
  headers: { from: 840, dur: 140 },
  clean: { from: 980, dur: 105 },
  down: { from: 1085, dur: 95 },
};
export const TOTAL = 1180;
