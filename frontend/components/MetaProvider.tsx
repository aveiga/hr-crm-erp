"use client";

import { createContext, useContext, useEffect, useState } from "react";
import { api } from "@/lib/api";
import type { Entity } from "@/lib/types";

interface MetaState {
  entities: Entity[];
  byKey: globalThis.Record<string, Entity>;
  ready: boolean;
}

const Ctx = createContext<MetaState>({ entities: [], byKey: {}, ready: false });

export function MetaProvider({ children }: { children: React.ReactNode }) {
  const [entities, setEntities] = useState<Entity[]>([]);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    api
      .meta()
      .then((m) => setEntities(m.entities))
      .catch(() => setEntities([]))
      .finally(() => setReady(true));
  }, []);

  const byKey: globalThis.Record<string, Entity> = {};
  for (const e of entities) byKey[e.key] = e;

  return <Ctx.Provider value={{ entities, byKey, ready }}>{children}</Ctx.Provider>;
}

export function useMeta() {
  return useContext(Ctx);
}

export function useEntity(key: string): Entity | undefined {
  return useMeta().byKey[key];
}
