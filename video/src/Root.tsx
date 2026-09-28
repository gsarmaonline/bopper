import React from "react";
import { Composition } from "remotion";
import { BopDemo } from "./compositions/BopDemo";
import { TOTAL } from "./theme";

export const RemotionRoot: React.FC = () => (
  <Composition
    id="BopDemo"
    component={BopDemo}
    durationInFrames={TOTAL}
    fps={30}
    width={1280}
    height={720}
  />
);
