import React from "react";
import {
  AbsoluteFill,
  Sequence,
  interpolate,
  useCurrentFrame,
} from "remotion";
import { C, FONT_MONO, FONT_SANS, SCENES } from "../theme";
import { ServiceBox, SceneTitle, Terminal, useFadeUp, usePop } from "../components";

const Stage: React.FC<{ children: React.ReactNode }> = ({ children }) => (
  <AbsoluteFill style={{ backgroundColor: C.bg, fontFamily: FONT_SANS }}>{children}</AbsoluteFill>
);

/* ------------------------------------------------------------------ title */
const Title: React.FC = () => {
  const frame = useCurrentFrame();
  const pop = usePop(2);
  const sub = useFadeUp(18);
  const rule = interpolate(frame, [14, 34], [0, 260], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
  return (
    <Stage>
      <AbsoluteFill style={{ justifyContent: "center", alignItems: "center" }}>
        <div
          style={{
            transform: `scale(${0.9 + pop * 0.1})`,
            opacity: pop,
            fontFamily: FONT_MONO,
            fontSize: 132,
            fontWeight: 700,
            color: C.text,
            letterSpacing: -4,
          }}
        >
          bop
        </div>
        <div style={{ width: rule, height: 3, background: C.blue, borderRadius: 2, marginTop: 6 }} />
        <div style={{ ...sub, marginTop: 26, fontSize: 30, color: C.dim, textAlign: "center" }}>
          copy-on-write Docker Compose
          <br />
          for git worktrees
        </div>
      </AbsoluteFill>
    </Stage>
  );
};

/* ---------------------------------------------------------------- problem */
const Problem: React.FC = () => {
  const frame = useCurrentFrame();
  const head = useFadeUp(4);
  const services = ["web", "orders", "payments", "postgres", "redis"];
  return (
    <Stage>
      <SceneTitle>the problem</SceneTitle>
      <AbsoluteFill style={{ justifyContent: "center", alignItems: "center" }}>
        <div style={{ ...head, fontSize: 34, color: C.text, marginBottom: 34 }}>
          Every worktree starts a <span style={{ color: C.red }}>full</span> stack
        </div>
        <div style={{ display: "flex", gap: 46 }}>
          {["main", "feature-x", "bugfix-y"].map((name, col) => {
            const at = 16 + col * 12;
            const o = interpolate(frame, [at, at + 14], [0, 1], {
              extrapolateLeft: "clamp",
              extrapolateRight: "clamp",
            });
            const y = interpolate(frame, [at, at + 14], [26, 0], {
              extrapolateLeft: "clamp",
              extrapolateRight: "clamp",
            });
            return (
              <div key={name} style={{ opacity: o, transform: `translateY(${y}px)`, textAlign: "center" }}>
                <div style={{ fontFamily: FONT_MONO, fontSize: 19, color: col === 0 ? C.green : C.dim, marginBottom: 12 }}>
                  {name}
                </div>
                <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
                  {services.map((s) => (
                    <ServiceBox key={s} label={s} scale={0.86} />
                  ))}
                </div>
              </div>
            );
          })}
        </div>
        <div
          style={{
            ...useFadeUp(58),
            marginTop: 30,
            fontFamily: FONT_MONO,
            fontSize: 24,
            color: C.red,
          }}
        >
          15 containers · 3 × node_modules · 3 × cold builds
        </div>
      </AbsoluteFill>
    </Stage>
  );
};

/* --------------------------------------------------------------- bop up */
const Counter: React.FC<{ from: number; to: number; start: number; unit: string; color: string }> = ({
  from, to, start, unit, color,
}) => {
  const frame = useCurrentFrame();
  const v = interpolate(frame, [start, start + 34], [from, to], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
  return (
    <span style={{ color, fontFamily: FONT_MONO, fontWeight: 700 }}>
      {Math.round(v).toLocaleString()} {unit}
    </span>
  );
};

const Stat: React.FC<{ label: string; before: string; children: React.ReactNode }> = ({
  label, before, children,
}) => (
  <div style={{ textAlign: "center" }}>
    <div style={{ fontSize: 17, color: C.dim, letterSpacing: 1.5 }}>{label}</div>
    <div
      style={{
        fontFamily: FONT_MONO,
        fontSize: 20,
        color: "#5b6672",
        textDecoration: "line-through",
        marginTop: 4,
      }}
    >
      {before}
    </div>
    <div style={{ marginTop: 2 }}>{children}</div>
  </div>
);

const Up: React.FC = () => {
  const stat = useFadeUp(72);
  return (
    <Stage>
      <SceneTitle>bop up</SceneTitle>
      <AbsoluteFill style={{ justifyContent: "center", alignItems: "center" }}>
        <Terminal
          start={4}
          command="bop up feature-x"
          width={820}
          lines={[
            { text: "workspace feature-x", color: C.text, bold: true },
            { text: "  dir     ~/myapp-feature-x", color: C.dim },
            { text: "  branch  feature-x (from main)", color: C.dim },
            { text: "  env     starting 1 of 4 services...", color: C.dim },
            { text: "  overlay orders", color: C.blue },
            { text: "  url     http://feature-x.localhost:8080", color: C.green },
          ]}
        />
        <div style={{ ...stat, marginTop: 34, textAlign: "center" }}>
          <div style={{ fontSize: 22, color: C.dim, marginBottom: 14 }}>
            reflink clone · 41k files · dependencies and build cache included
          </div>
          <div style={{ display: "flex", gap: 46, alignItems: "flex-start", fontSize: 40 }}>
            <Stat label="DISK" before="1,305 MB">
              <Counter from={1305} to={22} start={78} unit="MB" color={C.green} />
            </Stat>
            <div style={{ width: 1, height: 82, background: C.panelEdge }} />
            <Stat label="TIME" before="35 s">
              <Counter from={35} to={9} start={78} unit="s" color={C.green} />
            </Stat>
            <div style={{ width: 1, height: 82, background: C.panelEdge }} />
            <Stat label="REINSTALL" before="npm install">
              <span style={{ color: C.green, fontFamily: FONT_MONO, fontWeight: 700 }}>none</span>
            </Stat>
          </div>
        </div>
      </AbsoluteFill>
    </Stage>
  );
};

/* ------------------------------------------------------- baseline+overlay */
const Overlay: React.FC = () => {
  const frame = useCurrentFrame();
  const head = useFadeUp(2);
  const ovl = usePop(26);
  const arrow = interpolate(frame, [54, 74], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
  const note = useFadeUp(78);
  const rows = ["orders", "payments", "postgres", "redis"];

  return (
    <Stage>
      <SceneTitle>one baseline, thin overlays</SceneTitle>
      <AbsoluteFill style={{ justifyContent: "center", alignItems: "center" }}>
        <div style={{ ...head, fontSize: 32, color: C.text, marginBottom: 30 }}>
          Start only what the branch changed
        </div>

        <div style={{ display: "flex", alignItems: "flex-start" }}>
          {/* baseline column */}
          <div style={{ textAlign: "center" }}>
            <ColHead color={C.green}>baseline · main</ColHead>
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              {rows.map((r) => (
                <ServiceBox key={r} label={r} />
              ))}
            </div>
          </div>

          {/* connector column: the arrow runs overlay -> baseline, because an
              unchanged service falls through to the baseline's copy */}
          <div style={{ width: 210, paddingTop: 34 }}>
            <div style={{ height: 54 }} />
            <div style={{ opacity: arrow, textAlign: "center" }}>
              <div style={{ fontFamily: FONT_MONO, fontSize: 16, color: C.violet, marginBottom: 6 }}>
                falls through
              </div>
              <div style={{ display: "flex", alignItems: "center", padding: "0 18px" }}>
                <div
                  style={{
                    width: 0,
                    height: 0,
                    borderTop: "7px solid transparent",
                    borderBottom: "7px solid transparent",
                    borderRight: `10px solid ${C.violet}`,
                  }}
                />
                <div style={{ flex: 1, height: 2, background: C.violet }} />
              </div>
            </div>
          </div>

          {/* overlay column */}
          <div style={{ textAlign: "center" }}>
            <ColHead color={C.blue}>overlay · feature-x</ColHead>
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <div style={{ transform: `scale(${0.85 + ovl * 0.15})`, opacity: ovl }}>
                <ServiceBox label="orders" tone="changed" />
              </div>
              {rows.slice(1).map((r) => (
                <ServiceBox key={r} label={r} tone="ghost" />
              ))}
            </div>
          </div>
        </div>

        <div style={{ ...note, marginTop: 30, fontSize: 22, color: C.dim }}>
          1 container instead of 4 — the rest is already running
        </div>
      </AbsoluteFill>
    </Stage>
  );
};

const ColHead: React.FC<{ color: string; children: React.ReactNode }> = ({ color, children }) => (
  <div style={{ fontFamily: FONT_MONO, fontSize: 19, color, marginBottom: 12 }}>{children}</div>
);

/* ------------------------------------------------------------ bop status */
const Status: React.FC = () => {
  const note = useFadeUp(66);
  return (
    <Stage>
      <SceneTitle>bop status</SceneTitle>
      <AbsoluteFill style={{ justifyContent: "center", alignItems: "center" }}>
        <Terminal
          start={4}
          command="bop status feature-x"
          width={880}
          lineDelay={7}
          lines={[
            { text: "feature-x vs baseline (myapp)", color: C.text, bold: true },
            { text: " " },
            { text: "SERVICE   CHANGED  BASELINE          WORKSPACE", color: C.dim },
            { text: "orders    build    33e60ca063bb563b  8a2dff74283db72c", color: C.blue },
            { text: "payments  config   ab7738be1c5b0619  1c9f59e4856ba851", color: C.amber },
          ]}
        />
        <div style={{ ...note, marginTop: 32, fontSize: 22, color: C.dim, textAlign: "center" }}>
          Input hashes, not image digests —{" "}
          <span style={{ color: C.text }}>no build runs to decide what to build</span>
        </div>
      </AbsoluteFill>
    </Stage>
  );
};

/* ----------------------------------------------------------- bop headers */
const Headers: React.FC = () => {
  const frame = useCurrentFrame();
  const head = useFadeUp(2);
  const rowIn = (at: number) =>
    interpolate(frame, [at, at + 14], [0, 1], {
      extrapolateLeft: "clamp",
      extrapolateRight: "clamp",
    });
  const note = useFadeUp(96);

  // Fixed column widths, so both rows line up however wide the tag text is.
  const Row: React.FC<{
    at: number;
    tag: string;
    tagColor: string;
    overlay: boolean;
    label: string;
  }> = ({ at, tag, tagColor, overlay, label }) => (
    <div
      style={{
        opacity: rowIn(at),
        display: "flex",
        alignItems: "center",
        fontFamily: FONT_MONO,
        fontSize: 19,
      }}
    >
      <div style={{ width: 130 }}>
        <ServiceBox label="payments" scale={0.8} />
      </div>
      <div style={{ width: 300, textAlign: "center", color: tagColor }}>{tag}</div>
      <div style={{ display: "flex", alignItems: "center", width: 70 }}>
        <div style={{ flex: 1, height: 2, background: tagColor }} />
        <div
          style={{
            width: 0,
            height: 0,
            borderTop: "6px solid transparent",
            borderBottom: "6px solid transparent",
            borderLeft: `9px solid ${tagColor}`,
          }}
        />
      </div>
      <div style={{ width: 150, textAlign: "center" }}>
        <ServiceBox label="orders" scale={0.8} tone={overlay ? "changed" : "base"} />
        <div style={{ fontSize: 14, color: overlay ? C.blue : C.dim, marginTop: 5 }}>{label}</div>
      </div>
    </div>
  );

  return (
    <Stage>
      <SceneTitle>bop headers on</SceneTitle>
      <AbsoluteFill style={{ justifyContent: "center", alignItems: "center" }}>
        <div style={{ ...head, fontSize: 32, color: C.text, marginBottom: 12 }}>
          A baseline service can reach the overlay
        </div>
        <div style={{ ...head, fontSize: 21, color: C.dim, marginBottom: 30 }}>
          so the changed service no longer has to sit at the edge
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
          <Row at={24} tag="calls orders" tagColor={C.dim} overlay={false} label="baseline" />
          <Row at={54} tag="X-Worktree: feature-x" tagColor={C.violet} overlay label="overlay" />
        </div>
        <div style={{ ...note, marginTop: 30, fontSize: 21, color: C.dim, textAlign: "center" }}>
          Opt-in. Your services must forward the header —{" "}
          <span style={{ color: C.amber }}>the tool cannot do that for you</span>
        </div>
      </AbsoluteFill>
    </Stage>
  );
};

/* ------------------------------------------------------------- bop clean */
const Clean: React.FC = () => {
  const note = useFadeUp(64);
  return (
    <Stage>
      <SceneTitle>bop clean</SceneTitle>
      <AbsoluteFill style={{ justifyContent: "center", alignItems: "center" }}>
        <Terminal
          start={4}
          command="bop clean"
          width={900}
          lineDelay={8}
          lines={[
            { text: "ACTION  KIND       NAME                   WHY", color: C.dim },
            { text: "stop    container  ws-feature-x-orders-1  idle 4h12m", color: C.amber },
            { text: "remove  container  ws-old-orders-1        workspace no longer exists", color: C.red },
          ]}
        />
        <div style={{ ...note, marginTop: 30, fontSize: 21, color: C.dim, textAlign: "center" }}>
          Docker records no last-access time.{" "}
          <span style={{ color: C.text }}>The proxy log does.</span>
        </div>
      </AbsoluteFill>
    </Stage>
  );
};

/* -------------------------------------------------------------- bop down */
const Down: React.FC = () => {
  const frame = useCurrentFrame();
  const out = interpolate(frame, [40, 62], [1, 0], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
  const end = useFadeUp(58);
  return (
    <Stage>
      <div style={{ opacity: out }}>
        <SceneTitle>bop down</SceneTitle>
      </div>
      <AbsoluteFill style={{ justifyContent: "center", alignItems: "center" }}>
        <div style={{ opacity: out, transform: `translateY(${(1 - out) * -20}px)` }}>
          <Terminal
            start={2}
            command="bop down feature-x -delete-branch"
            width={820}
            typeFor={30}
            lines={[{ text: "removed workspace feature-x", color: C.green, bold: true }]}
          />
        </div>
        <div
          style={{
            ...end,
            position: "absolute",
            textAlign: "center",
          }}
        >
          <div style={{ fontFamily: FONT_MONO, fontSize: 60, fontWeight: 700, color: C.text }}>
            bop
          </div>
          <div style={{ fontSize: 24, color: C.dim, marginTop: 12 }}>
            worktree, containers and clones — gone
          </div>
          <div style={{ fontFamily: FONT_MONO, fontSize: 20, color: C.blue, marginTop: 22 }}>
            github.com/gsarmaonline/bopper
          </div>
        </div>
      </AbsoluteFill>
    </Stage>
  );
};

/* ----------------------------------------------------------------- root */
export const BopDemo: React.FC = () => (
  <AbsoluteFill style={{ backgroundColor: C.bg }}>
    <Sequence from={SCENES.title.from} durationInFrames={SCENES.title.dur}>
      <Title />
    </Sequence>
    <Sequence from={SCENES.problem.from} durationInFrames={SCENES.problem.dur}>
      <Problem />
    </Sequence>
    <Sequence from={SCENES.up.from} durationInFrames={SCENES.up.dur}>
      <Up />
    </Sequence>
    <Sequence from={SCENES.overlay.from} durationInFrames={SCENES.overlay.dur}>
      <Overlay />
    </Sequence>
    <Sequence from={SCENES.status.from} durationInFrames={SCENES.status.dur}>
      <Status />
    </Sequence>
    <Sequence from={SCENES.headers.from} durationInFrames={SCENES.headers.dur}>
      <Headers />
    </Sequence>
    <Sequence from={SCENES.clean.from} durationInFrames={SCENES.clean.dur}>
      <Clean />
    </Sequence>
    <Sequence from={SCENES.down.from} durationInFrames={SCENES.down.dur}>
      <Down />
    </Sequence>
  </AbsoluteFill>
);
