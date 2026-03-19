"use client";

import { useEffect, useEffectEvent } from "react";
import { capitalize } from "@/lib/utils";
import { toast } from "sonner";

interface Props {
  provider?: string;
}

export function LinkedNotice({ provider }: Props) {
  // useEffectEvent captures the latest `provider` value without stale closures.
  // React 19.2 stable API — replaces the fired.current guard pattern.
  const fireNotice = useEffectEvent(() => {
    if (!provider) return;
    toast.success(`${capitalize(provider)} account linked successfully.`);
  });

  useEffect(() => {
    fireNotice();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return null;
}
