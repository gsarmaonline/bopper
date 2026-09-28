# bop demo video

A Remotion animation showing the `bop` workflow: `bop up`, `bop status`, `bop down`,
with the copy-on-write numbers and the baseline-plus-overlay idea.

Every figure on screen is measured, not invented:

- 1,305 MB → 22 MB and 35 s → 9 s come from [../spikes/b-reflink.md](../spikes/b-reflink.md)
- the fall-through behaviour comes from [../spikes/a-networking.md](../spikes/a-networking.md)

## Build

```
npm install
npm run studio     # preview at http://localhost:3000
npm run render     # writes out/bop.mp4
```

1280×720, 30 fps, 665 frames (22 s).

## Layout

```
src/
  index.ts                  registers the root
  Root.tsx                  the composition
  theme.ts                  palette, fonts, scene boundaries
  components.tsx            Terminal, ServiceBox, entrance helpers
  compositions/BopDemo.tsx  the six scenes
```

Scene boundaries live in `theme.ts` so `Root.tsx` and the composition cannot disagree
about the duration.
