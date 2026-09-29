# bop demo video

A Remotion animation showing the `bop` workflow end to end: adopting an existing
Compose file, `bop up`, `bop status`, the database modes and their `-share-db` /
`-isolate-db` flags, `bop headers on`, `bop clean` and `bop down` — with the
copy-on-write numbers and the baseline-plus-overlay idea.

Every figure on screen is measured, not invented:

- 1,305 MB → 22 MB and 35 s → 9 s come from [../spikes/b-reflink.md](../spikes/b-reflink.md)
- the fall-through behaviour comes from [../spikes/a-networking.md](../spikes/a-networking.md)

## Build

```
npm install
npm run studio     # preview at http://localhost:3000
npm run render     # writes out/bop.mp4
```

1280×720, 30 fps, 1180 frames (39 s).

The committed copies live in `../docs/`: `demo.gif` (960×540, 10 fps, 2.5 MB) is what
the top-level README embeds, because GitHub will not play a repo-relative `.mp4`
inline. `demo.mp4` is the full-resolution version. Regenerate both with:

```
npm run render                                                   # ../docs/demo.mp4 source
npx remotion render src/index.ts BopDemo out/bop.gif \
  --codec=gif --every-nth-frame=3 --scale=0.75                   # ../docs/demo.gif source
```

## Layout

```
src/
  index.ts                  registers the root
  Root.tsx                  the composition
  theme.ts                  palette, fonts, scene boundaries
  components.tsx            Terminal, ServiceBox, entrance helpers
  compositions/BopDemo.tsx  the ten scenes
```

Scene boundaries live in `theme.ts` so `Root.tsx` and the composition cannot disagree
about the duration.

The GIF runs at 10 fps rather than 15. The scenes are mostly static cards with fades,
so the motion loss is slight, and it takes the file from 3.4 MB to 2.5 MB — which
matters for something the README loads on every page view.
