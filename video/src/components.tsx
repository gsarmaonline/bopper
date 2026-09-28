import React from "react";
import { interpolate, spring, useCurrentFrame, useVideoConfig } from "remotion";
import { C, FONT_MONO, FONT_SANS } from "./theme";

/** fadeUp returns opacity and a small upward slide, the workhorse entrance. */
export const useFadeUp = (start: number, dist = 24, len = 16) => {
  const frame = useCurrentFrame();
  const opacity = interpolate(frame, [start, start + len], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
  const y = interpolate(frame, [start, start + len], [dist, 0], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
  return { opacity, transform: `translateY(${y}px)` };
};

export const usePop = (start: number) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  return spring({
    frame: frame - start,
    fps,
    config: { damping: 14, stiffness: 190 },
  });
};

/** SceneTitle is the small label every scene carries in the top left. */
export const SceneTitle: React.FC<{ children: React.ReactNode; start?: number }> = ({
  children,
  start = 0,
}) => {
  const s = useFadeUp(start, 12);
  return (
    <div
      style={{
        ...s,
        position: "absolute",
        top: 44,
        left: 64,
        fontFamily: FONT_SANS,
        fontSize: 22,
        fontWeight: 600,
        letterSpacing: 2.4,
        textTransform: "uppercase",
        color: C.dim,
      }}
    >
      {children}
    </div>
  );
};

/** Terminal draws a window chrome with a typed command and revealed output. */
export const Terminal: React.FC<{
  command: string;
  lines?: { text: string; color?: string; bold?: boolean }[];
  start: number;
  typeFor?: number;
  lineDelay?: number;
  width?: number;
}> = ({ command, lines = [], start, typeFor = 26, lineDelay = 6, width = 880 }) => {
  const frame = useCurrentFrame();
  const shell = useFadeUp(start, 20);

  const typed = Math.round(
    interpolate(frame, [start + 8, start + 8 + typeFor], [0, command.length], {
      extrapolateLeft: "clamp",
      extrapolateRight: "clamp",
    })
  );
  const caretOn = frame % 24 < 14;
  const outStart = start + 10 + typeFor;

  return (
    <div
      style={{
        ...shell,
        width,
        background: C.panel,
        border: `1px solid ${C.panelEdge}`,
        borderRadius: 12,
        overflow: "hidden",
        boxShadow: "0 24px 60px rgba(0,0,0,0.45)",
      }}
    >
      <div
        style={{
          height: 36,
          background: "#1c2128",
          borderBottom: `1px solid ${C.panelEdge}`,
          display: "flex",
          alignItems: "center",
          paddingLeft: 14,
          gap: 8,
        }}
      >
        {[C.red, C.amber, C.green].map((c) => (
          <div key={c} style={{ width: 11, height: 11, borderRadius: 6, background: c }} />
        ))}
      </div>
      <div style={{ padding: "20px 24px 24px", fontFamily: FONT_MONO, fontSize: 21, lineHeight: 1.65 }}>
        <div style={{ color: C.text }}>
          <span style={{ color: C.green }}>$ </span>
          {command.slice(0, typed)}
          {typed < command.length && caretOn ? (
            <span style={{ background: C.blue, color: C.blue }}>|</span>
          ) : null}
        </div>
        {lines.map((l, i) => {
          const at = outStart + i * lineDelay;
          const o = interpolate(frame, [at, at + 8], [0, 1], {
            extrapolateLeft: "clamp",
            extrapolateRight: "clamp",
          });
          return (
            <div
              key={i}
              style={{
                opacity: o,
                color: l.color ?? C.dim,
                fontWeight: l.bold ? 700 : 400,
                whiteSpace: "pre",
              }}
            >
              {l.text}
            </div>
          );
        })}
      </div>
    </div>
  );
};

/** ServiceBox is one container in the stack diagrams. */
export const ServiceBox: React.FC<{
  label: string;
  tone?: "base" | "changed" | "ghost";
  scale?: number;
  style?: React.CSSProperties;
}> = ({ label, tone = "base", scale = 1, style }) => {
  const palette = {
    base: { bg: "#1b2430", border: C.panelEdge, fg: C.text },
    changed: { bg: "rgba(88,166,255,0.16)", border: C.blue, fg: C.blue },
    ghost: { bg: "transparent", border: "#242c36", fg: "#414c58" },
  }[tone];
  return (
    <div
      style={{
        width: 150 * scale,
        height: 46 * scale,
        borderRadius: 8,
        background: palette.bg,
        border: `${tone === "changed" ? 2 : 1}px ${tone === "ghost" ? "dashed" : "solid"} ${palette.border}`,
        color: palette.fg,
        fontFamily: FONT_MONO,
        fontSize: 18 * scale,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        ...style,
      }}
    >
      {label}
    </div>
  );
};
