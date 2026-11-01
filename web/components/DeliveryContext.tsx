"use client";

import { createContext, ReactNode, useContext } from "react";
import { api, DeliveryStatus } from "@/lib/api";
import { usePoll } from "@/lib/usePoll";

type DeliveryState = ReturnType<typeof usePoll<DeliveryStatus>>;

const DeliveryContext = createContext<DeliveryState | null>(null);

/** One live poll of the outbox shared by the sidebar, overview and campaign pages. */
export function DeliveryProvider({ children }: { children: ReactNode }) {
  const state = usePoll(() => api<DeliveryStatus>("/api/v1/delivery/status"), 2500);
  return <DeliveryContext.Provider value={state}>{children}</DeliveryContext.Provider>;
}

export function useDelivery() {
  const state = useContext(DeliveryContext);
  if (!state) throw new Error("useDelivery must be used inside DeliveryProvider");
  return state;
}
